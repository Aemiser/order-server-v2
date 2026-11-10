package kernel

import (
	"context"
	"sync"
)

// Task 是异步任务；返回 error 会通过 Submit 的结果 channel 回传。
type Task func(ctx context.Context) error

// WorkerPool 有界并发的异步任务池。所有任务共享生命周期 ctx：
// ctx 取消时，在途任务应当感知并中断；关闭窗口后不再派发新任务。
//
// 信号量（带缓冲的 channel）提供天然背压——提交速度超过并发上限时 Submit 会阻塞，
// 不会无节制地起 goroutine（修复此前 100 个裸 goroutine 无上限的问题）。
type WorkerPool struct {
	ctx context.Context
	sem chan struct{}
	wg  sync.WaitGroup
}

// NewWorkerPool 创建一个并发上限为 concurrency 的任务池；concurrency<1 时退化为 1。
func NewWorkerPool(ctx context.Context, concurrency int) *WorkerPool {
	if concurrency < 1 {
		concurrency = 1
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return &WorkerPool{ctx: ctx, sem: make(chan struct{}, concurrency)}
}

// Submit 提交一个任务，返回结果 channel（缓冲 1）。
// 当生命周期 ctx 已取消时立即返回 ctx.Err()，不再派发。
// 当并发已满时阻塞，直到有空闲槽位（背压）。
func (p *WorkerPool) Submit(task Task) <-chan error {
	res := make(chan error, 1)
	// 入口确定性检查：ctx 已取消时绝不派发（避免 select 在 ctx 与 sem 同时就绪时随机命中）。
	if p.ctx.Err() != nil {
		res <- p.ctx.Err()
		return res
	}
	select {
	case <-p.ctx.Done():
		res <- p.ctx.Err()
		return res
	case p.sem <- struct{}{}:
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() { <-p.sem }()
		res <- task(p.ctx)
	}()
	return res
}

// TrySubmit 非阻塞提交：若并发已满或 ctx 已取消，返回 (nil, false) 而不阻塞。
func (p *WorkerPool) TrySubmit(task Task) (<-chan error, bool) {
	if p.ctx.Err() != nil {
		return nil, false
	}
	select {
	case <-p.ctx.Done():
		return nil, false
	case p.sem <- struct{}{}:
	default:
		return nil, false
	}
	res := make(chan error, 1)
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() { <-p.sem }()
		res <- task(p.ctx)
	}()
	return res, true
}

// Wait 阻塞直到所有已提交任务完成。
func (p *WorkerPool) Wait() { p.wg.Wait() }

// Running 返回当前在途任务数量（近似，仅用于观测/日志）。
func (p *WorkerPool) Running() int { return len(p.sem) }
