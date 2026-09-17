package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

var update = flag.Bool("update", false, "update golden files")

const testdata = "../../../testdata/claude"

// loadFixture reads a fixture, pointing "/work/app" at the test workspace so
// file references resolve.
func loadFixture(t *testing.T, name string) (*Transcript, *deadletter.Recorder, string) {
	t.Helper()
	ws, err := filepath.Abs(filepath.Join(testdata, "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(testdata, name))
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte("/work/app"), []byte(ws))
	rec := &deadletter.Recorder{}
	tr, err := Decode(bytes.NewReader(raw), "claude:test", rec)
	if err != nil {
		t.Fatal(err)
	}
	return tr, rec, ws
}

func assertGolden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(testdata, name)
	if *update {
		if err := os.WriteFile(path, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s missing (run go test -update): %v", name, err)
	}
	if string(bytes.TrimSpace(want)) != string(bytes.TrimSpace(got)) {
		t.Errorf("%s mismatch\n--- got ---\n%s", name, got)
	}
}

func TestClaudeの通常の会話を共通メッセージに変換できる(t *testing.T) {
	tr, rec, ws := loadFixture(t, "normal.jsonl")
	msgs := tr.Messages(ParseOptions{SessionID: "claude:test", Root: ws, Sink: rec})
	assertGolden(t, "normal.golden.json", msgs)
	if len(rec.Entries) != 0 {
		t.Errorf("dead letters = %+v, want none", rec.Entries)
	}
	if tr.Title != "認証処理の修正" {
		t.Errorf("title = %q", tr.Title)
	}
}

func TestClaudeのAskUserQuestionを構造化して回答結果も表示できる(t *testing.T) {
	tr, rec, _ := loadFixture(t, "ask_user_question.jsonl")
	msgs := tr.Messages(ParseOptions{SessionID: "claude:test", Sink: rec})
	assertGolden(t, "ask_user_question.golden.json", msgs)
	if len(rec.Entries) != 0 {
		t.Errorf("dead letters = %+v, want none", rec.Entries)
	}
	var found []*model.Interaction
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == model.BlockInteraction {
				found = append(found, b.Interaction)
			}
		}
	}
	if len(found) != 2 {
		t.Fatalf("interactions = %d, want 2", len(found))
	}
	first := found[0]
	if first.State != model.InteractionAnswered || len(first.Questions) != 2 || first.Questions[1].Type != model.QuestionMultiSelect {
		t.Errorf("unexpected first interaction: %+v", first)
	}
	if !strings.Contains(first.Answer, "Green") {
		t.Errorf("answer summary = %q", first.Answer)
	}
}

func TestClaudeの承認待ちツールはブロック中のみ承認インタラクションになる(t *testing.T) {
	tr, _, _ := loadFixture(t, "pending_approval.jsonl")

	blocked := &providers.Live{PaneID: "w1:p1", HerdrStatus: herdr.StatusBlocked}
	msgs := tr.Messages(ParseOptions{SessionID: "claude:test", Live: blocked})
	last := msgs[len(msgs)-1]
	ia := last.Blocks[len(last.Blocks)-1].Interaction
	if ia == nil || ia.Type != model.InteractionApproval || ia.State != model.InteractionPending || ia.Detail != "touch approved.txt" {
		t.Fatalf("expected pending approval, got %+v", last.Blocks)
	}
	if s := tr.summary(ParseOptions{Live: blocked}); s.Pending != model.InteractionApproval {
		t.Errorf("summary pending = %q", s.Pending)
	}

	working := &providers.Live{PaneID: "w1:p1", HerdrStatus: herdr.StatusWorking}
	msgs = tr.Messages(ParseOptions{SessionID: "claude:test", Live: working})
	for _, b := range msgs[len(msgs)-1].Blocks {
		if b.Type == model.BlockInteraction {
			t.Errorf("no approval expected while working: %+v", b.Interaction)
		}
	}
}

func TestClaudeの未知データはフォールバック表示しつつdeadletterに記録する(t *testing.T) {
	tr, rec, _ := loadFixture(t, "unknown_tool.jsonl")
	msgs := tr.Messages(ParseOptions{SessionID: "claude:test", Sink: rec})
	assertGolden(t, "unknown_tool.golden.json", msgs)

	kinds := map[deadletter.Kind]int{}
	for _, e := range rec.Entries {
		kinds[e.Kind]++
		if len(e.Raw) == 0 {
			t.Errorf("dead letter without raw payload: %+v", e)
		}
	}
	want := map[deadletter.Kind]int{
		deadletter.ParseError:     1, // truncated line
		deadletter.UnknownContent: 2, // FooTool, hologram
		deadletter.UnknownEvent:   2, // brand-new-event, future-metadata
	}
	for k, n := range want {
		if kinds[k] != n {
			t.Errorf("dead letters of kind %s = %d, want %d (all: %+v)", k, kinds[k], n, rec.Entries)
		}
	}
	// 壊れた行があっても前後のメッセージは失われない
	if msgs[0].Blocks[0].Text != "test" || msgs[len(msgs)-1].Blocks[0].Text != "完了" {
		t.Errorf("surrounding messages lost: %+v", msgs)
	}
}

