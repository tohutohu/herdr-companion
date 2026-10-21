package notifications

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

type fakeSessions struct{ list []model.Session }

func (f *fakeSessions) LiveSessions(context.Context) ([]model.Session, error) { return f.list, nil }

type sent struct {
	token string
	data  map[string]string
}

type fakeSender struct {
	mu   sync.Mutex
	sent []sent
	err  error
}

func (f *fakeSender) Send(_ context.Context, token string, data map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sent{token, data})
	return f.err
}

func newWatcher(t *testing.T, sessions *fakeSessions, sender *fakeSender) (*Watcher, *config.Store) {
	t.Helper()
	store, err := config.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.UpsertDevice("Pixel", "tok-1")
	w := &Watcher{Sessions: sessions, Sender: sender, Config: store, Sink: deadletter.Nop{}, last: map[string]observation{}}
	return w, store
}

func sess(status model.Status) model.Session {
	return model.Session{
		ID: "claude:s1", Provider: "claude", ProviderName: "Claude Code",
		Project: "my-project", Title: "認証処理の修正", Status: status,
		LastMessage: "認証処理の修正が完了しました", CanSend: true,
	}
}

func Test状態遷移に応じて通知を送り重複は送らない(t *testing.T) {
	fs := &fakeSessions{}
	sender := &fakeSender{}
	w, _ := newWatcher(t, fs, sender)
	ctx := context.Background()

	steps := []model.Status{
		model.StatusCompleted, // 起動直後の初回観測は通知しない
		model.StatusRunning,
		model.StatusWaitingInput, // 通知
		model.StatusWaitingInput, // 変化なし
		model.StatusRunning,
		model.StatusWaitingApproval, // 通知
		model.StatusRunning,
		model.StatusCompleted, // 通知
		model.StatusIdle,      // 見た後の idle は通知しない
		model.StatusRunning,
		model.StatusIdle, // running -> idle は完了扱いで通知
		model.StatusRunning,
		model.StatusFailed, // 通知
	}
	for _, st := range steps {
		fs.list = []model.Session{sess(st)}
		w.Evaluate(ctx)
	}
	var got []string
	for _, s := range sender.sent {
		got = append(got, s.data["status"])
	}
	want := "waiting_input,waiting_approval,completed,completed,failed"
	if strings.Join(got, ",") != want {
		t.Errorf("pushed %v, want %s", got, want)
	}
	first := sender.sent[0]
	if first.token != "tok-1" || first.data["sessionId"] != "claude:s1" || first.data["title"] != "Claude Code needs input" ||
		first.data["body"] != "認証処理の修正\n認証処理の修正が完了しました" {
		t.Errorf("payload = %+v", first)
	}
}

func Testペイロードの見出しはセッションタイトルにする(t *testing.T) {
	s := sess(model.StatusCompleted)
	got := Payload(s, model.StatusCompleted)
	if got["body"] != "認証処理の修正\n認証処理の修正が完了しました" || got["canSend"] != "true" {
		t.Errorf("payload = %+v", got)
	}

	// タイトル未確定のセッションはプロジェクト名で代用する
	s.Title = ""
	s.CanSend = false
	got = Payload(s, model.StatusCompleted)
	if got["body"] != "my-project\n認証処理の修正が完了しました" || got["canSend"] != "false" {
		t.Errorf("payload = %+v", got)
	}
}

type fakeConversations struct {
	msgs []model.Message
	err  error
}

func (f *fakeConversations) Conversation(context.Context, string) (model.Session, []model.Message, error) {
	return model.Session{}, f.msgs, f.err
}

func payloadSize(data map[string]string) int {
	n := 0
	for k, v := range data {
		n += len(k) + len(v)
	}
	return n
}

func decodeReport(t *testing.T, data map[string]string) model.Message {
	t.Helper()
	var m model.Message
	if err := json.Unmarshal([]byte(data[KeyReport]), &m); err != nil {
		t.Fatalf("report = %q: %v", data[KeyReport], err)
	}
	return m
}

