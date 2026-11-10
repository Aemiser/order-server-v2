package wechatorder

// 订单识别链路的状态与调度兼容层。
//
// 当前产品语义（runPreviewOnly）：一键按钮只做「导出 → 粗筛 → AI 识别」，
// 结果仅回填数据预览区，不写数据库、不生成文件、不读写状态文件、不做增量去重。
// 下方 AutoState / 水位线 / 定时调度代码保留作后台增量运行的兼容实现，
// 默认停用，且同样不会自动落库或自动导出。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"order_server/go/global"
	"order_server/go/models"
	ordertutil "order_server/go/utils/order"

	"github.com/sirupsen/logrus"
)

// ---------------- 运行状态（水位线 / 去重） ----------------

type AutoState struct {
	LastMessageTs  float64  `json:"lastMessageTs"`  // 已处理消息的最大 Unix 时间戳
	BoundaryIDs    []string `json:"boundaryIds"`    // 与水位线同一秒、已处理的消息 ID
	ImportedHashes []string `json:"importedHashes"` // 已识别/入库的订单内容指纹
	LastRunAt      string   `json:"lastRunAt"`
	LastSuccessAt  string   `json:"lastSuccessAt"`
	LastMessage    string   `json:"lastMessage"`
	LastTrigger    string   `json:"lastTrigger"`
	LastError      string   `json:"lastError"`
	NextRunAt      string   `json:"nextRunAt"`
	TotalRuns      int      `json:"totalRuns"`
	TotalOrders    int      `json:"totalOrders"`
	TotalImported  int      `json:"totalImported"`
}

func StatePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "order_server", "wechat_auto_state.json")
}

func LoadAutoState() AutoState {
	var s AutoState
	data, err := os.ReadFile(StatePath())
	if err != nil || json.Unmarshal(data, &s) != nil {
		return AutoState{}
	}
	return s
}

