// Command schemademo 不依赖 Wails / 数据库 / 网络，离线跑一遍
// 「样例 → 模板 → 人工确认 → 回放 → 路由 → 落库」的地基闭环。
//
//	go run ./cmd/schemademo
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"order_server/go/orderschema"
)

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

const booking2 = `预约单
客户：王先生
联系方式：13600136000
服务项目：家电清洗
预约日期：11-13
时长：2小时
备注：周末上门`

func section(title string) {
	fmt.Printf("\n%s\n%s\n", title, strings.Repeat("=", 64))
}

func showTemplate(title string, t *orderschema.Template, report *orderschema.InferReport) []string {
	section(title)
	fmt.Printf("模板 %s  v%d  status=%s  样例=%d\n", t.ID, t.Version, t.Status, t.SampleCount)
	fmt.Println("字段定义（schema 真相）：")
	for _, f := range t.Fields {
		flag := "      "
		if f.Builtin {
			flag = "内置  "
		}
		if f.Required {
			flag += "必填"
		}
		fmt.Printf("  %-24s %-12s %-8s %s\n", f.Key, f.Label, f.Type, flag)
	}
	fmt.Printf("订单行 enabled=%v  默认单位=%q\n", t.Items.Enabled, t.Items.QuantityUnit)

	fmt.Println("别名收敛（不新增字段）：")
	if len(report.AliasMerges) == 0 {
		fmt.Println("  （无）")
	}
	for _, m := range report.AliasMerges {
		fmt.Printf("  %s → %s\n", m.From, m.To)
	}

	newKeys := []string{}
	fmt.Println("待人工确认的新字段提议（AI/启发式只提议，不落库）：")
	for _, p := range report.Proposals {
		if !p.New {
			continue
		}
		newKeys = append(newKeys, p.Field.Key)
		fmt.Printf("  %-24s %-12s %s\n", p.Field.Key, p.Field.Label, p.Field.Type)
	}
	if len(newKeys) == 0 {
		fmt.Println("  （无，全部收敛到内置字段）")
	}
	fmt.Printf("路由签名: keywords=%v separators=%v\n", t.Signatures[0].Keywords, t.Signatures[0].Separators)
	return newKeys
}

func confirmAndPublish(t *orderschema.Template, report *orderschema.InferReport, keys []string) {
	n := orderschema.AcceptProposals(t, report.Proposals, keys)
	if err := orderschema.Publish(t); err != nil {
		panic(err)
	}
	fmt.Printf("[%s] 人工确认加入 %d 个自定义字段，发布为 v%d\n", t.ID, n, t.Version)
}

