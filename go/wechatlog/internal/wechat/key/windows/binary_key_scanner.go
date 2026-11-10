//go:build windows

package windows

// 微信 4.1+ 之后，数据库密钥不再以 x'<hex>' 这种 ASCII 明文缓存在进程内存里；
// 4.2 更进一步，把 WCDB Config.Cipher 的 passphrase blob 用 32 字节循环掩码
// XOR 混淆，因此「直接搜 32 字节明文高熵窗口」也必然 0 命中。
// 取密钥主路径已改为 cipher_scanner.go 的 Config.Cipher 相位盲扫；
// 本文件保留作为「仍缓存明文 enc_key」的老版本 / 特殊环境兼容兜底。
// 扫描原理：
//
//   1. 用每个 .db 第一页的 salt 做「已派生 enc_key」的 HMAC 校验（verifyDbEncKey，
//      仅 2 次 PBKDF2 迭代，微秒级），因此可以对内存做滑窗暴力校验；
//   2. 先定位 salt 在内存中的出现位置，只检查其邻域；
//   3. 未命中再对私有可写内存做高熵滑窗，按 8 字节对齐逐窗口校验。
//
// 全程只读进程内存，不写内存、不注入、不启动外部程序。
//
// 输出格式与 db_key_scanner.go 一致：{"相对路径": {"enc_key": "...", "salt": "..."}}。

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
)

const (
	binaryKeyAlign      = 8                    // 堆上 32 字节密钥通常按 8/16 对齐
	binaryKeySize       = dbKeySize           // 32
	binaryChunkSize     = 2 * 1024 * 1024     // 分块读，避免大区域整块 ReadProcessMemory 失败被丢弃
	binarySaltNeighbor  = 4096                // salt 命中点前后各检查 4KB
	binaryMaxRegionSize = 64 * 1024 * 1024    // 单区域上限，控制并行内存占用
	binaryMaxWorkers    = 8
	defaultBinaryScanTO = 3 * time.Minute
)

type binaryRegion struct {
	region MemoryRegion
	order  int // 0 = 私有可写（堆，优先），1 = 其它可读
}

