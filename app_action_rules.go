package main

// 「订单操作术语规则」的 Wails 绑定：人工审核区 / 应用设置页共用的增删改查。
//
// 规则写入后立即刷新内存快照，下一轮一键识别就能用上新词，无需重启、无需等 60 秒。
// 返回值统一用「空串 = 成功，非空 = 错误信息」，与项目其它配置绑定风格一致。

import (
	"fmt"
	"strings"

	"order_server/go/core"
	"order_server/go/global"
	"order_server/go/models"

	"gorm.io/gorm"
)

// OrderActionRuleDTO 是前端配置弹窗与后端之间的扁平结构。
// 刻意不暴露 models.CfgOrderActionRule 内嵌的 GORM 元数据（CreatedAt / DeletedAt 等），
// 避免 Wails 生成的 TS 类型里出现用不到的时间字段。
type OrderActionRuleDTO struct {
	ID        uint   `json:"id"`
	Keyword   string `json:"keyword"`
	Action    string `json:"action"`
	Field     string `json:"field"`
	ValueRule string `json:"valueRule"`
	Scope     string `json:"scope"`
	Priority  int    `json:"priority"`
	Enabled   bool   `json:"enabled"`
	SortOrder int    `json:"sortOrder"`
	Remark    string `json:"remark"`
}

func toActionRuleDTO(r models.CfgOrderActionRule) OrderActionRuleDTO {
	return OrderActionRuleDTO{
		ID: r.ID, Keyword: r.Keyword, Action: r.Action, Field: r.Field,
		ValueRule: r.ValueRule, Scope: r.Scope, Priority: r.Priority,
		Enabled: r.Enabled, SortOrder: r.SortOrder, Remark: r.Remark,
	}
}

// ListOrderActionRules 返回全部规则（含已停用），直接查库而不是读内存快照，
// 因为 global 快照按设计只保留 enabled=true 的行，配置页需要看到被停用的规则。
func (a *App) ListOrderActionRules() []OrderActionRuleDTO {
	out := make([]OrderActionRuleDTO, 0)
	if !a.requireAuth() || global.Db == nil {
		return out
	}
	var rows []models.CfgOrderActionRule
	if err := global.Db.Order("sort_order asc, id asc").Find(&rows).Error; err != nil {
		return out
	}
	for _, r := range rows {
		out = append(out, toActionRuleDTO(r))
	}
	return out
}

func validateActionRuleDTO(r OrderActionRuleDTO) error {
	switch r.Action {
	case "delete", "modify", "ignore":
	default:
		return fmt.Errorf("订单操作类型无效：%s", r.Action)
	}
	if r.Action == "modify" {
		switch r.Field {
		case "address", "phone", "recipient", "remarks", "specs":
		default:
			return fmt.Errorf("修改订单时必须选择目标字段")
		}
	}
	switch r.ValueRule {
	case "none", "after", "phone", "append_rest":
	default:
		return fmt.Errorf("新值抽取方式无效：%s", r.ValueRule)
	}
	switch r.Scope {
	case "", "both", "trigger", "prompt":
	default:
		return fmt.Errorf("作用范围无效：%s", r.Scope)
	}
	return nil
}

// SaveOrderActionRule 新增或编辑一条规则，写入后立即刷新运行时快照。
func (a *App) SaveOrderActionRule(rule OrderActionRuleDTO) string {
	if !a.requireAuth() {
		return "会话已失效，请重新登录"
	}
	if global.Db == nil {
		return "数据库未初始化"
	}
	rule.Keyword = strings.TrimSpace(rule.Keyword)
	if rule.Keyword == "" {
		return "关键词不能为空"
	}
	if rule.Scope == "" {
		rule.Scope = "both"
	}
	if err := validateActionRuleDTO(rule); err != nil {
		return err.Error()
	}

	var dup models.CfgOrderActionRule
	q := global.Db.Unscoped().Where("keyword = ?", rule.Keyword)
	if rule.ID > 0 {
		q = q.Where("id <> ?", rule.ID)
	}
	if err := q.First(&dup).Error; err == nil {
		return "关键词「" + rule.Keyword + "」已存在，请直接编辑那条规则"
	} else if err != gorm.ErrRecordNotFound {
		return "查询重复规则失败：" + err.Error()
	}

	values := map[string]interface{}{
		"keyword": rule.Keyword, "action_type": rule.Action,
		"field_name": rule.Field, "value_rule": rule.ValueRule,
		"scope": rule.Scope, "priority": rule.Priority,
		"enabled": rule.Enabled, "sort_order": rule.SortOrder,
		"remark": strings.TrimSpace(rule.Remark),
	}
	if rule.ID > 0 {
		var row models.CfgOrderActionRule
		if err := global.Db.First(&row, rule.ID).Error; err != nil {
			return "规则不存在或已被删除"
		}
		if err := global.Db.Model(&row).Updates(values).Error; err != nil {
			return "保存失败：" + err.Error()
		}
	} else {
		row := models.CfgOrderActionRule{
			Keyword: rule.Keyword, Action: rule.Action, Field: rule.Field,
			ValueRule: rule.ValueRule, Scope: rule.Scope, Priority: rule.Priority,
			Enabled: rule.Enabled, SortOrder: rule.SortOrder,
			Remark: strings.TrimSpace(rule.Remark),
		}
		if err := global.Db.Create(&row).Error; err != nil {
			return "保存失败：" + err.Error()
		}
	}
	if err := core.RefreshActionRules(); err != nil {
		return "已写入数据库，但刷新内存失败（下次同步后生效）：" + err.Error()
	}
	return ""
}

// DeleteOrderActionRule 硬删规则：keyword 上有唯一索引，软删会占住关键词，
// 导致用户删掉「消单」后再也加不回来；规则是配置数据，不需要保留删除历史。
func (a *App) DeleteOrderActionRule(id uint) string {
	if !a.requireAuth() {
		return "会话已失效，请重新登录"
	}
	if global.Db == nil {
		return "数据库未初始化"
	}
	if id == 0 {
		return "缺少规则 ID"
	}
	if err := global.Db.Unscoped().Delete(&models.CfgOrderActionRule{}, id).Error; err != nil {
		return "删除失败：" + err.Error()
	}
	if err := core.RefreshActionRules(); err != nil {
		return "已从数据库删除，但刷新内存失败（下次同步后生效）：" + err.Error()
	}
	return ""
}
