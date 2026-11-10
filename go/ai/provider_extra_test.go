package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"order_server/go/kernel"
	"order_server/go/ports"
	"order_server/go/utils/httputils"
)

const okBody = `{"choices":[{"message":{"content":"广香5斤"}}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`

type cfgOpt func(*ProviderConfig)

func newFastProvider(url string, opts ...cfgOpt) *Provider {
	cfg := ProviderConfig{
		Name:           "test",
		APIURL:         url,
		APIKey:         "k",
		Model:          "deepseek-chat",
		TimeoutSec:     5,
		MaxRetries:     2,
		RetryBaseDelay: time.Millisecond,
		RetryMaxDelay:  4 * time.Millisecond,
		PromptPrice:    0.001,
		CompPrice:      0.002,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return NewProvider(cfg, kernel.NoopLogger{})
}

func writeOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(okBody))
}

// 关键回归：compose.CompletionFunc 不指定 Model，请求体必须用 ProviderConfig.Model，
// 否则真实 API 会收到 "model": "default" 而拒绝，模板归纳静默退化为启发式。
func TestPayloadUsesConfiguredModel(t *testing.T) {
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		writeOK(w)
	}))
	defer srv.Close()

	p := newFastProvider(srv.URL)
	if _, err := p.Complete(context.Background(), ports.CompletionRequest{UserPrompt: "x"}); err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	if got["model"] != "deepseek-chat" {
		t.Fatalf("未使用配置的默认模型，请求体 model=%v", got["model"])
	}
	if got["stream"] != false {
		t.Fatalf("非流式请求 stream 应为 false，实际 %v", got["stream"])
	}
}

func TestPayloadModelOverride(t *testing.T) {
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		writeOK(w)
	}))
	defer srv.Close()

	p := newFastProvider(srv.URL)
	_, _ = p.Complete(context.Background(), ports.CompletionRequest{Model: "qwen-plus", UserPrompt: "x"})
	if got["model"] != "qwen-plus" {
		t.Fatalf("显式 Model 未生效，实际 %v", got["model"])
	}
}

func TestPayloadCustomHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant") != "acme" {
			t.Errorf("自定义头丢失: %q", r.Header.Get("X-Tenant"))
		}
		writeOK(w)
	}))
	defer srv.Close()

	p := newFastProvider(srv.URL, func(c *ProviderConfig) {
		c.Headers = map[string]string{"X-Tenant": "acme"}
	})
	if _, err := p.Complete(context.Background(), ports.CompletionRequest{UserPrompt: "x"}); err != nil {
		t.Fatalf("请求失败: %v", err)
	}
}

// 调用方取消必须立刻中断在途 HTTP 请求，不能等满 TimeoutSec。
func TestContextCancelsInFlight(t *testing.T) {
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
	_, err := newFastProvider(srv.URL).Complete(ctx, ports.CompletionRequest{UserPrompt: "x"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("取消应返回 DeadlineExceeded，实际: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("取消未及时中断，耗时 %s", elapsed)
	}
}

// 4xx 是配置/请求错误，重试只会放大故障；5xx 与 429 才值得重试。
func TestRetryClassification(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		wantHits  int32
		wantError bool
	}{
		{"400 不重试", http.StatusBadRequest, 1, true},
		{"401 不重试", http.StatusUnauthorized, 1, true},
		{"429 重试", http.StatusTooManyRequests, 3, true},
		{"500 重试", http.StatusInternalServerError, 3, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&hits, 1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":"boom"}`))
			}))
			defer srv.Close()

			_, err := newFastProvider(srv.URL).Complete(context.Background(), ports.CompletionRequest{UserPrompt: "x"})
			if (err != nil) != tc.wantError {
				t.Fatalf("错误状态不符: err=%v", err)
			}
			if got := atomic.LoadInt32(&hits); got != tc.wantHits {
				t.Fatalf("请求次数应为 %d，实际 %d", tc.wantHits, got)
			}
		})
	}
}

// 429 之后恢复成功，验证退避重试仍能拿到正常结果。
func TestRetryThenSuccess(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		writeOK(w)
	}))
	defer srv.Close()

	resp, err := newFastProvider(srv.URL).Complete(context.Background(), ports.CompletionRequest{UserPrompt: "x"})
	if err != nil || resp.Text != "广香5斤" {
		t.Fatalf("重试后应成功: resp=%+v err=%v", resp, err)
	}
	if atomic.LoadInt32(&hits) != 3 {
		t.Fatalf("应请求 3 次，实际 %d", atomic.LoadInt32(&hits))
	}
}

// 限流信号量：同时在途请求数不得超过 MaxConcurrency。
func TestConcurrencyLimit(t *testing.T) {
	var inFlight, maxSeen int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			old := atomic.LoadInt32(&maxSeen)
			if n <= old || atomic.CompareAndSwapInt32(&maxSeen, old, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		writeOK(w)
	}))
	defer srv.Close()

	const limit = 3
	p := newFastProvider(srv.URL, func(c *ProviderConfig) { c.MaxConcurrency = limit })
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.Complete(context.Background(), ports.CompletionRequest{UserPrompt: "x"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发请求失败: %v", err)
		}
	}
	if got := atomic.LoadInt32(&maxSeen); got > limit {
		t.Fatalf("同时在途 %d 超过上限 %d", got, limit)
	}
}

