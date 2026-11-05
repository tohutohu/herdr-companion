package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
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

func Test確定済みメッセージはターン完了前に翻訳可能で末尾のストリームだけ待つ(t *testing.T) {
	th := &Thread{ID: "thread-123", Turns: []Turn{{ID: "turn", Status: "inProgress", Items: []json.RawMessage{
		json.RawMessage(`{"type":"agentMessage","id":"first","text":"First message."}`),
		json.RawMessage(`{"type":"agentMessage","id":"tail","text":"Second message."}`),
	}}}}
	p, _ := cacheTestProvider(t, th)
	d := newServerConn(deadletter.Nop{})
	d.c = p.owned.c
	p.owned = d
	live := &providers.Live{}
	before := p.messagesFromThread(context.Background(), th, live)
	if before[0].Streaming || !before[1].Streaming {
		t.Fatal(before)
	}
	d.Notification("item/completed", json.RawMessage(`{"threadId":"thread-123","item":{"id":"tail","type":"agentMessage"}}`))
	after := p.messagesFromThread(context.Background(), th, live)
	if after[1].Streaming || !before[1].Streaming {
		t.Fatal("completion not applied or prior snapshot changed")
	}
	d.Notification("turn/started", json.RawMessage(`{"threadId":"thread-123","turn":{"id":"next","status":"inProgress"}}`))
	if !p.messagesFromThread(context.Background(), th, live)[1].Streaming {
		t.Fatal("completion leaked to next turn")
	}
	th.Turns[0].Status = "completed"
	if ConvertThread(th, convertOptions{})[1].Streaming {
		t.Fatal("completed turn still marked streaming")
	}
}
