package codex

import (
	"encoding/json"
	"testing"
	"time"
)

func Test報告本文だけを翻訳可能とする(t *testing.T) {
	for _, tt := range []struct {
		raw  string
		want bool
	}{
		{`{"type":"agentMessage","id":"a","text":"Build succeeded."}`, true},
		{`{"type":"plan","id":"p","text":"Run tests"}`, false},
		{`{"type":"commandExecution","id":"c","command":"go test ./...","status":"completed"}`, false},
	} {
		m, ok := convertItem(json.RawMessage(tt.raw), time.Now(), convertOptions{})
		if !ok || len(m.Blocks) == 0 || m.Blocks[0].Report != tt.want {
			t.Fatalf("%s: %+v", tt.raw, m)
		}
	}
}
