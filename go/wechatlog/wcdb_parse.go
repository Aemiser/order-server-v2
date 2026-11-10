package wechatlog

// 参考 hicccc77/WeFlow 的 wcdbCore / contentDecoder：
// WCDB 组件以 C ABI 返回 JSON 数组，消息正文可能是 hex/base64 编码的 zstd 压缩内容，
// 群消息正文常带「wxid_xxx:\n」前缀。这里做与 WeFlow 一致的容错解析，纯 Go、无平台依赖。

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
)

var reSenderPrefix = regexp.MustCompile(`^([A-Za-z0-9_\-@.]{4,64}):\r?\n`)

// WCDB 组件可能直接把引用消息的原始 XML 交上来（type 49 / subtype 57）。
// 这种消息会被 usableTextContent 当成非文本丢掉，导致「引用订单 → 这单不要了」
// 这类操作永远看不见。下面这组正则只做「尽力而为」的拆解，失败也不影响普通消息。
var (
	reQuoteBlock   = regexp.MustCompile(`(?s)<refermsg>(.*?)</refermsg>`)
	reQuoteContent = regexp.MustCompile(`(?s)<content>(.*?)</content>`)
	reQuoteName    = regexp.MustCompile(`(?s)<displayname>(.*?)</displayname>`)
	reQuoteChatUsr = regexp.MustCompile(`(?s)<chatusr>(.*?)</chatusr>`)
	reQuoteTitle   = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
)

// parseQuoteXML 把引用消息 XML 拆成「引用者回复正文 / 被引用原文 / 被引用人」。
func parseQuoteXML(content string) (reply, quoted, sender string, ok bool) {
	s := strings.TrimSpace(content)
	if !strings.HasPrefix(s, "<msg") || !strings.Contains(s, "<refermsg") {
		return content, "", "", false
	}
	rest := s
	var block string
	if m := reQuoteBlock.FindStringSubmatch(s); m != nil {
		block = m[1]
		rest = strings.Replace(s, m[0], "", 1)
	}
	if m := reQuoteTitle.FindStringSubmatch(rest); m != nil {
		reply = cleanXMLText(m[1])
	}
	if m := reQuoteContent.FindStringSubmatch(block); m != nil {
		quoted = cleanXMLText(m[1])
	}
	if m := reQuoteName.FindStringSubmatch(block); m != nil {
		sender = cleanXMLText(m[1])
	}
	if sender == "" {
		if m := reQuoteChatUsr.FindStringSubmatch(block); m != nil {
			sender = cleanXMLText(m[1])
		}
	}
	if reply == "" {
		reply = "[引用消息]"
	}
	return reply, quoted, sender, true
}

func cleanXMLText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<![CDATA[")
	s = strings.TrimSuffix(s, "]]>")
	s = strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"",
		"&apos;", "'", "&#39;", "'", "&nbsp;", " ",
	).Replace(s)
	return strings.TrimSpace(s)
}

// parseWcdbJSON 兼容组件返回「数组」或「{rows/messages/data/sessions: [...]}」两种形态。
func parseWcdbJSON(raw string) ([]map[string]interface{}, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var arr []map[string]interface{}
	if strings.HasPrefix(raw, "[") {
		if err := json.Unmarshal([]byte(raw), &arr); err != nil {
			return nil, fmt.Errorf("解析 WCDB 返回数组失败: %w", err)
		}
		return arr, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, fmt.Errorf("解析 WCDB 返回对象失败: %w", err)
	}
	for _, key := range []string{"rows", "messages", "data", "sessions", "items", "list", "result"} {
		if v, ok := obj[key]; ok && len(v) > 0 && string(v) != "null" {
			if err := json.Unmarshal(v, &arr); err == nil {
				return arr, nil
			}
		}
	}
	return nil, nil
}

// decodeBinaryContent 对应 WeFlow contentDecoder.decodeBinaryContent：
// zstd magic(0xFD2FB528) 优先解压，否则按 UTF-8 文本处理。
func decodeBinaryContent(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if len(data) >= 4 && binary.LittleEndian.Uint32(data[:4]) == 0xFD2FB528 {
		dec, err := zstd.NewReader(bytes.NewReader(data), zstd.WithDecoderConcurrency(1))
		if err == nil {
			defer dec.Close()
			if out, err := io.ReadAll(dec); err == nil {
				return string(out)
			}
		}
		return ""
	}
	if utf8.Valid(data) {
		return string(data)
	}
	// 非 UTF-8 时保留原始字节，避免直接丢掉整条消息
	return string(bytes.Map(func(r rune) rune {
		if r == utf8.RuneError {
			return -1
		}
		return r
	}, data))
}

