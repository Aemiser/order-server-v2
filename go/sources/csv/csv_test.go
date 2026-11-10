package csvsource

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"order_server/go/ports"
)

func TestExport_CSV_WithHeader(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "msgs.csv")
	content := "id,sender,content,time,issystem\n" +
		"1,张三,店名：测试店 手机：13800138000,2024-11-09T10:00:00Z,false\n" +
		"2,李四,店名：另一家,2024-11-09T11:00:00Z,true\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := New("csv").Export(context.Background(), ports.ExportOptions{DataDir: p})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("期望 2 条，实际 %d", len(res.Messages))
	}
	if res.Messages[0].Sender != "张三" {
		t.Fatalf("sender 解析错误: %q", res.Messages[0].Sender)
	}
	if !res.Messages[1].IsSystem {
		t.Fatalf("issystem 解析错误")
	}
}

func TestExport_JSONL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "msgs.jsonl")
	content := `{"id":"1","sender":"张三","content":"店名：测试店 手机：13800138000","time":"2024-11-09T10:00:00Z"}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := New("csv").Export(context.Background(), ports.ExportOptions{DataDir: p})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("期望 1 条，实际 %d", len(res.Messages))
	}
	if res.Messages[0].Content == "" {
		t.Fatal("content 为空")
	}
}

func TestExport_Errors(t *testing.T) {
	s := New("csv")
	if _, err := s.Export(context.Background(), ports.ExportOptions{DataDir: ""}); err == nil {
		t.Fatal("空 DataDir 应报错")
	}
	if _, err := s.Export(context.Background(), ports.ExportOptions{DataDir: "/no/such/file.csv"}); err == nil {
		t.Fatal("不存在的文件应报错")
	}
}
