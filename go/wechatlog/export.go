package wechatlog

// 从 sjzar/chatlog 移植的核心实现（本目录 internal/ 与 pkg/ 下的 Go 源码）。
// 这里只做一层薄封装：进程探测 -> 取密钥 -> 解密本地微信数据库 -> 按群名/时间读取消息。
// 全程直接调用 Go 代码，不启动 HTTP 服务，也不操作微信窗口。

import (
	"context"
	"crypto/md5"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"order_server/go/wechatlog/internal/errors"
	"order_server/go/wechatlog/internal/wechat"
	"order_server/go/wechatlog/internal/wechat/decrypt"
	"order_server/go/wechatlog/internal/wechatdb"

	"github.com/rs/zerolog"
	"github.com/sirupsen/logrus"
)

func init() {
	// chatlog 核心包默认会打印大量内存扫描 debug 日志，桌面应用里收敛为 warn
	zerolog.SetGlobalLevel(zerolog.WarnLevel)
}

// detectWechatPID 决定内存扫描要用的微信主进程 PID。
// 先尝试管理器探测（能同时拿到数据目录/账号）；若管理器因权限/完整性级别不一致
// 而无法打开微信进程而返回空，则退回到不依赖打开进程的快照枚举
// （CreateToolhelp32Snapshot，与真正取密钥时用的 collectWechatPIDs 同源）。
// 返回 0 表示确实没有任何微信进程在运行。accountID 非 nil 且为空时会被回填。
func detectWechatPID(accountID *string) uint32 {
	_ = wechat.Load()
	if accs := wechat.GetAccounts(); len(accs) > 0 {
		if accountID != nil && *accountID == "" {
			*accountID = accs[0].Name
		}
		return accs[0].PID
	}
	return findWechatPID()
}

// wechatProcMissingErr 在确实没发现微信进程时给出统一、可操作的提示。
func wechatProcMissingErr() error {
	return fmt.Errorf("未检测到运行中的微信客户端，无法做只读内存扫描。" +
		"请确认微信客户端已登录且正在运行；若本程序与微信的「以管理员身份运行」状态不一致" +
		"（例如微信以管理员运行而本程序没有，或反之），请让两者保持一致后重试。")
}

// wechatProcAccessErr 在发现微信进程却无法读取其内存时给出统一、可操作的提示。
func wechatProcAccessErr(pid uint32, err error) error {
	return fmt.Errorf("已定位微信进程（PID=%d），但无法读取其内存（OpenProcess 失败：%v）。"+
		"这通常是因为本程序与微信客户端的「以管理员身份运行」状态不一致、权限/完整性级别不同所致。"+
		"请将两者调整为相同的运行方式：要么都右键「以管理员身份运行」，要么都正常双击运行，然后重试。", pid, err)
}

// Options 导出选项。DataDir / DataKey 留空时自动从运行中的微信进程探测。
// KeyProviderPath / WCDBLibPath 参考 WeFlow：都是用户显式配置的可插拔组件，
// 留空时程序不会加载任何 DLL、不会拉起任何外部程序。
type Options struct {
	Group            string        // 群聊名称（支持 wxid / 群 ID / 备注名 / 昵称）
	Senders          string        // 顾客昵称，逗号分隔；留空不过滤
	Days             int           // 导出天数（1 = 目标日当天，2 = 目标日 + 前一天）
	EndDate          string        // 目标订单日期 YYYY-MM-DD；留空 = 今天。窗口以该日为终点往前 Days 天
	StartDate        string        // 可选：显式起始日 YYYY-MM-DD。设置后窗口改为 [StartDate, EndDate]，Days 不再参与窗口计算
	DataDir          string        // 微信加密数据库目录
	WorkDir          string        // 解密后数据库的存放目录
	DataKey          string        // 手动填写的数据库密钥（hex 或按库 JSON）
	Platform         string        // windows / darwin
	Version          int           // 3 或 4
	AutoKey          bool          // 是否允许（仅在需要时）从微信进程只读扫描密钥
	KeyProviderPath  string        // WeFlow 式外部密钥提供者可执行程序
	ProviderTimeout  time.Duration // 外部程序超时，默认 120s
	AutoKeyOnFailure bool          // 密钥缓存失效 / 解密校验失败时自动重取
	WCDBLibPath      string        // WCDB 动态库路径；配置后走时间游标直读，不做全量解密
	SessionID        string        // 可选：群会话 ID，填了就跳过按群名查会话
}

