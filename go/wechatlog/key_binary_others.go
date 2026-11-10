//go:build !windows

package wechatlog

import (
	"context"
	"fmt"
)

func ScanBinaryDataKey(_ context.Context, _ string, _ uint32, _ func(string)) (string, error) {
	return "", fmt.Errorf("仅 Windows 支持从微信进程内存提取数据库密钥")
}

func ScanCipherDataKey(_ context.Context, _ string, _ uint32, _ func(string)) (string, error) {
	return "", fmt.Errorf("仅 Windows 支持从微信进程内存提取数据库密钥")
}

func DiagnoseKeyMemory(_ context.Context, _ string, _ uint32, _ func(string)) string {
	return "仅 Windows 支持内存密钥诊断"
}
