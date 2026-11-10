//go:build !windows

package windows

import (
	"context"

	"order_server/go/wechatlog/internal/wechat/model"
)

func (e *V3Extractor) Extract(ctx context.Context, proc *model.Process) (string, string, error) {
	return "", "", nil
}
