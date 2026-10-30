// Package launcher starts and resumes the sessions the gateway runs, and lets
// the app pick or create their working directory under configured workspace
// roots.
package launcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/tohutohu/herdr-android-client/gateway/internal/files"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/runtime"
	"github.com/tohutohu/herdr-android-client/gateway/internal/worktree"
)

var (
	ErrInvalidName     = errors.New("invalid directory name")
	ErrUnknownProvider = errors.New("unknown provider")
	ErrInvalidModel    = errors.New("invalid model")
	ErrInvalidEffort   = errors.New("invalid effort")
	ErrInvalidMode     = errors.New("invalid mode")
	ErrNoCwd           = errors.New("session has no known working directory")
	ErrNoPendingTrust  = errors.New("no launch is waiting on a trust answer under this id")
	ErrCwdGone         = errors.New("the session's working directory no longer exists")
)

type Launcher struct {
	Roots     []string
	Providers []providers.Provider
	// Direct runs the sessions.
	Direct *runtime.Manager
	// WorktreeDir is where worktrees for new sessions are made.
	WorktreeDir string

	mu sync.Mutex
	// pending holds starts waiting on the folder-trust answer, by launch id,
	// until they are answered or the gateway restarts. Entries are small, so
	// unanswered ones are simply kept.
	pending map[string]*StartRequest
}

// DefaultRoots returns ~/workspace when it exists, else the home directory.
func DefaultRoots() []string {
	home, _ := os.UserHomeDir()
	ws := filepath.Join(home, "workspace")
	if st, err := os.Stat(ws); err == nil && st.IsDir() {
		return []string{ws}
	}
	return []string{home}
}

type Dir struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Listing struct {
	Path    string `json:"path"`             // empty for the list of roots
	Parent  string `json:"parent,omitempty"` // empty at a root
	Entries []Dir  `json:"entries"`
	// Git means Path is in a git working tree, so a session can start in a
	// new worktree of it.
	Git bool `json:"git,omitempty"`
}

func (l *Launcher) rootFor(real string) (string, bool) {
	for _, r := range l.Roots {
		rr, err := filepath.EvalSymlinks(r)
		if err != nil {
			continue
		}
		if real == rr {
			return rr, true
		}
	}
	return "", false
}

// resolveDir returns the real path of an absolute directory inside a root.
func (l *Launcher) resolveDir(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", files.ErrForbidden
	}
	real, err := files.Resolve(l.Roots, path)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", files.ErrNotFound
	}
	return real, nil
}

// ResolveDir validates an absolute working directory using the launch restrictions.
func (l *Launcher) ResolveDir(path string) (string, error) { return l.resolveDir(path) }

