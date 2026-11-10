// Package compose 是「聊天→识别→入库」后端的编排层，也是本架构的纵向切片示例：
// 它只依赖 ports 端口与 orderschema 的接口，不依赖任何具体实现（微信/DeepSeek/GORM 都是可替换的）。
//
// 引擎分三种，对应「设计期」与「运行期」两个互不依赖的阶段：
//   - NewEngine             完整引擎：两者兼备，保留原有调用契约。
//   - NewRecognitionEngine  运行期引擎：ChatSource + Store，按已发布模板做确定性路由与抽取，
//     **不依赖 AI**。格式归纳是设计期一次性动作，运行期不该被 AI 配置绑架。
//   - NewInferenceEngine    设计期引擎：AI + Store，只做样例归纳，**不依赖任何聊天源配置**。
//
// 运行 Run / RunDry 时按 ExportStage → RouteStage → PersistStage 串联，每阶段都是 ports.PipelineStage，
// 因此新增「粗筛」「订单操作规则」「人工审查」等只要再挂一个 Stage，不触碰现有阶段。
package compose

import (
	"context"
	"fmt"
	"time"

	"order_server/go/kernel"
	"order_server/go/orderschema"
	"order_server/go/ports"
)

// Engine 把可插拔模块编排成一个可用的识别后端。
type Engine struct {
	chat           ports.ChatSource
	ai             ports.CompletionProvider
	store          orderschema.Store
	logger         kernel.Logger
	routeThreshold float64
	sourceName     string
}

// NewEngine 构造完整引擎（设计期 + 运行期）。chat/ai/store 任一缺失都会拒绝构造，
// 保留原有契约：调用方明确要 AI 能力时，缺 AI 属于配置错误而非静默降级。
func NewEngine(chat ports.ChatSource, ai ports.CompletionProvider, store orderschema.Store, logger kernel.Logger, routeThreshold float64) (*Engine, error) {
	if chat == nil || ai == nil || store == nil {
		return nil, fmt.Errorf("compose: chat/ai/store 均不能为空")
	}
	e, err := NewRecognitionEngine(chat, store, logger, routeThreshold)
	if err != nil {
		return nil, err
	}
	e.ai = ai
	return e, nil
}

// NewRecognitionEngine 构造运行期引擎：只做「导出 → 路由 → 按已注册 schema 抽取 → 入库」。
// 不需要 CompletionProvider——模板是设计期由人确认过的，运行期用确定性规则回放即可。
// 因此未配置任何 AI Key 的部署也能正常识别订单。
func NewRecognitionEngine(chat ports.ChatSource, store orderschema.Store, logger kernel.Logger, routeThreshold float64) (*Engine, error) {
	if chat == nil || store == nil {
		return nil, fmt.Errorf("compose: chat/store 均不能为空")
	}
	if logger == nil {
		logger = kernel.NoopLogger{}
	}
	if routeThreshold <= 0 {
		routeThreshold = 0.35
	}
	return &Engine{
		chat:           chat,
		store:          store,
		logger:         logger,
		routeThreshold: routeThreshold,
		sourceName:     chat.Name(),
	}, nil
}

// NewInferenceEngine 构造设计期引擎：只需要 AI + Store，从样例归纳模板草案。
// 不要求配置任何聊天源——用户可能还没接微信，只是想先用样例试出一套模板。
func NewInferenceEngine(ai ports.CompletionProvider, store orderschema.Store, logger kernel.Logger) (*Engine, error) {
	if ai == nil || store == nil {
		return nil, fmt.Errorf("compose: ai/store 均不能为空")
	}
	if logger == nil {
		logger = kernel.NoopLogger{}
	}
	return &Engine{ai: ai, store: store, logger: logger, routeThreshold: 0.35}, nil
}

// Report 一次识别运行的结果。
type Report struct {
	Orders    []*orderschema.Order // Run：实际成功入库的订单；RunDry：抽取出的订单（未入库，无 ID）
	Unmatched []ports.ChatMessage  // 未命中任何模板、需人工审查的消息
	DryRun    bool                 // true 表示本次为试跑，Orders 未落库
}

// Run 执行完整流水线：导出 → 路由+抽取 → 入库。
func (e *Engine) Run(ctx context.Context, opt ports.ExportOptions) (*Report, error) {
	return e.run(ctx, opt, false)
}

// RunDry 试跑：完整执行导出 / 路由 / 抽取，但**不写库**。
// 用于验证模板效果或预览识别结果，不污染 orders，也不触发去重水位变化。
func (e *Engine) RunDry(ctx context.Context, opt ports.ExportOptions) (*Report, error) {
	return e.run(ctx, opt, true)
}

