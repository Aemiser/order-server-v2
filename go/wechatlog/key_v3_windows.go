//go:build windows

package wechatlog

// 微信 3.9.x（WeChat.exe，32 位 WOW64 进程）的密钥获取。
//
// 与 4.x 完全不同的三点：
//  1. 全库共用一把 32 字节「原始密钥」，不是每库独立的已派生 enc_key；
//  2. 加密参数是 SQLCipher3：PBKDF2-SHA1 64000 + HMAC-SHA1（见 decrypt/windows/v3.go）；
//  3. 密钥不在 db_storage 里，数据库位于账号目录 Msg/Misc.db、Msg/Multi/MSG0.db，
//     内存中是一个「4 字节长度 0x20 + 其后 32 字节数据」的结构，长度字段前
//     4 字节是 32 位指针（chatlog 上游 V3Extractor 的成熟做法）。
//
// 因此这里不复用 4.x 的三条扫描器（它们全部写死 db_storage 且按 SHA512 验签），
// 改为：定位 Msg 下的加密库第一页 → 只读扫描进程内存 → 用 SQLCipher3 验签。
// 全程不注入、不写内存、不拉起外部程序。

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"time"

	wxkey "order_server/go/wechatlog/internal/wechat/key/windows"

	"golang.org/x/sys/windows"
)

const (
	v3KeySize       = 32
	v3FirstPageSize = 4096
	v3ChunkSize     = 2 * 1024 * 1024
	v3ChunkOverlap  = 96
	v3MaxRegionSize = 96 * 1024 * 1024
	v3MaxChecks     = 2500 // 每次校验要做 64000 次 PBKDF2-SHA1，必须设预算
	v3ScanTimeout   = 100 * time.Second
	v3SlideMax      = 160 // 每个 2MB 块最多取多少个滑窗候选
)

