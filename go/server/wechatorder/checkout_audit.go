package wechatorder

// 账单结算页「审核」：按用户选择的日期范围导出群聊 → 复用一键识别的
// 规则粗筛 / 并发 AI 识别 / 原文规格校正 → 与已入库的账单订单逐笔核对 →
// 把「疑似漏单」和「不确定订单」放进账单页专属的人工审核区。
//
// 刻意与「新建订单页的一键识别」隔离，互不干扰：
//   - 不写 global.AddressList，绝不覆盖数据预览区；
//   - 不写 reviewRecords，绝不清空新建订单页的人工审查区；
//   - 不写数据库、不生成文件；只有人工点「加入预览区」才追加到预览区，仍不入库。
//
// 与一键识别共用的部分：ExportRange（密钥/解密/导出）、RuleFilterWithConfig、
// AnalyzeAllWithOptions（同一个并发 AI 引擎）、FixSpecNames、SetVid、
// AddressToExcelAddress、ResolveOrderDate。

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"order_server/go/global"
	"order_server/go/models"
	ordertutil "order_server/go/utils/order"

	"github.com/sirupsen/logrus"
)

// ---------------- 对外数据结构 ----------------

// CheckoutAuditItem 是账单页人工审核区的一条数据（一笔订单）。
type CheckoutAuditItem struct {
	ID          string                        `json:"id"`
	Kind        string                        `json:"kind"`        // missing 漏单 / uncertain 核对存疑 / review 识别存疑
	KindLabel   string                        `json:"kindLabel"`
	Reason      string                        `json:"reason"`
	Confidence  string                        `json:"confidence"`
	Sender      string                        `json:"sender"`
	MessageTime string                        `json:"messageTime"`
	MessageDate string                        `json:"messageDate"`
	OrderDate   string                        `json:"orderDate"`
	RawContent  string                        `json:"rawContent"`
	OrderCount  int                           `json:"orderCount"`
	Orders      []ordertutil.ExceladdressInfo `json:"orders"`      // 群聊里识别出的这笔订单
	MatchScore  int                           `json:"matchScore"`  // 与最相似的账单订单的相似度 0~100
	MatchLabel  string                        `json:"matchLabel"`  // 疑似对应账单订单的摘要
	MatchedAt   string                        `json:"matchedAt"`   // 疑似对应账单订单的入库时间
	MatchOrders []ordertutil.ExceladdressInfo `json:"matchOrders"` // 疑似对应的账单订单，供人工对照
}

// CheckoutAuditResult 是一轮审核的汇总。
type CheckoutAuditResult struct {
	Success          bool     `json:"success"`
	Message          string   `json:"message"`
	From             string   `json:"from"`
	To               string   `json:"to"`
	Source           string   `json:"source"`
	Exported         int      `json:"exported"`         // 导出的群聊消息数
	Candidates       int      `json:"candidates"`       // 粗筛后交 AI 的消息数
	Dropped          int      `json:"dropped"`          // 粗筛丢弃的消息数
	AIFailed         int      `json:"aiFailed"`         // AI 请求失败的消息数
	CheckoutOrders   int      `json:"checkoutOrders"`   // 该日期范围内已入库的账单订单数
	RecognizedOrders int      `json:"recognizedOrders"` // 群聊里识别出的订单笔数
	Matched          int      `json:"matched"`          // 与账单订单核对一致的笔数
	Missing          int      `json:"missing"`          // 疑似漏单笔数
	Uncertain        int      `json:"uncertain"`        // 存疑笔数（识别存疑 + 核对存疑）
	UnmatchedBills   int      `json:"unmatchedBills"`   // 账单里有、群聊里没匹配到的订单数
	ReviewCount      int      `json:"reviewCount"`      // 进入人工审核区的条数
	Warnings         []string `json:"warnings"`
}

// ---------------- 内存状态 ----------------

