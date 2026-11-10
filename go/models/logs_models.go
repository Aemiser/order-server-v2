package models

import "order_server/go/models/enum"

type LogModels struct {
	Model
	Title   string            `gorm:"size:64" json:"title"`
	Content string            `json:"content"`
	Level   enum.LogLevelType `json:"level"` // 日志等级 1 2 3
}
