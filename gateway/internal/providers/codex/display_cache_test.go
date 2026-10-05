package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

func cacheTestProvider(t *testing.T, th *Thread) (*Provider, *fakeServer) {
	t.Helper()
	raw, _ := json.Marshal(th)
	f := &fakeServer{t: t, thread: raw}
	pt := &pipeTransport{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
	go f.serve(pt, func(v any) { b, _ := json.Marshal(v); pt.in <- b })
	p := New("unused", "/nonexistent.sock", &fakeTerm{}, deadletter.Nop{})
	p.reader = newRPCClient(pt, nil)
	t.Cleanup(p.reader.close)
	return p, f
}

func setCacheThread(f *fakeServer, th *Thread) {
	raw, _ := json.Marshal(th)
	f.mu.Lock()
	f.thread = raw
	f.mu.Unlock()
}

func historyCalls(f *fakeServer) int {
	n := 0
	for _, call := range f.callList() {
		if strings.Contains(call, `"includeTurns":true`) {
			n++
		}
	}
	return n
}

func Test履歴不変の詳細取得は再取得せず追記と同サイズの書換えで更新する(t *testing.T) {
	th, _ := loadThread(t, "normal.json")
	th.Path = filepath.Join(t.TempDir(), "rollout.jsonl")
	os.WriteFile(th.Path, []byte("initial\n"), 0600)
	p, f := cacheTestProvider(t, th)
	ctx := context.Background()
	_, first, err := p.Conversation(ctx, th.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, again, err := p.Conversation(ctx, th.ID, nil)
	if err != nil || !reflect.DeepEqual(first, again) || historyCalls(f) != 1 {
		t.Fatalf("unchanged: err=%v, calls=%v", err, f.callList())
	}
	if e := p.historyEntries[th.ID]; e == nil || e.messages == nil {
		t.Fatal("converted messages not retained")
	}
	th.Turns = append(th.Turns, Turn{ID: "new-turn", Status: "inProgress", Items: []json.RawMessage{json.RawMessage(`{"type":"agentMessage","id":"new-answer","text":"新しい結果"}`)}})
	setCacheThread(f, th)
	os.WriteFile(th.Path, []byte("appended\n"), 0600)
	_, got, err := p.Conversation(ctx, th.ID, nil)
	if err != nil || got[len(got)-1].ID != "new-answer" || historyCalls(f) != 2 {
		t.Fatalf("append: err=%v, calls=%v", err, f.callList())
	}
	os.WriteFile(th.Path, []byte("rewritte\n"), 0600)
	os.Chtimes(th.Path, time.Now().Add(time.Second), time.Now().Add(time.Second))
	if _, _, err := p.Conversation(ctx, th.ID, nil); err != nil || historyCalls(f) != 3 {
		t.Fatalf("rewrite: err=%v, calls=%v", err, f.callList())
	}
	if first[len(first)-1].ID == "new-answer" {
		t.Fatal("old snapshot mutated")
	}
}

func Test履歴不変でも承認の解消とモード変更を即座に反映する(t *testing.T) {
	th, _ := loadThread(t, "normal.json")
	th.Path = filepath.Join(t.TempDir(), "rollout.jsonl")
	os.WriteFile(th.Path, []byte("stable\n"), 0600)
	p, f := cacheTestProvider(t, th)
	d := newDaemonConn(deadletter.Nop{})
	d.c = p.reader
	p.daemon = d
	d.Request(json.RawMessage(`1`), "item/commandExecution/requestApproval", json.RawMessage(`{"threadId":"`+th.ID+`","turnId":"t","itemId":"i","command":"go test"}`))
	ctx := context.Background()
	sum, msgs, err := p.Conversation(ctx, th.ID, nil)
	if err != nil || sum.Pending != model.InteractionApproval || msgs[len(msgs)-1].Blocks[0].Interaction == nil {
		t.Fatalf("approval: %+v, %v", sum, err)
	}
	d.Notification("serverRequest/resolved", json.RawMessage(`{"threadId":"`+th.ID+`","requestId":1}`))
	d.Notification("thread/settings/updated", json.RawMessage(`{"threadId":"`+th.ID+`","threadSettings":{"collaborationMode":{"mode":"plan"}}}`))
	sum, msgs, err = p.Conversation(ctx, th.ID, nil)
	if err != nil || sum.Pending != "" || sum.Mode != "Plan" || msgs[len(msgs)-1].Blocks[0].Interaction != nil || historyCalls(f) != 1 {
		t.Fatalf("resolved: %+v, err=%v, calls=%v", sum, err, f.callList())
	}
	d.Notification("item/agentMessage/delta", json.RawMessage(`{"threadId":"`+th.ID+`","delta":"new"}`))
	if _, _, err = p.Conversation(ctx, th.ID, nil); err != nil || historyCalls(f) != 2 {
		t.Fatalf("event: %v %v", err, f.callList())
	}
	p.actionVersion.Add(1)
	if _, _, err = p.Conversation(ctx, th.ID, nil); err != nil || historyCalls(f) != 3 {
		t.Fatalf("action: %v %v", err, f.callList())
	}
}

func Test履歴キャッシュはファイルの置換と接続の切替えと期限切れで再取得する(t *testing.T) {
	th, _ := loadThread(t, "normal.json")
	th.Path = filepath.Join(t.TempDir(), "rollout.jsonl")
	os.WriteFile(th.Path, []byte("stable\n"), 0600)
	p, f := cacheTestProvider(t, th)
	ctx := context.Background()
	if _, err := p.displayThread(ctx, th.ID); err != nil {
		t.Fatal(err)
	}
	tmp := th.Path + ".new"
	os.WriteFile(tmp, []byte("stable\n"), 0600)
	os.Rename(tmp, th.Path)
	if _, err := p.displayThread(ctx, th.ID); err != nil || historyCalls(f) != 2 {
		t.Fatal("replacement did not invalidate", err)
	}
	p.historyEntries[th.ID].at = time.Now().Add(-historyLifetime)
	if _, err := p.displayThread(ctx, th.ID); err != nil || historyCalls(f) != 3 {
		t.Fatal("expiry did not invalidate", err)
	}
	other, server := cacheTestProvider(t, th)
	p.reader = other.reader
	if _, err := p.displayThread(ctx, th.ID); err != nil || historyCalls(server) != 1 {
		t.Fatal("connection did not invalidate", err)
	}
	if err := os.Remove(th.Path); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := p.displayThread(ctx, th.ID); err != nil {
			t.Fatal(err)
		}
	}
	if historyCalls(server) != 3 {
		t.Fatal("missing file reused history")
	}
}

func Test詳細の変換結果は現在の作業ディレクトリに依存する(t *testing.T) {
	th := &Thread{ID: "thread-000001", Cwd: t.TempDir(), Path: filepath.Join(t.TempDir(), "rollout"), Turns: []Turn{{ID: "turn", Items: []json.RawMessage{json.RawMessage(`{"type":"agentMessage","id":"a","text":"README.md を確認"}`)}}}}
	os.WriteFile(th.Path, []byte("stable\n"), 0600)
	a, b := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(a, "README.md"), []byte("a"), 0600)
	os.WriteFile(filepath.Join(b, "README.md"), []byte("longer"), 0600)
	p, f := cacheTestProvider(t, th)
	first, err := p.Messages(context.Background(), th.ID, &providers.Live{Cwd: a})
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Messages(context.Background(), th.ID, &providers.Live{Cwd: b})
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Blocks[1].Size != 1 || second[0].Blocks[1].Size != 6 || historyCalls(f) != 1 {
		t.Fatal("root conversion reused incorrectly")
	}
}

