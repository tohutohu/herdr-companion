package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/archive"
	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/launcher"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/runtime"
	"github.com/tohutohu/herdr-android-client/gateway/internal/sessions"
	"github.com/tohutohu/herdr-android-client/gateway/internal/uploads"
)

// directFake is fakeProvider with a direct runtime the test controls.
type directFake struct {
	*fakeProvider
	mu       sync.Mutex
	infos    map[string]*providers.DirectInfo
	starts   []providers.DirectStart
	models   []string
	modes    []string
	trusted  bool
	listener func(string)
	// asked records side questions; sideAnswer answers them (nil: no conversation yet).
	asked      []sideAsk
	sideAnswer *string
	// m is the runtime manager the server uses.
	m *runtime.Manager
}

// run makes a session live in the runtime, working in cwd.
func (d *directFake) run(t *testing.T, id, cwd string) {
	t.Helper()
	if err := d.m.Resume(context.Background(), "fake:"+id, providers.DirectStart{Cwd: cwd}); err != nil {
		t.Fatal(err)
	}
}

// finishTurn ends a turn of a live session, which leaves it unread.
func (d *directFake) finishTurn(id string) {
	d.mu.Lock()
	d.infos[id].Turns++
	d.mu.Unlock()
	d.listener(id)
}

func (d *directFake) alive(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	in := d.infos[id]
	return in != nil && in.Alive
}

func (d *directFake) setCwd(id, cwd string) {
	d.mu.Lock()
	d.infos[id].Cwd = cwd
	d.mu.Unlock()
}

func (d *directFake) DirectStart(_ context.Context, opts providers.DirectStart) (string, error) {
	d.mu.Lock()
	d.starts = append(d.starts, opts)
	id := opts.Resume
	if id == "" {
		id = "s1"
	}
	d.infos[id] = &providers.DirectInfo{NativeID: id, Cwd: opts.Cwd, Alive: true}
	d.mu.Unlock()
	if !opts.Prompt.Empty() {
		d.sent = append(d.sent, opts.Prompt)
	}
	return id, nil
}
func (d *directFake) DirectStop(_ context.Context, id string) error {
	d.mu.Lock()
	if in := d.infos[id]; in != nil {
		in.Alive = false
	}
	d.mu.Unlock()
	return nil
}
func (d *directFake) DirectInterrupt(context.Context, string) error { return nil }

type sideAsk struct {
	id, question string
	history      []providers.SideTurn
}

func (d *directFake) DirectSideQuestion(_ context.Context, id, question string, history []providers.SideTurn) (providers.SideAnswer, error) {
	if !d.alive(id) {
		return providers.SideAnswer{}, providers.ErrNotLive
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.asked = append(d.asked, sideAsk{id, question, history})
	if d.sideAnswer == nil {
		return providers.SideAnswer{}, providers.ErrNoConversation
	}
	return providers.SideAnswer{Answer: *d.sideAnswer}, nil
}
func (d *directFake) DirectSetModel(_ context.Context, id, m string) error {
	d.mu.Lock()
	d.models = append(d.models, m)
	d.infos[id].RequestedModel = m
	d.mu.Unlock()
	return nil
}
func (d *directFake) DirectSetEffort(context.Context, string, string) error { return nil }
func (d *directFake) DirectSetMode(_ context.Context, _, mode string) error {
	d.mu.Lock()
	d.modes = append(d.modes, mode)
	d.mu.Unlock()
	return nil
}
func (d *directFake) SettingsApply() string            { return providers.ApplyNextTurn }
func (d *directFake) SetDirectListener(l func(string)) { d.listener = l }
func (d *directFake) FolderTrusted(string) bool        { return d.trusted }
func (d *directFake) DirectSessions() []providers.DirectInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []providers.DirectInfo
	for _, in := range d.infos {
		out = append(out, *in)
	}
	return out
}

// Send goes to the runtime: only a live process takes it.
func (d *directFake) Send(ctx context.Context, id string, live *providers.Live, in model.Input) error {
	if !d.alive(id) {
		return providers.ErrNotLive
	}
	return d.fakeProvider.Send(ctx, id, live, in)
}

