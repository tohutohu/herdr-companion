package resident

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/files"
)

// Runner executes one resident run with a provider. Implementations start
// a fresh, unpersisted agent conversation for every call, never touch the
// session runtime, and end everything they started before returning.
type Runner interface {
	Run(ctx context.Context, req RunRequest, hooks RunHooks) (*RunOutput, error)
}

// RunRequest is what a runner needs.
type RunRequest struct {
	RunID       string
	AttemptID   string
	Cwd         string
	Model       string
	Effort      string
	Prompt      string
	Schema      json.RawMessage
	Permissions Permissions
}

// RunHooks let the service persist progress at the points that matter for
// recovery. BeforeSend runs after the agent is ready and before the prompt
// is sent; when it fails the runner must stop without sending.
type RunHooks struct {
	BeforeSend func(nativeID, model string) error
	Sent       func()
}

// RunOutput is what the agent produced.
type RunOutput struct {
	NativeID string `json:"nativeId,omitempty"`
	// Structured is the provider's structured output, when it has one.
	Structured json.RawMessage `json:"structured,omitempty"`
	// Text is the agent's final message.
	Text  string `json:"text,omitempty"`
	Model string `json:"model,omitempty"`
	Usage Usage  `json:"usage"`
	// PermissionDenials are tool calls the run's permissions refused.
	PermissionDenials []string `json:"permissionDenials,omitempty"`
	// Error is set when the agent's turn itself failed.
	Error string `json:"error,omitempty"`
}

var (
	errUserCancel  = errors.New("ユーザーが停止しました")
	errTaskDeleted = errors.New("タスクが削除されました")
	errShutdown    = errors.New("the gateway is shutting down")
	errRunTimeout  = errors.New("the run exceeded its time limit")
	errNotSent     = errors.New("the prompt was not sent")
)

type activeRun struct {
	taskID   string
	cancel   context.CancelCauseFunc
	deadline time.Time
	sent     bool
	done     chan struct{}
}

// --- queries ---

const runColumns = `id, task_id, status, triggers, reason, interactive, queued_at, scheduled_for, started_at, ended_at, outcome, summary, report,
	error_kind, error, settings, settings_version, memory_version, provider, model, effort, usage, warnings, notification_key, notify_decision, next_wake_at`

func scanRun(row scanner) (*Run, error) {
	var r Run
	var triggers string
	var queued int64
	var sched, started, ended, nextWake sql.NullInt64
	var settings, usage, warnings sql.NullString
	if err := row.Scan(&r.ID, &r.TaskID, &r.Status, &triggers, &r.Reason, &r.Interactive, &queued, &sched, &started, &ended, &r.Outcome, &r.Summary, &r.Report,
		&r.ErrorKind, &r.Error, &settings, &r.SettingsVersion, &r.MemoryVersion, &r.Provider, &r.Model, &r.Effort, &usage, &warnings, &r.NotificationKey, &r.NotifyDecision, &nextWake); err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	jsonUnmarshal(triggers, &r.Triggers)
	r.QueuedAt = fromMs(queued)
	r.ScheduledFor, r.StartedAt, r.EndedAt, r.NextWakeAt = fromNull(sched), fromNull(started), fromNull(ended), fromNull(nextWake)
	if settings.Valid {
		r.Settings = &Settings{}
		jsonUnmarshal(settings.String, r.Settings)
	}
	if usage.Valid {
		r.Usage = &Usage{}
		jsonUnmarshal(usage.String, r.Usage)
	}
	if warnings.Valid {
		jsonUnmarshal(warnings.String, &r.Warnings)
	}
	return &r, nil
}

func loadRunTx(tx *sql.Tx, id string) (*Run, error) {
	return scanRun(tx.QueryRow(`SELECT `+runColumns+` FROM runs WHERE id = ?`, id))
}

