package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"order_server/go/kernel"
	"order_server/go/ports"
)

// FallbackProvider 把若干 CompletionProvider 串成「主 → 备」降级链，
// 本身仍是一个 ports.CompletionProvider，因此对上层完全透明：
// compose.Engine / orderschema 不需要知道背后换过模型。
//
// 两条刻意的边界：
//  1. ctx 已取消时不再降级——关窗口就该立刻停，不该再打一次备用端点；
//  2. 流式请求不降级——主链路可能已经吐出半段文本，切备用会让用户看到重复内容。
//
// 降级时不会改写 CompletionRequest.Model：自动模式请留空 Model（各 provider
// 用自己的默认模型）；若显式指定了模型，调用方需保证备用方兼容该模型名。
type FallbackProvider struct {
	name      string
	providers []ports.CompletionProvider
	logger    kernel.Logger

	mu   sync.RWMutex
	last string // 上次实际生效的 provider 名，便于诊断「现在跑在哪个模型上」
}

// NewFallback 构造降级链。primary 必填，backups 中的 nil 会被过滤；
// 没有任何有效 provider 时返回错误而不是构造出一个永远失败的链。
func NewFallback(primary ports.CompletionProvider, backups ...ports.CompletionProvider) (*FallbackProvider, error) {
	all := make([]ports.CompletionProvider, 0, 1+len(backups))
	for _, p := range append([]ports.CompletionProvider{primary}, backups...) {
		if p != nil {
			all = append(all, p)
		}
	}
	if len(all) == 0 {
		return nil, errors.New("ai: 降级链至少需要一个 provider")
	}
	names := make([]string, 0, len(all))
	for _, p := range all {
		names = append(names, p.Name())
	}
	return &FallbackProvider{
		name:      "fallback(" + strings.Join(names, ",") + ")",
		providers: all,
		logger:    kernel.NoopLogger{},
		last:      names[0],
	}, nil
}

// WithLogger 设置降级事件日志（返回自身，便于链式构造）。
func (f *FallbackProvider) WithLogger(l kernel.Logger) *FallbackProvider {
	if l != nil {
		f.logger = l
	}
	return f
}

// Name 返回组合标识，如 "fallback(deepseek,qwen)"。
func (f *FallbackProvider) Name() string { return f.name }

// Providers 返回链上各 provider 的名称（按优先级）。
func (f *FallbackProvider) Providers() []string {
	out := make([]string, 0, len(f.providers))
	for _, p := range f.providers {
		out = append(out, p.Name())
	}
	return out
}

// LastProvider 返回最近一次实际生效的 provider 名。
func (f *FallbackProvider) LastProvider() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.last
}

func (f *FallbackProvider) setLast(name string) {
	f.mu.Lock()
	f.last = name
	f.mu.Unlock()
}

// Complete 依次尝试链上 provider，返回第一个成功结果。
func (f *FallbackProvider) Complete(ctx context.Context, req ports.CompletionRequest) (*ports.CompletionResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// 流式不做降级：主链路一旦吐出 chunk，切换会造成重复文本。
	if req.Stream != nil {
		return f.providers[0].Complete(ctx, req)
	}

	var errs []string
	for i, p := range f.providers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resp, err := p.Complete(ctx, req)
		if err == nil && resp != nil {
			f.setLast(p.Name())
			if i > 0 {
				f.logger.Info("AI 已降级到备用提供方",
					kernel.F("provider", p.Name()),
					kernel.F("attempts", i+1))
			}
			return resp, nil
		}
		if err == nil {
			err = errors.New("返回空响应")
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		errs = append(errs, fmt.Sprintf("%s: %v", p.Name(), err))
		f.logger.Warn("AI 提供方失败，尝试下一个",
			kernel.F("provider", p.Name()), kernel.F("error", err.Error()))
	}
	return nil, fmt.Errorf("所有 AI 提供方均失败: %s", strings.Join(errs, " | "))
}
