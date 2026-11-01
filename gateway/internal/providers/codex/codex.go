// Package codex adapts Codex sessions through the Codex app-server protocol.
//
// History is read with thread/read. The gateway runs its own `codex
// app-server` child (stdio), which also hosts the sessions it manages directly:
// it starts and resumes their threads, starts and steers turns with the
// session's model, effort and collaboration mode, and answers approvals and
// requestUserInput prompts (see direct.go).
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

const (
	providerName    = "codex"
	maxRecent       = 30
	requestIDPrefix = "codex-request:"
	callTimeout     = 30 * time.Second
)

var threadIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

type Provider struct {
	bin  string
	sink deadletter.Sink

	mu              sync.Mutex
	owned           *serverConn
	skillRoots      []string
	readMu          sync.Mutex
	reads           map[threadReadKey]*threadReadFlight
	displayMu       sync.Mutex
	metadataEntries map[string]*metadataEntry
	historyEntries  map[string]*historyEntry
	cacheBytes      int
	actionVersion   atomic.Uint64
	summaryEntries  map[string]*summaryRolloutEntry

	directMu sync.Mutex
	direct   map[string]*directThread
	listener func(string)
}

type threadReadKey struct {
	id    string
	turns bool
}
type threadReadFlight struct {
	done   chan struct{}
	thread *Thread
	err    error
}

func codexHome() string {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return home
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".codex")
}

// inManagedWorktree reports whether cwd is in a worktree Codex created with
// --worktree. The agent works there without changing its process directory,
// so the runtime keeps reporting the repository it was started from.
func inManagedWorktree(cwd string) bool {
	if cwd == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Join(codexHome(), "worktrees"), cwd)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

func New(bin string, sink deadletter.Sink) *Provider {
	if bin == "" {
		bin = "codex"
	}
	return &Provider{bin: bin, sink: sink}
}

// SetSkillRoots gives every thread the skills in these directories, in
// addition to the user's own; app-servers started afterwards get them.
func (p *Provider) SetSkillRoots(roots []string) {
	p.mu.Lock()
	p.skillRoots = roots
	p.mu.Unlock()
}

func (p *Provider) currentSkillRoots() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.skillRoots
}

func (p *Provider) Name() string        { return providerName }
func (p *Provider) DisplayName() string { return "Codex" }

func gatewayID(native string) string { return providerName + ":" + native }

// --- connections ---

// client returns the gateway's own `codex app-server` child for reads.
func (p *Provider) client(ctx context.Context) (*rpcClient, error) {
	d, err := p.ownedConn(ctx)
	if err != nil {
		return nil, err
	}
	return d.c, nil
}

// ownedConn returns the gateway's own app-server, starting it when needed.
// It serves reads and hosts the directly managed threads; it is the child of
// the gateway and ends with it.
func (p *Provider) ownedConn(ctx context.Context) (*serverConn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owned != nil && p.owned.c.alive() {
		return p.owned, nil
	}
	t, err := startStdio(p.bin, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", providers.ErrRuntimeUnavailable, err)
	}
	d := newServerConn(p.sink)
	d.events = p.directEvent
	d.closed = func(err error) { p.ownedClosed(d, err) }
	d.c = newRPCClient(t, d)
	ictx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := d.c.initialize(ictx, p.skillRoots); err != nil {
		d.c.close()
		return nil, fmt.Errorf("%w: codex app-server initialize: %v", providers.ErrRuntimeUnavailable, err)
	}
	p.owned = d
	slog.Info("started codex app-server", "provider", providerName, "operation", "app-server.start", "pid", t.pid())
	return d, nil
}

// currentOwned is the gateway's app-server when it runs.
func (p *Provider) currentOwned() *serverConn {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owned != nil && p.owned.c.alive() {
		return p.owned
	}
	return nil
}

// connFor is the connection with structured state for a thread: the
// gateway's own app-server while it runs the thread.
func (p *Provider) connFor(thread string, live *providers.Live) *serverConn {
	if live == nil && !p.isDirect(thread) {
		return nil
	}
	return p.currentOwned()
}

// Run ends the gateway's app-server when ctx ends.
func (p *Provider) Run(ctx context.Context) {
	<-ctx.Done()
	p.mu.Lock()
	owned := p.owned
	p.mu.Unlock()
	if owned != nil {
		owned.c.close()
	}
}

