//go:build windows

package wechatlog

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// findWechatPID 通过系统快照（CreateToolhelp32Snapshot）枚举微信主进程，
// 不依赖打开目标进程，因此即便本程序与微信的完整性级别不一致也能可靠地
// 「发现」进程。返回 0 表示未发现。优先 Weixin.exe（微信 4.x 主进程）。
func findWechatPID() uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)

	// 优先匹配的主进程名顺序
	candidates := []string{"Weixin.exe", "WeChat.exe"}
	found := make(map[string]uint32)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return 0
	}
	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		for _, c := range candidates {
			if name == c {
				if _, ok := found[c]; !ok {
					found[c] = entry.ProcessID
				}
			}
		}
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}

	for _, c := range candidates {
		if pid, ok := found[c]; ok && pid != 0 {
			return pid
		}
	}
	return 0
}

// probeWeChatProcess 尝试以「读取内存 + 查询信息」权限打开微信进程，
// 用于提前发现权限/完整性级别不一致（例如微信以管理员运行而本程序没有）。
// 返回 nil 表示可以打开；否则返回打开失败的原因。
func probeWeChatProcess(pid uint32) error {
	if pid == 0 {
		return nil
	}
	h, err := windows.OpenProcess(windows.PROCESS_VM_READ|windows.PROCESS_QUERY_INFORMATION, false, pid)
	if err != nil {
		return err
	}
	windows.CloseHandle(h)
	return nil
}
