package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

const asyncInputPrefix = "codex-async:"

type asyncQuestion struct {
	Title   string   `json:"title"`
	Options []string `json:"options"`
}

// Unlike blocking requestUserInput, async questions survive turn completion
// and are recorded as agentMessage items, even for standalone TUIs.
func latestAsyncQuestion(th *Thread) *model.Interaction {
	for ti := len(th.Turns) - 1; ti >= 0; ti-- {
		items := th.Turns[ti].Items
		for i := len(items) - 1; i >= 0; i-- {
			var it item
			if json.Unmarshal(items[i], &it) != nil || it.Type != "agentMessage" || it.Delivery != "async" || len(it.Questions) == 0 {
				continue
			}
			// The terminal fallback currently handles one question. Do not guess
			// the cursor or answered subset of a multi-question queue.
			if len(it.Questions) != 1 || it.ID == "" || strings.TrimSpace(it.Questions[0].Title) == "" {
				return nil
			}
			q := it.Questions[0]
			mq := model.Question{ID: "0", Type: model.QuestionText, Question: q.Title, AllowOther: true}
			for _, label := range q.Options {
				mq.Options = append(mq.Options, model.Option{Label: label})
			}
			if len(mq.Options) > 0 {
				mq.Type = model.QuestionSelect
			}
			return &model.Interaction{ID: asyncInputPrefix + it.ID, Type: model.InteractionQuestions, State: model.InteractionPending, Title: "Codex needs input", Supported: true, Questions: []model.Question{mq}}
		}
	}
	return nil
}

func (p *Provider) visible(ctx context.Context, pane string) (string, error) {
	return providers.Screen(ctx, p.term, pane)
}

func compactSpace(s string) string { return strings.Join(strings.Fields(s), "") }

func queueBody(screen string) string {
	_, body, ok := strings.Cut(screen, "• Queued follow-up inputs")
	if !ok {
		return ""
	}
	// Use the last rendered queue, never a quotation earlier in the viewport.
	if i := strings.LastIndex(body, "• Queued follow-up inputs"); i >= 0 {
		body = body[i+len("• Queued follow-up inputs"):]
	}
	return body
}

var singleQueuedQuestion = regexp.MustCompile(`(?m)^\s*\? 1 question(?: · [^\n]*)?\s*$`)

func collapsedQuestion(screen string) bool {
	body := queueBody(screen)
	return singleQueuedQuestion.MatchString(body) && strings.Contains(body, "↑ to answer")
}

func focusedQuestion(screen string, q model.Question) bool {
	body := queueBody(screen)
	title := strings.SplitN(strings.TrimSpace(body), "\n\n", 2)[0]
	return strings.Contains(body, "enter submit") && strings.Contains(body, "↓ main prompt") && compactSpace(title) == compactSpace(q.Question)
}

func (p *Provider) asyncInteraction(ctx context.Context, th *Thread, live *providers.Live) *model.Interaction {
	ia := latestAsyncQuestion(th)
	if ia == nil {
		return nil
	}
	screen, err := p.visible(ctx, live.PaneID)
	if err != nil || (!collapsedQuestion(screen) && !focusedQuestion(screen, ia.Questions[0])) {
		return nil
	}
	return ia
}

func waitInput(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}

var selectedOption = regexp.MustCompile(`(?m)^\s*› (\d+)\. ([^\n]*)`)

func emptyAsyncDraft(screen string, q model.Question) bool {
	body := queueBody(screen)
	if len(q.Options) == 0 {
		return strings.Contains(body, "\n  Type your answer\n")
	}
	m := selectedOption.FindStringSubmatch(body)
	return len(m) == 3 && m[1] == strconv.Itoa(len(q.Options)+1) && strings.TrimSpace(m[2]) == "Other"
}

func asyncAnswer(q model.Question, r model.InteractionResponse) (string, int, error) {
	a, ok := r.Answers[q.ID]
	if !ok || len(a.Selected) > 1 {
		return "", 0, fmt.Errorf("one answer is required")
	}
	text := strings.TrimSpace(a.Text)
	if text != "" && len(a.Selected) != 0 {
		return "", 0, fmt.Errorf("choose an option or enter text")
	}
	if text != "" {
		// Do not allow pasted terminal controls or multi-line submissions.
		if strings.ContainsAny(text, "\r\n\x1b") || strings.IndexFunc(text, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			return "", 0, fmt.Errorf("answer must be a single line without control characters")
		}
		return text, len(q.Options) + 1, nil
	}
	if len(a.Selected) == 1 {
		for i, o := range q.Options {
			if o.Label == a.Selected[0] {
				return "", i + 1, nil
			}
		}
	}
	return "", 0, fmt.Errorf("empty or unknown answer")
}

func (p *Provider) respondAsync(ctx context.Context, nativeID string, live *providers.Live, r model.InteractionResponse) error {
	if live == nil {
		return providers.ErrNotLive
	}
	p.terminalInputMu.Lock()
	defer p.terminalInputMu.Unlock()
	th, err := p.readThread(ctx, nativeID, true)
	if err != nil {
		return err
	}
	ia := p.asyncInteraction(ctx, th, live)
	if ia == nil || ia.ID != r.InteractionID {
		return providers.ErrInteractionGone
	}
	q := ia.Questions[0]
	text, target, err := asyncAnswer(q, r)
	if err != nil {
		return err
	}
	screen, err := p.visible(ctx, live.PaneID)
	if err != nil {
		return err
	}
	if collapsedQuestion(screen) {
		if err := p.term.SendKeys(ctx, live.PaneID, "alt+up"); err != nil {
			return err
		}
		if err := waitInput(ctx); err != nil {
			return err
		}
		screen, err = p.visible(ctx, live.PaneID)
		if err != nil {
			return err
		}
	}
	if !focusedQuestion(screen, q) {
		return providers.ErrInteractionGone
	}
	if len(q.Options) > 0 {
		match := selectedOption.FindStringSubmatch(queueBody(screen))
		if len(match) == 0 {
			return providers.ErrInteractionGone
		}
		current, _ := strconv.Atoi(match[1])
		if current < 1 || current > len(q.Options)+1 {
			return providers.ErrInteractionGone
		}
		var keys []string
		for current < target {
			keys = append(keys, "down")
			current++
		}
		for current > target {
			keys = append(keys, "up")
			current--
		}
		if len(keys) > 0 {
			if err := p.term.SendKeys(ctx, live.PaneID, keys...); err != nil {
				return err
			}
			if err := waitInput(ctx); err != nil {
				return err
			}
		}
	}
	if text != "" {
		// Refuse to overwrite an answer the user is already editing.
		screen, err = p.visible(ctx, live.PaneID)
		if err != nil {
			return err
		}
		if !focusedQuestion(screen, q) || !emptyAsyncDraft(screen, q) {
			return fmt.Errorf("question has an existing draft or changed; check the terminal")
		}
		if err := p.term.SendText(ctx, live.PaneID, "\x1b[200~"+text+"\x1b[201~"); err != nil {
			return err
		}
		// Codex treats Enter immediately after paste as another pasted newline.
		if err := waitInput(ctx); err != nil {
			return err
		}
	}
	screen, err = p.visible(ctx, live.PaneID)
	if err != nil {
		return err
	}
	if !focusedQuestion(screen, q) {
		return providers.ErrInteractionGone
	}
	if len(q.Options) > 0 {
		m := selectedOption.FindStringSubmatch(queueBody(screen))
		if len(m) != 3 || m[1] != strconv.Itoa(target) {
			return providers.ErrInteractionGone
		}
	}
	return p.term.SendKeys(ctx, live.PaneID, "enter")
}
