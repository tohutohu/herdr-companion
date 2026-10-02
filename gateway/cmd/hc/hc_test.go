package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// fakeGateway serves the few Gateway endpoints hc uses from memory.
type fakeGateway struct {
	mu       sync.Mutex
	sessions []model.Session
	messages map[string][]model.Message
	posted   []postedRequest
	// onPost lets a test change state when a command posts.
	onPost func(path string, body map[string]any)
}

type postedRequest struct {
	Path string
	Body map[string]any
}

func (g *fakeGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer secret" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	writeJSON := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	path := r.URL.Path
	if r.Method == http.MethodPost {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		g.posted = append(g.posted, postedRequest{path, body})
		if g.onPost != nil {
			g.onPost(path, body)
		}
		w.WriteHeader(http.StatusAccepted)
		writeJSON(map[string]bool{"ok": true})
		return
	}
	switch {
	case path == "/v1/sessions":
		if r.URL.Query().Get("archived") == "true" {
			writeJSON(map[string]any{"sessions": []model.Session{}})
			return
		}
		writeJSON(map[string]any{"sessions": g.sessions})
		return
	case strings.HasSuffix(path, "/messages"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/sessions/"), "/messages")
		writeJSON(map[string]any{"session": g.find(id), "messages": g.messages[id]})
		return
	case strings.HasPrefix(path, "/v1/sessions/"):
		writeJSON(g.find(strings.TrimPrefix(path, "/v1/sessions/")))
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (g *fakeGateway) find(id string) model.Session {
	for _, s := range g.sessions {
		if s.ID == id {
			return s
		}
	}
	return model.Session{}
}

func (g *fakeGateway) set(fn func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	fn()
}

// setStatus changes a session's status and bumps its update time.
func (g *fakeGateway) setStatus(id string, st model.Status) {
	g.set(func() {
		for i := range g.sessions {
			if g.sessions[i].ID == id {
				g.sessions[i].Status = st
				g.sessions[i].UpdatedAt = g.sessions[i].UpdatedAt.Add(time.Minute)
			}
		}
	})
}

func newTestApp(t *testing.T, g *fakeGateway) (*app, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	out := &bytes.Buffer{}
	a := &app{
		out: out, errOut: out, in: strings.NewReader(""),
		client:   newClient(connection{baseURL: srv.URL, token: "secret"}),
		state:    &stateStore{dir: t.TempDir()},
		selfPane: "self:p1",
	}
	old := pollInterval
	pollInterval = 5 * time.Millisecond
	t.Cleanup(func() { pollInterval = old })
	return a, out
}

func (a *app) mustRun(t *testing.T, args ...string) string {
	t.Helper()
	buf := a.out.(*bytes.Buffer)
	buf.Reset()
	if code := a.run(args); code != 0 {
		t.Fatalf("hc %s exited %d:\n%s", strings.Join(args, " "), code, buf.String())
	}
	return buf.String()
}

var t0 = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

func text(id string, role model.Role, minute int, s string) model.Message {
	return model.Message{ID: id, Role: role, Timestamp: t0.Add(time.Duration(minute) * time.Minute), Blocks: []model.Block{model.TextBlock(s)}}
}

func sampleGateway() *fakeGateway {
	return &fakeGateway{
		sessions: []model.Session{
			{ID: "claude:aaaa1111-0000", Provider: "claude", Project: "app", Title: "ログイン修正", Status: model.StatusIdle, UpdatedAt: t0, PaneID: "w1:p1", CanSend: true},
			{ID: "claude:aaaa2222-0000", Provider: "claude", Project: "app", Status: model.StatusRunning, UpdatedAt: t0, PaneID: "w2:p1", CanSend: true},
			{ID: "codex:bbbb3333", Provider: "codex", Project: "api", Status: model.StatusIdle, UpdatedAt: t0, PaneID: "w3:p1", CanSend: true},
			{ID: "claude:ffff0000-self", Provider: "claude", Project: "boss", Status: model.StatusRunning, UpdatedAt: t0, PaneID: "self:p1", CanSend: true},
		},
		messages: map[string][]model.Message{
			"claude:aaaa1111-0000": {
				text("m1", model.RoleUser, 0, "ログインを直して"),
				text("m2", model.RoleAssistant, 1, "Find the handler\n$ grep -rn login src"),
				text("m3", model.RoleTool, 1, "src/login.go:10\nsrc/login.go:20"),
				text("m4", model.RoleAssistant, 2, "Read src/login.go"),
				text("m5", model.RoleAssistant, 3, "直しました。"),
			},
		},
	}
}

func Testツール呼び出しは散文の間で1行にまとまる(t *testing.T) {
	g := sampleGateway()
	rows := foldRows(g.messages["claude:aaaa1111-0000"])
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3: %+v", len(rows), rows)
	}
	out := &bytes.Buffer{}
	renderRow(out, rows[1], renderOpts{short: "aaaa1111"})
	if got := out.String(); got != "   ⋯ 2 tool calls, latest: Read src/login.go\n" {
		t.Errorf("got %q", got)
	}
}

func Test読むたびに前回以降の新着だけを表示する(t *testing.T) {
	g := sampleGateway()
	a, _ := newTestApp(t, g)

	first := a.mustRun(t, "read", "aaaa1111")
	if !strings.Contains(first, "ログインを直して") || !strings.Contains(first, "直しました。") {
		t.Fatalf("first read:\n%s", first)
	}
	if again := a.mustRun(t, "read", "aaaa1111"); !strings.Contains(again, "(no new messages)") {
		t.Fatalf("second read should be empty:\n%s", again)
	}

	g.set(func() {
		id := "claude:aaaa1111-0000"
		g.messages[id] = append(g.messages[id], text("m6", model.RoleUser, 4, "テストも"), text("m7", model.RoleAssistant, 5, "書きま"))
	})
	got := a.mustRun(t, "read", "aaaa1111")
	if strings.Contains(got, "直しました。") || !strings.Contains(got, "テストも") || !strings.Contains(got, "書きま") {
		t.Fatalf("third read:\n%s", got)
	}

	// The last message grows while the agent writes: it is shown again.
	g.set(func() {
		id := "claude:aaaa1111-0000"
		g.messages[id][len(g.messages[id])-1] = text("m7", model.RoleAssistant, 5, "書きました。")
	})
	got = a.mustRun(t, "read", "aaaa1111")
	if !strings.Contains(got, "書きました。") || strings.Contains(got, "テストも") {
		t.Fatalf("updated read:\n%s", got)
	}
}

func Test長い本文は切り詰めて全文の取り出し方を示す(t *testing.T) {
	g := sampleGateway()
	g.messages["claude:aaaa1111-0000"] = []model.Message{text("long-message-id", model.RoleAssistant, 0, strings.Repeat("あ", 50))}
	a, _ := newTestApp(t, g)
	got := a.mustRun(t, "read", "aaaa1111", "--max", "10")
	if !strings.Contains(got, strings.Repeat("あ", 10)+"… (+40 chars; full: hc read aaaa1111 --msg long-mes)") {
		t.Fatalf("got:\n%s", got)
	}
	full := a.mustRun(t, "read", "aaaa1111", "--msg", "long-mes")
	if !strings.Contains(full, strings.Repeat("あ", 50)) {
		t.Fatalf("full:\n%s", full)
	}
}

func Test保留中の質問には回答コマンドを添える(t *testing.T) {
	g := sampleGateway()
	g.messages["claude:aaaa1111-0000"] = append(g.messages["claude:aaaa1111-0000"], model.Message{
		ID: "q", Role: model.RoleAssistant, Timestamp: t0.Add(10 * time.Minute),
		Blocks: []model.Block{{Type: model.BlockInteraction, Interaction: &model.Interaction{
			ID: "toolu_1", Type: model.InteractionQuestions, State: model.InteractionPending, Supported: true,
			Questions: []model.Question{{ID: "q1", Type: model.QuestionSelect, Question: "DBは？", AllowOther: true,
				Options: []model.Option{{Label: "SQLite"}, {Label: "Postgres", Description: "本番と同じ"}}}},
		}}},
	})
	g.sessions[0].Status = model.StatusWaitingInput
	a, _ := newTestApp(t, g)
	got := a.mustRun(t, "show", "aaaa1111")
	for _, want := range []string{"[question PENDING]", "1) SQLite", "2) Postgres — 本番と同じ", "*) free text", "answer: hc answer aaaa1111 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	a.mustRun(t, "answer", "aaaa1111", "2")
	last := g.posted[len(g.posted)-1]
	if last.Path != "/v1/sessions/claude:aaaa1111-0000/respond" {
		t.Fatalf("posted to %s", last.Path)
	}
	b, _ := json.Marshal(last.Body)
	if string(b) != `{"answers":{"q1":{"selected":["Postgres"]}},"interactionId":"toolu_1"}` {
		t.Errorf("body = %s", b)
	}
}

func Test回答は番号ラベル自由入力と承認の別名を受け付ける(t *testing.T) {
	q := model.Interaction{ID: "i", Type: model.InteractionQuestions, Questions: []model.Question{
		{ID: "a", Type: model.QuestionSelect, Options: []model.Option{{Label: "Yes"}, {Label: "No"}}},
		{ID: "b", Type: model.QuestionMultiSelect, AllowOther: true, Options: []model.Option{{Label: "x"}, {Label: "y"}, {Label: "z"}}},
	}}
	res, _, err := buildResponse(q, []string{"a=no", "b=1,3"})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Answers["a"].Selected; len(got) != 1 || got[0] != "No" {
		t.Errorf("a = %+v", res.Answers["a"])
	}
	if got := res.Answers["b"].Selected; len(got) != 2 || got[0] != "x" || got[1] != "z" {
		t.Errorf("b = %+v", res.Answers["b"])
	}
	if res, _, _ = buildResponse(q, []string{"a=1", "b=全部"}); res.Answers["b"].Text != "全部" {
		t.Errorf("free text = %+v", res.Answers["b"])
	}
	if _, _, err := buildResponse(q, []string{"a=maybe", "b=1"}); err == nil || !strings.Contains(err.Error(), "1) Yes") {
		t.Errorf("an unknown option without free text must list the options, got %v", err)
	}
	if _, _, err := buildResponse(q, []string{"a=1"}); err == nil {
		t.Error("an unanswered question must be an error")
	}
	if _, _, err := buildResponse(q, []string{"1"}); err == nil {
		t.Error("a bare answer must be refused when there are several questions")
	}

	single := model.Interaction{ID: "i", Type: model.InteractionQuestions, Questions: []model.Question{{ID: "q", Type: model.QuestionText}}}
	if res, _, _ := buildResponse(single, []string{"use", "sqlite"}); res.Answers["q"].Text != "use sqlite" {
		t.Errorf("words of a bare answer are joined: %+v", res.Answers["q"])
	}

	approval := model.Interaction{ID: "i", Type: model.InteractionApproval, Decisions: []string{"approve", "deny"}}
	if res, _, _ := buildResponse(approval, []string{"yes"}); res.Decision != "approve" {
		t.Errorf("yes = %q", res.Decision)
	}
	if _, _, err := buildResponse(approval, []string{"always"}); err == nil {
		t.Error("a decision the approval does not offer must be refused")
	}
}

func Test状態の変化のうち対応が必要なものだけを注意として扱う(t *testing.T) {
	cases := []struct {
		from, to model.Status
		want     bool
	}{
		{model.StatusRunning, model.StatusWaitingApproval, true},
		{model.StatusRunning, model.StatusCompleted, true},
		{model.StatusRunning, model.StatusIdle, true},
		{model.StatusCompleted, model.StatusIdle, false},
		{model.StatusIdle, model.StatusRunning, false},
		{model.StatusIdle, model.StatusOffline, false},
	}
	for _, c := range cases {
		if got := attention(c.from, c.to); got != c.want {
			t.Errorf("%s → %s = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func Test変化の一覧は自分自身のセッションを含めない(t *testing.T) {
	g := sampleGateway()
	a, _ := newTestApp(t, g)
	if got := a.mustRun(t, "changes"); !strings.Contains(got, "first run") {
		t.Fatalf("first changes:\n%s", got)
	}
	g.setStatus("claude:aaaa2222-0000", model.StatusCompleted)
	g.setStatus("claude:ffff0000-self", model.StatusCompleted)
	got := a.mustRun(t, "changes")
	if !strings.Contains(got, "! aaaa2222 running → completed") || strings.Contains(got, "ffff0000") {
		t.Fatalf("changes:\n%s", got)
	}
	if got := a.mustRun(t, "changes"); !strings.Contains(got, "no changes") {
		t.Fatalf("changes are reported once:\n%s", got)
	}
}

func Test待機は対応が必要になったセッションを保留中の質問付きで返す(t *testing.T) {
	g := sampleGateway()
	a, _ := newTestApp(t, g)
	a.mustRun(t, "changes")
	go func() {
		time.Sleep(30 * time.Millisecond)
		g.set(func() {
			g.messages["codex:bbbb3333"] = []model.Message{{ID: "x", Role: model.RoleAssistant, Timestamp: t0,
				Blocks: []model.Block{{Type: model.BlockInteraction, Interaction: &model.Interaction{
					ID: "appr", Type: model.InteractionApproval, State: model.InteractionPending, Supported: true,
					Title: "Run command", Decisions: []string{"approve", "deny"}}}}}}
		})
		g.setStatus("codex:bbbb3333", model.StatusWaitingApproval)
	}()
	got := a.mustRun(t, "wait", "--timeout", "5s")
	for _, want := range []string{"! bbbb3333 idle → waiting_approval", "[approval PENDING] Run command", "hc answer bbbb3333 approve | deny"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestID指定の待機は他のセッションの変化を後に残す(t *testing.T) {
	g := sampleGateway()
	a, _ := newTestApp(t, g)
	a.mustRun(t, "changes")
	g.setStatus("codex:bbbb3333", model.StatusRunning)
	g.setStatus("claude:aaaa2222-0000", model.StatusCompleted)
	got := a.mustRun(t, "wait", "aaaa2222", "--timeout", "5s")
	if !strings.Contains(got, "aaaa2222 running → completed") || strings.Contains(got, "bbbb3333") {
		t.Fatalf("wait:\n%s", got)
	}
	if got := a.mustRun(t, "changes"); !strings.Contains(got, "bbbb3333 idle → running") || strings.Contains(got, "aaaa2222") {
		t.Fatalf("the other session's change must remain:\n%s", got)
	}
}

func Test待機は時間切れなら動作中のセッションを伝えて終わる(t *testing.T) {
	g := sampleGateway()
	a, _ := newTestApp(t, g)
	a.mustRun(t, "changes")
	got := a.mustRun(t, "wait", "--timeout", "20ms")
	if !strings.Contains(got, "nothing needs you yet") || !strings.Contains(got, "still running: aaaa2222.") {
		t.Fatalf("got:\n%s", got)
	}
}

func Test送信して待つと応答の新着を表示する(t *testing.T) {
	g := sampleGateway()
	a, _ := newTestApp(t, g)
	a.mustRun(t, "read", "aaaa1111")
	id := "claude:aaaa1111-0000"
	g.onPost = func(path string, body map[string]any) {
		go func() {
			g.setStatus(id, model.StatusRunning)
			time.Sleep(20 * time.Millisecond)
			g.set(func() {
				g.messages[id] = append(g.messages[id], text("u", model.RoleUser, 9, body["text"].(string)), text("r", model.RoleAssistant, 10, "完了しました"))
			})
			g.setStatus(id, model.StatusCompleted)
		}()
	}
	got := a.mustRun(t, "send", "aaaa1111", "テストを", "実行して", "--wait", "--timeout", "5s")
	if g.posted[0].Body["text"] != "テストを 実行して" {
		t.Errorf("sent %v", g.posted[0].Body)
	}
	if !strings.Contains(got, "sent to aaaa1111") || !strings.Contains(got, "完了しました") || strings.Contains(got, "直しました。") {
		t.Fatalf("got:\n%s", got)
	}
}

func Test自分自身には送信しない(t *testing.T) {
	g := sampleGateway()
	a, out := newTestApp(t, g)
	if code := a.run([]string{"send", "ffff0000", "hi"}); code != 1 || !strings.Contains(out.String(), "yourself") {
		t.Fatalf("code %d: %s", code, out.String())
	}
	if len(g.posted) != 0 {
		t.Errorf("posted %v", g.posted)
	}
}

func Testセッションは前方一致かペインIDで指定できる(t *testing.T) {
	g := sampleGateway()
	cases := map[string]string{
		"aaaa1111":             "claude:aaaa1111-0000",
		"bbbb":                 "codex:bbbb3333",
		"codex:bbbb3333":       "codex:bbbb3333",
		"w2:p1":                "claude:aaaa2222-0000",
		"claude:aaaa2222-0000": "claude:aaaa2222-0000",
	}
	for ref, want := range cases {
		if s, _ := matchSession(g.sessions, ref); s.ID != want {
			t.Errorf("%s → %q, want %s", ref, s.ID, want)
		}
	}
	if s, many := matchSession(g.sessions, "aaaa"); s.ID != "" || len(many) != 2 {
		t.Errorf("an ambiguous prefix must not match: %q %d", s.ID, len(many))
	}
	shorts := shortIDs(g.sessions)
	if shorts["claude:aaaa1111-0000"] != "aaaa1111" || shorts["codex:bbbb3333"] != "bbbb3333" {
		t.Errorf("shorts = %v", shorts)
	}
}

func Testフラグは位置引数の後ろにも書ける(t *testing.T) {
	rest, g, err := splitGlobal([]string{"read", "abc", "--json", "--cursor=x", "--peek"})
	if err != nil || !g.json || g.cursor != "x" || strings.Join(rest, " ") != "read abc --peek" {
		t.Fatalf("rest=%v g=%+v err=%v", rest, g, err)
	}
}

func Test待ち受けアドレスから接続先URLを作る(t *testing.T) {
	cases := map[string]string{
		"100.99.15.34:8765": "http://100.99.15.34:8765",
		":8765":             "http://127.0.0.1:8765",
		"0.0.0.0:8766":      "http://127.0.0.1:8766",
	}
	for in, want := range cases {
		if got := listenURL(in); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}

func Test走る様子が見えない短いターンでも応答が増えれば待機を終える(t *testing.T) {
	g := sampleGateway()
	a, _ := newTestApp(t, g)
	a.mustRun(t, "read", "aaaa1111")
	id := "claude:aaaa1111-0000"
	g.onPost = func(path string, body map[string]any) {
		// The prompt lands first and the session stays idle a while.
		g.messages[id] = append(g.messages[id], text("u", model.RoleUser, 9, "色は？"))
		g.sessions[0].UpdatedAt = t0.Add(9 * time.Minute)
		go func() {
			time.Sleep(30 * time.Millisecond)
			g.set(func() {
				g.messages[id] = append(g.messages[id], text("r", model.RoleAssistant, 10, "BLUE"))
				g.sessions[0].UpdatedAt = t0.Add(10 * time.Minute)
			})
		}()
	}
	got := a.mustRun(t, "send", "aaaa1111", "色は？", "--wait", "--timeout", "5s")
	if !strings.Contains(got, "BLUE") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestID指定の待機は既に回答待ちなら即座に返す(t *testing.T) {
	g := sampleGateway()
	g.sessions[0].Status = model.StatusWaitingInput
	a, _ := newTestApp(t, g)
	a.mustRun(t, "changes")
	got := a.mustRun(t, "wait", "aaaa1111", "--timeout", "5s")
	if !strings.Contains(got, "! aaaa1111 waiting_input") {
		t.Fatalf("got:\n%s", got)
	}
}

func Test一覧は既定で停止中のセッションを件数だけにする(t *testing.T) {
	g := sampleGateway()
	g.sessions[2].Status = model.StatusOffline
	a, _ := newTestApp(t, g)
	got := a.mustRun(t, "ls")
	if strings.Contains(got, "bbbb3333") || !strings.Contains(got, "(+1 offline: hc ls --all)") || !strings.Contains(got, "ffff0000") {
		t.Fatalf("ls:\n%s", got)
	}
	if got := a.mustRun(t, "ls", "--all"); !strings.Contains(got, "bbbb3333") {
		t.Fatalf("ls --all:\n%s", got)
	}
}

func Test停止中のセッションの出入りは変化として扱わない(t *testing.T) {
	g := sampleGateway()
	g.sessions[2].Status = model.StatusOffline
	a, _ := newTestApp(t, g)
	a.mustRun(t, "changes")
	g.set(func() {
		g.sessions[2].UpdatedAt = t0.Add(time.Hour)
		g.sessions = append(g.sessions, model.Session{ID: "claude:cccc0000", Status: model.StatusOffline, UpdatedAt: t0})
	})
	if got := a.mustRun(t, "changes"); !strings.Contains(got, "no changes") {
		t.Fatalf("offline churn reported:\n%s", got)
	}
	g.set(func() { g.sessions = []model.Session{g.sessions[0], g.sessions[1], g.sessions[3]} })
	if got := a.mustRun(t, "changes"); !strings.Contains(got, "no changes") {
		t.Fatalf("aged-out offline sessions reported:\n%s", got)
	}
}

func Test別マシンのGatewayに起動するときは作業ディレクトリの指定を求める(t *testing.T) {
	g := sampleGateway()
	a, out := newTestApp(t, g)
	if code := a.run([]string{"start", "hello"}); code != 2 || !strings.Contains(out.String(), "--cwd is required") {
		t.Fatalf("code %d: %s", code, out.String())
	}
	if len(g.posted) != 0 {
		t.Errorf("posted %v", g.posted)
	}
}

func Test標準入力のCRLFはLFにして送る(t *testing.T) {
	g := sampleGateway()
	a, _ := newTestApp(t, g)
	a.in = strings.NewReader("1行目\r\n2行目\r\n")
	a.mustRun(t, "send", "aaaa1111", "-")
	if got := g.posted[0].Body["text"]; got != "1行目\n2行目" {
		t.Errorf("sent %q", got)
	}
}

func Testバージョンは接続設定なしで表示できる(t *testing.T) {
	out := &bytes.Buffer{}
	a := &app{out: out, errOut: out}
	if code := a.run([]string{"--version"}); code != 0 || out.String() != "hc dev\n" {
		t.Fatalf("code %d: %q", code, out.String())
	}
}
