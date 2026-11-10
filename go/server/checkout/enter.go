package checkout

import (
	"context"
	"fmt"
	"order_server/go/core"
	"order_server/go/global"
	"order_server/go/models"
	ordertutil "order_server/go/utils/order"
	time2 "order_server/go/utils/time"
	"os"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type Spec struct {
	Name         string  `json:"name"`
	PricePerKg   float64 `json:"pricePerKg"`   // 每斤/元（用户输入）
	PricePerItem float64 `json:"pricePerItem"` // 每件/元（用户输入，可选）
	Total        int     `json:"total"`        // 总件数
}
type ProSPecs struct {
	SID   uint
	Name  string `json:"Name"`
	Specs []Spec
}

type CheckoutResponse struct {
	FieldList       []string            // 未分类字段
	PBMap           map[string][]string // 商品归属map
	Count           uint                // 查询的条数
	ProSPecs        []ProSPecs          // 商品规格及分类.及表头
	TableData       []string            // 表格数据
	TotalRow        []int               // 总件数行
	PricePerKgRow   []float64           // 每件/元 行（用户输入）
	PricePerItemRow []float64           // 每件/元 行
	TotalAmountRow  []float64           // 总金额行
}

type ProPriceRequest struct {
	ProductName  string  `json:"productName"`  // 商品: 香水柠檬
	Spec         string  `json:"spec"`         // 规格 : 5斤
	Total        int     `json:"total"`        // 总件数
	PricePerKg   float64 `json:"pricePerKg"`   //每斤/元
	PricePerItem float64 `json:"pricePerItem"` // 每件/元
	TotalAmount  float64 `json:"totalAmount"`  // 总金额
}

type CheackDay struct {
	Date  time.Time
	specs []ProSPecs
}

var checkoutresp = CheckoutResponse{}           // 响应副本
var gspecsMap = make(map[string]map[string]int) // map[商品规格名称]map[商品规格]总件数
var proPrice = []ProPriceRequest{}
var addresinfolist = []models.AddressInfo{}
var begtime2, endtime2 string

// 添加结账数据
func AddcheckoutData(sm *SpecManager) {
	fmt.Println("====================")
	// 处理数据,获取总件数
	fmt.Println("处理数据:", addresinfolist)
	fmt.Println("====================")
	var orderlist []models.Order

	// 按日期分组统计销售数据
	dateProductSpecCount := make(map[string]map[string]map[string]int)
	for _, info := range addresinfolist {
		date := info.CreatedAt.Format("2006-01-02")
		if _, ok := dateProductSpecCount[date]; !ok {
			dateProductSpecCount[date] = make(map[string]map[string]int)
		}

		for _, spec := range info.ProductSpecs {
			productName := spec.Name
			specName := fmt.Sprintf("%d斤", spec.Weight)

			if _, ok := dateProductSpecCount[date][productName]; !ok {
				dateProductSpecCount[date][productName] = make(map[string]int)
			}
			dateProductSpecCount[date][productName][specName]++
		}
	}

	// 生成按日期分组的订单列表
	for date, productSpecMap := range dateProductSpecCount {
		fmt.Printf("\n日期: %s\n", date)
		fmt.Println("商品销售数据:")

		for productName, specMap := range productSpecMap {
			for specName, count := range specMap {
				// 查找对应的价格数据
				var pricePerKg, pricePerItem float64
				for _, pr := range proPrice {
					if pr.ProductName == productName && pr.Spec == specName {
						pricePerKg = pr.PricePerKg
						pricePerItem = pr.PricePerItem
						break
					}
				}

				// 计算金额
				totalAmount := float64(count) * pricePerItem

				// 打印销售数据
				fmt.Printf("  - 商品: %s, 规格: %s, 数量: %d, 单价: %.2f元/件, 总金额: %.2f元\n",
					productName, specName, count, pricePerItem, totalAmount)

				// 创建订单并添加到列表
				dateTime, _ := time.Parse("2006-01-02", date)
				order := models.Order{
					Date:         dateTime,
					ProductName:  productName,
					Spec:         specName,
					Quantity:     count,
					PricePerKg:   pricePerKg,
					PricePerItem: pricePerItem,
				}
				orderlist = append(orderlist, order)
				fmt.Println("orderlist", orderlist)
				// 添加到规格管理器
				sm.AddOrder(order)
			}
		}
	}

	// 打印总订单列表
	fmt.Println("\n====================")
	fmt.Println("生成的总订单列表:")
	for _, order := range orderlist {
		fmt.Printf("日期: %s, 商品: %s, 规格: %s, 数量: %d, 单价: %.2f元/件, 总金额: %.2f元\n",
			order.Date.Format("2006-01-02"), order.ProductName, order.Spec, order.Quantity,
			order.PricePerItem, float64(order.Quantity)*order.PricePerItem)
	}
	fmt.Println("====================")
}

func Checkout(ctx context.Context) {
	fmt.Println("========== 商品销售统计Excel生成器 ==========")

	// 创建规格管理器
	sm := NewSpecManager()

	// 添加数据
	AddcheckoutData(sm)
	fmt.Println(sm.Orders)
	// 打印统计摘要
	sm.PrintSummary()

	// 生成Excel文件
	fmt.Println("\n========== 生成Excel文件 ==========")

	// 选择保存目录
	saveDir, err := selectSaveDirectory(ctx)
	if err != nil {
		fmt.Printf("❌ 选择保存目录失败: %v\n", err)
		return
	}

	fmt.Println("begtime:", begtime2)
	fmt.Println("endtime:", endtime2)
	// 生成带时间戳的文件名
	filename := fmt.Sprintf("%s_%s结账.xlsx", begtime2, endtime2)
	filePath := fmt.Sprintf("%s/%s", saveDir, filename)

	err = sm.GenerateExcel(filePath)
	if err != nil {
		fmt.Printf("❌ 生成Excel失败: %v\n", err)
	} else {
		fmt.Printf("✅ Excel生成成功！文件: %s\n", filePath)
		fmt.Printf("📁 文件保存在: %s\n", saveDir)
	}

	// 显示一些统计信息
	fmt.Println("\n========== 统计信息 ==========")
	fmt.Printf("总订单数: %d\n", len(sm.Orders))
	fmt.Printf("总商品种类: %d\n", len(sm.ProductMap))
	fmt.Printf("总销售天数: %d\n", len(sm.DateMap))

	// 显示所有商品
	fmt.Println("\n📦 商品列表:")
	for _, product := range sm.ProductMap {
		fmt.Printf("  - %s (共%d种规格, 总销量:%d)\n",
			product.Name, len(product.Specs), product.TotalQty)
	}

	// 显示所有日期
	fmt.Println("\n📅 销售日期:")
	dates := make([]string, 0, len(sm.DateMap))
	for date := range sm.DateMap {
		dates = append(dates, date)
	}
	for _, date := range dates {
		fmt.Printf("  - %s\n", date)
	}
}

// 获取指定月份数据
func GetMonthdata(timedate time.Time) {
	// 起始日期
	begindate := time.Date(timedate.Year(), timedate.Month(), 1, 0, 0, 0, 0, time.Local)
	// 截至日期
	enddate := time.Date(timedate.Year(), timedate.Month(), time2.GetDaysInMonth(timedate.Year(), timedate.Month()), 23, 59, 59, 0, time.Local)
	// 查询数据库获取该月数据
	var _list []models.AddressInfo
	global.Db.Debug().Model(&_list).Preload("ProductSpecs").Where("created_at >= ? AND created_at <= ?", begindate, enddate).Find(&_list)
	fmt.Println(_list)

	// 统计该月每天的订单数，以及漏统计的日期
	var monthdate = make(map[string]int, 0)
	for _, info := range _list {
		if _, ok := monthdate[info.CreatedAt.Format("2006-01-02")]; ok {
			monthdate[info.CreatedAt.Format("2006-01-02")]++
		} else {
			monthdate[info.CreatedAt.Format("2006-01-02")] = 1
		}
	}

	for k, v := range monthdate {
		fmt.Println(k, v)
	}

}

// 选择保存目录
func selectSaveDirectory(ctx context.Context) (string, error) {
	// 使用 Wails 运行时的文件夹选择对话框
	dir, err := runtime.OpenDirectoryDialog(ctx, runtime.OpenDialogOptions{
		Title:                "选择保存目录",
		DefaultDirectory:     ".",
		CanCreateDirectories: true,
	})
	if err != nil {
		return "", fmt.Errorf("打开文件夹对话框失败: %w", err)
	}

	// 检查用户是否取消选择
	if dir == "" {
		return "", fmt.Errorf("用户取消选择")
	}

	// 验证目录是否存在
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// 尝试创建目录
			err = os.MkdirAll(dir, 0755)
			if err != nil {
				return "", fmt.Errorf("创建目录失败: %w", err)
			}
		} else {
			return "", fmt.Errorf("检查目录失败: %w", err)
		}
	} else {
		// 验证是否为目录
		if !info.IsDir() {
			return "", fmt.Errorf("选择的路径不是目录")
		}
	}

	// 验证目录是否可写
	testFile := fmt.Sprintf("%s/.test_write.txt", dir)
	err = os.WriteFile(testFile, []byte("test"), 0644)
	if err != nil {
		return "", fmt.Errorf("目录不可写: %w", err)
	}
	// 清理测试文件
	os.Remove(testFile)

	return dir, nil
}

