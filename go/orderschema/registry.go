package orderschema

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"unicode"
)

// 内置 canonical 字段。它们是「默认模板预置项」，不是数据库列约束：
// 模板可以声明某个字段不使用，值依然可以为空。真实列只在 Indexed 时做镜像。
var builtinFields = FieldDefs{
	{
		Key: "recipient", Label: "姓名/收件人", Type: FieldString,
		Builtin: true, Indexed: true, Sort: 10,
		Aliases: []string{"收件人", "收货人", "姓名", "名字", "客户", "客户名", "店名", "门店", "店铺", "联系人", "昵称", "买家", "下单人"},
		Extract: ExtractRule{Kind: ExtractLabel, Labels: []string{"收件人", "收货人", "姓名", "店名", "门店", "联系人", "客户", "昵称"}},
	},
	{
		Key: "phone", Label: "手机号", Type: FieldPhone,
		Builtin: true, Indexed: true, Sort: 20,
		Aliases: []string{"手机", "手机号", "电话", "联系电话", "联系方式", "号码", "tel", "phone", "mobile"},
		Extract: ExtractRule{Kind: ExtractComposite, Labels: []string{"手机", "电话", "联系电话", "联系方式"}, Regex: `1[3-9]\d{9}`},
	},
	{
		Key: "address", Label: "收货地址", Type: FieldString,
		Builtin: true, Indexed: true, Sort: 30,
		Aliases: []string{"地址", "收货地址", "配送地址", "店址", "收货地", "详细地址", "送到", "address"},
		Extract: ExtractRule{Kind: ExtractLabel, Labels: []string{"地址", "收货地址", "配送地址", "店址", "送到"}},
	},
	{
		Key: "remarks", Label: "备注", Type: FieldString,
		Builtin: true, Indexed: true, Sort: 40,
		Aliases: []string{"备注", "说明", "要求", "留言", "备注说明", "特殊要求", "附注", "note", "remark"},
		Extract: ExtractRule{Kind: ExtractLabel, Labels: []string{"备注", "说明", "要求", "留言", "备注说明"}},
	},
}

// mirrorKeys 是允许镜像到订单表固定列的 canonical 字段。
// 只有这些字段会获得真实列，其余自定义字段永远只进 JSON。
var mirrorKeys = map[string]bool{
	"recipient": true, "phone": true, "address": true, "remarks": true,
}

var aliasToCanonical = buildAliasIndex(builtinFields)

func buildAliasIndex(defs FieldDefs) map[string]string {
	m := make(map[string]string)
	for _, f := range defs {
		m[NormalizeKey(f.Key)] = f.Key
		m[NormalizeKey(f.Label)] = f.Key
		for _, a := range f.Aliases {
			m[NormalizeKey(a)] = f.Key
		}
	}
	return m
}

// NormalizeKey 把「收货地址：」「TEL 」「联系电话(必填)」这类写法归一成可比较的键。
func NormalizeKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// CanonicalKey 尝试把任意标签收敛到内置字段。
func CanonicalKey(s string) (string, bool) {
	k, ok := aliasToCanonical[NormalizeKey(s)]
	return k, ok
}

// CustomKey 为自定义字段生成稳定 key：纯 ASCII 用可读名，中文用内容哈希。
// 稳定性很重要——同一个标签在不同批次样例里必须落到同一个 key，否则字段会漂移。
func CustomKey(label string) string {
	n := NormalizeKey(label)
	if n == "" {
		return ""
	}
	ascii := true
	for _, r := range n {
		if r > 127 {
			ascii = false
			break
		}
	}
	if ascii {
		return "f_" + n
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(n))
	return fmt.Sprintf("f_%08x", h.Sum32())
}

// BuiltinFields 返回内置字段的副本，调用方可以安全修改。
func BuiltinFields() FieldDefs {
	return cloneFieldDefs(builtinFields)
}

func cloneFieldDefs(src FieldDefs) FieldDefs {
	b, _ := json.Marshal(src)
	var dst FieldDefs
	_ = json.Unmarshal(b, &dst)
	return dst
}