type checkoutAuditRecord struct {
	Public   CheckoutAuditItem
	Raw      []models.AddressInfo // 「加入预览区」要追加的订单
	Promoted bool
	Removed  bool
}

var (
	checkoutAuditMu   sync.Mutex
	checkoutAuditBusy bool
	checkoutAuditRecs []checkoutAuditRecord
	checkoutAuditLast CheckoutAuditResult
)

// CheckoutAuditReviews 返回当前仍待人工审核的账单核对结果。
func CheckoutAuditReviews() []CheckoutAuditItem {
	checkoutAuditMu.Lock()
	defer checkoutAuditMu.Unlock()
	return checkoutAuditPendingLocked()
}

func checkoutAuditPendingLocked() []CheckoutAuditItem {
	out := make([]CheckoutAuditItem, 0, len(checkoutAuditRecs))
	for _, r := range checkoutAuditRecs {
		if !r.Promoted && !r.Removed {
			out = append(out, r.Public)
		}
	}
	return out
}

// CheckoutAuditSummary 返回最近一轮审核的汇总（切页回来时前端重新拉取）。
func CheckoutAuditSummary() CheckoutAuditResult {
	checkoutAuditMu.Lock()
	defer checkoutAuditMu.Unlock()
	return checkoutAuditLast
}

// PromoteCheckoutAuditReviews 把人工确认的漏单追加到「数据预览区」。
// 与新建订单页的「加入预览区」语义一致：只进预览区、不入库、不生成文件；
// ids 为空表示全部加入，重复点击幂等。
func PromoteCheckoutAuditReviews(ids []string) []ordertutil.ExceladdressInfo {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	all := len(ids) == 0
	var fresh []models.AddressInfo

	checkoutAuditMu.Lock()
	for i := range checkoutAuditRecs {
		r := &checkoutAuditRecs[i]
		if r.Promoted || r.Removed || (!all && !want[r.Public.ID]) {
			continue
		}
		if len(r.Raw) == 0 {
			r.Removed = true
			continue
		}
		fresh = append(fresh, r.Raw...)
		r.Promoted = true
	}
	checkoutAuditMu.Unlock()

	// 写预览区必须与一键识别 / 原有审核区共用同一把 runMu，避免并发覆盖。
	runMu.Lock()
	defer runMu.Unlock()
	if len(fresh) > 0 {
		fresh = ordertutil.SetVid(fresh)
		global.AddressList = append(global.AddressList, fresh...)
	}
	return ordertutil.AddressToExcelAddress(global.AddressList)
}

// DiscardCheckoutAuditReviews 忽略指定条目（ids 为空 = 全部），返回剩余待审数据。
// 账单审核项没有「撤销订单操作」语义，忽略不会改动预览区。
func DiscardCheckoutAuditReviews(ids []string) []CheckoutAuditItem {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	all := len(ids) == 0
	checkoutAuditMu.Lock()
	defer checkoutAuditMu.Unlock()
	for i := range checkoutAuditRecs {
		r := &checkoutAuditRecs[i]
		if r.Promoted || r.Removed {
			continue
		}
		if all || want[r.Public.ID] {
			r.Removed = true
		}
	}
	return checkoutAuditPendingLocked()
}

// ---------------- 审核主流程 ----------------

