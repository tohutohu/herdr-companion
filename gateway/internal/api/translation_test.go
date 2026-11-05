package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/translation"
)

type testTranslator struct {
	st    translation.Status
	fail  bool
	calls int
}

func (t *testTranslator) Session(session model.Session) model.Session { return session }
func (t *testTranslator) Status() translation.Status                  { return t.st }
func (t *testTranslator) Start(size string) error                     { t.st.Model = size; return nil }
func (t *testTranslator) Stop() error                                 { return nil }
func (t *testTranslator) Configure(in, out bool) error                { t.st.Output = out; return nil }
func (t *testTranslator) Messages(_ string, msgs []model.Message, _ bool) []model.Message {
	return msgs
}
func (t *testTranslator) Input(_ context.Context, _ string, text string) (string, error) {
	t.calls++
	if t.fail {
		return "", errors.New("model unavailable")
	}
	if t.st.Input {
		return "translated: " + text, nil
	}
	return text, nil
}
func Test翻訳モードでもプロンプト本文と二重送信防止を変更しない(t *testing.T) {
	ts, p, _, token := newTestServer(t, func(s *Server) {
		s.Translation = &testTranslator{st: translation.Status{Options: translation.Options{Output: true}}}
		s.MessageDelivery, _ = OpenMessageDeliveryStore(t.TempDir())
	})
	payload := []byte(`{"text":"修正して","requestId":"translated-send"}`)
	for range 2 {
		response, _ := do(t, ts, token, http.MethodPost, "/v1/sessions/fake:s1/messages", payload, "application/json")
		if response.StatusCode != 202 {
			t.Fatal(response.StatusCode)
		}
	}
	if len(p.sent) != 1 || p.sent[0].Text != "修正して" {
		t.Fatalf("sent %+v", p.sent)
	}
}
func Test翻訳管理には認証が必要(t *testing.T) {
	ts, _, _, token := newTestServer(t, func(s *Server) { s.Translation = &testTranslator{} })
	for _, method := range []string{"GET", "POST"} {
		r, _ := do(t, ts, "", method, "/v1/translation", []byte(`{"action":"stop"}`), "application/json")
		if r.StatusCode != 401 {
			t.Fatal(r.StatusCode)
		}
	}
	r, _ := do(t, ts, token, "POST", "/v1/translation", []byte(`{"action":"invalid"}`), "application/json")
	if r.StatusCode != 400 {
		t.Fatal(r.StatusCode)
	}
}

func Test翻訳失敗後は同じ送信IDで再試行でき成功後は再翻訳も再送もしない(t *testing.T) {
	tr := &testTranslator{fail: true, st: translation.Status{Options: translation.Options{Input: true}}}
	ts, p, _, token := newTestServer(t, func(s *Server) { s.Translation = tr; s.MessageDelivery, _ = OpenMessageDeliveryStore(t.TempDir()) })
	payload := []byte(`{"text":"修正して","requestId":"translated-send"}`)
	response, _ := do(t, ts, token, http.MethodPost, "/v1/sessions/fake:s1/messages", payload, "application/json")
	if response.StatusCode != 503 || len(p.sent) != 0 {
		t.Fatal(response.StatusCode, p.sent)
	}
	tr.fail = false
	for range 2 {
		response, _ = do(t, ts, token, http.MethodPost, "/v1/sessions/fake:s1/messages", payload, "application/json")
		if response.StatusCode != 202 {
			t.Fatal(response.StatusCode)
		}
	}
	if len(p.sent) != 1 || p.sent[0].Text != "translated: 修正して" || tr.calls != 2 {
		t.Fatal(p.sent, tr.calls)
	}
}
