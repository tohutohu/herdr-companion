package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// planLines is a plan-mode turn ending in ExitPlanMode, plus an optional result.
func planLines(planPath string, result ...string) []string {
	ls := []string{
		`{"type":"user","uuid":"u1","permissionMode":"plan","timestamp":"2026-09-18T00:00:00Z","message":{"role":"user","content":"plan it"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-09-18T00:00:01Z","message":{"role":"assistant","model":"claude-sonnet-5","content":[{"type":"tool_use","id":"toolu_plan","name":"ExitPlanMode","input":{"plan":"# Add subtract","planFilePath":"` + planPath + `"}}]}}`,
	}
	for _, r := range result {
		ls = append(ls, `{"type":"user","uuid":"u2","timestamp":"2026-09-18T00:00:02Z","message":{"role":"user","content":[`+r+`]}}`)
	}
	return ls
}

func decodeLines(t *testing.T, ls []string) *Transcript {
	t.Helper()
	tr, err := Decode(strings.NewReader(strings.Join(ls, "\n")), "claude:test", deadletter.Nop{})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func planBlocks(msgs []model.Message) (plan string, file *model.Block, ia *model.Interaction) {
	for _, m := range msgs {
		for i, b := range m.Blocks {
			switch {
			case b.Type == model.BlockText && strings.HasPrefix(b.Text, "Plan:\n"):
				plan = b.Text
			case b.Type == model.BlockFile:
				file = &m.Blocks[i]
			case b.Type == model.BlockInteraction:
				ia = b.Interaction
			}
		}
	}
	return
}

func TestClaudeのプラン承認は選択肢つきのプランカードになりプランファイルも開ける(t *testing.T) {
	planPath := filepath.Join(t.TempDir(), "plan-add-subtract.md")
	os.WriteFile(planPath, []byte("# Add subtract\n"), 0o644)
	tr := decodeLines(t, planLines(planPath))

	blocked := &providers.Live{PaneID: "w1:p1", HerdrStatus: herdr.StatusBlocked}
	plan, file, ia := planBlocks(tr.Messages(ParseOptions{SessionID: "claude:test", Live: blocked}))
	if plan != "Plan:\n# Add subtract" {
		t.Errorf("plan text = %q", plan)
	}
	if file == nil || file.Path != planPath || file.Text != "plan-add-subtract.md" || file.Size != 15 {
		t.Errorf("plan file block = %+v", file)
	}
	if ia == nil || ia.Kind != model.KindPlan || ia.Type != model.InteractionQuestions || ia.State != model.InteractionPending || ia.Detail != "" {
		t.Fatalf("plan interaction = %+v", ia)
	}
	var labels []string
	for _, o := range ia.Questions[0].Options {
		labels = append(labels, o.Label)
	}
	if got := strings.Join(labels, "|"); got != "Yes, auto-accept edits|Yes, manually approve edits|No, keep planning" {
		t.Errorf("options = %s", got)
	}
	if q := ia.Questions[0]; !q.AllowOther || q.OtherLabel != "Tell Claude what to change" {
		t.Errorf("feedback choice = %+v", q)
	}
	// A plan waiting for the go-ahead is an approval for the session status.
	if s := tr.summary(ParseOptions{Live: blocked}); s.Pending != model.InteractionApproval {
		t.Errorf("summary pending = %q", s.Pending)
	}
}

func TestClaudeのプラン承認の結果が回答として残る(t *testing.T) {
	for result, want := range map[string]string{
		`{"type":"tool_result","tool_use_id":"toolu_plan","content":"User has approved your plan. You can now start coding."}`: "Approved",
		`{"type":"tool_result","tool_use_id":"toolu_plan","is_error":true,"content":"The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). To tell you how to proceed, the user said:\nAlso add a type hint\n\nNote: The user's next message may contain a correction or preference."}`: "Tell Claude what to change: Also add a type hint",
		`{"type":"tool_result","tool_use_id":"toolu_plan","is_error":true,"content":"The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing and wait for the user to tell you how to proceed."}`:                                                                 "Rejected",
	} {
		_, file, ia := planBlocks(decodeLines(t, planLines("/nonexistent/plan.md", result)).Messages(ParseOptions{SessionID: "claude:test"}))
		if ia == nil || ia.State != model.InteractionAnswered || ia.Answer != want {
			t.Errorf("answer = %+v, want %q", ia, want)
		}
		if file != nil {
			t.Errorf("missing plan file should not be linked: %+v", file)
		}
	}
}

const planScreen = `
 Ready to code?
 Here is Claude's plan:
  ╌╌╌╌╌╌╌╌
   1. Add subtract to calc.py
  ╌╌╌╌╌╌╌╌
 ───────────────────────────
  Claude has written up a plan and is ready to execute. Would you like to proceed?

  ❯ 1. Yes, clear context (41% used) and auto-accept edits
    2. Yes, auto-accept edits
    3. Yes, manually approve edits
    4. Tell Claude what to change
       shift+tab to approve with this feedback

  ctrl+g to edit in Vim · ~/.claude/plans/plan-add-subtract.md
`

func Test画面のプランダイアログから選択肢を読み取る(t *testing.T) {
	rows, ok := planRows(planScreen)
	if !ok {
		t.Fatal("dialog not found")
	}
	var labels []string
	for _, r := range rows {
		labels = append(labels, r.Label)
	}
	// The numbered plan body above the dialog and the feedback row are not choices.
	if got := strings.Join(labels, "|"); got != "Yes, clear context (41% used) and auto-accept edits|Yes, auto-accept edits|Yes, manually approve edits" {
		t.Errorf("rows = %s", got)
	}
	if _, ok := planRows("❯ \n  ⏸ plan mode on"); ok {
		t.Error("no dialog on screen")
	}
}

func stepsText(steps []step) string {
	var got []string
	for _, s := range steps {
		if s.text != "" {
			got = append(got, "text:"+s.text)
		} else {
			got = append(got, strings.Join(s.keys, ","))
		}
	}
	return strings.Join(got, "|")
}

func Testプラン承認への回答をキー操作に変換する(t *testing.T) {
	ia := planInteraction("toolu_plan")
	for answer, want := range map[string]string{
		"Yes, auto-accept edits":      "enter",
		"Yes, manually approve edits": "down,enter",
		"No, keep planning":           "esc",
	} {
		steps, err := dialogKeys(ia, model.InteractionResponse{Answers: map[string]model.Answer{"0": {Selected: []string{answer}}}})
		if err != nil || stepsText(steps) != want {
			t.Errorf("%s -> %s, %v; want %s", answer, stepsText(steps), err, want)
		}
	}
	steps, err := dialogKeys(ia, model.InteractionResponse{Answers: map[string]model.Answer{"0": {Text: "Also add\na type hint"}}})
	if err != nil || stepsText(steps) != "down,down|text:Also add a type hint|enter" {
		t.Errorf("feedback -> %s, %v", stepsText(steps), err)
	}
	if _, err := dialogKeys(ia, model.InteractionResponse{Answers: map[string]model.Answer{"0": {Selected: []string{"Maybe"}}}}); err == nil {
		t.Error("unknown option should fail")
	}
}

type screenTerminal struct {
	fakeTerminal
	screen string
}

func (s *screenTerminal) ReadVisiblePane(context.Context, string) (*herdr.ReadResult, error) {
	return &herdr.ReadResult{Text: s.screen}, nil
}

func TestProviderは画面に出ているプランの選択肢で回答する(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "projects", "-work-playground")
	os.MkdirAll(proj, 0o755)
	id := "5828c3f8-8401-41bc-9e95-e8a2cf18c3ba"
	os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(strings.Join(planLines("/nonexistent/plan.md"), "\n")), 0o644)

	term := &screenTerminal{screen: planScreen}
	p := New(dir, term, deadletter.Nop{})
	p.keyDelay = 0
	live := &providers.Live{PaneID: "w1:p1", HerdrStatus: herdr.StatusBlocked}

	msgs, err := p.Messages(context.Background(), id, live)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ia := planBlocks(msgs); ia == nil || len(ia.Questions[0].Options) != 4 || ia.Questions[0].Options[1].Label != "Yes, auto-accept edits" {
		t.Fatalf("options from screen = %+v", ia)
	}
	err = p.Respond(context.Background(), id, live, model.InteractionResponse{InteractionID: "toolu_plan", Answers: map[string]model.Answer{"0": {Selected: []string{"Yes, auto-accept edits"}}}})
	if err != nil || strings.Join(term.calls, "|") != "keys:down,enter" {
		t.Errorf("calls = %v, %v", term.calls, err)
	}
	term.calls = nil
	err = p.Respond(context.Background(), id, live, model.InteractionResponse{InteractionID: "toolu_plan", Answers: map[string]model.Answer{"0": {Text: "Use type hints"}}})
	if err != nil || strings.Join(term.calls, "|") != "keys:down,down,down|text:Use type hints|keys:enter" {
		t.Errorf("feedback calls = %v, %v", term.calls, err)
	}
	if roots := p.FileRoots(); len(roots) != 1 || roots[0] != filepath.Join(dir, "plans") {
		t.Errorf("file roots = %v", roots)
	}
}
