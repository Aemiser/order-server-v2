//go:build wechat

package wechatapi

import "order_server/go/wechatlog"

// Enabled 表示本构建链入了真实微信能力（桌面版发版路径）。
const Enabled = true

// logOptions 把门面类型转成 wechatlog 类型。逐字段显式赋值，避免
// 两侧结构体将来各自演进时被无脑转换悄悄吞掉字段。
func logOptions(o Options) wechatlog.Options {
	return wechatlog.Options{
		Group:            o.Group,
		Senders:          o.Senders,
		Days:             o.Days,
		EndDate:          o.EndDate,
		StartDate:        o.StartDate,
		DataDir:          o.DataDir,
		WorkDir:          o.WorkDir,
		DataKey:          o.DataKey,
		Platform:         o.Platform,
		Version:          o.Version,
		AutoKey:          o.AutoKey,
		KeyProviderPath:  o.KeyProviderPath,
		ProviderTimeout:  o.ProviderTimeout,
		AutoKeyOnFailure: o.AutoKeyOnFailure,
		WCDBLibPath:      o.WCDBLibPath,
		SessionID:        o.SessionID,
	}
}

func apiResult(r wechatlog.Result) Result {
	out := Result{
		Source:       r.Source,
		DataDir:      r.DataDir,
		WorkDir:      r.WorkDir,
		Version:      r.Version,
		KeySource:    r.KeySource,
		KeyCachePath: r.KeyCachePath,
		From:         r.From,
		To:           r.To,
		EndDate:      r.EndDate,
		Warnings:     append([]string(nil), r.Warnings...),
		Messages:     make([]Message, 0, len(r.Messages)),
	}
	for _, m := range r.Messages {
		out.Messages = append(out.Messages, Message{
			ID:          m.ID,
			Sender:      m.Sender,
			Content:     m.Content,
			Quote:       m.Quote,
			QuoteSender: m.QuoteSender,
			Time:        m.Time,
			Date:        m.Date,
			Ts:          m.Ts,
		})
	}
	return out
}

// Export 导出本地微信聊天记录。
func Export(opt Options) (Result, error) {
	r, err := wechatlog.Export(logOptions(opt))
	return apiResult(r), err
}

// Detect 探测运行中的微信客户端数据目录与版本。
func Detect() map[string]interface{} { return wechatlog.Detect() }

// KeyCacheInfo 返回密钥缓存状态（不含密钥明文）。
func KeyCacheInfo() map[string]interface{} { return wechatlog.KeyCacheInfo() }

// ClearKeyCache 清除本地密钥缓存。
func ClearKeyCache() error { return wechatlog.ClearKeyCache() }

// ResolveAndCacheKey 只解析并缓存密钥，不解密、不导出。
func ResolveAndCacheKey(opt Options, onStatus func(string)) (KeyResolution, error) {
	r, err := wechatlog.ResolveAndCacheKey(logOptions(opt), onStatus)
	return KeyResolution{Key: r.Key, Source: r.Source, Warnings: append([]string(nil), r.Warnings...)}, err
}

// StartKeyDropWatcher 启动自动密钥目录的后台监视。
func StartKeyDropWatcher(dataDir string, onFound func(source string)) {
	wechatlog.StartKeyDropWatcher(dataDir, onFound)
}

// KeyDropStatus 返回自动密钥目录状态。
func KeyDropStatus() map[string]interface{} { return wechatlog.KeyDropStatus() }

// KeyDropDir 返回自动密钥目录路径。
func KeyDropDir() string { return wechatlog.KeyDropDir() }
