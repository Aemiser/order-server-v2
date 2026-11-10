//go:build cfgintegration

// 需要真实数据库的集成测试，默认不参与 go test ./...。
// 手动运行：go test -tags cfgintegration ./go/core -run "TestCfg" -v
package core

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"order_server/go/flags"
	"order_server/go/global"
	"order_server/go/models"

	"gorm.io/gorm"
)

func TestCfgRuntimeWrites(t *testing.T) {
	flags.FlagOptions.File = "../../settings.yaml"
	global.SetConfig(ReadConf())
	global.Db = InitDB()
	if err := LoadDBCfg(); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	// ---- 登录开关：必须写到 cfg_settings，且内存立即同步 ----
	origLogin := global.GetConfig().Auth.LoginRequired()
	origBelongs := global.GetProductBelongs()
	t.Cleanup(func() {
		if err := SetAuthRequireLogin(origLogin); err != nil {
			t.Errorf("恢复登录开关失败: %v", err)
		}
		if err := WriteProductBelongMap(origBelongs); err != nil {
			t.Errorf("恢复商品归属失败: %v", err)
		}
	})

	wantLogin := !origLogin
	if err := SetAuthRequireLogin(wantLogin); err != nil {
		t.Fatalf("写登录开关失败: %v", err)
	}
	var row models.CfgSetting
	if err := global.Db.Where("cfg_group = ? AND cfg_key = ?", "auth", "requireLogin").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Value != strconv.FormatBool(wantLogin) {
		t.Errorf("数据库里 auth.requireLogin=%q，期望 %q", row.Value, strconv.FormatBool(wantLogin))
	}
	if global.GetConfig().Auth.LoginRequired() != wantLogin {
		t.Error("内存配置未同步")
	}

	// ---- 商品归属：全量写回 cfg_product_belongs ----
	testMap := map[string][]string{
		"集成测试分类":  {"别名A", "别名B"},
		"集成测试空分类": {},
	}
	if err := WriteProductBelongMap(testMap); err != nil {
		t.Fatalf("写商品归属失败: %v", err)
	}
	got := ReadProductBelongMap()
	if len(got) != 2 {
		t.Fatalf("商品分类数量不对: %d -> %+v", len(got), got)
	}
	if len(got["集成测试分类"]) != 2 || got["集成测试分类"][0] != "别名A" {
		t.Errorf("别名未正确写回: %+v", got)
	}
	if v, ok := got["集成测试空分类"]; !ok || len(v) != 0 {
		t.Errorf("空分类应保留空数组: %+v", got)
	}

	// ---- 提示词：确认运行时取到的是配置表内容 ----
	if global.Prompt(global.PromptOrderExtract) == "" {
		t.Error("cfg_prompts.order_extract 未装载")
	}
	t.Logf("运行时配置就绪：登录开关=%v，商品分类=%d，提示词=%d 条",
		global.GetConfig().Auth.LoginRequired(), len(got), len(global.GetPrompts()))
}

