// Package dsh adapts DeepSeek Harness's SDK stdio profile to the Gateway.
package dsh

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

const DefaultModel = "deepseek-flash"
const workspaceMode = "workspace-write"
const fullMode = "danger-full-access"

var nativeID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Config struct{ Binary, Home, Model, APIKey string }
type savedSession struct {
	Summary providers.Summary `json:"summary"`
	Turns   int               `json:"turns"`
}
type session struct {
	op sync.Mutex
	savedSession
	info     providers.DirectInfo
	conn     *conn
	stopping bool
	activity uint64
	pending  map[string]*pendingRequest
}
type Provider struct {
	mu       sync.Mutex
	cfg      Config
	dir      string
	sessions map[string]*session
	listener func(string)
	sink     deadletter.Sink
	closed   bool
}

var _ providers.Provider = (*Provider)(nil)
var _ providers.DirectRuntime = (*Provider)(nil)

func New(cfg Config, dir string, sink deadletter.Sink) (*Provider, error) {
	if cfg.Binary == "" {
		cfg.Binary = "dsh"
	}
	if cfg.Home == "" {
		cfg.Home = filepath.Join(dir, "home")
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	p := &Provider{cfg: cfg, dir: dir, sessions: map[string]*session{}, sink: sink}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if entry.Name() == id || !nativeID.MatchString(id) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var saved savedSession
		if err := json.Unmarshal(b, &saved); err != nil {
			return nil, fmt.Errorf("DSH session %s: %w", id, err)
		}
		if saved.Summary.NativeID != id {
			return nil, fmt.Errorf("DSH session identity mismatch: %s", id)
		}
		s := &session{savedSession: saved, pending: map[string]*pendingRequest{}}
		s.info = providers.DirectInfo{NativeID: id, Cwd: s.Summary.Cwd, Model: s.Summary.Model, Effort: s.Summary.Effort, Mode: s.Summary.Mode, ModeID: s.Summary.Mode, Turns: s.Turns}
		p.sessions[id] = s
	}
	return p, nil
}
func (*Provider) Name() string                       { return "dsh" }
func (*Provider) DisplayName() string                { return "DeepSeek Harness" }
func (*Provider) SettingsApply() string              { return providers.ApplyNextTurn }
func (*Provider) FolderTrusted(string) bool          { return false }
func (p *Provider) SetDirectListener(f func(string)) { p.mu.Lock(); p.listener = f; p.mu.Unlock() }
func (p *Provider) notify(id string) {
	p.mu.Lock()
	f := p.listener
	p.mu.Unlock()
	if f != nil {
		f(id)
	}
}
func (p *Provider) get(id string) (*session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.sessions[id]
	if s == nil {
		return nil, providers.ErrNotFound
	}
	return s, nil
}
func (p *Provider) saveLocked(s *session) error {
	b, err := json.Marshal(s.savedSession)
	if err != nil {
		return err
	}
	path := filepath.Join(p.dir, s.Summary.NativeID+".json")
	if err = os.WriteFile(path+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
func (p *Provider) recordError(id string, err error) {
	if p.sink != nil {
		p.sink.Record("dsh", id, deadletter.ProviderError, err.Error(), nil)
	}
}

func (p *Provider) Models(context.Context) (providers.ModelCatalog, error) {
	models := []providers.ModelOption{{ID: p.cfg.Model, Name: p.cfg.Model, Default: true}}
	for _, id := range []string{DefaultModel, "deepseek-v4-pro"} {
		if id != p.cfg.Model {
			models = append(models, providers.ModelOption{ID: id, Name: id})
		}
	}
	// The SDK validates exact model/effort routes at initialize. It has no
	// catalog endpoint; do not invent reasoning-effort choices.
	return providers.ModelCatalog{Models: models, Modes: []providers.ModeOption{
		{ID: workspaceMode, Name: "Workspace write", Default: true},
		{ID: fullMode, Name: "Full access", Description: "Run without sandbox or approval prompts"},
	}}, nil
}

func (p *Provider) DirectStart(ctx context.Context, opts providers.DirectStart) (string, error) {
	if opts.Mode != "" && opts.Mode != workspaceMode && opts.Mode != fullMode {
		return "", errors.New("unsupported DSH permission mode")
	}
	id := opts.Resume
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return "", providers.ErrRuntimeUnavailable
	}
	var s *session
	if id != "" {
		s = p.sessions[id]
		if s == nil {
			p.mu.Unlock()
			return "", providers.ErrNotFound
		}
	} else {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			p.mu.Unlock()
			return "", err
		}
		id = hex.EncodeToString(b[:])
		s = &session{pending: map[string]*pendingRequest{}}
		s.Summary = providers.Summary{NativeID: id, Cwd: opts.Cwd, UpdatedAt: time.Now(), Model: p.cfg.Model, Mode: workspaceMode}
		p.sessions[id] = s
	}
	p.mu.Unlock()
	s.op.Lock()
	defer s.op.Unlock()
	p.mu.Lock()
	if s.conn != nil {
		p.mu.Unlock()
		return "", errors.New("DSH session already running")
	}
	if opts.Model != "" {
		s.Summary.Model = opts.Model
	}
	if opts.Effort != "" {
		s.Summary.Effort = opts.Effort
	}
	if opts.Mode != "" {
		s.Summary.Mode = opts.Mode
	}
	if opts.Cwd != "" {
		s.Summary.Cwd = opts.Cwd
	}
	err := p.saveLocked(s)
	p.mu.Unlock()
	if err != nil {
		return "", err
	}
	if err = p.startLocked(ctx, s, opts.Resume != ""); err != nil {
		if opts.Resume == "" {
			p.mu.Lock()
			delete(p.sessions, id)
			p.mu.Unlock()
			os.Remove(filepath.Join(p.dir, id+".json"))
			os.Remove(filepath.Join(p.dir, id+".jsonl"))
		}
		return "", err
	}
	if !opts.Prompt.Empty() {
		err = p.sendLocked(ctx, s, opts.Prompt)
	}
	return id, err
}

