package dsh

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// A real subprocess exercises bidirectional stdio and shutdown, including
// server requests arriving before the session/query receipt.
func TestDSHプロトコル子プロセス(t *testing.T) {
	if os.Getenv("HERDR_DSH_TEST_HELPER") != "1" {
		return
	}
	enc := json.NewEncoder(os.Stdout)
	emit := func(v any) {
		if enc.Encode(v) != nil {
			os.Exit(2)
		}
	}
	var id, pending string
	seq := int64(0)
	event := func(kind string, data any) {
		seq++
		emit(map[string]any{"jsonrpc": "2.0", "method": "session.event", "params": map[string]any{"sessionId": id, "event": map[string]any{"type": kind, "seq": seq, "time": time.Now().UnixMilli(), "data": data}}})
	}
	status := func(value string) {
		emit(map[string]any{"jsonrpc": "2.0", "method": "session.status", "params": map[string]string{"sessionId": id, "status": value}})
	}
	finish := func() {
		event("assistant/message", map[string]any{"message": map[string]any{"content": []any{map[string]string{"type": "text", "text": "done"}}}})
		event("turn/end", map[string]any{"reason": map[string]string{"kind": "completed"}})
		status("idle")
	}
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var f frame
		if json.Unmarshal(scan.Bytes(), &f) != nil {
			os.Exit(3)
		}
		if f.Method == "" {
			if pending == "approval" && !strings.Contains(string(f.Result), "allowed-once") {
				os.Exit(4)
			}
			if pending == "question" && !strings.Contains(string(f.Result), `"selected":["Yes"]`) {
				os.Exit(5)
			}
			pending = ""
			finish()
			continue
		}
		var params struct {
			SessionID     string         `json:"sessionId"`
			ContentBlocks []contentBlock `json:"contentBlocks"`
		}
		json.Unmarshal(f.Params, &params)
		if params.SessionID != "" {
			id = params.SessionID
		}
		result := any(map[string]any{})
		switch f.Method {
		case "initialize":
			if !strings.Contains(string(f.Params), `"approvalRequests":true`) || !strings.Contains(string(f.Params), `"userQuestions":true`) {
				os.Exit(6)
			}
			result = map[string]any{"serverInfo": map[string]string{"name": "deepseek-harness-sdk-runtime", "version": "0.0.1"}}
		case "session/working-directory/get":
			os.WriteFile(filepath.Join(os.Getenv("DSH_HOME"), id), []byte("saved"), 0600)
		case "session/resume":
			if _, err := os.Stat(filepath.Join(os.Getenv("DSH_HOME"), id)); err != nil {
				emit(map[string]any{"id": f.ID, "error": rpcError{Code: -32603, Message: "missing session"}})
				continue
			}
			seq = 100
		case "session/query":
			text := params.ContentBlocks[0].Text
			if strings.HasPrefix(text, "/permission ") {
				result = map[string]any{"kind": "command", "execution": map[string]any{"result": map[string]string{"kind": "text", "text": "ok"}}}
				break
			}
			status("running")
			event("turn/start", map[string]int{"turn": 1})
			event("user/message", map[string]any{"content": params.ContentBlocks, "source": map[string]string{"kind": "user"}})
			switch text {
			case "approval":
				pending = "approval"
				emit(map[string]any{"jsonrpc": "2.0", "id": 900, "method": "approval/request", "params": map[string]string{"sessionId": id, "toolName": "bash", "reason": "Run test command", "callId": "call-1"}})
			case "question":
				pending = "question"
				emit(map[string]any{"jsonrpc": "2.0", "id": "question-1", "method": "user-question/ask", "params": map[string]any{"sessionId": id, "requestId": "q-1", "questions": []any{map[string]any{"id": "answer", "question": "Continue?", "detail": "Review this plan", "intent": map[string]string{"kind": "plan-review", "approve": "Yes"}, "options": []any{map[string]string{"label": "Yes"}, map[string]string{"label": "No"}}}}}})
			case "cancel-question":
				emit(map[string]any{"jsonrpc": "2.0", "method": "user-question.cancelled", "params": map[string]string{"sessionId": id, "requestId": "q-1"}})
			case "crash":
				os.Exit(9)
			case "hold":
			default:
				finish()
			}
			result = map[string]string{"kind": "prompt", "messageId": "message-1"}
		default:
			emit(map[string]any{"id": f.ID, "error": rpcError{Code: -32601, Message: "unknown method"}})
			continue
		}
		emit(map[string]any{"jsonrpc": "2.0", "id": f.ID, "result": result})
	}
	os.Exit(0)
}

