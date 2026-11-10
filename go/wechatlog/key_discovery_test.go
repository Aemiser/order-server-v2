package wechatlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseKeyCandidates(t *testing.T) {
	h := strings.Repeat("ab", 32) // 64 位 hex = 32 字节密钥
	perDB := `{"db_storage/message/message_0.db":{"enc_key":"` + h + `","salt":"0102030405060708090a0b0c0d0e0f10"}}`

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"纯 hex", h, h},
		{"x'<hex>' 日志片段", "2026-11-07 INFO db key x'" + h + "' found", h},
		{"key 字段 JSON", `{"key":"` + h + `"}`, h},
		{"dataKey 字段 JSON", `{"dataKey":"` + h + `"}`, h},
		{"按库密钥 JSON", perDB, perDB},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseKeyCandidates([]byte(c.raw))
			found := false
			for _, g := range got {
				if g == c.want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("未解析出期望候选 %q，实际 %v", c.want[:min(len(c.want), 24)], got)
			}
		})
	}
	if got := parseKeyCandidates([]byte("今天天气不错，没有密钥")); len(got) != 0 {
		t.Fatalf("无关文本不应产生密钥候选，实际 %v", got)
	}
	if got := parseKeyCandidates([]byte("")); len(got) != 0 {
		t.Fatalf("空文件不应产生密钥候选，实际 %v", got)
	}
}

func TestKeyDropDirDiscoversFiles(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	dir := KeyDropDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "wx_key.json")
	if err := os.WriteFile(want, []byte(`{"key":"`+strings.Repeat("cd", 32)+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	// 非密钥后缀应被忽略
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("ignore me"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := keyDiscoveryPaths()
	found := false
	for _, p := range paths {
		if filepath.Clean(p) == filepath.Clean(want) {
			found = true
		}
		if strings.HasSuffix(p, ".md") {
			t.Fatal("不应把 .md 文件当作密钥文件")
		}
	}
	if !found {
		t.Fatalf("自动密钥目录里的文件未被发现: %v", paths)
	}

	status := KeyDropStatus()
	if status["dir"] != dir {
		t.Fatalf("KeyDropStatus 目录不对: %v", status["dir"])
	}
	files, ok := status["files"].([]string)
	if !ok || len(files) != 1 || files[0] != "wx_key.json" {
		t.Fatalf("KeyDropStatus 文件列表不对: %v", status["files"])
	}
}

func TestValidateKeyCandidateRejectsInvalid(t *testing.T) {
	if ValidateKeyCandidate("", strings.Repeat("ab", 32)) {
		t.Fatal("不存在的数据库路径应校验失败而不是 panic")
	}
	tmp := t.TempDir()
	plain := filepath.Join(tmp, "plain.db")
	if err := os.WriteFile(plain, []byte("SQLite format 3\u0000"+strings.Repeat("x", 4080)), 0600); err != nil {
		t.Fatal(err)
	}
	if ValidateKeyCandidate(plain, strings.Repeat("ab", 32)) {
		t.Fatal("已是明文 SQLite 不应再当作加密库校验")
	}
	if ValidateKeyCandidate(plain, "not-a-hex-key") {
		t.Fatal("非法 hex 应校验失败")
	}
	enc := filepath.Join(tmp, "enc.db")
	if err := os.WriteFile(enc, []byte(strings.Repeat("z", 4096)), 0600); err != nil {
		t.Fatal(err)
	}
	if ValidateKeyCandidate(enc, strings.Repeat("ab", 32)) {
		t.Fatal("随机数据不应通过 HMAC 校验")
	}
}

func TestDiscoverKeyFromFilesWithoutAnchor(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	if _, _, ok := DiscoverKeyFromFiles("", nil); ok {
		t.Fatal("空数据目录不应发现密钥")
	}
	if _, _, ok := DiscoverKeyFromFiles(filepath.Join(t.TempDir(), "nonexistent"), nil); ok {
		t.Fatal("不存在的数据库目录不应发现密钥")
	}
}
