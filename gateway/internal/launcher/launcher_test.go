package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/files"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

func setupRoots(t *testing.T) (root, outside string) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root = filepath.Join(base, "workspace")
	outside = filepath.Join(base, "secret")
	for _, d := range []string{root + "/app-b", root + "/App-a/src", root + "/.hidden", outside} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644)
	os.Symlink(outside, filepath.Join(root, "escape"))
	return root, outside
}

func Testルート配下のディレクトリだけを一覧できる(t *testing.T) {
	root, outside := setupRoots(t)
	l := &Launcher{Roots: []string{root}}

	roots, err := l.List("")
	if err != nil || len(roots.Entries) != 1 || roots.Entries[0].Path != root {
		t.Fatalf("roots = %+v, %v", roots, err)
	}
	got, err := l.List(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range got.Entries {
		names = append(names, e.Name)
	}
	// 隠しディレクトリ・ファイル・ルート外へのリンクは出さない
	if strings.Join(names, ",") != "App-a,app-b" || got.Parent != "" {
		t.Errorf("entries = %v parent = %q", names, got.Parent)
	}
	sub, _ := l.List(filepath.Join(root, "App-a"))
	if sub.Parent != root {
		t.Errorf("parent = %q", sub.Parent)
	}
	for _, p := range []string{outside, filepath.Join(root, "escape"), root + "/../secret", "workspace"} {
		if _, err := l.List(p); !errors.Is(err, files.ErrForbidden) {
			t.Errorf("%s: err = %v", p, err)
		}
	}
}

func Testディレクトリを作成でき不正な名前は拒否する(t *testing.T) {
	root, outside := setupRoots(t)
	l := &Launcher{Roots: []string{root}}
	p, err := l.Mkdir(root, " new-project ")
	if err != nil || p != filepath.Join(root, "new-project") {
		t.Fatalf("mkdir = %q, %v", p, err)
	}
	if st, err := os.Stat(p); err != nil || !st.IsDir() {
		t.Errorf("not created: %v", err)
	}
	if _, err := l.Mkdir(root, "new-project"); !errors.Is(err, os.ErrExist) {
		t.Errorf("duplicate err = %v", err)
	}
	for _, name := range []string{"", ".", "..", "a/b", "..\\x", ".git", strings.Repeat("x", 101)} {
		if _, err := l.Mkdir(root, name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%q: err = %v", name, err)
		}
	}
	if _, err := l.Mkdir(outside, "x"); !errors.Is(err, files.ErrForbidden) {
		t.Errorf("outside err = %v", err)
	}
}

type fakeHerdr struct {
	mu       sync.Mutex
	screen   string
	status   string
	session  *herdr.AgentSession
	startErr error
	// startErrs are returned by successive StartAgent calls before startErr.
	startErrs []error
	snap      *herdr.Snapshot
	calls     []string
}

func (f *fakeHerdr) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeHerdr) CreateWorkspace(_ context.Context, cwd, label string) (string, string, error) {
	f.record("create " + label)
	return "w9", "w9:p1", nil
}
func (f *fakeHerdr) StartAgent(_ context.Context, name, kind, pane string, args []string, _ time.Duration) error {
	f.record("start " + kind + " " + strings.Join(args, " "))
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.startErrs) > 0 {
		err := f.startErrs[0]
		f.startErrs = f.startErrs[1:]
		return err
	}
	return f.startErr
}
func (f *fakeHerdr) ReadVisible(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.screen, nil
}
func (f *fakeHerdr) SendKeys(_ context.Context, _ string, keys ...string) error {
	f.record("keys " + strings.Join(keys, ","))
	f.mu.Lock()
	f.status, f.screen = herdr.StatusIdle, ""
	f.mu.Unlock()
	return nil
}
func (f *fakeHerdr) Prompt(_ context.Context, _ string, text string) error {
	f.record("prompt " + text)
	f.mu.Lock()
	f.session = &herdr.AgentSession{Agent: "claude", Kind: "id", Value: "abc-123"}
	f.mu.Unlock()
	return nil
}
func (f *fakeHerdr) Snapshot(context.Context) (*herdr.Snapshot, error) { return f.snap, nil }
func (f *fakeHerdr) ClosePane(_ context.Context, pane string) error {
	f.record("close pane " + pane)
	return nil
}
func (f *fakeHerdr) CloseWorkspace(_ context.Context, ws string) error {
	f.record("close workspace " + ws)
	return nil
}
func (f *fakeHerdr) ReportAgentSession(_ context.Context, pane, agent, id string) error {
	f.record("report " + agent + " " + id)
	f.mu.Lock()
	f.session = &herdr.AgentSession{Agent: agent, Kind: "id", Value: id}
	f.mu.Unlock()
	return nil
}
func (f *fakeHerdr) Pane(context.Context, string) (*herdr.Pane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	agent := "claude"
	return &herdr.Pane{PaneID: "w9:p1", Agent: &agent, AgentStatus: f.status, AgentSession: f.session}, nil
}

