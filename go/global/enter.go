package global

import (
	"sync"

	"order_server/go/conf"
	"order_server/go/models"

	"gorm.io/gorm"
)

const (
	Version = "10.0.13"
)

// cfg_prompts 表中的提示词编码。业务代码只按编码取词，
// 提示词正文一律存放在数据库里，不在代码中出现字面量。
const (
	PromptOrderExtract      = "order_extract"       // 订单信息提取（原 systemPrompt.txt）
	PromptWechatOrderBase   = "wechat_order_base"   // 微信订单判定基础提示词
	PromptWechatFullScan    = "wechat_full_scan"    // 粗筛关闭时的全量消息模式追加片段
	PromptWechatTargetDate  = "wechat_target_date"  // 目标订单日期注入模板
	PromptWechatOrderAction = "wechat_order_action" // 聊天记录里的订单操作（删除/修改）识别提示词
)

// CfgSnapshot 是运行时业务配置的一份不可变快照。
//
// 数据库 cfg_* 表支持热更新：后台定时器发现配置变化时会整体替换这份快照，
// 绝不原地修改已发布的 map / slice / struct —— AI 识别最多 100 个协程并发读配置，
// 原地写 map 会触发 fatal error: concurrent map read and map write。
// 调用方通过 Get* 拿到的都是副本，可以放心遍历。
type CfgSnapshot struct {
	Config         *conf.Config
	Prompts        map[string]string
	AIMap          map[string]int
	ProductBelongs map[string][]string
	Settings       []models.CfgSetting
	ActionRules    []models.CfgOrderActionRule
}

var (
	cfgMu sync.RWMutex
	cfg   = CfgSnapshot{
		Prompts:        map[string]string{},
		AIMap:          map[string]int{},
		ProductBelongs: map[string][]string{},
		ActionRules:    []models.CfgOrderActionRule{},
	}

	// Db 与 AddressList 是运行时状态，不是可热更新的配置：
	// Db 只在启动时建立连接，AddressList 是用户正在编辑的数据预览区。
	Db          *gorm.DB
	AddressList []models.AddressInfo
)

// ---------------- 读取（返回副本 / 在锁内取单个值） ----------------

func GetConfig() *conf.Config {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return cfg.Config
}

// Prompt 按编码取提示词正文；未加载到时返回空串，由调用方决定如何兜底报错。
func Prompt(code string) string {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return cfg.Prompts[code]
}

func GetPrompts() map[string]string {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	out := make(map[string]string, len(cfg.Prompts))
	for k, v := range cfg.Prompts {
		out[k] = v
	}
	return out
}

func GetAIMap() map[string]int {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	out := make(map[string]int, len(cfg.AIMap))
	for k, v := range cfg.AIMap {
		out[k] = v
	}
	return out
}

func GetProductBelongs() map[string][]string {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return copyBelongs(cfg.ProductBelongs)
}

func GetSettings() []models.CfgSetting {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	out := make([]models.CfgSetting, len(cfg.Settings))
	copy(out, cfg.Settings)
	return out
}

// GetActionRules 返回订单操作自定义术语规则的副本；调用方可放心遍历与排序。
func GetActionRules() []models.CfgOrderActionRule {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	out := make([]models.CfgOrderActionRule, len(cfg.ActionRules))
	copy(out, cfg.ActionRules)
	return out
}

// ---------------- 写入（写时复制，整体替换） ----------------

func SetConfig(c *conf.Config) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	cfg.Config = c
}

// ReplaceConfig 在锁内基于旧配置派生一份新配置再替换，
// 用于 SaveToExcel 这类「只改一个字段」的运行期写入，避免原地改已被其他协程持有的对象。
func ReplaceConfig(fn func(old *conf.Config) *conf.Config) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	cfg.Config = fn(cfg.Config)
}

// SetSavePathExcel 更新 Excel 导出目录（用户每次选目录时写入内存，不落数据库）。
func SetSavePathExcel(dir string) {
	ReplaceConfig(func(old *conf.Config) *conf.Config {
		next := conf.Config{}
		if old != nil {
			next = *old
		}
		next.SavePath.Excel = dir
		return &next
	})
}

// SetCfgSnapshot 一次性替换整份配置快照，供启动加载与热更新使用。
func SetCfgSnapshot(s CfgSnapshot) {
	if s.Prompts == nil {
		s.Prompts = map[string]string{}
	}
	if s.AIMap == nil {
		s.AIMap = map[string]int{}
	}
	if s.ProductBelongs == nil {
		s.ProductBelongs = map[string][]string{}
	}
	if s.ActionRules == nil {
		s.ActionRules = []models.CfgOrderActionRule{}
	}
	cfgMu.Lock()
	defer cfgMu.Unlock()
	cfg = s
}

func SetPrompts(m map[string]string) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	cfg.Prompts = out
}

// SetPrompt 单点改写一条提示词（测试与工具用），同样走写时复制。
func SetPrompt(code, content string) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	out := make(map[string]string, len(cfg.Prompts)+1)
	for k, v := range cfg.Prompts {
		out[k] = v
	}
	out[code] = content
	cfg.Prompts = out
}

func SetAIMap(m map[string]int) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	cfg.AIMap = out
}

func SetProductBelongs(m map[string][]string) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	cfg.ProductBelongs = copyBelongs(m)
}

// SetActionRules 单点替换订单操作术语规则（测试与工具用），同样走写时复制。
func SetActionRules(rows []models.CfgOrderActionRule) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	out := make([]models.CfgOrderActionRule, len(rows))
	copy(out, rows)
	cfg.ActionRules = out
}

// ApplySettings 同时替换 cfg_settings 行与 conf.Config 中派生出来的字段，
// 保证 SaveSetting 之后「行」与「内存配置」一致且只加一次锁。
func ApplySettings(rows []models.CfgSetting, apply func(*conf.Config, []models.CfgSetting)) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	out := make([]models.CfgSetting, len(rows))
	copy(out, rows)
	cfg.Settings = out
	next := conf.Config{}
	if cfg.Config != nil {
		next = *cfg.Config
	}
	apply(&next, out)
	cfg.Config = &next
}

func copyBelongs(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for k, v := range in {
		vv := make([]string, len(v))
		copy(vv, v)
		out[k] = vv
	}
	return out
}