// RunCheckoutAudit 执行一轮账单审核。from/to 为 YYYY-MM-DD，来自账单页的日期范围。
func RunCheckoutAudit(from, to string, progress ProgressFunc) CheckoutAuditResult {
	result := CheckoutAuditResult{Warnings: []string{}}
	startDay, endDay, err := ParseDateRange(from, to)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	result.From = startDay.Format("2006-01-02")
	result.To = endDay.Format("2006-01-02")

	// 只做轻量的忙标志，避免长时间持锁阻塞审核区的读取 / 人工操作。
	checkoutAuditMu.Lock()
	if checkoutAuditBusy {
		checkoutAuditMu.Unlock()
		result.Message = "审核正在进行中，请等待本轮完成后再试"
		return result
	}
	checkoutAuditBusy = true
	checkoutAuditMu.Unlock()
	defer func() {
		checkoutAuditMu.Lock()
		checkoutAuditBusy = false
		checkoutAuditMu.Unlock()
	}()

	emit := func(stage string, done, total int, msg string) {
		if progress != nil {
			progress(stage, done, total, msg)
		}
	}

	cfg := LoadConfig()
	// 本轮只改内存里的配置副本：导出用显式范围，提示词的目标日期用范围最后一天，
	// 配置文件不会被写入，下次点「一键识别」仍然默认今天。
	cfg.Days = int(endDay.Sub(startDay)/(24*time.Hour)) + 1
	cfg.AutoTargetDate = false
	cfg.TargetDate = result.To
	if cfg.OnlyToday {
		result.Warnings = append(result.Warnings,
			"配置里的「严格模式（只保留目标日期）」不适用于日期范围审核，本轮已忽略，避免误丢订单")
	}

	emit("export", 0, 1, fmt.Sprintf("正在导出群「%s」在 %s ~ %s 的聊天记录…", cfg.Group, result.From, result.To))
	payload, err := ExportRange(cfg, result.From, result.To)
	if err != nil {
		result.Message = err.Error()
		emit("done", 0, 0, result.Message)
		return result
	}
	result.Source = payload.Source
	result.Exported = payload.Count
	result.Warnings = append(result.Warnings, payload.Warnings...)
	emit("export", 1, 1, fmt.Sprintf("导出完成：%d 条消息（窗口 %s~%s，来源 %s）",
		payload.Count, payload.From, payload.To, payload.Source))

	// 已入库的账单订单：按同一日期范围（本地日历日）取逐单明细，
	// 账单表格是「日期 × 规格」的透视结果，本身没有订单 id / 收件人，无法逐笔核对。
	emit("reconcile", 0, 1, "正在读取该日期范围内已入库的账单订单…")
	bills, err := queryCheckoutOrders(startDay, endDay)
	if err != nil {
		result.Message = err.Error()
		emit("done", 0, 0, result.Message)
		return result
	}
	result.CheckoutOrders = len(bills)

	candidates, dropped := RuleFilterWithConfig(payload.Messages, cfg)
	result.Candidates, result.Dropped = len(candidates), dropped
	filterNote := "规则粗筛"
	if !cfg.UseRuleFilter {
		filterNote = "粗筛已关闭，全量交 AI"
	}
	emit("rule", len(candidates), len(payload.Messages),
		fmt.Sprintf("%s：%d 条交 AI 判定（跳过 %d 条）", filterNote, len(candidates), dropped))

	if len(candidates) == 0 {
		// 没识别到任何订单时，账单里所有订单都成了「群聊里没找到」的线索，
		// 但仍不生成审核项（没有可补录的订单），只在汇总里如实提示。
		result.UnmatchedBills = len(bills)
		result.Success = true
		result.Message = fmt.Sprintf("范围 %s~%s：导出 %d 条消息，未发现疑似订单；账单里有 %d 笔订单，请确认微信导出配置是否正确",
			result.From, result.To, result.Exported, len(bills))
		checkoutAuditReplace(nil, result)
		emit("done", 0, 0, result.Message)
		return result
	}

	items := AnalyzeAllWithOptions(candidates, cfg, AnalyzeOptions{
		FilterOff:  !cfg.UseRuleFilter,
		TargetDate: result.To,
		DateScope:  result.From + " ~ " + result.To,
	}, emit)

	recognized := collectRecognizedOrders(items, startDay, endDay, &result)
	emit("reconcile", 1, 2, fmt.Sprintf("识别到 %d 笔订单，正在与 %d 笔账单订单逐一核对…",
		len(recognized), len(bills)))

	records := reconcileOrders(recognized, bills, startDay, endDay, &result)
	checkoutAuditReplace(records, result)
	emit("done", 1, 1, result.Message)
	logrus.Infof("账单审核完成: %s", result.Message)
	return result
}

