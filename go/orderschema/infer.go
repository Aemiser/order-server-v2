package orderschema

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FieldProposer 是「样例 → 字段草案」的可插拔接口。
// 生产接入时把它换成 DeepSeek/Qwen 实现即可，归纳流程与确认纪律不变。
type FieldProposer interface {
	Propose(ctx context.Context, samples []string, builtin FieldDefs) ([]ProposedField, error)
}

// ProposedField 是提议字段 + 它在样例中的出现频率(0~1)。
type ProposedField struct {
	Def       FieldDef
	Frequency float64
}

var labelLinePattern = regexp.MustCompile(`^\s*([^\s：:，,【】]{1,12})\s*[：:]\s*(.+?)\s*$`)
var qtyPattern = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(斤|公斤|千克|kg|KG|g|克|件|箱|份|个|杯)`)
var specBlockPattern = regexp.MustCompile(`【([^】]+)】`)
var dateLikePattern = regexp.MustCompile(`^\d{1,4}[-/月]\d{1,2}(?:[-/日]\d{1,2})?$`)
var pureNumberPattern = regexp.MustCompile(`^\d+(?:\.\d+)?$`)

// HeuristicProposer 是不联网的确定性兜底：从「标签：值」、手机号、
// 数量单位和【】规格块里归纳字段。LLM 失败或离线时用它。
type HeuristicProposer struct{}

func (HeuristicProposer) Propose(_ context.Context, samples []string, _ FieldDefs) ([]ProposedField, error) {
	type stat struct {
		label  string
		count  map[int]bool
		values []string
	}
	stats := map[string]*stat{}
	for i, s := range samples {
		for _, line := range strings.Split(s, "\n") {
			m := labelLinePattern.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			label := strings.TrimSpace(m[1])
			value := strings.TrimSpace(m[2])
			k := NormalizeKey(label)
			if k == "" {
				continue
			}
			st := stats[k]
			if st == nil {
				st = &stat{label: label, count: map[int]bool{}}
				stats[k] = st
			}
			st.count[i] = true
			st.values = append(st.values, value)
		}
	}

	out := make([]ProposedField, 0, len(stats))
	for _, st := range stats {
		freq := float64(len(st.count)) / float64(len(samples))
		def := FieldDef{
			Label:   st.label,
			Type:    guessType(st.values),
			Aliases: []string{st.label},
			Extract: ExtractRule{Kind: ExtractLabel, Labels: []string{st.label}},
		}
		if canonical, ok := CanonicalKey(st.label); ok {
			def.Key = canonical
		} else {
			def.Key = CustomKey(st.label)
		}
		if def.Type == FieldPhone {
			def.Extract = ExtractRule{Kind: ExtractComposite, Labels: []string{st.label}, Regex: `1[3-9]\d{9}`}
		}
		out = append(out, ProposedField{Def: def, Frequency: freq})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Def.Key < out[j].Def.Key })
	return out, nil
}

func guessType(values []string) FieldType {
	for _, v := range values {
		if phonePattern.MatchString(v) {
			return FieldPhone
		}
		if qtyPattern.MatchString(v) {
			return FieldMeasure
		}
		if dateLikePattern.MatchString(v) {
			return FieldDate
		}
		if pureNumberPattern.MatchString(v) {
			return FieldNumber
		}
	}
	return FieldString
}

// InferOptions 控制一次模板归纳。
type InferOptions struct {
	ID           string
	Name         string
	Description  string
	Proposer     FieldProposer
	MinFrequency float64 // 出现比例达到该值才设为必填，默认 0.6
	MaxSamples   int     // 默认 20
}

// AliasMerge 记录一次别名收敛，用于 UI 展示「为什么没有新增字段」。
type AliasMerge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// SampleCoverage 是每条样例的字段覆盖情况。
type SampleCoverage struct {
	SampleIndex     int      `json:"sample_index"`
	MatchedFields   []string `json:"matched_fields"`
	MissingRequired []string `json:"missing_required,omitempty"`
}

// InferReport 是归纳报告，供 Schema Studio 展示与人工确认。
type InferReport struct {
	SampleCount int              `json:"sample_count"`
	Proposals   []FieldProposal  `json:"proposals"`
	AliasMerges []AliasMerge     `json:"alias_merges,omitempty"`
	Signatures  []Signature      `json:"signatures,omitempty"`
	Coverage    []SampleCoverage `json:"coverage,omitempty"`
}

// InferTemplate 从样例生成 draft 模板。关键纪律：
// 内置字段默认都带上(可不必填)，自定义字段只生成提议，不自动写进模板。
func InferTemplate(samples []string, opt InferOptions) (*Template, *InferReport, error) {
	clean := make([]string, 0, len(samples))
	for _, s := range samples {
		if strings.TrimSpace(s) != "" {
			clean = append(clean, strings.TrimSpace(s))
		}
	}
	maxSamples := opt.MaxSamples
	if maxSamples <= 0 {
		maxSamples = 20
	}
	if len(clean) == 0 {
		return nil, nil, fmt.Errorf("至少需要 1 条订单样例")
	}
	if len(clean) > maxSamples {
		return nil, nil, fmt.Errorf("样例最多 %d 条，当前 %d 条", maxSamples, len(clean))
	}
	minFreq := opt.MinFrequency
	if minFreq <= 0 || minFreq > 1 {
		minFreq = 0.6
	}
	proposer := opt.Proposer
	if proposer == nil {
		proposer = HeuristicProposer{}
	}

	proposed, err := proposer.Propose(context.Background(), clean, BuiltinFields())
	if err != nil {
		return nil, nil, fmt.Errorf("字段归纳失败: %w", err)
	}

	base := BuiltinFields()
	proposedDefs := make(FieldDefs, 0, len(proposed))
	freqByKey := make(map[string]float64, len(proposed))
	aliasMerges := make([]AliasMerge, 0)
	for _, p := range proposed {
		proposedDefs = append(proposedDefs, p.Def)
		key := p.Def.Key
		if c, ok := CanonicalKey(p.Def.Label); ok {
			key = c
			aliasMerges = append(aliasMerges, AliasMerge{From: p.Def.Label, To: c})
		}
		if p.Frequency > freqByKey[key] {
			freqByKey[key] = p.Frequency
		}
	}

	fields, proposals := MergeProposed(base, proposedDefs)
	for i := range proposals {
		key := proposals[i].Field.Key
		if proposals[i].MergedInto != "" {
			key = proposals[i].MergedInto
		}
		proposals[i].Frequency = freqByKey[key]
	}
	for i := range fields {
		fields[i].Required = freqByKey[fields[i].Key] >= minFreq
	}

	id := strings.TrimSpace(opt.ID)
	if id == "" {
		id = "tpl_" + hashText(clean[0])[:12]
	}
	name := strings.TrimSpace(opt.Name)
	if name == "" {
		name = "未命名订单模板"
	}

	hasSpecBlock := false
	for _, s := range clean {
		if specBlockPattern.MatchString(s) {
			hasSpecBlock = true
			break
		}
	}
	items := ItemDef{Enabled: hasSpecBlock || hasQuantity(clean), AllowAttrs: true}
	if hasSpecBlock {
		items.NameLabels = []string{"商品", "品名", "规格"}
		items.QuantityUnit = "斤"
	}

	t := &Template{
		ID: id, Name: name, Description: opt.Description,
		Version: 1, Status: TemplateDraft,
		Fields: fields, Items: items,
		Signatures:  buildSignatures(clean, proposedDefs, hasSpecBlock),
		SampleCount: len(clean), CreatedAt: time.Now(),
	}
	report := &InferReport{
		SampleCount: len(clean), Proposals: proposals,
		AliasMerges: aliasMerges, Signatures: t.Signatures,
	}

	rows := Replay(t, clean)
	for _, row := range rows {
		matched := make([]string, 0, len(row.Fields))
		for k := range row.Fields {
			matched = append(matched, k)
		}
		sort.Strings(matched)
		report.Coverage = append(report.Coverage, SampleCoverage{
			SampleIndex: row.SampleIndex, MatchedFields: matched,
			MissingRequired: row.MissingRequired,
		})
	}
	return t, report, nil
}

func hasQuantity(samples []string) bool {
	for _, s := range samples {
		if qtyPattern.MatchString(s) {
			return true
		}
	}
	return false
}

func buildSignatures(samples []string, proposed FieldDefs, hasSpecBlock bool) Signatures {
	labelFreq := map[string]int{}
	for _, f := range proposed {
		for _, l := range f.Extract.Labels {
			labelFreq[l]++
		}
	}
	labels := make([]string, 0, len(labelFreq))
	for l := range labelFreq {
		labels = append(labels, l)
	}
	sort.Slice(labels, func(i, j int) bool {
		if labelFreq[labels[i]] != labelFreq[labels[j]] {
			return labelFreq[labels[i]] > labelFreq[labels[j]]
		}
		return labels[i] < labels[j]
	})
	if len(labels) > 8 {
		labels = labels[:8]
	}

	keywords := append([]string{}, labels...)
	seps := make([]string, 0, 3)
	if hasSpecBlock {
		seps = append(seps, "【", "】")
	}
	if strings.Contains(strings.Join(samples, ""), "，") {
		seps = append(seps, "，")
	}
	if strings.Contains(strings.Join(samples, ""), "x") || strings.Contains(strings.Join(samples, ""), "×") {
		seps = append(seps, "x")
	}
	return Signatures{{Keywords: keywords, Labels: labels, Separators: seps}}
}

// ReplayRow 是「套用草案重新抽一遍样例」的结果，
// 让用户看到实际抽取效果再确认，比读字段定义直观得多。
type ReplayRow struct {
	SampleIndex     int                   `json:"sample_index"`
	Text            string                `json:"text"`
	Fields          map[string]FieldValue `json:"fields"`
	Items           []LineItem            `json:"items,omitempty"`
	MissingRequired []string              `json:"missing_required,omitempty"`
	NeedsLLM        []string              `json:"needs_llm,omitempty"`
}

// Replay 用模板的确定性抽取规则回放样例。
func Replay(t *Template, samples []string) []ReplayRow {
	rows := make([]ReplayRow, 0, len(samples))
	for i, s := range samples {
		row := ReplayRow{SampleIndex: i, Text: s, Fields: map[string]FieldValue{}}
		for _, f := range t.Fields {
			if f.Deprecated {
				continue
			}
			v, ok := ExtractByRule(f.Extract, s)
			if ok {
				row.Fields[f.Key] = v
			} else if f.Extract.Kind == ExtractLLM {
				row.NeedsLLM = append(row.NeedsLLM, f.Key)
			}
			if f.Required {
				if _, exists := row.Fields[f.Key]; !exists && f.Extract.Kind != ExtractLLM {
					row.MissingRequired = append(row.MissingRequired, f.Key)
				}
			}
		}
		if t.Items.Enabled {
			row.Items = extractItems(s)
		}
		rows = append(rows, row)
	}
	return rows
}

// ExtractByRule 是运行时确定性抽取器。LLM 类型返回 false，由上层标记待补全。
func ExtractByRule(rule ExtractRule, text string) (FieldValue, bool) {
	switch rule.Kind {
	case ExtractLabel:
		return extractByLabels(rule.Labels, text)
	case ExtractRegex:
		if rule.Regex == "" {
			return FieldValue{}, false
		}
		re, err := regexp.Compile(rule.Regex)
		if err != nil {
			return FieldValue{}, false
		}
		m := re.FindStringSubmatch(text)
		if m == nil {
			return FieldValue{}, false
		}
		val := m[0]
		if len(m) > 1 {
			val = m[1]
		}
		return FieldValue{Value: strings.TrimSpace(val), Raw: strings.TrimSpace(val), Source: "regex", Confidence: 0.95}, true
	case ExtractComposite:
		if v, ok := extractByLabels(rule.Labels, text); ok {
			return v, true
		}
		return ExtractByRule(ExtractRule{Kind: ExtractRegex, Regex: rule.Regex}, text)
	case ExtractLLM:
		return FieldValue{}, false
	}
	return FieldValue{}, false
}

func extractByLabels(labels []string, text string) (FieldValue, bool) {
	// 长标签优先，避免「地址」抢在「收货地址」前面命中。
	ordered := append([]string{}, labels...)
	sort.Slice(ordered, func(i, j int) bool { return len([]rune(ordered[i])) > len([]rune(ordered[j])) })
	for _, label := range ordered {
		if label == "" {
			continue
		}
		re, err := regexp.Compile(`(?:^|[\s　])` + regexp.QuoteMeta(label) + `\s*[：:]\s*([^\r\n]+)`)
		if err != nil {
			continue
		}
		m := re.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		val := strings.TrimSpace(m[1])
		if val == "" {
			continue
		}
		return FieldValue{Value: val, Raw: val, Source: "label", Confidence: 0.98}, true
	}
	return FieldValue{}, false
}

func extractItems(text string) []LineItem {
	items := make([]LineItem, 0)
	blocks := specBlockPattern.FindAllStringSubmatch(text, -1)
	for _, b := range blocks {
		raw := strings.TrimSpace(b[1])
		item := LineItem{Name: raw}
		if m := qtyPattern.FindStringSubmatch(raw); m != nil {
			n, _ := strconv.ParseFloat(m[1], 64)
			item.Qty = Measure{Value: n, Unit: NormalizeUnit(m[2])}
			item.Name = strings.TrimSpace(strings.Split(raw, m[0])[0])
			if item.Name == "" {
				item.Name = raw
			}
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		// 退化为「商品：xxx x2」这类单行文
		for _, line := range strings.Split(text, "\n") {
			if !strings.Contains(line, "x") && !strings.Contains(line, "×") {
				continue
			}
			v, ok := extractByLabels([]string{"商品", "品名", "项目", "服务项目"}, line)
			if ok {
				s, _ := valueString(v.Value)
				parts := regexp.MustCompile(`[x×]\s*(\d+(?:\.\d+)?)`).FindStringSubmatch(s)
				name := strings.TrimSpace(regexp.MustCompile(`[x×]\s*\d+(?:\.\d+)?`).ReplaceAllString(s, ""))
				item := LineItem{Name: name}
				if parts != nil {
					n, _ := strconv.ParseFloat(parts[1], 64)
					item.Qty = Measure{Value: n, Unit: "件"}
				}
				items = append(items, item)
			}
		}
	}
	return items
}

// Publish 把 draft 模板发布。已发布版本不可再改，只能 CloneForEdit 出新版本。
func Publish(t *Template) error {
	if t == nil {
		return fmt.Errorf("模板为空")
	}
	if t.Status == TemplatePublished {
		return fmt.Errorf("v%d 已发布，修改请先用 CloneForEdit 生成新版本", t.Version)
	}
	if err := CheckTemplate(t); err != nil {
		return err
	}
	now := time.Now()
	t.Status = TemplatePublished
	t.PublishedAt = &now
	return nil
}

// CloneForEdit 从已发布模板派生新版本，老订单仍指向旧版本。
func CloneForEdit(t *Template) (*Template, error) {
	if t == nil {
		return nil, fmt.Errorf("模板为空")
	}
	next := cloneTemplate(t)
	next.Version = t.Version + 1
	next.Status = TemplateDraft
	next.PublishedAt = nil
	next.CreatedAt = time.Now()
	next.SampleCount = 0
	return next, nil
}
