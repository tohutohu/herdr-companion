package deadletter

import (
	"bufio"
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
