package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
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

	blocked := &providers.Live{State: providers.StateBlocked}
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
