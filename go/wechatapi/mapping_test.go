//go:build wechat

package wechatapi

import (
	"reflect"
	"testing"
	"time"

	"order_server/go/wechatlog"
)

// 真实构建下最容易出的错是「转换层漏字段」：Options 新增字段后
// enabled.go 忘了同步，功能会静默退化。这里逐字段比对兜底。

func TestLogOptionsMapsEveryField(t *testing.T) {
	o := Options{
		Group: "群A", Senders: "张三,李四", Days: 3,
		EndDate: "2026-11-03", StartDate: "2026-11-01",
		DataDir: "D:/wx", WorkDir: "D:/work", DataKey: "aabb",
		Platform: "windows", Version: 4, AutoKey: true,
		KeyProviderPath: "kp.exe", ProviderTimeout: 90 * time.Second,
		AutoKeyOnFailure: true, WCDBLibPath: "wc.dll", SessionID: "sid",
	}
	want := wechatlog.Options{
		Group: "群A", Senders: "张三,李四", Days: 3,
		EndDate: "2026-11-03", StartDate: "2026-11-01",
		DataDir: "D:/wx", WorkDir: "D:/work", DataKey: "aabb",
		Platform: "windows", Version: 4, AutoKey: true,
		KeyProviderPath: "kp.exe", ProviderTimeout: 90 * time.Second,
		AutoKeyOnFailure: true, WCDBLibPath: "wc.dll", SessionID: "sid",
	}
	if got := logOptions(o); !reflect.DeepEqual(got, want) {
		t.Fatalf("Options 映射不等价\n got: %+v\nwant: %+v", got, want)
	}
}

func TestAPIResultMapsEveryField(t *testing.T) {
	in := wechatlog.Result{
		Source: "wcdb", DataDir: "D:/wx", WorkDir: "D:/work", Version: 4,
		KeySource: "缓存", KeyCachePath: "k.json",
		From: "2026-11-01", To: "2026-11-03", EndDate: "2026-11-03",
		Warnings: []string{"w1"},
		Messages: []wechatlog.Message{{
			ID: "1", Sender: "张三", Content: "不要了", Quote: "原单",
			QuoteSender: "李四", Time: "10:00", Date: "2026-11-03", Ts: 123.5,
		}},
	}
	want := Result{
		Source: "wcdb", DataDir: "D:/wx", WorkDir: "D:/work", Version: 4,
		KeySource: "缓存", KeyCachePath: "k.json",
		From: "2026-11-01", To: "2026-11-03", EndDate: "2026-11-03",
		Warnings: []string{"w1"},
		Messages: []Message{{
			ID: "1", Sender: "张三", Content: "不要了", Quote: "原单",
			QuoteSender: "李四", Time: "10:00", Date: "2026-11-03", Ts: 123.5,
		}},
	}
	if got := apiResult(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("Result 映射不等价\n got: %+v\nwant: %+v", got, want)
	}
}
