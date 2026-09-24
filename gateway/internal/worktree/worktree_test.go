package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo makes a repository with one commit and returns its real path.
func newRepo(t *testing.T) string {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(dir, "repo")
	os.Mkdir(repo, 0o755)
	run(t, repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi\n"), 0o644)
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "init")
	return repo
}

// addWorktree adds a worktree on a new branch (or detached when branch is
// empty) and returns its path.
func addWorktree(t *testing.T, repo, name, branch string) string {
	t.Helper()
	wt := filepath.Join(filepath.Dir(repo), "worktrees", name)
	if branch == "" {
		run(t, repo, "worktree", "add", "-q", "--detach", wt)
	} else {
		run(t, repo, "worktree", "add", "-q", "-b", branch, wt)
	}
	return wt
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func branches(t *testing.T, repo string) string {
	return run(t, repo, "branch", "--format=%(refname:short)")
}

var claude = Owner{Provider: "claude", NativeID: "s1"}

func Testゲートウェイが作ったクリーンなworktreeは削除しマージ済みのブランチも消す(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := addWorktree(t, repo, "a", "worktree/a")
	if err := Mark(ctx, wt, "claude"); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(wt, "sub"), 0o755)

	out, err := Cleanup(ctx, filepath.Join(wt, "sub"), claude, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || !out.Removed || out.Path != wt || out.Warning() != "" {
		t.Fatalf("outcome = %+v", out)
	}
	if exists(wt) {
		t.Error("worktree directory is still there")
	}
	if b := branches(t, repo); b != "main" {
		t.Errorf("branches = %q", b)
	}
}

func Testマーカーのないworktreeや通常のチェックアウトには触れない(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := addWorktree(t, repo, "mine", "mine")
	for _, dir := range []string{wt, repo, t.TempDir(), filepath.Join(repo, "missing")} {
		out, err := Cleanup(ctx, dir, claude, nil)
		if err != nil || out != nil {
			t.Errorf("%s: outcome = %+v, err = %v", dir, out, err)
		}
	}
	if !exists(wt) {
		t.Error("an unmarked worktree was removed")
	}
	if err := Mark(ctx, repo, "claude"); err == nil {
		t.Error("the main checkout must not be marked")
	}
}

func Test作業中の変更があるworktreeは理由を添えて残す(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := addWorktree(t, repo, "a", "worktree/a")
	Mark(ctx, wt, "claude")
	os.WriteFile(filepath.Join(wt, "new.txt"), []byte("x"), 0o644)

	out, err := Cleanup(ctx, wt, claude, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || out.Removed || out.Warning() != "Kept the worktree at "+wt+" because it has uncommitted changes." {
		t.Fatalf("outcome = %+v", out)
	}
	if !exists(filepath.Join(wt, "new.txt")) {
		t.Error("the uncommitted file is gone")
	}
}

func Test無視されたファイルだけならworktreeを削除する(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("build/\n"), 0o644)
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "ignore")
	wt := addWorktree(t, repo, "a", "worktree/a")
	Mark(ctx, wt, "opencode")
	os.MkdirAll(filepath.Join(wt, "build"), 0o755)
	os.WriteFile(filepath.Join(wt, "build", "out"), []byte("x"), 0o644)

	out, err := Cleanup(ctx, wt, Owner{Provider: "opencode"}, nil)
	if err != nil || out == nil || !out.Removed {
		t.Fatalf("outcome = %+v, err = %v", out, err)
	}
}

func Testマージされていないコミットのあるブランチはworktreeを消しても残す(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := addWorktree(t, repo, "a", "worktree/a")
	Mark(ctx, wt, "claude")
	os.WriteFile(filepath.Join(wt, "feature.txt"), []byte("x"), 0o644)
	run(t, wt, "add", ".")
	run(t, wt, "commit", "-qm", "feature")

	out, err := Cleanup(ctx, wt, claude, nil)
	if err != nil || out == nil || !out.Removed {
		t.Fatalf("outcome = %+v, err = %v", out, err)
	}
	if b := branches(t, repo); b != "main\nworktree/a" {
		t.Errorf("branches = %q", b)
	}
}

