package claude

import (
	"context"
	"strings"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// Herdr can miss the blocked lifecycle notification. Confirm an unanswered
// question against the visible pane before closing it or typing a new prompt.
// A transcript alone cannot distinguish a dialog from an interrupted tool.
func (p *Provider) dialogLive(ctx context.Context, id string, live *providers.Live) *providers.Live {
	resolved, _ := p.dialogState(ctx, id, live)
	return resolved
}

func (p *Provider) dialogState(ctx context.Context, id string, live *providers.Live) (*providers.Live, *model.Interaction) {
	if live == nil {
		return live, nil
	}
	// Plans can survive compaction/resume without an unanswered tool call.
	// Inspect every live pane, independently of transcript mode and hooks.
	screen, err := providers.Screen(ctx, p.term, live.PaneID)
	if err != nil {
		return live, nil
	}
	plan := screenPlan(screen)
	if plan != nil {
		resolved := *live
		resolved.HerdrStatus = herdr.StatusBlocked
		return &resolved, plan
	}
	if live.Blocked() || !strings.Contains(screen, dialogFooter) {
		return live, nil
	}
	path, err := p.transcriptPath(id)
	if err != nil {
		return live, nil
	}
	c, err := p.transcript(path, id)
	if err != nil {
		return live, nil
	}
	if !c.dialogsKnown {
		c.dialogs = unansweredDialogs(c.t.entries)
		c.dialogsKnown = true
	}
	dialogs := c.dialogs
	c.mu.Unlock()
	for _, b := range dialogs {
		if dialogOnScreen(b, screen) {
			resolved := *live
			resolved.HerdrStatus = herdr.StatusBlocked
			return &resolved, nil
		}
	}
	return live, nil
}

// Only the latest user turn can still own a dialog. Results are encountered
// before their calls during this reverse scan; meta messages are not prompts.
func unansweredDialogs(entries []*entry) []contentBlock {
	answered := map[string]bool{}
	var dialogs []contentBlock
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.IsSidechain || e.Message == nil {
			continue
		}
		blocks, _ := decodeBlocks(e.Message.Content)
		if e.Type == "user" {
			if len(blocks) > 0 && blocks[0].Type == "tool_result" {
				for _, b := range blocks {
					if b.Type == "tool_result" {
						answered[b.ToolUseID] = true
					}
				}
				continue
			}
			if !e.IsMeta {
				break
			}
		}
		if e.Type == "assistant" {
			for _, b := range blocks {
				if b.Type == "tool_use" && !answered[b.ID] && (b.Name == toolAskUserQuestion || b.Name == toolExitPlanMode) {
					dialogs = append(dialogs, b)
				}
			}
		}
	}
	return dialogs
}

func dialogOnScreen(b contentBlock, screen string) bool {
	if b.Name == toolExitPlanMode {
		_, ok := planRows(screen)
		return ok
	}
	// The footer distinguishes an active dialog from old conversation text.
	footer := strings.LastIndex(screen, dialogFooter)
	if footer < 0 {
		return false
	}
	ia, err := askUserQuestionInteraction(b)
	if err != nil {
		return false
	}
	body := strings.Join(strings.Fields(screen[:footer]), "")
	for _, q := range ia.Questions {
		if question := strings.Join(strings.Fields(q.Question), ""); question != "" && strings.Contains(body, question) {
			return true
		}
	}
	return false
}

func dialogStatus(s *providers.Summary) {
	switch s.Pending {
	case model.InteractionQuestions:
		s.Status = model.StatusWaitingInput
	case model.InteractionApproval:
		s.Status = model.StatusWaitingApproval
	}
}
