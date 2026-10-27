package codex

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

func delegationItem(t *testing.T, name, output string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"type": "functionCallOutput", "id": "fco_1", "name": name, "namespace": "codex_tui", "output": output})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func Test自分のスレッドから届いた委譲メッセージは送信元へのリンクを付けない(t *testing.T) {
	raw := delegationItem(t, "send_message_to_thread", "<codex_delegation>\n  <source_thread_id>01a0f000-0000-7000-8000-000000000001</source_thread_id>\n  <input>メモ</input>\n</codex_delegation>")
	m, ok := convertItem(raw, time.Time{}, convertOptions{SessionID: "codex:01a0f000-0000-7000-8000-000000000001"})
	if !ok || m.Role != model.RoleSystem || len(m.Blocks) != 1 || m.Blocks[0].Type != model.BlockSessionMessage || m.Blocks[0].Text != "メモ" || m.Blocks[0].SessionID != "" {
		t.Fatalf("message = %+v", m)
	}
}

func Test委譲の枠を持たない関数出力はツール出力のまま表示する(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"別のツール":  delegationItem(t, "exec", "<codex_delegation><source_thread_id>01a0f000-0000-7000-8000-000000000002</source_thread_id><input>x</input></codex_delegation>"),
		"本文が空":   delegationItem(t, "send_message_to_thread", "<codex_delegation><source_thread_id>01a0f000-0000-7000-8000-000000000002</source_thread_id><input> </input></codex_delegation>"),
		"壊れたXML": delegationItem(t, "send_message_to_thread", "<codex_delegation><input>a < b</input></codex_delegation>"),
	} {
		m, ok := convertItem(raw, time.Time{}, convertOptions{SessionID: "codex:test"})
		if !ok || m.Role != model.RoleTool || m.Blocks[0].Type != model.BlockText {
			t.Errorf("%s: message = %+v", name, m)
		}
	}
}
