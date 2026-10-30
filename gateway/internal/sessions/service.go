// Package sessions joins the sessions the gateway runs with provider data,
// and lists recently updated sessions that run nowhere as offline.
package sessions

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/runtime"
)

// Archive is the set of archived session ids.
type Archive interface {
	Has(id string) bool
	IDs() []string
}

type Service struct {
	providers     []providers.Provider
	offlineWindow time.Duration
	// Archive hides archived offline sessions from List. Optional.
	Archive Archive
	// Direct is the runtime of the sessions the gateway runs. Optional in
	// tests; without it no session is live.
	Direct *runtime.Manager
	// Hidden lists gateway ids that are not sessions (resident task runs),
	// kept out of the offline list in case an agent saved anything for
	// them. Optional.
	Hidden func(ctx context.Context) map[string]bool
}

func New(offlineWindow time.Duration, ps ...providers.Provider) *Service {
	return &Service{providers: ps, offlineWindow: offlineWindow}
}

// Resolved is a session with the handles needed to act on it.
type Resolved struct {
	Provider providers.Provider
	NativeID string
	Live     *providers.Live
	// Direct holds the runtime details of a live session.
	Direct *runtime.Session
}

func (r *Resolved) ID() string { return r.Provider.Name() + ":" + r.NativeID }

