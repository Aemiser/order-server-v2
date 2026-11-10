// Package ai 是「AI 识别模块」：实现 ports.CompletionProvider 端口，
// 把「安全模型调用」这一职责彻底封装——重试、超时、限流、流式、降级、
// 成本统计都在这一层，而 AI 模块完全不认识「订单」等业务概念。
//
// 扩展方式（开源友好）：新增一种大模型 provider，只需新建一个文件实现
// ports.CompletionProvider 并调用 ai.Register(...)，不触碰任何现有文件。
// 若要「主模型挂了自动切备用」，用 ai.NewFallback(primary, backups...) 组合即可。
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"order_server/go/kernel"
	"order_server/go/ports"
	"order_server/go/utils/httputils"
)

// ProviderConfig 一个 provider 的连接、容量与计费配置。
type ProviderConfig struct {
	Name        string // 唯一标识，如 "deepseek" / "qwen"
	APIURL      string // 兼容 OpenAI 格式的端点
	APIKey      string
	Model       string  // 默认模型；CompletionRequest.Model 为空时使用它
	TimeoutSec  int64   // 单次请求超时（秒），<=0 时用 90
	PromptPrice float64 // 每 1k 输入 token 价格（元）
	CompPrice   float64 // 每 1k 输出 token 价格（元）
	MaxRetries  int     // 失败重试次数（不含首次），负数按 0 处理

	// MaxConcurrency 同一 provider 允许同时在途的请求数，0 表示不限。
	// 用于防止「100 个 AI 请求」在服务端限流时互相踩踏。
	MaxConcurrency int
	// RetryBaseDelay / RetryMaxDelay 指数退避区间，默认 50ms / 500ms。
	// 实际等待为 [d/2, d) 的半抖动，避免多个协程同时重试造成同步冲击。
	RetryBaseDelay time.Duration
	RetryMaxDelay  time.Duration
	// Headers 额外的请求头（某些兼容端点需要自定义鉴权或租户头）。
	Headers map[string]string
}

const (
	defaultTimeout     = int64(90)
	defaultRetryBase   = 50 * time.Millisecond
	defaultRetryMax    = 500 * time.Millisecond
	defaultPlaceholder = "default"
)

// Provider 基于 httputils 的 ports.CompletionProvider 参考实现。
type Provider struct {
	cfg    ProviderConfig
	logger kernel.Logger
	sem    chan struct{} // nil 表示不限流
}

// NewProvider 构造一个 provider。logger 为 nil 时退化为无副作用实现。
func NewProvider(cfg ProviderConfig, logger kernel.Logger) *Provider {
	if logger == nil {
		logger = kernel.NoopLogger{}
	}
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = defaultTimeout
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.RetryBaseDelay <= 0 {
		cfg.RetryBaseDelay = defaultRetryBase
	}
	if cfg.RetryMaxDelay <= 0 {
		cfg.RetryMaxDelay = defaultRetryMax
	}
	if cfg.RetryMaxDelay < cfg.RetryBaseDelay {
		cfg.RetryMaxDelay = cfg.RetryBaseDelay
	}
	p := &Provider{cfg: cfg, logger: logger}
	if cfg.MaxConcurrency > 0 {
		p.sem = make(chan struct{}, cfg.MaxConcurrency)
	}
	return p
}

// Name 返回 provider 唯一标识。
func (p *Provider) Name() string { return p.cfg.Name }

// Config 返回构造时的配置快照，便于诊断与降级链展示。
func (p *Provider) Config() ProviderConfig { return p.cfg }

// Complete 完成一次补全。req.Stream 非 nil 时走流式并逐块回调；否则走普通请求。
// 调用方的 ctx 会一路贯穿到底层 HTTP 请求，取消可立即中断在途调用。
func (p *Provider) Complete(ctx context.Context, req ports.CompletionRequest) (*ports.CompletionResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := p.acquire(ctx); err != nil {
		return nil, err
	}
	defer p.release()

	if req.Stream != nil {
		return p.completeStream(ctx, req)
	}
	return p.completeOnce(ctx, req, true)
}

