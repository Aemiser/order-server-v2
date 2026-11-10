package config

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// 注册表是包级状态，每个用例独立隔离，避免互相污染节数与 SQL 断言。
func resetSections(t *testing.T) {
	t.Helper()
	sectionMu.Lock()
	oldList, oldIndex, oldAlias := sectionList, sectionIndex, aliasIndex
	sectionList = nil
	sectionIndex = map[string]int{}
	aliasIndex = map[string]int{}
	sectionMu.Unlock()
	t.Cleanup(func() {
		sectionMu.Lock()
		sectionList, sectionIndex, aliasIndex = oldList, oldIndex, oldAlias
		sectionMu.Unlock()
	})
}

func sampleSections() []Section {
	return []Section{
		{Name: "cfg_alpha", Alias: "alpha_md5", Columns: []string{"id", "k", "v"}},
		{Name: "cfg_beta", Alias: "beta_md5", Columns: []string{"id", "code", "enabled"}},
	}
}

func TestRegisterAndRevisionSQL(t *testing.T) {
	resetSections(t)
	for _, s := range sampleSections() {
		MustRegister(s)
	}
	if got := Names(); !reflect.DeepEqual(got, []string{"cfg_alpha", "cfg_beta"}) {
		t.Fatalf("注册顺序不符: %v", got)
	}

	sql := RevisionSQL()
	for _, needle := range []string{
		"stored_md5", "actual_md5", "cfg_revision",
		"cfg_alpha", "cfg_beta", "alpha_md5", "beta_md5",
		"CONCAT_WS('|', alpha_md5, beta_md5)",
		"MD5(CONCAT_WS(CHAR(31), id, k, v))",
		"MD5(CONCAT_WS(CHAR(31), id, code, enabled))",
		"deleted_at IS NULL", "GROUP_CONCAT(t.h ORDER BY t.id SEPARATOR '')",
	} {
		if !strings.Contains(sql, needle) {
			t.Errorf("生成的 SQL 缺少 %q\nSQL: %s", needle, sql)
		}
	}
	if s, ok := SectionByAlias("beta_md5"); !ok || s.Name != "cfg_beta" {
		t.Fatalf("别名反查失败: %+v / %v", s, ok)
	}
	if _, ok := SectionByAlias("not_exists"); ok {
		t.Fatal("不存在的别名不应命中")
	}

	// Sections 返回深拷贝，调用方改动不能污染注册表。
	ss := Sections()
	ss[0].Name = "hacked"
	ss[0].Columns[0] = "hacked"
	if got := Names(); got[0] != "cfg_alpha" {
		t.Fatalf("注册表被外部改动: %v", got)
	}
	if Sections()[0].Columns[0] != "id" {
		t.Fatal("列清单被外部改动")
	}
}

func TestRegisterRejectsInvalidSections(t *testing.T) {
	resetSections(t)
	MustRegister(sampleSections()[0])

	cases := []struct {
		name string
		s    Section
	}{
		{"重名", Section{Name: "cfg_alpha", Alias: "other_md5", Columns: []string{"id", "v"}}},
		{"别名冲突", Section{Name: "cfg_other", Alias: "alpha_md5", Columns: []string{"id", "v"}}},
		{"表名带注入", Section{Name: "cfg_a; DROP TABLE x", Alias: "a_md5", Columns: []string{"id", "v"}}},
		{"别名非法字符", Section{Name: "cfg_other", Alias: "a-b", Columns: []string{"id", "v"}}},
		{"列名带注入", Section{Name: "cfg_other", Alias: "a_md5", Columns: []string{"id", "v); DROP"}}},
		{"空列清单", Section{Name: "cfg_other", Alias: "a_md5"}},
		{"首列不是 id", Section{Name: "cfg_other", Alias: "a_md5", Columns: []string{"code", "v"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Register(c.s); err == nil {
				t.Fatal("应拒绝该配置节")
			}
		})
	}
	if len(Names()) != 1 {
		t.Fatalf("被拒绝的注册不应进入注册表: %v", Names())
	}
}

// 新增一节后，SQL 自动多一列、多一段子查询，且整体 MD5 把它算进去。
func TestRevisionSQLGrowsWithNewSection(t *testing.T) {
	resetSections(t)
	MustRegister(sampleSections()[0])
	before := RevisionSQL()
	if strings.Contains(before, "cfg_gamma") {
		t.Fatal("未注册的配置节不应出现在 SQL 里")
	}
	MustRegister(Section{Name: "cfg_gamma", Alias: "gamma_md5", Columns: []string{"id", "name"}})
	after := RevisionSQL()
	for _, needle := range []string{"cfg_gamma", "gamma_md5", "CONCAT_WS('|', alpha_md5, gamma_md5)"} {
		if !strings.Contains(after, needle) {
			t.Errorf("新增节未接入 SQL: 缺 %q\nSQL: %s", needle, after)
		}
	}
}

func TestRevisionSQLWithoutSections(t *testing.T) {
	resetSections(t)
	sql := RevisionSQL()
	if strings.Contains(sql, "CONCAT_WS('|', )") {
		t.Fatalf("空注册表不应拼出语法错误的 SQL: %s", sql)
	}
	if !strings.Contains(sql, "'' AS actual_md5") {
		t.Fatalf("空注册表应返回安全空值: %s", sql)
	}
}

func TestProviderPublish(t *testing.T) {
	p := NewMemory(nil)
	ctx := context.Background()
	fired := 0
	p.Watch(ctx, "cfg_alpha", func() { fired++ })
	p.Set("cfg_alpha", "old", "1")
	p.Publish("cfg_alpha", map[string]string{"new": "2", "__revision__": "r1"})
	if fired != 2 {
		t.Fatalf("Set + Publish 应触发 2 次回调，实际 %d", fired)
	}
	if v, err := p.Get(ctx, "cfg_alpha", "new"); err != nil || v != "2" {
		t.Fatalf("发布的新键读不到: %q / %v", v, err)
	}
	if _, err := p.Get(ctx, "cfg_alpha", "old"); err == nil {
		t.Fatal("Publish 应整体替换命名空间，旧键必须消失")
	}
	p.Set("cfg_beta", "x", "y")
	if fired != 2 {
		t.Fatal("其它命名空间写入不应触发本命名空间回调")
	}

	// 单个监听者 panic 不能影响配置装载流程。
	p.Watch(ctx, "cfg_alpha", func() { panic("boom") })
	p.Publish("cfg_alpha", map[string]string{"new": "3"})
	if v, err := p.Get(ctx, "cfg_alpha", "new"); err != nil || v != "3" {
		t.Fatalf("panic 监听者不应阻断发布: %q / %v", v, err)
	}
}
