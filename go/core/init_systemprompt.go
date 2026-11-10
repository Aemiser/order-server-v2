package core

import (
	"order_server/go/global"
	"order_server/go/models"

	"github.com/sirupsen/logrus"
)

// Readsystemprompt 从 cfg_prompts 读取订单识别系统提示词（原 systemPrompt.txt）。
// 保留原函数名以兼容历史脚本；正常启动路径走 LoadDBCfg -> 配置快照（global.Prompt(code)）。
func Readsystemprompt() (str string) {
	if global.Db == nil {
		return
	}
	if err := EnsureCfgTables(); err != nil {
		logrus.Warnf("初始化 cfg_prompts 失败: %v", err)
		return
	}
	var row models.CfgPrompt
	if err := global.Db.Where("code = ? AND enabled = ?", global.PromptOrderExtract, true).First(&row).Error; err != nil {
		logrus.Warnf("读取提示词 %s 失败: %v", global.PromptOrderExtract, err)
		return
	}
	return row.Content
}
