package wechatlog

// 自动密钥发现（兜底通道）：内置的 Config.Cipher 只读内存扫描
// （见 internal/wechat/key/windows/cipher_scanner.go）已在 4.2.0.13 上实测
// 自动取全 23 个库密钥，正常情况下不需要这里。
// 仅当未来微信改版导致内置扫描失效时，用户可把任意取密钥工具的输出文件放进
// 「自动密钥目录」，程序会自动解析、用真实数据库第一页校验、写入本地缓存。
//
// 支持：按库密钥 JSON（wx_key / EchoTrace / 本项目扫描器）、{"key": "64hex"}、
// 纯 hex 文本、x'<hex>' 日志片段。格式不做任何信任假设，一律先验签再入库。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

var hexKeyPattern = regexp.MustCompile(`(?:x')?([0-9a-fA-F]{64,192})(?:')?`)

// KeyDropDir 是自动密钥监视目录，程序启动时自动创建。
// 派生自 orderServerDir()，与缓存目录位于同一父目录；创建失败会写日志，避免静默。
func KeyDropDir() string {
	base, _ := orderServerDir()
	dir := filepath.Join(base, "keys")
	if err := os.MkdirAll(dir, 0700); err != nil {
		logrus.Errorf("[微信密钥] 无法创建自动密钥目录 %s: %v", dir, err)
	}
	return dir
}

func keyDiscoveryPaths() []string {
	seen := make(map[string]bool)
	var out []string
	addDir := func(dir string) {
		if dir == "" {
			return
		}
		key := strings.ToLower(filepath.Clean(dir))
		if seen[key] {
			return
		}
		seen[key] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			low := strings.ToLower(e.Name())
			if !strings.HasSuffix(low, ".json") && !strings.HasSuffix(low, ".txt") && !strings.HasSuffix(low, ".key") {
				continue
			}
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	drop := KeyDropDir() // 内部已创建目录
	addDir(drop)
	if exe, err := os.Executable(); err == nil {
		addDir(filepath.Dir(exe))
	}
	addDir(".")
	return out
}

// parseKeyCandidates 从文件内容里抽出可能的密钥（hex 或按库 JSON 原文）。
func parseKeyCandidates(raw []byte) []string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] || len(out) >= 24 {
			return
		}
		seen[s] = true
		out = append(out, s)
	}

	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") {
		// 按库密钥 JSON：整份保留，后续从对应库里取 enc_key 校验
		var perDB map[string]struct {
			EncKey string `json:"enc_key"`
		}
		if json.Unmarshal([]byte(trimmed), &perDB) == nil && len(perDB) > 0 {
			for _, v := range perDB {
				if v.EncKey != "" {
					add(trimmed)
					break
				}
			}
		}
		var generic map[string]json.RawMessage
		if json.Unmarshal([]byte(trimmed), &generic) == nil {
			for _, k := range []string{"key", "db_key", "dbKey", "enc_key", "encKey", "dataKey", "data_key", "keys"} {
				if v, ok := generic[k]; ok {
					var s string
					if json.Unmarshal(v, &s) == nil {
						add(s)
					} else if strings.HasPrefix(strings.TrimSpace(string(v)), "{") {
						add(strings.TrimSpace(string(v)))
					}
				}
			}
			for _, v := range generic {
				var s string
				if json.Unmarshal(v, &s) == nil && len(s) >= 64 {
					add(s)
				}
			}
		}
	}
	for _, m := range hexKeyPattern.FindAllStringSubmatch(text, -1) {
		if len(m) > 1 {
			add(m[1])
		}
	}
	return out
}

// validateDiscoveredKey 用真实数据库第一页校验候选；按库 JSON 时取对应库的 enc_key。
// version=0 表示自动（按锚点库实际参数同时兼容 3.9 / 4.x）。
func validateDiscoveredKey(candidate, anchorDB string) bool {
	return validateDiscoveredKeyVersion(candidate, anchorDB, 0)
}

func validateDiscoveredKeyVersion(candidate, anchorDB string, version int) bool {
	if strings.HasPrefix(strings.TrimSpace(candidate), "{") {
		var perDB map[string]struct {
			EncKey string `json:"enc_key"`
		}
		if json.Unmarshal([]byte(candidate), &perDB) != nil {
			return false
		}
		// 优先校验锚点库，其次任意一条
		for rel, v := range perDB {
			if v.EncKey == "" {
				continue
			}
			if strings.Contains(strings.ReplaceAll(strings.ToLower(rel), "\\", "/"), "message_0.db") {
				if ValidateKeyCandidateVersion(anchorDB, v.EncKey, version) {
					return true
				}
			}
		}
		for _, v := range perDB {
			if v.EncKey != "" && ValidateKeyCandidateVersion(anchorDB, v.EncKey, version) {
				return true
			}
		}
		return false
	}
	return ValidateKeyCandidateVersion(anchorDB, candidate, version)
}

