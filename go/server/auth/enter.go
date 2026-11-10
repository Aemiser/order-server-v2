package auth

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"order_server/go/global"
	"order_server/go/models"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	maxFailedAttempts = 5
	lockDuration      = 10 * time.Minute
	minPasswordLength = 8
	bcryptCost        = 12

	// 免登录（访客）模式使用的内置身份，只在进程内存在，不查库、不落盘。
	guestUsername = "guest"
	guestNickname = "访客"
	guestRole     = "guest"
)

// Session 是当前进程的登录会话。桌面客户端是单实例进程，会话保存在内存中，
// 不写入文件、不做「记住我」，程序重启后必须重新登录。
type Session struct {
	UserID   uint64    `json:"userId"`
	Username string    `json:"username"`
	Nickname string    `json:"nickname"`
	Role     string    `json:"role"`
	LoginAt  time.Time `json:"loginAt"`
	// Guest 标记这是「启动免登录」自动建立的访客会话，而非数据库账号登录。
	Guest bool `json:"guest"`
}

type LoginResult struct {
	OK      bool     `json:"ok"`
	Message string   `json:"message"`
	Session *Session `json:"session"`
}

type AuthState struct {
	LoggedIn bool   `json:"loggedIn"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Role     string `json:"role"`
	Guest    bool   `json:"guest"`
}

type failRecord struct {
	Count       int
	LockedUntil time.Time
}

var (
	sessionMu sync.RWMutex
	current   *Session

	failMu   sync.Mutex
	failures = make(map[string]*failRecord)

	// guestMode 为 true 时进程内自动持有内置访客会话，
	// 用于「启动免登录」开关：所有 requireAuth 门禁照常通过，但完全不依赖数据库。
	guestMode atomic.Bool
)

func failResult(msg string) LoginResult {
	return LoginResult{OK: false, Message: msg}
}

// normalizeBcrypt 把 $2b$ / $2y$ 规范化为 Go bcrypt 支持的 $2a$（算法完全一致）。
func normalizeBcrypt(hash string) string {
	if len(hash) > 4 && strings.HasPrefix(hash, "$2") && hash[3] == '$' && (hash[2] == 'b' || hash[2] == 'y') {
		return "$2a$" + hash[4:]
	}
	return hash
}

// verifyPassword 优先按 bcrypt 校验；兼容历史明文密码，避免老账号无法登录。
func verifyPassword(stored, input string) bool {
	stored = strings.TrimSpace(stored)
	if strings.HasPrefix(stored, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(normalizeBcrypt(stored)), []byte(input)) == nil
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(input)) == 1
}

func lockRemaining(username string) time.Duration {
	failMu.Lock()
	defer failMu.Unlock()
	r, ok := failures[username]
	if !ok {
		return 0
	}
	if !r.LockedUntil.IsZero() {
		if time.Now().Before(r.LockedUntil) {
			return time.Until(r.LockedUntil)
		}
		delete(failures, username) // 锁定期已过，重新开始计数
	}
	return 0
}

func recordFailure(username string) {
	failMu.Lock()
	defer failMu.Unlock()
	r, ok := failures[username]
	if !ok {
		r = &failRecord{}
		failures[username] = r
	}
	r.Count++
	if r.Count >= maxFailedAttempts {
		r.LockedUntil = time.Now().Add(lockDuration)
		r.Count = 0
		logrus.Warnf("账号 %s 连续登录失败过多，临时锁定 %v", username, lockDuration)
	}
}

func clearFailure(username string) {
	failMu.Lock()
	defer failMu.Unlock()
	delete(failures, username)
}

// Login 校验 users 表中的账号密码，成功后在当前进程建立会话。
func Login(username, password string) LoginResult {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return failResult("请输入账号和密码")
	}
	if remain := lockRemaining(username); remain > 0 {
		return failResult(fmt.Sprintf("连续登录失败次数过多，请 %.0f 秒后再试", remain.Seconds()))
	}

	if global.Db == nil {
		return failResult("数据库未连接，无法验证账号")
	}

	var u models.User
	err := global.Db.Where("username = ? AND enabled = ?", username, true).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		recordFailure(username)
		logrus.Warnf("登录失败（账号不存在或已禁用）: %s", username)
		return failResult("账号或密码错误")
	}
	if err != nil {
		logrus.Errorf("登录查询用户失败: %v", err)
		return failResult("无法连接账号服务，请检查网络或数据库配置")
	}

	if !verifyPassword(u.Password, password) {
		recordFailure(username)
		logrus.Warnf("登录失败（密码错误）: %s", username)
		return failResult("账号或密码错误")
	}

	// 历史明文密码在第一次成功登录时自动升级为 bcrypt，原密码不变。
	if !strings.HasPrefix(strings.TrimSpace(u.Password), "$2") {
		if hash, hashErr := bcrypt.GenerateFromPassword([]byte(password), bcryptCost); hashErr == nil {
			if upErr := global.Db.Model(&models.User{}).Where("id = ?", u.ID).Update("password", string(hash)).Error; upErr != nil {
				logrus.Warnf("升级密码存储失败: %v", upErr)
			} else {
				logrus.Infof("已将账号 %s 的密码升级为 bcrypt 存储", u.Username)
			}
		}
	}

	clearFailure(username)
	now := time.Now()
	if upErr := global.Db.Model(&models.User{}).Where("id = ?", u.ID).Updates(map[string]interface{}{
		"last_login_at": now,
		"last_login_ip": "desktop",
	}).Error; upErr != nil {
		logrus.Warnf("更新最后登录时间失败: %v", upErr)
	}

	s := &Session{
		UserID:   u.ID,
		Username: u.Username,
		Nickname: u.Nickname,
		Role:     u.Role,
		LoginAt:  now,
	}
	sessionMu.Lock()
	current = s
	sessionMu.Unlock()

	logrus.Infof("用户登录成功: %s (role=%s)", u.Username, u.Role)
	return LoginResult{OK: true, Message: "登录成功", Session: s}
}

// SetGuestMode 切换「启动免登录」模式。
// 开启时进程自动持有内置访客会话：不查数据库、不写账号数据、不弹登录页，
// 但 IsLoggedIn / requireAuth 全部照常通过，主界面可以完整使用。
// 关闭时恢复原来的空会话状态（需要真实账号登录）。
func SetGuestMode(enabled bool) {
	guestMode.Store(enabled)
	// 清掉可能残留的真实会话，避免访客身份与数据库账号身份混用。
	sessionMu.Lock()
	current = nil
	sessionMu.Unlock()
	if enabled {
		logrus.Info("已启用免登录模式：自动以内置访客身份进入主界面")
	}
}

// GuestMode 返回当前是否处于免登录（访客）模式。
func GuestMode() bool {
	return guestMode.Load()
}

// guestSession 每次合成一份全新的访客会话；不持久化，进程退出即消失。
func guestSession() *Session {
	return &Session{
		UserID:   0,
		Username: guestUsername,
		Nickname: guestNickname,
		Role:     guestRole,
		LoginAt:  time.Now(),
		Guest:    true,
	}
}

// Current 返回当前会话的副本，未登录且非访客模式时返回 nil。
// 访客模式下即使有人调用了 Logout，也会继续返回访客会话，
// 免登录状态下界面不会被意外踢回登录页。
func Current() *Session {
	sessionMu.RLock()
	s := current
	sessionMu.RUnlock()
	if s == nil {
		if guestMode.Load() {
			return guestSession()
		}
		return nil
	}
	cp := *s
	return &cp
}

func IsLoggedIn() bool {
	return Current() != nil
}

func GetAuthState() AuthState {
	s := Current()
	if s == nil {
		return AuthState{}
	}
	return AuthState{
		LoggedIn: true,
		Username: s.Username,
		Nickname: s.Nickname,
		Role:     s.Role,
		Guest:    s.Guest,
	}
}

func Logout() {
	if guestMode.Load() {
		logrus.Debug("免登录模式下忽略退出登录请求（保持访客会话）")
		return
	}
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if current != nil {
		logrus.Infof("用户退出登录: %s", current.Username)
	}
	current = nil
}

// ChangePassword 修改当前登录用户的密码（需要原密码）。
func ChangePassword(oldPassword, newPassword string) string {
	s := Current()
	if s == nil {
		return "会话已失效，请重新登录"
	}
	if s.Guest {
		return "免登录模式使用内置访客身份，没有账号密码可修改；如需修改密码请先开启「启动需要登录」"
	}
	if len(newPassword) < minPasswordLength {
		return fmt.Sprintf("新密码长度不能少于 %d 位", minPasswordLength)
	}
	if global.Db == nil {
		return "数据库未连接"
	}

	var u models.User
	if err := global.Db.Where("id = ?", s.UserID).First(&u).Error; err != nil {
		return "账号不存在或已被删除"
	}
	if !verifyPassword(u.Password, oldPassword) {
		return "原密码不正确"
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return "密码加密失败"
	}
	if err := global.Db.Model(&models.User{}).Where("id = ?", u.ID).Update("password", string(hash)).Error; err != nil {
		logrus.Errorf("修改密码失败: %v", err)
		return "修改失败，请稍后重试"
	}

	logrus.Infof("用户 %s 修改了密码", s.Username)
	return "修改成功"
}
