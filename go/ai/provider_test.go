package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"order_server/go/kernel"
	"order_server/go/ports"
)

const sampleResp = `{"choices":[{"message":{"content":"广香5斤"}}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`

func newTestProvider(url string) *Provider {
	return NewProvider(ProviderConfig{
		Name:        "test",
		APIURL:      url,
		APIKey:      "k",
		TimeoutSec:  5,
		PromptPrice: 0.001,
		CompPrice:   0.002,
		MaxRetries:  2,
	}, kernel.NoopLogger{})
}

func TestComplete_NonStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleResp))
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	resp, err := p.Complete(context.Background(), ports.CompletionRequest{
		Model:        "test",
		SystemPrompt: "你是一个订单识别器",
		UserPrompt:   "广香5斤",
	})
	if err != nil {
		t.Fatalf("Complete 失败: %v", err)
	}
	if resp.Text != "广香5斤" {
		t.Fatalf("文本解析错误: %q", resp.Text)
	}
	if resp.TokensIn != 10 || resp.TokensOut != 3 {
		t.Fatalf("token 解析错误: in=%d out=%d", resp.TokensIn, resp.TokensOut)
	}
	// 成本 = 10/1000*0.001 + 3/1000*0.002 = 0.00001 + 0.000006 = 0.000016
	if resp.Cost <= 0 {
		t.Fatalf("成本应大于 0: %v", resp.Cost)
	}
}

func TestComplete_Retry(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n <= 2 { // 前两次失败
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleResp))
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	resp, err := p.Complete(context.Background(), ports.CompletionRequest{Model: "test", UserPrompt: "x"})
	if err != nil {
		t.Fatalf("重试后应成功，却失败: %v", err)
	}
	if resp.Text != "广香5斤" {
		t.Fatalf("重试后文本错误: %q", resp.Text)
	}
	if atomic.LoadInt32(&hits) != 3 {
		t.Fatalf("应恰好请求 3 次（2 失败 + 1 成功），实际 %d", hits)
	}
}

func TestComplete_AllFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL) // MaxRetries=2 → 共 3 次
	_, err := p.Complete(context.Background(), ports.CompletionRequest{Model: "test", UserPrompt: "x"})
	if err == nil {
		t.Fatal("全部失败时应返回错误")
	}
}

func TestComplete_Stream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, c := range []string{`{"choices":[{"delta":{"content":"你"}}]}`, `{"choices":[{"delta":{"content":"好"}}]}`, `[DONE]`} {
			fmt.Fprintf(w, "data: %s\n\n", c)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	var got []string
	resp, err := p.Complete(context.Background(), ports.CompletionRequest{
		Model:      "test",
		UserPrompt: "x",
		Stream:     func(delta string) { got = append(got, delta) },
	})
	if err != nil {
		t.Fatalf("流式失败: %v", err)
	}
	if resp.Text != "你好" {
		t.Fatalf("流式文本拼接错误: %q", resp.Text)
	}
	if strings.Join(got, "") != "你好" {
		t.Fatalf("流式回调内容错误: %v", got)
	}
}

func TestRegistry(t *testing.T) {
	Register(&Provider{cfg: ProviderConfig{Name: "deepseek"}})
	p, ok := Get("deepseek")
	if !ok || p.Name() != "deepseek" {
		t.Fatal("注册/获取失败")
	}
	found := false
	for _, n := range List() {
		if n == "deepseek" {
			found = true
		}
	}
	if !found {
		t.Fatal("List 应包含已注册 provider")
	}
}
