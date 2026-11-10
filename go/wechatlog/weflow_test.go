package wechatlog

// 覆盖 WeFlow 式链路：NDJSON 协议、密钥缓存与失效判定、WCDB JSON/内容解码。
// TestMain 里的 helper 模式让测试二进制自身充当 keyProvider，可真实拉起子进程验证协议。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestMain(m *testing.M) {
	if os.Getenv("WF_KEYPROVIDER_HELPER") == "1" {
		scanner := bufio.NewScanner(os.Stdin)
		var req map[string]interface{}
		if scanner.Scan() {
			_ = json.Unmarshal(scanner.Bytes(), &req)
		}
		if req["action"] != "get_db_key" {
			fmt.Println(`{"type":"result","success":false,"error":"unexpected action"}`)
			os.Exit(0)
		}
		fmt.Println(`{"type":"progress","message":"正在读取密钥","level":0}`)
		fmt.Println(`{"type":"result","success":true,"key":"aabbccdd00112233","accountId":"wxid_helper"}`)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestScanProviderOutput(t *testing.T) {
	raw := strings.Join([]string{
		"diagnostic line (ignored)",
		`{"type":"progress","message":"扫描内存中","level":0}`,
		`{"type":"progress","message":"已找到候选","level":1}`,
		`{"type":"result","success":true,"key":"aabbcc","accountId":"wxid_test"}`,
		`{"type":"result","success":true,"key":"must-not-be-read"}`,
	}, "\n")
	var got KeyProviderResult
	var statuses []string
	found := scanProviderOutput(strings.NewReader(raw), func(m string, _ int) {
		statuses = append(statuses, m)
	}, &got)
	if !found || !got.Success || got.Key != "aabbcc" || got.AccountID != "wxid_test" {
		t.Fatalf("unexpected result: %+v found=%v", got, found)
	}
	if len(statuses) != 2 || len(got.Logs) != 2 {
		t.Fatalf("progress events = %v / %v", statuses, got.Logs)
	}
}

func TestScanProviderOutputKeysAndFailure(t *testing.T) {
	var got KeyProviderResult
	raw := `{"type":"result","success":true,"keys":{"message/message_0.db":{"enc_key":"deadbeef"}}}` + "\n"
	if !scanProviderOutput(strings.NewReader(raw), nil, &got) || !strings.Contains(got.Key, "deadbeef") {
		t.Fatalf("keys JSON not accepted: %+v", got)
	}
	got = KeyProviderResult{}
	raw = `{"type":"result","success":false,"error":"未找到微信进程"}` + "\n"
	if !scanProviderOutput(strings.NewReader(raw), nil, &got) || got.Success || got.Error == "" {
		t.Fatalf("failed result not parsed: %+v", got)
	}
	if scanProviderOutput(strings.NewReader("只有日志\n"), nil, &got) {
		t.Fatal("plain logs must not count as a result")
	}
}

// 真实拉起一个子进程，验证 stdin 一行请求 + stdout NDJSON 的完整协议。
func TestInvokeKeyProviderRealProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WF_KEYPROVIDER_HELPER", "1")
	got, err := InvokeKeyProvider(context.Background(), exe,
		ProviderRequest{DBPath: "session.db", AccountID: "wxid_test"}, 15*time.Second, nil)
	if err != nil {
		t.Fatalf("InvokeKeyProvider: %v", err)
	}
	if !got.Success || got.Key != "aabbccdd00112233" || got.AccountID != "wxid_helper" {
		t.Fatalf("unexpected provider result: %+v", got)
	}
	if len(got.Logs) == 0 {
		t.Fatal("progress event was not collected")
	}
}

func TestKeyCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	_ = ClearKeyCache()
	if info := KeyCacheInfo(); info["exists"] != false {
		t.Fatalf("cache should be empty: %v", info)
	}
	want := KeyCache{DataDir: dir, Fingerprint: "fp123", Key: "deadbeef", Source: "keyProvider", AccountID: "wxid_a"}
	if err := SaveKeyCache(want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadKeyCache()
	if err != nil || got.Key != want.Key || got.Fingerprint != want.Fingerprint || got.UpdatedAt == "" {
		t.Fatalf("cache round trip failed: %+v %v", got, err)
	}
	if info := KeyCacheInfo(); info["exists"] != true {
		t.Fatalf("cache info wrong: %v", info)
	}
	if err := ClearKeyCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyCache(); err == nil {
		t.Fatal("cache file should be gone")
	}
}

func TestDBFingerprintChanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "message_0.db"), []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}
	fp1 := dbFingerprint(dir)
	if fp1 == "" {
		t.Fatal("fingerprint must not be empty")
	}
	if fp2 := dbFingerprint(dir); fp2 != fp1 {
		t.Fatalf("fingerprint not stable: %s vs %s", fp1, fp2)
	}
	if err := os.WriteFile(filepath.Join(dir, "message_1.db"), []byte("second"), 0644); err != nil {
		t.Fatal(err)
	}
	if fp3 := dbFingerprint(dir); fp3 == fp1 {
		t.Fatal("new message shard must invalidate the cache")
	}
}

