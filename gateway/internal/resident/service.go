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
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

var (
	ErrNotFound = errors.New("not found")
	// ErrInvalid wraps a request the gateway refuses, with the reason.
	ErrInvalid = errors.New("invalid request")
	// ErrConflict means the client's version is stale or a request id was
	// reused for a different request.
	ErrConflict = errors.New("conflict")
	// ErrTrustRequired means the folder was never trusted in the agent and
	// the request did not confirm it.
	ErrTrustRequired = errors.New("trust required")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Environment is what the service needs from the rest of the gateway to
// check settings, the same checks new sessions get.
type Environment interface {
	// ResolveDir validates a working directory against workspaceRoots.
	ResolveDir(path string) (string, error)
	// FolderTrusted reports whether the agent itself trusts the folder.
	FolderTrusted(provider, cwd string) bool
	// Catalog lists the models and efforts the provider offers.
	Catalog(ctx context.Context, provider string) (providers.ModelCatalog, error)
}

// Config tunes the service. Zero values take the defaults.
type Config struct {
	// MaxConcurrentRuns across all tasks (default 1).
	MaxConcurrentRuns int
	// CatchUpWindow: a missed scheduled run is made up when it is at most
	// this old (default 24 h); older ones are recorded as skipped.
	CatchUpWindow time.Duration
	// MinSelfWake is the shortest delay an agent may ask for (default 15 min).
	MinSelfWake time.Duration
	// MaxSelfWake is the furthest ahead an agent may ask for (default 7 days).
	MaxSelfWake time.Duration
	// QuestionTTL is how long a question can be answered (default 7 days).
	QuestionTTL time.Duration
	// Tick is how often the scheduler looks at the clock (default 30 s).
	Tick time.Duration
}

func (c Config) withDefaults() Config {
	if c.MaxConcurrentRuns <= 0 {
		c.MaxConcurrentRuns = 1
	}
	if c.CatchUpWindow <= 0 {
		c.CatchUpWindow = 24 * time.Hour
	}
	if c.MinSelfWake <= 0 {
		c.MinSelfWake = 15 * time.Minute
	}
	if c.MaxSelfWake <= 0 {
		c.MaxSelfWake = 7 * 24 * time.Hour
	}
	if c.QuestionTTL <= 0 {
		c.QuestionTTL = 7 * 24 * time.Hour
	}
	if c.Tick <= 0 {
		c.Tick = 30 * time.Second
	}
	return c
}

// Defaults for new tasks.
const (
	DefaultMaxRunMinutes = 30
	DefaultMaxRunsPerDay = 12
	maxRunMinutesLimit   = 240
	maxRunsPerDayLimit   = 288
)

// Service owns the resident tasks.
type Service struct {
	store   *Store
	env     Environment
	runners map[string]Runner
	cfg     Config
	now     func() time.Time
	// Notifier delivers the notification outbox; nil keeps entries pending.
	Notifier *Notifier

	dispatchMu sync.Mutex
	mu         sync.Mutex
	active     map[string]*activeRun // by run id
	closing    bool
	wg         sync.WaitGroup
	kick       chan struct{}
	// OnChange is called after any change apps may want to refresh for.
	OnChange func()
	// Precheck runs tasks' precheck commands (RunPrecheck by default).
	Precheck PrecheckRunner
}

// New creates the service. now may be nil (time.Now).
func New(store *Store, env Environment, runners map[string]Runner, cfg Config, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, env: env, runners: runners, cfg: cfg.withDefaults(), now: now,
		active: map[string]*activeRun{}, kick: make(chan struct{}, 1), Precheck: RunPrecheck}
}

func (s *Service) clock() time.Time { return s.now().UTC() }

// Kick asks the loop to look at queued work now.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// --- validation ---

var allowedToolPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\([^()\x00-\x1f]{1,200}\))?$|^mcp__[A-Za-z0-9_-]+(__[A-Za-z0-9_*-]+)?$`)

// claudeBuiltinTools are rules the profiles already decide; listing them in
// AllowedTools would bypass the profile.
var profileOwnedTools = []string{"Edit", "Write", "NotebookEdit", "MultiEdit"}

// normalize checks settings and fills defaults. It does not touch the
// filesystem or the provider; see validateEnv.
func normalize(in Settings) (Settings, error) {
	s := in
	s.Name = strings.TrimSpace(s.Name)
	s.Instruction = strings.TrimSpace(s.Instruction)
	if s.Name == "" {
		// Like a session, a task needs no name: its instruction's first line stands in.
		s.Name = defaultName(s.Instruction)
	}
	if s.Name == "" {
		return s, invalid("instruction is required")
	}
	if utf8.RuneCountInString(s.Name) > 80 {
		return s, invalid("name is longer than 80 characters")
	}
	if s.Instruction == "" {
		return s, invalid("instruction is required")
	}
	if len(s.Instruction) > 32<<10 {
		return s, invalid("instruction is longer than 32 KiB")
	}
	switch s.Provider {
	case ProviderClaude, ProviderCodex, ProviderDSH:
	case "":
		return s, invalid("provider is required (claude, codex or dsh)")
	default:
		return s, invalid("unknown provider %q (claude, codex or dsh)", s.Provider)
	}
	if s.Model != "" && !providers.ValidModelID(s.Model) {
		return s, invalid("model %q is not a model id", s.Model)
	}
	if s.Effort != "" && !providers.ValidEffortID(s.Effort) {
		return s, invalid("effort %q is not an effort id", s.Effort)
	}
	if strings.TrimSpace(s.Cwd) == "" {
		return s, invalid("cwd is required")
	}
	sched, err := s.Schedule.Normalize()
	if err != nil {
		return s, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	s.Schedule = sched
	s.Permissions.Network = false
	switch s.Permissions.Profile {
	case "":
		s.Permissions.Profile = ProfileWorkspaceWrite
	case ProfileReadOnly, ProfileWorkspaceWrite:
	default:
		return s, invalid("unknown permission profile %q (read_only or workspace_write)", s.Permissions.Profile)
	}
	if s.Permissions.AllowedTools == nil {
		s.Permissions.AllowedTools = []string{}
	}
	if len(s.Permissions.AllowedTools) > 0 && s.Provider != ProviderClaude {
		return s, invalid("allowedTools are Claude Code permission rules; Codex and DSH runs use their sandbox only")
	}
	if len(s.Permissions.AllowedTools) > 50 {
		return s, invalid("at most 50 allowedTools")
	}
	for _, t := range s.Permissions.AllowedTools {
		if !allowedToolPattern.MatchString(t) {
			return s, invalid("allowedTools entry %q is not a Claude Code permission rule such as Bash(gh pr view:*)", t)
		}
		name, _, _ := strings.Cut(t, "(")
		if slices.Contains(profileOwnedTools, name) {
			return s, invalid("%s is decided by the permission profile; use workspace_write instead", name)
		}
		if name == "Bash" && !strings.Contains(t, "(") {
			return s, invalid("Bash needs a command pattern, e.g. Bash(gh pr view:*); unrestricted Bash is not allowed for unattended runs")
		}
	}
	switch s.Notify.Mode {
	case "":
		// No notification settings at all: the defaults, which report
		// questions and failures too.
		if s.Notify == (Notify{}) {
			s.Notify = Notify{OnFailure: true, OnQuestion: true}
		}
		s.Notify.Mode = "changes"
	case "changes", "always", "never":
	default:
		return s, invalid("unknown notify mode %q (changes, always or never)", s.Notify.Mode)
	}
	if s.Notify.CooldownMinutes < 0 || s.Notify.CooldownMinutes > 7*24*60 {
		return s, invalid("cooldownMinutes must be between 0 and 10080")
	}
	if (s.Notify.QuietStart == "") != (s.Notify.QuietEnd == "") {
		return s, invalid("quiet hours need both quietStart and quietEnd")
	}
	if s.Notify.QuietStart != "" {
		if _, _, err := parseClock(s.Notify.QuietStart); err != nil {
			return s, invalid("quietStart: %v", err)
		}
		if _, _, err := parseClock(s.Notify.QuietEnd); err != nil {
			return s, invalid("quietEnd: %v", err)
		}
	}
	if s.Limits.MaxRunMinutes == 0 {
		s.Limits.MaxRunMinutes = DefaultMaxRunMinutes
	}
	if s.Limits.MaxRunMinutes < 1 || s.Limits.MaxRunMinutes > maxRunMinutesLimit {
		return s, invalid("maxRunMinutes must be between 1 and %d", maxRunMinutesLimit)
	}
	if s.Limits.MaxRunsPerDay == 0 {
		s.Limits.MaxRunsPerDay = DefaultMaxRunsPerDay
	}
	if s.Limits.MaxRunsPerDay < 1 || s.Limits.MaxRunsPerDay > maxRunsPerDayLimit {
		return s, invalid("maxRunsPerDay must be between 1 and %d", maxRunsPerDayLimit)
	}
	if s.Precheck, err = normalizePrecheck(s.Precheck, s.Limits.MaxRunMinutes); err != nil {
		return s, err
	}
	return s, nil
}

// defaultName is a task name made from the instruction's first line.
func defaultName(instruction string) string {
	line, _, _ := strings.Cut(instruction, "\n")
	line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#-*> "))
	if r := []rune(line); len(r) > 40 {
		line = string(r[:40]) + "…"
	}
	return line
}

// validateEnv checks the parts of the settings that depend on the Mac: the
// folder, its trust, the model and the effort.
func (s *Service) validateEnv(ctx context.Context, st *Settings, trust bool, alreadyTrusted bool) (trusted bool, err error) {
	if s.env == nil {
		return alreadyTrusted || trust, nil
	}
	cwd, err := s.env.ResolveDir(st.Cwd)
	if err != nil {
		return false, invalid("cwd %q cannot be used: %v (it must be an existing folder inside the workspace roots)", st.Cwd, err)
	}
	st.Cwd = cwd
	trusted = alreadyTrusted || trust || s.env.FolderTrusted(st.Provider, cwd)
	if !trusted {
		return false, fmt.Errorf("%w: %s was never trusted in %s; its project settings (hooks, MCP servers) would run unattended. Confirm with trust: true", ErrTrustRequired, cwd, st.Provider)
	}
	if st.Model == "" && st.Effort == "" {
		return true, nil
	}
	cat, err := s.env.Catalog(ctx, st.Provider)
	if err != nil {
		return false, invalid("cannot check the model against %s's catalog: %v", st.Provider, err)
	}
	efforts := cat.Efforts
	if st.Model != "" {
		var found *providers.ModelOption
		for i := range cat.Models {
			if cat.Models[i].ID == st.Model {
				found = &cat.Models[i]
			}
		}
		if found == nil {
			// Claude Code takes full model ids besides its aliases.
			if !(st.Provider == ProviderClaude && strings.HasPrefix(st.Model, "claude-")) {
				var ids []string
				for _, m := range cat.Models {
					ids = append(ids, m.ID)
				}
				return false, invalid("model %q is not offered by %s (available: %s)", st.Model, st.Provider, strings.Join(ids, ", "))
			}
		} else if len(found.Efforts) > 0 {
			efforts = found.Efforts
		}
	}
	if st.Effort != "" && len(efforts) > 0 {
		var ids []string
		ok := false
		for _, e := range efforts {
			ids = append(ids, e.ID)
			ok = ok || e.ID == st.Effort
		}
		if !ok {
			return false, invalid("effort %q is not offered for this model (available: %s)", st.Effort, strings.Join(ids, ", "))
		}
	}
	return true, nil
}

// --- idempotent requests ---

func fingerprint(v any) string {
	sum := sha256.Sum256([]byte(mustJSON(v)))
	return hex.EncodeToString(sum[:])
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,100}$`)

