package conf

type Log struct {
	Level string `yaml:"level"`
	App   string `yaml:"app"`
	Dir   string `yaml:"dir"`
}
