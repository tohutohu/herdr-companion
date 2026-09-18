package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// ExitPlanMode opens Claude Code's plan dialog. Verified against Claude Code
// 2.1.276:
//
//	Claude has written up a plan and is ready to execute. Would you like to proceed?
//
//	❯ 1. Yes, auto-accept edits
//	  2. Yes, manually approve edits
//	  3. Tell Claude what to change
//	     shift+tab to approve with this feedback
//
// Rows are chosen with arrows + Enter. The last row is a text field: typing
// replaces its label and Enter rejects the plan with that feedback. Esc
// rejects it without feedback and Claude stays in plan mode. Claude Code adds
// rows in some states (e.g. clearing a full context first), so the rows on
// screen replace the defaults below whenever the dialog is visible.
const toolExitPlanMode = "ExitPlanMode"

const (
	planQuestion      = "Claude has written up a plan and is ready to execute. Would you like to proceed?"
	planKeepPlanning  = "No, keep planning"
	planFeedbackLabel = "Tell Claude what to change"
)

var defaultPlanOptions = []model.Option{
	{Label: "Yes, auto-accept edits"},
	{Label: "Yes, manually approve edits"},
}

func planInteraction(id string) *model.Interaction {
	return &model.Interaction{
		ID:        id,
		Type:      model.InteractionQuestions,
		Kind:      model.KindPlan,
		Title:     "Claude has a plan",
		Supported: true,
		Questions: []model.Question{{
			ID:         "0",
			Type:       model.QuestionSelect,
			Question:   planQuestion,
			Options:    planOptions(defaultPlanOptions),
			AllowOther: true,
			OtherLabel: planFeedbackLabel,
		}},
	}
}

// planOptions appends the Esc choice, which has no row in the dialog.
func planOptions(rows []model.Option) []model.Option {
	out := append([]model.Option{}, rows...)
	return append(out, model.Option{Label: planKeepPlanning, Description: "Reject the plan and stay in plan mode."})
}

// planAnswer summarizes the ExitPlanMode result.
func planAnswer(res toolResult) string {
	text := resultText(res)
	if !res.block.IsError {
		return "Approved"
	}
	if _, fb, ok := strings.Cut(text, "the user said:\n"); ok {
		fb, _, _ = strings.Cut(fb, "\n\nNote:")
		if fb = strings.TrimSpace(fb); fb != "" {
			return model.Truncate(planFeedbackLabel+": "+fb, 500)
		}
	}
	return "Rejected"
}

func resultText(res toolResult) string {
	text := ""
	if len(res.block.Content) > 0 && res.block.Content[0] == '"' {
		json.Unmarshal(res.block.Content, &text)
	} else if inner, ok := decodeBlocks(res.block.Content); ok {
		for _, ib := range inner {
			text += ib.Text
		}
	}
	return text
}

// planFile links the saved plan. It lives in Claude's config dir, outside the
// workspace, so the block keeps the absolute path and names the file in Text.
func planFile(p string) (model.Block, bool) {
	if !filepath.IsAbs(p) {
		return model.Block{}, false
	}
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return model.Block{}, false
	}
	return model.Block{Type: model.BlockFile, Path: p, Text: filepath.Base(p), Size: st.Size()}, true
}

// FileRoots lets the app open saved plans (Claude Code's default plansDirectory).
func (p *Provider) FileRoots() []string {
	return []string{filepath.Join(p.configDir, "plans")}
}

var planRow = regexp.MustCompile(`^\s*(?:❯\s*)?(\d+)\.\s+(.*\S)\s*$`)

// planRows reads the dialog's rows from the screen, without the trailing
// feedback row. ok is false when the dialog is not visible.
func planRows(screen string) (rows []model.Option, ok bool) {
	i := strings.LastIndex(screen, "Would you like to proceed?")
	if i < 0 {
		return nil, false
	}
	var labels []string
	for _, line := range strings.Split(screen[i:], "\n")[1:] {
		m := planRow.FindStringSubmatch(line)
		if m == nil {
			if len(labels) > 0 && strings.TrimSpace(line) != "" && !strings.HasPrefix(strings.TrimSpace(line), "shift+tab") {
				break
			}
			continue
		}
		if n, _ := strconv.Atoi(m[1]); n != len(labels)+1 {
			break
		}
		labels = append(labels, m[2])
	}
	if len(labels) < 2 {
		return nil, false
	}
	for _, l := range labels[:len(labels)-1] {
		rows = append(rows, model.Option{Label: l})
	}
	return rows, true
}

// withScreenPlanRows replaces a pending plan's default rows with the ones on
// screen, so answers map to the rows Claude Code actually shows.
func (p *Provider) withScreenPlanRows(ctx context.Context, msgs []model.Message, live *providers.Live) {
	if !live.Blocked() {
		return
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		for _, b := range msgs[i].Blocks {
			ia := b.Interaction
			if b.Type != model.BlockInteraction || ia.Kind != model.KindPlan || ia.State != model.InteractionPending {
				continue
			}
			screen, err := providers.Screen(ctx, p.term, live.PaneID)
			if err != nil {
				return
			}
			if rows, ok := planRows(screen); ok {
				ia.Questions[0].Options = planOptions(rows)
			}
			return
		}
	}
}

func planKeys(ia *model.Interaction, r model.InteractionResponse) ([]step, error) {
	q := ia.Questions[0]
	a, ok := r.Answers[q.ID]
	if !ok {
		return nil, fmt.Errorf("missing answer for question %s", q.ID)
	}
	// Rows on screen: every option but the Esc one, then the feedback row.
	feedbackRow := len(q.Options) - 1
	if text := singleLine(a.Text); text != "" && len(a.Selected) == 0 {
		return []step{keys(repeat("down", feedbackRow)...), {text: text}, keys("enter")}, nil
	}
	if len(a.Selected) != 1 {
		return nil, fmt.Errorf("missing answer for question %s", q.ID)
	}
	if a.Selected[0] == planKeepPlanning {
		return []step{keys("esc")}, nil
	}
	idx := optionIndex(q, a.Selected[0])
	if idx < 0 {
		return nil, fmt.Errorf("unknown option %q", a.Selected[0])
	}
	return []step{keys(append(repeat("down", idx), "enter")...)}, nil
}
