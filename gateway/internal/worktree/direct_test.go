package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test直接管理のworktreeを作り削除できる(t *testing.T) {
	repo := newRepo(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	ctx := context.Background()
	wt, err := Create(ctx, repo, base, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(wt, filepath.Join(base, "repo", "claude-")) || !exists(filepath.Join(wt, "README.md")) {
		t.Fatalf("worktree at %s", wt)
	}
	if branch := run(t, wt, "branch", "--show-current"); branch != "worktree/"+filepath.Base(wt) {
		t.Errorf("branch = %s", branch)
	}
	out, err := Cleanup(ctx, wt, nil)
	if err != nil || out == nil || !out.Removed || exists(wt) {
		t.Fatalf("cleanup = %+v, %v", out, err)
	}
	// ブランチは残すのでコミットは失われない
	if !strings.Contains(run(t, repo, "branch", "--list", "worktree/*"), filepath.Base(wt)) {
		t.Error("branch removed")
	}
}

func Test未コミットや未追跡の変更があるworktreeは削除しない(t *testing.T) {
	repo := newRepo(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	ctx := context.Background()
	for _, change := range []func(wt string){
		func(wt string) { os.WriteFile(filepath.Join(wt, "new.txt"), []byte("x"), 0o644) },
		func(wt string) { os.WriteFile(filepath.Join(wt, "README.md"), []byte("changed"), 0o644) },
	} {
		wt, err := Create(ctx, repo, base, "codex")
		if err != nil {
			t.Fatal(err)
		}
		change(wt)
		out, err := Cleanup(ctx, wt, nil)
		if err != nil || out == nil || out.Removed || !strings.Contains(out.Reason, "uncommitted") || !exists(wt) {
			t.Fatalf("cleanup = %+v, %v", out, err)
		}
	}
}

func Test他のセッションが使っているworktreeは削除しない(t *testing.T) {
	repo := newRepo(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	ctx := context.Background()
	wt, _ := Create(ctx, repo, base, "claude")
	out, err := Cleanup(ctx, wt, func(path string) bool { return path == wt })
	if err != nil || out == nil || out.Removed || !strings.Contains(out.Reason, "another session") {
		t.Fatalf("cleanup = %+v, %v", out, err)
	}
	if out, _ := Cleanup(ctx, repo, nil); out != nil {
		t.Errorf("main checkout: %+v", out)
	}
}
