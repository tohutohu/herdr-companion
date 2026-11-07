// Package providers defines the adapter boundary between coding agents
// (Claude Code, Codex) and the provider-neutral model.
package providers

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

var (
	ErrNotFound        = errors.New("session not found")
	ErrNotLive         = errors.New("session is not running; resume it first")
	ErrUnsupported     = errors.New("not supported for this session")
	ErrInteractionGone = errors.New("interaction is no longer pending")
	// ErrRuntimeUnavailable means the agent's runtime (Claude sidecar, Codex
	// app-server) could not be started.
	ErrRuntimeUnavailable = errors.New("agent runtime unavailable")
)

// DirectStart describes a session the gateway starts or resumes itself.
type DirectStart struct {
	Cwd    string
	Model  string
	Effort string
	Mode   string
	// Translation requests English replies for local Japanese display translation.
	Translation bool
	// Resume is the native id of an existing session to continue; empty
	// starts a new one.
	Resume string
	// Prompt is sent as the first message; it may be empty.
	Prompt model.Input
}

// DirectInfo is a directly managed session's runtime state.
type DirectInfo struct {
	NativeID string
	Cwd      string
	// Alive means the runtime process holds the session.
	Alive bool
	// Running means a turn is in progress.
	Running bool
	// Waiting means the agent waits on an approval or a question.
	Waiting bool
	// Model, Effort and Mode are what the runtime runs with now (empty when
	// unknown); RequestedModel / RequestedEffort are changes not yet used by
	// a model response.
	Model, Effort, Mode             string
	RequestedModel, RequestedEffort string
	// ModeID is the id of Mode to resume with, for a runtime whose mode can
	// change without the manager choosing it (empty when unknown).
	ModeID string
	// Turns counts finished turns; LastTurnFailed is the latest one's outcome.
	Turns          int
	LastTurnFailed bool
	// Error is the reason the runtime or the latest turn failed.
	Error string
	// Background is the work the session left running, oldest first.
	Background []model.BackgroundTask
}

// SettingsApply says when a model or effort change takes effect.
const (
	ApplyNextRequest = "next_request" // the next model call, even inside a turn (Claude)
	ApplyNextTurn    = "next_turn"    // the next turn (Codex)
)

// DirectRuntime is implemented by providers whose sessions the gateway runs.
type DirectRuntime interface {
	// DirectStart starts (or resumes) a session and returns its native id.
	DirectStart(ctx context.Context, opts DirectStart) (string, error)
	// DirectStop ends the session's process; its history stays.
	DirectStop(ctx context.Context, nativeID string) error
	DirectInterrupt(ctx context.Context, nativeID string) error
	DirectSetModel(ctx context.Context, nativeID, model string) error
	DirectSetEffort(ctx context.Context, nativeID, effort string) error
	// DirectSetMode sets a mode id from the catalog; "" advances to the next one.
	DirectSetMode(ctx context.Context, nativeID, mode string) error
	// DirectSessions lists the sessions the runtime holds.
	DirectSessions() []DirectInfo
	// SettingsApply is ApplyNextRequest or ApplyNextTurn.
	SettingsApply() string
	// SetDirectListener registers a callback run after any runtime change.
	SetDirectListener(func(nativeID string))
}

// SideTurn is an earlier question and answer of a side conversation.
type SideTurn struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// SideAnswer answers a side question. Synthetic marks an answer the agent
// made up without the model, e.g. to say why it could not answer.
type SideAnswer struct {
	Answer    string `json:"answer"`
	Synthetic bool   `json:"synthetic"`
}

// ErrNoConversation means a side question has no conversation to be
// answered from yet.
var ErrNoConversation = errors.New("the session has no conversation to ask about yet")

// SideQuestioner is a DirectRuntime that answers side questions (Claude
// Code's /btw): from the session's conversation, without tools, alongside a
// running turn, and without adding them to the session's history.
type SideQuestioner interface {
	DirectSideQuestion(ctx context.Context, nativeID, question string, history []SideTurn) (SideAnswer, error)
}

// Runtime states of a live session.
const (
	StateIdle    = "idle"    // no turn yet, or the finished turn was seen
	StateWorking = "working" // a turn is running
	StateBlocked = "blocked" // waiting on an approval or a question
	StateDone    = "done"    // the turn finished and was not seen yet
)

// Live describes a session the gateway runs. Nil means it runs nowhere.
type Live struct {
	// State is one of StateIdle, StateWorking, StateBlocked, StateDone.
	State string
	Cwd   string
	// Dormant marks a session with no process: the gateway restarted or the
	// runtime exited. The next message resumes it in a new process.
	Dormant bool
	// Failed marks a session whose process died during a turn.
	Failed bool
	// Revision changes each time the session finishes a turn, for providers
	// whose history has no completion revision of its own.
	Revision string
}

func (l *Live) Blocked() bool { return l != nil && l.State == StateBlocked }

// FileRooter is implemented by providers that keep files a session links to
// outside its workspace, such as Claude's saved plans. The app may read them.
type FileRooter interface {
	FileRoots() []string
}

// SessionFileRooter is implemented by providers whose sessions link to files
// under directories other than the current cwd, such as the earlier working
// directories recorded in a Claude transcript. The app may read files there.
type SessionFileRooter interface {
	SessionFileRoots(ctx context.Context, nativeID string) []string
}

