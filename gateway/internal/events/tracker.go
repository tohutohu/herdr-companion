package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// Tracker publishes a TypeSession event when a session the Gateway runs
// changes: its status, its summary, or its conversation (the summary follows
// the transcript, so a new tool result changes it too).
//
// The runtime tells it about state changes (Kick), but an agent also writes
// its transcript between them, so while a session is running and someone is
// connected it reads the live sessions every Interval; otherwise every Resync,
// which keeps the fingerprints current for the next client.
type Tracker struct {
	Hub *Hub
	// Live returns the sessions the Gateway runs (sessions.Service.LiveSessions).
	Live func(context.Context) ([]model.Session, error)
	// Debounce lets providers flush transcripts before they are read.
	Debounce time.Duration
	// Interval is the read period while a session is running.
	Interval time.Duration
	// Resync is the read period otherwise.
	Resync time.Duration

	kickOnce sync.Once
	kick     chan struct{}

	mu sync.Mutex
	// last is each live session's fingerprint; nil until the first read,
	// which only records them.
	last map[string]string
}

func (t *Tracker) kicks() chan struct{} {
	t.kickOnce.Do(func() { t.kick = make(chan struct{}, 1) })
	return t.kick
}

// Kick asks for a read soon. It never blocks.
func (t *Tracker) Kick() {
	select {
	case t.kicks() <- struct{}{}:
	default:
	}
}

// Run reads until ctx ends.
func (t *Tracker) Run(ctx context.Context) {
	debounce := orDefault(t.Debounce, 150*time.Millisecond)
	interval := orDefault(t.Interval, time.Second)
	resync := orDefault(t.Resync, 15*time.Second)
	t.Hub.OnSubscribers(func(n int) {
		if n > 0 {
			t.Kick()
		}
	})
	t.Evaluate(ctx)
	timer := time.NewTimer(resync)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.kicks():
			select {
			case <-ctx.Done():
				return
			case <-time.After(debounce):
			}
		case <-timer.C:
		}
		next := resync
		if t.Evaluate(ctx) && t.Hub.Subscribers() > 0 {
			next = interval
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(next)
	}
}

// Evaluate reads the live sessions once and publishes what changed. It
// reports whether one of them is running.
func (t *Tracker) Evaluate(ctx context.Context) (running bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	sessions, err := t.Live(ctx)
	if err != nil {
		slog.Debug("live sessions unavailable", "operation", "events.track", "error", err)
		return false
	}
	now := make(map[string]string, len(sessions))
	status := make(map[string]model.Status, len(sessions))
	for _, s := range sessions {
		raw, _ := json.Marshal(s)
		now[s.ID] = string(raw)
		status[s.ID] = s.Status
		if s.Status == model.StatusRunning {
			running = true
		}
	}
	t.mu.Lock()
	prev := t.last
	t.last = now
	t.mu.Unlock()
	if prev == nil {
		// The first read: nobody has been told anything yet.
		return running
	}
	for id, fp := range now {
		if prev[id] != fp {
			t.Hub.Publish(TypeSession, SessionChange{SessionID: id, Status: string(status[id])})
		}
	}
	for id := range prev {
		if _, ok := now[id]; !ok {
			t.Hub.Publish(TypeSession, SessionChange{SessionID: id, Removed: true})
		}
	}
	return running
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}
