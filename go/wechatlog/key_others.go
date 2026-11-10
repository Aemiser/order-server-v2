//go:build !windows

package wechatlog

import (
	"context"
	"fmt"
)

func scanDataKey(_ context.Context, _ string, _ uint32, _ int) (string, error) {
	return "", fmt.Errorf("仅 Windows 支持从微信进程内存提取数据库密钥")
}
