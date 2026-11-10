package core

import (
	"reflect"
	"strings"
	"testing"

	"order_server/go/config"
)

// 改造前写死在 cfg_revision.go 的 SQL，留作回归基准。
// Phase 3 改成按注册表动态拼 SQL，逐字节不许变（空白归一化后比较），
// 否则逐表 MD5 基线会全部失效一轮，甚至静默漏检。
const legacyRevisionPairSQL = `SELECT
 stored_md5,
 MD5(CONCAT_WS('|', settings_md5, aimodels_md5, belongs_md5, prompts_md5, actionrules_md5)) AS actual_md5,
 settings_md5, aimodels_md5, belongs_md5, prompts_md5, actionrules_md5
 FROM (SELECT
 IFNULL((SELECT cfg_md5 FROM cfg_revision WHERE id = 1), '') AS stored_md5,
 MD5((SELECT IFNULL(GROUP_CONCAT(t.h ORDER BY t.id SEPARATOR ''), '') FROM
   (SELECT id, MD5(CONCAT_WS(CHAR(31), id, cfg_group, cfg_key, cfg_value, value_type, remark, enabled)) AS h
    FROM cfg_settings WHERE deleted_at IS NULL) t)) AS settings_md5,
 MD5((SELECT IFNULL(GROUP_CONCAT(t.h ORDER BY t.id SEPARATOR ''), '') FROM
   (SELECT id, MD5(CONCAT_WS(CHAR(31), id, code, label, name, model_type, api_key, api_url, model_name, timeout, enabled, sort_order, remark)) AS h
    FROM cfg_ai_models WHERE deleted_at IS NULL) t)) AS aimodels_md5,
 MD5((SELECT IFNULL(GROUP_CONCAT(t.h ORDER BY t.id SEPARATOR ''), '') FROM
   (SELECT id, MD5(CONCAT_WS(CHAR(31), id, category, aliases, sort_order, enabled, remark)) AS h
    FROM cfg_product_belongs WHERE deleted_at IS NULL) t)) AS belongs_md5,
 MD5((SELECT IFNULL(GROUP_CONCAT(t.h ORDER BY t.id SEPARATOR ''), '') FROM
   (SELECT id, MD5(CONCAT_WS(CHAR(31), id, code, name, scene, content, is_template, enabled, sort_order, remark)) AS h
    FROM cfg_prompts WHERE deleted_at IS NULL) t)) AS prompts_md5,
 MD5((SELECT IFNULL(GROUP_CONCAT(t.h ORDER BY t.id SEPARATOR ''), '') FROM
   (SELECT id, MD5(CONCAT_WS(CHAR(31), id, keyword, action_type, field_name, value_rule, scope, priority, enabled, sort_order, remark)) AS h
    FROM cfg_order_action_rules WHERE deleted_at IS NULL) t)) AS actionrules_md5) x`

func normalizeSQL(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// 动态拼出的 SQL 必须与改造前逐字等价（忽略空白）：算法没变、列顺序没变、
// 别名没变，生产库里已有的 cfg_revision 基线与逐表 MD5 才不会失效。
func TestRevisionSQLMatchesLegacy(t *testing.T) {
	got := normalizeSQL(config.RevisionSQL())
	want := normalizeSQL(legacyRevisionPairSQL)
	if got != want {
		t.Fatalf("配置 MD5 SQL 与改造前不一致\n期望: %s\n实际: %s", want, got)
	}
}

// 逐表 MD5 的判定逻辑不依赖数据库：改了哪张表，就只应该返回哪张表。
func TestChangedSections(t *testing.T) {
	full := cfgRevisionPair{
		Actual: "overall-1",
		Sections: map[string]string{
			"cfg_settings":           "s1",
			"cfg_ai_models":          "a1",
			"cfg_product_belongs":    "b1",
			"cfg_prompts":            "p1",
			"cfg_order_action_rules": "r1",
		},
	}
	base := full.sections()
	with := func(name, value string) cfgRevisionPair {
		p := cfgRevisionPair{Stored: full.Stored, Actual: full.Actual, Sections: map[string]string{}}
		for k, v := range full.Sections {
			p.Sections[k] = v
		}
		p.Sections[name] = value
		return p
	}
	two := with("cfg_product_belongs", "b2")
	two.Sections["cfg_ai_models"] = "a2"

	cases := []struct {
		name string
		pair cfgRevisionPair
		base map[string]string
		want []string
	}{
		{"五表全一致", full, base, []string{}},
		{"只改提示词", with("cfg_prompts", "p2"), base, []string{"cfg_prompts"}},
		{"只改订单操作术语", with("cfg_order_action_rules", "r2"), base,
			[]string{"cfg_order_action_rules"}},
		{"改商品归属和模型", two, base,
			[]string{"cfg_ai_models", "cfg_product_belongs"}},
		{"基线为空必须全量", full, nil, cfgSections},
		{"基线缺四张表", full, map[string]string{"cfg_prompts": "p1"},
			[]string{"cfg_settings", "cfg_ai_models", "cfg_product_belongs", "cfg_order_action_rules"}},
		{"实算值不可用必须全量", cfgRevisionPair{}, base, cfgSections},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.pair.changedSections(c.base)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("期望 %v，实际 %v", c.want, got)
			}
		})
	}
}

func TestCfgSectionsCoverAllTables(t *testing.T) {
	if len(cfgSections) != 5 {
		t.Fatalf("配置表清单应该有 5 张，实际 %v", cfgSections)
	}
	p := cfgRevisionPair{
		Actual: "x",
		Sections: map[string]string{
			"cfg_settings": "s", "cfg_ai_models": "a", "cfg_product_belongs": "b",
			"cfg_prompts": "p", "cfg_order_action_rules": "r",
		},
	}
	for _, s := range cfgSections {
		if p.sections()[s] == "" {
			t.Errorf("表 %s 没有对应的 MD5 列", s)
		}
	}
}

// 注册表驱动的清单必须与内置节一一对应，且顺序稳定（SQL 列顺序 / 基线顺序）。
func TestCfgSectionsRegistered(t *testing.T) {
	want := []string{
		"cfg_settings", "cfg_ai_models", "cfg_product_belongs",
		"cfg_prompts", "cfg_order_action_rules",
	}
	if !reflect.DeepEqual(cfgSections, want) {
		t.Fatalf("内置配置节顺序不符：%v", cfgSections)
	}
	for _, name := range want {
		if _, ok := cfgLoaders[name]; !ok {
			t.Errorf("配置节 %s 缺少行加载器", name)
		}
	}
}