// once runs fn unless requestID was already handled, in which case the
// stored result is decoded into out. A reused id with a different payload
// is a conflict.
func once[T any](tx *sql.Tx, requestID, kind string, payload any, now time.Time, fn func() (T, error)) (T, bool, error) {
	var zero T
	if requestID == "" {
		v, err := fn()
		return v, false, err
	}
	if !requestIDPattern.MatchString(requestID) {
		return zero, false, invalid("requestId must be 8-100 characters of letters, digits and ._:-")
	}
	fp := fingerprint(payload)
	var gotKind, gotFP, result string
	err := tx.QueryRow(`SELECT kind, fingerprint, result FROM requests WHERE id = ?`, requestID).Scan(&gotKind, &gotFP, &result)
	switch {
	case err == nil:
		if gotKind != kind || gotFP != fp {
			return zero, false, fmt.Errorf("%w: requestId %s was already used for a different request", ErrConflict, requestID)
		}
		var v T
		if err := json.Unmarshal([]byte(result), &v); err != nil {
			return zero, false, err
		}
		return v, true, nil
	case !isNoRows(err):
		return zero, false, err
	}
	v, err := fn()
	if err != nil {
		return zero, false, err
	}
	if _, err := tx.Exec(`INSERT INTO requests (id, kind, fingerprint, result, created_at) VALUES (?, ?, ?, ?, ?)`,
		requestID, kind, fp, mustJSON(v), ms(now)); err != nil {
		return zero, false, err
	}
	return v, false, nil
}

// --- tasks ---

// CreateRequest creates a task.
type CreateRequest struct {
	Settings
	Enabled   *bool  `json:"enabled,omitempty"`
	Trust     bool   `json:"trust"`
	RequestID string `json:"requestId,omitempty"`
}

