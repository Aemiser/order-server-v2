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

// DeepSeekRequest API请求结构
type DeepSeekRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens"`
	Stream      bool      `json:"stream"`
}

// DeepSeekResponse API响应结构
type DeepSeekResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// 请求地址与模型名来自 cfg_ai_models 表（global.GetConfig().AiModel.Deepseek），
// 这里不再硬编码任何连接参数。

func OrderRecDeepseek(inputText string, previewType enum.RecPreviewType) []ordertutil.ExceladdressInfo {
	fmt.Println("==============调用deepseek==============")
	apiKey := global.GetConfig().AiModel.Deepseek.Apikey
	// 调用API进行识别
	addressList, err := RecognizeAddresses(apiKey, inputText)
	if err != nil {
		logrus.Errorf("识别失败: %v", err)
		alog.Logaction(enum.LogWarnLevel, "DeepSeek API识别识别失败", err.Error())
		return nil
	}
	// vid赋值
	addressList = ordertutil.SetVid(addressList)

	// JSON格式
	jsonData, _ := json.MarshalIndent(addressList, "", "  ")
	fmt.Println(string(jsonData))

	// 日志记录
	logcontent := "原始输入:" + "\n" + inputText + "\n" + "识别结果:" + "\n" + string(jsonData)
	alog.Logaction(enum.LogInfoLevel, "DeepSeek API识别", logcontent)

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

// RecognizeAddresses 调用DeepSeek API识别多条地址
func RecognizeAddresses(apiKey, inputText string) ([]models.AddressInfo, error) {
	// 构建请求体
	reqBody := DeepSeekRequest{
		Model: global.GetConfig().AiModel.Deepseek.Modelname,
		Messages: []Message{
			{Role: "system", Content: global.Prompt(global.PromptOrderExtract)},
			{Role: "user", Content: inputText},
		},
		Temperature: 0.5, // 低温使输出更确定
		MaxTokens:   8192,
		Stream:      false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("请求序列化失败: %w", err)
	}

	body, err := httputils.AiRequest(apiKey, global.GetConfig().AiModel.Deepseek.ApiUrl, jsonData, global.GetConfig().AiModel.Deepseek.Timeout, nil)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	// 解析响应
	var apiResp DeepSeekResponse
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

type DeepSeekStreamCallback func(data string) error
type DeepSeekDoneCallback func()

func StreamRecDeepseek(inputText string, previewType enum.RecPreviewType, callback func(string) error, doneCallback func()) {
	fmt.Println("==============流式调用DeepSeek==============")
	apiKey := global.GetConfig().AiModel.Deepseek.Apikey

	callChatStreamDeepseek(apiKey, inputText, func(content string) error {
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

func callChatStreamDeepseek(apiKey, inputText string, chunkCallback func(string) error, doneCallback func()) {
	reqBody := DeepSeekRequest{
		Model: global.GetConfig().AiModel.Deepseek.Modelname,
		Messages: []Message{
			{Role: "system", Content: global.Prompt(global.PromptOrderExtract)},
			{Role: "user", Content: inputText},
		},
		Temperature: 0.5,
		MaxTokens:   8192,
		Stream:      true,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		logrus.Errorf("请求序列化失败: %v", err)
		return
	}

	err = httputils.AiStreamRequest(apiKey, global.GetConfig().AiModel.Deepseek.ApiUrl, jsonData, global.GetConfig().AiModel.Deepseek.Timeout, nil, func(data string) error {
		if data == "DONE" {
			doneCallback()
			return nil
		}

		var streamResp DeepSeekResponse
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
