package dsh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/resident"
)

// A resident run has its own provider instance and process. It never registers
// with runtime.Manager or emits ordinary-session notifications. Native history
// and the display journal are adopted only after shutdown, for later promotion.
type ResidentRunner struct{ p *Provider }
type residentState struct {
	text          string
	usage         resident.Usage
	denials       []string
	questions     []wireQuestion
	questionReady chan struct{}
	questionOnce  sync.Once
}

func (p *Provider) ResidentRunner() *ResidentRunner { return &ResidentRunner{p: p} }
func (*ResidentRunner) Close()                      {}
func (r *ResidentRunner) Rename(_ context.Context, id, title string) error {
	r.p.mu.Lock()
	defer r.p.mu.Unlock()
	s := r.p.sessions[id]
	if s == nil {
		return providers.ErrNotFound
	}
	s.Summary.Title = title
	return r.p.saveLocked(s)
}

func (r *ResidentRunner) Run(ctx context.Context, req resident.RunRequest, hooks resident.RunHooks) (*resident.RunOutput, error) {
	mode := readOnlyMode
	switch req.Permissions.Profile {
	case resident.ProfileReadOnly:
	case resident.ProfileWorkspaceWrite:
		mode = workspaceMode
	default:
		return nil, errors.New("unsupported DSH resident permission profile")
	}
	if len(req.Permissions.AllowedTools) > 0 {
		return nil, errors.New("DSH runs do not accept Claude allowedTools rules")
	}
	if !json.Valid(req.Schema) {
		return nil, errors.New("invalid resident result schema")
	}
	state := &residentState{questionReady: make(chan struct{})}
	p := &Provider{cfg: r.p.cfg, dir: r.p.dir, routes: r.p.routes, sessions: map[string]*session{}, sink: r.p.sink, unattended: state}
	var saved *session
	var stop = func() {}
	defer func() {
		stop()
		p.Close()
		if saved != nil {
			r.p.mu.Lock()
			r.p.sessions[saved.Summary.NativeID] = saved
			r.p.mu.Unlock()
		} else {
			// BeforeSend never persisted a delivery intent. Do not expose an empty
			// orphan conversation after the next Gateway restart.
			for id := range p.sessions {
				os.Remove(filepath.Join(p.dir, id+".json"))
				os.Remove(filepath.Join(p.dir, id+".jsonl"))
			}
		}
	}()
	id, err := p.DirectStart(ctx, providers.DirectStart{Cwd: req.Cwd, Model: req.Model, Effort: req.Effort, Mode: mode})
	if err != nil {
		return nil, err
	}
	s, _ := p.get(id)
	p.mu.Lock()
	selected, c := s.Summary.Model, s.conn
	p.mu.Unlock()
	if c == nil {
		return nil, errors.New("DSH runtime exited before resident prompt")
	}
	var stopOnce sync.Once
	stop = func() {
		stopOnce.Do(func() {
			descendants := providers.Descendants(c.cmd.Process.Pid)
			c.close()
			providers.EndProcesses(descendants, 3*time.Second)
		})
	}
	if hooks.BeforeSend != nil {
		if err = hooks.BeforeSend(id, selected); err != nil {
			return nil, err
		}
	}
	saved = s // retained even if delivery becomes uncertain
	endWatch := make(chan struct{})
	defer close(endWatch)
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-endWatch:
		}
	}()
	begun := time.Now()
	prompt := req.Prompt + "\n\nThe final JSON object must match this schema exactly:\n" + string(req.Schema)
	if err = p.Send(ctx, id, nil, model.Input{Text: prompt}); err != nil {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, err
	}
	if hooks.Sent != nil {
		hooks.Sent()
	}
	waited := make(chan error, 1)
	go func() { waited <- c.call(ctx, "session/wait", map[string]string{"sessionId": id}, nil) }()
	select {
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-state.questionReady:
		// Native questions cannot wait for a person in this process. Store a
		// blocked report and let the resident conversation own the next answer.
		stop()
	case err = <-waited:
	}
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	p.mu.Lock()
	if err != nil && len(state.questions) == 0 {
		p.mu.Unlock()
		return nil, fmt.Errorf("wait for DSH resident run: %w", err)
	}
	out := &resident.RunOutput{NativeID: id, Model: selected, Text: state.text, Usage: state.usage, PermissionDenials: append([]string(nil), state.denials...)}
	out.Usage.DurationMs = time.Since(begun).Milliseconds()
	out.Usage.NumTurns = s.Turns
	if s.info.LastTurnFailed {
		// A failed final turn must not promote an earlier JSON-shaped message
		// into a successful resident result.
		out.Text = ""
		out.Error = s.info.Error
		if out.Error == "" {
			out.Error = "DSH resident turn failed"
		}
	}
	if len(state.questions) > 0 {
		out.Structured = questionResult(state.questions)
		out.Error = ""
	}
	p.mu.Unlock()
	return out, nil
}

