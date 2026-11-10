package ordertutil

import (
	"encoding/json"
	"fmt"
	"order_server/go/global"
	"order_server/go/models"
	"regexp"
	"strconv"
	"strings"
)

// 商品规格解析：只认【】括号里的「品名 + 数字 + 重量单位」，
// 括号外的备注（如「100-130克」果径说明）不参与解析。
// 一个【】里允许用 + / 顿号 / 逗号 / & / 斜杠 写多个商品项，
// 例如「【广香5斤+黄柠檬3斤】」必须拆成 {广香,5} 与 {黄柠檬,3} 两项。
//
// 旧实现的正则 `【([^】]+?)(\d+)(?:斤)?】` 会把上面这种组合规格解析成
// {name:"广香5斤+黄柠檬", weight:3}，再被 FixSpecNames 刷到 AI 输出的所有规格上，
// 造成「【广香5斤+黄柠檬5斤】,【广香5斤+黄柠檬3斤】」这种凭空多出来的规格。
var (
	reSpecBlock = regexp.MustCompile(`【([^】]+)】`)
	reSpecSep   = regexp.MustCompile(`[+＋、,，&/]`)
	reSpecPair  = regexp.MustCompile(`([^\d\s+＋、,，&/【】]+)\s*(\d+)\s*(?:斤|公斤|kg|KG|千克)`)
	reSpecBare  = regexp.MustCompile(`^\s*([^\d\s+＋、,，&/【】]+)\s*(\d+)\s*$`)
)

func Specprocessing(input string) (models.ProductSpecsJSON, error) {
	items := make(models.ProductSpecsJSON, 0)
	for _, block := range reSpecBlock.FindAllStringSubmatch(input, -1) {
		for _, seg := range reSpecSep.Split(strings.TrimSpace(block[1]), -1) {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			matched := false
			for _, m := range reSpecPair.FindAllStringSubmatch(seg, -1) {
				n, err := strconv.Atoi(m[2])
				if err != nil || n <= 0 {
					continue
				}
				items = append(items, models.ProductSpec{Name: strings.TrimSpace(m[1]), Weight: n})
				matched = true
			}
			// 兼容不带单位的「【广香5】」：只在整段就是「纯品名 + 数字」时接受，
			// 避免把「100-130克」这类果径备注误当成商品重量。
			if !matched {
				if m := reSpecBare.FindStringSubmatch(seg); m != nil {
					if n, err := strconv.Atoi(m[2]); err == nil && n > 0 {
						items = append(items, models.ProductSpec{Name: strings.TrimSpace(m[1]), Weight: n})
					}
				}
			}
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("没有找到匹配的内容")
	}
	return items, nil
}

func SetVid(infolist []models.AddressInfo) []models.AddressInfo {
	length := len(global.AddressList)
	for i := range infolist {
		addr := &infolist[i] // 获取元素的指针
		addr.PreviewID = uint(length + i + 1)
	}
	return infolist
}

// 检查是否存在新字段
func Specchecking(list []models.AddressInfo) (newSpecList []string) {
	newSpecMap := make(map[string]int, 0)
	SpecMap := make(map[string]int, 0)
	// 把map拉长为一维
	for k, v := range global.GetProductBelongs() {
		SpecMap[k] = 1
		for _, vv := range v {
			SpecMap[vv] = 1
		}
	}

	for _, info := range list {
		for _, spec := range info.ProductSpecs {
			if _, ok := SpecMap[spec.Name]; !ok {
				newSpecMap[spec.Name] = 1
			}
		}
	}
	// map转一维切片
	for k, _ := range newSpecMap {
		newSpecList = append(newSpecList, k)
	}
	fmt.Println("检查到新商品：", newSpecList)
	return
}

func GetNumberInSpec(s string) int {
	// 创建一个正则表达式模式
	re := regexp.MustCompile(`\d+`)

	// 使用正则表达式模式匹配字符串
	matches := re.FindString(s)

	// 检查匹配结果
	if len(matches) > 0 {
		// 获取匹配到的第一个数字
		num, err := strconv.Atoi(matches)
		if err != nil {
			return 0
		}
		return num
	}
	return 0
}

func RespBodyToJson(content string) ([]models.AddressInfo, error) {

	// 清理可能存在的Markdown代码块标记
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	// 解析JSON数组
	var addressList []models.AddressInfo
	if err := json.Unmarshal([]byte(content), &addressList); err != nil {
		// 尝试解析为单个对象
		var singleAddr models.AddressInfo
		if err2 := json.Unmarshal([]byte(content), &singleAddr); err2 == nil {
			addressList = []models.AddressInfo{singleAddr}
		} else {
			return nil, fmt.Errorf("地址信息解析失败: %w\n原始返回: %s", err, content)
		}
	}
	return addressList, nil
}
