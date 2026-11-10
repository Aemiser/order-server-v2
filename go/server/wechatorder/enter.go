package wechatorder

import (
	"fmt"
	"sync"
	"sync/atomic"

	ordertutil "order_server/go/utils/order"
)

// ProgressFunc 用于把后台进度推给前端（Wails 事件）。
type ProgressFunc func(stage string, done, total int, message string)

// AutoOrderDetail 是每条候选消息的判定明细，方便核对 AI 过滤效果。
type AutoOrderDetail struct {
	Sender        string `json:"sender"`
	MessageTime   string `json:"messageTime"`
	OrderDate     string `json:"orderDate"`
	IsToday       bool   `json:"isToday"`
	Score         int    `json:"score"`
	IsOrder       bool   `json:"isOrder"`
	DateConfirmed bool   `json:"dateConfirmed"`
	NeedsReview   bool   `json:"needsReview"`
	Confidence    string `json:"confidence"`
	Disposition   string `json:"disposition"` // 预览 / 待审查 / 非订单 / 日期不符 / 失败
	Reason        string `json:"reason"`
	OrderCount    int    `json:"orderCount"`
	Recipient     string `json:"recipient"`
	SpecFixed     bool   `json:"specFixed"` // 商品规格与原文不符，已按原文确定性校正
	Error         string `json:"error"`
}

// ReviewOrderItem 是「待人工审查」区的一条数据。
// AI 识别为订单，但缺少日期标记、日期与目标订单日期不符、或判定置信度偏低，
// 需要人工核对发送时间等信息后，主动点按钮才能进入数据预览区。
type ReviewOrderItem struct {
	ID          string                        `json:"id"`          // 稳定标识，用于「加入预览区」
	Sender      string                        `json:"sender"`      // 消息发送人
	MessageTime string                        `json:"messageTime"` // 消息发送时间（完整）
	MessageDate string                        `json:"messageDate"` // 消息发送日期
	OrderDate   string                        `json:"orderDate"`   // 提取到的订单日期标记，空 = 没写
	Reason      string                        `json:"reason"`      // 为什么需要人工审查
	Confidence  string                        `json:"confidence"`
	RawContent  string                        `json:"rawContent"` // 原始聊天消息，供人工核对
	OrderCount  int                           `json:"orderCount"`
	Orders      []ordertutil.ExceladdressInfo `json:"orders"` // 识别出的订单字段（可预览）
	// 以下字段只在「订单操作」审查项上有值；普通审查项保持零值，前端按 kind 区分渲染。
	Kind         string                        `json:"kind,omitempty"`         // action = 订单操作；空 = 普通待审订单
	ActionType   string                        `json:"actionType,omitempty"`   // delete / modify
	ActionStatus string                        `json:"actionStatus,omitempty"` // applied / manual / ambiguous / unmatched
	MatchBy      string                        `json:"matchBy,omitempty"`      // 定位方式：引用精确 / AI 候选 / 关键词 / 未定位
	Changes      string                        `json:"changes,omitempty"`      // 变更内容说明
	Quote        string                        `json:"quote,omitempty"`        // 被引用的订单消息原文
	Before       []ordertutil.ExceladdressInfo `json:"before,omitempty"`       // 操作前的原始订单
	After        []ordertutil.ExceladdressInfo `json:"after,omitempty"`        // 操作后的订单（删除时为空）
	Candidates   []string                      `json:"candidates,omitempty"`   // 歧义 / 未定位时的候选订单摘要
}

// ActionOrderItem 是一键识别第二阶段「订单操作」的结果摘要，供前端展示与核对。
type ActionOrderItem struct {
	ID          string `json:"id"`
	Action      string `json:"action"`      // delete / modify
	ActionLabel string `json:"actionLabel"` // 删除订单 / 修改订单
	Sender      string `json:"sender"`
	MessageTime string `json:"messageTime"`
	RawContent  string `json:"rawContent"` // 操作消息原文
	Quote       string `json:"quote"`      // 引用的订单原文
	Reason      string `json:"reason"`
	Confidence  string `json:"confidence"`
	TargetLabel string `json:"targetLabel"` // 定位到的订单摘要
	MatchedBy   string `json:"matchedBy"`
	Status      string `json:"status"` // applied / manual / ambiguous / unmatched
	Changes     string `json:"changes"`
	ReviewID    string `json:"reviewId"` // 对应的人工审查项
}

// AutoOrderResult 是「一键获取订单」按钮 / 后台自动运行的完整返回。
type AutoOrderResult struct {
	Success      bool                          `json:"success"`
	Message      string                        `json:"message"`
	Source       string                        `json:"source"`
	ExportFrom   string                        `json:"exportFrom"`
	ExportTo     string                        `json:"exportTo"`
	Exported     int                           `json:"exported"`
	Candidates   int                           `json:"candidates"`
	Dropped      int                           `json:"dropped"`
	AIOrders     int                           `json:"aiOrders"`
	Skipped      int                           `json:"skipped"`
	Failed       int                           `json:"failed"`
	Workers      int                           `json:"workers"`
	TotalOrders  int                           `json:"totalOrders"`
	Orders       []ordertutil.ExceladdressInfo `json:"orders"`
	TargetDate   string                        `json:"targetDate"`   // 本轮要识别的订单日期
	ReviewCount  int                           `json:"reviewCount"`  // 待人工审查的消息数
	ReviewOrders []ReviewOrderItem             `json:"reviewOrders"` // 不确认数据，人工审查后才可加入预览区
	Details      []AutoOrderDetail             `json:"details"`
	// 订单操作层（第二阶段）：识别聊天记录里的删除 / 修改，并把被处理过的订单送进人工审查区。
	ActionCandidates int               `json:"actionCandidates"` // 疑似操作消息数
	ActionCount      int               `json:"actionCount"`      // 识别为操作的条数
	ActionApplied    int               `json:"actionApplied"`    // 已定位并生成审查项的条数
	ActionFailed     int               `json:"actionFailed"`     // AI 请求失败的条数
	ActionManual     int               `json:"actionManual"`     // 需人工定位 / 补充内容的条数
	Actions          []ActionOrderItem `json:"actions"`          // 逐条识别与定位结果
	Warnings         []string          `json:"warnings"`
	ConfigPath       string            `json:"configPath"`
	// 自动化扩展字段
	NewMessages  int    `json:"newMessages"`  // 增量模式下本轮的新消息数
	Duplicates   int    `json:"duplicates"`   // 跨运行去重掉的订单数
	Imported     int    `json:"imported"`     // 本轮自动写入数据库的订单数
	ExportedFile string `json:"exportedFile"` // 自动生成的 Excel 路径
	Trigger      string `json:"trigger"`      // manual / auto / startup
	AutoMode     bool   `json:"autoMode"`
	NextRunAt    string `json:"nextRunAt"`
}