func testProvider(t *testing.T) (*Provider, Config, string) {
	t.Helper()
	t.Setenv("HERDR_DSH_TEST_HELPER", "1")
	dir := t.TempDir()
	binary := filepath.Join(dir, "dsh")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(exe, "'", "'\\''") + "' -test.run '^TestDSHプロトコル子プロセス$' -- \"$@\"\n"
	if err = os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Binary: binary}
	state := filepath.Join(dir, "state")
	p, err := New(cfg, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p, cfg, state
}
func startTest(t *testing.T, p *Provider, text string) string {
	t.Helper()
	id, err := p.DirectStart(context.Background(), providers.DirectStart{Cwd: t.TempDir(), Prompt: model.Input{Text: text}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func waitInfo(t *testing.T, p *Provider, id string, check func(providers.DirectInfo) bool) providers.DirectInfo {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		for _, in := range p.DirectSessions() {
			if in.NativeID == id && check(in) {
				return in
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state timeout: %+v", p.DirectSessions())
	return providers.DirectInfo{}
}
func pendingInteraction(t *testing.T, p *Provider, id string) model.Interaction {
	t.Helper()
	waitInfo(t, p, id, func(i providers.DirectInfo) bool { return i.Waiting })
	ms, err := p.Messages(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		for _, b := range m.Blocks {
			if b.Interaction != nil && b.Interaction.State == model.InteractionPending {
				return *b.Interaction
			}
		}
	}
	t.Fatal("missing interaction")
	return model.Interaction{}
}

func TestDSH会話と再起動後の履歴再開(t *testing.T) {
	p, cfg, dir := testProvider(t)
	id := startTest(t, p, "hello")
	waitInfo(t, p, id, func(i providers.DirectInfo) bool { return !i.Running && i.Turns == 1 })
	s, _ := p.Summary(context.Background(), id, nil)
	if s.Title != "hello" || s.LastMessage != "done" {
		t.Fatalf("summary: %+v", s)
	}
	p.Close()
	p2, err := New(cfg, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	ms, err := p2.Messages(context.Background(), id, nil)
	if err != nil || len(ms) != 2 {
		t.Fatalf("history %d: %v", len(ms), err)
	}
	got, err := p2.DirectStart(context.Background(), providers.DirectStart{Resume: id, Prompt: model.Input{Text: "again"}})
	if err != nil || got != id {
		t.Fatalf("resume %s: %v", got, err)
	}
	waitInfo(t, p2, id, func(i providers.DirectInfo) bool { return !i.Running && i.Turns == 2 })
	ms, _ = p2.Messages(context.Background(), id, nil)
	if len(ms) != 4 {
		t.Fatalf("history overwritten: %+v", ms)
	}
}

func TestDSH承認は一回限りで再開前の回答を拒否(t *testing.T) {
	p, _, _ := testProvider(t)
	id := startTest(t, p, "approval")
	ia := pendingInteraction(t, p, id)
	if err := p.Respond(context.Background(), id, nil, model.InteractionResponse{InteractionID: ia.ID, Decision: model.DecisionApproveSession}); err == nil {
		t.Fatal("session-wide grant accepted")
	}
	if err := p.DirectInterrupt(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	_, err := p.DirectStart(context.Background(), providers.DirectStart{Resume: id, Prompt: model.Input{Text: "approval"}})
	if err != nil {
		t.Fatal(err)
	}
	newIA := pendingInteraction(t, p, id)
	if ia.ID == newIA.ID {
		t.Fatal("reused interaction identity")
	}
	if err = p.Respond(context.Background(), id, nil, model.InteractionResponse{InteractionID: ia.ID, Decision: model.DecisionApprove}); !errors.Is(err, providers.ErrInteractionGone) {
		t.Fatal(err)
	}
	answer := model.InteractionResponse{InteractionID: newIA.ID, Decision: model.DecisionApprove}
	if err = p.Respond(context.Background(), id, nil, answer); err != nil {
		t.Fatal(err)
	}
	if err = p.Respond(context.Background(), id, nil, answer); !errors.Is(err, providers.ErrInteractionGone) {
		t.Fatal("duplicate", err)
	}
	waitInfo(t, p, id, func(i providers.DirectInfo) bool { return !i.Running && i.Turns == 1 })
}

func TestDSH質問とプラン回答(t *testing.T) {
	p, _, _ := testProvider(t)
	id := startTest(t, p, "question")
	ia := pendingInteraction(t, p, id)
	if ia.Kind != model.KindPlan || !strings.Contains(ia.Detail, "Review this plan") {
		t.Fatalf("plan: %+v", ia)
	}
	invalid := model.InteractionResponse{InteractionID: ia.ID, Answers: map[string]model.Answer{"answer": {Selected: []string{"unknown"}}}}
	if p.Respond(context.Background(), id, nil, invalid) == nil {
		t.Fatal("invalid answer accepted")
	}
	invalid.Answers["answer"] = model.Answer{Selected: []string{"Yes"}}
	if err := p.Respond(context.Background(), id, nil, invalid); err != nil {
		t.Fatal(err)
	}
	waitInfo(t, p, id, func(i providers.DirectInfo) bool { return !i.Running && i.Turns == 1 })
}

func TestDSH中断は他のセッションに影響しない(t *testing.T) {
	p, _, _ := testProvider(t)
	a := startTest(t, p, "hold")
	b := startTest(t, p, "hold")
	if err := p.DirectInterrupt(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	waitInfo(t, p, a, func(i providers.DirectInfo) bool { return !i.Alive })
	waitInfo(t, p, b, func(i providers.DirectInfo) bool { return i.Alive && i.Running })
}

func TestDSHモデル変更は次のターンから(t *testing.T) {
	p, _, _ := testProvider(t)
	id := startTest(t, p, "hello")
	waitInfo(t, p, id, func(i providers.DirectInfo) bool { return !i.Running && i.Turns == 1 })
	if err := p.DirectSetModel(context.Background(), id, "deepseek-v4-pro"); err != nil {
		t.Fatal(err)
	}
	waitInfo(t, p, id, func(i providers.DirectInfo) bool {
		return i.Model == DefaultModel && i.RequestedModel == "deepseek-v4-pro"
	})
	if err := p.Send(context.Background(), id, nil, model.Input{Text: "again"}); err != nil {
		t.Fatal(err)
	}
	waitInfo(t, p, id, func(i providers.DirectInfo) bool {
		return i.Model == "deepseek-v4-pro" && i.RequestedModel == "" && i.Turns == 2
	})
}

func TestDSHプロセス異常終了を失敗として表示(t *testing.T) {
	p, _, _ := testProvider(t)
	id, err := p.DirectStart(context.Background(), providers.DirectStart{Cwd: t.TempDir(), Prompt: model.Input{Text: "crash"}})
	if err == nil || id == "" {
		t.Fatalf("lost receipt %q %v", id, err)
	}
	waitInfo(t, p, id, func(i providers.DirectInfo) bool { return !i.Alive && i.LastTurnFailed })
}

// Opt-in real SDK smoke: never sends a model prompt or requires an API key.
func TestDSH実ランタイムの起動と再開(t *testing.T) {
	binary := os.Getenv("HERDR_DSH_E2E_BINARY")
	if binary == "" {
		t.Skip("set HERDR_DSH_E2E_BINARY")
	}
	p, err := New(Config{Binary: binary}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	id := startTest(t, p, "")
	if err = p.DirectSetMode(context.Background(), id, workspaceMode); err != nil {
		t.Fatal(err)
	}
	if err = p.DirectInterrupt(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	got, err := p.DirectStart(context.Background(), providers.DirectStart{Resume: id})
	if err != nil || got != id {
		t.Fatal(fmt.Sprintf("resume %s: %v", got, err))
	}
}

func TestDSH実ランタイムと模擬モデルの会話(t *testing.T) {
	binary := os.Getenv("HERDR_DSH_E2E_BINARY")
	if binary == "" {
		t.Skip("set HERDR_DSH_E2E_BINARY")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.Error(w, "unexpected path", 404)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, item := range []struct{ name, data string }{
			{"message_start", `{"type":"message_start","message":{"id":"msg-test","type":"message","role":"assistant","model":"deepseek-flash","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":0}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"DSH integration works"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":4}}`},
			{"message_stop", `{"type":"message_stop"}`},
		} {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", item.name, item.data)
		}
	}))
	defer server.Close()
	t.Setenv("DEEPSEEK_BASE_URL", server.URL)
	p, err := New(Config{Binary: binary, APIKey: "test-local-only"}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	id := startTest(t, p, "hello from integration test")
	waitInfo(t, p, id, func(i providers.DirectInfo) bool { return !i.Running && i.Turns > 0 })
	s, _ := p.Summary(context.Background(), id, nil)
	if s.LastTurnFailed || s.LastMessage != "DSH integration works" || s.Title != "hello from integration test" {
		t.Fatalf("real SDK summary: %+v", s)
	}
	ms, err := p.Messages(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || messageText(ms[1]) != "DSH integration works" {
		t.Fatalf("real SDK messages: %+v", ms)
	}
}

func TestDSH実ランタイムの質問を回答して続行(t *testing.T) {
	binary := os.Getenv("HERDR_DSH_E2E_BINARY")
	if binary == "" {
		t.Skip("set HERDR_DSH_E2E_BINARY")
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.Error(w, "unexpected path", 404)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(kind string, v map[string]any) {
			v["type"] = kind
			b, _ := json.Marshal(v)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, b)
		}
		emit("message_start", map[string]any{"message": map[string]any{"id": "msg-test", "type": "message", "role": "assistant", "model": DefaultModel, "content": []any{}, "usage": map[string]int{"input_tokens": 10, "output_tokens": 0}}})
		stop := "end_turn"
		if calls.Add(1) == 1 {
			stop = "tool_use"
			emit("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "tool_use", "id": "tool-question", "name": "ask_user_question", "input": map[string]any{}}})
			emit("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": `{"questions":[{"id":"choice","question":"Continue?","options":[{"label":"Yes"},{"label":"No"}]}]}`}})
		} else {
			emit("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
			emit("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": "Question answered"}})
		}
		emit("content_block_stop", map[string]any{"index": 0})
		emit("message_delta", map[string]any{"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 10}})
		emit("message_stop", map[string]any{})
	}))
	defer server.Close()
	t.Setenv("DEEPSEEK_BASE_URL", server.URL)
	p, err := New(Config{Binary: binary, APIKey: "test-local-only"}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	id := startTest(t, p, "Ask me before continuing")
	ia := pendingInteraction(t, p, id)
	if err = p.Respond(context.Background(), id, nil, model.InteractionResponse{InteractionID: ia.ID, Answers: map[string]model.Answer{"choice": {Selected: []string{"Yes"}}}}); err != nil {
		t.Fatal(err)
	}
	waitInfo(t, p, id, func(i providers.DirectInfo) bool { return !i.Running && i.Turns > 0 })
	s, _ := p.Summary(context.Background(), id, nil)
	if s.LastTurnFailed || s.LastMessage != "Question answered" || calls.Load() != 2 {
		t.Fatalf("question continuation: %+v, calls=%d", s, calls.Load())
	}
}
