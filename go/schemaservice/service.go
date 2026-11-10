// Package schemaservice 是模块化后端（orderschema + compose + ports）接入
// Wails 主程序的第一条绞杀者纵切片：模板工作台页面全部走新链路。
//
// 边界纪律：
//   - 只读写新表（order_templates / orders_v2 / orderschema_migrations），
//     绝不碰 wechatorder/address_infos 等旧链路数据；
//   - 依赖方向：schemaservice → compose/orderschema/ports/kernel，
//     不被任何旧业务包 import（wechatorder 仅在适配器中读取其 Config）；
//   - AI/数据源全部经 ports 注入，单测可用内存实现替换。
package schemaservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"order_server/go/ai"
	"order_server/go/compose"
	"order_server/go/kernel"
	"order_server/go/orderschema"
	"order_server/go/ports"

	"gorm.io/gorm"
)

// Emitter 事件发射器，由 app 层注入（Wails runtime.EventsEmit）。
// 为 nil 时静默，保证服务可独立测试。
type Emitter func(event string, data any)

// RunOptions 一次识别运行的参数。
type RunOptions struct {
	From     string `json:"from"`     // YYYY-MM-DD
	To       string `json:"to"`       // YYYY-MM-DD
	Provider string `json:"provider"` // 仅「样例归纳」用：deepseek / qwen / 空=自动
	Source   string `json:"source"`   // 聊天来源注册名：空=wechat / file / 第三方注册名
	DataDir  string `json:"dataDir"`  // file 来源的文件路径（CSV / TSV / JSONL）
	DryRun   bool   `json:"dryRun"`   // 试跑：完整抽取但不入库
}

// RunReport 一次识别运行的结果快照（也作为 schema-result 事件载荷）。
type RunReport struct {
	From           string               `json:"from"`
	To             string               `json:"to"`
	Source         string               `json:"source"`
	DryRun         bool                 `json:"dryRun"`
	Cancelled      bool                 `json:"cancelled"`
	StartedAt      time.Time            `json:"startedAt"`
	FinishedAt     time.Time            `json:"finishedAt"`
	OrderCount     int                  `json:"orderCount"`
	UnmatchedCount int                  `json:"unmatchedCount"`
	Orders         []*orderschema.Order `json:"orders"`
	Unmatched      []ports.ChatMessage  `json:"unmatched"`
	Error          string               `json:"error,omitempty"`
}

// OrderQueryOptions 前端订单查询参数。
type OrderQueryOptions struct {
	TemplateID string `json:"templateId"`
	Status     string `json:"status"`
	From       string `json:"from"` // YYYY-MM-DD
	To         string `json:"to"`
	Limit      int    `json:"limit"`
}

// Service 模板工作台的应用服务。
type Service struct {
	mu    sync.Mutex
	db    *gorm.DB
	store orderschema.Store
	log   kernel.Logger

	sourceFactory SourceFactory // 测试 / 宿主注入的覆盖；nil 时走 sources.go 注册表
	aiFactory     func(name string) (ports.CompletionProvider, error)
	emit          Emitter
	baseCtx       context.Context // 进程生命周期 ctx，关窗口时统一取消在途任务
	runCancel     context.CancelFunc

	running  atomic.Bool
	lastRun  *RunReport
	lastRunM sync.RWMutex
}

var (
	defaultSvc *Service
	initErr    error
	initOnce   sync.Once
)

// Init 兼容入口：CLI / 示例等没有进程生命周期的场景，用 Background 作基 ctx。
func Init(db *gorm.DB, logger kernel.Logger) error {
	return InitWithContext(context.Background(), db, logger)
}

