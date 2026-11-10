package core

// 内置配置节注册点。
//
// 以前「哪五张 cfg_* 表参与热更新」写死在 cfg_revision.go 的 cfgSections 和
// 一整段 MD5 SQL 里，新增一张配置表要改判定、SQL、结构体、加载、摘要好几处，
// 漏改一处就是静默漏检。现在改为：本文件一次性注册节元数据 + 行加载器，
// core 的轮询 / 基线 / 增量拉取 / 内容指纹复核全部按注册表驱动，逻辑一行未改。
//
// 第三方模块要加自己的配置节，在自己的 init() 里调用 config.MustRegister 即可
// 接入变更检测；若还要把行读进内存快照，再注册一个 cfgLoader（见下）。
// 不注册 loader 的节只参与 MD5 比对，会被安全跳过，不影响其余表。

import (
	"order_server/go/config"

	"gorm.io/gorm"
)

// cfgSections 是注册表在 core 内的有序快照，沿用既有变量名，
// 顺序 = 注册顺序 = SQL 列顺序 = 逐表基线顺序 = 变更摘要顺序。
var cfgSections []string

// cfgLoaders 把配置节名映射到该表的行加载器。加载器由本包提供，
// 因为 cfgRows 是 core 的私有快照类型，config 包不依赖任何业务模型。
var cfgLoaders = map[string]func(*gorm.DB, *cfgRows) error{}

func init() {
	// 列清单与顺序严格对应 models 中各表的业务列，必须包含 id 且以 id 开头；
	// 逐行 MD5 的 CONCAT_WS 列顺序变了会导致所有基线失效一轮（安全但会全量拉一次）。
	config.MustRegister(config.Section{
		Name: "cfg_settings", Alias: "settings_md5",
		Columns: []string{"id", "cfg_group", "cfg_key", "cfg_value", "value_type", "remark", "enabled"},
	})
	config.MustRegister(config.Section{
		Name: "cfg_ai_models", Alias: "aimodels_md5",
		Columns: []string{"id", "code", "label", "name", "model_type", "api_key", "api_url", "model_name", "timeout", "enabled", "sort_order", "remark"},
	})
	config.MustRegister(config.Section{
		Name: "cfg_product_belongs", Alias: "belongs_md5",
		Columns: []string{"id", "category", "aliases", "sort_order", "enabled", "remark"},
	})
	config.MustRegister(config.Section{
		Name: "cfg_prompts", Alias: "prompts_md5",
		Columns: []string{"id", "code", "name", "scene", "content", "is_template", "enabled", "sort_order", "remark"},
	})
	config.MustRegister(config.Section{
		Name: "cfg_order_action_rules", Alias: "actionrules_md5",
		Columns: []string{"id", "keyword", "action_type", "field_name", "value_rule", "scope", "priority", "enabled", "sort_order", "remark"},
	})
	cfgSections = config.Names()

	// 每次查询前把目标 slice 置 nil，避免 GORM Find 往旧数据后面追加。
	cfgLoaders["cfg_settings"] = func(db *gorm.DB, r *cfgRows) error {
		r.Settings = nil
		return db.Order("id asc").Find(&r.Settings).Error
	}
	cfgLoaders["cfg_ai_models"] = func(db *gorm.DB, r *cfgRows) error {
		r.AIModels = nil
		return db.Order("id asc").Find(&r.AIModels).Error
	}
	cfgLoaders["cfg_product_belongs"] = func(db *gorm.DB, r *cfgRows) error {
		r.Belongs = nil
		return db.Order("id asc").Find(&r.Belongs).Error
	}
	cfgLoaders["cfg_prompts"] = func(db *gorm.DB, r *cfgRows) error {
		r.Prompts = nil
		return db.Order("id asc").Find(&r.Prompts).Error
	}
	cfgLoaders["cfg_order_action_rules"] = func(db *gorm.DB, r *cfgRows) error {
		r.ActionRules = nil
		return db.Order("id asc").Find(&r.ActionRules).Error
	}
}
