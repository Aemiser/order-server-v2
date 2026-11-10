package auth

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestVerifyPassword(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("Passw0rd!"), bcryptCost)
	if err != nil {
		t.Fatal(err)
	}

	if !verifyPassword(string(hash), "Passw0rd!") {
		t.Error("正确的 bcrypt 密码应当校验通过")
	}
	if verifyPassword(string(hash), "wrong-password") {
		t.Error("错误的密码不应校验通过")
	}

	// 兼容 PHP 等系统生成的 $2y$ / $2b$ 前缀
	for _, prefix := range []string{"$2y$", "$2b$"} {
		converted := prefix + string(hash)[4:]
		if !verifyPassword(converted, "Passw0rd!") {
			t.Errorf("%s 前缀的哈希应当兼容", prefix)
		}
	}

	// 兼容历史明文密码
	if !verifyPassword("legacy123", "legacy123") {
		t.Error("历史明文密码应当校验通过")
	}
	if verifyPassword("legacy123", "bad") {
		t.Error("错误的明文密码不应校验通过")
	}
}

func TestSessionLifecycle(t *testing.T) {
	Logout()
	if IsLoggedIn() {
		t.Fatal("初始状态应为未登录")
	}

	// 不连数据库也能验证会话本身的读写
	sessionMu.Lock()
	current = &Session{UserID: 1, Username: "tester", Nickname: "测试", Role: "admin"}
	sessionMu.Unlock()

	if !IsLoggedIn() {
		t.Error("写入会话后应处于登录状态")
	}
	state := GetAuthState()
	if !state.LoggedIn || state.Username != "tester" || state.Role != "admin" {
		t.Errorf("GetAuthState 返回异常: %+v", state)
	}

	Logout()
	if IsLoggedIn() || GetAuthState().LoggedIn {
		t.Error("Logout 之后应恢复为未登录")
	}
}

// TestGuestMode 覆盖「启动免登录」开关：开启后不查数据库也能让所有
// requireAuth 门禁通过，关闭后完全恢复原来的登录行为。
func TestGuestMode(t *testing.T) {
	t.Cleanup(func() {
		SetGuestMode(false)
		Logout()
	})

	SetGuestMode(false)
	Logout()
	if IsLoggedIn() {
		t.Fatal("未开启免登录且未登录时应处于未登录状态")
	}

	SetGuestMode(true)
	if GuestMode() != true {
		t.Error("GuestMode 应反映已开启的免登录状态")
	}
	if !IsLoggedIn() {
		t.Fatal("免登录模式应自动处于登录状态，否则所有业务接口都会被 requireAuth 拒绝")
	}

	state := GetAuthState()
	if !state.LoggedIn || !state.Guest || state.Username != guestUsername || state.Nickname != guestNickname {
		t.Errorf("访客状态异常: %+v", state)
	}
	if s := Current(); s == nil || !s.Guest || s.UserID != 0 {
		t.Errorf("访客会话应带 Guest 标记且无真实用户 ID: %+v", s)
	}

	// 访客模式下退出登录不能把会话清空，否则界面会被踢回登录页
	Logout()
	if !IsLoggedIn() || !GetAuthState().Guest {
		t.Error("免登录模式下退出不应影响访客会话")
	}

	if msg := ChangePassword("whatever", "12345678"); !strings.Contains(msg, "免登录") {
		t.Errorf("访客模式修改密码应给出明确提示，实际: %s", msg)
	}

	// 关闭开关 = 恢复原有登录流程，不留任何访客痕迹
	SetGuestMode(false)
	if IsLoggedIn() || GetAuthState().LoggedIn {
		t.Error("关闭免登录模式后应恢复为未登录")
	}
}
