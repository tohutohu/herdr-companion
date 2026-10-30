package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/resident"
)

// ResidentRunner runs resident task runs through a sidecar of its own, so
// its events never reach the session runtime's listener and stopping it
// never touches a session. It shares the sessions' configuration: the same
// sidecar executable, the user's claude CLI (login, version, settings) and
// the same API-key billing guard.
//
// Each run is one query() with persistSession: false (no transcript, so it
// never appears among sessions), outputFormat set to the result contract,
// and a permission mode chosen by the task's profile. Permission requests
// the rules did not already allow are denied and reported, never approved.
type ResidentRunner struct {
	p *Provider

	mu   sync.Mutex
	sc   *sidecar
	runs map[string]*residentRun
}

type residentRun struct {
	events  chan sidecarEvent
	denials []string
}

// ResidentRunner returns the provider's resident runner.
func (p *Provider) ResidentRunner() *ResidentRunner {
	return &ResidentRunner{p: p, runs: map[string]*residentRun{}}
}

// Close ends the runner's sidecar and with it any run still going.
func (r *ResidentRunner) Close() {
	r.mu.Lock()
	sc := r.sc
	r.mu.Unlock()
	if sc != nil {
		sc.close()
	}
}

func (r *ResidentRunner) sidecar() (*sidecar, DirectConfig, error) {
	r.p.direct.mu.Lock()
	cfg := r.p.direct.cfg
	r.p.direct.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sc != nil && r.sc.alive() {
		return r.sc, cfg, nil
	}
	if cfg.Sidecar == "" {
		return nil, cfg, fmt.Errorf("%w: the Claude sidecar (herdr-claude-sidecar) was not found", providers.ErrRuntimeUnavailable)
	}
	if cfg.Claude == "" {
		return nil, cfg, fmt.Errorf("%w: the claude CLI was not found; install Claude Code or set claudeBinary", providers.ErrRuntimeUnavailable)
	}
	sc, err := startSidecar(cfg.Sidecar, r.event, r.exited)
	if err != nil {
		return nil, cfg, fmt.Errorf("%w: %v", providers.ErrRuntimeUnavailable, err)
	}
	r.sc = sc
	return sc, cfg, nil
}

func (r *ResidentRunner) event(ev sidecarEvent) {
	r.mu.Lock()
	run := r.runs[ev.SessionID]
	r.mu.Unlock()
	if run == nil {
		return
	}
	switch ev.Event {
	case "result", "exit", "denied", "init", "auth":
		select {
		case run.events <- ev:
		default:
			slog.Warn("resident event dropped", "provider", providerName, "operation", "resident.event", "event", ev.Event)
		}
	}
}

func (r *ResidentRunner) exited(err error) {
	r.mu.Lock()
	runs := make([]*residentRun, 0, len(r.runs))
	for _, run := range r.runs {
		runs = append(runs, run)
	}
	r.mu.Unlock()
	msg := "the Claude sidecar exited"
	if err != nil {
		msg += ": " + err.Error()
	}
	data, _ := json.Marshal(map[string]any{"error": msg})
	for _, run := range runs {
		select {
		case run.events <- sidecarEvent{Event: "exit", Data: data}:
		default:
		}
	}
}

// readTools are allowed in every profile.
var readTools = []string{"Read", "Glob", "Grep", "LS", "TodoWrite"}

// editTools need the workspace_write profile.
var editTools = []string{"Edit", "Write", "MultiEdit", "NotebookEdit"}

// residentPermissions maps a task's permissions to the SDK's options.
func residentPermissions(p resident.Permissions) (mode string, allowed, disallowed []string) {
	allowed = append(allowed, readTools...)
	mode = "default"
	if p.Profile == resident.ProfileWorkspaceWrite {
		// Edits inside the working folder are accepted, anything else
		// still goes to the (denying) permission handler.
		mode = "acceptEdits"
	} else {
		disallowed = append(disallowed, editTools...)
	}
	if p.Network {
		allowed = append(allowed, "WebSearch", "WebFetch")
	} else {
		disallowed = append(disallowed, "WebSearch", "WebFetch")
	}
	allowed = append(allowed, p.AllowedTools...)
	return mode, allowed, disallowed
}

