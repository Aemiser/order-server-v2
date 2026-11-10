package models

import "time"

// Order 订单结构
type Order struct {
	ID           uint
	Date         time.Time
	ProductName  string
	Spec         string
	Quantity     int
	PricePerKg   float64 // 每斤/元（用户输入）
	PricePerItem float64 // 每件/元（用户输入，可选）
}
