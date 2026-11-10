package models

type PredictAmount struct {
	ID           uint    `json:"ID"`
	DateGap      string  `json:"dateGap"`
	ProductName  string  `json:"productName"`
	PricePerKg   float64 `json:"pricePerKg"`   // 每斤/元（用户输入）
	PricePerItem float64 `json:"pricePerItem"` // 每件/元（用户输入，可选）
}