func (p *Provider) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.sem == nil {
		return nil
	}
	select {
	case p.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Provider) release() {
	if p.sem != nil {
		<-p.sem
	}
}

func (p *Provider) completeOnce(ctx context.Context, req ports.CompletionRequest, retry bool) (*ports.CompletionResponse, error) {
	payload, err := p.buildPayload(req)
	if err != nil {
		return nil, err
	}
	attempts := 0
	if retry {
		attempts = p.cfg.MaxRetries
	}
	var lastErr error
	for attempt := 0; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if attempt > 0 {
			if !isRetryable(lastErr) {
				break
			}
			if err := p.waitBackoff(ctx, attempt); err != nil {
				return nil, err
			}
			p.logger.Warn("AI 请求重试", kernel.F("provider", p.cfg.Name), kernel.F("attempt", attempt))
		}
		raw, callErr := httputils.AiRequestCtx(ctx, p.cfg.APIKey, p.cfg.APIURL, payload, p.cfg.TimeoutSec, p.cfg.Headers)
		if callErr == nil {
			text, tin, tout := parseNonStream(raw)
			cost := estimateCost(tin, tout, p.cfg.PromptPrice, p.cfg.CompPrice)
			return &ports.CompletionResponse{Text: text, Raw: raw, TokensIn: tin, TokensOut: tout, Cost: cost}, nil
		}
		lastErr = callErr
		if ctx.Err() != nil { // 取消优先于「可重试错误」，别在关窗口后再打一轮
			return nil, ctx.Err()
		}
		if !isRetryable(callErr) {
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("未知错误")
	}
	return nil, fmt.Errorf("AI 请求失败（重试 %d 次）: %w", p.cfg.MaxRetries, lastErr)
}

// completeStream 只在「一个字都还没吐给调用方」时才重试。
// 流式一旦发出过 chunk，重试会让用户看到重复文本，因此中途失败直接带错返回，
// 由上层决定是提示还是重新发起——这是流式与批式重试的关键区别。
func (p *Provider) completeStream(ctx context.Context, req ports.CompletionRequest) (*ports.CompletionResponse, error) {
	payload, err := p.buildPayload(req)
	if err != nil {
		return nil, err
	}
	attempts := p.cfg.MaxRetries
	var lastErr error
	for attempt := 0; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if attempt > 0 {
			if !isRetryable(lastErr) {
				break
			}
			if err := p.waitBackoff(ctx, attempt); err != nil {
				return nil, err
			}
			p.logger.Warn("AI 流式请求重试", kernel.F("provider", p.cfg.Name), kernel.F("attempt", attempt))
		}
		var mu sync.Mutex
		var acc strings.Builder
		emitted := false
		cb := func(delta string) error {
			chunk := parseStreamChunk([]byte(delta))
			if chunk == "" {
				return nil
			}
			mu.Lock()
			acc.WriteString(chunk)
			emitted = true
			mu.Unlock()
			req.Stream(chunk)
			return nil
		}
		callErr := httputils.AiStreamRequestCtx(ctx, p.cfg.APIKey, p.cfg.APIURL, payload, p.cfg.TimeoutSec, p.cfg.Headers, cb)
		mu.Lock()
		text, wasEmitted := acc.String(), emitted
		mu.Unlock()

		if callErr == nil && text != "" {
			// 流式响应通常不含 usage；这里不编造 token / 成本，避免假精度。
			return &ports.CompletionResponse{Text: text}, nil
		}
		if callErr == nil {
			lastErr = errors.New("AI 流式响应为空")
		} else {
			lastErr = callErr
		}
		if wasEmitted {
			return nil, fmt.Errorf("AI 流式响应中断（已生成 %d 字符，不自动重试以避免重复文本）: %w", len([]rune(text)), lastErr)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !isRetryable(lastErr) {
			break
		}
	}
	return nil, fmt.Errorf("AI 流式请求失败（重试 %d 次）: %w", p.cfg.MaxRetries, lastErr)
}