func checkoutAuditReplace(records []checkoutAuditRecord, result CheckoutAuditResult) {
	checkoutAuditMu.Lock()
	defer checkoutAuditMu.Unlock()
	checkoutAuditRecs = records
	checkoutAuditLast = result
}

// ---------------- 识别结果收集（复用一键识别的分流语义，按「范围」而非单日） ----------------

type recognizedOrder struct {
	Order      models.AddressInfo
	Message    ChatMessage
	ReviewWhy  string // 非空 = 识别本身存疑，必须进人工审核区
	Confidence string
	DateLabel  string
	Seq        int
}

func collectRecognizedOrders(items []AnalyzeItem, from, to time.Time, result *CheckoutAuditResult) []recognizedOrder {
	var out []recognizedOrder
	seq, specFixed := 0, 0
	for _, item := range items {
		msg := item.Candidate.Message
		if item.Err != nil {
			result.AIFailed++
			continue
		}
		if !item.Result.IsOrder || len(item.Result.Orders) == 0 {
			continue
		}
		orders := item.Result.Orders
		if FixSpecNames(msg.Content, orders) {
			// 与一键识别一致：规格以原文【】的确定性解析为真值
			specFixed++
		}

		dateLabel, hasMark, _ := ResolveOrderDate(item.Result.OrderDate, item.Candidate.OrderDate, to)
		var why string
		switch {
		case !hasMark:
			why = "消息没写日期标记，无法确认这笔订单归属哪一天"
		case !auditDateInRange(dateLabel, from, to):
			why = fmt.Sprintf("订单日期标记 %s 不在本次审核范围 %s~%s 内", dateLabel,
				from.Format("2006-01-02"), to.Format("2006-01-02"))
		case item.Result.NeedsReview:
			why = "AI 建议复核：" + strings.TrimSpace(item.Result.Reason)
		case strings.EqualFold(strings.TrimSpace(item.Result.Confidence), "low"):
			why = "AI 判定置信度偏低：" + strings.TrimSpace(item.Result.Reason)
		}
		if why != "" && strings.TrimSpace(why) == "AI 建议复核：" {
			why = "AI 建议人工复核"
		}

		for j := range orders {
			o := orders[j]
			if msg.Ts > 0 {
				t := time.Unix(int64(msg.Ts), 0).In(time.Local)
				o.CreatedAt, o.UpdatedAt = t, t
			}
			out = append(out, recognizedOrder{
				Order: o, Message: msg, ReviewWhy: why,
				Confidence: item.Result.Confidence, DateLabel: dateLabel, Seq: seq,
			})
			seq++
		}
	}
	result.RecognizedOrders = len(out)
	if specFixed > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("有 %d 条消息的商品规格已按聊天原文自动校正", specFixed))
	}
	return out
}

// auditDateInRange 判断 "M-D" / "YYYY-MM-DD" 形式的日期标记是否落在审核范围内。
func auditDateInRange(label string, from, to time.Time) bool {
	s := strings.TrimSpace(label)
	if s == "" {
		return false
	}
	s = strings.NewReplacer("月", "-", "日", "", "号", "", "/", "-", ".", "-").Replace(s)
	parts := strings.Split(s, "-")
	if len(parts) < 2 {
		return false
	}
	m, err1 := strconv.Atoi(strings.TrimSpace(parts[len(parts)-2]))
	d, err2 := strconv.Atoi(strings.TrimSpace(parts[len(parts)-1]))
	if err1 != nil || err2 != nil || m < 1 || m > 12 || d < 1 || d > 31 {
		return false
	}
	years := []int{to.Year()}
	if from.Year() != to.Year() {
		years = append(years, from.Year())
	}
	for _, y := range years {
		t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.Local)
		if !t.Before(from) && !t.After(to) {
			return true
		}
	}
	return false
}

// ---------------- 逐笔核对 ----------------