// Summary is what the session list needs; cheaper than full messages.
type Summary struct {
	NativeID    string
	Cwd         string
	Title       string
	UpdatedAt   time.Time
	LastMessage string
	// LastTurnFailed marks the latest turn as ended by an error.
	LastTurnFailed bool
	// CompletionRevision identifies the latest finished turn and its content,
	// when available. Background results can change it within the same turn;
	// lifecycle fluctuations without new content leave it unchanged.
	CompletionRevision string
	// Pending is the kind of interaction waiting for the user, if any.
	Pending model.InteractionType
	// Status, when set, overrides the runtime's status while the session is live.
	Status model.Status
	// Model is the model the session last used, as the provider names it.
	Model string
	// Effort is the reasoning effort (low, medium, high, ...), if known.
	Effort string
	// Mode is a display label for the permission / collaboration mode.
	Mode string
	// Context is how full the session's context window is, when the provider
	// reports it.
	Context *model.ContextUsage
	// Cost is what the session spent, priced from its token counts.
	Cost *model.Cost
	// PinnedCwd keeps Cwd as the session's directory while it runs, where
	// the runtime's directory would otherwise win: the agent works in a
	// worktree it created without moving its process there.
	PinnedCwd bool
}

// FilteredRecentProvider can skip unwanted native IDs before reading history.
// Filtering does not backfill beyond the provider's normal candidate limit.
type FilteredRecentProvider interface {
	RecentExcluding(ctx context.Context, since time.Time, exclude map[string]bool) ([]Summary, error)
}

// ConversationProvider builds the summary and messages from one history read.
// Providers without it use their separate Summary and Messages methods.
type ConversationProvider interface {
	Conversation(ctx context.Context, nativeID string, live *Live) (*Summary, []model.Message, error)
}

type Provider interface {
	Name() string        // id prefix, e.g. "claude"
	DisplayName() string // e.g. "Claude Code"

	Summary(ctx context.Context, nativeID string, live *Live) (*Summary, error)
	// Recent lists sessions updated since the given time (for offline entries).
	Recent(ctx context.Context, since time.Time) ([]Summary, error)
	Messages(ctx context.Context, nativeID string, live *Live) ([]model.Message, error)
	// Image returns inline image bytes referenced by a message.
	Image(ctx context.Context, nativeID, messageID string, index int) (mime string, data []byte, err error)
	Send(ctx context.Context, nativeID string, live *Live, in model.Input) error
	Respond(ctx context.Context, nativeID string, live *Live, r model.InteractionResponse) error
}

// Forgetter is implemented by providers that cache display reads. Forget
// drops what is cached for one session, so its next read starts from the
// transcript or app-server again.
type Forgetter interface {
	Forget(nativeID string)
}

// ImageURL builds the gateway URL for an inline image of a message.
func ImageURL(sessionID, messageID string, index int) string {
	return "/v1/sessions/" + sessionID + "/messages/" + messageID + "/images/" + itoa(index)
}

// FileImageURL builds the gateway URL for an image file on disk.
func FileImageURL(sessionID, path string) string {
	return "/v1/sessions/" + sessionID + "/files/content?path=" + queryEscape(path)
}

// ModelOption is a model the user can pick when starting a session.
type ModelOption struct {
	ID          string `json:"id"` // the agent's model id
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Default marks the model used when none is given.
	Default bool `json:"default,omitempty"`
	// Efforts are the reasoning efforts this model offers; empty means the
	// catalog's efforts apply.
	Efforts []EffortOption `json:"efforts,omitempty"`
}

// EffortOption is a reasoning effort the user can pick when starting a session.
type EffortOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Default marks the effort used when none is given.
	Default bool `json:"default,omitempty"`
}

// ModeOption is a session mode the user can pick when starting a session.
type ModeOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Default marks the mode a session starts in when none is given.
	Default bool `json:"default,omitempty"`
}

// ModelCatalog is what a provider offers when starting a session.
type ModelCatalog struct {
	Models []ModelOption `json:"models"`
	// Efforts apply when no model is picked, and to models listing none.
	Efforts []EffortOption `json:"efforts,omitempty"`
	// Modes are the session modes a new session can start in; empty means the
	// agent offers no choice at startup.
	Modes []ModeOption `json:"modes,omitempty"`
}

var modelIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/\[\]-]{0,99}$`)

// ValidModelID reports whether id looks like a model id.
func ValidModelID(id string) bool { return modelIDPattern.MatchString(id) }

var effortIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidEffortID reports whether id looks like an effort id.
func ValidEffortID(id string) bool { return effortIDPattern.MatchString(id) }

// Claude Code names its permission modes in camelCase (acceptEdits).
var modeIDPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{0,31}$`)

// ValidModeID reports whether id looks like a mode id.
func ValidModeID(id string) bool { return modeIDPattern.MatchString(id) }

// effortNames are the labels for the efforts both agents share.
var effortNames = map[string]string{
	"low":    "Low",
	"medium": "Medium",
	"high":   "High",
	"xhigh":  "Extra high",
	"max":    "Max",
	"ultra":  "Ultra",
}

// EffortName is the label shown for an effort id.
func EffortName(id string) string {
	if n, ok := effortNames[id]; ok {
		return n
	}
	if id == "" {
		return ""
	}
	return strings.ToUpper(id[:1]) + id[1:]
}

// ModelLister lists the models, efforts and modes a provider offers.
type ModelLister interface {
	Models(ctx context.Context) (ModelCatalog, error)
}

// TrustChecker reports whether the user already trusted a folder in the
// agent itself. A session the gateway runs gets no folder-trust dialog from
// the agent, so the gateway asks before starting in a folder the agent has
// never been trusted in (project hooks and MCP servers run code).
type TrustChecker interface {
	FolderTrusted(cwd string) bool
}

// ParentDirs returns dir and every parent directory, nearest first.
func ParentDirs(dir string) []string {
	var out []string
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		out = append(out, d)
		if d == filepath.Dir(d) {
			return out
		}
	}
}
