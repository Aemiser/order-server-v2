//go:build windows

package wechatlog

import (
	"context"
	"fmt"
	"strings"
	"time"

	wxkey "order_server/go/wechatlog/internal/wechat/key/windows"

	"github.com/sirupsen/logrus"
)

// scanDataKey 从运行中的微信进程内存提取数据库密钥，全程只读、零配置。
// 按微信大版本分流：
//
//	3.9.x：WeChat.exe + SQLCipher3，全库共用一把 32 字节原始密钥，
//	       走 scanV3DataKey（指针特征 + ASCII 缓存 + 受控滑窗）；
//	4.x  ：Weixin.exe + SQLCipher4，每库独立 enc_key，依次尝试
//	       1. WCDB Config.Cipher 混淆 blob 解码（4.1+/4.2 主路径）；
//	       2. x'<hex>' ASCII 缓存扫描（4.0/4.1）；
//	       3. 32 字节二进制 enc_key 滑窗扫描（备用）。
func scanDataKey(ctx context.Context, dataDir string, pid uint32, version int) (string, error) {
	if version == 3 {
		key, err := scanV3DataKey(ctx, dataDir, pid, func(s string) {
			logrus.Info("[微信密钥·3.9] " + s)
		})
		if err == nil && strings.TrimSpace(key) != "" {
			return key, nil
		}
		detail := "未匹配到可校验的密钥"
		if err != nil {
			detail = err.Error()
		}
		return "", fmt.Errorf("微信 3.9 内存密钥扫描（数据目录 %s）：%s；"+
			"请确认微信 3.9 已登录、本程序与微信的「以管理员身份运行」状态一致，"+
			"或把取密钥工具的输出放进自动密钥目录 / 在配置页手动粘贴密钥", dataDir, detail)
	}

	var errs []string

	// 1) Config.Cipher：不依赖对象链偏移，直接对被 XOR 混淆的字面量做 32 相位盲扫。
	cipherCtx, cancelCipher := context.WithTimeout(ctx, 90*time.Second)
	key, err := ScanCipherDataKey(cipherCtx, dataDir, pid, func(s string) {
		logrus.Info("[微信密钥·Config.Cipher] " + s)
	})
	cancelCipher()
	if err == nil && strings.TrimSpace(key) != "" {
		logrus.Info("[微信密钥] Config.Cipher 扫描命中，已取到按库密钥")
		return key, nil
	}
	if err != nil {
		errs = append(errs, "Config.Cipher 扫描："+err.Error())
	}

	// 2) 老版本 ASCII 缓存
	asciiCtx, cancelASCII := context.WithTimeout(ctx, 60*time.Second)
	res := wxkey.GetDbKeyFromMemory(asciiCtx, dataDir, pid, nil)
	cancelASCII()
	if res.Success && strings.TrimSpace(res.Key) != "" {
		logrus.Info("[微信密钥] ASCII 缓存扫描命中")
		return res.Key, nil
	}
	if res.Error != "" {
		errs = append(errs, "ASCII 缓存扫描："+res.Error)
	} else {
		errs = append(errs, "ASCII 缓存扫描：未命中")
	}

	// 3) 二进制明文密钥滑窗（4.1+ 上密钥已被混淆，仅作兼容兜底）
	binaryCtx, cancelBinary := context.WithTimeout(ctx, 3*time.Minute)
	bkey, berr := ScanBinaryDataKey(binaryCtx, dataDir, pid, func(s string) {
		logrus.Warn("[微信密钥·二进制] " + s)
	})
	cancelBinary()
	if berr == nil && strings.TrimSpace(bkey) != "" {
		logrus.Info("[微信密钥] 二进制内存扫描命中，已取到按库密钥")
		return bkey, nil
	}
	if berr != nil {
		errs = append(errs, "二进制内存扫描："+berr.Error())
	}

	return "", fmt.Errorf("微信 %d.x 密钥扫描（数据目录 %s）：%s", version, dataDir, strings.Join(errs, "；"))
}
