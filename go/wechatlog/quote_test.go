package wechatlog

import (
	"strings"
	"testing"

	"order_server/go/wechatlog/internal/model"
)

// 内置解密链路：ParseMediaInfo 解析出的引用消息必须能把「被引用的订单原文」带出来。
func TestReferQuote(t *testing.T) {
	orderText := "11-9\n店名：寿光示例广场店\n电话：13800138000\n【广香5斤】"
	ref := &model.Message{
		Type: 1, SenderName: "帅涛", Content: orderText,
	}
	m := &model.Message{
		Type: 49, SubType: 57, Content: "这单不要了",
		Contents: map[string]interface{}{"refer": ref},
	}
	text, sender := m.ReferQuote()
	if text != orderText || sender != "帅涛" {
		t.Fatalf("引用原文/发言人提取不对: text=%q sender=%q", text, sender)
	}
	if text, sender := (&model.Message{Content: "普通消息"}).ReferQuote(); text != "" || sender != "" {
		t.Fatalf("非引用消息不应带出引用: %q %q", text, sender)
	}
}

// WCDB 直读链路：引用消息的原始 XML 要被拆成「回复正文 + 引用原文 + 引用人」，
// 而不是被 usableTextContent 当成非文本消息整条丢掉。
func TestParseQuoteXML(t *testing.T) {
	raw := `<msg><appmsg appid=""><title>这单不要了</title>` +
		`<refermsg><type>1</type><displayname>帅涛</displayname>` +
		`<content>11-9 店名：寿光示例广场店 13800138000 【广香5斤】</content>` +
		`</refermsg></appmsg></msg>`
	reply, quoted, sender, ok := parseQuoteXML(raw)
	if !ok {
		t.Fatal("应识别为引用消息")
	}
	if reply != "这单不要了" || sender != "帅涛" || !strings.Contains(quoted, "寿光示例广场店") {
		t.Fatalf("引用 XML 拆解不对: reply=%q quoted=%q sender=%q", reply, quoted, sender)
	}
	if !usableTextContent(reply) {
		t.Fatal("拆解后的回复正文应能通过文本消息过滤")
	}

	if _, _, _, ok := parseQuoteXML("今天天气不错"); ok {
		t.Fatal("普通文本不应被当成引用消息")
	}
}
