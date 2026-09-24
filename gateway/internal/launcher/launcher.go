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
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/files"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/worktree"
)

var (
	ErrInvalidName     = errors.New("invalid directory name")
	ErrUnknownProvider = errors.New("unknown provider")
	ErrInvalidModel    = errors.New("invalid model")
	ErrInvalidEffort   = errors.New("invalid effort")
	ErrInvalidMode     = errors.New("invalid mode")
	ErrNoCwd           = errors.New("session has no known working directory")
	ErrNoPendingTrust  = errors.New("no launch is waiting on a trust answer for this pane")
	ErrCwdGone         = errors.New("the session's working directory no longer exists")
	// ErrStartRefused wraps an agent's own explanation of why it did not start.
	ErrStartRefused = errors.New("the agent did not start")
)

// Herdr is the subset of the Herdr client used to launch agents.
type Herdr interface {
	CreateWorkspace(ctx context.Context, cwd, label string) (workspaceID, paneID string, err error)
	CreateWorktree(ctx context.Context, cwd, label string) (workspaceID, paneID, path string, err error)
	StartAgent(ctx context.Context, name, kind, paneID string, args []string, timeout time.Duration) error
	ReadVisible(ctx context.Context, paneID string) (string, error)
	SendKeys(ctx context.Context, paneID string, keys ...string) error
	Prompt(ctx context.Context, paneID, text string) error
	Pane(ctx context.Context, paneID string) (*herdr.Pane, error)
	Snapshot(ctx context.Context) (*herdr.Snapshot, error)
	ClosePane(ctx context.Context, paneID string) error
	CloseWorkspace(ctx context.Context, workspaceID string) error
	ReportAgentSession(ctx context.Context, paneID, agent, sessionID string) error
}

type Launcher struct {
	Herdr     Herdr
	Roots     []string
	Providers []providers.Provider

	// Timings (overridable in tests).
	StartTimeout time.Duration
	PollInterval time.Duration
	IdentityWait time.Duration

	mu sync.Mutex
	// pending holds launches waiting on trust, readiness or identity, by pane
	// id, until they finish or the gateway restarts. Entries are small, so unanswered
	// ones are simply kept.
	pending map[string]*pendingLaunch
	panes   map[string]bool
}

// pendingLaunch retains the context needed to finish a partial launch.
type pendingLaunch struct {
	p          providers.Provider
	lp         providers.Launchable
	ws, cwd    string
	prompt     string
	promptSent bool
	knownID    string
	// mode is applied to the live TUI once, before the first prompt.
	mode    string
	started time.Time
	// worktree is the checkout made for the session, removed again when the
	// launch is cancelled.
	worktree string
	logKV    []any
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

// ResolveDir validates an absolute working directory using the launch restrictions.
func (l *Launcher) ResolveDir(path string) (string, error) { return l.resolveDir(path) }

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
	// Effort is an effort id from Models; empty uses the agent's default.
	Effort string `json:"effort,omitempty"`
	// Mode is a mode id from Models; empty starts in the agent's own mode.
	Mode string `json:"mode,omitempty"`
	// Trust accepts the agent's folder-trust dialog on the user's behalf.
	// Without it the launch stops at the dialog and reports TrustRequired.
	Trust bool `json:"trust"`
	// Worktree starts the session in a new git worktree of Cwd's repository:
	// made by the agent when it can, otherwise by Herdr.
	Worktree bool `json:"worktree,omitempty"`
}

