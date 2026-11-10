package wechatlog

// 微信 3.9 的「最新消息在 WAL 里」问题。
//
// 3.9 用 SQLCipher3 + WAL 模式：MSG0.db 是预分配的主库，日常写入全部落在
// MSG0.db-wal，主库 mtime 可能停在几天前。只解密主库会读到过期消息
// （本机实测：主库停在 11-08，WAL 里有 11-09 全天消息）。
//
// 实测确认 WAL 帧与主库页是同一套加密布局：
//   salt      = 主库第一页前 16 字节
//   encKey    = PBKDF2(rawKey, salt, 64000, 32, SHA1)
//   macKey    = PBKDF2(encKey, salt^0x3a, 2, 32, SHA1)
//   帧页数据  = [密文 0:4048][IV 4048:4064][HMAC-SHA1 4064:4084][填充 4084:4096]
//   HMAC      = HMAC-SHA1(macKey, 帧页[0:4064] + LE32(帧头里的 pgno))
//
// 这里不做「重算 WAL checksum 再交给 SQLite」，而是把已提交帧的明文页直接
// 按 pgno 写回解密后的主库，然后丢弃 -wal/-shm：等效于 SQLite 的 checkpoint，
// 但不需要复刻 WAL checksum 算法，也不依赖 SQLCipher 驱动。

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"order_server/go/wechatlog/internal/wechat/decrypt/common"

	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/pbkdf2"
)

const (
	v3WALMagic     = 0x377f0682
	v3WALHeader    = 32
	v3WALFrameHead = 24
	v3WALReserve   = 48 // IV 16 + HMAC-SHA1 20，向上对齐到 16 的倍数
	v3WALHmacSize  = 20
)

// mergeV3WALInto 把加密 WAL 中已提交的帧解密并合并进解密后的明文主库。
// srcDB 是加密主库（只用来取 salt），outDB 是已经解密好的明文 SQLite 文件。
// 返回成功合并的帧数；单帧 HMAC 失败只跳过，不影响其余帧。
func mergeV3WALInto(srcDB, walPath, outDB, rawKeyHex string) (int, error) {
	key, err := hex.DecodeString(strings.TrimSpace(rawKeyHex))
	if err != nil || len(key) != 32 {
		return 0, fmt.Errorf("3.9 密钥不是合法的 64 位 hex")
	}
	fdb, err := os.Open(srcDB)
	if err != nil {
		return 0, err
	}
	salt := make([]byte, 16)
	_, err = fdb.Read(salt)
	_ = fdb.Close()
	if err != nil {
		return 0, fmt.Errorf("读取主库 salt 失败: %w", err)
	}
	encKey := pbkdf2.Key(key, salt, 64000, 32, sha1.New)
	macKey := pbkdf2.Key(encKey, common.XorBytes(salt, 0x3a), 2, 32, sha1.New)

	wal, err := os.ReadFile(walPath)
	if err != nil {
		return 0, err
	}
	if len(wal) < v3WALHeader {
		return 0, fmt.Errorf("WAL 文件过短")
	}
	if magic := binary.BigEndian.Uint32(wal[:4]); magic&0xfffffffe != v3WALMagic {
		return 0, fmt.Errorf("WAL magic 不匹配: %08x", magic)
	}
	pageSize := int(binary.BigEndian.Uint32(wal[8:12]))
	if pageSize != 4096 {
		return 0, fmt.Errorf("暂不支持页大小 %d 的 WAL", pageSize)
	}
	frameSize := v3WALFrameHead + pageSize
	frames := (len(wal) - v3WALHeader) / frameSize
	if frames == 0 {
		return 0, nil
	}

	// 与 SQLite WAL 恢复一致：只应用到最后一个 commit 帧，丢弃未提交事务。
	lastCommit, commitSize := 0, uint32(0)
	for i := 0; i < frames; i++ {
		off := v3WALHeader + i*frameSize
		if size := binary.BigEndian.Uint32(wal[off+4 : off+8]); size > 0 {
			lastCommit, commitSize = i+1, size
		}
	}
	if lastCommit == 0 {
		// 没有 commit 帧时尽力应用全部（微信运行中偶见，读到的内容仍比旧主库新）
		lastCommit = frames
	}

	out, err := os.OpenFile(outDB, os.O_RDWR, 0600)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	applied, skipped := 0, 0
	for i := 0; i < lastCommit; i++ {
		off := v3WALHeader + i*frameSize
		pgno := binary.BigEndian.Uint32(wal[off : off+4])
		if pgno == 0 || pgno > 1<<24 {
			skipped++
			continue
		}
		framePage := wal[off+v3WALFrameHead : off+v3WALFrameHead+pageSize]
		plain, derr := common.DecryptPage(framePage, encKey, macKey, int64(pgno)-1,
			sha1.New, v3WALHmacSize, v3WALReserve, pageSize)
		if derr != nil {
			skipped++
			continue
		}
		page := plain
		if pgno == 1 {
			// 第一页前 16 字节原本是 salt，明文库里要换成 SQLite 文件头
			page = append([]byte(common.SQLiteHeader), plain...)
		}
		if len(page) != pageSize {
			skipped++
			continue
		}
		if _, werr := out.WriteAt(page, int64(pgno-1)*int64(pageSize)); werr != nil {
			return applied, werr
		}
		applied++
	}
	if applied == 0 {
		return 0, fmt.Errorf("WAL 共 %d 帧，无一是可用明文页（跳过 %d）", frames, skipped)
	}

	// WAL 若新增了页，更新明文库文件头里的「数据库页数」（offset 28，大端），
	// 否则 SQLite 会以旧值截断、看不到新增页。
	if commitSize > 0 {
		head := make([]byte, 100)
		if n, rerr := out.ReadAt(head, 0); rerr == nil && n == len(head) {
			old := binary.BigEndian.Uint32(head[28:32])
			if commitSize > old {
				binary.BigEndian.PutUint32(head[28:32], commitSize)
				// version-valid-for 与 change counter 保持一致，避免触发额外恢复
				copy(head[92:96], head[24:28])
				_, _ = out.WriteAt(head, 0)
			}
		}
	}
	if skipped > 0 {
		logrus.Warnf("[微信3.9] WAL 合并跳过 %d/%d 帧（可能是未提交事务或加密布局不同的页）", skipped, lastCommit)
	}
	return applied, nil
}

// mergeV3WALForDecryptedDB 是 decryptAll 用的薄封装：源库没有 WAL 时静默跳过，
// 失败只记日志不中断导出（主库数据仍可用）。
func mergeV3WALForDecryptedDB(srcDB, outDB, rawKeyHex string) {
	// 解密目录里不能残留旧的 WAL/SHM，否则 SQLite 会去应用旧内容
	_ = os.Remove(outDB + "-wal")
	_ = os.Remove(outDB + "-shm")
	walPath := srcDB + "-wal"
	if st, err := os.Stat(walPath); err != nil || st.IsDir() || st.Size() <= v3WALHeader {
		return
	}
	n, err := mergeV3WALInto(srcDB, walPath, outDB, rawKeyHex)
	if err != nil {
		logrus.Warnf("[微信3.9] WAL 合并失败（将只使用主库已有数据）: %v", err)
		return
	}
	logrus.Infof("[微信3.9] 已把 WAL 中 %d 个最新页合并进解密数据库：%s", n, outDB)
}
