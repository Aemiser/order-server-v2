package version

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"order_server/go/global"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ---------------- 内嵌的更新链路配置 ----------------
// 这些值编译进 exe，不读 settings.yaml / 数据库 cfg_settings。
// 原因：全新机器可能没有配置文件、或 cfg_settings 里缺 version.repo，
// 而自动更新必须始终可用，否则用户会永远停在旧版本。
const (
	Repo = "Aemiser/order_manager" // 版本检查与下载共用的 GitHub 仓库
	// CheckInterval 是后台轮询间隔，按要求设为 1 分钟。
	// 注意：GitHub 匿名 API 限流正好是 60 次/小时/IP，1 分钟一轮刚好踩在边界上；
	// 若同 IP 还有别的请求、或代理失败后回源直连多打一次，就可能 403。
	// 403 时 getLatestRelease 失败会被当作「已是最新」，需要时调大到 5~10 分钟。
	CheckInterval = 1 * time.Minute
	apiLatestURL  = "https://api.github.com/repos/%s/releases/latest"
	// 国内直连 api.github.com 实测约 3.8s 且常超时；gh-proxy.com 实测约 0.75s 且能正确返回 JSON。
	apiProxyURL = "https://gh-proxy.com/https://api.github.com/repos/%s/releases/latest"
)

// defaultDownloadMirrors 是内嵌的默认下载镜像；配置里显式给了 version.mirrors 时优先用配置。
// 顺序来自 2026-11-08 实测（Range 取 1MB）：gh-proxy.org / ghfast.top 吞吐最好，
// githubproxy.cc / gh-proxy.com 备用，GitHub 官方最后兜底。
// 已移除 ghproxy.com：实测 15s 超时不可达，留着只会让每次更新白等一轮。
// 每个模板支持占位符：{url}=原始 GitHub 下载地址；{tag}/{file}=便于自建或 Gitee 类站点。
var defaultDownloadMirrors = []string{
	"https://gh-proxy.org/{url}",
	"https://ghfast.top/{url}",
	"https://githubproxy.cc/{url}",
	"https://gh-proxy.com/{url}",
	"{url}",
}

// maxMirrorAttempts 单轮更新最多尝试的镜像数量（避免极端情况下无限尝试）。
const maxMirrorAttempts = 6

// 发布包中主程序的文件名，Release 里有多个 exe 时优先选它
const preferredExeName = "OnePoint.exe"

// apiClient 用于请求 GitHub API：每个候选地址最多 8s，失败立即切下一个（代理优先、直连兜底）
var apiClient = &http.Client{Timeout: 8 * time.Second}

// downloadClient 用于下载更新包：不设整体超时（大文件可能较慢），只限制建连与 TLS 握手
var downloadClient = &http.Client{
	Transport: &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	},
}

type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type GitHubRelease struct {
	TagName    string        `json:"tag_name"`
	Name       string        `json:"name"`
	Prerelease bool          `json:"prerelease"`
	Assets     []GitHubAsset `json:"assets"`
}

