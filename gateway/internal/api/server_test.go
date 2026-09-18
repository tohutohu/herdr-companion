package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/archive"
	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/files"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/launcher"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/sessions"
	"github.com/tohutohu/herdr-android-client/gateway/internal/uploads"
	"github.com/tohutohu/herdr-android-client/gateway/internal/usage"
)

type fakeHerdr struct {
	snap  *herdr.Snapshot
	calls []string
}

func (f *fakeHerdr) Snapshot(context.Context) (*herdr.Snapshot, error) { return f.snap, nil }
func (f *fakeHerdr) ReadPane(_ context.Context, pane string, lines int) (*herdr.ReadResult, error) {
	return &herdr.ReadResult{PaneID: pane, Text: "$ hello"}, nil
}
func (f *fakeHerdr) SendKeys(_ context.Context, pane string, keys ...string) error {
	f.calls = append(f.calls, "keys:"+strings.Join(keys, ","))
	return nil
}
func (f *fakeHerdr) SendText(_ context.Context, pane, text string) error {
	f.calls = append(f.calls, "text:"+text)
	return nil
}

func (f *fakeHerdr) CreateWorkspace(_ context.Context, cwd, label string) (string, string, error) {
	f.calls = append(f.calls, "create:"+label)
	return "w2", "w2:p1", nil
}
func (f *fakeHerdr) StartAgent(_ context.Context, name, kind, pane string, args []string, _ time.Duration) error {
	f.calls = append(f.calls, "start:"+kind+" "+strings.Join(args, " "))
	f.snap.Panes = append(f.snap.Panes, herdr.Pane{
		PaneID: pane, WorkspaceID: "w2", Agent: str(kind), AgentStatus: herdr.StatusIdle,
		AgentSession: &herdr.AgentSession{Agent: kind, Kind: "id", Value: args[len(args)-1]},
	})
	return nil
}
func (f *fakeHerdr) ReadVisible(context.Context, string) (string, error) { return "", nil }
func (f *fakeHerdr) Prompt(context.Context, string, string) error        { return nil }
func (f *fakeHerdr) Pane(_ context.Context, pane string) (*herdr.Pane, error) {
	for i := range f.snap.Panes {
		if f.snap.Panes[i].PaneID == pane {
			return &f.snap.Panes[i], nil
		}
	}
	return nil, &herdr.Error{Code: "pane_not_found"}
}
func (f *fakeHerdr) ReportAgentSession(context.Context, string, string, string) error { return nil }
func (f *fakeHerdr) ClosePane(_ context.Context, pane string) error {
	f.calls = append(f.calls, "close-pane:"+pane)
	return nil
}
func (f *fakeHerdr) CloseWorkspace(_ context.Context, ws string) error {
	f.calls = append(f.calls, "close-workspace:"+ws)
	var keep []herdr.Pane
	for _, p := range f.snap.Panes {
		if p.WorkspaceID != ws {
			keep = append(keep, p)
		}
	}
	f.snap.Panes = keep
	return nil
}

type fakeProvider struct {
	root string
	sent []model.Input
	resp []model.InteractionResponse
}

