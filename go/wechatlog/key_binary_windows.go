//go:build windows

package wechatlog

import (
	"context"
	"fmt"
	"strings"

	wxkey "order_server/go/wechatlog/internal/wechat/key/windows"
)

// ScanBinaryDataKey 在微信进程内存中按二进制窗口搜索「已派生的数据库 enc_key」。
// 微信 4.1+ 不再把密钥以 x'<hex>' ASCII 形式缓存，但 32 字节二进制密钥仍在可读内存里；
// 这里不依赖任何结构体特征，直接用每个库第一页的 salt 做 HMAC 校验来确认。
// 返回与手动粘贴格式一致的「按库密钥」JSON。纯只读内存，不注入、不写内存。
func ScanBinaryDataKey(ctx context.Context, dataDir string, pid uint32, onProgress func(string)) (string, error) {
	res := wxkey.ScanBinaryDbKeys(ctx, dataDir, pid, onProgress)
	if res.Success && strings.TrimSpace(res.Key) != "" {
		return res.Key, nil
	}
	detail := res.Error
	if detail == "" {
		detail = "未匹配到可验证的数据库密钥"
	}
	return "", fmt.Errorf("%s", detail)
}

// ScanCipherDataKey 用 WCDB Config.Cipher 混淆 blob 解码取密钥（微信 4.1+/4.2 主路径）。
// 纯只读进程内存，不注入、不依赖外部程序、不需要用户配置。
func ScanCipherDataKey(ctx context.Context, dataDir string, pid uint32, onProgress func(string)) (string, error) {
	res := wxkey.ScanCipherDbKeys(ctx, dataDir, pid, onProgress)
	if res.Success && strings.TrimSpace(res.Key) != "" {
		return res.Key, nil
	}
	detail := res.Error
	if detail == "" {
		detail = "Config.Cipher 扫描未匹配到可验证的密钥"
	}
	return "", fmt.Errorf("%s", detail)
}

// DiagnoseKeyMemory 输出微信进程内存里的 salt / 密钥候选诊断信息，供运维排查。
func DiagnoseKeyMemory(ctx context.Context, dataDir string, pid uint32, onProgress func(string)) string {
	return wxkey.DiagnoseKeyMemory(ctx, dataDir, pid, onProgress)
}
