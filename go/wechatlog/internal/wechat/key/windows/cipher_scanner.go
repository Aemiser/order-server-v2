//go:build windows

package windows

// 微信 4.1+ / 4.2 的密钥不是明文驻留内存，而是 WCDB 的 Config.Cipher
// passphrase 对象：blob 被 Weixin.exe 自身一段 32 字节机器码循环 XOR 混淆，
// 解码后形如 x'<64hex enc_key><32hex salt>'。
//
// 因此「直接搜 32 字节高熵明文 + HMAC 校验」在 4.1+ 上必然 0 命中
// （本项目 binary_key_scanner.go 的旧结论即由此而来）。
//
// 这里实现两条纯只读路径，都不注入、不写内存、不拉起外部程序：
//   A. 掩码相位盲扫：枚举 32 个掩码相位，直接在整个进程内存里找被混淆的
//      "x'" 两字节，就地解码并切出 enc_key/salt，再用数据库第一页 HMAC 验签。
//      不依赖任何对象偏移，能自动适配版本变化。
//   B. Config.Cipher 对象链：定位 needle 字符串 → addr/len pair → node →
//      config_ptr → data_ptr/data_len → blob → XOR 解码 → 同样的 HMAC 验签。
//
// 两条路径互补：A 抗偏移变化，B 抗掩码相位/前缀变化。

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const (
	cipherConfigName   = "com.Tencent.WCDB.Config.Cipher"
	cipherBlobMax      = 1024
	cipherChunkSize    = 4 * 1024 * 1024
	cipherChunkOverlap = 512
	cipherMaxRegion    = 500 * 1024 * 1024
	cipherMaxUserAddr  = 0x0000800000000000
	cipherScanTimeout  = 3 * time.Minute
)

// WINDOWS_CONFIG_XOR_MASK：Weixin.exe 里被用作循环 XOR 密钥的一段机器码。
var cipherXORMask = []byte{
	0xd2, 0xc7, 0x44, 0x24, 0x58, 0x02, 0x00, 0x00,
	0x00, 0x48, 0x89, 0x44, 0x24, 0x50, 0x48, 0x8b,
	0x45, 0x00, 0x48, 0x84, 0x4c, 0x24, 0x48, 0x48,
	0x89, 0x44, 0x25, 0x40, 0x48, 0x58, 0x4c, 0x24,
}

type cipherPhasePattern struct {
	b0, b1 byte
	phase  int
}

func cipherPhasePatterns() map[uint16][]int {
	out := make(map[uint16][]int, 64)
	for p := 0; p < len(cipherXORMask); p++ {
		for _, first := range []byte{'x', 'X'} {
			b0 := first ^ cipherXORMask[p]
			b1 := byte('\'') ^ cipherXORMask[(p+1)%len(cipherXORMask)]
			k := uint16(b0)<<8 | uint16(b1)
			out[k] = append(out[k], p)
		}
	}
	return out
}

// cipherCandidates 把 x'<hex>' 里的 run 切成 (enc_key, salt) 候选。
// 长 run 可能是多个 key 串接，按 32 hex 步长滑动并补最后一个起点。
func cipherCandidates(run string) [][2]string {
	starts := []int{0}
	if len(run) > 96 {
		for s := 0; s < len(run)-63; s += 32 {
			starts = append(starts, s)
		}
		starts = append(starts, len(run)-64)
	}
	seen := make(map[int]bool, len(starts))
	var out [][2]string
	for _, s := range starts {
		if seen[s] || s < 0 || s+64 > len(run) {
			continue
		}
		seen[s] = true
		key := strings.ToLower(run[s : s+64])
		salt := ""
		if s+96 <= len(run) {
			salt = strings.ToLower(run[s+64 : s+96])
		}
		out = append(out, [2]string{key, salt})
	}
	return out
}

// decodeCipherLiteral 从 pos 处按相位 p 解码，检查是否是被混淆的 x'<hex>' 字面量。
func decodeCipherLiteral(data []byte, pos, phase int) (string, bool) {
	n := pos + 220
	if n > len(data) {
		n = len(data)
	}
	if n-pos < 68 {
		return "", false
	}
	dec := make([]byte, n-pos)
	maskLen := len(cipherXORMask)
	for i := range dec {
		dec[i] = data[pos+i] ^ cipherXORMask[(phase+i)%maskLen]
	}
	if (dec[0] != 'x' && dec[0] != 'X') || dec[1] != '\'' {
		return "", false
	}
	j := 2
	for j < len(dec) {
		c := dec[j]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			break
		}
		j++
	}
	if j >= len(dec) || dec[j] != '\'' || j-2 < 64 || j-2 > 192 {
		return "", false
	}
	return string(dec[2:j]), true
}

