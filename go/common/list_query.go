package common

import (
	"fmt"
	"order_server/go/global"
	"strings"

	"gorm.io/gorm"
)

type PageInfo struct {
	Limit int    `form:"limit" json:"limit"`
	Page  int    `form:"page" json:"page"`
	Key   string `form:"key" json:"key"`
	Order string `form:"order" json:"order"`
}

func (p PageInfo) GetLimit() int {
	if p.Limit < 0 {
		p.Limit = 1
	}
	return p.Limit
}

func (p PageInfo) GetPage() int {
	// 页码最小为 1（0 或负数都不合法，否则会算出负 offset）
	if p.Page < 1 {
		p.Page = 1
	}
	return p.Page
}

func (p PageInfo) GetOffset() int {
	offset := (p.GetPage() - 1) * p.GetLimit()
	// offset 不允许为负，避免 MySQL 把负 OFFSET 当作 0 而退回第一页数据
	if offset < 0 {
		offset = 0
	}
	return offset
}

type Options struct {
	PageInfo     PageInfo
	Likes        []string // 关键字模糊查询的列
	Preloads     []string
	Where        *gorm.DB
	Debug        bool
	DefaultOrder string
}

func ListQuery[T any](model T, options Options) (list []T, count int, err error) {
	var query = global.Db.Model(model).Where(model)

	// 显示日志
	if options.Debug {
		query = query.Debug()
	}

	// 模糊查询
	if len(options.Likes) > 0 && options.PageInfo.Key != "" {
		fmt.Println("关键词模糊查询列:", options.Likes)
		likes := global.Db.Where("")
		for _, column := range options.Likes {
			likes.Or(
				fmt.Sprintf("%s like ?", column),
				fmt.Sprintf("%%%s%%", options.PageInfo.Key),
			)
		}
		query = query.Where(likes)
	}

	// 定制化查询
	if options.Where != nil {
		query = query.Where(options.Where)
	}

	// 预加载
	for _, preload := range options.Preloads {
		query = query.Preload(preload)
	}

	// 查总数
	var _count int64
	query.Count(&_count)
	count = int(_count)

	// 分页
	offest := options.PageInfo.GetOffset()
	limit := options.PageInfo.GetLimit()

	// 排序
	orderClause := ""
	if options.PageInfo.Order != "" {
		orderClause = options.PageInfo.Order
	} else if options.DefaultOrder != "" {
		orderClause = options.DefaultOrder
	}
	if orderClause != "" {
		// 关键修复：追加主键 id 作为最终排序键，保证排序完全确定。
		// 否则当 created_at 等排序字段存在大量并列值（如同一天/同一秒创建、
		// 或批量导入设置了相同时间）时，MySQL 在多次分页查询之间对并列行的
		// 返回顺序是未定义的，会导致 LIMIT/OFFSET 切出的"页"互相重叠，
		// 即"翻到下一页出现上一页的数据"。
		if !strings.Contains(strings.ToLower(orderClause), "id") {
			orderClause = orderClause + ", id"
		}
		query = query.Order(orderClause)
	}

	// 排除limit 为0的情况
	if limit != 0 {
		query = query.Offset(offest).Limit(limit)
	}
	err = query.Find(&list).Error
	return
}