// Message 是交给上层规则引擎的单条群聊消息。
// Quote / QuoteSender 是「引用回复」里被引用消息的原文与发言人，
// 只有引用消息才有值；订单操作层靠它把「这单不要了」精确对应到原订单。
type Message struct {
	ID          string  `json:"id"`
	Sender      string  `json:"sender"`
	Content     string  `json:"content"`
	Quote       string  `json:"quote,omitempty"`
	QuoteSender string  `json:"quoteSender,omitempty"`
	Time        string  `json:"time"`
	Date        string  `json:"date"`
	Ts          float64 `json:"ts"`
}

// Result 是导出结果。
type Result struct {
	Source       string    `json:"source"`
	DataDir      string    `json:"dataDir"`
	WorkDir      string    `json:"workDir"`
	Version      int       `json:"version"`
	KeySource    string    `json:"keySource"`
	KeyCachePath string    `json:"keyCachePath"`
	From         string    `json:"from"`    // 导出窗口起始日 YYYY-MM-DD
	To           string    `json:"to"`      // 导出窗口结束日（= 目标订单日期）
	EndDate      string    `json:"endDate"` // 本轮目标订单日期
	Messages     []Message `json:"messages"`
	Warnings     []string  `json:"warnings"`
}

// Export 读取本地微信数据库导出指定群聊近 N 天的消息。
//
// 编排参考 hicccc77/WeFlow：密钥获取外包（手动 / 本地缓存 / 外部 keyProvider /
// 显式开启的只读内存扫描），取到后缓存复用；配置了 WCDB 组件就按时间游标直读，
// 否则走内置离线解密。日常运行只碰本地 .db 文件，不发消息、不注入、不碰微信窗口。
func Export(opt Options) (Result, error) {
	var res Result
	if opt.Group == "" {
		return res, fmt.Errorf("群聊名称不能为空")
	}
	if opt.Days <= 0 {
		opt.Days = 2
	}
	if opt.Platform == "" {
		opt.Platform = "windows"
	}

	// 外部密钥提供者可能耗时 120s，整体给足时间
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	// 导出窗口以「目标订单日期」为终点往前 Days 天，而不是永远以今天为终点：
	// 这样手动指定历史日期时，也能导出该日期前一天顾客发来的订单消息。
	endDay := today
	end := now
	if s := strings.TrimSpace(opt.EndDate); s != "" {
		if t, err := time.ParseInLocation("2006-01-02", s, now.Location()); err == nil {
			endDay = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
			switch {
			case endDay.Before(today):
				end = endDay.Add(24*time.Hour - time.Second) // 历史日期：取当天 23:59:59，不漏当天消息
			case endDay.After(today):
				endDay, end = today, now // 不允许导出未来
			}
		} else {
			res.Warnings = append(res.Warnings, "目标订单日期格式无效，已按今天处理："+s)
		}
	}
	start := endDay.AddDate(0, 0, -(opt.Days - 1))
	// 账单审核等场景需要「用户选的起止日期」，而不是「终点往前 N 天」。
	// 底层 GetMessages / ReadViaWCDB 本来就接收 start、end 两个时间点，
	// 因此这里只替换窗口的起点，其余密钥 / 解密 / 去重逻辑完全复用。
	if s := strings.TrimSpace(opt.StartDate); s != "" {
		if t, err := time.ParseInLocation("2006-01-02", s, now.Location()); err == nil {
			start = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
			if start.After(endDay) {
				return res, fmt.Errorf("导出起始日期 %s 晚于结束日期 %s",
					start.Format("2006-01-02"), endDay.Format("2006-01-02"))
			}
		} else {
			res.Warnings = append(res.Warnings, "导出起始日期格式无效，已按近 N 天窗口处理："+s)
		}
	}
	res.From = start.Format("2006-01-02")
	res.To = endDay.Format("2006-01-02")
	res.EndDate = endDay.Format("2006-01-02")

	// 1) 只在确实缺路径/版本信息时探测微信进程（只读进程信息，不操作窗口）
	var accountID string
	var pid uint32
	if opt.DataDir == "" || opt.Version == 0 {
		if err := wechat.Load(); err != nil {
			res.Warnings = append(res.Warnings, "微信进程探测失败（可手动填写数据目录，不影响离线读取）："+err.Error())
		}
		accounts := wechat.GetAccounts()
		if len(accounts) > 0 {
			a := accounts[0]
			if opt.DataDir == "" {
				opt.DataDir = a.DataDir
			}
			if opt.Version == 0 {
				opt.Version = a.Version
			}
			accountID, pid = a.Name, a.PID
		}
	}
	if opt.Version == 0 {
		opt.Version = 4 // 探测不到时按当前主流微信 4.x 处理
	}
	if opt.DataDir == "" {
		return res, fmt.Errorf("未找到微信数据目录，请在配置页填写「微信数据目录」（或保持微信客户端登录）")
	}
	if _, err := os.Stat(opt.DataDir); err != nil {
		return res, fmt.Errorf("微信数据目录不可访问 %s: %w", opt.DataDir, err)
	}
	// 配置里写死的版本可能来自上一次使用的另一个微信大版本；以数据目录实际布局为准，
	// 否则 3.9 会被当成 4.x：解密器、密钥扫描、datasource 全都会选错。
	if v, note := resolveWechatVersion(opt.Platform, opt.DataDir, opt.Version); v != opt.Version || note != "" {
		opt.Version = v
		if note != "" {
			res.Warnings = append(res.Warnings, note)
		}
	}
	if opt.WorkDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			base = os.TempDir()
		}
		opt.WorkDir = filepath.Join(base, "order_server", "wechat_decrypted")
	}
	if err := os.MkdirAll(opt.WorkDir, 0755); err != nil {
		return res, fmt.Errorf("创建解密输出目录失败 %s: %w", opt.WorkDir, err)
	}

	// 2) 取密钥：手动 > 本地缓存 > 外部 keyProvider > （显式开启的）内存扫描
	//    只有真正需要扫描时才去读微信进程，日常有缓存/手动密钥时完全离线。
	scan := func() (string, error) {
		if pid == 0 {
			pid = detectWechatPID(&accountID)
			if pid == 0 {
				return "", wechatProcMissingErr()
			}
		}
		if err := probeWeChatProcess(pid); err != nil {
			return "", wechatProcAccessErr(pid, err)
		}
		return scanDataKey(ctx, opt.DataDir, pid, opt.Version)
	}
	keyReq := KeyRequest{
		DataDir:          opt.DataDir,
		AccountID:        accountID,
		Version:          opt.Version,
		ManualKey:        opt.DataKey,
		KeyProviderPath:  opt.KeyProviderPath,
		ProviderTimeout:  opt.ProviderTimeout,
		AutoKey:          opt.AutoKey,
		AutoKeyOnFailure: opt.AutoKeyOnFailure,
		Scan:             scan,
		Log:              func(s string) { logrus.Warn("[微信密钥] " + s) },
	}
	decryptedOnly := opt.DataKey == "" && !opt.AutoKey && opt.KeyProviderPath == "" &&
		opt.WCDBLibPath == "" && workDirHasDB(opt.WorkDir)

	var keyRes KeyResolution
	if decryptedOnly {
		keyRes.Source = "已解密目录（无需密钥）"
		res.Warnings = append(res.Warnings, "未提供密钥且未开启自动取密钥，直接读取解密输出目录中已解密的数据库")
	} else {
		var err error
		keyRes, err = ResolveKey(keyReq)
		if err != nil {
			return res, err
		}
		opt.DataKey = keyRes.Key
	}
	res.KeySource = keyRes.Source
	res.KeyCachePath = KeyCachePath()
	res.Warnings = append(res.Warnings, keyRes.Warnings...)

	// 3) WeFlow 式 WCDB 组件：直接打开加密库，按时间游标批量读，跳过全量解密。
	//    该协议以 4.x 的 session.db 为入口，3.9 不适用，自动回退内置解密链路。
	if strings.TrimSpace(opt.WCDBLibPath) != "" && opt.Version != 3 {
		msgs, warns, err := ReadViaWCDB(opt.WCDBLibPath, opt.DataDir, opt.Group,
			opt.SessionID, opt.DataKey, parseSenders(opt.Senders), start, end)
		res.Warnings = append(res.Warnings, warns...)
		if err != nil {
			return res, fmt.Errorf("WCDB 组件读取失败: %w", err)
		}
		res.Messages = msgs
		res.Source = "wcdb/" + filepath.Base(opt.WCDBLibPath)
		res.DataDir, res.WorkDir, res.Version = opt.DataDir, opt.WorkDir, opt.Version
		return res, nil
	} else if strings.TrimSpace(opt.WCDBLibPath) != "" {
		res.Warnings = append(res.Warnings, "微信 3.9 不支持 WCDB 直读组件（4.x session.db 协议），已改用内置解密链路")
	}

	// 4) 内置链路：解密到 WorkDir -> pure-Go SQLite 按群名/时间查询
	if opt.DataKey != "" {
		n, err := decryptAll(ctx, opt.DataDir, opt.WorkDir, opt.DataKey, opt.Platform, opt.Version)
		// 解密一个库都没成功且密钥来自缓存/provider：判定密钥失效，清缓存后重取一次
		if (n == 0 || err != nil) && opt.AutoKeyOnFailure &&
			keyRes.Source != "手动配置" && keyRes.Source != "已解密目录（无需密钥）" {
			_ = ClearKeyCache()
			res.Warnings = append(res.Warnings, "解密校验未通过，判定密钥可能失效，正在重新获取")
			keyReq.ForceRefresh = true
			if fresh, e := ResolveKey(keyReq); e == nil {
				opt.DataKey = fresh.Key
				res.KeySource = fresh.Source + "（失效重取）"
				res.Warnings = append(res.Warnings, fresh.Warnings...)
				n, err = decryptAll(ctx, opt.DataDir, opt.WorkDir, opt.DataKey, opt.Platform, opt.Version)
			} else {
				res.Warnings = append(res.Warnings, "重新获取密钥失败："+e.Error())
			}
		}
		if err != nil {
			return res, fmt.Errorf("解密微信数据库失败: %w", err)
		}
		if n == 0 {
			res.Warnings = append(res.Warnings, "没有找到可解密的 .db 文件（请检查密钥、微信版本与数据目录）")
		}
	}

	db, err := wechatdb.New(opt.WorkDir, opt.Platform, opt.Version)
	if err != nil {
		return res, fmt.Errorf("打开解密数据库失败（%s）：%w；请确认密钥、微信版本与数据目录是否正确", opt.WorkDir, err)
	}
	defer db.Close()

	msgs, err := db.GetMessages(start, end, opt.Group, "", "", 0, 0)
	if err != nil {
		return res, fmt.Errorf("读取聊天记录失败: %w", err)
	}

	senders := parseSenders(opt.Senders)
	seen := make(map[string]bool)
	for _, m := range msgs {
		if m == nil || m.Time.IsZero() {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if !usableTextContent(content) {
			continue
		}
		sender := m.SenderName
		if sender == "" {
			sender = m.Sender
		}
		if !senderAllowed(sender, senders) {
			continue
		}
		quote, quoteSender := m.ReferQuote()
		key := sender + "|" + content
		if quote != "" {
			key += "|" + quote
		}
		id := fmt.Sprintf("%x", md5.Sum([]byte(key)))[:16]
		if seen[id] {
			continue
		}
		seen[id] = true
		res.Messages = append(res.Messages, Message{
			ID:          id,
			Sender:      sender,
			Content:     content,
			Quote:       quote,
			QuoteSender: quoteSender,
			Time:        m.Time.Format("2006-01-02 15:04:05"),
			Date:        m.Time.Format("2006-01-02"),
			Ts:          float64(m.Time.Unix()),
		})
	}

	res.Source = "chatlog/" + opt.Platform + "-v" + strconv.Itoa(opt.Version)
	res.DataDir = opt.DataDir
	res.WorkDir = opt.WorkDir
	res.Version = opt.Version
	return res, nil
}

