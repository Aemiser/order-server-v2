package core

import (
	"order_server/go/global"
	"order_server/go/models"

	"github.com/sirupsen/logrus"
)

// ReadAiMap 从 cfg_ai_models 读取「展示名 -> 模型枚举值」映射（原 aimap.json）。
// 保留该函数名以兼容历史脚本；正常启动路径走 LoadDBCfg。
func ReadAiMap() (mapps map[string]int) {
	mapps = map[string]int{}
	if global.Db == nil {
		return
	}
	if err := EnsureCfgTables(); err != nil {
		logrus.Warnf("初始化 cfg_ai_models 失败: %v", err)
		return
	}
	var rows []models.CfgAIModel
	if err := global.Db.Where("enabled = ?", true).Order("sort_order asc, id asc").Find(&rows).Error; err != nil {
		logrus.Warnf("读取 cfg_ai_models 失败: %v", err)
		return
	}
	for _, r := range rows {
		if r.Label != "" {
			mapps[r.Label] = r.ModelType
		}
	}
	return
}
