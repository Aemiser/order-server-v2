package httputils

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type StreamCallback func(content string) error

// baseCtx 是所有 AI 请求的「祖父 context」。默认是 context.Background()（与历史行为一致）；
// 进程退出时由 main.go 调用 SetBaseContext 替换为可取消的生命周期 ctx，
// 这样关窗口后能统一取消在途的 AI 请求，而不会悬空 100 个 goroutine。
var baseCtx = context.Background()

// SetBaseContext 设置 AI 请求的基 context（一般传入 kernel.Lifecycle.Ctx()）。
func SetBaseContext(ctx context.Context) {
	if ctx != nil {
		baseCtx = ctx
	}
}

// HTTPStatusError 是带状态码的类型化错误。Error() 文本与改造前 fmt.Errorf
// 的输出逐字一致，因此旧链路按字符串判断错误的行为不受影响；新链路
// （go/ai）可用 errors.As 取出 StatusCode，区分「重试无意义的 4xx」与
// 「值得重试的 5xx / 429」。
type HTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("API返回错误状态码: %d, 响应: %s", e.StatusCode, e.Body)
}

// ---------------------------------------------------------------------------
// 兼容入口：旧识别链路继续调用这两个函数，行为与改造前逐字一致
// （仍使用包级 baseCtx，流式不额外检查状态码、不因 ctx 取消改变返回值）。
// ---------------------------------------------------------------------------

func AiRequest(apiKey, APIURL string, jsonData []byte, timeout int64, headmap map[string]string) ([]byte, error) {
	return aiRequest(baseCtx, apiKey, APIURL, jsonData, timeout, headmap)
}

func AiStreamRequest(apiKey, APIURL string, jsonData []byte, timeout int64, headmap map[string]string, callback StreamCallback) error {
	return aiStreamRequest(baseCtx, apiKey, APIURL, jsonData, timeout, headmap, callback, false)
}

// ---------------------------------------------------------------------------
// ctx 感知入口：新链路（go/ai）专用。调用方的取消/超时能真正中断在途请求，
// 流式路径额外检查 HTTP 状态码，避免把错误 JSON 当成 SSE 数据静默丢弃。
// ---------------------------------------------------------------------------

// AiRequestCtx 与普通 AiRequest 等价，但使用调用方传入的 ctx。
func AiRequestCtx(ctx context.Context, apiKey, APIURL string, jsonData []byte, timeout int64, headmap map[string]string) ([]byte, error) {
	return aiRequest(ensureCtx(ctx), apiKey, APIURL, jsonData, timeout, headmap)
}

// AiStreamRequestCtx 与 AiStreamRequest 等价，但使用调用方传入的 ctx，
// 且非 200 响应返回 *HTTPStatusError、ctx 取消返回 ctx.Err()（绝不返回「空结果」）。
func AiStreamRequestCtx(ctx context.Context, apiKey, APIURL string, jsonData []byte, timeout int64, headmap map[string]string, callback StreamCallback) error {
	return aiStreamRequest(ensureCtx(ctx), apiKey, APIURL, jsonData, timeout, headmap, callback, true)
}

func ensureCtx(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// strict 为 true 时启用新链路专属的状态码校验与取消返回；旧入口传 false，
// 保证「不改调用点就零行为变化」的绞杀者约束。
func aiRequest(ctx context.Context, apiKey, APIURL string, jsonData []byte, timeout int64, headmap map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", APIURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败：%w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	if headmap != nil {
		for k, v := range headmap {
			req.Header.Set(k, v)
		}
	}

	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败：%w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return body, nil
}

func aiStreamRequest(ctx context.Context, apiKey, APIURL string, jsonData []byte, timeout int64, headmap map[string]string, callback StreamCallback, strict bool) error {
	req, err := http.NewRequestWithContext(ctx, "POST", APIURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("创建请求失败：%w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	if headmap != nil {
		for k, v := range headmap {
			req.Header.Set(k, v)
		}
	}

	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("发送请求失败：%w", err)
	}
	defer resp.Body.Close()

	// 旧流式实现没有这一步：非 200 时错误体被当成 SSE 行喂给回调，
	// 上层拿到的是「解析不出内容」的空结果。新链路必须显式报错。
	if strict && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	buf := make([]byte, 4096)
	accumulated := ""

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			accumulated += string(buf[:n])

			for strings.Contains(accumulated, "\n") {
				lines := strings.SplitN(accumulated, "\n", 2)
				if len(lines) < 2 {
					break
				}
				line := strings.TrimSpace(lines[0])
				accumulated = lines[1]

				if strings.HasPrefix(line, "data:") {
					data := strings.TrimPrefix(line, "data:")
					data = strings.TrimSpace(data)

					if strings.HasPrefix(data, "[") || strings.HasPrefix(data, "{") {
						if callback != nil {
							if err := callback(data); err != nil {
								return err
							}
						}
					} else if data == "[DONE]" {
						if callback != nil {
							callback("DONE")
						}
					}
				}
			}
		}

		if readErr != nil {
			// 取消后请求体会关闭、Read 返回错误。旧路径沿用 break 返回 nil，
			// 新路径返回 ctx.Err()，避免「取消」被误当作「生成了空文本」。
			if strict && ctx.Err() != nil {
				return ctx.Err()
			}
			break
		}
	}

	return nil
}
