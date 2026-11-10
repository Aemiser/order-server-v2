package conf

// AuthConf 登录相关配置。
type AuthConf struct {
	// RequireLogin 控制软件启动时是否必须输入账号密码。
	// 用指针是为了区分「配置里没写」和「显式写成 false」：
	// 老版本 settings.yaml 没有 auth 这一节，此时按 true 处理，
	// 保证升级之后原有的启动登录行为不发生任何变化。
	RequireLogin *bool `yaml:"requireLogin"`
}

// LoginRequired 返回启动时是否需要登录；未配置时默认需要登录。
func (c AuthConf) LoginRequired() bool {
	return c.RequireLogin == nil || *c.RequireLogin
}
