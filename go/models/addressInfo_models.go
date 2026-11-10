package models

import (
	"database/sql/driver"
	"encoding/json"
	"strconv"
)

// 商品规格项 - 只有 Name 和 Weight 两个字段
type ProductSpec struct {
	Name   string `json:"name"`   // 商品名称
	Weight int    `json:"weight"` // 商品重量（斤）
}

// Value 实现 driver.Valuer 接口，用于写入数据库
func (ps ProductSpec) Value() (driver.Value, error) {
	return json.Marshal(ps)
}

// Scan 实现 sql.Scanner 接口，用于从数据库读取
func (ps *ProductSpec) Scan(value interface{}) error {
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, ps)
}

// ProductSpecsJSON 自定义类型用于 GORM 的 JSON 字段
type ProductSpecsJSON []ProductSpec

// Value 实现 driver.Valuer 接口
func (p ProductSpecsJSON) Value() (driver.Value, error) {
	return json.Marshal(p)
}

// Scan 实现 sql.Scanner 接口
func (p *ProductSpecsJSON) Scan(value interface{}) error {
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, p)
}

// AddressInfo 地址信息结构体
type AddressInfo struct {
	Model
	PreviewID    uint             `gorm:"-" json:"VID"`
	Recipient    string           `gorm:"recipient" json:"收件人"`
	Phone        string           `gorm:"phone" json:"手机号"`
	Address      string           `gorm:"address" json:"收货地址"`
	ProductSpecs ProductSpecsJSON `gorm:"productSpecs;type:json" json:"商品规格"`
	Remarks      string           `gorm:"remarks" json:"备注"`
	Isstorage    bool             `gorm:"-" json:"Isstorage"`
}

// 获取商品规格字符串
func (address AddressInfo) ProductSpecsString(remarks bool) string {
	var specsString string
	for i, spec := range address.ProductSpecs {
		specsString += "【"
		if i == len(address.ProductSpecs)-1 {
			specsString += spec.Name + strconv.Itoa(spec.Weight) + "斤】"
			if remarks {
				specsString += address.Remarks
			}
			break
		}
		specsString += spec.Name + strconv.Itoa(spec.Weight) + "斤】,"
	}
	return specsString
}
