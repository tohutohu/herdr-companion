package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/notes"
)

// withNotes copies sessions with their notes, as the Gateway returns them.
func (g *fakeGateway) withNotes(list []model.Session) []model.Session {
	if g.notes == nil {
		return list
	}
	out := append([]model.Session{}, list...)
	for i := range out {
		out[i].Notes = g.notes.For(out[i].ID)
	}
	return out
}

// serveNotes answers the note endpoints like the Gateway (internal/api/notes.go).
func (g *fakeGateway) serveNotes(w http.ResponseWriter, r *http.Request) bool {
	reply := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	fail := func(err error) {
		status := http.StatusBadRequest
		if err == notes.ErrNotFound {
			status = http.StatusNotFound
		}
		reply(status, map[string]string{"error": err.Error()})
	}
	change := func() notes.Change {
		var body struct {
			Text      *string `json:"text"`
			Author    string  `json:"author"`
			ExpiresAt *string `json:"expiresAt"`
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		var fields map[string]json.RawMessage
		json.Unmarshal(b, &fields)
		g.posted = append(g.posted, postedRequest{r.URL.Path, map[string]any{"author": body.Author}})
		c := notes.Change{Text: body.Text, Author: body.Author}
		if _, ok := fields["expiresAt"]; ok {
			c.ExpiresAt = &time.Time{}
			if body.ExpiresAt != nil {
				t, _ := time.Parse(time.RFC3339, *body.ExpiresAt)
				c.ExpiresAt = &t
			}
		}
		return c
	}
	path := r.URL.Path
	switch {
	case path == "/v1/capabilities":
		reply(http.StatusOK, map[string]any{"notes": true})
	case path == "/v1/notes":
		reply(http.StatusOK, map[string]any{"notes": g.notes.All()})
	case strings.HasPrefix(path, "/v1/notes/"):
		id := strings.TrimPrefix(path, "/v1/notes/")
		var n model.Note
		var err error
		switch r.Method {
		case http.MethodGet:
			n, err = g.notes.Get(id)
		case http.MethodPatch:
			n, err = g.notes.Update(id, change())
		case http.MethodDelete:
			n, err = g.notes.Delete(id)
		}
		if err != nil {
			fail(err)
		} else {
			reply(http.StatusOK, n)
		}
	case strings.HasPrefix(path, "/v1/sessions/") && strings.HasSuffix(path, "/notes"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/sessions/"), "/notes")
		switch r.Method {
		case http.MethodPost:
			n, err := g.notes.Create(id, change())
			if err != nil {
				fail(err)
				return true
			}
			reply(http.StatusCreated, n)
		case http.MethodDelete:
			gone, _ := g.notes.Clear(id)
			reply(http.StatusOK, map[string]any{"notes": gone})
		default:
			reply(http.StatusOK, map[string]any{"notes": g.notes.For(id)})
		}
	default:
		return false
	}
	return true
}

func notesGateway(t *testing.T) *fakeGateway {
	t.Helper()
	store, err := notes.Open(filepath.Join(t.TempDir(), "notes.json"))
	if err != nil {
		t.Fatal(err)
	}
	g := sampleGateway()
	g.notes = store
	return g
}

func Testメモを残すと一覧と詳細と待機結果に表示される(t *testing.T) {
	g := notesGateway(t)
	a, _ := newTestApp(t, g)
	out := a.mustRun(t, "note", "add", "aaaa1111", "PRにレビューが来たら対応させて")
	if !strings.HasPrefix(out, "added note n") || !strings.Contains(out, "claude:aaaa1111-0000") {
		t.Fatalf("add = %q", out)
	}
	if got := g.posted[len(g.posted)-1].Body["author"]; got != "claude:ffff0000-self" {
		t.Errorf("author = %v", got)
	}
	n := g.notes.For("claude:aaaa1111-0000")[0]

	out = a.mustRun(t, "ls")
	if !strings.Contains(out, "ログイン修正\n    note "+n.ID+" (now, by claude:ffff0000): PRにレビューが来たら対応させて\n") {
		t.Errorf("ls =\n%s", out)
	}
	out = a.mustRun(t, "note", "ls")
	if !strings.Contains(out, "aaaa1111") || !strings.Contains(out, "note "+n.ID+" (now, by claude:ffff0000): PRにレビューが来たら対応させて\n") {
		t.Errorf("note ls =\n%s", out)
	}
	out = a.mustRun(t, "show", "aaaa1111")
	if !strings.Contains(out, "notes (1,") || !strings.Contains(out, "PRにレビューが来たら対応させて") {
		t.Errorf("show =\n%s", out)
	}

	// 監視中のセッションが回答待ちになったら、メモが一緒に出る
	a.mustRun(t, "changes")
	g.setStatus("claude:aaaa1111-0000", model.StatusWaitingInput)
	out = a.mustRun(t, "wait", "--timeout", "1s")
	if !strings.Contains(out, "note "+n.ID+" (now, by claude:ffff0000): PRにレビューが来たら対応させて") {
		t.Errorf("wait =\n%s", out)
	}
	out = a.mustRun(t, "until", "aaaa1111", "--for", "input")
	if !strings.Contains(out, "note "+n.ID) {
		t.Errorf("until =\n%s", out)
	}
}

func Testメモを前方一致のIDで更新して削除できる(t *testing.T) {
	g := notesGateway(t)
	a, _ := newTestApp(t, g)
	a.mustRun(t, "note", "add", "bbbb3333", "--ttl", "3d", "ずっと待機させておく")
	n := g.notes.For("codex:bbbb3333")[0]
	if n.ExpiresAt == nil || time.Until(*n.ExpiresAt) < 71*time.Hour {
		t.Fatalf("expiry = %v", n.ExpiresAt)
	}

	a.in = strings.NewReader("PRがマージされたら\nアーカイブしてよい\n")
	out := a.mustRun(t, "note", "edit", n.ID[:5], "--no-expiry", "-")
	if !strings.Contains(out, "updated note "+n.ID) {
		t.Fatalf("edit = %q", out)
	}
	got, _ := g.notes.Get(n.ID)
	if got.Text != "PRがマージされたら\nアーカイブしてよい" || got.ExpiresAt != nil || got.UpdatedBy != "claude:ffff0000-self" {
		t.Fatalf("after edit = %+v", got)
	}
	out = a.mustRun(t, "note", "show", n.ID)
	if !strings.Contains(out, "session codex:bbbb3333") || !strings.Contains(out, "(now, by claude:ffff0000):\n  PRがマージされたら\n  アーカイブしてよい\n") {
		t.Errorf("show =\n%s", out)
	}

	out = a.mustRun(t, "note", "rm", n.ID)
	if !strings.Contains(out, "removed note "+n.ID+" from codex:bbbb3333") {
		t.Errorf("rm = %q", out)
	}
	if left := g.notes.All(); len(left) != 0 {
		t.Errorf("left = %+v", left)
	}
	if out := a.mustRun(t, "note", "ls"); !strings.Contains(out, "no notes") {
		t.Errorf("ls after rm = %q", out)
	}
}

func Test自分自身のセッションにもメモを残せてまとめて消せる(t *testing.T) {
	g := notesGateway(t)
	a, _ := newTestApp(t, g)
	a.mustRun(t, "note", "add", "self", "PR #12 を作った。レビューが付いたら起こして")
	a.mustRun(t, "note", "add", "self", "CI が落ちたら直させる")
	if got := g.notes.For("claude:ffff0000-self"); len(got) != 2 {
		t.Fatalf("self notes = %+v", got)
	}
	out := a.mustRun(t, "note", "ls", "self")
	if !strings.Contains(out, "(you)") || strings.Count(out, "note n") != 2 {
		t.Errorf("ls self =\n%s", out)
	}
	if out := a.mustRun(t, "note", "clear", "self"); !strings.Contains(out, "removed 2 note(s)") {
		t.Errorf("clear = %q", out)
	}
}

func Testメモの誤った指定は何も変えずに失敗する(t *testing.T) {
	g := notesGateway(t)
	a, out := newTestApp(t, g)
	a.mustRun(t, "note", "add", "aaaa1111", "残す")
	for _, args := range [][]string{
		{"note", "add", "aaaa1111"},
		{"note", "add", "aaaa1111", "--ttl", "soon", "x"},
		{"note", "edit", "n"},
		{"note", "frobnicate"},
	} {
		out.Reset()
		if code := a.run(args); code != 2 {
			t.Errorf("hc %v exited %d, want 2: %s", args, code, out)
		}
	}
	out.Reset()
	if code := a.run([]string{"note", "rm", g.notes.All()[0].ID, "nzzzz"}); code != 1 || !strings.Contains(out.String(), "no note matches") {
		t.Errorf("rm with a typo: %d %s", code, out)
	}
	if len(g.notes.All()) != 1 {
		t.Error("a note was removed although one of the IDs did not match")
	}
}

func Testメモに対応しないGatewayでは更新を促す(t *testing.T) {
	a, out := newTestApp(t, sampleGateway())
	if code := a.run([]string{"note", "ls"}); code != 1 || !strings.Contains(out.String(), "update the Gateway") {
		t.Errorf("old gateway: %d %s", code, out)
	}
}

func Testセッション一覧だけでメモの全文と期限が分かる(t *testing.T) {
	g := notesGateway(t)
	g.sessions = append(g.sessions, model.Session{ID: "claude:cccc4444-0000", Provider: "claude", Project: "app", Title: "待機中", Status: model.StatusOffline, UpdatedAt: t0})
	g.sessions = append(g.sessions, model.Session{ID: "claude:dddd5555-0000", Provider: "claude", Project: "old", Status: model.StatusOffline, UpdatedAt: t0})
	a, _ := newTestApp(t, g)
	long := strings.Repeat("レビューコメントに一つずつ返信し、直したらプッシュする。", 20)
	a.mustRun(t, "note", "add", "aaaa1111", long)
	a.mustRun(t, "note", "add", "aaaa1111", "--ttl", "3d", "レビュー待ち")
	a.in = strings.NewReader("1. CI が通るまで待つ\n2. マージされたらアーカイブしてよい\n")
	a.mustRun(t, "note", "add", "aaaa1111", "-")
	a.mustRun(t, "note", "add", "cccc4444", "ずっと待機させておく")

	out := a.mustRun(t, "ls")
	if !strings.Contains(out, long) {
		t.Errorf("ls cut a long note:\n%s", out)
	}
	if !strings.Contains(out, ", expires in 2d): レビュー待ち\n") && !strings.Contains(out, ", expires in 3d): レビュー待ち\n") {
		t.Errorf("ls does not say when the note expires:\n%s", out)
	}
	if !strings.Contains(out, "):\n      1. CI が通るまで待つ\n      2. マージされたらアーカイブしてよい\n") {
		t.Errorf("ls does not keep the lines of a note:\n%s", out)
	}
	// 止まっているセッションでも、メモがあれば一覧に残る。メモの無いものは数えるだけ
	if !strings.Contains(out, "cccc4444 offline") || !strings.Contains(out, "ずっと待機させておく") || strings.Contains(out, "dddd5555") ||
		!strings.Contains(out, "(+1 offline: hc ls --all)") {
		t.Errorf("ls with an offline session that has a note:\n%s", out)
	}
}

func Test検索結果にもメモが出る(t *testing.T) {
	g := notesGateway(t)
	a, _ := newTestApp(t, g)
	a.mustRun(t, "note", "add", "aaaa1111", "マージされたらアーカイブしてよい")
	g.search = func(url.Values) any {
		s := g.withNotes(g.sessions)[0]
		return map[string]any{"sessions": []any{s}}
	}
	out := a.mustRun(t, "search", "ログイン")
	if !strings.Contains(out, "ログイン修正\n    note n") || !strings.Contains(out, "マージされたらアーカイブしてよい") {
		t.Errorf("search =\n%s", out)
	}
}
