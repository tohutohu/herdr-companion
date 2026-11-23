package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/favorites"
	"github.com/tohutohu/herdr-android-client/gateway/internal/projects"
)

func Testプロジェクトの通知を変えるとセッションに所属と段階が付く(t *testing.T) {
	store, err := favorites.Open(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatal(err)
	}
	ts, fp, _, tok := newTestServer(t, func(s *Server) {
		s.Favorites = store
		s.Projects = &projects.Resolver{Store: store}
		s.Sessions.Annotate = s.Projects.Annotate
	})
	if _, err := store.Add(fp.root, nil); err != nil {
		t.Fatal(err)
	}

	resp, body := do(t, ts, tok, "PATCH", "/v1/projects", []byte(`{"path":`+jsonString(fp.root)+`,"name":"Bot","notify":"off"}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch %d %s", resp.StatusCode, body)
	}
	resp, body = do(t, ts, tok, "GET", "/v1/projects", nil, "")
	var list struct{ Projects []favorites.Project }
	json.Unmarshal(body, &list)
	if resp.StatusCode != 200 || len(list.Projects) != 1 || list.Projects[0] != (favorites.Project{Path: fp.root, Name: "Bot", Notify: "off"}) {
		t.Fatalf("list %d %s", resp.StatusCode, body)
	}

	_, body = do(t, ts, tok, "GET", "/v1/sessions/fake:s1", nil, "")
	var sess struct{ ProjectPath, ProjectNotify string }
	json.Unmarshal(body, &sess)
	if sess.ProjectPath != fp.root || sess.ProjectNotify != "off" {
		t.Fatalf("session = %s", body)
	}

	for _, c := range []struct {
		body string
		want int
	}{
		{`{"path":"/not/registered","notify":"off"}`, http.StatusNotFound},
		{`{"path":` + jsonString(fp.root) + `,"notify":"loud"}`, http.StatusBadRequest},
		{`{"notify":"off"}`, http.StatusBadRequest},
	} {
		if resp, body := do(t, ts, tok, "PATCH", "/v1/projects", []byte(c.body), "application/json"); resp.StatusCode != c.want {
			t.Errorf("%s: %d %s", c.body, resp.StatusCode, body)
		}
	}
	_, body = do(t, ts, tok, "GET", "/v1/capabilities", nil, "")
	if !json.Valid(body) || !strings.Contains(string(body), `"projects":true`) {
		t.Fatalf("capabilities = %s", body)
	}
}
