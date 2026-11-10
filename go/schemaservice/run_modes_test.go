package schemaservice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"order_server/go/kernel"
	"order_server/go/orderschema"
	"order_server/go/ports"
)

func waitRun(t *testing.T, s *Service) *RunReport {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.IsRunning() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.IsRunning() {
		t.Fatal("识别运行超时未结束")
	}
	rep := s.GetLastRun()
	if rep == nil {
		t.Fatal("应产生运行报告")
	}
	return rep
}

// blockingSource 卡在 ctx 上，用来验证取消确实贯穿到导出阶段。
type blockingSource struct {
	started sync.Once
	ready   chan struct{}
}

func (s *blockingSource) Name() string { return "blocking" }
func (s *blockingSource) Export(ctx context.Context, _ ports.ExportOptions) (*ports.ExportResult, error) {
	s.started.Do(func() { close(s.ready) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func failingFactory[T any](msg string) func(RunOptions) (T, error) {
	return func(RunOptions) (T, error) { return *new(T), errors.New(msg) }
}

// TestRunModesContract 一次覆盖三条本阶段的核心契约：
// 运行期不碰 AI、试跑零入库、取消不算失败。
func TestRunModesContract(t *testing.T) {
	t.Run("no AI needed", func(t *testing.T) {
		s := newTestService([]ports.ChatMessage{
			{ID: "m1", Sender: "A", Content: "店名：示例广场店\n电话：13800138000", Time: "2026-11-10T10:00:00+08:00"},
		})
		publishedTemplate(t, s)
		s.aiFactory = func(string) (ports.CompletionProvider, error) {
			return nil, errors.New("未配置任何 AI 模型")
		}
		if err := s.StartRun(RunOptions{From: "2026-11-10", To: "2026-11-10"}); err != nil {
			t.Fatal(err)
		}
		rep := waitRun(t, s)
		if rep.Error != "" || rep.OrderCount != 1 {
			t.Fatalf("运行期不应被 AI 配置阻断: %+v", rep)
		}
	})

	t.Run("dry run does not persist", func(t *testing.T) {
		s := newTestService([]ports.ChatMessage{
			{ID: "m1", Sender: "A", Content: "店名：示例广场店\n电话：13800138000", Time: "2026-11-10T10:00:00+08:00"},
		})
		publishedTemplate(t, s)
		s.aiFactory = func(string) (ports.CompletionProvider, error) {
			return nil, errors.New("未配置任何 AI 模型")
		}
		if err := s.StartRun(RunOptions{From: "2026-11-10", To: "2026-11-10", DryRun: true}); err != nil {
			t.Fatal(err)
		}
		rep := waitRun(t, s)
		if !rep.DryRun || rep.Error != "" || rep.OrderCount != 1 {
			t.Fatalf("试跑应完整抽取但标记 DryRun: %+v", rep)
		}
		rows, err := s.ListOrders(OrderQueryOptions{})
		if err != nil || len(rows) != 0 {
			t.Fatalf("试跑绝不能入库: rows=%d err=%v", len(rows), err)
		}
		// 试跑不应占用去重水位：正式运行仍能入库。
		if err := s.StartRun(RunOptions{From: "2026-11-10", To: "2026-11-10"}); err != nil {
			t.Fatal(err)
		}
		real := waitRun(t, s)
		if real.DryRun || real.OrderCount != 1 {
			t.Fatalf("试跑后的正式运行应入库 1 笔: %+v", real)
		}
	})

	t.Run("cancel is not a failure", func(t *testing.T) {
		src := &blockingSource{ready: make(chan struct{})}
		s := &Service{
			store: orderschema.NewMemoryStore(),
			log:   kernel.NoopLogger{},
			sourceFactory: func(RunOptions) (ports.ChatSource, error) {
				return src, nil
			},
			baseCtx: context.Background(),
		}
		if err := s.StartRun(RunOptions{From: "2026-11-10", To: "2026-11-10"}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-src.ready:
		case <-time.After(3 * time.Second):
			t.Fatal("导出阶段未启动")
		}
		if !s.CancelRun() {
			t.Fatal("运行中 CancelRun 应返回 true")
		}
		rep := waitRun(t, s)
		if !rep.Cancelled || rep.Error != "" {
			t.Fatalf("取消应标记 Cancelled 而非 Error: %+v", rep)
		}
		rows, _ := s.ListOrders(OrderQueryOptions{})
		if len(rows) != 0 {
			t.Fatalf("取消后不应写入订单，实际 %d 笔", len(rows))
		}
		if s.CancelRun() {
			t.Fatal("空闲时 CancelRun 应返回 false")
		}
	})
}

// TestInferenceDoesNotNeedChatSource 设计期归纳与运行期识别彻底解耦：
// 纯启发式连 AI 都不碰，AI 归纳也不需要聊天源。
func TestInferenceDoesNotNeedChatSource(t *testing.T) {
	s := newTestService(nil)
	s.sourceFactory = failingFactory[ports.ChatSource]("不应访问聊天源")
	s.aiFactory = func(string) (ports.CompletionProvider, error) {
		return nil, errors.New("未配置任何 AI 模型")
	}
	res, err := s.InferTemplate(context.Background(), InferInput{
		Name:    "纯启发式",
		Samples: []string{"店名：示例店\n电话：13800138000", "店名：另一店\n电话：13900139000"},
	})
	if err != nil || res.Template == nil {
		t.Fatalf("纯启发式归纳不应依赖 AI / 聊天源: res=%v err=%v", res, err)
	}

	s.aiFactory = func(string) (ports.CompletionProvider, error) { return stubAI{}, nil }
	res2, err := s.InferTemplate(context.Background(), InferInput{
		Name: "AI归纳", UseAI: true,
		Samples: []string{"店名：示例店\n电话：13800138000"},
	})
	if err != nil || res2.Template == nil {
		t.Fatalf("AI 归纳不应依赖聊天源: res=%v err=%v", res2, err)
	}
}

// TestFileSourceEndToEnd 真实落一份 JSONL，验证注册表来源 + CSV/JSONL 适配器接进主链路。
func TestFileSourceEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.jsonl")
	row := map[string]any{
		"id": "m1", "sender": "张三",
		"content": "店名：示例广场店\n电话：13800138000",
		"time":    "2026-11-10T10:00:00+08:00",
	}
	line, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(line, '\n'), 0600); err != nil {
		t.Fatal(err)
	}

	s := &Service{
		store: orderschema.NewMemoryStore(),
		log:   kernel.NoopLogger{},
		aiFactory: func(string) (ports.CompletionProvider, error) {
			return nil, errors.New("运行期不应访问 AI")
		},
	}
	publishedTemplate(t, s)
	if err := s.StartRun(RunOptions{
		From: "2026-11-10", To: "2026-11-10", Source: "file", DataDir: path,
	}); err != nil {
		t.Fatal(err)
	}
	rep := waitRun(t, s)
	if rep.Error != "" || rep.Source != "file" || rep.OrderCount != 1 {
		t.Fatalf("文件来源端到端失败: %+v", rep)
	}
}

// TestSourceRegistry 注册表的校验与内置来源，第三方可一行注册。
func TestSourceRegistry(t *testing.T) {
	list := Sources()
	for _, want := range []string{"wechat", "file"} {
		found := false
		for _, name := range list {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("内置来源 %q 未注册，实际 %v", want, list)
		}
	}
	if err := RegisterSource("", func(RunOptions) (ports.ChatSource, error) { return nil, nil }); err == nil {
		t.Fatal("空名称应被拒绝")
	}
	if err := RegisterSource("probe-nil", nil); err == nil {
		t.Fatal("空工厂应被拒绝")
	}
	if err := RegisterSource("WECHAT", func(RunOptions) (ports.ChatSource, error) { return nil, nil }); err == nil {
		t.Fatal("重复注册（大小写不敏感）应被拒绝")
	}
	if err := RegisterSource("probe", func(RunOptions) (ports.ChatSource, error) { return &stubSource{}, nil }); err != nil {
		t.Fatalf("第三方注册应成功: %v", err)
	}
	defer func() {
		sourceMu.Lock()
		delete(sourceFactories, "probe")
		sourceMu.Unlock()
	}()
	if got := strings.Join(Sources(), ","); !strings.Contains(got, "probe") {
		t.Fatalf("新注册来源应出现在列表中: %s", got)
	}

	// 未知来源要给出可诊断的错误，而不是静默回退到微信。
	s := &Service{store: orderschema.NewMemoryStore(), log: kernel.NoopLogger{}}
	if _, err := s.resolveSource(RunOptions{Source: "does-not-exist"}); err == nil || !strings.Contains(err.Error(), "未知数据源") {
		t.Fatalf("未知来源应明确报错，实际 %v", err)
	}
}

// TestFileSourceValidation 文件来源的路径校验，避免把目录 / 非法扩展名送进解析器。
func TestFileSourceValidation(t *testing.T) {
	dir := t.TempDir()
	if _, err := newFileSource(RunOptions{}); err == nil {
		t.Fatal("空路径应报错")
	}
	if _, err := newFileSource(RunOptions{DataDir: dir}); err == nil {
		t.Fatal("目录应被拒绝")
	}
	txt := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(txt, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newFileSource(RunOptions{DataDir: txt}); err == nil {
		t.Fatal("不支持的扩展名应被拒绝")
	}
	if _, err := newFileSource(RunOptions{DataDir: filepath.Join(dir, "missing.csv")}); err == nil {
		t.Fatal("不存在的文件应报错")
	}
	good := filepath.Join(dir, "a.jsonl")
	if err := os.WriteFile(good, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	src, err := newFileSource(RunOptions{DataDir: good})
	if err != nil || src.Name() != "file" {
		t.Fatalf("合法文件应构造成功: src=%v err=%v", src, err)
	}
}
