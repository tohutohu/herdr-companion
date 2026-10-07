package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

func Test現在のClaudeの承認画面と折り返された画面を検出できる(t *testing.T) {
	real, err := os.ReadFile(filepath.Join(testdata, "plan_screen_2_1_292.txt"))
	if err != nil {
		t.Fatal(err)
	}
	wrapped := strings.ReplaceAll(planScreen, "Would you like to proceed?", "Would you like to\n proceed?")
	wrapped = strings.ReplaceAll(wrapped, "Yes, clear context (41% used) and auto-accept edits", "Yes, clear context (41% used) and\n       auto-accept edits")
	for name, screen := range map[string]string{"2.1.292": string(real), "折り返し": wrapped} {
		t.Run(name, func(t *testing.T) {
			ia := screenPlan(screen)
			if ia == nil || ia.Kind != model.KindPlan || ia.State != model.InteractionPending || len(ia.Questions[0].Options) < 3 {
				t.Fatalf("plan = %+v", ia)
			}
			if len(ia.Detail) == 0 || strings.Contains(ia.Detail, "Would you like") || strings.Contains(ia.Detail, "auto-accept edits") {
				t.Fatalf("plan body = %q", ia.Detail)
			}
		})
	}
	if got := screenPlan(wrapped).Questions[0].Options[0].Label; got != "Yes, clear context (41% used) and auto-accept edits" {
		t.Fatalf("wrapped choice = %s", got)
	}
	if screenPlan(wrapped).ID != screenPlan(planScreen).ID {
		t.Fatal("wrapping changed the screen plan ID")
	}
	if screenPlan(strings.ReplaceAll(planScreen, "plan-add-subtract.md", "plan-other.md")).ID == screenPlan(planScreen).ID {
		t.Fatal("different plan files have the same screen plan ID")
	}
	for name, screen := range map[string]string{
		"引用後の入力欄": planScreen + "\n❯ Ask anything",
		"フォーカスなし": strings.ReplaceAll(planScreen, "❯", " "),
		"操作ヒントなし": strings.ReplaceAll(planScreen, "shift+tab to approve with this feedback", ""),
		"不完全な選択肢": strings.ReplaceAll(planScreen, "2. Yes, auto-accept edits", "7. Yes, auto-accept edits"),
	} {
		if screenPlan(screen) != nil {
			t.Errorf("%s was detected as an actionable plan", name)
		}
	}
}

func TestExitPlanModeも通知も履歴にない承認画面からカードを復元する(t *testing.T) {
	ctx := context.Background()
	for _, status := range []string{herdr.StatusIdle, herdr.StatusWorking, herdr.StatusBlocked} {
		t.Run(status, func(t *testing.T) {
			p, _, write := cacheProvider(t)
			// No plan mode metadata and no ExitPlanMode tool call.
			write([]byte(questionPrompt + "\n"))
			term := &screenTerminal{screen: planScreen}
			p.term, p.keyDelay = term, 0
			live := &providers.Live{PaneID: "w1:p1", HerdrStatus: status}
			s, err := p.Summary(ctx, cacheSession, live)
			if err != nil || s.Pending != model.InteractionApproval || s.Status != model.StatusWaitingApproval {
				t.Fatalf("summary = %+v, err = %v", s, err)
			}
			msgs, err := p.Messages(ctx, cacheSession, live)
			if err != nil {
				t.Fatal(err)
			}
			_, _, ia := planBlocks(msgs)
			if ia == nil || !strings.HasPrefix(ia.ID, screenPlanPrefix) || !strings.Contains(ia.Detail, "Add subtract") {
				t.Fatalf("screen-derived plan = %+v", ia)
			}
			response := model.InteractionResponse{InteractionID: ia.ID, Answers: map[string]model.Answer{"0": {Selected: []string{"Yes, auto-accept edits"}}}}
			if err := p.Respond(ctx, cacheSession, live, response); err != nil || strings.Join(term.calls, "|") != "keys:down,enter" {
				t.Fatalf("response = %v, calls = %v", err, term.calls)
			}
			term.calls = nil
			// A different plan must reject answers from the previous card.
			term.screen = strings.ReplaceAll(planScreen, "Add subtract", "Delete all files")
			if err := p.Respond(ctx, cacheSession, live, response); !errors.Is(err, providers.ErrInteractionGone) || len(term.calls) != 0 {
				t.Fatalf("replaced plan response = %v, calls = %v", err, term.calls)
			}
			term.screen = "❯ Ask anything"
			if err := p.Respond(ctx, cacheSession, live, response); !errors.Is(err, providers.ErrInteractionGone) || len(term.calls) != 0 {
				t.Fatalf("closed plan response = %v, calls = %v", err, term.calls)
			}
			// Neither screen-derived cards nor status may remain in caches.
			s, err = p.Summary(ctx, cacheSession, live)
			if err != nil || s.Pending != "" || s.Status != "" {
				t.Fatalf("closed summary = %+v, err = %v", s, err)
			}
			msgs, err = p.Messages(ctx, cacheSession, live)
			if _, _, ia := planBlocks(msgs); err != nil || ia != nil {
				t.Fatalf("closed plan = %+v, err = %v", ia, err)
			}
		})
	}
}

func Testプラン承認のカードを重複させず現在のフォーカスから回答する(t *testing.T) {
	p, _, write := cacheProvider(t)
	write([]byte(strings.Join(planLines("/nonexistent/plan.md"), "\n")))
	term := &screenTerminal{screen: planScreen}
	p.term, p.keyDelay = term, 0
	live := &providers.Live{PaneID: "w1:p1", HerdrStatus: herdr.StatusBlocked}
	msgs, err := p.Messages(context.Background(), cacheSession, live)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Interaction != nil && b.Interaction.Kind == model.KindPlan {
				count++
				if b.Interaction.ID != "toolu_plan" {
					t.Fatalf("native plan ID replaced: %s", b.Interaction.ID)
				}
			}
		}
	}
	if count != 1 {
		t.Fatalf("plan cards = %d", count)
	}
	// The user moved focus to the third row in the terminal.
	term.screen = strings.ReplaceAll(strings.ReplaceAll(planScreen, "❯ 1.", "  1."), "  3. Yes, manually", "❯ 3. Yes, manually")
	response := model.InteractionResponse{InteractionID: "toolu_plan", Answers: map[string]model.Answer{"0": {Selected: []string{"Yes, auto-accept edits"}}}}
	if err := p.Respond(context.Background(), cacheSession, live, response); err != nil || strings.Join(term.calls, "|") != "keys:up,up|keys:down,enter" {
		t.Fatalf("focused response = %v, calls = %v", err, term.calls)
	}
	term.screen, term.calls = "❯ Ask anything", nil
	// Herdr can still say blocked after cancellation and before the result is flushed.
	if err := p.Respond(context.Background(), cacheSession, live, response); !errors.Is(err, providers.ErrInteractionGone) || len(term.calls) != 0 {
		t.Fatalf("stale native response = %v, calls = %v", err, term.calls)
	}
}
