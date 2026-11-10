package alog

import (
	"order_server/go/global"
	"order_server/go/models"
	"order_server/go/models/enum"
	"time"

	"github.com/sirupsen/logrus"
)

func Logaction(level enum.LogLevelType, title, content string) {
	logModels := models.LogModels{
		Title:   title,
		Content: content,
		Level:   level,
	}
	// 1分钟前的时间
	oneMinuteAgo := time.Now().Add(-1 * time.Minute)

	// 查询1分钟内是否有相同的记录
	var existingLog models.LogModels
	err := global.Db.Where("title = ? AND content = ? AND created_at > ?",
		title, content, oneMinuteAgo).
		First(&existingLog).Error

	// 如果找到了记录（没有错误），说明重复了
	if err == nil {
		return
	}

	err = global.Db.Create(&logModels).Error
	if err != nil {
		logrus.Errorf("日志写入失败: %v", err)
	}
}