func (p *fakeProvider) Name() string        { return "fake" }
func (p *fakeProvider) DisplayName() string { return "Fake Agent" }
func (p *fakeProvider) HerdrAgent() string  { return "fake" }
func (p *fakeProvider) Summary(_ context.Context, id string, live *providers.Live) (*providers.Summary, error) {
	if id != "s1" && id != "old" {
		return nil, providers.ErrNotFound
	}
	return &providers.Summary{NativeID: id, Cwd: p.root, UpdatedAt: time.Unix(100, 0), LastMessage: "done", Model: "fake-large"}, nil
}
func (p *fakeProvider) Recent(context.Context, time.Time) ([]providers.Summary, error) {
	return []providers.Summary{
		{NativeID: "s1", Cwd: p.root, UpdatedAt: time.Unix(100, 0)},
		{NativeID: "old", Cwd: p.root, UpdatedAt: time.Unix(50, 0)},
	}, nil
}
func (p *fakeProvider) Messages(_ context.Context, id string, _ *providers.Live) ([]model.Message, error) {
	if id != "s1" {
		return nil, providers.ErrNotFound
	}
	return []model.Message{
		{ID: "m1", Role: model.RoleUser, Blocks: []model.Block{model.TextBlock("hi")}},
		{ID: "m2", Role: model.RoleAssistant, Blocks: []model.Block{model.TextBlock("hello")}},
		{ID: "m3", Role: model.RoleAssistant, Blocks: []model.Block{model.TextBlock("bye")}},
	}, nil
}
func (p *fakeProvider) Image(context.Context, string, string, int) (string, []byte, error) {
	return "image/png", []byte("PNG"), nil
}
func (p *fakeProvider) Send(_ context.Context, id string, live *providers.Live, in model.Input) error {
	if live == nil {
		return providers.ErrNotLive
	}
	p.sent = append(p.sent, in)
	return nil
}
func (p *fakeProvider) LaunchArgs(providers.LaunchOptions) []string { return nil }
func (p *fakeProvider) ResumeArgs(id, _ string) []string            { return []string{"resume", id} }
func (p *fakeProvider) StartupKeys(string) []string                 { return nil }
func (p *fakeProvider) Models(context.Context) (providers.ModelCatalog, error) {
	return providers.ModelCatalog{
		Models:  []providers.ModelOption{{ID: "fake-large", Name: "Large", Default: true}},
		Efforts: []providers.EffortOption{{ID: "high", Name: "High", Default: true}},
	}, nil
}
func (p *fakeProvider) Respond(_ context.Context, id string, live *providers.Live, r model.InteractionResponse) error {
	p.resp = append(p.resp, r)
	return nil
}

func str(s string) *string { return &s }