// ScanBinaryDbKeys 在微信进程内存里按二进制窗口搜索数据库密钥。
// pid = 0 时自动枚举所有 Weixin.exe / WeChat.exe 进程。
func ScanBinaryDbKeys(ctx context.Context, dataDir string, pid uint32, onProgress func(string)) GetDbKeyResult {
	messages := make([]string, 0, 16)
	var msgMu sync.Mutex
	report := func(format string, a ...any) {
		msg := strings.TrimSpace(fmt.Sprintf(format, a...))
		if msg == "" {
			return
		}
		msgMu.Lock()
		messages = append(messages, msg)
		msgMu.Unlock()
		if onProgress != nil {
			onProgress(msg)
		}
	}
	fail := func(format string, a ...any) GetDbKeyResult {
		return GetDbKeyResult{
			Success: false,
			Error:   fmt.Sprintf(format, a...),
			Message: strings.Join(messages, "\n"),
		}
	}

	if strings.TrimSpace(dataDir) == "" {
		return fail("微信数据目录为空")
	}
	candidates, err := collectDbKeyCandidates(filepath.Join(dataDir, "db_storage"))
	if err != nil {
		return fail("收集数据库 salt 失败: %v", err)
	}
	if len(candidates) == 0 {
		return fail("未找到可用于校验的加密数据库（%s）", filepath.Join(dataDir, "db_storage"))
	}
	sortDbKeyCandidates(candidates)
	saltCount := countDbKeySalts(candidates)
	report("二进制密钥扫描：找到 %d 个数据库 / %d 个 salt", len(candidates), saltCount)

	// 每个 salt 只保留一个代表页用于校验，减少重复计算
	seenSalt := make(map[string]bool, saltCount)
	anchors := make([]dbKeyCandidate, 0, saltCount)
	saltBytes := make([][]byte, 0, saltCount)
	for _, c := range candidates {
		if seenSalt[c.saltHex] {
			continue
		}
		seenSalt[c.saltHex] = true
		anchors = append(anchors, c)
		b, e := hex.DecodeString(c.saltHex)
		if e == nil && len(b) == dbSaltSize {
			saltBytes = append(saltBytes, b)
		}
	}

	pids := collectWechatPIDs(pid)
	if len(pids) == 0 {
		return fail("未检测到运行中的微信客户端")
	}
	report("待扫描微信进程：%v", pids)

	scanCtx, cancel := context.WithTimeout(ctx, defaultBinaryScanTO)
	defer cancel()

	keyBySalt := make(map[string]string, saltCount)
	var mu sync.Mutex
	var stop int32
	merge := func(local map[string]string) int {
		if len(local) == 0 {
			return 0
		}
		mu.Lock()
		defer mu.Unlock()
		for k, v := range local {
			if _, ok := keyBySalt[k]; !ok {
				keyBySalt[k] = v
			}
		}
		if len(keyBySalt) >= saltCount {
			atomic.StoreInt32(&stop, 1)
		}
		return len(keyBySalt)
	}

	started := time.Now()
	for _, p := range pids {
		if atomic.LoadInt32(&stop) == 1 || scanCtx.Err() != nil {
			break
		}
		report("正在扫描 PID=%d 的进程内存…", p)
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, p)
		if err != nil {
			report("PID=%d 打开失败：%v", p, err)
			continue
		}
		regions := binaryScanRegions(h)
		if len(regions) == 0 {
			_ = windows.CloseHandle(h)
			report("PID=%d 没有可读内存区域", p)
			continue
		}
		report("PID=%d 共 %d 个可读内存区域", p, len(regions))
		scanBinaryRegions(scanCtx, h, regions, saltBytes, anchors, &stop, merge, report)
		_ = windows.CloseHandle(h)
		report("PID=%d 扫描结束，已匹配 %d/%d 个 salt（耗时 %.1fs）",
			p, len(keyBySalt), saltCount, time.Since(started).Seconds())
	}

	if len(keyBySalt) == 0 {
		if scanCtx.Err() != nil {
			return fail("二进制密钥扫描超时或被取消（%v）", scanCtx.Err())
		}
		return fail("二进制内存扫描未取到数据库密钥（微信 4.2 可能已把密钥移出可读内存）")
	}
	crossVerifyDbKeys(candidates, keyBySalt)
	keyJSON, err := buildDbKeyJSON(candidates, keyBySalt)
	if err != nil {
		return fail("生成按库密钥 JSON 失败: %v", err)
	}
	report("二进制扫描成功：匹配 %d/%d 个 salt，耗时 %.1fs", len(keyBySalt), saltCount, time.Since(started).Seconds())
	return GetDbKeyResult{Success: true, Key: keyJSON, Message: strings.Join(messages, "\n")}
}

func collectWechatPIDs(preferred uint32) []uint32 {
	seen := make(map[uint32]bool)
	var out []uint32
	add := func(p uint32) {
		if p != 0 && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(preferred)
	for _, name := range []string{"Weixin.exe", "WeChat.exe"} {
		if ids, err := FindProcessIdsByName(name); err == nil {
			for _, id := range ids {
				add(id)
			}
		}
	}
	return out
}

// binaryScanRegions 收集可读区域，私有可写（堆）排前面，提高命中速度。
func binaryScanRegions(handle windows.Handle) []binaryRegion {
	all := GetMemoryRegions(handle)
	out := make([]binaryRegion, 0, len(all))
	for _, r := range all {
		if r.RegionSize == 0 || r.RegionSize > binaryMaxRegionSize {
			continue
		}
		if r.State != windows.MEM_COMMIT {
			continue
		}
		if r.Protect == windows.PAGE_NOACCESS || (r.Protect&windows.PAGE_GUARD) != 0 {
			continue
		}
		order := 1
		if r.Type == 0x20000 { // MEM_PRIVATE
			order = 0
		}
		out = append(out, binaryRegion{region: r, order: order})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].order < out[j].order })
	return out
}

func scanBinaryRegions(
	ctx context.Context,
	handle windows.Handle,
	regions []binaryRegion,
	salts [][]byte,
	anchors []dbKeyCandidate,
	stop *int32,
	merge func(map[string]string) int,
	report func(string, ...any),
) {
	workers := runtime.NumCPU()
	if workers > binaryMaxWorkers {
		workers = binaryMaxWorkers
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan MemoryRegion, workers*2)
	var wg sync.WaitGroup
	var scanned int64

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make(map[string]string)
			for r := range jobs {
				if atomic.LoadInt32(stop) == 1 || ctx.Err() != nil {
					return
				}
				data := readBinaryRegion(handle, r.BaseAddress, r.RegionSize)
				if len(data) < binaryKeySize {
					continue
				}
				binaryScanChunk(ctx, data, salts, anchors, local, stop)
				n := atomic.AddInt64(&scanned, 1)
				if len(local) > 0 {
					merge(local)
				}
				if n%200 == 0 {
					report("已扫描 %d/%d 个内存区域…", n, len(regions))
				}
			}
			if len(local) > 0 {
				merge(local)
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, br := range regions {
			select {
			case <-ctx.Done():
				return
			case jobs <- br.region:
			}
			if atomic.LoadInt32(stop) == 1 {
				return
			}
		}
	}()
	wg.Wait()
}

