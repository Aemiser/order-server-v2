package wechatorder

import (
	"strings"
	"testing"

	"order_server/go/global"
	"order_server/go/models"
)

func actionMsg(id, sender, content, quote string, ts float64) ChatMessage {
	return ChatMessage{
		ID: id, Sender: sender, Content: content, Quote: quote,
		Time: "2026-11-09 09:00:00", Date: "2026-11-09", Ts: ts,
	}
}

func actionOrder(store string, weight int) models.AddressInfo {
	return models.AddressInfo{
		Recipient:    store,
		Phone:        "13800138000",
		Address:      "华夏省大兵市寿光区万达广场店",
		ProductSpecs: models.ProductSpecsJSON{{Name: "广香", Weight: weight}},
	}
}

func actionSplit(addr models.AddressInfo, msg ChatMessage) *classifyResult {
	return &classifyResult{
		Preview:       []models.AddressInfo{addr},
		PreviewSource: []ChatMessage{msg},
	}
}

func resetActionState() {
	global.AddressList = nil
	replaceReviewRecords(nil)
}

// 场景一：客户引用订单消息回复「这单不要了」→ 引用原文可唯一定位，订单移入人工审查区，
// 「忽略」操作应恢复原始订单，绝不静默丢单。
func TestApplyActionQuoteDeleteAndUndo(t *testing.T) {
	resetActionState()
	defer resetActionState()

	orderText := "11-9\n店名：寿光示例广场店\n电话：13800138000\n地址：华夏省大兵市寿光区万达广场店\n【广香5斤】"
	orderMsg := actionMsg("m1", "帅涛", orderText, "", 1000)
	split := actionSplit(actionOrder("寿光示例广场店", 5), orderMsg)

	hit := ActionHit{
		Message: actionMsg("m2", "帅涛", "这单不要了", orderText, 2000),
		Action: OrderAction{
			IsAction: true, Action: "delete", Confidence: "high", Reason: "客户明确取消",
			Target: ActionTarget{QuotePresent: true, SameSender: true},
		},
	}
	report := applyActions(split, []ActionHit{hit}, []ChatMessage{orderMsg}, "2026-11-09")

	if report.Applied != 1 || report.Actions != 1 {
		t.Fatalf("应定位并应用 1 条删除操作，实际 report=%+v", report)
	}
	if len(split.Preview) != 0 {
		t.Fatalf("被删除的订单不应留在数据预览区，实际 %d 笔", len(split.Preview))
	}
	if len(split.Review) != 1 {
		t.Fatalf("应生成 1 条订单操作审查项，实际 %d 条", len(split.Review))
	}
	rr := split.Review[0]
	if rr.Public.Kind != "action" || rr.Public.ActionType != "delete" || rr.Public.MatchBy != "引用原文精确定位" {
		t.Fatalf("审查项元数据不对: %+v", rr.Public)
	}
	if len(rr.Raw) != 0 {
		t.Fatalf("删除操作的「加入预览区」不应写入订单，实际 %d 笔", len(rr.Raw))
	}
	if len(rr.Fallback) != 1 || len(rr.Public.Before) != 1 {
		t.Fatal("应保留原始订单供撤销 / 对照")
	}
	if !strings.Contains(rr.Public.RawContent, orderText) {
		t.Fatal("审查项应展示被引用的订单原文")
	}

	replaceReviewOrdersForTest(split.Review)
	global.AddressList = nil
	left := DiscardReviewOrders([]string{rr.Public.ID})
	if len(left) != 0 {
		t.Fatalf("忽略后不应还有待审查项，实际 %d 条", len(left))
	}
	if len(global.AddressList) != 1 || global.AddressList[0].Recipient != "寿光示例广场店" {
		t.Fatalf("撤销删除应把原始订单放回数据预览区，实际 %+v", global.AddressList)
	}
}

// 场景二：没引用、直接说「寿光示例广场店，加五斤」→ 靠门店关键词 + 发送人 + 时间先后定位，
// 加量落到规格上，原始 / 结果对照进人工审查区，人工确认后才采用。
func TestApplyActionKeywordAddJin(t *testing.T) {
	resetActionState()
	defer resetActionState()

	orderMsg := actionMsg("m1", "帅涛", "11-9 店名：寿光示例广场店 13800138000 【广香5斤】", "", 1000)
	split := actionSplit(actionOrder("寿光示例广场店", 5), orderMsg)
	hit := ActionHit{
		Message: actionMsg("m2", "帅涛", "寿光示例广场店，加五斤", "", 2000),
		Action: OrderAction{
			IsAction: true, Action: "modify", Confidence: "high", Reason: "门店加量",
			Target:  ActionTarget{Keywords: []string{"寿光示例广场店"}, DayScope: "today", SameSender: true},
			Changes: ActionChanges{AddSpecs: []models.ProductSpec{{Weight: 5}}},
		},
	}
	report := applyActions(split, []ActionHit{hit}, []ChatMessage{orderMsg}, "2026-11-09")
	if report.Applied != 1 {
		t.Fatalf("应自动定位并应用加量，实际 report=%+v", report)
	}
	if len(split.Preview) != 0 || len(split.Review) != 1 {
		t.Fatalf("修改后的订单应进入人工审查区，preview=%d review=%d", len(split.Preview), len(split.Review))
	}
	rr := split.Review[0]
	if len(rr.Raw) != 1 || rr.Raw[0].ProductSpecs[0].Weight != 10 {
		t.Fatalf("5 斤应加成 10 斤，实际 %+v", rr.Raw)
	}
	if len(rr.Fallback) != 1 || rr.Fallback[0].ProductSpecs[0].Weight != 5 {
		t.Fatal("撤销用的原始订单应保持 5 斤")
	}
	if len(rr.Public.Before) != 1 || len(rr.Public.After) != 1 {
		t.Fatal("应同时给出修改前后对照")
	}
	if !strings.Contains(rr.Public.Before[0].ProductSpecs, "5") ||
		!strings.Contains(rr.Public.After[0].ProductSpecs, "10") {
		t.Fatalf("前后对照内容不对: before=%v after=%v", rr.Public.Before[0], rr.Public.After[0])
	}

	replaceReviewOrdersForTest(split.Review)
	global.AddressList = nil
	latest := PromoteReviewOrders([]string{rr.Public.ID})
	if len(latest) != 1 || global.AddressList[0].ProductSpecs[0].Weight != 10 {
		t.Fatalf("人工采用修改后，预览区应是 10 斤，实际 %+v", global.AddressList)
	}
}

