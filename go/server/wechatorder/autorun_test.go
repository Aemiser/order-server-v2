package wechatorder

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"order_server/go/global"
	"order_server/go/models"

	"gorm.io/gorm"
)

func newTestOrder() models.AddressInfo {
	return models.AddressInfo{
		Model: models.Model{Model: gorm.Model{
			CreatedAt: time.Date(2026, 11, 7, 0, 0, 0, 0, time.Local),
		}},
		Recipient:    "测试店",
		Phone:        "13800138000",
		Address:      "季红市夏安区某路 1 号",
		Remarks:      "请送到门店",
		ProductSpecs: models.ProductSpecsJSON{{Name: "广香", Weight: 2}},
	}
}

func TestFilterNewMessages(t *testing.T) {
	msgs := []ChatMessage{
		{ID: "a", Ts: 100}, {ID: "b", Ts: 101},
		{ID: "c", Ts: 102}, {ID: "d", Ts: 102}, {ID: "e", Ts: 103},
	}
	state := AutoState{LastMessageTs: 102, BoundaryIDs: []string{"c"}}

	got := filterNewMessages(msgs, state, true)
	if len(got) != 2 || got[0].ID != "d" || got[1].ID != "e" {
		t.Fatalf("增量过滤结果不对: %+v", got)
	}
	if len(filterNewMessages(msgs, state, false)) != len(msgs) {
		t.Fatal("关闭增量模式时应返回全部消息")
	}
	if len(filterNewMessages(msgs, AutoState{}, true)) != len(msgs) {
		t.Fatal("首次运行（无水位线）应处理全部消息")
	}
}

func TestAdvanceWatermark(t *testing.T) {
	s := AutoState{LastMessageTs: 100, BoundaryIDs: []string{"a"}}
	msgs := []ChatMessage{{ID: "b", Ts: 101}, {ID: "c", Ts: 102}, {ID: "d", Ts: 102}}
	advanceWatermark(&s, msgs)
	if s.LastMessageTs != 102 {
		t.Fatalf("水位线应推进到 102，实际 %v", s.LastMessageTs)
	}
	if len(s.BoundaryIDs) != 2 {
		t.Fatalf("只应保留边界那一秒的 2 个 ID，实际 %v", s.BoundaryIDs)
	}
	// 再跑一次没有新消息，水位线与边界 ID 不应漂移
	before := append([]string(nil), s.BoundaryIDs...)
	advanceWatermark(&s, []ChatMessage{{ID: "c", Ts: 102}, {ID: "d", Ts: 102}})
	if s.LastMessageTs != 102 || len(s.BoundaryIDs) != len(before) {
		t.Fatalf("重复导出不应改变水位线: %+v", s)
	}
	// 状态文件不能无限膨胀：指纹超过上限时应裁剪
	s.ImportedHashes = make([]string, 5100)
	for i := range s.ImportedHashes {
		s.ImportedHashes[i] = "h"
	}
	advanceWatermark(&s, nil)
	if len(s.ImportedHashes) != 3000 {
		t.Fatalf("指纹集合应裁剪到 3000，实际 %d", len(s.ImportedHashes))
	}
}

func TestOrderHashStable(t *testing.T) {
	base := newTestOrder()
	a := orderHash(base)
	b := orderHash(base)
	if a != b {
		t.Fatal("同一订单内容指纹应稳定")
	}
	base.Remarks = "不要辣"
	if orderHash(base) == a {
		t.Fatal("备注变化后指纹应不同")
	}
	base = newTestOrder()
	base.Address = "季红市 夏安区 某路 1 号"
	if orderHash(base) != a {
		t.Fatal("地址里的空白字符不应影响指纹")
	}
	base = newTestOrder()
	base.Model.CreatedAt = base.Model.CreatedAt.AddDate(0, 0, 1)
	if orderHash(base) == a {
		t.Fatal("订单日期应参与指纹（同一内容隔天再发视为新订单）")
	}
}

