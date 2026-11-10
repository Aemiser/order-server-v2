package wechatorder

import (
	"fmt"
	"order_server/go/cfgseed"
	"order_server/go/global"
	"os"
	"strings"
	"testing"
)

// 提示词正文现在存放在数据库 cfg_prompts 表，其初始内容来自内嵌的 cfg_seed.json。
// 测试把种子灌进配置快照，等价于「从配置表装载完成」的状态。
func TestMain(m *testing.M) {
	seed, err := cfgseed.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载 cfg_seed.json 失败: %v\n", err)
		os.Exit(1)
	}
	prompts := make(map[string]string, len(seed.Prompts))
	for _, p := range seed.Prompts {
		prompts[p.Code] = p.Content
	}
	global.SetPrompts(prompts)
	os.Exit(m.Run())
}

// 证明提示词真的来自配置表：改掉表里的值，拼出的提示词立刻跟着变。
func TestSystemPromptComesFromCfgTable(t *testing.T) {
	old := global.Prompt(global.PromptWechatOrderBase)
	global.SetPrompt(global.PromptWechatOrderBase, "来自配置表的自定义提示词")
	t.Cleanup(func() { global.SetPrompt(global.PromptWechatOrderBase, old) })
	if got := buildSystemPrompt(AnalyzeOptions{}); got != "来自配置表的自定义提示词" {
		t.Fatalf("提示词未从配置表读取: %q", got)
	}
}

// 提示词必须保留原 systemPrompt.txt（现 cfg_prompts.order_extract）的字段提取规则与中文键 JSON 格式。
func TestSystemPromptKeepsSourceRules(t *testing.T) {
	base := buildSystemPrompt(AnalyzeOptions{})
	for _, want := range []string{
		"记录分割", "收件人", "手机号", "收货地址", "商品规格", "备注",
		`"name"`, `"weight"`, "1點點奶茶店", "严格", "按输入顺序",
		"date_confirmed", "needs_review", "confidence",
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("提示词缺少来自 cfg_prompts 的规则/字段 %q", want)
		}
	}
	if strings.Contains(base, "全量消息模式") {
		t.Fatal("粗筛开启时不应注入「全量消息模式」判定依据")
	}
}

// 粗筛关闭时，提示词自动追加「订单数据 / 聊天数据」的判定依据。
func TestSystemPromptAdaptsWhenRuleFilterOff(t *testing.T) {
	off := buildSystemPrompt(AnalyzeOptions{FilterOff: true})
	for _, want := range []string{
		"全量消息模式", "规则粗筛已关闭", "订单数据", "聊天数据",
		"可配送对象", "可下单内容", "is_order=false", "机器人", "订单管理后台",
	} {
		if !strings.Contains(off, want) {
			t.Fatalf("粗筛关闭时提示词缺少判定依据 %q", want)
		}
	}
}

// 目标订单日期要注入提示词，并明确禁止模型给无日期订单编造日期。
func TestSystemPromptInjectsTargetDate(t *testing.T) {
	p := buildSystemPrompt(AnalyzeOptions{TargetDate: "2026-11-07"})
	for _, want := range []string{"2026-11-07", "11-7", "绝对不要", "needs_review=true"} {
		if !strings.Contains(p, want) {
			t.Fatalf("目标日期注入缺少 %q", want)
		}
	}
	if strings.Contains(buildSystemPrompt(AnalyzeOptions{TargetDate: "bad"}), "本轮目标订单日期") {
		t.Fatal("非法日期不应注入提示词")
	}
}

func TestParseAIResultConfirmedOrder(t *testing.T) {
	raw := `{"is_order":true,"order_date":"11-7","date_confirmed":true,"confidence":"high","needs_review":false,"reason":"含店名、电话、地址与商品行","orders":[{"收件人":"示例广场店","手机号":"13955557083","收货地址":"华夏省大兵市健康东街示例路1号，1點點奶茶店","商品规格":[{"name":"广香柠檬","weight":5}],"备注":""}]}`
	res, err := parseAIResult("```json\n" + raw + "\n```")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !res.IsOrder || !res.DateConfirmed || res.NeedsReview || res.OrderDate != "11-7" {
		t.Fatalf("确认订单的判定字段不对: %+v", res)
	}
	if len(res.Orders) != 1 || res.Orders[0].Recipient != "示例广场店" || res.Orders[0].Phone != "13955557083" {
		t.Fatalf("中文键订单字段解析失败: %+v", res.Orders)
	}
	if len(res.Orders[0].ProductSpecs) != 1 ||
		res.Orders[0].ProductSpecs[0].Name != "广香柠檬" || res.Orders[0].ProductSpecs[0].Weight != 5 {
		t.Fatalf("商品规格解析失败: %+v", res.Orders[0].ProductSpecs)
	}
}

// 没有日期标记的订单也要识别出来，但必须标记为待人工审查。
func TestParseAIResultNoDateNeedsReview(t *testing.T) {
	raw := `{"is_order":true,"order_date":"","date_confirmed":false,"confidence":"medium","needs_review":true,"reason":"缺少日期","orders":[{"收件人":"紫薇路店","手机号":"13900002222","收货地址":"华夏省大兵市芙蓉区紫薇路221号","商品规格":[{"name":"广香","weight":5}],"备注":""}]}`
	res, err := parseAIResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsOrder || res.DateConfirmed || !res.NeedsReview || res.OrderDate != "" {
		t.Fatalf("无日期订单应标记待审查: %+v", res)
	}
	if len(res.Orders) != 1 {
		t.Fatal("无日期订单的字段不能丢")
	}
}

// 兼容 cfg_prompts.order_extract 的原生格式：模型直接返回订单数组。
func TestParseAIResultPlainArrayCompatible(t *testing.T) {
	raw := `[{"收件人":"中山西路店","手机号":"13700003333","收货地址":"华夏省大兵市新罗区中山西路1-9号","商品规格":[{"name":"香水柠檬","weight":5}],"备注":""}]`
	res, err := parseAIResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsOrder || len(res.Orders) != 1 || res.Orders[0].Recipient != "中山西路店" {
		t.Fatalf("纯数组格式解析失败: %+v", res)
	}
	if res.DateConfirmed || !res.NeedsReview {
		t.Fatal("纯数组格式没有日期信息，应交给人工审查")
	}
}

func TestParseAIResultNonOrderClearsOrders(t *testing.T) {
	res, err := parseAIResult(`{"is_order":false,"order_date":"","reason":"闲聊","orders":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsOrder || len(res.Orders) != 0 || res.NeedsReview {
		t.Fatalf("非订单结果不对: %+v", res)
	}
}
