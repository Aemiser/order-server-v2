package order

import (
	"fmt"
	"order_server/go/global"
	"order_server/go/models"
	"order_server/go/models/enum"
	alog "order_server/go/server/log"
	"order_server/go/utils/save"
	"time"

	"github.com/sirupsen/logrus"
)

func SaveToExcel() {
	addressList := global.AddressList
	now := time.Now()
	filename := fmt.Sprintf("%s.xlsx", now.Format("2006-01-02"))
	// 保存为Excel文件
	excelFile, err := save.SaveToExcel(addressList, filename)
	if err != nil {
		logrus.Errorf("保存Excel文件失败: %v", err)
		alog.Logaction(enum.LogErrLevel, "保存Excel文件失败", err.Error())
	}
	logrus.Infof("Excel文件已保存: %s\n", excelFile)
	logtent := "保存路径：\n" + global.GetConfig().SavePath.Excel + "\\" + filename
	alog.Logaction(enum.LogInfoLevel, "保存Excel文件成功", logtent)

}

func SaveToJSON(addressList []models.AddressInfo) {
	now := time.Now()
	filename := fmt.Sprintf("%s.json", now.Format("2006-01-02"))
	// 保存为JSON文件
	jsonFile, err := save.SaveToJSON(addressList, filename)
	if err != nil {
		logrus.Errorf("保存JSON文件失败: %v", err)
		alog.Logaction(enum.LogErrLevel, "保存JSON文件失败", err.Error())
	}
	logrus.Infof("JSON文件已保存: %s\n", jsonFile)
	logtent := "保存路径：\n" + global.GetConfig().SavePath.Json + "\\" + filename
	alog.Logaction(enum.LogInfoLevel, "保存JSON文件成功", logtent)
}
