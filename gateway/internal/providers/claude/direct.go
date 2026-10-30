package claude

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// DirectConfig says how the gateway runs Claude Code sessions itself.
type DirectConfig struct {
	// Sidecar is the herdr-claude-sidecar executable.
	Sidecar string
	// Claude is the Claude Code CLI the SDK runs: the user's own install, so
	// sessions get the same version, login, settings and subscription.
	Claude string
	// AllowAPIKey lets a session run on an API key (ANTHROPIC_API_KEY or an
	// apiKeyHelper) instead of the user's subscription. Off by default: the
	// key is removed from the environment and such a session is refused.
	AllowAPIKey bool
}

const (
	startTimeout   = 2 * time.Minute
	controlTimeout = 30 * time.Second
	// maxPartial bounds the streamed text kept for a reply in progress.
	maxPartial = 64 << 10
	// streamingPrefix marks the in-progress reply, shown until the
	// transcript has the finished message.
	streamingPrefix = "streaming:"
)

// permissionRequest is a canUseTool call waiting for the user.
type permissionRequest struct {
	ID             string          `json:"requestId"` // the tool_use id
	ToolName       string          `json:"toolName"`
	Input          json.RawMessage `json:"input"`
	Title          string          `json:"title"`
	DisplayName    string          `json:"displayName"`
	Description    string          `json:"description"`
	DecisionReason string          `json:"decisionReason"`
	BlockedPath    string          `json:"blockedPath"`
	CanAlwaysAllow bool            `json:"canAlwaysAllow"`
	received       time.Time
	// autoMode: the session's model could run in auto mode when this was
	// asked, so a plan can be approved into it as in the CLI.
	autoMode bool
}

type directSession struct {
	cwd     string
	alive   bool
	running bool
	// model is what the latest reply used; effort and mode what the session
	// runs with now. requested* wait for the next model request.
	model, effort, mode             string
	requestedModel, requestedEffort string
	turns                           int
	lastFailed                      bool
	err                             string
	pending                         []*permissionRequest
	partialID, partialText          string
	partialAt                       time.Time
	// autoModels are the model ids the account can run in auto mode.
	autoModels map[string]bool
}

type directState struct {
	mu       sync.Mutex
	cfg      DirectConfig
	sc       *sidecar
	sessions map[string]*directSession
	listener func(string)
}

// SetDirect configures how sessions are run.
func (p *Provider) SetDirect(cfg DirectConfig) {
	p.direct.mu.Lock()
	p.direct.cfg = cfg
	p.direct.mu.Unlock()
}

func (p *Provider) SetDirectListener(f func(string)) {
	p.direct.mu.Lock()
	p.direct.listener = f
	p.direct.mu.Unlock()
}

// SettingsApply: Query.setModel and the effort flag apply from the session's
// next model request, which may be inside the running turn.
func (p *Provider) SettingsApply() string { return providers.ApplyNextRequest }

func (p *Provider) notifyDirect(id string) {
	p.direct.mu.Lock()
	l := p.direct.listener
	p.direct.mu.Unlock()
	if l != nil {
		l(id)
	}
}

// Close stops the sidecar and with it every Claude Code process it runs.
func (p *Provider) Close() {
	p.direct.mu.Lock()
	sc := p.direct.sc
	p.direct.mu.Unlock()
	if sc != nil {
		sc.close()
	}
}

func (p *Provider) sidecar() (*sidecar, error) {
	d := &p.direct
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sc != nil && d.sc.alive() {
		return d.sc, nil
	}
	if d.cfg.Sidecar == "" {
		return nil, fmt.Errorf("%w: the Claude sidecar (herdr-claude-sidecar) was not found", providers.ErrRuntimeUnavailable)
	}
	if d.cfg.Claude == "" {
		return nil, fmt.Errorf("%w: the claude CLI was not found; install Claude Code or set claudeBinary", providers.ErrRuntimeUnavailable)
	}
	sc, err := startSidecar(d.cfg.Sidecar, p.sidecarEvent, p.sidecarExited)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", providers.ErrRuntimeUnavailable, err)
	}
	d.sc = sc
	return sc, nil
}