func Test取得中に追記やイベントが来た履歴を最新として保存しない(t *testing.T) {
	for _, change := range []string{"append", "event"} {
		t.Run(change, func(t *testing.T) {
			th, _ := loadThread(t, "normal.json")
			th.Path = filepath.Join(t.TempDir(), "rollout")
			os.WriteFile(th.Path, []byte("stable\n"), 0600)
			pt := &pipeTransport{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
			p := New("unused", "/nonexistent.sock", &fakeTerm{}, deadletter.Nop{})
			p.reader = newRPCClient(pt, nil)
			t.Cleanup(p.reader.close)
			d := newDaemonConn(deadletter.Nop{})
			d.c = p.reader
			p.daemon = d
			meta := *th
			meta.Turns = nil
			p.metadataEntries = map[string]*metadataEntry{th.ID: {thread: &meta, version: p.displayVersion(th.ID, false), at: time.Now()}}
			done := make(chan error, 1)
			go func() { _, err := p.displayThread(context.Background(), th.ID); done <- err }()
			var request wireMessage
			select {
			case raw := <-pt.out:
				json.Unmarshal(raw, &request)
			case <-time.After(2 * time.Second):
				t.Fatal("no read")
			}
			if change == "append" {
				appendRollout(t, th.Path, "changed\n")
			} else {
				d.Notification("item/agentMessage/delta", json.RawMessage(`{"threadId":"`+th.ID+`","delta":"new"}`))
			}
			response, _ := json.Marshal(map[string]any{"id": request.ID, "result": map[string]any{"thread": th}})
			pt.in <- response
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if p.historyEntries[th.ID] != nil {
				t.Fatal("raced snapshot was cached")
			}
		})
	}
}