func looksLikeHex(s string) bool {
	if len(s) <= 16 || len(s)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func looksLikeBase64(s string) bool {
	if len(s) <= 16 || len(s)%4 != 0 {
		return false
	}
	for _, c := range s {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=' {
			continue
		}
		return false
	}
	return true
}

func decodeMaybeCompressed(v interface{}) string {
	s := pickStringScalar(v)
	if s == "" {
		return ""
	}
	if len(s) > 0 {
		allDigit := true
		for _, c := range s {
			if c < '0' || c > '9' {
				allDigit = false
				break
			}
		}
		if allDigit {
			return s
		}
	}
	if looksLikeHex(s) {
		if b, err := hex.DecodeString(s); err == nil {
			return decodeBinaryContent(b)
		}
	}
	if looksLikeBase64(s) {
		if b, err := base64.StdEncoding.DecodeString(s); err == nil {
			return decodeBinaryContent(b)
		}
	}
	return s
}

// decodeMessageContent 优先 compress_content，其次 message_content/content。
func decodeMessageContent(row map[string]interface{}) string {
	for _, key := range []string{"compress_content", "compressContent"} {
		if c := decodeMaybeCompressed(row[key]); c != "" {
			return c
		}
	}
	for _, key := range []string{"message_content", "messageContent", "content", "Content", "msg_content", "text"} {
		if c := decodeMaybeCompressed(row[key]); c != "" {
			return c
		}
	}
	return ""
}

// splitRowSender 剥掉群消息正文的「wxid:\n」前缀，顺便补出发言人 wxid。
func splitRowSender(row map[string]interface{}) (sender, wxid, content string) {
	content = decodeMessageContent(row)
	sender = pickString(row, "sender_name", "senderName", "display_name", "displayName",
		"sender_username", "senderUsername", "real_sender_id", "realSenderId",
		"sender", "talker", "from", "from_user", "source")
	if m := reSenderPrefix.FindStringSubmatch(content); m != nil {
		content = content[len(m[0]):]
		wxid = m[1]
		if sender == "" {
			sender = wxid
		}
	}
	return sender, wxid, content
}

func rowCreateTime(row map[string]interface{}) time.Time {
	v := pickNumber(row, "create_time", "createTime", "CreateTime", "msgCreateTime",
		"msg_create_time", "msg_time", "timestamp", "Timestamp", "time")
	if v <= 0 {
		return time.Time{}
	}
	if v > 1e12 {
		v /= 1000 // 毫秒
	}
	if v > 1e11 {
		v /= 1000 // 微秒
	}
	return time.Unix(int64(v), 0)
}

