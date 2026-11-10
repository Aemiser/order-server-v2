//go:build !windows

package wechatlog

// 非 Windows 平台不支持从微信进程内存取密钥，这里仅提供占位实现。

func findWechatPID() uint32 { return 0 }

func probeWeChatProcess(_ uint32) error { return nil }