// Caller holds s.op. Events only take p.mu, so replies remain readable while
// a caller waits for initialization, native commands or an approval result.
func (p *Provider) startLocked(ctx context.Context, s *session, resume bool) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return providers.ErrRuntimeUnavailable
	}
	summary := s.Summary
	p.mu.Unlock()
	if err := os.MkdirAll(p.cfg.Home, 0700); err != nil {
		return err
	}
	c, err := launch(p.cfg.Binary, p.cfg.Home, summary.Cwd, p.cfg.APIKey)
	if err != nil {
		return err
	}
	p.mu.Lock()
	s.conn = c
	s.stopping = false
	s.info = providers.DirectInfo{NativeID: summary.NativeID, Cwd: summary.Cwd, Model: summary.Model, Effort: summary.Effort, Mode: summary.Mode, ModeID: summary.Mode, Turns: s.Turns, Alive: true}
	p.mu.Unlock()
	go c.read(func(f frame) { p.handle(s, c, f) }, func(err error) { p.exited(s, c, err) })
	params := map[string]any{"cwd": summary.Cwd, "provider": "deepseek-official", "model": summary.Model, "approvalRequests": true, "capabilities": map[string]bool{"userQuestions": true}}
	if summary.Effort != "" {
		params["reasoningEffort"] = summary.Effort
	}
	var init struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	err = c.call(ctx, "initialize", params, &init)
	if err == nil && init.ServerInfo.Name != "deepseek-harness-sdk-runtime" {
		err = errors.New("unexpected DSH SDK identity")
	}
	if err == nil {
		method := "session/working-directory/get"
		if resume {
			method = "session/resume"
		}
		err = c.call(ctx, method, map[string]string{"sessionId": summary.NativeID}, nil)
	}
	if err == nil {
		err = p.modeCall(ctx, c, summary.NativeID, summary.Mode)
	}
	if err != nil {
		c.close()
		return err
	}
	p.notify(summary.NativeID)
	return nil
}