var (
	v3QuotedHexPattern = regexp.MustCompile(`(?:x'|X')([0-9a-fA-F]{64,192})'`)
	v3BareHexPattern   = regexp.MustCompile(`[^0-9a-fA-F]([0-9a-fA-F]{64})[^0-9a-fA-F]`)
	v3Len32Pattern     = []byte{0x20, 0x00, 0x00, 0x00}
	v3Len64Pattern     = []byte{0x20, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
)

type v3AddrRange struct{ lo, hi uint64 }

type v3Scanner struct {
	ctx      context.Context
	handle   windows.Handle
	page1    []byte
	ranges   []v3AddrRange
	seen     map[string]bool
	deadline time.Time
	checks   int
	found    string
}

func (s *v3Scanner) exhausted() bool {
	return s.found != "" || s.checks >= v3MaxChecks ||
		time.Now().After(s.deadline) || s.ctx.Err() != nil
}

// tryKey 用锚点库第一页做 SQLCipher3 验签。dedupe=true 用于「候选量小」的指针/ASCII 路径。
func (s *v3Scanner) tryKey(candidate []byte, dedupe bool) bool {
	if s.found != "" || len(candidate) != v3KeySize {
		return false
	}
	tag := string(candidate)
	if dedupe {
		if s.seen[tag] {
			return false
		}
		s.seen[tag] = true
	}
	if s.exhausted() {
		return false
	}
	s.checks++
	if validateV3KeyPage(s.page1, candidate) {
		s.found = hex.EncodeToString(candidate)
		return true
	}
	return false
}

func (s *v3Scanner) addrReadable(addr uint64) bool {
	if addr < 0x10000 || addr >= 0x7FFFFFFFFFFF {
		return false
	}
	i := sort.Search(len(s.ranges), func(i int) bool { return s.ranges[i].hi > addr })
	return i < len(s.ranges) && s.ranges[i].lo <= addr
}

func readProcessBytes(h windows.Handle, addr uintptr, size int) ([]byte, bool) {
	if size <= 0 {
		return nil, false
	}
	buf := make([]byte, size)
	var read uintptr
	if err := windows.ReadProcessMemory(h, addr, &buf[0], uintptr(size), &read); err != nil {
		return nil, false
	}
	if read < uintptr(size) {
		return nil, false
	}
	return buf, true
}

// readProcessChunk 整块读失败时按 4KB 页读，单页失败只清零该页，
// 避免「一块里有一页不可读就整块丢弃」造成漏检。
func readProcessChunk(h windows.Handle, base uintptr, size int) []byte {
	buf := make([]byte, size)
	var read uintptr
	if err := windows.ReadProcessMemory(h, base, &buf[0], uintptr(size), &read); err == nil && read == uintptr(size) {
		return buf
	}
	const page = 4096
	for off := 0; off < size; off += page {
		n := page
		if off+n > size {
			n = size - off
		}
		var got uintptr
		if err := windows.ReadProcessMemory(h, base+uintptr(off), &buf[off], uintptr(n), &got); err != nil {
			for i := 0; i < n; i++ {
				buf[off+i] = 0
			}
			continue
		}
		for i := int(got); i < n; i++ {
			buf[off+i] = 0
		}
	}
	return buf
}

func firstPageOfDB(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	page1 := make([]byte, v3FirstPageSize)
	if _, err := io.ReadFull(f, page1); err != nil {
		return nil, err
	}
	return page1, nil
}

// scanV3DataKey 从微信 3.9 进程内存中取回全库共用的 64 位 hex 原始密钥。
func scanV3DataKey(ctx context.Context, dataDir string, pid uint32, onProgress func(string)) (string, error) {
	report := func(format string, a ...any) {
		if onProgress != nil {
			onProgress(fmt.Sprintf(format, a...))
		}
	}
	anchors := v3DBPaths(dataDir)
	if len(anchors) == 0 {
		return "", fmt.Errorf("未在 %s 下找到微信 3.9 的加密数据库（Msg/Misc.db、Msg/MicroMsg.db、Msg/Multi/MSG0.db）", dataDir)
	}
	page1, err := firstPageOfDB(anchors[0])
	if err != nil {
		return "", fmt.Errorf("读取锚点库 %s 第一页失败: %w", anchors[0], err)
	}
	report("3.9 密钥扫描：锚点库 %s", anchors[0])

	pids := []uint32{}
	if pid != 0 {
		pids = append(pids, pid)
	} else {
		for _, name := range []string{"WeChat.exe", "Weixin.exe"} {
			if found, e := wxkey.FindProcessIdsByName(name); e == nil {
				pids = append(pids, found...)
			}
		}
	}
	if len(pids) == 0 {
		return "", fmt.Errorf("未检测到运行中的微信客户端")
	}

	for _, p := range pids {
		if ctx.Err() != nil {
			break
		}
		h, e := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, p)
		if e != nil {
			report("PID=%d 打开失败：%v", p, e)
			continue
		}
		report("正在扫描 PID=%d 的进程内存…", p)
		regions := wxkey.GetMemoryRegions(h)
		s := &v3Scanner{
			ctx: ctx, handle: h, page1: page1,
			seen:     make(map[string]bool, 4096),
			deadline: time.Now().Add(v3ScanTimeout),
		}
		for _, r := range regions {
			if r.RegionSize == 0 || r.State != windows.MEM_COMMIT {
				continue
			}
			if r.Protect == windows.PAGE_NOACCESS || (r.Protect&windows.PAGE_GUARD) != 0 {
				continue
			}
			s.ranges = append(s.ranges, v3AddrRange{
				lo: uint64(r.BaseAddress), hi: uint64(r.BaseAddress) + uint64(r.RegionSize),
			})
		}
		sort.Slice(s.ranges, func(i, j int) bool { return s.ranges[i].lo < s.ranges[j].lo })

		// 阶段 1：WeChatWin.dll 模块内的可写区域（3.9 密钥结构就在这里，噪声最小）
		if mod, ok := wxkey.FindModule(p, "WeChatWin.dll"); ok {
			lo, hi := uint64(mod.ModBaseAddr), uint64(mod.ModBaseAddr)+uint64(mod.ModBaseSize)
			var inModule []wxkey.MemoryRegion
			for _, r := range regions {
				if uint64(r.BaseAddress) < hi && uint64(r.BaseAddress)+uint64(r.RegionSize) > lo &&
					v3RegionWritable(r.Protect) && r.RegionSize <= v3MaxRegionSize {
					inModule = append(inModule, r)
				}
			}
			report("WeChatWin.dll 模块内可写区域 %d 个，先做指针定位", len(inModule))
			s.scanRegions(inModule, false)
		} else {
			report("未定位到 WeChatWin.dll 模块，直接全内存扫描")
		}

		// 阶段 2：全内存（指针 + ASCII + 受控滑窗兜底）
		if s.found == "" {
			var all []wxkey.MemoryRegion
			for _, r := range regions {
				if r.RegionSize > 0 && r.RegionSize <= v3MaxRegionSize &&
					r.State == windows.MEM_COMMIT && r.Protect != windows.PAGE_NOACCESS &&
					(r.Protect&windows.PAGE_GUARD) == 0 {
					all = append(all, r)
				}
			}
			s.scanRegions(all, true)
		}
		_ = windows.CloseHandle(h)

		if s.found != "" {
			report("命中 3.9 数据库密钥，共校验 %d 个候选（耗时 %.1fs）", s.checks, v3ScanTimeout.Seconds()-time.Until(s.deadline).Seconds())
			return s.found, nil
		}
		report("PID=%d 未命中，共校验 %d 个候选", p, s.checks)
	}
	return "", fmt.Errorf("未从微信 3.9 进程内存取到可用密钥（锚点库 %s）", anchors[0])
}

