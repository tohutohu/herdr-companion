// Package codex adapts Codex sessions through the Codex app-server protocol.
//
// History is read with thread/read. When Codex runs on the shared app-server
// daemon (TUI started with `codex --remote unix://`), the gateway also
// subscribes to loaded threads, which gives structured approvals and
// requestUserInput prompts and lets it start turns. Otherwise input falls back
// to the Herdr pane.
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
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

const (
	providerName     = "codex"
	maxRecent        = 30
	requestIDPrefix  = "codex-request:"
	blockedPromptID  = "codex-terminal-prompt"
	callTimeout      = 30 * time.Second
	daemonRetryEvery = 10 * time.Second
	daemonSyncEvery  = 5 * time.Second
)

var threadIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

type Provider struct {
	bin        string
	daemonSock string
	term       providers.Terminal
	sink       deadletter.Sink

	mu     sync.Mutex
	reader *rpcClient
	daemon *daemonConn
}

func DefaultDaemonSocket() string {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".codex")
	}
	return filepath.Join(home, "app-server-control", "app-server-control.sock")
}

func New(bin, daemonSock string, term providers.Terminal, sink deadletter.Sink) *Provider {
	if bin == "" {
		bin = "codex"
	}
	if daemonSock == "" {
		daemonSock = DefaultDaemonSocket()
	}
	return &Provider{bin: bin, daemonSock: daemonSock, term: term, sink: sink}
}

func (p *Provider) Name() string        { return providerName }
func (p *Provider) DisplayName() string { return "Codex" }
func (p *Provider) HerdrAgent() string  { return "codex" }

func gatewayID(native string) string { return providerName + ":" + native }

// --- connections ---

// client returns a connection for reads: the daemon when connected,
// otherwise a private `codex app-server` child.
func (p *Provider) client(ctx context.Context) (*rpcClient, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.daemon != nil && p.daemon.c.alive() {
		return p.daemon.c, nil
	}
	if p.reader != nil && p.reader.alive() {
		return p.reader, nil
	}
	t, err := startStdio(p.bin)
	if err != nil {
		return nil, err
	}
	c := newRPCClient(t, nil)
	ictx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := c.initialize(ictx); err != nil {
		c.close()
		return nil, fmt.Errorf("codex app-server initialize: %w", err)
	}
	p.reader = c
	return c, nil
}

func (p *Provider) currentDaemon() *daemonConn {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.daemon != nil && p.daemon.c.alive() {
		return p.daemon
	}
	return nil
}