func (s *Service) provider(name string) providers.Provider {
	for _, p := range s.providers {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

func SplitID(id string) (provider, native string, ok bool) {
	provider, native, ok = strings.Cut(id, ":")
	return provider, native, ok && provider != "" && native != ""
}

// liveAll returns every session the runtime holds, keyed by gateway id.
func (s *Service) liveAll(ctx context.Context) map[string]*Resolved {
	out := map[string]*Resolved{}
	if s.Direct == nil {
		return out
	}
	for id, d := range s.Direct.Sessions() {
		pname, native, ok := SplitID(id)
		p := s.provider(pname)
		if !ok || p == nil {
			continue
		}
		d := d
		out[id] = &Resolved{Provider: p, NativeID: native, Live: d.Live, Direct: &d}
	}
	return out
}

func (s *Service) Resolve(ctx context.Context, id string) (*Resolved, error) {
	pname, native, ok := SplitID(id)
	if !ok {
		return nil, providers.ErrNotFound
	}
	p := s.provider(pname)
	if p == nil {
		return nil, providers.ErrNotFound
	}
	if r, ok := s.liveAll(ctx)[id]; ok {
		return r, nil
	}
	return &Resolved{Provider: p, NativeID: native}, nil
}

// Session builds the DTO for a resolved session.
func (s *Service) Session(ctx context.Context, r *Resolved) (model.Session, error) {
	sum, err := r.Provider.Summary(ctx, r.NativeID, r.Live)
	if errors.Is(err, providers.ErrNotFound) && r.Live != nil {
		// The list shows a live session whose agent has saved nothing yet;
		// opening or archiving it must work too.
		sum, err = liveFallback(r), nil
	}
	if err != nil {
		return model.Session{}, err
	}
	return s.toSession(r, sum), nil
}

// liveFallback summarizes a live session the provider has no data for.
func liveFallback(r *Resolved) *providers.Summary {
	return &providers.Summary{NativeID: r.NativeID, Cwd: r.Live.Cwd, UpdatedAt: time.Now()}
}

func (s *Service) Get(ctx context.Context, id string) (model.Session, *Resolved, error) {
	r, err := s.Resolve(ctx, id)
	if err != nil {
		return model.Session{}, nil, err
	}
	sess, err := s.Session(ctx, r)
	return sess, r, err
}

// Conversation returns the session and messages from the same provider read
// when supported, avoiding a full history read just to build the summary.
func (s *Service) Conversation(ctx context.Context, id string) (model.Session, []model.Message, error) {
	r, err := s.Resolve(ctx, id)
	if err != nil {
		return model.Session{}, nil, err
	}
	if p, ok := r.Provider.(providers.ConversationProvider); ok {
		sum, msgs, err := p.Conversation(ctx, r.NativeID, r.Live)
		if errors.Is(err, providers.ErrNotFound) && r.Live != nil {
			// A live agent may not have saved its first history yet.
			sum, msgs, err = liveFallback(r), nil, nil
		}
		if err != nil {
			return model.Session{}, nil, err
		}
		return s.toSession(r, sum), msgs, nil
	}
	sess, err := s.Session(ctx, r)
	if err != nil {
		return model.Session{}, nil, err
	}
	msgs, err := r.Provider.Messages(ctx, r.NativeID, r.Live)
	if errors.Is(err, providers.ErrNotFound) && r.Live != nil {
		msgs, err = nil, nil
	}
	return sess, msgs, err
}

// Forget drops what the session's provider has cached for it.
func (s *Service) Forget(id string) {
	pname, native, ok := SplitID(id)
	if !ok {
		return
	}
	if f, ok := s.provider(pname).(providers.Forgetter); ok {
		f.Forget(native)
	}
}

// LiveSessions returns only the sessions the gateway runs.
func (s *Service) LiveSessions(ctx context.Context) ([]model.Session, error) {
	return s.liveSessions(ctx, s.liveAll(ctx)), nil
}

// liveSessions summarizes each live session concurrently: providers read
// transcripts or call the Codex app-server, so one slow session should not
// delay the others.
func (s *Service) liveSessions(ctx context.Context, live map[string]*Resolved) []model.Session {
	out := make([]model.Session, 0, len(live))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, r := range live {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sum, err := r.Provider.Summary(ctx, r.NativeID, r.Live)
			if err != nil {
				// The runtime knows the session but the provider has no data
				// yet (e.g. a brand new session): still list it.
				slog.Debug("summary unavailable", "provider", r.Provider.Name(), "session_id", r.ID(), "error", err)
				sum = liveFallback(r)
			}
			sess := s.toSession(r, sum)
			mu.Lock()
			out = append(out, sess)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// List returns live sessions first plus recently updated offline ones. Live
// summaries and each provider's recent sessions are read concurrently.
func (s *Service) List(ctx context.Context) ([]model.Session, error) {
	live := s.liveAll(ctx)
	var liveOut []model.Session
	offline := make([][]model.Session, len(s.providers))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		liveOut = s.liveSessions(ctx, live)
	}()
	if s.offlineWindow > 0 {
		since := time.Now().Add(-s.offlineWindow)
		for i, p := range s.providers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				offline[i] = s.recentSessions(ctx, p, live, since)
			}()
		}
	}
	wg.Wait()
	out := liveOut
	for _, sessions := range offline {
		out = append(out, sessions...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		li, lj := out[i].Status != model.StatusOffline, out[j].Status != model.StatusOffline
		if li != lj {
			return li
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

// recentSessions returns p's recently updated sessions that are neither live
// nor archived.
func (s *Service) recentSessions(ctx context.Context, p providers.Provider, live map[string]*Resolved, since time.Time) []model.Session {
	var recent []providers.Summary
	var err error
	if filtered, ok := p.(providers.FilteredRecentProvider); ok {
		exclude := map[string]bool{}
		for _, r := range live {
			if r.Provider.Name() == p.Name() {
				exclude[r.NativeID] = true
			}
		}
		if s.Archive != nil {
			for _, id := range s.Archive.IDs() {
				name, native, ok := SplitID(id)
				if ok && name == p.Name() {
					exclude[native] = true
				}
			}
		}
		recent, err = filtered.RecentExcluding(ctx, since, exclude)
	} else {
		recent, err = p.Recent(ctx, since)
	}
	if err != nil {
		slog.Warn("listing recent sessions failed", "provider", p.Name(), "operation", "recent", "error", err)
	}
	var hidden map[string]bool
	if s.Hidden != nil {
		hidden = s.Hidden(ctx)
	}
	var out []model.Session
	for i := range recent {
		r := &Resolved{Provider: p, NativeID: recent[i].NativeID}
		if _, ok := live[r.ID()]; ok {
			continue
		}
		if hidden[r.ID()] {
			continue
		}
		if s.archived(r.ID()) {
			continue
		}
		out = append(out, s.toSession(r, &recent[i]))
	}
	return out
}

// archivedConcurrency bounds the summaries read at once for the archive list.
const archivedConcurrency = 8

// Archived lists archived sessions, most recently archived first, starting at
// the offset-th archived id. A limit <= 0 lists the rest. next is the offset
// of the following page, or 0 when there is none. Sessions the provider no
// longer knows are skipped, so a page may hold fewer than limit sessions.
func (s *Service) Archived(ctx context.Context, offset, limit int) (out []model.Session, next int) {
	if s.Archive == nil {
		return nil, 0
	}
	ids := s.Archive.IDs()
	if offset >= len(ids) {
		return nil, 0
	}
	ids = ids[max(offset, 0):]
	if limit > 0 && limit < len(ids) {
		ids = ids[:limit]
		next = max(offset, 0) + limit
	}
	live := s.liveAll(ctx)
	// Summaries read transcripts or call the Codex app-server; reading them
	// one by one made a long archive take seconds.
	found := make([]*model.Session, len(ids))
	sem := make(chan struct{}, archivedConcurrency)
	var wg sync.WaitGroup
	for i, id := range ids {
		r, ok := live[id]
		if !ok {
			pname, native, valid := SplitID(id)
			p := s.provider(pname)
			if !valid || p == nil {
				continue
			}
			r = &Resolved{Provider: p, NativeID: native}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			sess, err := s.Session(ctx, r)
			if err != nil {
				slog.Debug("archived session unavailable", "session_id", id, "error", err)
				return
			}
			found[i] = &sess
		}()
	}
	wg.Wait()
	for _, sess := range found {
		if sess != nil {
			out = append(out, *sess)
		}
	}
	return out, next
}

func (s *Service) archived(id string) bool { return s.Archive != nil && s.Archive.Has(id) }

func (s *Service) toSession(r *Resolved, sum *providers.Summary) model.Session {
	sess := toSession(r, sum)
	sess.Archived = s.archived(sess.ID)
	if d := r.Direct; d != nil {
		if d.Mode != "" {
			sess.Mode = d.Mode
		}
		sess.PendingModel, sess.PendingEffort, sess.SettingsApply = d.PendingModel, d.PendingEffort, d.SettingsApply
		if sess.Status == model.StatusFailed {
			sess.Error = d.Error
		}
	}
	return sess
}

func toSession(r *Resolved, sum *providers.Summary) model.Session {
	sess := model.Session{
		ID:                 r.ID(),
		Provider:           r.Provider.Name(),
		ProviderName:       r.Provider.DisplayName(),
		Title:              sum.Title,
		Cwd:                sum.Cwd,
		UpdatedAt:          sum.UpdatedAt,
		LastMessage:        sum.LastMessage,
		CompletionRevision: sum.CompletionRevision,
		Model:              sum.Model,
		Effort:             sum.Effort,
		Mode:               sum.Mode,
		Context:            sum.Context,
		Cost:               sum.Cost,
		Status:             Normalize(r.Live, sum),
	}
	if sess.CompletionRevision == "" && r.Live != nil {
		sess.CompletionRevision = r.Live.Revision
	}
	if r.Live != nil {
		sess.Unread = r.Live.State == providers.StateDone
		// Providers resolve file links against the session's cwd, so the
		// file API must use the same root (the transcript cwd follows `cd`s).
		if r.Live.Cwd != "" && !sum.PinnedCwd {
			sess.Cwd = r.Live.Cwd
		}
	}
	sess.CanSend = r.Live != nil
	sess.Project = providers.ProjectName(sess.Cwd)
	return sess
}

// Normalize maps the runtime state + provider hints to the public status set.
func Normalize(live *providers.Live, sum *providers.Summary) model.Status {
	if live == nil {
		return model.StatusOffline
	}
	if sum.Status != "" {
		return sum.Status
	}
	if live.Failed && live.State != providers.StateWorking && live.State != providers.StateBlocked {
		// A session whose process died during a turn.
		return model.StatusFailed
	}
	switch live.State {
	case providers.StateWorking:
		return model.StatusRunning
	case providers.StateBlocked:
		if sum.Pending == model.InteractionApproval {
			return model.StatusWaitingApproval
		}
		return model.StatusWaitingInput
	case providers.StateDone:
		if sum.LastTurnFailed {
			return model.StatusFailed
		}
		return model.StatusCompleted
	default: // idle
		if sum.LastTurnFailed {
			return model.StatusFailed
		}
		return model.StatusIdle
	}
}

// Send delivers input to a live session. A session without a process is
// resumed first, under the same id, with nothing replayed.
func (s *Service) Send(ctx context.Context, r *Resolved, in model.Input) error {
	if r.Live == nil {
		return providers.ErrNotLive
	}
	live := r.Live
	if live.Dormant {
		if s.Direct == nil {
			return providers.ErrNotLive
		}
		if err := s.Direct.EnsureRunning(ctx, r.ID()); err != nil {
			return err
		}
		fresh, ok := s.Direct.Sessions()[r.ID()]
		if !ok || fresh.Live.Dormant {
			return providers.ErrRuntimeUnavailable
		}
		live = fresh.Live
	}
	return r.Provider.Send(ctx, r.NativeID, live, in)
}
