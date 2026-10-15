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

func Test複数行に折り返された長い質問を狭いパネルで検出する(t *testing.T) {
	// Claude Code 2.1.300 in a 40x19 pane, where Herdr reports idle.
	screen, err := os.ReadFile(filepath.Join(testdata, "question_screen_2_1_300_40cols.txt"))
	if err != nil {
		t.Fatal(err)
	}
	call := `{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_long","name":"AskUserQuestion","input":{"questions":[{"header":"方針","question":"このリポジトリのGatewayで、Herdrの状態通知が欠けたときにClaudeの質問ダイアログを画面から復元する処理を、今回の調査結果を踏まえてどの方針で直すのが一番よいと思いますか？既存のテストとの互換性も考慮してください。","multiSelect":false,"options":[{"label":"画面の判定を強化","description":"折り返しや省略に強い照合にする"},{"label":"Herdrに報告","description":"上流のルールを直してもらう"}]}]}}]}}`
	blocks, _ := decodeBlocks(decodeLines(t, []string{call}).entries[0].Message.Content)
	if !dialogOnScreen(blocks[0], string(screen)) {
		t.Error("question with a gutter not detected")
	}
}

func Test上が画面外に出た長い質問を狭く低いパネルで検出する(t *testing.T) {
	// Claude Code 2.1.300 in a 40x13 pane: only the last rows of the question remain.
	screen, err := os.ReadFile(filepath.Join(testdata, "question_screen_2_1_300_40x13_scrolled.txt"))
	if err != nil {
		t.Fatal(err)
	}
	question := "We need to decide how the gateway should recover question dialogs when the terminal multiplexer misses the blocked lifecycle notification. Consider wrapping, gutters, truncation, small panes, previews, multiple questions, and compatibility with the existing fixtures and golden files. Also keep in mind that the phone app only shows the answer panel for pending interactions, so any false negative hides the panel entirely. Which approach do you prefer?"
	call := func(q string) contentBlock {
		line := `{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_long","name":"AskUserQuestion","input":{"questions":[{"header":"Approach","question":"` + q + `","options":[{"label":"Screen"},{"label":"Upstream"}]}]}}]}}`
		blocks, _ := decodeBlocks(decodeLines(t, []string{line}).entries[0].Message.Content)
		return blocks[0]
	}
	if !dialogOnScreen(call(question), string(screen)) {
		t.Error("question scrolled off the top not detected")
	}
	// Only the start of the screen may continue a cut question, and a common
	// short ending is not enough evidence on its own.
	for name, q := range map[string]string{
		"別の長い質問": "Something else entirely. Which approach do you prefer?",
		"短い末尾":   "Do you prefer?",
	} {
		if dialogOnScreen(call(q), string(screen)) {
			t.Errorf("%s matched", name)
		}
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