func queryCheckoutOrders(from, to time.Time) ([]models.AddressInfo, error) {
	if global.Db == nil {
		return nil, fmt.Errorf("数据库尚未连接，无法读取账单订单")
	}
	begin := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.Local)
	end := time.Date(to.Year(), to.Month(), to.Day(), 23, 59, 59, 0, time.Local)
	var list []models.AddressInfo
	if err := global.Db.Where("created_at >= ? AND created_at <= ?", begin, end).
		Order("created_at asc, id asc").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("读取账单订单失败: %w", err)
	}
	return list, nil
}

type auditProfile struct {
	name    string
	phone   string
	address string
	specs   map[string]int
}

func auditAliasMap() map[string]string {
	out := make(map[string]string)
	for primary, aliases := range global.GetProductBelongs() {
		p := normAuditText(primary)
		if p != "" {
			out[p] = p
		}
		for _, a := range aliases {
			if k := normAuditText(a); k != "" {
				out[k] = p
			}
		}
	}
	return out
}

func buildAuditProfile(o models.AddressInfo, alias map[string]string) auditProfile {
	p := auditProfile{
		name:    normAuditText(o.Recipient),
		phone:   normAuditPhone(o.Phone),
		address: normAuditText(o.Address),
		specs:   make(map[string]int, len(o.ProductSpecs)),
	}
	for _, s := range o.ProductSpecs {
		n := normAuditText(s.Name)
		if primary, ok := alias[n]; ok {
			n = primary
		}
		p.specs[fmt.Sprintf("%s|%d", n, s.Weight)]++
	}
	return p
}

// normAuditText 去掉空白/标点并把全角转半角，避免「张三 」与「张三」被判成不同人。
func normAuditText(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if r >= 0xFF01 && r <= 0xFF5E {
			r -= 0xFEE0
		}
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func normAuditPhone(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	p := b.String()
	if len(p) == 13 && strings.HasPrefix(p, "86") {
		p = p[2:]
	}
	return p
}

// auditTextHit 返回 -1 表示该字段不可比（任一方为空），否则返回 0~1 的命中系数。
func auditTextHit(a, b string) float64 {
	if a == "" || b == "" {
		return -1
	}
	if a == b {
		return 1
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return 0.8
	}
	if auditSimilarity(a, b) >= 0.72 {
		return 0.6
	}
	return 0
}

func auditSimilarity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	max := len(ra)
	if len(rb) > max {
		max = len(rb)
	}
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = minInt(cur[j-1]+1, minInt(prev[j]+1, prev[j-1]+cost))
		}
		prev = cur
	}
	return 1 - float64(prev[len(rb)])/float64(max)
}

func auditSpecSimilarity(a, b map[string]int) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	exact := len(a) == len(b)
	if exact {
		for k, v := range a {
			if b[k] != v {
				exact = false
				break
			}
		}
	}
	if exact {
		return 1
	}
	inter := 0
	for k, v := range a {
		if bv, ok := b[k]; ok {
			inter += minInt(v, bv)
		}
	}
	if inter == 0 {
		return 0
	}
	if auditSubset(a, b) || auditSubset(b, a) {
		return 0.6
	}
	return 0.3
}

func auditSubset(a, b map[string]int) bool {
	for k, v := range a {
		if b[k] < v {
			return false
		}
	}
	return true
}

