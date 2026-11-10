package dashboard

import (
	"testing"
	"time"

	"order_server/go/global"
	"order_server/go/models"
)

// withBelongMap 替换商品归属内存映射，并在用例结束后恢复，避免污染其它用例。
func withBelongMap(t *testing.T, m map[string][]string) {
	t.Helper()
	orig := global.GetProductBelongs()
	global.SetProductBelongs(m)
	t.Cleanup(func() { global.SetProductBelongs(orig) })
}

func TestBuildBelongIndex(t *testing.T) {
	withBelongMap(t, map[string][]string{
		"广香一级":  {"广香", "广香一级果", " 广香柠檬 "},
		"香水柠檬":  {"香水", "柠檬", "广香柠檬"}, // 与上一分类的别名冲突
		" 黄柠檬 ": {"黄柠", "香水柠檬"},       // 别名撞上别家的一级属性名
		"空分类":   {},
	})

	idx := buildBelongIndex()

	// 一级属性自身命中自身
	if got := idx["广香一级"]; got != "广香一级" {
		t.Fatalf("一级属性应命中自身，得到 %q", got)
	}
	// 别名归并到一级属性，且配置里的首尾空格被清理
	if got := idx["广香"]; got != "广香一级" {
		t.Errorf("别名 广香 应归并到 广香一级，得到 %q", got)
	}
	if got := idx["广香柠檬"]; got != "广香一级" {
		t.Errorf("带空格的别名应归并到 广香一级，得到 %q", got)
	}
	// 一级属性名优先：即使被别家当作别名，也不能被改指向
	if got := idx["香水柠檬"]; got != "香水柠檬" {
		t.Errorf("一级属性名优先级最高，得到 %q", got)
	}
	// 一级属性键自身的空格被清理
	if got := idx["黄柠檬"]; got != "黄柠檬" {
		t.Errorf("一级属性键应去空格，得到 %q", got)
	}
	if _, ok := idx[" 黄柠檬 "]; ok {
		t.Error("不应保留带空格的一级属性键")
	}
	if _, ok := idx[""]; ok {
		t.Error("不应收录空品名")
	}
}

func TestAggregateTopProductsByBelongCategory(t *testing.T) {
	withBelongMap(t, map[string][]string{
		"广香一级":  {"广香", "广香一级果"},
		"香水柠檬":  {"香水", "柠檬"},
		"青柠尤力克": {"青柠"},
	})

	day1 := time.Date(2026, 11, 8, 10, 0, 0, 0, time.Local)
	day2 := day1.AddDate(0, 0, -1)
	list := []models.AddressInfo{
		{ProductSpecs: models.ProductSpecsJSON{
			{Name: "广香", Weight: 10},
			{Name: "广香一级果", Weight: 5},
			{Name: "香水", Weight: 20},
			{Name: "新品测试", Weight: 7}, // 未配置，不应进榜
		}},
		{ProductSpecs: models.ProductSpecsJSON{
			{Name: "柠檬", Weight: 3},
			{Name: "青柠", Weight: 4},
		}},
	}
	list[0].CreatedAt = day1
	list[1].CreatedAt = day2

	resp := aggregate(list, DashboardRequest{Period: "day"})

	if !resp.BelongReady {
		t.Fatal("配置存在时 BelongReady 应为 true")
	}
	if resp.Summary.TotalOrders != 2 {
		t.Errorf("总订单数应为 2，得到 %d", resp.Summary.TotalOrders)
	}
	// 总销量不被爆品榜筛选影响：10+5+20+7+3+4 = 49
	if resp.Summary.TotalWeight != 49 {
		t.Errorf("总销量应为全量 49 斤，得到 %v", resp.Summary.TotalWeight)
	}
	if resp.Summary.ProductCount != 3 {
		t.Errorf("有销量的一级属性应为 3 个，得到 %d", resp.Summary.ProductCount)
	}
	if len(resp.Trend) != 2 || resp.Trend[1].Weight != 42 {
		t.Errorf("趋势应按全量规格计重，得到 %+v", resp.Trend)
	}

	want := map[string]float64{"广香一级": 15, "香水柠檬": 23, "青柠尤力克": 4}
	if len(resp.TopProducts) != len(want) {
		t.Fatalf("爆品榜应只有 3 个一级属性，得到 %+v", resp.TopProducts)
	}
	for _, p := range resp.TopProducts {
		if w, ok := want[p.Name]; !ok || w != p.Value {
			t.Errorf("爆品榜出现异常项或斤数错误: %+v", p)
		}
	}
	if len(resp.UnmappedProducts) != 1 || resp.UnmappedProducts[0].Name != "新品测试" || resp.UnmappedProducts[0].Value != 7 {
		t.Errorf("未配置商品应单独统计，得到 %+v", resp.UnmappedProducts)
	}

	// 时间范围过滤：总订单数/总销量/榜单都只算范围内的订单
	start := day1.Add(-time.Hour)
	end := day1.Add(time.Hour)
	scoped := aggregate(list, DashboardRequest{Period: "day", Start: start, End: end})
	if scoped.Summary.TotalOrders != 1 || scoped.Summary.TotalWeight != 42 {
		t.Errorf("时间范围过滤后应为 1 单 / 42 斤，得到 %d 单 / %v 斤",
			scoped.Summary.TotalOrders, scoped.Summary.TotalWeight)
	}
}

func TestAggregateFallbackWhenBelongMissing(t *testing.T) {
	withBelongMap(t, map[string][]string{})
	now := time.Now()
	list := []models.AddressInfo{
		{ProductSpecs: models.ProductSpecsJSON{{Name: "广香", Weight: 10}}},
	}
	list[0].CreatedAt = now

	resp := aggregate(list, DashboardRequest{})
	if resp.BelongReady {
		t.Fatal("配置为空时 BelongReady 应为 false")
	}
	if len(resp.TopProducts) != 1 || resp.TopProducts[0].Name != "广香" {
		t.Errorf("配置缺失时应退回原始品名展示，得到 %+v", resp.TopProducts)
	}
	if len(resp.UnmappedProducts) != 0 {
		t.Errorf("配置缺失时不应产生未配置提示，得到 %+v", resp.UnmappedProducts)
	}
}
