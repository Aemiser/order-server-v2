package conf

import (
	"fmt"
)

type DB struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	User         string `yaml:"user"`
	Password     string `yaml:"password"`
	DBName       string `yaml:"dbname"`
	Debug        bool   `yaml:"debug"`
	MaxIdleConns int    `yaml:"maxIdleConns"`
	MaxOpenConns int    `yaml:"maxOpenConns"`
}

func (d DB) GetDSN() string {
	dsn := fmt.Sprintf(
		"%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		d.User,
		d.Password,
		d.Host,
		d.Port,
		d.DBName,
	)
	return dsn
}

func (d DB) Empty() bool {
	return d.Host == "" && d.Port == 0 && d.User == "" && d.Password == "" && d.DBName == ""
}

func (d DB) Addr() string {
	return fmt.Sprintf("%s:%d", d.Host, d.Port)
}
