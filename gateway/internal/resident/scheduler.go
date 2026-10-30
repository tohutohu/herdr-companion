package resident

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// Scheduling rules (see docs/resident.md):
//
//   - An enabled task with a schedule has exactly one pending schedule
//     wake-up: its next occurrence. Editing, pausing or deleting the task
//     cancels it and computes a new one from the new settings, so an old
//     wake-up never starts a run.
//   - At most one pending self wake-up per task, set by the agent's
//     nextWakeAt; a newer one replaces it.
//   - A due wake-up queues a run, or joins the task's run that is already
//     queued and not started (one queued run per task at most).
//   - Occurrences missed while the gateway was stopped or the Mac slept:
//     the newest one within CatchUpWindow runs once; the others are
//     recorded as one skipped run.
//   - Automatic starts beyond the task's MaxRunsPerDay are recorded as
//     skipped (daily_limit).
//   - A timed pause ends at its time: the task resumes and its schedule
//     continues from that time, so an occurrence between the end of the
//     pause and a late tick (the Mac slept) is caught up like any other.
//   - No model is involved in any of this: wake-ups are plain timestamps.

// rescheduleTx brings a task's pending wake-ups in line with its settings.
func (s *Service) rescheduleTx(tx *sql.Tx, taskID string, now time.Time) error {
	t, err := loadTaskTx(tx, taskID)
	if err != nil {
		return err
	}
	if !t.Enabled {
		_, err := tx.Exec(`UPDATE wakeups SET status = 'cancelled', reason = 'task paused' WHERE task_id = ? AND status = 'pending'`, taskID)
		return err
	}
	// Schedule wake-ups of an older version never fire.
	if _, err := tx.Exec(`UPDATE wakeups SET status = 'cancelled', reason = 'schedule changed' WHERE task_id = ? AND kind = 'schedule' AND status = 'pending' AND settings_version <> ?`, taskID, t.Version); err != nil {
		return err
	}
	// A self wake-up survives edits, but not past the task's end.
	if t.Limits.ExpiresAt != nil {
		if _, err := tx.Exec(`UPDATE wakeups SET status = 'cancelled', reason = 'after the task ends' WHERE task_id = ? AND kind = 'self' AND status = 'pending' AND due_at > ?`, taskID, ms(*t.Limits.ExpiresAt)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE wakeups SET settings_version = ? WHERE task_id = ? AND kind = 'self' AND status = 'pending'`, t.Version, taskID); err != nil {
		return err
	}
	var pending int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM wakeups WHERE task_id = ? AND kind = 'schedule' AND status = 'pending'`, taskID).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return nil
	}
	return s.addScheduleWakeTx(tx, t, now)
}

// addScheduleWakeTx adds the next schedule occurrence after after.
func (s *Service) addScheduleWakeTx(tx *sql.Tx, t *Task, after time.Time) error {
	next, ok := t.Schedule.Next(after)
	if !ok {
		return nil
	}
	if t.Limits.ExpiresAt != nil && next.After(*t.Limits.ExpiresAt) {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO wakeups (id, task_id, kind, due_at, status, settings_version, created_at) VALUES (?, ?, 'schedule', ?, 'pending', ?, ?)`,
		newID("wake"), t.ID, ms(next), t.Version, ms(after))
	return err
}

// enqueueTx queues a run for the task, or joins the queued run it already
// has. scheduledFor is the occurrence a schedule run stands for.
func (s *Service) enqueueTx(tx *sql.Tx, taskID, trigger, reason string, scheduledFor *time.Time, interactive bool, now time.Time) (*Run, bool, error) {
	var id, triggersJSON string
	err := tx.QueryRow(`SELECT id, triggers FROM runs WHERE task_id = ? AND status = 'queued' ORDER BY seq LIMIT 1`, taskID).Scan(&id, &triggersJSON)
	switch {
	case err == nil:
		var triggers []string
		jsonUnmarshal(triggersJSON, &triggers)
		if !contains(triggers, trigger) {
			triggers = append(triggers, trigger)
		}
		if _, err := tx.Exec(`UPDATE runs SET triggers = ?, interactive = MAX(interactive, ?), reason = CASE WHEN reason = '' THEN ? ELSE reason || '; ' || ? END WHERE id = ?`,
			mustJSON(triggers), interactive, reason, reason, id); err != nil {
			return nil, false, err
		}
		r, err := loadRunTx(tx, id)
		return r, true, err
	case !isNoRows(err):
		return nil, false, err
	}
	r := &Run{ID: newID("run"), TaskID: taskID, Status: RunQueued, Triggers: []string{trigger}, Reason: reason,
		QueuedAt: now, ScheduledFor: scheduledFor, Interactive: interactive}
	_, err = tx.Exec(`INSERT INTO runs (id, task_id, status, triggers, reason, interactive, queued_at, scheduled_for) VALUES (?, ?, 'queued', ?, ?, ?, ?, ?)`,
		r.ID, taskID, mustJSON(r.Triggers), reason, interactive, ms(now), msPtr(scheduledFor))
	return r, false, err
}

// skipTx records automatic starts that did not happen.
func skipTx(tx *sql.Tx, taskID, reason string, scheduledFor *time.Time, now time.Time) error {
	_, err := tx.Exec(`INSERT INTO runs (id, task_id, status, triggers, reason, queued_at, scheduled_for, ended_at) VALUES (?, ?, 'skipped', '["schedule"]', ?, ?, ?, ?)`,
		newID("run"), taskID, reason, ms(now), msPtr(scheduledFor), ms(now))
	return err
}

// automaticStartsToday counts the task's runs started or queued today.
func automaticStartsToday(tx *sql.Tx, t *Task, now time.Time) (int, error) {
	start, end := dayBounds(now, t.Schedule.Timezone)
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE task_id = ? AND status <> 'skipped' AND status <> 'cancelled'
		AND COALESCE(started_at, queued_at) >= ? AND COALESCE(started_at, queued_at) < ?`, t.ID, ms(start), ms(end)).Scan(&n)
	return n, err
}