func SaveAutoState(s AutoState) error {
	path := StatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func ResetAutoState() error {
	err := os.Remove(StatePath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// AutoStatus 给前端展示自动化运行状况（不含任何密钥/消息内容）。
func AutoStatus() map[string]interface{} {
	cfg := LoadConfig()
	s := LoadAutoState()
	return map[string]interface{}{
		"enabled":         cfg.AutoRun,
		"runOnStartup":    cfg.RunOnStartup,
		"intervalMinutes": cfg.AutoRunInterval,
		"incremental":     cfg.Incremental,
		"autoImport":      cfg.AutoImport,
		"autoExportExcel": cfg.AutoExportExcel,
		"exportDir":       cfg.ExportDir,
		"statePath":       StatePath(),
		"lastRunAt":       s.LastRunAt,
		"lastSuccessAt":   s.LastSuccessAt,
		"lastTrigger":     s.LastTrigger,
		"lastMessage":     s.LastMessage,
		"lastError":       s.LastError,
		"nextRunAt":       s.NextRunAt,
		"totalRuns":       s.TotalRuns,
		"totalOrders":     s.TotalOrders,
		"totalImported":   s.TotalImported,
		"lastMessageTs":   s.LastMessageTs,
		"trackedHashes":   len(s.ImportedHashes),
	}
}

func orderHash(o models.AddressInfo) string {
	norm := func(s string) string { return strings.Join(strings.Fields(s), "") }
	parts := []string{norm(o.Recipient), norm(o.Phone), norm(o.Address), norm(o.Remarks),
		o.CreatedAt.Format("2006-01-02")}
	for _, s := range o.ProductSpecs {
		parts = append(parts, fmt.Sprintf("%s=%d", norm(s.Name), s.Weight))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:8])
}

func hashSet(hashes []string) map[string]bool {
	m := make(map[string]bool, len(hashes)+16)
	for _, h := range hashes {
		m[h] = true
	}
	return m
}

// filterNewMessages 按水位线挑出尚未处理过的新消息。
func filterNewMessages(msgs []ChatMessage, s AutoState, incremental bool) []ChatMessage {
	if !incremental || s.LastMessageTs <= 0 {
		return msgs
	}
	boundary := make(map[string]bool, len(s.BoundaryIDs))
	for _, id := range s.BoundaryIDs {
		boundary[id] = true
	}
	out := make([]ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.Ts > s.LastMessageTs || (m.Ts == s.LastMessageTs && !boundary[m.ID]) {
			out = append(out, m)
		}
	}
	return out
}

// advanceWatermark 推进水位线；只保留边界那一秒的消息 ID，避免状态文件无限膨胀。
func advanceWatermark(s *AutoState, msgs []ChatMessage) {
	maxTs := s.LastMessageTs
	for _, m := range msgs {
		if m.Ts > maxTs {
			maxTs = m.Ts
		}
	}
	ids := make(map[string]bool)
	if maxTs == s.LastMessageTs {
		for _, id := range s.BoundaryIDs {
			ids[id] = true
		}
	}
	for _, m := range msgs {
		if m.Ts == maxTs {
			ids[m.ID] = true
		}
	}
	s.LastMessageTs = maxTs
	s.BoundaryIDs = s.BoundaryIDs[:0]
	for id := range ids {
		s.BoundaryIDs = append(s.BoundaryIDs, id)
	}
	if len(s.ImportedHashes) > 5000 {
		s.ImportedHashes = append([]string(nil), s.ImportedHashes[len(s.ImportedHashes)-3000:]...)
	}
}

// ---------------- 全自动流水线 ----------------

type RunOptions struct {
	Trigger     string // manual / auto / startup
	ForceFull   bool   // 忽略增量水位线，全量重新识别
	Commit      bool   // 已废弃：识别流程不再写数据库或生成文件
	PreviewOnly bool   // 只做识别，结果仅回填数据预览区（一键按钮专用）
	TargetDate  string // 本轮目标订单日期 YYYY-MM-DD；空 = 用配置默认（跟随今天）
	Incremental bool   // 增量模式：只把「目标日期当天数据库里还没有」的订单放进数据预览区
}

var runMu sync.Mutex

// runPreviewOnly 是「一键识别订单」的唯一语义：导出 → 粗筛 → AI 识别 → 填入数据预览区。
//
// 刻意不做以下「多余操作」：不读/写自动运行状态文件、不推进增量水位线、不写数据库、
// 不生成 Excel、不弹文件框。全量模式下每次点击都是一次独立的完整识别，因此按钮可以
// 无限次重复点击，且每轮结果都完整覆盖数据预览区。
// 增量模式（incremental=true，前端默认）在此基础上再做一步：识别完成后，把「目标日期当天
// 数据库里已经存在」的订单从本轮结果里剔除（普通待审订单也一并去重，订单操作项保留），
// 只把还没入库的新订单放进数据预览区 / 人工审查区，避免重复识别已入库的订单。
func runPreviewOnly(cfg Config, incremental bool, progress ProgressFunc) AutoOrderResult {
	target := EffectiveTargetDate(cfg)
	result := AutoOrderResult{
		ConfigPath: ConfigPath(), Workers: cfg.Concurrency,
		Trigger: "manual", AutoMode: false,
		TargetDate: target.Format("2006-01-02"),
	}
	// 每一轮都是独立的全量识别：先清掉上一轮遗留的待审查数据。
	replaceReviewRecords(nil)
	emit := func(stage string, done, total int, msg string) {
		if progress != nil {
			progress(stage, done, total, msg)
		}
	}

	emit("export", 0, 1, fmt.Sprintf("正在导出群「%s」在 %s 前 %d 天的聊天记录…",
		cfg.Group, result.TargetDate, cfg.Days))
	payload, err := Export(cfg)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	result.Source = payload.Source
	result.ExportFrom, result.ExportTo = payload.From, payload.To
	result.Exported = payload.Count
	result.NewMessages = payload.Count
	result.Warnings = append(result.Warnings, payload.Warnings...)
	emit("export", 1, 1, fmt.Sprintf("导出完成：%d 条消息（窗口 %s~%s，来源 %s）",
		payload.Count, payload.From, payload.To, payload.Source))

	filterNote := "规则粗筛"
	if !cfg.UseRuleFilter {
		filterNote = "粗筛已关闭，全量交 AI"
	}
	candidates, dropped := RuleFilterWithConfig(payload.Messages, cfg)
	result.Candidates, result.Dropped = len(candidates), dropped
	emit("rule", len(candidates), len(payload.Messages),
		fmt.Sprintf("%s：%d 条交 AI 判定（跳过 %d 条）", filterNote, len(candidates), dropped))

	// 没有候选时也要刷新预览区，避免留着上一轮的旧结果
	if len(candidates) == 0 {
		global.AddressList = nil
		result.Success = true
		result.Message = fmt.Sprintf("目标日期 %s：导出 %d 条消息，未发现疑似订单，数据预览区已清空",
			result.TargetDate, payload.Count)
		emit("done", 0, 0, result.Message)
		return result
	}

	items := AnalyzeAll(candidates, cfg, emit)
	split := classifyItems(items, cfg, target)

	// 第二阶段：等全部订单识别完成后，再识别聊天记录里的「订单操作」并统一回放。
	// 被操作命中的订单会从预览区移出，连同「原始 → 操作后」对照与操作依据进入人工审查区；
	// 人工点「加入预览区」= 采用操作结果，点「忽略」= 撤销操作并恢复原始订单。
	// 本层不写数据库、不生成文件，也不会把任何订单自动入库。
	actionHits, orderMsgs := AnalyzeActions(payload.Messages, items, cfg, emit)
	actionReport := applyActions(&split, actionHits, orderMsgs, result.TargetDate)
	result.ActionCandidates = actionReport.Candidates
	result.ActionCount = actionReport.Actions
	result.ActionApplied = actionReport.Applied
	result.ActionManual = actionReport.Manual
	result.ActionFailed = actionReport.Failed
	result.Actions = actionReport.Items
	if actionReport.Failed > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("有 %d 条疑似订单操作消息 AI 判定失败，已跳过，可重试", actionReport.Failed))
	}
	if actionReport.Manual > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("有 %d 条订单操作未能自动定位或缺少变更内容，已列入人工审查区", actionReport.Manual))
	}

	// 增量模式：把「目标日期当天数据库里已经存在」的订单从本轮结果里剔除，
	// 只把还没入库的新订单放进数据预览区 / 人工审查区，避免重复识别。
	if incremental {
		existing, err := loadExistingOrdersForDate(target)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("增量模式查询今日已存订单失败，已按全量保留结果：%s", err.Error()))
		} else if len(existing) > 0 {
			var kept []models.AddressInfo
			skipped := 0
			for _, o := range split.Preview {
				if existing[orderDedupKey(o)] {
					skipped++
					continue
				}
				kept = append(kept, o)
			}
			split.Preview = kept
			result.Duplicates = skipped

			// 人工审查区里的普通订单（未命中日期 / 低置信）同样去重；订单操作项保留。
			var keptReview []reviewRecord
			for _, r := range split.Review {
				if r.Public.Kind == "action" || len(r.Raw) == 0 {
					keptReview = append(keptReview, r)
					continue
				}
				if existing[reviewDedupKey(r.Raw)] {
					result.Duplicates++
					continue
				}
				keptReview = append(keptReview, r)
			}
			split.Review = keptReview
		}
	}

	result.AIOrders = split.AIOrders
	result.Skipped = split.Skipped
	result.Failed = split.Failed
	result.Details = split.Details
	result.ReviewCount = len(split.Review)
	result.ReviewOrders = make([]ReviewOrderItem, 0, len(split.Review))
	for _, r := range split.Review {
		result.ReviewOrders = append(result.ReviewOrders, r.Public)
	}
	replaceReviewRecords(split.Review)
	if split.DroppedByDate > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("严格模式下跳过 %d 条日期不符 %s 的订单", split.DroppedByDate, result.TargetDate))
	}
	if split.SpecFixed > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("有 %d 条消息的商品规格与原文不一致，已按原文自动校正（品名/重量逐项对齐）", split.SpecFixed))
	}

	// 回填数据预览区：只放「日期确认」的订单；不确认的停在人工审查区，
	// 必须由用户点「加入预览区」才追加进来，绝不自动入库、不生成文件。
	global.AddressList = nil
	collected := ordertutil.SetVid(split.Preview)
	global.AddressList = collected
	result.TotalOrders = len(collected)
	result.Orders = ordertutil.AddressToExcelAddress(collected)

	// AI 全部失败时如实报错，但不写入任何状态，随时可以再点一次重试。
	if result.Failed >= len(candidates) && result.AIOrders == 0 {
		result.Success = false
		result.Message = fmt.Sprintf("AI 请求全部失败（%d 条），数据预览区未更新，可直接重试", result.Failed)
		emit("done", 0, 0, result.Message)
		return result
	}
	result.Success = true
	result.Message = fmt.Sprintf("目标日期 %s：导出 %d 条 → %s %d 条 → 确认 %d 笔进预览区，待人工审查 %d 条（失败 %d 条）",
		result.TargetDate, result.Exported, filterNote, result.Candidates,
		result.TotalOrders, result.ReviewCount, result.Failed)
	if result.ActionCount > 0 {
		result.Message += fmt.Sprintf("；识别订单操作 %d 条，其中 %d 条已处理并转入人工审查",
			result.ActionCount, result.ActionApplied+result.ActionManual)
	}
	if incremental && result.Duplicates > 0 {
		result.Message += fmt.Sprintf("；增量模式已跳过 %d 笔今日已存在订单", result.Duplicates)
	}
	emit("done", result.TotalOrders, result.TotalOrders, result.Message)
	logrus.Infof("微信订单识别完成: %s", result.Message)
	return result
}

