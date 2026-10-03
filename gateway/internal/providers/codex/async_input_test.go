package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

const collapsedAsync = "• Queued follow-up inputs\n  ? 1 question\n    ⌥ + ↑ to answer\n\n› Ask Codex to do anything"

type asyncTerminal struct {
	fakeTerm
	screen      string
	opened      string
	keys        []string
	text        string
	afterSubmit []string
}

func (f *asyncTerminal) ReadVisiblePane(context.Context, string) (*herdr.ReadResult, error) {
	return &herdr.ReadResult{Text: f.screen}, nil
}
func (f *asyncTerminal) SendKeys(_ context.Context, _ string, keys ...string) error {
	f.keys = append(f.keys, keys...)
	for _, k := range keys {
		if k == "alt+up" || k == "shift+left" {
			f.screen = f.opened
		}
		if k == "enter" {
			f.screen = "› Ask Codex to do anything"
			if len(f.afterSubmit) > 0 {
				f.screen, f.afterSubmit = f.afterSubmit[0], f.afterSubmit[1:]
			}
		}
	}
	return nil
}
func (f *asyncTerminal) SendText(_ context.Context, _, text string) error { f.text = text; return nil }

func asyncTestProvider(t *testing.T, term *asyncTerminal) (*Provider, *Thread) {
	t.Helper()
	th, _ := loadThread(t, "async_question.json")
	return asyncProviderWithThread(t, term, th), th
}

