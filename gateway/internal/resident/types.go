// Package resident runs the user's standing tasks ("常駐"): instructions the
// gateway carries out on a schedule or on request, each with its own
// conversation, memory, wake-ups, run history and notifications.
//
// It is independent of the session runtime: runs are identified by run id,
// use their own Claude sidecar and Codex app-server, and are never registered
// with internal/runtime, direct-sessions.json or the session watcher. The
// gateway's other state stays provider-owned; this package keeps its own
// SQLite database (resident.db in the state directory) as the source of
// truth for tasks.
package resident

import (
	"encoding/json"
	"time"
)

// Providers a task can run with.
const (
	ProviderClaude = "claude"
	ProviderCodex  = "codex"
)

// Permission profiles. They are enforced by the provider's own permission
// machinery (Claude Code's permission mode and tool rules with every other
// request denied; Codex's sandbox with approvals off), never by prompt text.
const (
	ProfileReadOnly       = "read_only"
	ProfileWorkspaceWrite = "workspace_write"
)

// Permissions is what an unattended run may do.
type Permissions struct {
	Profile string `json:"profile"`
	// Network is ignored: runs may always reach the network (Claude:
	// WebSearch/WebFetch; Codex: sandbox network access). Kept so older
	// clients that send it are not refused.
	Network bool `json:"network,omitempty"`
	// AllowedTools are extra Claude Code permission rules for the run, such
	// as "Bash(gh pr view:*)" or "mcp__github__get_issue". Codex has no
	// per-command rules; they are refused for Codex tasks.
	AllowedTools []string `json:"allowedTools"`
}

// Notify says when the gateway pushes a run's report.
type Notify struct {
	// Mode: "changes" (outcome changed and the agent asked), "always"
	// (every changed outcome) or "never". Questions and failures follow
	// OnQuestion / OnFailure.
	Mode       string `json:"mode"`
	OnFailure  bool   `json:"onFailure"`
	OnQuestion bool   `json:"onQuestion"`
	// CooldownMinutes suppresses change notifications that follow another
	// one of the task sooner than this.
	CooldownMinutes int `json:"cooldownMinutes"`
	// QuietStart / QuietEnd ("HH:MM", task timezone) defer notifications
	// until the quiet hours end. Empty disables them.
	QuietStart string `json:"quietStart,omitempty"`
	QuietEnd   string `json:"quietEnd,omitempty"`
}

