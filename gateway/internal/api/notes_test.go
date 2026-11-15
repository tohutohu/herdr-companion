package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/notes"
)

func newNotesServer(t *testing.T) (func(method, path, body string) (int, []byte), *notes.Store) {
	t.Helper()
	store, err := notes.Open(filepath.Join(t.TempDir(), "notes.json"))
	if err != nil {
		t.Fatal(err)
	}
	ts, _, _, tok := newTestServer(t, func(s *Server) { s.Notes = store })
	call := func(method, path, body string) (int, []byte) {
		t.Helper()
		var b []byte
		ctype := ""
		if body != "" {
			b, ctype = []byte(body), "application/json"
		}
		resp, out := do(t, ts, tok, method, path, b, ctype)
		return resp.StatusCode, out
	}
	return call, store
}

func Testセッションにメモを残すとセッション情報とメモ一覧に載る(t *testing.T) {
	call, _ := newNotesServer(t)
	status, body := call("POST", "/v1/sessions/fake:s1/notes", `{"text":"  PRにレビューが来たら対応させて  ","author":"claude:watcher"}`)
	if status != http.StatusCreated {
		t.Fatalf("create status %d: %s", status, body)
	}
	var n model.Note
	json.Unmarshal(body, &n)
	if !strings.HasPrefix(n.ID, "n") || n.SessionID != "fake:s1" || n.Text != "PRにレビューが来たら対応させて" || n.Author != "claude:watcher" || n.ExpiresAt != nil {
		t.Fatalf("note = %+v", n)
	}

	for _, path := range []string{"/v1/sessions/fake:s1", "/v1/sessions"} {
		_, body = call("GET", path, "")
		var got struct {
			model.Session
			Sessions []model.Session `json:"sessions"`
		}
		json.Unmarshal(body, &got)
		sess := got.Session
		if len(got.Sessions) > 0 {
			sess = got.Sessions[0]
		}
		if sess.ID != "fake:s1" || len(sess.Notes) != 1 || sess.Notes[0].ID != n.ID {
			t.Fatalf("GET %s: session = %+v", path, sess)
		}
	}
	_, body = call("GET", "/v1/notes", "")
	if !strings.Contains(string(body), n.ID) {
		t.Fatalf("all notes = %s", body)
	}
	_, body = call("GET", "/v1/sessions/fake:old/notes", "")
	if strings.TrimSpace(string(body)) != `{"notes":[]}` {
		t.Fatalf("notes of a session without notes = %s", body)
	}
}

func Test存在しないセッションや空のメモは作れない(t *testing.T) {
	call, _ := newNotesServer(t)
	if status, body := call("POST", "/v1/sessions/fake:nope/notes", `{"text":"x"}`); status != http.StatusNotFound {
		t.Errorf("unknown session: %d %s", status, body)
	}
	if status, body := call("POST", "/v1/sessions/fake:s1/notes", `{"text":"   "}`); status != http.StatusBadRequest {
		t.Errorf("empty text: %d %s", status, body)
	}
	if status, body := call("POST", "/v1/sessions/fake:s1/notes", `{"text":"x","expiresAt":"tomorrow"}`); status != http.StatusBadRequest {
		t.Errorf("bad expiry: %d %s", status, body)
	}
	if status, body := call("PATCH", "/v1/notes/nmissing", `{"text":"x"}`); status != http.StatusNotFound {
		t.Errorf("unknown note: %d %s", status, body)
	}
}

func Testメモを更新し期限を付け外しして削除できる(t *testing.T) {
	call, _ := newNotesServer(t)
	_, body := call("POST", "/v1/sessions/fake:s1/notes", `{"text":"待機させておく"}`)
	var n model.Note
	json.Unmarshal(body, &n)

	exp := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	status, body := call("PATCH", "/v1/notes/"+n.ID, `{"text":"PRがマージされたらアーカイブしてよい","expiresAt":"`+exp+`","author":"codex:bot"}`)
	if status != http.StatusOK {
		t.Fatalf("update status %d: %s", status, body)
	}
	var upd model.Note
	json.Unmarshal(body, &upd)
	if upd.Text != "PRがマージされたらアーカイブしてよい" || upd.ExpiresAt == nil || upd.UpdatedBy != "codex:bot" || upd.Author != "" || !upd.CreatedAt.Equal(n.CreatedAt) {
		t.Fatalf("updated = %+v", upd)
	}

	// 期限だけを外す。本文はそのまま
	_, body = call("PATCH", "/v1/notes/"+n.ID, `{"expiresAt":null}`)
	upd = model.Note{}
	json.Unmarshal(body, &upd)
	if upd.ExpiresAt != nil || upd.Text != "PRがマージされたらアーカイブしてよい" {
		t.Fatalf("after clearing expiry = %+v", upd)
	}
	if status, _ := call("PATCH", "/v1/notes/"+n.ID, `{}`); status != http.StatusBadRequest {
		t.Errorf("empty update status = %d", status)
	}

	if status, body := call("DELETE", "/v1/notes/"+n.ID, ""); status != http.StatusOK {
		t.Fatalf("delete status %d: %s", status, body)
	}
	if status, _ := call("GET", "/v1/notes/"+n.ID, ""); status != http.StatusNotFound {
		t.Errorf("deleted note status = %d", status)
	}
}

func Testアーカイブ済みセッションのメモは全体一覧で既定では出ない(t *testing.T) {
	call, _ := newNotesServer(t)
	call("POST", "/v1/sessions/fake:s1/notes", `{"text":"マージされたらアーカイブ"}`)
	if status, body := call("POST", "/v1/sessions/fake:s1/archive", ""); status != http.StatusOK {
		t.Fatalf("archive: %d %s", status, body)
	}
	_, body := call("GET", "/v1/notes", "")
	if strings.TrimSpace(string(body)) != `{"notes":[]}` {
		t.Errorf("default list = %s", body)
	}
	_, body = call("GET", "/v1/notes?archived=true", "")
	if !strings.Contains(string(body), "マージされたらアーカイブ") {
		t.Errorf("list with archived = %s", body)
	}
	// セッション単位では、アーカイブ済みでもメモは残っている
	_, body = call("GET", "/v1/sessions/fake:s1/notes", "")
	if !strings.Contains(string(body), "マージされたらアーカイブ") {
		t.Errorf("session notes = %s", body)
	}
}

func Testセッションのメモをまとめて消せる(t *testing.T) {
	call, store := newNotesServer(t)
	call("POST", "/v1/sessions/fake:s1/notes", `{"text":"a"}`)
	call("POST", "/v1/sessions/fake:s1/notes", `{"text":"b"}`)
	status, body := call("DELETE", "/v1/sessions/fake:s1/notes", "")
	var res struct{ Notes []model.Note }
	json.Unmarshal(body, &res)
	if status != http.StatusOK || len(res.Notes) != 2 {
		t.Fatalf("clear: %d %s", status, body)
	}
	if left := store.For("fake:s1"); len(left) != 0 {
		t.Errorf("left = %+v", left)
	}
}

func Test機能一覧でメモに対応していることを示す(t *testing.T) {
	call, _ := newNotesServer(t)
	_, body := call("GET", "/v1/capabilities", "")
	if !strings.Contains(string(body), `"notes":true`) {
		t.Errorf("capabilities = %s", body)
	}
}