func Test通知に最後の報告を添える(t *testing.T) {
	fs := &fakeSessions{list: []model.Session{sess(model.StatusRunning)}}
	sender := &fakeSender{}
	w, _ := newWatcher(t, fs, sender)
	at := time.Date(2026, 10, 21, 1, 2, 3, 0, time.UTC)
	w.Conversations = &fakeConversations{msgs: []model.Message{
		{ID: "u1", Role: model.RoleUser, Blocks: []model.Block{model.TextBlock("直して")}},
		{ID: "a1", Role: model.RoleAssistant, Timestamp: at, Blocks: []model.Block{
			model.TextBlock("修正しました。"),
			{Type: model.BlockImage, URL: "/v1/files/x.png"},
			model.TextBlock("テストも通っています。"),
		}},
		{ID: "t1", Role: model.RoleTool, Blocks: []model.Block{model.TextBlock("go test ./...")}},
		{ID: "queued:x", Role: model.RoleAssistant, Queued: true, Blocks: []model.Block{model.TextBlock("待機中")}},
	}}
	ctx := context.Background()
	w.Evaluate(ctx)
	fs.list = []model.Session{sess(model.StatusCompleted)}
	w.Evaluate(ctx)

	if len(sender.sent) != 1 {
		t.Fatalf("sent %d pushes", len(sender.sent))
	}
	got := decodeReport(t, sender.sent[0].data)
	want := model.Message{ID: "a1", Role: model.RoleAssistant, Timestamp: at, Blocks: []model.Block{model.TextBlock("修正しました。\n\nテストも通っています。")}}
	if got.ID != want.ID || got.Role != want.Role || !got.Timestamp.Equal(at) || len(got.Blocks) != 1 || got.Blocks[0] != want.Blocks[0] {
		t.Errorf("report = %+v", got)
	}
	if sender.sent[0].data["providerName"] != "Claude Code" {
		t.Errorf("providerName = %q", sender.sent[0].data["providerName"])
	}
}

func Test長い報告はFCMの上限に収まるよう切り詰める(t *testing.T) {
	data := Payload(sess(model.StatusCompleted), model.StatusCompleted)
	data["gatewayId"] = strings.Repeat("g", 16)
	long := strings.Repeat("日本語の報告<&>\"です。\n", 400)
	report := model.Message{ID: "a1", Role: model.RoleAssistant, Blocks: []model.Block{model.TextBlock(long)}}
	if !AttachReport(data, report) {
		t.Fatal("report not attached")
	}
	if n := payloadSize(data); n > maxPayloadBytes || n < maxPayloadBytes-200 {
		t.Errorf("payload size = %d", n)
	}
	text := decodeReport(t, data).Blocks[0].Text
	if !strings.HasSuffix(text, "…") || !strings.HasPrefix(long, strings.TrimSuffix(text, "…")) {
		t.Errorf("text = %q", text)
	}
}

func Test報告がなければ添えずに通知する(t *testing.T) {
	fs := &fakeSessions{list: []model.Session{sess(model.StatusRunning)}}
	sender := &fakeSender{}
	w, _ := newWatcher(t, fs, sender)
	conv := &fakeConversations{err: errors.New("transcript unreadable")}
	w.Conversations = conv
	ctx := context.Background()
	w.Evaluate(ctx)
	fs.list = []model.Session{sess(model.StatusWaitingInput)}
	w.Evaluate(ctx)
	conv.err = nil
	conv.msgs = []model.Message{{ID: "u1", Role: model.RoleUser, Blocks: []model.Block{model.TextBlock("直して")}}}
	fs.list = []model.Session{sess(model.StatusCompleted)}
	w.Evaluate(ctx)

	if len(sender.sent) != 2 {
		t.Fatalf("sent %d pushes", len(sender.sent))
	}
	for _, p := range sender.sent {
		if _, ok := p.data[KeyReport]; ok {
			t.Errorf("unexpected report: %+v", p.data)
		}
	}
}

