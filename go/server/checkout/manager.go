package checkout

import (
	"fmt"
	"order_server/go/models"
	"order_server/go/utils/execl"
	"sort"
	"strconv"
	"time"

	"github.com/xuri/excelize/v2"
)

// ProductSpecs 商品规格集合
type ProductSpecs struct {
	Name            string
	Specs           []string
	SpecMap         map[string]int     // 规格 -> 总数量
	PricePerKgMap   map[string]float64 // 规格 -> 每斤价格
	PricePerItemMap map[string]float64 // 规格 -> 每件价格
	TotalQty        int
}

// SpecManager 规格管理器
type SpecManager struct {
	Orders     []models.Order
	DateMap    map[string][]models.Order
	ProductMap map[string]*ProductSpecs
}

// NewSpecManager 创建规格管理器
func NewSpecManager() *SpecManager {
	return &SpecManager{
		Orders:     make([]models.Order, 0),
		DateMap:    make(map[string][]models.Order),
		ProductMap: make(map[string]*ProductSpecs),
	}
}

// AddOrder 添加订单
func (sm *SpecManager) AddOrder(order models.Order) {
	sm.Orders = append(sm.Orders, order)

	// 使用格式化的日期字符串作为key
	dateKey := execl.FormatDate(order.Date)
	sm.DateMap[dateKey] = append(sm.DateMap[dateKey], order)

	// 按商品分组
	if _, exists := sm.ProductMap[order.ProductName]; !exists {
		sm.ProductMap[order.ProductName] = &ProductSpecs{
			Name:            order.ProductName,
			Specs:           []string{},
			SpecMap:         make(map[string]int),
			PricePerKgMap:   make(map[string]float64),
			PricePerItemMap: make(map[string]float64),
			TotalQty:        0,
		}
	}

	product := sm.ProductMap[order.ProductName]
	product.SpecMap[order.Spec] = product.SpecMap[order.Spec] + order.Quantity
	product.TotalQty += order.Quantity

	// 更新价格信息（如果有）
	if order.PricePerKg > 0 {
		product.PricePerKgMap[order.Spec] = order.PricePerKg
	}
	if order.PricePerItem > 0 {
		product.PricePerItemMap[order.Spec] = order.PricePerItem
	}
}

// BuildSpecList 构建规格列表
func (sm *SpecManager) BuildSpecList() {
	for _, product := range sm.ProductMap {
		// 收集所有规格
		for spec := range product.SpecMap {
			product.Specs = append(product.Specs, spec)
		}
		// 规格排序（按数字大小）
		sort.Slice(product.Specs, func(i, j int) bool {
			numI := execl.ExtractNumber(product.Specs[i])
			numJ := execl.ExtractNumber(product.Specs[j])
			return numI < numJ
		})
	}
}

