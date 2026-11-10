package save

import (
	"encoding/json"
	"fmt"
	"order_server/go/models"
	"order_server/go/utils/order"
	"os"
	"path/filepath"

	"github.com/xuri/excelize/v2"
)

// SaveToExcel 保存地址信息到Excel文件
func SaveToExcel(_addressList []models.AddressInfo, filename string) (string, error) {
	// 转换为ExceladdressInfo
	var addressList []ordertutil.ExceladdressInfo
	addressList = ordertutil.AddressToExcelAddress(_addressList)

	// 创建Excel文件
	f := excelize.NewFile()
	defer func() {
		if err := f.Close(); err != nil {
			fmt.Println(err)
		}
	}()

	// 创建工作表
	sheetName := "地址识别结果"
	index, err := f.NewSheet(sheetName)
	if err != nil {
		return "", fmt.Errorf("创建工作表失败: %w", err)
	}

	// 删除默认的Sheet1
	f.DeleteSheet("Sheet1")

	// 设置活动工作表
	f.SetActiveSheet(index)

	// 设置表头样式
	headerStyle, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{
			Bold: true,
			Size: 12,
		},
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#E0E0E0"},
			Pattern: 1,
		},
		Border: []excelize.Border{
			{Type: "left", Color: "000000", Style: 1},
			{Type: "right", Color: "000000", Style: 1},
			{Type: "top", Color: "000000", Style: 1},
			{Type: "bottom", Color: "000000", Style: 1},
		},
		Alignment: &excelize.Alignment{
			Horizontal: "center",
			Vertical:   "center",
		},
	})
	if err != nil {
		return "", fmt.Errorf("创建表头样式失败: %w", err)
	}

	// 设置数据行样式
	dataStyle, err := f.NewStyle(&excelize.Style{
		Border: []excelize.Border{
			{Type: "left", Color: "000000", Style: 1},
			{Type: "right", Color: "000000", Style: 1},
			{Type: "top", Color: "000000", Style: 1},
			{Type: "bottom", Color: "000000", Style: 1},
		},
		Alignment: &excelize.Alignment{
			Vertical: "center",
		},
	})
	if err != nil {
		return "", fmt.Errorf("创建数据样式失败: %w", err)
	}

	// 写入表头
	headers := []string{"序号", "收件人", "手机号", "收货地址", "商品规格"}
	for i, header := range headers {
		cell := fmt.Sprintf("%s1", string(rune('A'+i)))
		f.SetCellValue(sheetName, cell, header)
		f.SetCellStyle(sheetName, cell, cell, headerStyle)
	}

	// 写入数据行
	for i, addr := range addressList {
		rowNum := i + 2 // 从第2行开始

		// 序号
		f.SetCellValue(sheetName, fmt.Sprintf("A%d", rowNum), i+1)
		f.SetCellStyle(sheetName, fmt.Sprintf("A%d", rowNum), fmt.Sprintf("A%d", rowNum), dataStyle)

		// 收件人
		f.SetCellValue(sheetName, fmt.Sprintf("B%d", rowNum), addr.Recipient)
		f.SetCellStyle(sheetName, fmt.Sprintf("B%d", rowNum), fmt.Sprintf("B%d", rowNum), dataStyle)

		// 手机号
		f.SetCellValue(sheetName, fmt.Sprintf("C%d", rowNum), addr.Phone)
		f.SetCellStyle(sheetName, fmt.Sprintf("C%d", rowNum), fmt.Sprintf("C%d", rowNum), dataStyle)

		// 收货地址
		f.SetCellValue(sheetName, fmt.Sprintf("D%d", rowNum), addr.Address)
		f.SetCellStyle(sheetName, fmt.Sprintf("D%d", rowNum), fmt.Sprintf("D%d", rowNum), dataStyle)

		// 商品规格
		f.SetCellValue(sheetName, fmt.Sprintf("E%d", rowNum), addr.ProductSpecs)
		f.SetCellStyle(sheetName, fmt.Sprintf("E%d", rowNum), fmt.Sprintf("E%d", rowNum), dataStyle)
	}

	// 设置列宽
	f.SetColWidth(sheetName, "A", "A", 8)  // 序号
	f.SetColWidth(sheetName, "B", "B", 25) // 收件人
	f.SetColWidth(sheetName, "C", "C", 15) // 手机号
	f.SetColWidth(sheetName, "D", "D", 80) // 收货地址
	f.SetColWidth(sheetName, "E", "E", 25) // 商品规格

	// 保存文件
	if err := f.SaveAs(filename); err != nil {
		return "", fmt.Errorf("保存Excel文件失败: %w", err)
	}

	// 获取绝对路径
	absPath, _ := filepath.Abs(filename)
	return absPath, nil
}

// SaveToJSON 保存地址信息到JSON文件
func SaveToJSON(addressList []models.AddressInfo, filename string) (string, error) {
	// 格式化JSON
	jsonData, err := json.MarshalIndent(addressList, "", "  ")
	if err != nil {
		return "", fmt.Errorf("JSON序列化失败: %w", err)
	}

	// 写入文件
	if err := os.WriteFile(filename, jsonData, 0644); err != nil {
		return "", fmt.Errorf("写入JSON文件失败: %w", err)
	}

	// 获取绝对路径
	absPath, _ := filepath.Abs(filename)
	return absPath, nil
}