func TestClaudeのAPIエラーで終わったターンは失敗扱いになる(t *testing.T) {
	tr, _, _ := loadFixture(t, "normal.jsonl")
	s := tr.summary(ParseOptions{})
	if !s.LastTurnFailed {
		t.Error("LastTurnFailed = false, want true")
	}
	if s.LastMessage != "API Error: 529 overloaded" && s.LastMessage != "この画像を見て" {
		t.Errorf("last message = %q", s.LastMessage)
	}
}

func TestClaudeのインライン画像を取り出せる(t *testing.T) {
	tr, _, _ := loadFixture(t, "normal.jsonl")
	src, ok := tr.imageAt("u5", 0)
	if !ok || src.MediaType != "image/png" {
		t.Fatalf("image not found: %+v", src)
	}
	if _, ok := tr.imageAt("u5", 1); ok {
		t.Error("unexpected second image")
	}
}

type fakeTerminal struct {
	calls []string
}

func (f *fakeTerminal) SendKeys(_ context.Context, pane string, keys ...string) error {
	f.calls = append(f.calls, "keys:"+strings.Join(keys, ","))
	return nil
}
func (f *fakeTerminal) SendText(_ context.Context, pane, text string) error {
	f.calls = append(f.calls, "text:"+text)
	return nil
}
func (f *fakeTerminal) Prompt(_ context.Context, pane, text string) error {
	f.calls = append(f.calls, "prompt:"+text)
	return nil
}
func (f *fakeTerminal) ReadPane(context.Context, string, int) (*herdr.ReadResult, error) {
	return &herdr.ReadResult{}, nil
}