// GenerateExcel 生成Excel（带价格和金额计算）
func (sm *SpecManager) GenerateExcel(filename string) error {
	sm.BuildSpecList()

	f := excelize.NewFile()
	defer f.Close()

	sheetName := "商品销售统计"
	f.SetSheetName("Sheet1", sheetName)

	// 获取所有日期并排序
	dates := make([]string, 0, len(sm.DateMap))
	for date := range sm.DateMap {
		dates = append(dates, date)
	}
	sort.Strings(dates)

	// 获取所有商品并排序
	products := make([]string, 0, len(sm.ProductMap))
	for product := range sm.ProductMap {
		products = append(products, product)
	}
	sort.Strings(products)

	// 创建样式
	styles := sm.createStyles(f)

	// ===== 设置表头结构 =====
	// 每个商品规格占据4行：
	// 第1行：商品名称
	// 第2行：规格
	// 第3行：每斤/元 和 每件/元
	// 第4行：金额

	col := 2 // B列开始

	// 记录每个商品的起始列和结束列
	productCols := make(map[string]int)    // 商品起始列
	productEndCols := make(map[string]int) // 商品结束列

	// 第1行：设置商品名称
	for _, product := range products {
		specs := sm.ProductMap[product].Specs
		productCols[product] = col
		productEndCols[product] = col + len(specs) - 1

		// 商品名称放在第1行，合并该商品的所有规格列
		startCell := execl.GetColumnLetter(col) + "1"
		endCell := execl.GetColumnLetter(col+len(specs)-1) + "1"
		f.MergeCell(sheetName, startCell, endCell)
		f.SetCellValue(sheetName, startCell, product)
		f.SetCellStyle(sheetName, startCell, endCell, styles["productHeader"])

		col += len(specs)
	}

	// 第2行：设置规格
	col = 2 // 重置列
	for _, product := range products {
		specs := sm.ProductMap[product].Specs
		for i, spec := range specs {
			specCell := execl.GetColumnLetter(col+i) + "2"
			f.SetCellValue(sheetName, specCell, spec)
			f.SetCellStyle(sheetName, specCell, specCell, styles["specHeader"])
		}
		col += len(specs)
	}

	//// 第3行：设置价格信息（每斤/元 和 每件/元）
	//col = 2 // 重置列
	//priceRow := 3
	//priceInfoStyle, _ := f.NewStyle(&excelize.Style{
	//	Fill: excelize.Fill{
	//		Type:    "pattern",
	//		Color:   []string{"#FFE699"},
	//		Pattern: 1,
	//	},
	//	Alignment: &excelize.Alignment{
	//		Horizontal: "center",
	//		WrapText:   true, // 允许文本换行
	//	},
	//})
	//
	//for _, product := range products {
	//	specs := sm.ProductMap[product].Specs
	//	productInfo := sm.ProductMap[product]
	//
	//	for i, spec := range specs {
	//		cell := execl.GetColumnLetter(col+i) + strconv.Itoa(priceRow)
	//
	//		// 获取价格信息
	//		pricePerKg := productInfo.PricePerKgMap[spec]
	//		pricePerItem := productInfo.PricePerItemMap[spec]
	//
	//		// 显示价格信息
	//		priceText := ""
	//		if pricePerKg > 0 {
	//			priceText += fmt.Sprintf("每斤:¥%.2f", pricePerKg)
	//		}
	//		if pricePerItem > 0 {
	//			if priceText != "" {
	//				priceText += "\n"
	//			}
	//			priceText += fmt.Sprintf("每件:¥%.2f", pricePerItem)
	//		}
	//		if priceText == "" {
	//			priceText = "未设置"
	//		}
	//
	//		f.SetCellValue(sheetName, cell, priceText)
	//		f.SetCellStyle(sheetName, cell, cell, priceInfoStyle)
	//
	//		// 设置行高适应多行文本
	//		f.SetRowHeight(sheetName, priceRow, 30)
	//	}
	//	col += len(specs)
	//}
	//
	//// 第4行：设置金额标题
	//amountTitleRow := 4
	//col = 2 // 重置列
	//for _, product := range products {
	//	specs := sm.ProductMap[product].Specs
	//	for i := range specs {
	//		cell := execl.GetColumnLetter(col+i) + strconv.Itoa(amountTitleRow)
	//		f.SetCellValue(sheetName, cell, "金额(元)")
	//		f.SetCellStyle(sheetName, cell, cell, styles["specHeader"])
	//	}
	//	col += len(specs)
	//}
	//
	// 日期列合并4行（标题行）
	f.MergeCell(sheetName, "A1", "A2")
	f.SetCellValue(sheetName, "A1", "日期")
	f.SetCellStyle(sheetName, "A1", "A2", styles["dateHeader"])

	// 设置垂直居中样式
	verticalCenterStyle, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{
			Vertical:   "center",
			Horizontal: "center",
		},
	})
	f.SetCellStyle(sheetName, "A1", "A4", verticalCenterStyle)

	// 填充数据 - 从第5行开始
	dataStartRow := 3
	// 先填充数量数据
	for rowIdx, dateStr := range dates {
		row := rowIdx + dataStartRow

		// 解析日期
		date, _ := time.Parse("2006-01-02", dateStr)

		// 设置日期
		dateCell := fmt.Sprintf("A%d", row)
		f.SetCellValue(sheetName, dateCell, execl.FormatShortDate(date))
		f.SetCellStyle(sheetName, dateCell, dateCell, styles["dateData"])

		// 设置每个商品的数量
		for _, product := range products {
			startCol := productCols[product]
			specs := sm.ProductMap[product].Specs

			for i, spec := range specs {
				// 查找该日期下该商品该规格的数量
				qty := 0
				for _, order := range sm.DateMap[dateStr] {
					if order.ProductName == product && order.Spec == spec {
						qty += order.Quantity
					}
				}

				// 数量单元格
				qtyCell := execl.GetColumnLetter(startCol+i) + strconv.Itoa(row)
				if qty > 0 {
					f.SetCellValue(sheetName, qtyCell, qty)
				}
				f.SetCellStyle(sheetName, qtyCell, qtyCell, styles["data"])
			}
		}
	}

	// 添加数量合计行
	totalQtyRow := dataStartRow + len(dates)
	f.SetCellValue(sheetName, fmt.Sprintf("A%d", totalQtyRow), "总件数")
	f.SetCellStyle(sheetName, fmt.Sprintf("A%d", totalQtyRow), fmt.Sprintf("A%d", totalQtyRow), styles["total"])

	// 计算每个规格的总数量
	for _, product := range products {
		startCol := productCols[product]
		specs := sm.ProductMap[product].Specs

		for i, spec := range specs {
			total := 0
			for _, orders := range sm.DateMap {
				for _, order := range orders {
					if order.ProductName == product && order.Spec == spec {
						total += order.Quantity
					}
				}
			}

			cell := execl.GetColumnLetter(startCol+i) + strconv.Itoa(totalQtyRow)
			if total > 0 {
				f.SetCellValue(sheetName, cell, total)
			}
			f.SetCellStyle(sheetName, cell, cell, styles["total"])
		}
	}

	// 添加价格行（每斤/元和每件/元）
	priceRow2 := totalQtyRow + 1
	f.SetCellValue(sheetName, fmt.Sprintf("A%d", priceRow2), "每斤/元")
	f.SetCellValue(sheetName, fmt.Sprintf("A%d", priceRow2+1), "每件/元")
	f.SetCellStyle(sheetName, fmt.Sprintf("A%d", priceRow2), fmt.Sprintf("A%d", priceRow2+1), styles["specHeader"])

	// 填充价格信息
	col = 2
	for _, product := range products {
		specs := sm.ProductMap[product].Specs
		productInfo := sm.ProductMap[product]

		for i, spec := range specs {
			// 每斤价格
			pricePerKgCell := execl.GetColumnLetter(col+i) + strconv.Itoa(priceRow2)
			if productInfo.PricePerKgMap[spec] > 0 {
				f.SetCellValue(sheetName, pricePerKgCell, productInfo.PricePerKgMap[spec])
				// 设置数字格式
				priceStyle, _ := f.NewStyle(&excelize.Style{
					NumFmt: 4,
					Alignment: &excelize.Alignment{
						Horizontal: "center",
					},
				})
				f.SetCellStyle(sheetName, pricePerKgCell, pricePerKgCell, priceStyle)
			}

			// 每件价格（下一行）
			pricePerItemCell := execl.GetColumnLetter(col+i) + strconv.Itoa(priceRow2+1)
			var pricePerItem float64
			if productInfo.PricePerItemMap[spec] > 0 {
				pricePerItem = productInfo.PricePerItemMap[spec]
				f.SetCellValue(sheetName, pricePerItemCell, pricePerItem)
			} else if productInfo.PricePerKgMap[spec] > 0 {
				// 根据每斤价格计算每件价格
				jinCount := execl.ExtractJinCount(spec)
				pricePerItem = jinCount * productInfo.PricePerKgMap[spec]
				f.SetCellValue(sheetName, pricePerItemCell, pricePerItem)
			}

			if pricePerItem > 0 {
				priceStyle, _ := f.NewStyle(&excelize.Style{
					NumFmt: 4,
					Alignment: &excelize.Alignment{
						Horizontal: "center",
					},
				})
				f.SetCellStyle(sheetName, pricePerItemCell, pricePerItemCell, priceStyle)
			}
		}
		col += len(specs)
	}

	// 添加金额行
	amountRow := priceRow2 + 2
	f.SetCellValue(sheetName, fmt.Sprintf("A%d", amountRow), "金额(元)")
	f.SetCellStyle(sheetName, fmt.Sprintf("A%d", amountRow), fmt.Sprintf("A%d", amountRow), styles["total"])

	// 计算每个规格的金额
	totalAmount := 0.0
	for _, product := range products {
		startCol := productCols[product]
		specs := sm.ProductMap[product].Specs
		productInfo := sm.ProductMap[product]

		for i, spec := range specs {
			// 计算该规格的总数量
			totalQty := 0
			for _, orders := range sm.DateMap {
				for _, order := range orders {
					if order.ProductName == product && order.Spec == spec {
						totalQty += order.Quantity
					}
				}
			}

			if totalQty > 0 {
				// 计算每件价格
				var pricePerItem float64
				if productInfo.PricePerItemMap[spec] > 0 {
					pricePerItem = productInfo.PricePerItemMap[spec]
				} else if productInfo.PricePerKgMap[spec] > 0 {
					jinCount := execl.ExtractJinCount(spec)
					pricePerItem = jinCount * productInfo.PricePerKgMap[spec]
				}

				if pricePerItem > 0 {
					amount := float64(totalQty) * pricePerItem
					totalAmount += amount

					cell := execl.GetColumnLetter(startCol+i) + strconv.Itoa(amountRow)
					f.SetCellValue(sheetName, cell, amount)

					// 设置金额格式为两位小数
					amountStyle, _ := f.NewStyle(&excelize.Style{
						NumFmt: 4, // 4 是会计格式，显示两位小数
						Alignment: &excelize.Alignment{
							Horizontal: "right",
						},
					})
					f.SetCellStyle(sheetName, cell, cell, amountStyle)
				}
			}
		}
	}

	// 添加总金额行
	totalAmountRow := amountRow + 1
	f.SetCellValue(sheetName, fmt.Sprintf("A%d", totalAmountRow), "总金额")
	f.SetCellStyle(sheetName, fmt.Sprintf("A%d", totalAmountRow), fmt.Sprintf("A%d", totalAmountRow), styles["total"])

	// 在总金额位置填写汇总金额
	lastCol := execl.GetColumnLetter(col - 1)
	f.SetCellValue(sheetName, fmt.Sprintf("%s%d", lastCol, totalAmountRow), totalAmount)

	// 设置总金额格式
	totalAmountStyle, _ := f.NewStyle(&excelize.Style{
		NumFmt: 4,
		Font: &excelize.Font{
			Bold: true,
			Size: 12,
		},
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#E2EFDA"},
			Pattern: 1,
		},
		Alignment: &excelize.Alignment{
			Horizontal: "right",
		},
	})
	f.SetCellStyle(sheetName, fmt.Sprintf("%s%d", lastCol, totalAmountRow), fmt.Sprintf("%s%d", lastCol, totalAmountRow), totalAmountStyle)

	// 设置列宽
	f.SetColWidth(sheetName, "A", "A", 12) // 日期列
	for _, product := range products {
		for i := productCols[product]; i <= productEndCols[product]; i++ {
			colLetter := execl.GetColumnLetter(i)
			f.SetColWidth(sheetName, colLetter, colLetter, 12)
		}
	}

	// 添加边框
	f.SetCellStyle(sheetName, "A1", lastCol+strconv.Itoa(totalAmountRow), styles["border"])

	// ===== 新增：每日订单明细工作表 =====
	sm.GenerateDailyDetail(f)

	return f.SaveAs(filename)
}

