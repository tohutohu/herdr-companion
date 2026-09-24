package launcher

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/files"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/worktree"
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

	roots, err := l.List(context.Background(), "")
	if err != nil || len(roots.Entries) != 1 || roots.Entries[0].Path != root {
		t.Fatalf("roots = %+v, %v", roots, err)
	}
	got, err := l.List(context.Background(), root)
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
	sub, _ := l.List(context.Background(), filepath.Join(root, "App-a"))
	if sub.Parent != root {
		t.Errorf("parent = %q", sub.Parent)
	}
	for _, p := range []string{outside, filepath.Join(root, "escape"), root + "/../secret", "workspace"} {
		if _, err := l.List(context.Background(), p); !errors.Is(err, files.ErrForbidden) {
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
	// worktree is the checkout CreateWorktree reports; onStart runs in
	// StartAgent (e.g. to create the worktree an agent would).
	worktree string
	onStart  func()
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
func (f *fakeHerdr) CreateWorktree(_ context.Context, cwd, label string) (string, string, string, error) {
	f.record("worktree " + label)
	return "w9", "w9:p1", f.worktree, nil
}
func (f *fakeHerdr) StartAgent(_ context.Context, name, kind, pane string, args []string, _ time.Duration) error {
	f.record("start " + kind + " " + strings.Join(args, " "))
	if f.onStart != nil {
		f.onStart()
	}
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

type launchPromptProvider struct {
	fakeProvider
	herdr  *fakeHerdr
	prompt string
}

func (p *launchPromptProvider) SendLaunchPrompt(_ context.Context, _ string, text string) error {
	p.prompt = text
	p.herdr.mu.Lock()
	p.herdr.session = &herdr.AgentSession{Agent: "claude", Kind: "id", Value: "abc-123"}
	p.herdr.mu.Unlock()
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

func Testプロバイダが初回promptの入力経路を選べる(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusIdle}
	l, root := newLauncher(t, fh)
	p := &launchPromptProvider{fakeProvider: fakeProvider{}, herdr: fh}
	l.Providers = []providers.Provider{p}

	res, err := l.Start(context.Background(), StartRequest{
		Provider: "claude",
		Cwd:      filepath.Join(root, "app-b"),
		Prompt:   "abc123",
		Trust:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "claude:abc-123" || p.prompt != "abc123" {
		t.Errorf("result = %+v, prompt = %q", res, p.prompt)
	}
	if got := strings.Join(fh.calls, "|"); strings.Contains(got, "prompt abc123") {
		t.Errorf("must use provider launch input path: %s", got)
	}
}

func Test信頼しない場合はダイアログで止めて回答を待つ(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusBlocked, screen: "Yes, I trust this folder", startErr: &herdr.Error{Code: "agent_not_ready"}}
	l, root := newLauncher(t, fh)
	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: filepath.Join(root, "app-b"), Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TrustRequired || res.PaneID != "w9:p1" || res.Warning != "" || res.SessionID != "" {
		t.Errorf("result = %+v", res)
	}
	if got := strings.Join(fh.calls, "|"); got != "create app-b|start claude --default" {
		t.Errorf("must not answer the dialog yet: %s", got)
	}

	res, err = l.AnswerTrust(context.Background(), "w9:p1", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "claude:abc-123" || res.TrustRequired || res.Warning != "" {
		t.Errorf("answered result = %+v", res)
	}
	want := "create app-b|start claude --default|keys down,enter|prompt hello"
	if got := strings.Join(fh.calls, "|"); got != want {
		t.Errorf("calls = %s", got)
	}
	// 回答は一度きり
	if _, err := l.AnswerTrust(context.Background(), "w9:p1", true); !errors.Is(err, ErrNoPendingTrust) {
		t.Errorf("second answer err = %v", err)
	}
}

func Test信頼を断るとワークスペースを閉じる(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusBlocked, screen: "Yes, I trust this folder", startErr: &herdr.Error{Code: "agent_not_ready"}}
	l, root := newLauncher(t, fh)
	if res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root, Prompt: "hello"}); err != nil || !res.TrustRequired {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if _, err := l.AnswerTrust(context.Background(), "w9:p1", false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fh.calls, "|"); got != "create workspace|start claude --default|close workspace w9" {
		t.Errorf("calls = %s", got)
	}
	if _, err := l.AnswerTrust(context.Background(), "w9:p2", true); !errors.Is(err, ErrNoPendingTrust) {
		t.Errorf("unknown pane err = %v", err)
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
	for _, m := range []string{"--permission-mode", "accept edits", "plan;rm"} {
		if _, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root, Mode: m}); !errors.Is(err, ErrInvalidMode) {
			t.Errorf("mode %q err = %v", m, err)
		}
	}
	if len(fh.calls) != 0 {
		t.Errorf("nothing should be created: %v", fh.calls)
	}
	// 初期プロンプトなしではID待ちをせず、起動自体を成功扱いにする
	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root})
	if err != nil || res.SessionID != "" || res.Warning != "" {
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
	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root})
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "" || res.Warning != "" {
		t.Errorf("result = %+v", res)
	}
	if lp.cwd != "" || !lp.since.IsZero() {
		t.Errorf("locate should wait for the first prompt: %s %v", lp.cwd, lp.since)
	}
	if last := fh.calls[len(fh.calls)-1]; last != "start claude --default" {
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

func Test未知の起動ダイアログから端末操作後に続行できる(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusBlocked, screen: "Update now?", startErr: &herdr.Error{Code: "agent_not_ready"}}
	l, root := newLauncher(t, fh)
	l.StartTimeout = 25 * time.Millisecond
	ctx := context.Background()
	res, err := l.Start(ctx, StartRequest{Provider: "claude", Cwd: filepath.Join(root, "app-b"), Prompt: "hello"})
	if err != nil || res.Warning == "" || res.SessionID != "" {
		t.Fatalf("%+v %v", res, err)
	}
	if pane, err := l.TerminalPane(ctx, res.PaneID); err != nil || pane != res.PaneID {
		t.Fatal(pane, err)
	}
	if _, err := l.TerminalPane(ctx, "unrelated"); !errors.Is(err, providers.ErrNotFound) {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(fh.calls, "|"), "prompt ") {
		t.Fatal("sent before readiness")
	}
	// The terminal handles the update prompt, then a separate trust prompt appears.
	fh.screen = "trust this folder"
	res, err = l.Continue(ctx, res.PaneID)
	if err != nil || !res.TrustRequired {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = l.AnswerTrust(ctx, res.PaneID, true)
	if err != nil || res.SessionID != "claude:abc-123" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err = l.Continue(ctx, res.PaneID); !errors.Is(err, ErrNoPendingTrust) {
		t.Fatal(err)
	}
	if strings.Count(strings.Join(fh.calls, "|"), "prompt hello") != 1 {
		t.Fatal(fh.calls)
	}
}

func TestセッションID待ちの再試行でプロンプトを再送しない(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusIdle}
	l, root := newLauncher(t, fh)
	// Identity belongs to a different provider, so the launcher keeps waiting.
	p := fakeProvider{}
	pl := &pendingLaunch{p: p, lp: p, cwd: root, prompt: "hello", started: time.Now()}
	l.IdentityWait = time.Nanosecond
	res := l.finishPending(context.Background(), "w9:p1", pl)
	if res.SessionID != "" || res.Warning == "" {
		t.Fatal(res)
	}
	l.IdentityWait = time.Second
	res, err := l.Continue(context.Background(), "w9:p1")
	if err != nil || res.SessionID != "claude:abc-123" {
		t.Fatal(res, err)
	}
	if strings.Count(strings.Join(fh.calls, "|"), "prompt hello") != 1 {
		t.Fatal(fh.calls)
	}
}

