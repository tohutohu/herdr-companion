package claude

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

const questionCall = `{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_question","name":"AskUserQuestion","input":{"questions":[{"question":"Which color?","header":"Color","options":[{"label":"Red"},{"label":"Green"}]}]}}]}}`
const questionPrompt = `{"type":"user","uuid":"u1","message":{"role":"user","content":"Pick a color"}}`
const questionScreen = `
 Color
 Which color?

 ❯ 1. Red
   2. Green
   3. Type something.

 Enter to select · Tab/Arrow keys to navigate · Esc to cancel
`

func Test回答待ち通知が欠けても画面の質問に回答できる(t *testing.T) {
	ctx := context.Background()
	for _, status := range []string{herdr.StatusIdle, herdr.StatusWorking, herdr.StatusDone, herdr.StatusUnknown} {
		t.Run(status, func(t *testing.T) {
			p, _, write := cacheProvider(t)
			write([]byte(questionPrompt + "\n" + questionCall + "\n"))
			term := &screenTerminal{}
			p.term, p.keyDelay = term, 0
			live := &providers.Live{PaneID: "w1:p1", HerdrStatus: status}
			check := func(want model.InteractionState, wantStatus model.Status) {
				t.Helper()
				msgs, err := p.Messages(ctx, cacheSession, live)
				if err != nil {
					t.Fatal(err)
				}
				ia := findInteraction(msgs, "toolu_question")
				if ia == nil || ia.State != want {
					t.Fatalf("interaction = %+v, want %s", ia, want)
				}
				s, err := p.Summary(ctx, cacheSession, live)
				if err != nil || s.Status != wantStatus {
					t.Fatalf("summary = %+v, err = %v, want status %s", s, err, wantStatus)
				}
			}
			// Warm the caches before the dialog appears; only the screen changes.
			check(model.InteractionClosed, "")
			term.screen = questionScreen
			check(model.InteractionPending, model.StatusWaitingInput)
			if live.HerdrStatus != status {
				t.Fatal("reported lifecycle was mutated")
			}
			err := p.Send(ctx, cacheSession, live, model.Input{Text: "new prompt", Images: []string{"/tmp/image.png"}})
			var herr *herdr.Error
			if !errors.As(err, &herr) || herr.Code != "agent_blocked" || len(term.calls) != 0 {
				t.Fatalf("send = %v, calls = %v", err, term.calls)
			}
			response := model.InteractionResponse{InteractionID: "toolu_question", Answers: map[string]model.Answer{"0": {Selected: []string{"Green"}}}}
			if err := p.Respond(ctx, cacheSession, live, response); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(term.calls, "|"); got != "keys:down,enter" {
				t.Fatalf("answer calls = %s", got)
			}
			// Closing the dialog must not reuse a cached waiting state.
			term.screen, term.calls = "❯", nil
			check(model.InteractionClosed, "")
			if err := p.Respond(ctx, cacheSession, live, response); !errors.Is(err, providers.ErrInteractionGone) || len(term.calls) != 0 {
				t.Fatalf("closed response = %v, calls = %v", err, term.calls)
			}
			term.screen = questionScreen
			msgs, err := p.Messages(ctx, cacheSession, nil)
			if err != nil || findInteraction(msgs, "toolu_question").State != model.InteractionClosed {
				t.Fatalf("offline messages = %+v, err = %v", msgs, err)
			}
			// Appending a result invalidates the cached unanswered-dialog list,
			// even if a stale screen still contains the question.
			write([]byte(questionPrompt + "\n" + questionCall + "\n" + `{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_question","content":"Green"}]}}` + "\n"))
			check(model.InteractionAnswered, "")
		})
	}
}

func Test回答済みや放棄した質問は画面に文言が残っても再開しない(t *testing.T) {
	for name, suffix := range map[string]string{
		"回答済み":     `{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_question","content":"Red"}]}}`,
		"新しいプロンプト": `{"type":"user","uuid":"u2","message":{"role":"user","content":"Never mind"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			p, _, write := cacheProvider(t)
			write([]byte(questionPrompt + "\n" + questionCall + "\n" + suffix + "\n"))
			p.term = &screenTerminal{screen: questionScreen}
			live := &providers.Live{PaneID: "w1:p1", HerdrStatus: herdr.StatusIdle}
			msgs, err := p.Messages(context.Background(), cacheSession, live)
			if err != nil || findInteraction(msgs, "toolu_question").State == model.InteractionPending {
				t.Fatalf("messages = %+v, err = %v", msgs, err)
			}
		})
	}
}

func Test質問本文とダイアログのフッターが両方ある時だけ待機を検出する(t *testing.T) {
	b := decodeLines(t, []string{questionCall}).entries[0]
	blocks, _ := decodeBlocks(b.Message.Content)
	for name, screen := range map[string]string{
		"会話本文":  "Which color?",
		"別の質問":  "Which fruit?\n Enter to select",
		"閉じた画面": "❯ Ask anything",
	} {
		if dialogOnScreen(blocks[0], screen) {
			t.Errorf("%s matched", name)
		}
	}
	if !dialogOnScreen(blocks[0], strings.ReplaceAll(questionScreen, "Which color?", "Which\n color?")) {
		t.Error("wrapped question not detected")
	}
}

func Test通知のないプラン承認も画面の選択肢で回答できる(t *testing.T) {
	p, _, write := cacheProvider(t)
	write([]byte(strings.Join(planLines("/nonexistent/plan.md"), "\n")))
	term := &screenTerminal{screen: planScreen}
	p.term, p.keyDelay = term, 0
	live := &providers.Live{PaneID: "w1:p1", HerdrStatus: herdr.StatusIdle}
	s, err := p.Summary(context.Background(), cacheSession, live)
	if err != nil || s.Status != model.StatusWaitingApproval {
		t.Fatalf("summary = %+v, err = %v", s, err)
	}
	err = p.Respond(context.Background(), cacheSession, live, model.InteractionResponse{InteractionID: "toolu_plan", Answers: map[string]model.Answer{"0": {Selected: []string{"Yes, auto-accept edits"}}}})
	if err != nil || strings.Join(term.calls, "|") != "keys:down,enter" {
		t.Fatalf("response = %v, calls = %v", err, term.calls)
	}
	// A terminal without visible-pane support keeps the existing lifecycle behavior.
	p.term = &fakeTerminal{}
	msgs, err := p.Messages(context.Background(), cacheSession, live)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ia := planBlocks(msgs); ia.State != model.InteractionClosed {
		t.Fatalf("no visible pane: %+v", ia)
	}
}
