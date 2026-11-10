package wechatorder

// 订单操作层：在**全部订单识别完成后**统一回放聊天记录里的订单操作。
//
// 设计要点：
//  1. 不自动入库、不直接改数据预览区。被操作命中的订单会从原来的区域移出，
//     连同「原始 → 操作后」对照与操作依据一起送入人工审查区；
//     「加入预览区」= 采用操作结果，「忽略」= 撤销操作并恢复原始订单。
//  2. 定位分级：引用原文精确匹配 → AI 候选编号 → 关键词 + 同发送人 + 时间先后打分
//     → 同人就近兜底。任何一步有歧义都不自动应用，只生成待人工处理的审查项。
//  3. 操作消息一定发生在其目标订单消息之后，这是所有定位路径的硬约束。

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"order_server/go/models"
	ordertutil "order_server/go/utils/order"
)

// actionRef 是操作层统一索引里的一笔订单，可追溯到它来自哪条聊天消息。
type actionRef struct {
	Zone    string // preview / review
	Idx     int
	Sub     int // review 里 Raw 的下标；preview 固定 -1
	Current models.AddressInfo
	Origin  models.AddressInfo
	Msg     ChatMessage
	Touched bool
	Deleted bool
	Manual  bool
	MatchBy string
	Hits    []ActionHit
	Ops     []string
}

// ActionReport 是订单操作层的执行报告。
type ActionReport struct {
	Items      []ActionOrderItem
	Applied    int
	Manual     int
	Failed     int
	Candidates int
	Actions    int
}

type actionOutcome struct {
	Hit        ActionHit
	RefIdx     int
	Status     string
	MatchBy    string
	Changes    string
	Candidates []string
	ReviewID   string
}

var reDeictic = regexp.MustCompile(`(这单|那单|刚才|刚发|今天那个|上一单|刚刚|这条)`)

