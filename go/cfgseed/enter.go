// Package cfgseed 提供 cfg_* 配置表的初始种子数据。
//
// cfg_seed.json 是唯一的数据源，由 scripts/gen_cfg_seed.py 从项目原有配置
// 与代码常量生成，并通过 go:embed 编进二进制：程序在任意新环境首次启动时
// 都能自动建表并灌入初始配置，不需要依赖项目根目录下的任何 json/txt/yaml。
// 运行时的配置与提示词一律从数据库读取，这里的数据只在「表为空」时使用。
package cfgseed

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed cfg_seed.json
var seedFS embed.FS

const seedFile = "cfg_seed.json"

type Setting struct {
	Group   string `json:"group"`
	Key     string `json:"key"`
	Value   string `json:"value"`
	Type    string `json:"type"`
	Remark  string `json:"remark"`
	Enabled bool   `json:"enabled"`
}

type AIModel struct {
	Code      string `json:"code"`
	Label     string `json:"label"`
	Name      string `json:"name"`
	ModelType int    `json:"modelType"`
	ApiKey    string `json:"apiKey"`
	ApiUrl    string `json:"apiUrl"`
	ModelName string `json:"modelName"`
	Timeout   int64  `json:"timeout"`
	Enabled   bool   `json:"enabled"`
	SortOrder int    `json:"sortOrder"`
	Remark    string `json:"remark"`
}

type ProductBelong struct {
	Category  string   `json:"category"`
	Aliases   []string `json:"aliases"`
	SortOrder int      `json:"sortOrder"`
	Enabled   bool     `json:"enabled"`
	Remark    string   `json:"remark"`
}

type Prompt struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Scene      string `json:"scene"`
	Content    string `json:"content"`
	IsTemplate bool   `json:"isTemplate"`
	Enabled    bool   `json:"enabled"`
	SortOrder  int    `json:"sortOrder"`
	Remark     string `json:"remark"`
}

// ActionRule 是 cfg_order_action_rules 的种子结构，字段与 models.CfgOrderActionRule 一致。
type ActionRule struct {
	Keyword   string `json:"keyword"`
	Action    string `json:"action"`
	Field     string `json:"field"`
	ValueRule string `json:"valueRule"`
	Scope     string `json:"scope"`
	Priority  int    `json:"priority"`
	Enabled   bool   `json:"enabled"`
	SortOrder int    `json:"sortOrder"`
	Remark    string `json:"remark"`
}

type Seed struct {
	Version        int             `json:"version"`
	Settings       []Setting       `json:"settings"`
	AIModels       []AIModel       `json:"aiModels"`
	ProductBelongs []ProductBelong `json:"productBelongs"`
	Prompts        []Prompt        `json:"prompts"`
	ActionRules    []ActionRule    `json:"actionRules"`
}

// Bytes 返回原始种子 JSON（供外部工具/测试使用）。
func Bytes() ([]byte, error) {
	return seedFS.ReadFile(seedFile)
}

// Load 解析种子数据。
func Load() (*Seed, error) {
	b, err := Bytes()
	if err != nil {
		return nil, err
	}
	var s Seed
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("cfg_seed.json 解析失败: %w", err)
	}
	return &s, nil
}