// 获取指定日期的数据
func GetDaydata(statime time.Time, endtime time.Time) CheckoutResponse {
	// 验证日期参数是否有效
	if statime.IsZero() || endtime.IsZero() {
		// 返回空响应
		return CheckoutResponse{
			FieldList: []string{},
			Count:     0,
			ProSPecs:  []ProSPecs{},
			TableData: []string{},
			PBMap:     global.GetProductBelongs(),
			TotalRow:  []int{},
		}
	}

	// 起始日期
	begindate := time.Date(statime.Year(), statime.Month(), statime.Day()+1, 0, 0, 0, 0, time.Local)
	begtime2 = fmt.Sprintf("%d年%d月%d日", begindate.Year(), int(begindate.Month()), begindate.Day())
	// 截至日期
	enddate := time.Date(endtime.Year(), endtime.Month(), endtime.Day(), 23, 59, 59, 0, time.Local)
	endtime2 = fmt.Sprintf("%d年%d月%d日", endtime.Year(), int(endtime.Month()), endtime.Day())

	// 查询数据库获取日期数据
	var _list []models.AddressInfo
	var count int64
	global.Db.Debug().Model(&_list).Count(&count).Where("created_at >= ? AND created_at <= ?", begindate, enddate).Find(&_list).Order("created_at")
	addresinfolist = _list
	// 检查是否存在新商品
	newSpecList := ordertutil.Specchecking(_list)

	// 对列表的商品规格进行统一的整理，使其只存在第一元素
	for i := range _list {
		for j := range _list[i].ProductSpecs {
			spec := &_list[i].ProductSpecs[j] // 获取商品规格的指针，确保修改能够生效
			for k, v := range global.GetProductBelongs() {
				if spec.Name == k { // 如果商品规格名称等于商品分类第一元素，则跳过
					continue
				}

				for _, vv := range v { // 遍历商品分类子字段
					if spec.Name == vv { // 如果商品分类子字段等于商品分类子字段，则将其赋值为第一元素
						spec.Name = k
						// 找到匹配后跳出内层循环，避免重复修改
						break
					}
				}
			}
		}
	}

	// 获取商品分类及规格，同时计算该分类的总件数 int 键为商品分类名称，值为商品规格 map[string]int   ==》 商品规格为键，总件数为值
	specsMap := make(map[string]map[string]int)
	for _, info := range _list {
		for _, spec := range info.ProductSpecs { // 得到这订单所有的商品规格
			wg := fmt.Sprintf("%d斤", spec.Weight)
			if _, ok := specsMap[spec.Name]; !ok {
				specsMap[spec.Name] = make(map[string]int)
			}
			specsMap[spec.Name][wg] += 1
		}
	}
	gspecsMap = specsMap

	// 解析规格中的数值部分
	parseSpecValue := func(spec string) int {
		re, err := regexp.Compile(`\d+`)
		if err != nil {
			return 0
		}
		numberStr := re.FindString(spec)
		number, err := strconv.Atoi(numberStr)
		if err != nil {
			return 0
		}
		return number
	}

	// 转换为切片格式
	ProSPecsMap := make(map[string][]string)
	for k, v := range specsMap {
		for spec := range v {
			ProSPecsMap[k] = append(ProSPecsMap[k], spec)
		}
		// 对规格进行降序排序
		sort.Slice(ProSPecsMap[k], func(i, j int) bool {
			return parseSpecValue(ProSPecsMap[k][i]) < parseSpecValue(ProSPecsMap[k][j])
		})
	}

	// Map转List
	var ProSPecsList []ProSPecs
	sid := uint(1)
	for k, v := range ProSPecsMap {
		var speclist = make([]Spec, 0)
		for _, vv := range v {
			// 这里可以从全局变量或数据库中获取价格信息
			// 暂时使用默认值
			speclist = append(speclist, Spec{
				Name:         vv,
				PricePerKg:   0, // 默认为0，需要用户填写
				PricePerItem: 0, // 默认为0，需要用户填写
			})
		}
		ppcs := ProSPecs{
			SID:   sid,
			Name:  k,
			Specs: speclist,
		}

		ProSPecsList = append(ProSPecsList, ppcs)
		sid++
	}

	// 获取每天的总商品规格
	// 按日期分组
	dateMap := make(map[string]map[string]map[string]int)
	for _, info := range _list {
		date := info.CreatedAt.Format("2006-01-02")
		if _, ok := dateMap[date]; !ok {
			dateMap[date] = make(map[string]map[string]int)
		}
		for _, spec := range info.ProductSpecs {
			productName := spec.Name
			specStr := fmt.Sprintf("%d斤", spec.Weight)
			if _, ok := dateMap[date][productName]; !ok {
				dateMap[date][productName] = make(map[string]int)
			}
			dateMap[date][productName][specStr]++
		}
	}

	// 获取表格数据
	var tableData []string
	var _totalRowlist = make(map[string][]int, 0)
	var Rowlen int

	// 按日期升序排列，确保返回的表格数据按日期升序
	dates := make([]string, 0, len(dateMap))
	for date := range dateMap {
		dates = append(dates, date)
	}
	sort.Strings(dates) // "2006-01-02" 格式的字符串按字典序即等于按日期升序

	// 生成表格数据（按日期升序遍历）
	for _, date := range dates {
		productMap := dateMap[date]
		// 转换日期格式为 "3月8日" 格式
		t, err := time.Parse("2006-01-02", date)
		if err != nil {
			continue
		}
		dateStr := fmt.Sprintf("%d月%d日", t.Month(), t.Day())

		// 构建行数据
		rowData := dateStr

		var rowlen int
		// 按照 ProSPecsList 的顺序和每个商品的规格顺序生成数据
		for _, product := range ProSPecsList {
			for _, spec := range product.Specs {
				if countVal, ok := productMap[product.Name][spec.Name]; ok {
					_totalRowlist[date] = append(_totalRowlist[date], countVal)
					rowData += fmt.Sprintf(",%d", countVal)
				} else {
					_totalRowlist[date] = append(_totalRowlist[date], 0)
					rowData += ",—"
				}
				rowlen++
			}
		}
		Rowlen = rowlen
		tableData = append(tableData, rowData)
	}

	// 获取总件数表格数据
	var totalRow = make([]int, Rowlen)
	for _, v := range _totalRowlist {
		for i, i2 := range v {
			totalRow[i] += i2
		}

	}

	// 构建响应
	resp := CheckoutResponse{
		FieldList: newSpecList,
		Count:     uint(len(_list)),
		PBMap:     global.GetProductBelongs(),
		ProSPecs:  ProSPecsList,
		TableData: tableData,
		TotalRow:  totalRow,
	}
	checkoutresp = resp
	return resp
}

