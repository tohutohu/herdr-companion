// Package runtime keeps track of the sessions the gateway runs:
// Claude Code through the Agent SDK sidecar and Codex through the
// gateway's own app-server. The providers own the processes; the manager owns
// what outlives them.
//
// The agents' own histories stay the source of truth. The manager persists
// only a small record per session (id, directory, requested model / effort /
// mode, whether a turn was running) in direct-sessions.json, so that after a
// gateway restart the sessions are still listed as directly managed, a
// session that was cut off mid-turn shows as failed, and the next message
// resumes it in a new process. Nothing is ever re-sent on its own.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// ErrNotManaged is returned for a session the manager has no record of.
var ErrNotManaged = errors.New("the gateway does not run this session")

// Record is what the manager persists about one session.
type Record struct {
	ID        string    `json:"id"` // gateway id, "<provider>:<native>"
	Cwd       string    `json:"cwd"`
	StartedAt time.Time `json:"startedAt"`
	ActiveAt  time.Time `json:"activeAt"`
	// Model, Effort and Mode are the user's latest choices, restored when the
	// session is resumed in a new process.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	Mode   string `json:"mode,omitempty"`
	// Running is set while a turn runs. Still set at load, it means the
	// gateway stopped during the turn.
	Running bool `json:"running,omitempty"`
}

type file struct {
	Version  int       `json:"version"`
	Sessions []*Record `json:"sessions"`
}

// state is what the manager knows only while it runs.
type state struct {
	turns   int
	running bool
	unread  bool
	failed  bool
	err     string
}

type Manager struct {
	path     string
	window   time.Duration
	runtimes map[string]providers.DirectRuntime

	mu       sync.Mutex
	records  map[string]*Record
	states   map[string]*state
	resuming map[string]*sync.Mutex
	listener func()
	now      func() time.Time
	// closing freezes the records while the gateway shuts down: the runtimes
	// exit then, and a session that was running must still be recorded as
	// running, so the next start shows it as cut off.
	closing bool
}

// Shutdown freezes the records before the runtimes are stopped.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	m.closing = true
	m.mu.Unlock()
}

// New loads the records at path. Providers that are not DirectRuntimes are
// ignored. Records idle for longer than window are dropped.
func New(path string, window time.Duration, ps ...providers.Provider) (*Manager, error) {
	m := &Manager{path: path, window: window, runtimes: map[string]providers.DirectRuntime{},
		records: map[string]*Record{}, states: map[string]*state{}, resuming: map[string]*sync.Mutex{}, now: time.Now}
	if err := m.load(); err != nil {
		return nil, err
	}
	for _, p := range ps {
		rt, ok := p.(providers.DirectRuntime)
		if !ok {
			continue
		}
		name := p.Name()
		m.runtimes[name] = rt
		rt.SetDirectListener(func(native string) { m.changed(name, native) })
	}
	return m, nil
}

func (m *Manager) load() error {
	b, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("parse %s: %w", m.path, err)
	}
	for _, r := range f.Sessions {
		if r == nil || r.ID == "" {
			continue
		}
		m.records[r.ID] = r
		if r.Running {
			// The gateway stopped while a turn ran: the agent process went with
			// it. Show the session as failed rather than idle, and never run
			// the turn again.
			m.states[r.ID] = &state{failed: true, err: "The gateway stopped while this session was running; the turn was cut off."}
			r.Running = false
		}
	}
	m.pruneLocked()
	return nil
}

// saveLocked writes the records atomically. Errors are logged: losing a
// record only makes a session show as offline, never loses history.
func (m *Manager) saveLocked() {
	f := file{Version: 1}
	for _, r := range m.records {
		cp := *r
		f.Sessions = append(f.Sessions, &cp)
	}
	sort.Slice(f.Sessions, func(i, j int) bool { return f.Sessions[i].ID < f.Sessions[j].ID })
	b, _ := json.MarshalIndent(f, "", "  ")
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		slog.Warn("saving direct sessions failed", "operation", "runtime.save", "error", err)
		return
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		slog.Warn("saving direct sessions failed", "operation", "runtime.save", "error", err)
		return
	}
	if err := os.Rename(tmp, m.path); err != nil {
		slog.Warn("saving direct sessions failed", "operation", "runtime.save", "error", err)
	}
}

