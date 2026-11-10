package wechatorder

// 聊天记录「订单操作」识别（一键识别的第二阶段）。
//
// 与订单识别刻意解耦：订单识别仍是「一条消息 → 一笔订单」的原文转录，输入与提示词
// 完全不变；本阶段从**全部导出消息**里另挑「疑似操作消息」，附上「此前已识别的订单候选」
// 交给模型，只输出「删除 / 修改 + 定位线索 + 变更内容」，不参与订单字段提取。
// 这样既不会拉低现有订单识别质量，也能让「这单不要了」这类没有上下文就无法理解的消息
// 有判断依据。

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"order_server/go/cfgseed"
	"order_server/go/global"
	"order_server/go/models"
	"order_server/go/utils/httputils"

	"github.com/sirupsen/logrus"
)

// ActionTarget 是「这条操作针对哪一笔订单」的定位线索，全部来自消息字面，不得臆造。
type ActionTarget struct {
	QuotePresent bool     `json:"quote_present"`
	QuoteExcerpt string   `json:"quote_excerpt"`
	CandidateNo  int      `json:"candidate_no"` // 对应上下文里的 #N 号订单候选，0 = 未指定
	Keywords     []string `json:"keywords"`
	Store        string   `json:"store"`
	Phone        string   `json:"phone"`
	Product      string   `json:"product"`
	DayScope     string   `json:"day_scope"` // today / recent / ""
	SameSender   bool     `json:"same_sender"`
}

// ActionChanges 描述一次修改要落到订单上的增量。
type ActionChanges struct {
	AddSpecs      []models.ProductSpec `json:"add_specs"`    // 加 N 斤；name 可空，由后端按唯一规格补
	RemoveSpecs   []models.ProductSpec `json:"remove_specs"` // 去掉某商品
	SetSpecs      []models.ProductSpec `json:"set_specs"`    // 改成 N 斤（weight 是改后总斤数）
	Recipient     string               `json:"recipient"`
	Phone         string               `json:"phone"`
	Address       string               `json:"address"`
	Remarks       string               `json:"remarks"`
	AppendRemarks string               `json:"append_remarks"` // 追加到原备注，不覆盖（自定义术语规则专用）
}

// OrderAction 是模型对一条后续消息的判定结果。
type OrderAction struct {
	IsAction   bool          `json:"is_action"`
	Action     string        `json:"action"` // delete / modify / none
	Confidence string        `json:"confidence"`
	Reason     string        `json:"reason"`
	Target     ActionTarget  `json:"target"`
	Changes    ActionChanges `json:"changes"`
}

// ActionHit 把识别结果与源消息、上下文候选编号映射绑在一起。
type ActionHit struct {
	Message    ChatMessage
	Action     OrderAction
	ContextNos []int // candidate_no -> orderMessages 下标（1-based，0 号无意义）
	Err        error
}

// reActionVerb 只用来「广撒网」挑候选，真正的判定交给模型；宁可多判一条也不能漏。
// 支持「加五斤」这类中文数字与「这单不要了」这类无引用的口语表达。
var reActionVerb = regexp.MustCompile(`(不要了|这单不要|那单不要|不要这单|别送|不用送|不发了|取消|退掉|撤单|删掉|作废|去掉|不要\s*[^\s，。]{1,8}\s*了|[加补]\s*(?:[0-9]+|[一二两三四五六七八九十半]+)\s*斤|加单|补单|多\s*(?:[0-9]+|[一二两三四五六七八九十半]+)\s*斤|再\s*加|再\s*来|[减少]\s*(?:[0-9]+|[一二两三四五六七八九十半]+)\s*斤|改成|改为|换成|改一下|这单改|那单改|刚才那单|今天那单|刚发的|这单|那单|改地址|改电话|改备注)`)

var (
	reAddJin = regexp.MustCompile(`(?:加|补|多|再)\s*([0-9]+|[一二两三四五六七八九十半]+)\s*斤`)
	reSetJin = regexp.MustCompile(`(?:改成|改为|换成|改)\s*([0-9]+|[一二两三四五六七八九十半]+)\s*斤`)
)