type fakeProvider struct{ providers.Provider }

func (fakeProvider) Name() string       { return "claude" }
func (fakeProvider) HerdrAgent() string { return "claude" }
func (fakeProvider) LaunchArgs(o providers.LaunchOptions) []string {
	if o.Model == "" && o.Effort == "" {
		return []string{"--default"}
	}
	var args []string
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Effort != "" {
		args = append(args, "--effort", o.Effort)
	}
	return args
}
func (fakeProvider) ResumeArgs(id, _ string) []string { return []string{"--resume", id} }
func (fakeProvider) Models(context.Context) (providers.ModelCatalog, error) {
	return providers.ModelCatalog{
		Models:  []providers.ModelOption{{ID: "haiku", Name: "Haiku"}},
		Efforts: []providers.EffortOption{{ID: "high", Name: "High"}},
	}, nil
}
func (fakeProvider) StartupKeys(s string) []string {
	if strings.Contains(s, "trust this folder") {
		return []string{"down", "enter"}
	}
	return nil
}

func newLauncher(t *testing.T, fh *fakeHerdr) (*Launcher, string) {
	root, _ := setupRoots(t)
	return &Launcher{
		Herdr: fh, Roots: []string{root}, Providers: []providers.Provider{fakeProvider{}},
		StartTimeout: time.Second, PollInterval: 5 * time.Millisecond, IdentityWait: 200 * time.Millisecond,
	}, root
}

func Test信頼ダイアログを承認して起動しプロンプトを送る(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusBlocked, screen: "Yes, I trust this folder", startErr: &herdr.Error{Code: "agent_not_ready"}}
	l, root := newLauncher(t, fh)
	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: filepath.Join(root, "app-b"), Prompt: "hello", Model: "haiku", Effort: "high", Trust: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "claude:abc-123" || res.PaneID != "w9:p1" || res.Warning != "" {
		t.Errorf("result = %+v", res)
	}
	want := "create app-b|start claude --model haiku --effort high|keys down,enter|prompt hello"
	if strings.Join(fh.calls, "|") != want {
		t.Errorf("calls = %v", fh.calls)
	}
}

func Test信頼しない場合はダイアログに触れず警告を返す(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusBlocked, screen: "Yes, I trust this folder", startErr: &herdr.Error{Code: "agent_not_ready"}}
	l, root := newLauncher(t, fh)
	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root, Trust: false})
	if err != nil {
		t.Fatal(err)
	}
	if res.Warning == "" || res.SessionID != "" {
		t.Errorf("result = %+v", res)
	}
	for _, c := range fh.calls {
		if strings.HasPrefix(c, "keys") {
			t.Errorf("must not answer the dialog: %v", fh.calls)
		}
	}
}

func Test不正な起動リクエストは拒否する(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusIdle}
	l, root := newLauncher(t, fh)
	if _, err := l.Start(context.Background(), StartRequest{Provider: "gemini", Cwd: root}); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("provider err = %v", err)
	}
	if _, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: "/etc"}); !errors.Is(err, files.ErrForbidden) {
		t.Errorf("cwd err = %v", err)
	}
	for _, m := range []string{"--dangerously-skip-permissions", "a b", "x;rm"} {
		if _, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root, Model: m}); !errors.Is(err, ErrInvalidModel) {
			t.Errorf("model %q err = %v", m, err)
		}
	}
	for _, e := range []string{"--print", "a b", "High"} {
		if _, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root, Effort: e}); !errors.Is(err, ErrInvalidEffort) {
			t.Errorf("effort %q err = %v", e, err)
		}
	}
	if len(fh.calls) != 0 {
		t.Errorf("nothing should be created: %v", fh.calls)
	}
	// session id が報告されなくても起動自体は成功扱い
	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root})
	if err != nil || res.SessionID != "" || !strings.Contains(res.Warning, "integration") {
		t.Errorf("res = %+v err = %v", res, err)
	}
	if last := fh.calls[len(fh.calls)-1]; last != "start claude --default" {
		t.Errorf("default model start = %q", last)
	}
	if _, err := l.Models(context.Background(), "gemini"); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("models err = %v", err)
	}
}

