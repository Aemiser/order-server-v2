package wechatlog

// 参考 hicccc77/WeFlow 的 electron/services/keyProviderService.ts：
// 密钥获取外包给用户自备的可执行程序，应用只按 NDJSON 协议交互，
// 不做任何隐式注入、UI 自动化或来源校验；未配置时明确报「未配置」。
//
// 协议：
//   请求：一行 JSON 写入子进程 stdin 后关闭
//     {"action":"get_db_key","dbPath":"...","accountId":"...","internalDbKeyHex":"..."}
//   响应：stdout 按行输出 NDJSON
//     {"type":"progress","message":"...","level":0}   // 可多次
//     {"type":"result","success":true,"key":"..."}    // 恰好一次
//   超时默认 120s。

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const DefaultProviderTimeout = 120 * time.Second

// ProviderRequest 是写进密钥提供者 stdin 的一行 JSON。
type ProviderRequest struct {
	Action           string `json:"action"`
	DBPath           string `json:"dbPath"`
	DataDir          string `json:"dataDir,omitempty"`
	AccountID        string `json:"accountId,omitempty"`
	InternalDBKeyHex string `json:"internalDbKeyHex,omitempty"`
}

// KeyProviderResult 是外部程序的最终返回。
type KeyProviderResult struct {
	Success   bool     `json:"success"`
	Key       string   `json:"key"`
	AccountID string   `json:"accountId"`
	Error     string   `json:"error"`
	Logs      []string `json:"logs"`
}

type providerEvent struct {
	Type      string          `json:"type"`
	Message   string          `json:"message"`
	Level     int             `json:"level"`
	Success   bool            `json:"success"`
	Key       string          `json:"key"`
	DBKey     string          `json:"dbKey"`
	Keys      json.RawMessage `json:"keys"`
	AccountID string          `json:"accountId"`
	Error     string          `json:"error"`
}

// InvokeKeyProvider 拉起外部密钥提供者并按 NDJSON 协议取数据库密钥。
func InvokeKeyProvider(ctx context.Context, providerPath string, req ProviderRequest, timeout time.Duration, onStatus func(string, int)) (KeyProviderResult, error) {
	var out KeyProviderResult
	providerPath = strings.TrimSpace(providerPath)
	if providerPath == "" {
		return out, fmt.Errorf("未配置外部密钥获取程序（keyProviderPath）")
	}
	if _, err := os.Stat(providerPath); err != nil {
		return out, fmt.Errorf("外部密钥获取程序不存在：%s", providerPath)
	}
	if timeout <= 0 {
		timeout = DefaultProviderTimeout
	}
	childCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(childCtx, providerPath)
	hideProviderWindow(cmd)
	req.Action = "get_db_key"
	payload, _ := json.Marshal(req)
	cmd.Stdin = bytes.NewReader(append(payload, '\n'))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return out, fmt.Errorf("创建密钥提供者输出管道失败: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return out, fmt.Errorf("外部密钥获取程序启动失败: %w", err)
	}

	found := scanProviderOutput(stdout, onStatus, &out)
	cancel()
	_ = cmd.Wait()

	if !found {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(strings.Join(out.Logs, "；"))
		}
		if childCtx.Err() == context.DeadlineExceeded {
			return out, fmt.Errorf("外部密钥获取程序超时（%s）", timeout)
		}
		if detail != "" {
			if len([]rune(detail)) > 300 {
				detail = string([]rune(detail)[:300]) + "…"
			}
			return out, fmt.Errorf("外部密钥获取程序未返回 result：%s", detail)
		}
		return out, fmt.Errorf("外部密钥获取程序未返回 result")
	}
	if !out.Success || strings.TrimSpace(out.Key) == "" {
		msg := out.Error
		if msg == "" {
			msg = "返回的 success=false 或 key 为空"
		}
		out.Error = msg
	}
	return out, nil
}

