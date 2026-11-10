package models

import (
	"time"

	"gorm.io/gorm"
)

// User 复用数据库中已有的 users 表，不新建表、不改动表结构。
// 密码字段存 bcrypt 哈希（$2a$ 等），登录时校验；历史明文密码会在首次登录成功后自动升级。
type User struct {
	ID          uint64         `gorm:"primaryKey;column:id" json:"id"`
	CreatedAt   time.Time      `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt   time.Time      `gorm:"column:updated_at" json:"updatedAt"`
	DeletedAt   gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`
	Username    string         `gorm:"column:username;size:64;uniqueIndex" json:"username"`
	Password    string         `gorm:"column:password;size:100" json:"-"`
	Nickname    string         `gorm:"column:nickname;size:64" json:"nickname"`
	Role        string         `gorm:"column:role;size:32" json:"role"`
	Enabled     bool           `gorm:"column:enabled" json:"enabled"`
	LastLoginAt *time.Time     `gorm:"column:last_login_at" json:"lastLoginAt"`
	LastLoginIP string         `gorm:"column:last_login_ip;size:64" json:"lastLoginIp"`
}

func (User) TableName() string {
	return "users"
}