// ResolveAndCacheKey 只解析并缓存微信数据库密钥（不解密、不导出），
// 供「启动时自动获取密钥」与「配置页手动触发」复用。
// 编排与 Export 完全一致：手动 > 缓存 > 外部 keyProvider > 只读内存扫描。
// 取到的密钥会写入本地缓存（权限 0600），之后导出无需再次获取。
func ResolveAndCacheKey(opt Options, onStatus func(string)) (KeyResolution, error) {
	var res KeyResolution
	if opt.Platform == "" {
		opt.Platform = "windows"
	}
	if opt.ProviderTimeout <= 0 {
		opt.ProviderTimeout = DefaultProviderTimeout
	}
	log := func(s string) {
		if onStatus != nil {
			onStatus(s)
		}
	}

	// 只在确实缺路径/版本信息时探测微信进程（只读进程信息，不操作窗口）
	var accountID string
	var pid uint32
	if opt.DataDir == "" || opt.Version == 0 {
		if err := wechat.Load(); err != nil {
			res.Warnings = append(res.Warnings, "微信进程探测失败（可手动填写数据目录）："+err.Error())
		}
		accounts := wechat.GetAccounts()
		if len(accounts) > 0 {
			a := accounts[0]
			if opt.DataDir == "" {
				opt.DataDir = a.DataDir
			}
			if opt.Version == 0 {
				opt.Version = a.Version
			}
			accountID, pid = a.Name, a.PID
		}
	}
	if opt.Version == 0 {
		opt.Version = 4
	}
	if opt.DataDir == "" {
		return res, fmt.Errorf("未找到微信数据目录，请在配置页填写「微信数据目录」（或保持微信客户端登录）")
	}
	if _, err := os.Stat(opt.DataDir); err != nil {
		return res, fmt.Errorf("微信数据目录不可访问 %s: %w", opt.DataDir, err)
	}
	if v, note := resolveWechatVersion(opt.Platform, opt.DataDir, opt.Version); v != opt.Version || note != "" {
		opt.Version = v
		if note != "" {
			res.Warnings = append(res.Warnings, note)
		}
	}

	// 内存扫描闭包：仅在显式开启且确实缺密钥时由 ResolveKey 调用（只读，不注入）
	scan := func() (string, error) {
		if pid == 0 {
			pid = detectWechatPID(&accountID)
			if pid == 0 {
				return "", wechatProcMissingErr()
			}
		}
		if err := probeWeChatProcess(pid); err != nil {
			return "", wechatProcAccessErr(pid, err)
		}
		return scanDataKey(context.Background(), opt.DataDir, pid, opt.Version)
	}

	keyReq := KeyRequest{
		DataDir:          opt.DataDir,
		AccountID:        accountID,
		Version:          opt.Version,
		ManualKey:        opt.DataKey,
		KeyProviderPath:  opt.KeyProviderPath,
		ProviderTimeout:  opt.ProviderTimeout,
		AutoKey:          opt.AutoKey,
		AutoKeyOnFailure: opt.AutoKeyOnFailure,
		Scan:             scan,
		Log:              log,
	}
	return ResolveKey(keyReq)
}