func Test古い完了ターンの状態が揺れても再通知しない(t *testing.T) {
	for _, terminal := range []model.Status{model.StatusCompleted, model.StatusIdle, model.StatusFailed} {
		for _, initial := range []model.Status{terminal, model.StatusRunning} {
			t.Run(string(terminal)+"/初回="+string(initial), func(t *testing.T) {
				fs := &fakeSessions{}
				sender := &fakeSender{}
				w, _ := newWatcher(t, fs, sender)
				for i, status := range []model.Status{initial, model.StatusRunning, terminal, model.StatusRunning, terminal} {
					s := sess(status)
					s.CompletionRevision = "old-turn"
					// Resuming/redrawing can touch metadata without executing a turn.
					s.UpdatedAt = time.Unix(int64(i), 0)
					fs.list = []model.Session{s}
					w.Evaluate(context.Background())
				}
				if len(sender.sent) != 0 {
					t.Fatalf("old turn was notified: %+v", sender.sent)
				}
			})
		}
	}
}

func Test新しいターンの完了は同じ本文でも通知する(t *testing.T) {
	for _, terminal := range []model.Status{model.StatusCompleted, model.StatusIdle, model.StatusFailed} {
		t.Run(string(terminal), func(t *testing.T) {
			fs := &fakeSessions{}
			sender := &fakeSender{}
			w, _ := newWatcher(t, fs, sender)
			steps := []struct {
				status model.Status
				turn   string
			}{
				{model.StatusCompleted, "old-turn"},
				{model.StatusRunning, ""},
				// Transcript can finish before Herdr notices completion.
				{model.StatusRunning, "new-turn"},
				{terminal, "new-turn"},
				{model.StatusRunning, "new-turn"},
				{terminal, "new-turn"},
				{model.StatusRunning, ""},
				{terminal, "another-turn"},
			}
			for _, step := range steps {
				// Text and timestamps are deliberately identical across turns.
				s := sess(step.status)
				s.CompletionRevision = step.turn
				fs.list = []model.Session{s}
				w.Evaluate(context.Background())
			}
			if len(sender.sent) != 2 {
				t.Fatalf("new turns should each notify once: %+v", sender.sent)
			}
			want := terminal
			if terminal == model.StatusIdle {
				want = model.StatusCompleted
			}
			for _, push := range sender.sent {
				if push.data["status"] != string(want) {
					t.Fatalf("status = %s, want %s", push.data["status"], want)
				}
			}
		})
	}
}

func Test完了ターンが同じでも入力と承認の要求は通知する(t *testing.T) {
	fs := &fakeSessions{}
	sender := &fakeSender{}
	w, _ := newWatcher(t, fs, sender)
	for _, status := range []model.Status{model.StatusIdle, model.StatusWaitingInput, model.StatusWaitingApproval} {
		s := sess(status)
		s.CompletionRevision = "old-turn"
		fs.list = []model.Session{s}
		w.Evaluate(context.Background())
	}
	if len(sender.sent) != 2 {
		t.Fatalf("attention notifications = %+v", sender.sent)
	}
}

func Test実行中の観測が抜けても新しい完了ターンは通知する(t *testing.T) {
	for _, terminal := range []model.Status{model.StatusCompleted, model.StatusIdle, model.StatusFailed} {
		t.Run(string(terminal), func(t *testing.T) {
			fs := &fakeSessions{}
			sender := &fakeSender{}
			w, _ := newWatcher(t, fs, sender)
			for _, revision := range []string{"old-turn", "new-turn", "new-turn", "another-turn"} {
				s := sess(terminal)
				s.CompletionRevision = revision
				fs.list = []model.Session{s}
				w.Evaluate(context.Background())
			}
			if len(sender.sent) != 2 {
				t.Fatalf("missed running observations should still notify each new turn: %+v", sender.sent)
			}
			want := terminal
			if terminal == model.StatusIdle {
				want = model.StatusCompleted
			}
			for _, push := range sender.sent {
				if push.data["status"] != string(want) {
					t.Fatalf("status = %s, want %s", push.data["status"], want)
				}
			}
		})
	}
}

