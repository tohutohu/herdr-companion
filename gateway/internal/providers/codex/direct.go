package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// directThread is a thread the gateway hosts on its own app-server. Model,
// effort and collaboration mode are kept here and given with every
// turn/start, the way Codex's own clients do: a change never touches a
// running turn and applies from the next one, on the same thread id.
type directThread struct {
	cwd string
	// model, effort and mode are the user's choices; empty keeps the thread's.
	model, effort, mode string
	// threadModel / threadEffort are what the thread runs with, as the
	// app-server reported at start or resume and updated as turns start.
	threadModel, threadEffort string
	// requested* are changes that no turn has started with yet.
	requestedModel, requestedEffort string
	activeTurn                      string
	turns                           int
	lastFailed                      bool
	err                             string
	alive                           bool
}

func (p *Provider) isDirect(id string) bool {
	p.directMu.Lock()
	defer p.directMu.Unlock()
	dt := p.direct[id]
	return dt != nil && dt.alive
}

func (p *Provider) SetDirectListener(f func(string)) {
	p.directMu.Lock()
	p.listener = f
	p.directMu.Unlock()
}

func (p *Provider) notifyDirect(id string) {
	p.directMu.Lock()
	l := p.listener
	p.directMu.Unlock()
	if l != nil {
		l(id)
	}
}

// SettingsApply: Codex takes model and effort per turn.
func (p *Provider) SettingsApply() string { return providers.ApplyNextTurn }

type threadOpened struct {
	Thread          Thread          `json:"thread"`
	Model           string          `json:"model"`
	ReasoningEffort *string         `json:"reasoningEffort"`
	Cwd             string          `json:"cwd"`
	ApprovalPolicy  json.RawMessage `json:"approvalPolicy"`
	Sandbox         *sandboxPolicy  `json:"sandbox"`
}

