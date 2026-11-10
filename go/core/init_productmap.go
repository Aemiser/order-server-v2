package core

import (
	"encoding/json"
	"fmt"
	"order_server/go/global"
	"order_server/go/models"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
)

// ReadProductBelongMap 从 cfg_product_belongs 读取商品归属映射（原 productbelong.json）。
func ReadProductBelongMap() (mapps map[string][]string) {
	mapps = map[string][]string{}
	if global.Db == nil {
		return
	}
	if err := EnsureCfgTables(); err != nil {
		logrus.Warnf("初始化 cfg_product_belongs 失败: %v", err)
		return
	}
	var rows []models.CfgProductBelong
	if err := global.Db.Where("enabled = ?", true).Order("sort_order asc, id asc").Find(&rows).Error; err != nil {
		logrus.Warnf("读取 cfg_product_belongs 失败: %v", err)
		return
	}
	for _, r := range rows {
		aliases := []string{}
		if strings.TrimSpace(r.Aliases) != "" {
			if err := json.Unmarshal([]byte(r.Aliases), &aliases); err != nil {
				logrus.Warnf("商品分类 %s 的别名不是合法 JSON: %v", r.Category, err)
			}
		}
		mapps[r.Category] = aliases
	}
	return
}

// WriteProductBelongMap 把前端设置的商品归属全量写回 cfg_product_belongs
// （原实现写 productbelong.json）。事务内替换，保证不会留下半张表。
func WriteProductBelongMap(dataMap map[string][]string) error {
	if global.Db == nil {
		return fmt.Errorf("数据库未初始化，无法保存商品归属")
	}
	categories := make([]string, 0, len(dataMap))
	for k := range dataMap {
		categories = append(categories, k)
	}
	sort.Strings(categories)

	rows := make([]models.CfgProductBelong, 0, len(categories))
	for i, k := range categories {
		aliases := dataMap[k]
		if aliases == nil {
			aliases = []string{}
		}
		b, err := json.Marshal(aliases)
		if err != nil {
			return err
		}
		rows = append(rows, models.CfgProductBelong{
			Category: k, Aliases: string(b), SortOrder: i + 1, Enabled: true,
		})
	}
	if err := replaceRows(&rows, true); err != nil {
		return fmt.Errorf("写入商品归属失败: %w", err)
	}
	logrus.Infof("商品归属已保存到 cfg_product_belongs，共 %d 个分类", len(rows))
	return nil
}
