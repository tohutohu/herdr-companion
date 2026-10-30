package codex

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// directServer is a fake gateway-owned app-server: it answers the calls a
// direct session makes and records them.
type directServer struct {
	pt    *pipeTransport
	mu    sync.Mutex
	calls []directCall
	turn  int
}

type directCall struct {
	Method string
	Params map[string]any
}

const directThreadID = "019a0000-0000-7000-8000-000000000001"

func (s *directServer) send(v any) {
	b, _ := json.Marshal(v)
	s.pt.in <- b
}

func (s *directServer) serve() {
	for {
		var b []byte
		select {
		case b = <-s.pt.out:
		case <-s.pt.closed:
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		json.Unmarshal(b, &m)
		if m.Method == "" {
			continue // a response to a server request
		}
		s.mu.Lock()
		s.calls = append(s.calls, directCall{m.Method, m.Params})
		s.turn++
		turn := s.turn
		s.mu.Unlock()
		var result any = map[string]any{}
		switch m.Method {
		case "thread/start", "thread/resume":
			result = map[string]any{
				"thread": map[string]any{"id": directThreadID, "cwd": "/work", "status": map[string]any{"type": "idle"}},
				"model":  "gpt-6-luna", "reasoningEffort": "low", "cwd": "/work",
				"approvalPolicy": "on-request", "sandbox": map[string]any{"type": "workspaceWrite"},
			}
		case "turn/start":
			result = map[string]any{"turn": map[string]any{"id": "turn-" + string(rune('0'+turn)), "status": "inProgress"}}
		}
		if len(m.ID) > 0 {
			s.send(map[string]any{"id": json.RawMessage(m.ID), "result": result})
		}
	}
}

func (s *directServer) last(method string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.calls) - 1; i >= 0; i-- {
		if s.calls[i].Method == method {
			return s.calls[i].Params
		}
	}
	return nil
}

