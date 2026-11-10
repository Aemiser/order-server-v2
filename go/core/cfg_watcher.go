package core

// 数据库配置热更新：后台协程按固定间隔检查 cfg_* 五张表是否改过配置
// （提示词 / AI 模型 / 商品归属 / 通用设置），有差异就整体替换内存快照并推事件。
//
// 常态走逐表 MD5 快比对：MySQL 每轮按内容实算五张表各自的 MD5，
// 与本地逐表基线一致就直接跳过；哪张表不一致就只拉那张表，
// 其余表复用上次已加载的行（详见 cfg_revision.go）。
// 拉回来的行仍用 Go 侧内容指纹二次确认，避免误报。
// 不依赖 updated_at：配置可能由外部 SQL / 管理后台直接修改，未必维护时间戳。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// DefaultCfgWatchInterval 配置轮询间隔。
// 60 秒的取舍：改完提示词基本能在一分钟内生效，又不会给远程 MySQL 造成压力；
// 界面另有「立即同步」按钮，需要马上生效时不必等定时器。
const DefaultCfgWatchInterval = 60 * time.Second

// CfgRefreshResult 是「立即同步」按钮的返回：本轮是否真的有变化 + 最新状态。
type CfgRefreshResult struct {
	Status  CfgWatchStatus `json:"status"`
	Changed bool           `json:"changed"`
	Summary string         `json:"summary"`
	Error   string         `json:"error"`
}

// CfgChange 是一轮检查的结果，同时作为 Wails 事件 cfg-reloaded 的载荷。
type CfgChange struct {
	Changed  bool     `json:"changed"`
	Sections []string `json:"sections"`
	Summary  string   `json:"summary"` // 人话版变更摘要，例如「提示词「微信订单判定基础提示词」」
	At       string   `json:"at"`
	Revision string   `json:"revision"` // 当前配置版本短指纹
	Trigger  string   `json:"trigger"`  // auto / manual
}

// CfgWatchStatus 供应用设置页展示同步状态。
type CfgWatchStatus struct {
	Enabled         bool   `json:"enabled"`
	IntervalSeconds int    `json:"intervalSeconds"`
	Checks          int    `json:"checks"`
	Changes         int    `json:"changes"`
	LastCheckAt     string `json:"lastCheckAt"`
	LastChangedAt   string `json:"lastChangedAt"`
	LastChange      string `json:"lastChange"`
	NextCheckAt     string `json:"nextCheckAt"`
	LastError       string `json:"lastError"`
	Revision        string `json:"revision"`
	CfgMD5          string `json:"cfgMd5"`       // cfg_revision 里存的整体配置 MD5
	FastSkips       int    `json:"fastSkips"`    // 逐表 MD5 全一致、直接跳过的轮次
	FullLoads       int    `json:"fullLoads"`    // 真正读了配置表的轮次
	PartialLoads    int    `json:"partialLoads"` // 只拉了部分表的轮次
	LastSections    string `json:"lastSections"` // 本轮实际读了哪几张表（逗号分隔）
	LastMode        string `json:"lastMode"`     // 本轮走的是快比对还是配置表比对
}

var (
	watchMu sync.Mutex

	watchStarted      bool
	watchInterval     = DefaultCfgWatchInterval
	watchChecks       int
	watchChanges      int
	watchLastCheck    time.Time
	watchLastChangeAt time.Time
	watchLastChange   string
	watchLastError    string
	watchRevision     string
	watchOnChange     func(CfgChange)

	// 本地基线：cfg_revision 的整体 MD5（展示用）+ 五张表各自的 MD5
	// （判定用，逐表比对才能做到「只拉变化的那张表」）。
	watchCfgMD5      string
	watchSectionMD5  map[string]string
	watchFastSkips   int
	watchFullLoads   int
	watchPartialLoad int
	watchLastSection string
	watchLastMode    string

	// 启动时 / 每次成功热更新后记录的完整配置行与内容指纹。
	// 增量拉取就是用这里的旧行打底，只覆盖本轮真正读过的表。
	cfgBaseline map[string]string
	cfgLastRows cfgRows
)

func nowStamp() string { return time.Now().Format("2006-01-02 15:04:05") }

func revisionOf(digests map[string]string) string {
	var sb strings.Builder
	for _, k := range cfgSections {
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(digests[k])
		sb.WriteByte(';')
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:6])
}

// setCfgBaseline 同时记录完整配置行、Go 内容指纹与逐表 MD5 基线。
// p.Actual 为空表示本轮没拿到实算值（轻量 SQL 降级）：清空逐表基线，
// 下一轮退回全量比对，行为安全。
func setCfgBaseline(rows cfgRows, p cfgRevisionPair) {
	watchMu.Lock()
	defer watchMu.Unlock()
	cfgLastRows = rows
	cfgBaseline = rows.digest()
	watchRevision = revisionOf(cfgBaseline)
	if p.Actual != "" {
		watchCfgMD5 = p.Actual
		watchSectionMD5 = p.sections()
	} else {
		watchSectionMD5 = nil
	}
}

