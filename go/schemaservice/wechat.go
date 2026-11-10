package schemaservice

import (
	"order_server/go/global"
	"order_server/go/ports"
	"order_server/go/server/wechatorder"
	wechatsource "order_server/go/sources/wechat"
)

// ---------------------------------------------------------------------------
// AI 配置（读主程序 global 快照，不重复实现配置装载）
// ---------------------------------------------------------------------------

type aiConfig struct {
	APIKey  string
	APIURL  string
	Model   string
	Timeout int64
}

type aiConfigPair struct {
	deepseek aiConfig
	qwen     aiConfig
}

func aiSettings() aiConfigPair {
	var p aiConfigPair
	cfg := global.GetConfig()
	if cfg == nil {
		return p
	}
	p.deepseek = aiConfig{
		APIKey:  cfg.AiModel.Deepseek.Apikey,
		APIURL:  cfg.AiModel.Deepseek.ApiUrl,
		Model:   cfg.AiModel.Deepseek.Modelname,
		Timeout: cfg.AiModel.Deepseek.Timeout,
	}
	p.qwen = aiConfig{
		APIKey:  cfg.AiModel.Qwen.Apikey,
		APIURL:  cfg.AiModel.Qwen.ApiUrl,
		Model:   cfg.AiModel.Qwen.Modelname,
		Timeout: cfg.AiModel.Qwen.Timeout,
	}
	return p
}

// ---------------------------------------------------------------------------
// 微信 ChatSource：复用 go/sources/wechat 的唯一实现。
// 这里只把主程序配置（群名 / 数据目录 / 密钥策略）转成门面选项，不再重复
// 实现导出逻辑——两份适配器曾因 Group 校验 / Days 推导 / 错误包装而分叉，
// 现由 go/sources/wechat/wechat_test.go 的 characterization 用例统一锁住。
// ---------------------------------------------------------------------------

// newWechatSource 从主程序配置构造微信导出源。
func newWechatSource() (ports.ChatSource, error) {
	return wechatsource.New(wechatorder.OptionsFromConfig(wechatorder.LoadConfig())), nil
}