// List lists the roots (path == "") or the visible subdirectories of path.
func (l *Launcher) List(ctx context.Context, path string) (*Listing, error) {
	if path == "" {
		out := &Listing{}
		for _, r := range l.Roots {
			if real, err := filepath.EvalSymlinks(r); err == nil {
				out.Entries = append(out.Entries, Dir{Name: real, Path: real})
			}
		}
		return out, nil
	}
	real, err := l.resolveDir(path)
	if err != nil {
		return nil, err
	}
	des, err := os.ReadDir(real)
	if err != nil {
		return nil, err
	}
	out := &Listing{Path: real, Entries: []Dir{}}
	_, err = worktree.Toplevel(ctx, real)
	out.Git = err == nil
	if _, isRoot := l.rootFor(real); !isRoot {
		out.Parent = filepath.Dir(real)
	}
	for _, de := range des {
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		p := filepath.Join(real, de.Name())
		if _, err := l.resolveDir(p); err == nil { // skips files and links leaving the roots
			out.Entries = append(out.Entries, Dir{Name: de.Name(), Path: p})
		}
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	return out, nil
}

// Mkdir creates parent/name. Names are single path segments.
func (l *Launcher) Mkdir(parent, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || len(name) > 100 ||
		strings.ContainsAny(name, "/\\\x00") || strings.HasPrefix(name, ".") {
		return "", ErrInvalidName
	}
	real, err := l.resolveDir(parent)
	if err != nil {
		return "", err
	}
	p := filepath.Join(real, name)
	if err := os.Mkdir(p, 0o755); err != nil {
		return "", err
	}
	return p, nil
}

type StartRequest struct {
	Provider string `json:"provider"`
	Cwd      string `json:"cwd"`
	Prompt   string `json:"prompt"`
	// Model is a model id from Models; empty uses the agent's default.
	Model string `json:"model,omitempty"`
	// Effort is an effort id from Models; empty uses the agent's default.
	Effort string `json:"effort,omitempty"`
	// Mode is a mode id from Models; empty starts in the agent's own mode.
	Mode string `json:"mode,omitempty"`
	// Trust starts in a folder the agent was never trusted in without
	// asking. Without it such a start returns TrustRequired.
	Trust bool `json:"trust"`
	// Worktree starts the session in a new git worktree of Cwd's repository.
	Worktree bool `json:"worktree,omitempty"`
	// Images and Files are attachments of the first prompt, as local paths
	// the API has already resolved from upload ids.
	Images []string `json:"-"`
	Files  []string `json:"-"`
}

type StartResult struct {
	SessionID string `json:"sessionId,omitempty"`
	// LaunchID names a start waiting on the folder-trust answer.
	LaunchID string `json:"launchId,omitempty"`
	// Warning explains a partial start (the first message was not delivered).
	Warning string `json:"warning,omitempty"`
	// TrustRequired means the folder was never trusted in the agent; the
	// start goes ahead when the app answers with AnswerTrust.
	TrustRequired bool `json:"trustRequired,omitempty"`
}

func (l *Launcher) provider(name string) (providers.Provider, error) {
	for _, p := range l.Providers {
		if p.Name() == name && l.Direct != nil && l.Direct.Supports(name) {
			return p, nil
		}
	}
	return nil, ErrUnknownProvider
}

// Models lists the models and efforts a provider offers for new sessions.
func (l *Launcher) Models(ctx context.Context, provider string) (providers.ModelCatalog, error) {
	p, err := l.provider(provider)
	if err != nil {
		return providers.ModelCatalog{}, err
	}
	ml, ok := p.(providers.ModelLister)
	if !ok {
		return providers.ModelCatalog{}, nil
	}
	return ml.Models(ctx)
}

func launchID(kind string) string {
	b := make([]byte, 3)
	rand.Read(b)
	return "launch-" + kind + "-" + hex.EncodeToString(b)
}

// Start starts a new session in cwd. The agent shows no folder-trust dialog
// to the gateway, so a folder the agent was never trusted in is confirmed
// with the user first (TrustRequired with a launch id).
func (l *Launcher) Start(ctx context.Context, req StartRequest) (*StartResult, error) {
	p, err := l.provider(req.Provider)
	if err != nil {
		return nil, err
	}
	if req.Model != "" && !providers.ValidModelID(req.Model) {
		return nil, ErrInvalidModel
	}
	if req.Effort != "" && !providers.ValidEffortID(req.Effort) {
		return nil, ErrInvalidEffort
	}
	if req.Mode != "" && !providers.ValidModeID(req.Mode) {
		return nil, ErrInvalidMode
	}
	cwd, err := l.resolveDir(req.Cwd)
	if err != nil {
		return nil, err
	}
	req.Cwd = cwd
	if tc, ok := p.(providers.TrustChecker); ok && !req.Trust && !tc.FolderTrusted(cwd) {
		id := launchID(p.Name())
		l.mu.Lock()
		if l.pending == nil {
			l.pending = map[string]*StartRequest{}
		}
		l.pending[id] = &req
		l.mu.Unlock()
		slog.Info("launch waiting on folder trust", "provider", p.Name(), "launch", id, "cwd", cwd)
		return &StartResult{LaunchID: id, TrustRequired: true}, nil
	}
	return l.run(ctx, p, req)
}

// AnswerTrust goes ahead with a start that waited on the folder-trust
// answer, or drops it when declined.
func (l *Launcher) AnswerTrust(ctx context.Context, id string, accept bool) (*StartResult, error) {
	l.mu.Lock()
	req := l.pending[id]
	delete(l.pending, id)
	l.mu.Unlock()
	if req == nil {
		return nil, ErrNoPendingTrust
	}
	if !accept {
		slog.Info("folder trust declined", "provider", req.Provider, "launch", id, "cwd", req.Cwd)
		return &StartResult{LaunchID: id}, nil
	}
	p, err := l.provider(req.Provider)
	if err != nil {
		return nil, err
	}
	return l.run(ctx, p, *req)
}

func (l *Launcher) run(ctx context.Context, p providers.Provider, req StartRequest) (*StartResult, error) {
	cwd, wt := req.Cwd, ""
	if req.Worktree {
		path, err := worktree.Create(ctx, cwd, l.WorktreeDir, p.Name())
		if err != nil {
			return nil, fmt.Errorf("create worktree: %w", err)
		}
		cwd, wt = path, path
	}
	opts := providers.DirectStart{Cwd: cwd, Model: req.Model, Effort: req.Effort, Mode: req.Mode,
		Prompt: model.Input{Text: req.Prompt, Images: req.Images, Files: req.Files}}
	id, err := l.Direct.Start(ctx, p.Name(), opts)
	if id == "" {
		if wt != "" {
			if derr := worktree.Discard(context.WithoutCancel(ctx), wt); derr != nil {
				slog.Warn("removing worktree of a failed start failed", "provider", p.Name(), "path", wt, "error", derr)
			}
		}
		return nil, err
	}
	res := &StartResult{SessionID: id}
	if err != nil {
		// The session exists, but its first prompt did not go through.
		res.Warning = "The session started, but its first message was not delivered: " + err.Error()
	}
	slog.Info("session started", "provider", p.Name(), "session_id", id, "operation", "launch", "cwd", cwd,
		"model", req.Model, "effort", req.Effort, "mode", req.Mode, "worktree", req.Worktree)
	return res, nil
}

// resumeDir checks a session's directory for resuming it.
func (l *Launcher) resumeDir(ctx context.Context, cwd string) (string, error) {
	if cwd == "" {
		return "", ErrNoCwd
	}
	dir, err := l.resolveDir(cwd)
	if errors.Is(err, files.ErrForbidden) {
		dir, err = l.resolveWorktree(ctx, cwd)
	}
	if errors.Is(err, files.ErrNotFound) {
		// e.g. the worktree it ran in was removed when it was archived
		return "", ErrCwdGone
	}
	return dir, err
}

// Resume reopens an existing session that runs nowhere, under its own id,
// in its working directory.
func (l *Launcher) Resume(ctx context.Context, provider, nativeID, cwd string) (*StartResult, error) {
	p, err := l.provider(provider)
	if err != nil {
		return nil, err
	}
	dir, err := l.resumeDir(ctx, cwd)
	if err != nil {
		return nil, err
	}
	id := p.Name() + ":" + nativeID
	if err := l.Direct.Resume(ctx, id, providers.DirectStart{Cwd: dir}); err != nil {
		return nil, err
	}
	return &StartResult{SessionID: id}, nil
}

// resolveWorktree accepts a linked worktree outside the roots (agents keep
// them in their own directories) whose repository is inside them.
func (l *Launcher) resolveWorktree(ctx context.Context, dir string) (string, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", files.ErrForbidden
	}
	main, err := worktree.MainCheckout(ctx, real)
	if err != nil {
		return "", files.ErrForbidden
	}
	if _, err := l.resolveDir(main); err != nil {
		return "", err
	}
	return real, nil
}
