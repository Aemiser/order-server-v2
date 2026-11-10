//go:build !windows

package wechatlog

import (
	"fmt"
	"time"
)

// ReadViaWCDB 的非 Windows 占位实现：DLL 组件只在 Windows 上适用，
// 其他平台保持原有「离线解密 -> pure-Go SQLite 查询」链路。
func ReadViaWCDB(_, _, _, _, _ string, _ []string, _, _ time.Time) ([]Message, []string, error) {
	return nil, nil, fmt.Errorf("WCDB 动态库组件仅支持 Windows；请留空「WCDB 动态库路径」使用内置离线解密链路")
}
