// cfginit 一次性初始化 orderrec 库里的 cfg_* 配置表。
//
// 用法：
//
//	go run ./cmd/cfginit                 # 建表 + 表为空时灌入内置种子（不覆盖已有数据）
//	go run ./cmd/cfginit -f settings.yaml
//	go run ./cmd/cfginit -force         # 用内置种子重置全部 cfg 表（会丢失数据库中改过的配置）
//
// 应用本身每次启动也会自动执行「建表 + 空表补种」，正常无需手动运行本命令；
// 它主要用于换数据库 / 新环境部署 / 显式恢复默认配置。
package main

import (
	"flag"
	"fmt"
	"os"

	"order_server/go/core"
	"order_server/go/flags"
	"order_server/go/global"
	"order_server/go/models"

	"github.com/sirupsen/logrus"
)

func main() {
	var confPath string
	var force bool
	flag.StringVar(&confPath, "f", "settings.yaml", "配置文件路径（只需 log/db）")
	flag.BoolVar(&force, "force", false, "用内置种子覆盖全部 cfg 表（危险：会丢失数据库中修改过的配置与提示词）")
	flag.Parse()

	flags.FlagOptions.File = confPath
	global.SetConfig(core.ReadConf())
	core.InitLogrus()
	global.Db = core.InitDB()

	if force {
		logrus.Warn("正在用内置种子重置全部 cfg 配置表")
		if err := core.SeedCfg(true); err != nil {
			fmt.Fprintf(os.Stderr, "重置失败: %v\n", err)
			os.Exit(1)
		}
	} else if err := core.EnsureCfgTables(); err != nil {
		fmt.Fprintf(os.Stderr, "初始化失败: %v\n", err)
		os.Exit(1)
	}

	if err := core.LoadDBCfg(); err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	var ns, na, nb, np, nr int64
	global.Db.Model(&models.CfgSetting{}).Count(&ns)
	global.Db.Model(&models.CfgAIModel{}).Count(&na)
	global.Db.Model(&models.CfgProductBelong{}).Count(&nb)
	global.Db.Model(&models.CfgPrompt{}).Count(&np)
	global.Db.Model(&models.CfgOrderActionRule{}).Count(&nr)
	fmt.Printf("cfg 配置表就绪：cfg_settings=%d cfg_ai_models=%d cfg_product_belongs=%d cfg_prompts=%d cfg_order_action_rules=%d\n",
		ns, na, nb, np, nr)
	fmt.Printf("AI 模型映射：%v\n", global.GetAIMap())
	fmt.Printf("DeepSeek：url=%s model=%s timeout=%d\n",
		global.GetConfig().AiModel.Deepseek.ApiUrl, global.GetConfig().AiModel.Deepseek.Modelname, global.GetConfig().AiModel.Deepseek.Timeout)
	fmt.Printf("Qwen：url=%s model=%s timeout=%d\n",
		global.GetConfig().AiModel.Qwen.ApiUrl, global.GetConfig().AiModel.Qwen.Modelname, global.GetConfig().AiModel.Qwen.Timeout)
	fmt.Printf("邮箱：%s:%d；保存目录：%s；启动登录：%v\n",
		global.GetConfig().Email.Domain, global.GetConfig().Email.Port, global.GetConfig().SavePath.Excel, global.GetConfig().Auth.LoginRequired())
	fmt.Printf("商品分类：%d 个；提示词：%d 条\n", len(global.GetProductBelongs()), len(global.GetPrompts()))
	for code := range global.GetPrompts() {
		fmt.Printf("  - cfg_prompts.%s (%d 字符)\n", code, len(global.GetPrompts()[code]))
	}
}
