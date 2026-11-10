package schemaservice

import (
	"testing"

	wechatsource "order_server/go/sources/wechat"
)

// TestWechatSourceUsesSharedAdapter 锁住注册表里的 wechat 来源复用
// go/sources/wechat 的唯一实现：默认构建下也必须能解析出来，不允许
// 因为加了 build tag 又退化成第二份平行适配器。
func TestWechatSourceUsesSharedAdapter(t *testing.T) {
	found := false
	for _, name := range Sources() {
		if name == "wechat" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("注册表应包含 wechat 来源，实际 %v", Sources())
	}

	src, err := newWechatSource()
	if err != nil {
		t.Fatalf("构造微信来源失败: %v", err)
	}
	if src.Name() != "wechat" {
		t.Fatalf("来源名应为 wechat，实际 %q", src.Name())
	}
	if _, ok := src.(*wechatsource.Source); !ok {
		t.Fatalf("wechat 来源应复用 go/sources/wechat 的唯一实现，实际 %T", src)
	}
}