func (s *Service) CreateTask(ctx context.Context, req CreateRequest) (*Task, error) {
	st, err := normalize(req.Settings)
	if err != nil {
		return nil, err
	}
	if _, err := s.validateEnv(ctx, &st, req.Trust, false); err != nil {
		return nil, err
	}
	enabled := req.Enabled == nil || *req.Enabled
	now := s.clock()
	var id string
	err = s.store.tx(ctx, func(tx *sql.Tx) error {
		var err error
		id, _, err = once(tx, req.RequestID, "create_task", req, now, func() (string, error) {
			id := newID("task")
			if _, err := tx.Exec(`INSERT INTO tasks (id, settings, enabled, version, created_at, updated_at, trusted_at) VALUES (?, ?, ?, 1, ?, ?, ?)`,
				id, mustJSON(st), enabled, ms(now), ms(now), ms(now)); err != nil {
				return "", err
			}
			if _, err := tx.Exec(`INSERT INTO memory (task_id, version, summary, state, updated_at, updated_by) VALUES (?, 1, '', '{}', ?, 'user')`, id, ms(now)); err != nil {
				return "", err
			}
			return id, s.rescheduleTx(tx, id, now)
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	slog.Info("resident task created", "operation", "resident.create", "task_id", id, "provider", st.Provider, "schedule", st.Schedule.Kind)
	s.changed()
	return s.GetTask(ctx, id)
}

// UpdateRequest changes a task. Nil fields are kept. ExpectedVersion must
// match the task's version.
type UpdateRequest struct {
	ExpectedVersion int64        `json:"expectedVersion"`
	Name            *string      `json:"name,omitempty"`
	Instruction     *string      `json:"instruction,omitempty"`
	Provider        *string      `json:"provider,omitempty"`
	Model           *string      `json:"model,omitempty"`
	Effort          *string      `json:"effort,omitempty"`
	Cwd             *string      `json:"cwd,omitempty"`
	Schedule        *Schedule    `json:"schedule,omitempty"`
	Permissions     *Permissions `json:"permissions,omitempty"`
	Notify          *Notify      `json:"notify,omitempty"`
	Limits          *Limits      `json:"limits,omitempty"`
	// Precheck replaces the task's precheck; an empty command removes it.
	Precheck *Precheck `json:"precheck,omitempty"`
	Enabled  *bool     `json:"enabled,omitempty"`
	Trust    bool      `json:"trust,omitempty"`
}

func (s *Service) UpdateTask(ctx context.Context, id string, req UpdateRequest) (*Task, error) {
	cur, err := s.loadTask(ctx, id)
	if err != nil {
		return nil, err
	}
	st := cur.Settings
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&st.Name, req.Name)
	set(&st.Instruction, req.Instruction)
	set(&st.Provider, req.Provider)
	set(&st.Model, req.Model)
	set(&st.Effort, req.Effort)
	set(&st.Cwd, req.Cwd)
	if req.Schedule != nil {
		st.Schedule = *req.Schedule
	}
	if req.Permissions != nil {
		st.Permissions = *req.Permissions
	}
	if req.Notify != nil {
		st.Notify = *req.Notify
	}
	if req.Limits != nil {
		st.Limits = *req.Limits
	}
	if req.Precheck != nil {
		st.Precheck = req.Precheck
	}
	st, err = normalize(st)
	if err != nil {
		return nil, err
	}
	sameFolder := st.Cwd == cur.Cwd && st.Provider == cur.Provider
	if _, err := s.validateEnv(ctx, &st, req.Trust, sameFolder && cur.TrustedAt != nil); err != nil {
		return nil, err
	}
	now := s.clock()
	err = s.store.tx(ctx, func(tx *sql.Tx) error {
		var version int64
		var deleted sql.NullInt64
		var enabled bool
		if err := tx.QueryRow(`SELECT version, deleted_at, enabled FROM tasks WHERE id = ?`, id).Scan(&version, &deleted, &enabled); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if deleted.Valid {
			return ErrNotFound
		}
		if req.ExpectedVersion != version {
			return fmt.Errorf("%w: the task changed (version %d, you sent %d); reload it and try again", ErrConflict, version, req.ExpectedVersion)
		}
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		// Setting enabled either way replaces a timed pause.
		if _, err := tx.Exec(`UPDATE tasks SET settings = ?, enabled = ?, paused_until = CASE WHEN ? THEN NULL ELSE paused_until END,
			version = version + 1, updated_at = ?, trusted_at = COALESCE(trusted_at, ?) WHERE id = ?`,
			mustJSON(st), enabled, req.Enabled != nil, ms(now), ms(now), id); err != nil {
			return err
		}
		return s.rescheduleTx(tx, id, now)
	})
	if err != nil {
		return nil, err
	}
	slog.Info("resident task updated", "operation", "resident.update", "task_id", id)
	s.changed()
	return s.GetTask(ctx, id)
}

// MaxPause bounds a timed pause.
const MaxPause = 366 * 24 * time.Hour

// SetEnabled pauses or resumes a task's automatic starts. A pause with
// until ends on its own at that time (see resumeDue); without it the task
// stays paused until resumed.
func (s *Service) SetEnabled(ctx context.Context, id string, enabled bool, until *time.Time) (*Task, error) {
	now := s.clock()
	var untilMs any
	if until != nil && !enabled {
		u := until.UTC().Truncate(time.Second)
		switch {
		case !u.After(now):
			return nil, fmt.Errorf("%w: the pause must end in the future (%s)", ErrInvalid, u.Format(time.RFC3339))
		case u.After(now.Add(MaxPause)):
			return nil, fmt.Errorf("%w: a pause can last at most %d days", ErrInvalid, int(MaxPause.Hours()/24))
		}
		untilMs = ms(u)
	}
	err := s.store.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE tasks SET enabled = ?, paused_until = ?, version = version + 1, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, enabled, untilMs, ms(now), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return s.rescheduleTx(tx, id, now)
	})
	if err != nil {
		return nil, err
	}
	s.changed()
	return s.GetTask(ctx, id)
}

// DeleteTask stops a task for good: pending wake-ups and queued runs are
// cancelled and a running run is stopped. The history stays in the
// database but is no longer served.
func (s *Service) DeleteTask(ctx context.Context, id string) error {
	now := s.clock()
	err := s.store.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE tasks SET deleted_at = ?, enabled = 0, version = version + 1, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, ms(now), ms(now), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if _, err := tx.Exec(`UPDATE wakeups SET status = 'cancelled', reason = 'task deleted' WHERE task_id = ? AND status = 'pending'`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE runs SET status = 'cancelled', ended_at = ?, error = 'task deleted' WHERE task_id = ? AND status = 'queued'`, ms(now), id); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE questions SET status = 'superseded' WHERE task_id = ? AND status = 'open'`, id)
		return err
	})
	if err != nil {
		return err
	}
	s.cancelTaskRuns(id, errTaskDeleted)
	slog.Info("resident task deleted", "operation", "resident.delete", "task_id", id)
	s.changed()
	return nil
}