// DirectStart starts a thread (or resumes one) on the gateway's app-server
// and sends the first prompt as its first turn. The thread id is returned
// even when that prompt fails, since the thread exists by then.
func (p *Provider) DirectStart(ctx context.Context, opts providers.DirectStart) (string, error) {
	defer p.actionVersion.Add(1)
	d, err := p.ownedConn(ctx)
	if err != nil {
		return "", err
	}
	if opts.Mode != "" && opts.Mode != "default" && opts.Mode != "plan" {
		return "", fmt.Errorf("unknown Codex mode %q", opts.Mode)
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var r threadOpened
	if opts.Resume != "" {
		if !threadIDPattern.MatchString(opts.Resume) {
			return "", providers.ErrNotFound
		}
		params := map[string]any{"threadId": opts.Resume}
		if opts.Cwd != "" {
			params["cwd"] = opts.Cwd
		}
		if err := d.c.call(cctx, "thread/resume", params, &r); err != nil {
			var rerr *rpcError
			if errors.As(err, &rerr) && strings.Contains(rerr.Message, "active writer") {
				return "", fmt.Errorf("the thread is still open in another Codex app-server (a TUI or the shared daemon): %w", err)
			}
			if errors.As(err, &rerr) {
				return "", fmt.Errorf("%w: %v", providers.ErrNotFound, err)
			}
			return "", err
		}
	} else {
		// Everything not chosen comes from the user's Codex config, as for
		// the CLI: approval policy, sandbox, model.
		params := map[string]any{"cwd": opts.Cwd}
		if opts.Model != "" {
			params["model"] = opts.Model
		}
		if err := d.c.call(cctx, "thread/start", params, &r); err != nil {
			return "", err
		}
	}
	id := r.Thread.ID
	if id == "" {
		return "", errors.New("codex app-server returned no thread id")
	}
	dt := &directThread{cwd: r.Cwd, model: opts.Model, effort: opts.Effort, mode: opts.Mode, threadModel: r.Model, alive: true}
	if dt.cwd == "" {
		dt.cwd = r.Thread.Cwd
	}
	if dt.cwd == "" {
		dt.cwd = opts.Cwd
	}
	if r.ReasoningEffort != nil {
		dt.threadEffort = *r.ReasoningEffort
	}
	d.mu.Lock()
	d.subscribed[id] = true
	if r.Thread.Status.Type != "" {
		d.status[id] = r.Thread.Status
	}
	if r.Sandbox != nil {
		d.settings[id] = threadSettings{ApprovalPolicy: r.ApprovalPolicy, SandboxPolicy: r.Sandbox}
	}
	d.mu.Unlock()
	p.directMu.Lock()
	if p.direct == nil {
		p.direct = map[string]*directThread{}
	}
	p.direct[id] = dt
	p.directMu.Unlock()
	slog.Info("codex thread opened", "provider", providerName, "session_id", gatewayID(id), "operation", "direct.start", "resume", opts.Resume != "", "model", dt.threadModel)
	p.notifyDirect(id)
	if !opts.Prompt.Empty() {
		if err := p.directSend(ctx, id, opts.Prompt); err != nil {
			return id, fmt.Errorf("send first prompt: %w", err)
		}
	}
	return id, nil
}

func (p *Provider) directThread(id string) (*directThread, *serverConn, error) {
	d := p.currentOwned()
	p.directMu.Lock()
	defer p.directMu.Unlock()
	dt := p.direct[id]
	if d == nil || dt == nil || !dt.alive {
		return nil, nil, providers.ErrNotLive
	}
	return dt, d, nil
}

// turnParams are the per-turn settings: the chosen model and effort, and the
// collaboration mode once one was chosen (it carries model and effort too).
func (p *Provider) turnParams(id string, params map[string]any) {
	p.directMu.Lock()
	defer p.directMu.Unlock()
	dt := p.direct[id]
	if dt == nil {
		return
	}
	model, effort := dt.model, dt.effort
	if model != "" {
		params["model"] = model
	}
	if effort != "" {
		params["effort"] = effort
	}
	if dt.mode != "" {
		if model == "" {
			model = dt.threadModel
		}
		if effort == "" {
			effort = dt.threadEffort
		}
		settings := map[string]any{"model": model, "developer_instructions": nil}
		if effort != "" {
			settings["reasoning_effort"] = effort
		} else {
			settings["reasoning_effort"] = nil
		}
		params["collaborationMode"] = map[string]any{"mode": dt.mode, "settings": settings}
	}
}

// directSend starts a turn, or steers the running one. A steer the server
// rejects (the turn ended meanwhile) was not taken, so it becomes a new turn.
func (p *Provider) directSend(ctx context.Context, id string, in model.Input) error {
	defer p.actionVersion.Add(1)
	_, d, err := p.directThread(id)
	if err != nil {
		return err
	}
	var input []map[string]any
	if text := providers.TextWithFiles(in); text != "" {
		input = append(input, map[string]any{"type": "text", "text": text})
	}
	for _, img := range in.Images {
		input = append(input, map[string]any{"type": "localImage", "path": img})
	}
	if len(input) == 0 {
		return errors.New("empty message")
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	p.directMu.Lock()
	active := p.direct[id].activeTurn
	p.directMu.Unlock()
	if active != "" {
		err := d.c.call(cctx, "turn/steer", map[string]any{"threadId": id, "input": input, "expectedTurnId": active}, nil)
		var rerr *rpcError
		if err == nil || !errors.As(err, &rerr) {
			return err
		}
		slog.Info("steer rejected; starting a new turn", "provider", providerName, "session_id", gatewayID(id), "error", err)
	}
	params := map[string]any{"threadId": id, "input": input}
	p.turnParams(id, params)
	var r struct {
		Turn Turn `json:"turn"`
	}
	if err := d.c.call(cctx, "turn/start", params, &r); err != nil {
		return err
	}
	p.turnStarted(id, r.Turn.ID)
	p.notifyDirect(id)
	return nil
}

func (p *Provider) turnStarted(id, turn string) {
	p.directMu.Lock()
	defer p.directMu.Unlock()
	dt := p.direct[id]
	if dt == nil || turn == "" {
		return
	}
	dt.activeTurn = turn
	dt.lastFailed, dt.err = false, ""
	// The turn runs with the chosen settings now.
	if dt.model != "" {
		dt.threadModel = dt.model
	}
	if dt.effort != "" {
		dt.threadEffort = dt.effort
	}
	dt.requestedModel, dt.requestedEffort = "", ""
}

// directEvent folds app-server traffic about direct threads into their
// state. It runs on the connection's read loop: no calls from here.
func (p *Provider) directEvent(method string, params json.RawMessage) {
	var ev struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		WillRetry bool `json:"willRetry"`
	}
	json.Unmarshal(params, &ev)
	if ev.ThreadID == "" || !p.hasDirect(ev.ThreadID) {
		return
	}
	switch method {
	case "turn/started":
		p.turnStarted(ev.ThreadID, ev.Turn.ID)
	case "turn/completed":
		p.directMu.Lock()
		if dt := p.direct[ev.ThreadID]; dt != nil {
			dt.activeTurn = ""
			dt.turns++
			dt.lastFailed = ev.Turn.Status == "failed"
			if ev.Turn.Error != nil {
				dt.err = ev.Turn.Error.Message
			}
		}
		p.directMu.Unlock()
	case "error":
		if ev.Error != nil && !ev.WillRetry {
			p.directMu.Lock()
			if dt := p.direct[ev.ThreadID]; dt != nil {
				dt.err = ev.Error.Message
			}
			p.directMu.Unlock()
		}
	case "thread/closed":
		p.directMu.Lock()
		if dt := p.direct[ev.ThreadID]; dt != nil {
			dt.alive, dt.activeTurn = false, ""
		}
		p.directMu.Unlock()
	case "thread/status/changed", "serverRequest/resolved", "thread/settings/updated":
	default:
		if len(method) < 8 || method[:8] != "request:" {
			return // item streaming and token counts change no session state
		}
	}
	go p.notifyDirect(ev.ThreadID)
}

func (p *Provider) hasDirect(id string) bool {
	p.directMu.Lock()
	defer p.directMu.Unlock()
	_, ok := p.direct[id]
	return ok
}

// ownedClosed marks every thread of a dead app-server as gone. A turn that
// was running is lost with it: it is reported as failed, never re-run.
func (p *Provider) ownedClosed(d *serverConn, err error) {
	p.mu.Lock()
	if p.owned == d {
		p.owned = nil
	}
	p.mu.Unlock()
	var ids []string
	p.directMu.Lock()
	for id, dt := range p.direct {
		if !dt.alive {
			continue
		}
		if dt.activeTurn != "" {
			dt.lastFailed = true
			dt.err = "The Codex app-server exited during the turn."
		}
		dt.alive, dt.activeTurn = false, ""
		ids = append(ids, id)
	}
	p.directMu.Unlock()
	if len(ids) > 0 {
		slog.Warn("codex app-server exited", "provider", providerName, "operation", "app-server.exit", "threads", len(ids), "error", err)
	}
	for _, id := range ids {
		p.notifyDirect(id)
	}
}

// DirectStop interrupts a running turn and unloads the thread from the
// gateway's app-server. Its rollout stays where Codex keeps it.
func (p *Provider) DirectStop(ctx context.Context, id string) error {
	defer p.actionVersion.Add(1)
	dt, d, err := p.directThread(id)
	if err != nil {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	p.directMu.Lock()
	active := dt.activeTurn
	p.directMu.Unlock()
	if active != "" {
		if err := d.c.call(cctx, "turn/interrupt", map[string]any{"threadId": id, "turnId": active}, nil); err != nil {
			slog.Warn("interrupt before stop failed", "provider", providerName, "session_id", gatewayID(id), "error", err)
		}
	}
	if err := d.c.call(cctx, "thread/unsubscribe", map[string]any{"threadId": id}, nil); err != nil {
		return err
	}
	p.directMu.Lock()
	dt.alive, dt.activeTurn = false, ""
	p.directMu.Unlock()
	d.mu.Lock()
	delete(d.subscribed, id)
	delete(d.status, id)
	delete(d.pending, id)
	d.mu.Unlock()
	p.notifyDirect(id)
	return nil
}

func (p *Provider) DirectInterrupt(ctx context.Context, id string) error {
	defer p.actionVersion.Add(1)
	dt, d, err := p.directThread(id)
	if err != nil {
		return err
	}
	p.directMu.Lock()
	active := dt.activeTurn
	p.directMu.Unlock()
	if active == "" {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return d.c.call(cctx, "turn/interrupt", map[string]any{"threadId": id, "turnId": active}, nil)
}

func (p *Provider) DirectSetModel(_ context.Context, id, model string) error {
	return p.directSetting(id, func(dt *directThread) {
		dt.model = model
		dt.requestedModel = ""
		if model != dt.threadModel {
			dt.requestedModel = model
		}
	})
}

func (p *Provider) DirectSetEffort(_ context.Context, id, effort string) error {
	return p.directSetting(id, func(dt *directThread) {
		dt.effort = effort
		dt.requestedEffort = ""
		if effort != dt.threadEffort {
			dt.requestedEffort = effort
		}
	})
}

// DirectSetMode switches between Codex's collaboration modes, Default and
// Plan; "" toggles. Like model and effort it applies from the next turn.
func (p *Provider) DirectSetMode(_ context.Context, id, mode string) error {
	switch mode {
	case "", "default", "plan":
	default:
		return fmt.Errorf("unknown Codex mode %q", mode)
	}
	return p.directSetting(id, func(dt *directThread) {
		if mode == "" {
			mode = "plan"
			if dt.mode == "plan" {
				mode = "default"
			}
		}
		dt.mode = mode
	})
}

func (p *Provider) directSetting(id string, set func(*directThread)) error {
	defer p.actionVersion.Add(1)
	p.directMu.Lock()
	dt := p.direct[id]
	if dt == nil || !dt.alive {
		p.directMu.Unlock()
		return providers.ErrNotLive
	}
	set(dt)
	p.directMu.Unlock()
	p.notifyDirect(id)
	return nil
}

// DirectSessions reports the threads the gateway's app-server hosts or
// hosted until it exited.
func (p *Provider) DirectSessions() []providers.DirectInfo {
	d := p.currentOwned()
	p.directMu.Lock()
	type snap struct {
		id string
		dt directThread
	}
	var all []snap
	for id, dt := range p.direct {
		all = append(all, snap{id, *dt})
	}
	p.directMu.Unlock()
	out := make([]providers.DirectInfo, 0, len(all))
	for _, s := range all {
		dt := s.dt
		in := providers.DirectInfo{
			NativeID: s.id, Cwd: dt.cwd, Alive: dt.alive && d != nil,
			Running: dt.activeTurn != "",
			Model:   dt.threadModel, Effort: dt.threadEffort,
			RequestedModel: dt.requestedModel, RequestedEffort: dt.requestedEffort,
			Turns: dt.turns, LastTurnFailed: dt.lastFailed, Error: dt.err,
		}
		switch dt.mode {
		case "plan":
			in.Mode = planLabel
		case "default":
			in.Mode = "Default"
		}
		if in.Alive {
			pending, _ := d.state(s.id, &providers.Live{})
			in.Waiting = pending != ""
			d.mu.Lock()
			if st, ok := d.status[s.id]; ok && st.Type == "active" {
				in.Running = true
			}
			d.mu.Unlock()
		}
		out = append(out, in)
	}
	return out
}