// Called only for the private provider instance; normal sessions keep their
// interactive RPC handling. Never grant an unattended approval.
func (p *Provider) residentRequest(c *conn, f frame) {
	var r struct {
		ToolName  string         `json:"toolName"`
		Reason    string         `json:"reason"`
		Questions []wireQuestion `json:"questions"`
	}
	if json.Unmarshal(f.Params, &r) != nil {
		c.write(map[string]any{"jsonrpc": "2.0", "id": f.ID, "error": rpcError{Code: -32602, Message: "invalid request"}})
		return
	}
	switch f.Method {
	case "approval/request":
		p.mu.Lock()
		p.unattended.denials = append(p.unattended.denials, strings.TrimSpace(r.ToolName+" "+r.Reason))
		p.mu.Unlock()
		c.reply(f.ID, map[string]string{"outcome": "rejected"})
	case "user-question/ask":
		if len(r.Questions) == 0 {
			c.write(map[string]any{"jsonrpc": "2.0", "id": f.ID, "error": rpcError{Code: -32602, Message: "questions are required"}})
			return
		}
		p.mu.Lock()
		p.unattended.questions = append(p.unattended.questions, r.Questions...)
		p.mu.Unlock()
		c.reply(f.ID, map[string]bool{"cancelled": true})
		p.unattended.questionOnce.Do(func() { close(p.unattended.questionReady) })
	default:
		c.write(map[string]any{"jsonrpc": "2.0", "id": f.ID, "error": rpcError{Code: -32601, Message: "not available in an unattended run"}})
	}
}

func questionResult(questions []wireQuestion) json.RawMessage {
	var text []string
	options := []string{}
	for _, q := range questions {
		text = append(text, strings.TrimSpace(q.Question+"\n"+q.Detail))
		if len(questions) > 1 {
			for _, o := range q.Options {
				text = append(text, "- "+o.Label)
			}
		}
	}
	if len(questions) == 1 {
		for _, o := range questions[0].Options {
			options = append(options, o.Label)
		}
	}
	report := strings.TrimSpace(strings.Join(text, "\n\n"))
	question := report
	if question == "" {
		question = "DSHからの質問に回答が必要です。"
	}
	// Resident questions have a 2,000-rune limit; preserve the full prompt in
	// the report, including details/options for a multi-question request.
	if runes := []rune(question); len(runes) > 1900 {
		question = string(runes[:1900]) + "…（質問の全文は報告を参照）"
	}
	b, _ := json.Marshal(map[string]any{"schemaVersion": 1, "outcome": "blocked", "summary": "DSHからの質問に回答が必要です。", "report": report, "notification": map[string]any{"requested": true, "key": nil}, "statePatch": []any{}, "memorySummary": nil, "nextWakeAt": nil, "question": map[string]any{"text": question, "options": options}, "artifacts": []any{}})
	return b
}
