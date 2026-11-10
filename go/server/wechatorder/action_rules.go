package wechatorder

// 订单操作「自定义术语规则」：把 cfg_order_action_rules 的配置编译成一份匹配器，
// 供生产链路的三个点共用，避免规则散落在正则、提示词、兜底逻辑里各写一遍：
//
//  1. 候选闸门：内置 reActionVerb ∪ 自定义触发词，让「消单」这类口语能进入 LLM；
//  2. 提示词：把规则渲染成固定模板追加到 wechat_order_action 系统提示词尾部；
//  3. 确定性兜底：命中规则时覆盖 / 补强模型判定，并从已识别订单反查定位关键词。
//
// 规则来自 global 的不可变配置快照，支持 60 秒热更新；用户加词不需要重新发版。
// 所有匹配都是纯字符串包含，不对用户输入做正则编译，避免特殊字符引发误匹配或 panic。

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"order_server/go/global"
	"order_server/go/models"
	ordertutil "order_server/go/utils/order"
)

const (
	ruleScopeBoth    = "both"
	ruleScopeTrigger = "trigger"
	ruleScopePrompt  = "prompt"

	ruleActionDelete = "delete"
	ruleActionModify = "modify"
	ruleActionIgnore = "ignore"

	ruleValueNone       = "none"
	ruleValueAfter      = "after"
	ruleValuePhone      = "phone"
	ruleValueAppendRest = "append_rest"
)

var (
	reRulePhone       = regexp.MustCompile(`1[3-9][0-9]{9}`)
	reRuleValueMark   = regexp.MustCompile(`(?:改为|改成|换成|变更为|为|是|：|:)\s*`)
	reRuleTrimPunct   = regexp.MustCompile(`^[\s，。；、：,.;:!！?？~～\-—]+|[\s，。；、：,.;:!！?？~～\-—]+$`)
	reRuleStoreSuffix = regexp.MustCompile(`(?:门店|分店|店)$`)
)

// compiledRule 是别名展开后的一条可匹配规则；Keyword 是实际命中的那个词。
type compiledRule struct {
	Keyword string
	Rule    models.CfgOrderActionRule
}

// compileActionRules 按用途筛选并展开别名，排序规则为「匹配词越长越优先」，
// 同长度再按 priority 降序、sortOrder 升序。这样「消单了吗」会压过「消单」，
// 用户拉黑疑问句时不会被删除规则抢先命中。
func compileActionRules(usage string) []compiledRule {
	rows := global.GetActionRules()
	out := make([]compiledRule, 0, len(rows)*2)
	for _, r := range rows {
		if !r.Enabled {
			continue
		}
		scope := strings.TrimSpace(r.Scope)
		if scope == "" {
			scope = ruleScopeBoth
		}
		switch usage {
		case "candidate":
			if scope != ruleScopeBoth && scope != ruleScopeTrigger {
				continue
			}
		default: // prompt / override
			if scope != ruleScopeBoth && scope != ruleScopePrompt {
				continue
			}
		}
		for _, part := range strings.Split(r.Keyword, "/") {
			kw := strings.TrimSpace(part)
			if kw == "" {
				continue
			}
			out = append(out, compiledRule{Keyword: kw, Rule: r})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		li, lj := len([]rune(out[i].Keyword)), len([]rune(out[j].Keyword))
		if li != lj {
			return li > lj
		}
		if out[i].Rule.Priority != out[j].Rule.Priority {
			return out[i].Rule.Priority > out[j].Rule.Priority
		}
		return out[i].Rule.SortOrder < out[j].Rule.SortOrder
	})
	return out
}

func matchActionRule(text string, rules []compiledRule) (compiledRule, bool) {
	for _, r := range rules {
		if strings.Contains(text, r.Keyword) {
			return r, true
		}
	}
	return compiledRule{}, false
}

// ---------------- ① 候选闸门 ----------------

// actionCandidateHit 返回自定义触发词对一条消息的处理意见：
// blocked 表示命中了明确排除规则（ignore）；matched 表示应放行进 LLM。
func actionCandidateHit(text string, rules []compiledRule) (matched, blocked bool) {
	r, ok := matchActionRule(text, rules)
	if !ok {
		return false, false
	}
	return r.Rule.Action != ruleActionIgnore, r.Rule.Action == ruleActionIgnore
}

// ---------------- ② 提示词固定模板 ----------------

func ruleFieldLabel(field string) string {
	switch field {
	case "address":
		return "修改收货地址"
	case "phone":
		return "修改手机号"
	case "recipient":
		return "修改店名 / 收件人"
	case "remarks":
		return "追加订单备注"
	case "specs":
		return "修改商品规格"
	default:
		return "修改订单"
	}
}

