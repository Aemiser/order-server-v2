package wechatapi

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// 本文件不带 build tag，默认构建与 -tags wechat 构建都会跑。
// 降级路径必须给出明确错误，绝不能静默返回空结果。

func TestDisabledContract(t *testing.T) {
	if Enabled {
		t.Skip("本构建链入了真实微信能力，降级契约不适用")
	}

	if _, err := Export(Options{Group: "x", Days: 1}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Export 应返回 ErrDisabled，实际 %v", err)
	}
	if _, err := ResolveAndCacheKey(Options{}, nil); !errors.Is(err, ErrDisabled) {
		t.Fatalf("ResolveAndCacheKey 应返回 ErrDisabled，实际 %v", err)
	}
	if err := ClearKeyCache(); !errors.Is(err, ErrDisabled) {
		t.Fatalf("ClearKeyCache 应返回 ErrDisabled，实际 %v", err)
	}
	if got := Detect(); got["success"] != false || !strings.Contains(asString(got["message"]), "wechat") {
		t.Fatalf("Detect 应返回可读的降级状态，实际 %v", got)
	}
	if got := KeyCacheInfo(); got["exists"] != false {
		t.Fatalf("KeyCacheInfo 降级时必须 exists=false，实际 %v", got)
	}
	if got := KeyDropStatus(); got["writable"] != false || asString(got["message"]) == "" {
		t.Fatalf("KeyDropStatus 降级状态不完整，实际 %v", got)
	}
	if got := KeyDropDir(); got != "" {
		t.Fatalf("KeyDropDir 降级时必须为空串，实际 %q", got)
	}
	// 空操作不能 panic，也不能回调。
	StartKeyDropWatcher("", func(string) { t.Fatal("降级构建不应触发投放回调") })
}

func TestErrDisabledIsActionable(t *testing.T) {
	if !strings.Contains(ErrDisabled.Error(), "-tags wechat") {
		t.Fatal("ErrDisabled 必须告诉用户如何重新构建")
	}
	if DisabledHint == "" {
		t.Fatal("DisabledHint 不能为空")
	}
}

// Options 的字段在两种构建下都必须完整；漏字段会让调用方编译失败，
// 这里额外断言几个容易被转换层吞掉的值。
func TestOptionsFieldRoundTripShape(t *testing.T) {
	o := Options{
		Group: "群A", Days: 3, StartDate: "2026-11-01", EndDate: "2026-11-03",
		DataDir: "D:/wx", DataKey: "ab", Platform: "windows", Version: 4,
		AutoKey: true, ProviderTimeout: 90 * time.Second,
		AutoKeyOnFailure: true, WCDBLibPath: "wc.dll", SessionID: "sid",
	}
	if o.ProviderTimeout != 90*time.Second || o.Version != 4 || !o.AutoKeyOnFailure {
		t.Fatal("Options 字段读写异常")
	}
}

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}
