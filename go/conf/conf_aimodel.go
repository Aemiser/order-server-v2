package conf

type AT struct {
	Apikey    string `yaml:"apikey"`
	Timeout   int64  `yaml:"timeout"`
	ApiUrl    string `yaml:"apiUrl"`
	Modelname string `yaml:"modelname"`
}
type AiModel struct {
	Deepseek AT `yaml:"deepseek"`
	Qwen     AT `yaml:"qwen"`
}
