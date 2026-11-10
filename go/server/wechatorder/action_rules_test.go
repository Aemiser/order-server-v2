package wechatorder

import (
	"fmt"
	"strings"
	"testing"

	"order_server/go/global"
	"order_server/go/models"
)

// 用户实际遇到的场景：
//
//	A:11-9 店名：华夏农业大学店 收件人：王示例 电话：13800001234
//	   地址：华夏省大兵市下区东陵路120号… 【广香5斤】新客户发好一点
//	A:华夏农业大学消单          ← 「消单」= 取消订单，模型以前识别不到
//	A:华夏农业大学 这单发小果    ← 给订单追加备注
//	A:华夏农业大学 改地址 为 华夏省大兵市…  ← 修改地址
func seedActionRulesForTest(t *testing.T) {
	t.Helper()
	global.SetActionRules([]models.CfgOrderActionRule{
		{Keyword: "消单了吗/消单吗/销单了吗", Action: "ignore", ValueRule: "none", Scope: "trigger", Priority: 200, Enabled: true},
		{Keyword: "消单/销单/抹单/砍单", Action: "delete", ValueRule: "none", Scope: "both", Priority: 100, Enabled: true},
		{Keyword: "改地址/地址改", Action: "modify", Field: "address", ValueRule: "after", Scope: "both", Priority: 90, Enabled: true},
		{Keyword: "改电话/电话改", Action: "modify", Field: "phone", ValueRule: "phone", Scope: "both", Priority: 90, Enabled: true},
		{Keyword: "这单发/那单发/加备注", Action: "modify", Field: "remarks", ValueRule: "append_rest", Scope: "both", Priority: 80, Enabled: true},
		{Keyword: "改规格/规格改", Action: "modify", Field: "specs", ValueRule: "after", Scope: "both", Priority: 90, Enabled: true},
	})
	t.Cleanup(func() { global.SetActionRules(nil) })
}

func shenyangOrder() models.AddressInfo {
	return models.AddressInfo{
		Recipient:    "华夏农业大学店",
		Phone:        "13800001234",
		Address:      "华夏省大兵市下区东陵路120号示例商场门口3号铺",
		ProductSpecs: models.ProductSpecsJSON{{Name: "广香", Weight: 5}},
		Remarks:      "新客户发好一点",
	}
}

func shenyangItems(orderMsg ChatMessage) []AnalyzeItem {
	return []AnalyzeItem{{
		Candidate: Candidate{Message: orderMsg},
		Result:    AIResult{IsOrder: true, Orders: []models.AddressInfo{shenyangOrder()}},
	}}
}

// 场景一（决定性）：「消单」既不在内置正则也不在提示词词表里，
// 以前在 LLM 之前就被丢弃。加了规则后必须进入候选、被兜底成删除、并靠店名定位到订单。
func TestRuleCancelOrderEndToEnd(t *testing.T) {
	resetActionState()
	seedActionRulesForTest(t)
	defer resetActionState()

	orderText := "A:11-9\n店名：华夏农业大学店\n收件人：王示例\n电话：13800001234\n" +
		"地址：华夏省大兵市下区东陵路120号示例商场门口3号铺\n【广香5斤】新客户发好一点"
	orderMsg := actionMsg("m1", "A", orderText, "", 1000)
	cancelMsg := actionMsg("m2", "A", "华夏农业大学消单", "", 2000)

	got := pickActionMessages([]ChatMessage{orderMsg, cancelMsg}, map[string]bool{"m1": true})
	if len(got) != 1 || got[0].ID != "m2" {
		t.Fatalf("「消单」应被自定义规则放进候选，实际 %+v", got)
	}

	// 模拟大模型没认出「消单」，返回 none。
	act := OrderAction{Action: "none", Reason: "无法判断"}
	applyRuleOverride(cancelMsg.Content, &act, compileActionRules("prompt"), shenyangItems(orderMsg))
	if !act.IsAction || act.Action != "delete" {
		t.Fatalf("命中「消单」必须强制判为删除，实际 %+v", act)
	}
	if !strings.Contains(strings.Join(act.Target.Keywords, ","), "华夏农业大学") {
		t.Fatalf("应从已识别订单反查店名补进 keywords，实际 %+v", act.Target.Keywords)
	}

	split := actionSplit(shenyangOrder(), orderMsg)
	hit := ActionHit{Message: cancelMsg, Action: act, ContextNos: []int{0, 0}}
	report := applyActions(split, []ActionHit{hit}, []ChatMessage{orderMsg}, "2026-11-09")
	if report.Applied != 1 || len(split.Preview) != 0 || len(split.Review) != 1 {
		t.Fatalf("应定位并应用删除，report=%+v preview=%d review=%d", report, len(split.Preview), len(split.Review))
	}
	rr := split.Review[0]
	if rr.Public.ActionType != "delete" || rr.Public.ActionStatus != "applied" || len(rr.Raw) != 0 {
		t.Fatalf("审查项应为已定位的删除操作: %+v", rr.Public)
	}
	if !strings.Contains(rr.Public.MatchBy, "关键词") {
		t.Fatalf("应靠店名关键词定位（少一个「店」字也要能命中），实际 %q", rr.Public.MatchBy)
	}
}

