package conf

type Email struct {
	Domain       string `yaml:"domain" json:"domain"`
	Port         int    `yaml:"port" json:"port"`
	SendEmail    string `yaml:"sendEmail" json:"sendEmail"`
	AuthCode     string `yaml:"AuthCode" json:"AuthCode"`
	SendNickname string `yaml:"sendNickname" json:"sendNickname"`
	SSL          bool   `yaml:"SSL" json:"SSL"`
	TLS          bool   `yaml:"TLS" json:"TLS"`
}
