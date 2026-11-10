//go:build windows

package wechatlog

// 参考 hicccc77/WeFlow docs/third-party-components.md 的 WCDB 组件协议：
// 通过 C ABI 直接打开本地 SQLCipher 数据库，用时间游标分批读消息，
// 不做全量解密、不启动 HTTP 服务、不触碰微信窗口或进程。
// 组件由用户自备并显式配置路径；未配置时完全不加载任何 DLL。

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const wcdbBatchSize = 500
const wcdbMaxBatches = 200

type wcdbDLL struct {
	dll    *syscall.DLL
	procs  map[string]*syscall.Proc
	handle int64
}

func (d *wcdbDLL) proc(name string) (*syscall.Proc, error) {
	p := d.procs[name]
	if p == nil {
		return nil, fmt.Errorf("WCDB 组件未提供 %s", name)
	}
	return p, nil
}

func (d *wcdbDLL) call(name string, args ...uintptr) (int32, error) {
	p, err := d.proc(name)
	if err != nil {
		return 0, err
	}
	r, _, _ := p.Call(args...)
	return int32(r), nil
}

func readWcdbString(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	base := unsafe.Pointer(ptr)
	var buf []byte
	for i := 0; i < 64<<20; i++ {
		b := *(*byte)(unsafe.Add(base, i))
		if b == 0 {
			break
		}
		buf = append(buf, b)
	}
	return string(buf)
}

// callJSON 调用形如 int32 xxx(..., _Out_ void** outJson) 的导出，
// 按协议用 wcdb_free_string 释放组件分配的字符串。
func (d *wcdbDLL) callJSON(name string, args ...uintptr) (string, error) {
	var out uintptr
	all := append(append([]uintptr{}, args...), uintptr(unsafe.Pointer(&out)))
	st, err := d.call(name, all...)
	raw := readWcdbString(out)
	if out != 0 {
		if free, e := d.proc("wcdb_free_string"); e == nil {
			_, _, _ = free.Call(out)
		}
	}
	if err != nil {
		return "", err
	}
	if st != 0 {
		return "", fmt.Errorf("WCDB %s 返回状态码 %d", name, st)
	}
	return raw, nil
}

func loadWcdb(libPath string) (*wcdbDLL, error) {
	dll, err := syscall.LoadDLL(libPath)
	if err != nil {
		return nil, fmt.Errorf("加载 WCDB 动态库失败 %s: %w", libPath, err)
	}
	d := &wcdbDLL{dll: dll, procs: map[string]*syscall.Proc{}}
	required := []string{"wcdb_init", "wcdb_shutdown", "wcdb_open_account", "wcdb_close_account", "wcdb_free_string"}
	optional := []string{
		"wcdb_get_sessions", "wcdb_get_display_names",
		"wcdb_open_message_cursor", "wcdb_fetch_message_batch", "wcdb_close_message_cursor",
	}
	for _, name := range required {
		p, e := dll.FindProc(name)
		if e != nil {
			_ = dll.Release()
			return nil, fmt.Errorf("WCDB 组件缺少必需导出 %s", name)
		}
		d.procs[name] = p
	}
	for _, name := range optional {
		if p, e := dll.FindProc(name); e == nil {
			d.procs[name] = p
		}
	}
	return d, nil
}