type preparedProvider struct {
	locatingProvider
	fail    error
	options providers.LaunchOptions
}

func (p *preparedProvider) PrepareLaunch(_ context.Context, options providers.LaunchOptions) ([]string, string, error) {
	p.options = options
	return []string{"--session", "prepared-1"}, "prepared-1", p.fail
}
func Test準備済みのネイティブIDを起動とHerdrへの報告に使う(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusIdle}
	l, root := newLauncher(t, fh)
	p := &preparedProvider{}
	l.Providers = []providers.Provider{p}
	result, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root, Model: "provider/model"})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID != "claude:prepared-1" || p.options.Model != "provider/model" || p.options.Cwd != root {
		t.Fatalf("%+v %+v", result, p.options)
	}
	calls := strings.Join(fh.calls, "|")
	if !strings.Contains(calls, "start claude --session prepared-1") || !strings.Contains(calls, "report claude prepared-1") {
		t.Fatal(calls)
	}
	if p.cwd != "" {
		t.Fatal("searched for a different session despite a known id")
	}
	fh.calls = nil
	p.fail = errors.New("prepare failed")
	if _, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root}); err == nil || len(fh.calls) != 0 {
		t.Fatal("started a pane despite failed preparation")
	}
}

// modeProvider stands for Codex, whose mode is set in the live TUI.
type modeProvider struct {
	fakeProvider
	modes []string
	fail  error
}

