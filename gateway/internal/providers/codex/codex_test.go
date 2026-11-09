package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
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

func Test要約は最後に終了したターンの内容の版を通知の重複判定用に返す(t *testing.T) {
	p := New("unused", deadletter.Nop{})
	for _, status := range []string{"completed", "interrupted", "failed", "inProgress", ""} {
		t.Run(status, func(t *testing.T) {
			th := &Thread{ID: "thread", Turns: []Turn{
				{ID: "old-turn", Status: "completed"},
				{ID: "latest-turn", Status: status},
			}}
			// A terminal redraw may make Herdr claim a finished turn is working.
			sum := p.summaryFromText(context.Background(), th, &providers.Live{State: providers.StateWorking}, "same text")
			finished := true
			if status == "inProgress" || status == "" {
				finished = false
			}
			if (sum.CompletionRevision != "") != finished || (finished && !strings.HasPrefix(sum.CompletionRevision, "latest-turn:")) || sum.LastTurnFailed != (status == "failed") {
				t.Fatalf("summary = %+v, finished = %v", sum, finished)
			}
		})
	}
	if sum := p.summaryFromText(context.Background(), &Thread{ID: "new"}, nil, ""); sum.CompletionRevision != "" {
		t.Fatalf("new session completion revision = %q", sum.CompletionRevision)
	}
}

