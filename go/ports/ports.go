// Package ports 定义后端所有「可扩展模块」的端口（接口）契约。
//
// 这是模块化架构的中枢：上层业务模块只依赖这里的接口，不依赖具体实现；
// 新增一种聊天来源、AI provider、流水线阶段或配置后端，只需实现对应接口，
// 不触碰任何现有文件。
//
// 依赖方向严格单向：ports → orderschema → kernel → 标准库。
// 具体实现（go/ai、go/wechatlog 插件、compose 编排层）依赖 ports/orderschema，
// 绝不允许具体实现反过来被硬编码分散在各处。
//
// 模块清单（对应重构规划）：
//   - ChatSource        导出微信/Telegram/CSV 等聊天记录的来源
//   - CompletionProvider AI 补全（重试/超时/限流/流式/降级/成本统计都在这一层）
//   - PipelineStage     中间件流水线阶段（粗筛/路由分发/入库都是它的实现）
//   - ConfigProvider    配置读取（按命名空间 + 模块自注册，替掉写死的 cfgSections）
//
// 订单模板与订单持久化的契约在 go/orderschema.Store（模板+订单二合一），
// 由 orderschema.MemoryStore / GormStore 共同实现，本包不再重复定义。
// Logger 与 EventSink 复用 kernel 中的定义。
package ports

import (
	"context"

	"order_server/go/kernel"
	"order_server/go/orderschema"
)

// ---------------- 聊天导出 ----------------

// ChatSource 一种聊天记录导出来源。新增来源（微信/飞书/CSV…）只需实现本接口并注册。
type ChatSource interface {
	// Name 返回来源唯一标识，如 "wechat"、"csv"。
	Name() string
	// Export 按给定选项导出消息。
	Export(ctx context.Context, opt ExportOptions) (*ExportResult, error)
}

// ExportOptions 导出参数。
type ExportOptions struct {
	DataDir string // 数据源目录（如微信数据目录）
	ChatID  string // 会话标识（群/单聊）
	Since   string // 起始时间（RFC3339 或业务约定格式）
	Until   string // 结束时间
	Limit   int    // 最大条数，0 表示不限
}

// ExportResult 导出结果。
type ExportResult struct {
	Messages []ChatMessage
}

// ChatMessage 一条归一化后的聊天消息。
type ChatMessage struct {
	ID       string // 来源内唯一 ID（用于订单去重与定位）
	Sender   string // 发送者显示名 / 备注
	Content  string // 文本正文
	Time     string // 发送时间（RFC3339）
	IsSystem bool   // 是否为系统消息
}

// ---------------- AI 补全 ----------------

// CompletionProvider AI 对话补全端口。AI 模块不认识「订单」，只负责安全的模型调用。
// 重试、超时、限流、流式、降级、成本统计都应封装在这一层。新增 provider 只需实现并注册。
type CompletionProvider interface {
	Name() string
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}

// CompletionRequest 一次补全请求。
type CompletionRequest struct {
	Model        string             // 模型标识
	SystemPrompt string             // 系统提示词（通常由模板模块生成）
	UserPrompt   string             // 用户提示词（业务拼接）
	Temperature  float64            // 温度
	MaxTokens    int                // 最大输出 token
	Stream       func(delta string) // 流式回调（非流式时为 nil）
}

// CompletionResponse 一次补全响应。
type CompletionResponse struct {
	Text      string  // 完整文本（流式时为空，需自行拼接）
	Raw       []byte  // 原始响应体
	TokensIn  int     // 输入 token（用于成本统计）
	TokensOut int     // 输出 token
	Cost      float64 // 估算成本
}

// ---------------- 中间件流水线 ----------------

// PipelineStage 中间件流水线的一个阶段。粗筛、路由分发、入库都是它的具体实现。
// 多个 Stage 串成管线，每个 Stage 读入并（可原地）修改 PipelineContext 后返回。
type PipelineStage interface {
	Name() string
	Process(ctx context.Context, in *PipelineContext) (*PipelineContext, error)
}

// PipelineContext 流水线在阶段间传递的上下文。
type PipelineContext struct {
	Ctx       context.Context
	Logger    kernel.Logger
	Messages  []ChatMessage           // 归一化消息（导出阶段产出）
	Templates []*orderschema.Template // 已加载的模板（路由阶段填充一次）
	Orders    []*orderschema.Order    // 已识别并准备入库的订单（识别阶段产出）
	Unmatched []ChatMessage           // 未命中任何模板、需人工审查的消息
	Artifacts map[string]interface{}  // 阶段间临时产物（如路由命中的模板）
}

// NewPipelineContext 构造一个空的流水线上下文。
func NewPipelineContext() *PipelineContext {
	return &PipelineContext{Artifacts: make(map[string]interface{})}
}

// ---------------- 配置 ----------------

// ConfigProvider 配置读取端口。配置按命名空间 + 模块自注册，替掉写死的 cfgSections。
type ConfigProvider interface {
	Get(ctx context.Context, namespace, key string) (string, error)
	Watch(ctx context.Context, namespace string, onChange func())
}