// applyActions 把识别出的订单操作回放一遍，并重写 split 的两个区域。
// hits 为空时 split 保持原样，订单识别结果不受任何影响。
func applyActions(split *classifyResult, hits []ActionHit, orderMsgs []ChatMessage, targetDate string) ActionReport {
	var report ActionReport
	if split == nil || len(hits) == 0 {
		return report
	}
	report.Candidates = len(hits)
	refs, refByKey := buildActionRefs(split)
	if len(refs) == 0 {
		return report
	}

	ordered := append([]ActionHit(nil), hits...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Message.Ts != ordered[j].Message.Ts {
			return ordered[i].Message.Ts < ordered[j].Message.Ts
		}
		return ordered[i].Message.ID < ordered[j].Message.ID
	})

	outcomes := make([]actionOutcome, 0, len(ordered))
	for _, hit := range ordered {
		if hit.Err != nil {
			report.Failed++
			continue
		}
		if !hit.Action.IsAction {
			continue
		}
		report.Actions++
		idx, how, candidates := locateAction(hit, refs, orderMsgs, targetDate)
		if idx < 0 {
			status := "unmatched"
			if len(candidates) > 0 && strings.Contains(how, "多个") {
				status = "ambiguous"
			}
			report.Manual++
			outcomes = append(outcomes, actionOutcome{
				Hit: hit, RefIdx: -1, Status: status,
				MatchBy: how, Changes: describeActionChanges(hit.Action, nil),
				Candidates: candidates,
			})
			continue
		}

		ref := refs[idx]
		ok, desc := applyActionToOrder(ref, hit.Action)
		ref.Touched = true
		ref.Hits = append(ref.Hits, hit)
		ref.Ops = append(ref.Ops, desc)
		ref.MatchBy = how
		if !ok {
			ref.Manual = true
		}
		status := "applied"
		if ref.Manual {
			status = "manual"
			report.Manual++
		} else {
			report.Applied++
		}
		outcomes = append(outcomes, actionOutcome{
			Hit: hit, RefIdx: idx, Status: status,
			MatchBy: how, Changes: desc,
		})
	}

	if len(outcomes) == 0 {
		return report
	}

	newReview := make([]reviewRecord, 0, len(split.Review)+len(outcomes))
	seq := 0
	// 被操作命中的订单：一条订单汇总成一条审查项（可能链式命中多个操作）。
	for i, ref := range refs {
		if !ref.Touched {
			continue
		}
		seq++
		rid := fmt.Sprintf("act-%s-%d", ref.Msg.ID, seq)
		after := []models.AddressInfo{}
		if !ref.Deleted {
			after = append(after, ref.Current)
		}
		before := []models.AddressInfo{ref.Origin}
		status := "applied"
		if ref.Manual {
			status = "manual"
		}
		var rawContent strings.Builder
		for _, h := range ref.Hits {
			rawContent.WriteString(actionRawContent(h.Message))
			rawContent.WriteString("\n\n")
		}
		first := ref.Hits[0]
		labels := make([]string, 0, len(ref.Hits))
		for _, h := range ref.Hits {
			labels = append(labels, actionLabel(h.Action.Action))
		}
		rr := reviewRecord{
			Public: ReviewOrderItem{
				ID:           rid,
				Sender:       first.Message.Sender,
				MessageTime:  first.Message.Time,
				MessageDate:  first.Message.Date,
				OrderDate:    dateLabelOf(targetDate),
				Reason:       "客户要求「" + strings.Join(labels, "、") + "」；定位依据：" + ref.MatchBy + "；" + first.Action.Reason,
				Confidence:   first.Action.Confidence,
				RawContent:   strings.TrimSpace(rawContent.String()),
				OrderCount:   len(after),
				Orders:       ordertutil.AddressToExcelAddress(after),
				Kind:         "action",
				ActionType:   first.Action.Action,
				ActionStatus: status,
				MatchBy:      ref.MatchBy,
				Changes:      strings.Join(ref.Ops, "；"),
				Quote:        first.Message.Quote,
				Before:       ordertutil.AddressToExcelAddress(before),
				After:        ordertutil.AddressToExcelAddress(after),
			},
			Raw:      after,
			Fallback: before,
			Source:   ref.Msg,
		}
		newReview = append(newReview, rr)
		for k := range outcomes {
			if outcomes[k].RefIdx == i {
				outcomes[k].ReviewID = rid
			}
		}
	}

	// 未定位 / 歧义的操作单独成项，展示候选订单，让人自己判断。
	for i := range outcomes {
		o := &outcomes[i]
		if o.RefIdx >= 0 {
			continue
		}
		seq++
		rid := fmt.Sprintf("act-unresolved-%s-%d", o.Hit.Message.ID, seq)
		rr := reviewRecord{
			Public: ReviewOrderItem{
				ID:           rid,
				Sender:       o.Hit.Message.Sender,
				MessageTime:  o.Hit.Message.Time,
				MessageDate:  o.Hit.Message.Date,
				OrderDate:    dateLabelOf(targetDate),
				Reason:       "识别到订单操作但" + o.MatchBy + "，未改动任何订单，请人工核对后处理：" + o.Hit.Action.Reason,
				Confidence:   o.Hit.Action.Confidence,
				RawContent:   actionRawContent(o.Hit.Message),
				OrderCount:   0,
				Orders:       []ordertutil.ExceladdressInfo{},
				Kind:         "action",
				ActionType:   o.Hit.Action.Action,
				ActionStatus: o.Status,
				MatchBy:      o.MatchBy,
				Changes:      o.Changes,
				Quote:        o.Hit.Message.Quote,
				Before:       []ordertutil.ExceladdressInfo{},
				After:        []ordertutil.ExceladdressInfo{},
				Candidates:   o.Candidates,
			},
			Source: o.Hit.Message,
		}
		newReview = append(newReview, rr)
		o.ReviewID = rid
	}

	// 未被触碰的订单原样留在原来的区域；被触碰的已经由上面的操作审查项接管。
	newPreview := make([]models.AddressInfo, 0, len(split.Preview))
	newPreviewSource := make([]ChatMessage, 0, len(split.PreviewSource))
	for i := range split.Preview {
		if r := refByKey[actionRefKey("preview", i, -1)]; r != nil && r.Touched {
			continue
		}
		newPreview = append(newPreview, split.Preview[i])
		if i < len(split.PreviewSource) {
			newPreviewSource = append(newPreviewSource, split.PreviewSource[i])
		}
	}
	for i := range split.Review {
		orig := split.Review[i]
		kept := make([]models.AddressInfo, 0, len(orig.Raw))
		for j := range orig.Raw {
			if r := refByKey[actionRefKey("review", i, j)]; r != nil && r.Touched {
				continue
			}
			kept = append(kept, orig.Raw[j])
		}
		if len(kept) == 0 {
			continue
		}
		nr := orig
		nr.Raw = kept
		nr.Public.Orders = ordertutil.AddressToExcelAddress(kept)
		nr.Public.OrderCount = len(kept)
		newReview = append(newReview, nr)
	}

	split.Preview = newPreview
	split.PreviewSource = newPreviewSource
	split.Review = newReview

	report.Items = make([]ActionOrderItem, 0, len(outcomes))
	for _, o := range outcomes {
		report.Items = append(report.Items, ActionOrderItem{
			ID:          o.Hit.Message.ID,
			Action:      o.Hit.Action.Action,
			ActionLabel: actionLabel(o.Hit.Action.Action),
			Sender:      o.Hit.Message.Sender,
			MessageTime: o.Hit.Message.Time,
			RawContent:  o.Hit.Message.Content,
			Quote:       o.Hit.Message.Quote,
			Reason:      o.Hit.Action.Reason,
			Confidence:  o.Hit.Action.Confidence,
			TargetLabel: actionTargetLabel(o, refs),
			MatchedBy:   o.MatchBy,
			Status:      o.Status,
			Changes:     o.Changes,
			ReviewID:    o.ReviewID,
		})
	}
	return report
}