// actionSystemPrompt 优先取 cfg_prompts（支持热更新）；存量库尚未补齐新提示词时
// 从内嵌种子兜底，保证升级后第一次启动就能用，且不在代码里写死提示词正文。
// 末尾固定追加「群内自定义术语表」模板：用户加词只需改 cfg_order_action_rules，
// 不用改提示词正文，也不用重新发版。
func actionSystemPrompt() (string, error) {
	base := ""
	if p := strings.TrimSpace(global.Prompt(global.PromptWechatOrderAction)); p != "" {
		base = p
	} else {
		seed, err := cfgseed.Load()
		if err != nil {
			return "", err
		}
		for _, s := range seed.Prompts {
			if s.Code == global.PromptWechatOrderAction {
				base = s.Content
				break
			}
		}
		if base == "" {
			return "", fmt.Errorf("cfg_prompts 与内嵌种子均缺少 %s", global.PromptWechatOrderAction)
		}
	}
	return base + renderActionRulePrompt(compileActionRules("prompt")), nil
}

// looksLikeFullOrder 排除「消息本身就是一笔完整新订单」的情况，避免重复请求模型。
func looksLikeFullOrder(text string) bool {
	return rePhoneNum.MatchString(text) &&
		(reSpec.MatchString(text) || reQuantity.MatchString(text)) &&
		(reAddress.MatchString(text) || reStore.MatchString(text))
}

func hasOrderSignal(text string) bool {
	return rePhoneNum.MatchString(text) || reSpec.MatchString(text) ||
		reStore.MatchString(text) || reAddress.MatchString(text)
}

// pickActionMessages 从全部导出消息里挑「疑似订单操作」：
//   - 引用回复：引用原文像订单、或正文有操作动词（内置 / 自定义）→ 候选；
//   - 无引用：命中内置或自定义操作动词，且不是已被识别成订单的完整新订单 → 候选。
//
// 自定义术语规则是这里的关键：像「华夏农业大学消单」这种不含内置动词的口语，
// 以前在 LLM 之前就被丢掉，配置里加了「消单」后才会进入第二阶段。
// scope=trigger 的 ignore 规则可直接把误伤词挡在门外。
func pickActionMessages(messages []ChatMessage, orderMsgIDs map[string]bool) []ChatMessage {
	rules := compileActionRules("candidate")
	out := make([]ChatMessage, 0)
	for _, m := range messages {
		text := strings.TrimSpace(m.Content)
		if text == "" || reBotEcho.MatchString(text) {
			continue
		}
		custom, blocked := actionCandidateHit(text, rules)
		if blocked {
			continue
		}
		quoted := strings.TrimSpace(m.Quote)
		if quoted != "" {
			if custom || reActionVerb.MatchString(text) || hasOrderSignal(quoted) {
				out = append(out, m)
			}
			continue
		}
		if orderMsgIDs[m.ID] || (!reActionVerb.MatchString(text) && !custom) {
			continue
		}
		if looksLikeFullOrder(text) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// collectOrderMessages 汇总本轮被识别成订单的消息，按时间升序，供上下文与定位使用。
func collectOrderMessages(items []AnalyzeItem) ([]ChatMessage, map[string]bool) {
	msgs := make([]ChatMessage, 0)
	ids := make(map[string]bool)
	for _, it := range items {
		if it.Err != nil || !it.Result.IsOrder || len(it.Result.Orders) == 0 {
			continue
		}
		msgs = append(msgs, it.Candidate.Message)
		ids[it.Candidate.Message.ID] = true
	}
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].Ts < msgs[j].Ts })
	return msgs, ids
}