// 场景二：「这单发小果」是追加备注，不能把原备注「新客户发好一点」覆盖掉。
func TestRuleAppendRemarkKeepsOriginal(t *testing.T) {
	resetActionState()
	seedActionRulesForTest(t)
	defer resetActionState()

	orderMsg := actionMsg("m1", "A", "店名：华夏农业大学店 【广香5斤】", "", 1000)
	text := "华夏农业大学 这单发小果"
	act := OrderAction{Action: "none"}
	applyRuleOverride(text, &act, compileActionRules("prompt"), shenyangItems(orderMsg))
	if !act.IsAction || act.Action != "modify" {
		t.Fatalf("应提升为修改订单，实际 %+v", act)
	}
	if act.Changes.AppendRemarks != "小果" {
		t.Fatalf("追加备注应抽到「小果」，实际 %q", act.Changes.AppendRemarks)
	}

	split := actionSplit(shenyangOrder(), orderMsg)
	hit := ActionHit{Message: actionMsg("m2", "A", text, "", 2000), Action: act}
	report := applyActions(split, []ActionHit{hit}, []ChatMessage{orderMsg}, "2026-11-09")
	if report.Applied != 1 || len(split.Review) != 1 {
		t.Fatalf("应定位并应用备注修改，report=%+v", report)
	}
	got := split.Review[0].Raw[0].Remarks
	if got != "新客户发好一点；小果" {
		t.Fatalf("备注应追加而非覆盖，实际 %q", got)
	}
}

// 场景三：改地址 / 改电话 / 改规格，新值由 value_rule 确定性抽取，不依赖模型。
func TestRuleModifyFieldsExtractValue(t *testing.T) {
	seedActionRulesForTest(t)

	cases := []struct {
		name  string
		text  string
		check func(OrderAction) bool
	}{
		{"改地址", "华夏农业大学 改地址 为 华夏省大兵市天河区1号", func(a OrderAction) bool {
			return a.Action == "modify" && a.Changes.Address == "华夏省大兵市天河区1号"
		}},
		{"改电话", "华夏农业大学 改电话 13900001111", func(a OrderAction) bool {
			return a.Action == "modify" && a.Changes.Phone == "13900001111"
		}},
		{"改规格", "华夏农业大学 改规格 为 【广香8斤】", func(a OrderAction) bool {
			return a.Action == "modify" && len(a.Changes.SetSpecs) == 1 &&
				a.Changes.SetSpecs[0].Name == "广香" && a.Changes.SetSpecs[0].Weight == 8
		}},
	}
	orderMsg := actionMsg("m1", "A", "店名：华夏农业大学店 【广香5斤】", "", 1000)
	items := shenyangItems(orderMsg)
	rules := compileActionRules("prompt")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			act := OrderAction{Action: "none"}
			applyRuleOverride(c.text, &act, rules, items)
			if !c.check(act) {
				t.Fatalf("确定性抽取不对: %+v", act)
			}
		})
	}
}

