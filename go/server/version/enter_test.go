package version

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"1.0.0", "10.0.1", false}, // 旧 tag 不能触发误报
		{"10.0.1", "10.0.1", false},
		{"10.0.2", "10.0.1", true},
		{"v10.0.2", "10.0.1", true},
		{"10.1.0", "10.0.9", true},
		{"10.0.10", "10.0.9", true},
		{"9.9.9", "10.0.1", false},
		{"10.0.1-beta.1", "10.0.1", false},
		{"dev", "10.0.1", true},
	}
	for _, c := range cases {
		if got := isNewerVersion(c.latest, c.current); got != c.want {
			t.Errorf("isNewerVersion(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestFindAssetPrefersMainExe(t *testing.T) {
	assets := []GitHubAsset{
		{Name: "installer.exe", Size: 1},
		{Name: "OnePoint.exe", Size: 2},
	}
	if got := findAsset(assets); got == nil || got.Name != "OnePoint.exe" {
		t.Fatalf("findAsset should prefer OnePoint.exe, got %+v", got)
	}
}

// 防止以后有人把更新链路重新接回外部配置：仓库/间隔/默认镜像必须始终内嵌可用。
func TestEmbeddedUpdateConfig(t *testing.T) {
	if Repo == "" || Repo == "your-username/your-repo" {
		t.Fatalf("内嵌仓库地址无效: %q", Repo)
	}
	// 间隔下限 1 分钟：GitHub 匿名 API 限流 60 次/小时/IP，再快就会 403 被误判成「已是最新」。
	if CheckInterval < time.Minute {
		t.Fatalf("轮询间隔过短，会触发 GitHub API 匿名限流: %v", CheckInterval)
	}
	urls := buildCandidateURLs(nil, "https://github.com/Aemiser/order_manager/releases/download/10.0.6/OnePoint.exe", "10.0.6", "OnePoint.exe")
	if len(urls) != len(defaultDownloadMirrors) {
		t.Fatalf("内嵌镜像未生效: got %d urls, want %d", len(urls), len(defaultDownloadMirrors))
	}
}

// 防止把已失效的镜像或错误的 API 代理格式带进发布包。
func TestMirrorAndAPIConfig(t *testing.T) {
	if len(defaultDownloadMirrors) < 2 {
		t.Fatal("下载镜像至少要有一个加速代理 + 官方兜底")
	}
	for _, m := range defaultDownloadMirrors {
		if strings.Contains(m, "//ghproxy.com") {
			t.Fatalf("已失效的镜像仍在默认列表: %s", m)
		}
		if !strings.Contains(m, "{url}") {
			t.Fatalf("镜像模板缺少 {url} 占位符: %s", m)
		}
	}
	proxy := fmt.Sprintf(apiProxyURL, Repo)
	if !strings.HasPrefix(proxy, "https://gh-proxy.com/https://api.github.com/repos/"+Repo) {
		t.Fatalf("API 代理地址格式不对: %s", proxy)
	}
}

// 中文 Windows 的 cmd 按 GBK 解析 .bat，UTF-8 写出的中文路径会乱码，
// 导致「点击重启后程序关闭但不再启动」。脚本内容必须纯 ASCII，路径走命令行参数。
func TestUpdateBatIsASCII(t *testing.T) {
	for i, b := range []byte(updateBatContent) {
		if b > 127 {
			t.Fatalf("重启脚本含非 ASCII 字节（偏移 %d, 值 %d）：路径必须走命令行参数", i, b)
		}
	}
	for _, want := range []string{"%~1", "%~2", `/D "%~dp2"`, "order_server_update.log"} {
		if !strings.Contains(updateBatContent, want) {
			t.Fatalf("重启脚本缺少关键片段 %q", want)
		}
	}
}