// scanProviderOutput 逐行解析 NDJSON，拿到 result 立即结束（与 WeFlow 一致）。
func scanProviderOutput(r io.Reader, onStatus func(string, int), out *KeyProviderResult) bool {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev providerEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue // 非 JSON 的诊断行直接忽略
		}
		switch ev.Type {
		case "progress":
			if ev.Message != "" {
				out.Logs = append(out.Logs, ev.Message)
				if onStatus != nil {
					onStatus(ev.Message, ev.Level)
				}
			}
		case "result":
			out.Success = ev.Success
			out.Key = firstNonEmpty(ev.Key, ev.DBKey)
			if out.Key == "" && len(ev.Keys) > 0 && string(ev.Keys) != "null" {
				out.Key = string(ev.Keys) // 允许直接返回「按库密钥」JSON
			}
			out.AccountID = ev.AccountID
			out.Error = ev.Error
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 密钥缓存：取一次即可长期离线使用；数据库文件变化（新增分片/换账号/重装）时失效。

// KeyCache 存的是密钥本身，权限收紧到 0600，只落本地，不外传。
type KeyCache struct {
	DataDir     string `json:"dataDir"`
	Fingerprint string `json:"fingerprint"`
	Key         string `json:"key"`
	Source      string `json:"source"`
	AccountID   string `json:"accountId"`
	Version     int    `json:"version,omitempty"` // 微信大版本：3 / 4；0 = 旧缓存（未知）
	UpdatedAt   string `json:"updatedAt"`
}

var keyCacheMu sync.Mutex

// orderServerDir 返回 order_server 专用配置目录（%APPDATA%/order_server 或等价位置），
// 并尽力创建它。KeyDropDir 与 KeyCachePath 都从这里派生，保证两者始终位于同一父目录，
// 不会因各自回退到不同 CWD 而导致「写入 keys/、读取 wechat_key_cache.json 错位」。
//
// 当 os.UserConfigDir() 不可用（极少见）时，回退到「可执行文件同级 config 目录」，
// 比回退到 "." 更可预测——不会随进程当前工作目录漂移，也不会在打包后指向不可写处。
func orderServerDir() (string, error) {
	base, ucErr := os.UserConfigDir()
	if ucErr != nil || strings.TrimSpace(base) == "" {
		if exe, e := os.Executable(); e == nil {
			base = filepath.Join(filepath.Dir(exe), "config")
		} else {
			base = "config"
		}
	}
	dir := filepath.Join(base, "order_server")
	if mkErr := os.MkdirAll(dir, 0700); mkErr != nil {
		logrus.Errorf("[微信密钥] 无法创建配置目录 %s: %v", dir, mkErr)
		if ucErr != nil {
			return dir, ucErr
		}
		return dir, mkErr
	}
	return dir, ucErr
}

func KeyCachePath() string {
	dir, _ := orderServerDir()
	return filepath.Join(dir, "wechat_key_cache.json")
}

func LoadKeyCache() (KeyCache, error) {
	keyCacheMu.Lock()
	defer keyCacheMu.Unlock()
	var c KeyCache
	path := KeyCachePath()
	// 历史异常（或用户误建）可能让缓存路径成了一个目录，这里清理掉，
	// 避免一直把它当成「损坏缓存」卡住、又无法自愈。
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		_ = os.RemoveAll(path)
		return c, fmt.Errorf("缓存路径异常（曾为目录），已清理后可重新获取")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return KeyCache{}, err
	}
	return c, nil
}

