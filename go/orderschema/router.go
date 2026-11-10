package orderschema

import (
	"sort"
	"strings"
)

// RouteMatch 是一次模板路由结果，Reasons 用于前端解释「为什么命中这个模板」。
type RouteMatch struct {
	TemplateID string   `json:"template_id"`
	Version    int      `json:"version"`
	Score      float64  `json:"score"`
	Reasons    []string `json:"reasons,omitempty"`
}

// Route 基于签名做轻量模板匹配，返回按分数降序的候选。
// 空结果表示「未识别」，应进人工审查区，而不是硬套某个模板。
func Route(text string, templates []*Template, threshold float64) []RouteMatch {
	if strings.TrimSpace(text) == "" || len(templates) == 0 {
		return nil
	}
	if threshold <= 0 {
		threshold = 0.35
	}
	norm := normalizeRouteText(text)
	matches := make([]RouteMatch, 0, len(templates))
	for _, t := range templates {
		if t == nil || t.Status == TemplateDeprecated {
			continue
		}
		best, reasons := scoreTemplate(norm, t)
		if best < threshold {
			continue
		}
		matches = append(matches, RouteMatch{
			TemplateID: t.ID, Version: t.Version, Score: round2(best), Reasons: reasons,
		})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].TemplateID < matches[j].TemplateID
	})
	return matches
}

// RouteBest 返回分数最高的模板；无命中时 ok=false。
func RouteBest(text string, templates []*Template, threshold float64) (RouteMatch, bool) {
	all := Route(text, templates, threshold)
	if len(all) == 0 {
		return RouteMatch{}, false
	}
	return all[0], true
}

func normalizeRouteText(text string) string {
	r := strings.NewReplacer("：", ":", "　", " ", "，", ",", "（", "(", "）", ")")
	return r.Replace(text)
}

func scoreTemplate(text string, t *Template) (float64, []string) {
	fieldCoverage := 0.0
	labelTotal := 0
	labelHit := 0
	for _, f := range t.Fields {
		for _, l := range f.Extract.Labels {
			labelTotal++
			if strings.Contains(text, l+":") || strings.Contains(text, l+"：") {
				labelHit++
			}
		}
	}
	if labelTotal > 0 {
		fieldCoverage = float64(labelHit) / float64(labelTotal)
	}

	best := 0.0
	bestReasons := []string{}
	for _, sig := range t.Signatures {
		kwTotal := len(sig.Keywords)
		kwHit := 0
		for _, k := range sig.Keywords {
			if k != "" && strings.Contains(text, k) {
				kwHit++
			}
		}
		lbTotal := len(sig.Labels)
		lbHit := 0
		for _, l := range sig.Labels {
			if l != "" && (strings.Contains(text, l+":") || strings.Contains(text, l+"：")) {
				lbHit++
			}
		}
		sepTotal := len(sig.Separators)
		sepHit := 0
		for _, s := range sig.Separators {
			if s != "" && strings.Contains(text, s) {
				sepHit++
			}
		}
		kwScore, lbScore, sepScore := 0.0, 0.0, 0.0
		if kwTotal > 0 {
			kwScore = float64(kwHit) / float64(kwTotal)
		}
		if lbTotal > 0 {
			lbScore = float64(lbHit) / float64(lbTotal)
		}
		if sepTotal > 0 {
			sepScore = float64(sepHit) / float64(sepTotal)
		}
		score := 0.45*kwScore + 0.30*lbScore + 0.15*sepScore + 0.10*fieldCoverage
		if score > best {
			best = score
			bestReasons = []string{
				"关键词命中 " + itoa(kwHit) + "/" + itoa(kwTotal),
				"标签命中 " + itoa(lbHit) + "/" + itoa(lbTotal),
				"字段标签覆盖 " + percent(fieldCoverage),
			}
		}
	}
	// 没有签名时退化为字段标签覆盖，避免模板永远路由不到。
	if len(t.Signatures) == 0 {
		best = 0.6 * fieldCoverage
		bestReasons = []string{"无签名，按字段标签覆盖 " + percent(fieldCoverage)}
	}
	if best > 1 {
		best = 1
	}
	return best, bestReasons
}

// BuildSignatures 供 Schema Studio 在用户编辑字段后重建路由签名。
func BuildSignatures(t *Template, samples []string) Signatures {
	hasBlock := false
	for _, s := range samples {
		if specBlockPattern.MatchString(s) {
			hasBlock = true
			break
		}
	}
	return buildSignatures(samples, t.Fields, hasBlock)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func percent(v float64) string {
	return itoa(int(v*100)) + "%"
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
