package conf

import "testing"

func TestAuthConfLoginRequired(t *testing.T) {
	// 老配置文件没有 auth 这一节，指针为 nil，必须默认「需要登录」，
	// 否则软件升级后会意外变成免登录。
	var c AuthConf
	if !c.LoginRequired() {
		t.Error("配置里没有 auth 段时，默认应需要登录")
	}

	yes := true
	c.RequireLogin = &yes
	if !c.LoginRequired() {
		t.Error("显式 true 应需要登录")
	}

	no := false
	c.RequireLogin = &no
	if c.LoginRequired() {
		t.Error("显式 false 应为免登录")
	}
}
