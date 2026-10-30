package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/resident"
)

// ResidentRunner runs resident task runs on a `codex app-server` of its own,
// separate from the one hosting sessions and reading history: its threads,
// notifications and server requests never reach the session runtime, and
// stopping a run never touches a session.
//
// Each run is one ephemeral thread (thread/start ephemeral: true) with
// approvals off (approvalPolicy "never") and the sandbox the task's profile
// chooses; one turn/start carries the result contract as outputSchema. The
// run's events are collected here, not read back from thread history.
type ResidentRunner struct {
	bin string
	// dial starts the app-server; tests replace it.
	dial func() (transport, int, error)

	mu   sync.Mutex
	conn *rpcClient
	runs map[string]*codexRun // by thread id
}

type codexRun struct {
	events  chan codexEvent
	denials []string
}

type codexEvent struct {
	method string
	params json.RawMessage
}

// ResidentRunner returns a runner using the provider's Codex binary.
func (p *Provider) ResidentRunner() *ResidentRunner {
	return &ResidentRunner{bin: p.bin, runs: map[string]*codexRun{}}
}

// Close ends the runner's app-server and with it any run still going.
func (r *ResidentRunner) Close() {
	r.mu.Lock()
	c := r.conn
	r.mu.Unlock()
	if c != nil {
		c.close()
	}
}

type residentHandler struct{ r *ResidentRunner }

func (h residentHandler) Notification(method string, params json.RawMessage) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	json.Unmarshal(params, &p)
	h.r.deliver(p.ThreadID, codexEvent{method, params})
}

// Request declines everything: approvals are off, so a request here means
// the agent wanted something the run may not do unattended.
func (h residentHandler) Request(id json.RawMessage, method string, params json.RawMessage) {
	var p struct {
		ThreadID string `json:"threadId"`
		Command  any    `json:"command"`
		Reason   string `json:"reason"`
	}
	json.Unmarshal(params, &p)
	r := h.r
	r.mu.Lock()
	c := r.conn
	r.mu.Unlock()
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		c.respond(ctx, id, map[string]any{"decision": "decline"})
	case "execCommandApproval", "applyPatchApproval":
		c.respond(ctx, id, map[string]any{"decision": "denied"})
	case "mcpServer/elicitation/request":
		c.respond(ctx, id, map[string]any{"action": "decline", "content": nil})
	case "item/tool/requestUserInput":
		c.respond(ctx, id, map[string]any{"answers": map[string]any{}})
	default:
		c.respondError(ctx, id, -32601, "not available in an unattended resident run")
	}
	label := method
	if p.Command != nil {
		b, _ := json.Marshal(p.Command)
		label += " " + string(b)
	}
	b, _ := json.Marshal(map[string]string{"threadId": p.ThreadID, "label": label})
	r.deliver(p.ThreadID, codexEvent{"resident/denied", b})
}

func (h residentHandler) Closed(err error) {
	r := h.r
	r.mu.Lock()
	r.conn = nil
	runs := make(map[string]*codexRun, len(r.runs))
	for k, v := range r.runs {
		runs[k] = v
	}
	r.mu.Unlock()
	msg := "the Codex app-server exited"
	if err != nil {
		msg += ": " + err.Error()
	}
	b, _ := json.Marshal(map[string]string{"error": msg})
	for id := range runs {
		r.deliver(id, codexEvent{"resident/closed", b})
	}
}

func (r *ResidentRunner) deliver(thread string, ev codexEvent) {
	r.mu.Lock()
	run := r.runs[thread]
	r.mu.Unlock()
	if run == nil {
		return
	}
	select {
	case run.events <- ev:
	default:
		// Item deltas are not needed; the terminal events must not be lost,
		// so the buffer is large and only streaming noise can fill it.
		if ev.method == "turn/completed" || ev.method == "resident/closed" {
			go func() { run.events <- ev }()
		}
	}
}

func (r *ResidentRunner) client(ctx context.Context) (*rpcClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn != nil && r.conn.alive() {
		return r.conn, nil
	}
	dial := r.dial
	if dial == nil {
		dial = func() (transport, int, error) {
			// The agent's tools (hc, herdr) can tell they run in a resident run.
			t, err := startStdio(r.bin, map[string]string{"HC_RESIDENT": "1"})
			if err != nil {
				return nil, 0, err
			}
			return t, t.pid(), nil
		}
	}
	t, pid, err := dial()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", providers.ErrRuntimeUnavailable, err)
	}
	c := newRPCClient(t, residentHandler{r})
	ictx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := c.initialize(ictx); err != nil {
		c.close()
		return nil, fmt.Errorf("%w: codex app-server initialize: %v", providers.ErrRuntimeUnavailable, err)
	}
	r.conn = c
	slog.Info("started codex app-server for resident runs", "provider", providerName, "operation", "resident.app-server", "pid", pid)
	return c, nil
}

// residentSandbox maps a task's profile to Codex's sandbox.
func residentSandbox(p resident.Permissions) (mode string, policy map[string]any) {
	if p.Profile == resident.ProfileWorkspaceWrite {
		return "workspace-write", map[string]any{"type": "workspaceWrite", "networkAccess": p.Network}
	}
	return "read-only", map[string]any{"type": "readOnly", "networkAccess": p.Network}
}