func TestAutoStateRoundTrip(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	want := AutoState{
		LastMessageTs: 1730000000, BoundaryIDs: []string{"x", "y"},
		ImportedHashes: []string{"h1", "h2"}, LastRunAt: "2026-11-07T10:00:00+08:00",
		TotalRuns: 3, TotalOrders: 12, TotalImported: 10, NextRunAt: "2026-11-07T10:30:00+08:00",
	}
	if err := SaveAutoState(want); err != nil {
		t.Fatalf("保存状态失败: %v", err)
	}
	got := LoadAutoState()
	if got.LastMessageTs != want.LastMessageTs || got.TotalRuns != 3 ||
		got.TotalImported != 10 || len(got.ImportedHashes) != 2 || got.NextRunAt != want.NextRunAt {
		t.Fatalf("状态回读不一致: %+v", got)
	}
	info, err := os.Stat(StatePath())
	if err != nil {
		t.Fatalf("状态文件不存在: %v", err)
	}
	// Windows 不使用 Unix 权限位，仅在 Unix 系校验 0600
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatalf("状态文件权限应仅本人可读写，实际 %v", info.Mode().Perm())
	}
	if err := ResetAutoState(); err != nil {
		t.Fatalf("重置状态失败: %v", err)
	}
	if _, err := os.Stat(StatePath()); !os.IsNotExist(err) {
		t.Fatal("重置后状态文件应被删除")
	}
}

func TestAutoStatusShape(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	s := AutoStatus()
	for _, k := range []string{"enabled", "intervalMinutes", "incremental", "autoImport", "statePath", "totalRuns", "nextRunAt"} {
		if _, ok := s[k]; !ok {
			t.Fatalf("AutoStatus 缺少字段 %s", k)
		}
	}
	if s["autoImport"] != false {
		t.Fatal("识别流程不得自动入库，autoImport 必须恒为 false")
	}
	if s["enabled"] != false {
		t.Fatal("识别流程不得在后台定时运行，enabled 必须恒为 false")
	}
}

// 一键识别走 PreviewOnly：导出失败时也必须零副作用，
// 不能写自动状态文件、不能入库、不能生成 Excel、不能标记为后台自动触发。
func TestPreviewOnlyHasNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	cfg := DefaultConfig()
	cfg.DataDir = filepath.Join(dir, "wechat-data-not-exist")
	cfg.Version = 4
	cfg.AutoKey = false
	cfg.DataKey = ""
	cfg.KeyProviderPath = ""
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("准备测试配置失败: %v", err)
	}
	global.AddressList = nil

	res := Run(nil)
	if res.Success {
		t.Fatalf("数据目录不存在时导出应失败: %+v", res)
	}
	if _, err := os.Stat(StatePath()); !os.IsNotExist(err) {
		t.Fatal("纯识别模式不应创建 / 写入自动运行状态文件")
	}
	if res.Imported != 0 || res.ExportedFile != "" {
		t.Fatal("纯识别模式不应写数据库或生成 Excel")
	}
	if res.AutoMode || res.Trigger != "manual" {
		t.Fatalf("纯识别模式应始终标记为手动触发: %+v", res)
	}
	if len(global.AddressList) != 0 {
		t.Fatal("导出失败时不应改动数据预览区")
	}
}

// 旧配置文件里可能残留 autoRun / autoImport / incremental = true，
// normalize 必须一律纠正，确保「一键识别」永远只回填数据预览区。
func TestNormalizeEnforcesPreviewOnly(t *testing.T) {
	cfg := Config{
		Group: "测试群", AutoRun: true, RunOnStartup: true,
		Incremental: true, AutoImport: true, AutoExportExcel: true,
	}
	normalize(&cfg)
	if cfg.AutoRun || cfg.RunOnStartup || cfg.Incremental || cfg.AutoImport || cfg.AutoExportExcel {
		t.Fatalf("识别流程不应有后台运行 / 自动落库 / 自动导出副作用: %+v", cfg)
	}
	if cfg.Group != "测试群" || cfg.Days <= 0 {
		t.Fatalf("normalize 不应破坏其它配置项: %+v", cfg)
	}
}