// Limits bound a task's runs.
type Limits struct {
	MaxRunMinutes int `json:"maxRunMinutes"`
	// MaxRunsPerDay caps automatic starts (schedule and self wake-ups) per
	// calendar day in the task's timezone. Manual runs and follow-ups count
	// toward it but are never refused.
	MaxRunsPerDay int `json:"maxRunsPerDay"`
	// ExpiresAt ends the task's automatic runs; self wake-ups after it are
	// refused.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// Settings are a task's user-editable configuration. Every change bumps
// the task's version; a run keeps the settings it started with.
type Settings struct {
	Name        string      `json:"name"`
	Instruction string      `json:"instruction"`
	Provider    string      `json:"provider"`
	Model       string      `json:"model,omitempty"`
	Effort      string      `json:"effort,omitempty"`
	Cwd         string      `json:"cwd"`
	Schedule    Schedule    `json:"schedule"`
	Permissions Permissions `json:"permissions"`
	Notify      Notify      `json:"notify"`
	Limits      Limits      `json:"limits"`
	// Precheck, when set, decides whether an automatic run needs the agent.
	Precheck *Precheck `json:"precheck,omitempty"`
}

// Task states shown in the list.
const (
	TaskIdle           = "idle"
	TaskQueued         = "queued"
	TaskRunning        = "running"
	TaskWaitingUser    = "waiting_user"
	TaskNeedsAttention = "needs_attention"
	TaskPaused         = "paused"
)

// Task is a resident task with its derived status.
type Task struct {
	ID       string `json:"id"`
	Settings        // flattened
	Enabled  bool   `json:"enabled"`
	// PausedUntil is when a paused task resumes on its own; nil while
	// enabled or paused until the user resumes it.
	PausedUntil *time.Time `json:"pausedUntil,omitempty"`
	Version     int64      `json:"version"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	// TrustedAt records the user's confirmation that the folder's project
	// settings (hooks, MCP servers) may run unattended.
	TrustedAt *time.Time `json:"trustedAt,omitempty"`

	State           string     `json:"state"`
	NextRunAt       *time.Time `json:"nextRunAt,omitempty"`
	NextWakeReason  string     `json:"nextWakeReason,omitempty"`
	LastSuccessAt   *time.Time `json:"lastSuccessAt,omitempty"`
	LastRun         *RunBrief  `json:"lastRun,omitempty"`
	LatestReport    *Report    `json:"latestReport,omitempty"`
	PendingQuestion *Question  `json:"pendingQuestion,omitempty"`
	Unread          int        `json:"unread"`
	RunsToday       int        `json:"runsToday"`
	Attention       string     `json:"attention,omitempty"`
}

// RunBrief summarizes a run for the task list.
type RunBrief struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	Outcome   string     `json:"outcome,omitempty"`
	Summary   string     `json:"summary,omitempty"`
	Error     string     `json:"error,omitempty"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

// Report is the latest important report of a task.
type Report struct {
	RunID   string    `json:"runId"`
	Outcome string    `json:"outcome"`
	Summary string    `json:"summary"`
	At      time.Time `json:"at"`
}

// Message roles in a task's conversation.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
)

// Message kinds.
const (
	MsgInstruction = "instruction" // a follow-up instruction (no longer sent; kept for old conversations)
	MsgAnswer      = "answer"      // the user's answer to a question
	MsgReport      = "report"      // a run's report
	MsgQuestion    = "question"    // the agent asks the user
	MsgNotice      = "notice"      // failures and other gateway notes
)

// Message is one entry of a task's conversation.
type Message struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"taskId"`
	Role      string    `json:"role"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text"`
	RunID     string    `json:"runId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	// HandledBy is the run that took a user message into its context.
	HandledBy string `json:"handledBy,omitempty"`
	Outcome   string `json:"outcome,omitempty"`
	// QuestionID links a question message to its question.
	QuestionID string `json:"questionId,omitempty"`
	// Conversation and SessionID are the run's (see Run), so a report can
	// offer to continue in a session.
	Conversation string `json:"conversation,omitempty"`
	SessionID    string `json:"sessionId,omitempty"`
}

// Memory is a task's state carried from run to run.
type Memory struct {
	TaskID    string                     `json:"taskId"`
	Version   int64                      `json:"version"`
	Summary   string                     `json:"summary"`
	State     map[string]json.RawMessage `json:"state"`
	UpdatedAt time.Time                  `json:"updatedAt"`
	// UpdatedBy is "user" or the run id that wrote it.
	UpdatedBy string `json:"updatedBy,omitempty"`
}

// Memory limits.
const (
	MaxSummaryBytes = 8 << 10
	MaxStateBytes   = 64 << 10
	MaxStateKeys    = 256
)

// Run statuses.
const (
	RunQueued              = "queued"
	RunRunning             = "running"
	RunCompleted           = "completed"
	RunWaitingUser         = "waiting_user"
	RunFailed              = "failed"
	RunCancelled           = "cancelled"
	RunTimedOut            = "timed_out"
	RunNeedsReconciliation = "needs_reconciliation"
	RunSkipped             = "skipped"
	// RunDismissed is a needs_reconciliation run the user looked at and closed.
	RunDismissed = "dismissed"
)

// Outcomes of the result contract.
const (
	OutcomeChanged   = "changed"
	OutcomeUnchanged = "unchanged"
	OutcomeBlocked   = "blocked"
	OutcomeFailed    = "failed"
)

// Triggers say why a run started.
const (
	TriggerSchedule = "schedule"
	TriggerSelf     = "self_wake"
	TriggerManual   = "manual"
	TriggerFollowUp = "follow_up" // no longer queued; kept for old runs
	TriggerAnswer   = "answer"
	TriggerCatchUp  = "catch_up"
)

// Error kinds of a failed run.
const (
	ErrKindContract    = "contract"   // no valid result
	ErrKindProvider    = "provider"   // the agent or its runtime failed
	ErrKindPermission  = "permission" // a needed permission was denied
	ErrKindTimeout     = "timeout"
	ErrKindInterrupted = "interrupted" // the gateway stopped during the run
	ErrKindConfig      = "config"      // the task cannot run as configured
	ErrKindAgent       = "agent"       // the agent reported outcome failed
	ErrKindPrecheck    = "precheck"    // the precheck command failed
)

// Run is one execution of a task.
type Run struct {
	ID           string     `json:"id"`
	TaskID       string     `json:"taskId"`
	Status       string     `json:"status"`
	Triggers     []string   `json:"triggers"`
	Reason       string     `json:"reason,omitempty"`
	QueuedAt     time.Time  `json:"queuedAt"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	EndedAt      *time.Time `json:"endedAt,omitempty"`
	ScheduledFor *time.Time `json:"scheduledFor,omitempty"`
	Outcome      string     `json:"outcome,omitempty"`
	Summary      string     `json:"summary,omitempty"`
	Report       string     `json:"report,omitempty"`
	ErrorKind    string     `json:"errorKind,omitempty"`
	Error        string     `json:"error,omitempty"`
	// Settings are the task settings the run started with.
	SettingsVersion int64      `json:"settingsVersion"`
	Settings        *Settings  `json:"settings,omitempty"`
	MemoryVersion   int64      `json:"memoryVersion,omitempty"`
	Provider        string     `json:"provider,omitempty"`
	Model           string     `json:"model,omitempty"`
	Effort          string     `json:"effort,omitempty"`
	Usage           *Usage     `json:"usage,omitempty"`
	Warnings        []string   `json:"warnings,omitempty"`
	NotificationKey string     `json:"notificationKey,omitempty"`
	NotifyDecision  string     `json:"notifyDecision,omitempty"`
	NextWakeAt      *time.Time `json:"nextWakeAt,omitempty"`
	// Interactive runs were asked for by the user (manual, answer): their
	// reply goes to the conversation even when unchanged.
	Interactive bool `json:"interactive"`
	// Conversation says whether the agent's conversation of the run is
	// kept and can become a session ("kept") or already is one ("promoted",
	// SessionID); "" for runs from before conversations were kept.
	Conversation string     `json:"conversation,omitempty"`
	SessionID    string     `json:"sessionId,omitempty"`
	Attempts     []Attempt  `json:"attempts,omitempty"`
	Artifacts    []Artifact `json:"artifacts,omitempty"`
	Question     *Question  `json:"question,omitempty"`
}