// ReleaseInfo 是给前端展示用的一条版本更新日志。
// 数据源为编译进 exe 的 CHANGELOG.md（离线可用），不依赖网络。
type ReleaseInfo struct {
	Tag         string `json:"tag"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
}

// downloadedPath 保存已下载更新包的路径。轮询下载协程与用户点击重启可能并发访问，
// 用 atomic.Value 代替裸 string，避免数据竞争。
var downloadedPath atomic.Value

var (
	checkRunning atomic.Bool // 是否正在做版本检查：防止启动检查与高频轮询重叠
	downloading  atomic.Bool // 是否正在下载：防止为同一个包开多个下载任务
	updateReady  atomic.Bool // 已下载完成、等待重启：不再重复检查或下载
)

func newAPIRequest(url string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// GitHub API 要求带 User-Agent，否则可能被拒绝
	req.Header.Set("User-Agent", "order_server-updater/"+global.Version)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return req, nil
}

// getLatestRelease 依次尝试内嵌的 API 地址：国内优先走 gh-proxy 代理，失败再直连 GitHub。
// 任一地址返回合法 release 即结束；全部失败才把最后一个错误交给上层。
func getLatestRelease() (*GitHubRelease, error) {
	urls := []string{
		fmt.Sprintf(apiProxyURL, Repo),
		fmt.Sprintf(apiLatestURL, Repo),
	}
	var lastErr error
	for _, url := range urls {
		release, err := fetchLatestRelease(url)
		if err == nil {
			return release, nil
		}
		lastErr = err
		logrus.Warnf("版本检查地址不可用 (%s): %v，尝试下一个", url, err)
	}
	return nil, lastErr
}

func fetchLatestRelease(url string) (*GitHubRelease, error) {
	req, err := newAPIRequest(url)
	if err != nil {
		return nil, err
	}
	// 代理可能有缓存，显式要求回源，避免拿到过期的 latest 导致漏更新
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := apiClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	if release.TagName == "" {
		return nil, fmt.Errorf("release 响应缺少 tag_name")
	}
	return &release, nil
}

// headingRe 匹配 CHANGELOG.md 的版本标题行，日期与标题均可选：
// "## v10.0.10 - 2026-11-08 顶栏更新日志入口"
// 捕获组：1=版本号，2=发布日期，3=版本标题（没有则为空）。
var headingRe = regexp.MustCompile(`^##\s+v?(\d+(?:\.\d+)+(?:[-.][0-9A-Za-z]+)*)\s*(?:-\s*(\d{4}-\d{2}-\d{2}))?\s*(.*)$`)

// ParseChangelog 解析 CHANGELOG.md 全文，按版本标题切分成倒序列表（文件里最新在前）。
// 文件开头的「格式约定」说明、分隔线 --- 等都在第一个版本标题之前，会被自动跳过。
func ParseChangelog(md string) []ReleaseInfo {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	lines := strings.Split(md, "\n")

	var list []ReleaseInfo
	var cur *ReleaseInfo
	var body []string
	flush := func() {
		if cur == nil {
			return
		}
		cur.Body = strings.TrimSpace(strings.Join(body, "\n"))
		list = append(list, *cur)
		cur = nil
		body = nil
	}

	for _, line := range lines {
		if m := headingRe.FindStringSubmatch(strings.TrimRight(line, " \t")); m != nil {
			flush()
			// 标题可写作 "... - 2026-11-08 版本标题"；没有标题时留空，
			// 避免界面出现与版本号重复的 "OnePoint v10.0.10" 占位行。
			title := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(m[3]), "-–—|·"))
			info := ReleaseInfo{
				Tag:         m[1],
				Name:        title,
				PublishedAt: m[2],
			}
			cur = &info
			continue
		}
		if cur == nil {
			continue // 跳过文件头说明
		}
		if strings.TrimSpace(line) == "---" {
			continue // 段落分隔线不进正文
		}
		body = append(body, line)
	}
	flush()
	return list
}

// GetChangelog 返回编译进 exe 的全部版本更新日志，供前端「版本更新历史」展示。
// 离线可用、零网络请求；embeddedChangelog 由 main 包用 go:embed 注入（见 SetChangelog）。
func GetChangelog() []ReleaseInfo {
	mu.RLock()
	defer mu.RUnlock()
	return ParseChangelog(embeddedChangelog)
}

var (
	mu                sync.RWMutex
	embeddedChangelog string
)

// SetChangelog 由 main 包在启动时注入 go:embed 的 CHANGELOG.md 内容。
func SetChangelog(md string) {
	mu.Lock()
	defer mu.Unlock()
	embeddedChangelog = md
}

