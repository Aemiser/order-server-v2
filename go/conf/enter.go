package conf

// Config 是运行时配置的内存镜像。
// settings.yaml 只解析 Log / DB 两段；其余字段由 core.LoadDBCfg 从
// cfg_settings / cfg_ai_models 填充，所以业务代码通过 global.GetConfig() 读取运行时快照。
type Config struct {
	Log      Log           `yaml:"log"`
	DB       []DB          `yaml:"db"`
	Email    Email         `yaml:"email"`
	AiModel  AiModel       `yaml:"aimodel"`
	SavePath SavePath      `yaml:"savePath"`
	Version  VersionConfig `yaml:"version"`
	Auth     AuthConf      `yaml:"auth"`
}

type VersionConfig struct {
	// Repo 已废弃：更新器改用 version.Repo 内嵌常量，保留此字段只为兼容旧配置解析。
	Repo string `yaml:"repo"`
	// Mirrors 仍可覆盖下载镜像；为空则使用更新器内嵌的默认镜像（gh-proxy 等代理 + GitHub 官方兜底）。
	// 模板支持占位符：{url}=原始 GitHub 下载地址；{tag}/{file}=便于自建或 Gitee 类站点。
	// 例： "https://gh-proxy.org/{url}" 或 "https://gitee.com/Aemiser/order_manager/releases/download/{tag}/{file}"
	Mirrors []string `yaml:"mirrors"`
}
