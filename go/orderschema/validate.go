package orderschema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ValidationIssue 是一条校验结果。Level 为 error 时会阻止发布/入库。
type ValidationIssue struct {
	Field   string `json:"field"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// ValidationResult 汇总一次订单校验。UnknownFields 不判错，
// 因为模板可能已升级，老订单里保留着当时的额外字段。
type ValidationResult struct {
	OK            bool              `json:"ok"`
	Issues        []ValidationIssue `json:"issues,omitempty"`
	UnknownFields []string          `json:"unknown_fields,omitempty"`
}

var phonePattern = regexp.MustCompile(`1[3-9]\d{9}`)

var weightToKg = map[string]float64{
	"斤": 0.5, "公斤": 1, "千克": 1, "kg": 1, "g": 0.001, "克": 0.001,
}

// Validate 按模板校验订单字段。真实类型以 FieldValue.Value 为准。
func Validate(t *Template, o *Order) ValidationResult {
	res := ValidationResult{OK: true}
	if t == nil || o == nil {
		res.OK = false
		res.Issues = append(res.Issues, ValidationIssue{Level: "error", Message: "模板或订单为空"})
		return res
	}
	if o.TemplateID != t.ID || o.TemplateVersion != t.Version {
		res.Issues = append(res.Issues, ValidationIssue{
			Level: "warning",
			Message: fmt.Sprintf("订单模板版本 %s/v%d 与当前模板 %s/v%d 不一致，按订单自带版本渲染",
				o.TemplateID, o.TemplateVersion, t.ID, t.Version),
		})
	}
	if o.Fields == nil {
		o.Fields = FieldValues{}
	}

	known := make(map[string]bool, len(t.Fields))
	for _, f := range t.Fields {
		if f.Deprecated {
			continue
		}
		known[f.Key] = true
		v, exists := o.Fields[f.Key]
		if !exists || isEmptyValue(v.Value) {
			if f.Required {
				res.OK = false
				res.Issues = append(res.Issues, ValidationIssue{
					Field: f.Key, Level: "error", Message: fmt.Sprintf("必填字段「%s」缺失", f.Label),
				})
			}
			continue
		}
		if msg := checkFieldType(f, v.Value); msg != "" {
			res.Issues = append(res.Issues, ValidationIssue{Field: f.Key, Level: "error", Message: msg})
			res.OK = false
		}
		if f.Type == FieldPhone {
			s, ok := valueString(v.Value)
			if ok && !phonePattern.MatchString(s) {
				res.Issues = append(res.Issues, ValidationIssue{
					Field: f.Key, Level: "warning", Message: fmt.Sprintf("「%s」不像手机号: %s", f.Label, s),
				})
			}
		}
	}
	for k := range o.Fields {
		if !known[k] {
			res.UnknownFields = append(res.UnknownFields, k)
		}
	}
	return res
}

func isEmptyValue(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

func valueString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func checkFieldType(f FieldDef, v any) string {
	switch f.Type {
	case FieldString, FieldPhone, FieldDate:
		if _, ok := v.(string); !ok {
			return fmt.Sprintf("「%s」应为文本", f.Label)
		}
	case FieldNumber, FieldMeasure:
		switch v.(type) {
		case float64, int, int64, json.Number:
			return ""
		case map[string]any:
			if f.Type == FieldMeasure {
				return ""
			}
		case string:
			if f.Type == FieldMeasure {
				return "" // 允许「5斤」原文，由抽取层再解析
			}
			return fmt.Sprintf("「%s」应为数值", f.Label)
		default:
			return fmt.Sprintf("「%s」类型不支持 %T", f.Label, v)
		}
	case FieldBool:
		if _, ok := v.(bool); !ok {
			return fmt.Sprintf("「%s」应为布尔值", f.Label)
		}
	case FieldEnum:
		s, ok := v.(string)
		if !ok {
			return fmt.Sprintf("「%s」应为文本枚举", f.Label)
		}
		if len(f.Options) > 0 && !contains(f.Options, s) {
			return fmt.Sprintf("「%s」的值 %q 不在可选项内", f.Label, s)
		}
	case FieldArray:
		if _, ok := v.([]any); !ok {
			return fmt.Sprintf("「%s」应为数组", f.Label)
		}
	case FieldObject:
		if _, ok := v.(map[string]any); !ok {
			return fmt.Sprintf("「%s」应为对象", f.Label)
		}
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// FieldAsString 取字段的可读文本，供镜像列与展示使用。
func FieldAsString(o *Order, key string) (string, bool) {
	if o == nil {
		return "", false
	}
	v, ok := o.Fields[key]
	if !ok {
		return "", false
	}
	return valueString(v.Value)
}

// SyncMirrors 把 canonical 字段同步到订单表固定列。
// 模板未声明或声明不使用的字段，镜像列保持 NULL。
func SyncMirrors(t *Template, o *Order) {
	if o == nil {
		return
	}
	o.RecipientName, o.Phone, o.Address, o.Remarks = nil, nil, nil, nil
	if t == nil {
		return
	}
	setMirror := func(key string, dst **string) {
		for _, f := range t.Fields {
			if f.Key != key || f.Deprecated || !f.Indexed || !mirrorKeys[key] {
				continue
			}
			if s, ok := FieldAsString(o, key); ok && strings.TrimSpace(s) != "" {
				copy := s
				*dst = &copy
			}
			return
		}
	}
	setMirror("recipient", &o.RecipientName)
	setMirror("phone", &o.Phone)
	setMirror("address", &o.Address)
	setMirror("remarks", &o.Remarks)
}

// ComputeDedupHash 计算幂等键。有来源消息 ID 时优先用它，
// 否则用 canonical 字段 + 明细的稳定序列化。
func ComputeDedupHash(o *Order) string {
	if o == nil {
		return ""
	}
	if o.SourceMsgID != "" {
		return hashText(strings.Join([]string{o.TenantID, o.Source, o.SourceMsgID}, "|"))
	}
	canonical := map[string]any{}
	for _, k := range []string{"recipient", "phone", "address"} {
		if s, ok := FieldAsString(o, k); ok {
			canonical[k] = strings.TrimSpace(s)
		}
	}
	items := o.Items
	if items == nil {
		items = LineItems{}
	}
	payload, _ := json.Marshal(struct {
		TenantID string         `json:"tenant_id"`
		Template string         `json:"template_id"`
		Fields   map[string]any `json:"fields"`
		Items    LineItems      `json:"items"`
	}{o.TenantID, o.TemplateID, canonical, items})
	return hashText(string(payload))
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// NormalizeUnit 把 kg/KG/公斤/千克 等写法归一，避免单位别名造成字段漂移。
func NormalizeUnit(unit string) string {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "kg", "公斤", "千克", "kilo", "kilogram":
		return "kg"
	case "g", "克", "gram":
		return "g"
	case "斤", "jin":
		return "斤"
	case "件", "个", "箱", "份", "杯", "份数":
		return strings.TrimSpace(unit)
	default:
		return strings.TrimSpace(unit)
	}
}

// ConvertMeasure 只做重量单位换算；件/箱/份这类计数单位不可与重量互转，
// 明确报错而不是悄悄算错。
func ConvertMeasure(m Measure, to string) (Measure, error) {
	from := NormalizeUnit(m.Unit)
	target := NormalizeUnit(to)
	fromKg, ok1 := weightToKg[from]
	toKg, ok2 := weightToKg[target]
	if !ok1 || !ok2 {
		return Measure{}, fmt.Errorf("单位 %q → %q 不是可换算的重量单位", m.Unit, to)
	}
	return Measure{Value: m.Value * fromKg / toKg, Unit: target}, nil
}

// CheckTemplate 是发布前的结构校验。
func CheckTemplate(t *Template) error {
	if t == nil {
		return fmt.Errorf("模板为空")
	}
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("template_id 不能为空")
	}
	if t.Version < 1 {
		return fmt.Errorf("模板版本号必须 >= 1")
	}
	if len(t.Fields) == 0 {
		return fmt.Errorf("模板至少需要一个字段")
	}
	seen := make(map[string]bool, len(t.Fields))
	for _, f := range t.Fields {
		if strings.TrimSpace(f.Key) == "" || seen[f.Key] {
			return fmt.Errorf("字段 key 为空或重复: %q", f.Key)
		}
		seen[f.Key] = true
		if !validFieldTypes[f.Type] {
			return fmt.Errorf("字段 %s 的类型 %q 不合法", f.Key, f.Type)
		}
		if !validExtractKinds[f.Extract.Kind] {
			return fmt.Errorf("字段 %s 的抽取器 %q 不合法", f.Key, f.Extract.Kind)
		}
		if f.Extract.Regex != "" {
			if _, err := regexp.Compile(f.Extract.Regex); err != nil {
				return fmt.Errorf("字段 %s 的正则无效: %w", f.Key, err)
			}
		}
		if f.Type == FieldEnum && len(f.Options) == 0 {
			return fmt.Errorf("枚举字段 %s 必须提供 options", f.Key)
		}
	}
	return nil
}
