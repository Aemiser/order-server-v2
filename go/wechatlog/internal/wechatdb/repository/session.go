package repository

import (
	"context"

	"order_server/go/wechatlog/internal/model"
)

func (r *Repository) GetSessions(ctx context.Context, key string, limit, offset int) ([]*model.Session, error) {
	return r.ds.GetSessions(ctx, key, limit, offset)
}