func TestAskUserQuestionへの回答をキー操作に変換できる(t *testing.T) {
	ia := &model.Interaction{Type: model.InteractionQuestions, Questions: []model.Question{
		{ID: "0", Type: model.QuestionSelect, Options: []model.Option{{Label: "Red"}, {Label: "Green"}, {Label: "Blue"}}},
		{ID: "1", Type: model.QuestionMultiSelect, Options: []model.Option{{Label: "Apple"}, {Label: "Banana"}, {Label: "Cherry"}}},
		{ID: "2", Type: model.QuestionSelect, Options: []model.Option{{Label: "A"}, {Label: "B"}}},
	}}
	steps, err := dialogKeys(ia, model.InteractionResponse{Answers: map[string]model.Answer{
		"0": {Selected: []string{"Green"}},
		"1": {Selected: []string{"Apple", "Cherry"}},
		"2": {Text: "free\ntext"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range steps {
		if s.text != "" {
			got = append(got, "text:"+s.text)
		} else {
			got = append(got, strings.Join(s.keys, ","))
		}
	}
	want := []string{
		"down,enter",
		"space,down,down,space,down",
		"down,enter",
		"down,down",
		"text:free text",
		"enter",
		"enter", // review tab
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("steps\n got %v\nwant %v", got, want)
	}

	if _, err := dialogKeys(ia, model.InteractionResponse{Answers: map[string]model.Answer{"0": {Selected: []string{"Pink"}}}}); err == nil {
		t.Error("unknown option should fail")
	}
}

func Test承認と拒否はEnterとEscに変換される(t *testing.T) {
	ia := &model.Interaction{Type: model.InteractionApproval}
	for decision, want := range map[string]string{model.DecisionApprove: "enter", model.DecisionDeny: "esc"} {
		steps, err := dialogKeys(ia, model.InteractionResponse{Decision: decision})
		if err != nil || len(steps) != 1 || steps[0].keys[0] != want {
			t.Errorf("%s -> %+v, %v", decision, steps, err)
		}
	}
	if _, err := dialogKeys(ia, model.InteractionResponse{Decision: "maybe"}); err == nil {
		t.Error("unknown decision should fail")
	}
}

func TestProviderはtranscriptを探して承認をペインへ送る(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "projects", "-work-playground")
	os.MkdirAll(proj, 0o755)
	raw, _ := os.ReadFile(filepath.Join(testdata, "pending_approval.jsonl"))
	id := "f21c11f9-c52b-4cf9-84c0-65208b400cde"
	os.WriteFile(filepath.Join(proj, id+".jsonl"), raw, 0o644)

	term := &fakeTerminal{}
	p := New(dir, term, deadletter.Nop{})
	p.keyDelay = 0
	live := &providers.Live{PaneID: "w1:p1", HerdrStatus: herdr.StatusBlocked}

	sum, err := p.Summary(context.Background(), id, live)
	if err != nil || sum.Pending != model.InteractionApproval {
		t.Fatalf("summary = %+v, %v", sum, err)
	}
	err = p.Respond(context.Background(), id, live, model.InteractionResponse{InteractionID: "toolu_01A7BGENJFCoiVFp3Xw3Rdox", Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(term.calls, "|") != "keys:enter" {
		t.Errorf("calls = %v", term.calls)
	}

	err = p.Respond(context.Background(), id, live, model.InteractionResponse{InteractionID: "nope", Decision: "approve"})
	if err != providers.ErrInteractionGone {
		t.Errorf("err = %v, want ErrInteractionGone", err)
	}
	if _, err := p.Summary(context.Background(), "../etc", nil); err != providers.ErrNotFound {
		t.Errorf("invalid id err = %v", err)
	}

	if err := p.Send(context.Background(), id, live, model.Input{Text: "見て", Images: []string{"/tmp/x.png"}}); err != nil {
		t.Fatal(err)
	}
	if last := term.calls[len(term.calls)-1]; last != "prompt:見て\n\n[Attached image: /tmp/x.png]" {
		t.Errorf("prompt = %q", last)
	}
	if err := p.Send(context.Background(), id, nil, model.Input{Text: "x"}); err != providers.ErrNotLive {
		t.Errorf("offline send err = %v", err)
	}
}

func Test信頼ダイアログの2種類の文言に対応するキーを返す(t *testing.T) {
	p := New(t.TempDir(), &fakeTerminal{}, deadletter.Nop{})
	cases := map[string]string{
		"Quick safety check\n ❯ No, exit\n   Yes, I trust this folder": "down,enter",
		"Do you trust the files in this folder?\n ❯ 1. Yes, proceed\n   2. No, exit": "enter",
		"❯ Try \"fix lint errors\"": "",
	}
	for screen, want := range cases {
		if got := strings.Join(p.StartupKeys(screen), ","); got != want {
			t.Errorf("%q -> %q, want %q", screen, got, want)
		}
	}
}

func TestClaudeのモデルは最新の応答と_modelコマンドから決まる(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-09-01T00:00:00Z","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-09-01T00:00:01Z","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-09-01T00:00:02Z","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"No response requested."}]}}`,
	}
	decode := func(ls []string) *Transcript {
		tr, err := Decode(strings.NewReader(strings.Join(ls, "\n")), "claude:test", deadletter.Nop{})
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	if s := decode(lines).summary(ParseOptions{}); s.Model != "claude-opus-5" {
		t.Errorf("model = %q", s.Model)
	}
	lines = append(lines,
		`{"type":"user","uuid":"u2","timestamp":"2026-09-01T00:00:03Z","message":{"role":"user","content":"<local-command-stdout>Set model to `+"`Sonnet 5`"+` and saved as your default for new sessions</local-command-stdout>"}}`)
	if s := decode(lines).summary(ParseOptions{}); s.Model != "Sonnet 5" {
		t.Errorf("model after /model = %q", s.Model)
	}
	lines = append(lines,
		`{"type":"assistant","uuid":"a3","timestamp":"2026-09-01T00:00:04Z","message":{"role":"assistant","model":"claude-sonnet-5","content":[{"type":"text","text":"ok"}]}}`)
	if s := decode(lines).summary(ParseOptions{}); s.Model != "claude-sonnet-5" {
		t.Errorf("model after reply = %q", s.Model)
	}
}

func Test起動時のモデル指定をCLI引数にする(t *testing.T) {
	p := New(t.TempDir(), nil, deadletter.Nop{})
	if args := p.LaunchArgs(""); args != nil {
		t.Errorf("default = %v", args)
	}
	if got := strings.Join(p.LaunchArgs("sonnet"), " "); got != "--model sonnet" {
		t.Errorf("args = %q", got)
	}
	models, _ := p.Models(context.Background())
	for _, m := range models {
		if !providers.ValidModelID(m.ID) {
			t.Errorf("invalid id %q", m.ID)
		}
	}
}