func newDirectProvider(t *testing.T) (*Provider, *directServer, *[]string) {
	t.Helper()
	pt := &pipeTransport{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
	srv := &directServer{pt: pt}
	go srv.serve()
	p := New("codex-not-used", "/nonexistent.sock", &fakeTerm{}, deadletter.Nop{})
	d := newDaemonConn(deadletter.Nop{})
	d.events = p.directEvent
	d.closed = func(err error) { p.ownedClosed(d, err) }
	d.c = newRPCClient(pt, d)
	p.owned = d
	t.Cleanup(func() { pt.close() })
	var mu sync.Mutex
	var changed []string
	p.SetDirectListener(func(id string) {
		mu.Lock()
		changed = append(changed, id)
		mu.Unlock()
	})
	return p, srv, &changed
}

func directInfo(p *Provider) providers.DirectInfo {
	for _, in := range p.DirectSessions() {
		if in.NativeID == directThreadID {
			return in
		}
	}
	return providers.DirectInfo{}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func Test直接管理のスレッドは選んだモデルと推論強度を次のターンに渡す(t *testing.T) {
	p, srv, _ := newDirectProvider(t)
	ctx := context.Background()
	id, err := p.DirectStart(ctx, providers.DirectStart{Cwd: "/work", Model: "gpt-6-luna", Effort: "low", Prompt: model.Input{Text: "hi"}})
	if err != nil || id != directThreadID {
		t.Fatalf("start = %q, %v", id, err)
	}
	if start := srv.last("thread/start"); start["cwd"] != "/work" || start["model"] != "gpt-6-luna" {
		t.Errorf("thread/start = %v", start)
	}
	if turn := srv.last("turn/start"); turn["model"] != "gpt-6-luna" || turn["effort"] != "low" {
		t.Errorf("turn/start = %v", turn)
	}
	in := directInfo(p)
	if !in.Alive || !in.Running {
		t.Fatalf("info = %+v", in)
	}
	srv.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": id, "turn": map[string]any{"id": "turn-2", "status": "completed"}}})
	waitFor(t, "turn end", func() bool { return directInfo(p).Turns == 1 })

	// 実行中のターンには触れず、次のターンから適用する
	if err := p.DirectSetModel(ctx, id, "gpt-5.6-luna"); err != nil {
		t.Fatal(err)
	}
	if err := p.DirectSetEffort(ctx, id, "medium"); err != nil {
		t.Fatal(err)
	}
	if in := directInfo(p); in.RequestedModel != "gpt-5.6-luna" || in.RequestedEffort != "medium" || in.Model != "gpt-6-luna" {
		t.Errorf("pending = %+v", in)
	}
	if err := p.Send(ctx, id, &providers.Live{Direct: true}, model.Input{Text: "again"}); err != nil {
		t.Fatal(err)
	}
	if turn := srv.last("turn/start"); turn["model"] != "gpt-5.6-luna" || turn["effort"] != "medium" {
		t.Errorf("second turn/start = %v", turn)
	}
	if in := directInfo(p); in.RequestedModel != "" || in.Model != "gpt-5.6-luna" || in.Effort != "medium" {
		t.Errorf("applied = %+v", in)
	}
}

func Test実行中のターンへの追加指示はsteerで送る(t *testing.T) {
	p, srv, _ := newDirectProvider(t)
	ctx := context.Background()
	id, _ := p.DirectStart(ctx, providers.DirectStart{Cwd: "/work", Prompt: model.Input{Text: "long task"}})
	if err := p.Send(ctx, id, &providers.Live{Direct: true}, model.Input{Text: "also this"}); err != nil {
		t.Fatal(err)
	}
	steer := srv.last("turn/steer")
	if steer == nil || steer["expectedTurnId"] == "" {
		t.Fatalf("turn/steer = %v", steer)
	}
	if err := p.DirectInterrupt(ctx, id); err != nil {
		t.Fatal(err)
	}
	if in := srv.last("turn/interrupt"); in["threadId"] != id || in["turnId"] != steer["expectedTurnId"] {
		t.Errorf("turn/interrupt = %v", in)
	}
}

func TestPlanモードは次のターンのcollaborationModeとして渡す(t *testing.T) {
	p, srv, _ := newDirectProvider(t)
	ctx := context.Background()
	id, _ := p.DirectStart(ctx, providers.DirectStart{Cwd: "/work"})
	if err := p.DirectSetMode(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	if in := directInfo(p); in.Mode != planLabel {
		t.Errorf("mode = %q", in.Mode)
	}
	p.Send(ctx, id, &providers.Live{Direct: true}, model.Input{Text: "plan it"})
	cm, _ := srv.last("turn/start")["collaborationMode"].(map[string]any)
	settings, _ := cm["settings"].(map[string]any)
	if cm["mode"] != "plan" || settings["model"] != "gpt-6-luna" {
		t.Errorf("collaborationMode = %v", cm)
	}
}

func Test承認要求で待機状態になり回答すると解消する(t *testing.T) {
	p, srv, changed := newDirectProvider(t)
	ctx := context.Background()
	id, _ := p.DirectStart(ctx, providers.DirectStart{Cwd: "/work", Prompt: model.Input{Text: "build"}})
	srv.send(map[string]any{"id": 7, "method": "item/commandExecution/requestApproval",
		"params": map[string]any{"threadId": id, "turnId": "t", "itemId": "i", "command": "make"}})
	waitFor(t, "approval", func() bool { return directInfo(p).Waiting })
	live := &providers.Live{Direct: true}
	// The gateway's own connection holds the request for the thread.
	if d := p.connFor(id, live); d != p.currentOwned() {
		t.Fatal("direct thread not read from the gateway's app-server")
	}
	d := p.currentOwned()
	pending := d.interactions(id, deadletter.Nop{})
	if len(pending) != 1 || pending[0].Blocks[0].Interaction.Type != model.InteractionApproval {
		t.Fatalf("pending = %+v", pending)
	}
	if err := p.Respond(ctx, id, live, model.InteractionResponse{InteractionID: requestIDPrefix + "7", Decision: model.DecisionApprove}); err != nil {
		t.Fatal(err)
	}
	if directInfo(p).Waiting {
		t.Error("still waiting after the answer")
	}
	if len(*changed) == 0 {
		t.Error("listener never called")
	}
}

func Testアプリサーバーが終了すると実行中のターンは失敗として扱う(t *testing.T) {
	p, srv, _ := newDirectProvider(t)
	ctx := context.Background()
	id, _ := p.DirectStart(ctx, providers.DirectStart{Cwd: "/work", Prompt: model.Input{Text: "build"}})
	srv.pt.close()
	waitFor(t, "exit", func() bool { return !directInfo(p).Alive })
	in := directInfo(p)
	if !in.LastTurnFailed || in.Running || in.Error == "" {
		t.Errorf("info = %+v", in)
	}
	if err := p.Send(ctx, id, &providers.Live{Direct: true}, model.Input{Text: "x"}); err == nil {
		t.Error("send to a dead app-server succeeded")
	}
}

func Test共有daemonのスレッドは手放されるまで待ってから引き継ぎ再購読しない(t *testing.T) {
	pt := &pipeTransport{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
	t.Cleanup(func() { pt.close() })
	var mu sync.Mutex
	var calls []string
	lists := 0
	go func() {
		for {
			var b []byte
			select {
			case b = <-pt.out:
			case <-pt.closed:
				return
			}
			var m struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			json.Unmarshal(b, &m)
			mu.Lock()
			calls = append(calls, m.Method)
			var result any = map[string]any{}
			if m.Method == "thread/loaded/list" {
				lists++
				data := []string{}
				if lists < 3 { // the daemon unloads the thread a moment later
					data = []string{directThreadID}
				}
				result = map[string]any{"data": data}
			}
			mu.Unlock()
			b, _ = json.Marshal(map[string]any{"id": m.ID, "result": result})
			pt.in <- b
		}
	}()
	p := New("codex-not-used", "/nonexistent.sock", &fakeTerm{}, deadletter.Nop{})
	d := newDaemonConn(deadletter.Nop{})
	d.c = newRPCClient(pt, d)
	d.subscribed[directThreadID] = true
	p.daemon = d
	if err := p.ReleaseShared(context.Background(), directThreadID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	if got[0] != "thread/unsubscribe" || lists != 3 {
		t.Errorf("calls = %v (lists %d)", got, lists)
	}
	// 手放す途中の同期でも再購読しない
	d.mu.Lock()
	d.released[directThreadID] = true
	d.mu.Unlock()
	mu.Lock()
	lists = 0
	mu.Unlock()
	if err := d.sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, c := range calls[len(got):] {
		if c == "thread/resume" {
			t.Errorf("resubscribed: %v", calls)
		}
	}
}
