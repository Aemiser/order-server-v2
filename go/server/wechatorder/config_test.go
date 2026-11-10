package wechatorder

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 目标订单日期默认跟随今天：每次读取配置都重算当天，等价于「每次启动默认今天」。
func TestTargetDateFollowsTodayByDefault(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.AutoTargetDate {
		t.Fatal("默认应开启「跟随今天」")
	}
	today := time.Now().Format("2006-01-02")
	if TargetDateString(cfg) != today {
		t.Fatalf("默认目标日期应为今天 %s，实际 %s", today, TargetDateString(cfg))
	}
	if TargetDateLabel(EffectiveTargetDate(cfg)) != time.Now().Format("1-2") {
		t.Fatal("目标日期的 M-D 标记应与当天一致")
	}

	// 模拟「上次手动选了历史日期，重启后应回到今天」
	cfg.TargetDate = "2020-01-01"
	normalize(&cfg)
	if cfg.TargetDate != today || !cfg.AutoTargetDate {
		t.Fatalf("跟随今天时应强制回到当天，实际 %+v", cfg)
	}
}

// 关闭跟随今天后，手动指定的日期必须被保留并用于导出窗口。
func TestManualTargetDateIsKept(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AutoTargetDate = false
	cfg.TargetDate = "2026-11-05"
	normalize(&cfg)
	if cfg.AutoTargetDate || cfg.TargetDate != "2026-11-05" {
		t.Fatalf("手动日期应保留，实际 %+v", cfg)
	}
	if got := TargetDateString(cfg); got != "2026-11-05" {
		t.Fatalf("EffectiveTargetDate 应使用手动日期，实际 %s", got)
	}
	if got := TargetDateLabel(EffectiveTargetDate(cfg)); got != "11-5" {
		t.Fatalf("日期标记应为 11-5，实际 %s", got)
	}

	// 非法日期回退到今天，避免导出窗口算错
	cfg.TargetDate = "not-a-date"
	normalize(&cfg)
	if !cfg.AutoTargetDate || cfg.TargetDate != time.Now().Format("2006-01-02") {
		t.Fatalf("非法日期应回退今天，实际 %+v", cfg)
	}
}

// 按钮旁临时选的日期只覆盖本轮：生效于导出窗口 / AI 提示词 / 日期分流，
// 但不写配置文件，下次运行（或重启）仍然默认今天。
func TestApplyTargetDateOverride(t *testing.T) {
	cfg := DefaultConfig()
	if !ApplyTargetDateOverride(&cfg, "2026-11-05") {
		t.Fatal("合法日期应覆盖成功")
	}
	if cfg.AutoTargetDate || cfg.TargetDate != "2026-11-05" {
		t.Fatalf("覆盖后应使用指定日期，实际 %+v", cfg)
	}
	if got := TargetDateString(cfg); got != "2026-11-05" {
		t.Fatalf("导出窗口终点应为 2026-11-05，实际 %s", got)
	}
	if got := TargetDateLabel(EffectiveTargetDate(cfg)); got != "11-5" {
		t.Fatalf("日期标记应为 11-5，实际 %s", got)
	}

	// 兼容 2026-1-7 这类非补零写法
	cfg = DefaultConfig()
	if !ApplyTargetDateOverride(&cfg, "2026-1-7") || cfg.TargetDate != "2026-01-07" {
		t.Fatalf("非补零日期应被归一化，实际 %+v", cfg)
	}

	// 空串 / 非法格式 / 未来日期 / nil 一律忽略，沿用默认（今天）
	today := time.Now().Format("2006-01-02")
	future := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	for _, bad := range []string{"", "  ", "not-a-date", "2026-13-45", future} {
		cfg = DefaultConfig()
		if ApplyTargetDateOverride(&cfg, bad) || cfg.TargetDate != today || !cfg.AutoTargetDate {
			t.Fatalf("非法输入 %q 应被忽略，实际 %+v", bad, cfg)
		}
	}
	if ApplyTargetDateOverride(nil, "2026-11-05") {
		t.Fatal("nil 配置应安全返回 false")
	}
}

// 覆盖只改内存，不落盘：写一份配置文件，覆盖后文件内容与下次 LoadConfig 都不变。
func TestTargetDateOverrideDoesNotPersist(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0755); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"group":"测试群","autoTargetDate":true,"targetDate":"2020-01-01","useRuleFilter":true}`)
	if err := os.WriteFile(ConfigPath(), before, 0644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadConfig()
	if !cfg.AutoTargetDate || cfg.TargetDate != time.Now().Format("2006-01-02") {
		t.Fatalf("读取配置时跟随今天应重算当天，实际 %+v", cfg)
	}
	ApplyTargetDateOverride(&cfg, "2026-11-05")
	after, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("按次覆盖不应改写配置文件")
	}
	if next := LoadConfig(); next.TargetDate != time.Now().Format("2006-01-02") || !next.AutoTargetDate {
		t.Fatalf("下一轮应回到今天，实际 %+v", next)
	}
}

// 旧配置文件里没有新字段时，粗筛必须默认开启、目标日期必须默认今天，
// 不能因为 Go bool 零值把粗筛意外关掉。
func TestOldConfigKeepsNewDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0755); err != nil {
		t.Fatal(err)
	}
	old := []byte(`{"group":"旧群","days":2,"concurrency":50}`)
	if err := os.WriteFile(ConfigPath(), old, 0644); err != nil {
		t.Fatal(err)
	}
	cfg := LoadConfig()
	if cfg.Group != "旧群" || cfg.Concurrency != 50 {
		t.Fatalf("旧配置项应保留，实际 %+v", cfg)
	}
	if !cfg.UseRuleFilter {
		t.Fatal("旧配置升级后粗筛应默认开启")
	}
	if !cfg.AutoTargetDate || cfg.TargetDate != time.Now().Format("2006-01-02") {
		t.Fatalf("旧配置升级后目标日期应默认今天，实际 %+v", cfg)
	}
}
