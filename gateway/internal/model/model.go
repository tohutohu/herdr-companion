// Package model defines the provider-neutral DTOs returned to Android.
// Keep these small: anything provider specific must be converted to text.
package model

import "time"

type Status string

const (
	StatusRunning         Status = "running"
	StatusWaitingInput    Status = "waiting_input"
	StatusWaitingApproval Status = "waiting_approval"
	StatusCompleted       Status = "completed"
	StatusIdle            Status = "idle"
	StatusFailed          Status = "failed"
	StatusOffline         Status = "offline"
)

// Notifiable reports whether entering this status should produce a push.
func (s Status) Notifiable() bool {
	switch s {
	case StatusCompleted, StatusWaitingInput, StatusWaitingApproval, StatusFailed:
		return true
	}
	return false
}

type Session struct {
	ID           string    `json:"id"` // "<provider>:<native-id>"
	Provider     string    `json:"provider"`
	ProviderName string    `json:"providerName"`
	Project      string    `json:"project"`
	Title        string    `json:"title,omitempty"`
	Cwd          string    `json:"cwd,omitempty"`
	Status       Status    `json:"status"`
	UpdatedAt    time.Time `json:"updatedAt"`
	LastMessage  string    `json:"lastMessage,omitempty"`
	PaneID       string    `json:"paneId,omitempty"` // where it currently runs; not an identity
	Model        string    `json:"model,omitempty"`  // last used model, provider naming
	Effort       string    `json:"effort,omitempty"` // reasoning effort: low, medium, high, ...
	Mode         string    `json:"mode,omitempty"`   // display label, e.g. "Plan", "Accept edits"
	CanSend      bool      `json:"canSend"`
	Archived     bool      `json:"archived,omitempty"`
	// Context is how full the model's context window is; nil when unknown.
	Context *ContextUsage `json:"context,omitempty"`
	// Cost is what the session's tokens are worth; nil when nothing is known.
	Cost *Cost `json:"cost,omitempty"`
}

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleSystem    Role = "system"
)

type Message struct {
	ID        string    `json:"id"`
	Role      Role      `json:"role"`
	Timestamp time.Time `json:"timestamp"`
	Blocks    []Block   `json:"blocks"`
}

type BlockType string

const (
	BlockText        BlockType = "text"
	BlockImage       BlockType = "image"
	BlockFile        BlockType = "file"
	BlockInteraction BlockType = "interaction"
)

// Block is a flat union; only fields relevant to Type are set.
// New block kinds (diff, command...) can be added later without breaking clients
// because Android falls back to rendering Text for unknown types.
type Block struct {
	Type        BlockType    `json:"type"`
	Text        string       `json:"text,omitempty"`
	URL         string       `json:"url,omitempty"` // image: gateway-relative URL
	Path        string       `json:"path,omitempty"`
	Line        int          `json:"line,omitempty"`
	Size        int64        `json:"size,omitempty"` // file: bytes
	Interaction *Interaction `json:"interaction,omitempty"`
}

func TextBlock(s string) Block { return Block{Type: BlockText, Text: s} }

type InteractionType string

const (
	InteractionQuestions InteractionType = "questions"
	InteractionApproval  InteractionType = "approval"
)

type InteractionState string

const (
	InteractionPending  InteractionState = "pending"
	InteractionAnswered InteractionState = "answered"
	// InteractionClosed: never answered and no longer actionable (interrupted, or session not blocked).
	InteractionClosed InteractionState = "closed"
)

type Interaction struct {
	ID        string           `json:"id"`
	Type      InteractionType  `json:"type"`
	State     InteractionState `json:"state"`
	Title     string           `json:"title,omitempty"`
	Detail    string           `json:"detail,omitempty"`
	Questions []Question       `json:"questions,omitempty"`
	// Supported is false when the gateway can show but not answer it;
	// Android then offers the terminal fallback.
	Supported bool   `json:"supported"`
	Answer    string `json:"answer,omitempty"`
	// Decisions available for approvals (subset of approve, approve_session, deny).
	Decisions []string `json:"decisions,omitempty"`
}

type QuestionType string

const (
	QuestionSelect      QuestionType = "select"
	QuestionMultiSelect QuestionType = "multiselect"
	QuestionText        QuestionType = "text"
)

type Question struct {
	ID         string       `json:"id"`
	Type       QuestionType `json:"type"`
	Header     string       `json:"header,omitempty"`
	Question   string       `json:"question"`
	Options    []Option     `json:"options,omitempty"`
	AllowOther bool         `json:"allowOther"`
}

type Option struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

const (
	DecisionApprove        = "approve"
	DecisionApproveSession = "approve_session"
	DecisionDeny           = "deny"
)

type Answer struct {
	Selected []string `json:"selected,omitempty"`
	Text     string   `json:"text,omitempty"`
}

type InteractionResponse struct {
	InteractionID string            `json:"interactionId"`
	Answers       map[string]Answer `json:"answers,omitempty"` // question id -> answer
	Decision      string            `json:"decision,omitempty"`
}

// Input is a user message to send to an agent. Images are local file paths
// already resolved from upload ids.
type Input struct {
	Text   string
	Images []string
}