type residentResult struct {
	Subtype          string          `json:"subtype"`
	IsError          bool            `json:"isError"`
	Errors           []string        `json:"errors"`
	NumTurns         int             `json:"numTurns"`
	Result           *string         `json:"result"`
	StructuredOutput json.RawMessage `json:"structuredOutput"`
	Usage            struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	TotalCostUSD      float64  `json:"totalCostUsd"`
	DurationMs        int64    `json:"durationMs"`
	TerminalReason    *string  `json:"terminalReason"`
	PermissionDenials []string `json:"permissionDenials"`
}

// Run executes one resident run and ends its Claude Code process.
func (r *ResidentRunner) Run(ctx context.Context, req resident.RunRequest, hooks resident.RunHooks) (*resident.RunOutput, error) {
	sc, cfg, err := r.sidecar()
	if err != nil {
		return nil, err
	}
	id := newSessionID()
	run := &residentRun{events: make(chan sidecarEvent, 64)}
	r.mu.Lock()
	r.runs[id] = run
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.runs, id)
		r.mu.Unlock()
	}()

	// The agent's tools see which run they belong to (hc and herdr keep
	// working as tools); no session id, since this is not a session.
	env := providers.ChildEnv(os.Environ(), map[string]string{"HC_RESIDENT_RUN": req.RunID})
	if !cfg.AllowAPIKey {
		env = withoutAPIKeys(env)
	}
	envMap := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if k == "HC_SESSION_ID" {
			continue
		}
		envMap[k] = v
	}
	mode, allowed, disallowed := residentPermissions(req.Permissions)
	params := map[string]any{
		"sessionId": id, "resume": false, "cwd": req.Cwd,
		"model": req.Model, "effort": req.Effort, "permissionMode": mode,
		"claudePath": cfg.Claude, "env": envMap,
		"resident":     true,
		"outputFormat": map[string]any{"type": "json_schema", "schema": req.Schema},
		"allowedTools": allowed, "disallowedTools": disallowed,
	}
	stop := func() {
		sctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
		defer cancel()
		sc.call(sctx, "stop", map[string]any{"sessionId": id}, nil)
		// Wait for the process to end so nothing is left running.
		deadline := time.After(10 * time.Second)
		for {
			select {
			case ev := <-run.events:
				if ev.Event == "exit" {
					return
				}
			case <-deadline:
				slog.Warn("resident run did not exit after stop", "provider", providerName, "operation", "resident.stop", "run_id", req.RunID)
				return
			}
		}
	}
	sctx, cancel := context.WithTimeout(ctx, startTimeout)
	var start startReply
	err = sc.call(sctx, "start", params, &start)
	cancel()
	if err != nil {
		var serr *sidecarError
		if errors.As(err, &serr) {
			return nil, fmt.Errorf("start Claude Code: %w", err)
		}
		return nil, fmt.Errorf("%w: %v", providers.ErrRuntimeUnavailable, err)
	}
	if problem := billingProblem(start, cfg.AllowAPIKey); problem != "" {
		stop()
		return nil, errors.New(problem)
	}
	if req.Model != "" && !modelOffered(start, req.Model) {
		stop()
		return nil, fmt.Errorf("model %q is not offered by this Claude Code login; it was not replaced with another", req.Model)
	}
	model := req.Model
	if err := hooks.BeforeSend(id, model); err != nil {
		stop()
		return nil, err
	}
	sendCtx, cancel := context.WithTimeout(ctx, controlTimeout)
	err = sc.call(sendCtx, "send", map[string]any{"sessionId": id, "text": req.Prompt, "images": []string{}}, nil)
	cancel()
	if err != nil {
		stop()
		return nil, fmt.Errorf("send the run's prompt: %w", err)
	}
	hooks.Sent()
	slog.Info("resident run sent to Claude Code", "provider", providerName, "operation", "resident.run", "run_id", req.RunID, "mode", mode, "claude_code", start.ClaudeCodeVersion)

	for {
		select {
		case <-ctx.Done():
			ictx, cancel := context.WithTimeout(context.Background(), controlTimeout)
			sc.call(ictx, "interrupt", map[string]any{"sessionId": id}, nil)
			cancel()
			stop()
			return nil, context.Cause(ctx)
		case ev := <-run.events:
			switch ev.Event {
			case "init":
				var d struct {
					Model string `json:"model"`
				}
				json.Unmarshal(ev.Data, &d)
				if d.Model != "" {
					model = d.Model
				}
			case "denied":
				var d struct {
					ToolName string `json:"toolName"`
					Input    string `json:"input"`
				}
				json.Unmarshal(ev.Data, &d)
				label := d.ToolName
				if d.Input != "" {
					label += "(" + d.Input + ")"
				}
				if !slices.Contains(run.denials, label) {
					run.denials = append(run.denials, label)
				}
			case "auth":
				var d struct {
					Error *string `json:"error"`
				}
				json.Unmarshal(ev.Data, &d)
				if d.Error != nil && *d.Error != "" {
					stop()
					return nil, fmt.Errorf("Claude Code login: %s", *d.Error)
				}
			case "exit":
				var d struct {
					Error *string `json:"error"`
				}
				json.Unmarshal(ev.Data, &d)
				msg := "Claude Code exited before the run finished"
				if d.Error != nil && *d.Error != "" {
					msg += ": " + *d.Error
				}
				return nil, errors.New(msg)
			case "result":
				var res residentResult
				if err := json.Unmarshal(ev.Data, &res); err != nil {
					stop()
					return nil, fmt.Errorf("read the run's result: %w", err)
				}
				stop()
				return residentOutput(id, model, res, run.denials), nil
			}
		}
	}
}