func ruleValueLabel(v string) string {
	switch v {
	case ruleValueAfter:
		return "取「为 / 改成 / ：」之后的内容"
	case ruleValuePhone:
		return "取消息里的手机号"
	case ruleValueAppendRest:
		return "取去掉定位词与触发词后的剩余内容"
	default:
		return "不需要新值"
	}
}

// renderActionRulePrompt 把规则渲染成固定骨架 + 表格，追加到操作识别提示词尾部。
// 提示词正文（cfg_prompts.wechat_order_action）不需要改动，存量库也不需要重刷。
func renderActionRulePrompt(rules []compiledRule) string {
	if len(rules) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## 五、群内自定义术语表（最高优先级）\n")
	b.WriteString("下表是本群实际使用的口语 / 缩写，由系统按配置自动生成。命中即按表判定，\n")
	b.WriteString("**不得因为「第一节未列举该词」就判 is_action=false**。\n\n")
	b.WriteString("| 命中词 | 订单操作 | 目标字段 | 新值抽取 |\n| --- | --- | --- | --- |\n")
	seen := make(map[string]bool)
	for _, r := range rules {
		key := r.Rule.Keyword
		if seen[key] {
			continue
		}
		seen[key] = true
		op := ""
		switch r.Rule.Action {
		case ruleActionDelete:
			op = "delete（删除整笔订单）"
		case ruleActionModify:
			op = "modify（" + ruleFieldLabel(r.Rule.Field) + "）"
		case ruleActionIgnore:
			op = "明确排除，判非操作"
		default:
			continue
		}
		field := r.Rule.Field
		if field == "" {
			field = "—"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
			strings.ReplaceAll(strings.TrimSpace(r.Rule.Keyword), "/", "、"),
			op, field, ruleValueLabel(r.Rule.ValueRule))
	}
	b.WriteString("\n【判定约束】\n")
	b.WriteString("1. 命中上表词语即按表判定；与第一节默认词表冲突时以本表为准。多条同时命中时以匹配字数最多的一条为准。\n")
	b.WriteString("2. 无论命中哪条，本条消息里出现的店名 / 收件人 / 电话 / 地址片段一律写进 target.keywords（能确定时同时填 store / phone）。这是定位目标订单的关键，绝不省略。\n")
	b.WriteString("3. 修改类规则的新值由后端按上表确定性抽取并填入 changes 对应字段；抽不到会标低置信度交人工，你不要编造地址、电话或数字。\n")
	b.WriteString("4. 出现「店名 + 一句要求」但不构成新订单时，判 modify 并填好 target.keywords 即可；备注类要求由后端追加到原订单，不要把整句塞进 changes.remarks 覆盖原备注。\n")
	b.WriteString("5. 输出格式仍严格按第四节的 JSON，不要输出任何解释文字。\n")
	return b.String()
}

// ---------------- ③ 确定性兜底 ----------------

// ruleLocatorKeywords 从本轮已识别订单里反查消息中的定位线索。
// 「消单」被兜底提升为删除时模型没有输出 keywords，若不补这一步，
// 定位层会因为 keywordScore=0 而把操作丢进「未定位到订单」的人工区。
// 门店名常见的「店 / 门店 / 分店」后缀会被去掉再试一次，
// 因此「华夏农业大学消单」能命中订单里的「华夏农业大学店」。
func ruleLocatorKeywords(text string, items []AnalyzeItem) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, 4)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if len([]rune(s)) < 2 || seen[s] || !strings.Contains(text, s) {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, it := range items {
		if it.Err != nil || !it.Result.IsOrder {
			continue
		}
		for _, o := range it.Result.Orders {
			add(o.Recipient)
			add(reRuleStoreSuffix.ReplaceAllString(strings.TrimSpace(o.Recipient), ""))
			add(o.Phone)
			for _, sp := range o.ProductSpecs {
				add(sp.Name)
			}
		}
	}
	return out
}

func trimRuleValue(s string) string {
	return strings.TrimSpace(reRuleTrimPunct.ReplaceAllString(strings.TrimSpace(s), ""))
}

