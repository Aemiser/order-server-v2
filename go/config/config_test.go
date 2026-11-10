package config

import (
	"context"
	"testing"
)

func TestProvider_GetSet(t *testing.T) {
	p := NewMemory(nil)
	if _, err := p.Get(context.Background(), "ai", "key"); err == nil {
		t.Fatal("空命名空间读取应报错")
	}
	p.Set("ai", "deepseek_key", "sk-123")
	v, err := p.Get(context.Background(), "ai", "deepseek_key")
	if err != nil {
		t.Fatal(err)
	}
	if v != "sk-123" {
		t.Fatalf("读取错误: %q", v)
	}
}

func TestProvider_Watch(t *testing.T) {
	p := NewMemory(nil)
	fired := 0
	p.Watch(context.Background(), "ai", func() { fired++ })
	p.Set("ai", "k", "v")
	p.Set("ai", "k2", "v2")
	if fired != 2 {
		t.Fatalf("热更新回调触发 %d 次，期望 2", fired)
	}
	// 其它命名空间的写入不应触发本命名空间监听
	other := 0
	p.Watch(context.Background(), "other", func() { other++ })
	p.Set("ai", "k3", "v3")
	if other != 0 {
		t.Fatalf("其它命名空间写入误触发，other=%d", other)
	}
}