// 限流排队时也能被 ctx 取消，不会卡在 acquire 上。
func TestAcquireRespectsCancellation(t *testing.T) {
	p := newFastProvider("http://127.0.0.1:1", func(c *ProviderConfig) { c.MaxConcurrency = 1 })
	p.sem <- struct{}{} // 占满唯一名额，制造排队
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Complete(ctx, ports.CompletionRequest{UserPrompt: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("排队中的取消应返回 context.Canceled，实际: %v", err)
	}
}

// 退避必须被 RetryMaxDelay 封顶，attempt 很大时也不能溢出成超长等待。
func TestBackoffCapped(t *testing.T) {
	p := newFastProvider("http://127.0.0.1:1", func(c *ProviderConfig) {
		c.RetryBaseDelay = 10 * time.Millisecond
		c.RetryMaxDelay = 20 * time.Millisecond
	})
	start := time.Now()
	if err := p.waitBackoff(context.Background(), 100); err != nil {
		t.Fatalf("waitBackoff 失败: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("退避未被封顶，耗时 %s", elapsed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.waitBackoff(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("退避等待应响应取消，实际: %v", err)
	}
}

// 新流式路径对非 200 显式报错，不再返回「空结果」。
func TestStreamNon200ReturnsError(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"overloaded"}`))
	}))
	defer srv.Close()

	_, err := newFastProvider(srv.URL).Complete(context.Background(), ports.CompletionRequest{
		UserPrompt: "x",
		Stream:     func(string) {},
	})
	var se *httputils.HTTPStatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("应返回 503 类型化错误，实际: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("5xx 应重试共 3 次，实际 %d", got)
	}
}

// 首包为空（只有 [DONE]）时允许重试；此时还没吐过字，不会重复。
func TestStreamRetryOnEmptyResponse(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if atomic.AddInt32(&hits, 1) == 1 {
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		for _, c := range []string{"你", "好"} {
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"%s\"}}]}\n\n", c)
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	var chunks []string
	resp, err := newFastProvider(srv.URL).Complete(context.Background(), ports.CompletionRequest{
		UserPrompt: "x",
		Stream:     func(s string) { chunks = append(chunks, s) },
	})
	if err != nil || resp == nil || resp.Text != "你好" {
		t.Fatalf("空响应后应重试成功: resp=%+v err=%v", resp, err)
	}
	if strings.Join(chunks, "") != "你好" || atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("流式回调/重试次数异常: chunks=%v hits=%d", chunks, atomic.LoadInt32(&hits))
	}
}

// 已经吐出 chunk 再中断：绝不重试，否则用户会看到重复文本。
func TestStreamNoRetryAfterEmit(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n")
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
	var chunks []string
	_, err := newFastProvider(srv.URL).Complete(ctx, ports.CompletionRequest{
		UserPrompt: "x",
		Stream:     func(s string) { chunks = append(chunks, s) },
	})
	if err == nil || !strings.Contains(err.Error(), "不自动重试") {
		t.Fatalf("中断后应带「不自动重试」说明返回错误，实际: %v", err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("已吐字后不应重试，实际请求 %d 次", atomic.LoadInt32(&hits))
	}
	if strings.Join(chunks, "") != "你" {
		t.Fatalf("回调内容异常: %v", chunks)
	}
}

func TestConfigDefaults(t *testing.T) {
	p := NewProvider(ProviderConfig{Name: "x"}, nil)
	c := p.Config()
	if c.TimeoutSec != defaultTimeout || c.RetryBaseDelay != defaultRetryBase || c.RetryMaxDelay != defaultRetryMax {
		t.Fatalf("默认值异常: %+v", c)
	}
	if p.sem != nil {
		t.Fatal("MaxConcurrency=0 时不应创建信号量")
	}
	if p2 := NewProvider(ProviderConfig{Name: "y", RetryBaseDelay: time.Second, MaxConcurrency: 2}, nil); p2.Config().RetryMaxDelay < p2.Config().RetryBaseDelay {
		t.Fatal("RetryMaxDelay 应至少等于 RetryBaseDelay")
	}
}

func TestRegistryValidation(t *testing.T) {
	if err := RegisterChecked(nil); err == nil {
		t.Fatal("nil provider 应被拒绝")
	}
	if err := RegisterChecked(&Provider{cfg: ProviderConfig{Name: "  "}}); err == nil {
		t.Fatal("空名称应被拒绝")
	}
	before := len(List())
	Register(nil)
	Register(&Provider{cfg: ProviderConfig{Name: " "}})
	if len(List()) != before {
		t.Fatal("非法 provider 不应污染注册表")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("MustRegister 对非法 provider 应 panic")
			}
		}()
		MustRegister(nil)
	}()

	Register(&Provider{cfg: ProviderConfig{Name: "zz-test-provider"}})
	Register(&Provider{cfg: ProviderConfig{Name: "aa-test-provider"}})
	names := List()
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("List 应按字典序返回: %v", names)
		}
	}
}
