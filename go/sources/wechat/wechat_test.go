package wechatsource

import (
	"context"
	"errors"
	"strings"
	"testing"

	"order_server/go/ports"
	"order_server/go/wechatapi"
)

// stubExport 捕获真正传给门面的 Options，避免测试依赖微信数据库或构建标签。
func stubExport(capture *wechatapi.Options, msgs []wechatapi.Message, err error) ExportFunc {
	return func(o wechatapi.Options) (wechatapi.Result, error) {
		if capture != nil {
			*capture = o
		}
		return wechatapi.Result{Messages: msgs}, err
	}
}

func TestNew_Defaults(t *testing.T) {
	s := New(wechatapi.Options{Group: "群A"})
	if got := s.Name(); got != "wechat" {
		t.Fatalf("来源名应为 wechat，实际 %q", got)
	}
	if s.opts.Platform != "windows" || s.opts.Days != 2 {
		t.Fatalf("默认值应沿用既有链路（windows / 2 天），实际 %q / %d", s.opts.Platform, s.opts.Days)
	}
	if s.export == nil {
		t.Fatal("生产导出函数不应为空")
	}
	custom := New(wechatapi.Options{Platform: "linux", Days: 9})
	if custom.opts.Platform != "linux" || custom.opts.Days != 9 {
		t.Fatal("显式配置不应被默认值覆盖")
	}
}

// 锁住从 schemaservice 旧 wechatSource 搬来的群名校验：空群名要先报配置提示，
// 且不得发起导出（否则默认构建下会拿到 ErrDisabled，掩盖真正的配置问题）。
func TestExport_EmptyGroupRejectedWithoutCallingExporter(t *testing.T) {
	s := New(wechatapi.Options{})
	called := false
	s.export = func(wechatapi.Options) (wechatapi.Result, error) {
		called = true
		return wechatapi.Result{}, nil
	}
	_, err := s.Export(context.Background(), ports.ExportOptions{})
	if err == nil || !strings.Contains(err.Error(), "未配置要导出的群聊名称") {
		t.Fatalf("空群名应给出配置提示，实际 %v", err)
	}
	if called {
		t.Fatal("群名为空时不应调用导出")
	}
}

// 锁住 Days 推导口径：显式日期窗口优先，非法格式 / 倒置区间保留构造默认值。
func TestExport_DaysFromWindow(t *testing.T) {
	cases := []struct {
		name      string
		since     string
		until     string
		wantDays  int
		wantStart string
		wantEnd   string
	}{
		{"同日", "2026-11-10", "2026-11-10", 1, "2026-11-10", "2026-11-10"},
		{"三天窗口", "2026-11-08", "2026-11-10", 3, "2026-11-08", "2026-11-10"},
		{"非法格式保留默认", "2026/11/08", "2026-11-10", 7, "2026/11/08", "2026-11-10"},
		{"区间倒置保留默认", "2026-11-10", "2026-11-08", 7, "2026-11-10", "2026-11-08"},
		{"只给起始保留默认", "2026-11-08", "", 7, "2026-11-08", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got wechatapi.Options
			s := New(wechatapi.Options{Group: "群A", Days: 7})
			s.export = stubExport(&got, nil, nil)
			if _, err := s.Export(context.Background(), ports.ExportOptions{Since: tc.since, Until: tc.until}); err != nil {
				t.Fatalf("导出失败: %v", err)
			}
			if got.Days != tc.wantDays {
				t.Fatalf("Days 期望 %d，实际 %d", tc.wantDays, got.Days)
			}
			if got.StartDate != tc.wantStart || got.EndDate != tc.wantEnd {
				t.Fatalf("日期窗口期望 %q~%q，实际 %q~%q", tc.wantStart, tc.wantEnd, got.StartDate, got.EndDate)
			}
		})
	}
}