func (e *Engine) run(ctx context.Context, opt ports.ExportOptions, dryRun bool) (*Report, error) {
	if e.chat == nil {
		return nil, fmt.Errorf("compose: 当前为设计期归纳引擎（未注入 ChatSource），无法执行识别；请用 NewEngine / NewRecognitionEngine")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pc := ports.NewPipelineContext()
	pc.Ctx = ctx
	pc.Logger = e.logger

	stages := []ports.PipelineStage{
		&exportStage{src: e.chat, opt: opt},
		&routeStage{store: e.store, threshold: e.routeThreshold, logger: e.logger, source: e.sourceName},
		&persistStage{store: e.store, logger: e.logger, dryRun: dryRun},
	}
	cur := pc
	for _, st := range stages {
		// 阶段间检查取消：即便某个 ChatSource 实现不响应 ctx，也不会继续往下写库。
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next, err := st.Process(ctx, cur)
		if err != nil {
			return nil, fmt.Errorf("compose: 阶段 %q 失败: %w", st.Name(), err)
		}
		cur = next
	}
	return &Report{Orders: cur.Orders, Unmatched: cur.Unmatched, DryRun: dryRun}, nil
}

// InferTemplate 用 AI（经 CompletionProvider 注入）从样例归纳模板草案。
// 即使 AI 失败也会退化为启发式，不会中断模板生成。
// 仅完整引擎可用；运行期引擎没有 AI，遇到调用会明确报错而不是空指针。
func (e *Engine) InferTemplate(ctx context.Context, samples []string, opt orderschema.InferOptions) (*orderschema.Template, *orderschema.InferReport, error) {
	if e.ai == nil {
		return nil, nil, fmt.Errorf("compose: 当前为运行期引擎（未注入 AI），无法归纳模板；纯启发式请直接调用 orderschema.InferTemplate，或用 NewEngine 构造完整引擎")
	}
	opt.Proposer = orderschema.LLMProposer{Complete: CompletionFunc(e.ai)}
	return orderschema.InferTemplate(samples, opt)
}

// ---------------------------------------------------------------------------
// 流水线阶段
// ---------------------------------------------------------------------------

// exportStage 调用 ChatSource 把聊天记录导出为归一化消息。
type exportStage struct {
	src ports.ChatSource
	opt ports.ExportOptions
}

func (s *exportStage) Name() string { return "export" }

func (s *exportStage) Process(ctx context.Context, in *ports.PipelineContext) (*ports.PipelineContext, error) {
	res, err := s.src.Export(ctx, s.opt)
	if err != nil {
		return in, err
	}
	if res != nil {
		in.Messages = res.Messages
	}
	return in, nil
}

// routeStage 加载已发布模板，对每条消息自动路由（不逐条选模板），命中后用确定性规则抽取，
// 未命中的消息进入人工审查队列。
type routeStage struct {
	store     orderschema.Store
	threshold float64
	logger    kernel.Logger
	source    string
}

func (s *routeStage) Name() string { return "route" }

func (s *routeStage) Process(ctx context.Context, in *ports.PipelineContext) (*ports.PipelineContext, error) {
	if in.Templates == nil {
		tpls, err := s.store.ListTemplates(ctx, true)
		if err != nil {
			return in, err
		}
		in.Templates = tpls
	}
	for _, m := range in.Messages {
		if err := ctx.Err(); err != nil {
			return in, err
		}
		if m.IsSystem {
			continue
		}
		match, ok := orderschema.RouteBest(m.Content, in.Templates, s.threshold)
		if !ok {
			in.Unmatched = append(in.Unmatched, m)
			continue
		}
		tpl, err := s.store.GetTemplate(ctx, match.TemplateID, match.Version)
		if err != nil {
			in.Unmatched = append(in.Unmatched, m)
			s.logger.Warn("模板读取失败，转入人工审查", kernel.F("template", match.TemplateID), kernel.F("err", err.Error()))
			continue
		}
		rows := orderschema.Replay(tpl, []string{m.Content})
		if len(rows) == 0 {
			in.Unmatched = append(in.Unmatched, m)
			continue
		}
		row := rows[0]
		fields := orderschema.FieldValues{}
		for k, v := range row.Fields {
			fields[k] = v
		}
		in.Orders = append(in.Orders, &orderschema.Order{
			TemplateID:      tpl.ID,
			TemplateVersion: tpl.Version,
			Source:          s.source,
			SourceMsgID:     m.ID,
			RawText:         m.Content,
			OrderTime:       parseMsgTime(m.Time),
			Status:          orderschema.OrderPendingReview,
			Fields:          fields,
			Items:           row.Items,
		})
	}
	return in, nil
}

// persistStage 把识别出的订单入库；去重冲突或其它错误只记录并跳过该笔，不中断整批。
// dryRun 时原样保留抽取结果但不落库，供调用方预览。
type persistStage struct {
	store  orderschema.Store
	logger kernel.Logger
	dryRun bool
}

func (s *persistStage) Name() string { return "persist" }

func (s *persistStage) Process(ctx context.Context, in *ports.PipelineContext) (*ports.PipelineContext, error) {
	if s.dryRun {
		return in, nil
	}
	saved := make([]*orderschema.Order, 0, len(in.Orders))
	for _, o := range in.Orders {
		if err := ctx.Err(); err != nil {
			return in, err
		}
		if err := s.store.SaveOrder(ctx, o); err != nil {
			s.logger.Warn("保存订单失败（跳过）", kernel.F("template", o.TemplateID), kernel.F("msg", o.SourceMsgID), kernel.F("err", err.Error()))
			continue
		}
		saved = append(saved, o)
	}
	in.Orders = saved
	return in, nil
}

// parseMsgTime 把 RFC3339 / 常见业务时间串解析为 time.Time，失败回退当前时间。
func parseMsgTime(s string) time.Time {
	if s == "" {
		return time.Now()
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Now()
}