func (p *Provider) modeCall(ctx context.Context, c *conn, id, mode string) error {
	var result struct {
		Kind      string `json:"kind"`
		Execution struct {
			Result struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"result"`
		} `json:"execution"`
	}
	err := c.call(ctx, "session/query", map[string]any{"sessionId": id, "contentBlocks": []any{map[string]string{"type": "text", "text": "/permission " + mode}}}, &result)
	if err != nil {
		return err
	}
	if result.Kind != "command" {
		return errors.New("DSH SDK did not recognize /permission; update to a supported runtime")
	}
	if result.Execution.Result.Kind == "error" {
		return fmt.Errorf("DSH permission command: %s", result.Execution.Result.Text)
	}
	return nil
}

func (p *Provider) Send(ctx context.Context, id string, _ *providers.Live, in model.Input) error {
	s, err := p.get(id)
	if err != nil {
		return err
	}
	s.op.Lock()
	defer s.op.Unlock()
	return p.sendLocked(ctx, s, in)
}
func (p *Provider) sendLocked(ctx context.Context, s *session, in model.Input) error {
	blocks, err := inputBlocks(in)
	if err != nil {
		return err
	}
	p.mu.Lock()
	c := s.conn
	restart := s.info.RequestedModel != "" || s.info.RequestedEffort != ""
	busy := s.info.Running || s.info.Waiting
	activity := s.activity
	p.mu.Unlock()
	if c == nil {
		return providers.ErrNotLive
	}
	if restart {
		if busy {
			return errors.New("DSH model change waits for the current turn to finish")
		}
		p.stopLocked(s)
		if err := p.startLocked(ctx, s, true); err != nil {
			return err
		}
		p.mu.Lock()
		c = s.conn
		p.mu.Unlock()
	}
	p.mu.Lock()
	s.info.Running = true
	s.info.LastTurnFailed = false
	s.info.Error = ""
	p.mu.Unlock()
	p.notify(s.Summary.NativeID)
	var receipt struct {
		Kind      string `json:"kind"`
		MessageID string `json:"messageId"`
		Execution struct {
			Result struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"result"`
		} `json:"execution"`
	}
	err = c.call(ctx, "session/query", map[string]any{"sessionId": s.Summary.NativeID, "contentBlocks": blocks}, &receipt)
	if err != nil {
		// A lost receipt may already have enqueued the prompt. Do not retry.
		c.close()
		return err
	}
	if receipt.Kind != "command" && (receipt.Kind != "prompt" || receipt.MessageID == "") {
		c.close()
		return errors.New("DSH returned no valid prompt receipt; delivery is unconfirmed")
	}
	if receipt.Kind == "command" {
		// State-only commands may complete without a status transition.
		p.mu.Lock()
		if s.activity == activity {
			s.info.Running = busy
		}
		p.mu.Unlock()
		p.notify(s.Summary.NativeID)
		if receipt.Execution.Result.Kind == "error" {
			return fmt.Errorf("DSH command: %s", receipt.Execution.Result.Text)
		}
	}
	return nil
}

