package wechatorder

import (
	"testing"
	"time"
)

// 用户反馈被旧版粗筛误杀的真实消息：全文没有任何「店名：/地址：/电话：」标签，
// 按旧的 score>=5 门槛只有 4 分（手机号 1 + 【规格】2 + 日期 1）而被丢弃。
const plainTextOrder = "11-7\n黄示例：13812340012\n华夏省大兵市南山区 示例三道示例湾睿印B1层137号.示例牛道·潮汕活海鲜火锅(黑金店)\n【黄柠檬8斤】~~"

func TestRuleFilterKeepsPlainTextOrder(t *testing.T) {
	msgs := []ChatMessage{{ID: "1", Content: plainTextOrder}}
	candidates, dropped := RuleFilter(msgs)
	if len(candidates) != 1 || dropped != 0 {
		t.Fatalf("无标签真实订单必须放行，实际 candidates=%d dropped=%d", len(candidates), dropped)
	}
	c := candidates[0]
	if c.OrderDate != "11-7" {
		t.Fatalf("订单日期应解析为 11-7，实际 %q", c.OrderDate)
	}
	if c.Score < 1 {
		t.Fatal("应识别出至少一个下单要素")
	}
	if c.Message.Content != plainTextOrder {
		t.Fatal("交给 AI 的原文不能被改写")
	}
}

func TestRuleFilterKeepsLooseOrderVariants(t *testing.T) {
	cases := map[string]string{
		"去掉首行日期":      "黄示例：13812340012\n华夏省大兵市南山区示例三道137号\n【黄柠檬8斤】",
		"商品行不带方括号":    "11-7\n黄示例：13812340012\n华夏省大兵市南山区示例三道137号\n黄柠檬 8斤",
		"只有电话与商品行":    "13800138000\n【香水柠檬5斤】",
		"只有纯文本地址":     "11-7\n华夏省大兵市芙蓉区紫薇路221号 1點點奶茶店（请送到门店）",
		"带标签的传统格式":    "店名：紫薇路店\n电话：13900002222\n地址：华夏省大兵市芙蓉区紫薇路221号\n【广香5斤】",
		"订单里带闲聊词":     "11-7\n@小助手 别发表情包\n黄示例：13812340012\n华夏省大兵市南山区示例三道137号\n【黄柠檬8斤】",
		"含 @ 与日报字样":    "11-7 日报补单\n收货人：黄示例 13800138000\n【黄柠檬8斤】",
		"带 @ 的极简订单":   "@小助手 13800138000 【黄柠檬8斤】",
		"无要素但在谈送货":   "下午能送到吗 顺便确认一下数量",
		"灰度消息交 AI 判定": "今天下午三点能送到吗 麻烦确认一下",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			candidates, dropped := RuleFilter([]ChatMessage{{Content: content}})
			if len(candidates) != 1 || dropped != 0 {
				t.Fatalf("应放行给 AI，实际 candidates=%d dropped=%d", len(candidates), dropped)
			}
		})
	}
}

func TestRuleFilterDropsOnlyObviousNonOrders(t *testing.T) {
	cases := map[string]string{
		"空消息":      "   ",
		"纯图片占位":    "[图片]",
		"纯表情占位":    "[捂脸][ ok ]",
		"语音占位":     "[语音]  ",
		"极短寒暄":     "好的收到",
		"单个字":      "嗯",
		"群机器人播报":  "今日龙王是 @张三，活跃榜已更新 ⚡",
		"系统撤回提示":  "「张三」撤回了一条消息",
		"入群通知":     "李四邀请王五加入了群聊",
		"管理后台回显":  "今日订单 12 笔，累计 300 笔，置信 0.98",
		"纯链接":      "https://example.com/a/b/c",
		"天气播报":     "上海 11月7日（周六）天气 🌤，阴转多云，气温约 14～18℃",
		"游戏播报":     "🎮 看图猜成语 · 第 4 题开赛！答对 +10 分",
		"寒暄闲聊":     "脑筋急转弯 🧠 什么东西越洗越脏？",
		// 系统生成的订单明细含全套字段，但不是顾客下单，避免重复采集
		"订单管理回显":  "订单 c9321b90d05a68ed｜置信 0.99\n日期：11-5\n店名：世纪大道万达店\n电话：13600004444\n地址：华夏省大兵市历下区\n【广香5斤】",
		"今日订单汇总":  "今日订单 2 笔｜门店 2 家｜商品 10.0 斤",
		"游戏答案占位":  "⏰ 连续 181 秒无人答对，自动揭晓答案\n【答案】识石成金\n【发音】shí shí chéng jīn\n【解释】识石成金，意思是能够辨识璞石当中蕴藏的美玉",
		"工作区闲聊":   "我是你的 AI 助手，住在你的工作区里，能帮你查天气、出主意、管文件、跑任务",
		"都市闲聊":    "我是御坂美琴，学园都市第三位的Level 5，常盘台的王牌",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			candidates, dropped := RuleFilter([]ChatMessage{{Content: content}})
			if len(candidates) != 0 || dropped != 1 {
				t.Fatalf("明显非订单应被过滤，实际 candidates=%d dropped=%d", len(candidates), dropped)
			}
		})
	}
}

