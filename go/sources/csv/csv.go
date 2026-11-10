// Package csvsource 是 ports.ChatSource 的一个参考实现：从 CSV / JSONL 文件读取聊天消息。
//
// 这是"新增来源只需新建一个包、不碰任何现有文件"的具体示范。
// 微信 / 飞书 / 企业微信等真实来源应各自建独立包实现 ports.ChatSource。
// 涉及密钥/解密的直接依赖应收进带 build tag 的门面实现（见 go/wechatapi），
// 上层 ChatSource 适配器本身仍可在默认构建编译与测试（见 go/sources/wechat）。
package csvsource

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"order_server/go/ports"
)

// Source 从文件读取聊天消息。支持 CSV（首行表头）与 JSONL（每行一个 JSON 对象）。
type Source struct {
	name string
}

// New 构造一个 CSV/JSONL 来源，name 为空时默认 "csv"。
func New(name string) *Source {
	if name == "" {
		name = "csv"
	}
	return &Source{name: name}
}

func (s *Source) Name() string { return s.name }

// Export 读取 opt.DataDir 指向的文件。按扩展名选择解析方式：
// .csv/.tsv → 表格；.jsonl/.json → JSON Lines。
func (s *Source) Export(ctx context.Context, opt ports.ExportOptions) (*ports.ExportResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	path := strings.TrimSpace(opt.DataDir)
	if path == "" {
		return nil, fmt.Errorf("csv source: DataDir 必须指向一个文件")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("csv source: 打开文件失败: %w", err)
	}
	defer f.Close()

	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".jsonl"), strings.HasSuffix(lower, ".json"):
		return s.exportJSONL(f)
	default:
		return s.exportCSV(f)
	}
}

func (s *Source) exportCSV(r *os.File) (*ports.ExportResult, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	recs, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("csv source: 解析失败: %w", err)
	}
	if len(recs) == 0 {
		return &ports.ExportResult{}, nil
	}
	header := recs[0]
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	start := 0
	if _, ok := col["content"]; ok { // 有表头
		start = 1
	} else { // 无表头：默认列序 id,sender,content,time,issystem
		col = map[string]int{"id": 0, "sender": 1, "content": 2, "time": 3, "issystem": 4}
	}
	out := make([]ports.ChatMessage, 0, len(recs)-start)
	for i := start; i < len(recs); i++ {
		out = append(out, toMessage(recs[i], col, i))
	}
	return &ports.ExportResult{Messages: out}, nil
}

func toMessage(rec []string, col map[string]int, row int) ports.ChatMessage {
	get := func(key string) string {
		if i, ok := col[key]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	m := ports.ChatMessage{
		ID:      get("id"),
		Sender:  get("sender"),
		Content: get("content"),
		Time:    get("time"),
	}
	if v := get("issystem"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			m.IsSystem = b
		}
	}
	if m.ID == "" {
		m.ID = fmt.Sprintf("row-%d", row) // 退化为行号，保证去重键非空
	}
	return m
}

func (s *Source) exportJSONL(r *os.File) (*ports.ExportResult, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	out := make([]ports.ChatMessage, 0)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var obj struct {
			ID       string `json:"id"`
			Sender   string `json:"sender"`
			Content  string `json:"content"`
			Time     string `json:"time"`
			IsSystem bool   `json:"is_system"`
		}
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			return nil, fmt.Errorf("csv source: 第 %d 行 JSON 解析失败: %w", lineNo, err)
		}
		if obj.ID == "" {
			obj.ID = fmt.Sprintf("line-%d", lineNo)
		}
		out = append(out, ports.ChatMessage{
			ID:       obj.ID,
			Sender:   obj.Sender,
			Content:  obj.Content,
			Time:     obj.Time,
			IsSystem: obj.IsSystem,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("csv source: 读取失败: %w", err)
	}
	return &ports.ExportResult{Messages: out}, nil
}