// Conversation states of a run.
const (
	ConversationKept     = "kept"
	ConversationPromoted = "promoted"
)

// Usage is what a run consumed, as the provider reported it.
type Usage struct {
	InputTokens       int64   `json:"inputTokens,omitempty"`
	OutputTokens      int64   `json:"outputTokens,omitempty"`
	CachedInputTokens int64   `json:"cachedInputTokens,omitempty"`
	CostUSD           float64 `json:"costUsd,omitempty"`
	DurationMs        int64   `json:"durationMs,omitempty"`
	NumTurns          int     `json:"numTurns,omitempty"`
}

// Attempt phases. An attempt is persisted before anything is sent to an
// agent; the phase says how far it got, which decides what a restart may do.
const (
	PhasePreparing = "preparing" // nothing sent: a restart may queue the run again
	PhaseSending   = "sending"   // the prompt may have reached the agent
	PhaseRunning   = "running"
	PhaseFinished  = "finished"
	PhaseFailed    = "failed"
	PhaseCancelled = "cancelled"
	PhaseUnknown   = "unknown" // cut off: the outcome is not known
	PhaseAbandoned = "abandoned"
)

// Attempt is one provider invocation of a run. Runs never retry on their
// own, so a run has one attempt unless it was queued again before its
// prompt was sent.
type Attempt struct {
	ID        string     `json:"id"`
	RunID     string     `json:"runId"`
	Phase     string     `json:"phase"`
	Provider  string     `json:"provider"`
	NativeID  string     `json:"nativeId,omitempty"`
	Model     string     `json:"model,omitempty"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	Error     string     `json:"error,omitempty"`
	// HasResult means the agent's raw result was saved, so the run can be
	// finalized from it after a restart.
	HasResult bool `json:"hasResult,omitempty"`
}

// Question is something a run asked the user.
type Question struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"taskId"`
	RunID     string    `json:"runId"`
	Text      string    `json:"text"`
	Options   []string  `json:"options,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Answer    string    `json:"answer,omitempty"`
}

// Question statuses.
const (
	QuestionOpen       = "open"
	QuestionAnswered   = "answered"
	QuestionExpired    = "expired"
	QuestionSuperseded = "superseded"
)

// Artifact is a file a run reported, checked to be inside the task's folder.
type Artifact struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"taskId"`
	RunID     string    `json:"runId"`
	Path      string    `json:"path"`
	Title     string    `json:"title,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// Wakeup kinds and statuses.
const (
	WakeSchedule = "schedule"
	WakeSelf     = "self"

	WakePending   = "pending"
	WakeFired     = "fired"
	WakeSkipped   = "skipped"
	WakeCancelled = "cancelled"
	WakeMerged    = "merged"
)

// Wakeup is a pending or past automatic start.
type Wakeup struct {
	ID              string    `json:"id"`
	TaskID          string    `json:"taskId"`
	Kind            string    `json:"kind"`
	DueAt           time.Time `json:"dueAt"`
	Status          string    `json:"status"`
	SettingsVersion int64     `json:"settingsVersion"`
	Reason          string    `json:"reason,omitempty"`
	RunID           string    `json:"runId,omitempty"`
}

// Notification kinds.
const (
	NotifyChange   = "change"
	NotifyQuestion = "question"
	NotifyFailure  = "failure"
)

// Notification statuses.
const (
	NotifPending    = "pending"
	NotifSent       = "sent"
	NotifFailed     = "failed"
	NotifSuppressed = "suppressed"
)

// Notification is an outbox entry. Its id is stable across retries, so the
// app replaces rather than repeats a notification delivered twice.
type Notification struct {
	ID        string     `json:"id"`
	TaskID    string     `json:"taskId"`
	RunID     string     `json:"runId"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Key       string     `json:"key,omitempty"`
	Status    string     `json:"status"`
	Reason    string     `json:"reason,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	NotBefore time.Time  `json:"notBefore"`
	Attempts  int        `json:"attempts"`
	SentAt    *time.Time `json:"sentAt,omitempty"`
	LastError string     `json:"lastError,omitempty"`
}