func (s *Service) changed() {
	s.Kick()
	if s.OnChange != nil {
		s.OnChange()
	}
}

// loadTask reads a task's stored fields (no derived status).
func (s *Service) loadTask(ctx context.Context, id string) (*Task, error) {
	row := s.store.db.QueryRowContext(ctx, `SELECT id, settings, enabled, paused_until, version, created_at, updated_at, trusted_at FROM tasks WHERE id = ? AND deleted_at IS NULL`, id)
	return scanTask(row)
}

func loadTaskTx(tx *sql.Tx, id string) (*Task, error) {
	return scanTask(tx.QueryRow(`SELECT id, settings, enabled, paused_until, version, created_at, updated_at, trusted_at FROM tasks WHERE id = ? AND deleted_at IS NULL`, id))
}

func scanTask(row scanner) (*Task, error) {
	var t Task
	var settings string
	var created, updated int64
	var trusted, pausedUntil sql.NullInt64
	if err := row.Scan(&t.ID, &settings, &t.Enabled, &pausedUntil, &t.Version, &created, &updated, &trusted); err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal([]byte(settings), &t.Settings); err != nil {
		return nil, err
	}
	if t.Permissions.AllowedTools == nil {
		t.Permissions.AllowedTools = []string{}
	}
	t.CreatedAt, t.UpdatedAt, t.TrustedAt = fromMs(created), fromMs(updated), fromNull(trusted)
	if !t.Enabled {
		t.PausedUntil = fromNull(pausedUntil)
	}
	return &t, nil
}

