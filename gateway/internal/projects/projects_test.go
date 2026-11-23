package projects

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/favorites"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

func newResolver(t *testing.T, paths ...string) (*Resolver, *favorites.Store) {
	t.Helper()
	st, err := favorites.Open(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Set(paths); err != nil {
		t.Fatal(err)
	}
	return &Resolver{Store: st}, st
}

func Testフォルダは登録したうちで最も深いプロジェクトに属する(t *testing.T) {
	r, _ := newResolver(t, "/w/bot", "/w/bot/sub", "/w/klon")
	for dir, want := range map[string]string{
		"/w/bot":           "/w/bot",
		"/w/bot/src/x":     "/w/bot",
		"/w/bot/sub/a":     "/w/bot/sub",
		"/w/klon":          "/w/klon",
		"/w/bot-two":       "",
		"/w":               "",
		"":                 "",
		"/w/klon/../bot/a": "/w/bot",
	} {
		p, ok := r.Of(dir)
		if ok != (want != "") || p.Path != want {
			t.Errorf("Of(%q) = %q, %v; want %q", dir, p.Path, ok, want)
		}
	}
}

func TestWorktreeのセッションは元のリポジトリのプロジェクトに属し結果を覚える(t *testing.T) {
	r, _ := newResolver(t, "/w/bot")
	calls := 0
	r.MainCheckout = func(_ context.Context, dir string) (string, error) {
		calls++
		switch dir {
		case "/state/worktrees/bot-1a2b":
			return "/w/bot", nil
		case "/tmp/plain":
			return "", errors.New("not a git repository")
		}
		return dir, nil
	}
	for range 2 {
		if p, ok := r.Of("/state/worktrees/bot-1a2b"); !ok || p.Path != "/w/bot" {
			t.Fatalf("worktree = %+v, %v", p, ok)
		}
		if _, ok := r.Of("/tmp/plain"); ok {
			t.Fatal("plain folder has a project")
		}
	}
	if calls != 2 {
		t.Fatalf("git ran %d times, want 2 (cached)", calls)
	}
}

func Testシンボリックリンク越しのフォルダも実体のプロジェクトに属する(t *testing.T) {
	dir := t.TempDir()
	real, _ := filepath.EvalSymlinks(dir)
	os.MkdirAll(filepath.Join(real, "proj", "src"), 0o755)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(real, "proj"), link); err != nil {
		t.Skip(err)
	}
	r, _ := newResolver(t, filepath.Join(real, "proj"))
	if p, ok := r.Of(filepath.Join(link, "src")); !ok || p.Path != filepath.Join(real, "proj") {
		t.Fatalf("Of = %+v, %v", p, ok)
	}
}

func Testセッションにプロジェクトと絞った通知の段階を付ける(t *testing.T) {
	r, st := newResolver(t, "/w/bot", "/w/klon")
	off := favorites.NotifyOff
	if _, err := st.Update("/w/bot", nil, &off); err != nil {
		t.Fatal(err)
	}
	bot := model.Session{Cwd: "/w/bot/x"}
	r.Annotate(&bot)
	klon := model.Session{Cwd: "/w/klon"}
	r.Annotate(&klon)
	other := model.Session{Cwd: "/w/else"}
	r.Annotate(&other)
	if bot.ProjectPath != "/w/bot" || bot.ProjectNotify != "off" || klon.ProjectPath != "/w/klon" || klon.ProjectNotify != "" || other.ProjectPath != "" {
		t.Fatalf("bot=%+v klon=%+v other=%+v", bot, klon, other)
	}
}

func Test通知の段階ごとに送るものを決める(t *testing.T) {
	cases := []struct {
		level     string
		attention bool
		want      bool
	}{
		{"", false, true}, {"", true, true}, {"all", false, true},
		{"attention", false, false}, {"attention", true, true},
		{"off", false, false}, {"off", true, false},
	}
	for _, c := range cases {
		if got := Wants(c.level, c.attention); got != c.want {
			t.Errorf("Wants(%q, %v) = %v", c.level, c.attention, got)
		}
	}
}
