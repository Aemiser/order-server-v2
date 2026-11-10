package wechatorder

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"order_server/go/models"
	ordertutil "order_server/go/utils/order"
)

// Candidate 是规则引擎粗筛后、准备交给 DeepSeek 的疑似订单消息。
type Candidate struct {
	Message   ChatMessage
	Score     int
	Reasons   []string
	OrderDate string // 消息首行的订单日期标记，如 "11-7"
}

var (
	// ---- 下单要素（弱信号）：命中任意一条就直接放行给 AI，绝不在规则层丢单 ----
	reStore     = regexp.MustCompile(`(店名|门店|店铺|店址|收货人|收件人|联络人|联系人)`)
	reAddress   = regexp.MustCompile(`(地址|店址|收货地址)`)
	rePhoneWord = regexp.MustCompile(`(电话|联系电话|手机|手机号|固话|联系方式)`)
	rePhoneNum  = regexp.MustCompile(`1[3-9]\d{9}|0\d{2,3}-?\d{7,8}`)
	reSpec      = regexp.MustCompile(`【[^】]{1,40}】`)
	reBrand     = regexp.MustCompile(`(1點點|1点点|一點點|一点点|奶茶店)`)
	// 不带「地址：」标签的纯文本地址：省市区连续两级、路/街 + 门牌号、或独立门牌号。
	// 只认地址结构，不认单个「区/市/店」字，避免把「工作区」「学园都市」误判成地址。
	reAddressish = regexp.MustCompile(`\p{Han}{2,}(?:省|市|区|县|镇)\s*\p{Han}{2,}(?:省|市|区|县|镇)|\p{Han}{2,}(?:路|街|大道|巷)\s*\d+\s*号?|\d{2,4}\s*号`)
	// 不带【】的「品名 + 数量」商品行，如「黄柠檬 8斤」
	reQuantity = regexp.MustCompile(`\d+(?:\.\d+)?\s*(斤|公斤|kg|KG|件|箱|包|袋|瓶|桶|份|盒)`)
	// 群游戏 / 视频分享里的【答案】【发音】等不是商品行，不计入下单要素
	reSpecNoise = regexp.MustCompile(`^【(答案|发音|解释|释义|出处|原创编舞|提示|规则|公告|题目|成语|内容|标题|选项)】$`)

	// ---- 明确不是顾客下单：机器人 / 订单管理后台的回显，优先排除避免重复采集 ----
	reBotEcho = regexp.MustCompile(`(管理界面|管理命令|置信\s*0?\.?\d+|采集：|今日订单\s*\d+\s*笔|界面：http|订单\s+[0-9a-f]{8,}|累计\s*\d+\s*笔|/订单)`)
	// 没有任何具体要素、但明显在谈订单的灰度消息，兜底放行给 AI 判定
	reOrderIntent = regexp.MustCompile(`(下单|补单|加单|改单|送货|送到|发货|收货|要货|订(?:单|货)(?:吗|吧|了|没|信息|内容)|多少斤|几斤)`)

	reDateLine   = regexp.MustCompile(`^\s*(\d{1,2})\s*[-/月.]\s*(\d{1,2})\s*(?:日|号)?\s*$`)
	reDatePrefix = regexp.MustCompile(`^\s*(\d{1,2})\s*[-/月.]\s*(\d{1,2})\s*(?:日|号)?(?:\s|$|[^0-9])`)
	reDateAny    = regexp.MustCompile(`(\d{1,2})\s*[-/月.]\s*(\d{1,2})\s*(?:日|号)?`)
)

// ExtractOrderDate 只从消息开头几行抽取日期标记（如 "11-7"），
// 不在长文本里随意找数字，避免把地址/楼号误当成订单日期。
func ExtractOrderDate(content string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for i := 0; i < minInt(3, len(lines)); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if m := reDatePrefix.FindStringSubmatch(line); m != nil {
			return m[1] + "-" + m[2]
		}
	}
	return ""
}

// inspectMessage 统计消息里出现的「下单要素」。分数只用于结果明细展示与排查，
// 不再作为放行门槛——过去按 score>=5 卡阈值，会把「有手机号/地址/商品行、但没写
// 『店名：』『地址：』标签」的真实订单误杀。
func inspectMessage(text string) (score int, reasons []string, orderDate string) {
	add := func(n int, why string) {
		score += n
		reasons = append(reasons, why)
	}
	if reStore.MatchString(text) {
		add(2, "含店名/收件人")
	}
	if reAddress.MatchString(text) {
		add(2, "含收货地址")
	}
	if rePhoneWord.MatchString(text) {
		add(1, "含电话字段")
	}
	if rePhoneNum.MatchString(text) {
		add(1, "含电话号码")
	}
	// 【】里必须至少有一段不像群游戏/分享的占位标签，才算商品行
	for _, seg := range reSpec.FindAllString(text, -1) {
		if !reSpecNoise.MatchString(seg) {
			add(2, "含【商品规格】")
			break
		}
	}
	if reBrand.MatchString(text) {
		add(1, "含门店品牌")
	}
	if reAddressish.MatchString(text) {
		add(2, "含纯文本地址/门牌")
	}
	if reQuantity.MatchString(text) {
		add(1, "含数量/斤两")
	}
	orderDate = ExtractOrderDate(text)
	if orderDate != "" {
		add(1, "含日期标记 "+orderDate)
	}
	return
}

