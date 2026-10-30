package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/resident"
)

// ResidentRunner runs resident task runs, each on a `codex app-server` of
// its own, separate from the one hosting sessions and reading history: its
// threads, notifications and server requests never reach the session
// runtime, and ending a run never touches a session or another run. When a
// run ends (finished, stopped or timed out) its app-server and every
// process it started are ended, including commands Codex runs in process
// groups of their own, which turn/interrupt leaves running.
//
// Each run is one thread with approvals off (approvalPolicy "never") and
// the sandbox the task's profile chooses; one turn/start carries the result
// contract as outputSchema. The run's events are collected here, not read
// back from thread history. The thread is saved (not ephemeral) so the user
// can continue it as a session; the gateway hides it from the session list
// until then, and removes it (Discard) when it is not needed.
type ResidentRunner struct {
	bin string
	// p names and deletes saved threads through the gateway's app-server.
	p *Provider
	// dial starts an app-server and returns its pid (0: none to reap);
	// tests replace it.
	dial func() (transport, int, error)
}

type codexEvent struct {
	method string
	params json.RawMessage
}

// ResidentRunner returns a runner using the provider's Codex binary.
func (p *Provider) ResidentRunner() *ResidentRunner {
	return &ResidentRunner{bin: p.bin, p: p}
}

// Close is a no-op: every run ends its own app-server.
func (r *ResidentRunner) Close() {}

// Rename names a run's thread, as the session list shows it.
func (r *ResidentRunner) Rename(ctx context.Context, nativeID, title string) error {
	return r.threadCall(ctx, "thread/name/set", map[string]any{"threadId": nativeID, "name": title})
}

// Discard deletes a run's thread and its rollout.
func (r *ResidentRunner) Discard(ctx context.Context, nativeID string) error {
	return r.threadCall(ctx, "thread/delete", map[string]any{"threadId": nativeID})
}

func (r *ResidentRunner) threadCall(ctx context.Context, method string, params map[string]any) error {
	if !threadIDPattern.MatchString(fmt.Sprint(params["threadId"])) {
		return providers.ErrNotFound
	}
	c, err := r.p.client(ctx)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := c.call(cctx, method, params, nil); err != nil {
		var rerr *rpcError
		if errors.As(err, &rerr) && strings.Contains(strings.ToLower(rerr.Message), "not found") {
			return fmt.Errorf("%w: %v", providers.ErrNotFound, err)
		}
		return err
	}
	return nil
}

// residentConn is one run's app-server connection.
type residentConn struct {
	c      *rpcClient
	events chan codexEvent
}

func (h *residentConn) deliver(ev codexEvent) {
	select {
	case h.events <- ev:
	default:
		// Streaming deltas can fill the buffer; terminal events must arrive.
		if ev.method == "turn/completed" || ev.method == "resident/closed" || ev.method == "resident/denied" {
			go func() { h.events <- ev }()
		}
	}
}

func (h *residentConn) Notification(method string, params json.RawMessage) {
	h.deliver(codexEvent{method, params})
}

// Request declines everything: approvals are off, so a request here means
// the agent wanted something the run may not do unattended.
func (h *residentConn) Request(id json.RawMessage, method string, params json.RawMessage) {
	var p struct {
		Command any `json:"command"`
	}
	json.Unmarshal(params, &p)
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	if h.c != nil {
		switch method {
		case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
			h.c.respond(ctx, id, map[string]any{"decision": "decline"})
		case "execCommandApproval", "applyPatchApproval":
			h.c.respond(ctx, id, map[string]any{"decision": "denied"})
		case "mcpServer/elicitation/request":
			h.c.respond(ctx, id, map[string]any{"action": "decline", "content": nil})
		case "item/tool/requestUserInput":
			h.c.respond(ctx, id, map[string]any{"answers": map[string]any{}})
		default:
			h.c.respondError(ctx, id, -32601, "not available in an unattended resident run")
		}
	}
	label := method
	if p.Command != nil {
		b, _ := json.Marshal(p.Command)
		label += " " + string(b)
	}
	b, _ := json.Marshal(map[string]string{"label": label})
	h.deliver(codexEvent{"resident/denied", b})
}

func (h *residentConn) Closed(err error) {
	msg := "the Codex app-server exited"
	if err != nil {
		msg += ": " + err.Error()
	}
	b, _ := json.Marshal(map[string]string{"error": msg})
	h.deliver(codexEvent{"resident/closed", b})
}

// open starts the run's app-server. The returned end function ends it and
// everything it started.
func (r *ResidentRunner) open(ctx context.Context) (*residentConn, func(), error) {
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
		return nil, nil, fmt.Errorf("%w: %v", providers.ErrRuntimeUnavailable, err)
	}
	h := &residentConn{events: make(chan codexEvent, 1024)}
	h.c = newRPCClient(t, h)
	end := func() {
		procs := providers.Descendants(pid)
		h.c.close()
		providers.EndProcesses(procs, 3*time.Second)
	}
	ictx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := h.c.initialize(ictx); err != nil {
		end()
		return nil, nil, fmt.Errorf("%w: codex app-server initialize: %v", providers.ErrRuntimeUnavailable, err)
	}
	slog.Info("started codex app-server for a resident run", "provider", providerName, "operation", "resident.app-server", "pid", pid)
	return h, end, nil
}

// residentSandbox maps a task's profile to Codex's sandbox.
func residentSandbox(p resident.Permissions) (mode string, policy map[string]any) {
	if p.Profile == resident.ProfileWorkspaceWrite {
		return "workspace-write", map[string]any{"type": "workspaceWrite", "networkAccess": true}
	}
	return "read-only", map[string]any{"type": "readOnly", "networkAccess": true}
}

// Run executes one resident run on a thread of its own and unloads it.
func (r *ResidentRunner) Run(ctx context.Context, req resident.RunRequest, hooks resident.RunHooks) (*resident.RunOutput, error) {
	if len(req.Permissions.AllowedTools) > 0 {
		return nil, errors.New("Codex runs have no per-command permission rules; remove allowedTools")
	}
	h, end, err := r.open(ctx)
	if err != nil {
		return nil, err
	}
	defer end()
	c := h.c
	sandbox, policy := residentSandbox(req.Permissions)
	params := map[string]any{
		"cwd": req.Cwd, "ephemeral": false, "approvalPolicy": "never", "sandbox": sandbox,
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
	unload := func() {
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.call(uctx, "thread/unsubscribe", map[string]any{"threadId": thread}, nil); err != nil {
			slog.Debug("unloading a resident thread failed", "provider", providerName, "operation", "resident.unload", "run_id", req.RunID, "error", err)
		}
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
		case ev := <-h.events:
			switch ev.method {
			case "item/completed":
				var p struct {
					ThreadID string `json:"threadId"`
					Item     struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"item"`
				}
				json.Unmarshal(ev.params, &p)
				if p.Item.Type == "agentMessage" && p.ThreadID == thread {
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
				return nil, errors.New(p.Error)
			case "turn/completed":
				var p struct {
					ThreadID string `json:"threadId"`
					Turn     struct {
						ID     string `json:"id"`
						Status string `json:"status"`
						Error  *struct {
							Message string `json:"message"`
						} `json:"error"`
					} `json:"turn"`
				}
				json.Unmarshal(ev.params, &p)
				if p.ThreadID != thread || (turnID != "" && p.Turn.ID != "" && p.Turn.ID != turnID) {
					continue // a subagent's thread
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
