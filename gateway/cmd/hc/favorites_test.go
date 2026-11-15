package main

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	favstore "github.com/tohutohu/herdr-android-client/gateway/internal/favorites"
)

// serveFavorites answers the favorite folder endpoints like the Gateway
// (internal/api/favorites.go), without checking that folders exist.
func (g *fakeGateway) serveFavorites(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, favoritesPath) {
		return false
	}
	var body struct {
		Paths    []string `json:"paths"`
		Path     string   `json:"path"`
		Position *int     `json:"position"`
	}
	b, _ := io.ReadAll(r.Body)
	json.Unmarshal(b, &body)
	var snap favstore.Snapshot
	var err error
	switch r.Method {
	case http.MethodGet:
		snap = g.favorites.List()
	case http.MethodPut:
		snap, err = g.favorites.Set(body.Paths)
	case http.MethodPost:
		g.posted = append(g.posted, postedRequest{r.URL.Path, map[string]any{"path": body.Path, "position": body.Position}})
		snap, err = g.favorites.Add(body.Path, body.Position)
	case http.MethodDelete:
		snap, err = g.favorites.Remove(r.URL.Query().Get("path"))
	}
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return true
	}
	json.NewEncoder(w).Encode(snap)
	return true
}

func favoritesGateway(t *testing.T, paths ...string) *fakeGateway {
	t.Helper()
	store, err := favstore.Open(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) > 0 {
		store.Set(paths)
	}
	g := directGateway()
	g.favorites = store
	return g
}

func Testお気に入りの一覧は番号とフォルダ名とパスを出す(t *testing.T) {
	g := favoritesGateway(t, "/Users/me/workspace/bot", "/Users/me/workspace/herdr-android-client")
	a, _ := newTestApp(t, g)
	got := a.mustRun(t, "fav")
	want := "1  bot                   /Users/me/workspace/bot\n2  herdr-android-client  /Users/me/workspace/herdr-android-client\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	var res favorites
	if err := json.Unmarshal([]byte(a.mustRun(t, "fav", "ls", "--json")), &res); err != nil || len(res.Paths) != 2 || !res.Saved {
		t.Errorf("json %+v %v", res, err)
	}
	empty := favoritesGateway(t)
	b, _ := newTestApp(t, empty)
	if got := b.mustRun(t, "fav"); !strings.Contains(got, "no favorite folders") {
		t.Errorf("empty: %s", got)
	}
}

func Testお気に入りを番号や名前で外して並べ替える(t *testing.T) {
	g := favoritesGateway(t, "/w/a", "/w/b", "/w/c", "/x/c")
	a, out := newTestApp(t, g)
	if code := a.run([]string{"fav", "rm", "c"}); code == 0 || !strings.Contains(out.String(), "names 2 favorites") {
		t.Fatalf("ambiguous name: code %d\n%s", code, out.String())
	}
	a.mustRun(t, "fav", "rm", "1", "/x/c")
	a.mustRun(t, "fav", "mv", "c", "1")
	got := g.favorites.List().Paths
	if strings.Join(got, ",") != "/w/c,/w/b" {
		t.Errorf("got %v", got)
	}
	a.mustRun(t, "fav", "add", "/w/new", "--at", "2")
	if got := strings.Join(g.favorites.List().Paths, ","); got != "/w/c,/w/new,/w/b" {
		t.Errorf("after add: %s", got)
	}
	if code := a.run([]string{"fav", "add", "relative"}); code != 2 {
		t.Errorf("relative folder on a remote Gateway: code %d", code)
	}
}

func Test起動のcwdにお気に入りのフォルダ名を使える(t *testing.T) {
	g := favoritesGateway(t, "/Users/me/workspace/herdr-android-client", "/Users/me/workspace/bot")
	g.replies = map[string]any{"/v1/sessions": map[string]any{"sessionId": "claude:eeee5555-0000"}}
	a, out := newTestApp(t, g)
	a.mustRun(t, "start", "--cwd", "herdr-android-client", "hello")
	last := g.posted[len(g.posted)-1]
	if last.Path != "/v1/sessions" || last.Body["cwd"] != "/Users/me/workspace/herdr-android-client" {
		t.Fatalf("posted %+v", last)
	}
	if code := a.run([]string{"start", "--cwd", "unknown", "hello"}); code != 2 || !strings.Contains(out.String(), "nor a favorite") {
		t.Errorf("unknown name: code %d\n%s", code, out.String())
	}
}
