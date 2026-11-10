package orderschema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound         = errors.New("orderschema: 记录不存在")
	ErrImmutableVersion = errors.New("orderschema: 已发布模板版本不可修改，请派生新版本")
	ErrDuplicateOrder   = errors.New("orderschema: 相同 dedup_hash 的订单已存在")
)

// Store 是地基对存储的唯一抽象。测试用 MemoryStore，生产用 GormStore。
type Store interface {
	SaveTemplate(ctx context.Context, t *Template) error
	GetTemplate(ctx context.Context, id string, version int) (*Template, error)
	LatestTemplate(ctx context.Context, id string) (*Template, error)
	ListTemplates(ctx context.Context, onlyPublished bool) ([]*Template, error)
	SaveOrder(ctx context.Context, o *Order) error
	GetOrder(ctx context.Context, id uint64) (*Order, error)
	FindOrders(ctx context.Context, q OrderQuery) ([]*Order, error)
}

// OrderQuery 是订单检索条件，全部走固定骨架列，不查 JSON 内部字段。
type OrderQuery struct {
	TenantID   string
	TemplateID string
	Status     OrderStatus
	From       time.Time
	To         time.Time
	Limit      int
}

// MemoryStore 是纯内存实现，用于单测与离线 demo，无外部依赖。
type MemoryStore struct {
	mu        sync.RWMutex
	templates map[string]map[int]*Template
	orders    map[uint64]*Order
	byHash    map[string]uint64
	nextID    uint64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		templates: map[string]map[int]*Template{},
		orders:    map[uint64]*Order{},
		byHash:    map[string]uint64{},
	}
}

func (s *MemoryStore) SaveTemplate(_ context.Context, t *Template) error {
	if t == nil || strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("template_id 不能为空")
	}
	if t.Version < 1 {
		return fmt.Errorf("模板版本号必须 >= 1")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := s.templates[t.ID]
	if versions == nil {
		versions = map[int]*Template{}
		s.templates[t.ID] = versions
	}
	if old := versions[t.Version]; old != nil && old.Status == TemplatePublished {
		return ErrImmutableVersion
	}
	versions[t.Version] = cloneTemplate(t)
	return nil
}

func (s *MemoryStore) GetTemplate(_ context.Context, id string, version int) (*Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	versions := s.templates[id]
	if versions == nil {
		return nil, ErrNotFound
	}
	if version <= 0 {
		return s.latestLocked(id)
	}
	t := versions[version]
	if t == nil {
		return nil, ErrNotFound
	}
	return cloneTemplate(t), nil
}

func (s *MemoryStore) latestLocked(id string) (*Template, error) {
	versions := s.templates[id]
	if len(versions) == 0 {
		return nil, ErrNotFound
	}
	var best *Template
	for _, t := range versions {
		if best == nil || t.Version > best.Version {
			best = t
		}
	}
	return cloneTemplate(best), nil
}

func (s *MemoryStore) LatestTemplate(ctx context.Context, id string) (*Template, error) {
	return s.GetTemplate(ctx, id, 0)
}

func (s *MemoryStore) ListTemplates(_ context.Context, onlyPublished bool) ([]*Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Template, 0, len(s.templates))
	for id := range s.templates {
		latest, err := s.latestLocked(id)
		if err != nil {
			continue
		}
		if onlyPublished && latest.Status != TemplatePublished {
			continue
		}
		out = append(out, latest)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *MemoryStore) SaveOrder(ctx context.Context, o *Order) error {
	if o == nil || strings.TrimSpace(o.TemplateID) == "" {
		return fmt.Errorf("订单必须带 template_id")
	}
	version := o.TemplateVersion
	if version <= 0 {
		version = 1
	}
	tpl, err := s.GetTemplate(ctx, o.TemplateID, version)
	if err != nil {
		// 版本不在库里时退化为最新版本，仍可正常落库（渲染时按订单自带版本）。
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
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byHash[o.DedupHash]; exists {
		return ErrDuplicateOrder
	}
	s.nextID++
	o.ID = s.nextID
	if o.CreatedAt.IsZero() {
		o.CreatedAt = now
	}
	o.UpdatedAt = now
	s.orders[o.ID] = cloneOrder(o)
	s.byHash[o.DedupHash] = o.ID
	return nil
}

func (s *MemoryStore) GetOrder(_ context.Context, id uint64) (*Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o := s.orders[id]
	if o == nil {
		return nil, ErrNotFound
	}
	return cloneOrder(o), nil
}

func (s *MemoryStore) FindOrders(_ context.Context, q OrderQuery) ([]*Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Order, 0, len(s.orders))
	for _, o := range s.orders {
		if q.TenantID != "" && o.TenantID != q.TenantID {
			continue
		}
		if q.TemplateID != "" && o.TemplateID != q.TemplateID {
			continue
		}
		if q.Status != "" && o.Status != q.Status {
			continue
		}
		if !q.From.IsZero() && o.OrderTime.Before(q.From) {
			continue
		}
		if !q.To.IsZero() && o.OrderTime.After(q.To) {
			continue
		}
		out = append(out, cloneOrder(o))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func cloneOrder(src *Order) *Order {
	b, _ := json.Marshal(src)
	var dst Order
	_ = json.Unmarshal(b, &dst)
	return &dst
}
