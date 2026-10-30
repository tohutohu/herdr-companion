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
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/runtime"
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

// fakeProvider is a claude provider with a direct runtime that records
// what it was asked to start.
type fakeProvider struct {
	providers.Provider
	mu      sync.Mutex
	trusted bool
	fail    error
	starts  []providers.DirectStart
	alive   map[string]bool
}

func (*fakeProvider) Name() string { return "claude" }
func (*fakeProvider) Models(context.Context) (providers.ModelCatalog, error) {
	return providers.ModelCatalog{Models: []providers.ModelOption{{ID: "opus"}}}, nil
}
func (p *fakeProvider) FolderTrusted(string) bool { return p.trusted }
func (p *fakeProvider) DirectStart(_ context.Context, o providers.DirectStart) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return "", p.fail
	}
	p.starts = append(p.starts, o)
	id := o.Resume
	if id == "" {
		id = "abc-123"
	}
	if p.alive == nil {
		p.alive = map[string]bool{}
	}
	p.alive[id] = true
	return id, nil
}
func (*fakeProvider) DirectStop(context.Context, string) error              { return nil }
func (*fakeProvider) DirectInterrupt(context.Context, string) error         { return nil }
func (*fakeProvider) DirectSetModel(context.Context, string, string) error  { return nil }
func (*fakeProvider) DirectSetEffort(context.Context, string, string) error { return nil }
func (*fakeProvider) DirectSetMode(context.Context, string, string) error   { return nil }
func (*fakeProvider) SettingsApply() string                                 { return providers.ApplyNextRequest }
func (*fakeProvider) SetDirectListener(func(string))                        {}
func (p *fakeProvider) DirectSessions() []providers.DirectInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []providers.DirectInfo
	for id := range p.alive {
		out = append(out, providers.DirectInfo{NativeID: id, Alive: true})
	}
	return out
}

func newLauncher(t *testing.T, p *fakeProvider) (*Launcher, string) {
	t.Helper()
	root, _ := setupRoots(t)
	m, err := runtime.New(filepath.Join(t.TempDir(), "direct.json"), time.Hour, p)
	if err != nil {
		t.Fatal(err)
	}
	return &Launcher{Roots: []string{root}, Providers: []providers.Provider{p}, Direct: m, WorktreeDir: filepath.Join(t.TempDir(), "worktrees")}, root
}

func Test信頼済みのフォルダではそのまま起動し最初のメッセージを渡す(t *testing.T) {
	p := &fakeProvider{trusted: true}
	l, root := newLauncher(t, p)
	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root, Prompt: "hello", Model: "opus", Effort: "high", Mode: "plan", Images: []string{"/tmp/a.png"}})
	if err != nil || res.SessionID != "claude:abc-123" || res.TrustRequired || res.Warning != "" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	want := providers.DirectStart{Cwd: root, Model: "opus", Effort: "high", Mode: "plan", Prompt: model.Input{Text: "hello", Images: []string{"/tmp/a.png"}}}
	if len(p.starts) != 1 || p.starts[0].Cwd != want.Cwd || p.starts[0].Model != want.Model || p.starts[0].Effort != want.Effort ||
		p.starts[0].Mode != want.Mode || p.starts[0].Prompt.Text != "hello" || len(p.starts[0].Prompt.Images) != 1 {
		t.Fatalf("starts = %+v", p.starts)
	}
	if !l.Direct.Owns("claude:abc-123") {
		t.Error("the session is not recorded")
	}
}

func Test信頼していないフォルダでは確認を求め承認後に起動する(t *testing.T) {
	p := &fakeProvider{}
	l, root := newLauncher(t, p)
	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root, Prompt: "hello"})
	if err != nil || !res.TrustRequired || res.LaunchID == "" || res.SessionID != "" || len(p.starts) != 0 {
		t.Fatalf("res = %+v err = %v starts = %v", res, err, p.starts)
	}
	out, err := l.AnswerTrust(context.Background(), res.LaunchID, true)
	if err != nil || out.SessionID != "claude:abc-123" || len(p.starts) != 1 || p.starts[0].Prompt.Text != "hello" {
		t.Fatalf("out = %+v err = %v starts = %v", out, err, p.starts)
	}
	// 一度答えた起動は二度と進めない
	if _, err := l.AnswerTrust(context.Background(), res.LaunchID, true); !errors.Is(err, ErrNoPendingTrust) {
		t.Errorf("second answer err = %v", err)
	}
}

