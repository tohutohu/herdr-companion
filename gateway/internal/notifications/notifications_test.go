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

	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
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
	w := &Watcher{Sessions: sessions, Sender: sender, Config: store, Sink: deadletter.Nop{}, last: map[string]model.Status{}}
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
	if msg["token"] != "device-token" || android["priority"] != "HIGH" || android["collapseKey"] != "codex:t1" || msg["data"].(map[string]any)["status"] != "completed" {
		t.Errorf("message = %+v", got)
	}
	if _, ok := msg["notification"]; ok {
		t.Error("must be a data-only message")
	}
	if err := f.Send(ctx, "dead-token", data); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("err = %v", err)
	}
}