// getPrice 获取某商品某规格的单价（斤/元、件/元）
func (sm *SpecManager) getPrice(productName, specKey string) (pricePerKg, pricePerItem float64) {
	p, ok := sm.ProductMap[productName]
	if !ok {
		return 0, 0
	}
	pricePerKg = p.PricePerKgMap[specKey]
	pricePerItem = p.PricePerItemMap[specKey]
	return
}

// GenerateDailyDetail 在 Excel 中新增「每日订单明细」工作表
// 为所选日期区间的每一天生成订单数据表，列：收件人/收货地址/商品规格/备注/件/元/斤/元/金额
// 每天表尾统计当日总件数与总金额，最后统计全部合计
// GenerateDailyDetail 为所选日期区间的每一天生成一个独立的工作表（按日期升序）
// 每个工作表以日期命名（如「9月1日」），内含：表头 + 当日订单明细 + 当日合计（总件数 / 总金额）
func (sm *SpecManager) GenerateDailyDetail(f *excelize.File) {
	// 明细表列
	headers := []string{"收件人", "收货地址", "商品规格", "备注", "件/元", "斤/元", "金额"}
	colCount := len(headers) // 7 列：A~G
	lastColLetter := execl.GetColumnLetter(colCount)

	// ===== 样式 =====
	titleStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 13, Color: "#FFFFFF"},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#4472C4"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "#000000", Style: 1},
			{Type: "right", Color: "#000000", Style: 1},
			{Type: "top", Color: "#000000", Style: 1},
			{Type: "bottom", Color: "#000000", Style: 1},
		},
	})
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#D9E1F2"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
		Border: []excelize.Border{
			{Type: "left", Color: "#000000", Style: 1},
			{Type: "right", Color: "#000000", Style: 1},
			{Type: "top", Color: "#000000", Style: 1},
			{Type: "bottom", Color: "#000000", Style: 1},
		},
	})
	dataStyle, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
		Border: []excelize.Border{
			{Type: "left", Color: "#CCCCCC", Style: 1},
			{Type: "right", Color: "#CCCCCC", Style: 1},
			{Type: "top", Color: "#CCCCCC", Style: 1},
			{Type: "bottom", Color: "#CCCCCC", Style: 1},
		},
	})
	moneyStyle, _ := f.NewStyle(&excelize.Style{
		NumFmt: 4,
		Alignment: &excelize.Alignment{Horizontal: "right", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "#CCCCCC", Style: 1},
			{Type: "right", Color: "#CCCCCC", Style: 1},
			{Type: "top", Color: "#CCCCCC", Style: 1},
			{Type: "bottom", Color: "#CCCCCC", Style: 1},
		},
	})
	totalStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#E2EFDA"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "#000000", Style: 2},
			{Type: "right", Color: "#000000", Style: 2},
			{Type: "top", Color: "#000000", Style: 2},
			{Type: "bottom", Color: "#000000", Style: 2},
		},
	})
	totalMoneyStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		NumFmt: 4,
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#E2EFDA"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "right", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "#000000", Style: 2},
			{Type: "right", Color: "#000000", Style: 2},
			{Type: "top", Color: "#000000", Style: 2},
			{Type: "bottom", Color: "#000000", Style: 2},
		},
	})

	// 按日期分组（使用结账时缓存的订单列表）
	dateOrders := make(map[string][]models.AddressInfo)
	for _, info := range addresinfolist {
		d := info.CreatedAt.Format("2006-01-02")
		dateOrders[d] = append(dateOrders[d], info)
	}
	dates := make([]string, 0, len(dateOrders))
	for d := range dateOrders {
		dates = append(dates, d)
	}
	sort.Strings(dates) // "2006-01-02" 字典序即日期升序

	// 每个日期一张独立工作表（按日期升序）
	for _, date := range dates {
		orders := dateOrders[date]
		t, _ := time.Parse("2006-01-02", date)
		dateLabel := fmt.Sprintf("%d月%d日", t.Month(), t.Day())
		sheetName := dateLabel // 工作表以日期命名

		if _, err := f.NewSheet(sheetName); err != nil {
			// 名称非法或冲突时回退为安全名称
			sheetName = "D" + date
			if _, err2 := f.NewSheet(sheetName); err2 != nil {
				continue
			}
		}

		row := 1

		// 标题行（合并整行）
		titleStart := "A" + strconv.Itoa(row)
		titleEnd := lastColLetter + strconv.Itoa(row)
		f.MergeCell(sheetName, titleStart, titleEnd)
		f.SetCellValue(sheetName, titleStart, dateLabel+" 订单明细")
		f.SetCellStyle(sheetName, titleStart, titleEnd, titleStyle)
		f.SetRowHeight(sheetName, row, 22)
		row++

		// 表头行
		for i, h := range headers {
			c := execl.GetColumnLetter(i+1) + strconv.Itoa(row)
			f.SetCellValue(sheetName, c, h)
			f.SetCellStyle(sheetName, c, c, headerStyle)
		}
		row++

		// 数据行
		dayQty := 0
		dayAmount := 0.0
		for _, info := range orders {
			if len(info.ProductSpecs) == 0 {
				continue
			}
			startDataRow := row
			for _, spec := range info.ProductSpecs {
				specKey := fmt.Sprintf("%d斤", spec.Weight)
				pKg, pItem := sm.getPrice(spec.Name, specKey)
				if pItem <= 0 && pKg > 0 {
					pItem = pKg * float64(spec.Weight) // 件/元 = 斤/元 × 斤
				}
				amount := pItem // 每个规格条目计 1 件
				dayQty++
				dayAmount += amount

				r := strconv.Itoa(row)
				f.SetCellValue(sheetName, "A"+r, info.Recipient)
				f.SetCellValue(sheetName, "B"+r, info.Address)
				f.SetCellValue(sheetName, "C"+r, spec.Name+strconv.Itoa(spec.Weight)+"斤")
				f.SetCellValue(sheetName, "D"+r, info.Remarks)
				if pItem > 0 {
					f.SetCellValue(sheetName, "E"+r, pItem)
					f.SetCellStyle(sheetName, "E"+r, "E"+r, moneyStyle)
				}
				if pKg > 0 {
					f.SetCellValue(sheetName, "F"+r, pKg)
					f.SetCellStyle(sheetName, "F"+r, "F"+r, moneyStyle)
				}
				if pItem > 0 {
					f.SetCellValue(sheetName, "G"+r, amount)
					f.SetCellStyle(sheetName, "G"+r, "G"+r, moneyStyle)
				}
				// 基础数据样式（含边框与自动换行）
				f.SetCellStyle(sheetName, "A"+r, "D"+r, dataStyle)
				row++
			}
			// 同一订单多规格时，纵向合并 收件人/收货地址/备注
			if row-1 > startDataRow {
				for _, col := range []string{"A", "B", "D"} {
					s := col + strconv.Itoa(startDataRow)
					e := col + strconv.Itoa(row-1)
					f.MergeCell(sheetName, s, e)
				}
			}
		}

		// 当日合计行：合并 A~F 显示件数，G 显示总金额
		totalStart := "A" + strconv.Itoa(row)
		totalEnd := execl.GetColumnLetter(colCount-1) + strconv.Itoa(row) // F 列
		f.MergeCell(sheetName, totalStart, totalEnd)
		f.SetCellValue(sheetName, totalStart, fmt.Sprintf("当日合计：共 %d 件", dayQty))
		f.SetCellStyle(sheetName, totalStart, totalEnd, totalStyle)
		f.SetCellValue(sheetName, "G"+strconv.Itoa(row), dayAmount)
		f.SetCellStyle(sheetName, "G"+strconv.Itoa(row), "G"+strconv.Itoa(row), totalMoneyStyle)
		row++

		// 列宽
		f.SetColWidth(sheetName, "A", "A", 14) // 收件人
		f.SetColWidth(sheetName, "B", "B", 38) // 收货地址
		f.SetColWidth(sheetName, "C", "C", 16) // 商品规格
		f.SetColWidth(sheetName, "D", "D", 22) // 备注
		f.SetColWidth(sheetName, "E", "G", 12) // 件/元 斤/元 金额
	}

	// 确保打开文件时默认停留在汇总表
	if idx, err := f.GetSheetIndex("商品销售统计"); err == nil {
		f.SetActiveSheet(idx)
	}
}