func newTestServer(t *testing.T) (*httptest.Server, *fakeProvider, *fakeHerdr, string) {
	t.Helper()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644)
	store, err := config.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	fh := &fakeHerdr{snap: &herdr.Snapshot{
		Workspaces: []herdr.Workspace{{WorkspaceID: "w1", Label: "my-project"}},
		Panes: []herdr.Pane{{
			PaneID: "w1:p1", WorkspaceID: "w1", Agent: str("fake"), AgentStatus: herdr.StatusDone, Cwd: str(root),
			AgentSession: &herdr.AgentSession{Source: "herdr:fake", Agent: "fake", Kind: "id", Value: "s1"},
		}},
	}}
	fp := &fakeProvider{root: root}
	arch, err := archive.Open(filepath.Join(t.TempDir(), "archive.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc := sessions.New(fh, time.Hour, fp)
	svc.Archive = arch
	srv := &Server{
		Launcher: &launcher.Launcher{
			Herdr: fh, Roots: []string{root}, Providers: []providers.Provider{fp},
			StartTimeout: time.Second, PollInterval: time.Millisecond, IdentityWait: 100 * time.Millisecond,
		},
		Archive:  arch,
		Sessions: svc,
		Terminal: fh,
		Uploads:  uploads.New(t.TempDir(), time.Hour),
		Config:   store,
		Sink:     deadletter.Nop{},
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, fp, fh, store.Get().AuthToken
}

func do(t *testing.T, ts *httptest.Server, token, method, path string, body []byte, ctype string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return resp, buf.Bytes()
}

func Test認証トークンがないリクエストは拒否される(t *testing.T) {
	ts, _, _, _ := newTestServer(t)
	for _, tok := range []string{"", "wrong"} {
		resp, _ := do(t, ts, tok, "GET", "/v1/sessions", nil, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q: status = %d", tok, resp.StatusCode)
		}
	}
	resp, _ := do(t, ts, "", "GET", "/healthz", nil, "")
	if resp.StatusCode != 200 {
		t.Errorf("healthz = %d", resp.StatusCode)
	}
}

func Testセッション一覧はHerdrの状態とオフラインセッションを返す(t *testing.T) {
	ts, _, _, tok := newTestServer(t)
	resp, body := do(t, ts, tok, "GET", "/v1/sessions", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got struct{ Sessions []model.Session }
	json.Unmarshal(body, &got)
	if len(got.Sessions) != 2 {
		t.Fatalf("sessions = %+v", got.Sessions)
	}
	live, off := got.Sessions[0], got.Sessions[1]
	if live.ID != "fake:s1" || live.Status != model.StatusCompleted || live.Project != "my-project" || live.PaneID != "w1:p1" || !live.CanSend {
		t.Errorf("live session = %+v", live)
	}
	if live.Model != "fake-large" {
		t.Errorf("model = %q", live.Model)
	}
	if off.ID != "fake:old" || off.Status != model.StatusOffline || off.CanSend {
		t.Errorf("offline session = %+v", off)
	}
}

func Testメッセージ取得はafter指定で差分を返す(t *testing.T) {
	ts, _, _, tok := newTestServer(t)
	_, body := do(t, ts, tok, "GET", "/v1/sessions/fake:s1/messages?after=m2", nil, "")
	var got struct {
		Session  model.Session
		Messages []model.Message
	}
	json.Unmarshal(body, &got)
	if len(got.Messages) != 2 || got.Messages[0].ID != "m2" || got.Session.Status != model.StatusCompleted {
		t.Errorf("got %+v", got)
	}
	resp, _ := do(t, ts, tok, "GET", "/v1/sessions/fake:missing/messages", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing session status = %d", resp.StatusCode)
	}
	resp, _ = do(t, ts, tok, "GET", "/v1/sessions/nope/messages", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("malformed id status = %d", resp.StatusCode)
	}
}

// upload posts a body to /v1/uploads and returns the new upload id.
func upload(t *testing.T, ts *httptest.Server, token string, body []byte, ctype, filename string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/v1/uploads", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ctype)
	if filename != "" {
		req.Header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	var up struct{ ID string }
	json.Unmarshal(buf.Bytes(), &up)
	return resp, up.ID
}

func Test画像をアップロードしてメッセージに添付できる(t *testing.T) {
	ts, fp, _, tok := newTestServer(t)
	resp, id := upload(t, ts, tok, []byte("\x89PNG..."), "image/png", "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d", resp.StatusCode)
	}

	payload, _ := json.Marshal(map[string]any{"text": "見て", "uploads": []string{id}})
	resp, body := do(t, ts, tok, "POST", "/v1/sessions/fake:s1/messages", payload, "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send status %d: %s", resp.StatusCode, body)
	}
	if len(fp.sent) != 1 || fp.sent[0].Text != "見て" || len(fp.sent[0].Images) != 1 || !strings.HasSuffix(fp.sent[0].Images[0], id+".png") {
		t.Errorf("sent = %+v", fp.sent)
	}
	if len(fp.sent[0].Files) != 0 {
		t.Errorf("image landed in Files: %+v", fp.sent[0])
	}

	payload, _ = json.Marshal(map[string]any{"text": "x", "uploads": []string{"../../etc/passwd"}})
	resp, _ = do(t, ts, tok, "POST", "/v1/sessions/fake:s1/messages", payload, "application/json")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("bad upload id status = %d", resp.StatusCode)
	}
	resp, _ = do(t, ts, tok, "POST", "/v1/sessions/fake:old/messages", []byte(`{"text":"x"}`), "application/json")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("offline send status = %d", resp.StatusCode)
	}
}

func Test画像以外のファイルは元の名前のパスで添付される(t *testing.T) {
	ts, fp, _, tok := newTestServer(t)
	resp, id := upload(t, ts, tok, []byte("col1,col2\n"), "text/csv", "売上 レポート.csv")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d", resp.StatusCode)
	}

	payload, _ := json.Marshal(map[string]any{"uploads": []string{id}})
	resp, body := do(t, ts, tok, "POST", "/v1/sessions/fake:s1/messages", payload, "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send status %d: %s", resp.StatusCode, body)
	}
	if len(fp.sent) != 1 || len(fp.sent[0].Images) != 0 || len(fp.sent[0].Files) != 1 {
		t.Fatalf("sent = %+v", fp.sent)
	}
	// Spaces become "_" so the path stays one word inside a prompt.
	if !strings.HasSuffix(fp.sent[0].Files[0], "/"+id+"/売上_レポート.csv") {
		t.Errorf("file path = %q", fp.sent[0].Files[0])
	}
}

