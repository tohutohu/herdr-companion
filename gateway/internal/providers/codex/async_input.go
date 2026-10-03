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

// Async questions can outlive their originating turn. Keep each unanswered
// question separately; the visible queue selects the card to show next.
func pendingAsyncQuestions(th *Thread) []*model.Interaction {
	type entry struct {
		call string
		card *model.Interaction
	}
	var pending []entry
	answered := map[string]map[int]bool{}
	for _, turn := range th.Turns {
		for _, raw := range turn.Items {
			var it item
			if json.Unmarshal(raw, &it) != nil {
				continue
			}
			if it.Type == "userMessage" {
				for _, content := range it.Content {
					if content.Type != "text" {
						continue
					}
					for _, reply := range parseAsyncReplies(content.Text) {
						if answered[reply.callID] == nil {
							answered[reply.callID] = map[int]bool{}
						}
						answered[reply.callID][reply.index] = true
					}
				}
			}
			if it.Type != "agentMessage" || it.Delivery != "async" || it.ID == "" {
				continue
			}
			for index, q := range it.Questions {
				if strings.TrimSpace(q.Title) == "" {
					continue
				}
				mq := model.Question{ID: strconv.Itoa(index), Type: model.QuestionText, Question: q.Title, AllowOther: true}
				for _, label := range q.Options {
					mq.Options = append(mq.Options, model.Option{Label: label})
				}
				if len(mq.Options) > 0 {
					mq.Type = model.QuestionSelect
				}
				id := asyncInputPrefix + it.ID
				if len(it.Questions) > 1 {
					id += ":" + strconv.Itoa(index)
				}
				pending = append(pending, entry{it.ID, &model.Interaction{ID: id, Type: model.InteractionQuestions, State: model.InteractionPending, Title: "Codex needs input", Supported: true, Questions: []model.Question{mq}}})
			}
		}
	}
	var out []*model.Interaction
	for _, q := range pending {
		index, _ := strconv.Atoi(q.card.Questions[0].ID)
		if !answered[q.call][index] {
			out = append(out, q.card)
		}
	}
	return out
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

var queuedQuestionCount = regexp.MustCompile(`(?m)^\s*\? ([1-9][0-9]*) questions?(?: · [^\n]*)?\s*$`)
var queuePosition = regexp.MustCompile(`^([1-9][0-9]*) of ([1-9][0-9]*)$`)

// Codex 0.160 changed the queue shortcut from Alt+Up to Shift+Left.
func openQueueKey(screen string) string {
	body := queueBody(screen)
	if !queuedQuestionCount.MatchString(body) {
		return ""
	}
	if strings.Contains(body, "shift+← to answer") {
		return "shift+left"
	}
	if strings.Contains(body, "↑ to answer") {
		return "alt+up"
	}
	return ""
}

func collapsedQuestion(screen string) bool { return openQueueKey(screen) != "" }

func focusedQueue(screen string) (title string, position, count int) {
	body := queueBody(screen)
	if !strings.Contains(body, "enter submit") || (!strings.Contains(body, "↓ main prompt") && !strings.Contains(body, "shift+→ main prompt")) {
		return "", 0, 0
	}
	body = strings.TrimSpace(body)
	position, count = 1, 1
	first, rest, _ := strings.Cut(body, "\n")
	if m := queuePosition.FindStringSubmatch(strings.TrimSpace(first)); len(m) == 3 {
		position, _ = strconv.Atoi(m[1])
		count, _ = strconv.Atoi(m[2])
		if position > count {
			return "", 0, 0
		}
		body = strings.TrimSpace(rest)
	}
	title = strings.SplitN(body, "\n\n", 2)[0]
	return title, position, count
}

func focusedQuestion(screen string, q model.Question) bool {
	title, _, count := focusedQueue(screen)
	return count > 0 && compactSpace(title) == compactSpace(q.Question)
}

func asyncQuestionOnScreen(pending []*model.Interaction, screen string) *model.Interaction {
	title, position, count := focusedQueue(screen)
	if collapsedQuestion(screen) {
		m := queuedQuestionCount.FindStringSubmatch(queueBody(screen))
		count, _ = strconv.Atoi(m[1])
		position = 1
	}
	if count < 1 || count > len(pending) {
		return nil
	}
	// Old unanswered history can remain after Codex discards a queue. Only
	// the visible queue's newest questions are eligible, never that history.
	ia := pending[len(pending)-count+position-1]
	if title != "" && compactSpace(title) != compactSpace(ia.Questions[0].Question) {
		// The user may have skipped or answered another queue entry before the
		// corresponding history is persisted. Resolve the visible title instead.
		ia = nil
		for i := len(pending) - 1; i >= 0; i-- {
			if compactSpace(pending[i].Questions[0].Question) == compactSpace(title) {
				ia = pending[i]
				break
			}
		}
	}
	return ia
}

func (p *Provider) asyncInteraction(ctx context.Context, th *Thread, live *providers.Live) *model.Interaction {
	pending := pendingAsyncQuestions(th)
	if len(pending) == 0 {
		return nil
	}
	screen, err := p.visible(ctx, live.PaneID)
	if err != nil {
		return nil
	}
	return asyncQuestionOnScreen(pending, screen)
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
	if key := openQueueKey(screen); key != "" {
		if err := p.term.SendKeys(ctx, live.PaneID, key); err != nil {
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
