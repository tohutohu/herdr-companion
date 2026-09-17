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
	calls    []string
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
func (f *fakeHerdr) Pane(context.Context, string) (*herdr.Pane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	agent := "claude"
	return &herdr.Pane{PaneID: "w9:p1", Agent: &agent, AgentStatus: f.status, AgentSession: f.session}, nil
}

type fakeProvider struct{ providers.Provider }

func (fakeProvider) Name() string       { return "claude" }
func (fakeProvider) HerdrAgent() string { return "claude" }
func (fakeProvider) LaunchArgs(m string) []string {
	if m == "" {
		return []string{"--default"}
	}
	return []string{"--model", m}
}
func (fakeProvider) Models(context.Context) ([]providers.ModelOption, error) {
	return []providers.ModelOption{{ID: "haiku", Name: "Haiku"}}, nil
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
	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: filepath.Join(root, "app-b"), Prompt: "hello", Model: "haiku", Trust: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "claude:abc-123" || res.PaneID != "w9:p1" || res.Warning != "" {
		t.Errorf("result = %+v", res)
	}
	want := "create app-b|start claude --model haiku|keys down,enter|prompt hello"
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
