package orderschema

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// CompletionFunc 是 LLM 调用抽象。上层把现有的 DeepSeek/Qwen 客户端包一层
// 注入即可，本包不依赖任何 AI SDK、不读密钥、不落凭据。
//
//	llm := orderschema.LLMProposer{Complete: func(ctx, system, user string) (string, error) {
//	    return httputils.AiRequest(...) // 返回模型文本
//	}}
type CompletionFunc func(ctx context.Context, systemPrompt, userPrompt string) (string, error)

// LLMProposer 用模型归纳字段，但永远与确定性启发式结果合并，且失败自动降级。
// 这样离线、限流、模型胡说时模板生成都不会中断。
type LLMProposer struct {
	Complete CompletionFunc
	Fallback FieldProposer // 默认 HeuristicProposer
}

func (p LLMProposer) fallback() FieldProposer {
	if p.Fallback != nil {
		return p.Fallback
	}
	return HeuristicProposer{}
}

func (p LLMProposer) Propose(ctx context.Context, samples []string, builtin FieldDefs) ([]ProposedField, error) {
	base, err := p.fallback().Propose(ctx, samples, builtin)
	if err != nil {
		base = []ProposedField{}
	}
	if p.Complete == nil {
		return base, nil
	}
	system, user := buildProposePrompt(samples, builtin)
	raw, err := p.Complete(ctx, system, user)
	if err != nil {
		return base, nil
	}
	fromLLM, err := parseProposalDoc(raw)
	if err != nil {
		return base, nil
	}
	return mergeProposals(base, fromLLM), nil
}

const proposeSystemPrompt = `你是订单格式分析器。用户会给你若干条同一类型的订单原文示例，请归纳出这份订单格式的「字段定义草案」。

规则：
1. 只输出 JSON，不要任何解释文字，形如：
   {"fields":[{"label":"字段名","key":"","type":"string","frequency":1.0,"aliases":["同义写法"],"labels":["原文里的标签"],"regex":"","unit":"","options":[]}]}
2. label 用原文里出现的写法，labels 是原文中该字段的标签（如「店名」「联系方式」）。
3. type 只能是 string/number/phone/date/bool/enum/measure/array/object 之一。
4. frequency 是该字段出现在多少比例的样例里，0~1。
5. 不要把同一个含义的不同写法列成多个字段，放进 aliases。
6. 不要输出收货地址之外系统已有的时间、来源等元信息字段。
7. 内置字段会由程序统一收敛，key 留空即可。`

func buildProposePrompt(samples []string, builtin FieldDefs) (string, string) {
	var b strings.Builder
	b.WriteString("已知内置字段（同义写法会被自动收敛，不要再提议新字段）：\n")
	for _, f := range builtin {
		fmt.Fprintf(&b, "- %s (%s)，别名: %s\n", f.Key, f.Label, strings.Join(f.Aliases, "/"))
	}
	b.WriteString("\n订单样例：\n")
	for i, s := range samples {
		fmt.Fprintf(&b, "--- 样例 %d ---\n%s\n", i+1, s)
	}
	return proposeSystemPrompt, b.String()
}

type llmProposalDoc struct {
	Fields []struct {
		Label     string   `json:"label"`
		Key       string   `json:"key"`
		Type      string   `json:"type"`
		Frequency float64  `json:"frequency"`
		Aliases   []string `json:"aliases"`
		Labels    []string `json:"labels"`
		Regex     string   `json:"regex"`
		Unit      string   `json:"unit"`
		Options   []string `json:"options"`
	} `json:"fields"`
}

func parseProposalDoc(raw string) ([]ProposedField, error) {
	clean := extractJSONObject(raw)
	if clean == "" {
		return nil, fmt.Errorf("模型输出里没有 JSON 对象")
	}
	var doc llmProposalDoc
	if err := json.Unmarshal([]byte(clean), &doc); err != nil {
		return nil, err
	}
	out := make([]ProposedField, 0, len(doc.Fields))
	for _, f := range doc.Fields {
		label := strings.TrimSpace(f.Label)
		if label == "" {
			continue
		}
		ft := FieldType(strings.ToLower(strings.TrimSpace(f.Type)))
		if !validFieldTypes[ft] {
			ft = FieldString
		}
		key := strings.TrimSpace(f.Key)
		if c, ok := CanonicalKey(label); ok {
			key = c
		}
		if key == "" {
			key = CustomKey(label)
		}
		labels := append([]string{}, f.Labels...)
		if len(labels) == 0 {
			labels = []string{label}
		}
		rule := ExtractRule{Kind: ExtractLabel, Labels: labels}
		if f.Regex != "" {
			rule.Kind = ExtractComposite
			rule.Regex = f.Regex
		}
		freq := f.Frequency
		if freq <= 0 || freq > 1 {
			freq = 1
		}
		out = append(out, ProposedField{
			Def: FieldDef{
				Key: key, Label: label, Type: ft, Aliases: f.Aliases,
				Extract: rule, Unit: f.Unit, Options: f.Options,
			},
			Frequency: freq,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("模型没有给出有效字段")
	}
	return out, nil
}

func extractJSONObject(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

// mergeProposals 以启发式为底、LLM 为准：同 key 覆盖，LLM 新增的追加。
func mergeProposals(base, extra []ProposedField) []ProposedField {
	index := map[string]int{}
	out := make([]ProposedField, 0, len(base)+len(extra))
	for _, p := range base {
		index[p.Def.Key] = len(out)
		out = append(out, p)
	}
	for _, p := range extra {
		if i, ok := index[p.Def.Key]; ok {
			if p.Frequency > out[i].Frequency {
				out[i].Frequency = p.Frequency
			}
			out[i].Def.Aliases = appendUnique(out[i].Def.Aliases, p.Def.Aliases...)
			out[i].Def.Extract.Labels = appendUnique(out[i].Def.Extract.Labels, p.Def.Extract.Labels...)
			continue
		}
		index[p.Def.Key] = len(out)
		out = append(out, p)
	}
	return out
}

var _ FieldProposer = LLMProposer{}
var _ FieldProposer = HeuristicProposer{}
