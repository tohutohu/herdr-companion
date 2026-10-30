package resident

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// ConversationKeeper is implemented by runners whose runs leave the agent's
// conversation behind (a Claude transcript, a Codex rollout), as sessions
// do. It is kept, hidden from the session list until promoted.
type ConversationKeeper interface {
	// Rename sets the title the session list shows for the conversation.
	Rename(ctx context.Context, nativeID, title string) error
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
// named after the task and listed among the sessions. The caller resumes it. Promoting a run again returns the same session.
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
		return nil, invalid("this run kept no conversation to continue (runs from before the gateway kept them)")
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
	res, err := s.store.db.ExecContext(ctx, `UPDATE runs SET conversation = 'promoted', session_id = ? WHERE id = ? AND conversation = 'kept'`, p.SessionID, r.ID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return p, nil // promoted by a concurrent request
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