func newSessionID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// sdkMode maps a catalog mode id to the SDK's PermissionMode.
func sdkMode(mode string) string {
	if mode == "manual" {
		return "default"
	}
	return mode
}

type startReply struct {
	PermissionMode    string `json:"permissionMode"`
	ClaudeCodeVersion string `json:"claudeCodeVersion"`
	Models            []struct {
		Value         string `json:"value"`
		ResolvedModel string `json:"resolvedModel"`
		AutoMode      bool   `json:"supportsAutoMode"`
	} `json:"models"`
	Account struct {
		SubscriptionType *string `json:"subscriptionType"`
		APIKeySource     *string `json:"apiKeySource"`
		APIProvider      *string `json:"apiProvider"`
	} `json:"account"`
}

// billingProblem explains why a session must not run with the account the
// SDK reported, or "" when it may.
func billingProblem(r startReply, allowAPIKey bool) string {
	a := r.Account
	if a.APIProvider != nil && *a.APIProvider != "firstParty" {
		return "" // Bedrock, Vertex or a gateway the user configured deliberately
	}
	if a.APIKeySource != nil && *a.APIKeySource != "" && *a.APIKeySource != "none" && !allowAPIKey {
		return "Claude Code would use an API key (" + *a.APIKeySource + ") and bill it per token instead of your subscription. " +
			"Remove it, or set claudeAllowApiKey in the gateway config to allow it."
	}
	if (a.SubscriptionType == nil || *a.SubscriptionType == "") && (a.APIKeySource == nil || *a.APIKeySource == "" || *a.APIKeySource == "none") {
		return "Claude Code is not logged in. Run `claude` on the Mac and log in with /login, then try again."
	}
	return ""
}

// DirectStart runs a Claude Code session through the sidecar. A new session
// gets an id chosen here, so it is known before the first reply; a resumed
// one keeps its id and transcript.
func (p *Provider) DirectStart(ctx context.Context, opts providers.DirectStart) (string, error) {
	sc, err := p.sidecar()
	if err != nil {
		return "", err
	}
	id := opts.Resume
	if id == "" {
		id = newSessionID()
	} else if !sessionIDPattern.MatchString(id) {
		return "", providers.ErrNotFound
	}
	p.direct.mu.Lock()
	cfg := p.direct.cfg
	if s := p.direct.sessions[id]; s != nil && s.alive {
		p.direct.mu.Unlock()
		return "", errors.New("the session is already running")
	}
	p.direct.mu.Unlock()

	drop := map[string]string{"HC_SESSION_ID": gatewayID(id)}
	env := providers.ChildEnv(os.Environ(), drop)
	if !cfg.AllowAPIKey {
		env = withoutAPIKeys(env)
	}
	envMap := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		envMap[k] = v
	}
	params := map[string]any{
		"sessionId": id, "resume": opts.Resume != "", "cwd": opts.Cwd,
		"model": opts.Model, "effort": opts.Effort, "permissionMode": sdkMode(opts.Mode),
		"claudePath": cfg.Claude, "env": envMap,
	}
	// Mark the session before the start returns: the sidecar's events for
	// it (state, permission requests) may arrive first.
	p.direct.mu.Lock()
	if p.direct.sessions == nil {
		p.direct.sessions = map[string]*directSession{}
	}
	p.direct.sessions[id] = &directSession{cwd: opts.Cwd, effort: opts.Effort, mode: sdkMode(opts.Mode)}
	p.direct.mu.Unlock()
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), startTimeout)
	defer cancel()
	var r startReply
	if err := sc.call(sctx, "start", params, &r); err != nil {
		p.dropDirect(id)
		var serr *sidecarError
		if errors.As(err, &serr) {
			if opts.Resume != "" && strings.Contains(strings.ToLower(serr.msg), "no conversation found") {
				return "", fmt.Errorf("%w: %v", providers.ErrNotFound, err)
			}
			return "", fmt.Errorf("start Claude Code: %w", err)
		}
		return "", fmt.Errorf("%w: %v", providers.ErrRuntimeUnavailable, err)
	}
	if problem := billingProblem(r, cfg.AllowAPIKey); problem != "" {
		sc.call(context.WithoutCancel(ctx), "stop", map[string]any{"sessionId": id}, nil)
		p.dropDirect(id)
		return "", errors.New(problem)
	}
	p.direct.mu.Lock()
	if s := p.direct.sessions[id]; s != nil {
		s.alive = true
		if r.PermissionMode != "" {
			s.mode = r.PermissionMode
		}
		s.autoModels = map[string]bool{}
		for _, m := range r.Models {
			if m.AutoMode {
				s.autoModels[m.Value] = true
				s.autoModels[m.ResolvedModel] = true
			}
		}
	}
	p.direct.mu.Unlock()
	slog.Info("claude session started", "provider", providerName, "session_id", gatewayID(id), "operation", "direct.start",
		"resume", opts.Resume != "", "claude_code", r.ClaudeCodeVersion, "mode", r.PermissionMode)
	p.notifyDirect(id)
	if !opts.Prompt.Empty() {
		if err := p.directSend(ctx, id, opts.Prompt); err != nil {
			return id, fmt.Errorf("send first prompt: %w", err)
		}
	}
	return id, nil
}