// ListTasks returns every task with its status.
func (s *Service) ListTasks(ctx context.Context) ([]Task, error) {
	rows, err := s.store.db.QueryContext(ctx, `SELECT id, settings, enabled, paused_until, version, created_at, updated_at, trusted_at FROM tasks WHERE deleted_at IS NULL ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.decorate(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	if out == nil {
		out = []Task{}
	}
	return out, nil
}

// GetTask returns one task with its status.
func (s *Service) GetTask(ctx context.Context, id string) (*Task, error) {
	t, err := s.loadTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.decorate(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// decorate fills a task's derived fields.
func (s *Service) decorate(ctx context.Context, t *Task) error {
	db := s.store.db
	now := s.clock()
	// Next automatic start.
	var due sql.NullInt64
	var kind, reason sql.NullString
	err := db.QueryRowContext(ctx, `SELECT due_at, kind, reason FROM wakeups WHERE task_id = ? AND status = 'pending' ORDER BY due_at LIMIT 1`, t.ID).Scan(&due, &kind, &reason)
	if err != nil && !isNoRows(err) {
		return err
	}
	if due.Valid {
		t.NextRunAt = fromNull(due)
		if kind.String == WakeSelf {
			t.NextWakeReason = "self: " + reason.String
		} else {
			t.NextWakeReason = WakeSchedule
		}
	}
	var lastSuccess sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(ended_at) FROM runs WHERE task_id = ? AND status = 'completed' AND outcome IN ('changed','unchanged')`, t.ID).Scan(&lastSuccess); err != nil {
		return err
	}
	t.LastSuccessAt = fromNull(lastSuccess)
	last, err := s.lastRun(ctx, t.ID)
	if err != nil {
		return err
	}
	t.LastRun = last
	var rep Report
	var at int64
	err = db.QueryRowContext(ctx, `SELECT id, outcome, COALESCE(NULLIF(summary, ''), error), ended_at FROM runs WHERE task_id = ? AND ended_at IS NOT NULL
		AND (outcome IN ('changed','blocked','failed') OR status IN ('failed','timed_out','needs_reconciliation','waiting_user')) ORDER BY ended_at DESC LIMIT 1`, t.ID).
		Scan(&rep.RunID, &rep.Outcome, &rep.Summary, &at)
	switch {
	case err == nil:
		rep.At = fromMs(at)
		t.LatestReport = &rep
	case !isNoRows(err):
		return err
	}
	q, err := s.openQuestion(ctx, t.ID)
	if err != nil {
		return err
	}
	t.PendingQuestion = q
	var readThrough int64
	if err := db.QueryRowContext(ctx, `SELECT read_through FROM tasks WHERE id = ?`, t.ID).Scan(&readThrough); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE task_id = ? AND role = 'assistant' AND seq > ?`, t.ID, readThrough).Scan(&t.Unread); err != nil {
		return err
	}
	start, end := dayBounds(now, t.Schedule.Timezone)
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE task_id = ? AND status <> 'skipped' AND started_at >= ? AND started_at < ?`, t.ID, ms(start), ms(end)).Scan(&t.RunsToday); err != nil {
		return err
	}
	// State.
	var queued, running, reconcile int
	db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(status = 'queued'), 0), COALESCE(SUM(status = 'running'), 0), COALESCE(SUM(status = 'needs_reconciliation'), 0)
		FROM runs WHERE task_id = ?`, t.ID).Scan(&queued, &running, &reconcile)
	switch {
	case running > 0:
		t.State = TaskRunning
	case t.PendingQuestion != nil:
		t.State = TaskWaitingUser
	case reconcile > 0:
		t.State = TaskNeedsAttention
		t.Attention = "中断されて成否が分からない実行があります。結果を確認してから再実行してください。"
	case queued > 0:
		t.State = TaskQueued
	case !t.Enabled:
		t.State = TaskPaused
	case last != nil && (last.Status == RunFailed || last.Status == RunTimedOut):
		t.State = TaskNeedsAttention
		t.Attention = last.Error
	default:
		t.State = TaskIdle
	}
	return nil
}

func (s *Service) lastRun(ctx context.Context, taskID string) (*RunBrief, error) {
	var b RunBrief
	var started, ended sql.NullInt64
	err := s.store.db.QueryRowContext(ctx, `SELECT id, status, outcome, summary, error, started_at, ended_at FROM runs
		WHERE task_id = ? AND status <> 'queued' AND (status <> 'skipped' OR started_at IS NOT NULL) ORDER BY seq DESC LIMIT 1`, taskID).
		Scan(&b.ID, &b.Status, &b.Outcome, &b.Summary, &b.Error, &started, &ended)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b.StartedAt, b.EndedAt = fromNull(started), fromNull(ended)
	return &b, nil
}

// dayBounds is the calendar day containing t in tz, as UTC instants.
func dayBounds(t time.Time, tz string) (time.Time, time.Time) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	l := t.In(loc)
	start := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	return start.UTC(), start.AddDate(0, 0, 1).UTC()
}

// PreviewSchedule returns the next n starts of a schedule.
func (s *Service) PreviewSchedule(sched Schedule, n int) ([]time.Time, Schedule, error) {
	norm, err := sched.Normalize()
	if err != nil {
		return nil, sched, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	out := norm.NextN(s.clock(), n)
	if out == nil {
		out = []time.Time{}
	}
	return out, norm, nil
}

// --- memory ---

func (s *Service) GetMemory(ctx context.Context, taskID string) (*Memory, error) {
	if _, err := s.loadTask(ctx, taskID); err != nil {
		return nil, err
	}
	return loadMemory(s.store.db.QueryRowContext(ctx, `SELECT task_id, version, summary, state, updated_at, updated_by FROM memory WHERE task_id = ?`, taskID))
}

func loadMemory(row scanner) (*Memory, error) {
	var m Memory
	var state string
	var updated int64
	if err := row.Scan(&m.TaskID, &m.Version, &m.Summary, &state, &updated, &m.UpdatedBy); err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	m.State = map[string]json.RawMessage{}
	json.Unmarshal([]byte(state), &m.State)
	m.UpdatedAt = fromMs(updated)
	return &m, nil
}

// MemoryUpdate replaces a task's memory as the user edited it.
type MemoryUpdate struct {
	ExpectedVersion int64                      `json:"expectedVersion"`
	Summary         *string                    `json:"summary,omitempty"`
	State           map[string]json.RawMessage `json:"state,omitempty"`
}

func (s *Service) PutMemory(ctx context.Context, taskID string, up MemoryUpdate) (*Memory, error) {
	if up.Summary != nil && len(*up.Summary) > MaxSummaryBytes {
		return nil, invalid("summary is %d bytes, more than %d", len(*up.Summary), MaxSummaryBytes)
	}
	if up.State != nil {
		if err := checkState(up.State); err != nil {
			return nil, invalid("%v", err)
		}
	}
	now := s.clock()
	err := s.store.tx(ctx, func(tx *sql.Tx) error {
		if _, err := loadTaskTx(tx, taskID); err != nil {
			return err
		}
		m, err := loadMemory(tx.QueryRow(`SELECT task_id, version, summary, state, updated_at, updated_by FROM memory WHERE task_id = ?`, taskID))
		if err != nil {
			return err
		}
		if m.Version != up.ExpectedVersion {
			return fmt.Errorf("%w: memory changed (version %d, you sent %d); reload it", ErrConflict, m.Version, up.ExpectedVersion)
		}
		if up.Summary != nil {
			m.Summary = *up.Summary
		}
		if up.State != nil {
			m.State = up.State
		}
		_, err = tx.Exec(`UPDATE memory SET version = version + 1, summary = ?, state = ?, updated_at = ?, updated_by = 'user' WHERE task_id = ?`,
			m.Summary, mustJSON(m.State), ms(now), taskID)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.changed()
	return s.GetMemory(ctx, taskID)
}

// --- conversation ---

// Messages returns up to limit messages, oldest first, before the message
// with id before (or the newest).
func (s *Service) Messages(ctx context.Context, taskID, before string, limit int) ([]Message, bool, error) {
	if _, err := s.loadTask(ctx, taskID); err != nil {
		return nil, false, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	upper := int64(1 << 62)
	if before != "" {
		if err := s.store.db.QueryRowContext(ctx, `SELECT seq FROM messages WHERE id = ? AND task_id = ?`, before, taskID).Scan(&upper); err != nil {
			if isNoRows(err) {
				return nil, false, ErrNotFound
			}
			return nil, false, err
		}
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT m.id, m.task_id, m.role, m.kind, m.text, COALESCE(m.run_id,''), COALESCE(m.handled_by,''), COALESCE(m.outcome,''), COALESCE(m.question_id,''), m.created_at,
		COALESCE(r.conversation,''), COALESCE(r.session_id,'')
		FROM messages m LEFT JOIN runs r ON r.id = m.run_id AND m.role = 'assistant'
		WHERE m.task_id = ? AND m.seq < ? ORDER BY m.seq DESC LIMIT ?`, taskID, upper, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var created int64
		if err := rows.Scan(&m.ID, &m.TaskID, &m.Role, &m.Kind, &m.Text, &m.RunID, &m.HandledBy, &m.Outcome, &m.QuestionID, &created,
			&m.Conversation, &m.SessionID); err != nil {
			return nil, false, err
		}
		m.CreatedAt = fromMs(created)
		out = append(out, m)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	slices.Reverse(out)
	if out == nil {
		out = []Message{}
	}
	return out, more, rows.Err()
}