// Run 是前端「一键识别订单」的入口。
// 语义固定：导出群聊 → 规则粗筛 → AI 识别 → 结果填入数据预览区。
// 不写数据库、不生成文件、不做增量过滤或跨运行去重，每次点击都是独立全量识别，
// 因此按钮不受任何状态限制，可随时重复点击。
func Run(progress ProgressFunc) AutoOrderResult {
	return RunWith(RunOptions{Trigger: "manual", PreviewOnly: true}, progress)
}

// RunOnDate 按指定日期识别订单，供「一键识别订单」按钮旁的日期选择器使用。
// 日期只作用于本轮，不写配置文件；空串表示沿用配置默认（每次默认今天）。
func RunOnDate(targetDate string, incremental bool, progress ProgressFunc) AutoOrderResult {
	return RunWith(RunOptions{Trigger: "manual", PreviewOnly: true, TargetDate: targetDate, Incremental: incremental}, progress)
}

// RunFull 保留作兼容入口，与 Run 完全一致（纯识别、只回填预览区）。
func RunFull(progress ProgressFunc) AutoOrderResult {
	return Run(progress)
}

// AnalyzeAll 起 cfg.Concurrency 个协程并发请求 DeepSeek（默认 100 个）。
func AnalyzeAll(candidates []Candidate, cfg Config, progress ProgressFunc) []AnalyzeItem {
	return AnalyzeAllWithOptions(candidates, cfg, AnalyzeOptions{
		FilterOff:  !cfg.UseRuleFilter,
		TargetDate: TargetDateString(cfg),
	}, progress)
}

// AnalyzeAllWithOptions 与一键识别完全相同的并发识别引擎，只额外允许调用方
// 覆盖提示词的运行时选项（账单审核用它注入「日期范围」上下文）。
func AnalyzeAllWithOptions(candidates []Candidate, cfg Config, opts AnalyzeOptions, progress ProgressFunc) []AnalyzeItem {
	workers := cfg.Concurrency
	if workers <= 0 {
		workers = 100
	}
	key, url, model := aiSettings()
	items := make([]AnalyzeItem, len(candidates))
	jobs := make(chan int)
	var wg sync.WaitGroup
	var done int64

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				c := candidates[idx]
				res, err := AnalyzeOneWithOptions(key, url, model, c.Message.Content, cfg.AITimeout, opts)
				if err != nil {
					// 网络抖动或限流时重试一次
					res, err = AnalyzeOneWithOptions(key, url, model, c.Message.Content, cfg.AITimeout, opts)
				}
				items[idx] = AnalyzeItem{Candidate: c, Result: res, Err: err}
				n := int(atomic.AddInt64(&done, 1))
				if progress != nil && (n%5 == 0 || n == len(candidates)) {
					progress("ai", n, len(candidates),
						fmt.Sprintf("DeepSeek 分析中 %d/%d（%d 协程）", n, len(candidates), workers))
				}
			}
		}()
	}
	for i := range candidates {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return items
}

// TestExport 只跑导出链路，供配置页的「测试导出」按钮使用。
func TestExport() map[string]interface{} {
	cfg := LoadConfig()
	payload, err := Export(cfg)
	out := map[string]interface{}{
		"success":       err == nil,
		"group":         cfg.Group,
		"days":          cfg.Days,
		"targetDate":    TargetDateString(cfg),
		"targetLabel":   TargetDateLabel(EffectiveTargetDate(cfg)),
		"useRuleFilter": cfg.UseRuleFilter,
		"source":        payload.Source,
		"from":          payload.From,
		"to":            payload.To,
		"count":         payload.Count,
		"warnings":      payload.Warnings,
		"configPath":    ConfigPath(),
		"keySource":     payload.KeySource,
		"keyCache":      payload.KeyCache,
	}
	if err != nil {
		out["message"] = err.Error()
		return out
	}
	candidates, dropped := RuleFilterWithConfig(payload.Messages, cfg)
	out["candidates"] = len(candidates)
	out["dropped"] = dropped
	mode := "规则粗筛保留"
	if !cfg.UseRuleFilter {
		mode = "粗筛已关闭，全量交 AI 判定"
	}
	out["message"] = fmt.Sprintf("导出成功：目标订单日期 %s，窗口 %s~%s 共 %d 条消息，%s %d 条（跳过 %d 条空消息）",
		TargetDateString(cfg), payload.From, payload.To, payload.Count, mode, len(candidates), dropped)
	if len(payload.Messages) > 0 {
		sample := payload.Messages[0].Content
		if len([]rune(sample)) > 120 {
			sample = string([]rune(sample)[:120]) + "…"
		}
		out["sample"] = sample
	}
	return out
}