// orderDedupKey 计算一笔订单的「内容唯一指纹」：收件人 + 手机号 + 收货地址 + 排序后的商品规格。
// 用于增量模式判断「这笔订单今天是否已经在数据库里」，规避识别结果里规格顺序不同造成的误判。
func orderDedupKey(a models.AddressInfo) string {
	specs := make([]string, 0, len(a.ProductSpecs))
	for _, s := range a.ProductSpecs {
		specs = append(specs, strings.TrimSpace(s.Name)+strconv.Itoa(s.Weight))
	}
	sort.Strings(specs)
	return strings.Join([]string{
		strings.TrimSpace(a.Recipient),
		strings.TrimSpace(a.Phone),
		strings.TrimSpace(a.Address),
		strings.Join(specs, "|"),
	}, "\x00")
}

// reviewDedupKey 取一笔人工审查项里所有待入库订单指纹的合并串，用于增量去重。
func reviewDedupKey(raw []models.AddressInfo) string {
	keys := make([]string, 0, len(raw))
	for _, o := range raw {
		keys = append(keys, orderDedupKey(o))
	}
	sort.Strings(keys)
	return strings.Join(keys, "\x01")
}

// loadExistingOrdersForDate 返回「目标日期当天」已入库订单的内容指纹集合，供增量模式去重。
// 唯一性按「创建时间（落到目标日期那一天）+ 订单内容」判定；查询失败返回 error，调用方据此回退全量。
func loadExistingOrdersForDate(target time.Time) (map[string]bool, error) {
	loc := target.Location()
	start := time.Date(target.Year(), target.Month(), target.Day(), 0, 0, 0, 0, loc)
	end := start.Add(24 * time.Hour)
	var existing []models.AddressInfo
	if err := global.Db.Where("created_at >= ? AND created_at < ?", start, end).Find(&existing).Error; err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(existing))
	for _, e := range existing {
		set[orderDedupKey(e)] = true
	}
	return set, nil
}