// buildActionContext 取操作消息之前、最近若干条已识别订单作为候选，
// 编号 #1/#2 让模型可以直接指出操作针对哪一笔；返回 candidate_no -> 消息下标 的映射。
func buildActionContext(actionMsg ChatMessage, orderMsgs []ChatMessage, limit int) (string, []int) {
	if limit <= 0 {
		limit = 8
	}
	before := make([]int, 0, len(orderMsgs))
	for i, m := range orderMsgs {
		if m.Ts < actionMsg.Ts || (m.Ts == actionMsg.Ts && m.ID != actionMsg.ID) {
			before = append(before, i)
		}
	}
	sort.SliceStable(before, func(i, j int) bool {
		return orderMsgs[before[i]].Ts > orderMsgs[before[j]].Ts
	})
	if len(before) > limit {
		before = before[:limit]
	}

	var b strings.Builder
	b.WriteString("【此前已识别的订单候选】（按时间由近到远，编号供定位，操作一定发生在候选消息之后）\n")
	nos := make([]int, len(before)+1)
	if len(before) == 0 {
		b.WriteString("（本轮没有识别出更早的订单消息）\n")
	}
	for k, idx := range before {
		m := orderMsgs[idx]
		content := strings.TrimSpace(m.Content)
		if runes := []rune(content); len(runes) > 260 {
			content = string(runes[:260]) + "…"
		}
		fmt.Fprintf(&b, "#%d [%s] %s：%s\n", k+1, m.Time, m.Sender,
			strings.ReplaceAll(content, "\n", " / "))
		nos[k+1] = idx
	}
	return b.String(), nos
}

func buildActionUserText(hit ChatMessage, context string) string {
	var b strings.Builder
	b.WriteString(context)
	b.WriteString("\n【本条待判定消息】\n")
	fmt.Fprintf(&b, "发送人：%s\n时间：%s\n", hit.Sender, hit.Time)
	if q := strings.TrimSpace(hit.Quote); q != "" {
		fmt.Fprintf(&b, "【引用消息原文】（被操作的那条消息）\n%s\n引用消息发言人：%s\n",
			q, hit.QuoteSender)
	}
	fmt.Fprintf(&b, "本条正文：%s\n\n请只输出严格 JSON。", strings.TrimSpace(hit.Content))
	return b.String()
}

// AnalyzeActions 识别全部「疑似操作消息」，返回按时间升序排列的结果与本轮订单消息列表。
// 任何一步失败都只记录、不阻断订单识别主链路。
func AnalyzeActions(messages []ChatMessage, items []AnalyzeItem, cfg Config, progress ProgressFunc) ([]ActionHit, []ChatMessage) {
	orderMsgs, orderIDs := collectOrderMessages(items)
	if len(orderMsgs) == 0 {
		return nil, nil
	}
	candidates := pickActionMessages(messages, orderIDs)
	if len(candidates) == 0 {
		return nil, orderMsgs
	}
	sysPrompt, err := actionSystemPrompt()
	if err != nil {
		logrus.Warnf("订单操作识别提示词不可用，已跳过本层: %v", err)
		return nil, orderMsgs
	}
	// 同一份规则在同一轮里既生成提示词、又做 LLM 之后的确定性兜底，
	// 保证模型输出与规则约束不会读到两个不同版本的配置快照。
	overrideRules := compileActionRules("prompt")
	key, url, model := aiSettings()
	if key == "" {
		logrus.Warnf("未配置 DeepSeek API Key，已跳过订单操作识别")
		return nil, orderMsgs
	}
	timeout := cfg.AITimeout
	if timeout <= 0 {
		timeout = 90
	}
	workers := cfg.Concurrency
	if workers <= 0 || workers > 20 {
		workers = 20
	}

	hits := make([]ActionHit, len(candidates))
	jobs := make(chan int)
	var wg sync.WaitGroup
	var done int64
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				m := candidates[idx]
				context, nos := buildActionContext(m, orderMsgs, 8)
				userText := buildActionUserText(m, context)
				act, err := analyzeAction(key, url, model, sysPrompt, userText, timeout)
				if err != nil {
					act, err = analyzeAction(key, url, model, sysPrompt, userText, timeout)
				}
				if err == nil {
					fillActionChangesFromText(m.Content, &act)
					applyRuleOverride(m.Content, &act, overrideRules, items)
				}
				hits[idx] = ActionHit{Message: m, Action: act, ContextNos: nos, Err: err}
				n := int(atomic.AddInt64(&done, 1))
				if progress != nil && (n%5 == 0 || n == len(candidates)) {
					progress("action", n, len(candidates),
						fmt.Sprintf("识别订单操作 %d/%d", n, len(candidates)))
				}
			}
		}()
	}
	for i := range candidates {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return hits, orderMsgs
}

