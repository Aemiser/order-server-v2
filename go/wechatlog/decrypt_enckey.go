package wechatlog

// 微信 4.x 的内存里缓存的是「已经过 PBKDF2 派生的 32 字节 enc_key」，
// 而不是原始密码。db_key_scanner / binary_key_scanner 输出的按库 JSON 里
// 的 enc_key 就是它；V4Decryptor.Decrypt 却会把传入的 key 再派生一次，
// 因此按库密钥 JSON 走原链路一定解密失败。
//
// 这里补一条「直接用 enc_key 解密」的路径，并在失败时自动回退到
// 「把 key 当原始密码重新派生」，从而同时兼容：
//   - 本项目内存扫描 / wx_key / EchoTrace 导出的 enc_key
//   - 用户手动粘贴的 64 位原始密钥 hex
//   - 微信 3.x 与 macOS 平台（继续走原解密器）

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"order_server/go/wechatlog/internal/wechat/decrypt"
	"order_server/go/wechatlog/internal/wechat/decrypt/common"

	"golang.org/x/crypto/pbkdf2"
)

const (
	v4PageSize  = 4096
	v4IVSize    = 16
	v4HmacSize  = 64
	v4Reserve   = v4IVSize + v4HmacSize // 80
	v4MacIters  = 2
	v4KeySize   = 32
	v4SaltSize  = 16
)

// ValidateKeyCandidate 校验一个 64 位 hex 密钥能否解开指定数据库第一页。
// 自动兼容 3.9（SQLCipher3 原始密钥）与 4.x（已派生 enc_key / 原始密钥）。
func ValidateKeyCandidate(dbPath, keyHex string) bool {
	return ValidateKeyCandidateVersion(dbPath, keyHex, 0)
}

// ValidateKeyCandidateVersion 按指定微信大版本校验密钥；version=0 时自动依次尝试 4.x、3.9。
// 3.9 用的是 PBKDF2-SHA1 64000 + HMAC-SHA1，与 4.x 的 SHA512/256000 完全不同，
// 旧实现只按 4.x 参数验签，导致 3.9 的合法密钥永远被判为无效。
func ValidateKeyCandidateVersion(dbPath, keyHex string, version int) bool {
	key, err := hex.DecodeString(strings.TrimSpace(keyHex))
	if err != nil || len(key) != v4KeySize {
		return false
	}
	f, err := os.Open(dbPath)
	if err != nil {
		return false
	}
	defer f.Close()
	page1 := make([]byte, v4PageSize)
	if _, err := io.ReadFull(f, page1); err != nil {
		return false
	}
	if bytes.HasPrefix(page1, []byte("SQLite format 3")) {
		return false
	}
	if version == 4 || version == 0 {
		if validateV4EncKeyPage(page1, key) {
			return true
		}
		if dec4, e := decrypt.NewDecryptor("windows", 4); e == nil && dec4.Validate(page1, key) {
			return true
		}
	}
	if version == 3 || version == 0 {
		if dec3, e := decrypt.NewDecryptor("windows", 3); e == nil && dec3.Validate(page1, key) {
			return true
		}
	}
	return false
}

// validateV3KeyPage 用 SQLCipher3 参数校验一页数据（供 3.9 内存扫描器直接复用）。
func validateV3KeyPage(page1, key []byte) bool {
	dec, err := decrypt.NewDecryptor("windows", 3)
	return err == nil && dec.Validate(page1, key)
}

func validateV4EncKeyPage(page1, encKey []byte) bool {
	salt := page1[:v4SaltSize]
	macSalt := make([]byte, len(salt))
	for i, b := range salt {
		macSalt[i] = b ^ 0x3a
	}
	macKey := pbkdf2.Key(encKey, macSalt, v4MacIters, v4KeySize, sha512.New)
	mac := hmac.New(sha512.New, macKey)
	_, _ = mac.Write(page1[v4SaltSize : v4PageSize-v4Reserve+v4IVSize])
	var pageNo [4]byte
	binary.LittleEndian.PutUint32(pageNo[:], 1)
	_, _ = mac.Write(pageNo[:])
	return hmac.Equal(mac.Sum(nil), page1[v4PageSize-v4HmacSize:v4PageSize])
}

// decryptDBAuto 自动识别密钥语义并解密单个数据库。
// out 必须是可回退的 *os.File（解密失败时需要清空后重试另一种语义）。
func decryptDBAuto(ctx context.Context, dec decrypt.Decryptor, dbfile, keyHex, platform string, version int, out *os.File) error {
	if platform == "windows" && version == 4 {
		if err := decryptV4WithEncKey(ctx, dbfile, keyHex, out); err == nil {
			return nil
		}
		if _, err := out.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if err := out.Truncate(0); err != nil {
			return err
		}
	}
	return dec.Decrypt(ctx, dbfile, keyHex, out)
}

// decryptV4WithEncKey 用内存扫描得到的「已派生 enc_key」直接解密 v4 数据库。
// macKey = PBKDF2(enc_key, salt^0x3a, 2, 32, SHA512)，AES key 就是 enc_key 本身。
func decryptV4WithEncKey(ctx context.Context, dbfile, keyHex string, out io.Writer) error {
	encKey, err := hex.DecodeString(strings.TrimSpace(keyHex))
	if err != nil || len(encKey) != v4KeySize {
		if err == nil {
			err = fmt.Errorf("长度不是 32 字节")
		}
		return fmt.Errorf("enc_key 不是合法的 64 位 hex: %w", err)
	}

	f, err := os.Open(dbfile)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() < v4PageSize {
		return fmt.Errorf("数据库文件过小")
	}
	totalPages := (st.Size() + v4PageSize - 1) / v4PageSize

	page1 := make([]byte, v4PageSize)
	if _, err := io.ReadFull(f, page1); err != nil {
		return err
	}
	if bytes.HasPrefix(page1, []byte("SQLite format 3")) {
		return fmt.Errorf("数据库已是明文，无需按 enc_key 解密")
	}

	salt := page1[:v4SaltSize]
	macSalt := make([]byte, len(salt))
	for i, b := range salt {
		macSalt[i] = b ^ 0x3a
	}
	macKey := pbkdf2.Key(encKey, macSalt, v4MacIters, v4KeySize, sha512.New)

	// 先用第一页 HMAC 校验，确认这确实是「已派生」的 enc_key
	mac := hmac.New(sha512.New, macKey)
	_, _ = mac.Write(page1[v4SaltSize : v4PageSize-v4Reserve+v4IVSize])
	var pageNo [4]byte
	binary.LittleEndian.PutUint32(pageNo[:], 1)
	_, _ = mac.Write(pageNo[:])
	if !hmac.Equal(mac.Sum(nil), page1[v4PageSize-v4HmacSize:v4PageSize]) {
		return fmt.Errorf("enc_key 校验未通过（可能是原始密钥 hex）")
	}

	if _, err := out.Write([]byte(common.SQLiteHeader)); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}

	pageBuf := make([]byte, v4PageSize)
	for p := int64(0); p < totalPages; p++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, err := io.ReadFull(f, pageBuf)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break // 末尾不完整页跳过，与 V4Decryptor 行为一致
		}
		if err != nil {
			return err
		}
		_ = n
		allZero := true
		for _, b := range pageBuf {
			if b != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			if _, err := out.Write(pageBuf); err != nil {
				return err
			}
			continue
		}
		plain, err := common.DecryptPage(pageBuf, encKey, macKey, p,
			sha512.New, v4HmacSize, v4Reserve, v4PageSize)
		if err != nil {
			return err
		}
		if _, err := out.Write(plain); err != nil {
			return err
		}
	}
	return nil
}
