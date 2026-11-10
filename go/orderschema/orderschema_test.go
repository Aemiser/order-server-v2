package orderschema

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
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

func newFreshTemplate(t *testing.T) (*Template, *InferReport) {
	t.Helper()
	tpl, report, err := InferTemplate([]string{fresh1, fresh2}, InferOptions{
		ID: "tpl_fresh", Name: "生鲜团购",
	})
	if err != nil {
		t.Fatalf("InferTemplate: %v", err)
	}
	return tpl, report
}

func TestCanonicalAndCustomKey(t *testing.T) {
	cases := map[string]string{
		"联系电话":  "phone",
		"收货地址：": "address",
		"TEL":   "phone",
		"店名":    "recipient",
		"特殊要求":  "remarks",
	}
	for in, want := range cases {
		got, ok := CanonicalKey(in)
		if !ok || got != want {
			t.Errorf("CanonicalKey(%q) = %q,%v, want %q", in, got, ok, want)
		}
	}
	if k1, k2 := CustomKey("服务项目"), CustomKey("服务项目"); k1 != k2 || !strings.HasPrefix(k1, "f_") {
		t.Errorf("CustomKey 不稳定或格式不对: %q / %q", k1, k2)
	}
	if CustomKey("") != "" {
		t.Error("空标签不应生成 key")
	}
}

func TestMergeProposedDoesNotAddAliasFields(t *testing.T) {
	base := BuiltinFields()
	proposed := FieldDefs{{
		Key: "f_should_not_exist", Label: "联系电话", Type: FieldString,
		Extract: ExtractRule{Kind: ExtractLabel, Labels: []string{"联系电话"}},
	}}
	merged, proposals := MergeProposed(base, proposed)
	if len(merged) != len(base) {
		t.Fatalf("别名收敛不应新增字段: %d -> %d", len(base), len(merged))
	}
	if len(proposals) != 1 || proposals[0].New || proposals[0].MergedInto != "phone" {
		t.Fatalf("期望收敛到 phone, got %+v", proposals)
	}
	phone, _ := FindField(merged, "phone")
	found := false
	for _, a := range phone.Aliases {
		if a == "联系电话" {
			found = true
		}
	}
	if !found {
		t.Error("收敛后应把别名并入目标字段")
	}
}

func TestInferTemplateFresh(t *testing.T) {
	tpl, report := newFreshTemplate(t)
	if tpl.Version != 1 || tpl.Status != TemplateDraft || tpl.SampleCount != 2 {
		t.Fatalf("模板头部不对: %+v", tpl)
	}
	for _, key := range []string{"recipient", "phone", "address", "remarks"} {
		f, ok := FindField(tpl.Fields, key)
		if !ok {
			t.Fatalf("缺少内置字段 %s", key)
		}
		if !f.Required {
			t.Errorf("%s 在全部样例出现，应设为必填", key)
		}
	}
	if !tpl.Items.Enabled {
		t.Error("出现【】规格块，应启用订单行")
	}
	if len(tpl.Signatures) == 0 {
		t.Error("应生成路由签名")
	}
	if len(report.Coverage) != 2 {
		t.Errorf("覆盖报告应有 2 条, got %d", len(report.Coverage))
	}
	for _, p := range report.Proposals {
		if p.New {
			t.Errorf("内置格式不应产生待确认新字段: %+v", p.Field)
		}
	}
}

func TestInferBookingNeedsManualConfirmation(t *testing.T) {
	tpl, report, err := InferTemplate([]string{booking1, booking2}, InferOptions{
		ID: "tpl_booking", Name: "上门服务预约",
	})
	if err != nil {
		t.Fatalf("InferTemplate: %v", err)
	}
	// 预约单没有地址：内置字段保留但不应设为必填。
	addr, _ := FindField(tpl.Fields, "address")
	if addr.Required {
		t.Error("样例里没有地址，address 不该是必填")
	}
	newKeys := []string{}
	for _, p := range report.Proposals {
		if p.New {
			newKeys = append(newKeys, p.Field.Key)
		}
	}
	if len(newKeys) < 2 {
		t.Fatalf("应至少有两个自定义字段提议, got %v", newKeys)
	}
	for _, k := range newKeys {
		if _, ok := FindField(tpl.Fields, k); ok {
			t.Errorf("未确认前不应写入自定义字段 %s", k)
		}
	}
	if n := AcceptProposals(tpl, report.Proposals, newKeys); n != len(newKeys) {
		t.Fatalf("确认后应加入 %d 个字段, got %d", len(newKeys), n)
	}
	for _, k := range newKeys {
		if _, ok := FindField(tpl.Fields, k); !ok {
			t.Errorf("确认后应存在字段 %s", k)
		}
	}
	if err := Publish(tpl); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if tpl.Status != TemplatePublished || tpl.PublishedAt == nil {
		t.Error("发布状态不对")
	}
	if err := Publish(tpl); err == nil {
		t.Error("已发布版本不应允许再次发布/修改")
	}
	next, err := CloneForEdit(tpl)
	if err != nil || next.Version != 2 || next.Status != TemplateDraft {
		t.Fatalf("CloneForEdit: v=%d status=%s err=%v", next.Version, next.Status, err)
	}
	if tpl.Version != 1 {
		t.Error("派生新版本不应改动老版本")
	}
}

