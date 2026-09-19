package codex

import (
	"context"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

const rateLimitPromptScreen = `• 選択できるようにして、稼働中のGatewayにも反映しました。

  Worked for 2m 16s · done 9:41 PM


  Approaching rate limits
  Switch to gpt-5.6-luna for lower credit usage?

› 1. Switch to gpt-5.6-luna                 Fast and affordable agentic coding model.
  2. Keep current model
  3. Keep current model (never show again)  Hide future rate limit reminders about switching models.

  Press enter to confirm or esc to go back`

func Test画面のレート制限ダイアログから質問と選択肢を読み取る(t *testing.T) {
	sp, ok := rateLimitSelectPrompt(rateLimitPromptScreen)
	if !ok || len(sp.Rows) != 3 || sp.Cursor != 0 {
		t.Fatalf("prompt = %+v, %v", sp, ok)
	}
	if got := strings.Join(sp.Lines, " "); got != "Switch to gpt-5.6-luna for lower credit usage?" {
		t.Errorf("question = %q", got)
	}
	if sp.Rows[0].Label != "Switch to gpt-5.6-luna" || sp.Rows[0].Description != "Fast and affordable agentic coding model." {
		t.Errorf("first row = %+v", sp.Rows[0])
	}
	if sp.Rows[2].Label != "Keep current model (never show again)" {
		t.Errorf("last row = %+v", sp.Rows[2])
	}
	// A transcript quoting the title is not the popup.
	if _, ok := rateLimitSelectPrompt("  Approaching rate limits\n  1. a\n  2. b\n\n› Ask Codex to do anything"); ok {
		t.Error("quoted prompt without footer")
	}
	// The plan prompt reader does not take it for a plan.
	if _, ok := planPromptRows(rateLimitPromptScreen); ok {
		t.Error("rate-limit prompt read as plan prompt")
	}
}

func Testレート制限ダイアログは画面に出ている間だけ回答待ちの質問になる(t *testing.T) {
	th := planThread("completed")
	term := &asyncTerminal{screen: rateLimitPromptScreen}
	p := planTestProvider(t, th, term)
	live := &providers.Live{PaneID: "w2:p1", HerdrStatus: herdr.StatusIdle}

	msgs, err := p.Messages(context.Background(), th.ID, live)
	if err != nil {
		t.Fatal(err)
	}
	last := msgs[len(msgs)-1].Blocks[0].Interaction
	if last == nil || last.ID != "codex-ratelimit:turn1" || last.Type != model.InteractionQuestions || last.State != model.InteractionPending || !last.Supported {
		t.Fatalf("rate-limit prompt = %+v", msgs[len(msgs)-1])
	}
	if q := last.Questions[0]; q.Question != "Switch to gpt-5.6-luna for lower credit usage?" || len(q.Options) != 3 {
		t.Errorf("question = %+v", q)
	}
	sum, err := p.Summary(context.Background(), th.ID, live)
	if err != nil || sum.Pending != model.InteractionQuestions || sum.Status != model.StatusWaitingInput {
		t.Errorf("summary = %+v, %v", sum, err)
	}

	term.screen = "› Ask Codex to do anything"
	msgs, _ = p.Messages(context.Background(), th.ID, live)
	for _, b := range msgs[len(msgs)-1].Blocks {
		if b.Type == model.BlockInteraction {
			t.Errorf("no prompt expected: %+v", b.Interaction)
		}
	}
}

func Testレート制限ダイアログへの回答はカーソル位置から選択肢まで移動して確定する(t *testing.T) {
	th := planThread("completed")
	term := &asyncTerminal{screen: rateLimitPromptScreen}
	p := planTestProvider(t, th, term)
	live := &providers.Live{PaneID: "w2:p1", HerdrStatus: herdr.StatusIdle}
	answer := func(label string) error {
		return p.Respond(context.Background(), th.ID, live, model.InteractionResponse{
			InteractionID: "codex-ratelimit:turn1",
			Answers:       map[string]model.Answer{"0": {Selected: []string{label}}},
		})
	}

	if err := answer("Keep current model"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(term.keys, ","); got != "down,enter" {
		t.Errorf("keys = %s", got)
	}

	// With the cursor on row 3, row 1 is reached by moving up.
	term.keys = nil
	term.screen = strings.NewReplacer("› 1.", "  1.", "  3.", "› 3.").Replace(rateLimitPromptScreen)
	if err := answer("Switch to gpt-5.6-luna"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(term.keys, ","); got != "up,up,enter" {
		t.Errorf("keys = %s", got)
	}

	term.keys = nil
	if err := answer("Keep current model"); err != providers.ErrInteractionGone {
		t.Errorf("err = %v, want ErrInteractionGone", err)
	}
	if len(term.keys) != 0 {
		t.Errorf("keys sent without prompt: %v", term.keys)
	}
}

func Test実行中のペインではレート制限ダイアログを探さない(t *testing.T) {
	th := planThread("inProgress")
	term := &asyncTerminal{screen: rateLimitPromptScreen}
	p := planTestProvider(t, th, term)
	live := &providers.Live{PaneID: "w2:p1", HerdrStatus: herdr.StatusWorking}
	if ia, _ := p.rateLimitPrompt(context.Background(), th, live); ia != nil {
		t.Errorf("prompt while working = %+v", ia)
	}
}
