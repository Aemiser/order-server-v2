package compose

import (
	"context"

	"order_server/go/orderschema"
	"order_server/go/ports"
)

// CompletionFunc 把任意 ports.CompletionProvider 适配成 orderschema 需要的
// CompletionFunc（func(ctx, system, user) (string, error)）。
//
// orderschema 完全不依赖 go/ai 的具体实现，只认这个轻量函数类型；
// 因此无论是 go/ai 的 OpenAI 兼容 provider、还是测试用的桩、还是未来的本地模型，
// 都能零侵入地注入进模板归纳/字段补全流程。
func CompletionFunc(p ports.CompletionProvider) orderschema.CompletionFunc {
	return func(ctx context.Context, system, user string) (string, error) {
		resp, err := p.Complete(ctx, ports.CompletionRequest{
			SystemPrompt: system,
			UserPrompt:   user,
		})
		if err != nil {
			return "", err
		}
		return resp.Text, nil
	}
}