// 粗筛开关：关闭时聊天消息全部直接交 AI，不再由规则层过滤闲聊。
func TestRuleFilterSwitchOffSendsEverythingToAI(t *testing.T) {
	msgs := []ChatMessage{
		{ID: "1", Content: plainTextOrder},
		{ID: "2", Content: "今日龙王是 @张三，活跃榜已更新"},
		{ID: "3", Content: "好的收到"},
		{ID: "4", Content: "   "},
	}
	off := DefaultConfig()
	off.UseRuleFilter = false
	candidates, dropped := RuleFilterWithConfig(msgs, off)
	if len(candidates) != 3 || dropped != 1 {
		t.Fatalf("关闭粗筛应放行 3 条非空消息，实际 candidates=%d dropped=%d", len(candidates), dropped)
	}
	on := DefaultConfig()
	on.UseRuleFilter = true
	candidates, dropped = RuleFilterWithConfig(msgs, on)
	if len(candidates) != 1 || dropped != 3 {
		t.Fatalf("开启粗筛应只保留真实订单，实际 candidates=%d dropped=%d", len(candidates), dropped)
	}
	if len(candidates)+dropped != len(msgs) {
		t.Fatal("消息去向必须守恒")
	}
}

func TestResolveOrderDate(t *testing.T) {
	target := time.Date(2026, 11, 7, 0, 0, 0, 0, time.Local)
	cases := []struct {
		name, ai, rule, label string
		hasMark, matches      bool
	}{
		{"AI 提取的 M-D 命中", "11-7", "", "11-7", true, true},
		{"斜杠写法", "11/6", "", "11-6", true, false},
		{"回退到规则提取", "", "11-7", "11-7", true, true},
		{"完全没有日期标记", "", "", "", false, false},
		{"完整日期命中", "2026-11-07", "", "11-7", true, true},
		{"完整日期不符", "2026-11-05", "", "11-5", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			label, has, match := ResolveOrderDate(c.ai, c.rule, target)
			if label != c.label || has != c.hasMark || match != c.matches {
				t.Fatalf("得到 (%q,%v,%v)，期望 (%q,%v,%v)", label, has, match, c.label, c.hasMark, c.matches)
			}
		})
	}
}

func TestRuleFilterDroppedCountMatchesMessages(t *testing.T) {
	mixed := []ChatMessage{
		{Content: plainTextOrder},
		{Content: "[图片]"},
		{Content: "11-7\n店名：紫薇路店\n电话：13900002222\n【广香5斤】"},
		{Content: "好的"},
		{Content: "今天下午能送到吗 麻烦确认一下"},
	}
	candidates, dropped := RuleFilter(mixed)
	if len(candidates)+dropped != len(mixed) {
		t.Fatalf("每条消息都必须有明确去向，实际 %d + %d != %d", len(candidates), dropped, len(mixed))
	}
	if len(candidates) != 3 || dropped != 2 {
		t.Fatalf("期望保留 3 条、过滤 2 条，实际保留 %d、过滤 %d", len(candidates), dropped)
	}
}