// 场景四：ignore 规则要压过「消单」——疑问句不是操作指令。
func TestRuleIgnoreBeatsCancel(t *testing.T) {
	seedActionRulesForTest(t)
	msg := actionMsg("m2", "A", "华夏农业大学消单了吗", "", 2000)
	got := pickActionMessages([]ChatMessage{msg}, map[string]bool{})
	if len(got) != 0 {
		t.Fatalf("疑问句应被 ignore 规则挡在候选之外，实际 %+v", got)
	}
	// 明确排除规则（scope=trigger）不进入提示词与兜底，这是刻意的语义。
	// 注意「消单」这条 both 规则仍在 prompt 集里，这里只检查 trigger 行不混入。
	for _, r := range compileActionRules("prompt") {
		if r.Rule.Scope == "trigger" {
			t.Fatalf("scope=trigger 的规则不应出现在 prompt / override 规则集里: %+v", r)
		}
	}
}

// 场景五：提示词模板确实被追加，且用户改规则后无需重新发版即可生效。
func TestRenderActionRulePrompt(t *testing.T) {
	seedActionRulesForTest(t)
	prompt, err := actionSystemPrompt()
	if err != nil {
		t.Fatalf("取操作识别提示词失败: %v", err)
	}
	for _, want := range []string{"群内自定义术语表", "消单", "target.keywords", "判定约束"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("提示词模板缺少 %q", want)
		}
	}
	// 热更新：换一份规则，渲染结果必须立刻跟着变。
	global.SetActionRules([]models.CfgOrderActionRule{
		{Keyword: "撤单", Action: "delete", ValueRule: "none", Scope: "both", Enabled: true},
	})
	after, err := actionSystemPrompt()
	if err != nil || !strings.Contains(after, "撤单") || strings.Contains(after, "消单") {
		t.Fatalf("规则热更新未生效: err=%v", err)
	}
}

func TestRuleDisabledAndScope(t *testing.T) {
	global.SetActionRules([]models.CfgOrderActionRule{
		{Keyword: "消单", Action: "delete", ValueRule: "none", Scope: "prompt", Enabled: true},
		{Keyword: "销单", Action: "delete", ValueRule: "none", Scope: "both", Enabled: false},
	})
	t.Cleanup(func() { global.SetActionRules(nil) })
	if len(compileActionRules("candidate")) != 0 {
		t.Fatal("scope=prompt 或 enabled=false 的规则不应参与候选闸门")
	}
	if len(compileActionRules("prompt")) != 1 {
		t.Fatal("scope=prompt 的规则应进入提示词与兜底")
	}
}

// 模型偶尔会把「这句要求」填成覆盖式 remarks，兜底要把它交回追加语义，
// 不能出现「先冲掉原备注、再追加一遍」的情况。
func TestRuleAppendRemarkOverridesModelRemark(t *testing.T) {
	resetActionState()
	seedActionRulesForTest(t)
	defer resetActionState()

	orderMsg := actionMsg("m1", "A", "店名：华夏农业大学店 【广香5斤】", "", 1000)
	text := "华夏农业大学 这单发小果"
	act := OrderAction{IsAction: true, Action: "modify", Changes: ActionChanges{Remarks: "小果"}}
	applyRuleOverride(text, &act, compileActionRules("prompt"), shenyangItems(orderMsg))
	if act.Changes.Remarks != "" || act.Changes.AppendRemarks != "小果" {
		t.Fatalf("覆盖式备注应交回追加语义，实际 %+v", act.Changes)
	}

	split := actionSplit(shenyangOrder(), orderMsg)
	hit := ActionHit{Message: actionMsg("m2", "A", text, "", 2000), Action: act}
	applyActions(split, []ActionHit{hit}, []ChatMessage{orderMsg}, "2026-11-09")
	if got := split.Review[0].Raw[0].Remarks; got != "新客户发好一点；小果" {
		t.Fatalf("原备注必须保留并追加，实际 %q", got)
	}
}

func Example_renderActionRulePrompt() {
	global.SetActionRules([]models.CfgOrderActionRule{
		{Keyword: "消单/销单", Action: "delete", ValueRule: "none", Scope: "both", Enabled: true},
	})
	fmt.Println(strings.Contains(renderActionRulePrompt(compileActionRules("prompt")), "消单、销单"))
	// Output: true
}
