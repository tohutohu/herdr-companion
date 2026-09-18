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

// When a Plan mode turn ends with a proposed plan, the Codex TUI asks locally
// whether to implement it. The app-server never sees this prompt, so it is
// read from the pane and answered with keys. Verified against Codex 0.154:
//
//	  Implement this plan?
//
//	› 1. Yes, implement this plan          Switch to Default and start coding.
//	  2. Yes, clear context and implement  Fresh thread. Context: 4% used.
//	  3. No, stay in Plan mode             Continue planning with the model.
//
//	  Press enter to confirm or esc to go back
//
// Row 1 switches to Default mode and sends "Implement the plan.".
const (
	planPromptPrefix = "codex-plan:"
	planPromptTitle  = "Implement this plan?"
)

var planPromptRow = regexp.MustCompile(`^\s*(?:›\s*)?(\d+)\.\s+(.+?)(?:\s{2,}(\S.*?))?\s*$`)

// planPromptRows reads the prompt's rows, or ok=false when it is not shown.
func planPromptRows(screen string) (rows []model.Option, ok bool) {
	i := strings.LastIndex(screen, planPromptTitle)
	if i < 0 {
		return nil, false
	}
	for _, line := range strings.Split(screen[i:], "\n")[1:] {
		if strings.Contains(line, "Press enter to confirm") {
			break
		}
		m := planPromptRow.FindStringSubmatch(line)
		if m == nil {
			if strings.TrimSpace(line) != "" && len(rows) > 0 {
				break
			}
			continue
		}
		if n, _ := strconv.Atoi(m[1]); n != len(rows)+1 {
			break
		}
		rows = append(rows, model.Option{Label: m[2], Description: m[3]})
	}
	return rows, len(rows) >= 2
}

// latestPlanItem is the plan item of a finished last turn.
func latestPlanItem(th *Thread) string {
	lt := lastTurn(th)
	if lt == nil || lt.Status != "completed" {
		return ""
	}
	for i := len(lt.Items) - 1; i >= 0; i-- {
		var it itemHead
		if json.Unmarshal(lt.Items[i], &it) == nil && it.Type == "plan" && it.ID != "" {
			return it.ID
		}
	}
	return ""
}

// planPrompt is the pending prompt for the thread's latest plan, or nil when
// the pane does not show it.
func (p *Provider) planPrompt(ctx context.Context, th *Thread, live *providers.Live) *model.Interaction {
	if live == nil {
		return nil
	}
	id := latestPlanItem(th)
	if id == "" {
		return nil
	}
	screen, err := p.visible(ctx, live.PaneID)
	if err != nil {
		return nil
	}
	rows, ok := planPromptRows(screen)
	if !ok {
		return nil
	}
	return &model.Interaction{
		ID:        planPromptPrefix + id,
		Type:      model.InteractionQuestions,
		Kind:      model.KindPlan,
		State:     model.InteractionPending,
		Title:     "Codex has a plan",
		Supported: true,
		Questions: []model.Question{{ID: "0", Type: model.QuestionSelect, Question: planPromptTitle, Options: rows}},
	}
}

func planPromptMessage(th *Thread, ia *model.Interaction) model.Message {
	return model.Message{
		ID:        ia.ID,
		Role:      model.RoleAssistant,
		Timestamp: time.Unix(th.UpdatedAt, 0).UTC(),
		Blocks:    []model.Block{{Type: model.BlockInteraction, Interaction: ia}},
	}
}

func (p *Provider) respondPlan(ctx context.Context, nativeID string, live *providers.Live, r model.InteractionResponse) error {
	if live == nil {
		return providers.ErrNotLive
	}
	p.terminalInputMu.Lock()
	defer p.terminalInputMu.Unlock()
	th, err := p.readThread(ctx, nativeID, true)
	if err != nil {
		return err
	}
	// The prompt must still be on screen: keys sent to the composer would
	// type into it instead.
	ia := p.planPrompt(ctx, th, live)
	if ia == nil || ia.ID != r.InteractionID {
		return providers.ErrInteractionGone
	}
	a := r.Answers["0"]
	if len(a.Selected) != 1 {
		return fmt.Errorf("choose one option")
	}
	idx := -1
	for i, o := range ia.Questions[0].Options {
		if o.Label == a.Selected[0] {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("unknown option %q", a.Selected[0])
	}
	keys := make([]string, 0, idx+1)
	for range idx {
		keys = append(keys, "down")
	}
	return p.term.SendKeys(ctx, live.PaneID, append(keys, "enter")...)
}
