package kernel

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestLogrusLogger_LevelsAndWith(t *testing.T) {
	lg := logrus.New()
	lg.SetOutput(io.Discard) // 测试不污染 stdout
	k := NewLogrusLogger(lg)
	// 各级别不应 panic
	k.Debug("d", F("k", 1))
	k.Info("i")
	k.Warn("w")
	k.Error("e", nil)
	// With 应返回可用的子 logger
	sub := k.With(F("order_id", "abc"))
	sub.Info("child", F("step", 2))
}

func TestNoopLogger(t *testing.T) {
	var l Logger = NoopLogger{}
	l.Debug("x")
	l.Info("x")
	l.Warn("x")
	l.Error("x", nil)
	l.With(F("a", 1)).Info("x")
}

func TestRecoverLogrus(t *testing.T) {
	// 抛出 panic 后必须被兜住，不向外传播
	func() {
		defer RecoverLogrus(nil, "unit-test")
		panic("boom")
	}()
}

func TestLifecycle_ShutdownCancelsAndWaits(t *testing.T) {
	lc := NewLifecycle()
	done := make(chan struct{})
	lc.Go("waiter", func(ctx context.Context) {
		<-ctx.Done()
		close(done)
	})
	if lc.Cancelled() {
		t.Fatal("不应在 Shutdown 前已取消")
	}
	lc.Shutdown()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("受管控的 goroutine 未在关闭后退出")
	}
	if !lc.Cancelled() {
		t.Fatal("Shutdown 后 Ctx 应已取消")
	}
}

func TestWorkerPool_BoundedConcurrency(t *testing.T) {
	lc := NewLifecycle()
	defer lc.Shutdown()
	p := NewWorkerPool(lc.Ctx(), 2)

	var mu sync.Mutex
	cur, max := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		p.Submit(func(ctx context.Context) error {
			mu.Lock()
			cur++
			if cur > max {
				max = cur
			}
			mu.Unlock()
			time.Sleep(15 * time.Millisecond)
			mu.Lock()
			cur--
			mu.Unlock()
			wg.Done()
			return nil
		})
	}
	wg.Wait()
	if max > 2 {
		t.Fatalf("并发上限被突破: max=%d", max)
	}
}

func TestWorkerPool_CtxCancelledRejects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := NewWorkerPool(ctx, 1)
	cancel()
	select {
	case err := <-p.Submit(func(context.Context) error { return nil }):
		if err == nil {
			t.Fatal("ctx 已取消时应返回错误而非派发")
		}
	case <-time.After(time.Second):
		t.Fatal("ctx 取消后 Submit 应立刻返回")
	}
}

func TestEventBus_EmitOn(t *testing.T) {
	b := NewEventBus()
	got := make(chan string, 1)
	b.On("order:new", func(p interface{}) {
		got <- p.(string)
	})
	b.Emit("order:new", "hello")
	select {
	case v := <-got:
		if v != "hello" {
			t.Fatalf("载荷不匹配: %v", v)
		}
	case <-time.After(time.Second):
		t.Fatal("事件未被广播")
	}
}
