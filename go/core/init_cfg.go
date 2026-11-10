package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"order_server/go/cfgseed"
	"order_server/go/conf"
	"order_server/go/global"
	"order_server/go/models"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 配置表清单：settings.yaml 中除 log / db 外的配置、aimap.json、
// productbelong.json、systemPrompt.txt 以及代码里的提示词常量，全部落到这里。
func cfgTableModels() []interface{} {
	return []interface{}{
		&models.CfgSetting{},
		&models.CfgAIModel{},
		&models.CfgProductBelong{},
		&models.CfgPrompt{},
		&models.CfgOrderActionRule{}, // 订单操作自定义术语规则
		&models.CfgRevision{},        // 配置 MD5 版本行，供热更新快比对
	}
}

// EnsureCfgTables 建表并在「表为空」时灌入种子数据。
// 每次启动都会调用；已有数据一律不覆盖，保证在数据库里改过的配置不会被重置。
func EnsureCfgTables() error {
	if global.Db == nil {
		return fmt.Errorf("数据库未初始化，无法初始化 cfg 配置表")
	}
	if err := global.Db.AutoMigrate(cfgTableModels()...); err != nil {
		return fmt.Errorf("cfg 配置表迁移失败: %w", err)
	}
	return SeedCfg(false)
}

// SeedCfg 把嵌入二进制的 cfg_seed.json 写入配置表。
// force=false（默认）：仅当某张表为空时才灌入；
// force=true：先把该表清空再灌入，用于显式「恢复默认配置」。
func SeedCfg(force bool) error {
	if global.Db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	seed, err := cfgseed.Load()
	if err != nil {
		return err
	}

	if force || tableEmpty(&models.CfgSetting{}) {
		rows := make([]models.CfgSetting, 0, len(seed.Settings))
		for _, s := range seed.Settings {
			rows = append(rows, models.CfgSetting{
				Group: s.Group, Key: s.Key, Value: s.Value,
				ValueType: s.Type, Remark: s.Remark, Enabled: s.Enabled,
			})
		}
		if err := replaceRows(&rows, force); err != nil {
			return fmt.Errorf("写入 cfg_settings 失败: %w", err)
		}
	}

	if force || tableEmpty(&models.CfgAIModel{}) {
		rows := make([]models.CfgAIModel, 0, len(seed.AIModels))
		for _, s := range seed.AIModels {
			rows = append(rows, models.CfgAIModel{
				Code: s.Code, Label: s.Label, Name: s.Name, ModelType: s.ModelType,
				ApiKey: s.ApiKey, ApiUrl: s.ApiUrl, ModelName: s.ModelName,
				Timeout: s.Timeout, Enabled: s.Enabled, SortOrder: s.SortOrder, Remark: s.Remark,
			})
		}
		if err := replaceRows(&rows, force); err != nil {
			return fmt.Errorf("写入 cfg_ai_models 失败: %w", err)
		}
	}

	if force || tableEmpty(&models.CfgProductBelong{}) {
		rows := make([]models.CfgProductBelong, 0, len(seed.ProductBelongs))
		for _, s := range seed.ProductBelongs {
			b, _ := json.Marshal(s.Aliases)
			rows = append(rows, models.CfgProductBelong{
				Category: s.Category, Aliases: string(b),
				SortOrder: s.SortOrder, Enabled: s.Enabled, Remark: s.Remark,
			})
		}
		if err := replaceRows(&rows, force); err != nil {
			return fmt.Errorf("写入 cfg_product_belongs 失败: %w", err)
		}
	}

	if force || tableEmpty(&models.CfgPrompt{}) {
		rows := make([]models.CfgPrompt, 0, len(seed.Prompts))
		for _, s := range seed.Prompts {
			rows = append(rows, models.CfgPrompt{
				Code: s.Code, Name: s.Name, Scene: s.Scene, Content: s.Content,
				IsTemplate: s.IsTemplate, Enabled: s.Enabled,
				SortOrder: s.SortOrder, Remark: s.Remark,
			})
		}
		if err := replaceRows(&rows, force); err != nil {
			return fmt.Errorf("写入 cfg_prompts 失败: %w", err)
		}
	}

	// 术语规则表是本次新增的第 5 张表：存量库建表后必然为空，会灌入内置默认规则；
	// 之后用户增删改都只走数据库，force=false 绝不覆盖。
	if force || tableEmpty(&models.CfgOrderActionRule{}) {
		rows := make([]models.CfgOrderActionRule, 0, len(seed.ActionRules))
		for _, s := range seed.ActionRules {
			rows = append(rows, models.CfgOrderActionRule{
				Keyword: s.Keyword, Action: s.Action, Field: s.Field,
				ValueRule: s.ValueRule, Scope: s.Scope, Priority: s.Priority,
				Enabled: s.Enabled, SortOrder: s.SortOrder, Remark: s.Remark,
			})
		}
		if err := replaceRows(&rows, force); err != nil {
			return fmt.Errorf("写入 cfg_order_action_rules 失败: %w", err)
		}
	}

	// 存量库 cfg_prompts 非空时上面的「表为空才灌入」不会带入新版本新增的提示词，
	// 这里按 code 做一次幂等补齐，且绝不覆盖用户已修改过的正文。
	return ensurePromptCodes(seed)
}

