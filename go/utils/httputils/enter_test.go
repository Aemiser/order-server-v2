package httputils

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 旧入口的错误文本必须与改造前逐字一致：旧识别链路有按字符串判断错误的代码。
func TestLegacyAiRequestErrorTextUnchanged(t *testing.T) {
	const body = `{"error":"invalid api key"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	_, err := AiRequest("k", srv.URL, []byte(`{}`), 5, nil)
	if err == nil {
		t.Fatal("非 200 应返回错误")
	}
	want := fmt.Sprintf("API返回错误状态码: %d, 响应: %s", http.StatusBadRequest, body)
	if err.Error() != want {
		t.Fatalf("旧入口错误文本被改变:\n got: %q\nwant: %q", err.Error(), want)
	}
	var se *HTTPStatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusBadRequest {
		t.Fatal("新链路应能从错误中取出类型化状态码")
	}
}

// 旧流式入口历史上不检查状态码（错误体被当 SSE 忽略，返回 nil）。
// 这里锁住该行为，确保 Phase 5 对旧链路「零行为变化」。
func TestLegacyStreamKeepsBehaviourOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"overloaded"}`))
	}))
	defer srv.Close()

	err := AiStreamRequest("k", srv.URL, []byte(`{}`), 5, nil, func(string) error { return nil })
	if err != nil {
		t.Fatalf("旧流式入口行为被改变，应仍返回 nil，实际: %v", err)
	}
}

func TestLegacyAiRequestSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	raw, err := AiRequest("k", srv.URL, []byte(`{}`), 5, nil)
	if err != nil || string(raw) != `{"ok":true}` {
		t.Fatalf("旧入口正常响应异常: raw=%q err=%v", raw, err)
	}
}

// ctx 取消必须真正中断在途请求，而不是等满 http.Client.Timeout。
func TestAiRequestCtxCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := AiRequestCtx(ctx, "k", srv.URL, []byte(`{}`), 10, nil)
	if err == nil {
		t.Fatal("取消后应返回错误")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("期望 DeadlineExceeded，实际: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("取消未及时中断，耗时 %s", elapsed)
	}
}

func TestAiRequestCtxCustomHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant") != "acme" {
			t.Errorf("自定义请求头未透传: %q", r.Header.Get("X-Tenant"))
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("Authorization 异常: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	_, err := AiRequestCtx(context.Background(), "secret", srv.URL, []byte(`{}`), 5,
		map[string]string{"X-Tenant": "acme"})
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
}

// 新流式入口对非 200 显式报错，不再把错误 JSON 当成空结果吞掉。
func TestAiStreamRequestCtxStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"overloaded"}`))
	}))
	defer srv.Close()

	err := AiStreamRequestCtx(context.Background(), "k", srv.URL, []byte(`{}`), 5, nil,
		func(string) error { return nil })
	var se *HTTPStatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("应返回 503 类型化错误，实际: %v", err)
	}
}

// 新流式入口在取消时返回 ctx.Err()，绝不返回「成功但文本为空」。
func TestAiStreamRequestCtxCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"你"}}]}`+"\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	err := AiStreamRequestCtx(ctx, "k", srv.URL, []byte(`{}`), 10, nil,
		func(string) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("取消应返回 DeadlineExceeded，实际: %v", err)
	}
}
