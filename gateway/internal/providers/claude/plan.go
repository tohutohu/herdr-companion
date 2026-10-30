package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// ExitPlanMode opens Claude Code's plan dialog. Verified against Claude Code
// 2.1.292:
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
