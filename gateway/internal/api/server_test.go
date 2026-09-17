package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/launcher"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/sessions"
	"github.com/tohutohu/herdr-android-client/gateway/internal/uploads"
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
func (p *fakeProvider) LaunchArgs(_, _ string) []string { return nil }
func (p *fakeProvider) StartupKeys(string) []string     { return nil }
func (p *fakeProvider) Models(context.Context) ([]providers.ModelOption, error) {
	return []providers.ModelOption{{ID: "fake-large", Name: "Large", Default: true}}, nil
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
	srv := &Server{
		Launcher: &launcher.Launcher{Roots: []string{root}, Providers: []providers.Provider{fp}},
		Sessions: sessions.New(fh, time.Hour, fp),
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

func Test画像をアップロードしてメッセージに添付できる(t *testing.T) {
	ts, fp, _, tok := newTestServer(t)
	resp, body := do(t, ts, tok, "POST", "/v1/uploads", []byte("\x89PNG..."), "image/png")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d: %s", resp.StatusCode, body)
	}
	var up struct{ ID string }
	json.Unmarshal(body, &up)

	payload, _ := json.Marshal(map[string]any{"text": "見て", "uploads": []string{up.ID}})
	resp, body = do(t, ts, tok, "POST", "/v1/sessions/fake:s1/messages", payload, "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send status %d: %s", resp.StatusCode, body)
	}
	if len(fp.sent) != 1 || fp.sent[0].Text != "見て" || len(fp.sent[0].Images) != 1 || !strings.HasSuffix(fp.sent[0].Images[0], up.ID+".png") {
		t.Errorf("sent = %+v", fp.sent)
	}

	resp, _ = do(t, ts, tok, "POST", "/v1/uploads", []byte("#!/bin/sh"), "text/x-shellscript")
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("non-image upload status = %d", resp.StatusCode)
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
	if resp.StatusCode != 200 || string(bytes.TrimSpace(body)) != `{"models":[{"id":"fake-large","name":"Large","default":true}]}` {
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