func Test完了状態の後に履歴が更新されても通知する(t *testing.T) {
	fs := &fakeSessions{}
	sender := &fakeSender{}
	w, _ := newWatcher(t, fs, sender)
	for _, step := range []struct {
		status   model.Status
		revision string
	}{
		{model.StatusCompleted, "old-turn"},
		{model.StatusRunning, "old-turn"},
		{model.StatusCompleted, "old-turn"},
		{model.StatusCompleted, "new-turn"},
		{model.StatusCompleted, "new-turn"},
		{model.StatusIdle, "new-turn"},
	} {
		s := sess(step.status)
		s.CompletionRevision = step.revision
		fs.list = []model.Session{s}
		w.Evaluate(context.Background())
	}
	if len(sender.sent) != 1 {
		t.Fatalf("late transcript flush should notify once: %+v", sender.sent)
	}
}

type fakeSubscriber struct {
	events chan herdr.Event
	subs   []map[string]any
}

func (f *fakeSubscriber) Snapshot(context.Context) (*herdr.Snapshot, error) {
	return &herdr.Snapshot{Panes: []herdr.Pane{{PaneID: "w1:p1"}}}, nil
}

func (f *fakeSubscriber) Subscribe(_ context.Context, subs []map[string]any) (<-chan herdr.Event, error) {
	f.subs = subs
	return f.events, nil
}

func Testペイントポロジーの実際のイベント名で再購読する(t *testing.T) {
	for _, event := range []string{"pane_created", "pane_closed", "pane_agent_detected", "pane.created", "pane.closed", "pane.agent_detected"} {
		t.Run(event, func(t *testing.T) {
			f := &fakeSubscriber{events: make(chan herdr.Event, 2)}
			f.events <- herdr.Event{Event: "pane.agent_status_changed", Data: json.RawMessage(`{"pane_id":"w1:p1","agent_status":"working"}`)}
			f.events <- herdr.Event{Event: event}
			// An unrecognized topology event drains the channel and returns
			// a subscription error instead of rebuilding the pane set.
			close(f.events)
			w := &Watcher{Herdr: f}
			kicks := 0
			if err := w.stream(context.Background(), func() { kicks++ }); err != nil {
				t.Fatalf("topology did not trigger resubscription: %v", err)
			}
			if kicks != 3 {
				t.Fatalf("snapshot, status and topology should trigger evaluation: %d", kicks)
			}
			if len(f.subs) != 4 || f.subs[3]["type"] != "pane.agent_status_changed" || f.subs[3]["pane_id"] != "w1:p1" {
				t.Fatalf("subscriptions = %+v", f.subs)
			}
		})
	}
}

func Test同一ターンでもバックグラウンド結果が更新されて再完了したら通知する(t *testing.T) {
	for _, terminal := range []model.Status{model.StatusCompleted, model.StatusIdle, model.StatusFailed} {
		t.Run(string(terminal), func(t *testing.T) {
			fs := &fakeSessions{}
			sender := &fakeSender{}
			w, _ := newWatcher(t, fs, sender)
			steps := []struct {
				status   model.Status
				revision string
			}{
				{model.StatusRunning, ""},
				// Main work is done while a background task is still running.
				{terminal, "same-turn:initial-result"},
				{model.StatusRunning, "same-turn:initial-result"},
				// Tool output changes before Herdr observes done again.
				{model.StatusRunning, "same-turn:background-result"},
				{terminal, "same-turn:background-result"},
				// Subsequent lifecycle fluctuations with no new output are noise.
				{model.StatusRunning, "same-turn:background-result"},
				{terminal, "same-turn:background-result"},
			}
			for _, step := range steps {
				// A tool result can change while notification body stays the same.
				s := sess(step.status)
				s.CompletionRevision = step.revision
				fs.list = []model.Session{s}
				w.Evaluate(context.Background())
			}
			if len(sender.sent) != 2 {
				t.Fatalf("main and background completion should each notify once: %+v", sender.sent)
			}
		})
	}
}

