package schemaservice

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"order_server/go/kernel"
	"order_server/go/orderschema"
	"order_server/go/ports"
)

// ---------------------------------------------------------------------------
// 测试桩
// ---------------------------------------------------------------------------

type stubSource struct{ msgs []ports.ChatMessage }

func (s *stubSource) Name() string { return "stub" }
func (s *stubSource) Export(_ context.Context, _ ports.ExportOptions) (*ports.ExportResult, error) {
	return &ports.ExportResult{Messages: s.msgs}, nil
}

type stubAI struct{}

func (stubAI) Name() string { return "stub" }
func (stubAI) Complete(_ context.Context, _ ports.CompletionRequest) (*ports.CompletionResponse, error) {
	return &ports.CompletionResponse{Text: "{}"}, nil
}

func newTestService(msgs []ports.ChatMessage) *Service {
	return &Service{
		store: orderschema.NewMemoryStore(),
		log:   kernel.NoopLogger{},
		sourceFactory: func(RunOptions) (ports.ChatSource, error) {
			return &stubSource{msgs: msgs}, nil
		},
		aiFactory: func(string) (ports.CompletionProvider, error) { return stubAI{}, nil },
	}
}

func publishedTemplate(t *testing.T, s *Service) *orderschema.Template {
	t.Helper()
	tpl, err := s.SaveTemplate(SaveTemplateInput{
		Name:       "生鲜订单",
		Signatures: orderschema.Signatures{{Keywords: []string{"店名"}}},
		Fields: orderschema.FieldDefs{
			{Key: "recipient", Label: "收件人", Type: orderschema.FieldString, Required: true, Indexed: true,
				Extract: orderschema.ExtractRule{Kind: orderschema.ExtractLabel, Labels: []string{"店名"}}},
			{Key: "phone", Label: "电话", Type: orderschema.FieldPhone, Indexed: true,
				Extract: orderschema.ExtractRule{Kind: orderschema.ExtractLabel, Labels: []string{"电话", "手机"}}},
		},
	})
	if err != nil {
		t.Fatalf("保存模板失败: %v", err)
	}
	tpl, err = s.PublishTemplate(tpl.ID)
	if err != nil {
		t.Fatalf("发布模板失败: %v", err)
	}
	return tpl
}

// ---------------------------------------------------------------------------
// 模板生命周期
// ---------------------------------------------------------------------------

func TestTemplateLifecycle(t *testing.T) {
	s := newTestService(nil)
	tpl := publishedTemplate(t, s)

	if tpl.Status != orderschema.TemplatePublished || tpl.Version != 2 {
		t.Fatalf("发布后应为 published v2，实际 %s v%d", tpl.Status, tpl.Version)
	}

	// 修改 → 新版本 draft，最新版不再 published
	tpl2, err := s.SaveTemplate(SaveTemplateInput{
		ID:         tpl.ID,
		Name:       "生鲜订单",
		Signatures: orderschema.Signatures{{Keywords: []string{"店名"}}},
		Fields:     tpl.Fields,
	})
	if err != nil {
		t.Fatalf("另存失败: %v", err)
	}
	if tpl2.Version != 3 || tpl2.Status != orderschema.TemplateDraft {
		t.Fatalf("另存应为 draft v3，实际 %s v%d", tpl2.Status, tpl2.Version)
	}

	// 停用 → deprecated，路由不再命中
	tpl3, err := s.DeprecateTemplate(tpl.ID)
	if err != nil {
		t.Fatalf("停用失败: %v", err)
	}
	if tpl3.Status != orderschema.TemplateDeprecated {
		t.Fatalf("停用后应为 deprecated，实际 %s", tpl3.Status)
	}
	pub, err := s.ListTemplates(true)
	if err != nil || len(pub) != 0 {
		t.Fatalf("停用后已发布列表应为空，实际 %d (err=%v)", len(pub), err)
	}
}

func TestSaveTemplateValidation(t *testing.T) {
	s := newTestService(nil)
	if _, err := s.SaveTemplate(SaveTemplateInput{Name: " ", Signatures: orderschema.Signatures{{}}, Fields: orderschema.FieldDefs{{}}}); err == nil {
		t.Fatal("空名称应报错")
	}
	if _, err := s.SaveTemplate(SaveTemplateInput{Name: "x", Signatures: orderschema.Signatures{}, Fields: orderschema.FieldDefs{{}}}); err == nil {
		t.Fatal("无签名应报错")
	}
	if _, err := s.SaveTemplate(SaveTemplateInput{Name: "x", Signatures: orderschema.Signatures{{Keywords: []string{"k"}}}, Fields: nil}); err == nil {
		t.Fatal("无字段应报错")
	}
}

// ---------------------------------------------------------------------------
// 识别运行（stub 源端到端）
// ---------------------------------------------------------------------------