// Run executes one resident run on an ephemeral thread and unloads it.
func (r *ResidentRunner) Run(ctx context.Context, req resident.RunRequest, hooks resident.RunHooks) (*resident.RunOutput, error) {
	if len(req.Permissions.AllowedTools) > 0 {
		return nil, errors.New("Codex runs have no per-command permission rules; remove allowedTools")
	}
	c, err := r.client(ctx)
	if err != nil {
		return nil, err
	}
	sandbox, policy := residentSandbox(req.Permissions)
	params := map[string]any{
		"cwd": req.Cwd, "ephemeral": true, "approvalPolicy": "never", "sandbox": sandbox,
	}
	if req.Model != "" {
		params["model"] = req.Model
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	var opened threadOpened
	err = c.call(cctx, "thread/start", params, &opened)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("start a Codex thread: %w", err)
	}
	thread := opened.Thread.ID
	if thread == "" {
		return nil, errors.New("codex app-server returned no thread id")
	}
	run := &codexRun{events: make(chan codexEvent, 1024)}
	r.mu.Lock()
	r.runs[thread] = run
	r.mu.Unlock()
	unload := func() {
		uctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		if err := c.call(uctx, "thread/unsubscribe", map[string]any{"threadId": thread}, nil); err != nil {
			slog.Warn("unloading a resident thread failed", "provider", providerName, "operation", "resident.unload", "run_id", req.RunID, "error", err)
		}
		r.mu.Lock()
		delete(r.runs, thread)
		r.mu.Unlock()
	}
	if req.Model != "" && opened.Model != "" && opened.Model != req.Model {
		unload()
		return nil, fmt.Errorf("Codex would run %s instead of the task's model %s; it was not replaced", opened.Model, req.Model)
	}
	model := opened.Model
	if err := hooks.BeforeSend(thread, model); err != nil {
		unload()
		return nil, err
	}
	turn := map[string]any{
		"threadId": thread, "input": []map[string]any{{"type": "text", "text": req.Prompt}},
		"outputSchema": req.Schema, "approvalPolicy": "never", "sandboxPolicy": policy,
	}
	if req.Model != "" {
		turn["model"] = req.Model
	}
	if req.Effort != "" {
		turn["effort"] = req.Effort
	}
	var started struct {
		Turn Turn `json:"turn"`
	}
	tctx, cancel := context.WithTimeout(ctx, callTimeout)
	err = c.call(tctx, "turn/start", turn, &started)
	cancel()
	if err != nil {
		unload()
		return nil, fmt.Errorf("start the run's turn: %w", err)
	}
	hooks.Sent()
	turnID := started.Turn.ID
	slog.Info("resident run sent to Codex", "provider", providerName, "operation", "resident.run", "run_id", req.RunID, "sandbox", sandbox, "model", model)

	out := &resident.RunOutput{NativeID: thread, Model: model}
	begun := time.Now()
	for {
		select {
		case <-ctx.Done():
			ictx, cancel := context.WithTimeout(context.Background(), callTimeout)
			if turnID != "" {
				c.call(ictx, "turn/interrupt", map[string]any{"threadId": thread, "turnId": turnID}, nil)
			}
			cancel()
			unload()
			return nil, context.Cause(ctx)
		case ev := <-run.events:
			switch ev.method {
			case "item/completed":
				var p struct {
					Item struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"item"`
				}
				json.Unmarshal(ev.params, &p)
				if p.Item.Type == "agentMessage" {
					out.Text = p.Item.Text
				}
			case "thread/tokenUsage/updated":
				var p struct {
					TokenUsage struct {
						Total struct {
							InputTokens       int64 `json:"inputTokens"`
							CachedInputTokens int64 `json:"cachedInputTokens"`
							OutputTokens      int64 `json:"outputTokens"`
						} `json:"total"`
					} `json:"tokenUsage"`
				}
				json.Unmarshal(ev.params, &p)
				t := p.TokenUsage.Total
				out.Usage.InputTokens, out.Usage.CachedInputTokens, out.Usage.OutputTokens = t.InputTokens, t.CachedInputTokens, t.OutputTokens
			case "turn/started":
				var p struct {
					Turn Turn `json:"turn"`
				}
				json.Unmarshal(ev.params, &p)
				if turnID == "" {
					turnID = p.Turn.ID
				}
			case "resident/denied":
				var p struct {
					Label string `json:"label"`
				}
				json.Unmarshal(ev.params, &p)
				out.PermissionDenials = append(out.PermissionDenials, p.Label)
			case "resident/closed":
				var p struct {
					Error string `json:"error"`
				}
				json.Unmarshal(ev.params, &p)
				r.mu.Lock()
				delete(r.runs, thread)
				r.mu.Unlock()
				return nil, errors.New(p.Error)
			case "turn/completed":
				var p struct {
					Turn struct {
						ID     string `json:"id"`
						Status string `json:"status"`
						Error  *struct {
							Message string `json:"message"`
						} `json:"error"`
					} `json:"turn"`
				}
				json.Unmarshal(ev.params, &p)
				if turnID != "" && p.Turn.ID != "" && p.Turn.ID != turnID {
					continue
				}
				out.Usage.DurationMs = time.Since(begun).Milliseconds()
				out.Usage.NumTurns = 1
				if p.Turn.Status != "completed" {
					out.Error = "the Codex turn ended " + p.Turn.Status
					if p.Turn.Error != nil && p.Turn.Error.Message != "" {
						out.Error += ": " + p.Turn.Error.Message
					}
				}
				unload()
				return out, nil
			}
		}
	}
}
