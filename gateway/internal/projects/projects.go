// Package projects tells which project (favorite folder) a session or a
// resident task belongs to, and whether a notification for it should go out
// (docs/api.md#プロジェクト).
package projects

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/favorites"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// maxCached bounds the folders whose main checkout is remembered.
const maxCached = 4096

// Resolver maps working folders to projects.
type Resolver struct {
	Store *favorites.Store
	// MainCheckout returns the main checkout of the repository dir is in
	// (worktree.MainCheckout), so a session in a worktree belongs to the
	// project of its repository. Nil skips it.
	MainCheckout func(ctx context.Context, dir string) (string, error)

	mu sync.Mutex
	// mains caches MainCheckout per folder ("" when it has none): git is
	// slow next to a list read, and a folder's repository does not change.
	mains map[string]string
}

// Of returns the project dir belongs to: the registered folder that is dir
// or its deepest parent, else the one its repository's main checkout is in.
// ok is false for a folder in no project ("その他").
func (r *Resolver) Of(dir string) (favorites.Project, bool) {
	if r == nil || r.Store == nil || dir == "" {
		return favorites.Project{}, false
	}
	list := r.Store.Projects()
	if p, ok := match(list, filepath.Clean(dir)); ok {
		return p, true
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		if p, ok := match(list, real); ok {
			return p, true
		}
	}
	if main := r.mainCheckout(dir); main != "" {
		return match(list, main)
	}
	return favorites.Project{}, false
}

func (r *Resolver) mainCheckout(dir string) string {
	if r.MainCheckout == nil {
		return ""
	}
	r.mu.Lock()
	main, ok := r.mains[dir]
	r.mu.Unlock()
	if ok {
		return main
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	main, err := r.MainCheckout(ctx, dir)
	if err != nil {
		main = ""
	}
	if main == filepath.Clean(dir) {
		// Not a linked worktree: the direct match already covered it.
		main = ""
	}
	r.mu.Lock()
	if r.mains == nil || len(r.mains) >= maxCached {
		r.mains = map[string]string{}
	}
	r.mains[dir] = main
	r.mu.Unlock()
	return main
}

func match(list []favorites.Project, dir string) (favorites.Project, bool) {
	best, found := favorites.Project{}, false
	for _, p := range list {
		if (dir == p.Path || strings.HasPrefix(dir, p.Path+string(filepath.Separator))) && len(p.Path) > len(best.Path) {
			best, found = p, true
		}
	}
	return best, found
}

// Annotate fills in the session's project and notification level.
func (r *Resolver) Annotate(s *model.Session) {
	if p, ok := r.Of(s.Cwd); ok {
		s.ProjectPath = p.Path
		if p.Notify != favorites.NotifyAll {
			s.ProjectNotify = p.Notify
		}
	}
}

// Wants reports whether a notification of a kind should go out for
// something in project notify level level ("" is all). attention is true
// for what needs the user: a question, an approval or a failure.
func Wants(level string, attention bool) bool {
	switch level {
	case favorites.NotifyOff:
		return false
	case favorites.NotifyAttention:
		return attention
	}
	return true
}
