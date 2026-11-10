// Package wechatsource 是 ports.ChatSource 的微信实现，也是模板工作台
// 「wechat」数据源与第三方复用时的唯一适配器。
//
// 隔离边界只在 go/wechatapi 门面：本包不 import go/wechatlog，因此默认构建
// 也能编译并纳入测试；未启用微信能力时门面返回 ErrDisabled，这里会把它
// 包装成用户可见文案，绝不静默当成「没有消息」。
package wechatsource

import (
	"context"
	"errors"
	"fmt"
	"time"

	"order_server/go/ports"
	"order_server/go/wechatapi"
)

// ExportFunc 是导出能力的注入点。生产环境固定为 wechatapi.Export；
// 测试与特殊宿主可替换，从而在默认构建下也能验证参数计算与消息映射。
type ExportFunc func(wechatapi.Options) (wechatapi.Result, error)

// Source 把 wechatapi.Export 适配为 ports.ChatSource。
// 构造时传入基础 Options（平台/版本/数据目录/密钥策略等），
// 每次 Export 用入参覆盖群名与日期窗口，不修改基础配置。
type Source struct {
	opts                 wechatapi.Options
	export               ExportFunc
	allowDataDirOverride bool
}

// New 构造微信来源。base 里的 Group / StartDate / EndDate 会被每次
// Export 的入参覆盖；Platform 与 Days 为空时给出与既有链路一致的默认值。
func New(base wechatapi.Options) *Source {
	if base.Platform == "" {
		base.Platform = "windows"
	}
	if base.Days <= 0 {
		base.Days = 2
	}
	return &Source{opts: base, export: wechatapi.Export}
}

// WithDataDirOverride 允许运行参数 opt.DataDir 覆盖构造时的数据目录。
// 默认关闭：模板工作台里 DataDir 的语义是「文件数据源路径」，若用户选了
// wechat 源却填了路径，不应把文件路径错当成微信数据目录。需要按运行参数
// 动态指定目录的宿主可显式开启。
func (s *Source) WithDataDirOverride(allow bool) *Source {
	s.allowDataDirOverride = allow
	return s
}

func (s *Source) Name() string { return "wechat" }

// Export 导出 [Since, Until]（YYYY-MM-DD）窗口内的群聊消息。
//
// 以下口径沿用模板工作台既有的 wechatSource 实现，改动前请先看
// wechat_test.go 的 characterization 用例：日期窗口推导 Days、群名为空
// 时给出配置提示、导出错误统一包装、消息逐字段归一化。
func (s *Source) Export(ctx context.Context, opt ports.ExportOptions) (*ports.ExportResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	o := s.opts
	if s.allowDataDirOverride && opt.DataDir != "" {
		o.DataDir = opt.DataDir
	}
	if opt.ChatID != "" {
		o.Group = opt.ChatID
	}
	if opt.Since != "" {
		o.StartDate = opt.Since
	}
	if opt.Until != "" {
		o.EndDate = opt.Until
	}
	// Days 由显式日期窗口推导；格式非法或区间倒置时保留构造时的默认值。
	if o.StartDate != "" && o.EndDate != "" {
		from, err1 := time.ParseInLocation("2006-01-02", o.StartDate, time.Local)
		to, err2 := time.ParseInLocation("2006-01-02", o.EndDate, time.Local)
		if err1 == nil && err2 == nil && !to.Before(from) {
			o.Days = int(to.Sub(from)/(24*time.Hour)) + 1
		}
	}
	if o.Group == "" {
		return nil, errors.New("未配置要导出的群聊名称（应用设置 → 微信订单配置）")
	}

	res, err := s.export(o)
	if err != nil {
		return nil, fmt.Errorf("导出聊天记录失败: %w", err)
	}
	out := &ports.ExportResult{Messages: make([]ports.ChatMessage, 0, len(res.Messages))}
	for _, m := range res.Messages {
		out.Messages = append(out.Messages, ports.ChatMessage{
			ID:      m.ID,
			Sender:  m.Sender,
			Content: m.Content,
			Time:    m.Time,
		})
	}
	return out, nil
}
