package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/favorites"
)

type favoritesBody struct {
	Paths    []string `json:"paths"`
	Saved    bool     `json:"saved"`
	Imported bool     `json:"imported"`
}

func newFavoritesServer(t *testing.T) (func(method, path, body string) (int, favoritesBody), string) {
	t.Helper()
	store, err := favorites.Open(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatal(err)
	}
	ts, fp, _, tok := newTestServer(t, func(s *Server) { s.Favorites = store })
	call := func(method, path, body string) (int, favoritesBody) {
		t.Helper()
		var b []byte
		ctype := ""
		if body != "" {
			b, ctype = []byte(body), "application/json"
		}
		resp, out := do(t, ts, tok, method, path, b, ctype)
		var got favoritesBody
		json.Unmarshal(out, &got)
		return resp.StatusCode, got
	}
	return call, fp.root
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func Testお気に入りは端末からの取り込みを一度だけ受け付ける(t *testing.T) {
	call, _ := newFavoritesServer(t)
	status, got := call("GET", "/v1/directories/favorites", "")
	if status != http.StatusOK || got.Saved || len(got.Paths) != 0 {
		t.Fatalf("initial %d %+v", status, got)
	}
	status, got = call("POST", "/v1/directories/favorites/import", `{"paths":["/Users/me/workspace/b","/Users/me/workspace/a"]}`)
	if status != http.StatusOK || !got.Imported || !got.Saved || !reflect.DeepEqual(got.Paths, []string{"/Users/me/workspace/b", "/Users/me/workspace/a"}) {
		t.Fatalf("import %d %+v", status, got)
	}
	_, got = call("POST", "/v1/directories/favorites/import", `{"paths":["/phone/only"]}`)
	if got.Imported || !reflect.DeepEqual(got.Paths, []string{"/Users/me/workspace/b", "/Users/me/workspace/a"}) {
		t.Fatalf("second import %+v", got)
	}
}

func Testお気に入りの追加は起動できるフォルダだけを実パスで受け付ける(t *testing.T) {
	call, root := newFavoritesServer(t)
	app := filepath.Join(root, "app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}
	status, got := call("POST", "/v1/directories/favorites", `{"path":`+jsonString(app+"/")+`}`)
	if status != http.StatusOK || !reflect.DeepEqual(got.Paths, []string{app}) {
		t.Fatalf("add %d %+v", status, got)
	}
	status, got = call("POST", "/v1/directories/favorites", `{"path":`+jsonString(root)+`,"position":0}`)
	if status != http.StatusOK || !reflect.DeepEqual(got.Paths, []string{root, app}) {
		t.Fatalf("add at 0 %d %+v", status, got)
	}
	if status, _ := call("POST", "/v1/directories/favorites", `{"path":"/etc"}`); status != http.StatusForbidden {
		t.Fatalf("outside the roots: %d", status)
	}
	if status, _ := call("POST", "/v1/directories/favorites", `{"path":`+jsonString(filepath.Join(root, "missing"))+`}`); status != http.StatusNotFound {
		t.Fatalf("missing folder: %d", status)
	}
	if status, _ := call("POST", "/v1/directories/favorites", `{"path":"relative"}`); status != http.StatusForbidden {
		t.Fatalf("relative: %d", status)
	}
	// An app that kept favorites on the device sees the gateway already has a list.
	if _, got := call("POST", "/v1/directories/favorites/import", `{"paths":["/x"]}`); got.Imported {
		t.Fatal("import replaced a list built by adding")
	}
}

func Testお気に入りの並べ替えと削除(t *testing.T) {
	call, _ := newFavoritesServer(t)
	status, got := call("PUT", "/v1/directories/favorites", `{"paths":["/w/a","/w/b","/w/c","/w/a"]}`)
	if status != http.StatusOK || !reflect.DeepEqual(got.Paths, []string{"/w/a", "/w/b", "/w/c"}) {
		t.Fatalf("put %d %+v", status, got)
	}
	status, got = call("DELETE", "/v1/directories/favorites?path="+url.QueryEscape("/w/b"), "")
	if status != http.StatusOK || !reflect.DeepEqual(got.Paths, []string{"/w/a", "/w/c"}) {
		t.Fatalf("delete %d %+v", status, got)
	}
	if status, _ := call("DELETE", "/v1/directories/favorites?path="+url.QueryEscape("/w/b"), ""); status != http.StatusOK {
		t.Fatalf("deleting twice: %d", status)
	}
	if status, _ := call("PUT", "/v1/directories/favorites", `{"paths":["relative"]}`); status != http.StatusBadRequest {
		t.Fatalf("relative put: %d", status)
	}
	if status, _ := call("DELETE", "/v1/directories/favorites", ""); status != http.StatusBadRequest {
		t.Fatalf("delete without path: %d", status)
	}
	_, got = call("PUT", "/v1/directories/favorites", `{"paths":[]}`)
	if !got.Saved || got.Paths == nil || len(got.Paths) != 0 {
		t.Fatalf("emptied %+v", got)
	}
}