func cloneTemplate(src *Template) *Template {
	b, _ := json.Marshal(src)
	var dst Template
	_ = json.Unmarshal(b, &dst)
	return &dst
}

// FindField 按 key 查找字段定义。
func FindField(defs FieldDefs, key string) (FieldDef, bool) {
	for _, f := range defs {
		if f.Key == key {
			return f, true
		}
	}
	return FieldDef{}, false
}

func appendUnique(dst []string, values ...string) []string {
	seen := make(map[string]bool, len(dst))
	for _, v := range dst {
		seen[v] = true
	}
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		dst = append(dst, v)
		seen[v] = true
	}
	return dst
}

// FieldProposal 是一次字段归纳的结果项。
// New=false 表示它被收敛进了已有字段(MergedInto)，New=true 表示这是一个
// 待人工确认的新字段——默认不会写进模板，防止 schema 漂移。
type FieldProposal struct {
	Field      FieldDef `json:"field"`
	New        bool     `json:"new"`
	MergedInto string   `json:"merged_into,omitempty"`
	Reason     string   `json:"reason"`
	Frequency  float64  `json:"frequency,omitempty"`
}

// MergeProposed 把提议字段合并进基线：命中别名的一律收敛，不新增；
// 未命中的只生成提议，不自动落库。
func MergeProposed(base FieldDefs, proposed FieldDefs) (FieldDefs, []FieldProposal) {
	merged := cloneFieldDefs(base)
	proposals := make([]FieldProposal, 0, len(proposed))

	for _, p := range proposed {
		canonical := ""
		if k, ok := CanonicalKey(p.Label); ok {
			canonical = k
		} else if k, ok := CanonicalKey(p.Key); ok {
			canonical = k
		}

		idx := -1
		for i := range merged {
			if merged[i].Key == canonical || merged[i].Key == p.Key {
				idx = i
				break
			}
		}

		if idx < 0 && canonical != "" {
			if bf, ok := FindField(builtinFields, canonical); ok {
				merged = append(merged, bf)
				idx = len(merged) - 1
			}
		}

		if idx >= 0 {
			merged[idx].Aliases = appendUnique(merged[idx].Aliases, append([]string{p.Label}, p.Aliases...)...)
			if p.Extract.Kind != "" {
				merged[idx].Extract.Labels = appendUnique(merged[idx].Extract.Labels, p.Extract.Labels...)
			}
			proposals = append(proposals, FieldProposal{
				Field: p, New: false, MergedInto: merged[idx].Key,
				Reason: fmt.Sprintf("「%s」收敛到内置字段 %s", p.Label, merged[idx].Key),
			})
			continue
		}

		key := p.Key
		if key == "" {
			key = CustomKey(p.Label)
		}
		if key == "" {
			continue
		}
		if existing, ok := FindField(merged, key); ok {
			proposals = append(proposals, FieldProposal{
				Field: p, New: false, MergedInto: existing.Key,
				Reason: fmt.Sprintf("「%s」已存在为自定义字段 %s", p.Label, existing.Key),
			})
			continue
		}
		p.Key = key
		proposals = append(proposals, FieldProposal{
			Field: p, New: true, Reason: fmt.Sprintf("新字段「%s」，需人工确认后加入模板", p.Label),
		})
	}
	return merged, proposals
}

// AcceptProposals 是「人工确认」这一步：只把指定 key 的 New 提议加入模板。
func AcceptProposals(t *Template, proposals []FieldProposal, keys []string) int {
	accept := make(map[string]bool, len(keys))
	for _, k := range keys {
		accept[k] = true
	}
	n := 0
	for _, p := range proposals {
		if !p.New || !accept[p.Field.Key] {
			continue
		}
		if _, ok := FindField(t.Fields, p.Field.Key); ok {
			continue
		}
		f := p.Field
		f.Builtin = false
		f.Indexed = false
		f.Sort = (len(t.Fields) + 1) * 10
		t.Fields = append(t.Fields, f)
		n++
	}
	return n
}
