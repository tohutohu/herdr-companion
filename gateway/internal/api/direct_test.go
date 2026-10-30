package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
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
	trusted  bool
	listener func(string)
	// heldShared: a shared server (Codex's daemon) has the session open.
	heldShared bool
}

func (d *directFake) ReleaseShared(context.Context, string) error { return nil }

func (d *directFake) HeldShared(context.Context, string) bool { return d.heldShared }

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
func (d *directFake) DirectSetModel(_ context.Context, id, m string) error {
	d.mu.Lock()
	d.models = append(d.models, m)
	d.infos[id].RequestedModel = m
	d.mu.Unlock()
	return nil
}
func (d *directFake) DirectSetEffort(context.Context, string, string) error { return nil }
func (d *directFake) DirectSetMode(context.Context, string, string) error   { return nil }
func (d *directFake) SettingsApply() string                                 { return providers.ApplyNextTurn }
func (d *directFake) SetDirectListener(l func(string))                      { d.listener = l }
func (d *directFake) FolderTrusted(string) bool                             { return d.trusted }
func (d *directFake) DirectSessions() []providers.DirectInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []providers.DirectInfo
	for _, in := range d.infos {
		out = append(out, *in)
	}
	return out
}

// Send of a direct session goes to the runtime: only a live process takes it.
func (d *directFake) Send(ctx context.Context, id string, live *providers.Live, in model.Input) error {
	if live.IsDirect() {
		d.mu.Lock()
		info := d.infos[id]
		d.mu.Unlock()
		if info == nil || !info.Alive {
			return providers.ErrNotLive
		}
	}
	return d.fakeProvider.Send(ctx, id, live, in)
}

// herdrDown is a Herdr whose server is not running.
type herdrDown struct{}