func insertMessage(tx *sql.Tx, m *Message) error {
	if m.ID == "" {
		m.ID = newID("msg")
	}
	_, err := tx.Exec(`INSERT INTO messages (id, task_id, role, kind, text, run_id, handled_by, outcome, question_id, created_at) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?)`,
		m.ID, m.TaskID, m.Role, m.Kind, m.Text, m.RunID, m.HandledBy, m.Outcome, m.QuestionID, ms(m.CreatedAt))
	return err
}

// PostResult is what answering a question or asking for a run returns.
type PostResult struct {
	Message *Message `json:"message,omitempty"`
	Run     *Run     `json:"run"`
	// Merged means the request joined a run that was already queued.
	Merged bool `json:"merged,omitempty"`
}

// MarkRead marks the conversation read through messageID (or entirely).
func (s *Service) MarkRead(ctx context.Context, taskID, messageID string) error {
	return s.store.tx(ctx, func(tx *sql.Tx) error {
		if _, err := loadTaskTx(tx, taskID); err != nil {
			return err
		}
		var seq int64
		var err error
		if messageID == "" {
			err = tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE task_id = ?`, taskID).Scan(&seq)
		} else {
			err = tx.QueryRow(`SELECT seq FROM messages WHERE id = ? AND task_id = ?`, messageID, taskID).Scan(&seq)
		}
		if isNoRows(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE tasks SET read_through = MAX(read_through, ?) WHERE id = ?`, seq, taskID)
		return err
	})
}

// --- questions ---

func (s *Service) openQuestion(ctx context.Context, taskID string) (*Question, error) {
	row := s.store.db.QueryRowContext(ctx, `SELECT id, task_id, run_id, text, options, status, created_at, expires_at, answer FROM questions
		WHERE task_id = ? AND status = 'open' AND expires_at > ? ORDER BY created_at DESC LIMIT 1`, taskID, ms(s.clock()))
	q, err := scanQuestion(row)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return q, err
}

func scanQuestion(row scanner) (*Question, error) {
	var q Question
	var options string
	var created, expires int64
	if err := row.Scan(&q.ID, &q.TaskID, &q.RunID, &q.Text, &options, &q.Status, &created, &expires, &q.Answer); err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	json.Unmarshal([]byte(options), &q.Options)
	q.CreatedAt, q.ExpiresAt = fromMs(created), fromMs(expires)
	return &q, nil
}

