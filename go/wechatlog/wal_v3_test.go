package wechatlog

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"order_server/go/wechatlog/internal/wechat/decrypt/common"

	"golang.org/x/crypto/pbkdf2"
)

// 用真实的 SQLCipher3 参数合成一帧加密 WAL 页，验证 WAL 合并逻辑。
func makeV3WALEncryptedPage(t *testing.T, raw, salt []byte, pgno uint32, marker string) []byte {
	t.Helper()
	encKey := pbkdf2.Key(raw, salt, 64000, 32, sha1.New)
	macKey := pbkdf2.Key(encKey, common.XorBytes(salt, 0x3a), 2, 32, sha1.New)

	page := make([]byte, 4096)
	for i := range page {
		page[i] = byte((i*17 + 3) % 251)
	}
	iv := page[4096-v3WALReserve : 4096-v3WALReserve+16]
	for i := range iv {
		iv[i] = byte(0xa0 + i)
	}
	plain := make([]byte, 4096-v3WALReserve)
	copy(plain, []byte(marker))
	block, err := aes.NewCipher(encKey)
	if err != nil {
		t.Fatal(err)
	}
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(page[:4096-v3WALReserve], plain)

	mac := hmac.New(sha1.New, macKey)
	_, _ = mac.Write(page[:4096-v3WALReserve+16])
	var pn [4]byte
	binary.LittleEndian.PutUint32(pn[:], pgno)
	_, _ = mac.Write(pn[:])
	copy(page[4096-v3WALReserve+16:4096-v3WALReserve+16+v3WALHmacSize], mac.Sum(nil))
	return page
}

func makeV3WALFrame(pgno, dbsize uint32, page []byte) []byte {
	frame := make([]byte, v3WALFrameHead+len(page))
	binary.BigEndian.PutUint32(frame[0:4], pgno)
	binary.BigEndian.PutUint32(frame[4:8], dbsize)
	copy(frame[v3WALFrameHead:], page)
	return frame
}

func TestMergeV3WALInto(t *testing.T) {
	dir := t.TempDir()
	raw := []byte("0123456789abcdef0123456789abcdef")
	salt := []byte("0123456789abcdef")

	// 加密主库：只需要第一页前 16 字节的 salt
	srcDB := filepath.Join(dir, "MSG0.db")
	srcPage := make([]byte, 4096)
	copy(srcPage, salt)
	if err := os.WriteFile(srcDB, srcPage, 0600); err != nil {
		t.Fatal(err)
	}
	// 明文输出库：4 页占位，第一页是合法 SQLite 头
	outDB := filepath.Join(dir, "decrypted.db")
	plainDB := make([]byte, 4096*4)
	copy(plainDB, []byte(common.SQLiteHeader))
	binary.BigEndian.PutUint16(plainDB[16:18], 4096) // SQLite 页大小字段（2 字节）
	if err := os.WriteFile(outDB, plainDB, 0600); err != nil {
		t.Fatal(err)
	}

	walHeader := make([]byte, v3WALHeader)
	binary.BigEndian.PutUint32(walHeader[0:4], v3WALMagic)
	binary.BigEndian.PutUint32(walHeader[8:12], 4096)
	wal := append([]byte{}, walHeader...)
	wal = append(wal, makeV3WALFrame(2, 0, makeV3WALEncryptedPage(t, raw, salt, 2, "PAGE-TWO-MARK"))...)
	wal = append(wal, makeV3WALFrame(4, 4, makeV3WALEncryptedPage(t, raw, salt, 4, "PAGE-FOUR-MARK"))...)
	walPath := filepath.Join(dir, "MSG0.db-wal")
	if err := os.WriteFile(walPath, wal, 0600); err != nil {
		t.Fatal(err)
	}

	n, err := mergeV3WALInto(srcDB, walPath, outDB, hex.EncodeToString(raw))
	if err != nil {
		t.Fatalf("WAL 合并失败: %v", err)
	}
	if n != 2 {
		t.Fatalf("应合并 2 帧，实际 %d", n)
	}
	merged, err := os.ReadFile(outDB)
	if err != nil {
		t.Fatal(err)
	}
	if string(merged[4096:4096+13]) != "PAGE-TWO-MARK" {
		t.Fatalf("第 2 页未写入明文: %q", merged[4096:4096+16])
	}
	if string(merged[3*4096:3*4096+14]) != "PAGE-FOUR-MARK" {
		t.Fatalf("第 4 页未写入明文: %q", merged[3*4096:3*4096+16])
	}
	if got := binary.BigEndian.Uint32(merged[28:32]); got != 4 {
		t.Fatalf("文件头数据库页数应更新为 4，实际 %d", got)
	}
	// 错误密钥不应误写数据
	bad := filepath.Join(dir, "bad.db")
	_ = os.WriteFile(bad, plainDB, 0600)
	if _, err := mergeV3WALInto(srcDB, walPath, bad, "ee"+hex.EncodeToString(raw)[2:]); err == nil {
		t.Fatal("错误密钥应合并失败")
	}
}

func TestMergeV3WALRemovesStaleWALFiles(t *testing.T) {
	dir := t.TempDir()
	outDB := filepath.Join(dir, "decrypted.db")
	_ = os.WriteFile(outDB, []byte("plain"), 0600)
	_ = os.WriteFile(outDB+"-wal", []byte("stale"), 0600)
	_ = os.WriteFile(outDB+"-shm", []byte("stale"), 0600)
	// 源 WAL 不存在时应静默跳过，且清掉旧 WAL/SHM
	mergeV3WALForDecryptedDB(filepath.Join(dir, "none.db"), outDB, "")
	if _, err := os.Stat(outDB + "-wal"); !os.IsNotExist(err) {
		t.Fatal("残留 -wal 未被清理")
	}
	if _, err := os.Stat(outDB + "-shm"); !os.IsNotExist(err) {
		t.Fatal("残留 -shm 未被清理")
	}
}