// TestCfgRevisionFastPath 验证配置 MD5 快比对：
// 1) 配置没变时零全量拉取；2) 外部直接用 SQL 改 cfg_* 也能检出；
// 3) cfg_revision 版本行会被自动修正；4) 清理数据后 MD5 回到原值。
func TestCfgRevisionFastPath(t *testing.T) {
	flags.FlagOptions.File = "../../settings.yaml"
	global.SetConfig(ReadConf())
	global.Db = InitDB()

	// 清掉上一次异常退出可能残留的探针行，再装载基线。
	const probeGroup = "__cfg_md5_test__"
	global.Db.Unscoped().Where("cfg_group = ?", probeGroup).Delete(&models.CfgSetting{})
	t.Cleanup(func() {
		global.Db.Unscoped().Where("cfg_group = ?", probeGroup).Delete(&models.CfgSetting{})
	})
	if err := LoadDBCfg(); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	base, err := readCfgRevisionPair(false)
	if err != nil {
		t.Fatalf("读取配置 MD5 失败: %v", err)
	}
	if len(base.Actual) != 32 {
		t.Fatalf("数据库实算 MD5 格式不对: %q", base.Actual)
	}
	if base.Stored != base.Actual {
		t.Fatalf("启动时 cfg_revision 未对齐: stored=%q actual=%q", base.Stored, base.Actual)
	}

	st0 := GetCfgWatchStatus()
	ch, err := ReloadDBCfg(false, "test")
	if err != nil {
		t.Fatal(err)
	}
	if ch.Changed {
		t.Error("配置没变，不应报变更")
	}
	st1 := GetCfgWatchStatus()
	if st1.FastSkips != st0.FastSkips+1 || st1.FullLoads != st0.FullLoads {
		t.Errorf("无变化轮次应走 MD5 快比对：skips %d->%d, fullLoads %d->%d",
			st0.FastSkips, st1.FastSkips, st0.FullLoads, st1.FullLoads)
	}

	// 模拟外部工具直接 INSERT，完全不碰 cfg_revision
	probe := models.CfgSetting{
		Group: probeGroup, Key: "probe", Value: "1", ValueType: "string", Enabled: true,
	}
	if err := global.Db.Create(&probe).Error; err != nil {
		t.Fatal(err)
	}
	ch2, err := ReloadDBCfg(false, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !ch2.Changed {
		t.Error("外部直接改库未被检出")
	}
	pair2, err := readCfgRevisionPair(false)
	if err != nil {
		t.Fatal(err)
	}
	if pair2.Actual == base.Actual {
		t.Error("配置变了但实算 MD5 没变")
	}
	if pair2.Stored != pair2.Actual {
		t.Errorf("版本行未自动修正: stored=%q actual=%q", pair2.Stored, pair2.Actual)
	}

	// 清理探针后，MD5 应回到原值
	if err := global.Db.Unscoped().Where("id = ?", probe.ID).Delete(&models.CfgSetting{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ReloadDBCfg(false, "test"); err != nil {
		t.Fatal(err)
	}
	pair3, err := readCfgRevisionPair(false)
	if err != nil {
		t.Fatal(err)
	}
	if pair3.Actual != base.Actual {
		t.Errorf("清理后 MD5 未恢复: %q -> %q", base.Actual, pair3.Actual)
	}
	t.Logf("MD5 快比对通过：基线=%s，变更=%s，恢复=%s", base.Actual, pair2.Actual, pair3.Actual)
}

// TestCfgRevisionPerTable 验证「逐表 MD5 + 只拉变化的表」：
// 1) 配置没变时一张 cfg_* 表都不读；2) 外部只改 cfg_prompts 时只查 cfg_prompts，
// 其余三张表复用上次已加载的行；3) 恢复原文后整体 MD5 回到基线。
// 用 GORM 查询回调统计真正执行过的 SELECT，验证的是 SQL 而不是自报状态。
func TestCfgRevisionPerTable(t *testing.T) {
	flags.FlagOptions.File = "../../settings.yaml"
	global.SetConfig(ReadConf())
	global.Db = InitDB()

	var prompt models.CfgPrompt
	if err := global.Db.Where("code = ?", "wechat_order_base").First(&prompt).Error; err != nil {
		t.Fatalf("找不到用于测试的提示词: %v", err)
	}
	orig := prompt.Content
	t.Cleanup(func() {
		if err := global.Db.Model(&models.CfgPrompt{}).Where("id = ?", prompt.ID).
			Update("content", orig).Error; err != nil {
			t.Errorf("恢复提示词失败: %v", err)
		}
		if _, err := SyncCfgRevision(); err != nil {
			t.Errorf("恢复版本行失败: %v", err)
		}
	})
	if err := LoadDBCfg(); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	basePair, err := readCfgRevisionPair(false)
	if err != nil {
		t.Fatalf("读取基线 MD5 失败: %v", err)
	}

	var queried []string
	const cbName = "test:count_cfg_tables"
	if err := global.Db.Callback().Query().After("gorm:query").Register(cbName, func(tx *gorm.DB) {
		sql := tx.Statement.SQL.String()
		for _, s := range cfgSections {
			if strings.Contains(sql, "`"+s+"`") {
				queried = append(queried, s)
			}
		}
	}); err != nil {
		t.Fatalf("注册查询回调失败: %v", err)
	}
	t.Cleanup(func() { global.Db.Callback().Query().Remove(cbName) })

	// 1) 无变化：零配置表查询
	queried = nil
	ch, err := ReloadDBCfg(false, "test")
	if err != nil {
		t.Fatal(err)
	}
	if ch.Changed {
		t.Error("配置没变，不应报变更")
	}
	if len(queried) != 0 {
		t.Errorf("无变化轮次不应读任何配置表，实际读了 %v", queried)
	}

	// 2) 外部直接改 cfg_prompts，完全不碰 cfg_revision
	stBefore := GetCfgWatchStatus()
	if err := global.Db.Model(&models.CfgPrompt{}).Where("id = ?", prompt.ID).
		Update("content", orig+" ").Error; err != nil {
		t.Fatal(err)
	}
	queried = nil
	ch2, err := ReloadDBCfg(false, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !ch2.Changed {
		t.Error("外部直接改库未被检出")
	}
	if !reflect.DeepEqual(ch2.Sections, []string{"cfg_prompts"}) {
		t.Errorf("变更表应只有 cfg_prompts，实际 %v", ch2.Sections)
	}
	if !reflect.DeepEqual(queried, []string{"cfg_prompts"}) {
		t.Errorf("应只查询 cfg_prompts 一张表，实际查询了 %v", queried)
	}
	stAfter := GetCfgWatchStatus()
	if stAfter.PartialLoads != stBefore.PartialLoads+1 {
		t.Errorf("本轮应记为增量拉取：%d -> %d", stBefore.PartialLoads, stAfter.PartialLoads)
	}
	if stAfter.LastSections != "cfg_prompts" {
		t.Errorf("本轮读取的表记录不对: %q", stAfter.LastSections)
	}
	// 未变化的三张表必须仍在内存里（复用旧行，没被清空）
	if len(global.GetProductBelongs()) == 0 || len(global.GetPrompts()) == 0 || len(global.GetSettings()) == 0 {
		t.Error("增量拉取把未变化表的内存快照清空了")
	}

	// 3) 恢复原文：整体 MD5 回到原值，且仍只读一张表
	if err := global.Db.Model(&models.CfgPrompt{}).Where("id = ?", prompt.ID).
		Update("content", orig).Error; err != nil {
		t.Fatal(err)
	}
	queried = nil
	if _, err := ReloadDBCfg(false, "test"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(queried, []string{"cfg_prompts"}) {
		t.Errorf("恢复轮次应只查询 cfg_prompts，实际 %v", queried)
	}
	pair3, err := readCfgRevisionPair(false)
	if err != nil {
		t.Fatal(err)
	}
	if pair3.Actual != basePair.Actual {
		t.Errorf("恢复后整体 MD5 未回到基线: %s -> %s", basePair.Actual, pair3.Actual)
	}
	t.Logf("逐表增量通过：无变化 0 表查询，单表变更只读 %v，MD5 %s 复原",
		queried, pair3.Actual)
}