// readCipherRange 按 4KB 页读取，单页失败只清该页，避免整块读取失败丢弃大量数据。
func readCipherRange(h windows.Handle, base uintptr, size uintptr) []byte {
	buf := make([]byte, size)
	const page = 4096
	for off := uintptr(0); off < size; off += page {
		n := uintptr(page)
		if off+n > size {
			n = size - off
		}
		var read uintptr
		err := windows.ReadProcessMemory(h, base+off, &buf[off], n, &read)
		if err != nil {
			for i := uintptr(0); i < n; i++ {
				buf[off+i] = 0
			}
			continue
		}
		for i := read; i < n; i++ {
			buf[off+i] = 0
		}
	}
	return buf
}

func readCipherAt(h windows.Handle, addr uintptr, size int) ([]byte, bool) {
	buf := make([]byte, size)
	var read uintptr
	if err := windows.ReadProcessMemory(h, addr, &buf[0], uintptr(size), &read); err != nil {
		return nil, false
	}
	return buf[:read], true
}

func u64le(b []byte, off int) uint64 {
	if off < 0 || off+8 > len(b) {
		return 0
	}
	return binary.LittleEndian.Uint64(b[off : off+8])
}

type cipherStats struct {
	regions, bytes, needle, maskCode, plainHex, phaseHits, literals, candidates, verified int
}

