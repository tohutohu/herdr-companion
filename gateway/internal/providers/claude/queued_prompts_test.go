package claude

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

func TestClaudeの通知を先に取り出してもキューの対応がずれない(t *testing.T) {
	// Anonymized queue/user records from Claude Code 2.1.300. The second
	// contentless dequeue delivered the newer notification, not the queue head.
	tr, rec, _ := loadFixture(t, "queued_notifications.jsonl")
	msgs := tr.Messages(ParseOptions{SessionID: "claude:test", Live: &providers.Live{HerdrStatus: herdr.StatusWorking}, Sink: rec})
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