// workDirHasDB 判断解密输出目录里是否已有可用数据库。
func workDirHasDB(workDir string) bool {
	if workDir == "" {
		return false
	}
	found := false
	_ = filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found || d == nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".db") {
			found = true
		}
		return nil
	})
	return found
}

// Detect 探测运行中的微信客户端，返回数据目录、平台与版本，供配置页一键填入。
func Detect() map[string]interface{} {
	out := map[string]interface{}{"success": false}
	if err := wechat.Load(); err != nil {
		out["message"] = "微信进程探测失败：" + err.Error()
		return out
	}
	accounts := wechat.GetAccounts()
	if len(accounts) == 0 {
		out["message"] = "未检测到运行中的微信客户端，请手动填写数据目录与密钥"
		return out
	}
	a := accounts[0]
	version, note := resolveWechatVersion(a.Platform, a.DataDir, a.Version)
	out["success"] = true
	out["account"] = a.Name
	out["dataDir"] = a.DataDir
	out["platform"] = a.Platform
	out["version"] = version
	out["message"] = fmt.Sprintf("检测到微信账号 %s（%s，版本 %d）", a.Name, a.FullVersion, version)
	if note != "" {
		out["message"] = out["message"].(string) + "；" + note
	}
	return out
}

func parseSenders(raw string) []string {
	var out []string
	for _, s := range strings.Split(strings.ReplaceAll(raw, "，", ","), ",") {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func senderAllowed(sender string, senders []string) bool {
	if len(senders) == 0 {
		return true
	}
	low := strings.ToLower(strings.ReplaceAll(sender, " ", ""))
	for _, s := range senders {
		if strings.Contains(low, strings.ReplaceAll(s, " ", "")) {
			return true
		}
	}
	return false
}

// decryptAll 把 dataDir 下所有 .db 解密到 workDir，保持相对目录结构。
// keyOrJSON 可以是单个 hex 密钥（手动/旧版微信），也可以是新版扫描器返回的
// 「按库路径 -> enc_key」JSON（微信 4.x 每个数据库密钥不同）。
func decryptAll(ctx context.Context, dataDir, workDir, keyOrJSON, platform string, version int) (int, error) {
	dec, err := decrypt.NewDecryptor(platform, version)
	if err != nil {
		return 0, err
	}
	keyMap, isKeyMap := parseDbKeyMap(keyOrJSON)
	count := 0
	err = filepath.WalkDir(dataDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil // 跳过不可读目录
		}
		lower := strings.ToLower(d.Name())
		if !strings.HasSuffix(lower, ".db") || strings.Contains(strings.ToLower(path), "fts") {
			return nil
		}
		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return nil
		}
		out := filepath.Join(workDir, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
			return nil
		}
		// 3.9 的明文输出目录里不能有残留 WAL/SHM：数据已直接合并进主库
		if platform == "windows" && version == 3 {
			_ = os.Remove(out + "-wal")
			_ = os.Remove(out + "-shm")
		}
		// 微信 3.9 的最新消息只在 -wal 里，WAL 变新时即使主库没变也要重新解密合并。
		// 4.x 不做 WAL 合并，保持原来的「主库没变就跳过」行为，避免每次全量解密。
		walNewer := false
		if platform == "windows" && version == 3 {
			if wst, we := os.Stat(path + "-wal"); we == nil {
				if st, e2 := os.Stat(out); e2 != nil || wst.ModTime().After(st.ModTime()) {
					walNewer = true
				}
			}
		}
		// 缓存是否有效：已解密、比源主库新、且（非 3.9 或已带 WAL 合并标记）。
		// 3.9 必须带合并标记——旧版本产出的缓存 mtime 虽新但不含 WAL 数据，不能复用。
		cacheValid := false
		if st, e := os.Stat(out); e == nil && !walNewer {
			if src, e2 := d.Info(); e2 == nil && st.ModTime().After(src.ModTime()) {
				if platform == "windows" && version == 3 && !isKeyMap {
					cacheValid = v3MergeMarked(out)
				} else {
					cacheValid = true
				}
			}
		}
		if cacheValid {
			count++
			return nil
		}
		// 选择本库对应的密钥；按库密钥 JSON 里没有该库时跳过
		key := keyOrJSON
		if isKeyMap {
			key = keyMap[normalizeDBRel(rel)]
			if key == "" {
				return nil
			}
		}
		tmp := out + ".tmp"
		f, err := os.Create(tmp)
		if err != nil {
			return nil
		}
		derr := decryptDBAuto(ctx, dec, path, key, platform, version, f)
		_ = f.Close()
		if derr != nil {
			if stderrors.Is(derr, errors.ErrAlreadyDecrypted) {
				_ = copyFile(path, out)
			} else {
				_ = os.Remove(tmp)
				return nil // 单个文件失败（如非加密 db）不影响整体
			}
		} else if err := os.Rename(tmp, out); err != nil {
			_ = os.Remove(tmp)
			return nil
		}
		if platform == "windows" && version == 3 && !isKeyMap {
			// 3.9：把 WAL 中已提交的最新页合并进明文库，等价于 SQLite checkpoint；
			// 并打「已合并」标记，避免旧版本产出的「不含 WAL 数据」缓存被误判为有效。
			mergeV3WALForDecryptedDB(path, out, key)
			v3MergeMark(out)
		}
		count++
		return nil
	})
	return count, err
}