func (m *Manager) pruneLocked() {
	if m.window <= 0 {
		return
	}
	cutoff := m.now().Add(-m.window)
	for id, r := range m.records {
		if r.ActiveAt.Before(cutoff) && !m.aliveLocked(id) {
			delete(m.records, id)
			delete(m.states, id)
		}
	}
}

// SetListener registers a callback run after any change of a direct session
// (the notification watcher re-evaluates on it).
func (m *Manager) SetListener(f func()) {
	m.mu.Lock()
	m.listener = f
	m.mu.Unlock()
}

// Supports reports whether provider can run sessions directly.
func (m *Manager) Supports(provider string) bool {
	_, ok := m.runtimes[provider]
	return ok
}

func split(id string) (string, string) {
	for i := 0; i < len(id); i++ {
		if id[i] == ':' {
			return id[:i], id[i+1:]
		}
	}
	return "", ""
}

func (m *Manager) runtime(id string) (providers.DirectRuntime, string, error) {
	name, native := split(id)
	rt, ok := m.runtimes[name]
	if !ok || native == "" {
		return nil, "", providers.ErrNotFound
	}
	return rt, native, nil
}

func (m *Manager) info(id string) (providers.DirectInfo, bool) {
	rt, native, err := m.runtime(id)
	if err != nil {
		return providers.DirectInfo{}, false
	}
	for _, in := range rt.DirectSessions() {
		if in.NativeID == native {
			return in, true
		}
	}
	return providers.DirectInfo{}, false
}

func (m *Manager) aliveLocked(id string) bool {
	in, ok := m.info(id)
	return ok && in.Alive
}

// changed folds a runtime change into the session's state.
func (m *Manager) changed(provider, native string) {
	id := provider + ":" + native
	in, ok := m.info(id)
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return
	}
	st := m.states[id]
	if st == nil {
		st = &state{}
		m.states[id] = st
	}
	rec := m.records[id]
	save := false
	switch {
	case ok && in.Alive:
		if in.Turns > st.turns {
			// A finished turn is unread until someone looks at the session.
			st.unread = true
		}
		st.turns = in.Turns
		if in.Running || in.Waiting {
			st.failed, st.err = false, ""
		}
		if rec != nil && rec.Running != in.Running {
			rec.Running = in.Running
			rec.ActiveAt = m.now()
			save = true
		}
		st.running = in.Running
	case st.running || (rec != nil && rec.Running):
		// The process ended during a turn.
		st.running, st.failed = false, true
		st.err = "The agent runtime exited while this session was running."
		if ok && in.Error != "" {
			st.err = in.Error
		}
		if rec != nil {
			rec.Running = false
			save = true
		}
	}
	if save {
		m.saveLocked()
	}
	listener := m.listener
	m.mu.Unlock()
	if listener != nil {
		listener()
	}
}

func (m *Manager) notify() {
	m.mu.Lock()
	listener := m.listener
	m.mu.Unlock()
	if listener != nil {
		listener()
	}
}

// Start starts a new direct session and records it. An id with an error
// means the session started but its first prompt was not delivered.
func (m *Manager) Start(ctx context.Context, provider string, opts providers.DirectStart) (string, error) {
	rt, ok := m.runtimes[provider]
	if !ok {
		return "", providers.ErrNotFound
	}
	opts.Resume = ""
	native, err := rt.DirectStart(ctx, opts)
	if native == "" {
		return "", err
	}
	// The session exists even when its first prompt failed (err != nil).
	id := provider + ":" + native
	m.record(id, opts)
	return id, err
}

func (m *Manager) record(id string, opts providers.DirectStart) {
	now := m.now()
	m.mu.Lock()
	r := m.records[id]
	if r == nil {
		r = &Record{ID: id, StartedAt: now}
		m.records[id] = r
	}
	r.Cwd, r.ActiveAt = opts.Cwd, now
	r.Model, r.Effort, r.Mode = opts.Model, opts.Effort, opts.Mode
	if st := m.states[id]; st != nil {
		st.failed, st.err = false, ""
	}
	m.saveLocked()
	m.mu.Unlock()
	m.notify()
}