// ScanCipherDbKeys 用 Config.Cipher 混淆 blob 解码的方式取数据库密钥。
func ScanCipherDbKeys(ctx context.Context, dataDir string, pid uint32, onProgress func(string)) GetDbKeyResult {
	report := func(format string, a ...any) {
		if onProgress != nil {
			onProgress(fmt.Sprintf(format, a...))
		}
	}
	if strings.TrimSpace(dataDir) == "" {
		return GetDbKeyResult{Error: "微信数据目录为空"}
	}
	dbDir := filepath.Join(dataDir, "db_storage")
	candidates, err := collectDbKeyCandidates(dbDir)
	if err != nil || len(candidates) == 0 {
		return GetDbKeyResult{Error: fmt.Sprintf("收集数据库 salt 失败: %v", err)}
	}
	sortDbKeyCandidates(candidates)
	saltCount := countDbKeySalts(candidates)
	report("Config.Cipher 扫描：%d 个数据库 / %d 个 salt", len(candidates), saltCount)

	pids := collectWechatPIDs(pid)
	if len(pids) == 0 {
		return GetDbKeyResult{Error: "未检测到运行中的微信客户端"}
	}

	scanCtx, cancel := context.WithTimeout(ctx, cipherScanTimeout)
	defer cancel()

	var mu sync.Mutex
	keyBySalt := make(map[string]string, saltCount)
	patterns := cipherPhasePatterns()
	needle := []byte(cipherConfigName)
	var stats cipherStats

	// 记录一次 HMAC 通过的密钥，并把同 salt 的所有库都填上
	record := func(keyHex, saltHex string) bool {
		kb, e := hex.DecodeString(keyHex)
		if e != nil || len(kb) != dbKeySize {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		for _, c := range candidates {
			if saltHex != "" && c.saltHex != saltHex {
				continue
			}
			if _, ok := keyBySalt[c.saltHex]; ok {
				continue
			}
			if verifyDbEncKey(kb, c.page1) {
				keyBySalt[c.saltHex] = keyHex
				stats.verified++
				return true
			}
		}
		return false
	}
	handleLiteral := func(run string) {
		for _, pair := range cipherCandidates(run) {
			stats.candidates++
			record(pair[0], pair[1])
		}
	}

	needleAddrs := make(map[uint64]bool)

	started := time.Now()
	for _, p := range pids {
		if scanCtx.Err() != nil || len(keyBySalt) >= saltCount {
			break
		}
		h, e := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, p)
		if e != nil {
			report("PID=%d 打开失败：%v", p, e)
			continue
		}
		report("正在扫描 PID=%d …", p)
		all := GetMemoryRegions(h)
		for _, r := range all {
			if scanCtx.Err() != nil || len(keyBySalt) >= saltCount {
				break
			}
			if r.RegionSize == 0 || r.RegionSize > cipherMaxRegion || r.State != windows.MEM_COMMIT {
				continue
			}
			if r.Protect == windows.PAGE_NOACCESS || (r.Protect&windows.PAGE_GUARD) != 0 {
				continue
			}
			stats.regions++
			step := uintptr(cipherChunkSize - cipherChunkOverlap)
			for off := uintptr(0); off < r.RegionSize; off += step {
				if scanCtx.Err() != nil || len(keyBySalt) >= saltCount {
					break
				}
				n := uintptr(cipherChunkSize)
				if off+n > r.RegionSize {
					n = r.RegionSize - off
				}
				chunkBase := r.BaseAddress + off
				data := readCipherRange(h, chunkBase, n)
				stats.bytes += len(data)

				// --- 诊断统计：确认 4.2 里 needle / 掩码机器码是否仍然存在 ---
				stats.needle += bytes.Count(data, needle)
				stats.maskCode += bytes.Count(data, cipherXORMask)
				stats.plainHex += countPlainCipherLiterals(data)

				// 收集 needle 绝对地址（对象链第二阶段用）
				from := 0
				for {
					idx := bytes.Index(data[from:], needle)
					if idx < 0 {
						break
					}
					pos := from + idx
					needleAddrs[uint64(chunkBase)+uint64(pos)] = true
					from = pos + len(needle)
					if from >= len(data) {
						break
					}
				}

				// --- 路径 A：掩码相位盲扫 ---
				for i := 0; i+1 < len(data); i++ {
					k := uint16(data[i])<<8 | uint16(data[i+1])
					phases, ok := patterns[k]
					if !ok {
						continue
					}
					for _, ph := range phases {
						stats.phaseHits++
						if run, ok := decodeCipherLiteral(data, i, ph); ok {
							stats.literals++
							handleLiteral(run)
						}
					}
				}
			}
		}

		// --- 路径 B：Config.Cipher 对象链（A 未取全时兜底） ---
		if len(keyBySalt) < saltCount {
			scanCipherObjectChain(scanCtx, h, all, needleAddrs, &stats, handleLiteral, report)
		}
		_ = windows.CloseHandle(h)
	}

	report("统计(%.1fs)：区域=%d 读取=%dMB needle=%d 掩码机器码=%d 明文hex=%d 相位命中=%d 解码字面量=%d 候选=%d 验签通过=%d",
		time.Since(started).Seconds(), stats.regions, stats.bytes/(1024*1024),
		stats.needle, stats.maskCode, stats.plainHex, stats.phaseHits,
		stats.literals, stats.candidates, stats.verified)

	if len(keyBySalt) == 0 {
		return GetDbKeyResult{Error: "Config.Cipher 扫描未取到密钥（needle/掩码可能已随微信版本改变）"}
	}
	crossVerifyDbKeys(candidates, keyBySalt)
	keyJSON, err := buildDbKeyJSON(candidates, keyBySalt)
	if err != nil {
		return GetDbKeyResult{Error: fmt.Sprintf("生成按库密钥 JSON 失败: %v", err)}
	}
	report("Config.Cipher 扫描成功：匹配 %d/%d 个 salt", len(keyBySalt), saltCount)
	return GetDbKeyResult{Success: true, Key: keyJSON}
}

// countPlainCipherLiterals 统计内存里未混淆的 x'<64+hex>'（4.0 老版本路径）。
func countPlainCipherLiterals(data []byte) int {
	n := 0
	for _, prefix := range [][]byte{[]byte("x'"), []byte("X'")} {
		from := 0
		for {
			idx := bytes.Index(data[from:], prefix)
			if idx < 0 {
				break
			}
			pos := from + idx + len(prefix)
			j := pos
			for j < len(data) && j-pos < 192 {
				c := data[j]
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
					break
				}
				j++
			}
			if j-pos >= 64 && j < len(data) && data[j] == '\'' {
				n++
			}
			from = pos
			if from >= len(data) {
				break
			}
		}
	}
	return n
}

