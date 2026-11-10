package core

import (
	"os"
	"path/filepath"
	"testing"

	"order_server/go/conf"
	"order_server/go/flags"
	"order_server/go/global"
	"order_server/go/models"

	"gopkg.in/yaml.v3"
)

func withTempConf(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	oldFile := flags.FlagOptions.File
	oldCfg := global.GetConfig()
	t.Cleanup(func() {
		flags.FlagOptions.File = oldFile
		global.SetConfig(oldCfg)
	})
	flags.FlagOptions.File = path
	global.SetConfig(&conf.Config{})
	return path
}

// ReadConf 仍兼容解析 auth 段（conf.Config 保留该字段以承接数据库值）。
func TestReadConfAuthSection(t *testing.T) {
	withTempConf(t, "log:\n    level: debug\nauth:\n    requireLogin: false\n")
	if ReadConf().Auth.LoginRequired() {
		t.Error("配置文件里 requireLogin: false 应解析为免登录")
	}

	// 老配置文件没有 auth 段：默认仍需要登录
	withTempConf(t, "log:\n    level: debug\n")
	if !ReadConf().Auth.LoginRequired() {
		t.Error("缺少 auth 段时应默认需要登录，保证升级后行为不变")
	}
}

// settings.yaml 只保留连库前必须的 log / db，业务配置不得再写回文件。
func TestSettingsYamlKeepsOnlyBootstrapConfig(t *testing.T) {
	data, err := os.ReadFile("../../settings.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"email", "savePath", "version", "aimodel", "auth"} {
		if _, ok := raw[key]; ok {
			t.Errorf("settings.yaml 不应再包含 %q，该配置应放在 cfg_settings / cfg_ai_models 表中", key)
		}
	}
	for _, key := range []string{"log", "db"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("settings.yaml 缺少引导配置 %q", key)
		}
	}
}

// cfg_settings 的行会被映射回 global.GetConfig()，业务代码因此无需改动。
func TestApplySettingsFromDB(t *testing.T) {
	cfg := &conf.Config{}
	rows := []models.CfgSetting{
		{Group: "auth", Key: "requireLogin", Value: "false", ValueType: "bool"},
		{Group: "email", Key: "domain", Value: "smtp.qq.com", ValueType: "string"},
		{Group: "email", Key: "port", Value: "587", ValueType: "int"},
		{Group: "email", Key: "SSL", Value: "true", ValueType: "bool"},
		{Group: "email", Key: "TLS", Value: "false", ValueType: "bool"},
		{Group: "savePath", Key: "excel", Value: "./uploads", ValueType: "string"},
		{Group: "version", Key: "repo", Value: "Aemiser/order_manager", ValueType: "string"},
		{Group: "version", Key: "mirrors", Value: `["https://mirror/{url}"]`, ValueType: "json"},
	}
	applySettings(cfg, rows)

	if cfg.Auth.LoginRequired() {
		t.Error("数据库 auth.requireLogin=false 未生效")
	}
	if cfg.Email.Domain != "smtp.qq.com" || cfg.Email.Port != 587 || !cfg.Email.SSL || cfg.Email.TLS {
		t.Errorf("email 配置映射不对: %+v", cfg.Email)
	}
	if cfg.SavePath.Excel != "./uploads" || cfg.Version.Repo != "Aemiser/order_manager" {
		t.Errorf("savePath / version 配置映射不对: %+v / %+v", cfg.SavePath, cfg.Version)
	}
	if len(cfg.Version.Mirrors) != 1 || cfg.Version.Mirrors[0] != "https://mirror/{url}" {
		t.Errorf("mirrors JSON 未解析: %+v", cfg.Version.Mirrors)
	}

	// 数据库里把开关改回 true，内存同样跟着变
	rows[0].Value = "true"
	applySettings(cfg, rows)
	if !cfg.Auth.LoginRequired() {
		t.Error("数据库 auth.requireLogin=true 未生效")
	}
}

// 数据库缺项时不能把已有配置清成零值。
func TestApplySettingsKeepsMissingKeys(t *testing.T) {
	cfg := &conf.Config{Email: conf.Email{Domain: "old.example.com"}}
	applySettings(cfg, []models.CfgSetting{
		{Group: "auth", Key: "requireLogin", Value: "true", ValueType: "bool"},
	})
	if cfg.Email.Domain != "old.example.com" {
		t.Error("数据库未提供的键不应覆盖已有值")
	}
	if !cfg.Auth.LoginRequired() {
		t.Error("数据库提供的键应生效")
	}
}