// buildPayload 是方法而非自由函数：请求未指定模型时必须用 cfg.Model，
// 否则 compose.CompletionFunc（它刻意不传 Model）会退化成字面量 "default"，
// 真实 API 直接拒绝，模板归纳的 LLM 路径就会静默退化为启发式。
func (p *Provider) buildPayload(req ports.CompletionRequest) ([]byte, error) {
	model := req.Model
	if model == "" {
		model = p.cfg.Model
	}
	if model == "" {
		model = defaultPlaceholder
	}
	payload := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": req.SystemPrompt},
			{"role": "user", "content": req.UserPrompt},
		},
		"temperature": req.Temperature,
		"stream":      req.Stream != nil,
	}
	if req.MaxTokens > 0 {
		payload["max_tokens"] = req.MaxTokens
	}
	return json.Marshal(payload)
}

func (p *Provider) waitBackoff(ctx context.Context, attempt int) error {
	d := p.cfg.RetryBaseDelay
	for i := 1; i < attempt; i++ {
		if d > p.cfg.RetryMaxDelay/2 {
			d = p.cfg.RetryMaxDelay
			break
		}
		d *= 2
	}
	if d > p.cfg.RetryMaxDelay || d <= 0 {
		d = p.cfg.RetryMaxDelay
	}
	half := int64(d / 2)
	if half > 0 {
		d = d/2 + time.Duration(rand.Int63n(half))
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isRetryable 区分「重试有意义」与「重试只会加重故障」。
// 4xx（除 429）、ctx 取消/超时不重试；5xx、429、网络错误重试。
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var se *httputils.HTTPStatusError
	if errors.As(err, &se) {
		return se.StatusCode == http.StatusTooManyRequests || se.StatusCode >= 500
	}
	return true
}

type openAIMessage struct {
	Content string `json:"content"`
}
type openAIChoice struct {
	Message openAIMessage `json:"message"`
	Delta   openAIMessage `json:"delta"`
}
type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}
type openAIResponse struct {
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage    `json:"usage"`
}

func parseNonStream(raw []byte) (text string, in, out int) {
	var r openAIResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return string(raw), 0, 0
	}
	if len(r.Choices) > 0 {
		text = r.Choices[0].Message.Content
	}
	return text, r.Usage.PromptTokens, r.Usage.CompletionTokens
}

func parseStreamChunk(raw []byte) string {
	// 跳过 SSE 包裹：这里拿到的已是 data: 之后的 JSON 片段
	s := strings.TrimSpace(string(raw))
	s = strings.TrimPrefix(s, "data:")
	s = strings.TrimSpace(s)
	if s == "" || s == "[DONE]" || s == "DONE" {
		return ""
	}
	var r openAIResponse
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return ""
	}
	if len(r.Choices) > 0 {
		return r.Choices[0].Delta.Content
	}
	return ""
}

func estimateCost(in, out int, promptPrice, compPrice float64) float64 {
	return (float64(in)/1000)*promptPrice + (float64(out)/1000)*compPrice
}

// ---- 注册表：新增 provider 只需实现端口并调用 Register，零侵入 ----

var (
	regMu    sync.RWMutex
	registry = map[string]ports.CompletionProvider{}
)

// Register 注册一个 provider（按 Name 覆盖）。
// nil 或空 Name 会被拒绝并安全忽略；启动期希望「配置错了就立刻炸」请用 MustRegister。
func Register(p ports.CompletionProvider) {
	_ = RegisterChecked(p)
}

// MustRegister 与 Register 相同，但非法 provider 直接 panic，适合 init() 期使用。
func MustRegister(p ports.CompletionProvider) {
	if err := RegisterChecked(p); err != nil {
		panic(err)
	}
}

// RegisterChecked 注册并返回校验错误，供需要显式处理失败的场景使用。
func RegisterChecked(p ports.CompletionProvider) error {
	if p == nil {
		return errors.New("ai: 不能注册 nil provider")
	}
	name := strings.TrimSpace(p.Name())
	if name == "" {
		return errors.New("ai: provider 名称不能为空")
	}
	regMu.Lock()
	registry[name] = p
	regMu.Unlock()
	return nil
}

// Get 按名称取已注册的 provider。
func Get(name string) (ports.CompletionProvider, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// List 返回所有已注册 provider 的名称（字典序，结果稳定）。
func List() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