// ensurePromptCodes 补齐种子里新增、数据库里缺失的提示词编码。
func ensurePromptCodes(seed *cfgseed.Seed) error {
	rows := make([]models.CfgPrompt, 0)
	if err := global.Db.Unscoped().Select("code").Find(&rows).Error; err != nil {
		return fmt.Errorf("读取已有提示词编码失败: %w", err)
	}
	have := make(map[string]bool, len(rows))
	for _, r := range rows {
		have[r.Code] = true
	}
	for _, s := range seed.Prompts {
		if have[s.Code] {
			continue
		}
		row := models.CfgPrompt{
			Code: s.Code, Name: s.Name, Scene: s.Scene, Content: s.Content,
			IsTemplate: s.IsTemplate, Enabled: s.Enabled,
			SortOrder: s.SortOrder, Remark: s.Remark,
		}
		if err := global.Db.Create(&row).Error; err != nil {
			return fmt.Errorf("补齐提示词 %s 失败: %w", s.Code, err)
		}
		logrus.Infof("cfg_prompts 已补齐新增提示词: %s", s.Code)
	}
	return nil
}

func tableEmpty(model interface{}) bool {
	var n int64
	if err := global.Db.Model(model).Unscoped().Count(&n).Error; err != nil {
		// 表不存在等异常时不要盲目灌数据，交给上层报错。
		return false
	}
	return n == 0
}