// Run keeps a daemon connection when the shared app-server is running.
func (p *Provider) Run(ctx context.Context) {
	for {
		if _, err := os.Stat(p.daemonSock); err == nil {
			if err := p.runDaemon(ctx); err != nil && ctx.Err() == nil {
				slog.Debug("codex daemon connection ended", "provider", providerName, "operation", "daemon", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			p.mu.Lock()
			if p.reader != nil {
				p.reader.close()
			}
			p.mu.Unlock()
			return
		case <-time.After(daemonRetryEvery):
		}
	}
}

func (p *Provider) runDaemon(ctx context.Context) error {
	t, err := dialDaemon(ctx, p.daemonSock)
	if err != nil {
		return err
	}
	d := newDaemonConn(p.sink)
	d.c = newRPCClient(t, d)
	defer d.c.close()
	ictx, cancel := context.WithTimeout(ctx, callTimeout)
	err = d.c.initialize(ictx)
	cancel()
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.daemon = d
	p.mu.Unlock()
	slog.Info("connected to codex app-server daemon", "provider", providerName, "operation", "daemon", "socket", p.daemonSock)
	defer func() {
		p.mu.Lock()
		if p.daemon == d {
			p.daemon = nil
		}
		p.mu.Unlock()
	}()

	tick := time.NewTicker(daemonSyncEvery)
	defer tick.Stop()
	for {
		if err := d.sync(ctx); err != nil {
			slog.Warn("codex daemon sync failed", "provider", providerName, "operation", "daemon.sync", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-d.c.done:
			return d.c.err
		case <-tick.C:
		}
	}
}

func (p *Provider) readThread(ctx context.Context, id string, turns bool) (*Thread, error) {
	if !threadIDPattern.MatchString(id) {
		return nil, providers.ErrNotFound
	}
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
		p.sink.Record(providerName, gatewayID(id), deadletter.ProviderError, "thread/read: "+err.Error(), nil)
		return nil, err
	}
	return &r.Thread, nil
}

// --- Provider ---

func (p *Provider) Summary(ctx context.Context, nativeID string, live *providers.Live) (*providers.Summary, error) {
	th, err := p.readThread(ctx, nativeID, true)
	if err != nil {
		return nil, err
	}
	msgs := ConvertThread(th, convertOptions{SessionID: gatewayID(nativeID)})
	s := summaryFromThread(th)
	s.LastMessage = lastText(msgs)
	if lt := lastTurn(th); lt != nil {
		s.LastTurnFailed = lt.Status == "failed"
	}
	if d := p.currentDaemon(); d != nil {
		s.Pending, s.Status = d.state(nativeID, live)
	}
	return &s, nil
}

func summaryFromThread(th *Thread) providers.Summary {
	s := providers.Summary{NativeID: th.ID, Cwd: th.Cwd, Model: th.Model, LastMessage: providers.OneLine(th.Preview, 160)}
	if th.Name != nil {
		s.Title = *th.Name
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
	d := p.currentDaemon()
	var out []providers.Summary
	for i := range r.Data {
		th := &r.Data[i]
		if th.UpdatedAt < since.Unix() {
			continue
		}
		s := summaryFromThread(th)
		if d != nil {
			s.Pending, s.Status = d.state(th.ID, nil)
		}
		out = append(out, s)
	}
	return out, nil
}

func (p *Provider) Messages(ctx context.Context, nativeID string, live *providers.Live) ([]model.Message, error) {
	th, err := p.readThread(ctx, nativeID, true)
	if err != nil {
		return nil, err
	}
	root := th.Cwd
	if live != nil && live.Cwd != "" {
		root = live.Cwd
	}
	msgs := ConvertThread(th, convertOptions{SessionID: gatewayID(nativeID), Root: root, Sink: p.sink})

	var pending []model.Message
	if d := p.currentDaemon(); d != nil {
		pending = d.interactions(nativeID, p.sink)
	}
	if len(pending) == 0 && live.Blocked() {
		// Codex shows a dialog we have no structured data for (TUI not on
		// the shared daemon): offer the terminal.
		pending = []model.Message{{
			ID:        blockedPromptID,
			Role:      model.RoleAssistant,
			Timestamp: time.Now().UTC(),
			Blocks: []model.Block{{Type: model.BlockInteraction, Interaction: &model.Interaction{
				ID:    blockedPromptID,
				Type:  model.InteractionApproval,
				State: model.InteractionPending,
				Title: "Codex is waiting in the terminal",
				Detail: "Run Codex with `codex --remote unix://` (shared app-server) " +
					"to answer approvals and questions here.",
				Supported: false,
			}}},
		}}
	}
	return append(msgs, pending...), nil
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
	if d := p.currentDaemon(); d != nil && d.loaded(nativeID) {
		return p.sendStructured(ctx, d, nativeID, in)
	}
	if live == nil {
		return providers.ErrNotLive
	}
	text := strings.TrimSpace(in.Text)
	for _, img := range in.Images {
		// The Codex TUI attaches pasted image paths.
		text += "\n" + img
	}
	return p.term.Prompt(ctx, live.PaneID, strings.TrimSpace(text))
}

func (p *Provider) sendStructured(ctx context.Context, d *daemonConn, id string, in model.Input) error {
	var input []map[string]any
	if strings.TrimSpace(in.Text) != "" {
		input = append(input, map[string]any{"type": "text", "text": in.Text})
	}
	for _, img := range in.Images {
		input = append(input, map[string]any{"type": "localImage", "path": img})
	}
	th, err := p.readThread(ctx, id, true)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if lt := lastTurn(th); lt != nil && lt.Status == "inProgress" {
		return d.c.call(cctx, "turn/steer", map[string]any{"threadId": id, "input": input, "expectedTurnId": lt.ID}, nil)
	}
	return d.c.call(cctx, "turn/start", map[string]any{"threadId": id, "input": input}, nil)
}

func (p *Provider) Respond(ctx context.Context, nativeID string, live *providers.Live, r model.InteractionResponse) error {
	if r.InteractionID == blockedPromptID {
		return providers.ErrUnsupported
	}
	d := p.currentDaemon()
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

// --- daemon state ---

type pendingRequest struct {
	ID       json.RawMessage
	Method   string
	Params   json.RawMessage
	Received time.Time
}

type daemonConn struct {
	c    *rpcClient
	sink deadletter.Sink

	mu         sync.Mutex
	subscribed map[string]bool
	status     map[string]ThreadStatus
	pending    map[string]map[string]pendingRequest // thread -> request id -> request
}

func newDaemonConn(sink deadletter.Sink) *daemonConn {
	return &daemonConn{
		sink:       sink,
		subscribed: map[string]bool{},
		status:     map[string]ThreadStatus{},
		pending:    map[string]map[string]pendingRequest{},
	}
}

// sync subscribes to threads loaded in the daemon. Resuming an already loaded
// thread only subscribes this connection; the daemon then replays pending
// server requests for it.
func (d *daemonConn) sync(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var loaded struct {
		Data []string `json:"data"`
	}
	if err := d.c.call(cctx, "thread/loaded/list", map[string]any{}, &loaded); err != nil {
		return err
	}
	current := map[string]bool{}
	for _, id := range loaded.Data {
		current[id] = true
		d.mu.Lock()
		done := d.subscribed[id]
		d.mu.Unlock()
		if done {
			continue
		}
		var r struct {
			Thread Thread `json:"thread"`
		}
		if err := d.c.call(cctx, "thread/resume", map[string]any{"threadId": id}, &r); err != nil {
			slog.Warn("codex thread subscribe failed", "provider", providerName, "session_id", gatewayID(id), "operation", "thread/resume", "error", err)
			continue
		}
		d.mu.Lock()
		d.subscribed[id] = true
		if r.Thread.Status.Type != "" {
			d.status[id] = r.Thread.Status
		}
		d.mu.Unlock()
	}
	d.mu.Lock()
	for id := range d.subscribed {
		if !current[id] {
			delete(d.subscribed, id)
			delete(d.status, id)
			delete(d.pending, id)
		}
	}
	d.mu.Unlock()
	return nil
}

func (d *daemonConn) loaded(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.status[id]
	return ok && st.Type != "notLoaded"
}

// Requests that are for the owning client, not for a human.
var ignoredRequests = map[string]bool{
	"item/tool/call":                    true,
	"account/chatgptAuthTokens/refresh": true,
	"attestation/generate":              true,
}

func (d *daemonConn) Request(id json.RawMessage, method string, params json.RawMessage) {
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
}

func (d *daemonConn) Notification(method string, params json.RawMessage) {
	var p struct {
		ThreadID  string          `json:"threadId"`
		RequestID json.RawMessage `json:"requestId"`
		Status    ThreadStatus    `json:"status"`
	}
	switch method {
	case "serverRequest/resolved":
		json.Unmarshal(params, &p)
		d.resolve(p.ThreadID, string(p.RequestID))
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
		d.mu.Unlock()
	}
}

func (d *daemonConn) Closed(error) {}

func (d *daemonConn) resolve(thread, reqID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.pending[thread], reqID)
}

func (d *daemonConn) sortedPending(thread string) []pendingRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []pendingRequest
	for _, r := range d.pending[thread] {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Received.Before(out[j].Received) })
	return out
}

func (d *daemonConn) lookup(thread, interactionID string) (pendingRequest, bool) {
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
func (d *daemonConn) state(thread string, live *providers.Live) (model.InteractionType, model.Status) {
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

func (d *daemonConn) interactions(thread string, sink deadletter.Sink) []model.Message {
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

// LaunchArgs attaches new Codex TUIs to the shared daemon when it runs, so
// approvals and questions can be answered from the app.
func (p *Provider) LaunchArgs(modelID string) []string {
	var args []string
	if _, err := os.Stat(p.daemonSock); err == nil {
		args = append(args, "--remote", "unix://"+p.daemonSock)
	}
	if modelID != "" {
		args = append(args, "--model", modelID)
	}
	return args
}

type catalogModel struct {
	ID          string `json:"id"`
	Model       string `json:"model"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Hidden      bool   `json:"hidden"`
	IsDefault   bool   `json:"isDefault"`
}

// Models reads Codex's model catalog (model/list).
func (p *Provider) Models(ctx context.Context) ([]providers.ModelOption, error) {
	c, err := p.client(ctx)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var out []providers.ModelOption
	cursor := ""
	for range 10 {
		params := map[string]any{"includeHidden": false}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var r struct {
			Data       []catalogModel `json:"data"`
			NextCursor *string        `json:"nextCursor"`
		}
		if err := c.call(cctx, "model/list", params, &r); err != nil {
			return nil, err
		}
		out = append(out, modelOptions(r.Data)...)
		if r.NextCursor == nil || *r.NextCursor == "" {
			break
		}
		cursor = *r.NextCursor
	}
	return out, nil
}

func modelOptions(ms []catalogModel) []providers.ModelOption {
	var out []providers.ModelOption
	for _, m := range ms {
		id := m.Model
		if id == "" {
			id = m.ID
		}
		if m.Hidden || !providers.ValidModelID(id) {
			continue
		}
		name := m.DisplayName
		if name == "" {
			name = id
		}
		out = append(out, providers.ModelOption{ID: id, Name: name, Description: m.Description, Default: m.IsDefault})
	}
	return out
}

// StartupKeys accepts Codex's folder trust screen, whose default choice is
// to trust and continue.
func (p *Provider) StartupKeys(screen string) []string {
	s := strings.ToLower(screen)
	if strings.Contains(s, "trust and continue") || strings.Contains(s, "do you trust") || strings.Contains(s, "trust this folder") {
		return []string{"enter"}
	}
	return nil
}