func Testインタラクションへの回答をproviderに渡す(t *testing.T) {
	ts, fp, _, tok := newTestServer(t)
	resp, _ := do(t, ts, tok, "POST", "/v1/sessions/fake:s1/respond", []byte(`{"interactionId":"q1","answers":{"0":{"selected":["A"]}}}`), "application/json")
	if resp.StatusCode != http.StatusAccepted || len(fp.resp) != 1 || fp.resp[0].Answers["0"].Selected[0] != "A" {
		t.Errorf("status %d resp %+v", resp.StatusCode, fp.resp)
	}
	resp, _ = do(t, ts, tok, "POST", "/v1/sessions/fake:s1/respond", []byte(`{}`), "application/json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing interaction id status = %d", resp.StatusCode)
	}
}

func Testファイル取得はワークスペース外を拒否する(t *testing.T) {
	ts, _, _, tok := newTestServer(t)
	resp, body := do(t, ts, tok, "GET", "/v1/sessions/fake:s1/files/content?path=main.go", nil, "")
	if resp.StatusCode != 200 || string(body) != "package main\n" {
		t.Errorf("status %d body %q", resp.StatusCode, body)
	}
	resp, _ = do(t, ts, tok, "GET", "/v1/sessions/fake:s1/files/content?path=/etc/hosts", nil, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("outside status = %d", resp.StatusCode)
	}
	resp, body = do(t, ts, tok, "GET", "/v1/sessions/fake:s1/files", nil, "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"main.go"`) {
		t.Errorf("list status %d body %s", resp.StatusCode, body)
	}
}

func Test大きなファイルはプレビュー不可でダウンロード指定なら上限なしで取得できる(t *testing.T) {
	ts, fp, _, tok := newTestServer(t)
	big := bytes.Repeat([]byte{0}, files.MaxFileSize+1)
	os.WriteFile(filepath.Join(fp.root, "app.apk"), big, 0o644)

	resp, body := do(t, ts, tok, "GET", "/v1/sessions/fake:s1/files/stat?path=app.apk", nil, "")
	var info files.Info
	json.Unmarshal(body, &info)
	if resp.StatusCode != 200 || info.Size != int64(len(big)) || info.Name != "app.apk" || info.Previewable {
		t.Errorf("stat status %d info %+v", resp.StatusCode, info)
	}
	resp, body = do(t, ts, tok, "GET", "/v1/sessions/fake:s1/files/stat?path=main.go", nil, "")
	json.Unmarshal(body, &info)
	if resp.StatusCode != 200 || !info.Previewable {
		t.Errorf("text stat status %d info %+v", resp.StatusCode, info)
	}

	resp, _ = do(t, ts, tok, "GET", "/v1/sessions/fake:s1/files/content?path=app.apk", nil, "")
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("preview status = %d", resp.StatusCode)
	}
	resp, body = do(t, ts, tok, "GET", "/v1/sessions/fake:s1/files/content?path=app.apk&download=1", nil, "")
	if resp.StatusCode != 200 || len(body) != len(big) || !strings.Contains(resp.Header.Get("Content-Disposition"), `filename=app.apk`) {
		t.Errorf("download status %d len %d disposition %q", resp.StatusCode, len(body), resp.Header.Get("Content-Disposition"))
	}
	resp, _ = do(t, ts, tok, "GET", "/v1/sessions/fake:s1/files/content?path=/etc/hosts&download=1", nil, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("outside download status = %d", resp.StatusCode)
	}
}

func Testターミナルの読み取りと許可されたキー入力ができる(t *testing.T) {
	ts, _, fh, tok := newTestServer(t)
	resp, body := do(t, ts, tok, "GET", "/v1/sessions/fake:s1/terminal", nil, "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), "$ hello") {
		t.Errorf("read status %d body %s", resp.StatusCode, body)
	}
	resp, _ = do(t, ts, tok, "POST", "/v1/sessions/fake:s1/terminal", []byte(`{"text":"y","keys":["enter"]}`), "application/json")
	if resp.StatusCode != http.StatusAccepted || strings.Join(fh.calls, "|") != "text:y|keys:enter" {
		t.Errorf("status %d calls %v", resp.StatusCode, fh.calls)
	}
	resp, _ = do(t, ts, tok, "POST", "/v1/sessions/fake:s1/terminal", []byte(`{"keys":["ctrl+z"]}`), "application/json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("disallowed key status = %d", resp.StatusCode)
	}
	resp, _ = do(t, ts, tok, "GET", "/v1/sessions/fake:old/terminal", nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("offline terminal status = %d", resp.StatusCode)
	}
}

