package wechatlog

// 微信 3.9.x 与 4.x 的数据目录 / 加密参数完全不同：
//
//   3.9.x（WeChat.exe）  Documents\WeChat Files\wxid_xxx\Msg\*.db
//                        SQLCipher3：PBKDF2-SHA1 64000、HMAC-SHA1、全库共用一把 32 字节原始密钥
//   4.x  （Weixin.exe）  ...\xwechat_files\wxid_xxx\db_storage\message\message_0.db
//                        SQLCipher4：PBKDF2-SHA512 256000、HMAC-SHA512、每库独立 enc_key
//
// 旧实现把「找锚点库 / 校验密钥 / 内存扫描」全部按 4.x 写死，导致 3.9 上
// 取密钥必然失败（日志：未找到可用于校验的加密数据库 ...\db_storage）。
// 这里集中做目录布局识别与版本校正，供导出、取密钥、缓存校验共用。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// isDir / isFile 只做只读判断，任何错误都按「不存在」处理。
func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// v3DBRelCandidates 是微信 3.9 里最适合做「密钥校验锚点」的加密库。
// Misc.db 体积小、必定存在、与 MSG*.db 共用同一把密钥，优先用它。
var v3DBRelCandidates = []string{
	filepath.Join("Msg", "Misc.db"),
	filepath.Join("Msg", "MicroMsg.db"),
	filepath.Join("Msg", "Multi", "MSG0.db"),
	filepath.Join("Msg", "MSG0.db"),
	"Misc.db",
	"MicroMsg.db",
	filepath.Join("Multi", "MSG0.db"),
	"MSG0.db",
}

var v4DBRelCandidates = []string{
	filepath.Join("db_storage", "message", "message_0.db"),
	filepath.Join("db_storage", "session", "session.db"),
	filepath.Join("message", "message_0.db"),
	filepath.Join("session", "session.db"),
}

// dbLayoutVersion 只根据目录特征判断微信大版本，返回 3 / 4 / 0（无法判断）。
// 不读进程、不读注册表，纯离线，因此配置页与导出链路都能复用。
func dbLayoutVersion(dataDir string) int {
	base := strings.TrimSpace(dataDir)
	if base == "" {
		return 0
	}
	base = filepath.Clean(base)

	// 4.x 特征：db_storage 目录（或用户直接把数据目录填成了 db_storage / 其子目录）
	name := strings.ToLower(filepath.Base(base))
	if name == "db_storage" {
		return 4
	}
	for _, rel := range v4DBRelCandidates {
		if isFile(filepath.Join(base, rel)) {
			return 4
		}
	}
	if isDir(filepath.Join(base, "db_storage")) {
		return 4
	}

	// 3.9 特征：Msg 目录 + Misc.db / MicroMsg.db / MSG*.db
	if name == "msg" {
		for _, rel := range v3DBRelCandidates {
			if isFile(filepath.Join(base, rel)) {
				return 3
			}
		}
	}
	msgDir := filepath.Join(base, "Msg")
	if isDir(msgDir) {
		for _, rel := range []string{"Misc.db", "MicroMsg.db", filepath.Join("Multi", "MSG0.db"), "MSG0.db"} {
			if isFile(filepath.Join(msgDir, rel)) {
				return 3
			}
		}
	}
	for _, rel := range v3DBRelCandidates {
		if isFile(filepath.Join(base, rel)) {
			return 3
		}
	}
	return 0
}

// resolveWechatVersion 把「配置里写死的版本」与「数据目录实际布局」对齐。
// 用户从 4.x 换回 3.9（或反之）时，配置文件里的 version 往往是旧值，
// 若不校正会用错解密器、选错 datasource，即使拿到密钥也必然全库解密失败。
// 返回 (最终版本, 给用户的说明)。
func resolveWechatVersion(platform, dataDir string, configured int) (int, string) {
	if strings.TrimSpace(platform) != "" && !strings.EqualFold(platform, "windows") {
		if configured == 0 {
			return 4, ""
		}
		return configured, ""
	}
	detected := dbLayoutVersion(dataDir)
	if detected == 0 {
		if configured == 0 {
			return 4, ""
		}
		return configured, ""
	}
	if configured == 0 {
		return detected, fmt.Sprintf("数据目录为微信 %d.x 布局，已自动按 %d.x 处理", detected, detected)
	}
	if configured != detected {
		return detected, fmt.Sprintf("配置里的微信版本是 %d，但数据目录是 %d.x 布局，已自动按 %d.x 处理（建议到配置页重新探测保存）",
			configured, detected, detected)
	}
	return configured, ""
}

// encryptedDBPage 判断一个 .db 是否为「加密数据库」：文件够大且头部不是 SQLite 明文头。
func encryptedDBPage(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() < 4096 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 16)
	if _, err := f.Read(head); err != nil {
		return false
	}
	return !strings.HasPrefix(string(head), "SQLite format 3")
}

// anchorDBPathForVersion 找一个可用于「第一页 HMAC 验签」的加密数据库。
// version=0 时自动按目录布局判断；同时兼容用户把数据目录填成账号根目录、
// db_storage 目录、Msg 目录三种形态。
func anchorDBPathForVersion(dataDir string, version int) string {
	base := strings.TrimSpace(dataDir)
	if base == "" {
		return ""
	}
	base = filepath.Clean(base)
	if version == 0 {
		version = dbLayoutVersion(base)
	}

	ordered := make([]string, 0, len(v4DBRelCandidates)+len(v3DBRelCandidates))
	if version == 4 {
		ordered = append(ordered, v4DBRelCandidates...)
		ordered = append(ordered, v3DBRelCandidates...)
	} else if version == 3 {
		ordered = append(ordered, v3DBRelCandidates...)
		ordered = append(ordered, v4DBRelCandidates...)
	} else {
		ordered = append(ordered, v4DBRelCandidates...)
		ordered = append(ordered, v3DBRelCandidates...)
	}
	for _, rel := range ordered {
		p := filepath.Join(base, rel)
		if isFile(p) && encryptedDBPage(p) {
			return p
		}
	}

	// 兜底：目录布局不标准时，递归找第一个加密的 .db（跳过 WAL/SHM 与 FTS 索引）
	var found string
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" || d == nil || d.IsDir() {
			return nil
		}
		low := strings.ToLower(d.Name())
		if !strings.HasSuffix(low, ".db") || strings.Contains(low, "fts") {
			return nil
		}
		if encryptedDBPage(path) {
			found = path
		}
		return nil
	})
	return found
}

// anchorDBPath 保持原有调用点不变，按目录布局自动选择锚点库。
func anchorDBPath(dataDir string) string {
	return anchorDBPathForVersion(dataDir, 0)
}

// v3DBPathsForVersion 返回 v3 目录下优先用于校验的加密库（供内存扫描器用）。
func v3DBPaths(dataDir string) []string {
	base := filepath.Clean(strings.TrimSpace(dataDir))
	if base == "" || base == "." {
		return nil
	}
	var out []string
	for _, rel := range v3DBRelCandidates {
		p := filepath.Join(base, rel)
		if isFile(p) && encryptedDBPage(p) {
			out = append(out, p)
		}
	}
	return out
}
