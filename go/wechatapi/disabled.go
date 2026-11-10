//go:build !wechat

package wechatapi

// Enabled 表示本构建是「默认开源构建」：不链入 go/wechatlog。
const Enabled = false

// Export 在降级构建下统一返回 ErrDisabled，绝不静默返回空结果——
// 避免一键识别把「没导出」误当成「没有订单」。
func Export(_ Options) (Result, error) { return Result{}, ErrDisabled }

// Detect 保持与真实实现相同的键结构，让配置页能直接展示 message。
func Detect() map[string]interface{} {
	return map[string]interface{}{"success": false, "message": DisabledHint}
}

// KeyCacheInfo 保持 exists=false，启动检查据此不会误判为「已有缓存」。
func KeyCacheInfo() map[string]interface{} {
	return map[string]interface{}{"exists": false, "path": "", "message": DisabledHint}
}

// ClearKeyCache 返回 ErrDisabled，由调用方包装成用户可见文案。
func ClearKeyCache() error { return ErrDisabled }

// ResolveAndCacheKey 返回 ErrDisabled；onStatus 不触发，避免 UI 出现半截进度。
func ResolveAndCacheKey(_ Options, _ func(string)) (KeyResolution, error) {
	return KeyResolution{}, ErrDisabled
}

// StartKeyDropWatcher 降级构建下是空操作：不建目录、不起 goroutine、不碰本机文件。
func StartKeyDropWatcher(_ string, _ func(source string)) {}

// KeyDropStatus 返回可展示的降级状态。
func KeyDropStatus() map[string]interface{} {
	return map[string]interface{}{
		"dir":      "",
		"files":    []string{},
		"writable": false,
		"enabled":  false,
		"message":  DisabledHint,
	}
}

// KeyDropDir 返回空串；调用方需先判断 Enabled，避免 os.MkdirAll("") 报错。
func KeyDropDir() string { return "" }
