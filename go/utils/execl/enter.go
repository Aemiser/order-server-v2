package execl

import (
	"reflect"
	"regexp"
	"strconv"
	"time"
)

// getColumnLetter 将列索引转换为字母（1->A, 2->B, ..., 27->AA）
func GetColumnLetter(col int) string {
	letter := ""
	for col > 0 {
		col--
		letter = string(rune('A'+(col%26))) + letter
		col = col / 26
	}
	return letter
}

// extractNumber 从字符串中提取数字
func ExtractNumber(s string) int {
	// 使用正则表达式匹配数字
	re := regexp.MustCompile(`\d+`)
	numStr := re.FindString(s)

	// 如果没有找到数字，返回0
	if numStr == "" {
		return 0
	}

	// 转换为整数
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return 0
	}

	return num
}

// isZeroValue 检查值是否为零值
func IsZeroValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return v.String() == ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return v.IsNil()
	case reflect.Struct:
		// 对于结构体，检查是否是零值（比如time.Time）
		return v.IsZero()
	default:
		return false
	}
}

// joinStrings 连接字符串切片
func JoinStrings(strs []string, separator string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += separator + strs[i]
	}
	return result
}

// formatDate 格式化日期
func FormatDate(t time.Time) string {
	return t.Format("2006-01-02")
}

// formatShortDate 格式化短日期（MM-DD）
func FormatShortDate(t time.Time) string {
	return t.Format("01-02")
}

// 从规格字符串中提取斤数
func ExtractJinCount(spec string) float64 {
	re := regexp.MustCompile(`\d+`)
	numStr := re.FindString(spec)
	num, _ := strconv.ParseFloat(numStr, 64)
	return num
}

// 计算每件价格
func CalculatePricePerItem(spec string, pricePerKg float64, pricePerItem float64) float64 {
	// 如果直接提供了每件价格，优先使用
	if pricePerItem > 0 {
		return pricePerItem
	}

	// 如果提供了每斤价格，计算每件价格 = 斤数 * 每斤价格
	if pricePerKg > 0 {
		jinCount := ExtractJinCount(spec)
		return jinCount * pricePerKg
	}

	return 0
}

// 计算金额
func CalculateAmount(quantity int, pricePerItem float64) float64 {
	return float64(quantity) * pricePerItem
}
