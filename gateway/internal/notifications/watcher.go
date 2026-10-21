package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
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

// Conversations reads a session's messages for the report sent with a push.
type Conversations interface {
	Conversation(ctx context.Context, id string) (model.Session, []model.Message, error)
}

// Subscriber is the Herdr event stream.
type Subscriber interface {
	Snapshot(ctx context.Context) (*herdr.Snapshot, error)
	Subscribe(ctx context.Context, subs []map[string]any) (<-chan herdr.Event, error)
}

// Watcher notices state transitions of live sessions and pushes them.
// Statuses and finished turn revisions are kept in memory per session.
type Watcher struct {
	Herdr    Subscriber
	Sessions Sessions
	// Conversations supplies the last report sent with a push; nil sends none.
	Conversations Conversations
	Sender        Sender // nil disables sending (transitions are still logged)
	Config        *config.Store
	Sink          deadletter.Sink

	// Debounce lets providers flush transcripts before we read them.
	Debounce time.Duration
	// Resync re-evaluates everything periodically (covers missed events).
	Resync time.Duration

	mu   sync.Mutex
	last map[string]observation
}

type observation struct {
	status             model.Status
	completionRevision string
}

func (w *Watcher) Run(ctx context.Context) {
	if w.Debounce == 0 {
		w.Debounce = time.Second
	}
	if w.Resync == 0 {
		w.Resync = 15 * time.Second
	}
	w.last = map[string]observation{}
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
		// Topology events use the general event envelope (underscores),
		// unlike the pane status subscription envelope (dots).
		case "pane_created", "pane_closed", "pane_agent_detected",
			"pane.created", "pane.closed", "pane.agent_detected":
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
		next := observation{status: s.Status, completionRevision: prev.completionRevision}
		// Seed from the first read, including an old finished turn in a pane
		// Herdr temporarily calls running. Later running observations must not
		// consume a new completion before Herdr reports its terminal state.
		if s.CompletionRevision != "" && (!known || s.Status == model.StatusCompleted || s.Status == model.StatusIdle || s.Status == model.StatusFailed) {
			next.completionRevision = s.CompletionRevision
		}
		w.last[s.ID] = next
		w.mu.Unlock()
		if !known {
			continue // first sighting after (re)start: no push
		}
		var kind model.Status
		var notify bool
		if prev.status != s.Status {
			slog.Info("session status changed", "provider", s.Provider, "session_id", s.ID, "operation", "watch", "from", prev.status, "to", s.Status)
			kind, notify = notificationKind(prev.status, s.Status)
		}
		// A whole turn can happen between reads, or its transcript can flush
		// after the terminal status was observed. Do not consume that new
		// revision without notifying just because the status stayed the same.
		if next.completionRevision != prev.completionRevision && prev.completionRevision != "" {
			switch s.Status {
			case model.StatusCompleted, model.StatusIdle:
				kind, notify = model.StatusCompleted, true
			case model.StatusFailed:
				kind, notify = model.StatusFailed, true
			}
		}
		if notify {
			if (kind == model.StatusCompleted || kind == model.StatusFailed) && s.CompletionRevision != "" && s.CompletionRevision == prev.completionRevision {
				slog.Info("push skipped: completion already observed", "provider", s.Provider, "session_id", s.ID, "operation", "push", "completion_revision", s.CompletionRevision)
				continue
			}
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
	return map[string]string{
		"sessionId":    s.ID,
		"status":       string(kind),
		"title":        Title(s, kind),
		"body":         truncate(Body(s), 300),
		"provider":     s.Provider,
		"providerName": s.ProviderName,
		"project":      s.Project,
		// Lets Android offer a reply field only where a message would arrive.
		"canSend": strconv.FormatBool(s.CanSend),
	}
}

// Body names the session on its first line (which is all a collapsed
// notification shows), then quotes what the agent said last. The title says
// what the session is about; the project only stands in until there is one.
func Body(s model.Session) string {
	head := s.Title
	if head == "" {
		head = s.Project
	}
	if s.LastMessage == "" {
		return head
	}
	return strings.TrimSpace(head + "\n" + s.LastMessage)
}

// KeyReport carries the agent's last report, so the app can show it before
// it reaches the Gateway to fetch the conversation.
const KeyReport = "report"

// maxPayloadBytes keeps a push under FCM's 4096-byte limit on data keys
// and values, with room to spare.
const maxPayloadBytes = 3800

// reportTimeout bounds reading the conversation; a slow read sends the push
// without its report rather than late.
const reportTimeout = 5 * time.Second

// LastReport is the newest assistant message with text, reduced to that text:
// images, files and interactions need the Gateway anyway.
func LastReport(msgs []model.Message) (model.Message, bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != model.RoleAssistant || m.Queued {
			continue
		}
		var parts []string
		for _, b := range m.Blocks {
			if b.Type == model.BlockText && strings.TrimSpace(b.Text) != "" {
				parts = append(parts, strings.TrimSpace(b.Text))
			}
		}
		if len(parts) > 0 {
			return model.Message{ID: m.ID, Role: m.Role, Timestamp: m.Timestamp, Blocks: []model.Block{model.TextBlock(strings.Join(parts, "\n\n"))}}, true
		}
	}
	return model.Message{}, false
}

// AttachReport adds the report as message JSON, cutting its text to what fits
// next to the rest of data. It reports false when not even a start fits.
func AttachReport(data map[string]string, report model.Message) bool {
	budget := maxPayloadBytes - len(KeyReport)
	for k, v := range data {
		budget -= len(k) + len(v)
	}
	full := []rune(report.Blocks[0].Text)
	for keep := len(full); keep > 0; {
		text := string(full[:keep])
		if keep < len(full) {
			text = strings.TrimRight(text, " \t\n") + "…"
		}
		report.Blocks = []model.Block{model.TextBlock(text)}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(report); err != nil {
			return false
		}
		encoded := bytes.TrimSpace(buf.Bytes())
		if len(encoded) <= budget {
			data[KeyReport] = string(encoded)
			return true
		}
		// Escapes and multi-byte runes make bytes per rune uneven, so shrink
		// in proportion and always by at least one rune.
		keep = min(keep-1, keep*budget/len(encoded))
	}
	return false
}

func (w *Watcher) attachReport(ctx context.Context, s model.Session, data map[string]string) {
	if w.Conversations == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()
	_, msgs, err := w.Conversations.Conversation(ctx, s.ID)
	if err != nil {
		slog.Warn("push sent without report", "provider", s.Provider, "session_id", s.ID, "operation", "push", "error", err)
		return
	}
	if report, ok := LastReport(msgs); ok {
		AttachReport(data, report)
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
	data["gatewayId"] = config.GatewayID(w.Config.Get().AuthToken)
	w.attachReport(ctx, s, data)
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
