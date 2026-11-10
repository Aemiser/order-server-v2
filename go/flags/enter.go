package flags

import (
	"flag"
	"os"
)

type Options struct {
	File    string
	DB      bool
	Version bool
	Type    string
	Sub     string
	Es      bool
}

var FlagOptions = new(Options)

func Parse() {
	flag.StringVar(&FlagOptions.File, "f", "settings.yaml", "配置文件")
	flag.BoolVar(&FlagOptions.DB, "db", false, "迁移数据库")
	flag.BoolVar(&FlagOptions.Version, "v", false, "查看版本")
	flag.Parse()
}

func Run() {
	if FlagOptions.DB {
		FlagDB()
		os.Exit(0)
	}
}
