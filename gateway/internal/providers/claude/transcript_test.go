package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"math"
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

func TestClaudeの作業中に送ったメッセージも会話に表示される(t *testing.T) {
	tr, rec, ws := loadFixture(t, "queued_message.jsonl")
	working := &providers.Live{PaneID: "w1:p1", HerdrStatus: herdr.StatusWorking}
	msgs := tr.Messages(ParseOptions{SessionID: "claude:test", Root: ws, Live: working, Sink: rec})
	assertGolden(t, "queued_message.golden.json", msgs)
	if len(rec.Entries) != 0 {
		t.Errorf("dead letters = %+v, want none", rec.Entries)
	}

	texts := func(ms []model.Message) []string {
		var out []string
		for _, m := range ms {
			for _, b := range m.Blocks {
				if b.Type == model.BlockText {
					out = append(out, string(m.Role)+":"+b.Text)
				}
			}
		}
		return out
	}
	count := func(ms []model.Message, want string) int {
		n := 0
		for _, s := range texts(ms) {
			if s == want {
				n++
			}
		}
		return n
	}
	// ターンに取り込まれた分は attachment からのみ、キュー経由で
	// 通常のプロンプトになった分は user エントリからのみ表示する
	if n := count(msgs, "user:ごめん、テストも直して"); n != 1 {
		t.Errorf("取り込まれたメッセージ = %d 件, want 1 (%v)", n, texts(msgs))
	}
	if n := count(msgs, "user:CIも直して"); n != 1 {
		t.Errorf("キューから実行されたメッセージ = %d 件, want 1 (%v)", n, texts(msgs))
	}
	// まだ待機中の分は末尾に出す
	last := msgs[len(msgs)-1]
	if last.Role != model.RoleUser || last.Blocks[0].Text != "ついでに README も更新して" {
		t.Errorf("待機中のメッセージが末尾にない: %+v", last)
	}

	// 動いていないセッションでは、送られないまま残ったキューは表示しない
	offline := tr.Messages(ParseOptions{SessionID: "claude:test", Root: ws, Sink: rec})
	if n := count(offline, "user:ついでに README も更新して"); n != 0 {
		t.Errorf("停止中に待機中メッセージを表示した: %v", texts(offline))
	}
	if n := count(offline, "user:ごめん、テストも直して"); n != 1 {
		t.Errorf("停止中の取り込み済みメッセージ = %d 件, want 1", n)
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

	if err := p.Send(context.Background(), id, nil, model.Input{Text: "x"}); err != providers.ErrNotLive {
		t.Errorf("offline send err = %v", err)
	}
}

func Test画像はパスを個別にブラケットペーストしてから本文を送信する(t *testing.T) {
	term := &fakeTerminal{}
	p := New(t.TempDir(), term, deadletter.Nop{})
	p.keyDelay = 0
	live := &providers.Live{PaneID: "w1:p1", HerdrStatus: herdr.StatusIdle}

	if err := p.Send(context.Background(), "x", live, model.Input{Text: " 見て ", Images: []string{"/tmp/a.png", "/tmp/b.jpg"}}); err != nil {
		t.Fatal(err)
	}
	want := "text:\x1b[200~/tmp/a.png\x1b[201~|text:\x1b[200~/tmp/b.jpg\x1b[201~|prompt:見て"
	if got := strings.Join(term.calls, "|"); got != want {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

func Test本文なしの画像だけならペースト後にEnterで送信する(t *testing.T) {
	term := &fakeTerminal{}
	p := New(t.TempDir(), term, deadletter.Nop{})
	p.keyDelay = 0
	live := &providers.Live{PaneID: "w1:p1", HerdrStatus: herdr.StatusIdle}

	if err := p.Send(context.Background(), "x", live, model.Input{Images: []string{"/tmp/a.png"}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(term.calls, "|"); got != "text:\x1b[200~/tmp/a.png\x1b[201~|keys:enter" {
		t.Errorf("calls = %q", got)
	}
	if err := p.Send(context.Background(), "x", live, model.Input{Text: "  "}); err == nil {
		t.Error("empty message should fail")
	}
}

func Test画像以外の添付はパスを本文に並べて送信する(t *testing.T) {
	term := &fakeTerminal{}
	p := New(t.TempDir(), term, deadletter.Nop{})
	p.keyDelay = 0
	live := &providers.Live{PaneID: "w1:p1", HerdrStatus: herdr.StatusIdle}

	in := model.Input{Text: "読んで", Images: []string{"/tmp/a.png"}, Files: []string{"/tmp/up/notes.txt"}}
	if err := p.Send(context.Background(), "x", live, in); err != nil {
		t.Fatal(err)
	}
	want := "text:\x1b[200~/tmp/a.png\x1b[201~|prompt:読んで\n/tmp/up/notes.txt"
	if got := strings.Join(term.calls, "|"); got != want {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

func Testダイアログ表示中は画像をペーストせずagent_blockedを返す(t *testing.T) {
	term := &fakeTerminal{}
	p := New(t.TempDir(), term, deadletter.Nop{})
	live := &providers.Live{PaneID: "w1:p1", HerdrStatus: herdr.StatusBlocked}

	err := p.Send(context.Background(), "x", live, model.Input{Text: "見て", Images: []string{"/tmp/a.png"}})
	var herr *herdr.Error
	if !errors.As(err, &herr) || herr.Code != "agent_blocked" {
		t.Errorf("err = %v, want agent_blocked", err)
	}
	if len(term.calls) != 0 {
		t.Errorf("calls = %v, want none", term.calls)
	}
}

func Test信頼ダイアログの2種類の文言に対応するキーを返す(t *testing.T) {
	p := New(t.TempDir(), &fakeTerminal{}, deadletter.Nop{})
	cases := map[string]string{
		"Quick safety check\n ❯ No, exit\n   Yes, I trust this folder":               "down,enter",
		"Do you trust the files in this folder?\n ❯ 1. Yes, proceed\n   2. No, exit": "enter",
		"❯ Try \"fix lint errors\"":                                                  "",
	}
	for screen, want := range cases {
		if got := strings.Join(p.StartupKeys(screen), ","); got != want {
			t.Errorf("%q -> %q, want %q", screen, got, want)
		}
	}
}

func TestClaudeのモデルとエフォートとモードは最新の記録から決まる(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-09-01T00:00:00Z","message":{"role":"user","content":"hi"}}`,
		`{"type":"permission-mode","permissionMode":"acceptEdits","sessionId":"s"}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-09-01T00:00:01Z","effort":"high","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-09-01T00:00:02Z","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"No response requested."}]}}`,
	}
	decode := func(ls []string) *Transcript {
		tr, err := Decode(strings.NewReader(strings.Join(ls, "\n")), "claude:test", deadletter.Nop{})
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	if s := decode(lines).summary(ParseOptions{}); s.Model != "claude-opus-5" || s.Effort != "high" || s.Mode != "Accept edits" {
		t.Errorf("summary = %+v", s)
	}
	lines = append(lines,
		`{"type":"user","uuid":"u2","timestamp":"2026-09-01T00:00:03Z","message":{"role":"user","content":"<local-command-stdout>Set model to `+"`Sonnet 5`"+` and saved as your default for new sessions</local-command-stdout>"}}`)
	if s := decode(lines).summary(ParseOptions{}); s.Model != "Sonnet 5" {
		t.Errorf("model after /model = %q", s.Model)
	}
	lines = append(lines,
		`{"type":"user","uuid":"u3","permissionMode":"plan","timestamp":"2026-09-01T00:00:03Z","message":{"role":"user","content":"plan it"}}`,
		`{"type":"assistant","uuid":"a3","timestamp":"2026-09-01T00:00:04Z","effort":"max","message":{"role":"assistant","model":"claude-sonnet-5","content":[{"type":"text","text":"ok"}]}}`)
	if s := decode(lines).summary(ParseOptions{}); s.Model != "claude-sonnet-5" || s.Effort != "max" || s.Mode != "Plan" {
		t.Errorf("summary after changes = %+v", s)
	}
}

func Test起動時のモデルとエフォート指定をCLI引数にする(t *testing.T) {
	p := New(t.TempDir(), nil, deadletter.Nop{})
	args := func(model, effort string) []string {
		return p.LaunchArgs(providers.LaunchOptions{Model: model, Effort: effort, Cwd: "/w"})
	}
	if a := args("", ""); a != nil {
		t.Errorf("default = %v", a)
	}
	if got := strings.Join(args("sonnet", ""), " "); got != "--model sonnet" {
		t.Errorf("args = %q", got)
	}
	if got := strings.Join(args("opus", "xhigh"), " "); got != "--model opus --effort xhigh" {
		t.Errorf("args with effort = %q", got)
	}
	// エフォートだけの指定も通す
	if got := strings.Join(args("", "max"), " "); got != "--effort max" {
		t.Errorf("effort only = %q", got)
	}
	cat, _ := p.Models(context.Background())
	for _, m := range cat.Models {
		if !providers.ValidModelID(m.ID) {
			t.Errorf("invalid id %q", m.ID)
		}
	}
	if len(cat.Efforts) == 0 {
		t.Error("efforts are missing")
	}
	for _, e := range cat.Efforts {
		if !providers.ValidEffortID(e.ID) || e.Name == "" {
			t.Errorf("invalid effort %+v", e)
		}
	}
}

func Testタスク通知は要約とイベントだけをシステムメッセージにする(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "バックグラウンドコマンドの完了通知",
			in: "<task-notification>\n<task-id>bi5dkt941</task-id>\n" +
				"<tool-use-id>toolu_013xbZk4Ba1RWRpdEbGQQc1f</tool-use-id>\n" +
				"<output-file>/private/tmp/claude-501/x/tasks/bi5dkt941.output</output-file>\n" +
				"<status>completed</status>\n" +
				"<summary>Background command \"Wait for emulator boot\" completed (exit code 0)</summary>\n" +
				"</task-notification>",
			want: `Background command "Wait for emulator boot" completed (exit code 0)`,
		},
		{
			name: "Monitorのイベント通知",
			in: "<task-notification>\n<task-id>bdoll59l8</task-id>\n" +
				"<summary>Monitor event: \"CI の完了状況\"</summary>\n" +
				"<event>[17:40:54] 01-get-user-resource: OK rc=0 1539s</event>\n" +
				"If this event is something the user would act on now, send a PushNotification.\n" +
				"</task-notification>",
			want: "Monitor event: \"CI の完了状況\"\n[17:40:54] 01-get-user-resource: OK rc=0 1539s",
		},
		{
			name: "要約がなければタグを落として本文だけ残す",
			in:   "<task-notification>\n<task-id>abc</task-id>\n</task-notification>",
			want: "abc",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role, got := userText(tt.in)
			if role != model.RoleSystem {
				t.Errorf("role = %q, want %q", role, model.RoleSystem)
			}
			if got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClaudeのコンテキスト使用量は最新の本流の応答から決まる(t *testing.T) {
	usage := `"usage":{"input_tokens":2,"cache_creation_input_tokens":1000,"cache_read_input_tokens":59000,"output_tokens":998}`
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-09-01T00:00:00Z","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-09-01T00:00:01Z","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"hello"}],` + usage + `}}`,
	}
	decode := func(ls []string) *Transcript {
		tr, err := Decode(strings.NewReader(strings.Join(ls, "\n")), "claude:test", deadletter.Nop{})
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	// 61000 / 1000000 = 6%; current Opus defaults to the 1M window.
	if c := decode(lines).summary(ParseOptions{}).Context; c == nil || c.UsedTokens != 61000 || c.WindowTokens != 1_000_000 || c.UsedPercent != 6 {
		t.Errorf("context = %+v", c)
	}
	// サブエージェント(sidechain)は自分の窓を使うので本流の値を上書きしない。
	lines = append(lines,
		`{"type":"assistant","uuid":"s1","isSidechain":true,"timestamp":"2026-09-01T00:00:02Z","message":{"role":"assistant","model":"claude-haiku-4-5","content":[{"type":"text","text":"sub"}],"usage":{"input_tokens":5,"cache_read_input_tokens":100,"output_tokens":5}}}`)
	if c := decode(lines).summary(ParseOptions{}).Context; c == nil || c.UsedTokens != 61000 {
		t.Errorf("context after sidechain = %+v", c)
	}
	// modelアタッチメントだけが1M版かどうかを伝える。
	lines = append(lines,
		`{"type":"attachment","uuid":"m1","timestamp":"2026-09-01T00:00:03Z","attachment":{"type":"model","identity":{"modelId":"claude-opus-5[1m]"}}}`)
	if c := decode(lines).summary(ParseOptions{}).Context; c == nil || c.WindowTokens != 1_000_000 || c.UsedPercent != 6 {
		t.Errorf("context with 1M model = %+v", c)
	}
}

func TestClaudeのモデルごとのコンテキスト窓を判定する(t *testing.T) {
	cases := map[string]int64{
		"fable":                         1_000_000,
		"claude-fable-5-1":              1_000_000,
		"opus":                          1_000_000,
		"claude-opus-5":                 1_000_000,
		"claude-opus-4-8":               1_000_000,
		"sonnet":                        1_000_000,
		"claude-sonnet-5":               1_000_000,
		"claude-sonnet-4-6":             1_000_000,
		"claude-haiku-4-5-20251001":     200_000,
		"claude-sonnet-4-5-20250929":    200_000,
		"claude-opus-5[1m]":             1_000_000,
		"claude-haiku-4-5-20251001[1m]": 1_000_000,
	}
	for id, want := range cases {
		if got := contextWindow(id); got != want {
			t.Errorf("%s = %d, want %d", id, got, want)
		}
	}
}

func Test使用量の記録がないセッションはコンテキスト不明になる(t *testing.T) {
	tr, err := Decode(strings.NewReader(
		`{"type":"assistant","uuid":"a1","timestamp":"2026-09-01T00:00:01Z","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"hi"}]}}`,
	), "claude:test", deadletter.Nop{})
	if err != nil {
		t.Fatal(err)
	}
	if c := tr.summary(ParseOptions{}).Context; c != nil {
		t.Errorf("context = %+v, want nil", c)
	}
}

func TestClaudeのコストは応答ごとのトークン数から見積もる(t *testing.T) {
	// 同じ応答がブロックごとに複数行書かれるので、message.idで一度だけ数える。
	usage := `"usage":{"input_tokens":1000,"cache_read_input_tokens":1000000,"output_tokens":10000,` +
		`"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":20000}}`
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-09-01T00:00:00Z","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-09-01T00:00:01Z","message":{"id":"msg_1","role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"hello"}],` + usage + `}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-09-01T00:00:01Z","message":{"id":"msg_1","role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}],` + usage + `}}`,
	}
	decode := func(ls []string) *Transcript {
		tr, err := Decode(strings.NewReader(strings.Join(ls, "\n")), "claude:test", deadletter.Nop{})
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	// 入力1000*$5 + 1時間キャッシュ書き込み20000*$10 + 読み出し1000000*$0.5 + 出力10000*$25 ($/Mトークン)
	want := 0.005 + 0.2 + 0.5 + 0.25
	c := decode(lines).summary(ParseOptions{}).Cost
	if c == nil || math.Abs(c.USD-want) > 1e-9 || !c.Estimated {
		t.Errorf("cost = %+v, want %v(見積もり)", c, want)
	}
	// サブエージェント(sidechain)も同じセッションの支払いなので足す。
	lines = append(lines,
		`{"type":"assistant","uuid":"s1","isSidechain":true,"timestamp":"2026-09-01T00:00:02Z","message":{"id":"msg_2","role":"assistant","model":"claude-haiku-4-5","content":[{"type":"text","text":"sub"}],"usage":{"input_tokens":1000000,"output_tokens":0}}}`)
	if c := decode(lines).summary(ParseOptions{}).Cost; c == nil || math.Abs(c.USD-(want+1)) > 1e-9 {
		t.Errorf("cost with sidechain = %+v, want %v", c, want+1)
	}
	// 終了したセッションはClaude Code自身が記録した正確な金額を使う。
	lines = append(lines, `{"type":"cost-state","sessionId":"test","totalCostUSD":2.5}`)
	if c := decode(lines).summary(ParseOptions{}).Cost; c == nil || c.USD != 2.5 || c.Estimated {
		t.Errorf("cost from cost-state = %+v, want 2.5(実績)", c)
	}
}

func Test応答のないセッションはコストを表示しない(t *testing.T) {
	tr, err := Decode(strings.NewReader(
		`{"type":"user","uuid":"u1","timestamp":"2026-09-01T00:00:00Z","message":{"role":"user","content":"hi"}}`,
	), "claude:test", deadletter.Nop{})
	if err != nil {
		t.Fatal(err)
	}
	if c := tr.summary(ParseOptions{}).Cost; c != nil {
		t.Errorf("cost = %+v, want nil", c)
	}
}