// scanCipherObjectChain 复现 needle → (addr,len) pair → node → config_ptr →
// data_ptr/data_len → blob → XOR 解码的对象链。偏移来自 4.1.11 的 WCDB 对象布局，
// 这里对 config_ptr / data_ptr 做了邻域容错，尽量兼容 4.2 的偏移漂移。
func scanCipherObjectChain(
	ctx context.Context,
	h windows.Handle,
	regions []MemoryRegion,
	needleAddrs map[uint64]bool,
	stats *cipherStats,
	handleLiteral func(string),
	report func(string, ...any),
) {
	if len(needleAddrs) == 0 {
		return
	}
	nameLen := uint64(len(cipherConfigName))
	report("对象链：%d 个 needle 地址，单次遍历比对引用对…", len(needleAddrs))

	visited := make(map[uint64]bool)
	for _, r := range regions {
		if ctx.Err() != nil {
			return
		}
		if r.RegionSize == 0 || r.RegionSize > cipherMaxRegion || r.State != windows.MEM_COMMIT {
			continue
		}
		step := uintptr(cipherChunkSize - cipherChunkOverlap)
		for off := uintptr(0); off < r.RegionSize; off += step {
			if ctx.Err() != nil {
				return
			}
			n := uintptr(cipherChunkSize)
			if off+n > r.RegionSize {
				n = r.RegionSize - off
			}
			chunkBase := r.BaseAddress + off
			data := readCipherRange(h, chunkBase, n)
			// 引用对 (needle_addr, len=30) 在 node 内按 8 字节对齐。
			// 一次遍历比对指针集合，避免按 needle 数量做 N 次全内存搜索。
			for i := 0; i+16 <= len(data); i += 8 {
				addr := binary.LittleEndian.Uint64(data[i:])
				if !needleAddrs[addr] || binary.LittleEndian.Uint64(data[i+8:]) != nameLen {
					continue
				}
				qaddr := uint64(chunkBase) + uint64(i)
				if visited[qaddr] {
					continue
				}
				visited[qaddr] = true
				nodeBase := qaddr - 0x10
				node, ok := readCipherAt(h, uintptr(nodeBase), 0x50)
				if !ok || u64le(node, 0x10) != addr || u64le(node, 0x18) != nameLen {
					continue
				}
				// config_ptr 在 node+0x28；4.2 偏移可能漂移，向前后各试几个 8 字节槽位
				for _, nodeOff := range []int{0x20, 0x28, 0x30, 0x38, 0x40} {
					configPtr := u64le(node, nodeOff)
					if configPtr < 0x10000 || configPtr >= cipherMaxUserAddr {
						continue
					}
					for _, objOff := range []uintptr{0x88, 0x80, 0x90, 0x98, 0xA0} {
						obj, ok := readCipherAt(h, uintptr(configPtr)+objOff, 0x28)
						if !ok {
							continue
						}
						for _, fieldOff := range []int{0x0, 0x8} {
							dataPtr := u64le(obj, fieldOff)
							dataLen := u64le(obj, fieldOff+8)
							if dataPtr < 0x10000 || dataPtr >= cipherMaxUserAddr || dataLen == 0 || dataLen > cipherBlobMax {
								continue
							}
							blob, ok := readCipherAt(h, uintptr(dataPtr), int(dataLen))
							if !ok {
								continue
							}
							tryBlobLiterals(blob, stats, handleLiteral)
						}
					}
				}
			}
		}
	}
}

// tryBlobLiterals 对 blob 按所有 32 个相位解码（对象链里 blob 起点相位未知）。
func tryBlobLiterals(blob []byte, stats *cipherStats, handleLiteral func(string)) {
	maskLen := len(cipherXORMask)
	for phase := 0; phase < maskLen; phase++ {
		dec := make([]byte, len(blob))
		for i := range blob {
			dec[i] = blob[i] ^ cipherXORMask[(phase+i)%maskLen]
		}
		for _, prefix := range [][]byte{[]byte("x'"), []byte("X'")} {
			from := 0
			for {
				idx := bytes.Index(dec[from:], prefix)
				if idx < 0 {
					break
				}
				pos := from + idx + 2
				j := pos
				for j < len(dec) && j-pos < 192 {
					c := dec[j]
					if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
						break
					}
					j++
				}
				if j-pos >= 64 && j < len(dec) && dec[j] == '\'' {
					stats.literals++
					handleLiteral(string(dec[pos:j]))
				}
				from = pos
				if from >= len(dec) {
					break
				}
			}
		}
	}
}