func Test別のペインが使っているworktreeは残す(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := addWorktree(t, repo, "a", "worktree/a")
	Mark(ctx, wt, "claude")
	os.MkdirAll(filepath.Join(wt, "sub"), 0o755)

	out, err := Cleanup(ctx, wt, claude, []string{repo, filepath.Join(wt, "sub")})
	if err != nil || out == nil || out.Removed || out.Reason != "another pane is still using it" {
		t.Fatalf("outcome = %+v, err = %v", out, err)
	}
	// 名前が前方一致するだけの別ディレクトリは使用中とみなさない
	out, err = Cleanup(ctx, wt, claude, []string{wt + "-other"})
	if err != nil || out == nil || !out.Removed {
		t.Fatalf("outcome = %+v, err = %v", out, err)
	}
}

func TestCodexのworktreeは所有スレッドのセッションでだけ削除する(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	bucket := filepath.Join(filepath.Dir(repo), "codex", "114f")
	wt := filepath.Join(bucket, "repo")
	run(t, repo, "worktree", "add", "-q", "--detach", wt)
	gitDir := run(t, wt, "rev-parse", "--path-format=absolute", "--git-dir")
	os.WriteFile(filepath.Join(gitDir, "codex-thread.json"), []byte(`{"version":1,"ownerThreadId":"th-1"}`), 0o600)

	for _, o := range []Owner{{Provider: "codex", NativeID: "th-2"}, {Provider: "claude", NativeID: "th-1"}} {
		if out, err := Cleanup(ctx, wt, o, nil); err != nil || out != nil {
			t.Fatalf("%+v: outcome = %+v, err = %v", o, out, err)
		}
	}
	out, err := Cleanup(ctx, wt, Owner{Provider: "codex", NativeID: "th-1"}, nil)
	if err != nil || out == nil || !out.Removed {
		t.Fatalf("outcome = %+v, err = %v", out, err)
	}
	if exists(bucket) {
		t.Error("the empty Codex bucket directory is still there")
	}
}

func Testブランチのないコミットが残るdetachedなworktreeは削除しない(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := addWorktree(t, repo, "d", "")
	Mark(ctx, wt, "codex")
	os.WriteFile(filepath.Join(wt, "f.txt"), []byte("x"), 0o644)
	run(t, wt, "add", ".")
	run(t, wt, "commit", "-qm", "detached work")

	out, err := Cleanup(ctx, wt, Owner{Provider: "codex", NativeID: "x"}, nil)
	if err != nil || out == nil || out.Removed || out.Reason != "it has commits that are not on any branch" {
		t.Fatalf("outcome = %+v, err = %v", out, err)
	}
}

func Test終了したClaudeのロックは外して削除し他のロックは残す(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	dead := addWorktree(t, repo, "dead", "worktree-dead")
	Mark(ctx, dead, "claude")
	// PID 1 以外で存在しないであろう大きな番号を使う
	run(t, repo, "worktree", "lock", "--reason", "claude session dead (pid 999999 start Thu Sep 24 02:04:24 2026)", dead)
	other := addWorktree(t, repo, "other", "worktree-other")
	Mark(ctx, other, "claude")
	run(t, repo, "worktree", "lock", "--reason", "on a USB drive", other)
	running := addWorktree(t, repo, "running", "worktree-running")
	Mark(ctx, running, "claude")
	run(t, repo, "worktree", "lock", "--reason", "claude session running (pid "+strconv.Itoa(os.Getpid())+" start now)", running)
	defer func(d time.Duration) { LockWait = d }(LockWait)
	LockWait = 0

	if out, err := Cleanup(ctx, dead, claude, nil); err != nil || out == nil || !out.Removed {
		t.Fatalf("dead: outcome = %+v, err = %v", out, err)
	}
	if out, err := Cleanup(ctx, other, claude, nil); err != nil || out == nil || out.Removed || out.Reason != "it is locked" {
		t.Fatalf("other: outcome = %+v, err = %v", out, err)
	}
	if out, err := Cleanup(ctx, running, claude, nil); err != nil || out == nil || out.Removed || out.Reason != "its agent is still running" {
		t.Fatalf("running: outcome = %+v, err = %v", out, err)
	}
}

func Testリンクされたworktreeからメインのチェックアウトを求める(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := addWorktree(t, repo, "a", "worktree/a")
	for _, dir := range []string{repo, wt} {
		if got, err := MainCheckout(ctx, dir); err != nil || got != repo {
			t.Errorf("MainCheckout(%s) = %q, %v", dir, got, err)
		}
	}
	if got, err := Toplevel(ctx, wt); err != nil || got != wt {
		t.Errorf("Toplevel = %q, %v", got, err)
	}
	if _, err := Toplevel(ctx, t.TempDir()); err == nil {
		t.Error("a plain directory must not be a repository")
	}
}
