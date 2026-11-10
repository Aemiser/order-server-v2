package orderschema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// templateRow 把整份模板作为 JSON 文档存一行，主键 (id, version)。
// 这样加字段不需要改表，版本也能直接查询。
type templateRow struct {
	ID        string    `gorm:"primaryKey;size:64"`
	Version   int       `gorm:"primaryKey"`
	Name      string    `gorm:"size:128"`
	Status    string    `gorm:"size:24;index"`
	Document  string    `gorm:"type:json"`
	CreatedAt time.Time `gorm:"index"`
}

func (templateRow) TableName() string { return "order_templates" }

// SchemaMigration 记录已执行的迁移，替代「只靠 AutoMigrate 且无执行历史」的做法。
type SchemaMigration struct {
	Name      string    `gorm:"primaryKey;size:64"`
	AppliedAt time.Time `gorm:"not null"`
}

func (SchemaMigration) TableName() string { return "orderschema_migrations" }

var schemaMigrations = []struct {
	Name string
	Run  func(*gorm.DB) error
}{
	{"001_core_tables", func(db *gorm.DB) error {
		return db.AutoMigrate(&templateRow{}, &Order{})
	}},
}

// Migrate 幂等地建表并记录版本。接入现有 MySQL 时由新应用启动流程调用一次。
func Migrate(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("orderschema: db 为空")
	}
	if err := db.AutoMigrate(&SchemaMigration{}); err != nil {
		return err
	}
	for _, m := range schemaMigrations {
		var count int64
		if err := db.Model(&SchemaMigration{}).Where("name = ?", m.Name).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if err := m.Run(db); err != nil {
			return fmt.Errorf("orderschema: 迁移 %s 失败: %w", m.Name, err)
		}
		if err := db.Create(&SchemaMigration{Name: m.Name, AppliedAt: time.Now()}).Error; err != nil {
			return err
		}
	}
	return nil
}

// GormStore 是生产存储实现，注入已有的 *gorm.DB 即可，不自己连库。
type GormStore struct{ db *gorm.DB }

func NewGormStore(db *gorm.DB) (*GormStore, error) {
	if db == nil {
		return nil, fmt.Errorf("orderschema: db 为空")
	}
	return &GormStore{db: db}, nil
}

func (s *GormStore) SaveTemplate(ctx context.Context, t *Template) error {
	if t == nil || strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("template_id 不能为空")
	}
	if t.Version < 1 {
		return fmt.Errorf("模板版本号必须 >= 1")
	}
	var existing templateRow
	err := s.db.WithContext(ctx).
		Where("id = ? AND version = ?", t.ID, t.Version).
		First(&existing).Error
	if err == nil && TemplateStatus(existing.Status) == TemplatePublished {
		return ErrImmutableVersion
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	doc, err := json.Marshal(t)
	if err != nil {
		return err
	}
	row := templateRow{
		ID: t.ID, Version: t.Version, Name: t.Name,
		Status: string(t.Status), Document: string(doc), CreatedAt: t.CreatedAt,
	}
	return s.db.WithContext(ctx).Save(&row).Error
}

func (s *GormStore) GetTemplate(ctx context.Context, id string, version int) (*Template, error) {
	q := s.db.WithContext(ctx).Model(&templateRow{}).Where("id = ?", id)
	var row templateRow
	var err error
	if version > 0 {
		err = q.Where("version = ?", version).First(&row).Error
	} else {
		err = q.Order("version DESC").First(&row).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decodeTemplate(row.Document)
}

func (s *GormStore) LatestTemplate(ctx context.Context, id string) (*Template, error) {
	return s.GetTemplate(ctx, id, 0)
}

func (s *GormStore) ListTemplates(ctx context.Context, onlyPublished bool) ([]*Template, error) {
	var rows []templateRow
	if err := s.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	latest := map[string]templateRow{}
	for _, row := range rows {
		if old, ok := latest[row.ID]; !ok || row.Version > old.Version {
			latest[row.ID] = row
		}
	}
	out := make([]*Template, 0, len(latest))
	for _, row := range latest {
		if onlyPublished && TemplateStatus(row.Status) != TemplatePublished {
			continue
		}
		t, err := decodeTemplate(row.Document)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *GormStore) SaveOrder(ctx context.Context, o *Order) error {
	if o == nil || strings.TrimSpace(o.TemplateID) == "" {
		return fmt.Errorf("订单必须带 template_id")
	}
	version := o.TemplateVersion
	if version <= 0 {
		version = 1
	}
	tpl, err := s.GetTemplate(ctx, o.TemplateID, version)
	if err != nil {
		tpl, _ = s.LatestTemplate(ctx, o.TemplateID)
	}
	SyncMirrors(tpl, o)
	if o.Fields == nil {
		o.Fields = FieldValues{}
	}
	if o.Status == "" {
		o.Status = OrderPendingReview
	}
	if o.DedupHash == "" {
		o.DedupHash = ComputeDedupHash(o)
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&Order{}).
		Where("dedup_hash = ?", o.DedupHash).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrDuplicateOrder
	}
	return s.db.WithContext(ctx).Create(o).Error
}

func (s *GormStore) GetOrder(ctx context.Context, id uint64) (*Order, error) {
	var o Order
	if err := s.db.WithContext(ctx).First(&o, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &o, nil
}

func (s *GormStore) FindOrders(ctx context.Context, q OrderQuery) ([]*Order, error) {
	db := s.db.WithContext(ctx).Model(&Order{})
	if q.TenantID != "" {
		db = db.Where("tenant_id = ?", q.TenantID)
	}
	if q.TemplateID != "" {
		db = db.Where("template_id = ?", q.TemplateID)
	}
	if q.Status != "" {
		db = db.Where("status = ?", q.Status)
	}
	if !q.From.IsZero() {
		db = db.Where("order_time >= ?", q.From)
	}
	if !q.To.IsZero() {
		db = db.Where("order_time <= ?", q.To)
	}
	if q.Limit > 0 {
		db = db.Limit(q.Limit)
	}
	var out []*Order
	if err := db.Order("id DESC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func decodeTemplate(document string) (*Template, error) {
	var t Template
	if err := json.Unmarshal([]byte(document), &t); err != nil {
		return nil, fmt.Errorf("orderschema: 模板文档解析失败: %w", err)
	}
	return &t, nil
}

var _ Store = (*MemoryStore)(nil)
var _ Store = (*GormStore)(nil)
