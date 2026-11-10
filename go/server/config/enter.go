package config

//type ReConf struct {
//	Deepseek conf.AiModel `json:"deepseek"`
//	SavePath conf.SavePath `json:"savePath"`
//}
//
//func SetConf(conf ReConf) {
//	// 修改之前
//	_deepseek := global.Config.Deepseek
//	_savepath := global.Config.SavePath
//	// 修改之后
//	global.Config.Deepseek = conf.Deepseek
//	global.Config.SavePath = conf.SavePath
//	core.WriteConf()
//	logtent := "修改配置成功:\n" + " Deepseek: " + _deepseek.Apikey + " -> " + conf.Deepseek.Apikey + "\n" +
//		" SavePath: " + _savepath.Excel + " -> " + conf.SavePath.Excel + "\n" +
//		" SavePath: " + _savepath.Json + " -> " + conf.SavePath.Json
//	alog.Logaction(enum.LogInfoLevel, "修改配置成功", logtent)
//}
//
//func GetConf() ReConf {
//	var cf = ReConf{
//		Deepseek: global.Config.Deepseek,
//		SavePath: global.Config.SavePath,
//	}
//	return cf
//}