func TestResolveKeyPriority(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	_ = ClearKeyCache()

	// 手动密钥优先级最高，且不应触发任何外部调用
	scanCalls := 0
	res, err := ResolveKey(KeyRequest{
		DataDir: dir, ManualKey: "manualkey", KeyProviderPath: "/nonexistent/provider",
		AutoKey: true, AutoKeyOnFailure: true,
		Scan: func() (string, error) { scanCalls++; return "fromscan", nil },
	})
	if err != nil || res.Key != "manualkey" || res.Source != "手动配置" || scanCalls != 0 {
		t.Fatalf("manual key priority broken: %+v %v scanCalls=%d", res, err, scanCalls)
	}

	// 缓存命中：不调 provider、不扫描
	if err := os.WriteFile(filepath.Join(dir, "session.db"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	fp := dbFingerprint(dir)
	_ = SaveKeyCache(KeyCache{DataDir: dir, Fingerprint: fp, Key: "cachedkey", Source: "keyProvider"})
	res, err = ResolveKey(KeyRequest{
		DataDir: dir, AutoKey: true, AutoKeyOnFailure: true,
		Scan: func() (string, error) { scanCalls++; return "fromscan", nil },
	})
	if err != nil || res.Key != "cachedkey" || !strings.Contains(res.Source, "缓存") || scanCalls != 0 {
		t.Fatalf("cache priority broken: %+v %v scanCalls=%d", res, err, scanCalls)
	}

	// 完全没有任何来源时给出明确错误
	_ = ClearKeyCache()
	_, err = ResolveKey(KeyRequest{DataDir: dir})
	if err == nil || !strings.Contains(err.Error(), "未能取得微信数据库密钥") {
		t.Fatalf("expected a clear key error, got %v", err)
	}
}

func TestParseWcdbJSONShapes(t *testing.T) {
	rows, err := parseWcdbJSON(`[{"content":"a"},{"content":"b"}]`)
	if err != nil || len(rows) != 2 {
		t.Fatalf("array shape: %v %v", rows, err)
	}
	rows, err = parseWcdbJSON(`{"rows":[{"content":"c"}],"hasMore":true}`)
	if err != nil || len(rows) != 1 {
		t.Fatalf("object shape: %v %v", rows, err)
	}
}

func TestDecodeMessageContent(t *testing.T) {
	plain := "【广香 5斤】"
	hexed := hex.EncodeToString([]byte(plain))
	if got := decodeMessageContent(map[string]interface{}{"message_content": hexed}); got != plain {
		t.Fatalf("hex content = %q, want %q", got, plain)
	}
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = zw.Write([]byte(plain))
	_ = zw.Close()
	if got := decodeMessageContent(map[string]interface{}{
		"compress_content": hex.EncodeToString(buf.Bytes()),
	}); got != plain {
		t.Fatalf("zstd content = %q, want %q", got, plain)
	}
}

func TestSplitRowSenderAndTime(t *testing.T) {
	row := map[string]interface{}{
		"message_content": "wxid_abc123:\n【香水柠檬5斤】",
		"create_time":     fmt.Sprintf("%d", time.Now().Unix()),
	}
	sender, wxid, content := splitRowSender(row)
	if sender != "wxid_abc123" || wxid != "wxid_abc123" || !strings.Contains(content, "香水柠檬") {
		t.Fatalf("split = %q / %q / %q", sender, wxid, content)
	}
	if rowCreateTime(row).IsZero() {
		t.Fatal("create_time was not parsed")
	}
}

func TestPickSessionIDAndDisplayNames(t *testing.T) {
	sessions := []map[string]interface{}{
		{"username": "111@chatroom", "displayName": "帅涛吃什么"},
		{"username": "222@chatroom", "displayName": "别的群"},
	}
	if got := pickSessionID(sessions, "帅涛吃什么", ""); got != "111@chatroom" {
		t.Fatalf("session id = %q", got)
	}
	if got := pickSessionID(sessions, "未匹配", "333@chatroom"); got != "333@chatroom" {
		t.Fatalf("configured session id = %q", got)
	}
	names := parseDisplayNames(`{"wxid_abc123":"帅涛"}`)
	if names["wxid_abc123"] != "帅涛" {
		t.Fatalf("display names = %v", names)
	}
}

func TestSingleHexKey(t *testing.T) {
	if got := singleHexKey("deadbeef"); got != "deadbeef" {
		t.Fatalf("plain key = %q", got)
	}
	if got := singleHexKey(`{"session/session.db":{"enc_key":"sessionkey"},"message/message_0.db":{"enc_key":"msgkey"}}`); got != "sessionkey" {
		t.Fatalf("per-db key = %q", got)
	}
	if got := singleHexKey(`{"key":"simplekey"}`); got != "simplekey" {
		t.Fatalf("simple key object = %q", got)
	}
}

func TestWcdbMessagesFromRows(t *testing.T) {
	ts := time.Now().Add(-time.Hour)
	rows := []map[string]interface{}{
		{"sender": "wxid_abc123", "message_content": "【广香 5斤】", "create_time": fmt.Sprintf("%d", ts.Unix())},
		{"sender": "wxid_abc123", "message_content": "【广香 5斤】", "create_time": fmt.Sprintf("%d", ts.Unix())}, // 去重
		{"sender": "wxid_abc123", "message_content": "[图片]", "create_time": fmt.Sprintf("%d", ts.Unix())},
		{"sender": "wxid_other", "message_content": "今天天气不错", "create_time": fmt.Sprintf("%d", ts.Unix())},
	}
	names := map[string]string{"wxid_abc123": "帅涛"}
	msgs, warns := wcdbMessagesFromRows(rows, names, []string{"帅涛"}, ts.Add(-24*time.Hour), time.Now().Add(time.Hour))
	if len(warns) != 0 || len(msgs) != 1 || msgs[0].Sender != "帅涛" || !strings.Contains(msgs[0].Content, "广香") {
		t.Fatalf("messages = %+v warns=%v", msgs, warns)
	}

	// 没有显示名映射时应跳过昵称过滤并给出警告，避免误杀订单
	msgs, warns = wcdbMessagesFromRows(rows, nil, []string{"帅涛"}, ts.Add(-24*time.Hour), time.Now().Add(time.Hour))
	if len(warns) == 0 || len(msgs) != 2 {
		t.Fatalf("fallback messages = %+v warns=%v", msgs, warns)
	}
}