// RuleFilter 是「粗筛中间件」，职责只有一件事：简单过滤掉明显不是订单的信息。
// 真正「是不是订单、字段怎么提」一律交给 AI 层判定。
//
//  1. 机器人 / 订单管理后台的回显先排除（含店名电话但是系统生成，不是顾客下单）；
//  2. 命中任意下单要素（手机号、纯文本地址、【商品行】、数量斤两、日期、店名/电话标签…）
//     → 一律放行，不再设分数门槛，闲聊关键词也不再对其扣分；
//  3. 完全没有要素，但明显在谈订单（「能送到吗」「补单」等）→ 兜底放行给 AI；
//  4. 其余（天气、游戏播报、寒暄、媒体占位、系统提示）→ 过滤掉。
//
// 设计取舍：宁可让 AI 多判一条，也不能在这里丢掉真订单。旧版按 score>=5 卡阈值，
// 会把「有手机号/地址/商品行、但没写『店名：』『地址：』标签」的真实订单误杀。
func RuleFilter(messages []ChatMessage) (candidates []Candidate, dropped int) {
	for _, m := range messages {
		text := strings.TrimSpace(m.Content)
		if text == "" {
			dropped++
			continue
		}
		if reBotEcho.MatchString(text) {
			dropped++
			continue
		}
		score, reasons, od := inspectMessage(text)
		if score == 0 {
			if !reOrderIntent.MatchString(text) {
				dropped++
				continue
			}
			reasons = append(reasons, "无具体要素，疑似订单对话，交 AI 判定")
		}
		candidates = append(candidates, Candidate{
			Message:   m,
			Score:     score,
			Reasons:   reasons,
			OrderDate: od,
		})
	}
	return
}

// RuleFilterWithConfig 按配置决定是否启用粗筛中间件。
// 开关关闭时不作任何订单/闲聊预判，聊天消息全部作为候选直接交给 AI 层，
// 由提示词里的「全量消息模式」判定依据区分订单数据与聊天数据。
func RuleFilterWithConfig(messages []ChatMessage, cfg Config) (candidates []Candidate, dropped int) {
	if !cfg.UseRuleFilter {
		return allCandidates(messages)
	}
	return RuleFilter(messages)
}

// allCandidates 只跳过完全空白的消息（送进模型没有意义），其余一律放行，
// 同时保留要素得分与日期标记，供结果明细展示与后续分流使用。
func allCandidates(messages []ChatMessage) (candidates []Candidate, dropped int) {
	candidates = make([]Candidate, 0, len(messages))
	for _, m := range messages {
		text := strings.TrimSpace(m.Content)
		if text == "" {
			dropped++
			continue
		}
		score, reasons, od := inspectMessage(text)
		if score == 0 {
			reasons = append(reasons, "粗筛已关闭，全量交 AI 判定")
		}
		candidates = append(candidates, Candidate{
			Message: m, Score: score, Reasons: reasons, OrderDate: od,
		})
	}
	return
}

// IsTodayOrder 判断订单归属日期是否为今天。
// orderDate 优先取 AI 返回的日期标记，其次取规则引擎从首行解析的，最后退回消息发送日期。
func IsTodayOrder(aiDate, ruleDate, msgDate string, now time.Time) (bool, string) {
	today := now.Format("2006-01-02")
	date := strings.TrimSpace(aiDate)
	if date == "" {
		date = strings.TrimSpace(ruleDate)
	}
	if date != "" {
		if m, d, ok := parseMonthDay(date); ok {
			return m == int(now.Month()) && d == now.Day(), date
		}
		if len(date) >= 10 {
			if t, err := time.ParseInLocation("2006-01-02", date[:10], now.Location()); err == nil {
				return t.Format("2006-01-02") == today, date
			}
		}
	}
	return msgDate == today || msgDate == "", msgDate
}