// InitWithContext 用主程序 DB 与进程生命周期 ctx 初始化服务（幂等）。
// logger 为 nil 时退化为 Noop；失败不 panic：模板工作台不可用，其余功能照常。
func InitWithContext(baseCtx context.Context, db *gorm.DB, logger kernel.Logger) error {
	initOnce.Do(func() {
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		if db == nil {
			initErr = errors.New("schemaservice: db 为空")
			return
		}
		if logger == nil {
			logger = kernel.NoopLogger{}
		}
		if err := orderschema.Migrate(db); err != nil {
			initErr = fmt.Errorf("schemaservice: 迁移失败: %w", err)
			return
		}
		store, err := orderschema.NewGormStore(db)
		if err != nil {
			initErr = fmt.Errorf("schemaservice: 初始化 Store 失败: %w", err)
			return
		}
		defaultSvc = &Service{
			db:      db,
			store:   store,
			log:     logger.With(kernel.F("module", "schemaservice")),
			baseCtx: baseCtx,
			// 数据源走 sources.go 注册表，不在初始化时写死微信。
			aiFactory: newAIProvider,
		}
	})
	return initErr
}

// SetEmitter 注入事件发射器（Wails runtime）。
func SetEmitter(e Emitter) {
	if defaultSvc == nil {
		return
	}
	defaultSvc.mu.Lock()
	defaultSvc.emit = e
	defaultSvc.mu.Unlock()
}

// Get 返回默认服务实例。
func Get() (*Service, error) {
	if defaultSvc == nil {
		return nil, fmt.Errorf("schemaservice: 未初始化（%v）", initErr)
	}
	return defaultSvc, nil
}

// CancelRun 取消默认服务正在运行的识别任务；没有任务在跑时返回 false。
func CancelRun() bool {
	svc, err := Get()
	if err != nil {
		return false
	}
	return svc.CancelRun()
}

func (s *Service) fire(event string, data any) {
	s.mu.Lock()
	e := s.emit
	s.mu.Unlock()
	if e != nil {
		e(event, data)
	}
}

// ---------------------------------------------------------------------------
// 模板操作
// ---------------------------------------------------------------------------

// ListTemplates 列模板；onlyPublished 只看已发布（识别路由只用已发布模板）。
func (s *Service) ListTemplates(onlyPublished bool) ([]*orderschema.Template, error) {
	return s.store.ListTemplates(context.Background(), onlyPublished)
}

// GetTemplate 取模板最新版。
func (s *Service) GetTemplate(id string) (*orderschema.Template, error) {
	return s.store.LatestTemplate(context.Background(), id)
}

// SaveTemplateInput 保存模板的输入。ID 为空=新建（v1 draft）；
// ID 已存在=在最新版之上落一个新版本（draft）。
type SaveTemplateInput struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Fields      orderschema.FieldDefs  `json:"fields"`
	Items       orderschema.ItemDef    `json:"items"`
	Signatures  orderschema.Signatures `json:"signatures"`
}

// SaveTemplate 新建或另存新版本（draft 状态）。返回落库后的完整模板。
func (s *Service) SaveTemplate(in SaveTemplateInput) (*orderschema.Template, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, errors.New("模板名称不能为空")
	}
	if len(in.Fields) == 0 {
		return nil, errors.New("模板至少需要一个字段")
	}
	if len(in.Signatures) == 0 {
		return nil, errors.New("模板至少需要一组路由签名（关键词），否则消息无法路由到该模板")
	}
	id := strings.TrimSpace(in.ID)
	now := time.Now()
	tpl := &orderschema.Template{
		Name:        name,
		Description: strings.TrimSpace(in.Description),
		Status:      orderschema.TemplateDraft,
		Fields:      in.Fields,
		Items:       in.Items,
		Signatures:  in.Signatures,
		CreatedAt:   now,
	}
	if id == "" {
		// 新模板：用名称生成稳定 ID。
		tpl.ID = templateIDFromName(name)
		tpl.Version = 1
	} else {
		latest, err := s.store.LatestTemplate(context.Background(), id)
		if err != nil {
			return nil, fmt.Errorf("模板 %s 不存在: %w", id, err)
		}
		tpl.ID = latest.ID
		tpl.Version = latest.Version + 1
		tpl.SampleCount = latest.SampleCount
	}
	if err := s.store.SaveTemplate(context.Background(), tpl); err != nil {
		return nil, fmt.Errorf("保存模板失败: %w", err)
	}
	return tpl, nil
}

