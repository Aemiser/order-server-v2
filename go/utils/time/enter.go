package time

import "time"

// 计算润年
func IsLeapYearSimple(year int) bool {
	return (year%4 == 0 && year%100 != 0) || (year%400 == 0)
}

// 输入年月，返回该年月的月份天数
func GetDaysInMonth(year int, month time.Month) int {
	if month < 1 || month > 12 {
		return 0
	}
	days := [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if IsLeapYearSimple(year) {
		days[1] = 29
	}
	return days[month-1]
}
