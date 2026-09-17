package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

var update = flag.Bool("update", false, "update golden files")

const testdata = "../../../testdata/codex"

func loadThread(t *testing.T, name string) (*Thread, string) {
	t.Helper()
	ws, _ := filepath.Abs(filepath.Join(testdata, "workspace"))
	raw, err := os.ReadFile(filepath.Join(testdata, name))
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte("/work/app"), []byte(ws))
	var r struct {
		Thread Thread `json:"thread"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return &r.Thread, ws
}

func assertGolden(t *testing.T, name string, v any) {
	t.Helper()
	got, _ := json.MarshalIndent(v, "", "  ")
	path := filepath.Join(testdata, name)
	if *update {
		os.WriteFile(path, append(got, '\n'), 0o644)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s missing (run go test -update): %v", name, err)
	}
	if string(bytes.TrimSpace(want)) != string(bytes.TrimSpace(got)) {
		t.Errorf("%s mismatch\n--- got ---\n%s", name, got)
	}
}

func TestCodexの実スレッドを共通メッセージに変換できる(t *testing.T) {
	th, ws := loadThread(t, "normal.json")
	rec := &deadletter.Recorder{}
	msgs := ConvertThread(th, convertOptions{SessionID: "codex:test", Root: ws, Sink: rec})
	assertGolden(t, "normal.golden.json", msgs)
	if len(rec.Entries) != 0 {
		t.Errorf("dead letters: %+v", rec.Entries)
	}
}

func TestCodexの多様なitemと失敗ターンと未知itemを変換できる(t *testing.T) {
	th, ws := loadThread(t, "rich.json")
	rec := &deadletter.Recorder{}
	msgs := ConvertThread(th, convertOptions{SessionID: "codex:test", Root: ws, Sink: rec})
	assertGolden(t, "rich.golden.json", msgs)

	if len(rec.Entries) != 1 || rec.Entries[0].Kind != deadletter.UnknownContent || !strings.Contains(string(rec.Entries[0].Raw), "hologramCall") {
		t.Errorf("dead letters = %+v", rec.Entries)
	}
	last := msgs[len(msgs)-1]
	if last.Role != model.RoleSystem || last.Blocks[0].Text != "Turn failed: model requires a newer version" {
		t.Errorf("failed turn message = %+v", last)
	}
	mime, data, ok := dataURLImage(th, "u1", 2)
	if !ok || mime != "image/png" || len(data) == 0 {
		t.Errorf("data url image = %q %v %v", mime, len(data), ok)
	}
	if _, _, ok := dataURLImage(th, "u1", 0); ok {
		t.Error("text input is not an image")
	}
}

func loadRequests(t *testing.T) []pendingRequest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(testdata, "request_user_input.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reqs []struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	json.Unmarshal(raw, &reqs)
	var out []pendingRequest
	for i, r := range reqs {
		out = append(out, pendingRequest{ID: r.ID, Method: r.Method, Params: r.Params, Received: time.Unix(int64(i), 0)})
	}
	return out
}

func Test保留中のサーバーリクエストをインタラクションに変換できる(t *testing.T) {
	rec := &deadletter.Recorder{}
	var got []*model.Interaction
	for _, r := range loadRequests(t) {
		if ignoredRequests[r.Method] {
			continue
		}
		got = append(got, interactionFor(r, rec))
	}
	assertGolden(t, "request_user_input.golden.json", got)

	kinds := map[deadletter.Kind]int{}
	for _, e := range rec.Entries {
		kinds[e.Kind]++
	}
	if kinds[deadletter.UnsupportedInteraction] != 2 {
		t.Errorf("dead letters = %+v", rec.Entries)
	}
}

func Test回答をapp_serverのレスポンス形式に変換できる(t *testing.T) {
	reqs := loadRequests(t)
	res, err := responseFor(reqs[0], model.InteractionResponse{Answers: map[string]model.Answer{
		"color": {Selected: []string{"Blue"}},
		"name":  {Text: " herdr "},
	}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	if string(b) != `{"answers":{"color":{"answers":["Blue"]},"name":{"answers":["herdr"]}}}` {
		t.Errorf("requestUserInput response = %s", b)
	}
	if _, err := responseFor(reqs[0], model.InteractionResponse{Answers: map[string]model.Answer{"color": {Selected: []string{"Red"}}}}); err == nil {
		t.Error("missing answer should fail")
	}

	for decision, want := range map[string]string{"approve": "accept", "approve_session": "acceptForSession", "deny": "decline"} {
		res, err := responseFor(reqs[1], model.InteractionResponse{Decision: decision})
		b, _ := json.Marshal(res)
		if err != nil || string(b) != `{"decision":"`+want+`"}` {
			t.Errorf("%s -> %s %v", decision, b, err)
		}
	}

	res, _ = responseFor(reqs[2], model.InteractionResponse{Decision: "approve"})
	b, _ = json.Marshal(res)
	if string(b) != `{"permissions":{"network":{"enabled":true}},"scope":"turn"}` {
		t.Errorf("permissions approve = %s", b)
	}
	res, _ = responseFor(reqs[2], model.InteractionResponse{Decision: "deny"})
	b, _ = json.Marshal(res)
	if string(b) != `{"permissions":{}}` {
		t.Errorf("permissions deny = %s", b)
	}

	if _, err := responseFor(reqs[3], model.InteractionResponse{Decision: "approve"}); !errors.Is(err, providers.ErrUnsupported) {
		t.Errorf("elicitation err = %v", err)
	}
	legacy := pendingRequest{ID: json.RawMessage("1"), Method: "execCommandApproval", Params: json.RawMessage(`{"conversationId":"t","command":["ls","-la"]}`)}
	res, _ = responseFor(legacy, model.InteractionResponse{Decision: "deny"})
	b, _ = json.Marshal(res)
	if string(b) != `{"decision":"denied"}` {
		t.Errorf("legacy = %s", b)
	}
	if ia := interactionFor(legacy, deadletter.Nop{}); ia.Detail != "ls -la" {
		t.Errorf("legacy detail = %q", ia.Detail)
	}
}

func TestDaemonの保留リクエストと状態を追跡する(t *testing.T) {
	d := newDaemonConn(deadletter.Nop{})
	thread := "01a0afc5-1515-7970-ae39-12bc2b518ce2"
	for _, r := range loadRequests(t) {
		d.Request(r.ID, r.Method, r.Params)
	}
	d.Notification("thread/status/changed", json.RawMessage(`{"threadId":"`+thread+`","status":{"type":"active","activeFlags":["waitingOnUserInput"]}}`))

	pending, status := d.state(thread, nil)
	if pending != model.InteractionQuestions || status != model.StatusWaitingInput {
		t.Errorf("state = %s %s", pending, status)
	}
	msgs := d.interactions(thread, deadletter.Nop{})
	if len(msgs) != 5 { // item/tool/call is not user-facing
		t.Fatalf("interactions = %d", len(msgs))
	}
	if msgs[0].ID != "codex-request:7" {
		t.Errorf("order: %s", msgs[0].ID)
	}

	d.Notification("serverRequest/resolved", json.RawMessage(`{"threadId":"`+thread+`","requestId":7}`))
	if _, ok := d.lookup(thread, "codex-request:7"); ok {
		t.Error("resolved request still pending")
	}
	// 未対応の MCP elicitation も入力待ちとして扱う
	if pending, status = d.state(thread, nil); pending != model.InteractionQuestions {
		t.Errorf("state with elicitation = %s %s", pending, status)
	}
	d.resolve(thread, "10")
	if pending, status = d.state(thread, nil); pending != model.InteractionApproval || status != model.StatusWaitingApproval {
		t.Errorf("state after resolve = %s %s", pending, status)
	}

	d.Notification("turn/completed", json.RawMessage(`{"threadId":"`+thread+`","turn":{"id":"turn-3"}}`))
	d.Notification("thread/status/changed", json.RawMessage(`{"threadId":"`+thread+`","status":{"type":"idle"}}`))
	if pending, status = d.state(thread, nil); pending != "" || status != model.StatusIdle {
		t.Errorf("state after completion = %s %s", pending, status)
	}
	// Herdr で動いているときは done/idle の判定を Herdr に任せる
	live := &providers.Live{PaneID: "w1:p2", HerdrStatus: herdr.StatusDone}
	if _, status = d.state(thread, live); status != "" {
		t.Errorf("status with live pane = %s", status)
	}
}

// --- in-process fake app-server ---

type pipeTransport struct {
	in     chan []byte
	out    chan []byte
	closed chan struct{}
	once   sync.Once
}

func (p *pipeTransport) write(ctx context.Context, b []byte) error {
	select {
	case p.out <- b:
		return nil
	case <-p.closed:
		return io.ErrClosedPipe
	}
}

func (p *pipeTransport) read(ctx context.Context) ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-p.closed:
		return nil, io.EOF
	}
}

func (p *pipeTransport) close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

type fakeServer struct {
	t        *testing.T
	thread   json.RawMessage
	mu       sync.Mutex
	calls    []string
	answered []string
}

func (f *fakeServer) serve(p *pipeTransport, send func(any)) {
	for {
		var b []byte
		select {
		case b = <-p.out:
		case <-p.closed:
			return
		}
		var m wireMessage
		json.Unmarshal(b, &m)
		f.mu.Lock()
		if m.Method != "" {
			f.calls = append(f.calls, m.Method+" "+string(m.Params))
		} else {
			f.answered = append(f.answered, string(m.ID)+" "+string(m.Result))
		}
		f.mu.Unlock()
		if len(m.ID) == 0 || m.Method == "" {
			continue
		}
		var result any = map[string]any{}
		switch m.Method {
		case "thread/loaded/list":
			result = map[string]any{"data": []string{"thread-000001"}}
		case "thread/resume":
			result = map[string]any{"approvalPolicy": "on-request", "sandbox": map[string]any{"type": "readOnly"},
				"thread": map[string]any{"id": "thread-000001", "status": map[string]any{"type": "active", "activeFlags": []string{"waitingOnApproval"}}}}
			send(map[string]any{"id": 0, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "thread-000001", "turnId": "t1", "itemId": "i1", "startedAtMs": 1, "command": "rm -rf build"}})
		case "thread/read":
			f.mu.Lock()
			result = map[string]any{"thread": f.thread}
			f.mu.Unlock()
		case "model/list":
			var params struct{ Cursor string }
			json.Unmarshal(m.Params, &params)
			if params.Cursor == "" {
				result = map[string]any{"nextCursor": "p2", "data": []map[string]any{
					{"id": "gpt-6-astra", "model": "gpt-6-astra", "displayName": "GPT-6 Astra", "description": "Frontier", "isDefault": true},
					{"id": "hidden", "model": "gpt-internal", "displayName": "Internal", "hidden": true},
				}}
			} else {
				result = map[string]any{"nextCursor": nil, "data": []map[string]any{
					{"id": "mini", "model": "gpt-6-mini", "displayName": "", "description": "Fast"},
					{"id": "bad", "model": "--oops", "displayName": "Bad"},
				}}
			}
		}
		send(map[string]any{"id": m.ID, "result": result})
	}
}

func (f *fakeServer) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

type fakeTerm struct{ prompts []string }

func (f *fakeTerm) SendKeys(context.Context, string, ...string) error { return nil }
func (f *fakeTerm) SendText(context.Context, string, string) error    { return nil }
func (f *fakeTerm) Prompt(_ context.Context, _ string, text string) error {
	f.prompts = append(f.prompts, text)
	return nil
}
func (f *fakeTerm) ReadPane(context.Context, string, int) (*herdr.ReadResult, error) { return nil, nil }

func TestDaemon接続時は構造化APIで送信と承認を行う(t *testing.T) {
	thread := json.RawMessage(`{"id":"thread-000001","cwd":"/tmp","status":{"type":"active"},"turns":[{"id":"t1","status":"inProgress","items":[{"type":"userMessage","id":"u1","content":[{"type":"text","text":"build"}]}]}]}`)
	f := &fakeServer{t: t, thread: thread}
	pt := &pipeTransport{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
	send := func(v any) {
		b, _ := json.Marshal(v)
		pt.in <- b
	}
	go f.serve(pt, send)

	term := &fakeTerm{}
	p := New("codex-not-used", "/nonexistent.sock", term, deadletter.Nop{})
	d := newDaemonConn(deadletter.Nop{})
	d.c = newRPCClient(pt, d)
	defer d.c.close()
	p.daemon = d

	ctx := context.Background()
	if err := d.c.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.sync(ctx); err != nil {
		t.Fatal(err)
	}
	// wait for the replayed request to arrive
	deadline := time.Now().Add(2 * time.Second)
	for len(d.sortedPending("thread-000001")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	sum, err := p.Summary(ctx, "thread-000001", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 購読時の approvalPolicy / sandbox からモードが分かる
	if sum.Status != model.StatusWaitingApproval || sum.LastMessage != "build" || sum.Mode != "Read only" {
		t.Errorf("summary = %+v", sum)
	}
	msgs, err := p.Messages(ctx, "thread-000001", nil)
	if err != nil {
		t.Fatal(err)
	}
	ia := msgs[len(msgs)-1].Blocks[0].Interaction
	if ia == nil || ia.ID != "codex-request:0" || ia.Detail != "rm -rf build" {
		t.Fatalf("interaction = %+v", msgs[len(msgs)-1])
	}

	if err := p.Respond(ctx, "thread-000001", nil, model.InteractionResponse{InteractionID: ia.ID, Decision: "deny"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	f.mu.Lock()
	answered := strings.Join(f.answered, "|")
	f.mu.Unlock()
	if answered != `0 {"decision":"decline"}` {
		t.Errorf("answered = %s", answered)
	}
	if err := p.Respond(ctx, "thread-000001", nil, model.InteractionResponse{InteractionID: ia.ID, Decision: "deny"}); err != providers.ErrInteractionGone {
		t.Errorf("second respond err = %v", err)
	}

	// 進行中のターンがあるので steer で送る
	if err := p.Send(ctx, "thread-000001", nil, model.Input{Text: "急いで", Images: []string{"/tmp/a.png"}}); err != nil {
		t.Fatal(err)
	}
	calls := f.callList()
	last := calls[len(calls)-1]
	if !strings.HasPrefix(last, "turn/steer ") || !strings.Contains(last, `"expectedTurnId":"t1"`) || !strings.Contains(last, `{"path":"/tmp/a.png","type":"localImage"}`) {
		t.Errorf("last call = %s", last)
	}
	if len(term.prompts) != 0 {
		t.Errorf("should not use the terminal: %v", term.prompts)
	}
}

func TestDaemonがなければペインへ入力しブロック中は端末フォールバックを出す(t *testing.T) {
	term := &fakeTerm{}
	p := New("codex", "/nonexistent.sock", term, deadletter.Nop{})
	live := &providers.Live{PaneID: "w1:p2", HerdrStatus: herdr.StatusBlocked}
	if err := p.Send(context.Background(), "thread-000001", live, model.Input{Text: "見て", Images: []string{"/tmp/a.png"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(term.prompts, "|") != "見て\n/tmp/a.png" {
		t.Errorf("prompts = %q", term.prompts)
	}
	if err := p.Send(context.Background(), "thread-000001", nil, model.Input{Text: "x"}); err != providers.ErrNotLive {
		t.Errorf("offline err = %v", err)
	}
	if err := p.Respond(context.Background(), "thread-000001", live, model.InteractionResponse{InteractionID: blockedPromptID}); !errors.Is(err, providers.ErrUnsupported) {
		t.Errorf("terminal prompt respond err = %v", err)
	}
}

func Testスレッドのモデルをサマリーに含める(t *testing.T) {
	th, _ := loadThread(t, "normal.json")
	if s := summaryFromThread(th); s.Model != "gpt-6-astra" || s.Effort != "medium" {
		t.Errorf("model = %q effort = %q", s.Model, s.Effort)
	}
}

func Testモデル一覧をページングして取得し非表示や不正なIDを除く(t *testing.T) {
	f := &fakeServer{t: t}
	pt := &pipeTransport{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
	go f.serve(pt, func(v any) {
		b, _ := json.Marshal(v)
		pt.in <- b
	})
	p := New("codex-not-used", "/nonexistent.sock", &fakeTerm{}, deadletter.Nop{})
	p.reader = newRPCClient(pt, nil)
	defer p.reader.close()

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []providers.ModelOption{
		{ID: "gpt-6-astra", Name: "GPT-6 Astra", Description: "Frontier", Default: true},
		{ID: "gpt-6-mini", Name: "gpt-6-mini", Description: "Fast"},
	}
	got, _ := json.Marshal(models)
	exp, _ := json.Marshal(want)
	if string(got) != string(exp) {
		t.Errorf("models = %s", got)
	}
}

func Test起動引数にdaemon接続とモデル指定を含める(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "cx.sock")
	p := New("codex", sock, &fakeTerm{}, deadletter.Nop{})
	if got := strings.Join(p.LaunchArgs("gpt-6-mini", "/w/app"), " "); got != "--model gpt-6-mini" {
		t.Errorf("without daemon = %q", got)
	}
	os.WriteFile(sock, nil, 0o600)
	// daemon に繋ぐときは作業ディレクトリを明示する
	if got := strings.Join(p.LaunchArgs("", "/w/app"), " "); got != "--remote unix://"+sock+" --cd /w/app" {
		t.Errorf("default model = %q", got)
	}
	if got := strings.Join(p.LaunchArgs("gpt-6-mini", "/w/app"), " "); got != "--remote unix://"+sock+" --cd /w/app --model gpt-6-mini" {
		t.Errorf("with daemon = %q", got)
	}
}

func Testスレッド設定からモードの表示名を決める(t *testing.T) {
	cases := map[string]string{
		`{"approvalPolicy":"on-request","sandboxPolicy":{"type":"workspaceWrite"},"collaborationMode":{"mode":"plan","settings":{}}}`:    "Plan",
		`{"approvalPolicy":"on-request","sandboxPolicy":{"type":"workspaceWrite"},"collaborationMode":{"mode":"default","settings":{}}}`: "Default",
		`{"approvalPolicy":"on-request","sandboxPolicy":{"type":"readOnly"}}`:                                                            "Read only",
		`{"approvalPolicy":"never","sandboxPolicy":{"type":"dangerFullAccess"}}`:                                                         "Full access",
		`{"approvalPolicy":"untrusted","sandboxPolicy":{"type":"workspaceWrite"}}`:                                                       "workspaceWrite · untrusted",
		`{"approvalPolicy":{"granular":{"rules":true}},"sandboxPolicy":{"type":"readOnly"}}`:                                             "readOnly · custom approvals",
		`{}`: "",
	}
	for in, want := range cases {
		var s threadSettings
		if err := json.Unmarshal([]byte(in), &s); err != nil {
			t.Fatal(err)
		}
		if got := s.label(); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}

func Test設定変更通知でスレッドのモードを更新する(t *testing.T) {
	d := newDaemonConn(deadletter.Nop{})
	if m := d.mode("th1"); m != "" {
		t.Errorf("unknown thread mode = %q", m)
	}
	d.Notification("thread/settings/updated", json.RawMessage(`{"threadId":"th1","threadSettings":{"model":"gpt-6-astra","approvalPolicy":"on-request","sandboxPolicy":{"type":"workspaceWrite"},"collaborationMode":{"mode":"plan","settings":{}}}}`))
	if m := d.mode("th1"); m != "Plan" {
		t.Errorf("mode = %q", m)
	}
	d.Notification("thread/closed", json.RawMessage(`{"threadId":"th1"}`))
	if m := d.mode("th1"); m != "" {
		t.Errorf("closed thread mode = %q", m)
	}
}

func TestDaemon上で指定ディレクトリに新しく作られたスレッドを見つける(t *testing.T) {
	thread := json.RawMessage(`{"id":"thread-000001","cwd":"/w/app","createdAt":1000,"status":{"type":"idle"},"turns":[]}`)
	f := &fakeServer{t: t, thread: thread}
	pt := &pipeTransport{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
	go f.serve(pt, func(v any) {
		b, _ := json.Marshal(v)
		pt.in <- b
	})
	p := New("codex-not-used", "/nonexistent.sock", &fakeTerm{}, deadletter.Nop{})
	ctx := context.Background()
	if id := p.LocateLaunched(ctx, "/w/app", time.Unix(0, 0)); id != "" {
		t.Errorf("without daemon = %q", id)
	}
	d := newDaemonConn(deadletter.Nop{})
	d.c = newRPCClient(pt, d)
	defer d.c.close()
	p.daemon = d

	if id := p.LocateLaunched(ctx, "/w/app", time.Unix(999, 0)); id != "thread-000001" {
		t.Errorf("located = %q", id)
	}
	if id := p.LocateLaunched(ctx, "/w/other", time.Unix(999, 0)); id != "" {
		t.Errorf("other cwd = %q", id)
	}
	if id := p.LocateLaunched(ctx, "/w/app", time.Unix(1001, 0)); id != "" {
		t.Errorf("older thread = %q", id)
	}
	f.mu.Lock()
	f.thread = json.RawMessage(`{"id":"thread-000001","cwd":"/w/app","createdAt":1000,"ephemeral":true,"status":{"type":"idle"}}`)
	f.mu.Unlock()
	if id := p.LocateLaunched(ctx, "/w/app", time.Unix(999, 0)); id != "" {
		t.Errorf("ephemeral thread = %q", id)
	}
}