func TestReplayExtractsFieldsAndItems(t *testing.T) {
	tpl, report := newFreshTemplate(t)
	keys := []string{}
	for _, p := range report.Proposals {
		if p.New {
			keys = append(keys, p.Field.Key)
		}
	}
	AcceptProposals(tpl, report.Proposals, keys)
	rows := Replay(tpl, []string{fresh1})
	if len(rows) != 1 {
		t.Fatalf("回放应有 1 行, got %d", len(rows))
	}
	row := rows[0]
	if got := row.Fields["recipient"].Value; got != "华夏农业大学店" {
		t.Errorf("收件人抽取错误: %v", got)
	}
	if got := row.Fields["phone"].Value; got != "13800138000" {
		t.Errorf("手机号抽取错误: %v", got)
	}
	if len(row.Fields["address"].Raw) == 0 {
		t.Error("地址未抽到")
	}
	if len(row.Items) != 2 {
		t.Fatalf("应抽到 2 行明细, got %d: %+v", len(row.Items), row.Items)
	}
	if row.Items[0].Name != "广香" || row.Items[0].Qty.Value != 5 || row.Items[0].Qty.Unit != "斤" {
		t.Errorf("明细解析错误: %+v", row.Items[0])
	}
	if len(row.MissingRequired) != 0 {
		t.Errorf("完整样例不应缺必填字段: %v", row.MissingRequired)
	}
}

