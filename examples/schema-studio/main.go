// Command schema-studio 是一个**最小可运行示例**，演示「第三方如何在不修改任何现有文件的前提下」
// 给本后端扩展新的能力：
//
//  1. 新增一个数据源（ChatSource）—— 这里的 demoSource 用内存里的样例消息，真实场景换成微信/飞书/CSV 即可；
//  2. 新增一个 AI 提供商（CompletionProvider）—— 这里的 fakeProvider 返回固定 JSON，真实场景换成 DeepSeek/Qwen；
//  3. 用 orderschema 的内存 Store 存模板与订单；
//  4. 用 compose.Engine 把「导出 → 自动路由 → 入库」编排起来跑通。
//
// 运行（默认构建，不链入任何微信代码）：
//
//	go run ./examples/schema-studio
//
// 这个示例不依赖数据库、不依赖网络、不依赖 Wails，纯后端逻辑。
package main

import (
	"context"
	"fmt"

	"order_server/go/compose"
	"order_server/go/kernel"
	"order_server/go/orderschema"
	"order_server/go/ports"
)

// ---------------------------------------------------------------------------
// 1) 新增一个数据源：实现 ports.ChatSource 即可，不碰现有文件。
// ---------------------------------------------------------------------------

type demoSource struct{}

func (demoSource) Name() string { return "demo" }

func (demoSource) Export(_ context.Context, _ ports.ExportOptions) (*ports.ExportResult, error) {
	const t = "2026-11-10T09:00:00+08:00"
	msgs := []ports.ChatMessage{
		{
			ID:      "m1",
			Sender:  "张三",
			Content: "11-9\n店名：华夏农业大学店\n手机：13800138000\n地址：华夏省大兵市下区文化路1号\n【广香5斤】,【黄柠檬3斤】",
			Time:    t,
		},
		{
			ID:      "m2",
			Sender:  "李四",
			Content: "店名：示例广场店\n电话：13900139000\n地址：华夏省大兵市示例区广场路5栋\n【香水柠檬5斤】",
			Time:    t,
		},
		{
			// 这不是订单（没有「店名/地址/【」等模板特征），应进入人工审查。
			ID:      "m3",
			Sender:  "王五",
			Content: "今天天气不错，记得帮我看看物流到哪了",
			Time:    t,
		},
	}
	return &ports.ExportResult{Messages: msgs}, nil
}

// ---------------------------------------------------------------------------
// 2) 新增一个 AI 提供商：实现 ports.CompletionProvider 即可。
//    真实场景在这里调 DeepSeek / Qwen / 本地模型；重试/超时/限流都在这一层。
// ---------------------------------------------------------------------------

type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }

func (fakeProvider) Complete(_ context.Context, _ ports.CompletionRequest) (*ports.CompletionResponse, error) {
	// 返回符合 orderschema.LLMProposer 约定的 JSON（即使格式不对也会优雅降级为启发式）。
	json := `{"fields":[{"label":"店名","key":"shop","type":"string","frequency":1.0,` +
		`"aliases":[],"labels":["店名"],"regex":"","unit":"","options":[]}]}`
	return &ports.CompletionResponse{Text: json, TokensIn: 10, TokensOut: 20}, nil
}

// ---------------------------------------------------------------------------
// 3) 主流程：定义模板 → 编排引擎 → 跑通识别。
// ---------------------------------------------------------------------------

func main() {
	ctx := context.Background()
	store := orderschema.NewMemoryStore()

	// 由集成方手工定义一份「生鲜团购」模板：发布态 + 路由签名（关键词命中即匹配）。
	tpl := &orderschema.Template{
		ID:          "demo-fresh",
		Name:        "生鲜团购订单",
		Version:     1,
		Status:      orderschema.TemplatePublished,
		Signatures:  orderschema.Signatures{{Keywords: []string{"店名", "地址", "【"}}},
		Fields:      orderschema.FieldDefs{},
		SampleCount: 2,
	}
	if err := store.SaveTemplate(ctx, tpl); err != nil {
		panic(err)
	}

	// 把「新数据源 + 新 AI + 内存存储」注入编排引擎。三者缺一都会报错（识别链路不可替代）。
	engine, err := compose.NewEngine(demoSource{}, fakeProvider{}, store, kernel.NoopLogger{}, 0.35)
	if err != nil {
		panic(err)
	}

	// 运行完整流水线：导出 → 自动路由 → 入库。
	report, err := engine.Run(ctx, ports.ExportOptions{})
	if err != nil {
		panic(err)
	}

	fmt.Printf("✅ 识别入库订单：%d 笔\n", len(report.Orders))
	for _, o := range report.Orders {
		fmt.Printf("   - 消息 %s | 模板 %s | 原文: %q\n", o.SourceMsgID, o.TemplateID, o.RawText)
	}
	fmt.Printf("⏸  需人工审查（未命中模板）：%d 条\n", len(report.Unmatched))
	for _, m := range report.Unmatched {
		fmt.Printf("   - 消息 %s: %q\n", m.ID, m.Content)
	}

	// 演示 AI 注入路径：用 fakeProvider 从样例归纳模板草案（失败会自动降级为启发式，不中断）。
	samples := []string{
		"店名：样例店\n手机：13800138000\n地址：某路1号\n【苹果5斤】",
		"店名：另一店\n电话：13900139000\n地址：某路2号\n【橙子3斤】",
	}
	inferred, rep, err := engine.InferTemplate(ctx, samples, orderschema.InferOptions{
		ID:   "inferred-demo",
		Name: "AI 归纳模板",
	})
	if err != nil {
		fmt.Printf("⚠️  AI 归纳返回错误（已降级）: %v\n", err)
	} else {
		fmt.Printf("\n🤖 AI/启发式归纳出模板 %q：%d 个字段候选，%d 条签名关键词\n",
			inferred.Name, len(rep.Proposals), len(inferred.Signatures))
	}
}