func v3RegionWritable(protect uint32) bool {
	return (protect & (windows.PAGE_READWRITE | windows.PAGE_WRITECOPY |
		windows.PAGE_EXECUTE_READWRITE | windows.PAGE_EXECUTE_WRITECOPY)) != 0
}

func (s *v3Scanner) scanRegions(regions []wxkey.MemoryRegion, withSlide bool) {
	step := uintptr(v3ChunkSize - v3ChunkOverlap)
	for _, r := range regions {
		if s.exhausted() {
			return
		}
		for off := uintptr(0); off < r.RegionSize; off += step {
			if s.exhausted() {
				return
			}
			n := uintptr(v3ChunkSize)
			if off+n > r.RegionSize {
				n = r.RegionSize - off
			}
			if n < 64 {
				break
			}
			base := r.BaseAddress + off
			data := readProcessChunk(s.handle, base, int(n))
			s.scanChunk(data, withSlide)
		}
	}
}

func (s *v3Scanner) scanChunk(data []byte, withSlide bool) {
	// 1) x'<hex>' / 独立 64 位 hex：3.9 有些版本仍把 PRAGMA key 留在内存里
	for _, re := range []*regexp.Regexp{v3QuotedHexPattern, v3BareHexPattern} {
		for _, m := range re.FindAllSubmatchIndex(data, 64) {
			if s.exhausted() {
				return
			}
			if len(m) < 4 {
				continue
			}
			run := string(data[m[2]:m[3]])
			starts := []int{0}
			if len(run) > 64 {
				for i := 0; i+64 <= len(run); i += 32 {
					starts = append(starts, i)
				}
				starts = append(starts, len(run)-64)
			}
			for _, st := range starts {
				if st < 0 || st+64 > len(run) {
					continue
				}
				if kb, e := hex.DecodeString(run[st : st+64]); e == nil {
					if s.tryKey(kb, true) {
						return
					}
				}
			}
		}
	}

	// 2) 32 位指针模式：4 字节长度 0x20 前的 4 字节是密钥地址（WeChat 3.9 主路径）
	from := 4
	for {
		if s.exhausted() {
			return
		}
		idx := bytes.Index(data[from:], v3Len32Pattern)
		if idx < 0 {
			break
		}
		pos := from + idx
		ptr := uint64(binary.LittleEndian.Uint32(data[pos-4 : pos]))
		if s.addrReadable(ptr) {
			if kb, ok := readProcessBytes(s.handle, uintptr(ptr), v3KeySize); ok {
				if s.tryKey(kb, true) {
					return
				}
			}
		}
		from = pos + 1
		if from >= len(data) {
			break
		}
	}

	// 3) 64 位指针模式（部分 3.9 分支 / 64 位辅助进程）
	from = 8
	for {
		if s.exhausted() {
			return
		}
		idx := bytes.Index(data[from:], v3Len64Pattern)
		if idx < 0 {
			break
		}
		pos := from + idx
		ptr := binary.LittleEndian.Uint64(data[pos-8 : pos])
		if s.addrReadable(ptr) {
			if kb, ok := readProcessBytes(s.handle, uintptr(ptr), v3KeySize); ok {
				if s.tryKey(kb, true) {
					return
				}
			}
		}
		from = pos + 1
		if from >= len(data) {
			break
		}
	}

	// 4) 受控高熵滑窗兜底：只在全内存阶段启用，且每块限量
	if !withSlide {
		return
	}
	tried := 0
	for off := 0; off+v3KeySize <= len(data) && tried < v3SlideMax; off += 8 {
		if s.exhausted() {
			return
		}
		win := data[off : off+v3KeySize]
		if !v3LooksLikeKey(win) {
			continue
		}
		tried++
		cp := make([]byte, v3KeySize)
		copy(cp, win)
		if s.tryKey(cp, false) {
			return
		}
	}
}

// v3LooksLikeKey 做便宜的筛选，避免把大量零页 / 重复字节送进 PBKDF2。
func v3LooksLikeKey(b []byte) bool {
	if len(b) != v3KeySize {
		return false
	}
	nonZero := 0
	var distinct [256]bool
	kinds := 0
	for _, c := range b {
		if c != 0 {
			nonZero++
		}
		if !distinct[c] {
			distinct[c] = true
			kinds++
		}
	}
	return nonZero >= 28 && kinds >= 14
}
