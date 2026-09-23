// Package sessions joins Herdr's live view (which pane runs which native
// session, and its status) with provider data. Nothing is stored: every call
// reads current state.
package sessions

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// Snapshotter is the Herdr dependency.
type Snapshotter interface {
	Snapshot(ctx context.Context) (*herdr.Snapshot, error)
}

// Archive is the set of archived session ids.
type Archive interface {
	Has(id string) bool
	IDs() []string
}

type Service struct {
	herdr         Snapshotter
	providers     []providers.Provider
	offlineWindow time.Duration
	// Archive hides archived offline sessions from List. Optional.
	Archive Archive
}

func New(h Snapshotter, offlineWindow time.Duration, ps ...providers.Provider) *Service {
	return &Service{herdr: h, providers: ps, offlineWindow: offlineWindow}
}

// Resolved is a session with the handles needed to act on it.
type Resolved struct {
	Provider providers.Provider
	NativeID string
	Live     *providers.Live
	Label    string // Herdr workspace label
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

func (s *Service) snapshot(ctx context.Context) *herdr.Snapshot {
	snap, err := s.herdr.Snapshot(ctx)
	if err != nil {
		slog.Warn("herdr snapshot failed; sessions shown as offline", "operation", "session.snapshot", "error", err)
		return nil
	}
	return snap
}

// live returns sessions currently hosted by Herdr panes, keyed by gateway id.
func (s *Service) live(snap *herdr.Snapshot) map[string]*Resolved {
	out := map[string]*Resolved{}
	if snap == nil {
		return out
	}
	for _, pane := range snap.Panes {
		ref := pane.AgentSession
		if ref == nil || ref.Kind != "id" || ref.Value == "" {
			continue
		}
		for _, p := range s.providers {
			// agent_session is a stored reference; only trust it while the
			// same agent still occupies the pane.
			if ref.Agent != p.HerdrAgent() || pane.AgentName() != p.HerdrAgent() {
				continue
			}
			r := &Resolved{
				Provider: p,
				NativeID: ref.Value,
				Live:     &providers.Live{PaneID: pane.PaneID, HerdrStatus: pane.AgentStatus, Cwd: pane.WorkingDir()},
				Label:    snap.WorkspaceLabel(pane.WorkspaceID),
			}
			if _, dup := out[r.ID()]; !dup {
				out[r.ID()] = r
			}
		}
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
	if r, ok := s.live(s.snapshot(ctx))[id]; ok {
		return r, nil
	}
	return &Resolved{Provider: p, NativeID: native}, nil
}

// Session builds the DTO for a resolved session.
func (s *Service) Session(ctx context.Context, r *Resolved) (model.Session, error) {
	sum, err := r.Provider.Summary(ctx, r.NativeID, r.Live)
	if errors.Is(err, providers.ErrNotFound) && r.Live != nil {
		// The list shows a live session whose agent has saved nothing yet
		// (e.g. Devin before the first prompt); opening or archiving it must
		// work too.
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

// LiveSessions returns only sessions currently hosted by Herdr.
func (s *Service) LiveSessions(ctx context.Context) ([]model.Session, error) {
	snap, err := s.herdr.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return s.liveSessions(ctx, s.live(snap)), nil
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
				// Herdr knows the session but the provider has no data yet
				// (e.g. a brand new session): still list it.
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
	live := s.live(s.snapshot(ctx))
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
	var out []model.Session
	for i := range recent {
		r := &Resolved{Provider: p, NativeID: recent[i].NativeID}
		if _, ok := live[r.ID()]; ok {
			continue
		}
		if s.archived(r.ID()) {
			continue
		}
		out = append(out, s.toSession(r, &recent[i]))
	}
	return out
}

// Archived lists archived sessions, most recently archived first. Sessions
// the provider no longer knows are skipped.
func (s *Service) Archived(ctx context.Context) []model.Session {
	if s.Archive == nil {
		return nil
	}
	live := s.live(s.snapshot(ctx))
	var out []model.Session
	for _, id := range s.Archive.IDs() {
		r, ok := live[id]
		if !ok {
			pname, native, valid := SplitID(id)
			p := s.provider(pname)
			if !valid || p == nil {
				continue
			}
			r = &Resolved{Provider: p, NativeID: native}
		}
		sess, err := s.Session(ctx, r)
		if err != nil {
			slog.Debug("archived session unavailable", "session_id", id, "error", err)
			continue
		}
		out = append(out, sess)
	}
	return out
}

func (s *Service) archived(id string) bool { return s.Archive != nil && s.Archive.Has(id) }

func (s *Service) toSession(r *Resolved, sum *providers.Summary) model.Session {
	sess := toSession(r, sum)
	sess.Archived = s.archived(sess.ID)
	return sess
}

func toSession(r *Resolved, sum *providers.Summary) model.Session {
	sess := model.Session{
		ID:           r.ID(),
		Provider:     r.Provider.Name(),
		ProviderName: r.Provider.DisplayName(),
		Title:        sum.Title,
		Cwd:          sum.Cwd,
		UpdatedAt:    sum.UpdatedAt,
		LastMessage:  sum.LastMessage,
		Model:        sum.Model,
		Effort:       sum.Effort,
		Mode:         sum.Mode,
		Context:      sum.Context,
		Cost:         sum.Cost,
		Status:       Normalize(r.Live, sum),
	}
	if r.Live != nil {
		sess.PaneID = r.Live.PaneID
		// Providers resolve file links against the pane cwd, so the file API
		// must use the same root (the transcript cwd follows `cd`s).
		if r.Live.Cwd != "" {
			sess.Cwd = r.Live.Cwd
		}
	}
	sess.CanSend = r.Live != nil || sum.Status != ""
	sess.Project = r.Label
	if sess.Project == "" {
		sess.Project = providers.ProjectName(sess.Cwd)
	}
	return sess
}

// Normalize maps Herdr lifecycle + provider hints to the public status set.
func Normalize(live *providers.Live, sum *providers.Summary) model.Status {
	if sum.Status != "" {
		return sum.Status
	}
	if live == nil {
		return model.StatusOffline
	}
	switch live.HerdrStatus {
	case herdr.StatusWorking:
		return model.StatusRunning
	case herdr.StatusBlocked:
		if sum.Pending == model.InteractionApproval {
			return model.StatusWaitingApproval
		}
		return model.StatusWaitingInput
	case herdr.StatusDone:
		if sum.LastTurnFailed {
			return model.StatusFailed
		}
		return model.StatusCompleted
	default: // idle, unknown
		if sum.LastTurnFailed {
			return model.StatusFailed
		}
		return model.StatusIdle
	}
}
