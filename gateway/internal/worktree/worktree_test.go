package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

// addWorktree adds a worktree on a new branch and returns its path.
func addWorktree(t *testing.T, repo, name, branch string) string {
	t.Helper()
	wt := filepath.Join(filepath.Dir(repo), "worktrees", name)
	run(t, repo, "worktree", "add", "-q", "-b", branch, wt)
	return wt
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func Test1系がHerdr起動用に作ったworktreeもgitで削除する(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := addWorktree(t, repo, "a", "worktree/a")
	if err := mark(ctx, wt, "claude", "herdr"); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(wt, "sub"), 0o755)

	out, err := Cleanup(ctx, filepath.Join(wt, "sub"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || !out.Removed || out.Path != wt || out.Warning() != "" {
		t.Fatalf("outcome = %+v", out)
	}
	if exists(wt) {
		t.Error("worktree directory is still there")
	}
	if got := run(t, repo, "branch", "--list", "worktree/a"); got == "" {
		t.Error("the branch must be kept")
	}
}

func Testマーカーのないworktreeや通常のチェックアウトには触れない(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := addWorktree(t, repo, "mine", "mine")
	for _, dir := range []string{wt, repo, t.TempDir(), filepath.Join(repo, "missing"), ""} {
		out, err := Cleanup(ctx, dir, nil)
		if err != nil || out != nil {
			t.Errorf("%s: outcome = %+v, err = %v", dir, out, err)
		}
	}
	if !exists(wt) {
		t.Error("an unmarked worktree was removed")
	}
	if err := mark(ctx, repo, "claude", "direct"); err == nil {
		t.Error("the main checkout must not be marked")
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