// 设置商品归属
func SetFieldList(mapp map[string][]string) {
	if err := core.WriteProductBelongMap(mapp); err != nil {
		logrus.Errorf("保存商品归属到数据库失败: %v", err)
		return
	}
	global.SetProductBelongs(mapp)
}

// 设置金额
func SetPrice(_list []ProPriceRequest) CheckoutResponse {
	proPrice = _list
	fmt.Println("_list", _list)
	var pricePerkgRow, pricePerItemRow, totalAmountRow []float64
	// 获取该商品 ->规格的总件数
	for i := range _list { // 使用索引修改值
		for k, v := range gspecsMap {
			if _list[i].ProductName == k { // 定位商品
				for spec, total := range v {
					if spec == _list[i].Spec { // 定位规格
						_list[i].Total = total
						// 获取规格的斤数
						re, err := regexp.Compile(`\d+`)
						if err != nil {
							logrus.Errorf("设置金额获取规格的斤数error: %v", err)
						}
						number := re.FindString(spec)
						num, err := strconv.Atoi(number)
						if err != nil {
							logrus.Errorf("设置金额转换error: %v", err)
						}

						if _list[i].PricePerItem == 0.00 {
							_list[i].PricePerItem = _list[i].PricePerKg * float64(num) //每斤/元  * 斤 = 每件/元
						}

						// 计算总金额
						_list[i].TotalAmount = float64(_list[i].Total) * _list[i].PricePerItem
					}
				}
			}
		}
	}

	// 给副本响应数据赋值
	for i := range checkoutresp.ProSPecs { // 使用索引修改值
		for _, item := range _list { //
			if checkoutresp.ProSPecs[i].Name == item.ProductName { // 定位商品
				for j, spec := range checkoutresp.ProSPecs[i].Specs { // 遍历商品规格
					if spec.Name == item.Spec { // 定位规格
						checkoutresp.ProSPecs[i].Specs[j].PricePerKg = item.PricePerKg
						checkoutresp.ProSPecs[i].Specs[j].PricePerItem = item.PricePerItem

						pricePerkgRow = append(pricePerkgRow, item.PricePerKg)
						pricePerItemRow = append(pricePerItemRow, item.PricePerItem)
						totalAmountRow = append(totalAmountRow, item.TotalAmount)
					}

				}
			}
		}
	}
	fmt.Println("pricePerkgRow", pricePerkgRow)
	fmt.Println("pricePerItemRow", pricePerItemRow)
	fmt.Println("totalAmountRow", totalAmountRow)
	//设置返回值
	checkoutresp.PricePerKgRow = pricePerkgRow
	checkoutresp.PricePerItemRow = pricePerItemRow
	checkoutresp.TotalAmountRow = totalAmountRow

	fmt.Println("checkoutresp", checkoutresp)
	// 为execl添加数据
	return checkoutresp
}