func asyncProviderWithThread(t *testing.T, term *asyncTerminal, th *Thread) *Provider {
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

func Test単独TUIの非同期質問は完了ターンでも回答できる(t *testing.T) {
	term := &asyncTerminal{screen: collapsedAsync, opened: "• Queued follow-up inputs\n\n  APIキーの保存先を教えてください。\n\n  Type your answer\n\n  enter submit   ctrl + ] skip   ⌥ + ↓ main prompt"}
	p, th := asyncTestProvider(t, term)
	live := &providers.Live{PaneID: "test", HerdrStatus: herdr.StatusBlocked}
	msgs, err := p.Messages(context.Background(), th.ID, live)
	if err != nil {
		t.Fatal(err)
	}
	ia := msgs[len(msgs)-1].Blocks[0].Interaction
	if ia == nil || !ia.Supported || ia.Type != model.InteractionQuestions {
		t.Fatalf("interaction: %+v", ia)
	}
	assertGolden(t, "async_question.golden.json", ia)
	r := model.InteractionResponse{InteractionID: ia.ID, Answers: map[string]model.Answer{"0": {Text: "JEV_API_KEY"}}}
	if err := p.Respond(context.Background(), th.ID, live, r); err != nil {
		t.Fatal(err)
	}
	if strings.Join(term.keys, ",") != "alt+up,enter" || term.text != "\x1b[200~JEV_API_KEY\x1b[201~" {
		t.Fatalf("keys=%v text=%q", term.keys, term.text)
	}
	if err := p.Respond(context.Background(), th.ID, live, r); !errors.Is(err, providers.ErrInteractionGone) {
		t.Fatalf("duplicate response: %v", err)
	}
	if ia := p.asyncInteraction(context.Background(), th, live); ia != nil {
		t.Fatal("answered question is still pending")
	}
}

func Test非同期質問の画面が別質問なら送信しない(t *testing.T) {
	term := &asyncTerminal{screen: collapsedAsync, opened: "• Queued follow-up inputs\n  Different question\n  Type your answer\n  enter submit   ⌥ + ↓ main prompt"}
	p, th := asyncTestProvider(t, term)
	r := model.InteractionResponse{InteractionID: asyncInputPrefix + "async-question-1", Answers: map[string]model.Answer{"0": {Text: "answer"}}}
	if err := p.Respond(context.Background(), th.ID, &providers.Live{PaneID: "test"}, r); !errors.Is(err, providers.ErrInteractionGone) {
		t.Fatalf("got %v", err)
	}
	if term.text != "" || strings.Contains(strings.Join(term.keys, ","), "enter") {
		t.Fatal("sent to a different question")
	}
}

func Test非同期質問の未入力と選択肢を検証する(t *testing.T) {
	q := model.Question{ID: "0", Options: []model.Option{{Label: "Red"}, {Label: "Blue"}}}
	for _, a := range []model.Answer{{}, {Text: "a\nb"}, {Text: "\x1b[A"}, {Selected: []string{"unknown"}}, {Selected: []string{"Red"}, Text: "x"}} {
		if _, _, err := asyncAnswer(q, model.InteractionResponse{Answers: map[string]model.Answer{"0": a}}); err == nil {
			t.Fatalf("accepted %+v", a)
		}
	}
	_, index, err := asyncAnswer(q, model.InteractionResponse{Answers: map[string]model.Answer{"0": {Selected: []string{"Blue"}}}})
	if err != nil || index != 2 {
		t.Fatalf("index=%d error=%v", index, err)
	}
	for _, tc := range []struct {
		line  string
		empty bool
	}{{"  › 3. Other", true}, {"  › 3. draft", false}, {"  › 2. Blue", false}} {
		if got := emptyAsyncDraft("• Queued follow-up inputs\n"+tc.line+"\n", q); got != tc.empty {
			t.Errorf("%q: %v", tc.line, got)
		}
	}
}

func Test非同期質問の本文一致と複数待ちを確認する(t *testing.T) {
	if focusedQuestion("• Queued follow-up inputs\n\n  Test another\n\n  Type your answer\n\n  enter submit   ⌥ + ↓ main prompt", model.Question{Question: "Test"}) {
		t.Fatal("partial title matches a different question")
	}
	if !collapsedQuestion(strings.Replace(collapsedAsync, "1 question", "1 question · 20s", 1)) {
		t.Fatal("countdown hides pending question")
	}
	if !collapsedQuestion(strings.Replace(collapsedAsync, "1 question", "2 questions", 1)) {
		t.Fatal("multi-question queue not detected")
	}
	th, _ := loadThread(t, "async_question.json")
	th.Turns[0].Items[0] = json.RawMessage(`{"type":"agentMessage","id":"q","delivery":"async","questions":[{"title":"a"},{"title":"b"}]}`)
	if ia := asyncQuestionOnScreen(pendingAsyncQuestions(th), strings.Replace(collapsedAsync, "1 question", "2 questions", 1)); ia == nil || ia.Questions[0].Question != "a" {
		t.Fatal("first question not shown")
	}
}

// Visible queue fragments verified with an isolated Codex 0.160.0 TUI.
const multiCollapsed = "• Queued follow-up inputs\n  ? 3 questions\n    shift+← to answer\n\n› Ask Codex to do anything"
const multiColor = "• Queued follow-up inputs\n\n\n  1 of 3\n  Color?\n\n  › 1. Red\n    2. Blue\n    3. Other\n\n  enter submit   ctrl+] skip   shift+→ main prompt   shift+← next question"
const multiName = "• Queued follow-up inputs\n\n\n  1 of 2\n\n  Name?\n\n  Type your answer\n\n  enter submit   ctrl+] skip   shift+→ main prompt   shift+← next question"
const multiShape = "• Queued follow-up inputs\n\n  Shape?\n\n  › 1. Circle\n    2. Square\n    3. Other\n\n  enter submit   ctrl+] skip   shift+→ main prompt"

func multiThread(t *testing.T) *Thread {
	t.Helper()
	th, _ := loadThread(t, "async_multi.json")
	return th
}

func Test複数非同期質問をUIから順番に回答する(t *testing.T) {
	th := multiThread(t)
	term := &asyncTerminal{screen: multiCollapsed, opened: multiColor, afterSubmit: []string{multiName, multiShape}}
	p := asyncProviderWithThread(t, term, th)
	live := &providers.Live{PaneID: "test"}
	var first model.InteractionResponse
	for i, tc := range []struct {
		title  string
		answer model.Answer
	}{
		{"Color?", model.Answer{Selected: []string{"Red"}}},
		{"Name?", model.Answer{Text: "UI test"}},
		{"Shape?", model.Answer{Selected: []string{"Circle"}}},
	} {
		msgs, err := p.Messages(context.Background(), th.ID, live)
		if err != nil {
			t.Fatal(err)
		}
		ia := msgs[len(msgs)-1].Blocks[0].Interaction
		if ia == nil || ia.Questions[0].Question != tc.title {
			t.Fatalf("question %d: %+v", i, ia)
		}
		r := model.InteractionResponse{InteractionID: ia.ID, Answers: map[string]model.Answer{ia.Questions[0].ID: tc.answer}}
		if i == 0 {
			first = r
		}
		if err := p.Respond(context.Background(), th.ID, live, r); err != nil {
			t.Fatal(err)
		}
		if err := p.Respond(context.Background(), th.ID, live, first); !errors.Is(err, providers.ErrInteractionGone) {
			t.Fatalf("stale reply: %v", err)
		}
	}
	if got := strings.Join(term.keys, ","); got != "shift+left,enter,enter,enter" {
		t.Fatalf("keys: %s", got)
	}
	if term.text != "\x1b[200~UI test\x1b[201~" {
		t.Fatalf("text: %q", term.text)
	}
	if p.asyncInteraction(context.Background(), th, live) != nil {
		t.Fatal("queue still pending")
	}
}

func Test非同期質問の手動移動と複数呼出を区別する(t *testing.T) {
	th := multiThread(t)
	// A manual move can focus any question, not only the first one.
	screen := strings.Replace(multiName, "1 of 2", "2 of 3", 1)
	if ia := asyncQuestionOnScreen(pendingAsyncQuestions(th), screen); ia == nil || ia.ID != asyncInputPrefix+"call_multi:1" {
		t.Fatalf("focused: %+v", ia)
	}
	// The reply has not reached thread/read yet, so resolve a remaining old
	// question by its actual title when the queue shrinks out of order.
	screen = strings.Replace(multiColor, "1 of 3", "1 of 2", 1)
	if ia := asyncQuestionOnScreen(pendingAsyncQuestions(th), screen); ia == nil || ia.Questions[0].ID != "0" {
		t.Fatalf("remaining: %+v", ia)
	}
	th.Turns = append(th.Turns, Turn{Items: []json.RawMessage{json.RawMessage(`{"type":"agentMessage","id":"later_call","delivery":"async","questions":[{"title":"Another?"}]}`)}})
	if ia := asyncQuestionOnScreen(pendingAsyncQuestions(th), strings.Replace(multiCollapsed, "3 questions", "4 questions", 1)); ia == nil || ia.ID != asyncInputPrefix+"call_multi:0" {
		t.Fatalf("multiple calls: %+v", ia)
	}
	if ia := asyncQuestionOnScreen(pendingAsyncQuestions(th), strings.Replace(multiCollapsed, "3 questions", "1 question", 1)); ia == nil || ia.ID != asyncInputPrefix+"later_call" {
		t.Fatalf("expired history: %+v", ia)
	}
}

func Test非同期質問の既存下書きや回答中の別質問へ送らない(t *testing.T) {
	for _, screen := range []string{
		strings.Replace(multiName, "Type your answer", "Existing draft", 1),
		strings.Replace(multiName, "Name?", "Different?", 1),
	} {
		th := multiThread(t)
		term := &asyncTerminal{screen: screen}
		p := asyncProviderWithThread(t, term, th)
		err := p.Respond(context.Background(), th.ID, &providers.Live{PaneID: "test"}, model.InteractionResponse{InteractionID: asyncInputPrefix + "call_multi:1", Answers: map[string]model.Answer{"1": {Text: "replacement"}}})
		if err == nil || term.text != "" || len(term.keys) != 0 {
			t.Fatalf("sent to draft/other question: %v", err)
		}
	}
}