func newDirectServer(t *testing.T) (*httptest.Server, *directFake, string) {
	t.Helper()
	root := t.TempDir()
	store, err := config.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	root, _ = filepath.EvalSymlinks(root) // the launcher resolves /var to /private/var
	fp := &fakeProvider{root: root}
	dp := &directFake{fakeProvider: fp, infos: map[string]*providers.DirectInfo{}, trusted: true}
	svc := sessions.New(72*time.Hour, dp)
	arch, err := archive.Open(filepath.Join(t.TempDir(), "archive.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc.Archive = arch
	direct, err := runtime.New(filepath.Join(t.TempDir(), "direct.json"), 72*time.Hour, dp)
	if err != nil {
		t.Fatal(err)
	}
	svc.Direct = direct
	dp.m = direct
	delivery, err := OpenMessageDeliveryStore(filepath.Join(t.TempDir(), "delivery"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Sessions: svc, Direct: direct, Config: store, Sink: deadletter.Nop{}, Archive: arch, MessageDelivery: delivery,
		Uploads:  uploads.New(t.TempDir(), time.Hour),
		Launcher: &launcher.Launcher{Roots: []string{root}, Providers: []providers.Provider{dp}, Direct: direct, WorktreeDir: t.TempDir()},
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, dp, store.Get().AuthToken
}

func call(t *testing.T, srv *httptest.Server, token, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, srv.URL+path, r)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func Testセッションを開始して一覧に出す(t *testing.T) {
	srv, dp, token := newDirectServer(t)
	code, res := call(t, srv, token, "POST", "/v1/sessions", map[string]any{"provider": "fake", "cwd": dp.root, "prompt": "hi", "model": "fake-large"})
	if code != http.StatusCreated || res["sessionId"] != "fake:s1" {
		t.Fatalf("start = %d %v", code, res)
	}
	for _, gone := range []string{"paneId", "backend"} {
		if _, ok := res[gone]; ok {
			t.Errorf("%s is no longer part of the result: %v", gone, res)
		}
	}
	if len(dp.sent) != 1 || dp.starts[0].Model != "fake-large" || dp.starts[0].Cwd != dp.root {
		t.Errorf("starts = %+v sent = %+v", dp.starts, dp.sent)
	}
	_, list := call(t, srv, token, "GET", "/v1/sessions", nil)
	sessionsJSON, _ := json.Marshal(list["sessions"])
	var got []model.Session
	json.Unmarshal(sessionsJSON, &got)
	if len(got) == 0 || got[0].ID != "fake:s1" || !got[0].CanSend || got[0].Status == model.StatusOffline {
		t.Fatalf("sessions = %+v", got)
	}
}

func Test利用できないモデルや推論強度は理由を付けて拒否する(t *testing.T) {
	srv, dp, token := newDirectServer(t)
	call(t, srv, token, "POST", "/v1/sessions", map[string]any{"provider": "fake", "cwd": dp.root})
	code, res := call(t, srv, token, "POST", "/v1/sessions/fake:s1/model", map[string]any{"model": "fake-tiny"})
	if code != http.StatusBadRequest || !strings.Contains(res["error"].(string), "fake-large") {
		t.Fatalf("unknown model = %d %v", code, res)
	}
	code, res = call(t, srv, token, "POST", "/v1/sessions/fake:s1/effort", map[string]any{"effort": "max"})
	if code != http.StatusBadRequest || !strings.Contains(res["error"].(string), "high") {
		t.Fatalf("unknown effort = %d %v", code, res)
	}
	if len(dp.models) != 0 {
		t.Errorf("models = %v", dp.models)
	}
	code, res = call(t, srv, token, "POST", "/v1/sessions/fake:s1/model", map[string]any{"model": "fake-large"})
	if code != http.StatusOK || res["pendingModel"] != "fake-large" || res["settingsApply"] != "next_turn" {
		t.Fatalf("model = %d %v", code, res)
	}
}

func Test止まったセッションへの送信は同じIDで再開してから一度だけ届ける(t *testing.T) {
	srv, dp, token := newDirectServer(t)
	call(t, srv, token, "POST", "/v1/sessions", map[string]any{"provider": "fake", "cwd": dp.root})
	dp.DirectStop(context.Background(), "s1")
	body := map[string]any{"text": "continue", "requestId": "11111111-2222-4333-8444-555555555555"}
	if code, res := call(t, srv, token, "POST", "/v1/sessions/fake:s1/messages", body); code != http.StatusAccepted {
		t.Fatalf("send = %d %v", code, res)
	}
	if code, _ := call(t, srv, token, "POST", "/v1/sessions/fake:s1/messages", body); code != http.StatusAccepted {
		t.Fatalf("retry = %d", code)
	}
	if len(dp.starts) != 2 || dp.starts[1].Resume != "s1" || !dp.starts[1].Prompt.Empty() || dp.starts[1].Cwd != dp.root {
		t.Errorf("starts = %+v", dp.starts)
	}
	if len(dp.sent) != 1 || dp.sent[0].Text != "continue" {
		t.Errorf("sent = %+v", dp.sent)
	}
}

func Test一度も信頼していないフォルダでの起動は確認してから始める(t *testing.T) {
	srv, dp, token := newDirectServer(t)
	dp.trusted = false
	code, res := call(t, srv, token, "POST", "/v1/sessions", map[string]any{"provider": "fake", "cwd": dp.root, "prompt": "hi"})
	launch, _ := res["launchId"].(string)
	if code != http.StatusCreated || res["trustRequired"] != true || launch == "" || len(dp.starts) != 0 {
		t.Fatalf("start = %d %v", code, res)
	}
	code, res = call(t, srv, token, "POST", "/v1/launches/"+launch+"/trust", map[string]any{"trust": true})
	if code != http.StatusOK || res["sessionId"] != "fake:s1" || len(dp.sent) != 1 {
		t.Fatalf("trust = %d %v", code, res)
	}
	if code, _ := call(t, srv, token, "POST", "/v1/launches/"+launch+"/trust", map[string]any{"trust": true}); code != http.StatusNotFound {
		t.Errorf("second answer = %d", code)
	}
}

func TestセッションをアーカイブするとプロセスとRuntimeの記録を消す(t *testing.T) {
	srv, dp, token := newDirectServer(t)
	call(t, srv, token, "POST", "/v1/sessions", map[string]any{"provider": "fake", "cwd": dp.root})
	code, res := call(t, srv, token, "POST", "/v1/sessions/fake:s1/archive", nil)
	if code != http.StatusOK || res["status"] != "offline" || res["archived"] != true {
		t.Fatalf("archive = %d %v", code, res)
	}
	if dp.infos["s1"].Alive {
		t.Error("process still running")
	}
	if dp.m.Owns("fake:s1") {
		t.Error("the runtime still records the session")
	}
}

func Test止まっているセッションのモードとモデルを変えられる(t *testing.T) {
	srv, dp, token := newDirectServer(t)
	call(t, srv, token, "POST", "/v1/sessions", map[string]any{"provider": "fake", "cwd": dp.root})
	dp.DirectStop(context.Background(), "s1")
	if code, res := call(t, srv, token, "POST", "/v1/sessions/fake:s1/mode", map[string]any{"mode": "plan"}); code != http.StatusAccepted {
		t.Fatalf("mode = %d %v", code, res)
	}
	if code, res := call(t, srv, token, "POST", "/v1/sessions/fake:s1/model", map[string]any{"model": "fake-large"}); code != http.StatusOK {
		t.Fatalf("model = %d %v", code, res)
	}
}

func Test脇道の質問は履歴付きで聞き会話には何も送らない(t *testing.T) {
	srv, dp, token := newDirectServer(t)
	call(t, srv, token, "POST", "/v1/sessions", map[string]any{"provider": "fake", "cwd": dp.root})
	answer := "受信確認を見ています"
	dp.sideAnswer = &answer
	history := []map[string]any{{"question": "いま何してる？", "answer": "テストを読んでいます"}}
	code, res := call(t, srv, token, "POST", "/v1/sessions/fake:s1/side-question", map[string]any{"question": "  なぜ？  ", "history": history})
	if code != http.StatusOK || res["answer"] != answer || res["synthetic"] != false {
		t.Fatalf("side question = %d %v", code, res)
	}
	if len(dp.asked) != 1 || dp.asked[0].id != "s1" || dp.asked[0].question != "なぜ？" {
		t.Fatalf("asked = %+v", dp.asked)
	}
	if h := dp.asked[0].history; len(h) != 1 || h[0].Question != "いま何してる？" || h[0].Answer != "テストを読んでいます" {
		t.Errorf("history = %+v", h)
	}
	if len(dp.sent) != 0 {
		t.Errorf("sent = %+v", dp.sent)
	}
}

func Test止まったセッションへの脇道の質問は再開してから答える(t *testing.T) {
	srv, dp, token := newDirectServer(t)
	call(t, srv, token, "POST", "/v1/sessions", map[string]any{"provider": "fake", "cwd": dp.root})
	dp.DirectStop(context.Background(), "s1")
	answer := "ok"
	dp.sideAnswer = &answer
	code, res := call(t, srv, token, "POST", "/v1/sessions/fake:s1/side-question", map[string]any{"question": "まだある？"})
	if code != http.StatusOK || res["answer"] != "ok" {
		t.Fatalf("side question = %d %v", code, res)
	}
	if len(dp.starts) != 2 || dp.starts[1].Resume != "s1" || !dp.starts[1].Prompt.Empty() {
		t.Errorf("starts = %+v", dp.starts)
	}
}

func Test空の脇道の質問や会話のないセッションへの質問は理由を付けて断る(t *testing.T) {
	srv, dp, token := newDirectServer(t)
	call(t, srv, token, "POST", "/v1/sessions", map[string]any{"provider": "fake", "cwd": dp.root})
	if code, res := call(t, srv, token, "POST", "/v1/sessions/fake:s1/side-question", map[string]any{"question": " "}); code != http.StatusBadRequest {
		t.Errorf("empty = %d %v", code, res)
	}
	code, res := call(t, srv, token, "POST", "/v1/sessions/fake:s1/side-question", map[string]any{"question": "何してる？"})
	if code != http.StatusConflict || !strings.Contains(res["error"].(string), "no conversation") {
		t.Errorf("no conversation = %d %v", code, res)
	}
}