func SaveKeyCache(c KeyCache) error {
	keyCacheMu.Lock()
	defer keyCacheMu.Unlock()
	path := KeyCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if c.UpdatedAt == "" {
		c.UpdatedAt = time.Now().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func ClearKeyCache() error {
	keyCacheMu.Lock()
	defer keyCacheMu.Unlock()
	err := os.Remove(KeyCachePath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// KeyCacheInfo 供配置页展示缓存状态，不返回密钥明文。
// 同时暴露目录是否存在 / 是否可读，便于用户发现「缓存写入/读取失败」的根因。
func KeyCacheInfo() map[string]interface{} {
	path := KeyCachePath()
	out := map[string]interface{}{"path": path, "exists": false}
	dir := filepath.Dir(path)
	if _, statErr := os.Stat(dir); statErr != nil {
		out["dirError"] = statErr.Error()
		return out
	}
	c, err := LoadKeyCache()
	if err != nil {
		// 文件不存在是常态（首次运行），只有「存在但读不了 / 解析失败」才上报告警
		if !os.IsNotExist(err) {
			out["error"] = err.Error()
		}
		return out
	}
	out["exists"] = true
	out["dataDir"] = c.DataDir
	out["source"] = c.Source
	out["updatedAt"] = c.UpdatedAt
	out["fingerprint"] = c.Fingerprint
	return out
}

// dbFingerprint 用「相对路径 + 大小 + 修改时间」给数据目录做指纹，
// 只要微信新增 message_N.db 分片或换了账号，指纹就会变化。
func dbFingerprint(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	var lines []string
	_ = filepath.WalkDir(dataDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".db") {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return nil
		}
		rel, e := filepath.Rel(dataDir, path)
		if e != nil {
			return nil
		}
		lines = append(lines, fmt.Sprintf("%s|%d|%d", filepath.ToSlash(rel), info.Size(), info.ModTime().Unix()))
		return nil
	})
	if len(lines) == 0 {
		return ""
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:8])
}

// ---------------------------------------------------------------------------
// 密钥解析编排：手动 > 缓存 > 外部 provider > 内存扫描（仅在显式开启时）

// KeyRequest 汇总 ResolveKey 需要的全部输入。
type KeyRequest struct {
	DataDir          string
	AccountID        string
	Version          int // 微信大版本 3 / 4；0 = 自动
	ManualKey        string
	KeyProviderPath  string
	ProviderTimeout  time.Duration
	AutoKey          bool // 允许从微信进程内存扫描（只读，不注入）
	AutoKeyOnFailure bool // 缓存失效时允许重新取密钥
	ForceRefresh     bool // 解密校验失败后的重取
	Scan             func() (string, error)
	Log              func(string)
}

// KeyResolution 记录最终密钥及其来源，供界面显示。
type KeyResolution struct {
	Key      string
	Source   string
	Warnings []string
}

func ResolveKey(req KeyRequest) (KeyResolution, error) {
	var res KeyResolution
	warn := func(format string, a ...interface{}) {
		msg := fmt.Sprintf(format, a...)
		res.Warnings = append(res.Warnings, msg)
		if req.Log != nil {
			req.Log(msg)
		}
	}

	// 1) 手动填写的密钥优先级最高，且不进缓存（配置本身就是持久化）
	if k := strings.TrimSpace(req.ManualKey); k != "" {
		return KeyResolution{Key: k, Source: "手动配置", Warnings: res.Warnings}, nil
	}

	fp := dbFingerprint(req.DataDir)
	cache, cacheErr := LoadKeyCache()
	// 换版本（4.x <-> 3.9）时密钥语义完全不同，旧缓存必须作废
	versionMatches := cache.Version == 0 || req.Version == 0 || cache.Version == req.Version
	cachedValid := cacheErr == nil && cache.Key != "" && versionMatches &&
		strings.EqualFold(filepath.Clean(cache.DataDir), filepath.Clean(req.DataDir)) &&
		cache.Fingerprint != "" && cache.Fingerprint == fp
	cachedStale := cacheErr == nil && cache.Key != "" && versionMatches &&
		strings.EqualFold(filepath.Clean(cache.DataDir), filepath.Clean(req.DataDir)) &&
		cache.Fingerprint != fp
	if cacheErr == nil && cache.Key != "" && !versionMatches {
		warn("检测到微信版本变化（缓存 %d → 当前 %d），密钥缓存不适用，将重新获取", cache.Version, req.Version)
	}

	if !req.ForceRefresh && cachedValid {
		// 缓存不只要「存在」，还要能用当前数据库第一页通过 HMAC 验签；
		// 内容损坏 / 换库但指纹未变时自动重新获取，实现零配置自愈。
		anchor := anchorDBPathForVersion(req.DataDir, req.Version)
		if anchor == "" || !cachedKeyVerifiable(cache.Key) ||
			validateDiscoveredKeyVersion(cache.Key, anchor, req.Version) {
			return KeyResolution{Key: cache.Key, Source: "本地缓存（" + cache.Source + "）", Warnings: res.Warnings}, nil
		}
		warn("本地密钥缓存未通过数据库第一页校验，自动重新获取")
	}
	if cachedStale {
		warn("检测到微信数据库文件变化，密钥缓存已失效")
		if !req.AutoKeyOnFailure {
			return res, fmt.Errorf("密钥缓存已失效且未开启「缓存失效自动重取」；请重新粘贴密钥或开启该开关")
		}
	}

	// 1.5) 自动密钥目录（兜底）：若目录里放有外部工具输出，直接解析+验签+缓存。
	//     零配置主路径是第 3 步 req.Scan 里的内置 Config.Cipher 只读内存扫描。
	if key, src, ok := DiscoverKeyFromFilesVersion(req.DataDir, req.Version, func(s string) { warn("%s", s) }); ok {
		cacheDiscoveredKey(req.DataDir, key, src, req.Version)
		return KeyResolution{Key: key, Source: src, Warnings: res.Warnings}, nil
	}

	// 2) WeFlow 式外部密钥提供者
	if p := strings.TrimSpace(req.KeyProviderPath); p != "" {
		r, err := InvokeKeyProvider(context.Background(), p, ProviderRequest{
			DBPath:           sessionDBPath(req.DataDir),
			DataDir:          req.DataDir,
			AccountID:        req.AccountID,
			InternalDBKeyHex: cache.Key,
		}, req.ProviderTimeout, func(msg string, _ int) {
			warn("密钥提供者：%s", msg)
		})
		if err != nil {
			warn("外部密钥获取失败：%v", err)
		} else if !r.Success || r.Key == "" {
			warn("外部密钥获取返回失败：%s", r.Error)
		} else {
			key := strings.TrimSpace(r.Key)
			_ = SaveKeyCache(KeyCache{
				DataDir: req.DataDir, Fingerprint: fp, Key: key,
				Source: "keyProvider", AccountID: r.AccountID, Version: req.Version,
			})
			return KeyResolution{Key: key, Source: "外部密钥提供者", Warnings: res.Warnings}, nil
		}
	}

	// 3) 显式开启时才做只读内存扫描（不是静默降级）
	if req.AutoKey && req.Scan != nil {
		key, err := req.Scan()
		if err != nil {
			warn("内置自动取密钥失败：%v", err)
		} else if strings.TrimSpace(key) != "" {
			_ = SaveKeyCache(KeyCache{
				DataDir: req.DataDir, Fingerprint: fp, Key: key,
				Source: "内置自动取密钥", AccountID: req.AccountID, Version: req.Version,
			})
			return KeyResolution{Key: key, Source: "内置自动取密钥（只读内存）", Warnings: res.Warnings}, nil
		}
	}

	hint := fmt.Sprintf(
		"请确认微信客户端已登录且正在运行；若仍失败，可把取密钥工具输出放进自动密钥目录 %s，或在配置页手动粘贴密钥。密钥缓存写入 %s（若创建/写入失败，请检查该目录权限或磁盘是否只读）。"+
			"若微信已运行却仍报「未检测到」，多为本程序与微信的「以管理员身份运行」状态不一致（完整性级别不同），请让两者保持一致（都管理员或都普通）后重试。",
		KeyDropDir(), KeyCachePath())
	if runtime.GOOS != "windows" {
		hint += "；非 Windows 平台不支持内存扫描"
	}
	return res, fmt.Errorf("未能取得微信数据库密钥（%s）。%s", strings.Join(res.Warnings, "；"), hint)
}

// cachedKeyVerifiable 判断缓存里的密钥是否属于「可以用数据库第一页验签」的格式。
// 其它格式（例如老版本带 x' 前缀的日志片段）保守沿用缓存，不误判为失效。
func cachedKeyVerifiable(key string) bool {
	t := strings.TrimSpace(key)
	if strings.HasPrefix(t, "{") {
		return true
	}
	return len(t) >= 64 && len(t) <= 192 && hexKeyPattern.MatchString(t)
}

// sessionDBPath 推导微信账号的 session.db 路径，供 keyProvider / WCDB 使用。
func sessionDBPath(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	base := filepath.Clean(dataDir)
	low := strings.ToLower(filepath.ToSlash(base))
	var candidate string
	if strings.HasSuffix(low, "db_storage") {
		candidate = filepath.Join(base, "session", "session.db")
	} else {
		candidate = filepath.Join(base, "db_storage", "session", "session.db")
	}
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	// 兼容用户直接填了 db_storage 上级但目录布局不同的情况
	var found string
	_ = filepath.WalkDir(dataDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" || d == nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), "session.db") {
			found = path
		}
		return nil
	})
	if found != "" {
		return found
	}
	// 微信 3.9 没有 session.db，密钥提供者协议里的 dbPath 用账号的 Misc.db 代替
	if dbLayoutVersion(base) == 3 {
		for _, rel := range v3DBRelCandidates {
			p := filepath.Join(base, rel)
			if isFile(p) {
				return p
			}
		}
	}
	return candidate
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