// maxCatchUpScan bounds the occurrences walked for one missed wake-up (a
// per-minute cron after a week asleep).
const maxCatchUpScan = 20000

// Tick processes due wake-ups, expires questions, enforces run deadlines
// and starts queued runs. The loop calls it; tests call it with a fake
// clock.
func (s *Service) Tick(ctx context.Context) {
	now := s.clock()
	if err := s.resumeDue(ctx, now); err != nil {
		slog.Error("resident resume failed", "operation", "resident.resume", "error", err)
	}
	if err := s.fireDue(ctx, now); err != nil {
		slog.Error("resident scheduler failed", "operation", "resident.tick", "error", err)
	}
	s.store.db.ExecContext(ctx, `UPDATE questions SET status = 'expired' WHERE status = 'open' AND expires_at <= ?`, ms(now))
	s.enforceDeadlines(now)
	s.dispatch(ctx)
	if s.Notifier != nil {
		s.Notifier.Deliver(ctx)
	}
}

// resumeDue resumes the tasks whose timed pause has ended.
func (s *Service) resumeDue(ctx context.Context, now time.Time) error {
	rows, err := s.store.db.QueryContext(ctx, `SELECT id, paused_until FROM tasks WHERE deleted_at IS NULL AND enabled = 0 AND paused_until <= ?`, ms(now))
	if err != nil {
		return err
	}
	type due struct {
		id    string
		until time.Time
	}
	var tasks []due
	for rows.Next() {
		var d due
		var until int64
		rows.Scan(&d.id, &until)
		d.until = fromMs(until)
		tasks = append(tasks, d)
	}
	rows.Close()
	for _, d := range tasks {
		err := s.store.tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.Exec(`UPDATE tasks SET enabled = 1, paused_until = NULL, version = version + 1, updated_at = ? WHERE id = ?`, ms(now), d.id); err != nil {
				return err
			}
			return s.rescheduleTx(tx, d.id, d.until)
		})
		if err != nil {
			return err
		}
		slog.Info("resident task resumed after a timed pause", "operation", "resident.resume", "task_id", d.id)
	}
	if len(tasks) > 0 && s.OnChange != nil {
		s.OnChange()
	}
	return nil
}

func (s *Service) fireDue(ctx context.Context, now time.Time) error {
	rows, err := s.store.db.QueryContext(ctx, `SELECT DISTINCT task_id FROM wakeups WHERE status = 'pending' AND due_at <= ?`, ms(now))
	if err != nil {
		return err
	}
	var tasks []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		tasks = append(tasks, id)
	}
	rows.Close()
	fired := false
	for _, id := range tasks {
		err := s.store.tx(ctx, func(tx *sql.Tx) error {
			f, err := s.fireTaskTx(tx, id, now)
			fired = fired || f
			return err
		})
		if err != nil {
			slog.Error("resident wake-up failed", "operation", "resident.wake", "task_id", id, "error", err)
		}
	}
	if fired {
		s.changed()
	}
	return nil
}