// compareAuditProfiles 给两笔订单打分：收件人 35 / 手机 25 / 地址 20 / 规格 20。
// 只有「姓名、手机、地址」至少一项强命中才认作同一客户，避免不同客户买了
// 同样的规格就被当成同一单。
func compareAuditProfiles(a, b auditProfile) (int, float64) {
	total, got := 0.0, 0.0
	identity := false
	hn := auditTextHit(a.name, b.name)
	if hn >= 0 {
		total += 35
		got += 35 * hn
		identity = identity || hn >= 0.8
	}
	hp := auditTextHit(a.phone, b.phone)
	if hp < 0.5 && len(a.phone) >= 7 && len(b.phone) >= 7 &&
		(strings.HasSuffix(a.phone, b.phone) || strings.HasSuffix(b.phone, a.phone)) {
		hp = 0.5
	}
	if hp >= 0 {
		total += 25
		got += 25 * hp
		identity = identity || hp >= 0.8
	}
	ha := auditTextHit(a.address, b.address)
	if ha >= 0 {
		total += 20
		got += 20 * ha
		identity = identity || ha >= 0.8
	}
	sim := auditSpecSimilarity(a.specs, b.specs)
	if len(a.specs) > 0 && len(b.specs) > 0 {
		total += 20
		got += 20 * sim
	}
	if !identity || total == 0 {
		return 0, sim
	}
	return int(got * 100 / total), sim
}

type auditPair struct {
	ri, bi, score int
	sim           float64
}

