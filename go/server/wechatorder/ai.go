package wechatorder

import (
	"encoding/json"
	"fmt"
	"order_server/go/global"
	"order_server/go/models"
	"order_server/go/utils/httputils"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// 本文件不再包含任何提示词正文，也不写死 API 地址 / 模型名 / 密钥：
//   提示词      -> cfg_prompts 表（wechat_order_base / wechat_full_scan / wechat_target_date）
//   模型连接参数 -> cfg_ai_models 表（deepseek.api_key / api_url / model_name / timeout）

// AIResult 是单条聊天消息交给 DeepSeek 后的判定结果。
//
// 外层对象用于承载「是否订单 / 日期 / 是否需要人工审查」等业务信号；
// orders 数组里每个元素的字段沿用原 systemPrompt.txt（现已迁入
// cfg_prompts.order_extract）约定的中文键格式（收件人 / 手机号 /
// 收货地址 / 商品规格 / 备注），下游可直接反序列化成 models.AddressInfo。
type AIResult struct {
	IsOrder       bool                 `json:"is_order"`
	OrderDate     string               `json:"order_date"`
	DateConfirmed bool                 `json:"date_confirmed"` // 消息里是否有明确的「11-7」日期标记
	NeedsReview   bool                 `json:"needs_review"`   // 是否建议人工复核（后端还会按目标日期二次校正）
	Confidence    string               `json:"confidence"`     // high / medium / low
	Reason        string               `json:"reason"`
	Orders        []models.AddressInfo `json:"orders"`
}

// AnalyzeOptions 控制提示词的适配形态。
type AnalyzeOptions struct {
	FilterOff  bool   // 规则粗筛已关闭：所有消息（含大量闲聊）都交 AI，需自行严格区分订单 / 聊天
	TargetDate string // 本轮目标订单日期 YYYY-MM-DD
	DateScope  string // 非空 = 账单范围审核：告诉模型按「起止日期」而非单日识别，避免漏掉范围前几天的订单
}

// AnalyzeItem 把候选项与 AI 结果绑在一起，供结果明细展示。
type AnalyzeItem struct {
	Candidate Candidate
	Result    AIResult
	Err       error
}

// buildSystemPrompt 按本轮配置从 cfg_prompts 拼出最终提示词。
// 基础片段、日期模板、全量消息判定依据的正文全部存在数据库里，可随时修改而无需改代码。
func buildSystemPrompt(opts AnalyzeOptions) string {
	p := global.Prompt(global.PromptWechatOrderBase)
	if s := targetDatePrompt(opts.TargetDate); s != "" {
		p += s
	}
	if opts.FilterOff {
		p += global.Prompt(global.PromptWechatFullScan)
	}
	// 范围审核只追加运行时上下文，不改动数据库里的提示词正文，也不影响单日一键识别。
	if s := strings.TrimSpace(opts.DateScope); s != "" {
		p += "\n\n【本次核对的日期范围】" + s +
			"。这个范围内的订单消息都要正常识别，不要只保留范围最后一天的订单；" +
			"消息里没写日期标记时照常留空，不要把日期硬改成范围内的某一天。"
	}
	return p
}

// targetDatePrompt 渲染 cfg_prompts 里的日期注入模板。
// 占位符：{{date}}=YYYY-MM-DD，{{label}}=M-D。
func targetDatePrompt(date string) string {
	if strings.TrimSpace(date) == "" {
		return ""
	}
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(date), time.Local)
	if err != nil {
		return ""
	}
	tpl := global.Prompt(global.PromptWechatTargetDate)
	if strings.TrimSpace(tpl) == "" {
		logrus.Warnf("cfg_prompts 缺少提示词模板 %s，跳过目标日期注入", global.PromptWechatTargetDate)
		return ""
	}
	return strings.NewReplacer(
		"{{date}}", t.Format("2006-01-02"),
		"{{label}}", TargetDateLabel(t),
	).Replace(tpl)
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens"`
	Stream      bool      `json:"stream"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// AnalyzeOne 是默认形态（粗筛开启、不注入目标日期）的兼容入口。
func AnalyzeOne(apiKey, apiURL, model, content string, timeout int64) (AIResult, error) {
	return analyze(apiKey, apiURL, model, content, timeout, AnalyzeOptions{})
}

// AnalyzeOneWithOptions 按本轮配置（粗筛开关 / 目标订单日期）适配提示词后请求模型。
func AnalyzeOneWithOptions(apiKey, apiURL, model, content string, timeout int64, opts AnalyzeOptions) (AIResult, error) {
	return analyze(apiKey, apiURL, model, content, timeout, opts)
}

func analyze(apiKey, apiURL, model, content string, timeout int64, opts AnalyzeOptions) (AIResult, error) {
	var result AIResult
	if apiKey == "" {
		return result, fmt.Errorf("未配置 DeepSeek API Key（cfg_ai_models -> deepseek.api_key）")
	}
	// 调用方没传地址/模型名时，一律回落到配置表，不再有硬编码默认值。
	if global.GetConfig() != nil {
		if apiURL == "" {
			apiURL = global.GetConfig().AiModel.Deepseek.ApiUrl
		}
		if model == "" {
			model = global.GetConfig().AiModel.Deepseek.Modelname
		}
	}
	if apiURL == "" || model == "" {
		return result, fmt.Errorf("cfg_ai_models 中 deepseek 的 api_url / model_name 未配置")
	}

	reqBody := chatRequest{
		Model: model,
		Messages: []message{
			{Role: "system", Content: buildSystemPrompt(opts)},
			{Role: "user", Content: content},
		},
		Temperature: 0.1,
		MaxTokens:   4096,
		Stream:      false,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return result, err
	}
	body, err := httputils.AiRequest(apiKey, apiURL, data, timeout, nil)
	if err != nil {
		return result, err
	}
	var resp chatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return result, fmt.Errorf("解析模型响应失败: %w", err)
	}
	if resp.Error != nil {
		return result, fmt.Errorf("DeepSeek 返回错误: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return result, fmt.Errorf("DeepSeek 未返回有效结果")
	}
	return parseAIResult(resp.Choices[0].Message.Content)
}

func parseAIResult(raw string) (AIResult, error) {
	var result AIResult
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	// 兼容 cfg_prompts.order_extract 的原生格式：模型直接吐一个订单 JSON 数组。
	// 这种写法没有日期标记，统一标记为待人工审查。
	if strings.HasPrefix(s, "[") {
		var list []models.AddressInfo
		if err := json.Unmarshal([]byte(s), &list); err == nil {
			return AIResult{
				IsOrder: len(list) > 0, Reason: "模型直接返回订单数组", Orders: list,
				DateConfirmed: false, NeedsReview: len(list) > 0, Confidence: "medium",
			}, nil
		}
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return result, fmt.Errorf("模型未返回 JSON 对象: %.200s", s)
	}
	s = s[start : end+1]
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return result, fmt.Errorf("解析订单 JSON 失败: %w；内容: %.300s", err, s)
	}
	// 日期标记是「确认与否」的权威依据：模型偶尔漏写 date_confirmed 时按 order_date 校正。
	result.OrderDate = strings.TrimSpace(result.OrderDate)
	if result.OrderDate != "" {
		result.DateConfirmed = true
	}
	if !result.IsOrder {
		result.Orders = nil
		result.NeedsReview = false
		result.Confidence = ""
	} else {
		if result.Confidence == "" {
			result.Confidence = "medium"
		}
		if !result.DateConfirmed {
			result.NeedsReview = true // 无日期标记一律建议人工审查
		}
	}
	return result, nil
}

// aiSettings 从配置表（已装载到 global.GetConfig()）取 DeepSeek 连接参数。
func aiSettings() (key, url, model string) {
	if global.GetConfig() == nil {
		return
	}
	ds := global.GetConfig().AiModel.Deepseek
	return ds.Apikey, ds.ApiUrl, ds.Modelname
}
