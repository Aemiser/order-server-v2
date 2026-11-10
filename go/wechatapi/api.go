// Package wechatapi 是微信本地数据能力的「稳定门面」。
//
// 本包刻意不 import go/wechatlog：所有对外类型都在这里定义，调用方
// （主程序 app.go、旧链路 wechatorder、新链路 schemaservice / sources）
// 只依赖本包。真正的实现由构建标签二选一：
//
//   - -tags wechat  → enabled.go，转发到 go/wechatlog，行为与改造前一致；
//   - 默认构建      → disabled.go，返回明确的降级状态，不链入任何
//     微信内存取密钥 / 数据库解密代码。
//
// 这样「默认开源构建不链入合规敏感代码」成为编译期保证，而不是靠约定。
// 桌面版发版必须使用 `wails build -tags wechat`，否则微信相关功能会提示
// ErrDisabled，但其余功能（模板工作台、账单、看板等）不受影响。
package wechatapi

import (
	"errors"
	"time"
)

// ErrDisabled 是未启用微信能力时所有写操作 / 导出操作统一返回的错误。
// 上层把它包装成业务文案时无需再判断 Enabled。
var ErrDisabled = errors.New("微信能力未启用：本构建缺少 -tags wechat，请用 wails build -tags wechat 重新构建")

// DisabledHint 是状态类 API 在降级构建下塞进 message 字段的提示文案。
const DisabledHint = "微信能力未启用（缺失 -tags wechat 构建标签）"

// Enabled 由 enabled.go / disabled.go 按构建标签定义为常量。
// 调用方可用它在启动阶段跳过后台任务、给出更明确的 UI 提示。

// Options 是导出选项，字段与 go/wechatlog.Options 一一对应。
type Options struct {
	Group            string
	Senders          string
	Days             int
	EndDate          string
	StartDate        string
	DataDir          string
	WorkDir          string
	DataKey          string
	Platform         string
	Version          int
	AutoKey          bool
	KeyProviderPath  string
	ProviderTimeout  time.Duration
	AutoKeyOnFailure bool
	WCDBLibPath      string
	SessionID        string
}

// Message 是交给上层规则引擎的单条群聊消息。
type Message struct {
	ID          string  `json:"id"`
	Sender      string  `json:"sender"`
	Content     string  `json:"content"`
	Quote       string  `json:"quote,omitempty"`
	QuoteSender string  `json:"quoteSender,omitempty"`
	Time        string  `json:"time"`
	Date        string  `json:"date"`
	Ts          float64 `json:"ts"`
}

// Result 是导出结果。
type Result struct {
	Source       string    `json:"source"`
	DataDir      string    `json:"dataDir"`
	WorkDir      string    `json:"workDir"`
	Version      int       `json:"version"`
	KeySource    string    `json:"keySource"`
	KeyCachePath string    `json:"keyCachePath"`
	From         string    `json:"from"`
	To           string    `json:"to"`
	EndDate      string    `json:"endDate"`
	Messages     []Message `json:"messages"`
	Warnings     []string  `json:"warnings"`
}

// KeyResolution 是密钥解析结果（不含密钥明文以外的敏感信息持有）。
type KeyResolution struct {
	Key      string
	Source   string
	Warnings []string
}

// ---------------------------------------------------------------------------
// 下面这组函数由 enabled.go / disabled.go 分别实现，签名必须保持一致。
// ---------------------------------------------------------------------------
//
// Export(opt Options) (Result, error)
// Detect() map[string]interface{}
// KeyCacheInfo() map[string]interface{}
// ClearKeyCache() error
// ResolveAndCacheKey(opt Options, onStatus func(string)) (KeyResolution, error)
// StartKeyDropWatcher(dataDir string, onFound func(source string))
// KeyDropStatus() map[string]interface{}
// KeyDropDir() string
