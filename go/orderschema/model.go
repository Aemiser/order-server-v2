// Package orderschema 是通用订单识别应用的地基：模板(schema)、字段注册表、
// 统一订单信封(固定骨架 + JSON 载荷)。本包刻意不依赖 Wails 与老订单链路，
// 以便新应用从零接入。
//
// 核心约定：
//  1. 模板定义 + fields JSON 才是 schema 真相；订单表上的 recipient/phone/
//     address/remarks 只是可为空的「镜像列」，供索引、去重、查询使用。
//  2. 自定义字段只进 JSON，加字段不需要 DDL。
//  3. 每笔订单带 template_id + template_version，模板改动只能产生新版本，
//     老订单永远按当时的版本渲染。
package orderschema

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// FieldType 是字段值类型，决定校验与前端渲染方式。
type FieldType string

const (
	FieldString  FieldType = "string"
	FieldNumber  FieldType = "number"
	FieldPhone   FieldType = "phone"
	FieldDate    FieldType = "date"
	FieldBool    FieldType = "bool"
	FieldEnum    FieldType = "enum"
	FieldMeasure FieldType = "measure"
	FieldArray   FieldType = "array"
	FieldObject  FieldType = "object"
)

// ExtractKind 是字段抽取器类型。
type ExtractKind string

const (
	ExtractRegex     ExtractKind = "regex"
	ExtractLabel     ExtractKind = "label"
	ExtractLLM       ExtractKind = "llm"
	ExtractComposite ExtractKind = "composite"
)

var validFieldTypes = map[FieldType]bool{
	FieldString: true, FieldNumber: true, FieldPhone: true, FieldDate: true,
	FieldBool: true, FieldEnum: true, FieldMeasure: true, FieldArray: true,
	FieldObject: true,
}

var validExtractKinds = map[ExtractKind]bool{
	ExtractRegex: true, ExtractLabel: true, ExtractLLM: true, ExtractComposite: true,
}

// ExtractRule 描述「这个字段怎么从原文里取出来」。
// 确定性抽取(正则/标签)为真值来源，llm 仅作语义补全，与老链路 FixSpecNames 的思路一致。
type ExtractRule struct {
	Kind   ExtractKind `json:"kind"`
	Labels []string    `json:"labels,omitempty"`
	Regex  string      `json:"regex,omitempty"`
	Hint   string      `json:"hint,omitempty"`
}

// FieldDef 是模板里的一个字段定义。Builtin 表示它来自内置字段注册表，
// Indexed 表示它的值会被镜像到订单表的固定列。
type FieldDef struct {
	Key        string      `json:"key"`
	Label      string      `json:"label"`
	Type       FieldType   `json:"type"`
	Required   bool        `json:"required"`
	Builtin    bool        `json:"builtin"`
	Indexed    bool        `json:"indexed"`
	Aliases    []string    `json:"aliases,omitempty"`
	Extract    ExtractRule `json:"extract"`
	Unit       string      `json:"unit,omitempty"`
	Options    []string    `json:"options,omitempty"`
	Sort       int         `json:"sort"`
	Deprecated bool        `json:"deprecated,omitempty"`
}

// FieldDefs 用于模板文档的 JSON 列。
type FieldDefs []FieldDef

func (f FieldDefs) Value() (driver.Value, error) { return json.Marshal(f) }

func (f *FieldDefs) Scan(src any) error {
	b, err := scanBytes(src)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		*f = FieldDefs{}
		return nil
	}
	return json.Unmarshal(b, f)
}

// ItemDef 描述订单行(商品/服务明细)的结构。
type ItemDef struct {
	Enabled      bool     `json:"enabled"`
	NameLabels   []string `json:"name_labels,omitempty"`
	QuantityUnit string   `json:"quantity_unit,omitempty"`
	AllowAttrs   bool     `json:"allow_attrs"`
}

// TemplateStatus 是模板生命周期状态。
type TemplateStatus string

const (
	TemplateDraft      TemplateStatus = "draft"
	TemplatePublished  TemplateStatus = "published"
	TemplateDeprecated TemplateStatus = "deprecated"
)

