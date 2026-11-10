//go:build windows

package windows

// 内存密钥诊断：确认微信进程里到底有没有 salt / 高熵密钥候选，
// 以及候选是「已派生 enc_key」还是「原始 raw key」（后者需 PBKDF2 256000 次校验）。
// 仅用于排查，不参与正式扫描链路。
//
// 历史结论（4.2.0.13）：4942 个明文高熵候选双语义 0 命中，原因是密钥被
// Config.Cipher 的 32 字节掩码 XOR 混淆，而非「不在内存里」。
// 正式取密钥请走 cipher_scanner.go。

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"order_server/go/wechatlog/internal/wechat/decrypt"
)

func DiagnoseKeyMemory(ctx context.Context, dataDir string, pid uint32, onProgress func(string)) string {
	report := func(format string, a ...any) {
		if onProgress != nil {
			onProgress(fmt.Sprintf(format, a...))
		}
	}
	// 4.x 的数据库在 db_storage 下；3.9 直接散落在账号目录 Msg 下
	dbDir := filepath.Join(dataDir, "db_storage")
	version := 4
	if _, statErr := os.Stat(dbDir); statErr != nil {
		if _, v3Err := os.Stat(filepath.Join(dataDir, "Msg", "Misc.db")); v3Err == nil {
			version, dbDir = 3, dataDir
		}
	}
	candidates, err := collectDbKeyCandidates(dbDir)
	if err != nil || len(candidates) == 0 {
		return fmt.Sprintf("无法收集数据库 salt（微信 %d.x，目录 %s）: %v", version, dbDir, err)
	}
	// 锚点精确匹配 message\message_0.db，避免误选 biz_message_0.db
	var anchor dbKeyCandidate
	foundAnchor := false
	for _, c := range candidates {
		if strings.EqualFold(filepath.Base(c.relPath), "message_0.db") {
			anchor, foundAnchor = c, true
			break
		}
	}
	if !foundAnchor {
		anchor = candidates[0]
	}
	report("锚点库: %s, salt=%s", anchor.relPath, anchor.saltHex)

	salts := make([][]byte, 0, len(candidates))
	for _, c := range candidates {
		b, e := hex.DecodeString(c.saltHex)
		if e == nil && len(b) == dbSaltSize {
			salts = append(salts, b)
		}
	}
	if pid == 0 {
		pids := collectWechatPIDs(0)
		if len(pids) == 0 {
			return "未找到微信进程"
		}
		pid = pids[0]
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		return fmt.Sprintf("打开进程失败: %v", err)
	}
	defer windows.CloseHandle(h)

	regions := binaryScanRegions(h)
	start := time.Now()
	totalRegions, nonEmptyRegions, saltHits := 0, 0, 0
	seen := make(map[string]bool)
	var windowsToCheck [][]byte

	// 阶段 1：收集所有 salt 邻域内的高熵 32 字节候选
	for _, br := range regions {
		if ctx.Err() != nil {
			break
		}
		totalRegions++
		data := readBinaryRegion(h, br.region.BaseAddress, br.region.RegionSize)
		if len(data) < binaryKeySize {
			continue
		}
		nonZero := false
		for i := 0; i < len(data); i += 4096 {
			if data[i] != 0 {
				nonZero = true
				break
			}
		}
		if !nonZero {
			continue
		}
		nonEmptyRegions++
		for _, salt := range salts {
			from := 0
			for {
				idx := bytes.Index(data[from:], salt)
				if idx < 0 {
					break
				}
				pos := from + idx
				saltHits++
				lo := pos - binarySaltNeighbor
				if lo < 0 {
					lo = 0
				}
				hi := pos + binarySaltNeighbor
				if hi > len(data)-binaryKeySize {
					hi = len(data) - binaryKeySize
				}
				for off := lo - (lo % binaryKeyAlign); off <= hi; off += binaryKeyAlign {
					win := data[off : off+binaryKeySize]
					if !looksLikeBinaryKey(win) {
						continue
					}
					k := hex.EncodeToString(win)
					if seen[k] {
						continue
					}
					seen[k] = true
					cp := make([]byte, binaryKeySize)
					copy(cp, win)
					windowsToCheck = append(windowsToCheck, cp)
				}
				from = pos + len(salt)
				if from >= len(data) {
					break
				}
			}
		}
	}
	report("收集完成(%.1fs): 区域=%d 非空=%d, salt命中=%d, 高熵候选=%d，开始并行双语义校验",
		time.Since(start).Seconds(), totalRegions, nonEmptyRegions, saltHits, len(windowsToCheck))

	// 阶段 2：4.x 并行做「已派生 enc_key」与「原始 raw key」两种语义校验；
	// 3.9 只有「原始 raw key」一种语义，且解密器参数完全不同（SHA1/64000）。
	dec, _ := decrypt.NewDecryptor("windows", version)
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan []byte, 256)
	var wg sync.WaitGroup
	var mu sync.Mutex
	encHits, rawHits, checked := 0, 0, 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for win := range jobs {
				if ctx.Err() != nil {
					return
				}
				hitE := false
				if version == 4 {
					for _, c := range candidates {
						if verifyDbEncKey(win, c.page1) {
							hitE = true
							break
						}
					}
				}
				hitR := dec != nil && dec.Validate(anchor.page1, win)
				mu.Lock()
				checked++
				if hitE {
					encHits++
				}
				if hitR {
					rawHits++
				}
				if checked%1000 == 0 {
					report("已校验 %d/%d 候选…", checked, len(windowsToCheck))
				}
				mu.Unlock()
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, w := range windowsToCheck {
			select {
			case <-ctx.Done():
				return
			case jobs <- w:
			}
		}
	}()
	wg.Wait()

	return fmt.Sprintf("诊断完成(微信 %d.x, %.1fs): 区域=%d 非空=%d, salt命中=%d, 高熵候选=%d, "+
		"已校验=%d, enc_key语义命中=%d, raw_key语义命中=%d",
		version, time.Since(start).Seconds(), totalRegions, nonEmptyRegions, saltHits,
		len(windowsToCheck), checked, encHits, rawHits)
}
