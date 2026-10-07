package claude

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

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

var planRow = regexp.MustCompile(`^\s*(?:❯\s*)?(\d+)\.\s+(.*\S)\s*$`)
var planBodyStart = regexp.MustCompile(`Here\s+is\s+Claude's\s+plan:`)
var planBodyEnd = regexp.MustCompile(`Claude\s+has\s+written\s+up\s+a\s+plan`)

// planRows reads the dialog's rows from the screen, without the trailing
// feedback row. ok is false when the dialog is not visible.
func planRows(screen string) (rows []model.Option, ok bool) {
	lines := strings.Split(screen, "\n")
	// Visible panes preserve soft wraps. Locate the complete heading even
	// when "Would you like to proceed?" spans multiple terminal rows.
	header, found, end := "", -1, -1
	for i, line := range lines {
		header += strings.Join(strings.Fields(line), "")
		if at := strings.LastIndex(header, "Wouldyouliketoproceed?"); at > found {
			found, end = at, i
		}
	}
	if end < 0 {
		return nil, false
	}
	var labels []string
	focused, feedbackHint := false, false
	for _, line := range lines[end+1:] {
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, "shift+tab") {
			feedbackHint = strings.Contains(text, "approve with this feedback")
			continue
		}
		if strings.HasPrefix(text, "ctrl+g") {
			continue
		}
		m := planRow.FindStringSubmatch(line)
		if m == nil {
			// A normal composer after a quoted dialog is not an active plan.
			if strings.HasPrefix(text, "❯") {
				return nil, false
			}
			if len(labels) > 0 && text != "" && !feedbackHint {
				labels[len(labels)-1] += " " + text
			}
			continue
		}
		if n, _ := strconv.Atoi(m[1]); n != len(labels)+1 {
			return nil, false
		}
		focused = focused || strings.HasPrefix(text, "❯")
		labels = append(labels, m[2])
	}
	if len(labels) < 2 || !focused || !feedbackHint {
		return nil, false
	}
	for _, l := range labels[:len(labels)-1] {
		if !strings.HasPrefix(l, "Yes,") {
			return nil, false
		}
		rows = append(rows, model.Option{Label: l})
	}
	return rows, true
}

const screenPlanPrefix = "claude-plan-screen:"

// screenPlan recovers a plan approval even when its tool call has not been
// written to the transcript, or compaction/resume hid the original call.
func screenPlan(screen string) *model.Interaction {
	rows, ok := planRows(screen)
	if !ok {
		return nil
	}
	body := ""
	if start := planBodyStart.FindStringIndex(screen); start != nil {
		plan := screen[start[1]:]
		if end := planBodyEnd.FindStringIndex(plan); end != nil {
			plan = plan[:end[0]]
		}
		var lines []string
		for _, line := range strings.Split(plan, "\n") {
			line = strings.TrimSpace(line)
			if strings.Trim(line, "─╌▔ ") != "" {
				lines = append(lines, line)
			}
		}
		body = strings.Join(lines, "\n")
	}
	identity := strings.Join(strings.Fields(body), "")
	for _, row := range rows {
		identity += "\n" + strings.Join(strings.Fields(row.Label), "")
	}
	// The file name also identifies a plan when a long plan body is above
	// the viewport. Different plans can otherwise have identical choices.
	if _, footer, ok := strings.Cut(screen, "ctrl+g"); ok {
		identity += "\n" + strings.Join(strings.Fields(footer), "")
	}
	id := fmt.Sprintf("%s%x", screenPlanPrefix, sha256.Sum256([]byte(identity)))
	ia := planInteraction(id)
	ia.State = model.InteractionPending
	ia.Detail = model.Truncate(body, 8000)
	ia.Questions[0].Options = planOptions(rows)
	return ia
}

// Prefer the transcript's plan card so its answer remains in history. If it
// is missing, append a screen-derived card with the actual dialog choices.
func withScreenPlan(msgs []model.Message, plan *model.Interaction, updated time.Time) []model.Message {
	if plan == nil {
		return msgs
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		for _, b := range msgs[i].Blocks {
			ia := b.Interaction
			if b.Type != model.BlockInteraction || ia.Kind != model.KindPlan || ia.State != model.InteractionPending {
				continue
			}
			ia.Questions[0].Options = plan.Questions[0].Options
			return msgs
		}
	}
	return append(msgs, model.Message{ID: plan.ID, Role: model.RoleAssistant, Timestamp: updated.UTC(), Blocks: []model.Block{{Type: model.BlockInteraction, Interaction: plan}}})
}

func planFocusedRow(screen string) int {
	focused := 0
	for _, line := range strings.Split(screen, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "❯") {
			continue
		}
		if m := planRow.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			focused = n - 1
		}
	}
	return focused
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
