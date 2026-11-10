package ai_model

import (
	"encoding/json"
	"fmt"
	"order_server/go/global"
	"order_server/go/models"
	"order_server/go/models/enum"
	alog "order_server/go/server/log"
	"order_server/go/utils/httputils"
	ordertutil "order_server/go/utils/order"

	"github.com/sirupsen/logrus"
)

// 密钥、请求地址、模型名全部来自 cfg_ai_models 表（global.GetConfig().AiModel.Qwen），
// 原来写死在这里的 APIKey / APIURL / ModelName 常量已移除。

// 请求结构体 (兼容 OpenAI 格式)
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
}

// 响应结构体
type QwenResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
		Delta        Message `json:"delta"` // 流式模式下使用
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

func OrderRecQwen(inputText string, previewType enum.RecPreviewType) []ordertutil.ExceladdressInfo {
	fmt.Println("==============调用千问==============")
	apikey := global.GetConfig().AiModel.Qwen.Apikey
	addressList, err := callChat(apikey, inputText)
	if err != nil {
		logrus.Errorf("识别失败: %v", err)
		alog.Logaction(enum.LogWarnLevel, "Qwen API识别识别失败", err.Error())
		return nil
	}

	// vid赋值
	addressList = ordertutil.SetVid(addressList)

	// JSON格式
	jsonData, _ := json.MarshalIndent(addressList, "", "  ")
	fmt.Println(string(jsonData))

	// 日志记录
	logcontent := "原始输入:" + "\n" + inputText + "\n" + "识别结果:" + "\n" + string(jsonData)
	alog.Logaction(enum.LogInfoLevel, "千问 API识别", logcontent)

	// 预览数据模式判断
	// 追加
	if previewType == enum.RecPreviewType_ADD {
		global.AddressList = append(global.AddressList, addressList...)
	}

	// 清空重新识别
	if previewType == enum.RecPreviewType_RE {
		global.AddressList = addressList
	}

	// 转换成预览数据返回给前台
	return ordertutil.AddressToExcelAddress(global.AddressList)

}

type QwenStreamCallback func(data string) error
type QwenDoneCallback func()

func StreamRecQwen(inputText string, previewType enum.RecPreviewType, callback func(string) error, doneCallback func()) {
	fmt.Println("==============流式调用千问==============")
	apikey := global.GetConfig().AiModel.Qwen.Apikey

	callChatStream(apikey, inputText, func(content string) error {
		addressList, err := ordertutil.RespBodyToJson(content)
		if err != nil {
			return nil
		}

		if len(addressList) > 0 {
			addressList = ordertutil.SetVid(addressList)

			if previewType == enum.RecPreviewType_ADD {
				global.AddressList = append(global.AddressList, addressList...)
			} else if previewType == enum.RecPreviewType_RE {
				global.AddressList = addressList
			}

			excelData := ordertutil.AddressToExcelAddress(addressList)
			jsonData, _ := json.Marshal(excelData)
			callback(string(jsonData))
		}

		return nil
	}, func() {
		excelData := ordertutil.AddressToExcelAddress(global.AddressList)
		jsonData, _ := json.Marshal(excelData)
		callback(string(jsonData))
		doneCallback()
	})
}

func callChat(apiKey, inputText string) ([]models.AddressInfo, error) {
	messages := []Message{
		{Role: "system", Content: global.Prompt(global.PromptOrderExtract)},
		{Role: "user", Content: inputText},
	}
	reqBody := ChatRequest{
		Model:       global.GetConfig().AiModel.Qwen.Modelname,
		Messages:    messages,
		Stream:      false,
		Temperature: 0.7,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("JSON 序列化失败：%w", err)
	}

	// 千问兼容模式通常需要这个头，部分旧版本可能需要 X-DashScope-SSE
	body, err := httputils.AiRequest(apiKey, global.GetConfig().AiModel.Qwen.ApiUrl, jsonData, global.GetConfig().AiModel.Qwen.Timeout, map[string]string{
		"X-DashScope-SSE": "enable",
	})
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	// 解析响应
	var apiResp QwenResponse
	if err = json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w", err)
	}

	// 检查API错误
	if apiResp.Error != nil {
		return nil, fmt.Errorf("API错误: %s", apiResp.Error.Message)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("模型未返回有效结果")
	}

	// 获取模型返回的JSON字符串
	content := apiResp.Choices[0].Message.Content
	return ordertutil.RespBodyToJson(content)
}

func callChatStream(apiKey, inputText string, chunkCallback func(string) error, doneCallback func()) {
	messages := []Message{
		{Role: "system", Content: global.Prompt(global.PromptOrderExtract)},
		{Role: "user", Content: inputText},
	}
	reqBody := ChatRequest{
		Model:       global.GetConfig().AiModel.Qwen.Modelname,
		Messages:    messages,
		Stream:      true,
		Temperature: 0.7,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		logrus.Errorf("JSON序列化失败: %v", err)
		return
	}

	err = httputils.AiStreamRequest(apiKey, global.GetConfig().AiModel.Qwen.ApiUrl, jsonData, global.GetConfig().AiModel.Qwen.Timeout, map[string]string{
		"X-DashScope-SSE": "enable",
	}, func(data string) error {
		if data == "DONE" {
			doneCallback()
			return nil
		}

		var streamResp QwenResponse
		if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
			return nil
		}

		if len(streamResp.Choices) > 0 {
			delta := streamResp.Choices[0].Delta.Content
			if delta != "" {
				return chunkCallback(delta)
			}
		}

		return nil
	})

	if err != nil {
		logrus.Errorf("流式请求失败: %v", err)
	}
}