func (p *Provider) readThread(ctx context.Context, id string, turns bool) (*Thread, error) {
	if !threadIDPattern.MatchString(id) {
		return nil, providers.ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := threadReadKey{id, turns}
	p.readMu.Lock()
	f := p.reads[key]
	if f == nil {
		f = &threadReadFlight{done: make(chan struct{})}
		if p.reads == nil {
			p.reads = map[threadReadKey]*threadReadFlight{}
		}
		p.reads[key] = f
		// A closed phone request must not cancel the read shared by other
		// clients and the notification watcher. The RPC still has a deadline.
		go func() {
			readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), callTimeout)
			defer cancel()
			f.thread, f.err = p.fetchThread(readCtx, id, turns)
			p.readMu.Lock()
			delete(p.reads, key)
			close(f.done)
			p.readMu.Unlock()
		}()
	}
	p.readMu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.done:
		return f.thread, f.err
	}
}

func (p *Provider) fetchThread(ctx context.Context, id string, turns bool) (*Thread, error) {
	c, err := p.client(ctx)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var r struct {
		Thread Thread `json:"thread"`
	}
	if err := c.call(cctx, "thread/read", map[string]any{"threadId": id, "includeTurns": turns}, &r); err != nil {
		var rerr *rpcError
		if errors.As(err, &rerr) {
			// Unknown thread ids are reported as JSON-RPC errors.
			return nil, fmt.Errorf("%w: %v", providers.ErrNotFound, err)
		}
		// A caller that went away (closed request, shutdown) is not a Codex failure.
		if ctx.Err() == nil {
			p.sink.Record(providerName, gatewayID(id), deadletter.ProviderError, "thread/read: "+err.Error(), nil)
		}
		return nil, err
	}
	return &r.Thread, nil
}

// --- Provider ---