// PublishTemplate 把模板最新版发布（已发布版本不可变，发布=新版本）。
func (s *Service) PublishTemplate(id string) (*orderschema.Template, error) {
	latest, err := s.store.LatestTemplate(context.Background(), id)
	if err != nil {
		return nil, fmt.Errorf("模板 %s 不存在: %w", id, err)
	}
	if latest.Status == orderschema.TemplatePublished {
		return latest, nil
	}
	latest.Version++
	latest.Status = orderschema.TemplatePublished
	now := time.Now()
	latest.PublishedAt = &now
	if err := s.store.SaveTemplate(context.Background(), latest); err != nil {
		return nil, fmt.Errorf("发布模板失败: %w", err)
	}
	return latest, nil
}

// DeprecateTemplate 停用模板：新版本标记 deprecated，识别路由不再命中。
func (s *Service) DeprecateTemplate(id string) (*orderschema.Template, error) {
	latest, err := s.store.LatestTemplate(context.Background(), id)
	if err != nil {
		return nil, fmt.Errorf("模板 %s 不存在: %w", id, err)
	}
	if latest.Status == orderschema.TemplateDeprecated {
		return latest, nil
	}
	latest.Version++
	latest.Status = orderschema.TemplateDeprecated
	if err := s.store.SaveTemplate(context.Background(), latest); err != nil {
		return nil, fmt.Errorf("停用模板失败: %w", err)
	}
	return latest, nil
}

