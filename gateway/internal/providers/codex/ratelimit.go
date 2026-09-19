package codex

import (
	"context"
	"strings"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// Near a usage limit the Codex TUI offers a cheaper model in a popup of its
// own. The app-server never sees it, so it is read from the pane and answered
// with keys like the plan prompt. Verified against Codex 0.155:
//
//	  Approaching rate limits
//	  Switch to gpt-5.6-luna for lower credit usage?
//
//	› 1. Switch to gpt-5.6-luna                 Fast and affordable agentic coding model.
//	  2. Keep current model
//	  3. Keep current model (never show again)  Hide future rate limit reminders about switching models.
//
//	  Press enter to confirm or esc to go back
const (
	rateLimitPromptPrefix = "codex-ratelimit:"
	rateLimitPromptTitle  = "Approaching rate limits"
)

// rateLimitSelectPrompt reads the popup, or ok=false when it is not shown.
// It is the last thing on screen, so a quoted title higher up (a transcript
// mentioning it) does not count.
func rateLimitSelectPrompt(screen string) (selectPrompt, bool) {
	sp, ok := readSelectPrompt(screen, rateLimitPromptTitle)
	if !ok {
		return sp, false
	}
	tail := screen[strings.LastIndex(screen, rateLimitPromptTitle):]
	if !strings.Contains(tail, "Press enter to confirm") {
		return sp, false
	}
	return sp, true
}

// rateLimitPrompt is the pending popup, or nil when the pane does not show it.
func (p *Provider) rateLimitPrompt(ctx context.Context, th *Thread, live *providers.Live) (*model.Interaction, selectPrompt) {
	// Codex shows it once a turn ends; skip the pane read while one runs.
	if live == nil || live.HerdrStatus == herdr.StatusWorking {
		return nil, selectPrompt{}
	}
	screen, err := p.visible(ctx, live.PaneID)
	if err != nil {
		return nil, selectPrompt{}
	}
	sp, ok := rateLimitSelectPrompt(screen)
	if !ok {
		return nil, selectPrompt{}
	}
	// The popup follows a turn; its id changes with the next turn.
	id := th.ID
	if lt := lastTurn(th); lt != nil && lt.ID != "" {
		id = lt.ID
	}
	question := rateLimitPromptTitle
	if len(sp.Lines) > 0 {
		question = strings.Join(sp.Lines, " ")
	}
	return &model.Interaction{
		ID:        rateLimitPromptPrefix + id,
		Type:      model.InteractionQuestions,
		State:     model.InteractionPending,
		Title:     rateLimitPromptTitle,
		Supported: true,
		Questions: []model.Question{{ID: "0", Type: model.QuestionSelect, Question: question, Options: sp.Rows}},
	}, sp
}

func (p *Provider) respondRateLimit(ctx context.Context, nativeID string, live *providers.Live, r model.InteractionResponse) error {
	if live == nil {
		return providers.ErrNotLive
	}
	p.terminalInputMu.Lock()
	defer p.terminalInputMu.Unlock()
	th, err := p.readThread(ctx, nativeID, true)
	if err != nil {
		return err
	}
	// Keys sent after the popup closed would type into the composer.
	ia, sp := p.rateLimitPrompt(ctx, th, live)
	if ia == nil || ia.ID != r.InteractionID {
		return providers.ErrInteractionGone
	}
	return p.answerSelectPrompt(ctx, live, sp, r)
}
