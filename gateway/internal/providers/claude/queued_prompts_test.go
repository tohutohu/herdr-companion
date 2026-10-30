package claude

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

func TestClaudeの通知を先に取り出してもキューの対応がずれない(t *testing.T) {
	// Anonymized queue/user records from Claude Code 2.1.300. The second
	// contentless dequeue delivered the newer notification, not the queue head.
	tr, rec, _ := loadFixture(t, "queued_notifications.jsonl")
	msgs := tr.Messages(ParseOptions{SessionID: "claude:test", Live: &providers.Live{State: providers.StateWorking}, Sink: rec})
	assertGolden(t, "queued_notifications.golden.json", msgs)
	if len(rec.Entries) != 0 {
		t.Fatalf("dead letters = %+v", rec.Entries)
	}
	var ids []string
	for _, m := range msgs {
		if m.Queued {
			ids = append(ids, m.ID)
		}
	}
	if !reflect.DeepEqual(ids, []string{"queued:2026-10-15T08:19:43.337Z"}) {
		t.Fatalf("queued = %v", ids)
	}
}

func TestClaudeのキューは実際に取り込まれた項目を除く(t *testing.T) {
	timestamp := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	raw := func(text string) json.RawMessage { b, _ := json.Marshal(text); return b }
	enqueue := func(text string, offset int) *entry {
		return &entry{Type: "queue-operation", Operation: "enqueue", Timestamp: timestamp.Add(time.Duration(offset) * time.Second), Content: raw(text)}
	}
	dequeue := func() *entry { return &entry{Type: "queue-operation", Operation: "dequeue"} }
	user := func(text string) *entry { return &entry{Type: "user", Message: &apiMessage{Content: raw(text)}} }
	remove := func(text string) *entry {
		return &entry{Type: "queue-operation", Operation: "remove", Content: raw(text)}
	}
	for _, tc := range []struct {
		name    string
		entries []*entry
		want    []string
	}{
		{"順番を飛ばして取り出した後に古い項目が削除される", []*entry{enqueue("old", 0), enqueue("new", 1), dequeue(), user("new"), remove("old")}, nil},
		{"複数の取り出しが後続のプロンプトと対応する", []*entry{enqueue("old", 0), enqueue("a", 1), enqueue("b", 2), dequeue(), dequeue(), user("b"), user("a")}, []string{"old"}},
		{"取り込み記録がない取り出しは先頭に対応する", []*entry{enqueue("a", 0), enqueue("b", 1), dequeue()}, []string{"b"}},
		{"取り出し後に追加された項目には遡って適用しない", []*entry{enqueue("a", 0), dequeue(), enqueue("b", 1)}, []string{"b"}},
		{"重複した本文は一件ずつ取り出す", []*entry{enqueue("same", 0), enqueue("same", 1), dequeue(), user("same")}, []string{"same"}},
		{"取り込みattachmentは同じ時刻の画像付き項目を除く", []*entry{enqueue("image", 0), enqueue("pending", 1), {Type: "attachment", Timestamp: timestamp, Attachment: &attachment{Type: attachmentQueuedCommand, Prompt: json.RawMessage(`[{"type":"text","text":"image"},{"type":"image"}]`)}}}, []string{"pending"}},
		{"取り込み済みと同文の待機項目を消さない", []*entry{enqueue("same", 0), enqueue("same", 1), remove("same"), {Type: "attachment", Timestamp: timestamp, Attachment: &attachment{Type: attachmentQueuedCommand, Prompt: raw("same")}}}, []string{"same"}},
		{"本文付き削除の後のuserで同文の待機項目を消さない", []*entry{enqueue("same", 0), enqueue("same", 1), remove("same"), user("same")}, []string{"same"}},
		{"サイドチェーンの同文プロンプトは除去しない", []*entry{enqueue("pending", 0), {Type: "user", IsSidechain: true, Message: &apiMessage{Content: raw("pending")}}}, []string{"pending"}},
		{"本文付き取り出しは一致する項目を除く", []*entry{enqueue("a", 0), enqueue("b", 1), {Type: "queue-operation", Operation: "dequeue", Content: raw("b")}}, []string{"a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, m := range queuedPrompts(tc.entries) {
				got = append(got, m.Blocks[0].Text)
				if !m.Queued || m.Role != model.RoleUser {
					t.Errorf("invalid queued message: %+v", m)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("queued=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestClaudeの別セッションメッセージはルーティング属性が違っても取り込み済みとして除く(t *testing.T) {
	// Claude Code 2.1: the enqueue record carries hop-chain, the remove record
	// does not, and the queued_command attachment is 1 ms earlier than the enqueue.
	timestamp := time.Date(2026, 10, 30, 9, 56, 25, 587_000_000, time.UTC)
	raw := func(text string) json.RawMessage { b, _ := json.Marshal(text); return b }
	frame := func(attrs string) string {
		return `<cross-session-message from="uds:/tmp/cc-socks/42521.sock"` + attrs + ` from-name="peer-a" from-mode="prompting">
返信です。
</cross-session-message>`
	}
	entries := []*entry{
		{Type: "queue-operation", Operation: "enqueue", Timestamp: timestamp, Content: raw(frame(` hop-chain="225f6e0f631b2881af12018a"`))},
		{Type: "queue-operation", Operation: "remove", Timestamp: timestamp.Add(time.Minute), Content: raw(frame(""))},
		{Type: "attachment", Timestamp: timestamp.Add(-time.Millisecond), Attachment: &attachment{Type: attachmentQueuedCommand, IsMeta: true, Prompt: raw(frame(""))}},
	}
	if got := queuedPrompts(entries); len(got) != 0 {
		t.Fatalf("queued = %+v", got)
	}
	// A different body from the same sender is still waiting.
	entries[1].Content = raw(`<cross-session-message from="uds:/tmp/cc-socks/42521.sock" from-name="peer-a">別の返信</cross-session-message>`)
	if got := queuedPrompts(entries); len(got) != 1 || !got[0].Queued || got[0].Blocks[0].Type != model.BlockSessionMessage {
		t.Fatalf("queued = %+v", got)
	}
}
