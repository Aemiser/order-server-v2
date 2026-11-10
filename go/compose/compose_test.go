package compose

import (
	"context"
	"testing"

	"order_server/go/kernel"
	"order_server/go/orderschema"
	"order_server/go/ports"
)

// 以下样例与 orderschema 测试同源，覆盖「生鲜团购」与「服务预约」两种格式，
// 用来验证自动路由：生鲜模板只应命中生鲜消息，预约消息应进入人工审查。
const fresh1 = `11-9
店名：华夏农业大学店
手机：13800138000
地址：华夏省大兵市下区文化路1号
【广香5斤】,【黄柠檬3斤】
备注：新客户发好一点`

const fresh2 = `11-9
店名：中山西路店
手机：13900139000
地址：季红市夏安区南京北路2号
【香水柠檬5斤】
备注：请送到门店`

const booking1 = `预约单
客户：李女士
联系方式：13700137000
服务项目：上门保洁
预约日期：11-12
时长：3小时
备注：有宠物，请提前电话`

// memChatSource 内存聊天源，仅用于测试。
type memChatSource struct{ messages []ports.ChatMessage }

func (m *memChatSource) Name() string { return "mem" }
func (m *memChatSource) Export(_ context.Context, _ ports.ExportOptions) (*ports.ExportResult, error) {
	return &ports.ExportResult{Messages: m.messages}, nil
}

// fakeProvider 返回固定文本的桩 provider，用于测试 AI 注入路径（不联网）。
type fakeProvider struct{ text string }

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Complete(_ context.Context, _ ports.CompletionRequest) (*ports.CompletionResponse, error) {
	return &ports.CompletionResponse{Text: f.text}, nil
}

// TestEngine_Run_EndToEnd 验证「导出→自动路由→抽取→入库」全链路，且不依赖任何外部服务。
func TestEngine_Run_EndToEnd(t *testing.T) {
	store := orderschema.NewMemoryStore()
	chat := &memChatSource{messages: []ports.ChatMessage{
		{ID: "m1", Sender: "A", Content: fresh1, Time: "2024-11-09T10:00:00Z"},
		{ID: "m2", Sender: "B", Content: booking1, Time: "2024-11-09T11:00:00Z"},
	}}
	e, err := NewEngine(chat, &fakeProvider{}, store, kernel.NoopLogger{}, 0.35)
	if err != nil {
		t.Fatal(err)
	}

	// 用启发式（不联网）从样例归纳生鲜模板并保存为已发布模板。
	tpl, _, err := orderschema.InferTemplate([]string{fresh1, fresh2}, orderschema.InferOptions{
		ID: "fresh", Name: "生鲜团购",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orderschema.Publish(tpl); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTemplate(context.Background(), tpl); err != nil {
		t.Fatal(err)
	}

	rep, err := e.Run(context.Background(), ports.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Orders) != 1 {
		t.Fatalf("期望匹配 1 笔订单，实际 %d", len(rep.Orders))
	}
	if len(rep.Unmatched) != 1 {
		t.Fatalf("期望 1 条未匹配（预约单），实际 %d", len(rep.Unmatched))
	}
	o := rep.Orders[0]
	if o.TemplateID != "fresh" {
		t.Fatalf("模板 ID 不匹配: %s", o.TemplateID)
	}
	// 镜像列应被自动同步（订单表固定列可被索引/去重/查询）。
	if o.RecipientName == nil || *o.RecipientName != "华夏农业大学店" {
		t.Fatalf("镜像列 recipient 未同步: %v", o.RecipientName)
	}
	if o.Phone == nil || *o.Phone != "13800138000" {
		t.Fatalf("镜像列 phone 未同步: %v", o.Phone)
	}
	if _, err := store.GetOrder(context.Background(), o.ID); err != nil {
		t.Fatalf("订单未落库: %v", err)
	}

	// 同一条消息（相同 SourceMsgID）重跑应被去重，不再产生新订单。
	rep2, err := e.Run(context.Background(), ports.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep2.Orders) != 0 {
		t.Fatalf("期望去重后 0 笔新订单，实际 %d", len(rep2.Orders))
	}
}

// TestEngine_InferTemplate_WithAI 验证 AI provider 能注入进模板归纳流程，且失败会降级。
func TestEngine_InferTemplate_WithAI(t *testing.T) {
	store := orderschema.NewMemoryStore()
	e, err := NewEngine(&memChatSource{}, &fakeProvider{text: `{"fields":[{"label":"客户","type":"string","frequency":0.9,"labels":["客户"],"aliases":["客户"]}]}`}, store, kernel.NoopLogger{}, 0.35)
	if err != nil {
		t.Fatal(err)
	}
	tpl, rep, err := e.InferTemplate(context.Background(), []string{fresh1}, orderschema.InferOptions{
		ID: "ai1", Name: "AI模板",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tpl == nil || tpl.ID != "ai1" {
		t.Fatalf("模板未生成: %v", tpl)
	}
	if rep == nil || len(rep.Proposals) == 0 {
		t.Fatalf("归纳报告为空，AI 注入未生效")
	}
}

// TestEngine_RequiresDeps 验证缺依赖时拒绝构造，避免运行时空指针。
func TestEngine_RequiresDeps(t *testing.T) {
	store := orderschema.NewMemoryStore()
	if _, err := NewEngine(nil, &fakeProvider{}, store, nil, 0.35); err == nil {
		t.Fatal("chat 为 nil 时应报错")
	}
	if _, err := NewEngine(&memChatSource{}, nil, store, nil, 0.35); err == nil {
		t.Fatal("ai 为 nil 时应报错")
	}
	if _, err := NewEngine(&memChatSource{}, &fakeProvider{}, nil, nil, 0.35); err == nil {
		t.Fatal("store 为 nil 时应报错")
	}
}
