package models

import "time"

// 说明：以下 cfg_* 表统一承载原先散落在 settings.yaml / aimap.json /
// productbelong.json / systemPrompt.txt 以及 Go 代码常量里的业务配置与 AI 提示词。
// settings.yaml 只保留 log 与 db（启动数据库之前必须能读到的最小配置）。

// CfgSetting 通用键值配置：settings.yaml 中除 log / db 之外的所有配置项。
// group + cfg_key 唯一，例如 ("email", "domain")、("auth", "requireLogin")。
type CfgSetting struct {
	Model
	Group     string `gorm:"column:cfg_group;size:64;not null;uniqueIndex:uk_cfg_setting,priority:1" json:"group"`
	Key       string `gorm:"column:cfg_key;size:128;not null;uniqueIndex:uk_cfg_setting,priority:2" json:"key"`
	Value     string `gorm:"column:cfg_value;type:text" json:"value"`
	ValueType string `gorm:"column:value_type;size:16;default:string" json:"valueType"` // string / int / bool / json
	Remark    string `gorm:"size:255" json:"remark"`
	Enabled   bool   `gorm:"default:true" json:"enabled"`
}

func (CfgSetting) TableName() string {
	return "cfg_settings"
}

// CfgAIModel AI 模型配置：合并原 aimap.json（名称 -> 枚举值）与
// settings.yaml 的 aimodel.*（apikey / apiurl / modelname / timeout）。
// 代码里不再硬编码任何 API 地址、模型名或密钥。
type CfgAIModel struct {
	Model
	Code      string `gorm:"size:64;not null;uniqueIndex:uk_cfg_ai_model_code" json:"code"` // deepseek / qwen
	Label     string `gorm:"size:64;not null" json:"label"`                                 // 前端展示名：Deepseek / Qwen
	Name      string `gorm:"size:64" json:"name"`
	ModelType int    `gorm:"column:model_type;not null" json:"modelType"` // 对应 enum.AIModelType：1 DeepSeek，2 Qwen
	ApiKey    string `gorm:"column:api_key;size:255" json:"apiKey"`
	ApiUrl    string `gorm:"column:api_url;size:255" json:"apiUrl"`
	ModelName string `gorm:"column:model_name;size:64" json:"modelName"`
	Timeout   int64  `gorm:"default:600" json:"timeout"`
	Enabled   bool   `gorm:"default:true" json:"enabled"`
	SortOrder int    `gorm:"column:sort_order;default:0" json:"sortOrder"`
	Remark    string `gorm:"size:255" json:"remark"`
}

func (CfgAIModel) TableName() string {
	return "cfg_ai_models"
}

// CfgProductBelong 商品归属映射：原 productbelong.json。
// Aliases 存 JSON 数组字符串，一条记录对应一个商品分类，与前端
// map[string][]string 的结构一一对应，空别名用 [] 表示。
type CfgProductBelong struct {
	Model
	Category  string `gorm:"size:64;not null;uniqueIndex:uk_cfg_product_category" json:"category"`
	Aliases   string `gorm:"type:text" json:"aliases"`
	SortOrder int    `gorm:"column:sort_order;default:0" json:"sortOrder"`
	Enabled   bool   `gorm:"default:true" json:"enabled"`
	Remark    string `gorm:"size:255" json:"remark"`
}

func (CfgProductBelong) TableName() string {
	return "cfg_product_belongs"
}

// CfgPrompt AI 提示词：systemPrompt.txt 与代码中所有硬编码的提示词统一放这里。
// IsTemplate=true 表示内容含 {{date}} / {{label}} 等占位符，由渲染函数替换。
// 运行时只读 Content，代码里不再保留任何提示词字面量。
type CfgPrompt struct {
	Model
	Code       string `gorm:"size:64;not null;uniqueIndex:uk_cfg_prompt_code" json:"code"`
	Name       string `gorm:"size:128" json:"name"`
	Scene      string `gorm:"size:64" json:"scene"`
	Content    string `gorm:"type:longtext" json:"content"`
	IsTemplate bool   `gorm:"column:is_template;default:false" json:"isTemplate"`
	Enabled    bool   `gorm:"default:true" json:"enabled"`
	SortOrder  int    `gorm:"column:sort_order;default:0" json:"sortOrder"`
	Remark     string `gorm:"size:255" json:"remark"`
}

func (CfgPrompt) TableName() string {
	return "cfg_prompts"
}

// CfgOrderActionRule 订单操作自定义术语规则：把群内口语 / 缩写映射到订单操作。
//
// 一份规则在生产链路里被消费三次（wechatorder 包）：
//  1. 候选闸门：自定义触发词与内置正则取并集，让「消单」这类词能进入 LLM；
//  2. 提示词：渲染成固定模板追加到 wechat_order_action 系统提示词尾部；
//  3. 确定性兜底：命中规则时覆盖 / 补强 LLM 判定，并从已识别订单反查定位关键词。
//
// Keyword 用 / 分隔别名；Action 取 delete / modify / ignore；
// Field 取 address / phone / recipient / remarks / specs；
// ValueRule 取 none / after（取「为/改成/：」之后）/ phone（抽手机号）/
// append_rest（去掉触发词与定位词后的剩余文本，追加到备注）；
// Scope 取 both / trigger（只参与候选闸门）/ prompt（只进提示词与兜底）。
type CfgOrderActionRule struct {
	Model
	Keyword   string `gorm:"column:keyword;size:128;not null;uniqueIndex:uk_cfg_action_rule_keyword" json:"keyword"`
	Action    string `gorm:"column:action_type;size:16;not null" json:"action"`
	Field     string `gorm:"column:field_name;size:32" json:"field"`
	ValueRule string `gorm:"column:value_rule;size:32" json:"valueRule"`
	Scope     string `gorm:"column:scope;size:16;default:both" json:"scope"`
	Priority  int    `gorm:"column:priority;default:0" json:"priority"`
	Enabled   bool   `gorm:"default:true" json:"enabled"`
	SortOrder int    `gorm:"column:sort_order;default:0" json:"sortOrder"`
	Remark    string `gorm:"size:255" json:"remark"`
}

func (CfgOrderActionRule) TableName() string {
	return "cfg_order_action_rules"
}

// CfgRevision 配置版本：固定单行（id=1），保存各张 cfg_* 表内容的 MD5。
// 热更新轮询先拿这 32 个字符和内存基线比对，一致就不必把提示词正文等
// 全量配置从远程 MySQL 拉回来。字段由应用写入配置后刷新；
// 外部直接执行 SQL 改 cfg_* 没维护本表时，轮询会用「数据库实算 MD5」自动修正。
type CfgRevision struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	CfgMD5    string    `gorm:"column:cfg_md5;size:32;not null" json:"cfgMd5"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (CfgRevision) TableName() string {
	return "cfg_revision"
}