func Test端末登録でFCMトークンが設定に保存される(t *testing.T) {
	ts, _, _, tok := newTestServer(t)
	resp, _ := do(t, ts, tok, "POST", "/v1/devices", []byte(`{"name":"Pixel","fcmToken":"abc"}`), "application/json")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	resp, _ = do(t, ts, tok, "DELETE", "/v1/devices?fcmToken=abc", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("delete status %d", resp.StatusCode)
	}
}

func Testプロバイダーごとのモデル一覧を返す(t *testing.T) {
	ts, _, _, tok := newTestServer(t)
	resp, body := do(t, ts, tok, "GET", "/v1/models?provider=fake", nil, "")
	want := `{"models":[{"id":"fake-large","name":"Large","default":true}],"efforts":[{"id":"high","name":"High","default":true}]}`
	if resp.StatusCode != 200 || string(bytes.TrimSpace(body)) != want {
		t.Errorf("status %d: %s", resp.StatusCode, body)
	}
	resp, _ = do(t, ts, tok, "GET", "/v1/models?provider=nope", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown provider status = %d", resp.StatusCode)
	}
	resp, _ = do(t, ts, tok, "POST", "/v1/sessions", []byte(`{"provider":"fake","cwd":"/tmp","model":"--bad"}`), "application/json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid model status = %d", resp.StatusCode)
	}
}

func sessionIDs(t *testing.T, body []byte) []string {
	t.Helper()
	var got struct{ Sessions []model.Session }
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range got.Sessions {
		ids = append(ids, s.ID)
	}
	return ids
}

func Test実行中のセッションをアーカイブすると停止して一覧から外れ戻せる(t *testing.T) {
	ts, _, fh, tok := newTestServer(t)
	resp, body := do(t, ts, tok, "POST", "/v1/sessions/fake:s1/archive", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var sess model.Session
	json.Unmarshal(body, &sess)
	if !sess.Archived || sess.Status != model.StatusOffline || sess.PaneID != "" {
		t.Errorf("archived session = %+v", sess)
	}
	if strings.Join(fh.calls, ",") != "close-workspace:w1" {
		t.Errorf("herdr calls = %v", fh.calls)
	}

	_, body = do(t, ts, tok, "GET", "/v1/sessions", nil, "")
	if ids := sessionIDs(t, body); strings.Join(ids, ",") != "fake:old" {
		t.Errorf("list = %v", ids)
	}
	_, body = do(t, ts, tok, "GET", "/v1/sessions?archived=true", nil, "")
	if ids := sessionIDs(t, body); strings.Join(ids, ",") != "fake:s1" {
		t.Errorf("archived list = %v", ids)
	}

	resp, body = do(t, ts, tok, "DELETE", "/v1/sessions/fake:s1/archive", nil, "")
	sess = model.Session{}
	json.Unmarshal(body, &sess)
	if resp.StatusCode != 200 || sess.Archived {
		t.Errorf("unarchive status %d: %s", resp.StatusCode, body)
	}
	_, body = do(t, ts, tok, "GET", "/v1/sessions", nil, "")
	if ids := sessionIDs(t, body); strings.Join(ids, ",") != "fake:s1,fake:old" {
		t.Errorf("list after unarchive = %v", ids)
	}
}

func Test停止中のセッションを再開しアーカイブからも外す(t *testing.T) {
	ts, _, fh, tok := newTestServer(t)
	do(t, ts, tok, "POST", "/v1/sessions/fake:old/archive", nil, "")

	resp, body := do(t, ts, tok, "POST", "/v1/sessions/fake:old/resume", []byte(`{"trust":true}`), "application/json")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var res launcher.StartResult
	json.Unmarshal(body, &res)
	if res.SessionID != "fake:old" || res.PaneID != "w2:p1" {
		t.Errorf("result = %+v", res)
	}
	if got := strings.Join(fh.calls, ","); !strings.Contains(got, "start:fake resume old") {
		t.Errorf("herdr calls = %s", got)
	}
	_, body = do(t, ts, tok, "GET", "/v1/sessions?archived=true", nil, "")
	if ids := sessionIDs(t, body); len(ids) != 0 {
		t.Errorf("archived after resume = %v", ids)
	}

	// 実行中のセッションは再開できない
	resp, _ = do(t, ts, tok, "POST", "/v1/sessions/fake:s1/resume", nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("live resume status = %d", resp.StatusCode)
	}
}

// usageServer serves the limits from a stub command that prints fixed JSON.
func usageServer(t *testing.T, out string) (*httptest.Server, string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "fake-usage")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat <<'OUT'\n"+out+"\nOUT\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := config.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{Config: store, Usage: usage.New(script, time.Hour)}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, store.Get().AuthToken
}