// readBinaryRegion 分块读取内存；单块不可读时保留零填充继续，
// 避免整块 ReadProcessMemory 失败导致整个区域被丢弃（旧扫描器的常见漏扫原因）。
func readBinaryRegion(handle windows.Handle, base uintptr, size uintptr) []byte {
	buf := make([]byte, size)
	for off := uintptr(0); off < size; {
		n := size - off
		if n > binaryChunkSize {
			n = binaryChunkSize
		}
		var read uintptr
		err := windows.ReadProcessMemory(handle, base+off, &buf[off], n, &read)
		if err != nil {
			// 该块不可读，保留零值继续，保持地址连续性
			for i := uintptr(0); i < n; i++ {
				buf[off+i] = 0
			}
		}
		off += n
	}
	return buf
}

// binaryScanChunk 先在 salt 邻域找候选，未命中再对整块做高熵滑窗。
// 邻域候选很少，可以对全部 salt 校验；全内存滑窗候选极多，
// 只对锚点库（message_0.db）校验，命中后由 crossVerifyDbKeys 扩散到其它库，
// 这样把每窗口的 HMAC 校验次数从 ~23 次降到 1 次。
func binaryScanChunk(
	ctx context.Context,
	data []byte,
	salts [][]byte,
	anchors []dbKeyCandidate,
	local map[string]string,
	stop *int32,
) {
	verify := func(win []byte, pool []dbKeyCandidate) int {
		hit := 0
		for _, c := range pool {
			if _, ok := local[c.saltHex]; ok {
				continue
			}
			if verifyDbEncKey(win, c.page1) {
				local[c.saltHex] = hex.EncodeToString(win)
				hit++
			}
		}
		return hit
	}

	// 阶段 1：salt 命中点邻域（用 map 去重，邻域数量少，开销可忽略）
	checked := make(map[int]bool)
	for _, salt := range salts {
		if ctx.Err() != nil || atomic.LoadInt32(stop) == 1 {
			return
		}
		from := 0
		for {
			idx := bytes.Index(data[from:], salt)
			if idx < 0 {
				break
			}
			pos := from + idx
			lo := pos - binarySaltNeighbor
			if lo < 0 {
				lo = 0
			}
			hi := pos + binarySaltNeighbor
			if hi > len(data)-binaryKeySize {
				hi = len(data) - binaryKeySize
			}
			for off := lo - (lo % binaryKeyAlign); off <= hi; off += binaryKeyAlign {
				if checked[off] {
					continue
				}
				checked[off] = true
				win := data[off : off+binaryKeySize]
				if looksLikeBinaryKey(win) {
					verify(win, anchors)
				}
			}
			from = pos + len(salt)
			if from >= len(data) {
				break
			}
		}
	}
	if len(local) > 0 {
		return
	}

	// 阶段 2：整块高熵滑窗（顺序扫描，不需要去重 map）
	fastPool := anchors[:1]
	for off := 0; off+binaryKeySize <= len(data); off += binaryKeyAlign {
		if off%4096 == 0 && (ctx.Err() != nil || atomic.LoadInt32(stop) == 1) {
			return
		}
		win := data[off : off+binaryKeySize]
		if !looksLikeBinaryKey(win) {
			continue
		}
		verify(win, fastPool)
	}
}

// looksLikeBinaryKey 用「高熵 + 少量零字节」过滤掉代码、字符串、指针表、零填充，
// 真正的 32 字节随机密钥几乎必然通过。
func looksLikeBinaryKey(b []byte) bool {
	if len(b) != binaryKeySize {
		return false
	}
	var seen [256]bool
	distinct, zeros := 0, 0
	for _, v := range b {
		if v == 0 {
			zeros++
		}
		if !seen[v] {
			seen[v] = true
			distinct++
		}
	}
	return distinct >= 20 && zeros <= 4
}