func reconcileOrders(recognized []recognizedOrder, bills []models.AddressInfo,
	from, to time.Time, result *CheckoutAuditResult) []checkoutAuditRecord {

	alias := auditAliasMap()
	rp := make([]auditProfile, len(recognized))
	for i, r := range recognized {
		rp[i] = buildAuditProfile(r.Order, alias)
	}
	bp := make([]auditProfile, len(bills))
	for i, b := range bills {
		bp[i] = buildAuditProfile(b, alias)
	}

	// 用姓名 / 手机 / 地址三个精确键建索引，只对「至少共享一个强键」的账单订单
	// 做完整打分。范围审核可能一次面对几千笔订单，全量两两比较（含编辑距离）
	// 会拖到几分钟；不同客户的订单本来也不该被当成同一单。
	byName := make(map[string][]int)
	byPhone := make(map[string][]int)
	byAddr := make(map[string][]int)
	for j := range bp {
		if bp[j].name != "" {
			byName[bp[j].name] = append(byName[bp[j].name], j)
		}
		if bp[j].phone != "" {
			byPhone[bp[j].phone] = append(byPhone[bp[j].phone], j)
		}
		if bp[j].address != "" {
			byAddr[bp[j].address] = append(byAddr[bp[j].address], j)
		}
	}

	// 全局贪心：先算所有候选配对的分数，按分数从高到低一对一占用，
	// 一笔账单订单只能被认领一次（同一客户当天下两单不会被合并成一单）。
	var pairs []auditPair
	for i := range recognized {
		cand := make(map[int]struct{}, 8)
		for _, j := range byName[rp[i].name] {
			cand[j] = struct{}{}
		}
		for _, j := range byPhone[rp[i].phone] {
			cand[j] = struct{}{}
		}
		for _, j := range byAddr[rp[i].address] {
			cand[j] = struct{}{}
		}
		// 退化保护：若所有人都填了同一个地址，地址索引会把全表拉进来，
		// 这时收窄到姓名 / 电话命中的订单。
		if len(cand) > 300 {
			narrow := make(map[int]struct{})
			for _, j := range byName[rp[i].name] {
				narrow[j] = struct{}{}
			}
			for _, j := range byPhone[rp[i].phone] {
				narrow[j] = struct{}{}
			}
			if len(narrow) > 0 {
				cand = narrow
			}
		}
		for j := range cand {
			score, sim := compareAuditProfiles(rp[i], bp[j])
			if score >= 55 {
				pairs = append(pairs, auditPair{ri: i, bi: j, score: score, sim: sim})
			}
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].score != pairs[j].score {
			return pairs[i].score > pairs[j].score
		}
		return pairs[i].ri < pairs[j].ri
	})
	matchOf := make([]int, len(recognized))
	scoreOf := make([]int, len(recognized))
	simOf := make([]float64, len(recognized))
	for i := range matchOf {
		matchOf[i] = -1
	}
	usedBill := make([]bool, len(bills))
	for _, p := range pairs {
		if matchOf[p.ri] >= 0 || usedBill[p.bi] {
			continue
		}
		matchOf[p.ri], scoreOf[p.ri], simOf[p.ri] = p.bi, p.score, p.sim
		usedBill[p.bi] = true
	}

	var records []checkoutAuditRecord
	for i, r := range recognized {
		bi := matchOf[i]
		var matchRows []ordertutil.ExceladdressInfo
		matchedAt := ""
		if bi >= 0 {
			row := ordertutil.AddressToExcelAddress([]models.AddressInfo{bills[bi]})
			matchRows = append(matchRows, row...)
			matchedAt = bills[bi].CreatedAt.Format("2006-01-02 15:04")
		}
		orderRows := ordertutil.AddressToExcelAddress([]models.AddressInfo{r.Order})

		kind, kindLabel, reason := "", "", ""
		switch {
		case r.ReviewWhy != "":
			kind, kindLabel = "review", "识别存疑"
			reason = r.ReviewWhy
			if bi >= 0 {
				reason += fmt.Sprintf("；账单里另有一笔相似度 %d%% 的订单，请一并核对", scoreOf[i])
			}
		case bi >= 0 && scoreOf[i] >= 85 && simOf[i] >= 0.6:
			result.Matched++
			continue // 核对一致，不进人工审核区
		case bi >= 0:
			kind, kindLabel = "uncertain", "核对存疑"
			reason = fmt.Sprintf("账单里有一笔相似度 %d%% 的订单，但收件人 / 地址 / 商品规格不完全一致，可能是改单、补单或重复录入，请人工确认", scoreOf[i])
		default:
			kind, kindLabel = "missing", "疑似漏单"
			reason = fmt.Sprintf("在 %s~%s 的已入库账单订单里没有找到对应记录，可能是漏录或识别差异，请核对后决定是否补录",
				from.Format("2006-01-02"), to.Format("2006-01-02"))
		}
		if kind == "missing" {
			result.Missing++
		} else {
			result.Uncertain++
		}

		label := ""
		if len(matchRows) > 0 {
			label = fmt.Sprintf("%s · %s · %s",
				matchRows[0].Recipient, matchRows[0].Phone, matchRows[0].ProductSpecs)
		}
		records = append(records, checkoutAuditRecord{
			Public: CheckoutAuditItem{
				ID: fmt.Sprintf("audit-%s-%d", r.Message.ID, r.Seq),
				Kind: kind, KindLabel: kindLabel, Reason: reason,
				Confidence:  r.Confidence,
				Sender:      r.Message.Sender,
				MessageTime: r.Message.Time,
				MessageDate: r.Message.Date,
				OrderDate:   r.DateLabel,
				RawContent:  r.Message.Content,
				OrderCount:  1,
				Orders:      orderRows,
				MatchScore:  scoreOf[i],
				MatchLabel:  label,
				MatchedAt:   matchedAt,
				MatchOrders: matchRows,
			},
			Raw: []models.AddressInfo{r.Order},
		})
	}

	for _, used := range usedBill {
		if !used {
			result.UnmatchedBills++
		}
	}
	result.ReviewCount = len(records)
	result.Success = true
	result.Message = fmt.Sprintf("范围 %s~%s：导出 %d 条消息 → 识别 %d 笔订单，账单 %d 笔；核对一致 %d 笔，疑似漏单 %d 笔，存疑 %d 笔，已放入人工审核区",
		result.From, result.To, result.Exported, result.RecognizedOrders,
		result.CheckoutOrders, result.Matched, result.Missing, result.Uncertain)
	if result.AIFailed > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("有 %d 条消息 AI 判定失败，可能漏识别，建议缩小日期范围后重审一次", result.AIFailed))
	}
	if result.UnmatchedBills > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("账单里有 %d 笔订单没在群聊识别结果里找到对应项（可能是群聊导出不全 / AI 未识别 / 订单来自其它渠道）", result.UnmatchedBills))
	}
	return records
}