func (p *modeProvider) SetLaunchMode(_ context.Context, paneID, mode string) error {
	p.modes = append(p.modes, paneID+" "+mode)
	return p.fail
}

func Testモード引数を持たないエージェントは最初のプロンプトの前にTUIで設定する(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusIdle}
	l, root := newLauncher(t, fh)
	p := &modeProvider{}
	l.Providers = []providers.Provider{p}
	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root, Prompt: "hello", Mode: "plan"})
	if err != nil || res.Warning != "" {
		t.Fatalf("%+v %v", res, err)
	}
	if want := []string{"w9:p1 plan"}; len(p.modes) != 1 || p.modes[0] != want[0] {
		t.Fatalf("mode calls = %v", p.modes)
	}
	// LaunchArgs stays untouched: the mode never reaches the command line.
	if calls := strings.Join(fh.calls, "|"); !strings.Contains(calls, "start claude --default|prompt hello") {
		t.Fatalf("calls = %s", calls)
	}
}

func Testモード設定に失敗しても起動は続ける(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusIdle}
	l, root := newLauncher(t, fh)
	p := &modeProvider{fail: errors.New("pane is gone")}
	l.Providers = []providers.Provider{p}
	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root, Prompt: "hello", Mode: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "claude:abc-123" || !strings.Contains(res.Warning, "pane is gone") {
		t.Fatalf("res = %+v", res)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitRepo turns dir into a repository with one commit.
func gitRepo(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644)
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-qm", "init")
}

func marked(wt string) bool {
	_, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(wt)), "repo", ".git", "worktrees", filepath.Base(wt), "herdr-companion.json"))
	return err == nil
}

// worktreeProvider stands for an agent that creates its own worktree.
type worktreeProvider struct {
	fakeProvider
	name string
}

func (p *worktreeProvider) WorktreeArgs(o providers.LaunchOptions, name string) ([]string, bool) {
	p.name = name
	return append(p.LaunchArgs(o), "--worktree", name), true
}

func (p *worktreeProvider) StartFailure(screen string) error {
	if strings.Contains(screen, "trust not yet accepted") {
		return errors.New("trust the folder first")
	}
	return nil
}

func Test自前でworktreeを作れないエージェントはHerdrが作ったworktreeで起動する(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusIdle}
	l, root := newLauncher(t, fh)
	repo := filepath.Join(root, "repo")
	os.Mkdir(repo, 0o755)
	gitRepo(t, repo)
	wt := filepath.Join(root, "wt", "repo-a")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "worktree/a", wt)
	fh.worktree = wt

	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: repo, Prompt: "hello", Worktree: true})
	if err != nil || res.SessionID != "claude:abc-123" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	// ワークスペースはHerdrのworktreeで開き、別途作らない
	if got := strings.Join(fh.calls, "|"); got != "worktree repo|start claude --default|prompt hello" {
		t.Errorf("calls = %s", got)
	}
	if !marked(wt) {
		t.Error("the worktree is not marked as the gateway's")
	}
}

func Test自前でworktreeを作れるエージェントには自前のworktreeを作らせて印を付ける(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusIdle}
	l, root := newLauncher(t, fh)
	repo := filepath.Join(root, "My Repo")
	os.Mkdir(repo, 0o755)
	gitRepo(t, repo)
	p := &worktreeProvider{}
	l.Providers = []providers.Provider{p}
	var wt string
	// エージェントはHerdrに起動を報告されたあとでworktreeを作ることがある
	fh.onStart = func() {
		wt = filepath.Join(repo, ".claude", "worktrees", p.name)
		go func() {
			time.Sleep(20 * time.Millisecond)
			gitRun(t, repo, "worktree", "add", "-q", "-b", "worktree-"+p.name, wt)
		}()
	}

	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: repo, Model: "haiku", Worktree: true})
	if err != nil || res.PaneID != "w9:p1" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if !strings.HasPrefix(p.name, "My-Repo-") || len(p.name) != len("My-Repo-")+6 {
		t.Errorf("worktree name = %q", p.name)
	}
	if got := strings.Join(fh.calls, "|"); got != "create My Repo|start claude --model haiku --worktree "+p.name {
		t.Errorf("calls = %s", got)
	}
	gitDir := filepath.Join(repo, ".git", "worktrees", p.name, "herdr-companion.json")
	if _, err := os.Stat(gitDir); err != nil {
		t.Errorf("the agent's worktree is not marked: %v", err)
	}
}