func withoutAPIKeys(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if k == "ANTHROPIC_API_KEY" || k == "ANTHROPIC_AUTH_TOKEN" {
			slog.Warn("not passing "+k+" to Claude Code: sessions use the subscription (set claudeAllowApiKey to allow API billing)", "provider", providerName)
			continue
		}
		out = append(out, kv)
	}
	return out
}

func (p *Provider) dropDirect(id string) {
	p.direct.mu.Lock()
	delete(p.direct.sessions, id)
	p.direct.mu.Unlock()
	p.notifyDirect(id)
}

func (p *Provider) liveDirect(id string) (*sidecar, error) {
	p.direct.mu.Lock()
	defer p.direct.mu.Unlock()
	s := p.direct.sessions[id]
	if s == nil || !s.alive || p.direct.sc == nil || !p.direct.sc.alive() {
		return nil, providers.ErrNotLive
	}
	return p.direct.sc, nil
}

func (p *Provider) directSend(ctx context.Context, id string, in model.Input) error {
	sc, err := p.liveDirect(id)
	if err != nil {
		return err
	}
	text := providers.TextWithFiles(in)
	if text == "" && len(in.Images) == 0 {
		return errors.New("empty message")
	}
	images := in.Images
	if images == nil {
		images = []string{}
	}
	cctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	if err := sc.call(cctx, "send", map[string]any{"sessionId": id, "text": text, "images": images}, nil); err != nil {
		return err
	}
	p.direct.mu.Lock()
	if s := p.direct.sessions[id]; s != nil {
		// The SDK reports running a moment later; show it now.
		s.running = true
		s.lastFailed, s.err = false, ""
	}
	p.direct.mu.Unlock()
	p.notifyDirect(id)
	return nil
}

func (p *Provider) control(ctx context.Context, id, method string, params map[string]any) error {
	sc, err := p.liveDirect(id)
	if err != nil {
		return err
	}
	params["sessionId"] = id
	cctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	return sc.call(cctx, method, params, nil)
}

func (p *Provider) DirectStop(ctx context.Context, id string) error {
	if _, err := p.liveDirect(id); err != nil {
		return nil
	}
	return p.control(ctx, id, "stop", map[string]any{})
}

func (p *Provider) DirectInterrupt(ctx context.Context, id string) error {
	return p.control(ctx, id, "interrupt", map[string]any{})
}

func (p *Provider) DirectSetModel(ctx context.Context, id, m string) error {
	if err := p.control(ctx, id, "setModel", map[string]any{"model": m}); err != nil {
		return err
	}
	p.direct.mu.Lock()
	if s := p.direct.sessions[id]; s != nil {
		s.requestedModel = m
	}
	p.direct.mu.Unlock()
	p.notifyDirect(id)
	return nil
}

