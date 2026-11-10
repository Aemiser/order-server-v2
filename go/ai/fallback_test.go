package ai

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"order_server/go/ports"
)

type stubProvider struct {
	name        string
	resp        *ports.CompletionResponse
	err         error
	calls       int32
	streamCalls int32
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Complete(ctx context.Context, req ports.CompletionRequest) (*ports.CompletionResponse, error) {
	atomic.AddInt32(&s.calls, 1)
	if req.Stream != nil {
		atomic.AddInt32(&s.streamCalls, 1)
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func newStub(name string, text string, err error) *stubProvider {
	return &stubProvider{
		name: name,
		resp: &ports.CompletionResponse{Text: text},
		err:  err,
	}
}

func TestFallbackPrimaryWins(t *testing.T) {
	primary := newStub("primary", "主模型结果", nil)
	backup := newStub("backup", "备用结果", nil)
	f, err := NewFallback(primary, backup)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	resp, err := f.Complete(context.Background(), ports.CompletionRequest{UserPrompt: "x"})
	if err != nil || resp.Text != "主模型结果" {
		t.Fatalf("应返回主模型结果: resp=%+v err=%v", resp, err)
	}
	if atomic.LoadInt32(&backup.calls) != 0 {
		t.Fatal("主模型成功时不应调用备用")
	}
	if f.LastProvider() != "primary" {
		t.Fatalf("LastProvider 应为 primary，实际 %q", f.LastProvider())
	}
}

func TestFallbackSwitchesOnFailure(t *testing.T) {
	primary := newStub("primary", "", errors.New("503 upstream"))
	backup := newStub("backup", "备用结果", nil)
	f, err := NewFallback(primary, backup)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	resp, err := f.Complete(context.Background(), ports.CompletionRequest{UserPrompt: "x"})
	if err != nil || resp.Text != "备用结果" {
		t.Fatalf("应降级成功: resp=%+v err=%v", resp, err)
	}
	if f.LastProvider() != "backup" {
		t.Fatalf("LastProvider 应为 backup，实际 %q", f.LastProvider())
	}
	if got := f.Providers(); len(got) != 2 || got[0] != "primary" || got[1] != "backup" {
		t.Fatalf("Providers 顺序异常: %v", got)
	}
	if !strings.Contains(f.Name(), "primary") || !strings.Contains(f.Name(), "backup") {
		t.Fatalf("Name 应含链上成员: %q", f.Name())
	}
}

func TestFallbackAllFailAggregatesErrors(t *testing.T) {
	primary := newStub("primary", "", errors.New("主模型挂了"))
	backup := newStub("backup", "", errors.New("备用也挂了"))
	f, _ := NewFallback(primary, backup)
	_, err := f.Complete(context.Background(), ports.CompletionRequest{UserPrompt: "x"})
	if err == nil {
		t.Fatal("全部失败时应返回错误")
	}
	for _, want := range []string{"primary", "backup", "主模型挂了", "备用也挂了"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("聚合错误缺少 %q: %v", want, err)
		}
	}
}

// 关窗口就该立刻停，不该再打一次备用端点。
func TestFallbackCancelledBeforeCall(t *testing.T) {
	primary := newStub("primary", "ok", nil)
	backup := newStub("backup", "ok", nil)
	f, _ := NewFallback(primary, backup)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.Complete(ctx, ports.CompletionRequest{UserPrompt: "x"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回 context.Canceled，实际 %v", err)
	}
	if atomic.LoadInt32(&primary.calls) != 0 || atomic.LoadInt32(&backup.calls) != 0 {
		t.Fatal("ctx 已取消时不应调用任何 provider")
	}
}

func TestFallbackProviderCancelStopsChain(t *testing.T) {
	primary := newStub("primary", "", context.Canceled)
	backup := newStub("backup", "ok", nil)
	f, _ := NewFallback(primary, backup)
	_, err := f.Complete(context.Background(), ports.CompletionRequest{UserPrompt: "x"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回 context.Canceled，实际 %v", err)
	}
	if atomic.LoadInt32(&backup.calls) != 0 {
		t.Fatal("主模型因取消失败时不应降级")
	}
}

// 流式已可能吐出半段文本，切备用会造成重复内容，因此不降级。
func TestFallbackStreamDoesNotSwitch(t *testing.T) {
	primary := newStub("primary", "", errors.New("stream broke"))
	backup := newStub("backup", "ok", nil)
	f, _ := NewFallback(primary, backup)
	_, err := f.Complete(context.Background(), ports.CompletionRequest{
		UserPrompt: "x",
		Stream:     func(string) {},
	})
	if err == nil {
		t.Fatal("主模型失败应返回错误")
	}
	if atomic.LoadInt32(&backup.calls) != 0 {
		t.Fatal("流式请求不应降级")
	}
	if atomic.LoadInt32(&primary.streamCalls) != 1 {
		t.Fatal("流式请求应透传给主模型")
	}
}

func TestFallbackNilFilterAndValidation(t *testing.T) {
	if _, err := NewFallback(nil); err == nil {
		t.Fatal("没有有效 provider 时应返回错误")
	}
	if _, err := NewFallback(nil, nil); err == nil {
		t.Fatal("全部为 nil 时应返回错误")
	}
	primary := newStub("primary", "ok", nil)
	f, err := NewFallback(primary, nil)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if got := f.Providers(); len(got) != 1 || got[0] != "primary" {
		t.Fatalf("nil 备用应被过滤: %v", got)
	}
	if f.WithLogger(nil).logger == nil {
		t.Fatal("WithLogger(nil) 不应清空 logger")
	}
}
