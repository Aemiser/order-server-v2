package core

import (
	"fmt"
	"order_server/go/global"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

func InitDB() *gorm.DB {
	cfg := global.GetConfig()
	if cfg == nil || len(cfg.DB) == 0 {
		logrus.Fatalf("数据库配置为空")
		return nil
	}

	dc := cfg.DB[0] //读
	//TODO pgsql的支持

	db, err := gorm.Open(mysql.Open(dc.GetDSN()), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true, //不生成外键约束
	})
	if err != nil {
		logrus.Fatalf("数据库连接失败: %v", err)
	}
	sqlDB, err := db.DB()
	// SetMaxIdleConns 设置空闲连接池中连接的最大数量。
	sqlDB.SetMaxIdleConns(dc.MaxIdleConns)
	// SetMaxOpenConns 设置打开数据库连接的最大数量。
	sqlDB.SetMaxOpenConns(dc.MaxOpenConns)
	// SetConnMaxLifetime 设置了可以重新使用连接的最大时间。
	sqlDB.SetConnMaxLifetime(time.Hour)
	logrus.Infof("数据库连接成功")

	// 读写库存在，则配置读写分离
	if len(cfg.DB) > 1 {
		fmt.Println("读写库存在")
		readList := []gorm.Dialector{}
		for _, d := range cfg.DB[1:] {
			readList = append(readList, mysql.Open(d.GetDSN()))
		}
		db.Use(dbresolver.Register(dbresolver.Config{
			Sources:  []gorm.Dialector{mysql.Open(dc.GetDSN())}, // 写
			Replicas: readList,                                  // 读
			Policy:   dbresolver.RandomPolicy{},
		}))
	}
	return db
}