func (p *Provider) DirectSetEffort(ctx context.Context, id, effort string) error {
	if err := p.control(ctx, id, "setEffort", map[string]any{"effort": effort}); err != nil {
		return err
	}
	p.direct.mu.Lock()
	if s := p.direct.sessions[id]; s != nil {
		s.requestedEffort, s.effort = effort, effort
	}
	p.direct.mu.Unlock()
	p.notifyDirect(id)
	return nil
}

// directModeCycle is the order Claude Code's Shift+Tab goes through.
var directModeCycle = []string{"default", "acceptEdits", "plan"}

func (p *Provider) DirectSetMode(ctx context.Context, id, mode string) error {
	p.direct.mu.Lock()
	current := ""
	if s := p.direct.sessions[id]; s != nil {
		current = s.mode
	}
	p.direct.mu.Unlock()
	mode = sdkMode(mode)
	if mode == "" {
		mode = directModeCycle[0]
		for i, m := range directModeCycle {
			if m == current {
				mode = directModeCycle[(i+1)%len(directModeCycle)]
			}
		}
	}
	if mode == "bypassPermissions" {
		return errors.New("the gateway never sets bypass permissions mode")
	}
	if err := p.control(ctx, id, "setMode", map[string]any{"mode": mode}); err != nil {
		return err
	}
	p.direct.mu.Lock()
	if s := p.direct.sessions[id]; s != nil {
		s.mode = mode
	}
	p.direct.mu.Unlock()
	p.notifyDirect(id)
	return nil
}

// DirectSessions reports the sessions the sidecar runs or ran until it exited.
func (p *Provider) DirectSessions() []providers.DirectInfo {
	p.direct.mu.Lock()
	defer p.direct.mu.Unlock()
	scAlive := p.direct.sc != nil && p.direct.sc.alive()
	out := make([]providers.DirectInfo, 0, len(p.direct.sessions))
	for id, s := range p.direct.sessions {
		out = append(out, providers.DirectInfo{
			NativeID: id, Cwd: s.cwd, Alive: s.alive && scAlive,
			Running: s.running, Waiting: len(s.pending) > 0,
			Model: s.model, Effort: s.effort, Mode: modeLabel(s.mode),
			RequestedModel: s.requestedModel, RequestedEffort: s.requestedEffort,
			Turns: s.turns, LastTurnFailed: s.lastFailed, Error: s.err,
		})
	}
	return out
}