// parseDbKeyMap 解析新版扫描器输出的按库密钥 JSON：
// {"message/message_0.db": {"enc_key": "64位hex", "salt": "..."}, ...}
func parseDbKeyMap(raw string) (map[string]string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return nil, false
	}
	var m map[string]struct {
		EncKey string `json:"enc_key"`
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil || len(m) == 0 {
		return nil, false
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if v.EncKey != "" {
			out[normalizeDBRel(k)] = v.EncKey
		}
	}
	return out, len(out) > 0
}

// normalizeDBRel 统一路径分隔符，并去掉 db_storage/ 前缀，
// 使「相对 dataDir 的路径」与扫描器输出的「相对 db_storage 的路径」能对齐。
func normalizeDBRel(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	p = strings.ToLower(p)
	return strings.TrimPrefix(p, "db_storage/")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// v3MergeMarkerPath 返回 3.9 解密明文库的「WAL 已合并」标记文件路径。
// 旧版本产出的缓存没有这个标记，会被 decryptAll 判定为失效并强制重新解密 + 合并，
// 否则会复用一份 mtime 虽新、但不含最新 WAL 消息的过期缓存。
func v3MergeMarkerPath(out string) string { return out + ".v3m" }

func v3MergeMarked(out string) bool {
	_, err := os.Stat(v3MergeMarkerPath(out))
	return err == nil
}

func v3MergeMark(out string) {
	_ = os.WriteFile(v3MergeMarkerPath(out), []byte("1"), 0644)
}
