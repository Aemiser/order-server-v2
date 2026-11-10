package core

import (
	"fmt"
	"os"

	"order_server/go/conf"
	"order_server/go/flags"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// ReadConf 只读取启动必需的最小配置：log 与 db（连接数据库之前必须先拿到）。
// 其余业务配置（email / savePath / version / aimodel / auth）以及
// aimap.json、productbelong.json、systemPrompt.txt 与所有 AI 提示词，
// 统一由 LoadDBCfg 从数据库 cfg_* 表装载，不再写在本文件里。
func ReadConf() (c *conf.Config) {
	byteData, err := os.ReadFile(flags.FlagOptions.File)
	if err != nil {
		panic(err)
	}
	c = new(conf.Config)
	err = yaml.Unmarshal(byteData, c)
	if err != nil {
		panic(fmt.Sprintf("yaml配置文件格式错误 %s \n", err))
	}

	fmt.Printf("读取配置文件 %s 成功（仅 log/db，其余配置来自数据库 cfg_* 表）\n", flags.FlagOptions.File)
	return
}

// SetAuthRequireLogin 修改「启动需要登录」开关：写入 cfg_settings 并立即同步内存。
// 原实现定点改写 settings.yaml 的 auth.requireLogin；配置迁入数据库后不再改动该文件。
func SetAuthRequireLogin(required bool) error {
	value := "false"
	if required {
		value = "true"
	}
	if err := SaveSetting("auth", "requireLogin", value); err != nil {
		return err
	}
	logrus.Infof("已更新启动登录开关: auth.requireLogin=%s [cfg_settings]", value)
	return nil
}
