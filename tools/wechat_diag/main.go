//go:build windows

// wechat_diag 是一个零依赖的轻量诊断工具，用来在用户机器上快速定位
// 「明明微信开着，却报未检测到微信客户端 / 无法读取内存」的根因。
//
// 它会：
//   1. 报告本程序自身是否以管理员/高完整性级别运行；
//   2. 通过系统快照枚举 Weixin.exe / WeChat.exe 进程；
//   3. 对每个微信进程尝试 OpenProcess(读取内存+查询信息)，报告成功/失败。
//
// 用法（在 order_server 仓库根目录）：
//   cd tools/wechat_diag
//   go build -o wechat_diag.exe .
//   ./wechat_diag.exe
//
// 判读：
//   - 若「打开失败」：说明本程序无法读取微信进程，几乎可以确定是权限/完整性级别
//     不一致（微信以管理员运行而本程序没有，或反之）。按提示对齐即可。
//   - 若「打开成功」但仍取不到密钥：问题在密钥扫描逻辑，与权限无关。

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func main() {
	fmt.Println("=== 微信进程检测诊断 ===")

	elevated, etype := currentElevation()
	fmt.Printf("当前进程: 已提权(管理员运行)=%v  完整性类型=%d (1=默认 2=完全 3=受限)\n", elevated, etype)

	pids := enumWechatPIDs()
	if len(pids) == 0 {
		fmt.Println("未发现 Weixin.exe / WeChat.exe 进程。请确认微信客户端已启动并登录。")
		return
	}
	fmt.Printf("发现 %d 个微信进程 PID: %v\n", len(pids), pids)

	anyOpen := false
	for _, pid := range pids {
		h, err := windows.OpenProcess(windows.PROCESS_VM_READ|windows.PROCESS_QUERY_INFORMATION, false, pid)
		if err != nil {
			fmt.Printf("  PID=%d 打开失败: %v  -> 本程序无法读取该微信进程（权限/完整性级别不一致）\n", pid, err)
			continue
		}
		fmt.Printf("  PID=%d 打开成功，可读取内存。\n", pid)
		windows.CloseHandle(h)
		anyOpen = true
	}

	fmt.Println("=== 结论 ===")
	if !anyOpen {
		fmt.Println("所有微信进程都无法打开：请将 order_server 与微信客户端调整为相同的运行权限——")
		fmt.Println("  要么都右键「以管理员身份运行」，要么都正常双击运行，然后重试。")
	} else {
		fmt.Println("至少存在一个可读取的微信进程，权限层面正常；若仍取不到密钥，请检查微信版本/数据目录。")
	}
}

func enumWechatPIDs() []uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)

	var out []uint32
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return out
	}
	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if name == "Weixin.exe" || name == "WeChat.exe" {
			out = append(out, entry.ProcessID)
		}
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}
	return out
}

func currentElevation() (elevated bool, etype uint32) {
	h, _ := windows.GetCurrentProcess()
	var token windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return false, 0
	}
	defer token.Close()

	var e uint32
	if err := windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&e)), uint32(unsafe.Sizeof(e)), &e); err == nil {
		elevated = e != 0
	}
	var t uint32
	if err := windows.GetTokenInformation(token, windows.TokenElevationType, (*byte)(unsafe.Pointer(&t)), uint32(unsafe.Sizeof(t)), &t); err == nil {
		etype = t
	}
	return elevated, etype
}