// Resume continues an existing session (its own or one started anywhere
// else) in a new process, under the same id. Model, effort and mode left
// empty are taken from the session's record.
func (m *Manager) Resume(ctx context.Context, id string, opts providers.DirectStart) error {
	rt, native, err := m.runtime(id)
	if err != nil {
		return err
	}
	lock := m.resumeLock(id)
	lock.Lock()
	defer lock.Unlock()
	m.mu.Lock()
	_, recorded := m.records[id]
	m.mu.Unlock()
	if !recorded {
		// A session forgotten just now (archived) may still be closing: its
		// input is shut, so wait for it to go before starting it again.
		m.waitClosed(ctx, id)
	}
	if in, ok := m.info(id); ok && in.Alive {
		if !recorded {
			m.record(id, opts)
		}
		return nil
	}
	m.mu.Lock()
	if r := m.records[id]; r != nil {
		if opts.Cwd == "" {
			opts.Cwd = r.Cwd
		}
		if opts.Model == "" {
			opts.Model = r.Model
		}
		if opts.Effort == "" {
			opts.Effort = r.Effort
		}
		if opts.Mode == "" {
			opts.Mode = r.Mode
		}
	}
	m.mu.Unlock()
	opts.Resume, opts.Prompt = native, model.Input{}
	if _, err := rt.DirectStart(ctx, opts); err != nil {
		return err
	}
	m.record(id, opts)
	slog.Info("direct session resumed", "session_id", id, "operation", "runtime.resume", "cwd", opts.Cwd)
	return nil
}

// closeWait bounds how long Resume waits for a stopping session to close.
var closeWait = 10 * time.Second