func (herdrDown) Snapshot(context.Context) (*herdr.Snapshot, error) {
	return nil, errors.New("herdr: connect: no such file or directory")
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
	svc := sessions.New(herdrDown{}, 72*time.Hour, dp)
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

func TestHerdr停止中でも直接管理のセッションを開始して一覧に出す(t *testing.T) {
	srv, dp, token := newDirectServer(t)
	code, res := call(t, srv, token, "POST", "/v1/sessions", map[string]any{"provider": "fake", "cwd": dp.root, "prompt": "hi", "model": "fake-large"})
	if code != http.StatusCreated || res["sessionId"] != "fake:s1" || res["backend"] != "direct" {
		t.Fatalf("start = %d %v", code, res)
	}
	if _, ok := res["paneId"]; ok {
		t.Errorf("a direct session has no pane: %v", res)
	}
	if len(dp.sent) != 1 || dp.starts[0].Model != "fake-large" || dp.starts[0].Cwd != dp.root {
		t.Errorf("starts = %+v sent = %+v", dp.starts, dp.sent)
	}
	_, list := call(t, srv, token, "GET", "/v1/sessions", nil)
	sessionsJSON, _ := json.Marshal(list["sessions"])
	var got []model.Session
	json.Unmarshal(sessionsJSON, &got)
	if len(got) == 0 || got[0].ID != "fake:s1" || got[0].Backend != "direct" || !got[0].CanSend || got[0].Status == model.StatusOffline {
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

func Test直接管理のセッションにはターミナルがない(t *testing.T) {
	srv, dp, token := newDirectServer(t)
	call(t, srv, token, "POST", "/v1/sessions", map[string]any{"provider": "fake", "cwd": dp.root})
	if code, res := call(t, srv, token, "GET", "/v1/sessions/fake:s1/terminal", nil); code != http.StatusConflict || !strings.Contains(res["error"].(string), "no terminal") {
		t.Errorf("terminal = %d %v", code, res)
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

func Test一度も信頼していないフォルダでの直接起動は確認してから始める(t *testing.T) {
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

func Test直接管理のセッションをアーカイブするとプロセスを止め記録を消す(t *testing.T) {
	srv, dp, token := newDirectServer(t)
	call(t, srv, token, "POST", "/v1/sessions", map[string]any{"provider": "fake", "cwd": dp.root})
	code, res := call(t, srv, token, "POST", "/v1/sessions/fake:s1/archive", nil)
	if code != http.StatusOK || res["status"] != "offline" || res["archived"] != true {
		t.Fatalf("archive = %d %v", code, res)
	}
	if dp.infos["s1"].Alive {
		t.Error("process still running")
	}
	_, list := call(t, srv, token, "GET", "/v1/sessions", nil)
	if b, _ := json.Marshal(list); strings.Contains(string(b), `"backend":"direct"`) {
		t.Errorf("archived session still direct: %s", b)
	}
}

func TestHerdrのセッションにはモデル変更を提供しない(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	code, res := call(t, srv, token, "POST", "/v1/sessions/fake:s1/model", map[string]any{"model": "fake-large"})
	if code != http.StatusConflict || !strings.Contains(res["error"].(string), "Herdr") {
		t.Errorf("model = %d %v", code, res)
	}
}

func newMigrationServer(t *testing.T, status string) (*httptest.Server, *directFake, *fakeHerdr, string) {
	t.Helper()
	srv, dp, _ := newDirectServer(t) // for its provider and scratch root
	srv.Close()
	fh := &fakeHerdr{snap: &herdr.Snapshot{
		Workspaces: []herdr.Workspace{{WorkspaceID: "w1", Label: "app"}},
		Panes: []herdr.Pane{{
			PaneID: "w1:p1", WorkspaceID: "w1", Agent: str("fake"), AgentStatus: status, Cwd: str(dp.root),
			AgentSession: &herdr.AgentSession{Agent: "fake", Kind: "id", Value: "s1"},
		}},
	}}
	store, _ := config.Load(filepath.Join(t.TempDir(), "config.json"))
	svc := sessions.New(fh, time.Hour, dp)
	direct, _ := runtime.New(filepath.Join(t.TempDir(), "direct.json"), time.Hour, dp)
	svc.Direct = direct
	s := &Server{
		Sessions: svc, Direct: direct, Config: store, Sink: deadletter.Nop{}, Uploads: uploads.New(t.TempDir(), time.Hour),
		Launcher: &launcher.Launcher{Herdr: fh, Roots: []string{dp.root}, Providers: []providers.Provider{dp}, Direct: direct,
			StartTimeout: time.Second, PollInterval: time.Millisecond},
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, dp, fh, store.Get().AuthToken
}

func TestHerdrで動いているセッションを同じIDのまま直接管理へ移す(t *testing.T) {
	ts, dp, fh, token := newMigrationServer(t, herdr.StatusIdle)
	code, res := call(t, ts, token, "POST", "/v1/sessions/fake:s1/migrate", nil)
	if code != http.StatusOK || res["backend"] != "direct" || res["id"] != "fake:s1" {
		t.Fatalf("migrate = %d %v", code, res)
	}
	if len(fh.snap.Panes) != 0 || !strings.Contains(strings.Join(fh.calls, ","), "close-workspace:w1") {
		t.Errorf("pane not closed: %v", fh.calls)
	}
	if len(dp.starts) != 1 || dp.starts[0].Resume != "s1" || dp.starts[0].Cwd != dp.root || !dp.starts[0].Prompt.Empty() {
		t.Errorf("starts = %+v", dp.starts)
	}
}

func Test共有サーバーが開いているスレッドはペインを閉じる前に移行を断る(t *testing.T) {
	ts, dp, fh, token := newMigrationServer(t, herdr.StatusIdle)
	dp.heldShared = true
	code, res := call(t, ts, token, "POST", "/v1/sessions/fake:s1/migrate", nil)
	if code != http.StatusConflict || !strings.Contains(res["error"].(string), "keep it in Herdr") {
		t.Fatalf("migrate = %d %v", code, res)
	}
	if len(fh.calls) != 0 || len(dp.starts) != 0 {
		t.Errorf("acted: %v %v", fh.calls, dp.starts)
	}
}

func Test作業中や回答待ちのセッションは移さない(t *testing.T) {
	for _, st := range []string{herdr.StatusWorking, herdr.StatusBlocked} {
		ts, dp, fh, token := newMigrationServer(t, st)
		code, res := call(t, ts, token, "POST", "/v1/sessions/fake:s1/migrate", nil)
		if code != http.StatusConflict || !strings.Contains(res["error"].(string), "turn has ended") {
			t.Fatalf("%s: migrate = %d %v", st, code, res)
		}
		if len(fh.calls) != 0 || len(dp.starts) != 0 {
			t.Errorf("%s: acted: %v %v", st, fh.calls, dp.starts)
		}
	}
}
