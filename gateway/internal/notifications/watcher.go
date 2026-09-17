package notifications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// Sender delivers one message to one device.
type Sender interface {
	Send(ctx context.Context, deviceToken string, data map[string]string) error
}

// Sessions is the subset of the sessions service the watcher needs.
type Sessions interface {
	LiveSessions(ctx context.Context) ([]model.Session, error)
}

// Subscriber is the Herdr event stream.
type Subscriber interface {
	Snapshot(ctx context.Context) (*herdr.Snapshot, error)
	Subscribe(ctx context.Context, subs []map[string]any) (<-chan herdr.Event, error)
}

// Watcher notices state transitions of live sessions and pushes them.
// Its only state is the last status seen per session, in memory.
type Watcher struct {
	Herdr    Subscriber
	Sessions Sessions
	Sender   Sender // nil disables sending (transitions are still logged)
	Config   *config.Store
	Sink     deadletter.Sink

	// Debounce lets providers flush transcripts before we read them.
	Debounce time.Duration
	// Resync re-evaluates everything periodically (covers missed events).
	Resync time.Duration

	mu   sync.Mutex
	last map[string]model.Status
}

func (w *Watcher) Run(ctx context.Context) {
	if w.Debounce == 0 {
		w.Debounce = time.Second
	}
	if w.Resync == 0 {
		w.Resync = 15 * time.Second
	}
	w.last = map[string]model.Status{}
	trigger := make(chan struct{}, 1)
	kick := func() {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}
	go w.evaluator(ctx, trigger)
	kick()
	for ctx.Err() == nil {
		if err := w.stream(ctx, kick); err != nil && ctx.Err() == nil {
			slog.Warn("herdr event stream ended", "operation", "watch", "error", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
		}
	}
}

// stream subscribes to status changes of every agent pane. Pane topology
// changes end the stream so the subscription list is rebuilt.
func (w *Watcher) stream(ctx context.Context, kick func()) error {
	snap, err := w.Herdr.Snapshot(ctx)
	if err != nil {
		return err
	}
	subs := []map[string]any{
		{"type": "pane.created"},
		{"type": "pane.closed"},
		{"type": "pane.agent_detected"},
	}
	for _, p := range snap.Panes {
		subs = append(subs, map[string]any{"type": "pane.agent_status_changed", "pane_id": p.PaneID})
	}
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, err := w.Herdr.Subscribe(sctx, subs)
	if err != nil {
		return err
	}
	kick()
	for ev := range events {
		kick()
		switch ev.Event {
		case "pane.created", "pane.closed", "pane.agent_detected":
			return nil // resubscribe with the new pane set
		}
	}
	return errors.New("subscription closed")
}

func (w *Watcher) evaluator(ctx context.Context, trigger <-chan struct{}) {
	tick := time.NewTicker(w.Resync)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-trigger:
			select {
			case <-ctx.Done():
				return
			case <-time.After(w.Debounce):
			}
		case <-tick.C:
		}
		w.Evaluate(ctx)
	}
}

// Evaluate compares current statuses with the last seen ones.
func (w *Watcher) Evaluate(ctx context.Context) {
	sessions, err := w.Sessions.LiveSessions(ctx)
	if err != nil {
		slog.Warn("could not evaluate sessions", "operation", "watch.evaluate", "error", err)
		return
	}
	seen := map[string]bool{}
	for _, s := range sessions {
		seen[s.ID] = true
		w.mu.Lock()
		prev, known := w.last[s.ID]
		w.last[s.ID] = s.Status
		w.mu.Unlock()
		if !known || prev == s.Status {
			continue // first sighting after (re)start: no push
		}
		slog.Info("session status changed", "provider", s.Provider, "session_id", s.ID, "operation", "watch", "from", prev, "to", s.Status)
		if kind, ok := notificationKind(prev, s.Status); ok {
			w.push(ctx, s, kind)
		}
	}
	w.mu.Lock()
	for id := range w.last {
		if !seen[id] {
			delete(w.last, id)
		}
	}
	w.mu.Unlock()
}

// notificationKind decides whether a transition deserves a push. Herdr marks a
// finished agent idle instead of done once someone looked at it, so
// running -> idle also counts as completion.
func notificationKind(prev, next model.Status) (model.Status, bool) {
	switch next {
	case model.StatusWaitingInput, model.StatusWaitingApproval, model.StatusFailed:
		return next, true
	case model.StatusCompleted:
		return next, prev != model.StatusIdle
	case model.StatusIdle:
		return model.StatusCompleted, prev == model.StatusRunning
	}
	return "", false
}

func Title(s model.Session, kind model.Status) string {
	switch kind {
	case model.StatusWaitingInput:
		return s.ProviderName + " needs input"
	case model.StatusWaitingApproval:
		return s.ProviderName + " needs approval"
	case model.StatusFailed:
		return s.ProviderName + " failed"
	default:
		return s.ProviderName + " completed"
	}
}

func Payload(s model.Session, kind model.Status) map[string]string {
	body := s.Project
	if s.LastMessage != "" {
		body = strings.TrimSpace(body + "\n" + s.LastMessage)
	}
	return map[string]string{
		"sessionId": s.ID,
		"status":    string(kind),
		"title":     Title(s, kind),
		"body":      truncate(body, 300),
		"provider":  s.Provider,
		"project":   s.Project,
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (w *Watcher) push(ctx context.Context, s model.Session, kind model.Status) {
	if w.Sender == nil {
		slog.Info("push skipped: FCM not configured", "provider", s.Provider, "session_id", s.ID, "operation", "push")
		return
	}
	data := Payload(s, kind)
	for _, d := range w.Config.Get().Devices {
		err := w.Sender.Send(ctx, d.FCMToken, data)
		switch {
		case err == nil:
			slog.Info("push sent", "provider", s.Provider, "session_id", s.ID, "operation", "push", "device", d.Name, "status", kind)
		case errors.Is(err, ErrTokenInvalid):
			slog.Warn("removing invalid device token", "operation", "push", "device", d.Name, "error", err)
			w.Config.RemoveDeviceToken(d.FCMToken)
		default:
			slog.Error("push failed", "provider", s.Provider, "session_id", s.ID, "operation", "push", "device", d.Name, "error", err)
			w.Sink.Record(s.Provider, s.ID, deadletter.SendError, fmt.Sprintf("fcm: %v", err), data)
		}
	}
}
