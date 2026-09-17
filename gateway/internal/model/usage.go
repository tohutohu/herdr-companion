package model

import (
	"math"
	"time"
)

// UsageWindow is one subscription rate-limit window (Claude's 5h / weekly
// buckets, Codex's weekly bucket, ...).
type UsageWindow struct {
	// Key is stable across refreshes: primary, secondary, tertiary or the
	// source's id for extra windows. Android uses it only as a list key.
	Key string `json:"key"`
	// Label is the window length, ready to display ("5h", "7d").
	Label string `json:"label"`
	// Scope narrows the label when the window covers one model only ("Fable only").
	Scope         string     `json:"scope,omitempty"`
	UsedPercent   int        `json:"usedPercent"`
	WindowMinutes int        `json:"windowMinutes,omitempty"`
	ResetsAt      *time.Time `json:"resetsAt,omitempty"`
}

// UsageProvider is one agent's limits. Error is set when only this provider
// could not be read; the others are still returned.
type UsageProvider struct {
	Provider    string        `json:"provider"` // claude, codex
	DisplayName string        `json:"displayName"`
	Plan        string        `json:"plan,omitempty"`
	Account     string        `json:"account,omitempty"`
	Windows     []UsageWindow `json:"windows"`
	UpdatedAt   *time.Time    `json:"updatedAt,omitempty"`
	Error       string        `json:"error,omitempty"`
}

// Usage is the cached snapshot served to Android. Providers keeps the last
// good reading even when the most recent refresh failed, in which case
// Error explains why and FetchedAt still points at the last success.
type Usage struct {
	Providers []UsageProvider `json:"providers"`
	FetchedAt *time.Time      `json:"fetchedAt,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// ContextUsage is how much of a session's context window its conversation
// currently fills. Nil means the agent has not reported it yet (no turn ran,
// or the session predates the reporting).
type ContextUsage struct {
	UsedTokens   int64 `json:"usedTokens"`
	WindowTokens int64 `json:"windowTokens"`
	UsedPercent  int   `json:"usedPercent"`
}

// Cost is what a session's tokens are worth at the providers' list API
// rates. Agents running on a subscription are not billed this money, so it
// reads as "what this conversation would have cost through the API".
type Cost struct {
	USD float64 `json:"usd"`
	// Estimated marks a total the gateway priced from token counts. False
	// means the agent reported the figure itself.
	Estimated bool `json:"estimated"`
}

// NewContextUsage returns nil unless both numbers are known, so clients can
// tell "not reported" from "context is empty".
func NewContextUsage(used, window int64) *ContextUsage {
	if used <= 0 || window <= 0 {
		return nil
	}
	percent := int(math.Round(float64(used) / float64(window) * 100))
	if percent > 100 {
		percent = 100
	}
	return &ContextUsage{UsedTokens: used, WindowTokens: window, UsedPercent: percent}
}
