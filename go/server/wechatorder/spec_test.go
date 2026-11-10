package wechatorder

import (
	"testing"

	"order_server/go/models"
	ordertutil "order_server/go/utils/order"
)

// 回归用例：一个【】里用 + 连接的组合规格，必须拆成多个独立商品项，
// 括号外的「100-130克」果径说明不能变成重量或新商品。
// 曾因 Specprocessing + FixSpecNames 把正确的 {广香,5}/{黄柠檬,3}
// 改成「【广香5斤+黄柠檬5斤】,【广香5斤+黄柠檬3斤】」，属于发错货级别的严重错误。
const comboRaw = "11-8\n收件人：王13500005555\n" +
	"华夏省大兵市蚌山区宏业路示例CAFE（红树林网吧旁边）\n" +
	"【广香5斤+黄柠檬3斤】100-130克"

func TestSpecprocessingParsesCombination(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want models.ProductSpecsJSON
	}{
		{"单个规格", "【香水柠檬5斤】", models.ProductSpecsJSON{
			{Name: "香水柠檬", Weight: 5}}},
		{"多个括号", "【香水柠檬5斤】【黄柠檬10斤】", models.ProductSpecsJSON{
			{Name: "香水柠檬", Weight: 5}, {Name: "黄柠檬", Weight: 10}}},
		{"空格分隔品名与重量", "【黄柠檬 8斤】~~", models.ProductSpecsJSON{
			{Name: "黄柠檬", Weight: 8}}},
		{"顿号组合", "【广香5斤、黄柠檬3斤】", models.ProductSpecsJSON{
			{Name: "广香", Weight: 5}, {Name: "黄柠檬", Weight: 3}}},
		{"加号组合+果径备注", comboRaw, models.ProductSpecsJSON{
			{Name: "广香", Weight: 5}, {Name: "黄柠檬", Weight: 3}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ordertutil.Specprocessing(c.in)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if !sameSpecs(got, c.want) {
				t.Fatalf("解析结果不对：got=%+v want=%+v", got, c.want)
			}
		})
	}
}

func TestSpecprocessingIgnoresGroupGameTags(t *testing.T) {
	if _, err := ordertutil.Specprocessing("【答案】【发音】群里接龙"); err == nil {
		t.Fatal("群游戏标签不应被解析成商品规格")
	}
}

func TestFixSpecNamesKeepsCorrectSpecs(t *testing.T) {
	orders := []models.AddressInfo{{
		ProductSpecs: models.ProductSpecsJSON{
			{Name: "广香", Weight: 5}, {Name: "黄柠檬", Weight: 3}},
	}}
	if FixSpecNames(comboRaw, orders) {
		t.Fatal("AI 输出本来就正确，不应报告被校正")
	}
	if got := orders[0].ProductSpecsString(false); got != "【广香5斤】,【黄柠檬3斤】" {
		t.Fatalf("前端展示不对: %s", got)
	}
}

func TestFixSpecNamesRepairsHallucinatedSpecs(t *testing.T) {
	orders := []models.AddressInfo{{
		ProductSpecs: models.ProductSpecsJSON{
			{Name: "广香5斤+黄柠檬", Weight: 5},
			{Name: "广香5斤+黄柠檬", Weight: 3},
			{Name: "黄柠檬", Weight: 5}, // 凭空多出来的一条
		},
	}}
	if !FixSpecNames(comboRaw, orders) {
		t.Fatal("应检测到规格与原文不一致并校正")
	}
	want := models.ProductSpecsJSON{{Name: "广香", Weight: 5}, {Name: "黄柠檬", Weight: 3}}
	if !sameSpecs(orders[0].ProductSpecs, want) {
		t.Fatalf("校正结果不对：got=%+v want=%+v", orders[0].ProductSpecs, want)
	}
	if got := orders[0].ProductSpecsString(false); got != "【广香5斤】,【黄柠檬3斤】" {
		t.Fatalf("校正后前端展示不对: %s", got)
	}
}
