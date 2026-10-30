package resident

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// ConversationKeeper is implemented by runners whose runs leave the agent's
// conversation behind (a Claude transcript, a Codex rollout). A kept
// conversation can become an ordinary session; the others are removed.
type ConversationKeeper interface {
	// Rename sets the title the session list shows for the conversation.
	Rename(ctx context.Context, nativeID, title string) error
	// Discard removes the conversation. One already gone is not an error.
	Discard(ctx context.Context, nativeID string) error
}

// Promotion is a run's conversation made a session.
type Promotion struct {
	// SessionID is the gateway session id ("claude:<id>", "codex:<thread>").
	SessionID string `json:"sessionId"`
	Provider  string `json:"provider"`
	NativeID  string `json:"nativeId"`
	Cwd       string `json:"cwd"`
}

// Promote makes a finished run's conversation an ordinary session: it is
// named after the task, listed among the sessions and never discarded. The
// caller resumes it. Promoting a run again returns the same session.
func (s *Service) Promote(ctx context.Context, runID string) (*Promotion, error) {
	r, err := s.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	t, err := s.loadTask(ctx, r.TaskID)
	if err != nil {
		return nil, err
	}
	switch {
	case r.Status == RunRunning || r.Status == RunQueued:
		return nil, fmt.Errorf("%w: the run has not finished yet", ErrConflict)
	case r.Conversation != ConversationKept && r.Conversation != ConversationPromoted:
		return nil, invalid("this run kept no conversation to continue (only runs that report something keep theirs, for %d days)", int(s.cfg.KeepConversations/(24*time.Hour)))
	}
	var native, provider string
	for i := len(r.Attempts) - 1; i >= 0 && native == ""; i-- {
		native, provider = r.Attempts[i].NativeID, r.Attempts[i].Provider
	}
	if native == "" {
		return nil, invalid("this run kept no conversation to continue")
	}
	p := &Promotion{SessionID: provider + ":" + native, Provider: provider, NativeID: native, Cwd: t.Cwd}
	if r.Conversation == ConversationPromoted {
		return p, nil
	}
	if k, ok := s.runners[provider].(ConversationKeeper); ok {
		if err := k.Rename(ctx, native, sessionTitle(t, r)); err != nil {
			// The session works without its title.
			slog.Warn("naming a promoted resident run failed", "operation", "resident.promote", "run_id", r.ID, "error", err)
		}
	}
	res, err := s.store.db.ExecContext(ctx, `UPDATE runs SET conversation = 'promoted', keep_until = NULL, session_id = ? WHERE id = ? AND conversation = 'kept'`, p.SessionID, r.ID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Discarded meanwhile, or promoted by a concurrent request.
		if again, err := s.GetRun(ctx, runID); err != nil || again.Conversation != ConversationPromoted {
			return nil, invalid("this run's conversation is no longer kept")
		}
		return p, nil
	}
	slog.Info("resident run promoted to a session", "operation", "resident.promote", "task_id", t.ID, "run_id", r.ID, "session_id", p.SessionID)
	s.changed()
	return p, nil
}

// sessionTitle names a promoted run: the task and when the run started.
func sessionTitle(t *Task, r *Run) string {
	at := r.QueuedAt
	if r.StartedAt != nil {
		at = *r.StartedAt
	}
	if loc, err := time.LoadLocation(t.Schedule.Timezone); err == nil && t.Schedule.Timezone != "" {
		at = at.In(loc)
	}
	return fmt.Sprintf("%s（常駐 %s）", t.Name, at.Format("01/02 15:04"))
}

// sweepBatch bounds the conversations removed per tick.
const sweepBatch = 20

// sweepConversations removes kept conversations that are due: past
// KeepConversations, of an unchanged check, or of a deleted task. A run is
// claimed ("discarding") before its conversation is removed, so it cannot
// be promoted meanwhile; a removal that fails is retried an hour later.
func (s *Service) sweepConversations(ctx context.Context, now time.Time) {
	rows, err := s.store.db.QueryContext(ctx, `SELECT r.id, a.provider, a.native_id FROM runs r
		JOIN attempts a ON a.run_id = r.id AND a.native_id <> ''
		LEFT JOIN tasks t ON t.id = r.task_id
		WHERE r.status NOT IN ('queued', 'running') AND (r.conversation = 'discarding'
			OR r.conversation = 'kept' AND (r.keep_until <= ? OR t.deleted_at IS NOT NULL))
		ORDER BY r.keep_until LIMIT ?`, ms(now), sweepBatch)
	if err != nil {
		slog.Warn("listing resident conversations to remove failed", "operation", "resident.sweep", "error", err)
		return
	}
	type due struct{ run, provider, native string }
	var list []due
	for rows.Next() {
		var d due
		if rows.Scan(&d.run, &d.provider, &d.native) == nil {
			list = append(list, d)
		}
	}
	rows.Close()
	for _, d := range list {
		k, ok := s.runners[d.provider].(ConversationKeeper)
		if !ok {
			continue // the runner saves nothing
		}
		if _, err := s.store.db.ExecContext(ctx, `UPDATE runs SET conversation = 'discarding' WHERE id = ? AND conversation IN ('kept', 'discarding')`, d.run); err != nil {
			continue
		}
		if err := k.Discard(ctx, d.native); err != nil && !errors.Is(err, providers.ErrNotFound) {
			slog.Warn("removing a resident run's conversation failed", "operation", "resident.sweep", "run_id", d.run, "provider", d.provider, "error", err)
			s.store.db.ExecContext(ctx, `UPDATE runs SET conversation = 'kept', keep_until = ? WHERE id = ?`, ms(now.Add(time.Hour)), d.run)
			continue
		}
		s.store.db.ExecContext(ctx, `UPDATE runs SET conversation = 'discarded', keep_until = NULL WHERE id = ?`, d.run)
	}
}