// Runs lists a task's runs, newest first.
func (s *Service) Runs(ctx context.Context, taskID, before string, limit int) ([]Run, bool, error) {
	if _, err := s.loadTask(ctx, taskID); err != nil {
		return nil, false, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	upper := int64(1 << 62)
	if before != "" {
		if err := s.store.db.QueryRowContext(ctx, `SELECT seq FROM runs WHERE id = ? AND task_id = ?`, before, taskID).Scan(&upper); err != nil {
			if isNoRows(err) {
				return nil, false, ErrNotFound
			}
			return nil, false, err
		}
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT `+runColumns+` FROM runs WHERE task_id = ? AND seq < ? ORDER BY seq DESC LIMIT ?`, taskID, upper, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, false, err
		}
		r.Settings = nil // the list stays small; GetRun has them
		out = append(out, *r)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, rows.Err()
}

// GetRun returns a run with its attempts, artifacts and question.
func (s *Service) GetRun(ctx context.Context, id string) (*Run, error) {
	r, err := scanRun(s.store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if _, err := s.loadTask(ctx, r.TaskID); err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT id, run_id, phase, provider, native_id, model, started_at, ended_at, error, raw_result IS NOT NULL FROM attempts WHERE run_id = ? ORDER BY started_at`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a Attempt
		var started int64
		var ended sql.NullInt64
		if err := rows.Scan(&a.ID, &a.RunID, &a.Phase, &a.Provider, &a.NativeID, &a.Model, &started, &ended, &a.Error, &a.HasResult); err != nil {
			rows.Close()
			return nil, err
		}
		a.StartedAt, a.EndedAt = fromMs(started), fromNull(ended)
		r.Attempts = append(r.Attempts, a)
	}
	rows.Close()
	arts, err := s.store.db.QueryContext(ctx, `SELECT id, task_id, run_id, path, title, created_at FROM artifacts WHERE run_id = ?`, id)
	if err != nil {
		return nil, err
	}
	for arts.Next() {
		var a Artifact
		var created int64
		arts.Scan(&a.ID, &a.TaskID, &a.RunID, &a.Path, &a.Title, &created)
		a.CreatedAt = fromMs(created)
		r.Artifacts = append(r.Artifacts, a)
	}
	arts.Close()
	q, err := scanQuestion(s.store.db.QueryRowContext(ctx, `SELECT id, task_id, run_id, text, options, status, created_at, expires_at, answer FROM questions WHERE run_id = ?`, id))
	if err == nil {
		r.Question = q
	}
	return r, nil
}

// RunNow queues a manual run.
func (s *Service) RunNow(ctx context.Context, taskID, requestID string) (*PostResult, error) {
	now := s.clock()
	var res *PostResult
	err := s.store.tx(ctx, func(tx *sql.Tx) error {
		var err error
		res, _, err = once(tx, requestID, "run:"+taskID, map[string]string{"task": taskID}, now, func() (*PostResult, error) {
			if _, err := loadTaskTx(tx, taskID); err != nil {
				return nil, err
			}
			run, merged, err := s.enqueueTx(tx, taskID, TriggerManual, "run now", nil, true, now)
			if err != nil {
				return nil, err
			}
			return &PostResult{Run: run, Merged: merged}, nil
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	s.changed()
	return res, nil
}

// CancelRun stops one run: a queued run is dropped, a running one is
// interrupted and its agent ended. Other runs, tasks and sessions are not
// touched.
func (s *Service) CancelRun(ctx context.Context, runID string) (*Run, error) {
	now := s.clock()
	var status string
	err := s.store.tx(ctx, func(tx *sql.Tx) error {
		r, err := loadRunTx(tx, runID)
		if err != nil {
			return err
		}
		if _, err := loadTaskTx(tx, r.TaskID); err != nil {
			return err
		}
		status = r.Status
		switch r.Status {
		case RunQueued:
			_, err := tx.Exec(`UPDATE runs SET status = 'cancelled', ended_at = ?, error = ? WHERE id = ?`, ms(now), errUserCancel.Error(), runID)
			return err
		case RunRunning:
			return nil
		case RunWaitingUser:
			if _, err := tx.Exec(`UPDATE questions SET status = 'superseded' WHERE run_id = ? AND status = 'open'`, runID); err != nil {
				return err
			}
			_, err := tx.Exec(`UPDATE runs SET status = 'cancelled', error = ? WHERE id = ?`, errUserCancel.Error(), runID)
			return err
		default:
			return fmt.Errorf("%w: the run is %s", ErrConflict, r.Status)
		}
	})
	if err != nil {
		return nil, err
	}
	if status == RunRunning {
		s.mu.Lock()
		a := s.active[runID]
		s.mu.Unlock()
		if a == nil {
			return nil, fmt.Errorf("%w: the run is finishing", ErrConflict)
		}
		a.cancel(errUserCancel)
		select {
		case <-a.done:
		case <-time.After(45 * time.Second):
		case <-ctx.Done():
		}
	}
	s.changed()
	return s.GetRun(ctx, runID)
}

// DismissRun closes a run whose outcome is unknown after the user checked
// it. Nothing is re-sent.
func (s *Service) DismissRun(ctx context.Context, runID string) (*Run, error) {
	err := s.store.tx(ctx, func(tx *sql.Tx) error {
		r, err := loadRunTx(tx, runID)
		if err != nil {
			return err
		}
		if r.Status != RunNeedsReconciliation {
			return fmt.Errorf("%w: only a run whose outcome is unknown can be dismissed (this one is %s)", ErrConflict, r.Status)
		}
		_, err = tx.Exec(`UPDATE runs SET status = 'dismissed' WHERE id = ?`, runID)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.changed()
	return s.GetRun(ctx, runID)
}

func (s *Service) cancelTaskRuns(taskID string, cause error) {
	s.mu.Lock()
	var list []*activeRun
	for _, a := range s.active {
		if a.taskID == taskID {
			list = append(list, a)
		}
	}
	s.mu.Unlock()
	for _, a := range list {
		a.cancel(cause)
	}
}

func (s *Service) enforceDeadlines(now time.Time) {
	s.mu.Lock()
	var late []*activeRun
	for _, a := range s.active {
		if now.After(a.deadline) {
			late = append(late, a)
		}
	}
	s.mu.Unlock()
	for _, a := range late {
		a.cancel(errRunTimeout)
	}
}

// --- execution ---

// dispatch starts queued runs while there is room.
func (s *Service) dispatch(ctx context.Context) {
	// One dispatcher at a time: the room check and the start must not
	// interleave, or two callers could each start a run into one free slot.
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	for {
		s.mu.Lock()
		if s.closing || len(s.active) >= s.cfg.MaxConcurrentRuns {
			s.mu.Unlock()
			return
		}
		busy := map[string]bool{}
		for _, a := range s.active {
			busy[a.taskID] = true
		}
		s.mu.Unlock()
		started, err := s.startNext(ctx, busy)
		if err != nil {
			slog.Error("resident dispatch failed", "operation", "resident.dispatch", "error", err)
			return
		}
		if !started {
			return
		}
	}
}

func (s *Service) startNext(ctx context.Context, busy map[string]bool) (bool, error) {
	now := s.clock()
	var run *Run
	var task *Task
	var attemptID string
	var prompt string
	err := s.store.tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT r.id, r.task_id FROM runs r JOIN tasks t ON t.id = r.task_id
			WHERE r.status = 'queued' AND t.deleted_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM runs x WHERE x.task_id = r.task_id AND x.status = 'running')
			ORDER BY r.interactive DESC, r.seq`)
		if err != nil {
			return err
		}
		var pick string
		for rows.Next() {
			var id, task string
			rows.Scan(&id, &task)
			if !busy[task] && pick == "" {
				pick = id
			}
		}
		rows.Close()
		if pick == "" {
			return nil
		}
		r, err := loadRunTx(tx, pick)
		if err != nil {
			return err
		}
		t, err := loadTaskTx(tx, r.TaskID)
		if err != nil {
			return err
		}
		mem, err := loadMemory(tx.QueryRow(`SELECT task_id, version, summary, state, updated_at, updated_by FROM memory WHERE task_id = ?`, t.ID))
		if err != nil {
			return err
		}
		// The run takes the user's messages not yet handled.
		if _, err := tx.Exec(`UPDATE messages SET handled_by = ? WHERE task_id = ? AND role = 'user' AND handled_by IS NULL`, r.ID, t.ID); err != nil {
			return err
		}
		pctx, err := s.promptContextTx(tx, t, r, mem, now)
		if err != nil {
			return err
		}
		prompt = BuildPrompt(pctx)
		deadline := now.Add(time.Duration(t.Limits.MaxRunMinutes) * time.Minute)
		if _, err := tx.Exec(`UPDATE runs SET status = 'running', started_at = ?, deadline_at = ?, settings = ?, settings_version = ?, memory_version = ?, memory_snapshot = ?,
			provider = ?, model = ?, effort = ? WHERE id = ?`,
			ms(now), ms(deadline), mustJSON(t.Settings), t.Version, mem.Version, mustJSON(mem), t.Provider, t.Model, t.Effort, r.ID); err != nil {
			return err
		}
		attemptID = newID("att")
		if _, err := tx.Exec(`INSERT INTO attempts (id, run_id, phase, provider, started_at) VALUES (?, ?, 'preparing', ?, ?)`, attemptID, r.ID, t.Provider, ms(now)); err != nil {
			return err
		}
		r.Status, r.StartedAt = RunRunning, &now
		run, task = r, t
		return nil
	})
	if err != nil || run == nil {
		return false, err
	}
	actx, cancel := context.WithCancelCause(context.Background())
	a := &activeRun{taskID: task.ID, cancel: cancel, deadline: now.Add(time.Duration(task.Limits.MaxRunMinutes) * time.Minute), done: make(chan struct{})}
	s.mu.Lock()
	s.active[run.ID] = a
	s.wg.Add(1)
	s.mu.Unlock()
	slog.Info("resident run started", "operation", "resident.run", "task_id", task.ID, "run_id", run.ID, "provider", task.Provider, "triggers", strings.Join(run.Triggers, ","))
	s.changed()
	go s.execute(actx, a, run, task, attemptID, prompt)
	return true, nil
}

func (s *Service) execute(ctx context.Context, a *activeRun, run *Run, task *Task, attemptID, prompt string) {
	defer func() {
		s.mu.Lock()
		delete(s.active, run.ID)
		s.mu.Unlock()
		close(a.done)
		s.wg.Done()
		s.changed()
	}()
	tctx, cancel := context.WithTimeoutCause(ctx, time.Duration(task.Limits.MaxRunMinutes)*time.Minute, errRunTimeout)
	defer cancel()
	runner := s.runners[task.Provider]
	var out *RunOutput
	var err error
	switch {
	case runner == nil:
		err = fmt.Errorf("%w: no runner for %s", errNotSent, task.Provider)
	default:
		if cerr := s.checkRunnable(task); cerr != nil {
			err = fmt.Errorf("%w: %v", errNotSent, cerr)
			break
		}
		out, err = runner.Run(tctx, RunRequest{
			RunID: run.ID, AttemptID: attemptID, Cwd: task.Cwd, Model: task.Model, Effort: task.Effort,
			Prompt: prompt, Schema: ResultSchema, Permissions: task.Permissions,
		}, RunHooks{
			BeforeSend: func(nativeID, model string) error {
				if cause := context.Cause(tctx); cause != nil {
					return cause
				}
				_, err := s.store.db.Exec(`UPDATE attempts SET phase = 'sending', native_id = ?, model = ? WHERE id = ? AND phase = 'preparing'`, nativeID, model, attemptID)
				if err == nil {
					s.mu.Lock()
					a.sent = true
					s.mu.Unlock()
				}
				return err
			},
			Sent: func() {
				s.store.db.Exec(`UPDATE attempts SET phase = 'running' WHERE id = ? AND phase = 'sending'`, attemptID)
			},
		})
	}
	cause := context.Cause(tctx)
	now := s.clock()
	s.mu.Lock()
	sent := a.sent
	s.mu.Unlock()
	switch {
	case err == nil && out != nil:
		// A complete answer counts even when a stop came in just after it.
		// Keep the raw result first: if finalizing fails or the gateway
		// stops now, the next start finalizes from it.
		if _, err := s.store.db.Exec(`UPDATE attempts SET phase = 'finished', ended_at = ?, native_id = COALESCE(NULLIF(?, ''), native_id), raw_result = ? WHERE id = ?`,
			ms(now), out.NativeID, mustJSON(out), attemptID); err != nil {
			slog.Error("saving resident result failed", "operation", "resident.run", "run_id", run.ID, "error", err)
		}
		s.finalize(context.Background(), run.ID, attemptID, out, now)
	case errors.Is(cause, errShutdown):
		// The gateway is stopping: what happened is decided on the next
		// start (Recover), from what was persisted.
		slog.Warn("resident run cut off by shutdown", "operation", "resident.run", "run_id", run.ID)
	default:
		kind, status, msg := ErrKindProvider, RunFailed, ""
		if err != nil {
			msg = err.Error()
		}
		switch {
		case errors.Is(cause, errUserCancel):
			status, kind, msg = RunCancelled, "", errUserCancel.Error()
		case errors.Is(cause, errTaskDeleted):
			status, kind, msg = RunCancelled, "", errTaskDeleted.Error()
		case errors.Is(cause, errRunTimeout):
			status, kind, msg = RunTimedOut, ErrKindTimeout, fmt.Sprintf("1回の上限 %d 分を超えたため停止しました", task.Limits.MaxRunMinutes)
		case errors.Is(err, errNotSent) || !sent:
			kind = ErrKindConfig
		}
		phase := PhaseFailed
		if status == RunCancelled {
			phase = PhaseCancelled
		}
		s.store.db.Exec(`UPDATE attempts SET phase = ?, ended_at = ?, error = ? WHERE id = ?`, phase, ms(now), msg, attemptID)
		s.fail(context.Background(), run.ID, status, kind, msg, now)
	}
}

// checkRunnable re-checks the folder before a run: it may have been removed
// or moved out of the workspace roots since the task was saved.
func (s *Service) checkRunnable(t *Task) error {
	if s.env == nil {
		return nil
	}
	cwd, err := s.env.ResolveDir(t.Cwd)
	if err != nil {
		return fmt.Errorf("the folder %s cannot be used: %v", t.Cwd, err)
	}
	if cwd != t.Cwd {
		return fmt.Errorf("the folder %s now resolves to %s; save the task again", t.Cwd, cwd)
	}
	if t.TrustedAt == nil && !s.env.FolderTrusted(t.Provider, cwd) {
		return fmt.Errorf("the folder %s is not trusted for %s", cwd, t.Provider)
	}
	return nil
}

// fail ends a run that produced no result.
func (s *Service) fail(ctx context.Context, runID, status, kind, msg string, now time.Time) {
	err := s.store.tx(ctx, func(tx *sql.Tx) error {
		r, err := loadRunTx(tx, runID)
		if err != nil || (r.Status != RunRunning && r.Status != RunQueued) {
			return err
		}
		if _, err := tx.Exec(`UPDATE runs SET status = ?, ended_at = ?, error_kind = ?, error = ? WHERE id = ?`, status, ms(now), kind, truncateBytes(msg, 4000), runID); err != nil {
			return err
		}
		r.Status, r.ErrorKind, r.Error = status, kind, msg
		t, err := loadTaskTx(tx, r.TaskID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if status == RunCancelled {
			// Stopped on purpose: shown in the history, not reported.
			return nil
		}
		text := failureText(status, kind, msg)
		if err := insertMessage(tx, &Message{TaskID: t.ID, Role: RoleAssistant, Kind: MsgNotice, Text: text, RunID: runID, CreatedAt: now, Outcome: OutcomeFailed}); err != nil {
			return err
		}
		return s.notifyFailureTx(tx, t, r, text, now)
	})
	if err != nil {
		slog.Error("recording resident failure failed", "operation", "resident.run", "run_id", runID, "error", err)
	}
	slog.Warn("resident run ended without a result", "operation", "resident.run", "run_id", runID, "status", status, "kind", kind, "error", msg)
}

func failureText(status, kind, msg string) string {
	switch status {
	case RunTimedOut:
		return "実行が時間制限を超えたため停止しました: " + msg
	case RunNeedsReconciliation:
		return "実行が途中で中断され、外部操作の成否を確認できません。内容を確認してから再実行してください: " + msg
	}
	switch kind {
	case ErrKindContract:
		return "実行結果が結果契約を満たしませんでした（成功扱いにしていません）: " + msg
	case ErrKindConfig:
		return "設定のため実行できませんでした: " + msg
	case ErrKindPermission:
		return "必要な権限がないため完了できませんでした: " + msg
	}
	return "実行に失敗しました: " + msg
}

// finalize records a run's result: run status, memory, conversation,
// question, artifacts, self wake-up and notification outbox, in one
// transaction.
func (s *Service) finalize(ctx context.Context, runID, attemptID string, out *RunOutput, now time.Time) {
	var outcome string
	err := s.store.tx(ctx, func(tx *sql.Tx) error {
		r, err := loadRunTx(tx, runID)
		if err != nil {
			return err
		}
		if r.Status != RunRunning {
			return nil // already decided (cancelled meanwhile)
		}
		var snapshot sql.NullString
		tx.QueryRow(`SELECT memory_snapshot FROM runs WHERE id = ?`, runID).Scan(&snapshot)
		t, terr := loadTaskTx(tx, r.TaskID)
		deleted := errors.Is(terr, ErrNotFound)
		if terr != nil && !deleted {
			return terr
		}
		usage := mustJSON(out.Usage)
		if _, err := tx.Exec(`UPDATE runs SET usage = ?, model = COALESCE(NULLIF(?, ''), model) WHERE id = ?`, usage, out.Model, runID); err != nil {
			return err
		}
		raw := out.Structured
		if len(raw) == 0 || string(raw) == "null" {
			raw = json.RawMessage(out.Text)
		}
		res, perr := ParseResult(raw)
		if out.Error != "" && perr != nil {
			perr = fmt.Errorf("%s (and no valid result: %v)", out.Error, perr)
		}
		if perr != nil {
			kind := ErrKindContract
			if out.Error != "" {
				kind = ErrKindProvider
			}
			outcome = "error"
			return s.failTx(tx, t, r, kind, perr.Error(), now)
		}
		outcome = res.Outcome
		var warnings []string
		if len(out.PermissionDenials) > 0 {
			warnings = append(warnings, "denied by the task's permissions: "+strings.Join(out.PermissionDenials, ", "))
		}
		status := RunCompleted
		errKind, errMsg := "", ""
		switch res.Outcome {
		case OutcomeFailed:
			status, errKind, errMsg = RunFailed, ErrKindAgent, res.Summary
		case OutcomeUnchanged:
			if len(out.PermissionDenials) > 0 {
				// Not checking because a permission was missing is not "unchanged".
				status, errKind, errMsg = RunFailed, ErrKindPermission, "the run reported no change, but tools it needed were denied: "+strings.Join(out.PermissionDenials, ", ")
			}
		case OutcomeBlocked:
			if res.Question != nil {
				status = RunWaitingUser
			}
		}
		checkpoint := ""
		if res.Outcome == OutcomeBlocked {
			checkpoint = truncateBytes(strings.TrimSpace(res.Summary+"\n\n"+res.Report), 16<<10)
		}
		if _, err := tx.Exec(`UPDATE runs SET status = ?, ended_at = ?, outcome = ?, summary = ?, report = ?, error_kind = ?, error = ?, checkpoint = ? WHERE id = ?`,
			status, ms(now), res.Outcome, res.Summary, res.Report, errKind, errMsg, checkpoint, runID); err != nil {
			return err
		}
		r.Status, r.Outcome, r.Summary, r.ErrorKind, r.Error = status, res.Outcome, res.Summary, errKind, errMsg
		if deleted {
			return nil
		}
		// Memory: only a successful check moves it forward. A failed or
		// blocked run keeps the last good state.
		if status == RunCompleted && (res.Outcome == OutcomeChanged || res.Outcome == OutcomeUnchanged) {
			w, err := s.applyMemoryTx(tx, t.ID, runID, snapshot.String, res, now)
			if err != nil {
				return err
			}
			warnings = append(warnings, w...)
			// A success ends a failure streak.
			tx.Exec(`DELETE FROM notified_keys WHERE task_id = ? AND key = 'failure-streak'`, t.ID)
		}
		// Self wake-up: the newest result decides.
		if res.NextWakeAt != nil && strings.TrimSpace(*res.NextWakeAt) != "" {
			at, _ := time.Parse(time.RFC3339, strings.TrimSpace(*res.NextWakeAt))
			w, err := s.setSelfWakeTx(tx, t, runID, at.UTC(), res.Summary, now)
			if err != nil {
				return err
			}
			if w != "" {
				warnings = append(warnings, w)
			} else {
				tx.Exec(`UPDATE runs SET next_wake_at = ? WHERE id = ?`, ms(at), runID)
			}
		} else if status != RunFailed {
			tx.Exec(`UPDATE wakeups SET status = 'cancelled', reason = 'the latest run asked for no recheck' WHERE task_id = ? AND kind = 'self' AND status = 'pending'`, t.ID)
		}
		// Artifacts must stay inside the task folder.
		for _, art := range res.Artifacts {
			p, err := s.checkArtifact(t.Cwd, art.Path)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("artifact %q ignored: %v", art.Path, err))
				continue
			}
			tx.Exec(`INSERT INTO artifacts (id, task_id, run_id, path, title, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
				newID("art"), t.ID, runID, p, truncateBytes(art.Title, 200), ms(now))
		}
		// Conversation.
		var qid string
		if res.Question != nil && status == RunWaitingUser {
			tx.Exec(`UPDATE questions SET status = 'superseded' WHERE task_id = ? AND status = 'open'`, t.ID)
			qid = newID("q")
			if _, err := tx.Exec(`INSERT INTO questions (id, task_id, run_id, text, options, status, created_at, expires_at) VALUES (?, ?, ?, ?, ?, 'open', ?, ?)`,
				qid, t.ID, runID, res.Question.Text, mustJSON(nonNil(res.Question.Options)), ms(now), ms(now.Add(s.cfg.QuestionTTL))); err != nil {
				return err
			}
		}
		report := strings.TrimSpace(res.Report)
		if report == "" {
			report = res.Summary
		}
		show := r.Interactive || res.Outcome != OutcomeUnchanged || status != RunCompleted
		if show {
			kind := MsgReport
			if status == RunFailed {
				kind = MsgNotice
				report = failureText(status, errKind, errMsg) + "\n\n" + report
			}
			if err := insertMessage(tx, &Message{TaskID: t.ID, Role: RoleAssistant, Kind: kind, Text: report, RunID: runID, Outcome: res.Outcome, CreatedAt: now}); err != nil {
				return err
			}
			if qid != "" {
				text := res.Question.Text
				if len(res.Question.Options) > 0 {
					text += "\n\n- " + strings.Join(res.Question.Options, "\n- ")
				}
				if err := insertMessage(tx, &Message{TaskID: t.ID, Role: RoleAssistant, Kind: MsgQuestion, Text: text, RunID: runID, QuestionID: qid, CreatedAt: now}); err != nil {
					return err
				}
			}
		}
		if len(warnings) > 0 {
			tx.Exec(`UPDATE runs SET warnings = ? WHERE id = ?`, mustJSON(warnings), runID)
		}
		// Notifications, decided here and delivered from the outbox.
		switch {
		case status == RunWaitingUser:
			return s.notifyQuestionTx(tx, t, r, qid, res.Question.Text, now)
		case status == RunFailed || res.Outcome == OutcomeBlocked:
			return s.notifyFailureTx(tx, t, r, res.Summary, now)
		case res.Outcome == OutcomeChanged:
			key := ""
			if res.Notification.Key != nil {
				key = *res.Notification.Key
			}
			return s.notifyChangeTx(tx, t, r, res.Notification.Requested, key, res.Summary, now)
		}
		return nil
	})
	if err != nil {
		slog.Error("finalizing resident run failed", "operation", "resident.finalize", "run_id", runID, "error", err)
		return
	}
	slog.Info("resident run finished", "operation", "resident.run", "run_id", runID, "outcome", outcome)
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// failTx records a run without a valid result inside finalize.
func (s *Service) failTx(tx *sql.Tx, t *Task, r *Run, kind, msg string, now time.Time) error {
	if _, err := tx.Exec(`UPDATE runs SET status = 'failed', ended_at = ?, error_kind = ?, error = ? WHERE id = ?`, ms(now), kind, truncateBytes(msg, 4000), r.ID); err != nil {
		return err
	}
	r.Status, r.ErrorKind, r.Error = RunFailed, kind, msg
	if t == nil {
		return nil
	}
	text := failureText(RunFailed, kind, msg)
	if err := insertMessage(tx, &Message{TaskID: t.ID, Role: RoleAssistant, Kind: MsgNotice, Text: text, RunID: r.ID, Outcome: OutcomeFailed, CreatedAt: now}); err != nil {
		return err
	}
	return s.notifyFailureTx(tx, t, r, text, now)
}

// applyMemoryTx applies a result to the task's memory. Keys and the
// summary the user changed while the run ran keep the user's version.
func (s *Service) applyMemoryTx(tx *sql.Tx, taskID, runID, snapshotJSON string, res *Result, now time.Time) ([]string, error) {
	cur, err := loadMemory(tx.QueryRow(`SELECT task_id, version, summary, state, updated_at, updated_by FROM memory WHERE task_id = ?`, taskID))
	if err != nil {
		return nil, err
	}
	var snap Memory
	jsonUnmarshal(snapshotJSON, &snap)
	locked := map[string]bool{}
	summaryLocked := false
	if cur.Version != snap.Version {
		for k, v := range cur.State {
			if string(snap.State[k]) != string(v) {
				locked[k] = true
			}
		}
		for k := range snap.State {
			if _, ok := cur.State[k]; !ok {
				locked[k] = true
			}
		}
		summaryLocked = cur.Summary != snap.Summary
	}
	var warnings []string
	state, skipped, err := ApplyPatch(cur.State, res.StatePatch, locked)
	if err != nil {
		warnings = append(warnings, "statePatch rejected, memory kept: "+err.Error())
		state = cur.State
	} else if len(skipped) > 0 {
		warnings = append(warnings, "kept your edits of: "+strings.Join(skipped, ", "))
	}
	summary := cur.Summary
	if res.MemorySummary != nil {
		if summaryLocked {
			warnings = append(warnings, "kept your edit of the memory summary")
		} else {
			fit, cut := FitSummary(*res.MemorySummary)
			if cut {
				warnings = append(warnings, fmt.Sprintf("memory summary cut to %d bytes", MaxSummaryBytes))
			}
			summary = fit
		}
	}
	if summary == cur.Summary && mustJSON(state) == mustJSON(cur.State) {
		return warnings, nil
	}
	_, err = tx.Exec(`UPDATE memory SET version = version + 1, summary = ?, state = ?, updated_at = ?, updated_by = ? WHERE task_id = ?`,
		summary, mustJSON(state), ms(now), runID, taskID)
	return warnings, err
}

// checkArtifact resolves an artifact path the agent reported. It must be a
// file inside the task folder (symlinks resolved); anything else is
// refused, so a model cannot point the download API elsewhere.
func (s *Service) checkArtifact(cwd, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("empty path")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	real, err := files.Resolve([]string{cwd}, p)
	if err != nil {
		return "", err
	}
	return real, nil
}

// --- recovery ---

// Recover runs once at start, before the scheduler: it decides what became
// of runs that were in progress when the gateway stopped. Nothing is sent
// to an agent again on its own:
//
//   - nothing sent yet (preparing): the run is queued again;
//   - a saved result: the run is finalized from it;
//   - otherwise the outcome is unknown: needs_reconciliation.
func (s *Service) Recover(ctx context.Context) error {
	now := s.clock()
	rows, err := s.store.db.QueryContext(ctx, `SELECT id FROM runs WHERE status = 'running'`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		var attemptID, phase string
		var raw sql.NullString
		err := s.store.db.QueryRowContext(ctx, `SELECT id, phase, raw_result FROM attempts WHERE run_id = ? ORDER BY started_at DESC LIMIT 1`, id).Scan(&attemptID, &phase, &raw)
		switch {
		case isNoRows(err) || (err == nil && phase == PhasePreparing):
			err = s.store.tx(ctx, func(tx *sql.Tx) error {
				if attemptID != "" {
					tx.Exec(`UPDATE attempts SET phase = 'abandoned', ended_at = ?, error = 'the gateway stopped before the prompt was sent' WHERE id = ?`, ms(now), attemptID)
				}
				tx.Exec(`UPDATE messages SET handled_by = NULL WHERE handled_by = ?`, id)
				_, err := tx.Exec(`UPDATE runs SET status = 'queued', started_at = NULL, deadline_at = NULL WHERE id = ?`, id)
				return err
			})
			slog.Info("resident run queued again: nothing was sent before the restart", "operation", "resident.recover", "run_id", id)
		case err != nil:
			return err
		case raw.Valid:
			var out RunOutput
			jsonUnmarshal(raw.String, &out)
			s.finalize(ctx, id, attemptID, &out, now)
			slog.Info("resident run finalized from its saved result", "operation", "resident.recover", "run_id", id)
		default:
			s.store.db.ExecContext(ctx, `UPDATE attempts SET phase = 'unknown', ended_at = ?, error = 'the gateway stopped during the run' WHERE id = ?`, ms(now), attemptID)
			s.fail(ctx, id, RunNeedsReconciliation, ErrKindInterrupted,
				"エージェントの作業中にGatewayが停止しました（"+phase+"）。操作が完了したかは分かりません", now)
			slog.Warn("resident run needs reconciliation", "operation", "resident.recover", "run_id", id)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Shutdown stops the running runs' agents and waits for them. Their runs
// stay "running" in the database for Recover to decide on.
func (s *Service) Shutdown(timeout time.Duration) {
	s.mu.Lock()
	s.closing = true
	for _, a := range s.active {
		a.cancel(errShutdown)
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// ActiveRuns reports the run ids being executed.
func (s *Service) ActiveRuns() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for id := range s.active {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

func jsonUnmarshal(s string, v any) { json.Unmarshal([]byte(s), v) }

func contains(list []string, v string) bool { return slices.Contains(list, v) }

func hashKey(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