func TestValidateOrder(t *testing.T) {
	tpl, _ := newFreshTemplate(t)
	if err := Publish(tpl); err != nil {
		t.Fatal(err)
	}
	o := &Order{
		TemplateID: tpl.ID, TemplateVersion: tpl.Version,
		Fields: FieldValues{
			"recipient": {Value: "小王"},
			"phone":     {Value: "13800138000"},
			"remarks":   {Value: ""},
		},
	}
	res := Validate(tpl, o)
	if res.OK {
		t.Error("缺 address / remarks 应校验失败")
	}
	o.Fields["address"] = FieldValue{Value: "大兵市和平区1号"}
	o.Fields["remarks"] = FieldValue{Value: "加急"}
	o.Fields["f_extra"] = FieldValue{Value: "历史字段"}
	res = Validate(tpl, o)
	if !res.OK {
		t.Errorf("补齐后应通过: %+v", res.Issues)
	}
	if len(res.UnknownFields) != 1 || res.UnknownFields[0] != "f_extra" {
		t.Errorf("未知字段应被单独记录: %v", res.UnknownFields)
	}
	o.Fields["phone"] = FieldValue{Value: "123"}
	res = Validate(tpl, o)
	if !res.OK {
		t.Error("手机号格式问题只应是 warning，不阻断入库")
	}
	foundWarning := false
	for _, issue := range res.Issues {
		if issue.Level == "warning" && issue.Field == "phone" {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Error("应产生手机号格式 warning")
	}
}

func TestSyncMirrorsAndDedupHash(t *testing.T) {
	tpl, _ := newFreshTemplate(t)
	o := &Order{
		TenantID: "tenant_a", TemplateID: tpl.ID, TemplateVersion: tpl.Version,
		Fields: FieldValues{
			"recipient": {Value: "中山西路店"},
			"phone":     {Value: "13900139000"},
			"address":   {Value: "季红市夏安区南京北路2号"},
			"remarks":   {Value: "请送到门店"},
		},
	}
	SyncMirrors(tpl, o)
	if o.RecipientName == nil || *o.RecipientName != "中山西路店" {
		t.Error("收件人镜像列未同步")
	}
	if o.Address == nil {
		t.Error("地址镜像列未同步")
	}
	for i := range tpl.Fields {
		if tpl.Fields[i].Key == "address" {
			tpl.Fields[i].Deprecated = true
		}
	}
	SyncMirrors(tpl, o)
	if o.Address != nil {
		t.Error("字段被停用后镜像列应清空")
	}

	h1 := ComputeDedupHash(o)
	if h1 == "" || h1 != ComputeDedupHash(o) {
		t.Error("dedup hash 必须稳定")
	}
	other := cloneOrder(o)
	other.SourceMsgID = "msg-2"
	if ComputeDedupHash(other) == h1 {
		t.Error("不同来源消息应有不同 hash")
	}
}

func TestConvertMeasure(t *testing.T) {
	kg, err := ConvertMeasure(Measure{Value: 5, Unit: "斤"}, "kg")
	if err != nil || kg.Value != 2.5 || kg.Unit != "kg" {
		t.Errorf("5斤 -> 2.5kg 失败: %+v %v", kg, err)
	}
	if _, err := ConvertMeasure(Measure{Value: 2, Unit: "件"}, "kg"); err == nil {
		t.Error("计数单位不应能与重量互换")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	fv := FieldValues{"recipient": {Value: "小王", Source: "label", Confidence: 0.98}}
	raw, err := fv.Value()
	if err != nil {
		t.Fatal(err)
	}
	var back FieldValues
	if err := back.Scan(raw); err != nil {
		t.Fatal(err)
	}
	if back["recipient"].Value != "小王" {
		t.Errorf("FieldValues 往返失败: %+v", back)
	}
	items := LineItems{{Name: "广香", Qty: Measure{Value: 5, Unit: "斤"}}}
	raw, err = items.Value()
	if err != nil {
		t.Fatal(err)
	}
	var itemsBack LineItems
	if err := itemsBack.Scan(raw); err != nil {
		t.Fatal(err)
	}
	if len(itemsBack) != 1 || itemsBack[0].Qty.Value != 5 {
		t.Errorf("LineItems 往返失败: %+v", itemsBack)
	}
	if err := itemsBack.Scan(nil); err != nil || len(itemsBack) != 0 {
		t.Errorf("NULL 扫描应得空切片, got %+v %v", itemsBack, err)
	}
}

func TestMemoryStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	tpl, report := newFreshTemplate(t)
	keys := []string{}
	for _, p := range report.Proposals {
		if p.New {
			keys = append(keys, p.Field.Key)
		}
	}
	AcceptProposals(tpl, report.Proposals, keys)
	if err := store.SaveTemplate(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	o := &Order{
		TenantID: "tenant_a", TemplateID: tpl.ID, TemplateVersion: tpl.Version,
		SourceMsgID: "msg-1", RawText: fresh1,
		Fields: FieldValues{
			"recipient": {Value: "华夏农业大学店"},
			"phone":     {Value: "13800138000"},
			"address":   {Value: "华夏省大兵市下区文化路1号"},
			"remarks":   {Value: "新客户发好一点"},
		},
		Items: LineItems{{Name: "广香", Qty: Measure{Value: 5, Unit: "斤"}}},
	}
	if err := store.SaveOrder(ctx, o); err != nil {
		t.Fatalf("SaveOrder: %v", err)
	}
	if o.ID == 0 || o.DedupHash == "" || o.Status != OrderPendingReview {
		t.Errorf("入库应补齐 ID/hash/status: %+v", o)
	}
	got, err := store.GetOrder(ctx, o.ID)
	if err != nil || got.RecipientName == nil {
		t.Fatalf("GetOrder/镜像列: %+v %v", got, err)
	}
	list, err := store.FindOrders(ctx, OrderQuery{TemplateID: tpl.ID})
	if err != nil || len(list) != 1 {
		t.Fatalf("FindOrders: %d %v", len(list), err)
	}
	dup := cloneOrder(o)
	dup.ID = 0
	if err := store.SaveOrder(ctx, dup); !errors.Is(err, ErrDuplicateOrder) {
		t.Errorf("重复 dedup_hash 应被拒绝, got %v", err)
	}
	if err := Publish(tpl); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTemplate(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTemplate(ctx, tpl); !errors.Is(err, ErrImmutableVersion) {
		t.Errorf("已发布版本应不可覆盖, got %v", err)
	}
	next, _ := CloneForEdit(tpl)
	if err := store.SaveTemplate(ctx, next); err != nil {
		t.Fatalf("新版本应可保存: %v", err)
	}
	latest, err := store.LatestTemplate(ctx, tpl.ID)
	if err != nil || latest.Version != 2 {
		t.Errorf("LatestTemplate 应返回 v2: %+v %v", latest, err)
	}
}

func TestRoutePrefersMatchingTemplate(t *testing.T) {
	fresh, _ := newFreshTemplate(t)
	booking, report, err := InferTemplate([]string{booking1, booking2}, InferOptions{
		ID: "tpl_booking", Name: "上门服务预约",
	})
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, p := range report.Proposals {
		if p.New {
			keys = append(keys, p.Field.Key)
		}
	}
	AcceptProposals(booking, report.Proposals, keys)
	_ = Publish(fresh)
	_ = Publish(booking)

	incoming := `11-9
店名：和平路店
手机：13600136000
地址：大兵市和平区太原街3号
【广香10斤】
备注：加急`
	best, ok := RouteBest(incoming, []*Template{fresh, booking}, 0.35)
	if !ok || best.TemplateID != "tpl_fresh" {
		t.Fatalf("应路由到生鲜模板, got %+v ok=%v", best, ok)
	}
	if best.Score <= 0.5 {
		t.Errorf("命中分数过低: %v", best.Score)
	}
	if matches := Route("今天天气不错", []*Template{fresh, booking}, 0.35); len(matches) != 0 {
		t.Errorf("无关文本不应命中任何模板: %+v", matches)
	}
}

func TestLLMProposerMergesWithHeuristic(t *testing.T) {
	llm := LLMProposer{Complete: func(_ context.Context, _, _ string) (string, error) {
		return "```json\n" + `{"fields":[
			{"label":"配送时段","type":"string","frequency":0.8,"labels":["配送时段"]},
			{"label":"联系电话","type":"phone","frequency":1.0,"labels":["联系电话"]}
		]}` + "\n```", nil
	}}
	got, err := llm.Propose(context.Background(), []string{fresh1, fresh2}, BuiltinFields())
	if err != nil {
		t.Fatal(err)
	}
	foundHeuristic, foundLLM, phoneConverged := false, false, false
	phoneCount := 0
	for _, p := range got {
		if p.Def.Label == "店名" {
			foundHeuristic = true
		}
		if p.Def.Label == "配送时段" {
			foundLLM = true
			if p.Frequency != 0.8 {
				t.Errorf("频率未保留: %v", p.Frequency)
			}
		}
		if p.Def.Key == "phone" {
			phoneCount++
			for _, l := range p.Def.Extract.Labels {
				if l == "联系电话" {
					phoneConverged = true
				}
			}
		}
	}
	if !foundHeuristic || !foundLLM || !phoneConverged || phoneCount != 1 {
		t.Errorf("合并/收敛不符合预期: heuristic=%v llm=%v phoneAlias=%v phoneCount=%d %+v",
			foundHeuristic, foundLLM, phoneConverged, phoneCount, got)
	}
}

func TestLLMProposerDegradesOnFailure(t *testing.T) {
	heuristic, err := (HeuristicProposer{}).Propose(context.Background(), []string{fresh1, fresh2}, BuiltinFields())
	if err != nil {
		t.Fatal(err)
	}
	cases := []LLMProposer{
		{Complete: func(_ context.Context, _, _ string) (string, error) { return "模型拒绝回答", nil }},
		{Complete: func(_ context.Context, _, _ string) (string, error) { return "", fmt.Errorf("timeout") }},
		{}, // 未注入 LLM
	}
	for i, p := range cases {
		got, err := p.Propose(context.Background(), []string{fresh1, fresh2}, BuiltinFields())
		if err != nil {
			t.Errorf("case %d 降级不应报错: %v", i, err)
		}
		if len(got) != len(heuristic) {
			t.Errorf("case %d 应降级为启发式结果 %d 项, got %d", i, len(heuristic), len(got))
		}
	}
}

func TestLLMProposerEndToEndInfer(t *testing.T) {
	llm := LLMProposer{Complete: func(_ context.Context, _, _ string) (string, error) {
		return `{"fields":[{"label":"包装方式","type":"enum","frequency":1.0,"labels":["包装"],"options":["礼盒","散装"]}]}`, nil
	}}
	tpl, report, err := InferTemplate([]string{fresh1, fresh2}, InferOptions{
		ID: "tpl_llm", Name: "LLM 归纳", Proposer: llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	before := len(tpl.Fields)
	keys := []string{}
	for _, p := range report.Proposals {
		if p.New && p.Field.Label == "包装方式" {
			keys = append(keys, p.Field.Key)
		}
	}
	if len(keys) != 1 {
		t.Fatalf("应有一个「包装方式」提议: %+v", report.Proposals)
	}
	if n := AcceptProposals(tpl, report.Proposals, keys); n != 1 || len(tpl.Fields) != before+1 {
		t.Fatalf("确认后应恰好新增 1 个字段: n=%d before=%d after=%d", n, before, len(tpl.Fields))
	}
	if _, ok := FindField(tpl.Fields, keys[0]); !ok {
		t.Fatal("确认后字段应出现在模板里")
	}
	if err := Publish(tpl); err != nil {
		t.Fatalf("枚举字段发布失败: %v", err)
	}
}
