package core

// ConfigProvider 接线（Phase 3）。
//
// global.CfgSnapshot 仍是配置的唯一真值来源，热更新链路（逐表 MD5 快比对 →
// 增量拉取 → 内容指纹复核 → 整体发布快照）一行未改。这里只做一件事：
// 每轮快照发布成功后，把「非敏感、可直接消费的配置投影」写进 ConfigProvider，
// 让新模块通过 ports.ConfigProvider 读取配置并订阅热更新，而不必去挖 global。
//
// 刻意不投影的内容：AI 模型的 api_key / api_url、提示词正文、客户与地址数据。
// Provider 是进程内只读视图，不该成为第二份敏感数据副本。

import (
	"strconv"

	"order_server/go/config"
	"order_server/go/ports"
)

// cfgProviderInstance 是全进程唯一的配置端口实例。
// 由 core 持有而不是各模块各自 new，保证所有模块看到的是同一轮快照。
var cfgProviderInstance = config.NewMemory(nil)

// CfgProvider 返回主程序的配置端口。第三方模块只依赖 ports.ConfigProvider，
// 用 Get 读配置、Watch 订阅热更新；写入由主程序的热更新流程统一负责。
func CfgProvider() ports.ConfigProvider {
	return cfgProviderInstance
}

// cfgProjection 把一轮配置行整理成「命名空间 -> 键值」，是纯函数，方便单测。
// 每个已注册的配置节都有一个命名空间，即使本节没有可投影内容也保留，
// 这样 Watch 任意一节都能在每轮快照发布时收到一致通知。
func cfgProjection(r cfgRows, revision string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(cfgSections))
	for _, name := range cfgSections {
		out[name] = map[string]string{"__revision__": revision}
	}
	counts := map[string]int{
		"cfg_settings":           len(enabledSettings(r.Settings)),
		"cfg_ai_models":          len(enabledAIModels(r.AIModels)),
		"cfg_product_belongs":    len(enabledBelongs(r.Belongs)),
		"cfg_prompts":            len(enabledPrompts(r.Prompts)),
		"cfg_order_action_rules": len(enabledActionRules(r.ActionRules)),
	}
	for name, kv := range out {
		kv["__count__"] = strconv.Itoa(counts[name])
	}
	// 通用设置是最像「配置」的一节，逐条投影成 group.key -> value。
	// 该表只放版本 / 开关等通用项，不含凭据；若将来有人往里塞敏感值，
	// 应在这里加白名单而不是放宽整表投影。
	if kv, ok := out["cfg_settings"]; ok {
		for _, s := range enabledSettings(r.Settings) {
			kv[s.Group+"."+s.Key] = s.Value
		}
	}
	return out
}

// publishCfgProjection 把一轮快照整体发布到各命名空间并触发 Watch。
// 单节发布失败（监听者 panic）由 Provider 内部兜住，不会冒泡影响配置装载。
func publishCfgProjection(r cfgRows, revision string) {
	for ns, kv := range cfgProjection(r, revision) {
		cfgProviderInstance.Publish(ns, kv)
	}
}