func buildActionRefs(split *classifyResult) ([]*actionRef, map[string]*actionRef) {
	refs := make([]*actionRef, 0, len(split.Preview)+len(split.Review))
	byKey := make(map[string]*actionRef)
	for i := range split.Preview {
		msg := ChatMessage{}
		if i < len(split.PreviewSource) {
			msg = split.PreviewSource[i]
		}
		r := &actionRef{
			Zone: "preview", Idx: i, Sub: -1,
			Current: split.Preview[i], Origin: split.Preview[i], Msg: msg,
		}
		refs = append(refs, r)
		byKey[actionRefKey("preview", i, -1)] = r
	}
	for i := range split.Review {
		rec := split.Review[i]
		for j := range rec.Raw {
			r := &actionRef{
				Zone: "review", Idx: i, Sub: j,
				Current: rec.Raw[j], Origin: rec.Raw[j], Msg: rec.Source,
			}
			refs = append(refs, r)
			byKey[actionRefKey("review", i, j)] = r
		}
	}
	return refs, byKey
}

func actionRefKey(zone string, idx, sub int) string {
	return fmt.Sprintf("%s:%d:%d", zone, idx, sub)
}

func actionLabel(action string) string {
	if action == "delete" {
		return "删除订单"
	}
	return "修改订单"
}

func dateLabelOf(date string) string {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(date), time.Local)
	if err != nil {
		return ""
	}
	return TargetDateLabel(t)
}

func actionRawContent(m ChatMessage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "【客户操作消息】%s  %s\n%s", m.Sender, m.Time, strings.TrimSpace(m.Content))
	if q := strings.TrimSpace(m.Quote); q != "" {
		b.WriteString("\n\n【引用的订单消息原文】\n")
		if m.QuoteSender != "" {
			fmt.Fprintf(&b, "%s：\n", m.QuoteSender)
		}
		b.WriteString(q)
	}
	return b.String()
}

func describeOrderRef(ref *actionRef) string {
	a := ref.Current
	parts := make([]string, 0, 3)
	if v := strings.TrimSpace(a.Recipient); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(a.Phone); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(a.ProductSpecsString(false)); v != "" {
		parts = append(parts, v)
	}
	if len(parts) == 0 {
		return ref.Msg.Time + " 的订单"
	}
	return strings.Join(parts, " · ")
}

func actionTargetLabel(o actionOutcome, refs []*actionRef) string {
	if o.RefIdx >= 0 && o.RefIdx < len(refs) {
		return describeOrderRef(refs[o.RefIdx])
	}
	return "未定位"
}

// actionEligible 是定位的硬约束：操作消息必须晚于目标订单消息，且目标订单尚未被删除。
func actionEligible(ref *actionRef, hit ActionHit) bool {
	if ref.Deleted {
		return false
	}
	if ref.Msg.Ts > hit.Message.Ts {
		return false
	}
	if ref.Msg.Ts == hit.Message.Ts && ref.Msg.ID == hit.Message.ID {
		return false
	}
	return true
}