// fireTaskTx handles a task's due wake-ups: one run for all of them.
func (s *Service) fireTaskTx(tx *sql.Tx, taskID string, now time.Time) (bool, error) {
	t, err := loadTaskTx(tx, taskID)
	if err == ErrNotFound {
		_, err = tx.Exec(`UPDATE wakeups SET status = 'cancelled', reason = 'task deleted' WHERE task_id = ? AND status = 'pending'`, taskID)
		return false, err
	}
	if err != nil {
		return false, err
	}
	rows, err := tx.Query(`SELECT id, kind, due_at, settings_version, reason FROM wakeups WHERE task_id = ? AND status = 'pending' AND due_at <= ? ORDER BY due_at`, taskID, ms(now))
	if err != nil {
		return false, err
	}
	var due []Wakeup
	for rows.Next() {
		var w Wakeup
		var at int64
		rows.Scan(&w.ID, &w.Kind, &at, &w.SettingsVersion, &w.Reason)
		w.DueAt = fromMs(at)
		due = append(due, w)
	}
	rows.Close()
	if !t.Enabled {
		_, err := tx.Exec(`UPDATE wakeups SET status = 'cancelled', reason = 'task paused' WHERE task_id = ? AND status = 'pending'`, taskID)
		return false, err
	}
	var triggers []string
	var reasons []string
	var scheduledFor *time.Time
	var wakeIDs []string
	for _, w := range due {
		switch w.Kind {
		case WakeSchedule:
			if w.SettingsVersion != t.Version {
				tx.Exec(`UPDATE wakeups SET status = 'cancelled', reason = 'schedule changed' WHERE id = ?`, w.ID)
				continue
			}
			// Walk every occurrence up to now: the newest one within the
			// catch-up window runs, the rest are skipped.
			occ := []time.Time{w.DueAt}
			for i := 0; i < maxCatchUpScan; i++ {
				n, ok := t.Schedule.Next(occ[len(occ)-1])
				if !ok || n.After(now) {
					break
				}
				occ = append(occ, n)
			}
			latest := occ[len(occ)-1]
			missed := occ[:len(occ)-1]
			if now.Sub(latest) > s.cfg.CatchUpWindow {
				missed = occ
				latest = time.Time{}
			}
			if len(missed) > 0 {
				first := missed[0]
				reason := fmt.Sprintf("Gateway停止またはMacのスリープ中の予定 %d 回を見送り（最初 %s）", len(missed), first.Format(time.RFC3339))
				if len(missed) == 1 {
					reason = "Gateway停止またはMacのスリープ中の予定を見送り（予定 " + first.Format(time.RFC3339) + "）"
				}
				last := missed[len(missed)-1]
				if err := skipTx(tx, taskID, reason, &last, now); err != nil {
					return false, err
				}
			}
			if !latest.IsZero() {
				trig := TriggerSchedule
				if now.Sub(latest) > 2*time.Minute {
					trig = TriggerCatchUp
				}
				triggers = append(triggers, trig)
				l := latest
				scheduledFor = &l
				reasons = append(reasons, "scheduled "+latest.Format(time.RFC3339))
				wakeIDs = append(wakeIDs, w.ID)
			} else {
				tx.Exec(`UPDATE wakeups SET status = 'skipped', reason = 'older than the catch-up window' WHERE id = ?`, w.ID)
			}
			if err := s.addScheduleWakeAfterTx(tx, t, w.ID, now); err != nil {
				return false, err
			}
		case WakeSelf:
			if t.Limits.ExpiresAt != nil && w.DueAt.After(*t.Limits.ExpiresAt) {
				tx.Exec(`UPDATE wakeups SET status = 'cancelled', reason = 'after the task ends' WHERE id = ?`, w.ID)
				continue
			}
			if now.Sub(w.DueAt) > s.cfg.CatchUpWindow {
				tx.Exec(`UPDATE wakeups SET status = 'skipped', reason = 'older than the catch-up window' WHERE id = ?`, w.ID)
				continue
			}
			triggers = append(triggers, TriggerSelf)
			reasons = append(reasons, "self wake-up: "+w.Reason)
			wakeIDs = append(wakeIDs, w.ID)
		}
	}
	if len(wakeIDs) == 0 {
		return false, nil
	}
	if t.Limits.ExpiresAt != nil && now.After(*t.Limits.ExpiresAt) {
		for _, id := range wakeIDs {
			tx.Exec(`UPDATE wakeups SET status = 'skipped', reason = 'the task has ended' WHERE id = ?`, id)
		}
		return false, skipTx(tx, taskID, "タスクの期限を過ぎています", scheduledFor, now)
	}
	n, err := automaticStartsToday(tx, t, now)
	if err != nil {
		return false, err
	}
	var queued int
	tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE task_id = ? AND status = 'queued'`, taskID).Scan(&queued)
	if queued == 0 && n >= t.Limits.MaxRunsPerDay {
		for _, id := range wakeIDs {
			tx.Exec(`UPDATE wakeups SET status = 'skipped', reason = 'daily limit' WHERE id = ?`, id)
		}
		return false, skipTx(tx, taskID, fmt.Sprintf("1日の起動上限に達したため見送り（今日 %d 回、上限 %d 回）", n, t.Limits.MaxRunsPerDay), scheduledFor, now)
	}
	var run *Run
	merged := false
	for i, trig := range triggers {
		var err error
		run, merged, err = s.enqueueTx(tx, taskID, trig, reasons[i], scheduledFor, false, now)
		if err != nil {
			return false, err
		}
	}
	status := WakeFired
	if merged {
		status = WakeMerged
	}
	for _, id := range wakeIDs {
		if _, err := tx.Exec(`UPDATE wakeups SET status = ?, run_id = ? WHERE id = ?`, status, run.ID, id); err != nil {
			return false, err
		}
	}
	return true, nil
}

// addScheduleWakeAfterTx replaces a consumed schedule wake-up with the next
// occurrence after now.
func (s *Service) addScheduleWakeAfterTx(tx *sql.Tx, t *Task, consumed string, now time.Time) error {
	if _, err := tx.Exec(`UPDATE wakeups SET status = 'fired' WHERE id = ? AND status = 'pending'`, consumed); err != nil {
		return err
	}
	return s.addScheduleWakeTx(tx, t, now)
}

// setSelfWakeTx validates an agent's nextWakeAt and makes it the task's
// self wake-up. It returns a warning when the request was refused or merged.
func (s *Service) setSelfWakeTx(tx *sql.Tx, t *Task, runID string, at time.Time, reason string, now time.Time) (string, error) {
	switch {
	case at.Before(now.Add(s.cfg.MinSelfWake)):
		return fmt.Sprintf("再確認の予約 %s は拒否: %s 以上先である必要があります", at.Format(time.RFC3339), s.cfg.MinSelfWake), nil
	case at.After(now.Add(s.cfg.MaxSelfWake)):
		return fmt.Sprintf("再確認の予約 %s は拒否: %s より先は予約できません", at.Format(time.RFC3339), s.cfg.MaxSelfWake), nil
	case t.Limits.ExpiresAt != nil && at.After(*t.Limits.ExpiresAt):
		return fmt.Sprintf("再確認の予約 %s は拒否: タスクの期限 %s より後です", at.Format(time.RFC3339), t.Limits.ExpiresAt.Format(time.RFC3339)), nil
	case !t.Enabled:
		return "再確認の予約は無視: タスクは一時停止中です", nil
	}
	// A scheduled run within MinSelfWake of the request covers it.
	var schedDue sql.NullInt64
	tx.QueryRow(`SELECT due_at FROM wakeups WHERE task_id = ? AND kind = 'schedule' AND status = 'pending'`, t.ID).Scan(&schedDue)
	if schedDue.Valid {
		d := fromMs(schedDue.Int64)
		if diff := d.Sub(at); diff > -s.cfg.MinSelfWake && diff < s.cfg.MinSelfWake {
			if _, err := tx.Exec(`UPDATE wakeups SET status = 'cancelled', reason = 'replaced by a newer request' WHERE task_id = ? AND kind = 'self' AND status = 'pending'`, t.ID); err != nil {
				return "", err
			}
			return fmt.Sprintf("再確認の予約 %s は %s の定期実行にまとめました", at.Format(time.RFC3339), d.Format(time.RFC3339)), nil
		}
	}
	if _, err := tx.Exec(`UPDATE wakeups SET status = 'cancelled', reason = 'replaced by a newer request' WHERE task_id = ? AND kind = 'self' AND status = 'pending'`, t.ID); err != nil {
		return "", err
	}
	_, err := tx.Exec(`INSERT INTO wakeups (id, task_id, kind, due_at, status, settings_version, reason, run_id, created_at) VALUES (?, ?, 'self', ?, 'pending', ?, ?, ?, ?)`,
		newID("wake"), t.ID, ms(at.UTC().Truncate(time.Second)), t.Version, truncateBytes(reason, 200), runID, ms(now))
	return "", err
}

// Wakeups lists a task's pending wake-ups.
func (s *Service) Wakeups(ctx context.Context, taskID string) ([]Wakeup, error) {
	rows, err := s.store.db.QueryContext(ctx, `SELECT id, task_id, kind, due_at, status, settings_version, reason, run_id FROM wakeups WHERE task_id = ? AND status = 'pending' ORDER BY due_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Wakeup{}
	for rows.Next() {
		var w Wakeup
		var due int64
		if err := rows.Scan(&w.ID, &w.TaskID, &w.Kind, &due, &w.Status, &w.SettingsVersion, &w.Reason, &w.RunID); err != nil {
			return nil, err
		}
		w.DueAt = fromMs(due)
		out = append(out, w)
	}
	return out, rows.Err()
}

// Run drives the scheduler until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(s.cfg.Tick)
	defer t.Stop()
	s.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
		}
		s.Tick(ctx)
	}
}
