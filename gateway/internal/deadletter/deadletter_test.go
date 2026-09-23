package deadletter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func Test処理できなかったデータを日付ごとのJSONLに生データ付きで保存する(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir)
	w.now = func() time.Time { return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC) }

	raw := []byte(`{"type":"tool_use","name":"FooTool"}`)
	w.Record("claude", "claude:s1", UnknownContent, "unsupported tool_use", raw)
	w.Record("claude", "claude:s1", UnknownContent, "unsupported tool_use", raw) // 重複は1件にまとめる
	w.Record("codex", "codex:t1", ParseError, "bad json", []byte("{broken"))

	f, err := os.Open(filepath.Join(dir, "2026-09-17.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var entries []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line is not JSON: %s", sc.Text())
		}
		entries = append(entries, e)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Kind != UnknownContent || string(entries[0].Raw) != string(raw) || entries[0].SessionID != "claude:s1" {
		t.Errorf("entry 0 = %+v", entries[0])
	}
	// JSON でない raw は文字列として保存する
	if string(entries[1].Raw) != `"{broken"` {
		t.Errorf("entry 1 raw = %s", entries[1].Raw)
	}
}

func Test再起動後も当日すでに記録した内容は重複して保存しない(t *testing.T) {
	dir := t.TempDir()
	now := func() time.Time { return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC) }
	// HTML 文字や整形済み JSON は保存時にエスケープ・圧縮される
	raw := []byte("{\n  \"type\": \"frame-link\", \"title\": \"<a & b>\"\n}")
	first := NewWriter(dir)
	first.now = now
	first.Record("claude", "claude:s1", UnknownEvent, "unknown entry type: frame-link", raw)
	first.Record("codex", "codex:t1", ParseError, "bad json", []byte("{broken"))

	restarted := NewWriter(dir)
	restarted.now = now
	restarted.Record("claude", "claude:s1", UnknownEvent, "unknown entry type: frame-link", raw)
	restarted.Record("codex", "codex:t1", ParseError, "bad json", []byte("{broken"))
	restarted.Record("codex", "codex:t1", ParseError, "another", nil)

	b, err := os.ReadFile(filepath.Join(dir, "2026-09-23.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(b, []byte("\n")); n != 3 {
		t.Errorf("lines = %d, want 3:\n%s", n, b)
	}
}