// Signature 是模板路由签名：靠关键词/标签/分隔符做轻量匹配，不必每条消息都调 LLM。
type Signature struct {
	Keywords   []string `json:"keywords,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	Separators []string `json:"separators,omitempty"`
}

// Signatures 是模板文档的 JSON 列。
type Signatures []Signature

func (s Signatures) Value() (driver.Value, error) { return json.Marshal(s) }

func (s *Signatures) Scan(src any) error {
	b, err := scanBytes(src)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		*s = Signatures{}
		return nil
	}
	return json.Unmarshal(b, s)
}

// Template 是一份订单格式模板。ID 是稳定字符串标识(不用自增数字，
// 方便多租户与模板导入导出)，Version 只增不改。
type Template struct {
	ID          string         `json:"template_id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Version     int            `json:"version"`
	Status      TemplateStatus `json:"status"`
	Fields      FieldDefs      `json:"fields"`
	Items       ItemDef        `json:"items"`
	Signatures  Signatures     `json:"signatures,omitempty"`
	SampleCount int            `json:"sample_count"`
	CreatedAt   time.Time      `json:"created_at"`
	PublishedAt *time.Time     `json:"published_at,omitempty"`
}

// FieldValue 是订单里某个字段的值。保留 raw / source / confidence，
// 才能做人工审查、回放，以及统计「哪些字段 AI 老是认错」。
type FieldValue struct {
	Value      any     `json:"value"`
	Raw        string  `json:"raw,omitempty"`
	Source     string  `json:"source,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

// FieldValues 是订单的自定义/内置字段载荷，整列存 JSON。
type FieldValues map[string]FieldValue

func (v FieldValues) Value() (driver.Value, error) { return json.Marshal(v) }

func (v *FieldValues) Scan(src any) error {
	b, err := scanBytes(src)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		*v = FieldValues{}
		return nil
	}
	return json.Unmarshal(b, v)
}

// Measure 把「5 斤」抽象成数值 + 单位，取代写死的 Weight int(斤)。
type Measure struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

// LineItem 是一行明细：名称 + 数量(带单位) + 任意属性。
type LineItem struct {
	Name  string         `json:"name"`
	Qty   Measure        `json:"qty"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// LineItems 是订单行载荷，整列存 JSON。
type LineItems []LineItem

func (l LineItems) Value() (driver.Value, error) { return json.Marshal(l) }

func (l *LineItems) Scan(src any) error {
	b, err := scanBytes(src)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		*l = LineItems{}
		return nil
	}
	return json.Unmarshal(b, l)
}

// OrderStatus 是订单在应用内的流转状态。
type OrderStatus string

const (
	OrderPendingReview OrderStatus = "pending_review"
	OrderConfirmed     OrderStatus = "confirmed"
	OrderDiscarded     OrderStatus = "discarded"
)

// Order 是统一订单信封。固定骨架列与业务字段分离，业务字段全在 Fields。
type Order struct {
	ID              uint64      `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID        string      `json:"tenant_id" gorm:"size:64;index"`
	TemplateID      string      `json:"template_id" gorm:"size:64;not null;index:idx_tpl_ver"`
	TemplateVersion int         `json:"template_version" gorm:"not null;index:idx_tpl_ver"`
	Source          string      `json:"source" gorm:"size:32"`
	SourceMsgID     string      `json:"source_msg_id" gorm:"size:128;index"`
	RawText         string      `json:"raw_text" gorm:"type:text"`
	OrderTime       time.Time   `json:"order_time" gorm:"index"`
	Status          OrderStatus `json:"status" gorm:"size:24;index"`
	Confidence      float64     `json:"confidence"`
	DedupHash       string      `json:"dedup_hash" gorm:"size:64;uniqueIndex"`

	Fields FieldValues `json:"fields" gorm:"type:json"`
	Items  LineItems   `json:"items" gorm:"type:json"`

	// 镜像列：只用于索引/去重/查询，可为空，模板可以声明不使用。
	// schema 真相永远在 Fields，禁止业务代码直接读写这些列当字段用。
	RecipientName *string `json:"recipient_name,omitempty" gorm:"size:128;index"`
	Phone         *string `json:"phone,omitempty" gorm:"size:32;index"`
	Address       *string `json:"address,omitempty" gorm:"size:512"`
	Remarks       *string `json:"remarks,omitempty" gorm:"size:512"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Order) TableName() string { return "orders_v2" }

func scanBytes(src any) ([]byte, error) {
	if src == nil {
		return nil, nil
	}
	switch x := src.(type) {
	case []byte:
		return x, nil
	case string:
		return []byte(x), nil
	default:
		return nil, fmt.Errorf("orderschema: unsupported JSON scan source %T", src)
	}
}
