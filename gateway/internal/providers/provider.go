// Package providers defines the adapter boundary between coding agents
// (Claude Code, Codex, later OpenCode) and the provider-neutral model.
package providers

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

var (
	ErrNotFound        = errors.New("session not found")
	ErrNotLive         = errors.New("session is not running in herdr")
	ErrUnsupported     = errors.New("not supported for this session")
	ErrInteractionGone = errors.New("interaction is no longer pending")
)

// Live describes where a session currently runs. Nil means not in Herdr.
type Live struct {
	PaneID      string
	HerdrStatus string
	Cwd         string
}

func (l *Live) Blocked() bool { return l != nil && l.HerdrStatus == herdr.StatusBlocked }

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
	// Pending is the kind of interaction waiting for the user, if any.
	Pending model.InteractionType
	// Status, when set, is an authoritative provider status (e.g. Codex daemon).
	Status model.Status
	// Model is the model the session last used, as the provider names it.
	Model string
	// Effort is the reasoning effort (low, medium, high, ...), if known.
	Effort string
	// Mode is a display label for the permission / collaboration mode.
	Mode string
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
}

var modelIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/\[\]-]{0,99}$`)

// ValidModelID reports whether id is safe to pass as a CLI argument.
func ValidModelID(id string) bool { return modelIDPattern.MatchString(id) }

// SessionLocator is implemented by providers whose integration hook cannot
// always report the session to Herdr (Codex TUIs on the shared daemon run
// hooks in the daemon, outside the pane).
type SessionLocator interface {
	// LocateLaunched returns the native id of a session started in cwd at or
	// after since, or "" when none is known.
	LocateLaunched(ctx context.Context, cwd string, since time.Time) string
}

// Launchable providers can be started in a new Herdr pane.
type Launchable interface {
	// LaunchArgs are native CLI arguments passed after Herdr's agent kind.
	// modelID is empty for the provider's default model; cwd is the pane's
	// working directory.
	LaunchArgs(modelID, cwd string) []string
	// ResumeArgs are native CLI arguments that reopen an existing session.
	ResumeArgs(nativeID, cwd string) []string
	// Models lists the models offered when starting a session.
	Models(ctx context.Context) ([]ModelOption, error)
	// StartupKeys returns the keys that accept a folder-trust dialog shown on
	// screen, or nil when the screen is not a known trust dialog.
	StartupKeys(screen string) []string
}
