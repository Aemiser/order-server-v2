package wechatorder

import (
	"errors"
	"strings"
	"testing"
	"time"

	"order_server/go/global"
	"order_server/go/models"
)

func testAddr(name string) models.AddressInfo {
	return models.AddressInfo{
		Recipient: name, Phone: "13800138000", Address: "测试路1号",
		ProductSpecs: models.ProductSpecsJSON{{Name: "广香", Weight: 5}},
	}
}

func testAnalyzeItem(id, aiDate, ruleDate string, isOrder, needsReview bool, conf string) AnalyzeItem {
	res := AIResult{
		IsOrder: isOrder, OrderDate: aiDate, DateConfirmed: aiDate != "",
		NeedsReview: needsReview, Confidence: conf, Reason: "测试判定",
	}
	if isOrder {
		res.Orders = []models.AddressInfo{testAddr(id)}
	}
	return AnalyzeItem{
		Candidate: Candidate{
			Message: ChatMessage{
				ID: id, Sender: "帅涛", Time: "2026-11-06 20:00:00", Date: "2026-11-06",
				Content: "店名：测试店\n电话：13800138000\n地址：测试路1号\n【广香5斤】",
			},
			OrderDate: ruleDate,
		},
		Result: res,
	}
}

func TestClassifyItemsSplitsByTargetDate(t *testing.T) {
	target := time.Date(2026, 11, 7, 0, 0, 0, 0, time.Local)
	items := []AnalyzeItem{
		testAnalyzeItem("a", "11-7", "11-7", true, false, "high"), // 日期确认 → 预览区
		testAnalyzeItem("b", "", "", true, true, "medium"),         // 没写日期 → 人工审查
		testAnalyzeItem("c", "11-6", "11-6", true, false, "high"), // 日期不符 → 人工审查（宽松）
		testAnalyzeItem("d", "", "", false, false, "high"),         // 非订单
		{Candidate: Candidate{Message: ChatMessage{ID: "e"}}, Err: errors.New("timeout")},
	}

	cfg := DefaultConfig() // OnlyToday=false，日期不符保留给人工判断
	out := classifyItems(items, cfg, target)
	if len(out.Preview) != 1 || out.Preview[0].Recipient != "a" {
		t.Fatalf("只有日期命中目标日期的订单应进预览区，实际 %+v", out.Preview)
	}
	if len(out.Review) != 2 {
		t.Fatalf("无日期与日期不符的订单都应进人工审查区，实际 %d 条", len(out.Review))
	}
	if out.Skipped != 1 || out.Failed != 1 || out.AIOrders != 3 {
		t.Fatalf("统计不对: skipped=%d failed=%d aiOrders=%d", out.Skipped, out.Failed, out.AIOrders)
	}
	if out.Details[0].Disposition != "预览" || out.Details[1].Disposition != "待审查" {
		t.Fatalf("明细应标出去向: %+v", out.Details[:2])
	}

	// 审查项必须带上发送时间、发送人、原文等人工判断依据
	r := out.Review[0].Public
	if r.ID == "" || r.Sender == "" || r.MessageTime == "" || r.MessageDate == "" ||
		r.RawContent == "" || r.Reason == "" || len(r.Orders) != 1 {
		t.Fatalf("审查项缺少判断依据: %+v", r)
	}
	if !strings.Contains(r.Reason, "未写日期标记") {
		t.Fatalf("无日期订单应说明原因，实际 %q", r.Reason)
	}
	if r.OrderDate != "" {
		t.Fatal("无日期订单不能被自动补上目标日期")
	}

	// 严格模式：日期明确但与目标不符的直接丢弃，不自动进预览区也不占审查区
	strict := DefaultConfig()
	strict.OnlyToday = true
	out2 := classifyItems(items, strict, target)
	if out2.DroppedByDate != 1 || len(out2.Review) != 1 || len(out2.Preview) != 1 {
		t.Fatalf("严格模式分流不对: dropped=%d review=%d preview=%d",
			out2.DroppedByDate, len(out2.Review), len(out2.Preview))
	}

	// 低置信度即使日期匹配也要人工审查
	low := classifyItems([]AnalyzeItem{
		testAnalyzeItem("f", "11-7", "11-7", true, false, "low"),
	}, cfg, target)
	if len(low.Preview) != 0 || len(low.Review) != 1 {
		t.Fatalf("低置信度订单应进人工审查区: preview=%d review=%d", len(low.Preview), len(low.Review))
	}
}

func TestPromoteReviewOrdersIsIdempotent(t *testing.T) {
	global.AddressList = nil
	replaceReviewRecords(nil)
	defer func() {
		global.AddressList = nil
		replaceReviewRecords(nil)
	}()

	// 已有一笔预览数据，加入审查订单时应续号而不是从 1 重排
	global.AddressList = []models.AddressInfo{{PreviewID: 1, Recipient: "已有"}}
	replaceReviewRecords([]reviewRecord{
		{Public: ReviewOrderItem{ID: "r1"}, Raw: []models.AddressInfo{testAddr("A")}},
		{Public: ReviewOrderItem{ID: "r2"}, Raw: []models.AddressInfo{testAddr("B")}},
	})

	latest := PromoteReviewOrders([]string{"r1"})
	if len(global.AddressList) != 2 || len(latest) != 2 {
		t.Fatalf("应追加 1 笔到预览区，实际列表 %d / 返回 %d", len(global.AddressList), len(latest))
	}
	if global.AddressList[1].PreviewID != 2 || global.AddressList[1].Recipient != "A" {
		t.Fatalf("VID 续号或订单内容不对: %+v", global.AddressList[1])
	}
	PromoteReviewOrders([]string{"r1"}) // 重复点击同一条必须幂等
	if len(global.AddressList) != 2 {
		t.Fatal("重复加入不应产生重复订单")
	}
	left := ReviewOrders()
	if len(left) != 1 || left[0].ID != "r2" {
		t.Fatalf("已加入的应从待审查列表移除，实际 %+v", left)
	}

	PromoteReviewOrders(nil) // ids 为空 = 全部加入
	if len(global.AddressList) != 3 || global.AddressList[2].PreviewID != 3 {
		t.Fatalf("全部加入后应有 3 笔且 VID 连续: %+v", global.AddressList)
	}
	if len(ReviewOrders()) != 0 {
		t.Fatal("全部加入后审查区应清空")
	}
}

func TestDiscardReviewOrdersDoesNotTouchPreview(t *testing.T) {
	global.AddressList = nil
	replaceReviewRecords(nil)
	defer func() {
		global.AddressList = nil
		replaceReviewRecords(nil)
	}()
	replaceReviewRecords([]reviewRecord{
		{Public: ReviewOrderItem{ID: "r1"}, Raw: []models.AddressInfo{testAddr("A")}},
		{Public: ReviewOrderItem{ID: "r2"}, Raw: []models.AddressInfo{testAddr("B")}},
		{Public: ReviewOrderItem{ID: "r3"}, Raw: []models.AddressInfo{testAddr("C")}},
	})
	left := DiscardReviewOrders([]string{"r2"})
	if len(left) != 2 {
		t.Fatalf("忽略一条后应剩 2 条，实际 %d", len(left))
	}
	if len(DiscardReviewOrders(nil)) != 0 {
		t.Fatal("忽略全部后审查区应为空")
	}
	if len(global.AddressList) != 0 {
		t.Fatal("忽略待审查数据不能改动数据预览区")
	}
}