func Test会話の要約とメッセージを一度のスレッド取得で返す(t *testing.T) {
	for _, fixture := range []string{"normal.json", "rich.json"} {
		t.Run(fixture, func(t *testing.T) {
			th, _ := loadThread(t, fixture)
			raw, err := json.Marshal(th)
			if err != nil {
				t.Fatal(err)
			}
			f := &fakeServer{t: t, thread: raw}
			pt := &pipeTransport{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
			go f.serve(pt, func(v any) {
				b, _ := json.Marshal(v)
				pt.in <- b
			})
			p := New("codex-not-used", deadletter.Nop{})
			p.owned = testOwned(pt)
			t.Cleanup(p.owned.c.close)
			ctx := context.Background()
			sum, msgs, err := p.Conversation(ctx, th.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			calls := f.callList()
			if len(calls) != 2 || !strings.Contains(calls[0], `"includeTurns":false`) || !strings.Contains(calls[1], `"includeTurns":true`) {
				t.Fatalf("history requests = %v", calls)
			}
			wantSum, err := p.Summary(ctx, th.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantMsgs, err := p.Messages(ctx, th.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(sum, wantSum) || !reflect.DeepEqual(msgs, wantMsgs) {
				t.Fatal("combined conversation differs from separate summary and messages")
			}
		})
	}
}

type readWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *readWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func Test同じ履歴の並行取得を共有し一方の切断で他方を中断しない(t *testing.T) {
	pt := &pipeTransport{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
	p := New("unused", deadletter.Nop{})
	p.owned = testOwned(pt)
	t.Cleanup(p.owned.c.close)
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := &readWaitContext{Context: base, waiting: make(chan struct{})}
	second := &readWaitContext{Context: context.Background(), waiting: make(chan struct{})}
	firstErr := make(chan error, 1)
	secondErr := make(chan error, 1)
	go func() { _, err := p.readThread(first, "thread-000001", true); firstErr <- err }()
	select {
	case <-first.waiting:
	case <-time.After(2 * time.Second):
		t.Fatal("first read did not wait")
	}
	var request wireMessage
	select {
	case raw := <-pt.out:
		json.Unmarshal(raw, &request)
	case <-time.After(2 * time.Second):
		t.Fatal("no RPC")
	}
	go func() {
		th, err := p.readThread(second, "thread-000001", true)
		if err == nil && th.ID != "thread-000001" {
			err = fmt.Errorf("wrong thread")
		}
		secondErr <- err
	}()
	select {
	case <-second.waiting:
	case <-time.After(2 * time.Second):
		t.Fatal("second read did not wait")
	}
	select {
	case <-pt.out:
		t.Fatal("duplicate RPC")
	default:
	}
	cancel()
	if err := <-firstErr; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	response, _ := json.Marshal(map[string]any{"id": request.ID, "result": map[string]any{"thread": map[string]any{"id": "thread-000001"}}})
	pt.in <- response
	select {
	case err := <-secondErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shared read did not complete")
	}
	// Completed flights are removed, so a later read sees new agent output.
	go func() { _, err := p.readThread(context.Background(), "thread-000001", true); secondErr <- err }()
	select {
	case raw := <-pt.out:
		json.Unmarshal(raw, &request)
	case <-time.After(2 * time.Second):
		t.Fatal("next read reused old history")
	}
	response, _ = json.Marshal(map[string]any{"id": request.ID, "result": map[string]any{"thread": map[string]any{"id": "thread-000001"}}})
	pt.in <- response
	if err := <-secondErr; err != nil {
		t.Fatal(err)
	}
}

func Test要約の末尾テキストは全メッセージ変換と一致する(t *testing.T) {
	for _, name := range []string{"normal.json", "rich.json"} {
		th, _ := loadThread(t, name)
		want := lastText(ConvertThread(th, convertOptions{SessionID: gatewayID(th.ID)}))
		if got := lastThreadText(th); got != want {
			t.Fatalf("%s: %q != %q", name, got, want)
		}
	}
}

func Test中断したターンはプロンプトで終わらず中断の知らせを置く(t *testing.T) {
	th := &Thread{ID: "th1", Turns: []Turn{{ID: "turn1", Status: "interrupted", Items: []json.RawMessage{
		json.RawMessage(`{"type":"userMessage","id":"u1","content":[{"type":"text","text":"sleep 30"}]}`)}}}}
	msgs := ConvertThread(th, convertOptions{SessionID: "codex:th1"})
	last := msgs[len(msgs)-1]
	if len(msgs) != 2 || last.ID != "turn1#interrupted" || last.Role != model.RoleSystem || last.Blocks[0].Text != "Turn interrupted" {
		t.Fatalf("messages = %+v", msgs)
	}
	if got := lastThreadText(th); got != "sleep 30" {
		t.Errorf("the summary keeps the last spoken text: %q", got)
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
	d := newServerConn(deadletter.Nop{})
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
	live := &providers.Live{State: providers.StateDone}
	if _, status = d.state(thread, live); status != "" {
		t.Errorf("status with live pane = %s", status)
	}
}

func Test呼び出し元が中断したスレッド読み込みはdeadletterに記録しない(t *testing.T) {
	// 応答を返さない app-server
	pt := &pipeTransport{in: make(chan []byte), out: make(chan []byte, 16), closed: make(chan struct{})}
	rec := &deadletter.Recorder{}
	p := New("codex-not-used", rec)
	p.owned = testOwned(pt)
	defer p.owned.c.close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.readThread(ctx, "thread-000001", false); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(rec.Entries) != 0 {
		t.Errorf("dead letters = %+v, want none", rec.Entries)
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
	t      *testing.T
	thread json.RawMessage
	// threads, when set, answers thread/read and thread/loaded/list per id
	// instead of the single thread.
	threads  map[string]json.RawMessage
	mu       sync.Mutex
	calls    []string
	answered []string
}

func (f *fakeServer) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func Testスレッドのモデルをサマリーに含める(t *testing.T) {
	th, _ := loadThread(t, "normal.json")
	if s := summaryFromThread(th); s.Model != "gpt-6-astra" || s.Effort != "medium" {
		t.Errorf("model = %q effort = %q", s.Model, s.Effort)
	}
}

func Testモデル一覧をページングしてReserve以外の非表示や不正なIDを除く(t *testing.T) {
	f := &fakeServer{t: t}
	pt := &pipeTransport{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
	go f.serve(pt, func(v any) {
		b, _ := json.Marshal(v)
		pt.in <- b
	})
	p := New("codex-not-used", deadletter.Nop{})
	p.owned = testOwned(pt)
	defer p.owned.c.close()

	cat, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	astraEfforts := []providers.EffortOption{
		{ID: "medium", Name: "Medium", Description: "Balanced", Default: true},
		{ID: "xhigh", Name: "Extra high", Description: "Deep"},
	}
	want := providers.ModelCatalog{
		Models: []providers.ModelOption{
			{ID: "gpt-6-astra", Name: "GPT-6 Astra", Description: "Frontier", Default: true, Efforts: astraEfforts},
			{ID: "gpt-6-mini", Name: "gpt-6-mini", Description: "Fast"},
			{ID: "gpt-reserve", Name: "GPT-Reserve", Efforts: []providers.EffortOption{
				{ID: "medium", Name: "Medium", Description: "Balanced", Default: true},
			}},
		},
		// モデル未指定時は既定モデルのエフォートを出す
		Efforts: astraEfforts,
		Modes:   modes,
	}
	got, _ := json.Marshal(cat)
	exp, _ := json.Marshal(want)
	if string(got) != string(exp) {
		t.Errorf("catalog = %s", got)
	}
}

func TestReserveのレート制限を別ウィンドウとして取得する(t *testing.T) {
	f := &fakeServer{t: t}
	pt := &pipeTransport{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
	go f.serve(pt, func(v any) {
		b, _ := json.Marshal(v)
		pt.in <- b
	})
	p := New("codex-not-used", deadletter.Nop{})
	p.owned = testOwned(pt)
	defer p.owned.c.close()

	window, err := p.ReadReserveWindow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if window == nil {
		t.Fatal("Reserve window is missing")
	}
	if window.Key != "gpt-reserve" || window.Label != "7d" || window.Scope != "gpt-reserve" || window.UsedPercent != 6 {
		t.Errorf("window = %+v", window)
	}
	wantReset := time.Unix(1789855214, 0).UTC()
	if window.ResetsAt == nil || !window.ResetsAt.Equal(wantReset) {
		t.Errorf("resetsAt = %v, want %v", window.ResetsAt, wantReset)
	}
	calls := f.callList()
	if len(calls) != 1 || !strings.Contains(calls[0], `"supportsLunaReserve":true`) {
		t.Errorf("calls = %v", calls)
	}
}

func TestCodexが作ったworktreeのスレッドはペインではなくスレッドの作業ディレクトリを使う(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	for cwd, want := range map[string]bool{
		filepath.Join(home, "worktrees", "114f", "repo"): true,
		filepath.Join(home, "worktrees"):                 false,
		filepath.Join(home, "worktrees-other", "repo"):   false,
		"/w/app": false,
		"":       false,
	} {
		if got := inManagedWorktree(cwd); got != want {
			t.Errorf("%q = %v", cwd, got)
		}
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
	d := newServerConn(deadletter.Nop{})
	if m := d.mode("th1", ""); m != "" {
		t.Errorf("unknown thread mode = %q", m)
	}
	d.Notification("thread/settings/updated", json.RawMessage(`{"threadId":"th1","threadSettings":{"model":"gpt-6-astra","approvalPolicy":"on-request","sandboxPolicy":{"type":"workspaceWrite"},"collaborationMode":{"mode":"plan","settings":{}}}}`))
	if m := d.mode("th1", ""); m != "Plan" {
		t.Errorf("mode = %q", m)
	}
	d.Notification("thread/closed", json.RawMessage(`{"threadId":"th1"}`))
	if m := d.mode("th1", ""); m != "" {
		t.Errorf("closed thread mode = %q", m)
	}
}

func Test購読前にPlanモードで始まったスレッドはrolloutの記録でPlanと表示する(t *testing.T) {
	d := newServerConn(deadletter.Nop{})
	// thread/resume gives only the permission preset.
	d.settings["th1"] = threadSettings{ApprovalPolicy: json.RawMessage(`"on-request"`), SandboxPolicy: &sandboxPolicy{Type: "readOnly"}}
	if m := d.mode("th1", "Plan"); m != "Plan" {
		t.Errorf("mode = %q, want Plan", m)
	}
	if m := d.mode("th1", ""); m != "Read only" {
		t.Errorf("mode without plan = %q", m)
	}
	// A later notification knows the mode and wins over the rollout.
	d.Notification("thread/settings/updated", json.RawMessage(`{"threadId":"th1","threadSettings":{"approvalPolicy":"on-request","sandboxPolicy":{"type":"readOnly"},"collaborationMode":{"mode":"default","settings":{}}}}`))
	if m := d.mode("th1", "Plan"); m != "Read only" {
		t.Errorf("mode after notification = %q", m)
	}
	if m := d.mode("unknown", "Plan"); m != "Plan" {
		t.Errorf("unsubscribed thread mode = %q", m)
	}
}

func TestRolloutから最新のコラボレーションモードを読む(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	lines := []string{
		`{"type":"turn_context","payload":{"turn_id":"t1","collaboration_mode":{"mode":"plan","settings":{"model":"gpt-6-astra"}}}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"collaboration_mode is mentioned here"}]}}`,
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
	if m := collaborationModeFrom(rolloutTailLines(path)); m != "plan" {
		t.Errorf("mode = %q", m)
	}
	// Switching mode in the TUI records the thread settings without a turn.
	lines = append(lines, `{"type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"collaboration_mode":{"mode":"default","settings":{"model":"gpt-6-astra"}}}}}`)
	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
	if m := collaborationModeFrom(rolloutTailLines(path)); m != "default" {
		t.Errorf("mode after switch = %q", m)
	}
	if m := collaborationModeFrom(rolloutTailLines("")); m != "" {
		t.Errorf("no rollout = %q", m)
	}
}

func TestCodexのコンテキスト使用量をrolloutファイルの末尾から読む(t *testing.T) {
	count := func(total, window int64) string {
		return fmt.Sprintf(`{"timestamp":"2026-09-18T07:32:18.059Z","type":"event_msg","payload":{"type":"token_count",`+
			`"info":{"last_token_usage":{"total_tokens":%d},"model_context_window":%d}}}`, total, window)
	}
	lines := []string{
		`{"type":"session_meta","payload":{"id":"thread-1"}}`,
		count(10_000, 258_400),
		`{"type":"response_item","payload":{"type":"message"}}`,
		count(64_600, 258_400),
		// 中断したターンはinfoなしのtoken_countを残すので、その手前まで遡る。
		`{"timestamp":"2026-09-18T07:33:00.000Z","type":"event_msg","payload":{"type":"token_count","info":null}}`,
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := infoFromRollout(path).context()
	if c == nil || c.UsedTokens != 64_600 || c.WindowTokens != 258_400 || c.UsedPercent != 25 {
		t.Errorf("context = %+v", c)
	}
	if c := infoFromRollout(filepath.Join(t.TempDir(), "missing.jsonl")).context(); c != nil {
		t.Errorf("missing rollout = %+v, want nil", c)
	}
	if c := infoFromRollout("").context(); c != nil {
		t.Errorf("empty path = %+v, want nil", c)
	}
}

func TestCodexのrolloutが大きくても末尾だけ読んで使用量を求める(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":1},"model_context_window":100}}}` + "\n")
	for b.Len() < rolloutTail*2 {
		b.WriteString(`{"type":"response_item","payload":{"type":"message","content":"` + strings.Repeat("x", 500) + `"}}` + "\n")
	}
	b.WriteString(`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":50},"model_context_window":100}}}` + "\n")
	path := filepath.Join(t.TempDir(), "big.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := infoFromRollout(path).context(); c == nil || c.UsedPercent != 50 {
		t.Errorf("context = %+v", c)
	}
}

func TestCodexのコストはスレッドの累計トークンから見積もる(t *testing.T) {
	lines := []string{
		`{"type":"session_meta","payload":{"id":"thread-1"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{` +
			`"total_token_usage":{"input_tokens":1000000,"cached_input_tokens":900000,"output_tokens":100000},` +
			`"last_token_usage":{"total_tokens":64600},"model_context_window":258400}}}`,
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info := infoFromRollout(path)
	// gpt-5.6-luna: 非キャッシュ入力100000*$0.2 + キャッシュ読み出し900000*$0.02 + 出力100000*$1.2
	want := 0.02 + 0.018 + 0.12
	c := info.cost("gpt-5.6-luna")
	if c == nil || math.Abs(c.USD-want) > 1e-9 || !c.Estimated {
		t.Errorf("cost = %+v, want %v(見積もり)", c, want)
	}
	// 単価の分からないモデルとロールアウトのないスレッドは金額なし。
	if c := info.cost("codex-auto-review"); c != nil {
		t.Errorf("unknown model cost = %+v, want nil", c)
	}
	if c := infoFromRollout("").cost("gpt-5.6-luna"); c != nil {
		t.Errorf("missing rollout cost = %+v, want nil", c)
	}
}

// infoFromRollout returns the newest token accounting in the thread's rollout
// file, or nil when the file is missing or has no usable count (an
// interrupted turn records one without an info block).
func infoFromRollout(path string) *tokenInfo {
	return tokenInfoFrom(rolloutTailLines(path))
}

// testOwned stands in for the gateway's own app-server with a fake transport.
func testOwned(t transport) *serverConn {
	d := newServerConn(deadletter.Nop{})
	d.c = newRPCClient(t, nil)
	return d
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
		case "thread/resume":
			result = map[string]any{"approvalPolicy": "on-request", "sandbox": map[string]any{"type": "readOnly"},
				"thread": map[string]any{"id": "thread-000001", "status": map[string]any{"type": "active", "activeFlags": []string{"waitingOnApproval"}}}}
			send(map[string]any{"id": 0, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "thread-000001", "turnId": "t1", "itemId": "i1", "startedAtMs": 1, "command": "rm -rf build"}})
		case "thread/read":
			var params struct{ ThreadID string }
			json.Unmarshal(m.Params, &params)
			f.mu.Lock()
			if f.threads != nil {
				result = map[string]any{"thread": f.threads[params.ThreadID]}
			} else {
				result = map[string]any{"thread": f.thread}
			}
			f.mu.Unlock()
		case "model/list":
			var params struct {
				Cursor        string
				IncludeHidden bool
			}
			json.Unmarshal(m.Params, &params)
			if params.Cursor == "" {
				result = map[string]any{"nextCursor": "p2", "data": []map[string]any{
					{"id": "gpt-6-astra", "model": "gpt-6-astra", "displayName": "GPT-6 Astra", "description": "Frontier", "isDefault": true,
						"defaultReasoningEffort": "medium", "supportedReasoningEfforts": []map[string]any{
							{"reasoningEffort": "medium", "description": "Balanced"},
							{"reasoningEffort": "xhigh", "description": "Deep"},
							{"reasoningEffort": "--oops"},
						}},
					{"id": "hidden", "model": "gpt-internal", "displayName": "Internal", "hidden": true},
				}}
			} else {
				result = map[string]any{"nextCursor": nil, "data": []map[string]any{
					{"id": "mini", "model": "gpt-6-mini", "displayName": "", "description": "Fast"},
					{"id": "bad", "model": "--oops", "displayName": "Bad"},
				}}
				if params.IncludeHidden {
					page := result.(map[string]any)
					page["data"] = append(page["data"].([]map[string]any), map[string]any{
						"id": "gpt-reserve", "model": "gpt-reserve", "displayName": "GPT-Reserve", "hidden": true,
						"defaultReasoningEffort": "medium", "supportedReasoningEfforts": []map[string]any{
							{"reasoningEffort": "medium", "description": "Balanced"},
						},
					})
				}
			}
		case "command/exec":
			result = map[string]any{"exitCode": 0, "stdout": "/codex/arg0:" + os.Getenv("PATH") + "\n"}
		case "account/rateLimits/read":
			result = map[string]any{
				"rateLimitsByLimitId": map[string]any{
					"base_model_inference": map[string]any{
						"limitId":         "base_model_inference",
						"limitName":       "gpt-reserve",
						"normalModelSlug": "gpt-5.6-luna",
						"primary": map[string]any{
							"usedPercent":        6,
							"windowDurationMins": 10080,
							"resetsAt":           1789855214,
						},
					},
				},
			}
		}
		send(map[string]any{"id": m.ID, "result": result})
	}
}