func TestStartRunEndToEnd(t *testing.T) {
	ts := "2026-11-10T10:00:00+08:00"
	msgs := []ports.ChatMessage{
		{ID: "sys", Sender: "系统", Content: "系统消息", Time: ts, IsSystem: true},
		{ID: "m1", Sender: "张三", Content: "店名：示例广场店\n电话：13800138000", Time: ts},
		{ID: "m2", Sender: "李四", Content: "今天天气不错", Time: ts},
	}
	s := newTestService(msgs)
	publishedTemplate(t, s)

	var mu sync.Mutex
	var events []string
	s.mu.Lock()
	s.emit = func(name string, _ any) {
		mu.Lock()
		events = append(events, name)
		mu.Unlock()
	}
	s.mu.Unlock()

	if err := s.StartRun(RunOptions{From: "2026-11-10", To: "2026-11-10"}); err != nil {
		t.Fatalf("启动运行失败: %v", err)
	}
	// 第二次并发启动应被拒绝
	if err := s.StartRun(RunOptions{From: "2026-11-10", To: "2026-11-10"}); err == nil {
		t.Fatal("运行中重复启动应被拒绝")
	}
	// 等待完成
	deadline := time.Now().Add(5 * time.Second)
	for s.IsRunning() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.IsRunning() {
		t.Fatal("运行超时未结束")
	}
	rep := s.GetLastRun()
	if rep == nil || rep.Error != "" {
		t.Fatalf("运行失败: %+v", rep)
	}
	if rep.OrderCount != 1 || rep.UnmatchedCount != 1 {
		t.Fatalf("应为 1 订单 1 未匹配，实际 %d/%d", rep.OrderCount, rep.UnmatchedCount)
	}
	if len(rep.Orders) != 1 || rep.Orders[0].Phone == nil || *rep.Orders[0].Phone != "13800138000" {
		t.Fatalf("订单镜像列错误: %+v", rep.Orders)
	}
	// 幂等：再跑一次，去重后订单不重复入库
	if err := s.StartRun(RunOptions{From: "2026-11-10", To: "2026-11-10"}); err != nil {
		t.Fatalf("第二次运行失败: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for s.IsRunning() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	orders, err := s.ListOrders(OrderQueryOptions{})
	if err != nil {
		t.Fatalf("查询订单失败: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("重复运行后应仍只有 1 笔订单（按 SourceMsgID 去重），实际 %d", len(orders))
	}
	mu.Lock()
	hasResult := false
	for _, e := range events {
		if e == "schema-result" {
			hasResult = true
		}
	}
	mu.Unlock()
	if !hasResult {
		t.Fatal("应发出 schema-result 事件")
	}
}

func TestParseDayRange(t *testing.T) {
	if _, _, err := parseDayRange("", ""); err == nil {
		t.Fatal("空日期应报错")
	}
	if _, _, err := parseDayRange("2026-11-10", "2026-11-09"); err == nil {
		t.Fatal("结束早于开始应报错")
	}
	if _, _, err := parseDayRange("2026-01-01", "2026-12-31"); err == nil {
		t.Fatal("超 31 天应报错")
	}
	f, to, err := parseDayRange("2026-11-01", "2026-11-10")
	if err != nil {
		t.Fatalf("合法区间应通过: %v", err)
	}
	if f.Format("2006-01-02") != "2026-11-01" || to.Format("2006-01-02") != "2026-11-10" {
		t.Fatalf("区间解析错误: %v ~ %v", f, to)
	}
}

func TestTemplateIDFromName(t *testing.T) {
	if id := templateIDFromName("生鲜 团购订单!"); id != "生鲜-团购订单" && !strings.Contains(id, "tpl-") {
		// 中文字符会被过滤掉，兜底为 tpl-<ts>
		t.Logf("中文模板名兜底 ID: %s", id)
	}
	if id := templateIDFromName("Fresh Order 01"); id != "fresh-order-01" {
		t.Fatalf("英文模板 ID 应为 fresh-order-01，实际 %q", id)
	}
}

// ---------------------------------------------------------------------------
// 样例推断（启发式）
// ---------------------------------------------------------------------------

func TestInferTemplateHeuristic(t *testing.T) {
	s := newTestService(nil)
	res, err := s.InferTemplate(context.Background(), InferInput{
		Name:    "推断模板",
		Samples: []string{"店名：示例店\n电话：13800138000\n【广香5斤】", "店名：另一店\n电话：13900139000\n【黄柠檬3斤】"},
	})
	if err != nil {
		t.Fatalf("推断失败: %v", err)
	}
	if res.Template == nil || len(res.Template.Fields) == 0 {
		t.Fatalf("应推断出字段: %+v", res.Template)
	}
	if res.Report == nil || res.Report.SampleCount != 2 {
		t.Fatalf("报告应记录 2 条样例: %+v", res.Report)
	}
}
