package wechatorder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Config 是「自动获取今日订单」的配置项，由前端配置页读写。
// 聊天记录由项目内移植的 chatlog 核心 Go 代码读取本地微信数据库，
// 密钥获取与直读组件参考 WeFlow 做成可插拔：不配置就不加载、不拉起，
// 不启动 HTTP 服务、不操作微信窗口，无需任何第三方 UI 自动化运行时。
type Config struct {
	Group            string `json:"group"`                // 要导出的群聊名称
	Senders          string `json:"senders"`              // 顾客昵称，逗号分隔；留空=不过滤
	Days             int    `json:"days"`                 // 导出近 N 天（默认 2 = 今天 + 昨天）
	DataDir          string `json:"dataDir"`              // 微信加密数据库目录；留空自动探测
	WorkDir          string `json:"workDir"`              // 解密后数据库目录；留空用默认目录
	DataKey          string `json:"dataKey"`              // 手动填写的数据库密钥（hex 或按库 JSON）
	Platform         string `json:"platform"`             // windows / darwin
	Version          int    `json:"version"`              // 微信数据库版本 3 或 4
	AutoKey          bool   `json:"autoKey"`              // 允许（仅在需要时）从微信进程只读提取密钥
	KeyProviderPath  string `json:"keyProviderPath"`      // WeFlow 式外部密钥提供者可执行程序
	ProviderTimeout  int64  `json:"providerTimeout"`      // 外部程序超时（秒），默认 120
	AutoKeyOnFailure bool   `json:"autoKeyOnFailure"`     // 缓存/解密校验失败时自动重取密钥
	WCDBLibPath      string `json:"wcdbLibPath"`          // WCDB 动态库；配置后按时间游标直读加密库
	SessionID        string `json:"sessionId"`            // 可选：群会话 ID，填了跳过按群名查会话
	Concurrency      int    `json:"concurrency"`      // 请求 DeepSeek 的协程数，默认 100
	OnlyToday        bool   `json:"onlyToday"`        // 严格模式：日期不符目标日期的订单直接丢弃（否则进人工审查区）
	AITimeout        int64  `json:"aiTimeout"`        // 单条 AI 请求超时（秒）
	// ---- 目标订单日期 ----
	AutoTargetDate bool   `json:"autoTargetDate"` // 默认 true：每次启动 / 每次运行都取当天，即「默认今天」
	TargetDate     string `json:"targetDate"`     // 关闭跟随今天后手动指定 YYYY-MM-DD；为空也会回退到今天
	// ---- 粗筛开关 ----
	UseRuleFilter bool `json:"useRuleFilter"` // 默认 true：先规则粗筛；false：全部消息直接交 AI 判定
	// ---- 兼容字段：旧配置可能写入，运行时一律强制关闭 ----
	AutoRun         bool   `json:"autoRun"`         // 登录后后台定时自动执行整条链路
	RunOnStartup    bool   `json:"runOnStartup"`    // 登录成功后立即执行一次
	AutoRunInterval int    `json:"autoRunInterval"` // 自动运行间隔（分钟），默认 30
	Incremental     bool   `json:"incremental"`     // 增量模式：水位线去重，只处理新消息
	AutoImport      bool   `json:"autoImport"`      // 自动把新订单写入数据库
	AutoExportExcel bool   `json:"autoExportExcel"` // 自动生成 Excel（不再弹目录选择框）
	ExportDir       string `json:"exportDir"`       // 自动导出目录，留空用默认目录
	ConfigPath      string `json:"configPath,omitempty"` // 只读：配置文件位置
}

func DefaultConfig() Config {
	return Config{
		Group:            "帅涛吃什么",
		Senders:          "帅涛,aemiser",
		Days:             2,
		Platform:         "windows",
		Version:          0, // 0 = 运行时自动跟随微信客户端版本
		AutoKey:          true,
		ProviderTimeout:  120,
		AutoKeyOnFailure: true,
		Concurrency:      100,
		OnlyToday:        false,
		AITimeout:        90,
		// 默认每次启动取今天作为要识别的订单日期；粗筛默认开启，关闭后所有消息直接交 AI
		AutoTargetDate: true,
		TargetDate:     time.Now().Format("2006-01-02"),
		UseRuleFilter:  true,
		// 本功能只做订单识别，结果仅进入数据预览区；不自动入库、不生成文件、不后台定时跑
		AutoRun:         false,
		RunOnStartup:    false,
		AutoRunInterval: 30,
		Incremental:     false,
		AutoImport:      false,
		AutoExportExcel: false,
	}
}