func TestGitリポジトリでない場所ではworktreeで起動しない(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusIdle}
	l, root := newLauncher(t, fh)
	_, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: filepath.Join(root, "app-b"), Worktree: true})
	if !errors.Is(err, worktree.ErrNotRepository) || len(fh.calls) != 0 {
		t.Fatalf("err = %v calls = %v", err, fh.calls)
	}
	// 一覧でもworktreeを選べるディレクトリかどうかを返す
	repo := filepath.Join(root, "repo")
	os.MkdirAll(filepath.Join(repo, "sub"), 0o755)
	gitRepo(t, repo)
	for dir, want := range map[string]bool{repo: true, filepath.Join(repo, "sub"): true, filepath.Join(root, "app-b"): false} {
		if got, err := l.List(context.Background(), dir); err != nil || got.Git != want {
			t.Errorf("%s: git = %v, %v", dir, got.Git, err)
		}
	}
}

func Test信頼を断るとHerdrが作ったworktreeも削除する(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusBlocked, screen: "Yes, I trust this folder", startErr: &herdr.Error{Code: "agent_not_ready"}}
	l, root := newLauncher(t, fh)
	repo := filepath.Join(root, "repo")
	os.Mkdir(repo, 0o755)
	gitRepo(t, repo)
	wt := filepath.Join(root, "wt", "repo-a")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "worktree/a", wt)
	fh.worktree = wt

	if res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: repo, Worktree: true}); err != nil || !res.TrustRequired {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if _, err := l.AnswerTrust(context.Background(), "w9:p1", false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fh.calls, "|"); got != "worktree repo|start claude --default|close workspace w9" {
		t.Errorf("calls = %s", got)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree still exists: %v", err)
	}
}

func Testエージェントが起動を拒んだ理由を画面から伝える(t *testing.T) {
	fh := &fakeHerdr{screen: "Error creating worktree: Workspace trust not yet accepted.", startErr: &herdr.Error{Code: "timeout"}}
	l, root := newLauncher(t, fh)
	repo := filepath.Join(root, "repo")
	os.Mkdir(repo, 0o755)
	gitRepo(t, repo)
	l.Providers = []providers.Provider{&worktreeProvider{}}

	_, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: repo, Worktree: true})
	if !errors.Is(err, ErrStartRefused) || !strings.Contains(err.Error(), "trust the folder first") {
		t.Fatalf("err = %v", err)
	}
	if got := strings.Join(fh.calls, "|"); !strings.HasSuffix(got, "|close workspace w9") {
		t.Errorf("calls = %s", got)
	}
}

func Testルート外にあるworktreeのセッションもリポジトリがルート内なら再開できる(t *testing.T) {
	fh := &fakeHerdr{status: herdr.StatusIdle, session: &herdr.AgentSession{Agent: "claude", Kind: "id", Value: "abc-123"}}
	l, root := newLauncher(t, fh)
	repo := filepath.Join(root, "repo")
	os.Mkdir(repo, 0o755)
	gitRepo(t, repo)
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	wt := filepath.Join(outside, "worktrees", "repo-a")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "worktree/a", wt)

	res, err := l.Resume(context.Background(), "claude", "abc-123", wt, true)
	if err != nil || res.SessionID != "claude:abc-123" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if got := strings.Join(fh.calls, "|"); got != "create repo-a|start claude --resume abc-123" {
		t.Errorf("calls = %s", got)
	}
	// ルート外の普通のディレクトリは引き続き拒否する
	if _, err := l.Resume(context.Background(), "claude", "abc-123", outside, true); !errors.Is(err, files.ErrForbidden) {
		t.Errorf("outside err = %v", err)
	}
	// 削除済みのworktreeは分かるエラーにする
	if _, err := l.Resume(context.Background(), "claude", "abc-123", filepath.Join(root, "gone"), true); !errors.Is(err, ErrCwdGone) {
		t.Errorf("gone err = %v", err)
	}
}