// DataDir 只在显式开启时覆盖：模板工作台的 DataDir 语义是「文件数据源路径」，
// 选了 wechat 源却填了路径，不能把文件路径错当微信数据目录。
func TestExport_DataDirOverrideIsGated(t *testing.T) {
	base := wechatapi.Options{Group: "群A", DataDir: "/base/wechat"}
	opt := ports.ExportOptions{DataDir: "/run/orders.csv", ChatID: "指定群"}

	var defaultGot wechatapi.Options
	s := New(base)
	s.export = stubExport(&defaultGot, nil, nil)
	if _, err := s.Export(context.Background(), opt); err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if defaultGot.DataDir != "/base/wechat" {
		t.Fatalf("默认不应被运行参数覆盖，实际 %q", defaultGot.DataDir)
	}
	if defaultGot.Group != "指定群" {
		t.Fatalf("ChatID 仍应覆盖群名，实际 %q", defaultGot.Group)
	}

	var allowedGot wechatapi.Options
	s2 := New(base).WithDataDirOverride(true)
	s2.export = stubExport(&allowedGot, nil, nil)
	if _, err := s2.Export(context.Background(), opt); err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if allowedGot.DataDir != "/run/orders.csv" {
		t.Fatalf("开启后应允许覆盖，实际 %q", allowedGot.DataDir)
	}
}

// 导出错误统一包装成用户可见文案，同时保留 errors.Is 可判定性。
func TestExport_ErrorWrappedButUnwrapKept(t *testing.T) {
	sentinel := errors.New("boom")
	s := New(wechatapi.Options{Group: "群A"})
	s.export = stubExport(nil, nil, sentinel)
	_, err := s.Export(context.Background(), ports.ExportOptions{})
	if err == nil || !strings.HasPrefix(err.Error(), "导出聊天记录失败: ") {
		t.Fatalf("错误应带用户可见前缀，实际 %v", err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatal("包装后应仍能用 errors.Is 判定原始错误")
	}
}

func TestExport_MessageMappingAndEmptyResult(t *testing.T) {
	msgs := []wechatapi.Message{
		{ID: "m1", Sender: "张三", Content: "【示例5斤】", Time: "2026-11-10T10:00:00+08:00"},
		{ID: "m2", Sender: "李四", Content: "地址：示例路1号", Time: "2026-11-10T10:01:00+08:00"},
	}
	s := New(wechatapi.Options{Group: "群A"})
	s.export = stubExport(nil, msgs, nil)
	out, err := s.Export(context.Background(), ports.ExportOptions{})
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("消息数期望 2，实际 %d", len(out.Messages))
	}
	if out.Messages[0].ID != "m1" || out.Messages[0].Sender != "张三" ||
		out.Messages[0].Content != "【示例5斤】" || out.Messages[0].Time != msgs[0].Time {
		t.Fatalf("消息字段映射不符: %+v", out.Messages[0])
	}

	s.export = stubExport(nil, nil, nil)
	empty, err := s.Export(context.Background(), ports.ExportOptions{})
	if err != nil || empty == nil || len(empty.Messages) != 0 {
		t.Fatalf("空结果应返回非 nil 的空切片，实际 %+v / %v", empty, err)
	}
}

func TestExport_CancelledContextSkipsExporter(t *testing.T) {
	s := New(wechatapi.Options{Group: "群A"})
	called := false
	s.export = func(wechatapi.Options) (wechatapi.Result, error) {
		called = true
		return wechatapi.Result{}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Export(ctx, ports.ExportOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消应返回 context.Canceled，实际 %v", err)
	}
	if called {
		t.Fatal("已取消时不应发起导出")
	}
}

// 每次 Export 都从构造时的基础配置复制，避免上一次运行的群名 / 日期残留。
func TestExport_DoesNotMutateBaseOptions(t *testing.T) {
	var first, second wechatapi.Options
	s := New(wechatapi.Options{Group: "默认群", Days: 2})
	s.export = stubExport(&first, nil, nil)
	if _, err := s.Export(context.Background(), ports.ExportOptions{ChatID: "临时群", Since: "2026-11-08", Until: "2026-11-10"}); err != nil {
		t.Fatalf("首次导出失败: %v", err)
	}
	s.export = stubExport(&second, nil, nil)
	if _, err := s.Export(context.Background(), ports.ExportOptions{}); err != nil {
		t.Fatalf("二次导出失败: %v", err)
	}
	if second.Group != "默认群" || second.Days != 2 {
		t.Fatalf("基础配置被污染: %+v", second)
	}
	if first.Group != "临时群" || first.Days != 3 {
		t.Fatalf("首次运行参数未生效: %+v", first)
	}
}