// modelOffered reports whether the login offers model, as an alias or a
// resolved id. Full ids not in the list are left to Claude Code.
func modelOffered(r startReply, model string) bool {
	if len(r.Models) == 0 || strings.HasPrefix(model, "claude-") {
		return true
	}
	for _, m := range r.Models {
		if m.Value == model || m.ResolvedModel == model || baseModel(m.Value) == baseModel(model) {
			return true
		}
	}
	return false
}

func residentOutput(id, model string, res residentResult, denied []string) *resident.RunOutput {
	out := &resident.RunOutput{
		NativeID: id, Model: model,
		Usage: resident.Usage{
			InputTokens:       res.Usage.InputTokens + res.Usage.CacheCreationInputTokens,
			OutputTokens:      res.Usage.OutputTokens,
			CachedInputTokens: res.Usage.CacheReadInputTokens,
			CostUSD:           res.TotalCostUSD,
			DurationMs:        res.DurationMs,
			NumTurns:          res.NumTurns,
		},
	}
	if len(res.StructuredOutput) > 0 && string(res.StructuredOutput) != "null" {
		out.Structured = res.StructuredOutput
	}
	if res.Result != nil {
		out.Text = *res.Result
	}
	out.PermissionDenials = append(out.PermissionDenials, denied...)
	for _, d := range res.PermissionDenials {
		if !slices.ContainsFunc(out.PermissionDenials, func(s string) bool { return s == d || strings.HasPrefix(s, d+"(") }) {
			out.PermissionDenials = append(out.PermissionDenials, d)
		}
	}
	if res.IsError || res.Subtype != "success" {
		msg := strings.Join(res.Errors, "; ")
		if msg == "" {
			msg = "the run ended with " + res.Subtype
		}
		if res.TerminalReason != nil && *res.TerminalReason != "" {
			msg += " (" + *res.TerminalReason + ")"
		}
		out.Error = msg
	}
	return out
}