// sidecarEvent folds an event into the session's state. It runs on the
// sidecar's read loop: no calls to the sidecar from here.
func (p *Provider) sidecarEvent(ev sidecarEvent) {
	id := ev.SessionID
	p.direct.mu.Lock()
	s := p.direct.sessions[id]
	if s == nil {
		p.direct.mu.Unlock()
		return
	}
	notify := true
	switch ev.Event {
	case "init":
		var d struct {
			Model          string  `json:"model"`
			PermissionMode string  `json:"permissionMode"`
			Effort         *string `json:"effort"`
		}
		json.Unmarshal(ev.Data, &d)
		s.model = d.Model
		if d.PermissionMode != "" {
			s.mode = d.PermissionMode
		}
		if d.Effort != nil && *d.Effort != "" {
			s.effort = *d.Effort
		}
	case "state":
		var d struct {
			State string `json:"state"`
		}
		json.Unmarshal(ev.Data, &d)
		s.running = d.State == "running" || d.State == "requires_action"
	case "mode":
		var d struct {
			PermissionMode string `json:"permissionMode"`
		}
		json.Unmarshal(ev.Data, &d)
		s.mode = d.PermissionMode
	case "permission":
		var r permissionRequest
		if json.Unmarshal(ev.Data, &r) == nil && r.ID != "" {
			r.received = time.Now()
			r.autoMode = s.autoModels[baseModel(s.model)]
			s.pending = append(s.pending, &r)
		}
	case "permissionResolved":
		var d struct {
			RequestID string `json:"requestId"`
		}
		json.Unmarshal(ev.Data, &d)
		s.pending = withoutRequest(s.pending, d.RequestID)
	case "messageStart":
		var d struct {
			MessageID string `json:"messageId"`
			Model     string `json:"model"`
		}
		json.Unmarshal(ev.Data, &d)
		s.partialID, s.partialText, s.partialAt = d.MessageID, "", time.Now()
		// This request runs with whatever was asked for before it.
		s.requestedModel, s.requestedEffort = "", ""
		if d.Model != "" {
			s.model = d.Model
		}
	case "delta":
		var d struct {
			Text string `json:"text"`
		}
		json.Unmarshal(ev.Data, &d)
		if len(s.partialText) < maxPartial {
			s.partialText += d.Text
		}
		notify = false
	case "assistant":
		var d struct {
			Model string `json:"model"`
		}
		json.Unmarshal(ev.Data, &d)
		if d.Model != "" && d.Model != "<synthetic>" {
			s.model = d.Model
		}
		notify = false
	case "result":
		var d struct {
			IsError bool     `json:"isError"`
			Subtype string   `json:"subtype"`
			Errors  []string `json:"errors"`
		}
		json.Unmarshal(ev.Data, &d)
		s.turns++
		s.running = false
		s.partialID, s.partialText = "", ""
		s.lastFailed = d.IsError && d.Subtype != "error_during_execution"
		s.err = strings.Join(d.Errors, "; ")
		if s.lastFailed && s.err == "" {
			s.err = "The turn ended with an error (" + d.Subtype + ")."
		}
	case "auth":
		var d struct {
			Error *string `json:"error"`
		}
		json.Unmarshal(ev.Data, &d)
		if d.Error != nil {
			s.err = *d.Error
		}
	case "exit":
		var d struct {
			Error *string `json:"error"`
		}
		json.Unmarshal(ev.Data, &d)
		markExited(s, d.Error)
	default:
		notify = false
	}
	p.direct.mu.Unlock()
	if notify {
		p.notifyDirect(id)
	}
}

// markExited ends a session whose process is gone. A turn it was running is
// reported as failed; nothing is retried.
func markExited(s *directSession, reason *string) {
	if s.running {
		s.lastFailed = true
		s.err = "Claude Code exited during the turn."
		if reason != nil && *reason != "" {
			s.err += " " + *reason
		}
	}
	s.alive, s.running, s.pending = false, false, nil
	s.partialID, s.partialText = "", ""
}

func (p *Provider) sidecarExited(err error) {
	p.direct.mu.Lock()
	var ids []string
	for id, s := range p.direct.sessions {
		if s.alive {
			reason := "the Claude sidecar exited"
			markExited(s, &reason)
			ids = append(ids, id)
		}
	}
	p.direct.mu.Unlock()
	if len(ids) > 0 {
		slog.Warn("claude sidecar exited", "provider", providerName, "operation", "sidecar.exit", "sessions", len(ids), "error", err)
	}
	for _, id := range ids {
		p.notifyDirect(id)
	}
}

func withoutRequest(rs []*permissionRequest, id string) []*permissionRequest {
	out := rs[:0:0]
	for _, r := range rs {
		if r.ID != id {
			out = append(out, r)
		}
	}
	return out
}

// directSnapshot copies what a read of the session needs.
func (p *Provider) directSnapshot(id string) (pending []*permissionRequest, partialID, partialText string, partialAt time.Time) {
	p.direct.mu.Lock()
	defer p.direct.mu.Unlock()
	s := p.direct.sessions[id]
	if s == nil || !s.alive {
		return nil, "", "", time.Time{}
	}
	pending = append(pending, s.pending...)
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].received.Before(pending[j].received) })
	return pending, s.partialID, s.partialText, s.partialAt
}

// parseLive is the Live the transcript parser sees for a direct session:
// never blocked, since its pending tool calls are the runtime's interactions,
// not dialogs to read off a screen.
func parseLive(live *providers.Live) *providers.Live {
	cp := *live
	if cp.State == providers.StateBlocked {
		cp.State = providers.StateWorking
	}
	return &cp
}

// --- interactions ---

