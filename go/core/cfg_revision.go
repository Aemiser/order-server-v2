package core

// 配置 MD5 快比对（逐表版）：把「哪张 cfg_* 表变了」的判断下推给 MySQL。
//
// 原来的做法：每 60 秒执行 4 次 SELECT *，把提示词 longtext 等全部配置
// 通过远程网络拉回本地，再 JSON 序列化算 SHA256。绝大多数轮次配置没动，
// 这些传输和反序列化全是浪费。
//
// 现在的做法：一条 SQL 同时返回
//   - stored_md5：cfg_revision 里存的上次整体 MD5（持久化、可查、多实例共用）
//   - actual_md5：五张表逐表 MD5 拼起来再算的整体 MD5
//   - 五个逐表 MD5：settings_md5 / aimodels_md5 / belongs_md5 / prompts_md5 / actionrules_md5
// 内存里保存一份「逐表 MD5 基线」，哪张表不一致就只拉哪张表，
// 其余表复用上一次已加载的行，再整体发布一份一致的内存快照。
//
// 为什么要每轮实算真值：用户可能直接用 SQL / 管理后台改 cfg_*，
// 未必会同步维护 cfg_revision。只看 stored 会漏掉这类改动，
// 所以每轮都让数据库实算一次，顺便把 stored 自动修正回来。
//
// 为什么不生成列（row_md5）：五张表的 id 都是 AUTO_INCREMENT，
// MySQL 禁止生成列引用自增列（ERROR 3109）；且列清单写死在 DDL 里，
// 以后加字段忘了改表达式会静默漏检。由服务端每轮算 26 行更省心。

import (
	"fmt"
	"time"

	"order_server/go/config"
	"order_server/go/global"
	"order_server/go/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// cfgSections（有序节清单）与逐表 MD5 SQL 不再写死在这里：
// 见 cfg_sections.go 的注册点，以及 config.RevisionSQL() 按注册表动态拼出的 SQL。
// 判定口径保持不变：逐行 MD5（覆盖全部业务列，含 enabled 与 id）→ 按 id 排序
// 拼接 → 整体 MD5；deleted_at IS NULL 与 GORM Find 的默认过滤一致。

// cfgRevisionPair 是一轮轻量查询的结果。Sections 用 map 而非固定字段，
// 这样新增配置节只需注册，不必回来给结构体补字段和 gorm tag。
type cfgRevisionPair struct {
	Stored   string
	Actual   string
	Sections map[string]string // 配置节名（表名）-> 该表实算 MD5
}

// sections 返回逐表 MD5 的副本，供基线快照使用；顺序无关。
func (p cfgRevisionPair) sections() map[string]string {
	out := make(map[string]string, len(p.Sections))
	for k, v := range p.Sections {
		out[k] = v
	}
	return out
}

// changedSections 返回与本地逐表基线不一致的表名，顺序按注册表。
// 基线为空 / 本轮实算值不可用（例如轻量 SQL 降级）时返回全部配置节，
// 走原来的全量比对，行为安全。
func (p cfgRevisionPair) changedSections(base map[string]string) []string {
	if p.Actual == "" || len(base) == 0 {
		return append([]string(nil), cfgSections...)
	}
	out := make([]string, 0, len(cfgSections))
	for _, s := range cfgSections {
		if base[s] == "" || p.Sections[s] != base[s] {
			out = append(out, s)
		}
	}
	return out
}

// quiet=true 用于 60 秒轮询：远程 MySQL 偶尔会超过 GORM 慢 SQL 阈值，
// 静默掉这类日志，避免每分钟刷屏。
func cfgRevisionDB(quiet bool) *gorm.DB {
	db := global.Db
	if quiet {
		db = db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	}
	return db
}

// readCfgRevisionPair 执行按注册表拼出的 SQL，并把动态结果列还原成「节名 -> MD5」。
// 用 Rows() 逐列扫描而不是 Scan(&struct)，是因为配置节可增减，结果列数不固定。
func readCfgRevisionPair(quiet bool) (cfgRevisionPair, error) {
	var p cfgRevisionPair
	if global.Db == nil {
		return p, fmt.Errorf("数据库未初始化")
	}
	rows, err := cfgRevisionDB(quiet).Raw(config.RevisionSQL()).Rows()
	if err != nil {
		return p, fmt.Errorf("读取配置 MD5 失败: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return p, fmt.Errorf("读取配置 MD5 失败: %w", err)
		}
		return p, fmt.Errorf("读取配置 MD5 失败: 空结果集")
	}
	cols, err := rows.Columns()
	if err != nil {
		return p, fmt.Errorf("读取配置 MD5 失败: %w", err)
	}
	values := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return p, fmt.Errorf("读取配置 MD5 失败: %w", err)
	}
	if err := rows.Err(); err != nil {
		return p, fmt.Errorf("读取配置 MD5 失败: %w", err)
	}
	p.Sections = make(map[string]string, len(cfgSections))
	for i, col := range cols {
		text := cfgSQLText(values[i])
		switch col {
		case "stored_md5":
			p.Stored = text
		case "actual_md5":
			p.Actual = text
		default:
			if s, ok := config.SectionByAlias(col); ok {
				p.Sections[s.Name] = text
			}
		}
	}
	return p, nil
}

// cfgSQLText 把驱动返回的标量统一成字符串；NULL 视为空串，
// 与原来 GORM Scan 到 string 字段的表现一致。
func cfgSQLText(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

// writeCfgRevision 写入/更新 id=1 的版本行（MySQL 走 ON DUPLICATE KEY UPDATE）。
func writeCfgRevision(md5 string) error {
	if md5 == "" || global.Db == nil {
		return nil
	}
	row := models.CfgRevision{ID: 1, CfgMD5: md5, UpdatedAt: time.Now()}
	return cfgRevisionDB(true).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"cfg_md5", "updated_at"}),
	}).Create(&row).Error
}

// SyncCfgRevision 应用内写完 cfg_* 后调用：重算逐表 MD5、回写整体版本行，
// 返回本轮完整的 pair 供调用方一并更新本地基线，
// 免得定时器下一轮把自己刚写的变更又播报一次。
func SyncCfgRevision() (cfgRevisionPair, error) {
	p, err := readCfgRevisionPair(false)
	if err != nil {
		return cfgRevisionPair{}, err
	}
	if p.Stored != p.Actual {
		if err := writeCfgRevision(p.Actual); err != nil {
			// 回写失败不影响配置本身，逐表值仍然有效，下一轮轮询会自动修正。
			return p, err
		}
	}
	return p, nil
}