// ReadViaWCDB 走 WCDB 组件按时间游标读取指定会话的消息。
func ReadViaWCDB(libPath, dataDir, group, sessionID, key string,
	senders []string, start, end time.Time) ([]Message, []string, error) {

	var warnings []string
	libPath = strings.TrimSpace(libPath)
	if libPath == "" {
		return nil, warnings, fmt.Errorf("未配置 WCDB 动态库路径")
	}
	if _, err := os.Stat(libPath); err != nil {
		return nil, warnings, fmt.Errorf("WCDB 动态库不存在：%s", libPath)
	}
	if strings.TrimSpace(key) == "" {
		return nil, warnings, fmt.Errorf("WCDB 直读需要数据库密钥（hex）")
	}

	d, err := loadWcdb(libPath)
	if err != nil {
		return nil, warnings, err
	}
	defer d.dll.Release()

	if st, err := d.call("wcdb_init"); err != nil || st != 0 {
		return nil, warnings, fmt.Errorf("WCDB 初始化失败（状态码 %d）：%v", st, err)
	}
	defer func() { _, _ = d.call("wcdb_shutdown") }()

	dbPath := sessionDBPath(dataDir)
	pathPtr, _ := syscall.BytePtrFromString(dbPath)
	keyPtr, _ := syscall.BytePtrFromString(singleHexKey(key))
	st, err := d.call("wcdb_open_account",
		uintptr(unsafe.Pointer(pathPtr)), uintptr(unsafe.Pointer(keyPtr)),
		uintptr(unsafe.Pointer(&d.handle)))
	runtime.KeepAlive(pathPtr)
	runtime.KeepAlive(keyPtr)
	if err != nil || st != 0 || d.handle <= 0 {
		return nil, warnings, fmt.Errorf("WCDB 打开账号失败（状态码 %d）：%v；请确认 session.db 路径与密钥是否正确（%s）", st, err, dbPath)
	}
	defer func() { _, _ = d.call("wcdb_close_account", uintptr(d.handle)) }()

	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		raw, e := d.callJSON("wcdb_get_sessions", uintptr(d.handle))
		if e != nil {
			return nil, warnings, fmt.Errorf("读取会话列表失败：%w；可在配置页直接填写「群会话 ID」跳过此步", e)
		}
		sessions, _ := parseWcdbJSON(raw)
		sid = pickSessionID(sessions, group, "")
		if sid == "" {
			return nil, warnings, fmt.Errorf("未在会话列表中找到群「%s」，请在配置页填写该群的会话 ID（通常形如 12345678@chatroom）", group)
		}
		warnings = append(warnings, "已按群名匹配会话："+sid)
	}

	sidPtr, _ := syscall.BytePtrFromString(sid)
	var cursor int64
	st, err = d.call("wcdb_open_message_cursor",
		uintptr(d.handle), uintptr(unsafe.Pointer(sidPtr)),
		uintptr(wcdbBatchSize), uintptr(1),
		uintptr(uint32(int32(start.Unix()))), uintptr(uint32(int32(end.Unix()))),
		uintptr(unsafe.Pointer(&cursor)))
	runtime.KeepAlive(sidPtr)
	if err != nil || st != 0 || cursor <= 0 {
		return nil, warnings, fmt.Errorf("WCDB 打开消息游标失败（状态码 %d）：%v", st, err)
	}
	defer func() { _, _ = d.call("wcdb_close_message_cursor", uintptr(d.handle), uintptr(cursor)) }()

	var rows []map[string]interface{}
	for batch := 0; batch < wcdbMaxBatches; batch++ {
		var out uintptr
		var hasMore int32
		st, err := d.call("wcdb_fetch_message_batch",
			uintptr(d.handle), uintptr(cursor),
			uintptr(unsafe.Pointer(&out)), uintptr(unsafe.Pointer(&hasMore)))
		raw := readWcdbString(out)
		if out != 0 {
			if free, e := d.proc("wcdb_free_string"); e == nil {
				_, _, _ = free.Call(out)
			}
		}
		if err != nil || st != 0 {
			return nil, warnings, fmt.Errorf("WCDB 读取消息批次失败（状态码 %d）：%v", st, err)
		}
		part, e := parseWcdbJSON(raw)
		if e != nil {
			return nil, warnings, e
		}
		rows = append(rows, part...)
		if hasMore == 0 || len(part) == 0 {
			break
		}
		if batch == wcdbMaxBatches-1 {
			warnings = append(warnings, "消息批次达到上限，可能未读完整个时间窗口")
		}
	}

	// 尽量把 wxid 映射成昵称，让「顾客昵称」过滤继续生效
	names := map[string]string{}
	if _, ok := d.procs["wcdb_get_display_names"]; ok {
		ids := map[string]bool{}
		for _, row := range rows {
			s, wxid, _ := splitRowSender(row)
			if looksLikeWXID(s) {
				ids[s] = true
			}
			if wxid != "" {
				ids[wxid] = true
			}
		}
		if len(ids) > 0 {
			list := make([]string, 0, len(ids))
			for id := range ids {
				list = append(list, id)
			}
			if b, e := json.Marshal(list); e == nil {
				ptr, _ := syscall.BytePtrFromString(string(b))
				if raw, e := d.callJSON("wcdb_get_display_names", uintptr(d.handle), uintptr(unsafe.Pointer(ptr))); e == nil {
					names = parseDisplayNames(raw)
				} else {
					warnings = append(warnings, "显示名映射不可用："+e.Error())
				}
				runtime.KeepAlive(ptr)
			}
		}
	}

	msgs, parseWarnings := wcdbMessagesFromRows(rows, names, senders, start, end)
	warnings = append(warnings, parseWarnings...)
	warnings = append(warnings, fmt.Sprintf("WCDB 游标读取 %d 行原始消息，转换为 %d 条文本消息", len(rows), len(msgs)))
	return msgs, warnings, nil
}