func Testサブスクの残量はキャッシュされ明示的に再取得できる(t *testing.T) {
	const out = `[{"provider":"claude","usage":{"primary":{"usedPercent":16,"windowMinutes":300,` +
		`"resetsAt":"2026-09-18T02:30:00Z"},"updatedAt":"2026-09-17T22:01:25Z"}}]`
	ts, tok := usageServer(t, out)

	// Nothing has been read yet, so the cache is empty rather than an error.
	resp, body := do(t, ts, tok, "GET", "/v1/usage", nil, "")
	var got model.Usage
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	json.Unmarshal(body, &got)
	if len(got.Providers) != 0 || got.FetchedAt != nil {
		t.Fatalf("初期状態 = %+v", got)
	}

	resp, body = do(t, ts, tok, "POST", "/v1/usage/refresh", nil, "application/json")
	if resp.StatusCode != 200 {
		t.Fatalf("refresh status = %d: %s", resp.StatusCode, body)
	}
	json.Unmarshal(body, &got)
	if len(got.Providers) != 1 || got.Providers[0].Provider != "claude" {
		t.Fatalf("refresh = %+v", got)
	}
	if w := got.Providers[0].Windows; len(w) != 1 || w[0].Label != "5h" || w[0].UsedPercent != 16 {
		t.Fatalf("windows = %+v", w)
	}

	// The next GET is served from the cache.
	got = model.Usage{}
	_, body = do(t, ts, tok, "GET", "/v1/usage", nil, "")
	json.Unmarshal(body, &got)
	if len(got.Providers) != 1 || got.FetchedAt == nil {
		t.Errorf("キャッシュ = %+v", got)
	}
}

func Test使用状況が無効なときも一覧は空で返る(t *testing.T) {
	store, err := config.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer((&Server{Config: store, Usage: usage.New(usage.Disabled, 0)}).Handler())
	t.Cleanup(ts.Close)

	resp, body := do(t, ts, store.Get().AuthToken, "GET", "/v1/usage", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got model.Usage
	json.Unmarshal(body, &got)
	if got.Error == "" || len(got.Providers) != 0 {
		t.Errorf("無効時 = %+v", got)
	}
}

func Testディレクトリ判定は認証とパス制限を守り起動しない(t *testing.T) {
	ts, p, h, token := newTestServer(t)
	defer ts.Close()
	for _, tc := range []struct {
		token, cwd string
		want       int
	}{
		{"", p.root, 401}, {token, p.root, 200}, {token, t.TempDir(), 403}, {token, "relative", 403},
	} {
		body, _ := json.Marshal(map[string]string{"cwd": tc.cwd, "prompt": "通知を直して"})
		resp, b := do(t, ts, tc.token, "POST", "/v1/directories/check", body, "application/json")
		if resp.StatusCode != tc.want {
			t.Fatalf("got %d: %s", resp.StatusCode, b)
		}
		if tc.want == 200 && !bytes.Contains(b, []byte(`"verdict":"disabled"`)) {
			t.Fatalf("%s", b)
		}
	}
	if len(h.calls) != 0 {
		t.Fatalf("check launched: %v", h.calls)
	}
}
