package wechatlog

import "testing"

func TestParseDbKeyMap(t *testing.T) {
	raw := `{"message\\message_0.db":{"enc_key":"aabb","salt":"cc"},"db_storage\\session\\session.db":{"enc_key":"ddff","salt":"ee"}}`
	m, ok := parseDbKeyMap(raw)
	if !ok {
		t.Fatal("expected a key map")
	}
	if got := m["message/message_0.db"]; got != "aabb" {
		t.Fatalf("message key = %q, want aabb", got)
	}
	if got := m["session/session.db"]; got != "ddff" {
		t.Fatalf("session key = %q, want ddff", got)
	}
	if _, ok := parseDbKeyMap("0123456789abcdef"); ok {
		t.Fatal("a plain hex key must not be treated as a key map")
	}
}

func TestNormalizeDBRel(t *testing.T) {
	cases := map[string]string{
		`db_storage\message\message_0.db`:   "message/message_0.db",
		`message/message_0.db`:              "message/message_0.db",
		`./DB_Storage/message/message_0.db`: "message/message_0.db",
	}
	for in, want := range cases {
		if got := normalizeDBRel(in); got != want {
			t.Errorf("normalizeDBRel(%q) = %q, want %q", in, got, want)
		}
	}
}