// DiscoverKeyFromFiles 扫描自动目录，返回 (密钥, 来源说明, 是否有效)。
func DiscoverKeyFromFiles(dataDir string, onLog func(string)) (string, string, bool) {
	return DiscoverKeyFromFilesVersion(dataDir, 0, onLog)
}

// DiscoverKeyFromFilesVersion 按微信版本选择锚点库与验签参数。
func DiscoverKeyFromFilesVersion(dataDir string, version int, onLog func(string)) (string, string, bool) {
	if strings.TrimSpace(dataDir) == "" {
		return "", "", false
	}
	anchor := anchorDBPathForVersion(dataDir, version)
	if anchor == "" {
		return "", "", false
	}
	paths := keyDiscoveryPaths()
	sort.Slice(paths, func(i, j int) bool {
		si, _ := os.Stat(paths[i])
		sj, _ := os.Stat(paths[j])
		if si == nil || sj == nil {
			return false
		}
		return si.ModTime().After(sj.ModTime())
	})
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, cand := range parseKeyCandidates(raw) {
			if validateDiscoveredKeyVersion(cand, anchor, version) {
				src := "自动密钥发现（" + filepath.Base(p) + "）"
				if onLog != nil {
					onLog("从 " + filepath.Base(p) + " 自动导入并校验通过数据库密钥")
				}
				return cand, src, true
			}
		}
	}
	return "", "", false
}

func cacheDiscoveredKey(dataDir, key, source string, version int) {
	_ = SaveKeyCache(KeyCache{
		DataDir: dataDir, Fingerprint: dbFingerprint(dataDir),
		Key: key, Source: source, Version: version,
	})
}

// ---------------- 密钥文件后台监视 ----------------

var (
	watcherMu     sync.Mutex
	watcherCancel context.CancelFunc
)

// StartKeyDropWatcher 后台定时检查自动密钥目录；文件一放进去就自动导入缓存并回调。
// 配合自动运行调度，用户只需把取密钥工具的输出放进目录，后续链路全程无人值守。
func StartKeyDropWatcher(dataDir string, onFound func(source string)) {
	StopKeyDropWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	watcherMu.Lock()
	watcherCancel = cancel
	watcherMu.Unlock()

	go func() {
		_ = KeyDropDir() // 内部已创建目录
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		check := func() bool {
			if strings.TrimSpace(dataDir) == "" {
				return false
			}
			fp := dbFingerprint(dataDir)
			if c, err := LoadKeyCache(); err == nil && c.Key != "" && c.Fingerprint == fp &&
				strings.EqualFold(filepath.Clean(c.DataDir), filepath.Clean(dataDir)) {
				return false // 已有有效缓存
			}
			version := dbLayoutVersion(dataDir)
			key, src, ok := DiscoverKeyFromFilesVersion(dataDir, version, nil)
			if !ok {
				return false
			}
			cacheDiscoveredKey(dataDir, key, src, version)
			logrus.Infof("[微信密钥] %s，已写入本地缓存", src)
			if onFound != nil {
				onFound(src)
			}
			return true
		}
		check()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check()
			}
		}
	}()
}

func StopKeyDropWatcher() {
	watcherMu.Lock()
	defer watcherMu.Unlock()
	if watcherCancel != nil {
		watcherCancel()
		watcherCancel = nil
	}
}

// KeyDropStatus 给配置页展示自动密钥目录与已发现文件。
// 同时探测目录是否可写：不可写（只读盘 / 权限不足）时返回 writable=false 与 error，
// 让配置页直接告警，而不是让用户把文件放进去后毫无反应。
func KeyDropStatus() map[string]interface{} {
	dir := KeyDropDir()
	files := []string{}
	writable := true
	var dirErr string
	if _, statErr := os.Stat(dir); statErr != nil {
		writable = false
		dirErr = statErr.Error()
	} else {
		probe := filepath.Join(dir, ".wprobe")
		if err := os.WriteFile(probe, []byte("1"), 0600); err != nil {
			writable = false
			dirErr = err.Error()
		} else {
			_ = os.Remove(probe)
		}
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				low := strings.ToLower(e.Name())
				// 只列会被自动解析的格式，避免用户误以为其它文件也已被处理
				if !strings.HasSuffix(low, ".json") && !strings.HasSuffix(low, ".txt") && !strings.HasSuffix(low, ".key") {
					continue
				}
				files = append(files, e.Name())
			}
		}
	}
	out := map[string]interface{}{
		"dir":      dir,
		"files":    files,
		"writable": writable,
		"hint":     fmt.Sprintf("把取密钥工具输出的 JSON / txt 放进 %s 即自动生效", dir),
	}
	if dirErr != "" {
		out["error"] = dirErr
	}
	return out
}