// The plan dialog's approvals. Where auto mode is available, Claude Code
// 2.1.300 offers it in place of accepting edits (plan_screen_2_1_300_40cols.txt).
const (
	planAutoMode   = "Yes, and use auto mode"
	planAutoAccept = "Yes, auto-accept edits"
	planManual     = "Yes, manually approve edits"
)

// baseModel drops a context-window suffix such as "[1m]" from a model id.
func baseModel(m string) string {
	if i := strings.IndexByte(m, '['); i > 0 {
		return m[:i]
	}
	return m
}

func (r *permissionRequest) interaction() (*model.Interaction, []model.Block) {
	switch r.ToolName {
	case toolAskUserQuestion:
		ia, err := askUserQuestionInteraction(contentBlock{ID: r.ID, Name: r.ToolName, Input: r.Input})
		if err == nil {
			ia.State = model.InteractionPending
			return ia, nil
		}
	case toolExitPlanMode:
		var in struct {
			Plan         string `json:"plan"`
			PlanFilePath string `json:"planFilePath"`
		}
		json.Unmarshal(r.Input, &in)
		ia := planInteraction(r.ID)
		ia.State = model.InteractionPending
		first := planAutoAccept
		if r.autoMode {
			first = planAutoMode
		}
		ia.Questions[0].Options = planOptions([]model.Option{{Label: first}, {Label: planManual}})
		var blocks []model.Block
		if in.Plan != "" {
			blocks = append(blocks, model.TextBlock("Plan:\n"+in.Plan))
		}
		if fb, ok := planFile(in.PlanFilePath); ok {
			blocks = append(blocks, fb)
		}
		return ia, blocks
	}
	var in map[string]any
	json.Unmarshal(r.Input, &in)
	title := r.Title
	if title == "" {
		title = "Allow " + r.ToolName + "?"
	}
	detail := approvalDetail(r.ToolName, in, r.Input)
	if r.Description != "" {
		detail = r.Description + "\n" + detail
	}
	if r.DecisionReason != "" {
		detail += "\n" + r.DecisionReason
	}
	decisions := []string{model.DecisionApprove}
	if r.CanAlwaysAllow {
		decisions = append(decisions, model.DecisionApproveSession)
	}
	decisions = append(decisions, model.DecisionDeny)
	return &model.Interaction{
		ID: r.ID, Type: model.InteractionApproval, State: model.InteractionPending,
		Title: title, Detail: model.Truncate(detail, 1000), Supported: true, Decisions: decisions,
	}, nil
}

// pendingKind is what the session waits for, as Summary.Pending.
func pendingKind(pending []*permissionRequest) model.InteractionType {
	var kind model.InteractionType
	for _, r := range pending {
		ia, _ := r.interaction()
		kind = ia.Awaits()
		if kind == model.InteractionQuestions {
			break
		}
	}
	return kind
}

// withDirectState puts the runtime's pending requests into the transcript's
// messages: a question or plan card the transcript already shows (same tool
// call id) is replaced by the live one, anything else is appended. The reply
// still streaming is appended until the transcript has it. msgs are shared
// and never modified.
func withDirectState(msgs []model.Message, pending []*permissionRequest, partialID, partialText string, partialAt time.Time, inTranscript bool) []model.Message {
	streaming := partialID != "" && strings.TrimSpace(partialText) != "" && !inTranscript
	if len(pending) == 0 && !streaming {
		return msgs
	}
	out := append([]model.Message(nil), msgs...)
	for _, r := range pending {
		ia, extra := r.interaction()
		if !replaceInteraction(out, r.ID, ia) {
			blocks := append(extra, model.Block{Type: model.BlockInteraction, Interaction: ia})
			out = append(out, model.Message{ID: r.ID, Role: model.RoleAssistant, Timestamp: r.received.UTC(), Blocks: blocks})
		}
	}
	if streaming {
		out = append(out, model.Message{
			ID: streamingPrefix + partialID, Role: model.RoleAssistant, Timestamp: partialAt.UTC(),
			Blocks: []model.Block{model.TextBlock(partialText)},
		})
	}
	return out
}

