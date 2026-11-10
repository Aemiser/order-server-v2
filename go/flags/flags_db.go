package flags

import (
	"order_server/go/global"
	"order_server/go/models"

	"github.com/sirupsen/logrus"
)

func FlagDB() {
	// cfg_* 配置表由 core.LoadDBCfg 在正常启动时已确保存在；这里再补一遍无妨。
	err := global.Db.AutoMigrate(
		models.CfgSetting{},
		models.CfgAIModel{},
		models.CfgProductBelong{},
		models.CfgPrompt{},
		models.CfgRevision{},
	)
	if err != nil {
		logrus.Warnf("配置表迁移失败 %s", err)
	}
	logrus.Info("配置表迁移成功")
	// 业务表与看板索引：正常启动也会建（见 main.go），-db 显式路径同样覆盖。
	EnsureBusinessSchema()
}

// EnsureBusinessSchema 幂等地创建业务表（address_infos / 日志表）并补建看板增量优化
// 所需的复合索引。
//
// ⚠️ 之前这段只在 -db 参数下执行，正常桌面启动根本不跑，导致看板增量同步依赖的
// (created_at,id)/(updated_at,id) 索引永远没被建出来，变更探测被静默跳过。
// 现在 main.go 在正常启动也会调用本函数，索引会在首次启动时自动补齐。
func EnsureBusinessSchema() {
	if global.Db == nil {
		return
	}
	if err := global.Db.AutoMigrate(models.AddressInfo{}, models.LogModels{}); err != nil {
		logrus.Warnf("业务表迁移失败 %s", err)
	}
	ensureDashboardIndexes()
}

// ensureDashboardIndexes 幂等补建看板增量同步 / 订单范围查询所需的复合索引。
// 索引列顺序理由：
//   - (created_at, id)：created_at 是范围条件放最左，id 作尾列稳定排序、消除 filesort；
//     订单列表、账单月报的 created_at BETWEEN / ORDER BY created_at,id 也复用同一索引。
//   - (updated_at, id)：updated_at 是「最近变更探测」的排序列放最左，id 尾列
//     保证同秒更新时顺序稳定；索引可被反向扫描直接满足
//     ORDER BY updated_at DESC, id DESC LIMIT 200，消除 filesort。
//   两者都避开低区分度的 deleted_at（模型里已有单列索引，不重复建）。
//
// ⚠️ MySQL 5.7 及更早版本对大表 ADD INDEX 会 copy table 并阻塞写入，建议在低峰
//    手工执行下面两条 DDL（代码检测到索引已存在就会跳过，不重复执行）；
//    MySQL 8.0 为 INPLACE ONLINE DDL，通常可在启动时自动完成。
//    回滚：确认无其他查询依赖后再 DROP INDEX，索引本身不影响任何数据。
//      ALTER TABLE `address_infos` ADD INDEX `idx_addr_created_id` (`created_at`, `id`);
//      ALTER TABLE `address_infos` ADD INDEX `idx_addr_updated_id` (`updated_at`, `id`);
func ensureDashboardIndexes() {
	if global.Db == nil {
		return
	}
	indexes := []struct{ name, ddl string }{
		{"idx_addr_created_id", "ALTER TABLE `address_infos` ADD INDEX `idx_addr_created_id` (`created_at`, `id`)"},
		{"idx_addr_updated_id", "ALTER TABLE `address_infos` ADD INDEX `idx_addr_updated_id` (`updated_at`, `id`)"},
	}
	for _, it := range indexes {
		if global.Db.Migrator().HasIndex(&models.AddressInfo{}, it.name) {
			continue
		}
		if err := global.Db.Exec(it.ddl).Error; err != nil {
			logrus.Warnf("索引 %s 创建失败（建议低峰手工执行 DDL）: %v", it.name, err)
			continue
		}
		logrus.Infof("已补建索引 %s", it.name)
	}
}
