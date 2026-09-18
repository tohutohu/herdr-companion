package api

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
)

func Testペアリングは認証付き発行と一度だけの交換(t *testing.T) {
	store, err := config.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.Update(func(c *config.Config) {
		c.JevAPIKey = "private-secret"
		c.FirebaseAndroid = &config.FirebaseAndroid{ProjectID: "example"}
	})
	s := &Server{Config: store}
	h := s.Handler()
	issue := func(auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/pairing", nil)
		if auth {
			r.Header.Set("Authorization", "Bearer "+store.Get().AuthToken)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if issue(false).Code != 401 {
		t.Fatal("unauthenticated issue")
	}
	w := issue(true)
	var invitation struct{ Code string }
	json.Unmarshal(w.Body.Bytes(), &invitation)
	if len(invitation.Code) != 64 {
		t.Fatal("missing random invitation")
	}
	redeem := func(code string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/pair", strings.NewReader(`{"code":"`+code+`"}`)))
		return w
	}
	if redeem("bad").Code != 401 {
		t.Fatal("bad code accepted")
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := redeem(invitation.Code)
			if w.Code == 200 {
				successes.Add(1)
				if strings.Contains(w.Body.String(), "private-secret") || !strings.Contains(w.Body.String(), "example") || w.Header().Get("Cache-Control") != "no-store" {
					t.Error("incorrect exchange payload")
				}
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("invitation reused", successes.Load())
	}
	issue(true)
	s.pairing.expires = time.Now().Add(-time.Second)
	if redeem(s.pairing.code).Code != 401 {
		t.Fatal("expired invitation accepted")
	}
	issue(true)
	code := s.pairing.code
	store.Update(func(c *config.Config) { c.AuthToken = config.NewToken() })
	if redeem(code).Code != 401 {
		t.Fatal("rotated token did not invalidate invitation")
	}
	issue(true)
	code = s.pairing.code
	r := httptest.NewRequest("DELETE", "/v1/pairing", nil)
	r.Header.Set("Authorization", "Bearer "+store.Get().AuthToken)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if redeem(code).Code != 401 {
		t.Fatal("cancelled invitation accepted")
	}
}
