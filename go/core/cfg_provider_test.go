package core

import (
	"context"
	"testing"

	"order_server/go/models"
)

func projectionFixture() cfgRows {
	return cfgRows{
		Settings: []models.CfgSetting{
			{Group: "version", Key: "mirrors", Value: "https://example.invalid", Enabled: true},
			{Group: "hidden", Key: "k", Value: "v", Enabled: false},
		},
		AIModels: []models.CfgAIModel{
			{Code: "deepseek", ApiKey: "must-not-leak", Enabled: true},
		},
		Belongs:     []models.CfgProductBelong{{Category: "水果", Enabled: true}},
		Prompts:     []models.CfgPrompt{{Code: "order", Content: "提示词正文", Enabled: true}},
		ActionRules: []models.CfgOrderActionRule{{Keyword: "不要了", Enabled: true}},
	}
}

func TestCfgProviderProjection(t *testing.T) {
	proj := cfgProjection(projectionFixture(), "rev-1")

	// 每个已注册配置节都有命名空间，Watch 任意一节都能收到通知。
	for _, name := range cfgSections {
		if _, ok := proj[name]; !ok {
			t.Errorf("配置节 %s 没有投影命名空间", name)
		}
	}

	kv := proj["cfg_settings"]
	if kv["version.mirrors"] != "https://example.invalid" {
		t.Fatalf("通用设置键值未投影: %v", kv)
	}
	if _, ok := kv["hidden.k"]; ok {
		t.Fatal("禁用项不应投影")
	}
	if kv["__count__"] != "1" || kv["__revision__"] != "rev-1" {
		t.Fatalf("元信息不正确: %v", kv)
	}

	// AI 密钥、提示词正文等只允许以计数形式出现，不能成为第二份敏感副本。
	for _, name := range []string{"cfg_ai_models", "cfg_prompts", "cfg_product_belongs", "cfg_order_action_rules"} {
		if len(proj[name]) != 2 {
			t.Errorf("配置节 %s 不应投影明细: %v", name, proj[name])
		}
		for k := range proj[name] {
			if k != "__count__" && k != "__revision__" {
				t.Errorf("配置节 %s 出现非白名单键 %q", name, k)
			}
		}
	}
}

func TestCfgProviderPublishesOnProjection(t *testing.T) {
	ctx := context.Background()
	p := CfgProvider()
	fired := 0
	p.Watch(ctx, "cfg_settings", func() { fired++ })

	rows := projectionFixture()
	publishCfgProjection(rows, "rev-2")
	if fired == 0 {
		t.Fatal("快照投影后应触发 Watch 回调")
	}
	if v, err := p.Get(ctx, "cfg_settings", "version.mirrors"); err != nil || v != "https://example.invalid" {
		t.Fatalf("ConfigProvider 读不到主程序配置: %q / %v", v, err)
	}
	if v, err := p.Get(ctx, "cfg_settings", "__revision__"); err != nil || v != "rev-2" {
		t.Fatalf("revision 不一致: %q / %v", v, err)
	}

	// 再发布一轮：旧键消失，revision 更新，验证「同一轮一致快照」语义。
	rows.Settings = []models.CfgSetting{{Group: "version", Key: "channel", Value: "stable", Enabled: true}}
	publishCfgProjection(rows, "rev-3")
	if _, err := p.Get(ctx, "cfg_settings", "version.mirrors"); err == nil {
		t.Fatal("第二轮发布后旧键应被清掉")
	}
	if v, err := p.Get(ctx, "cfg_settings", "version.channel"); err != nil || v != "stable" {
		t.Fatalf("第二轮发布的新键读不到: %q / %v", v, err)
	}
}
