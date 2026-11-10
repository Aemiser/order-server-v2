package kernel

import (
	"context"
	"sync"
)

// Lifecycle 管理进程级生命周期：一个可取消的根 context、优雅退出的 WaitGroup、
// 以及一次性关闭钩子。所有长生命周期后台任务都从这里领取 ctx 并登记到 WaitGroup；
// 关闭时统一取消根 ctx、逆序执行钩子、等待任务结束。
//
// 这直接解决「关窗口后协程还在跑、100 个 AI 请求停不下来、worker 无限 ticker」的问题。
type Lifecycle struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	hooks  []func(ctx context.Context)
}

// NewLifecycle 构造一个生命周期管理器（根 context 默认未取消）。
func NewLifecycle() *Lifecycle {
	ctx, cancel := context.WithCancel(context.Background())
	return &Lifecycle{ctx: ctx, cancel: cancel}
}

// Ctx 返回受本生命周期管控的根 context；任务应在 ctx.Done() 上及时退出。
func (l *Lifecycle) Ctx() context.Context { return l.ctx }

// Cancelled 报告根 context 是否已被取消。
func (l *Lifecycle) Cancelled() bool {
	select {
	case <-l.ctx.Done():
		return true
	default:
		return false
	}
}

// Go 启动一个受管控的 goroutine；ctx 取消时 fn 应当自行返回。
func (l *Lifecycle) Go(name string, fn func(ctx context.Context)) {
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		defer RecoverLogrus(nil, "lifecycle:"+name)
		fn(l.ctx)
	}()
}

// OnShutdown 注册关闭钩子，按注册逆序执行（后注册先执行，符合「后开先关」）。
func (l *Lifecycle) OnShutdown(hook func(ctx context.Context)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hooks = append(l.hooks, hook)
}

// Shutdown 取消根 ctx、逆序执行关闭钩子、并等待所有 Go 任务结束。
// 可安全重复调用（后续调用只等待，不再重复执行钩子）。
func (l *Lifecycle) Shutdown() {
	l.cancel()
	l.mu.Lock()
	hooks := l.hooks
	l.hooks = nil
	l.mu.Unlock()
	for i := len(hooks) - 1; i >= 0; i-- {
		hooks[i](l.ctx)
	}
	l.wg.Wait()
}

// ---- 进程级默认生命周期（便捷入口，不排斥业务自行持有实例）----
// 单实例桌面应用只需一个进程级生命周期信号，因此提供一个默认实例。
// 它不是一个「服务定位器」：业务依赖（DB、配置、AI）仍走显式注入，
// 这里只承载「何时该停」这一个跨模块的进程信号。

var defaultLifecycle = NewLifecycle()

// DefaultLifecycle 返回进程级默认生命周期实例。
func DefaultLifecycle() *Lifecycle { return defaultLifecycle }

// ShutdownDefault 触发默认生命周期的优雅关闭。
func ShutdownDefault() { defaultLifecycle.Shutdown() }
