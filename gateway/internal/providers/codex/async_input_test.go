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
	screen string
	opened string
	keys   []string
	text   string
}

func (f *asyncTerminal) ReadVisiblePane(context.Context, string) (*herdr.ReadResult, error) {
	return &herdr.ReadResult{Text: f.screen}, nil
}
func (f *asyncTerminal) SendKeys(_ context.Context, _ string, keys ...string) error {
	f.keys = append(f.keys, keys...)
	for _, k := range keys {
		if k == "alt+up" {
			f.screen = f.opened
		}
		if k == "enter" {
			f.screen = "› Ask Codex to do anything"
		}
	}
	return nil
}
func (f *asyncTerminal) SendText(_ context.Context, _, text string) error { f.text = text; return nil }

func asyncTestProvider(t *testing.T, term *asyncTerminal) (*Provider, *Thread) {
	t.Helper()
	th, _ := loadThread(t, "async_question.json")
	raw, _ := json.Marshal(th)
	f := &fakeServer{t: t, thread: raw}
	pt := &pipeTransport{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
	go f.serve(pt, func(v any) { b, _ := json.Marshal(v); pt.in <- b })
	p := New("unused", "/nonexistent", term, deadletter.Nop{})
	p.reader = newRPCClient(pt, nil)
	t.Cleanup(p.reader.close)
	return p, th
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

func Test非同期質問の複数待ちを単一質問として扱わない(t *testing.T) {
	if focusedQuestion("• Queued follow-up inputs\n\n  Test another\n\n  Type your answer\n\n  enter submit   ⌥ + ↓ main prompt", model.Question{Question: "Test"}) {
		t.Fatal("partial title matches a different question")
	}
	if !collapsedQuestion(strings.Replace(collapsedAsync, "1 question", "1 question · 20s", 1)) {
		t.Fatal("countdown hides pending question")
	}
	if collapsedQuestion(strings.Replace(collapsedAsync, "1 question", "2 questions", 1)) {
		t.Fatal("ambiguous queue")
	}
	th, _ := loadThread(t, "async_question.json")
	th.Turns[0].Items[0] = json.RawMessage(`{"type":"agentMessage","id":"q","delivery":"async","questions":[{"title":"a"},{"title":"b"}]}`)
	if latestAsyncQuestion(th) != nil {
		t.Fatal("multi-question queue is ambiguous")
	}
}
