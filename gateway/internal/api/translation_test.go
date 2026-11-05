package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/translation"
)

type testTranslator struct {
	st            translation.Status
	fail          bool
	calls         int
	remembered    [][3]string
	rememberError error
}

func (t *testTranslator) Session(session model.Session, _ ...*bool) model.Session { return session }
func (t *testTranslator) Status() translation.Status                              { return t.st }
func (t *testTranslator) Start(size string) error                                 { t.st.Model = size; return nil }
func (t *testTranslator) Stop() error                                             { return nil }
func (t *testTranslator) RememberPrompt(session, source, sent string) error {
	t.remembered = append(t.remembered, [3]string{session, source, sent})
	return t.rememberError
}
func (t *testTranslator) Configure(in, out bool) error { t.st.Output = out; return nil }
func (t *testTranslator) Messages(_ string, msgs []model.Message, _ bool, _ ...*bool) []model.Message {
	return msgs
}

func Test添付ファイル付きプロンプトは履歴の形式でも原文を保存する(t *testing.T) {
	tr := &testTranslator{st: translation.Status{Options: translation.Options{Input: true}}}
	s := &Server{Translation: tr}
	files := []string{"/uploads/資料.pdf", "/uploads/second.txt"}
	got, err := s.translateInput(context.Background(), "session", "確認して", files)
	if err != nil || got != "translated: 確認して" {
		t.Fatal(got, err)
	}
	want := [3]string{"session", "確認して\n/uploads/資料.pdf\n/uploads/second.txt", "translated: 確認して\n/uploads/資料.pdf\n/uploads/second.txt"}
	if len(tr.remembered) != 1 || tr.remembered[0] != want {
		t.Fatal(tr.remembered)
	}
	tr.rememberError = errors.New("disk full")
	if _, err := s.translateInput(context.Background(), "session", "確認して", files); err == nil {
		t.Fatal("must not send if the original cannot be saved")
	}
}

type displayTranslator struct{ testTranslator }

func (t *displayTranslator) Session(session model.Session, _ ...*bool) model.Session {
	session.OriginalTitle, session.Title = session.Title, "日本語タイトル"
	return session
}
func (t *displayTranslator) Messages(_ string, msgs []model.Message, _ bool, _ ...*bool) []model.Message {
	out := append([]model.Message(nil), msgs...)
	for i := range out {
		out[i].Blocks = append([]model.Block(nil), out[i].Blocks...)
		for b := range out[i].Blocks {
			block := &out[i].Blocks[b]
			if block.Type == model.BlockText {
				block.OriginalText, block.Text = block.Text, "日本語表示"
			}
		}
	}
	return out
}

func Test再読込と差分取得も保存済み原文と翻訳表示を返す(t *testing.T) {
	tr := &displayTranslator{testTranslator{st: translation.Status{Options: translation.Options{InputEverEnabled: true}}}}
	ts, _, _, token := newTestServer(t, func(s *Server) { s.Translation = tr })
	for _, req := range []struct{ method, path string }{
		{"GET", "/v1/sessions/fake:s1/messages?after=m2"},
		{"POST", "/v1/sessions/fake:s1/reload"},
	} {
		resp, data := do(t, ts, token, req.method, req.path, nil, "application/json")
		var got reloadResponse
		if resp.StatusCode != 200 || json.Unmarshal(data, &got) != nil {
			t.Fatal(resp.StatusCode, string(data))
		}
		if got.Session.Title != "日本語タイトル" || len(got.Messages) != 3 ||
			got.Messages[0].Blocks[0].Text != "日本語表示" || got.Messages[0].Blocks[0].OriginalText != "hi" {
			t.Fatal(string(data))
		}
	}
}
func (t *testTranslator) Input(_ context.Context, _ string, text string, mode ...*bool) (string, error) {
	t.calls++
	if t.fail {
		return "", errors.New("model unavailable")
	}
	enabled := t.st.Input
	if len(mode) > 0 && mode[0] != nil {
		enabled = *mode[0]
	}
	if enabled {
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
	for i := range 2 {
		var body []byte
		response, body = do(t, ts, token, http.MethodPost, "/v1/sessions/fake:s1/messages", payload, "application/json")
		if response.StatusCode != 202 {
			t.Fatal(response.StatusCode)
		}
		if i == 0 {
			var result struct{ SentText string }
			if json.Unmarshal(body, &result) != nil || result.SentText != "translated: 修正して" {
				t.Fatal(string(body))
			}
		}
	}
	if len(p.sent) != 1 || p.sent[0].Text != "translated: 修正して" || tr.calls != 2 {
		t.Fatal(p.sent, tr.calls)
	}
}

func Test開始時の翻訳選択は初回と後続の送信に適用され信頼確認でも保持する(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, trusted := range []bool{false, true} {
			t.Run(fmt.Sprintf("translation=%v/trusted=%v", enabled, trusted), func(t *testing.T) {
				tr := &testTranslator{st: translation.Status{Options: translation.Options{Input: !enabled}}}
				ts, p, dp, token := newTestServer(t, func(s *Server) { s.Translation = tr })
				dp.trusted = trusted
				code, result := call(t, ts, token, "POST", "/v1/sessions", map[string]any{
					"provider": "fake", "cwd": p.root, "prompt": "修正して", "translation": enabled,
				})
				if code != http.StatusCreated {
					t.Fatal(code, result)
				}
				if !trusted {
					code, result = call(t, ts, token, "POST", "/v1/launches/"+result["launchId"].(string)+"/trust", map[string]bool{"trust": true})
					if code != http.StatusOK {
						t.Fatal(code, result)
					}
				}
				id := result["sessionId"].(string)
				if choice := dp.m.Translation(id); choice == nil || *choice != enabled {
					t.Fatal(choice)
				}
				code, detail := call(t, ts, token, "GET", "/v1/sessions/"+id, nil)
				if code != http.StatusOK || detail["translation"] != enabled {
					t.Fatal(code, detail)
				}
				code, result = call(t, ts, token, "POST", "/v1/sessions/"+id+"/messages", map[string]string{"text": "続けて"})
				if code != http.StatusAccepted {
					t.Fatal(code, result)
				}
				if len(p.sent) != 2 {
					t.Fatal(p.sent)
				}
				prefix := ""
				if enabled {
					prefix = "translated: "
				}
				if p.sent[0].Text != prefix+"修正して" || p.sent[1].Text != prefix+"続けて" {
					t.Fatal(p.sent)
				}
				if tr.st.Input == enabled {
					t.Fatal("global default changed")
				}
			})
		}
	}
}

func Test翻訳状態は既定設定と翻訳機能なしでも一覧と詳細に明示する(t *testing.T) {
	for _, options := range []*translation.Options{nil, {}, {Input: true}, {Output: true}, {InputEverEnabled: true, OutputEverEnabled: true}} {
		ts, _, _, token := newTestServer(t, func(s *Server) {
			if options != nil {
				s.Translation = &testTranslator{st: translation.Status{Options: *options}}
			}
		})
		want := options != nil && (options.Input || options.Output)
		for _, path := range []string{"/v1/sessions/fake:s1", "/v1/sessions", "/v1/sessions/fake:s1/messages"} {
			code, result := call(t, ts, token, "GET", path, nil)
			if code != http.StatusOK {
				t.Fatal(code, result)
			}
			session := result
			if list, ok := result["sessions"].([]any); ok {
				session = list[0].(map[string]any)
			}
			if detail, ok := result["session"].(map[string]any); ok {
				session = detail
			}
			if session["translation"] != want {
				t.Fatalf("%s: %v, want %v", path, session, want)
			}
		}
	}
}