func (p *Provider) exited(s *session, c *conn, err error) {
	p.mu.Lock()
	if s.conn != c {
		p.mu.Unlock()
		return
	}
	s.conn = nil
	s.info.Alive = false
	if s.info.Running && !s.stopping {
		s.info.LastTurnFailed = true
		if s.info.Error == "" {
			s.info.Error = err.Error()
		}
		s.Summary.LastTurnFailed = true
	}
	s.info.Running = false
	s.info.Waiting = false
	s.pending = map[string]*pendingRequest{}
	saveErr := p.saveLocked(s)
	p.mu.Unlock()
	if saveErr != nil {
		p.recordError(s.Summary.NativeID, saveErr)
	}
	p.notify(s.Summary.NativeID)
}
func (p *Provider) stopLocked(s *session) {
	p.mu.Lock()
	c := s.conn
	s.stopping = true
	s.info.Running = false
	s.info.Waiting = false
	s.pending = map[string]*pendingRequest{}
	p.mu.Unlock()
	p.notify(s.Summary.NativeID)
	if c != nil {
		c.close()
	}
}
func (p *Provider) DirectStop(_ context.Context, id string) error {
	s, err := p.get(id)
	if err != nil {
		return err
	}
	s.op.Lock()
	defer s.op.Unlock()
	p.stopLocked(s)
	return nil
}
func (p *Provider) DirectInterrupt(ctx context.Context, id string) error {
	return p.DirectStop(ctx, id)
}
func (p *Provider) DirectSetModel(_ context.Context, id, value string) error {
	return p.setting(id, value, false)
}
func (p *Provider) DirectSetEffort(_ context.Context, id, value string) error {
	return p.setting(id, value, true)
}
func (p *Provider) setting(id, value string, effort bool) error {
	if effort && !providers.ValidEffortID(value) || !effort && !providers.ValidModelID(value) {
		return errors.New("invalid DSH model or effort")
	}
	s, err := p.get(id)
	if err != nil {
		return err
	}
	s.op.Lock()
	defer s.op.Unlock()
	p.mu.Lock()
	if effort {
		s.Summary.Effort = value
		s.info.RequestedEffort = value
	} else {
		s.Summary.Model = value
		s.info.RequestedModel = value
	}
	err = p.saveLocked(s)
	p.mu.Unlock()
	p.notify(id)
	return err
}
func (p *Provider) DirectSetMode(ctx context.Context, id, mode string) error {
	s, err := p.get(id)
	if err != nil {
		return err
	}
	s.op.Lock()
	defer s.op.Unlock()
	p.mu.Lock()
	c := s.conn
	current := s.Summary.Mode
	p.mu.Unlock()
	if c == nil {
		return providers.ErrNotLive
	}
	if mode == "" {
		mode = workspaceMode
		if current == workspaceMode {
			mode = fullMode
		}
	}
	if mode != workspaceMode && mode != fullMode {
		return errors.New("unsupported DSH permission mode")
	}
	if err = p.modeCall(ctx, c, id, mode); err != nil {
		return err
	}
	p.mu.Lock()
	s.Summary.Mode = mode
	s.info.Mode = mode
	s.info.ModeID = mode
	err = p.saveLocked(s)
	p.mu.Unlock()
	p.notify(id)
	return err
}
func (p *Provider) DirectSessions() []providers.DirectInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]providers.DirectInfo, 0, len(p.sessions))
	for _, s := range p.sessions {
		out = append(out, s.info)
	}
	return out
}
func (p *Provider) Summary(_ context.Context, id string, _ *providers.Live) (*providers.Summary, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.sessions[id]
	if s == nil {
		return nil, providers.ErrNotFound
	}
	summary := s.Summary
	if s.info.Alive {
		summary.Model = s.info.Model
		summary.Effort = s.info.Effort
	}
	summary.Pending = ""
	for _, req := range s.pending {
		summary.Pending = req.Interaction.Awaits()
		break
	}
	return &summary, nil
}
func (p *Provider) Recent(_ context.Context, since time.Time) ([]providers.Summary, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []providers.Summary{}
	for _, s := range p.sessions {
		if !s.Summary.UpdatedAt.Before(since) {
			out = append(out, s.Summary)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}
func (*Provider) Image(context.Context, string, string, int) (string, []byte, error) {
	return "", nil, providers.ErrNotFound
}
func (p *Provider) Close() {
	p.mu.Lock()
	p.closed = true
	all := make([]*session, 0, len(p.sessions))
	for _, s := range p.sessions {
		all = append(all, s)
	}
	p.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range all {
		wg.Add(1)
		go func() { defer wg.Done(); s.op.Lock(); defer s.op.Unlock(); p.stopLocked(s) }()
	}
	wg.Wait()
}
