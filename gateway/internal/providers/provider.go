// Package providers defines the adapter boundary between coding agents
// (Claude Code, Codex, OpenCode, Devin) and the provider-neutral model.
package providers

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

var (
	ErrNotFound        = errors.New("session not found")
	ErrNotLive         = errors.New("session is not running in herdr")
	ErrUnsupported     = errors.New("not supported for this session")
	ErrInteractionGone = errors.New("interaction is no longer pending")
	// ErrNoTerminal is returned for terminal operations on a directly managed
	// session, which has no TUI.
	ErrNoTerminal = errors.New("this session is managed directly by the gateway and has no terminal")
	// ErrNotDirect is returned for runtime operations (model, effort,
	// interrupt) on a session that is not managed directly.
	ErrNotDirect = errors.New("only sessions managed directly by the gateway support this; this one runs in Herdr")
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
	// Turns counts finished turns; LastTurnFailed is the latest one's outcome.
	Turns          int
	LastTurnFailed bool
	// Error is the reason the runtime or the latest turn failed.
	Error string
}

// SettingsApply says when a model or effort change takes effect.
const (
	ApplyNextRequest = "next_request" // the next model call, even inside a turn (Claude)
	ApplyNextTurn    = "next_turn"    // the next turn (Codex)
)

// DirectRuntime is implemented by providers the gateway can run without Herdr.
// Send and Respond of the Provider handle a Live with Direct set.
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

// Live describes where a session currently runs. Nil means it runs nowhere:
// neither in a Herdr pane nor under the gateway's own runtime.
type Live struct {
	// PaneID is the Herdr pane; empty for a directly managed session.
	PaneID string
	// HerdrStatus is Herdr's agent status. For a directly managed session the
	// runtime fills in the equivalent value (working, blocked, done, idle), so
	// status normalization is the same for both backends.
	HerdrStatus string
	Cwd         string
	// Direct marks a session the gateway runs itself (Claude Agent SDK or its
	// own Codex app-server) rather than in a Herdr pane.
	Direct bool
	// Dormant marks a direct session with no process: the gateway restarted
	// or the runtime exited. The next message resumes it in a new process.
	Dormant bool
	// Failed marks a direct session whose process died during a turn.
	Failed bool
	// Revision changes each time a direct session finishes a turn, for
	// providers whose history has no completion revision of its own.
	Revision string
}

func (l *Live) Blocked() bool { return l != nil && l.HerdrStatus == herdr.StatusBlocked }

// IsDirect reports whether l is a directly managed session.
func (l *Live) IsDirect() bool { return l != nil && l.Direct }

// InPane reports whether l is a session in a Herdr pane, whose screen and
// keys may be used.
func (l *Live) InPane() bool { return l != nil && !l.Direct && l.PaneID != "" }

// visibleTerminal reads what a pane shows right now. Herdr's client has it;
// test terminals may not, so it is not part of Terminal.
type visibleTerminal interface {
	ReadVisiblePane(context.Context, string) (*herdr.ReadResult, error)
}

// Screen returns the text a pane currently shows, for dialogs that are only
// on screen and never in a transcript.
func Screen(ctx context.Context, term Terminal, pane string) (string, error) {
	t, ok := term.(visibleTerminal)
	if !ok {
		return "", ErrUnsupported
	}
	r, err := t.ReadVisiblePane(ctx, pane)
	if err != nil {
		return "", err
	}
	if r == nil {
		return "", ErrInteractionGone
	}
	return r.Text, nil
}

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

// Terminal is the Herdr subset adapters may use for PTY fallbacks.
type Terminal interface {
	SendKeys(ctx context.Context, paneID string, keys ...string) error
	SendText(ctx context.Context, paneID, text string) error
	Prompt(ctx context.Context, paneID, text string) error
	ReadPane(ctx context.Context, paneID string, lines int) (*herdr.ReadResult, error)
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
	// Status, when set, overrides Herdr's status while the session has a live pane.
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
	// the pane's directory would otherwise win: the agent works in a
	// worktree it created without moving its process there.
	PinnedCwd bool
}

// FilteredRecentProvider can skip unwanted native IDs before reading history.
// Filtering does not backfill beyond the provider's normal candidate limit.
type FilteredRecentProvider interface {
	RecentExcluding(ctx context.Context, since time.Time, exclude map[string]bool) ([]Summary, error)
}