// replaceRows 全量替换一张配置表的内容（事务内完成，避免中途留下空表）。
func replaceRows[T any](rows *[]T, force bool) error {
	tx := global.Db.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	if force {
		var model T
		if err := tx.Unscoped().Where("1 = 1").Delete(&model).Error; err != nil {
			tx.Rollback()
			return err
		}
	}
	if len(*rows) > 0 {
		if err := tx.Create(rows).Error; err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit().Error
}

// cfgRows 是一轮轮询读到的全部配置行。
// 刻意连 enabled=false 的行一起读：把某条配置禁用也算「配置变化」，需要热更新生效。
type cfgRows struct {
	Settings    []models.CfgSetting
	AIModels    []models.CfgAIModel
	Belongs     []models.CfgProductBelong
	Prompts     []models.CfgPrompt
	ActionRules []models.CfgOrderActionRule
}

// readCfgRowsInto 以旧行打底，只查询 sections 里列出的表；sections 为空表示全量。
// 未列出的表原样保留，这样增量拉取后仍能用完整的 rows 发布一份一致的内存快照
// （conf.Config 由 settings + ai_models 共同派生，不能分表单独写）。
// 每个表的加载器注册在 cfg_sections.go：查询前把目标 slice 置 nil，避免 GORM Find
// 往旧数据后面追加；只注册了变更检测元数据、没有加载器的节会被安全跳过。
func readCfgRowsInto(r *cfgRows, sections []string, quiet bool) error {
	db := global.Db
	if quiet {
		db = db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	}
	targets := sections
	if len(targets) == 0 {
		targets = cfgSections
	}
	seen := make(map[string]bool, len(targets))
	for _, name := range targets {
		if seen[name] {
			continue
		}
		seen[name] = true
		loader, ok := cfgLoaders[name]
		if !ok {
			continue
		}
		if err := loader(db, r); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func readCfgRows(quiet bool) (cfgRows, error) {
	var r cfgRows
	err := readCfgRowsInto(&r, nil, quiet)
	return r, err
}

// digest 按表算内容指纹：只比较内容，不依赖 updated_at，
// 这样直接用 SQL / 别的管理工具改配置也能被检测出来。
func (r cfgRows) digest() map[string]string {
	hash := func(v interface{}) string {
		b, err := json.Marshal(v)
		if err != nil {
			return "marshal-error"
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:8])
	}
	return map[string]string{
		"cfg_product_belongs":    hash(r.Belongs),
		"cfg_prompts":            hash(r.Prompts),
		"cfg_order_action_rules": hash(r.ActionRules),
	}
}

func enabledSettings(rows []models.CfgSetting) []models.CfgSetting {
	out := make([]models.CfgSetting, 0, len(rows))
	for _, r := range rows {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out
}

func enabledAIModels(rows []models.CfgAIModel) []models.CfgAIModel {
	out := make([]models.CfgAIModel, 0, len(rows))
	for _, r := range rows {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out
}

func enabledBelongs(rows []models.CfgProductBelong) []models.CfgProductBelong {
	out := make([]models.CfgProductBelong, 0, len(rows))
	for _, r := range rows {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out
}

func enabledPrompts(rows []models.CfgPrompt) []models.CfgPrompt {
	out := make([]models.CfgPrompt, 0, len(rows))
	for _, r := range rows {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out
}

func enabledActionRules(rows []models.CfgOrderActionRule) []models.CfgOrderActionRule {
	out := make([]models.CfgOrderActionRule, 0, len(rows))
	for _, r := range rows {
		if r.Enabled && strings.TrimSpace(r.Keyword) != "" {
			out = append(out, r)
		}
	}
	return out
}

// applyCfgRows 把一批配置行整体发布成新的内存快照。
// 纯内存操作：热更新每轮都走这里，不做 AutoMigrate、不灌种子、不写库。
func applyCfgRows(r cfgRows) {
	settings := enabledSettings(r.Settings)
	// 以当前配置为底（保留 settings.yaml 里连库前必须读到的 Log / DB），
	// 派生一份新的 conf.Config，避免原地改其它协程正在读的对象。
	base := global.GetConfig()
	next := conf.Config{}
	if base != nil {
		next = *base
	}
	applySettings(&next, settings)
	aiMap := applyAIModels(&next, enabledAIModels(r.AIModels))
	belongMap := buildProductBelongs(enabledBelongs(r.Belongs))
	promptMap := buildPrompts(enabledPrompts(r.Prompts))
	rules := enabledActionRules(r.ActionRules)
	global.SetCfgSnapshot(global.CfgSnapshot{
		Config:         &next,
		Settings:       settings,
		AIMap:          aiMap,
		ProductBelongs: belongMap,
		Prompts:        promptMap,
		ActionRules:    rules,
	})
	// Phase 3：同一轮快照再投影一份到 ConfigProvider，供新模块按端口读取 / 订阅。
	// 放在 SetCfgSnapshot 之后，保证两个视图发布顺序一致、不会半新半旧。
	publishCfgProjection(r, revisionOf(r.digest()))
	logrus.Debugf("配置快照已发布：%d 项通用配置 / %d 个AI模型 / %d 个商品分类 / %d 条提示词 / %d 条订单操作术语",
		len(settings), len(aiMap), len(belongMap), len(promptMap), len(rules))
}

// LoadDBCfg 从数据库装载全部业务配置，并记录本轮配置指纹作为热更新基线。
// 调用时机：InitDB 之后、任何依赖配置/提示词的逻辑之前。
func LoadDBCfg() error {
	if global.Db == nil {
		return fmt.Errorf("数据库未初始化，无法加载 cfg 配置")
	}
	// 建表/灌种子失败不直接阻断：表可能已存在且可读，继续尝试读取。
	if err := EnsureCfgTables(); err != nil {
		logrus.Warnf("cfg 配置表初始化告警: %v", err)
	}
	rows, err := readCfgRows(false)
	if err != nil {
		return fmt.Errorf("加载数据库配置失败: %w", err)
	}
	applyCfgRows(rows)
	// 初始化 cfg_revision 版本行，并记下逐表 MD5 基线。首次启动（表刚建好、
	// 还没有 id=1 的行）会在这里补写，之后定时器就能走逐表快比对。
	var pair cfgRevisionPair
	if p, err := readCfgRevisionPair(false); err == nil {
		pair = p
		if p.Stored != p.Actual {
			if err := writeCfgRevision(p.Actual); err != nil {
				logrus.Warnf("写入配置 MD5 版本行失败（将回退全量比对）: %v", err)
			}
		}
	} else {
		logrus.Warnf("配置 MD5 初始化失败（将回退全量比对）: %v", err)
	}
	setCfgBaseline(rows, pair)
	logrus.Infof("数据库配置加载完成：%d 项通用配置 / %d 个AI模型 / %d 个商品分类 / %d 条提示词 / %d 条订单操作术语，配置 MD5=%s",
		len(enabledSettings(rows.Settings)), len(enabledAIModels(rows.AIModels)),
		len(enabledBelongs(rows.Belongs)), len(enabledPrompts(rows.Prompts)),
		len(enabledActionRules(rows.ActionRules)), pair.Actual)
	return nil
}

// ReloadDBCfg 先用逐表 MD5 判断「哪张表改了」，五张表全一致就直接跳过本轮；
// 不一致时只拉变化的表，其余表复用上次已加载的行，再用 Go 内容指纹复核。
//
// 快比对一条 SQL 完成（cfg_revision 里存的整体 MD5 + 五张表各自实算的 MD5），
// 不再把没变化的提示词正文从远程 MySQL 拉回本地。轻量查询失败时自动退回
// 全量比对，保证老环境 / 表未建好时功能不受影响。
func ReloadDBCfg(force bool, trigger string) (CfgChange, error) {
	if global.Db == nil {
		return CfgChange{}, fmt.Errorf("数据库未初始化")
	}

	pair, pairErr := readCfgRevisionPair(true)
	if pairErr != nil {
		// 降级：cfg_revision 未建表 / 权限不足 / MySQL 版本差异等，
		// 退回「拉全量 + 内容指纹」，行为与旧版完全一致。
		logrus.Warnf("配置 MD5 快比对不可用，回退全量比对: %v", pairErr)
		return reloadBySections(force, trigger, cfgRevisionPair{}, cfgSections)
	}
	// 外部直接改 cfg_* 没维护 cfg_revision 时，用实算值把版本行修正回来。
	if pair.Stored != pair.Actual {
		if err := writeCfgRevision(pair.Actual); err != nil {
			logrus.Warnf("回写配置 MD5 版本行失败（不影响热更新）: %v", err)
		}
	}

	targets := pair.changedSections(snapshotSectionMD5())
	_, oldDigests, _ := snapshotCfgBaseline()
	if len(oldDigests) == 0 {
		// 还没有完整基线，不能用旧行打底，必须全量读一次。
		targets = append([]string(nil), cfgSections...)
	}
	if len(targets) == 0 {
		// 逐表 MD5 全一致：连一张配置表都不用读（手动「立即同步」同样走这里）。
		noteCfgCheck("逐表 MD5 快比对（未读配置表）", false)
		noteCfgSections(nil)
		return CfgChange{
			At:       nowStamp(),
			Trigger:  trigger,
			Revision: currentRevision(),
		}, nil
	}
	return reloadBySections(force, trigger, pair, targets)
}

// reloadBySections 只查询 targets 里的表，以旧行打底合成完整配置，
// 再用 Go 侧内容指纹复核。pair 为零值（降级路径）时会自己补算一次。
func reloadBySections(force bool, trigger string, pair cfgRevisionPair, targets []string) (CfgChange, error) {
	oldRows, oldDigests, _ := snapshotCfgBaseline()
	if len(oldDigests) == 0 || len(targets) == 0 {
		targets = append([]string(nil), cfgSections...)
	}
	rows := oldRows
	if err := readCfgRowsInto(&rows, targets, true); err != nil {
		return CfgChange{}, err
	}
	newDigests := rows.digest()

	changedSections := make([]string, 0, len(targets))
	for _, section := range targets {
		if oldDigests[section] != newDigests[section] {
			changedSections = append(changedSections, section)
		}
	}
	change := CfgChange{
		At:       nowStamp(),
		Trigger:  trigger,
		Revision: revisionOf(newDigests),
	}
	if pair.Actual == "" {
		pair = cfgBaselinePair()
	}
	if len(changedSections) == 0 && !force {
		// MD5 变了但内容指纹没变：例如外部只改了不在清单里的列，或纯属哈希抖动。
		// 不播报、不替换快照，只把基线对齐，避免无谓的热更新事件。
		setCfgBaseline(rows, pair)
		noteCfgCheck("内容指纹复核（MD5 有差异）", true)
		noteCfgSections(targets)
		return change, nil
	}

	if len(changedSections) > 0 {
		applyCfgRows(rows)
		change.Changed = true
		change.Sections = changedSections
		change.Summary = describeCfgChange(oldRows, rows, changedSections)
		logrus.Infof("检测到数据库配置变化，已热更新：%s（本轮只读 %s）",
			change.Summary, strings.Join(targets, ", "))
	} else {
		// 手动强制同步：指纹没变也刷新一遍内存，确保与数据库绝对一致。
		applyCfgRows(rows)
		change.Summary = "配置无变化，已重新装载当前数据库配置"
	}
	setCfgBaseline(rows, pair)
	if len(targets) < len(cfgSections) {
		noteCfgCheck("逐表增量比对（只读变化的表）", true)
	} else {
		noteCfgCheck("全量配置比对", true)
	}
	noteCfgSections(targets)
	return change, nil
}

// cfgBaselinePair 补齐降级路径的逐表 MD5 基线；拿不到就返回零值，
// setCfgBaseline 会清空逐表基线，下一轮继续走全量比对（安全，不会漏检）。
func cfgBaselinePair() cfgRevisionPair {
	p, err := SyncCfgRevision()
	if err != nil {
		logrus.Warnf("补算配置 MD5 失败: %v", err)
		return cfgRevisionPair{}
	}
	return p
}

// describeCfgChange 把「哪张表变了」翻译成人话，提示词精确到具体哪一条。
func describeCfgChange(oldRows, newRows cfgRows, sections []string) string {
	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		switch section {
		case "cfg_prompts":
			old := map[string]string{}
			for _, p := range oldRows.Prompts {
				old[p.Code] = p.Content
			}
			named := false
			for _, p := range newRows.Prompts {
				if v, ok := old[p.Code]; !ok || v != p.Content {
					name := p.Name
					if name == "" {
						name = p.Code
					}
					parts = append(parts, "提示词「"+name+"」")
					named = true
				}
			}
			if !named {
				parts = append(parts, "提示词配置")
			}
		case "cfg_order_action_rules":
			old := map[string]string{}
			for _, r := range oldRows.ActionRules {
				old[r.Keyword] = r.Action + "|" + r.Field
			}
			named := false
			for _, r := range newRows.ActionRules {
				if v, ok := old[r.Keyword]; !ok || v != r.Action+"|"+r.Field {
					parts = append(parts, "订单操作术语「"+r.Keyword+"」")
					named = true
					break
				}
			}
			if !named {
				parts = append(parts, "订单操作术语")
			}
		case "cfg_ai_models":
			parts = append(parts, "AI 模型参数")
		case "cfg_product_belongs":
			parts = append(parts, "商品归属映射")
		case "cfg_settings":
			parts = append(parts, "通用设置")
		default:
			parts = append(parts, section)
		}
	}
	return strings.Join(parts, "、")
}

// applySettings 把 KV 行映射回 conf.Config；纯函数，便于脱离数据库单测。
// 只有数据库里存在的键才覆盖，缺省项保留 settings.yaml / 零值，避免误清配置。
func applySettings(cfg *conf.Config, rows []models.CfgSetting) {
	if cfg == nil {
		return
	}
	get := func(group, key string) (string, bool) {
		for _, r := range rows {
			if r.Group == group && r.Key == key {
				return r.Value, true
			}
		}
		return "", false
	}
	str := func(group, key string, dst *string) {
		if v, ok := get(group, key); ok {
			*dst = v
		}
	}
	num := func(group, key string, dst *int) {
		if v, ok := get(group, key); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				*dst = n
			}
		}
	}
	boolean := func(group, key string, dst *bool) {
		if v, ok := get(group, key); ok {
			*dst = parseCfgBool(v)
		}
	}

	str("email", "domain", &cfg.Email.Domain)
	num("email", "port", &cfg.Email.Port)
	str("email", "sendEmail", &cfg.Email.SendEmail)
	str("email", "AuthCode", &cfg.Email.AuthCode)
	str("email", "sendNickname", &cfg.Email.SendNickname)
	boolean("email", "SSL", &cfg.Email.SSL)
	boolean("email", "TLS", &cfg.Email.TLS)

	str("savePath", "excel", &cfg.SavePath.Excel)
	str("savePath", "json", &cfg.SavePath.Json)

	str("version", "repo", &cfg.Version.Repo)
	if v, ok := get("version", "mirrors"); ok && strings.TrimSpace(v) != "" {
		var mirrors []string
		if err := json.Unmarshal([]byte(v), &mirrors); err == nil {
			cfg.Version.Mirrors = mirrors
		}
	}

	if v, ok := get("auth", "requireLogin"); ok {
		b := parseCfgBool(v)
		cfg.Auth.RequireLogin = &b
	}
}

func parseCfgBool(v string) bool {
	s := strings.ToLower(strings.TrimSpace(v))
	return s == "true" || s == "1" || s == "yes" || s == "on"
}

// applyAIModels 从 cfg_ai_models 行构造模型映射并写进 conf.Config。
func applyAIModels(cfg *conf.Config, rows []models.CfgAIModel) map[string]int {
	aiMap := make(map[string]int, len(rows))
	for _, r := range rows {
		if r.Label != "" {
			aiMap[r.Label] = r.ModelType
		}
		at := conf.AT{
			Apikey: r.ApiKey, ApiUrl: r.ApiUrl,
			Modelname: r.ModelName, Timeout: r.Timeout,
		}
		switch strings.ToLower(r.Code) {
		case "deepseek":
			cfg.AiModel.Deepseek = at
		case "qwen":
			cfg.AiModel.Qwen = at
		}
	}
	return aiMap
}

func buildProductBelongs(rows []models.CfgProductBelong) map[string][]string {
	m := make(map[string][]string, len(rows))
	for _, r := range rows {
		aliases := []string{}
		if strings.TrimSpace(r.Aliases) != "" {
			if err := json.Unmarshal([]byte(r.Aliases), &aliases); err != nil {
				logrus.Warnf("商品分类 %s 的别名不是合法 JSON: %v", r.Category, err)
				aliases = []string{}
			}
		}
		m[r.Category] = aliases
	}
	return m
}

func buildPrompts(rows []models.CfgPrompt) map[string]string {
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Code] = r.Content
	}
	return m
}

// SaveSetting 写入/更新一条通用配置（数据库为准），并同步内存快照。
func SaveSetting(group, key, value string) error {
	if global.Db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	var row models.CfgSetting
	err := global.Db.Unscoped().Where("cfg_group = ? AND cfg_key = ?", group, key).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		row = models.CfgSetting{
			Group: group, Key: key, Value: value,
			ValueType: "string", Enabled: true,
		}
		if err := global.Db.Create(&row).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		if err := global.Db.Unscoped().Model(&row).Update("cfg_value", value).Error; err != nil {
			return err
		}
		row.Value = value
	}
	// 同步内存：只替换/追加本键，再整体发布一份新快照。
	rows := global.GetSettings()
	updated := false
	for i := range rows {
		if rows[i].Group == group && rows[i].Key == key {
			rows[i].Value = value
			updated = true
			break
		}
	}
	if !updated {
		rows = append(rows, row)
	}
	global.ApplySettings(rows, applySettings)
	// 本进程刚写过库：刷新 cfg_revision 的 MD5，并把内容指纹与 MD5 基线
	// 一起更新，免得定时器下一轮把自己写的变更又播报一次。
	if r, err := readCfgRows(false); err == nil {
		pair, err := SyncCfgRevision()
		if err != nil {
			logrus.Warnf("刷新配置 MD5 失败（不影响本次保存）: %v", err)
		}
		setCfgBaseline(r, pair)
	}
	return nil
}

// RefreshActionRules 应用内改完 cfg_order_action_rules 后立即刷新内存快照。
// 只替换快照里的规则段，其它配置行保持不动；同时刷新逐表 MD5 基线，
// 免得 60 秒定时器把本进程刚写的变更又当成外部改动播报一次。
func RefreshActionRules() error {
	if global.Db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	rows, err := readCfgRows(false)
	if err != nil {
		return err
	}
	global.SetActionRules(enabledActionRules(rows.ActionRules))
	pair, err := SyncCfgRevision()
	if err != nil {
		return err
	}
	setCfgBaseline(rows, pair)
	return nil
}

// GetSetting 读取一条通用配置的值。
func GetSetting(group, key string) (string, bool) {
	for _, r := range global.GetSettings() {
		if r.Group == group && r.Key == key {
			return r.Value, true
		}
	}
	return "", false
}
