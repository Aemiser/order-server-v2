package wechatlog

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/pbkdf2"
)

// makeFakeEncryptedDB 造一个「非 SQLite 明文头」的 4096 字节假数据库，
// 用于验证目录识别与锚点库选择，不需要真实微信数据。
func makeFakeEncryptedDB(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	for i := range buf {
		buf[i] = byte((i*37 + 11) % 251)
	}
	if err := os.WriteFile(path, buf, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDBLayoutVersion(t *testing.T) {
	v3 := t.TempDir()
	makeFakeEncryptedDB(t, filepath.Join(v3, "Msg", "Misc.db"))
	makeFakeEncryptedDB(t, filepath.Join(v3, "Msg", "Multi", "MSG0.db"))
	if got := dbLayoutVersion(v3); got != 3 {
		t.Fatalf("3.9 目录应识别为 3，实际 %d", got)
	}
	// 用户直接把数据目录填成 Msg 也要认出来
	if got := dbLayoutVersion(filepath.Join(v3, "Msg")); got != 3 {
		t.Fatalf("直接填 Msg 目录应识别为 3，实际 %d", got)
	}

	v4 := t.TempDir()
	makeFakeEncryptedDB(t, filepath.Join(v4, "db_storage", "message", "message_0.db"))
	if got := dbLayoutVersion(v4); got != 4 {
		t.Fatalf("4.x 目录应识别为 4，实际 %d", got)
	}
	if got := dbLayoutVersion(filepath.Join(v4, "db_storage")); got != 4 {
		t.Fatalf("直接填 db_storage 目录应识别为 4，实际 %d", got)
	}

	if got := dbLayoutVersion(t.TempDir()); got != 0 {
		t.Fatalf("空目录应识别为 0，实际 %d", got)
	}
}

func TestResolveWechatVersionCorrectsStaleConfig(t *testing.T) {
	v3 := t.TempDir()
	makeFakeEncryptedDB(t, filepath.Join(v3, "Msg", "Misc.db"))
	// 用户从 4.x 换回 3.9，配置里残留 version=4
	v, note := resolveWechatVersion("windows", v3, 4)
	if v != 3 || note == "" {
		t.Fatalf("应校正为 3 并给出提示，得到 version=%d note=%q", v, note)
	}
	// version=0（跟随客户端）时自动取目录布局
	if v, _ := resolveWechatVersion("windows", v3, 0); v != 3 {
		t.Fatalf("version=0 应自动识别为 3，实际 %d", v)
	}
	// 版本一致时不产生提示
	if v, note := resolveWechatVersion("windows", v3, 3); v != 3 || note != "" {
		t.Fatalf("版本一致时不应提示，得到 version=%d note=%q", v, note)
	}
}

func TestAnchorDBPathForVersion(t *testing.T) {
	v3 := t.TempDir()
	misc := filepath.Join(v3, "Msg", "Misc.db")
	makeFakeEncryptedDB(t, misc)
	if got := anchorDBPathForVersion(v3, 3); got != misc {
		t.Fatalf("3.9 锚点应为 Misc.db，实际 %q", got)
	}
	if got := anchorDBPath(v3); got != misc {
		t.Fatalf("自动锚点应为 Misc.db，实际 %q", got)
	}
	v4 := t.TempDir()
	msg := filepath.Join(v4, "db_storage", "message", "message_0.db")
	makeFakeEncryptedDB(t, msg)
	if got := anchorDBPathForVersion(v4, 4); got != msg {
		t.Fatalf("4.x 锚点应为 message_0.db，实际 %q", got)
	}
}

// makeSQLCipher3Page 合成一页合法的 SQLCipher3 加密数据（只保证 HMAC 可校验）。
// 参数：PBKDF2-SHA1 64000、HMAC-SHA1、reserve = IV16+HMAC20 对齐到 48。
func makeSQLCipher3Page(t *testing.T, rawKey []byte) []byte {
	t.Helper()
	page := make([]byte, 4096)
	for i := range page {
		page[i] = byte((i*29 + 7) % 251)
	}
	salt := page[:16]
	encKey := pbkdf2.Key(rawKey, salt, 64000, 32, sha1.New)
	macSalt := make([]byte, 16)
	for i, b := range salt {
		macSalt[i] = b ^ 0x3a
	}
	macKey := pbkdf2.Key(encKey, macSalt, 2, 32, sha1.New)
	const reserve = 48
	dataEnd := 4096 - reserve + 16
	mac := hmac.New(sha1.New, macKey)
	_, _ = mac.Write(page[16:dataEnd])
	var pageNo [4]byte
	binary.LittleEndian.PutUint32(pageNo[:], 1)
	_, _ = mac.Write(pageNo[:])
	copy(page[dataEnd:dataEnd+20], mac.Sum(nil))
	return page
}

func TestValidateKeyCandidateVersionV3(t *testing.T) {
	rawKey := []byte(strings.Repeat("k", 32))
	keyHex := hex.EncodeToString(rawKey)
	path := filepath.Join(t.TempDir(), "Misc.db")
	if err := os.WriteFile(path, makeSQLCipher3Page(t, rawKey), 0600); err != nil {
		t.Fatal(err)
	}
	if !ValidateKeyCandidateVersion(path, keyHex, 3) {
		t.Fatal("3.9 的合法原始密钥应通过 SQLCipher3 验签")
	}
	if ValidateKeyCandidateVersion(path, keyHex, 4) {
		t.Fatal("3.9 密钥不应被 4.x 参数误判为有效")
	}
	if !ValidateKeyCandidate(path, keyHex) {
		t.Fatal("自动模式应兼容 3.9 密钥")
	}
	if ValidateKeyCandidate(path, strings.Repeat("0", 64)) {
		t.Fatal("错误密钥不应通过校验")
	}
}