func (p *Provider) Summary(ctx context.Context, nativeID string, live *providers.Live) (*providers.Summary, error) {
	meta, err := p.metadata(ctx, nativeID)
	if err != nil {
		return nil, err
	}
	state, scanErr := p.rolloutSummary(ctx, meta.Path)
	if scanErr == nil {
		state = p.checkSummaryVersion(meta.ID, meta.Path, state)
	}
	if scanErr == nil && state.available && state.turnID != "" && !state.unsupported {
		th := state.thread(meta)
		if d := p.connFor(nativeID, live); d != nil {
			d.mu.Lock()
			if turn, ok := d.latestTurns[nativeID]; ok && turn.Status != "" && (turn.ID == state.turnID || d.latestTurnTimes[nativeID].After(state.turnAt)) {
				if turn.ID != state.turnID {
					state.planID = ""
					th = state.thread(meta)
				}
				th.Turns[0].ID, th.Turns[0].Status = turn.ID, turn.Status
			}
			d.mu.Unlock()
		}
		base := summaryFromMetadata(th)
		base.Context, base.Cost = state.tokens.context(), state.tokens.cost(th.Model)
		if state.mode == "plan" {
			base.Mode = planLabel
		}
		sum := p.summaryWithBase(ctx, th, live, state.lastText, base)
		if lt := lastTurn(th); lt != nil && (lt.Status == "completed" || lt.Status == "failed" || lt.Status == "interrupted") {
			if sum.CompletionRevision, err = p.rolloutCompletion(ctx, meta, state, lt); err != nil {
				return nil, err
			}
		}
		return sum, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Missing/unrecognized rollouts keep the canonical app-server behavior.
	before := rolloutStat(meta.Path)
	th, err := p.displayThread(ctx, nativeID)
	if err != nil {
		return nil, err
	}
	p.seedRolloutSummary(meta.Path, before, th)
	return p.summaryFromText(ctx, th, live, lastThreadText(th)), nil
}

func (p *Provider) summaryFromText(ctx context.Context, th *Thread, live *providers.Live, text string) *providers.Summary {
	return p.summaryWithBase(ctx, th, live, text, summaryFromThread(th))
}

func (p *Provider) summaryWithBase(ctx context.Context, th *Thread, live *providers.Live, text string, s providers.Summary) *providers.Summary {
	s.LastMessage = text
	if lt := lastTurn(th); lt != nil {
		s.LastTurnFailed = lt.Status == "failed"
		s.CompletionRevision = completionRevision(lt)
	}
	if d := p.connFor(th.ID, live); d != nil {
		s.Pending, s.Status = d.state(th.ID, live)
		s.Mode = d.mode(th.ID, s.Mode)
	}
	return &s
}

// planLabel is the mode label of a thread in Plan mode.
const planLabel = "Plan"

// summaryFromThread fills Mode only for Plan mode, the one mode the rollout
// shows on its own; the permission preset comes from the app-server.
func summaryFromThread(th *Thread) providers.Summary {
	lines := rolloutTailLines(th.Path)
	info := tokenInfoFrom(lines)
	s := summaryFromMetadata(th)
	s.Context, s.Cost = info.context(), info.cost(th.Model)
	if collaborationModeFrom(lines) == "plan" {
		s.Mode = planLabel
	}
	return s
}

func summaryFromMetadata(th *Thread) providers.Summary {
	s := providers.Summary{NativeID: th.ID, Cwd: th.Cwd, Model: th.Model, LastMessage: providers.OneLine(th.Preview, 160), PinnedCwd: inManagedWorktree(th.Cwd)}

	if th.Name != nil {
		s.Title = *th.Name
	}
	if th.Effort != nil {
		s.Effort = *th.Effort
	}
	if th.UpdatedAt > 0 {
		s.UpdatedAt = time.Unix(th.UpdatedAt, 0).UTC()
	}
	return s
}

func (p *Provider) Recent(ctx context.Context, since time.Time) ([]providers.Summary, error) {
	c, err := p.client(ctx)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var r struct {
		Data []Thread `json:"data"`
	}
	params := map[string]any{"limit": maxRecent, "sortKey": "updated_at", "sortDirection": "desc"}
	if err := c.call(cctx, "thread/list", params, &r); err != nil {
		return nil, err
	}
	var out []providers.Summary
	for i := range r.Data {
		th := &r.Data[i]
		if th.UpdatedAt < since.Unix() {
			continue
		}
		out = append(out, summaryFromThread(th))
	}
	return out, nil
}

func (p *Provider) Messages(ctx context.Context, nativeID string, live *providers.Live) ([]model.Message, error) {
	th, err := p.displayThread(ctx, nativeID)
	if err != nil {
		return nil, err
	}
	return p.messagesFromThread(ctx, th, live), nil
}

// Conversation reads and converts the thread once for GET /messages.
func (p *Provider) Conversation(ctx context.Context, nativeID string, live *providers.Live) (*providers.Summary, []model.Message, error) {
	th, err := p.displayThread(ctx, nativeID)
	if err != nil {
		return nil, nil, err
	}
	msgs := p.messagesFromThread(ctx, th, live)
	return p.summaryFromText(ctx, th, live, lastText(msgs)), msgs, nil
}

func (p *Provider) messagesFromThread(ctx context.Context, th *Thread, live *providers.Live) []model.Message {
	nativeID := th.ID
	root := th.Cwd
	if live != nil && live.Cwd != "" && !inManagedWorktree(th.Cwd) {
		root = live.Cwd
	}
	msgs := p.convertedHistory(th, root)
	if live == nil {
		return msgs
	}

	var pending []model.Message
	if d := p.connFor(nativeID, live); d != nil {
		pending = d.interactions(nativeID, p.sink)
	}
	return append(msgs, pending...)
}

func (p *Provider) Image(ctx context.Context, nativeID, messageID string, index int) (string, []byte, error) {
	th, err := p.readThread(ctx, nativeID, true)
	if err != nil {
		return "", nil, err
	}
	mime, data, ok := dataURLImage(th, messageID, index)
	if !ok {
		return "", nil, providers.ErrNotFound
	}
	return mime, data, nil
}

func (p *Provider) Send(ctx context.Context, nativeID string, live *providers.Live, in model.Input) error {
	defer p.actionVersion.Add(1)
	if live == nil {
		return providers.ErrNotLive
	}
	return p.directSend(ctx, nativeID, in)
}

func (p *Provider) Respond(ctx context.Context, nativeID string, live *providers.Live, r model.InteractionResponse) error {
	defer p.actionVersion.Add(1)
	if live == nil {
		return providers.ErrNotLive
	}
	defer p.notifyDirect(nativeID)
	d := p.connFor(nativeID, live)
	if d == nil {
		return providers.ErrInteractionGone
	}
	req, ok := d.lookup(nativeID, r.InteractionID)
	if !ok {
		return providers.ErrInteractionGone
	}
	result, err := responseFor(req, r)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := d.c.respond(cctx, req.ID, result); err != nil {
		return err
	}
	d.resolve(nativeID, string(req.ID))
	return nil
}

// --- app-server state ---

type pendingRequest struct {
	ID       json.RawMessage
	Method   string
	Params   json.RawMessage
	Received time.Time
}

type serverConn struct {
	c    *rpcClient
	sink deadletter.Sink
	// events and closed run on the connection's read loop and must not make
	// calls on it.
	events func(method string, params json.RawMessage)
	closed func(error)

	mu sync.Mutex
	// subscribed are the threads this connection has open.
	subscribed       map[string]bool
	status           map[string]ThreadStatus
	pending          map[string]map[string]pendingRequest // thread -> request id -> request
	settings         map[string]threadSettings
	historyVersions  map[string]uint64
	metadataVersions map[string]uint64
	latestTurns      map[string]Turn
	latestTurnTimes  map[string]time.Time
	summaryVersions  map[string]uint64
}

// threadSettings is the part of a loaded thread's settings shown as its mode.
type threadSettings struct {
	ApprovalPolicy    json.RawMessage    `json:"approvalPolicy"`
	SandboxPolicy     *sandboxPolicy     `json:"sandboxPolicy"`
	CollaborationMode *collaborationMode `json:"collaborationMode"`
}

type sandboxPolicy struct {
	Type string `json:"type"`
}

// label follows the Codex TUI: Plan mode, else the permission preset.
func (s threadSettings) label() string {
	if s.CollaborationMode != nil && s.CollaborationMode.Mode == "plan" {
		return planLabel
	}
	if s.SandboxPolicy == nil {
		return ""
	}
	var approval string
	if json.Unmarshal(s.ApprovalPolicy, &approval) != nil {
		approval = "custom approvals"
	}
	switch sb := s.SandboxPolicy.Type; {
	case sb == "dangerFullAccess" && approval == "never":
		return "Full access"
	case sb == "readOnly" && approval == "on-request":
		return "Read only"
	case sb == "workspaceWrite" && approval == "on-request":
		return "Default"
	default:
		return sb + " · " + approval
	}
}

func newServerConn(sink deadletter.Sink) *serverConn {
	return &serverConn{
		sink:             sink,
		subscribed:       map[string]bool{},
		status:           map[string]ThreadStatus{},
		pending:          map[string]map[string]pendingRequest{},
		settings:         map[string]threadSettings{},
		historyVersions:  map[string]uint64{},
		metadataVersions: map[string]uint64{},
		latestTurns:      map[string]Turn{},
		latestTurnTimes:  map[string]time.Time{},
		summaryVersions:  map[string]uint64{},
	}
}

// Requests that are for the owning client, not for a human.
var ignoredRequests = map[string]bool{
	"item/tool/call":                    true,
	"account/chatgptAuthTokens/refresh": true,
	"attestation/generate":              true,
}

func (d *serverConn) Request(id json.RawMessage, method string, params json.RawMessage) {
	if ignoredRequests[method] {
		return
	}
	var ref struct {
		ThreadID       string `json:"threadId"`
		ConversationID string `json:"conversationId"`
	}
	json.Unmarshal(params, &ref)
	thread := ref.ThreadID
	if thread == "" {
		thread = ref.ConversationID
	}
	if thread == "" {
		d.sink.Record(providerName, "", deadletter.UnknownEvent, "server request without thread: "+method, params)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pending[thread] == nil {
		d.pending[thread] = map[string]pendingRequest{}
	}
	d.pending[thread][string(id)] = pendingRequest{ID: id, Method: method, Params: params, Received: time.Now()}
	if d.events != nil {
		go d.events("request:"+method, params)
	}
}

func (d *serverConn) Notification(method string, params json.RawMessage) {
	var p struct {
		ThreadID  string          `json:"threadId"`
		RequestID json.RawMessage `json:"requestId"`
		Status    ThreadStatus    `json:"status"`
		Settings  threadSettings  `json:"threadSettings"`
		Turn      struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"turn"`
	}
	json.Unmarshal(params, &p)
	d.mu.Lock()
	if p.ThreadID != "" {
		if strings.HasPrefix(method, "item/") || strings.HasPrefix(method, "turn/") || method == "thread/reverted" || method == "thread/compacted" || method == "thread/closed" {
			d.historyVersions[p.ThreadID]++
		}
		if method == "thread/reverted" || method == "thread/compacted" || method == "thread/closed" {
			d.summaryVersions[p.ThreadID]++
			delete(d.latestTurns, p.ThreadID)
			delete(d.latestTurnTimes, p.ThreadID)
		}
		if strings.HasPrefix(method, "thread/") || method == "turn/started" || method == "turn/completed" {
			d.metadataVersions[p.ThreadID]++
		}
		if (method == "turn/started" || method == "turn/completed") && p.Turn.ID != "" {
			d.latestTurns[p.ThreadID] = Turn{ID: p.Turn.ID, Status: p.Turn.Status}
			d.latestTurnTimes[p.ThreadID] = time.Now()
		}
	}
	d.mu.Unlock()
	switch method {
	case "serverRequest/resolved":
		json.Unmarshal(params, &p)
		d.resolve(p.ThreadID, string(p.RequestID))
	case "thread/settings/updated":
		json.Unmarshal(params, &p)
		d.mu.Lock()
		d.settings[p.ThreadID] = p.Settings
		d.mu.Unlock()
	case "thread/status/changed":
		json.Unmarshal(params, &p)
		d.mu.Lock()
		d.status[p.ThreadID] = p.Status
		d.mu.Unlock()
	case "turn/completed":
		json.Unmarshal(params, &p)
		d.mu.Lock()
		delete(d.pending, p.ThreadID)
		d.mu.Unlock()
	case "thread/closed":
		json.Unmarshal(params, &p)
		d.mu.Lock()
		delete(d.subscribed, p.ThreadID)
		delete(d.status, p.ThreadID)
		delete(d.pending, p.ThreadID)
		delete(d.settings, p.ThreadID)
		d.mu.Unlock()
	}
	if d.events != nil {
		d.events(method, params)
	}
}

func (d *serverConn) Closed(err error) {
	if d.closed != nil {
		d.closed(err)
	}
}

// mode is the display label of a loaded thread's mode, or rolloutLabel (the
// label summaryFromThread read from the rollout) when the daemon has no
// settings for it. thread/resume leaves out the collaboration mode, so until a
// settings notification arrives Plan mode comes from the rollout.
func (d *serverConn) mode(thread, rolloutLabel string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.settings[thread]
	if !ok {
		return rolloutLabel
	}
	if s.CollaborationMode == nil && rolloutLabel == planLabel {
		return planLabel
	}
	return s.label()
}

func (d *serverConn) resolve(thread, reqID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.pending[thread], reqID)
}

func (d *serverConn) sortedPending(thread string) []pendingRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []pendingRequest
	for _, r := range d.pending[thread] {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Received.Before(out[j].Received) })
	return out
}

func (d *serverConn) lookup(thread, interactionID string) (pendingRequest, bool) {
	reqID, ok := strings.CutPrefix(interactionID, requestIDPrefix)
	if !ok {
		return pendingRequest{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.pending[thread][reqID]
	return r, ok
}

// state derives the pending interaction kind and, when the daemon knows the
// thread, an authoritative status.
func (d *serverConn) state(thread string, live *providers.Live) (model.InteractionType, model.Status) {
	var pending model.InteractionType
	for _, r := range d.sortedPending(thread) {
		ia := interactionFor(r, deadletter.Nop{})
		if ia == nil {
			continue
		}
		pending = ia.Type
		if ia.Type == model.InteractionQuestions {
			break
		}
	}
	d.mu.Lock()
	st, ok := d.status[thread]
	d.mu.Unlock()
	if !ok {
		return pending, ""
	}
	switch pending {
	case model.InteractionQuestions:
		return pending, model.StatusWaitingInput
	case model.InteractionApproval:
		return pending, model.StatusWaitingApproval
	}
	if live != nil {
		return pending, "" // Herdr's lifecycle is more precise for done/idle.
	}
	switch st.Type {
	case "active":
		return pending, model.StatusRunning
	case "systemError":
		return pending, model.StatusFailed
	case "idle":
		return pending, model.StatusIdle
	}
	return pending, ""
}

func (d *serverConn) interactions(thread string, sink deadletter.Sink) []model.Message {
	var out []model.Message
	for _, r := range d.sortedPending(thread) {
		ia := interactionFor(r, sink)
		if ia == nil {
			continue
		}
		out = append(out, model.Message{
			ID:        ia.ID,
			Role:      model.RoleAssistant,
			Timestamp: r.Received.UTC(),
			Blocks:    []model.Block{{Type: model.BlockInteraction, Interaction: ia}},
		})
	}
	return out
}

type catalogModel struct {
	ID          string `json:"id"`
	Model       string `json:"model"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Hidden      bool   `json:"hidden"`
	IsDefault   bool   `json:"isDefault"`
	// Codex offers reasoning efforts per model.
	SupportedReasoningEfforts []catalogEffort `json:"supportedReasoningEfforts"`
	DefaultReasoningEffort    string          `json:"defaultReasoningEffort"`
}

type catalogEffort struct {
	ReasoningEffort string `json:"reasoningEffort"`
	Description     string `json:"description"`
}

// Models reads Codex's model catalog (model/list).
func (p *Provider) Models(ctx context.Context) (providers.ModelCatalog, error) {
	c, err := p.client(ctx)
	if err != nil {
		return providers.ModelCatalog{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var cat providers.ModelCatalog
	cursor := ""
	for range 10 {
		// Reserve is hidden in Codex's catalog; fetch it along with visible
		// models and keep filtering other hidden models in modelOptions.
		params := map[string]any{"includeHidden": true}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var r struct {
			Data       []catalogModel `json:"data"`
			NextCursor *string        `json:"nextCursor"`
		}
		if err := c.call(cctx, "model/list", params, &r); err != nil {
			return providers.ModelCatalog{}, err
		}
		cat.Models = append(cat.Models, modelOptions(r.Data)...)
		if r.NextCursor == nil || *r.NextCursor == "" {
			break
		}
		cursor = *r.NextCursor
	}
	// Without a model the session runs the default one, so its efforts are
	// the ones on offer.
	for _, m := range cat.Models {
		if m.Default {
			cat.Efforts = m.Efforts
			break
		}
	}
	cat.Modes = modes
	return cat, nil
}

// Codex has the two collaboration modes its TUI cycles through.
var modes = []providers.ModeOption{
	{ID: "default", Name: "Default", Description: "Works on the task", Default: true},
	{ID: "plan", Name: planLabel, Description: "Plans the work without doing it"},
}

// SetLaunchMode starts a session in Plan mode. Codex takes no mode argument
// and its thread does not exist before the first prompt, so the mode is set
// with the TUI's own shortcut, which toggles between the two modes a fresh
// session has. A key sent while the TUI is still drawing its startup notices
// is dropped, so the status line is read back and the shortcut repeated.
// Codex applies its own plan-mode reasoning effort here, exactly as it does
// when the user presses the shortcut.
func modelOptions(ms []catalogModel) []providers.ModelOption {
	var out []providers.ModelOption
	for _, m := range ms {
		id := m.Model
		if id == "" {
			id = m.ID
		}
		if (m.Hidden && id != "gpt-reserve") || !providers.ValidModelID(id) {
			continue
		}
		name := m.DisplayName
		if name == "" {
			name = id
		}
		out = append(out, providers.ModelOption{
			ID: id, Name: name, Description: m.Description, Default: m.IsDefault,
			Efforts: effortOptions(m),
		})
	}
	return out
}

func effortOptions(m catalogModel) []providers.EffortOption {
	var out []providers.EffortOption
	for _, e := range m.SupportedReasoningEfforts {
		if !providers.ValidEffortID(e.ReasoningEffort) {
			continue
		}
		out = append(out, providers.EffortOption{
			ID:          e.ReasoningEffort,
			Name:        providers.EffortName(e.ReasoningEffort),
			Description: e.Description,
			Default:     e.ReasoningEffort == m.DefaultReasoningEffort,
		})
	}
	return out
}
