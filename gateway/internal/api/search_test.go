package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/search"
)

func withSearch(t *testing.T) func(*Server) {
	return func(s *Server) {
		idx, err := search.Open(context.Background(), filepath.Join(t.TempDir(), "search.db"), s.Sessions)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { idx.Close() })
		s.Search = idx
	}
}

type searchResponse struct {
	Sessions []struct {
		ID       string
		Archived bool
		Match    *search.Match
	}
	More     int
	Indexing bool
}

func TestSessionSearchは会話の本文からセッションを見つける(t *testing.T) {
	ts, _, _, tok := newTestServer(t, withSearch(t))
	resp, body := do(t, ts, tok, "GET", "/v1/sessions/search?q=BYE&text=true", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var res searchResponse
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 || res.Sessions[0].ID != "fake:s1" || res.Sessions[0].Match == nil ||
		res.Sessions[0].Match.Field != "assistant" || res.Sessions[0].Match.Snippet != "bye" {
		t.Fatalf("got %s", body)
	}
	// Without text=true only the list fields are searched.
	_, body = do(t, ts, tok, "GET", "/v1/sessions/search?q=bye", nil, "")
	json.Unmarshal(body, &res)
	if len(res.Sessions) != 0 {
		t.Errorf("本文は text=true のときだけ探す: %s", body)
	}
}

func TestSessionSearchはアーカイブ済みを範囲で絞れる(t *testing.T) {
	ts, _, _, tok := newTestServer(t, withSearch(t))
	if resp, body := do(t, ts, tok, "POST", "/v1/sessions/fake:s1/archive", nil, ""); resp.StatusCode/100 != 2 {
		t.Fatalf("archive: %d %s", resp.StatusCode, body)
	}
	var res searchResponse
	_, body := do(t, ts, tok, "GET", "/v1/sessions/search?q=done&scope=archived", nil, "")
	json.Unmarshal(body, &res)
	if len(res.Sessions) != 1 || res.Sessions[0].ID != "fake:s1" || !res.Sessions[0].Archived {
		t.Fatalf("archived: %s", body)
	}
	_, body = do(t, ts, tok, "GET", "/v1/sessions/search?q=fake:s1&scope=active", nil, "")
	res = searchResponse{}
	json.Unmarshal(body, &res)
	if len(res.Sessions) != 0 {
		t.Errorf("active にアーカイブ済みは出ない: %s", body)
	}
}

func TestSessionSearchは誤った指定を400にする(t *testing.T) {
	ts, _, _, tok := newTestServer(t, withSearch(t))
	for _, path := range []string{"/v1/sessions/search", "/v1/sessions/search?q=+", "/v1/sessions/search?q=a&scope=x", "/v1/sessions/search?q=a&limit=-1"} {
		if resp, body := do(t, ts, tok, "GET", path, nil, ""); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d: %s", path, resp.StatusCode, body)
		}
	}
}

func TestCapabilitiesは検索の有無を知らせる(t *testing.T) {
	ts, _, _, tok := newTestServer(t, withSearch(t))
	var caps map[string]any
	_, body := do(t, ts, tok, "GET", "/v1/capabilities", nil, "")
	json.Unmarshal(body, &caps)
	if caps["sessionSearch"] != true {
		t.Errorf("capabilities = %s", body)
	}
	ts2, _, _, tok2 := newTestServer(t)
	_, body = do(t, ts2, tok2, "GET", "/v1/capabilities", nil, "")
	caps = nil
	json.Unmarshal(body, &caps)
	if caps["sessionSearch"] != false {
		t.Errorf("検索なしの capabilities = %s", body)
	}
}