func normalizeForMatch(s string) string {
	s = strings.ToLower(s)
	s = strings.Join(strings.Fields(s), "")
	return strings.NewReplacer(
		"，", "", "。", "", "：", "", ":", "", "、", "", "；", "", ";", "",
		"（", "", "）", "", "(", "", ")", "", "【", "", "】", "", "-", "", "—", "",
		"～", "", "~", "", "！", "", "!", "", "？", "", "?", "",
	).Replace(s)
}

// quoteScore 判断引用原文与订单来源消息的贴合程度。
func quoteScore(quote string, ref *actionRef) int {
	q := normalizeForMatch(quote)
	c := normalizeForMatch(ref.Msg.Content)
	if len(q) < 8 || c == "" {
		return 0
	}
	if q == c {
		return 100
	}
	if strings.Contains(q, c) || strings.Contains(c, q) {
		return 90
	}
	// 微信引用框可能截断长文本，用订单原文开头一段做前缀匹配。
	runes := []rune(c)
	if len(runes) >= 12 {
		seg := c
		if len(runes) > 24 {
			seg = string(runes[:24])
		}
		if strings.Contains(q, seg) {
			return 70
		}
	}
	return 0
}

func actionKeywords(hit ActionHit) []string {
	t := hit.Action.Target
	raw := append([]string(nil), t.Keywords...)
	raw = append(raw, t.Store, t.Phone, t.Product, t.QuoteExcerpt)
	out := make([]string, 0, len(raw))
	seen := make(map[string]bool)
	for _, k := range raw {
		k = strings.TrimSpace(k)
		if len([]rune(k)) < 2 || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

func keywordScore(hit ActionHit, ref *actionRef, targetDate string) int {
	fields := []string{
		ref.Current.Recipient, ref.Current.Phone, ref.Current.Address,
		ref.Current.Remarks, ref.Msg.Content,
	}
	for _, s := range ref.Current.ProductSpecs {
		fields = append(fields, s.Name)
	}
	normFields := make([]string, 0, len(fields))
	for _, f := range fields {
		if v := normalizeForMatch(f); v != "" {
			normFields = append(normFields, v)
		}
	}
	score := 0
	for _, k := range actionKeywords(hit) {
		kn := normalizeForMatch(k)
		if kn == "" {
			continue
		}
		matched := false
		for _, fn := range normFields {
			if strings.Contains(fn, kn) || (len(kn) >= 4 && len(fn) >= 4 && strings.Contains(kn, fn)) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if rePhoneNum.MatchString(k) {
			score += 6
		} else if strings.Contains(normalizeForMatch(ref.Current.Recipient), kn) {
			score += 4 // 门店名 / 收件人是业务里最可靠的定位线索
		} else {
			score += 3
		}
	}
	// 同人 / 今天只是弱佐证，单独不足以自动定位（阈值 3），必须配合关键词，
	// 否则「这单不要了」在多笔同人订单里会被错误地猜中一笔。
	if hit.Action.Target.SameSender && ref.Msg.Sender != "" && ref.Msg.Sender == hit.Message.Sender {
		score++
	}
	if hit.Action.Target.DayScope == "today" && ref.Msg.Date != "" && ref.Msg.Date == targetDate {
		score++
	}
	return score
}

// locateAction 返回命中的 ref 下标；-1 表示歧义或无法定位，由人工处理。
func locateAction(hit ActionHit, refs []*actionRef, orderMsgs []ChatMessage, targetDate string) (int, string, []string) {
	eligible := make([]int, 0, len(refs))
	for i, ref := range refs {
		if actionEligible(ref, hit) {
			eligible = append(eligible, i)
		}
	}
	if len(eligible) == 0 {
		return -1, "在它之前没有可定位的订单", nil
	}

	// L1：引用原文精确定位（业务主路径，用户引用订单消息回复即可唯一确定）。
	if q := strings.TrimSpace(hit.Message.Quote); q != "" {
		best, bestScore, ties := -1, 0, 0
		for _, i := range eligible {
			s := quoteScore(q, refs[i])
			if s > bestScore {
				best, bestScore, ties = i, s, 1
			} else if s == bestScore && s > 0 {
				ties++
			}
		}
		if best >= 0 && bestScore >= 70 && ties == 1 {
			return best, "引用原文精确定位", nil
		}
	}

	// L2：模型在上下文里直接指出的订单候选编号。
	no := hit.Action.Target.CandidateNo
	if no > 0 && no < len(hit.ContextNos) {
		msgIdx := hit.ContextNos[no]
		if msgIdx >= 0 && msgIdx < len(orderMsgs) {
			id := orderMsgs[msgIdx].ID
			found := make([]int, 0, 1)
			for _, i := range eligible {
				if refs[i].Msg.ID == id {
					found = append(found, i)
				}
			}
			if len(found) == 1 {
				// 低置信度时必须有关键词佐证，不能只凭模型给的编号就改订单。
				supported := hit.Action.Confidence != "low" ||
					keywordScore(hit, refs[found[0]], targetDate) >= 3
				if supported {
					return found[0], "AI 候选定位", nil
				}
			}
		}
	}

	// L3：关键词 + 同发送人 + 时间范围打分。
	type scored struct{ idx, score int }
	list := make([]scored, 0, len(eligible))
	for _, i := range eligible {
		list = append(list, scored{i, keywordScore(hit, refs[i], targetDate)})
	}
	sort.SliceStable(list, func(a, b int) bool { return list[a].score > list[b].score })
	if list[0].score >= 3 && (len(list) == 1 || list[0].score >= list[1].score+2) {
		return list[0].idx, "关键词 / 时间定位", nil
	}

	// L4：只有本轮**仅有一笔**可定位订单、且消息带指代词（这单 / 刚才 / 今天那个）
	// 时才允许就近兜底。多笔订单且没有关键词一律交人工，绝不猜单。
	if list[0].score < 3 && len(eligible) == 1 &&
		(hit.Action.Target.DayScope != "" || reDeictic.MatchString(hit.Message.Content) ||
			hit.Action.Target.SameSender) {
		return eligible[0], "就近唯一订单", nil
	}

	candidates := make([]string, 0, 4)
	for k := 0; k < len(list) && k < 4; k++ {
		if list[k].score <= 0 {
			break
		}
		candidates = append(candidates, describeOrderRef(refs[list[k].idx]))
	}
	if len(candidates) == 0 {
		for k := 0; k < len(eligible) && k < 3; k++ {
			candidates = append(candidates, describeOrderRef(refs[eligible[k]]))
		}
		return -1, "未定位到目标订单", candidates
	}
	return -1, "多个订单线索相近，需人工指定", candidates
}

func specNameMatches(a, b string) bool {
	x, y := normalizeForMatch(a), normalizeForMatch(b)
	if x == "" || y == "" {
		return false
	}
	return x == y || strings.Contains(x, y) || strings.Contains(y, x)
}

// applyActionToOrder 把一次操作落到订单上；返回（是否无需人工补充, 变更说明）。
func applyActionToOrder(ref *actionRef, act OrderAction) (bool, string) {
	descs := make([]string, 0, 4)
	if act.Action == "delete" {
		ref.Deleted = true
		return true, "删除整笔订单"
	}

	modified, manual := false, false
	specs := append([]models.ProductSpec(nil), ref.Current.ProductSpecs...)

	for _, add := range act.Changes.AddSpecs {
		if add.Weight <= 0 {
			continue
		}
		name := strings.TrimSpace(add.Name)
		if name == "" {
			if len(specs) == 1 {
				specs[0].Weight += add.Weight
				descs = append(descs, fmt.Sprintf("%s 加 %d 斤 → %d 斤", specs[0].Name, add.Weight, specs[0].Weight))
				modified = true
			} else {
				manual = true
				descs = append(descs, fmt.Sprintf("加 %d 斤但未指定品名（订单有多个规格），需人工指定", add.Weight))
			}
			continue
		}
		found := false
		for i := range specs {
			if specNameMatches(specs[i].Name, name) {
				specs[i].Weight += add.Weight
				descs = append(descs, fmt.Sprintf("%s 加 %d 斤 → %d 斤", specs[i].Name, add.Weight, specs[i].Weight))
				found, modified = true, true
				break
			}
		}
		if !found {
			specs = append(specs, models.ProductSpec{Name: name, Weight: add.Weight})
			descs = append(descs, fmt.Sprintf("新增 %s %d 斤", name, add.Weight))
			modified = true
		}
	}

	for _, set := range act.Changes.SetSpecs {
		if set.Weight <= 0 {
			continue
		}
		name := strings.TrimSpace(set.Name)
		done := false
		if name == "" && len(specs) == 1 {
			descs = append(descs, fmt.Sprintf("%s 改为 %d 斤", specs[0].Name, set.Weight))
			specs[0].Weight = set.Weight
			done, modified = true, true
		}
		if !done && name != "" {
			for i := range specs {
				if specNameMatches(specs[i].Name, name) {
					descs = append(descs, fmt.Sprintf("%s 改为 %d 斤", specs[i].Name, set.Weight))
					specs[i].Weight = set.Weight
					done, modified = true, true
					break
				}
			}
		}
		if !done {
			manual = true
			descs = append(descs, fmt.Sprintf("改成 %d 斤但未能确定品名，需人工指定", set.Weight))
		}
	}

	for _, rm := range act.Changes.RemoveSpecs {
		name := strings.TrimSpace(rm.Name)
		if name == "" {
			continue
		}
		kept := make([]models.ProductSpec, 0, len(specs))
		for _, s := range specs {
			if specNameMatches(s.Name, name) {
				descs = append(descs, "去掉 "+s.Name)
				modified = true
				continue
			}
			kept = append(kept, s)
		}
		specs = kept
	}

	if v := strings.TrimSpace(act.Changes.Recipient); v != "" {
		descs = append(descs, "收件人改为 "+v)
		ref.Current.Recipient = v
		modified = true
	}
	if v := strings.TrimSpace(act.Changes.Phone); v != "" {
		descs = append(descs, "手机号改为 "+v)
		ref.Current.Phone = v
		modified = true
	}
	if v := strings.TrimSpace(act.Changes.Address); v != "" {
		descs = append(descs, "地址改为 "+v)
		ref.Current.Address = v
		modified = true
	}
	if v := strings.TrimSpace(act.Changes.Remarks); v != "" {
		descs = append(descs, "备注改为 "+v)
		ref.Current.Remarks = v
		modified = true
	}
	if v := strings.TrimSpace(act.Changes.AppendRemarks); v != "" {
		ref.Current.Remarks = appendRemark(ref.Current.Remarks, v)
		descs = append(descs, "备注追加 "+v)
		modified = true
	}
	ref.Current.ProductSpecs = specs

	if !modified && !manual {
		manual = true
		descs = append(descs, "未识别到具体变更内容（如「加单」但没说加什么/加多少），保留原单请人工补充")
	}
	if len(descs) == 0 {
		descs = append(descs, "未产生实际变更")
	}
	return !manual, strings.Join(descs, "；")
}

func appendRemark(old, add string) string {
	old, add = strings.TrimSpace(old), strings.TrimSpace(add)
	if old == "" {
		return add
	}
	if add == "" || old == add || strings.Contains(old, add) {
		return old // 已经写过就不再重复追加
	}
	return old + "；" + add
}

func describeActionChanges(act OrderAction, _ []string) string {
	parts := make([]string, 0, 4)
	if len(act.Changes.AddSpecs) > 0 {
		parts = append(parts, fmt.Sprintf("加 %d 项规格", len(act.Changes.AddSpecs)))
	}
	if len(act.Changes.SetSpecs) > 0 {
		parts = append(parts, fmt.Sprintf("改 %d 项规格", len(act.Changes.SetSpecs)))
	}
	if len(act.Changes.RemoveSpecs) > 0 {
		parts = append(parts, fmt.Sprintf("去掉 %d 项规格", len(act.Changes.RemoveSpecs)))
	}
	for k, v := range map[string]string{
		"收件人": act.Changes.Recipient, "手机号": act.Changes.Phone,
		"地址": act.Changes.Address, "备注": act.Changes.Remarks,
	} {
		if strings.TrimSpace(v) != "" {
			parts = append(parts, "改"+k)
		}
	}
	if strings.TrimSpace(act.Changes.AppendRemarks) != "" {
		parts = append(parts, "追加备注")
	}
	if act.Action == "delete" {
		parts = append([]string{"删除订单"}, parts...)
	}
	if len(parts) == 0 {
		return "未识别到具体变更内容"
	}
	return strings.Join(parts, "；")
}
