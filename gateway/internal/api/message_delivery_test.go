package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/sessions"
)

func Test送信受付後の同一IDはGateway再起動後も実行しない(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenMessageDeliveryStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.claim("gateway", "request", "payload-hash")
	if !fresh || err != nil {
		t.Fatalf("claim %v %v", fresh, err)
	}
	if err := s.accept("gateway", "request", "payload-hash"); err != nil {
		t.Fatal(err)
	}
	reopened, _ := OpenMessageDeliveryStore(dir)
	fresh, err = reopened.claim("gateway", "request", "payload-hash")
	if fresh || err != nil {
		t.Fatalf("replay %v %v", fresh, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("files %v", entries)
	}
	b, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if strings.Contains(string(b), "request") || strings.Contains(string(b), "gateway") {
		t.Fatal("identity in receipt")
	}
}

func Test送信途中の再起動と本文変更は再実行しない(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenMessageDeliveryStore(dir)
	if fresh, err := s.claim("g", "r", "one"); !fresh || err != nil {
		t.Fatal(err)
	}
	s, _ = OpenMessageDeliveryStore(dir)
	if _, err := s.claim("g", "r", "one"); !errors.Is(err, errDeliveryUnknown) {
		t.Fatalf("got %v", err)
	}
	if _, err := s.claim("g", "r", "two"); !errors.Is(err, errDeliveryConflict) {
		t.Fatalf("got %v", err)
	}
}

func Test同一IDの並列claimは一度しか実行権を渡さない(t *testing.T) {
	s, _ := OpenMessageDeliveryStore(t.TempDir())
	var wg sync.WaitGroup
	result := make(chan bool, 16)
	for range 16 {
		wg.Go(func() { fresh, _ := s.claim("g", "r", "hash"); result <- fresh })
	}
	wg.Wait()
	close(result)
	count := 0
	for fresh := range result {
		if fresh {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("claims %d", count)
	}
}

func Test壊れたreceiptを未送信と扱わない(t *testing.T) {
	s, _ := OpenMessageDeliveryStore(t.TempDir())
	if err := os.WriteFile(s.path("g", "r"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.claim("g", "r", "hash"); !errors.Is(err, errDeliveryUnknown) {
		t.Fatal(err)
	}
}

func Testメッセージ再送はproviderを一度だけ呼び本文変更は拒否する(t *testing.T) {
	dir := t.TempDir()
	var server *Server
	ts, provider, _, token := newTestServer(t, func(s *Server) {
		server = s
		var err error
		s.MessageDelivery, err = OpenMessageDeliveryStore(dir)
		if err != nil {
			t.Fatal(err)
		}
	})
	for range 2 {
		response, _ := do(t, ts, token, http.MethodPost, "/v1/sessions/fake:s1/messages", []byte(`{"text":"日本語🙂","requestId":"same-id"}`), "application/json")
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("status %d", response.StatusCode)
		}
		// Reopen the store to model gateway restart between requests.
		server.MessageDelivery, _ = OpenMessageDeliveryStore(dir)
	}
	if len(provider.sent) != 1 {
		t.Fatalf("provider sends %d", len(provider.sent))
	}
	response, _ := do(t, ts, token, http.MethodPost, "/v1/sessions/fake:s1/messages", []byte(`{"text":"different","requestId":"same-id"}`), "application/json")
	if response.StatusCode != http.StatusConflict || len(provider.sent) != 1 {
		t.Fatalf("status %d sends %d", response.StatusCode, len(provider.sent))
	}
}

type uncertainSendProvider struct {
	*fakeProvider
	calls atomic.Int32
}

func (p *uncertainSendProvider) Send(context.Context, string, *providers.Live, model.Input) error {
	p.calls.Add(1)
	return errors.New("connection lost after input")
}

func TestProviderエラー後の曖昧な再送を拒否する(t *testing.T) {
	p := &uncertainSendProvider{fakeProvider: &fakeProvider{root: t.TempDir()}}
	ts, _, _, token := newTestServer(t, func(s *Server) {
		s.MessageDelivery, _ = OpenMessageDeliveryStore(t.TempDir())
		s.Sessions = sessions.New(s.Terminal.(*fakeHerdr), time.Hour, p)
	})
	for range 2 {
		response, _ := do(t, ts, token, http.MethodPost, "/v1/sessions/fake:s1/messages", []byte(`{"text":"once","requestId":"r"}`), "application/json")
		if response.StatusCode != http.StatusConflict {
			t.Fatal(response.StatusCode)
		}
	}
	if p.calls.Load() != 1 {
		t.Fatalf("provider called %d times", p.calls.Load())
	}
}

func Test送信receipt機能の有無を認証後に取得できる(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		ts, _, _, token := newTestServer(t, func(s *Server) {
			if enabled {
				s.MessageDelivery, _ = OpenMessageDeliveryStore(t.TempDir())
			}
		})
		response, body := do(t, ts, token, http.MethodGet, "/v1/message-delivery", nil, "")
		if response.StatusCode != 200 || strings.Contains(string(body), `"idempotency":true`) != enabled {
			t.Fatalf("status %d body %s", response.StatusCode, body)
		}
		response, _ = do(t, ts, "wrong", http.MethodGet, "/v1/message-delivery", nil, "")
		if response.StatusCode != 401 {
			t.Fatal(response.StatusCode)
		}
	}
}

func Test無効な入力は実行前receiptを残さない(t *testing.T) {
	dir := t.TempDir()
	ts, _, _, token := newTestServer(t, func(s *Server) { s.MessageDelivery, _ = OpenMessageDeliveryStore(dir) })
	response, _ := do(t, ts, token, http.MethodPost, "/v1/sessions/fake:s1/messages", []byte(`{"text":"","requestId":"r"}`), "application/json")
	if response.StatusCode != 400 {
		t.Fatal(response.StatusCode)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal(entries)
	}
	response, _ = do(t, ts, token, http.MethodPost, "/v1/sessions/fake:s1/messages", []byte(`{"text":"valid","requestId":"r"}`), "application/json")
	if response.StatusCode != 202 {
		t.Fatal(response.StatusCode)
	}
}