// RunWith 是完整链路。一键按钮固定走 PreviewOnly（纯识别）；
// 后台定时路径保留作兼容，但同样不会写数据库或生成文件。
func RunWith(opts RunOptions, progress ProgressFunc) AutoOrderResult {
	if opts.PreviewOnly {
		runMu.Lock()
		defer runMu.Unlock()
		cfg := LoadConfig()
		// 按钮旁临时选的日期只覆盖本轮：导出窗口、AI 提示词、日期分流都会跟着用这个日期，
		// 但不会写进配置文件，所以下次启动仍然默认今天。
		ApplyTargetDateOverride(&cfg, opts.TargetDate)
		return runPreviewOnly(cfg, opts.Incremental, progress)
	}
	runMu.Lock()
	defer runMu.Unlock()

	cfg := LoadConfig()
	state := LoadAutoState()
	if opts.Trigger == "" {
		opts.Trigger = "manual"
	}
	state.TotalRuns++
	state.LastRunAt = time.Now().Format(time.RFC3339)
	state.LastTrigger = opts.Trigger

	result := AutoOrderResult{
		ConfigPath: ConfigPath(), Workers: cfg.Concurrency,
		Trigger: opts.Trigger, AutoMode: opts.Trigger != "manual",
	}
	emit := func(stage string, done, total int, msg string) {
		if progress != nil {
			progress(stage, done, total, msg)
		}
	}
	finish := func(err error) AutoOrderResult {
		if err != nil {
			state.LastError = err.Error()
			if result.Message == "" {
				result.Message = err.Error()
			}
		} else {
			state.LastError = ""
			state.LastSuccessAt = time.Now().Format(time.RFC3339)
		}
		state.LastMessage = result.Message
		if e := SaveAutoState(state); e != nil {
			logrus.Warnf("保存自动化状态失败: %v", e)
		}
		result.NextRunAt = state.NextRunAt
		return result
	}

	emit("export", 0, 1, fmt.Sprintf("正在自动导出群「%s」近 %d 天聊天记录…", cfg.Group, cfg.Days))
	payload, err := Export(cfg)
	if err != nil {
		return finish(err)
	}
	result.Source = payload.Source
	result.ExportFrom, result.ExportTo = payload.From, payload.To
	result.Exported = payload.Count
	result.Warnings = append(result.Warnings, payload.Warnings...)
	emit("export", 1, 1, fmt.Sprintf("导出完成：%d 条消息（来源 %s）", payload.Count, payload.Source))

	// 水位线只在「本轮确实处理成功」后推进（仅兼容路径使用）。
	markProcessed := func() { advanceWatermark(&state, payload.Messages) }

	// 增量：只处理上次水位线之后的新消息
	messages := filterNewMessages(payload.Messages, state, cfg.Incremental && !opts.ForceFull)
	result.NewMessages = len(messages)
	if len(messages) == 0 {
		markProcessed()
		result.Success = true
		result.Message = fmt.Sprintf("导出 %d 条消息，增量模式下没有新消息，跳过识别与入库", payload.Count)
		emit("done", 0, 0, result.Message)
		return finish(nil)
	}
	emit("rule", 0, len(messages), fmt.Sprintf("增量筛选到 %d 条新消息，正在规则粗筛…", len(messages)))

	candidates, dropped := RuleFilterWithConfig(messages, cfg)
	result.Candidates, result.Dropped = len(candidates), dropped
	emit("rule", len(candidates), len(messages),
		fmt.Sprintf("规则粗筛：%d 条疑似订单，过滤掉 %d 条闲聊", len(candidates), dropped))
	if len(candidates) == 0 {
		markProcessed()
		result.Success = true
		result.Message = "新消息中没有疑似订单，本轮无需入库"
		emit("done", 0, 0, result.Message)
		return finish(nil)
	}

	items := AnalyzeAll(candidates, cfg, emit)
	target := EffectiveTargetDate(cfg)
	result.TargetDate = target.Format("2006-01-02")
	var collected []models.AddressInfo
	details := make([]AutoOrderDetail, 0, len(items))
	for _, item := range items {
		d := AutoOrderDetail{
			Sender: item.Candidate.Message.Sender, MessageTime: item.Candidate.Message.Time,
			Score: item.Candidate.Score, OrderDate: item.Candidate.OrderDate,
		}
		if item.Err != nil {
			d.Error = item.Err.Error()
			result.Failed++
			details = append(details, d)
			continue
		}
		d.IsOrder, d.Reason = item.Result.IsOrder, item.Result.Reason
		d.OrderCount = len(item.Result.Orders)
		d.IsToday, d.OrderDate = IsTodayOrder(item.Result.OrderDate, item.Candidate.OrderDate,
			item.Candidate.Message.Date, target)
		if len(item.Result.Orders) > 0 {
			d.Recipient = item.Result.Orders[0].Recipient
		}
		if !item.Result.IsOrder || len(item.Result.Orders) == 0 {
			result.Skipped++
			details = append(details, d)
			continue
		}
		result.AIOrders++
		orders := item.Result.Orders
		FixSpecNames(item.Candidate.Message.Content, orders)
		for _, o := range orders {
			if cfg.OnlyToday && !d.IsToday {
				continue
			}
			collected = append(collected, o)
		}
		details = append(details, d)
	}

	// 指纹仅用于后台增量统计；识别流程不写数据库、不生成文件。
	seen := hashSet(state.ImportedHashes)
	fresh := make([]models.AddressInfo, 0, len(collected))
	freshHashes := make([]string, 0, len(collected))
	for _, o := range collected {
		h := orderHash(o)
		if seen[h] {
			continue
		}
		seen[h] = true
		fresh = append(fresh, o)
		freshHashes = append(freshHashes, h)
	}
	result.Duplicates = len(collected) - len(fresh)

	// 产品语义固定：不自动落库、不自动导出 Excel、不弹任何文件框。
	// 新订单只进入数据预览区，入库必须由用户点「确认数据无误」主动完成。
	state.ImportedHashes = append(state.ImportedHashes, freshHashes...)
	landed := true

	// AI 全部失败同样不算处理完成，保留水位线让下一轮重试。
	aiAllFailed := len(candidates) > 0 && result.Failed >= len(candidates) && result.AIOrders == 0
	if aiAllFailed {
		result.Warnings = append(result.Warnings, "本轮 AI 请求全部失败，已保留水位线待下轮自动重试")
	}
	if landed && !aiAllFailed {
		markProcessed()
	}

	// 回填预览表格：只有手动触发才覆盖全局列表，避免后台运行冲掉用户正在编辑的数据。
	if opts.Trigger == "manual" {
		global.AddressList = nil
		fresh = ordertutil.SetVid(fresh)
		global.AddressList = fresh
	} else {
		for i := range fresh {
			fresh[i].PreviewID = uint(i + 1)
		}
	}
	result.TotalOrders = len(fresh)
	state.TotalOrders += len(fresh)
	result.Orders = ordertutil.AddressToExcelAddress(fresh)
	result.Details = details
	result.Message = fmt.Sprintf("导出 %d 条 → 新增 %d 条 → 粗筛 %d 条 → AI 判定 %d 条 → 新订单 %d 笔（去重 %d，失败 %d），结果仅进入数据预览区",
		result.Exported, result.NewMessages, result.Candidates, result.AIOrders,
		result.TotalOrders, result.Duplicates, result.Failed)
	if aiAllFailed || !landed {
		result.Success = false
		emit("done", 0, 0, result.Message)
		return finish(fmt.Errorf("%s", result.Message))
	}
	result.Success = true
	emit("done", result.TotalOrders, result.TotalOrders, result.Message)
	logrus.Infof("微信订单自动化完成: %s", result.Message)
	return finish(nil)
}

