package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
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

// fakeHerdr removes worktrees the way Herdr does: `git worktree remove`
// without --force, through the workspace open in the worktree.
type fakeHerdr struct {
	open   map[string]bool // worktrees a workspace is open in
	fail   error           // returned by RemoveWorktree instead of removing
	paths  map[string]string
	calls  []string
	nextWS int
}

func (h *fakeHerdr) OpenWorktree(_ context.Context, path string) (string, bool, error) {
	h.calls = append(h.calls, "open "+path)
	if h.open[path] {
		return "w-open", true, nil
	}
	h.nextWS++
	ws := "w" + strconv.Itoa(h.nextWS)
	if h.paths == nil {
		h.paths = map[string]string{}
	}
	h.paths[ws] = path
	return ws, false, nil
}

func (h *fakeHerdr) RemoveWorktree(_ context.Context, ws string) error {
	h.calls = append(h.calls, "remove "+ws)
	if h.fail != nil {
		return h.fail
	}
	path := h.paths[ws]
	out, err := exec.Command("git", "-C", path, "worktree", "remove", path).CombinedOutput()
	if err != nil {
		return &herdr.Error{Code: herdr.ErrDirtyWorktree, Message: string(out)}
	}
	return nil
}

func (h *fakeHerdr) CloseWorkspace(_ context.Context, ws string) error {
	h.calls = append(h.calls, "close "+ws)
	return nil
}

func Testゲートウェイが作ったクリーンなworktreeはHerdrに削除させる(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := addWorktree(t, repo, "a", "worktree/a")
	if err := Mark(ctx, wt, "claude"); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(wt, "sub"), 0o755)
	h := &fakeHerdr{}

	out, err := Cleanup(ctx, h, filepath.Join(wt, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || !out.Removed || out.Path != wt || out.Warning() != "" {
		t.Fatalf("outcome = %+v", out)
	}
	if want := []string{"open " + wt, "remove w1"}; !slices.Equal(h.calls, want) {
		t.Errorf("calls = %q, want %q", h.calls, want)
	}
	if exists(wt) {
		t.Error("worktree directory is still there")
	}
}

func Testマーカーのないworktreeや通常のチェックアウトには触れない(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := addWorktree(t, repo, "mine", "mine")
	h := &fakeHerdr{}
	for _, dir := range []string{wt, repo, t.TempDir(), filepath.Join(repo, "missing"), ""} {
		out, err := Cleanup(ctx, h, dir)
		if err != nil || out != nil {
			t.Errorf("%s: outcome = %+v, err = %v", dir, out, err)
		}
	}
	if len(h.calls) != 0 {
		t.Errorf("calls = %q", h.calls)
	}
	if err := Mark(ctx, repo, "claude"); err == nil {
		t.Error("the main checkout must not be marked")
	}
}

func Test作業中の変更があるworktreeは開いたワークスペースを閉じて理由を添えて残す(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := addWorktree(t, repo, "a", "worktree/a")
	Mark(ctx, wt, "claude")
	os.WriteFile(filepath.Join(wt, "new.txt"), []byte("x"), 0o644)
	h := &fakeHerdr{}

	out, err := Cleanup(ctx, h, wt)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || out.Removed || out.Warning() != "Worktree kept because it has uncommitted changes: "+wt {
		t.Fatalf("outcome = %+v", out)
	}
	if want := []string{"open " + wt, "remove w1", "close w1"}; !slices.Equal(h.calls, want) {
		t.Errorf("calls = %q, want %q", h.calls, want)
	}
	if !exists(filepath.Join(wt, "new.txt")) {
		t.Error("the uncommitted file is gone")
	}
}

func TestHerdrのワークスペースが開いているworktreeは残す(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := addWorktree(t, repo, "a", "worktree/a")
	Mark(ctx, wt, "claude")
	h := &fakeHerdr{open: map[string]bool{wt: true}}

	out, err := Cleanup(ctx, h, wt)
	if err != nil || out == nil || out.Removed || out.Reason != "another pane is still using it" {
		t.Fatalf("outcome = %+v, err = %v", out, err)
	}
	if want := []string{"open " + wt}; !slices.Equal(h.calls, want) {
		t.Errorf("calls = %q, want %q", h.calls, want)
	}
}

func Test削除できなかったときは開いたワークスペースを閉じてエラーを返す(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := addWorktree(t, repo, "a", "worktree/a")
	Mark(ctx, wt, "claude")
	h := &fakeHerdr{fail: &herdr.Error{Code: "worktree_remove_failed", Message: "locked"}}

	out, err := Cleanup(ctx, h, wt)
	if err == nil || out != nil {
		t.Fatalf("outcome = %+v, err = %v", out, err)
	}
	if want := []string{"open " + wt, "remove w1", "close w1"}; !slices.Equal(h.calls, want) {
		t.Errorf("calls = %q, want %q", h.calls, want)
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
