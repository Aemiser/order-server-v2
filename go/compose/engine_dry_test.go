package compose

import (
	"context"
	"errors"
	"testing"

	"order_server/go/kernel"
	"order_server/go/orderschema"
	"order_server/go/ports"
)

// preparedStore 造一个已有「已发布生鲜模板」的内存仓储，供下面几个用例复用。
func preparedStore(t *testing.T) orderschema.Store {
	t.Helper()
	store := orderschema.NewMemoryStore()
	tpl, _, err := orderschema.InferTemplate([]string{fresh1, fresh2}, orderschema.InferOptions{
		ID: "fresh", Name: "生鲜团购",
	})
	if err != nil {
		t.Fatalf("归纳模板失败: %v", err)
	}
	if err := orderschema.Publish(tpl); err != nil {
		t.Fatalf("发布模板失败: %v", err)
	}
	if err := store.SaveTemplate(context.Background(), tpl); err != nil {
		t.Fatalf("保存模板失败: %v", err)
	}
	return store
}

func freshChat() *memChatSource {
	return &memChatSource{messages: []ports.ChatMessage{
		{ID: "m1", Sender: "A", Content: fresh1, Time: "2024-11-09T10:00:00Z"},
		{ID: "m2", Sender: "B", Content: booking1, Time: "2024-11-09T11:00:00Z"},
	}}
}

func countOrders(t *testing.T, store orderschema.Store) int {
	t.Helper()
	rows, err := store.FindOrders(context.Background(), orderschema.OrderQuery{})
	if err != nil {
		t.Fatalf("查询订单失败: %v", err)
	}
	return len(rows)
}

// TestRecognitionEngineRunsWithoutAI 锁住本阶段最关键的契约：
// 运行期只做路由 + 按已注册 schema 抽取，不依赖任何 AI 配置。
func TestRecognitionEngineRunsWithoutAI(t *testing.T) {
	store := preparedStore(t)
	chat := freshChat()
	e, err := NewRecognitionEngine(chat, store, kernel.NoopLogger{}, 0.35)
	if err != nil {
		t.Fatalf("运行期引擎不应要求 AI: %v", err)
	}
	rep, err := e.Run(context.Background(), ports.ExportOptions{})
	if err != nil {
		t.Fatalf("运行期识别失败: %v", err)
	}
	if len(rep.Orders) != 1 || len(rep.Unmatched) != 1 {
		t.Fatalf("应为 1 订单 / 1 未匹配，实际 %d / %d", len(rep.Orders), len(rep.Unmatched))
	}
	if rep.DryRun {
		t.Fatal("正式运行的 Report.DryRun 应为 false")
	}
	if countOrders(t, store) != 1 {
		t.Fatal("正式运行应落库 1 笔")
	}

	// 运行期引擎没有 AI，归纳要显式报错而不是空指针或静默结果。
	if _, _, err := e.InferTemplate(context.Background(), []string{fresh1}, orderschema.InferOptions{ID: "x", Name: "x"}); err == nil {
		t.Fatal("无 AI 的运行期引擎应拒绝模板归纳")
	}
	// 完整引擎仍保持原契约：明确要 AI 时，nil 必须被拒绝。
	if _, err := NewEngine(chat, nil, store, nil, 0.35); err == nil {
		t.Fatal("NewEngine 的 ai 为 nil 时仍应报错（原契约不得回归）")
	}
}

// TestRunDryDoesNotPersist 试跑必须完整抽取，但绝不写库、不改变后续正式运行的结果。
func TestRunDryDoesNotPersist(t *testing.T) {
	store := preparedStore(t)
	e, err := NewRecognitionEngine(freshChat(), store, nil, 0.35)
	if err != nil {
		t.Fatal(err)
	}
	dry, err := e.RunDry(context.Background(), ports.ExportOptions{})
	if err != nil {
		t.Fatalf("试跑失败: %v", err)
	}
	if !dry.DryRun {
		t.Fatal("RunDry 的报告应标记 DryRun=true")
	}
	if len(dry.Orders) != 1 || len(dry.Unmatched) != 1 {
		t.Fatalf("试跑也应完整抽取，实际 %d 订单 / %d 未匹配", len(dry.Orders), len(dry.Unmatched))
	}
	if countOrders(t, store) != 0 {
		t.Fatal("试跑绝不能写库")
	}

	real, err := e.Run(context.Background(), ports.ExportOptions{})
	if err != nil {
		t.Fatalf("试跑后的正式运行失败: %v", err)
	}
	if len(real.Orders) != 1 {
		t.Fatalf("试跑不应影响正式运行的去重，实际入库 %d 笔", len(real.Orders))
	}
	if countOrders(t, store) != 1 {
		t.Fatal("正式运行应恰好落库 1 笔")
	}
}

// TestInferenceEngine 设计期引擎不依赖聊天源配置，但不能被误用来跑识别。
func TestInferenceEngine(t *testing.T) {
	store := preparedStore(t)
	ai := &fakeProvider{text: `{"fields":[{"label":"客户","type":"string","frequency":0.9,"labels":["客户"],"aliases":["客户"]}]}`}
	e, err := NewInferenceEngine(ai, store, nil)
	if err != nil {
		t.Fatalf("归纳引擎不应要求 ChatSource: %v", err)
	}
	tpl, rep, err := e.InferTemplate(context.Background(), []string{fresh1}, orderschema.InferOptions{ID: "ai2", Name: "AI模板"})
	if err != nil || tpl == nil || rep == nil {
		t.Fatalf("归纳失败: tpl=%v rep=%v err=%v", tpl, rep, err)
	}
	if _, err := e.Run(context.Background(), ports.ExportOptions{}); err == nil {
		t.Fatal("归纳引擎没有 ChatSource，应明确拒绝 Run")
	}
	if _, err := NewInferenceEngine(nil, store, nil); err == nil {
		t.Fatal("ai 为 nil 时应拒绝构造")
	}
}

// TestRunRespectsCancellation 阶段间与逐条处理都要响应 ctx，取消后不得写库。
func TestRunRespectsCancellation(t *testing.T) {
	store := preparedStore(t)
	e, err := NewRecognitionEngine(freshChat(), store, nil, 0.35)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Run(ctx, ports.ExportOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消的 ctx 应返回 context.Canceled，实际 %v", err)
	}
	if _, err := e.RunDry(ctx, ports.ExportOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("试跑同样应响应取消，实际 %v", err)
	}
	if n := countOrders(t, store); n != 0 {
		t.Fatalf("取消后不应写入任何订单，实际 %d 笔", n)
	}
}
