package codex

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func Test終了ターンの版はバックグラウンド結果の追加と更新を区別する(t *testing.T) {
	command := json.RawMessage(`{"id":"job","type":"commandExecution","status":"inProgress","aggregatedOutput":"waiting"}`)
	answer := json.RawMessage(`{"id":"answer","type":"agentMessage","text":"バックグラウンドで実行中です"}`)
	turn := &Turn{ID: "same-turn", Status: "completed", Items: []json.RawMessage{command, answer}}
	before := completionRevision(turn)
	if before == "" {
		t.Fatal("missing completion revision")
	}
	// Background results can update an existing tool item without changing
	// the turn ID, item count, or last assistant text shown in the push.
	turn.Items[0] = json.RawMessage(`{"id":"job","type":"commandExecution","status":"completed","aggregatedOutput":"success","exitCode":0}`)
	after := completionRevision(turn)
	if after == "" || after == before {
		t.Fatal("background tool result did not change the revision")
	}
	turn.Items = append(turn.Items, json.RawMessage(`{"id":"subagent","type":"subAgentActivity","status":"completed","result":"確認しました"}`))
	if got := completionRevision(turn); got == "" || got == after {
		t.Fatal("background result appended to the same turn did not change the revision")
	}
}

func Test終了ターンの版はJSONの表記と時刻の変化では変わらない(t *testing.T) {
	started := int64(1)
	turn := &Turn{ID: "turn", Status: "completed", StartedAt: &started, Items: []json.RawMessage{
		json.RawMessage(`{"id":"answer","type":"agentMessage","text":"完了","extra":{"b":2,"a":1}}`),
	}}
	want := completionRevision(turn)
	started = 2
	turn.Items[0] = json.RawMessage(`{ "extra": {"a":1,"b":2}, "text":"完了", "type":"agentMessage", "id":"answer" }`)
	if got := completionRevision(turn); got != want || got == "" {
		t.Fatalf("revision changed without new content: got %q, want %q", got, want)
	}
	turn.ID = "another-turn"
	if completionRevision(turn) == want {
		t.Fatal("new turn with identical output needs a distinct revision")
	}
	turn.ID = "turn"
	turn.Status = "failed"
	if completionRevision(turn) == want {
		t.Fatal("new failure needs a distinct revision")
	}
	failed := completionRevision(turn)
	turn.Error = &TurnError{Message: "background task failed"}
	if completionRevision(turn) == failed {
		t.Fatal("new error details need a distinct revision")
	}
}

func Test終了ターンの版は未完了と識別子不明なら返さない(t *testing.T) {
	for _, turn := range []*Turn{nil, {}, {ID: "turn", Status: "inProgress"}, {Status: "completed"}} {
		if got := completionRevision(turn); got != "" {
			t.Fatalf("unexpected revision %q for %+v", got, turn)
		}
	}
}

func BenchmarkCompletionRevision(b *testing.B) {
	for _, size := range []int{64 << 10, 4 << 20} {
		b.Run(fmt.Sprintf("output_%dKiB", size>>10), func(b *testing.B) {
			_, _, rawItem := benchmarkThreadWithOutput(b, strings.Repeat("x", size))
			turn := &Turn{ID: "turn", Status: "completed", Items: []json.RawMessage{rawItem}}
			b.SetBytes(int64(len(rawItem)))
			b.ReportAllocs()
			for b.Loop() {
				if completionRevision(turn) == "" {
					b.Fatal("missing revision")
				}
			}
		})
	}
}