func Test消えたセッションは状態を忘れて再出現時に通知しない(t *testing.T) {
	fs := &fakeSessions{list: []model.Session{sess(model.StatusRunning)}}
	sender := &fakeSender{}
	w, _ := newWatcher(t, fs, sender)
	w.Evaluate(context.Background())
	fs.list = nil
	w.Evaluate(context.Background())
	fs.list = []model.Session{sess(model.StatusCompleted)}
	w.Evaluate(context.Background())
	if len(sender.sent) != 0 {
		t.Errorf("sent = %+v", sender.sent)
	}
}

func Test無効になった端末トークンは設定から削除する(t *testing.T) {
	fs := &fakeSessions{list: []model.Session{sess(model.StatusRunning)}}
	sender := &fakeSender{err: ErrTokenInvalid}
	w, store := newWatcher(t, fs, sender)
	w.Evaluate(context.Background())
	fs.list = []model.Session{sess(model.StatusFailed)}
	w.Evaluate(context.Background())
	if len(store.Get().Devices) != 0 {
		t.Errorf("devices = %+v", store.Get().Devices)
	}
}

func writeServiceAccount(t *testing.T, tokenURI string) string {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	b, _ := json.Marshal(map[string]string{
		"type":         "service_account",
		"project_id":   "herdr-mobile-test",
		"client_email": "gateway@herdr-mobile-test.iam.gserviceaccount.com",
		"private_key":  string(pemKey),
		"token_uri":    tokenURI,
	})
	path := filepath.Join(t.TempDir(), "sa.json")
	os.WriteFile(path, b, 0o600)
	return path
}

func TestFCMのHTTPv1APIへデータメッセージを送る(t *testing.T) {
	var tokenCalls int
	var got map[string]any
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenCalls++
			r.ParseForm()
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || strings.Count(r.Form.Get("assertion"), ".") != 2 {
				http.Error(w, "bad assertion", 400)
				return
			}
			w.Write([]byte(`{"access_token":"at-1","expires_in":3600,"token_type":"Bearer"}`))
		case "/v1/projects/herdr-mobile-test/messages:send":
			auth, path = r.Header.Get("Authorization"), r.URL.Path
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &got)
			if strings.Contains(string(b), "dead-token") {
				w.WriteHeader(404)
				w.Write([]byte(`{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`))
				return
			}
			w.Write([]byte(`{"name":"projects/herdr-mobile-test/messages/1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	f, err := NewFCM(writeServiceAccount(t, srv.URL+"/token"))
	if err != nil {
		t.Fatal(err)
	}
	f.baseURL = srv.URL
	ctx := context.Background()
	data := map[string]string{"sessionId": "codex:t1", "status": "completed"}
	for i := 0; i < 2; i++ {
		if err := f.Send(ctx, "device-token", data); err != nil {
			t.Fatal(err)
		}
	}
	if tokenCalls != 1 {
		t.Errorf("access token should be cached, calls = %d", tokenCalls)
	}
	if auth != "Bearer at-1" || path == "" {
		t.Errorf("auth = %q path = %q", auth, path)
	}
	msg := got["message"].(map[string]any)
	android := msg["android"].(map[string]any)
	if msg["token"] != "device-token" || android["priority"] != "HIGH" || android["ttl"] != "86400s" || msg["data"].(map[string]any)["status"] != "completed" {
		t.Errorf("message = %+v", got)
	}
	if _, ok := android["collapseKey"]; ok {
		t.Error("session notifications must not compete for FCM's four collapse keys")
	}
	if _, ok := msg["notification"]; ok {
		t.Error("must be a data-only message")
	}
	if err := f.Send(ctx, "dead-token", data); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("err = %v", err)
	}
}