// isNewerVersion 按数字逐段比较语义化版本，判断 latest 是否真的比 current 新。
// 支持可选的 v 前缀和 -beta / +build 后缀；无法解析时退回字符串比较。
func isNewerVersion(latest, current string) bool {
	parse := func(v string) ([]int, bool) {
		v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
		if i := strings.IndexAny(v, "-+"); i >= 0 {
			v = v[:i]
		}
		if v == "" {
			return nil, false
		}
		parts := strings.Split(v, ".")
		nums := make([]int, 0, len(parts))
		for _, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil {
				return nil, false
			}
			nums = append(nums, n)
		}
		return nums, true
	}

	a, okA := parse(latest)
	b, okB := parse(current)
	if !okA || !okB {
		return strings.TrimSpace(latest) != strings.TrimSpace(current)
	}
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	for i := 0; i < maxLen; i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// StartPeriodicCheck 启动后台版本轮询：先立即检查一次，之后每 interval 检查一次。
// 发现新版本由 CheckUpdate 自动异步下载，下载完成只推事件给前端，不弹系统窗口。
// interval <= 0 时回退到内嵌的 CheckInterval；应用退出时协程自然结束。
// 每轮检查放到独立协程：网络慢（API 候选各 8s 超时）时不会卡住 ticker，保证按固定间隔触发；
// CheckUpdate 内部的 checkRunning 保证同一时刻只有一轮真正在跑，重叠的轮次直接跳过。
func StartPeriodicCheck(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = CheckInterval
	}
	go CheckUpdate(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			go CheckUpdate(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// CheckUpdate 在每次启动时检查 GitHub 上的最新版本：
//   - 没有更新 / 网络异常 / 无适用安装包时静默结束，不弹任何窗口；
//   - 发现新版本则把状态推给前端顶栏，并立即在后台异步下载，
//     不阻塞主界面、不强制用户更新；下载完成后由前端提示“重启更新”。
func CheckUpdate(ctx context.Context) {
	// 已下载完成等待重启、正在下载、或上一轮检查尚未结束：静默跳过，避免重复下载与顶栏状态抖动
	if updateReady.Load() || downloading.Load() || !checkRunning.CompareAndSwap(false, true) {
		return
	}
	defer checkRunning.Store(false)

	// 仓库地址来自内嵌常量，不依赖外部配置；留一行日志便于确认检查确实跑过
	logrus.Infof("开始检查版本更新: repo=%s, 当前=%s", Repo, global.Version)
	wailsruntime.EventsEmit(ctx, "update_checking")

	release, err := getLatestRelease()
	if err != nil {
		logrus.Warnf("版本检查失败: %v", err)
		wailsruntime.EventsEmit(ctx, "update_uptodate", global.Version)
		return
	}

	// 只有语义化版本确实更新时才提示，避免旧 tag 反复弹窗
	if !isNewerVersion(release.TagName, global.Version) {
		logrus.Debugf("当前已是最新版本: %s", global.Version)
		wailsruntime.EventsEmit(ctx, "update_uptodate", global.Version)
		return
	}

	// 没有可下载的安装包就不提示，否则用户下载只会报错
	if findAsset(release.Assets) == nil {
		logrus.Warnf("发现新版本 %s，但发布包中没有适用于 %s 的安装文件，跳过", release.TagName, goruntime.GOOS)
		wailsruntime.EventsEmit(ctx, "update_uptodate", global.Version)
		return
	}

	logrus.Infof("发现新版本: %s -> %s，后台开始自动下载", global.Version, release.TagName)
	wailsruntime.EventsEmit(ctx, "update_available", release.TagName, release.Name)
	// 不弹窗、不阻塞：后台异步下载，进度由 download_progress 事件推给前端。
	go DownloadUpdate(ctx)
}

func DownloadUpdate(ctx context.Context) {
	// 已完成或正在下载时跳过：高频轮询会重复触发检查，不能并发下载同一个包
	if updateReady.Load() || !downloading.CompareAndSwap(false, true) {
		return
	}
	defer downloading.Store(false)

	release, err := getLatestRelease()
	if err != nil {
		msg := fmt.Sprintf("获取发布信息失败: %v", err)
		logrus.Errorf("版本更新下载: %s", msg)
		wailsruntime.EventsEmit(ctx, "download_error", msg)
		return
	}

	asset := findAsset(release.Assets)
	if asset == nil {
		msg := "未找到适用于当前系统的更新包"
		logrus.Errorf("版本更新下载: %s", msg)
		wailsruntime.EventsEmit(ctx, "download_error", msg)
		return
	}

	// 镜像优先取配置里的自定义值，缺省时用内嵌默认；global.GetConfig() 可能为 nil。
	mirrors := defaultDownloadMirrors
	if global.GetConfig() != nil && len(global.GetConfig().Version.Mirrors) > 0 {
		mirrors = global.GetConfig().Version.Mirrors
	}
	candidates := buildCandidateURLs(mirrors, asset.BrowserDownloadURL, release.TagName, asset.Name)
	if len(candidates) > maxMirrorAttempts {
		candidates = candidates[:maxMirrorAttempts]
	}

	for i, url := range candidates {
		logrus.Infof("尝试从镜像下载更新 (%d/%d): %s", i+1, len(candidates), url)
		// 每个镜像开始时重置进度条，避免上一个失败镜像的残留数值。
		wailsruntime.EventsEmit(ctx, "download_progress", int64(0), asset.Size, 0.0, 0.0)
		if tryDownload(ctx, url, asset) {
			return
		}
		logrus.Warnf("镜像下载失败，尝试下一个: %s", url)
	}

	msg := "所有下载镜像均失败，请检查网络或稍后重试"
	logrus.Errorf("版本更新下载: %s", msg)
	wailsruntime.EventsEmit(ctx, "download_error", msg)
}

// buildCandidateURLs 把镜像模板展开成真实下载地址。
// {url} 用原始 GitHub 下载地址替换（代理类镜像用）；{tag}/{file} 用于自建或 Gitee 类站点。
func buildCandidateURLs(mirrors []string, assetURL, tag, file string) []string {
	templates := mirrors
	if len(templates) == 0 {
		templates = defaultDownloadMirrors
	}
	out := make([]string, 0, len(templates))
	for _, t := range templates {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		u := strings.ReplaceAll(t, "{url}", assetURL)
		u = strings.ReplaceAll(u, "{tag}", tag)
		u = strings.ReplaceAll(u, "{file}", file)
		out = append(out, u)
	}
	return out
}

// tryDownload 从单个镜像地址完整下载更新包，成功返回 true。
// 任何一步失败只记录日志并返回 false，由上层切换到下一个镜像，不再单独弹错误窗。
func tryDownload(ctx context.Context, url string, asset *GitHubAsset) bool {
	req, err := newAPIRequest(url)
	if err != nil {
		logrus.Errorf("构造下载请求失败: %v", err)
		return false
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		logrus.Errorf("下载请求失败: %v", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		logrus.Errorf("下载请求返回状态码 %d", resp.StatusCode)
		return false
	}

	downloadPath := filepath.Join(os.TempDir(), asset.Name)

	out, err := os.Create(downloadPath)
	if err != nil {
		logrus.Errorf("创建下载文件失败: %v", err)
		return false
	}

	totalSize := resp.ContentLength
	if totalSize <= 0 {
		totalSize = asset.Size
	}

	reader := &progressReader{
		reader:    resp.Body,
		total:     totalSize,
		ctx:       ctx,
		startTime: time.Now(),
		lastEmit:  time.Now(),
	}

	_, copyErr := io.Copy(out, reader)
	closeErr := out.Close()
	if copyErr != nil {
		logrus.Errorf("下载写入失败: %v", copyErr)
		return false
	}
	if closeErr != nil {
		logrus.Errorf("保存下载文件失败: %v", closeErr)
		return false
	}

	// 校验文件大小，避免把不完整的可执行文件替换上去
	if asset.Size > 0 {
		info, statErr := os.Stat(downloadPath)
		if statErr != nil || info.Size() != asset.Size {
			logrus.Errorf("下载文件不完整（期望 %d 字节），已取消更新", asset.Size)
			return false
		}
	}

	// 校验 SHA256：尝试从同一镜像拉取 <file>.sha256，存在则比对，防止代理被篡改投毒。
	if !verifyChecksum(ctx, url, downloadPath) {
		return false
	}

	downloadedPath.Store(downloadPath)
	updateReady.Store(true)
	elapsed := time.Since(reader.startTime).Seconds()
	speedMB := float64(reader.current) / elapsed / 1024 / 1024
	logrus.Infof("更新下载完成: %s (耗时 %.1fs, 平均速度 %.2f MB/s)", downloadPath, elapsed, speedMB)
	wailsruntime.EventsEmit(ctx, "download_complete", downloadPath)
	return true
}

// verifyChecksum 尝试下载同名 .sha256 校验和文件并比对；文件不存在/无法获取时跳过（不影响更新）。
func verifyChecksum(ctx context.Context, assetURL, filePath string) bool {
	sumURL := assetURL + ".sha256"
	req, err := newAPIRequest(sumURL)
	if err != nil {
		return true
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		logrus.Warnf("获取校验和失败，跳过 SHA256 校验: %v", err)
		return true
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logrus.Warnf("校验和文件不存在(状态码 %d)，跳过 SHA256 校验", resp.StatusCode)
		return true
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil {
		logrus.Warnf("读取校验和失败，跳过 SHA256 校验: %v", err)
		return true
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		logrus.Warnf("校验和文件内容为空，跳过 SHA256 校验")
		return true
	}
	expected := strings.ToLower(fields[0])

	actual, err := sha256File(filePath)
	if err != nil {
		logrus.Warnf("计算本地校验和失败，跳过 SHA256 校验: %v", err)
		return true
	}
	if actual != expected {
		msg := fmt.Sprintf("SHA256 校验失败（期望 %s，实际 %s），疑似文件被篡改，已取消更新", expected, actual)
		logrus.Errorf("版本更新下载: %s", msg)
		wailsruntime.EventsEmit(ctx, "download_error", "下载文件校验失败（可能被篡改），已取消更新")
		return false
	}
	logrus.Infof("SHA256 校验通过")
	return true
}

// sha256File 计算文件 SHA256 的十六进制字符串。
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// updateBatContent 是 Windows 下替换并重启 exe 的脚本，内容必须保持纯 ASCII。
// 原因：cmd.exe 按系统 ANSI 代码页（中文 Windows 为 GBK）解析 .bat 文件内容，
// 而 Go 写出的是 UTF-8；一旦把含中文的安装路径写进文件，路径会变乱码，
// move/start 全部失败，表现为「点击重启后程序关闭但不再启动」。
// 所以路径一律通过命令行参数 %1/%2 传入（CreateProcessW 走 UTF-16，无编码问题）。
// 同时把每一步写进 %TEMP%\order_server_update.log，方便用户机器出问题时排查。
const updateBatContent = `@echo off
setlocal enabledelayedexpansion
set "NEW=%~1"
set "OLD=%~2"
set "BAK=%~2.bak"
set "LOG=%TEMP%\order_server_update.log"
set /a TRIES=0
echo [%DATE% %TIME%] update start new=%NEW% old=%OLD% > "%LOG%"
timeout /t 3 /nobreak >nul
if exist "%BAK%" del /F /Q "%BAK%" >nul 2>&1

:WAIT_EXIT
move /Y "%OLD%" "%BAK%" >nul 2>&1
if not exist "%OLD%" goto REPLACE
set /a TRIES+=1
if !TRIES! GEQ 20 goto FAILED
timeout /t 1 /nobreak >nul
goto WAIT_EXIT

:REPLACE
move /Y "%NEW%" "%OLD%" >> "%LOG%" 2>&1
if errorlevel 1 (
  move /Y "%BAK%" "%OLD%" >nul 2>&1
  echo [%DATE% %TIME%] replace FAILED, original restored >> "%LOG%"
  echo Update failed, the original version has been restored.
  pause
  exit /b 1
)
echo [%DATE% %TIME%] launching: %OLD% >> "%LOG%"
start "" /D "%~dp2" "%OLD%"
del /F /Q "%BAK%" >nul 2>&1
del /F /Q "%NEW%" >nul 2>&1
echo [%DATE% %TIME%] done >> "%LOG%"
goto DONE

:FAILED
echo [%DATE% %TIME%] FAILED: old exe still locked after !TRIES! tries >> "%LOG%"
echo Failed to replace the executable. Please close the app and update manually.
pause
exit /b 1

:DONE
del "%~f0"
`

func RestartApplication(ctx context.Context) {
	downloadedFilePath, _ := downloadedPath.Load().(string)
	if downloadedFilePath == "" {
		wailsruntime.EventsEmit(ctx, "download_error", "没有已下载的更新文件")
		return
	}

	exePath, err := os.Executable()
	if err != nil {
		msg := fmt.Sprintf("获取当前程序路径失败: %v", err)
		logrus.Errorf("应用重启: %s", msg)
		wailsruntime.EventsEmit(ctx, "download_error", msg)
		return
	}

	// Windows 下正在运行的 exe 不能直接覆盖：先等进程退出，把旧文件改名，
	// 再把新文件移动到位；失败时自动回滚，避免把程序弄坏。
	// 脚本内容见包级常量 updateBatContent（纯 ASCII，路径通过命令行参数传入）。
	batContent := updateBatContent

	batPath := filepath.Join(os.TempDir(), "order_server_update.bat")
	err = os.WriteFile(batPath, []byte(batContent), 0666)
	if err != nil {
		msg := fmt.Sprintf("创建重启脚本失败: %v", err)
		logrus.Errorf("应用重启: %s", msg)
		wailsruntime.EventsEmit(ctx, "download_error", msg)
		return
	}

	logrus.Infof("开始重启应用, 更新文件: %s -> %s，脚本日志: %s", downloadedFilePath, exePath, filepath.Join(os.TempDir(), "order_server_update.log"))
	// 路径作为命令行参数传入（UTF-16），绝不写进 bat 内容，避免中文路径被 GBK 解析成乱码
	cmd := exec.Command("cmd", "/c", batPath, downloadedFilePath, exePath)
	if err := cmd.Start(); err != nil {
		msg := fmt.Sprintf("启动更新脚本失败: %v", err)
		logrus.Errorf("应用重启: %s", msg)
		wailsruntime.EventsEmit(ctx, "download_error", msg)
		return
	}
	os.Exit(0)
}

func findAsset(assets []GitHubAsset) *GitHubAsset {
	// 优先使用与主程序同名的文件，避免 Release 里有多个 exe 时下错包
	if goruntime.GOOS == "windows" {
		for i := range assets {
			if strings.EqualFold(assets[i].Name, preferredExeName) {
				return &assets[i]
			}
		}
	}
	for i := range assets {
		name := assets[i].Name
		if goruntime.GOOS == "windows" && filepath.Ext(name) == ".exe" {
			return &assets[i]
		}
		if goruntime.GOOS == "darwin" && (filepath.Ext(name) == ".app" || filepath.Ext(name) == ".dmg") {
			return &assets[i]
		}
		if goruntime.GOOS == "linux" && filepath.Ext(name) == "" && !hasExt(name) {
			return &assets[i]
		}
	}
	return nil
}

func hasExt(name string) bool {
	ext := filepath.Ext(name)
	return ext != "" && ext != name
}

type progressReader struct {
	reader    io.Reader
	total     int64
	current   int64
	ctx       context.Context
	startTime time.Time
	lastEmit  time.Time
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.current += int64(n)

	reachedEnd := pr.total > 0 && pr.current >= pr.total
	if time.Since(pr.lastEmit) > 200*time.Millisecond || reachedEnd {
		pr.lastEmit = time.Now()
		percentage := 0.0
		if pr.total > 0 {
			percentage = float64(pr.current) / float64(pr.total) * 100
		}
		elapsed := time.Since(pr.startTime).Seconds()
		speed := 0.0
		if elapsed > 0 {
			speed = float64(pr.current) / elapsed / 1024 / 1024
		}
		wailsruntime.EventsEmit(pr.ctx, "download_progress", pr.current, pr.total, percentage, speed)
	}

	return n, err
}
