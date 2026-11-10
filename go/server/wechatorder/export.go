package wechatorder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"order_server/go/wechatapi"

	"github.com/sirupsen/logrus"
)

// ChatMessage 是导出工具返回的单条群聊消息。
// Quote / QuoteSender 是引用回复里被引用消息的原文与发言人；
// 订单操作层用它做「引用 → 原订单」的精确定位。
type ChatMessage struct {
	ID          string  `json:"id"`
	Sender      string  `json:"sender"`
	Content     string  `json:"content"`
	Quote       string  `json:"quote,omitempty"`
	QuoteSender string  `json:"quoteSender,omitempty"`
	Time        string  `json:"time"`
	Date        string  `json:"date"`
	Ts          float64 `json:"ts"`
}

// ExportPayload 是聊天记录导出的结果。
type ExportPayload struct {
	Group      string        `json:"group"`
	Source     string        `json:"source"`
	Days       int           `json:"days"`
	From       string        `json:"from"`
	To         string        `json:"to"`
	TargetDate string        `json:"targetDate"` // 本轮要识别的订单日期 YYYY-MM-DD
	ExportedAt string        `json:"exported_at"`
	Senders    string        `json:"senders"`
	Count      int           `json:"count"`
	Warnings   []string      `json:"warnings"`
	Messages   []ChatMessage `json:"messages"`
	KeySource  string        `json:"keySource"` // 密钥来源：手动 / 缓存 / 外部提供者 / 内存扫描
	KeyCache   string        `json:"keyCache"`  // 密钥缓存文件路径
}

// OptionsFromConfig 把用户配置转成导出选项的公共出口，供新架构
// （go/schemaservice 的 ChatSource 适配器）复用同一份配置，避免重复实现。
// 注意：EndDate/Days 由调用方按自己的窗口语义覆盖。
func OptionsFromConfig(cfg Config) wechatapi.Options { return wechatOrderOptions(cfg) }

// wechatOrderOptions 把微信订单配置转成导出选项，单日识别与范围审核共用。
func wechatOrderOptions(cfg Config) wechatapi.Options {
	return wechatapi.Options{
		Group:            cfg.Group,
		Senders:          cfg.Senders,
		Days:             cfg.Days,
		EndDate:          TargetDateString(cfg), // 窗口以目标订单日期为终点往前 Days 天
		DataDir:          cfg.DataDir,
		WorkDir:          cfg.WorkDir,
		DataKey:          cfg.DataKey,
		Platform:         cfg.Platform,
		Version:          cfg.Version,
		AutoKey:          cfg.AutoKey,
		KeyProviderPath:  cfg.KeyProviderPath,
		ProviderTimeout:  time.Duration(cfg.ProviderTimeout) * time.Second,
		AutoKeyOnFailure: cfg.AutoKeyOnFailure,
		WCDBLibPath:      cfg.WCDBLibPath,
		SessionID:        cfg.SessionID,
	}
}

// Export 直接调用项目内移植的 chatlog 核心 Go 代码读取本地微信数据库，
// 返回指定群聊近 N 天的消息。全程无 HTTP 服务、无外部进程、无 UI 自动化。
func Export(cfg Config) (ExportPayload, error) {
	return exportMessages(cfg, wechatOrderOptions(cfg), "")
}

// ExportRange 按显式起止日期导出群聊，专供「账单结算 → 审核」使用。
// 与一键识别共用同一套密钥 / 解密 / 消息转换 / 留档逻辑，只是把导出窗口
// 从「目标日期往前 Days 天」换成用户选择的日期范围。
func ExportRange(cfg Config, from, to string) (ExportPayload, error) {
	startDay, endDay, err := ParseDateRange(from, to)
	if err != nil {
		return ExportPayload{}, err
	}
	opt := wechatOrderOptions(cfg)
	opt.StartDate = startDay.Format("2006-01-02")
	opt.EndDate = endDay.Format("2006-01-02")
	opt.Days = int(endDay.Sub(startDay)/(24*time.Hour)) + 1
	return exportMessages(cfg, opt, opt.EndDate)
}

// ParseDateRange 校验前端传来的 YYYY-MM-DD 起止日期（本地时区的日历日）。
// 结束日期是未来时夹到今天（导出层本来也不允许导出未来）；范围过长直接拒绝，
// 避免一次审核把几十天的聊天记录全部丢给 AI。
func ParseDateRange(from, to string) (time.Time, time.Time, error) {
	parse := func(s string) (time.Time, error) {
		for _, layout := range []string{"2006-01-02", "2006-1-2"} {
			if t, err := time.ParseInLocation(layout, strings.TrimSpace(s), time.Local); err == nil {
				return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local), nil
			}
		}
		return time.Time{}, fmt.Errorf("日期 %q 格式无效，应为 YYYY-MM-DD", s)
	}
	start, err := parse(from)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := parse(to)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if start.After(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("起始日期 %s 不能晚于结束日期 %s",
			start.Format("2006-01-02"), end.Format("2006-01-02"))
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	if end.After(today) {
		end = today
	}
	if start.After(end) {
		start = end
	}
	if days := int(end.Sub(start)/(24*time.Hour)) + 1; days > 92 {
		return time.Time{}, time.Time{},
			fmt.Errorf("审核日期范围最多 92 天（当前 %d 天），请缩小范围后重试", days)
	}
	return start, end, nil
}

func exportMessages(cfg Config, opt wechatapi.Options, targetDate string) (ExportPayload, error) {
	var payload ExportPayload

	res, err := wechatapi.Export(opt)
	if err != nil {
		return payload, fmt.Errorf("读取微信聊天记录失败: %w", err)
	}

	now := time.Now()
	target := EffectiveTargetDate(cfg)
	if s := strings.TrimSpace(targetDate); s != "" {
		if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
			target = t
		}
	}
	payload.Group = cfg.Group
	payload.Source = res.Source
	payload.Days = opt.Days
	payload.TargetDate = target.Format("2006-01-02")
	payload.From = res.From
	payload.To = res.To
	if payload.From == "" {
		payload.From = target.AddDate(0, 0, -(opt.Days - 1)).Format("2006-01-02")
	}
	if payload.To == "" {
		payload.To = payload.TargetDate
	}
	payload.ExportedAt = now.Format(time.RFC3339)
	payload.Senders = cfg.Senders
	payload.Warnings = res.Warnings
	payload.KeySource = res.KeySource
	payload.KeyCache = res.KeyCachePath
	payload.Messages = make([]ChatMessage, 0, len(res.Messages))
	for _, m := range res.Messages {
		payload.Messages = append(payload.Messages, ChatMessage{
			ID: m.ID, Sender: m.Sender, Content: m.Content,
			Quote: m.Quote, QuoteSender: m.QuoteSender,
			Time: m.Time, Date: m.Date, Ts: m.Ts,
		})
	}
	payload.Count = len(payload.Messages)

	// 留档，便于排查导出内容与规则过滤效果
	data, _ := json.MarshalIndent(payload, "", "  ")
	archive := filepath.Join("uploads", fmt.Sprintf("chatlog_export_%s.json", now.Format("20060102_150405")))
	if err := os.MkdirAll(filepath.Dir(archive), 0755); err == nil {
		if werr := os.WriteFile(archive, data, 0644); werr != nil {
			logrus.Warnf("导出结果留档失败: %v", werr)
		}
	}
	logrus.Infof("聊天记录导出完成: 群=%s 来源=%s 目标订单日期=%s 窗口=%s~%s 消息=%d 条",
		payload.Group, payload.Source, payload.TargetDate, payload.From, payload.To, payload.Count)
	return payload, nil
}