var cfgMu sync.Mutex

func ConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "order_server", "wechat_order_config.json")
}

func normalize(c *Config) {
	d := DefaultConfig()
	if c.Group == "" {
		c.Group = d.Group
	}
	if c.Platform == "" {
		c.Platform = d.Platform
	}
	if c.Days <= 0 {
		c.Days = d.Days
	}
	if c.Concurrency <= 0 {
		c.Concurrency = d.Concurrency
	}
	if c.AITimeout <= 0 {
		c.AITimeout = d.AITimeout
	}
	if c.ProviderTimeout <= 0 {
		c.ProviderTimeout = d.ProviderTimeout
	}
	if c.AutoRunInterval <= 0 {
		c.AutoRunInterval = d.AutoRunInterval
	}
	// 目标订单日期：默认「跟随今天」，每次读取都重算当天，
	// 所以软件每次启动后该项一定回到今天的日期，不会沿用上次手动选的日期。
	if c.AutoTargetDate || strings.TrimSpace(c.TargetDate) == "" {
		c.TargetDate = time.Now().Format("2006-01-02")
		c.AutoTargetDate = true
	} else if t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(c.TargetDate), time.Local); err == nil {
		c.TargetDate = t.Format("2006-01-02")
	} else {
		c.TargetDate = time.Now().Format("2006-01-02")
		c.AutoTargetDate = true
	}
	// 产品语义固定：这里只做信息识别，结果进数据预览区，由用户确认后才入库。
	// 无论旧配置文件里写了什么，一律关闭后台定时运行与自动落库/自动导出，
	// 保证「一键识别订单」永远不会写数据库或生成文件。
	c.AutoRun = false
	c.RunOnStartup = false
	c.Incremental = false
	c.AutoImport = false
	c.AutoExportExcel = false
}

// ApplyTargetDateOverride 把「按钮旁边临时选的日期」应用到本轮配置。
// 只改内存里的 Config，绝不写配置文件：下次启动 / 不传日期时仍然默认今天。
// 返回是否成功覆盖；空串、非法格式一律忽略，沿用配置里的默认目标日期。
func ApplyTargetDateOverride(cfg *Config, date string) bool {
	if cfg == nil {
		return false
	}
	s := strings.TrimSpace(date)
	if s == "" {
		return false
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, layout := range []string{"2006-01-02", "2006-1-2"} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			// 导出层不允许导出未来；这里也一并拒绝，避免「导出窗口是今天、
			// 订单日期却是明天」导致所有订单都被判成日期不符。
			if t.After(today) {
				return false
			}
			cfg.AutoTargetDate = false
			cfg.TargetDate = t.Format("2006-01-02")
			return true
		}
	}
	return false
}

// EffectiveTargetDate 返回本轮要识别的订单日期（本地时区的当天 00:00）。
// 跟随今天时直接取 time.Now()；否则解析用户手动指定的 YYYY-MM-DD。
func EffectiveTargetDate(cfg Config) time.Time {
	if !cfg.AutoTargetDate {
		if t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(cfg.TargetDate), time.Local); err == nil {
			return t
		}
	}
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

// TargetDateString 返回 YYYY-MM-DD 形式的目标日期，用于导出窗口与结果展示。
func TargetDateString(cfg Config) string {
	return EffectiveTargetDate(cfg).Format("2006-01-02")
}

// TargetDateLabel 返回订单消息里常见的 "M-D" 写法（如 11-7），用于和 AI 提取的日期比对。
func TargetDateLabel(t time.Time) string {
	return t.Format("1-2")
}

// LoadConfig 读取配置，文件不存在时返回默认配置。
func LoadConfig() Config {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	cfg := DefaultConfig()
	cfg.ConfigPath = ConfigPath()
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		logrus.Warnf("微信订单配置解析失败，使用默认值: %v", err)
		def := DefaultConfig()
		def.ConfigPath = ConfigPath()
		return def
	}
	normalize(&cfg)
	cfg.ConfigPath = ConfigPath()
	return cfg
}

// SaveConfig 持久化配置。
func SaveConfig(cfg Config) error {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	normalize(&cfg)
	cfg.ConfigPath = "" // 路径是运行时信息，不写进配置文件
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