func analyzeAction(apiKey, apiURL, model, sysPrompt, userText string, timeout int64) (OrderAction, error) {
	var result OrderAction
	reqBody := chatRequest{
		Model: model,
		Messages: []message{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: userText},
		},
		Temperature: 0.1,
		MaxTokens:   1024,
		Stream:      false,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return result, err
	}
	body, err := httputils.AiRequest(apiKey, apiURL, data, timeout, nil)
	if err != nil {
		return result, err
	}
	var resp chatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return result, fmt.Errorf("解析模型响应失败: %w", err)
	}
	if resp.Error != nil {
		return result, fmt.Errorf("DeepSeek 返回错误: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return result, fmt.Errorf("DeepSeek 未返回有效结果")
	}
	return parseActionResult(resp.Choices[0].Message.Content)
}

func parseActionResult(raw string) (OrderAction, error) {
	var result OrderAction
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return result, fmt.Errorf("模型未返回 JSON 对象: %.200s", s)
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &result); err != nil {
		return result, fmt.Errorf("解析订单操作 JSON 失败: %w", err)
	}
	result.Action = strings.ToLower(strings.TrimSpace(result.Action))
	result.Confidence = strings.ToLower(strings.TrimSpace(result.Confidence))
	if !result.IsAction || (result.Action != "delete" && result.Action != "modify") {
		return OrderAction{Action: "none"}, nil
	}
	if result.Confidence == "" {
		result.Confidence = "medium"
	}
	result.Changes.AddSpecs = cleanSpecs(result.Changes.AddSpecs)
	result.Changes.RemoveSpecs = cleanSpecs(result.Changes.RemoveSpecs)
	result.Changes.SetSpecs = cleanSpecs(result.Changes.SetSpecs)
	return result, nil
}

func cleanSpecs(in []models.ProductSpec) []models.ProductSpec {
	out := make([]models.ProductSpec, 0, len(in))
	for _, s := range in {
		s.Name = strings.TrimSpace(s.Name)
		if s.Weight <= 0 && len(s.Name) == 0 {
			continue
		}
		out = append(out, s)
	}
	return out
}

// fillActionChangesFromText 用原文确定性兜底补「加 N 斤 / 改成 N 斤」，
// 避免模型漏填中文数字（如「加五斤」）导致操作无法落地。
func fillActionChangesFromText(text string, r *OrderAction) {
	if !r.IsAction || r.Action != "modify" {
		return
	}
	if len(r.Changes.AddSpecs) == 0 && len(r.Changes.SetSpecs) == 0 {
		if m := reAddJin.FindStringSubmatch(text); m != nil {
			if w := parseCnNumber(m[1]); w > 0 {
				r.Changes.AddSpecs = append(r.Changes.AddSpecs, models.ProductSpec{Weight: w})
			}
		} else if m := reSetJin.FindStringSubmatch(text); m != nil {
			if w := parseCnNumber(m[1]); w > 0 {
				r.Changes.SetSpecs = append(r.Changes.SetSpecs, models.ProductSpec{Weight: w})
			}
		}
	}
}

// parseCnNumber 把「5 / 五 / 十 / 两 / 半」这类常见数量转成整数，无法识别返回 0。
func parseCnNumber(s string) int {
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	switch s {
	case "一":
		return 1
	case "二", "两":
		return 2
	case "三":
		return 3
	case "四":
		return 4
	case "五":
		return 5
	case "六":
		return 6
	case "七":
		return 7
	case "八":
		return 8
	case "九":
		return 9
	case "十":
		return 10
	case "半":
		return 0 // 半斤不落整数，交人工处理
	}
	return 0
}