// extractRuleValue 按 value_rule 从原文确定性抽新值，抽不到返回空串交人工。
func extractRuleValue(text string, r compiledRule, keywords []string) string {
	switch r.Rule.ValueRule {
	case ruleValuePhone:
		return trimRuleValue(reRulePhone.FindString(text))
	case ruleValueAfter:
		locs := reRuleValueMark.FindAllStringIndex(text, -1)
		if len(locs) == 0 {
			return ""
		}
		return trimRuleValue(text[locs[len(locs)-1][1]:])
	case ruleValueAppendRest:
		s := strings.ReplaceAll(text, r.Keyword, "")
		for _, k := range keywords {
			s = strings.ReplaceAll(s, k, "")
		}
		for _, w := range []string{"这单", "那单", "刚刚", "刚才", "今天那个", "帮忙", "麻烦"} {
			s = strings.ReplaceAll(s, w, "")
		}
		return trimRuleValue(s)
	default:
		return ""
	}
}

func mergeRuleKeywords(act *OrderAction, extra []string) {
	seen := make(map[string]bool, len(act.Target.Keywords))
	for _, k := range act.Target.Keywords {
		seen[strings.TrimSpace(k)] = true
	}
	for _, k := range extra {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		act.Target.Keywords = append(act.Target.Keywords, k)
	}
	if act.Target.Store == "" && len(act.Target.Keywords) > 0 {
		act.Target.Store = act.Target.Keywords[0]
	}
}

// fillRuleChange 把确定性抽到的新值填进模型漏填的 changes 字段；
// 模型已经填过的字段不覆盖。specs 走原文【品名N斤】解析，解析不出交人工。
func fillRuleChange(text string, r compiledRule, keywords []string, act *OrderAction) {
	v := extractRuleValue(text, r, keywords)
	if v == "" {
		if r.Rule.ValueRule != ruleValueNone {
			act.Confidence = "low"
		}
		return
	}
	switch r.Rule.Field {
	case "address":
		if strings.TrimSpace(act.Changes.Address) == "" {
			act.Changes.Address = v
		}
	case "phone":
		if strings.TrimSpace(act.Changes.Phone) == "" {
			act.Changes.Phone = v
		}
	case "recipient":
		if strings.TrimSpace(act.Changes.Recipient) == "" {
			act.Changes.Recipient = v
		}
	case "remarks":
		if strings.TrimSpace(act.Changes.AppendRemarks) == "" {
			act.Changes.AppendRemarks = v
		}
		// 模型若把同一句要求填成了覆盖式 remarks，交回追加语义处理，
		// 否则会先把原备注冲掉、再追加一遍。
		if strings.TrimSpace(act.Changes.Remarks) == v {
			act.Changes.Remarks = ""
		}
	case "specs":
		if len(act.Changes.SetSpecs) == 0 {
			specs, err := ordertutil.Specprocessing(v)
			if err != nil && !strings.Contains(v, "【") {
				specs, err = ordertutil.Specprocessing("【" + v + "】")
			}
			if err == nil && len(specs) > 0 {
				act.Changes.SetSpecs = append(act.Changes.SetSpecs, specs...)
			} else {
				act.Confidence = "low"
			}
		}
	}
}

// applyRuleOverride 在 LLM 判定之后执行，且只在命中自定义术语时介入，
// 不改动未命中规则的原有识别路径。命中删除类术语时无论模型怎么判都强制删除，
// 定位歧义仍由 locateAction 交人工处理，安全语义不变。
func applyRuleOverride(text string, act *OrderAction, rules []compiledRule, items []AnalyzeItem) {
	r, ok := matchActionRule(text, rules)
	if !ok {
		return
	}
	hit := "命中自定义术语「" + r.Keyword + "」"
	if r.Rule.Action == ruleActionIgnore {
		*act = OrderAction{Action: "none", Confidence: "high", Reason: hit + "，按配置排除"}
		return
	}

	keywords := ruleLocatorKeywords(text, items)
	mergeRuleKeywords(act, keywords)

	oldReason := strings.TrimSpace(act.Reason)
	switch r.Rule.Action {
	case ruleActionDelete:
		act.IsAction = true
		act.Action = ruleActionDelete
		act.Confidence = "high"
		act.Reason = hit + "（取消 / 删除订单）"
	case ruleActionModify:
		if !act.IsAction || act.Action == "none" {
			act.IsAction = true
			act.Action = ruleActionModify
			act.Confidence = "high"
			act.Reason = hit + "（" + ruleFieldLabel(r.Rule.Field) + "）"
		} else if act.Action == ruleActionModify {
			act.Reason = hit + "；" + oldReason
		}
		fillRuleChange(text, r, keywords, act)
	default:
		return
	}
	if oldReason != "" && !strings.Contains(act.Reason, oldReason) {
		act.Reason += "；模型判断：" + oldReason
	}
}