// replaceInteraction swaps the card with the given id for ia in a copy of
// its message.
func replaceInteraction(msgs []model.Message, id string, ia *model.Interaction) bool {
	for i := len(msgs) - 1; i >= 0; i-- {
		for j, b := range msgs[i].Blocks {
			if b.Type == model.BlockInteraction && b.Interaction != nil && b.Interaction.ID == id {
				m := msgs[i]
				m.Blocks = append([]model.Block(nil), m.Blocks...)
				m.Blocks[j] = model.Block{Type: model.BlockInteraction, Interaction: ia}
				msgs[i] = m
				return true
			}
		}
	}
	return false
}

// directRespond answers a pending canUseTool request.
func (p *Provider) directRespond(ctx context.Context, id string, resp model.InteractionResponse) error {
	pending, _, _, _ := p.directSnapshot(id)
	var req *permissionRequest
	for _, r := range pending {
		if r.ID == resp.InteractionID {
			req = r
		}
	}
	if req == nil {
		return providers.ErrInteractionGone
	}
	ia, _ := req.interaction()
	params := map[string]any{"requestId": req.ID}
	setMode := ""
	switch {
	case req.ToolName == toolAskUserQuestion && ia.Type == model.InteractionQuestions:
		answers, err := questionAnswers(ia, resp)
		if err != nil {
			return err
		}
		var in map[string]any
		json.Unmarshal(req.Input, &in)
		in["answers"] = answers
		params["behavior"], params["updatedInput"] = "allow", in
	case req.ToolName == toolExitPlanMode:
		a := resp.Answers["0"]
		label := ""
		if len(a.Selected) > 0 {
			label = a.Selected[0]
		}
		switch {
		case label == planAutoMode:
			params["behavior"], setMode = "allow", "auto"
		case label == planAutoAccept || resp.Decision == model.DecisionApprove:
			params["behavior"], setMode = "allow", "acceptEdits"
		case label == planManual:
			params["behavior"], setMode = "allow", "default"
		case strings.TrimSpace(a.Text) != "":
			params["behavior"], params["message"] = "deny", "The user doesn't want to proceed with this plan yet. The user said:\n"+a.Text
		default:
			params["behavior"], params["message"] = "deny", "The user wants to keep planning."
		}
	default:
		switch resp.Decision {
		case model.DecisionApprove:
			params["behavior"] = "allow"
		case model.DecisionApproveSession:
			params["behavior"], params["alwaysAllow"] = "allow", true
		case model.DecisionDeny:
			params["behavior"], params["message"] = "deny", "The user denied this."
		default:
			return fmt.Errorf("unknown decision %q", resp.Decision)
		}
	}
	if err := p.control(ctx, id, "respond", params); err != nil {
		var serr *sidecarError
		if errors.As(err, &serr) && strings.Contains(serr.msg, "no longer pending") {
			return providers.ErrInteractionGone
		}
		return err
	}
	p.direct.mu.Lock()
	if s := p.direct.sessions[id]; s != nil {
		s.pending = withoutRequest(s.pending, req.ID)
	}
	p.direct.mu.Unlock()
	if setMode != "" {
		if err := p.DirectSetMode(ctx, id, setMode); err != nil {
			slog.Warn("setting the mode after the plan failed", "provider", providerName, "session_id", gatewayID(id), "error", err)
		}
	}
	p.notifyDirect(id)
	return nil
}

// questionAnswers turns the app's answers into AskUserQuestion's: question
// text to answer, multiple choices joined with ", ".
func questionAnswers(ia *model.Interaction, resp model.InteractionResponse) (map[string]string, error) {
	out := map[string]string{}
	for _, q := range ia.Questions {
		a, ok := resp.Answers[q.ID]
		if !ok {
			return nil, fmt.Errorf("missing answer for question %s", q.ID)
		}
		parts := append([]string(nil), a.Selected...)
		if t := strings.TrimSpace(a.Text); t != "" {
			parts = append(parts, t)
		}
		if len(parts) == 0 {
			return nil, fmt.Errorf("empty answer for question %s", q.ID)
		}
		out[q.Question] = strings.Join(parts, ", ")
	}
	return out, nil
}