// pickSessionID 从 wcdb_get_sessions 返回里按群名找会话 ID。
func pickSessionID(sessions []map[string]interface{}, group, configured string) string {
	if s := strings.TrimSpace(configured); s != "" {
		return s
	}
	if strings.Contains(group, "@chatroom") || strings.HasPrefix(group, "wxid_") {
		return group
	}
	nameKeys := []string{"displayName", "display_name", "remark", "nickname", "nickName", "name", "title", "group_name", "username", "sessionId", "session_id", "id"}
	idKeys := []string{"username", "sessionId", "session_id", "id", "session", "chatroomId", "chatroom_id"}
	lowerGroup := strings.ToLower(strings.TrimSpace(group))

	// 先精确匹配，再包含匹配，避免「帅涛吃什么」误命中同名前缀群
	for _, exact := range []bool{true, false} {
		for _, s := range sessions {
			match := false
			for _, k := range nameKeys {
				val := strings.ToLower(strings.TrimSpace(pickString(s, k)))
				if val == "" {
					continue
				}
				if (exact && val == lowerGroup) || (!exact && (strings.Contains(val, lowerGroup) || strings.Contains(lowerGroup, val))) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
			if id := pickString(s, idKeys...); id != "" {
				return id
			}
		}
	}
	return ""
}

// parseDisplayNames 解析 wcdb_get_display_names 的 {wxid: name} 或数组形态。
func parseDisplayNames(raw string) map[string]string {
	out := map[string]string{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	var direct map[string]interface{}
	if strings.HasPrefix(raw, "{") && json.Unmarshal([]byte(raw), &direct) == nil {
		for k, v := range direct {
			if s := pickStringScalar(v); s != "" {
				out[k] = s
			}
		}
	}
	if len(out) == 0 {
		rows, _ := parseWcdbJSON(raw)
		for _, r := range rows {
			id := pickString(r, "username", "wxid", "sender", "id")
			name := pickString(r, "displayName", "display_name", "name", "remark", "nickname")
			if id != "" && name != "" {
				out[id] = name
			}
		}
	}
	return out
}

func pickString(row map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := row[k]; ok {
			if s := pickStringScalar(v); strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func pickStringScalar(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	default:
		return ""
	}
}

func pickNumber(row map[string]interface{}, keys ...string) float64 {
	for _, k := range keys {
		v, ok := row[k]
		if !ok || v == nil {
			continue
		}
		switch x := v.(type) {
		case float64:
			if x > 0 {
				return x
			}
		case json.Number:
			if f, err := x.Float64(); err == nil && f > 0 {
				return f
			}
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil && f > 0 {
				return f
			}
		}
	}
	return 0
}

// usableTextContent 复用现有导出链路对非文本消息的过滤规则。
func usableTextContent(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "<?xml") || strings.HasPrefix(s, "<msg") {
		return false
	}
	switch s {
	case "[图片]", "[文件]", "[表情]", "[语音]", "[视频]", "[动画表情]", "[链接]", "[位置]":
		return false
	}
	return true
}

// singleHexKey 把「按库密钥 JSON」收敛成一个 hex 密钥，供 WCDB 组件打开账号使用。
func singleHexKey(keyOrJSON string) string {
	s := strings.TrimSpace(keyOrJSON)
	if !strings.HasPrefix(s, "{") {
		return s
	}
	if m, ok := parseDbKeyMap(s); ok {
		if k := m["session/session.db"]; k != "" {
			return k
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			return m[keys[0]]
		}
	}
	// 兼容 {"key":"..."} / {"dbKey":"..."} 这类简单对象
	var obj map[string]interface{}
	if json.Unmarshal([]byte(s), &obj) == nil {
		for _, k := range []string{"key", "dbKey", "db_key", "enc_key"} {
			if v := pickString(obj, k); v != "" {
				return v
			}
		}
	}
	return s
}

func looksLikeWXID(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(s, "wxid_") || strings.Contains(s, "@chatroom") || strings.HasSuffix(s, "@chatroom")
}

// wcdbMessagesFromRows 把 WCDB 游标返回的原始行转成上层规则引擎使用的消息。
// 若组件只给了 wxid、又没实现 wcdb_get_display_names，昵称过滤会跳过（并给出警告），
// 避免因为「昵称对不上 wxid」把订单全过滤掉。
func wcdbMessagesFromRows(rows []map[string]interface{}, names map[string]string,
	senders []string, start, end time.Time) ([]Message, []string) {

	var out []Message
	var warns []string
	skipSenderFilter := len(senders) > 0 && len(names) == 0
	if skipSenderFilter {
		for _, row := range rows {
			s, _, _ := splitRowSender(row)
			if s != "" && !looksLikeWXID(s) {
				skipSenderFilter = false // 组件直接给了昵称，可以正常过滤
				break
			}
		}
	}
	if skipSenderFilter {
		warns = append(warns, "WCDB 组件未提供发言人显示名映射（wcdb_get_display_names），已跳过昵称过滤，改由规则引擎粗筛")
	}

	seen := map[string]bool{}
	for _, row := range rows {
		sender, wxid, content := splitRowSender(row)
		if n := names[wxid]; n != "" {
			sender = n
		} else if n := names[sender]; n != "" {
			sender = n
		}
		t := rowCreateTime(row)
		if t.IsZero() || t.Before(start) || t.After(end) {
			continue
		}
		content = strings.TrimSpace(content)
		var quote, quoteSender string
		if reply, quoted, qs, isQuote := parseQuoteXML(content); isQuote {
			content, quote, quoteSender = reply, quoted, qs
		}
		if !usableTextContent(content) {
			continue
		}
		if !skipSenderFilter && !senderAllowed(sender, senders) {
			continue
		}
		key := sender + "|" + content
		if quote != "" {
			key += "|" + quote
		}
		id := fmt.Sprintf("%x", md5.Sum([]byte(key)))[:16]
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, Message{
			ID: id, Sender: sender, Content: content,
			Quote: quote, QuoteSender: quoteSender,
			Time: t.Format("2006-01-02 15:04:05"),
			Date: t.Format("2006-01-02"),
			Ts:   float64(t.Unix()),
		})
	}
	return out, warns
}