// createStyles 创建Excel样式
func (sm *SpecManager) createStyles(f *excelize.File) map[string]int {
	styles := make(map[string]int)

	// 商品表头样式
	productHeader, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{
			Bold:  true,
			Size:  12,
			Color: "#FFFFFF",
		},
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#4472C4"},
			Pattern: 1,
		},
		Alignment: &excelize.Alignment{
			Horizontal: "center",
			Vertical:   "center",
		},
		Border: []excelize.Border{
			{Type: "top", Color: "#000000", Style: 1},
			{Type: "bottom", Color: "#000000", Style: 1},
			{Type: "left", Color: "#000000", Style: 1},
			{Type: "right", Color: "#000000", Style: 1},
		},
	})
	styles["productHeader"] = productHeader

	// 规格表头样式
	specHeader, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{
			Bold: true,
		},
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#D9E1F2"},
			Pattern: 1,
		},
		Alignment: &excelize.Alignment{
			Horizontal: "center",
		},
		Border: []excelize.Border{
			{Type: "top", Color: "#000000", Style: 1},
			{Type: "bottom", Color: "#000000", Style: 1},
			{Type: "left", Color: "#000000", Style: 1},
			{Type: "right", Color: "#000000", Style: 1},
		},
	})
	styles["specHeader"] = specHeader

	// 日期表头样式
	dateHeader, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{
			Bold: true,
		},
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#FFC000"},
			Pattern: 1,
		},
		Alignment: &excelize.Alignment{
			Horizontal: "center",
		},
		Border: []excelize.Border{
			{Type: "top", Color: "#000000", Style: 1},
			{Type: "bottom", Color: "#000000", Style: 1},
			{Type: "left", Color: "#000000", Style: 1},
			{Type: "right", Color: "#000000", Style: 1},
		},
	})
	styles["dateHeader"] = dateHeader

	// 日期数据样式
	dateData, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#FFF2CC"},
			Pattern: 1,
		},
		Alignment: &excelize.Alignment{
			Horizontal: "center",
		},
	})
	styles["dateData"] = dateData

	// 数据样式
	data, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{
			Horizontal: "center",
		},
		Border: []excelize.Border{
			{Type: "left", Color: "#CCCCCC", Style: 1},
			{Type: "right", Color: "#CCCCCC", Style: 1},
		},
	})
	styles["data"] = data

	// 空单元格样式
	empty, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#F5F5F5"},
			Pattern: 1,
		},
	})
	styles["empty"] = empty

	// 合计行样式
	total, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{
			Bold: true,
		},
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#E2EFDA"},
			Pattern: 1,
		},
		Alignment: &excelize.Alignment{
			Horizontal: "center",
		},
		Border: []excelize.Border{
			{Type: "top", Color: "#000000", Style: 2},
			{Type: "bottom", Color: "#000000", Style: 2},
		},
	})
	styles["total"] = total

	// 边框样式
	border, _ := f.NewStyle(&excelize.Style{
		Border: []excelize.Border{
			{Type: "left", Color: "#000000", Style: 1},
			{Type: "right", Color: "#000000", Style: 1},
			{Type: "top", Color: "#000000", Style: 1},
			{Type: "bottom", Color: "#000000", Style: 1},
		},
	})
	styles["border"] = border

	return styles
}

// PrintSummary 打印统计摘要
func (sm *SpecManager) PrintSummary() {
	fmt.Println("=== 商品销售统计 ===")

	// 按日期统计
	fmt.Println("\n按日期统计：")
	dates := make([]string, 0, len(sm.DateMap))
	for date := range sm.DateMap {
		dates = append(dates, date)
	}
	sort.Strings(dates)

	for _, dateStr := range dates {
		orders := sm.DateMap[dateStr]
		totalQty := 0
		productStats := make(map[string]int)

		for _, order := range orders {
			totalQty += order.Quantity
			productStats[order.ProductName] += order.Quantity
		}

		fmt.Printf("  %s: 总销量 %d\n", dateStr, totalQty)
		for product, qty := range productStats {
			fmt.Printf("    %s: %d\n", product, qty)
		}
	}

	// 按商品统计
	fmt.Println("\n按商品统计：")
	for _, product := range sm.ProductMap {
		fmt.Printf("  %s:\n", product.Name)
		fmt.Printf("    总销量: %d\n", product.TotalQty)
		fmt.Println("    规格分布:")
		for _, spec := range product.Specs {
			fmt.Printf("      %s: %d\n", spec, product.SpecMap[spec])
		}
	}
}