func snapshotCfgBaseline() (cfgRows, map[string]string, string) {
	watchMu.Lock()
	defer watchMu.Unlock()
	d := make(map[string]string, len(cfgBaseline))
	for k, v := range cfgBaseline {
		d[k] = v
	}
	return cfgLastRows, d, watchCfgMD5
}

// snapshotSectionMD5 返回本地逐表 MD5 基线的副本（调用方可安全遍历）。
func snapshotSectionMD5() map[string]string {
	watchMu.Lock()
	defer watchMu.Unlock()
	out := make(map[string]string, len(watchSectionMD5))
	for k, v := range watchSectionMD5 {
		out[k] = v
	}
	return out
}

func currentRevision() string {
	watchMu.Lock()
	defer watchMu.Unlock()
	return watchRevision
}

// noteCfgCheck 记录本轮是「MD5 快比对跳过」还是「读了配置表」，供设置页展示效果。
func noteCfgCheck(mode string, full bool) {
	watchMu.Lock()
	defer watchMu.Unlock()
	watchLastMode = mode
	if full {
		watchFullLoads++
	} else {
		watchFastSkips++
	}
}

// noteCfgSections 记录本轮实际读了哪几张表，供设置页展示增量效果。
func noteCfgSections(sections []string) {
	watchMu.Lock()
	defer watchMu.Unlock()
	if len(sections) == 0 {
		watchLastSection = "无（逐表 MD5 一致）"
		return
	}
	watchLastSection = strings.Join(sections, ", ")
	if len(sections) < len(cfgSections) {
		watchPartialLoad++
	}
}

// StartCfgWatcher 启动后台定时检查（幂等，重复调用不会起多个协程）。
// onChange 在检测到数据库配置变化时被调用，通常用来推 Wails 事件。
func StartCfgWatcher(ctx context.Context, interval time.Duration, onChange func(CfgChange)) {
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = DefaultCfgWatchInterval
	}
	watchMu.Lock()
	if watchStarted {
		watchMu.Unlock()
		return
	}
	watchStarted = true
	watchInterval = interval
	watchOnChange = onChange
	watchMu.Unlock()

	logrus.Infof("数据库配置热更新已启动：每 %s 检查 %d 个配置节（%s）",
		interval, len(cfgSections), strings.Join(cfgSections, " / "))
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				logrus.Info("数据库配置热更新定时器已退出")
				return
			case <-ticker.C:
				_, _ = checkCfgOnce(false, "auto")
			}
		}
	}()
}

// RefreshDBCfgNow 手动触发一次检查（配置页「立即同步」），不等 60 秒定时器。
// force=true：即使逐表 MD5 有差异但内容指纹没变，也刷新一次内存快照与基线；
// 逐表 MD5 全一致时仍直接跳过，不做无谓的配置表查询。
func RefreshDBCfgNow(trigger string) (CfgChange, error) {
	if trigger == "" {
		trigger = "manual"
	}
	return checkCfgOnce(true, trigger)
}

func checkCfgOnce(force bool, trigger string) (CfgChange, error) {
	change, err := ReloadDBCfg(force, trigger)
	watchMu.Lock()
	watchChecks++
	watchLastCheck = time.Now()
	if err != nil {
		watchLastError = err.Error()
	} else {
		watchLastError = ""
	}
	if change.Changed {
		watchChanges++
		watchLastChangeAt = time.Now()
		watchLastChange = change.Summary
	}
	onChange := watchOnChange
	watchMu.Unlock()

	if err != nil {
		logrus.Warnf("配置热更新检查失败: %v", err)
		return change, err
	}
	if change.Changed && onChange != nil {
		onChange(change)
	}
	return change, nil
}

// GetCfgWatchStatus 返回当前同步状态（不含任何密钥 / 提示词正文）。
func GetCfgWatchStatus() CfgWatchStatus {
	watchMu.Lock()
	defer watchMu.Unlock()
	st := CfgWatchStatus{
		Enabled:         watchStarted,
		IntervalSeconds: int(watchInterval.Seconds()),
		Checks:          watchChecks,
		Changes:         watchChanges,
		LastChange:      watchLastChange,
		LastError:       watchLastError,
		Revision:        watchRevision,
		CfgMD5:          watchCfgMD5,
		FastSkips:       watchFastSkips,
		FullLoads:       watchFullLoads,
		PartialLoads:    watchPartialLoad,
		LastSections:    watchLastSection,
		LastMode:        watchLastMode,
	}
	if !watchLastCheck.IsZero() {
		st.LastCheckAt = watchLastCheck.Format("2006-01-02 15:04:05")
		st.NextCheckAt = watchLastCheck.Add(watchInterval).Format("2006-01-02 15:04:05")
	} else {
		st.NextCheckAt = time.Now().Add(watchInterval).Format("2006-01-02 15:04:05")
	}
	if !watchLastChangeAt.IsZero() {
		st.LastChangedAt = watchLastChangeAt.Format("2006-01-02 15:04:05")
	}
	return st
}