// ResolveOrderDate 汇总 AI 与规则提取的订单日期，规范化成 "M-D"，
// 并给出「是否有明确日期标记」「是否命中目标日期」两个信号。
// 与 IsTodayOrder 不同：这里绝不用消息发送日期兜底——没写日期标记的订单
// 必须走人工审查，不能被悄悄当作目标日期的订单自动导入预览区。
func ResolveOrderDate(aiDate, ruleDate string, target time.Time) (label string, hasMark bool, matches bool) {
	raw := strings.TrimSpace(aiDate)
	if raw == "" {
		raw = strings.TrimSpace(ruleDate)
	}
	if raw == "" {
		return "", false, false
	}
	if m, d, ok := parseMonthDay(raw); ok {
		label = strconv.Itoa(m) + "-" + strconv.Itoa(d)
		return label, true, m == int(target.Month()) && d == target.Day()
	}
	if len(raw) >= 10 {
		if t, err := time.ParseInLocation("2006-01-02", raw[:10], target.Location()); err == nil {
			return t.Format("1-2"), true, t.Format("2006-01-02") == target.Format("2006-01-02")
		}
	}
	return raw, true, false
}

func parseMonthDay(s string) (int, int, bool) {
	m := reDateLine.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		m = reDateAny.FindStringSubmatch(s)
	}
	if m == nil {
		return 0, 0, false
	}
	mo, err1 := strconv.Atoi(m[1])
	d, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil || mo < 1 || mo > 12 || d < 1 || d > 31 {
		return 0, 0, false
	}
	return mo, d, true
}

// FixSpecNames 以「原文确定性解析出的商品规格」为真值，校正 AI 输出。
//
// 旧实现只取出原文品名集合，只要集合里只有一个名字，就把 AI 输出的所有规格
// 都改成那个名字。遇到「【广香5斤+黄柠檬3斤】」时，旧解析器给出的唯一名字是
// 「广香5斤+黄柠檬」，于是正确的 {广香,5}/{黄柠檬,3} 被刷成
// {广香5斤+黄柠檬,5}/{广香5斤+黄柠檬,3}，前端就成了
// 「【广香5斤+黄柠檬5斤】,【广香5斤+黄柠檬3斤】」。
//
// 现在逐项对齐 (name, weight)：单笔订单直接用原文规格整体替换，
// 多笔订单按品名/重量逐项纠正，绝不把整段组合文字当品名。
// 返回是否发生了修正，供识别结果给出提示。
func FixSpecNames(content string, orders []models.AddressInfo) bool {
	if len(orders) == 0 {
		return false
	}
	truth, err := ordertutil.Specprocessing(content)
	if err != nil || len(truth) == 0 {
		return false // 原文解析不出规格时保留 AI 结果
	}

	// 单笔订单：整条消息里的规格都属于这一笔，直接用原文真值整体替换，
	// 顺带杜绝模型编造 / 复制规格条目、改写重量。
	if len(orders) == 1 {
		if sameSpecs(orders[0].ProductSpecs, truth) {
			return false
		}
		orders[0].ProductSpecs = cloneSpecs(truth)
		return true
	}

	// 多笔订单：原文规格是全局真值集合，逐项按品名或重量对齐，
	// 不新增 / 删除条目，避免把别的订单的规格串进来。
	byName := make(map[string]models.ProductSpec, len(truth))
	byWeight := make(map[int][]models.ProductSpec, len(truth))
	for _, t := range truth {
		byName[t.Name] = t
		byWeight[t.Weight] = append(byWeight[t.Weight], t)
	}
	changed := false
	for i := range orders {
		for j := range orders[i].ProductSpecs {
			got := orders[i].ProductSpecs[j]
			name := strings.TrimSpace(got.Name)
			if t, ok := byName[name]; ok {
				if t.Weight != got.Weight {
					orders[i].ProductSpecs[j].Weight = t.Weight
					changed = true
				}
				continue
			}
			// 品名被模型补全/改写（如「广香」→「广香柠檬」）：用同重量的原文项纠正
			if cands := byWeight[got.Weight]; len(cands) == 1 {
				orders[i].ProductSpecs[j].Name = cands[0].Name
				changed = true
				continue
			}
			for _, t := range truth {
				if name != "" && (strings.Contains(t.Name, name) || strings.Contains(name, t.Name)) {
					orders[i].ProductSpecs[j] = t
					changed = true
					break
				}
			}
			// 仍无法对应时保留原值，交给人工审查，不擅自删条目
		}
	}
	return changed
}

func sameSpecs(a, b models.ProductSpecsJSON) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i].Name) != strings.TrimSpace(b[i].Name) || a[i].Weight != b[i].Weight {
			return false
		}
	}
	return true
}

func cloneSpecs(in models.ProductSpecsJSON) models.ProductSpecsJSON {
	out := make(models.ProductSpecsJSON, len(in))
	copy(out, in)
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