// 场景三：多笔同人订单、无引用、没有门店关键词的「这单不要了」→ 必须交人工，绝不猜单。
func TestApplyActionAmbiguousDoesNotTouchOrders(t *testing.T) {
	resetActionState()
	defer resetActionState()

	m1 := actionMsg("m1", "帅涛", "11-9 店名：寿光示例广场店 【广香5斤】", "", 1000)
	m2 := actionMsg("m2", "帅涛", "11-9 店名：寿光示例广场店 【黄柠檬3斤】", "", 1500)
	split := &classifyResult{
		Preview:       []models.AddressInfo{actionOrder("寿光示例广场店", 5), actionOrder("寿光示例广场店", 3)},
		PreviewSource: []ChatMessage{m1, m2},
	}
	hit := ActionHit{
		Message: actionMsg("m3", "帅涛", "这单不要了", "", 2000),
		Action: OrderAction{
			IsAction: true, Action: "delete", Confidence: "medium", Reason: "指代不明",
			Target: ActionTarget{SameSender: true},
		},
	}
	report := applyActions(split, []ActionHit{hit}, []ChatMessage{m1, m2}, "2026-11-09")
	if len(split.Preview) != 2 {
		t.Fatalf("歧义时不得改动任何订单，实际预览区 %d 笔", len(split.Preview))
	}
	if report.Manual != 1 || len(split.Review) != 1 {
		t.Fatalf("应生成 1 条待人工定位的操作项，report=%+v review=%d", report, len(split.Review))
	}
	if split.Review[0].Public.ActionStatus == "applied" {
		t.Fatal("歧义操作不能被标记为已应用")
	}
	if len(split.Review[0].Public.Candidates) == 0 {
		t.Fatal("应列出候选订单供人工判断")
	}
}

// 中文数字兜底：「加五斤」在模型漏填时也要能补出 5 斤。
func TestFillActionChangesFromChineseNumber(t *testing.T) {
	act := OrderAction{IsAction: true, Action: "modify"}
	fillActionChangesFromText("寿光示例广场店，加五斤", &act)
	if len(act.Changes.AddSpecs) != 1 || act.Changes.AddSpecs[0].Weight != 5 {
		t.Fatalf("应补出加 5 斤，实际 %+v", act.Changes.AddSpecs)
	}
	act2 := OrderAction{IsAction: true, Action: "modify"}
	fillActionChangesFromText("广香改成 8 斤", &act2)
	if len(act2.Changes.SetSpecs) != 1 || act2.Changes.SetSpecs[0].Weight != 8 {
		t.Fatalf("应补出改成 8 斤，实际 %+v", act2.Changes.SetSpecs)
	}
}

func TestPickActionMessages(t *testing.T) {
	orderText := "11-9 店名：寿光示例广场店 13800138000 华夏省大兵市寿光区万达广场店 【广香5斤】"
	messages := []ChatMessage{
		actionMsg("m1", "帅涛", orderText, "", 1000),
		actionMsg("m2", "帅涛", "这单不要了", orderText, 2000), // 引用 → 候选
		actionMsg("m3", "帅涛", "寿光示例广场店，加五斤", "", 3000),    // 关键词 → 候选
		actionMsg("m4", "帅涛", "今天天气不错", "", 4000),       // 闲聊 → 排除
		actionMsg("m5", "帅涛", orderText, "", 5000),      // 完整新订单 → 排除
	}
	got := pickActionMessages(messages, map[string]bool{"m1": true, "m5": true})
	ids := make([]string, 0, len(got))
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "m2,m3" {
		t.Fatalf("候选挑选不对: %v", ids)
	}
}

func TestParseActionResult(t *testing.T) {
	raw := "```json\n{\"is_action\":true,\"action\":\"modify\",\"confidence\":\"high\"," +
		"\"target\":{\"keywords\":[\"寿光示例广场店\"]},\"changes\":{\"add_specs\":[{\"name\":\"\",\"weight\":5}]}}\n```"
	act, err := parseActionResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !act.IsAction || act.Action != "modify" || len(act.Changes.AddSpecs) != 1 {
		t.Fatalf("解析结果不对: %+v", act)
	}
	none, err := parseActionResult(`{"is_action":false,"action":"none"}`)
	if err != nil || none.IsAction || none.Action != "none" {
		t.Fatalf("非操作应归一化: %+v err=%v", none, err)
	}
}

// replaceReviewOrdersForTest 走与线上一致的内存状态写入，避免测试互相污染。
func replaceReviewOrdersForTest(records []reviewRecord) {
	replaceReviewRecords(records)
}