// DeleteTemplate 物理删除模板全部版本（含其识别出的订单不动，订单按
// template_id 仍可追溯原文 raw_text/fields JSON）。仅在确认时调用。
func (s *Service) DeleteTemplate(id string) error {
	if s.db == nil {
		return errors.New("schemaservice: 无 DB 句柄")
	}
	res := s.db.Exec("DELETE FROM order_templates WHERE id = ?", id)
	if res.Error != nil {
		return fmt.Errorf("删除模板失败: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return orderschema.ErrNotFound
	}
	return nil
}

// InferInput 样例推断输入。
type InferInput struct {
	Samples  []string `json:"samples"`
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	UseAI    bool     `json:"useAi"`
	Provider string   `json:"provider"`
}

// InferResult 样例推断输出：draft 模板（未落库）+ 推断报告。
type InferResult struct {
	Template *orderschema.Template    `json:"template"`
	Report   *orderschema.InferReport `json:"report"`
}

// InferTemplate 从样例文本归纳模板草案（不落库，前端确认后走 SaveTemplate）。
func (s *Service) InferTemplate(ctx context.Context, in InferInput) (*InferResult, error) {
	samples := make([]string, 0, len(in.Samples))
	for _, x := range in.Samples {
		if t := strings.TrimSpace(x); t != "" {
			samples = append(samples, t)
		}
	}
	if len(samples) == 0 {
		return nil, errors.New("至少需要一条样例消息")
	}
	opt := orderschema.InferOptions{
		ID:          strings.TrimSpace(in.ID),
		Name:        strings.TrimSpace(in.Name),
		Description: "由样例推断生成",
	}
	if !in.UseAI {
		// 纯启发式归纳既不碰 AI 也不碰聊天源：没配 API Key、没接微信也能用。
		// 传 nil Proposer 即可（orderschema.InferTemplate 内部兜底）。
		tpl, rep, err := orderschema.InferTemplate(samples, opt)
		if err != nil {
			return nil, err
		}
		return &InferResult{Template: tpl, Report: rep}, nil
	}
	engine, err := s.buildInferEngine(in.Provider)
	if err != nil {
		return nil, err
	}
	tpl, rep, err := engine.InferTemplate(ctx, samples, opt)
	if err != nil {
		return nil, err
	}
	return &InferResult{Template: tpl, Report: rep}, nil
}

// ---------------------------------------------------------------------------
// 识别运行
// ---------------------------------------------------------------------------

// IsRunning 是否有识别任务在跑。
func (s *Service) IsRunning() bool { return s.running.Load() }

// GetLastRun 最近一次运行结果（nil 表示还没跑过）。
func (s *Service) GetLastRun() *RunReport {
	s.lastRunM.RLock()
	defer s.lastRunM.RUnlock()
	return s.lastRun
}

// StartRun 异步启动一次识别运行；同一时刻只允许一个。
// 进度经 schema-progress 事件推送，结果经 schema-result 事件 + GetLastRun 获取。
// 运行 ctx 挂在进程生命周期下：关窗口自动取消，也可由 CancelRun 手动中止。
func (s *Service) StartRun(opt RunOptions) error {
	if !s.running.CompareAndSwap(false, true) {
		return errors.New("已有识别任务在运行，请等待完成")
	}
	base := s.baseCtx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	s.mu.Lock()
	s.runCancel = cancel
	s.mu.Unlock()
	go func() {
		defer func() {
			cancel()
			s.mu.Lock()
			s.runCancel = nil
			s.mu.Unlock()
			s.running.Store(false)
		}()
		report := s.run(ctx, opt)
		s.lastRunM.Lock()
		s.lastRun = report
		s.lastRunM.Unlock()
		s.fire("schema-result", report)
	}()
	return nil
}

// CancelRun 取消当前识别任务；没有任务在跑时返回 false。
func (s *Service) CancelRun() bool {
	s.mu.Lock()
	cancel := s.runCancel
	s.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// setError 把失败归类：用户主动取消属正常中止，不算识别失败。
func (s *Service) setError(rep *RunReport, ctx context.Context, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		rep.Cancelled = true
		return
	}
	rep.Error = err.Error()
}

func (s *Service) run(ctx context.Context, opt RunOptions) *RunReport {
	rep := &RunReport{
		From:      opt.From,
		To:        opt.To,
		Source:    sourceName(opt),
		DryRun:    opt.DryRun,
		StartedAt: time.Now(),
	}
	defer func() {
		rep.FinishedAt = time.Now()
		if r := recover(); r != nil {
			rep.Error = fmt.Sprintf("内部错误: %v", r)
			s.log.Error("schema 识别运行 panic", fmt.Errorf("%v", r))
		}
	}()
	s.fire("schema-progress", map[string]any{"stage": "export", "text": "正在导出聊天记录…"})

	from, to, err := parseDayRange(opt.From, opt.To)
	if err != nil {
		s.setError(rep, ctx, err)
		return rep
	}
	engine, err := s.buildRunEngine(opt)
	if err != nil {
		s.setError(rep, ctx, err)
		return rep
	}
	text := "正在路由与抽取订单…"
	if opt.DryRun {
		text = "正在试跑（只抽取、不入库）…"
	}
	s.fire("schema-progress", map[string]any{"stage": "route", "text": text})

	exportOpt := ports.ExportOptions{
		Since:   from.Format("2006-01-02"),
		Until:   to.Format("2006-01-02"),
		DataDir: strings.TrimSpace(opt.DataDir),
	}
	var result *compose.Report
	if opt.DryRun {
		result, err = engine.RunDry(ctx, exportOpt)
	} else {
		result, err = engine.Run(ctx, exportOpt)
	}
	if err != nil {
		s.setError(rep, ctx, err)
		return rep
	}
	rep.Orders = result.Orders
	rep.Unmatched = result.Unmatched
	rep.OrderCount = len(result.Orders)
	rep.UnmatchedCount = len(result.Unmatched)
	s.log.Info("schema 识别运行完成",
		kernel.F("orders", rep.OrderCount),
		kernel.F("unmatched", rep.UnmatchedCount),
		kernel.F("source", rep.Source),
		kernel.F("dryRun", rep.DryRun))
	return rep
}

// buildRunEngine 运行期引擎：只要聊天源 + Store，不碰 AI 配置。
// 落地「格式归纳属设计期、运行期只做路由 + 按已注册 schema 抽取」的数据契约。
func (s *Service) buildRunEngine(opt RunOptions) (*compose.Engine, error) {
	src, err := s.resolveSource(opt)
	if err != nil {
		return nil, fmt.Errorf("初始化数据源失败: %w", err)
	}
	return compose.NewRecognitionEngine(src, s.store, s.log, 0.35)
}

// buildInferEngine 设计期引擎：只要 AI + Store，不碰聊天源配置。
func (s *Service) buildInferEngine(providerName string) (*compose.Engine, error) {
	if s.aiFactory == nil {
		return nil, errors.New("schemaservice: AI 工厂未注入")
	}
	p, err := s.aiFactory(providerName)
	if err != nil {
		return nil, fmt.Errorf("初始化 AI 失败: %w", err)
	}
	return compose.NewInferenceEngine(p, s.store, s.log)
}

// ---------------------------------------------------------------------------
// 订单查询
// ---------------------------------------------------------------------------

// ListOrders 按条件查询订单（新链路订单表 orders_v2）。
func (s *Service) ListOrders(opt OrderQueryOptions) ([]*orderschema.Order, error) {
	q := orderschema.OrderQuery{TemplateID: strings.TrimSpace(opt.TemplateID)}
	if opt.Status != "" {
		q.Status = orderschema.OrderStatus(opt.Status)
	}
	if opt.From != "" {
		t, err := time.ParseInLocation("2006-01-02", opt.From, time.Local)
		if err != nil {
			return nil, fmt.Errorf("from 日期格式无效: %w", err)
		}
		q.From = t
	}
	if opt.To != "" {
		t, err := time.ParseInLocation("2006-01-02", opt.To, time.Local)
		if err != nil {
			return nil, fmt.Errorf("to 日期格式无效: %w", err)
		}
		q.To = t.Add(24*time.Hour - time.Second) // 含结束日全天
	}
	if opt.Limit <= 0 || opt.Limit > 500 {
		q.Limit = 200
	}
	return s.store.FindOrders(context.Background(), q)
}

// SetOrderStatus 更新订单状态（pending_review → confirmed / discarded）。
func (s *Service) SetOrderStatus(id uint64, status string) (*orderschema.Order, error) {
	st := orderschema.OrderStatus(status)
	switch st {
	case orderschema.OrderPendingReview, orderschema.OrderConfirmed, orderschema.OrderDiscarded:
	default:
		return nil, fmt.Errorf("无效订单状态 %q", status)
	}
	o, err := s.store.GetOrder(context.Background(), id)
	if err != nil {
		return nil, err
	}
	o.Status = st
	if err := s.store.SaveOrder(context.Background(), o); err != nil {
		return nil, err
	}
	return o, nil
}

// DeleteOrder 物理删除一笔订单（误识别清理）。
func (s *Service) DeleteOrder(id uint64) error {
	if s.db == nil {
		return errors.New("schemaservice: 无 DB 句柄")
	}
	res := s.db.Exec("DELETE FROM orders_v2 WHERE id = ?", id)
	if res.Error != nil {
		return fmt.Errorf("删除订单失败: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return orderschema.ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// AI provider 工厂（读主程序 DB 配置的 ai_models）
// ---------------------------------------------------------------------------

// aiMaxConcurrency 模板工作台一次识别可能并发发起多条补全；限制同时在途
// 请求数，避免服务端 429 时所有协程一起重试互相踩踏。
const aiMaxConcurrency = 8

func newAIProvider(name string) (ports.CompletionProvider, error) {
	aiCfg := aiSettings()
	explicit := strings.TrimSpace(name) != ""
	if !explicit {
		switch {
		case aiCfg.deepseek.APIKey != "":
			name = "deepseek"
		case aiCfg.qwen.APIKey != "":
			name = "qwen"
		default:
			return nil, errors.New("未配置任何 AI 模型（应用设置中先配置 DeepSeek 或千问）")
		}
	}
	var c aiConfig
	key := strings.ToLower(name)
	switch key {
	case "deepseek":
		c = aiCfg.deepseek
	case "qwen", "千问":
		c = aiCfg.qwen
		key = "qwen"
	default:
		return nil, fmt.Errorf("未知 AI 提供方 %q", name)
	}
	if c.APIKey == "" {
		return nil, fmt.Errorf("%s 未配置 API Key", name)
	}
	primary := newAIProviderFromConfig(key, c)

	// 自动模式（前端未指定）且另一方也配了 Key 时组成主备链：主模型连续失败
	// （5xx / 429 / 网络）后自动切备用，识别任务不至于整体失败。
	// 显式指定 provider 是用户的精确选择，不做隐式切换。
	if !explicit {
		var backup *ai.Provider
		switch key {
		case "deepseek":
			if aiCfg.qwen.APIKey != "" {
				backup = newAIProviderFromConfig("qwen", aiCfg.qwen)
			}
		case "qwen":
			if aiCfg.deepseek.APIKey != "" {
				backup = newAIProviderFromConfig("deepseek", aiCfg.deepseek)
			}
		}
		if backup != nil {
			fb, err := ai.NewFallback(primary, backup)
			if err != nil {
				return nil, err
			}
			return fb, nil
		}
	}
	return primary, nil
}

// newAIProviderFromConfig 把主程序配置映射为 ai.ProviderConfig。
// Model 必须传：compose.CompletionFunc 刻意不指定模型，由各 provider 用自己的
// 默认模型；漏传会让请求体出现 "model": "default" 而被真实 API 拒绝。
// 价格字段当前 DB 配置中没有，留空即成本为 0（不编造计费数据）。
func newAIProviderFromConfig(name string, c aiConfig) *ai.Provider {
	return ai.NewProvider(ai.ProviderConfig{
		Name:           name,
		APIURL:         c.APIURL,
		APIKey:         c.APIKey,
		Model:          c.Model,
		TimeoutSec:     c.Timeout,
		MaxRetries:     2,
		MaxConcurrency: aiMaxConcurrency,
		RetryBaseDelay: 200 * time.Millisecond,
		RetryMaxDelay:  2 * time.Second,
	}, nil)
}

// ---------------------------------------------------------------------------
// 工具
// ---------------------------------------------------------------------------

func parseDayRange(from, to string) (time.Time, time.Time, error) {
	parse := func(s string) (time.Time, error) {
		s = strings.TrimSpace(s)
		if s == "" {
			return time.Time{}, errors.New("日期不能为空")
		}
		for _, layout := range []string{"2006-01-02", "2006-1-2"} {
			if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
				return t, nil
			}
		}
		return time.Time{}, fmt.Errorf("日期 %q 格式无效，应为 YYYY-MM-DD", s)
	}
	f, err := parse(from)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	t, err := parse(to)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if t.Before(f) {
		return time.Time{}, time.Time{}, errors.New("结束日期不能早于开始日期")
	}
	if t.Sub(f) > 31*24*time.Hour {
		return time.Time{}, time.Time{}, errors.New("一次识别最多覆盖 31 天")
	}
	return f, t, nil
}

// templateIDFromName 用名称生成稳定模板 ID：小写、空格转 -、去非法字符。
func templateIDFromName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_', r == '/':
			b.WriteRune('-')
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" {
		id = fmt.Sprintf("tpl-%d", time.Now().Unix())
	}
	return id
}