func (m *Manager) waitClosed(ctx context.Context, id string) {
	deadline := time.Now().Add(closeWait)
	for {
		if in, ok := m.info(id); !ok || !in.Alive || time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (m *Manager) resumeLock(id string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.resuming[id]
	if l == nil {
		l = &sync.Mutex{}
		m.resuming[id] = l
	}
	return l
}

// EnsureRunning resumes a recorded session whose process is gone, so a new
// message can be sent to it. It never repeats anything the session did.
func (m *Manager) EnsureRunning(ctx context.Context, id string) error {
	m.mu.Lock()
	_, ok := m.records[id]
	m.mu.Unlock()
	if !ok {
		return ErrNotManaged
	}
	return m.Resume(ctx, id, providers.DirectStart{})
}

// Owns reports whether the session is recorded as directly managed.
func (m *Manager) Owns(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.records[id]
	return ok
}

// Alive reports whether the session's process is running.
func (m *Manager) Alive(id string) bool {
	in, ok := m.info(id)
	return ok && in.Alive
}

// Stop ends the session's process. The record stays, so the session is
// still listed and the next message resumes it.
func (m *Manager) Stop(ctx context.Context, id string) error {
	rt, native, err := m.runtime(id)
	if err != nil {
		return err
	}
	if in, ok := m.info(id); !ok || !in.Alive {
		return nil
	}
	return rt.DirectStop(ctx, native)
}

// Forget stops the session and drops its record (archiving).
func (m *Manager) Forget(ctx context.Context, id string) error {
	if err := m.Stop(ctx, id); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.records, id)
	delete(m.states, id)
	m.saveLocked()
	m.mu.Unlock()
	m.notify()
	return nil
}

// MarkSeen clears the unread mark of a finished turn.
func (m *Manager) MarkSeen(id string) {
	m.mu.Lock()
	st := m.states[id]
	changed := st != nil && st.unread
	if changed {
		st.unread = false
	}
	m.mu.Unlock()
	if changed {
		m.notify()
	}
}

func (m *Manager) Interrupt(ctx context.Context, id string) error {
	rt, native, err := m.runtime(id)
	if err != nil {
		return err
	}
	if !m.Alive(id) {
		return providers.ErrNotLive
	}
	return rt.DirectInterrupt(ctx, native)
}

// SetModel changes the model. A session without a process keeps the choice
// for the process that resumes it.
func (m *Manager) SetModel(ctx context.Context, id, model string) error {
	return m.setting(ctx, id, func(r *Record) { r.Model = model }, func(rt providers.DirectRuntime, native string) error {
		return rt.DirectSetModel(ctx, native, model)
	})
}

func (m *Manager) SetEffort(ctx context.Context, id, effort string) error {
	return m.setting(ctx, id, func(r *Record) { r.Effort = effort }, func(rt providers.DirectRuntime, native string) error {
		return rt.DirectSetEffort(ctx, native, effort)
	})
}

// SetMode sets a mode id, or advances to the next mode when mode is empty.
func (m *Manager) SetMode(ctx context.Context, id, mode string) error {
	rt, native, err := m.runtime(id)
	if err != nil {
		return err
	}
	if !m.Alive(id) {
		if mode == "" {
			return providers.ErrNotLive
		}
		return m.update(id, func(r *Record) { r.Mode = mode })
	}
	if err := rt.DirectSetMode(ctx, native, mode); err != nil {
		return err
	}
	if mode == "" {
		// Cycled: the runtime knows the new mode only by its label, which
		// is no mode id to resume with. The running process keeps it.
		m.notify()
		return nil
	}
	return m.update(id, func(r *Record) { r.Mode = mode })
}

func (m *Manager) setting(ctx context.Context, id string, set func(*Record), apply func(providers.DirectRuntime, string) error) error {
	rt, native, err := m.runtime(id)
	if err != nil {
		return err
	}
	if m.Alive(id) {
		if err := apply(rt, native); err != nil {
			return err
		}
	}
	return m.update(id, set)
}

func (m *Manager) update(id string, set func(*Record)) error {
	m.mu.Lock()
	r := m.records[id]
	if r == nil {
		m.mu.Unlock()
		return ErrNotManaged
	}
	set(r)
	m.saveLocked()
	m.mu.Unlock()
	m.notify()
	return nil
}

// SettingsApply says when a model or effort change applies for provider.
func (m *Manager) SettingsApply(provider string) string {
	if rt, ok := m.runtimes[provider]; ok {
		return rt.SettingsApply()
	}
	return ""
}

// Session is one directly managed session as the sessions service lists it.
type Session struct {
	ID   string
	Live *providers.Live
	// Requested model / effort not yet used by the agent, and when it will be.
	PendingModel, PendingEffort, SettingsApply string
	// Mode is the runtime's mode label, when it knows one.
	Mode  string
	Error string
}

// Sessions returns every recorded session with its live state.
func (m *Manager) Sessions() map[string]Session {
	infos := map[string]providers.DirectInfo{}
	for name, rt := range m.runtimes {
		for _, in := range rt.DirectSessions() {
			infos[name+":"+in.NativeID] = in
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	out := make(map[string]Session, len(m.records))
	for id, r := range m.records {
		st := m.states[id]
		if st == nil {
			st = &state{}
		}
		name, _ := split(id)
		in, ok := infos[id]
		live := &providers.Live{Cwd: r.Cwd}
		s := Session{ID: id, Live: live, Error: st.err}
		// Set from the start, so the first finished turn changes it even
		// when the running state was never observed.
		live.Revision = r.StartedAt.Format(time.RFC3339Nano) + "#" + strconv.Itoa(st.turns)
		live.Failed = st.failed
		if !ok || !in.Alive {
			live.Dormant = true
			live.State = providers.StateIdle
			if st.unread {
				live.State = providers.StateDone
			}
			out[id] = s
			continue
		}
		if in.Cwd != "" {
			live.Cwd = in.Cwd
		}
		switch {
		case in.Waiting:
			live.State = providers.StateBlocked
		case in.Running:
			live.State = providers.StateWorking
		case st.unread:
			live.State = providers.StateDone
		default:
			live.State = providers.StateIdle
		}
		s.Mode = in.Mode
		s.PendingModel, s.PendingEffort = in.RequestedModel, in.RequestedEffort
		if s.PendingModel != "" || s.PendingEffort != "" {
			s.SettingsApply = m.runtimes[name].SettingsApply()
		}
		if in.Error != "" && in.LastTurnFailed {
			s.Error = in.Error
		}
		out[id] = s
	}
	return out
}
