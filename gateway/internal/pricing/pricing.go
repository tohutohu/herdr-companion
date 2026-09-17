// Package pricing turns token counts into dollars.
//
// Neither agent tells us what a session costs while it runs: Claude Code
// writes its own total only when the session ends, and Codex never reports
// one. What both do record is how many tokens every turn used, so the gateway
// prices those counts itself with the providers' list rates. A session on a
// subscription is not billed that money, so the result is what the same work
// would have cost through the API, not an invoice.
//
// Rates are dollars per million tokens, as published in 2026-09:
// https://docs.claude.com/en/docs/about-claude/pricing and
// https://developers.openai.com/api/docs/pricing. Re-check them when a new
// model appears; an unknown model is left unpriced rather than guessed.
package pricing

import "strings"

const perMillion = 1_000_000

// Rates is what one model charges per million tokens.
type Rates struct {
	Input  float64
	Output float64
	// CacheWrite5m and CacheWrite1h are the premiums for putting a prefix in
	// the cache, per TTL. Zero where the provider writes the cache for free.
	CacheWrite5m float64
	CacheWrite1h float64
	// CacheRead is the discounted rate for a prefix that was already cached.
	CacheRead float64
}

// Tokens is one model's accounting. Input counts only tokens that were billed
// as input: cached ones belong in CacheRead.
type Tokens struct {
	Input        int64
	Output       int64
	CacheWrite5m int64
	CacheWrite1h int64
	CacheRead    int64
}

// USD is what these tokens cost at these rates.
func (r Rates) USD(t Tokens) float64 {
	sum := float64(t.Input)*r.Input +
		float64(t.Output)*r.Output +
		float64(t.CacheWrite5m)*r.CacheWrite5m +
		float64(t.CacheWrite1h)*r.CacheWrite1h +
		float64(t.CacheRead)*r.CacheRead
	return sum / perMillion
}

// anthropic fills in the cache rates Anthropic derives from the input rate:
// a write costs 1.25x for the 5 minute TTL and 2x for the 1 hour one.
func anthropic(input, output, cacheRead float64) Rates {
	return Rates{Input: input, Output: output,
		CacheWrite5m: input * 1.25, CacheWrite1h: input * 2, CacheRead: cacheRead}
}

// openai has no cache write premium; a cached prefix simply bills less.
func openai(input, cacheRead, output float64) Rates {
	return Rates{Input: input, Output: output, CacheRead: cacheRead}
}

var rates = map[string]Rates{
	// Anthropic (Claude Code).
	"claude-fable-5-1":  anthropic(10, 50, 0.25),
	"claude-mythos-5-1": anthropic(10, 50, 0.25),
	"claude-fable-5":    anthropic(10, 50, 1),
	"claude-mythos-5":   anthropic(10, 50, 1),
	"claude-opus-5":     anthropic(5, 25, 0.5),
	"claude-opus-4-8":   anthropic(5, 25, 0.5),
	"claude-opus-4-7":   anthropic(5, 25, 0.5),
	"claude-opus-4-6":   anthropic(5, 25, 0.5),
	"claude-sonnet-5":   anthropic(2, 10, 0.2),
	"claude-sonnet-4-6": anthropic(3, 15, 0.3),
	"claude-haiku-4-5":  anthropic(1, 5, 0.1),

	// OpenAI (Codex). The -codex variants bill as their base model.
	"gpt-6-astra":   openai(10, 1, 50),
	"gpt-5.6-sol":   openai(4, 0.4, 20),
	"gpt-5.6-terra": openai(2, 0.2, 12),
	"gpt-5.6-luna":  openai(0.2, 0.02, 1.2),
	"gpt-5.5":       openai(5, 0.5, 30),
	"gpt-5.4":       openai(2.5, 0.25, 15),
	"gpt-5.4-mini":  openai(0.75, 0.075, 4.5),
	"gpt-5.4-nano":  openai(0.2, 0.02, 1.25),
	"gpt-5.3-codex": openai(1.75, 0.175, 14),
	"gpt-5.2":       openai(1.75, 0.175, 14),
	"gpt-5.2-codex": openai(1.75, 0.175, 14),
	"gpt-5.1":       openai(1.25, 0.125, 10),
	"gpt-5.1-codex": openai(1.25, 0.125, 10),
	"gpt-5":         openai(1.25, 0.125, 10),
	"gpt-5-codex":   openai(1.25, 0.125, 10),
	"gpt-5-mini":    openai(0.25, 0.025, 2),
	"gpt-5-nano":    openai(0.05, 0.005, 0.4),
}

// Lookup finds the rates for a model id as an agent names it. The long
// context suffix Claude Code appends ("claude-opus-5[1m]") and a dated
// snapshot ("claude-haiku-4-5-20251001") price as the base model.
func Lookup(modelID string) (Rates, bool) {
	r, ok := rates[normalize(modelID)]
	return r, ok
}

func normalize(modelID string) string {
	id, _, _ := strings.Cut(modelID, "[")
	if base, date, ok := cutLast(id, "-"); ok && isDate(date) {
		id = base
	}
	return id
}

func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

func isDate(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Total prices a session's usage per model. ok is false when nothing could be
// priced, which means either no turn has run or every model is unknown to the
// table above; showing no cost is better than showing a wrong one.
func Total(usage map[string]Tokens) (usd float64, ok bool) {
	for modelID, t := range usage {
		r, found := Lookup(modelID)
		if !found {
			continue
		}
		usd += r.USD(t)
		ok = true
	}
	return usd, ok
}