func Test信頼を断ると起動しない(t *testing.T) {
	p := &fakeProvider{}
	l, root := newLauncher(t, p)
	res, _ := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root})
	out, err := l.AnswerTrust(context.Background(), res.LaunchID, false)
	if err != nil || out.SessionID != "" || len(p.starts) != 0 {
		t.Fatalf("out = %+v err = %v starts = %v", out, err, p.starts)
	}
	// trust付きの起動は確認を求めない
	if res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: root, Trust: true}); err != nil || res.SessionID == "" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func Test不正な起動リクエストは拒否する(t *testing.T) {
	p := &fakeProvider{trusted: true}
	l, root := newLauncher(t, p)
	for _, name := range []string{"gemini", "opencode", "devin"} {
		if _, err := l.Start(context.Background(), StartRequest{Provider: name, Cwd: root}); !errors.Is(err, ErrUnknownProvider) {
			t.Errorf("provider %s err = %v", name, err)
		}
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
	if len(p.starts) != 0 {
		t.Errorf("nothing should be started: %v", p.starts)
	}
	if _, err := l.Models(context.Background(), "gemini"); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("models err = %v", err)
	}
	if cat, err := l.Models(context.Background(), "claude"); err != nil || len(cat.Models) != 1 {
		t.Errorf("models = %+v, %v", cat, err)
	}
}

func Test既存セッションを作業ディレクトリで再開する(t *testing.T) {
	p := &fakeProvider{}
	l, root := newLauncher(t, p)
	dir := filepath.Join(root, "app-b")
	res, err := l.Resume(context.Background(), "claude", "old-1", dir)
	if err != nil || res.SessionID != "claude:old-1" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if len(p.starts) != 1 || p.starts[0].Resume != "old-1" || p.starts[0].Cwd != dir {
		t.Fatalf("starts = %+v", p.starts)
	}
	if _, err := l.Resume(context.Background(), "claude", "old-1", ""); !errors.Is(err, ErrNoCwd) {
		t.Errorf("no cwd err = %v", err)
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
	out, err := exec.Command("git", "-C", wt, "rev-parse", "--git-dir").Output()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(strings.TrimSpace(string(out)), "herdr-companion.json"))
	return err == nil
}

func TestWorktreeを指定すると新しいworktreeで起動し印を付ける(t *testing.T) {
	p := &fakeProvider{trusted: true}
	l, root := newLauncher(t, p)
	repo := filepath.Join(root, "repo")
	os.Mkdir(repo, 0o755)
	gitRepo(t, repo)

	res, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: repo, Prompt: "hello", Worktree: true})
	if err != nil || res.SessionID != "claude:abc-123" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	wt := p.starts[0].Cwd
	if !strings.Contains(wt, "/worktrees/repo/claude-") {
		t.Errorf("worktree path = %s", wt)
	}
	if !marked(wt) {
		t.Error("the worktree is not marked as the gateway's")
	}
}

func TestGitリポジトリでない場所ではworktreeで起動しない(t *testing.T) {
	p := &fakeProvider{trusted: true}
	l, root := newLauncher(t, p)
	_, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: filepath.Join(root, "app-b"), Worktree: true})
	if !errors.Is(err, worktree.ErrNotRepository) || len(p.starts) != 0 {
		t.Fatalf("err = %v starts = %v", err, p.starts)
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

func Test起動に失敗したら作ったworktreeを削除する(t *testing.T) {
	p := &fakeProvider{trusted: true, fail: errors.New("sidecar missing")}
	l, root := newLauncher(t, p)
	repo := filepath.Join(root, "repo")
	os.Mkdir(repo, 0o755)
	gitRepo(t, repo)
	if _, err := l.Start(context.Background(), StartRequest{Provider: "claude", Cwd: repo, Worktree: true}); err == nil {
		t.Fatal("start succeeded")
	}
	out, _ := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if n := strings.Count(string(out), "worktree "); n != 1 {
		t.Errorf("worktrees left:\n%s", out)
	}
}

func Testルート外にあるworktreeのセッションもリポジトリがルート内なら再開できる(t *testing.T) {
	p := &fakeProvider{}
	l, root := newLauncher(t, p)
	repo := filepath.Join(root, "repo")
	os.Mkdir(repo, 0o755)
	gitRepo(t, repo)
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	wt := filepath.Join(outside, "worktrees", "repo-a")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "worktree/a", wt)

	res, err := l.Resume(context.Background(), "claude", "abc-123", wt)
	if err != nil || res.SessionID != "claude:abc-123" || p.starts[0].Cwd != wt {
		t.Fatalf("res = %+v err = %v starts = %v", res, err, p.starts)
	}
	// ルート外の普通のディレクトリは引き続き拒否する
	if _, err := l.Resume(context.Background(), "claude", "abc-123", outside); !errors.Is(err, files.ErrForbidden) {
		t.Errorf("outside err = %v", err)
	}
	// 削除済みのworktreeは分かるエラーにする
	if _, err := l.Resume(context.Background(), "claude", "abc-123", filepath.Join(root, "gone")); !errors.Is(err, ErrCwdGone) {
		t.Errorf("gone err = %v", err)
	}
}
