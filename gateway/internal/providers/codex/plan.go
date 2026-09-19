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

var selectPromptRow = regexp.MustCompile(`^\s*(›\s*)?(\d+)\.\s+(.+?)(?:\s{2,}(\S.*?))?\s*$`)

// selectPrompt is a numbered choice popup the Codex TUI draws itself.
type selectPrompt struct {
	// Lines are the non-empty lines between the title and the first row.
	Lines  []string
	Rows   []model.Option
	Cursor int // row the › marker is on
}

// readSelectPrompt reads the last popup titled title, or ok=false when it is
// not shown.
func readSelectPrompt(screen, title string) (sp selectPrompt, ok bool) {
	i := strings.LastIndex(screen, title)
	if i < 0 {
		return sp, false
	}
	for _, line := range strings.Split(screen[i:], "\n")[1:] {
		if strings.Contains(line, "Press enter to confirm") {
			break
		}
		m := selectPromptRow.FindStringSubmatch(line)
		if m == nil {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if len(sp.Rows) > 0 {
				break
			}
			sp.Lines = append(sp.Lines, strings.TrimSpace(line))
			continue
		}
		if n, _ := strconv.Atoi(m[2]); n != len(sp.Rows)+1 {
			break
		}
		if m[1] != "" {
			sp.Cursor = len(sp.Rows)
		}
		sp.Rows = append(sp.Rows, model.Option{Label: m[3], Description: m[4]})
	}
	return sp, len(sp.Rows) >= 2
}

// keys moves the cursor to the row labelled label and confirms it.
func (sp selectPrompt) keys(label string) ([]string, error) {
	idx := -1
	for i, o := range sp.Rows {
		if o.Label == label {
			idx = i
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("unknown option %q", label)
	}
	var keys []string
	for i := sp.Cursor; i < idx; i++ {
		keys = append(keys, "down")
	}
	for i := sp.Cursor; i > idx; i-- {
		keys = append(keys, "up")
	}
	return append(keys, "enter"), nil
}

// planPromptRows reads the prompt's rows, or ok=false when it is not shown.
func planPromptRows(screen string) (rows []model.Option, ok bool) {
	sp, ok := planSelectPrompt(screen)
	return sp.Rows, ok
}

func planSelectPrompt(screen string) (selectPrompt, bool) {
	sp, ok := readSelectPrompt(screen, planPromptTitle)
	return sp, ok && len(sp.Lines) == 0
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
	ia, _ := p.planPromptOnScreen(ctx, th, live)
	return ia
}

func (p *Provider) planPromptOnScreen(ctx context.Context, th *Thread, live *providers.Live) (*model.Interaction, selectPrompt) {
	if live == nil {
		return nil, selectPrompt{}
	}
	id := latestPlanItem(th)
	if id == "" {
		return nil, selectPrompt{}
	}
	screen, err := p.visible(ctx, live.PaneID)
	if err != nil {
		return nil, selectPrompt{}
	}
	sp, ok := planSelectPrompt(screen)
	if !ok {
		return nil, selectPrompt{}
	}
	return &model.Interaction{
		ID:        planPromptPrefix + id,
		Type:      model.InteractionQuestions,
		Kind:      model.KindPlan,
		State:     model.InteractionPending,
		Title:     "Codex has a plan",
		Supported: true,
		Questions: []model.Question{{ID: "0", Type: model.QuestionSelect, Question: planPromptTitle, Options: sp.Rows}},
	}, sp
}

// screenPromptMessage carries a popup read from the pane.
func screenPromptMessage(th *Thread, ia *model.Interaction) model.Message {
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
	ia, sp := p.planPromptOnScreen(ctx, th, live)
	if ia == nil || ia.ID != r.InteractionID {
		return providers.ErrInteractionGone
	}
	return p.answerSelectPrompt(ctx, live, sp, r)
}

// answerSelectPrompt picks the single chosen row of a popup on screen.
func (p *Provider) answerSelectPrompt(ctx context.Context, live *providers.Live, sp selectPrompt, r model.InteractionResponse) error {
	a := r.Answers["0"]
	if len(a.Selected) != 1 {
		return fmt.Errorf("choose one option")
	}
	keys, err := sp.keys(a.Selected[0])
	if err != nil {
		return err
	}
	return p.term.SendKeys(ctx, live.PaneID, keys...)
}
