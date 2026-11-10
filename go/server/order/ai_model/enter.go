package ai_model

// AI 提示词已统一迁入数据库 cfg_prompts 表（编码 order_extract），
// 由 global.Prompt(global.PromptOrderExtract) 读取，本包不再保留任何提示词字面量。

// Message 消息结构
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