// ---------------- 定时调度（默认停用） ----------------

var (
	runnerMu     sync.Mutex
	runnerCancel context.CancelFunc
)

// StartAutoRunner 启动后台自动运行；重复调用会先停掉旧的调度。
func StartAutoRunner(interval time.Duration, onProgress ProgressFunc, onResult func(AutoOrderResult)) {
	StopAutoRunner()
	if interval < time.Minute {
		interval = 30 * time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	runnerMu.Lock()
	runnerCancel = cancel
	runnerMu.Unlock()

	go func() {
		cfg := LoadConfig()
		if cfg.RunOnStartup {
			runScheduledOnce(ctx, interval, onProgress, onResult)
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runScheduledOnce(ctx, interval, onProgress, onResult)
			}
		}
	}()
	logrus.Infof("微信订单自动运行已启动，间隔 %s", interval)
}

func runScheduledOnce(ctx context.Context, interval time.Duration, onProgress ProgressFunc, onResult func(AutoOrderResult)) {
	if ctx.Err() != nil {
		return
	}
	res := RunWith(RunOptions{Trigger: "auto", Commit: true}, onProgress)
	// 失败时退避：把下次运行时间推后一个间隔，避免密钥/网络异常时高频重试刷屏
	state := LoadAutoState()
	if !res.Success {
		state.NextRunAt = time.Now().Add(interval * 2).Format(time.RFC3339)
	} else {
		state.NextRunAt = time.Now().Add(interval).Format(time.RFC3339)
	}
	_ = SaveAutoState(state)
	res.NextRunAt = state.NextRunAt
	if onResult != nil {
		onResult(res)
	}
}

func StopAutoRunner() {
	runnerMu.Lock()
	defer runnerMu.Unlock()
	if runnerCancel != nil {
		runnerCancel()
		runnerCancel = nil
	}
}
