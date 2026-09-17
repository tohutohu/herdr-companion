// Package launcher starts new agent sessions in Herdr and lets the app pick or
// create their working directory under configured workspace roots.
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
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/files"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

var (
	ErrInvalidName     = errors.New("invalid directory name")
	ErrUnknownProvider = errors.New("unknown provider")
	ErrInvalidModel    = errors.New("invalid model")
)

// Herdr is the subset of the Herdr client used to launch agents.
type Herdr interface {
	CreateWorkspace(ctx context.Context, cwd, label string) (workspaceID, paneID string, err error)
	StartAgent(ctx context.Context, name, kind, paneID string, args []string, timeout time.Duration) error
	ReadVisible(ctx context.Context, paneID string) (string, error)
	SendKeys(ctx context.Context, paneID string, keys ...string) error
	Prompt(ctx context.Context, paneID, text string) error
	Pane(ctx context.Context, paneID string) (*herdr.Pane, error)
}

type Launcher struct {
	Herdr     Herdr
	Roots     []string
	Providers []providers.Provider

	// Timings (overridable in tests).
	StartTimeout time.Duration
	PollInterval time.Duration
	IdentityWait time.Duration
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

// List lists the roots (path == "") or the visible subdirectories of path.
func (l *Launcher) List(path string) (*Listing, error) {
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
	// Trust accepts the agent's folder-trust dialog on the user's behalf.
	Trust bool `json:"trust"`
}

type StartResult struct {
	SessionID string `json:"sessionId,omitempty"`
	PaneID    string `json:"paneId"`
	// Warning explains a partial start (e.g. waiting on a dialog, no identity yet).
	Warning string `json:"warning,omitempty"`
}

func (l *Launcher) provider(name string) (providers.Provider, providers.Launchable, error) {
	for _, p := range l.Providers {
		if p.Name() == name {
			if lp, ok := p.(providers.Launchable); ok {
				return p, lp, nil
			}
		}
	}
	return nil, nil, ErrUnknownProvider
}

// Models lists the models a provider offers for new sessions.
func (l *Launcher) Models(ctx context.Context, provider string) ([]providers.ModelOption, error) {
	_, lp, err := l.provider(provider)
	if err != nil {
		return nil, err
	}
	return lp.Models(ctx)
}

func agentName(kind string) string {
	b := make([]byte, 3)
	rand.Read(b)
	return kind + "-" + hex.EncodeToString(b)
}

// Start opens a Herdr workspace in cwd and launches the agent there.
func (l *Launcher) Start(ctx context.Context, req StartRequest) (*StartResult, error) {
	p, lp, err := l.provider(req.Provider)
	if err != nil {
		return nil, err
	}
	if req.Model != "" && !providers.ValidModelID(req.Model) {
		return nil, ErrInvalidModel
	}
	cwd, err := l.resolveDir(req.Cwd)
	if err != nil {
		return nil, err
	}
	_, pane, err := l.Herdr.CreateWorkspace(ctx, cwd, filepath.Base(cwd))
	if err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}
	res := &StartResult{PaneID: pane}
	log := slog.With("provider", p.Name(), "operation", "launch", "pane", pane)

	sctx, cancel := context.WithTimeout(ctx, l.startTimeout()+5*time.Second)
	err = l.Herdr.StartAgent(sctx, agentName(p.HerdrAgent()), p.HerdrAgent(), pane, lp.LaunchArgs(req.Model, cwd), l.startTimeout())
	cancel()
	var herr *herdr.Error
	// Herdr may report success or agent_not_ready while a startup dialog
	// (folder trust) is shown; handle both the same way.
	if err != nil && !(errors.As(err, &herr) && herr.Code == "agent_not_ready") {
		return res, fmt.Errorf("start agent: %w", err)
	}
	if !l.passStartupDialog(ctx, lp, pane, req.Trust) {
		res.Warning = "The agent is waiting on a startup dialog. Open the terminal to continue."
		return res, nil
	}

	if strings.TrimSpace(req.Prompt) != "" {
		if err := l.waitReady(ctx, pane); err != nil {
			res.Warning = "The agent did not become ready; the prompt was not sent."
			return res, nil
		}
		if err := l.Herdr.Prompt(ctx, pane, req.Prompt); err != nil {
			res.Warning = "Prompt could not be sent: " + err.Error()
		}
	}

	res.SessionID = l.waitIdentity(ctx, pane, p)
	if res.SessionID == "" && res.Warning == "" {
		res.Warning = "Started, but Herdr has not reported the session id yet (is `herdr integration install " + p.HerdrAgent() + "` done?)."
	}
	log.Info("session launched", "session_id", res.SessionID, "cwd", cwd, "model", req.Model)
	return res, nil
}

// passStartupDialog answers a folder-trust dialog when allowed and waits
// until the agent is ready. Dialogs can appear a moment after Herdr reports
// the agent, so readiness must be observed on consecutive polls.
func (l *Launcher) passStartupDialog(ctx context.Context, lp providers.Launchable, pane string, trust bool) bool {
	deadline := time.Now().Add(l.startTimeout())
	readyPolls := 0
	for time.Now().Before(deadline) {
		if screen, err := l.Herdr.ReadVisible(ctx, pane); err == nil {
			if keys := lp.StartupKeys(screen); keys != nil {
				if !trust {
					return false
				}
				if err := l.Herdr.SendKeys(ctx, pane, keys...); err != nil {
					return false
				}
				readyPolls = 0
				if !sleep(ctx, l.pollInterval()) {
					return false
				}
				continue
			}
		}
		pi, err := l.Herdr.Pane(ctx, pane)
		if err == nil && pi.AgentName() != "" && (pi.AgentStatus == herdr.StatusIdle || pi.AgentStatus == herdr.StatusDone || pi.AgentStatus == herdr.StatusWorking) {
			readyPolls++
			if readyPolls >= 2 {
				return true
			}
		} else {
			readyPolls = 0
		}
		if !sleep(ctx, l.pollInterval()) {
			return false
		}
	}
	return false
}

func (l *Launcher) waitReady(ctx context.Context, pane string) error {
	deadline := time.Now().Add(l.startTimeout())
	for time.Now().Before(deadline) {
		if pi, err := l.Herdr.Pane(ctx, pane); err == nil {
			switch pi.AgentStatus {
			case herdr.StatusIdle, herdr.StatusDone:
				return nil
			}
		}
		if !sleep(ctx, l.pollInterval()) {
			return ctx.Err()
		}
	}
	return errors.New("agent not ready")
}

// waitIdentity waits for the integration hook to report the native session id.
func (l *Launcher) waitIdentity(ctx context.Context, pane string, p providers.Provider) string {
	deadline := time.Now().Add(l.identityWait())
	for time.Now().Before(deadline) {
		if pi, err := l.Herdr.Pane(ctx, pane); err == nil && pi.AgentSession != nil &&
			pi.AgentSession.Agent == p.HerdrAgent() && pi.AgentSession.Kind == "id" && pi.AgentSession.Value != "" {
			return p.Name() + ":" + pi.AgentSession.Value
		}
		if !sleep(ctx, l.pollInterval()) {
			return ""
		}
	}
	return ""
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func orDefault(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

func (l *Launcher) startTimeout() time.Duration { return orDefault(l.StartTimeout, 60*time.Second) }
func (l *Launcher) pollInterval() time.Duration { return orDefault(l.PollInterval, time.Second) }
func (l *Launcher) identityWait() time.Duration { return orDefault(l.IdentityWait, 20*time.Second) }