type StartResult struct {
	SessionID string `json:"sessionId,omitempty"`
	PaneID    string `json:"paneId"`
	// Warning explains a partial start (e.g. waiting on a dialog, no identity yet).
	Warning string `json:"warning,omitempty"`
	// TrustRequired means the agent is showing its folder-trust dialog; the
	// launch continues when the app answers with AnswerTrust.
	TrustRequired bool `json:"trustRequired,omitempty"`
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

// Models lists the models and efforts a provider offers for new sessions.
func (l *Launcher) Models(ctx context.Context, provider string) (providers.ModelCatalog, error) {
	_, lp, err := l.provider(provider)
	if err != nil {
		return providers.ModelCatalog{}, err
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
	opts := providers.LaunchOptions{Model: req.Model, Effort: req.Effort, Mode: req.Mode, Cwd: cwd}
	plan := launchPlan{p: p, lp: lp, cwd: cwd, trust: req.Trust, prompt: req.Prompt, mode: req.Mode,
		logKV: []any{"model", req.Model, "effort", req.Effort, "mode", req.Mode, "worktree", req.Worktree}}
	if req.Worktree {
		top, err := worktree.Toplevel(ctx, cwd)
		if err != nil {
			return nil, err
		}
		if wl, ok := p.(providers.WorktreeLauncher); ok {
			plan.args, plan.agentWorktree = wl.WorktreeArgs(opts, worktreeName(top))
		}
		if !plan.agentWorktree {
			// The agent cannot make one here, so Herdr does and the
			// workspace opens in it.
			ws, pane, path, err := l.Herdr.CreateWorktree(ctx, cwd, filepath.Base(top))
			if err != nil {
				return nil, fmt.Errorf("create worktree: %w", err)
			}
			if err := worktree.Mark(ctx, path, p.Name()); err != nil {
				slog.Warn("marking worktree failed", "provider", p.Name(), "path", path, "error", err)
			}
			plan.ws, plan.pane, plan.worktree = ws, pane, path
			plan.cwd, opts.Cwd = path, path
		}
	}
	if !plan.agentWorktree {
		plan.args = lp.LaunchArgs(opts)
	}
	if prep, ok := p.(providers.PreparedLauncher); ok && !plan.agentWorktree {
		plan.args, plan.knownID, err = prep.PrepareLaunch(ctx, opts)
		if err != nil {
			l.discard(ctx, plan.ws, plan.worktree, p)
			return nil, err
		}
	}
	return l.launch(ctx, plan)
}

// worktreeName names a worktree an agent creates after its repository.
func worktreeName(top string) string {
	name := strings.Map(func(r rune) rune {
		if r < 0x80 && (r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return r
		}
		return '-'
	}, filepath.Base(top))
	name = strings.Trim(name, "-.")
	if name == "" {
		name = "session"
	}
	b := make([]byte, 3)
	rand.Read(b)
	return name + "-" + hex.EncodeToString(b)
}

// Resume reopens an existing session (not currently in Herdr) in a new
// workspace in its working directory.
func (l *Launcher) Resume(ctx context.Context, provider, nativeID, cwd string, trust bool) (*StartResult, error) {
	p, lp, err := l.provider(provider)
	if err != nil {
		return nil, err
	}
	if cwd == "" {
		return nil, ErrNoCwd
	}
	dir, err := l.resolveDir(cwd)
	if errors.Is(err, files.ErrForbidden) {
		dir, err = l.resolveWorktree(ctx, cwd)
	}
	if errors.Is(err, files.ErrNotFound) {
		// e.g. the worktree it ran in was removed when it was archived
		return nil, ErrCwdGone
	}
	if err != nil {
		return nil, err
	}
	return l.launch(ctx, launchPlan{p: p, lp: lp, cwd: dir, args: lp.ResumeArgs(nativeID, dir), trust: trust, knownID: nativeID, logKV: []any{"resume", nativeID}})
}

// resolveWorktree accepts a linked worktree outside the roots (agents and
// Herdr keep them in their own directories) whose repository is inside them.
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

// launchPlan is what launch starts. knownID is the native id when resuming
// a session; mode is set in the live TUI by providers that take no mode
// argument.
type launchPlan struct {
	p               providers.Provider
	lp              providers.Launchable
	cwd             string
	args            []string
	trust           bool
	prompt, knownID string
	mode            string
	// ws and pane are the workspace Herdr opened in a worktree it created
	// for the session (worktree); empty to open a new one in cwd.
	ws, pane, worktree string
	// agentWorktree means args make the agent create its own worktree.
	agentWorktree bool
	logKV         []any
}

// launch runs the agent in a new workspace.
func (l *Launcher) launch(ctx context.Context, plan launchPlan) (*StartResult, error) {
	started := time.Now()
	p, lp, cwd := plan.p, plan.lp, plan.cwd
	ws, pane := plan.ws, plan.pane
	if ws == "" {
		var err error
		if ws, pane, err = l.Herdr.CreateWorkspace(ctx, cwd, filepath.Base(cwd)); err != nil {
			return nil, fmt.Errorf("create workspace: %w", err)
		}
	}
	res := &StartResult{PaneID: pane}
	log := slog.With("provider", p.Name(), "operation", "launch", "pane", pane)

	var before []string
	if plan.agentWorktree {
		before, _ = worktree.List(ctx, cwd)
	}
	startErr := l.startAgent(ctx, p, pane, plan.args)
	wt := plan.worktree
	if plan.agentWorktree {
		wt = l.markAgentWorktree(ctx, p, cwd, before)
	}
	if startErr != nil {
		err := fmt.Errorf("start agent: %w", startErr)
		if ex, ok := p.(providers.StartFailureExplainer); ok {
			if screen, rerr := l.Herdr.ReadVisible(ctx, pane); rerr == nil {
				if why := ex.StartFailure(screen); why != nil {
					err = fmt.Errorf("%w: %w", ErrStartRefused, why)
				}
			}
		}
		// Don't leave an empty shell workspace (or checkout) behind.
		l.discard(ctx, ws, wt, p)
		return nil, err
	}
	l.mu.Lock()
	if l.panes == nil {
		l.panes = map[string]bool{}
	}
	l.panes[pane] = true
	l.mu.Unlock()
	pl := &pendingLaunch{p: p, lp: lp, ws: ws, cwd: cwd, prompt: plan.prompt, knownID: plan.knownID, mode: plan.mode, started: started, worktree: wt, logKV: plan.logKV}
	switch l.passStartupDialog(ctx, lp, pane, plan.trust) {
	case startupTrust:
		l.putPending(pane, pl)
		res.TrustRequired = true
		log.Info("launch waiting on folder trust", "cwd", cwd)
		return res, nil
	case startupStuck:
		l.putPending(pane, pl)
		res.Warning = "The agent is waiting on a startup dialog. Open the terminal to continue."
		return res, nil
	}
	return l.finishPending(ctx, pane, pl), nil
}

// markAgentWorktree finds the worktree an agent created on start by
// comparing the repository's worktrees with those before, and marks it as
// the gateway's so archiving the session removes it.
func (l *Launcher) markAgentWorktree(ctx context.Context, p providers.Provider, cwd string, before []string) string {
	after, err := worktree.List(ctx, cwd)
	if err != nil {
		return ""
	}
	var added []string
	for _, a := range after {
		if !slices.Contains(before, a) {
			added = append(added, a)
		}
	}
	if len(added) != 1 {
		if len(added) > 1 {
			slog.Warn("several new worktrees; none marked", "provider", p.Name(), "cwd", cwd, "worktrees", added)
		}
		return ""
	}
	if err := worktree.Mark(ctx, added[0], p.Name()); err != nil {
		slog.Warn("marking worktree failed", "provider", p.Name(), "path", added[0], "error", err)
		return ""
	}
	return added[0]
}

// discard closes a workspace opened for a launch that is not going ahead,
// and removes the worktree made for it.
func (l *Launcher) discard(ctx context.Context, ws, wt string, p providers.Provider) {
	ctx = context.WithoutCancel(ctx)
	if ws != "" {
		if err := l.Herdr.CloseWorkspace(ctx, ws); err != nil {
			slog.Warn("closing workspace of a cancelled launch failed", "provider", p.Name(), "workspace", ws, "error", err)
		}
	}
	if wt != "" {
		if _, err := worktree.Cleanup(ctx, wt, worktree.Owner{Provider: p.Name()}, nil); err != nil {
			slog.Warn("removing worktree of a cancelled launch failed", "provider", p.Name(), "path", wt, "error", err)
		}
	}
}

// AnswerTrust resumes a launch stopped at the folder-trust dialog. Declining
// closes the workspace that was opened for it.
func (l *Launcher) AnswerTrust(ctx context.Context, pane string, accept bool) (*StartResult, error) {
	pl := l.takePending(pane)
	if pl == nil {
		return nil, ErrNoPendingTrust
	}
	if !accept {
		l.mu.Lock()
		delete(l.panes, pane)
		l.mu.Unlock()
		slog.Info("folder trust declined", "provider", pl.p.Name(), "pane", pane, "cwd", pl.cwd)
		err := l.Herdr.CloseWorkspace(ctx, pl.ws)
		if err == nil {
			l.discard(ctx, "", pl.worktree, pl.p)
		}
		return &StartResult{PaneID: pane}, err
	}
	if l.passStartupDialog(ctx, pl.lp, pane, true) != startupReady {
		l.putPending(pane, pl)
		return &StartResult{PaneID: pane, Warning: "The agent is waiting on a startup dialog. Open the terminal to continue."}, nil
	}
	return l.finishPending(ctx, pane, pl), nil
}

// TerminalPane allows fallback access only to panes launched by this gateway.
func (l *Launcher) TerminalPane(ctx context.Context, pane string) (string, error) {
	l.mu.Lock()
	owned := l.panes[pane]
	l.mu.Unlock()
	if !owned {
		return "", providers.ErrNotFound
	}
	if _, err := l.Herdr.Pane(ctx, pane); err != nil {
		return "", err
	}
	return pane, nil
}

// Continue retries readiness after the user handles an unfamiliar dialog.
// Taking the pending entry prevents concurrent requests from sending twice.
func (l *Launcher) Continue(ctx context.Context, pane string) (*StartResult, error) {
	pl := l.takePending(pane)
	if pl == nil {
		return nil, ErrNoPendingTrust
	}
	switch l.passStartupDialog(ctx, pl.lp, pane, false) {
	case startupTrust:
		l.putPending(pane, pl)
		return &StartResult{PaneID: pane, TrustRequired: true}, nil
	case startupStuck:
		l.putPending(pane, pl)
		return &StartResult{PaneID: pane, Warning: "The agent is still waiting. Open the terminal to continue."}, nil
	}
	return l.finishPending(ctx, pane, pl), nil
}

func (l *Launcher) finishPending(ctx context.Context, pane string, pl *pendingLaunch) *StartResult {
	res := l.finish(ctx, pane, pl)
	if res.SessionID == "" {
		l.putPending(pane, pl)
	}
	return res
}

func (l *Launcher) putPending(pane string, pl *pendingLaunch) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pending == nil {
		l.pending = map[string]*pendingLaunch{}
	}
	l.pending[pane] = pl
}

func (l *Launcher) takePending(pane string) *pendingLaunch {
	l.mu.Lock()
	defer l.mu.Unlock()
	pl := l.pending[pane]
	delete(l.pending, pane)
	return pl
}

// finish sends the first prompt to a ready agent and waits for its identity.
func (l *Launcher) finish(ctx context.Context, pane string, pl *pendingLaunch) *StartResult {
	res := &StartResult{PaneID: pane}
	if w := l.applyMode(ctx, pane, pl); w != "" {
		res.Warning = w
	}
	if strings.TrimSpace(pl.prompt) != "" {
		if err := l.waitReady(ctx, pane); err != nil {
			res.Warning = "The agent did not become ready; the prompt was not sent."
			return res
		}
		var promptErr error
		if sender, ok := pl.p.(providers.LaunchPromptSender); ok {
			promptErr = sender.SendLaunchPrompt(ctx, pane, pl.prompt)
		} else {
			promptErr = l.Herdr.Prompt(ctx, pane, pl.prompt)
		}
		if promptErr != nil {
			res.Warning = "Prompt could not be sent: " + promptErr.Error()
		}
		// Never replay a prompt after an ambiguous send error.
		pl.prompt = ""
		pl.promptSent = true
	} else if !pl.promptSent && pl.knownID == "" {
		// Agents do not always create a native session until the first prompt
		// is entered. Waiting for an identity here makes a valid, prompt-less
		// launch look like a failure and needlessly blocks the response.
		// The pane is already running; the session id is picked up by the
		// normal session refresh after the first prompt creates the thread.
		return res
	}

	res.SessionID = l.waitIdentity(ctx, pane, pl.p, pl.cwd, pl.started, pl.knownID)
	if res.SessionID == "" && res.Warning == "" {
		res.Warning = "Started, but Herdr has not reported the session id yet (is `herdr integration install " + pl.p.HerdrAgent() + "` done?)."
	}
	slog.Info("session launched", append([]any{"provider", pl.p.Name(), "operation", "launch", "pane", pane, "session_id", res.SessionID, "cwd", pl.cwd}, pl.logKV...)...)
	return res
}

// applyMode sets the starting mode of agents that take no mode argument. It
// runs once, before the first prompt, so the mode applies to that prompt; a
// failure is reported but does not cancel the launch.
func (l *Launcher) applyMode(ctx context.Context, pane string, pl *pendingLaunch) string {
	if pl.mode == "" {
		return ""
	}
	setter, ok := pl.p.(providers.LaunchModeSetter)
	if !ok {
		return ""
	}
	// Only one attempt: a retry through Continue must not toggle it again.
	mode := pl.mode
	pl.mode = ""
	if err := l.waitReady(ctx, pane); err != nil {
		return "The agent did not become ready; it started in its usual mode."
	}
	if err := setter.SetLaunchMode(ctx, pane, mode); err != nil {
		return "The mode could not be set: " + err.Error()
	}
	return ""
}

// startAgent runs agent.start. A new pane's shell may still be running its
// startup files, which Herdr reports as agent_pane_busy; retry until it is
// an idle shell. Herdr may report success or agent_not_ready while a startup
// dialog (folder trust) is shown; both count as started.
func (l *Launcher) startAgent(ctx context.Context, p providers.Provider, pane string, args []string) error {
	deadline := time.Now().Add(l.startTimeout())
	for {
		sctx, cancel := context.WithTimeout(ctx, l.startTimeout()+5*time.Second)
		err := l.Herdr.StartAgent(sctx, agentName(p.HerdrAgent()), p.HerdrAgent(), pane, args, l.startTimeout())
		cancel()
		var herr *herdr.Error
		if err == nil || (errors.As(err, &herr) && herr.Code == "agent_not_ready") {
			return nil
		}
		if !errors.As(err, &herr) || herr.Code != "agent_pane_busy" || time.Now().After(deadline) {
			return err
		}
		if !sleep(ctx, l.pollInterval()) {
			return ctx.Err()
		}
	}
}

// Stop closes the pane running a session, and its workspace when the pane
// was the only one there.
func (l *Launcher) Stop(ctx context.Context, paneID string) error {
	snap, err := l.Herdr.Snapshot(ctx)
	if err != nil {
		return err
	}
	ws, others := "", 0
	for _, pn := range snap.Panes {
		if pn.PaneID == paneID {
			ws = pn.WorkspaceID
		}
	}
	if ws == "" {
		return nil // already gone
	}
	for _, pn := range snap.Panes {
		if pn.WorkspaceID == ws && pn.PaneID != paneID {
			others++
		}
	}
	if others == 0 {
		return l.Herdr.CloseWorkspace(ctx, ws)
	}
	return l.Herdr.ClosePane(ctx, paneID)
}

type startupOutcome int

const (
	startupReady startupOutcome = iota
	startupTrust                // a folder-trust dialog awaits the user's answer
	startupStuck
)

// passStartupDialog answers a folder-trust dialog when allowed and waits
// until the agent is ready. Dialogs can appear a moment after Herdr reports
// the agent, so readiness must be observed on consecutive polls.
func (l *Launcher) passStartupDialog(ctx context.Context, lp providers.Launchable, pane string, trust bool) startupOutcome {
	deadline := time.Now().Add(l.startTimeout())
	readyPolls := 0
	for time.Now().Before(deadline) {
		if screen, err := l.Herdr.ReadVisible(ctx, pane); err == nil {
			if keys := lp.StartupKeys(screen); keys != nil {
				if !trust {
					return startupTrust
				}
				if err := l.Herdr.SendKeys(ctx, pane, keys...); err != nil {
					return startupStuck
				}
				readyPolls = 0
				if !sleep(ctx, l.pollInterval()) {
					return startupStuck
				}
				continue
			}
		}
		pi, err := l.Herdr.Pane(ctx, pane)
		if err == nil && pi.AgentName() != "" && (pi.AgentStatus == herdr.StatusIdle || pi.AgentStatus == herdr.StatusDone || pi.AgentStatus == herdr.StatusWorking) {
			readyPolls++
			if readyPolls >= 2 {
				return startupReady
			}
		} else {
			readyPolls = 0
		}
		if !sleep(ctx, l.pollInterval()) {
			return startupStuck
		}
	}
	return startupStuck
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

// waitIdentity waits for the integration hook to report the native session
// id. Providers that can locate the session themselves report it to Herdr
// when the hook does not.
func (l *Launcher) waitIdentity(ctx context.Context, pane string, p providers.Provider, cwd string, started time.Time, knownID string) string {
	locator, _ := p.(providers.SessionLocator)
	deadline := time.Now().Add(l.identityWait())
	polls := 0
	for time.Now().Before(deadline) {
		if pi, err := l.Herdr.Pane(ctx, pane); err == nil && pi.AgentSession != nil &&
			pi.AgentSession.Agent == p.HerdrAgent() && pi.AgentSession.Kind == "id" && pi.AgentSession.Value != "" {
			return p.Name() + ":" + pi.AgentSession.Value
		}
		// Give the hook a moment before reporting on its behalf.
		if polls++; locator != nil && polls > 2 {
			id := knownID
			if id == "" {
				id = locator.LocateLaunched(ctx, cwd, started.Add(-2*time.Second))
			}
			if id != "" {
				if err := l.Herdr.ReportAgentSession(ctx, pane, p.HerdrAgent(), id); err != nil {
					slog.Warn("reporting agent session failed", "provider", p.Name(), "pane", pane, "session_id", id, "error", err)
				}
			}
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