// AnswerQuestion records the user's answer and queues a run that continues
// from the asking run's checkpoint. The answer is only information for the
// agent: it grants no permission the task does not already have.
func (s *Service) AnswerQuestion(ctx context.Context, questionID, text, requestID string) (*PostResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, invalid("text is empty")
	}
	if len(text) > 32<<10 {
		return nil, invalid("text is longer than 32 KiB")
	}
	now := s.clock()
	var res *PostResult
	err := s.store.tx(ctx, func(tx *sql.Tx) error {
		var err error
		res, _, err = once(tx, requestID, "answer:"+questionID, map[string]string{"question": questionID, "text": text}, now, func() (*PostResult, error) {
			q, err := scanQuestion(tx.QueryRow(`SELECT id, task_id, run_id, text, options, status, created_at, expires_at, answer FROM questions WHERE id = ?`, questionID))
			if err != nil {
				return nil, err
			}
			if _, err := loadTaskTx(tx, q.TaskID); err != nil {
				return nil, err
			}
			switch {
			case q.Status != QuestionOpen:
				return nil, fmt.Errorf("%w: the question is %s", ErrConflict, q.Status)
			case !now.Before(q.ExpiresAt):
				tx.Exec(`UPDATE questions SET status = 'expired' WHERE id = ?`, q.ID)
				return nil, fmt.Errorf("%w: the question expired at %s", ErrConflict, q.ExpiresAt.Format(time.RFC3339))
			}
			var runStatus string
			if err := tx.QueryRow(`SELECT status FROM runs WHERE id = ?`, q.RunID).Scan(&runStatus); err != nil {
				return nil, err
			}
			if runStatus != RunWaitingUser {
				return nil, fmt.Errorf("%w: the run that asked is %s", ErrConflict, runStatus)
			}
			if _, err := tx.Exec(`UPDATE questions SET status = 'answered', answer = ? WHERE id = ?`, text, q.ID); err != nil {
				return nil, err
			}
			m := &Message{TaskID: q.TaskID, Role: RoleUser, Kind: MsgAnswer, Text: text, CreatedAt: now, QuestionID: q.ID}
			if err := insertMessage(tx, m); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(`UPDATE runs SET status = 'completed' WHERE id = ? AND status = 'waiting_user'`, q.RunID); err != nil {
				return nil, err
			}
			run, merged, err := s.enqueueTx(tx, q.TaskID, TriggerAnswer, "answer to: "+truncateBytes(q.Text, 200), nil, true, now)
			if err != nil {
				return nil, err
			}
			if _, err := tx.Exec(`UPDATE runs SET checkpoint = (SELECT checkpoint FROM runs WHERE id = ?) WHERE id = ? AND checkpoint = ''`, q.RunID, run.ID); err != nil {
				return nil, err
			}
			return &PostResult{Message: m, Run: run, Merged: merged}, nil
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	s.changed()
	return res, nil
}

// --- artifacts ---

func (s *Service) Artifacts(ctx context.Context, taskID string) ([]Artifact, error) {
	if _, err := s.loadTask(ctx, taskID); err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT id, task_id, run_id, path, title, created_at FROM artifacts WHERE task_id = ? ORDER BY created_at DESC LIMIT 200`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Artifact{}
	for rows.Next() {
		var a Artifact
		var created int64
		if err := rows.Scan(&a.ID, &a.TaskID, &a.RunID, &a.Path, &a.Title, &created); err != nil {
			return nil, err
		}
		a.CreatedAt = fromMs(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

// Artifact returns one artifact and the task folder it must stay in.
func (s *Service) Artifact(ctx context.Context, id string) (*Artifact, string, error) {
	var a Artifact
	var created int64
	err := s.store.db.QueryRowContext(ctx, `SELECT id, task_id, run_id, path, title, created_at FROM artifacts WHERE id = ?`, id).
		Scan(&a.ID, &a.TaskID, &a.RunID, &a.Path, &a.Title, &created)
	if isNoRows(err) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	a.CreatedAt = fromMs(created)
	t, err := s.loadTask(ctx, a.TaskID)
	if err != nil {
		return nil, "", err
	}
	return &a, t.Cwd, nil
}

// HiddenSessionIDs are the gateway ids ("claude:<id>", "codex:<thread>") of
// resident runs. They are not sessions: the session list leaves out the
// conversations they keep, until one is promoted to a session.
func (s *Service) HiddenSessionIDs(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	rows, err := s.store.db.QueryContext(ctx, `SELECT a.provider, a.native_id FROM attempts a JOIN runs r ON r.id = a.run_id
		WHERE a.native_id <> '' AND r.conversation <> 'promoted'`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p, id string
		if rows.Scan(&p, &id) == nil {
			out[p+":"+id] = true
		}
	}
	return out
}