func main() {
	ctx := context.Background()

	fresh, freshReport, err := orderschema.InferTemplate([]string{fresh1, fresh2}, orderschema.InferOptions{
		ID: "tpl_fresh_group_buy", Name: "生鲜团购配送",
	})
	if err != nil {
		panic(err)
	}
	freshKeys := showTemplate("① 用生鲜订单样例自动生成模板", fresh, freshReport)

	booking, bookingReport, err := orderschema.InferTemplate([]string{booking1, booking2}, orderschema.InferOptions{
		ID: "tpl_home_service", Name: "上门服务预约",
	})
	if err != nil {
		panic(err)
	}
	bookingKeys := showTemplate("② 用服务预约样例自动生成另一套模板", booking, bookingReport)

	section("③ 人工确认 → 发布（字段只能提议 + 确认，不允许自动落库）")
	confirmAndPublish(fresh, freshReport, freshKeys)
	confirmAndPublish(booking, bookingReport, bookingKeys)
	addr, _ := orderschema.FindField(booking.Fields, "address")
	fmt.Printf("预约模板的 address 必填=%v（样例里没有地址，保留字段但不强制）\n", addr.Required)

	section("④ 回放校验：把草案套回样例，先看抽成什么样再确认")
	for i, row := range orderschema.Replay(booking, []string{booking1}) {
		fmt.Printf("样例 %d 抽出字段：\n", i)
		for k, v := range row.Fields {
			fmt.Printf("  %-24s = %v  (source=%s)\n", k, v.Value, v.Source)
		}
		fmt.Printf("  明细: %+v\n", row.Items)
		fmt.Printf("  缺必填: %v\n", row.MissingRequired)
	}

	section("⑤ 新消息自动路由（不重跑格式归纳）")
	incoming := `11-9
店名：和平路店
手机：13600136000
地址：大兵市和平区太原街3号
【广香10斤】
备注：加急`
	for _, m := range orderschema.Route(incoming, []*orderschema.Template{fresh, booking}, 0.35) {
		fmt.Printf("  候选 %-24s v%d  score=%.2f  %v\n", m.TemplateID, m.Version, m.Score, m.Reasons)
	}
	best, ok := orderschema.RouteBest(incoming, []*orderschema.Template{fresh, booking}, 0.35)
	if !ok {
		panic("路由失败")
	}

	section("⑥ 按命中的模板落库：固定骨架 + JSON 载荷")
	store := orderschema.NewMemoryStore()
	if err := store.SaveTemplate(ctx, fresh); err != nil {
		panic(err)
	}
	if err := store.SaveTemplate(ctx, booking); err != nil {
		panic(err)
	}
	row := orderschema.Replay(fresh, []string{incoming})[0]
	o := &orderschema.Order{
		TenantID: "tenant_a", TemplateID: best.TemplateID, TemplateVersion: best.Version,
		Source: "wechat", SourceMsgID: "msg-20261109-001",
		RawText: incoming, OrderTime: time.Now(), Confidence: 0.98,
		Fields: orderschema.FieldValues(row.Fields),
		Items:  orderschema.LineItems(row.Items),
	}
	v := orderschema.Validate(fresh, o)
	fmt.Printf("校验: ok=%v issues=%v unknown=%v\n", v.OK, v.Issues, v.UnknownFields)
	if err := store.SaveOrder(ctx, o); err != nil {
		panic(err)
	}
	fmt.Printf("订单 id=%d template=%s/v%d dedup=%s…\n", o.ID, o.TemplateID, o.TemplateVersion, o.DedupHash[:12])
	fmt.Printf("镜像列（仅供索引/查询，可为空）: recipient=%v phone=%v address=%v\n",
		deref(o.RecipientName), deref(o.Phone), deref(o.Address))

	section("⑦ 模板演进：改字段只能产生新版本，老订单仍按 v1 渲染")
	next, err := orderschema.CloneForEdit(fresh)
	if err != nil {
		panic(err)
	}
	next.Fields = append(next.Fields, orderschema.FieldDef{
		Key: "f_delivery_slot", Label: "配送时段", Type: orderschema.FieldString,
		Extract: orderschema.ExtractRule{Kind: orderschema.ExtractLabel, Labels: []string{"配送时段"}},
		Sort:    90,
	})
	if err := orderschema.Publish(next); err != nil {
		panic(err)
	}
	if err := store.SaveTemplate(ctx, next); err != nil {
		panic(err)
	}
	stored, _ := store.GetOrder(ctx, o.ID)
	fmt.Printf("老订单仍指向 %s/v%d；模板最新已是 v%d\n", stored.TemplateID, stored.TemplateVersion, next.Version)
	if err := store.SaveTemplate(ctx, fresh); err != nil {
		fmt.Printf("覆盖已发布 v1 被拒绝: %v\n", err)
	}

	section("⑧ 单位换算（取代写死的「斤」）")
	kg, err := orderschema.ConvertMeasure(orderschema.Measure{Value: 5, Unit: "斤"}, "kg")
	if err != nil {
		panic(err)
	}
	fmt.Printf("5 斤 = %.2f %s\n", kg.Value, kg.Unit)
	if _, err := orderschema.ConvertMeasure(orderschema.Measure{Value: 2, Unit: "件"}, "kg"); err != nil {
		fmt.Printf("件 → kg 被明确拒绝: %v\n", err)
	}
}

func deref(p *string) string {
	if p == nil {
		return "<NULL>"
	}
	return *p
}
