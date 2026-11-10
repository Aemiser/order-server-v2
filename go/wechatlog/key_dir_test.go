package wechatlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 模拟「用户没有这个路径/文件」：从干净环境出发，验证目录会被自动创建，
// 且 KeyDropDir 与 KeyCachePath 始终位于同一父目录（不会错位）。
func TestKeyDirsAutoCreatedAndConsistent(t *testing.T) {
	// 用一个隔离的 APPDATA，确保目录下没有任何 order_server 残留
	t.Setenv("APPDATA", t.TempDir())

	drop := KeyDropDir()
	cache := KeyCachePath()

	// 两者父目录都应是 order_server，保证「写 keys/ 与读缓存」在同一处，不会错位
	if filepath.Dir(drop) != filepath.Dir(cache) {
		t.Fatalf("自动密钥目录与缓存目录父目录不一致: drop=%s cache=%s", drop, cache)
	}
	// 目录必须被创建出来
	if fi, err := os.Stat(filepath.Dir(drop)); err != nil || !fi.IsDir() {
		t.Fatalf("order_server 配置目录未被自动创建: %v", err)
	}
	if fi, err := os.Stat(drop); err != nil || !fi.IsDir() {
		t.Fatalf("keys 子目录未被自动创建: %v", err)
	}
	// 缓存文件此时不应存在（只是没取到密钥），但路径要合法
	if _, err := os.Stat(cache); err == nil {
		t.Fatalf("首次运行不应已有缓存文件: %s", cache)
	}
	// 写一次缓存应当成功
	if err := SaveKeyCache(KeyCache{Key: strings.Repeat("ab", 32), Source: "test"}); err != nil {
		t.Fatalf("SaveKeyCache 失败（目录不可写？）: %v", err)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("SaveKeyCache 后缓存文件不存在: %v", err)
	}
}

// 当 os.UserConfigDir() 不可用（极端环境）时，应回退到可执行文件同级 config 目录，
// 而不是 "." —— 否则进程 CWD 漂移会导致「写入 keys/ 与读取缓存错位」，且可能写入不可写位置。
func TestOrderServerDirFallbackNotCWD(t *testing.T) {
	t.Setenv("APPDATA", "") // 触发回退分支
	dir, _ := orderServerDir()
	// 回退位置必须是绝对路径，且最终落在 config/order_server，不会随 CWD 漂移
	if !filepath.IsAbs(dir) {
		t.Fatalf("回退路径必须是绝对路径，实际 %s", dir)
	}
	if !strings.Contains(dir, "order_server") {
		t.Fatalf("回退目录结构异常: %s", dir)
	}
	// 回退目录必须能被创建出来
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("回退目录未被创建: %v", err)
	}
}

// 缓存路径曾是目录的异常场景：LoadKeyCache 应当清理掉并返回可重试错误，而不是卡死。
func TestLoadKeyCacheClearsDirectoryPath(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	path := KeyCachePath()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyCache(); err == nil {
		t.Fatal("缓存路径是目录时应返回错误")
	}
	// 清理后应变成文件或不存在，不再卡在「目录」状态
	fi, statErr := os.Stat(path)
	if statErr == nil && fi.IsDir() {
		t.Fatal("LoadKeyCache 未清理掉异常目录")
	}
}

// KeyDropStatus 在目录不可写时应暴露 writable=false 与 error（只读盘/权限不足场景）。
func TestKeyDropStatusReportsWritable(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	st := KeyDropStatus()
	if st["writable"] != true {
		t.Fatalf("正常可写目录应报告 writable=true，实际 %v", st["writable"])
	}
	if _, ok := st["error"]; ok {
		t.Fatalf("正常目录不应有 error 字段: %v", st["error"])
	}
}
