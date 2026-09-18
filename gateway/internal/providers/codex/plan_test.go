package codex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

const planPromptScreen = `• Proposed Plan

  ### Add divide
  - 1. Add divide(a, b) to calc.py.

────────────────────────────────────────

  Implement this plan?

› 1. Yes, implement this plan          Switch to Default and start coding.
  2. Yes, clear context and implement  Fresh thread. Context: 4% used.
  3. No, stay in Plan mode             Continue planning with the model.

  Press enter to confirm or esc to go back`

func planThread(turnStatus string) *Thread {
	return &Thread{ID: "01a0b27f-41ba-7f43-8bd8-20a91b5118bd", Cwd: "/w/app", UpdatedAt: 1789700000, Turns: []Turn{{
		ID:     "turn1",
		Status: turnStatus,
		Items: []json.RawMessage{
			json.RawMessage(`{"type":"userMessage","id":"u1","content":[{"type":"text","text":"plan divide"}]}`),
			json.RawMessage(`{"type":"plan","id":"turn1-plan","text":"### Add divide"}`),
		},
	}}}
}

func planTestProvider(t *testing.T, th *Thread, term *asyncTerminal) *Provider {
	t.Helper()
	raw, _ := json.Marshal(th)
	f := &fakeServer{t: t, thread: raw}
	pt := &pipeTransport{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
	go f.serve(pt, func(v any) { b, _ := json.Marshal(v); pt.in <- b })
	p := New("unused", "/nonexistent", term, deadletter.Nop{})
	p.reader = newRPCClient(pt, nil)
	t.Cleanup(p.reader.close)
	return p
}

func Test画面の実装確認ダイアログから選択肢を読み取る(t *testing.T) {
	rows, ok := planPromptRows(planPromptScreen)
	if !ok || len(rows) != 3 {
		t.Fatalf("rows = %+v, %v", rows, ok)
	}
	if rows[0].Label != "Yes, implement this plan" || rows[0].Description != "Switch to Default and start coding." {
		t.Errorf("first row = %+v", rows[0])
	}
	if rows[2].Label != "No, stay in Plan mode" {
		t.Errorf("last row = %+v", rows[2])
	}
	if _, ok := planPromptRows("› Ask Codex to do anything"); ok {
		t.Error("no prompt on screen")
	}
}

func TestPlanモードの実装確認は画面に出ている間だけ承認待ちのプランカードになる(t *testing.T) {
	th := planThread("completed")
	term := &asyncTerminal{screen: planPromptScreen}
	p := planTestProvider(t, th, term)
	live := &providers.Live{PaneID: "w2:p1", HerdrStatus: herdr.StatusIdle}

	msgs, err := p.Messages(context.Background(), th.ID, live)
	if err != nil {
		t.Fatal(err)
	}
	last := msgs[len(msgs)-1].Blocks[0].Interaction
	if last == nil || last.Kind != model.KindPlan || last.ID != "codex-plan:turn1-plan" || last.State != model.InteractionPending || len(last.Questions[0].Options) != 3 {
		t.Fatalf("plan prompt = %+v", msgs[len(msgs)-1])
	}
	sum, err := p.Summary(context.Background(), th.ID, live)
	if err != nil || sum.Pending != model.InteractionApproval || sum.Status != model.StatusWaitingApproval {
		t.Errorf("summary = %+v, %v", sum, err)
	}

	// Answered in the TUI: the composer is back and nothing is pending.
	term.screen = "› Ask Codex to do anything"
	msgs, _ = p.Messages(context.Background(), th.ID, live)
	for _, b := range msgs[len(msgs)-1].Blocks {
		if b.Type == model.BlockInteraction {
			t.Errorf("no prompt expected: %+v", b.Interaction)
		}
	}
	if sum, _ := p.Summary(context.Background(), th.ID, live); sum.Status != "" {
		t.Errorf("status without prompt = %q", sum.Status)
	}
}

func Test実装確認への回答は画面を確かめてからキー操作で送る(t *testing.T) {
	th := planThread("completed")
	term := &asyncTerminal{screen: planPromptScreen}
	p := planTestProvider(t, th, term)
	live := &providers.Live{PaneID: "w2:p1", HerdrStatus: herdr.StatusIdle}
	answer := func(label string) error {
		return p.Respond(context.Background(), th.ID, live, model.InteractionResponse{
			InteractionID: "codex-plan:turn1-plan",
			Answers:       map[string]model.Answer{"0": {Selected: []string{label}}},
		})
	}

	if err := answer("No, stay in Plan mode"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(term.keys, ","); got != "down,down,enter" {
		t.Errorf("keys = %s", got)
	}
	// The fake closes the prompt on enter; a second answer must not type into the composer.
	term.keys = nil
	if err := answer("Yes, implement this plan"); err != providers.ErrInteractionGone {
		t.Errorf("err = %v, want ErrInteractionGone", err)
	}
	if len(term.keys) != 0 {
		t.Errorf("keys sent without prompt: %v", term.keys)
	}
}

func Test実行中や計画のないターンでは実装確認を出さない(t *testing.T) {
	if id := latestPlanItem(planThread("inProgress")); id != "" {
		t.Errorf("in-progress turn plan = %q", id)
	}
	th := planThread("completed")
	th.Turns[0].Items = th.Turns[0].Items[:1]
	if id := latestPlanItem(th); id != "" {
		t.Errorf("turn without plan = %q", id)
	}
}