func Test作成直後のシェルがbusyなら待って再試行する(t *testing.T) {
	busy := &herdr.Error{Code: "agent_pane_busy", Message: "not an available shell"}
	fh := &fakeHerdr{status: herdr.StatusIdle, startErrs: []error{busy, busy}}
	l, root := newLauncher(t, fh)
	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root, Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "claude:abc-123" {
		t.Errorf("result = %+v", res)
	}
	want := "create workspace|start claude --default|start claude --default|start claude --default|prompt hi"
	if got := strings.Join(fh.calls, "|"); got != want {
		t.Errorf("calls = %s", got)
	}
}

func Test起動に失敗したら作ったワークスペースを閉じる(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusIdle, startErr: &herdr.Error{Code: "agent_pane_busy"}}
	l, root := newLauncher(t, fh)
	l.StartTimeout = 30 * time.Millisecond
	if _, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root}); err == nil {
		t.Fatal("expected error")
	}
	if last := fh.calls[len(fh.calls)-1]; last != "close workspace w9" {
		t.Errorf("calls = %v", fh.calls)
	}

	fh = &fakeHerdr{status: herdr.StatusIdle, startErr: &herdr.Error{Code: "invalid_kind"}}
	l, root = newLauncher(t, fh)
	if _, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root}); err == nil {
		t.Fatal("expected error")
	}
	// busy 以外のエラーは再試行しない
	if got := strings.Join(fh.calls, "|"); got != "create workspace|start claude --default|close workspace w9" {
		t.Errorf("calls = %s", got)
	}
}

func Test既存セッションを作業ディレクトリで再開する(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusIdle, session: &herdr.AgentSession{Agent: "claude", Kind: "id", Value: "abc-123"}}
	l, root := newLauncher(t, fh)
	res, err := l.Resume(context.Background(), "claude", "abc-123", filepath.Join(root, "app-b"), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "claude:abc-123" {
		t.Errorf("result = %+v", res)
	}
	if got := strings.Join(fh.calls, "|"); got != "create app-b|start claude --resume abc-123" {
		t.Errorf("calls = %s", got)
	}
	if _, err := l.Resume(context.Background(), "claude", "abc-123", "", false); !errors.Is(err, ErrNoCwd) {
		t.Errorf("no cwd err = %v", err)
	}
	if _, err := l.Resume(context.Background(), "claude", "abc-123", "/etc", false); !errors.Is(err, files.ErrForbidden) {
		t.Errorf("outside err = %v", err)
	}
}

func Test停止はペインだけのワークスペースなら丸ごと閉じる(t *testing.T) {
	fh := &fakeHerdr{snap: &herdr.Snapshot{Panes: []herdr.Pane{
		{PaneID: "w1:p1", WorkspaceID: "w1"},
		{PaneID: "w2:p1", WorkspaceID: "w2"},
		{PaneID: "w2:p2", WorkspaceID: "w2"},
	}}}
	l, _ := newLauncher(t, fh)
	ctx := context.Background()
	for _, pane := range []string{"w1:p1", "w2:p2", "w9:p9"} {
		if err := l.Stop(ctx, pane); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(fh.calls, "|"); got != "close workspace w1|close pane w2:p2" {
		t.Errorf("calls = %s", got)
	}
}

type locatingProvider struct {
	fakeProvider
	cwd   string
	since time.Time
}

func (p *locatingProvider) LocateLaunched(_ context.Context, cwd string, since time.Time) string {
	p.cwd, p.since = cwd, since
	return "thread-9"
}

func Testフックが報告しないセッションはproviderが見つけてHerdrに報告する(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusIdle}
	l, root := newLauncher(t, fh)
	lp := &locatingProvider{}
	l.Providers = []providers.Provider{lp}
	before := time.Now()
	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root})
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "claude:thread-9" || res.Warning != "" {
		t.Errorf("result = %+v", res)
	}
	if lp.cwd != root || lp.since.After(before) {
		t.Errorf("locate args = %s %v", lp.cwd, lp.since)
	}
	if last := fh.calls[len(fh.calls)-1]; last != "report claude thread-9" {
		t.Errorf("calls = %v", fh.calls)
	}

	// 再開時は既知の id をそのまま報告する
	fh = &fakeHerdr{status: herdr.StatusIdle}
	l.Herdr = fh
	res, err = l.Resume(context.Background(), "claude", "known-1", root, false)
	if err != nil || res.SessionID != "claude:known-1" {
		t.Errorf("resume = %+v %v", res, err)
	}
}
