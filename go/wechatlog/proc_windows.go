//go:build windows

package wechatlog

import (
	"os/exec"
	"syscall"
)

// hideProviderWindow 与 WeFlow 的 spawn({ windowsHide: true }) 对齐：
// 调用外部密钥提供者时不弹出控制台窗口，也不夺取微信窗口焦点。
func hideProviderWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