// LiveSessionValidator checks stored pane references against provider data.
// Shared servers can run hooks with another pane's inherited environment.
type LiveSessionValidator interface {
	ValidateLiveSession(ctx context.Context, nativeID string, live *Live) (bool, error)
}

// ConversationProvider builds the summary and messages from one history read.
// Providers without it use their separate Summary and Messages methods.
type ConversationProvider interface {
	Conversation(ctx context.Context, nativeID string, live *Live) (*Summary, []model.Message, error)
}

type Provider interface {
	Name() string        // id prefix, e.g. "claude"
	DisplayName() string // e.g. "Claude Code"
	// HerdrAgent is the agent label Herdr reports in agent_session.agent.
	HerdrAgent() string

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

// ModeChanger is an optional capability for providers whose live TUI can move
// to its next session mode. Keeping this outside Provider lets providers that
// do not expose a safe mode switch remain unchanged.
type ModeChanger interface {
	CycleMode(ctx context.Context, nativeID string, live *Live) error
}

// LaunchModeSetter is implemented by providers whose starting mode cannot be
// passed on the command line. The launcher calls it on the ready TUI, before
// the first prompt, so the mode already applies to that prompt.
type LaunchModeSetter interface {
	SetLaunchMode(ctx context.Context, paneID, mode string) error
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
	ID          string `json:"id"` // value passed to the CLI's --model
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

// ValidModelID reports whether id is safe to pass as a CLI argument.
func ValidModelID(id string) bool { return modelIDPattern.MatchString(id) }

var effortIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidEffortID reports whether id is safe to pass as a CLI argument.
func ValidEffortID(id string) bool { return effortIDPattern.MatchString(id) }

// Claude Code names its permission modes in camelCase (acceptEdits).
var modeIDPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{0,31}$`)

// ValidModeID reports whether id is safe to pass as a CLI argument.
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

// SessionLocator is implemented by providers whose integration hook cannot
// always report the session to Herdr (Codex TUIs on the shared daemon run
// hooks in the daemon, outside the pane).
type SessionLocator interface {
	// LocateLaunched returns the native id of a session started in cwd at or
	// after since, or "" when none is known.
	LocateLaunched(ctx context.Context, cwd string, since time.Time) string
}

// LaunchOptions are the user's choices for a new session. Model, Effort and
// Mode are empty for the provider's own defaults; Cwd is the pane's working
// directory.
type LaunchOptions struct {
	Model  string
	Effort string
	// Mode is a mode id from the provider's catalog.
	Mode string
	Cwd  string
}

// Launchable providers can be started in a new Herdr pane.
type Launchable interface {
	// LaunchArgs are native CLI arguments passed after Herdr's agent kind.
	LaunchArgs(opts LaunchOptions) []string
	// ResumeArgs are native CLI arguments that reopen an existing session.
	ResumeArgs(nativeID, cwd string) []string
	// Models lists the models and efforts offered when starting a session.
	Models(ctx context.Context) (ModelCatalog, error)
	// StartupKeys returns the keys that accept a folder-trust dialog shown on
	// screen, or nil when the screen is not a known trust dialog.
	StartupKeys(screen string) []string
}

// LaunchPromptSender lets a provider choose how its first prompt is entered
// after the TUI has started. Some TUIs treat Herdr's generic agent.prompt
// transport as pasted content, so providers that need real typing can opt in.
type LaunchPromptSender interface {
	SendLaunchPrompt(ctx context.Context, paneID string, in model.Input) error
}

// StartupSessionReporter is implemented by providers whose integration hook
// reports the session to Herdr as the TUI starts reading input (Claude Code's
// SessionStart). Herdr marks a new agent idle a fixed time after it sees the
// process, whether or not the TUI reads input yet, and a TUI discards what
// arrives before that. The launcher sends their first prompt on the report.
type StartupSessionReporter interface {
	ReportsSessionAtStartup()
}

// PreparedLauncher can create a native session before starting its TUI when
// model selection is an API operation rather than a command-line option.
type PreparedLauncher interface {
	PrepareLaunch(context.Context, LaunchOptions) (args []string, nativeID string, err error)
}

// TrustChecker reports whether the user already trusted a folder in the
// agent itself. A session the gateway runs directly gets no folder-trust
// dialog from the agent, so the gateway asks before starting in a folder the
// agent has never been trusted in (project hooks and MCP servers run code).
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
