package resident

import (
	"context"
	"database/sql"
	"errors"

	"log/slog"
	"strconv"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
	"github.com/tohutohu/herdr-android-client/gateway/internal/notifications"
)

// Notification decisions recorded on a run (notify_decision).
const (
	DecisionQueued       = "queued"
	DecisionDeferred     = "deferred_quiet_hours"
	DecisionNotRequested = "not_requested"
	DecisionModeNever    = "disabled"
	DecisionDuplicate    = "duplicate_key"
	DecisionCooldown     = "cooldown"
	DecisionStreak       = "failure_already_reported"
)

// notifyChangeTx decides on a change notification. The agent asks
// (notification.requested); the gateway applies the task's rules: mode,
// keys already notified, cooldown and quiet hours.
func (s *Service) notifyChangeTx(tx *sql.Tx, t *Task, r *Run, requested bool, key, summary string, now time.Time) error {
	decide := func(d string) error {
		_, err := tx.Exec(`UPDATE runs SET notify_decision = ?, notification_key = ? WHERE id = ?`, d, key, r.ID)
		return err
	}
	if key == "" {
		key = "summary:" + hashKey(summary)
	}
	switch {
	case t.Notify.Mode == "never":
		return decide(DecisionModeNever)
	case t.Notify.Mode == "changes" && !requested:
		return decide(DecisionNotRequested)
	}
	var seen int
	tx.QueryRow(`SELECT COUNT(*) FROM notified_keys WHERE task_id = ? AND key = ?`, t.ID, key).Scan(&seen)
	if seen > 0 {
		return decide(DecisionDuplicate)
	}
	if t.Notify.CooldownMinutes > 0 {
		var last sql.NullInt64
		tx.QueryRow(`SELECT MAX(created_at) FROM notifications WHERE task_id = ? AND kind = 'change' AND status <> 'suppressed'`, t.ID).Scan(&last)
		if last.Valid && now.Sub(fromMs(last.Int64)) < time.Duration(t.Notify.CooldownMinutes)*time.Minute {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO notified_keys (task_id, key, notified_at) VALUES (?, ?, ?)`, t.ID, key, ms(now)); err != nil {
				return err
			}
			return decide(DecisionCooldown)
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO notified_keys (task_id, key, notified_at) VALUES (?, ?, ?)`, t.ID, key, ms(now)); err != nil {
		return err
	}
	d, err := s.enqueueNotificationTx(tx, t, r, NotifyChange, t.Name, summary, key, now)
	if err != nil {
		return err
	}
	return decide(d)
}

func (s *Service) notifyQuestionTx(tx *sql.Tx, t *Task, r *Run, qid, text string, now time.Time) error {
	if !t.Notify.OnQuestion {
		_, err := tx.Exec(`UPDATE runs SET notify_decision = ? WHERE id = ?`, DecisionModeNever, r.ID)
		return err
	}
	d, err := s.enqueueNotificationTx(tx, t, r, NotifyQuestion, t.Name+": 判断待ち", text, "question:"+qid, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE runs SET notify_decision = ? WHERE id = ?`, d, r.ID)
	return err
}

// notifyFailureTx reports a failure once per streak: later failures before
// the next success are only recorded.
func (s *Service) notifyFailureTx(tx *sql.Tx, t *Task, r *Run, text string, now time.Time) error {
	decide := func(d string) error {
		_, err := tx.Exec(`UPDATE runs SET notify_decision = ? WHERE id = ?`, d, r.ID)
		return err
	}
	if !t.Notify.OnFailure {
		return decide(DecisionModeNever)
	}
	var streak int
	tx.QueryRow(`SELECT COUNT(*) FROM notified_keys WHERE task_id = ? AND key = 'failure-streak'`, t.ID).Scan(&streak)
	if streak > 0 && r.Status != RunNeedsReconciliation {
		return decide(DecisionStreak)
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO notified_keys (task_id, key, notified_at) VALUES (?, 'failure-streak', ?)`, t.ID, ms(now)); err != nil {
		return err
	}
	title := t.Name + ": 失敗"
	switch r.Status {
	case RunNeedsReconciliation:
		title = t.Name + ": 要確認（中断）"
	case RunTimedOut:
		title = t.Name + ": タイムアウト"
	case RunCompleted:
		title = t.Name + ": 進められません"
	}
	d, err := s.enqueueNotificationTx(tx, t, r, NotifyFailure, title, text, "failure:"+r.ID, now)
	if err != nil {
		return err
	}
	return decide(d)
}

func (s *Service) enqueueNotificationTx(tx *sql.Tx, t *Task, r *Run, kind, title, body, key string, now time.Time) (string, error) {
	notBefore := quietEnd(t.Notify, t.Schedule.Timezone, now)
	decision := DecisionQueued
	if notBefore.After(now) {
		decision = DecisionDeferred
	}
	_, err := tx.Exec(`INSERT INTO notifications (id, task_id, run_id, kind, title, body, key, status, created_at, not_before, next_attempt_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)`,
		newID("ntf"), t.ID, r.ID, kind, truncateBytes(title, 200), truncateBytes(body, 1500), key, ms(now), ms(notBefore), ms(notBefore))
	return decision, err
}

// quietEnd is when a notification created at now may be sent: now, or the
// end of the task's quiet hours.
func quietEnd(n Notify, tz string, now time.Time) time.Time {
	if n.QuietStart == "" || n.QuietEnd == "" {
		return now
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return now
	}
	sh, sm, err1 := parseClock(n.QuietStart)
	eh, em, err2 := parseClock(n.QuietEnd)
	if err1 != nil || err2 != nil {
		return now
	}
	l := now.In(loc)
	mins := l.Hour()*60 + l.Minute()
	start, end := sh*60+sm, eh*60+em
	inQuiet := false
	switch {
	case start == end:
		return now
	case start < end:
		inQuiet = mins >= start && mins < end
	default: // over midnight
		inQuiet = mins >= start || mins < end
	}
	if !inQuiet {
		return now
	}
	day := l
	if start > end && mins >= start {
		day = l.AddDate(0, 0, 1)
	}
	return resolveWall(day.Year(), day.Month(), day.Day(), eh, em, loc)
}

// Notifier delivers the outbox over FCM. Each entry keeps a stable id; a
// device that got it is remembered, so retries go only to the others. A
// send whose acknowledgement was lost may still reach a device twice: the
// app replaces a notification with the same id, and the conversation in
// the app is the record, not the push.
type Notifier struct {
	Store  *Store
	Sender notifications.Sender
	Config *config.Store
	Now    func() time.Time
}

// MaxNotificationAttempts bounds retries of one entry.
const MaxNotificationAttempts = 6

var retryDelays = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

func (n *Notifier) now() time.Time {
	if n.Now != nil {
		return n.Now().UTC()
	}
	return time.Now().UTC()
}

// Deliver sends the due entries.
func (n *Notifier) Deliver(ctx context.Context) {
	if n == nil || n.Store == nil {
		return
	}
	now := n.now()
	rows, err := n.Store.db.QueryContext(ctx, `SELECT id, task_id, run_id, kind, title, body, attempts, delivered FROM notifications
		WHERE status = 'pending' AND next_attempt_at <= ? AND not_before <= ? ORDER BY seq LIMIT 20`, ms(now), ms(now))
	if err != nil {
		return
	}
	type entry struct {
		Notification
		delivered []string
	}
	var due []entry
	for rows.Next() {
		var e entry
		var delivered string
		rows.Scan(&e.ID, &e.TaskID, &e.RunID, &e.Kind, &e.Title, &e.Body, &e.Attempts, &delivered)
		jsonUnmarshal(delivered, &e.delivered)
		due = append(due, e)
	}
	rows.Close()
	for _, e := range due {
		n.deliverOne(ctx, e.Notification, e.delivered)
	}
}

func (n *Notifier) deliverOne(ctx context.Context, e Notification, delivered []string) {
	now := n.now()
	if n.Sender == nil || n.Config == nil {
		n.Store.db.ExecContext(ctx, `UPDATE notifications SET status = 'suppressed', reason = 'push notifications are not configured on the gateway' WHERE id = ?`, e.ID)
		return
	}
	cfg := n.Config.Get()
	data := map[string]string{
		"type":           "resident",
		"gatewayId":      config.GatewayID(cfg.AuthToken),
		"taskId":         e.TaskID,
		"runId":          e.RunID,
		"notificationId": e.ID,
		"kind":           e.Kind,
		"title":          e.Title,
		"body":           e.Body,
	}
	var lastErr error
	for _, d := range cfg.Devices {
		h := hashKey(d.FCMToken)
		if contains(delivered, h) {
			continue
		}
		err := n.Sender.Send(ctx, d.FCMToken, data)
		switch {
		case err == nil:
			delivered = append(delivered, h)
		case errors.Is(err, notifications.ErrTokenInvalid):
			slog.Warn("removing invalid device token", "operation", "resident.push", "device", d.Name, "error", err)
			n.Config.RemoveDeviceToken(d.FCMToken)
		default:
			lastErr = err
		}
	}
	attempts := e.Attempts + 1
	switch {
	case lastErr == nil:
		n.Store.db.ExecContext(ctx, `UPDATE notifications SET status = 'sent', attempts = ?, sent_at = ?, delivered = ?, last_error = '' WHERE id = ?`, attempts, ms(now), mustJSON(delivered), e.ID)
		slog.Info("resident push sent", "operation", "resident.push", "task_id", e.TaskID, "run_id", e.RunID, "notification_id", e.ID, "kind", e.Kind)
	case attempts >= MaxNotificationAttempts:
		n.Store.db.ExecContext(ctx, `UPDATE notifications SET status = 'failed', attempts = ?, delivered = ?, last_error = ? WHERE id = ?`, attempts, mustJSON(delivered), lastErr.Error(), e.ID)
		slog.Error("resident push gave up", "operation", "resident.push", "notification_id", e.ID, "error", lastErr)
	default:
		next := now.Add(retryDelays[min(attempts-1, len(retryDelays)-1)])
		n.Store.db.ExecContext(ctx, `UPDATE notifications SET attempts = ?, next_attempt_at = ?, delivered = ?, last_error = ? WHERE id = ?`, attempts, ms(next), mustJSON(delivered), lastErr.Error(), e.ID)
		slog.Warn("resident push failed; will retry", "operation", "resident.push", "notification_id", e.ID, "attempt", strconv.Itoa(attempts), "error", lastErr)
	}
}

// Notifications lists a task's outbox entries, newest first.
func (s *Service) Notifications(ctx context.Context, taskID string, limit int) ([]Notification, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id, task_id, run_id, kind, title, body, key, status, reason, created_at, not_before, attempts, sent_at, last_error FROM notifications`
	args := []any{}
	if taskID != "" {
		if _, err := s.loadTask(ctx, taskID); err != nil {
			return nil, err
		}
		q += ` WHERE task_id = ?`
		args = append(args, taskID)
	}
	q += ` ORDER BY seq DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.store.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var e Notification
		var created, notBefore int64
		var sent sql.NullInt64
		if err := rows.Scan(&e.ID, &e.TaskID, &e.RunID, &e.Kind, &e.Title, &e.Body, &e.Key, &e.Status, &e.Reason, &created, &notBefore, &e.Attempts, &sent, &e.LastError); err != nil {
			return nil, err
		}
		e.CreatedAt, e.NotBefore, e.SentAt = fromMs(created), fromMs(notBefore), fromNull(sent)
		out = append(out, e)
	}
	return out, rows.Err()
}
