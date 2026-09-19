package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

func fixtureDB(t *testing.T) (*Provider, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range []string{
		"PRAGMA journal_mode=WAL",
		"CREATE TABLE session(id TEXT PRIMARY KEY,directory TEXT,title TEXT,parent_id TEXT,time_created INTEGER,time_updated INTEGER)",
		"CREATE TABLE session_v2(id TEXT PRIMARY KEY,directory TEXT,title TEXT,parent_id TEXT,time_created INTEGER,time_updated INTEGER)",
		"CREATE TABLE message(id TEXT PRIMARY KEY,session_id TEXT,time_created INTEGER,data TEXT)",
		"CREATE TABLE part(id TEXT PRIMARY KEY,message_id TEXT,session_id TEXT,data TEXT)",
		"CREATE TABLE session_message(id TEXT PRIMARY KEY,session_id TEXT,type TEXT,seq INTEGER,time_created INTEGER,data TEXT)",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return New(Config{Database: path, StateDir: dir}, &fakeTerminal{}, &deadletter.Recorder{}), db
}
func insertSession(t *testing.T, db *sql.DB, table, id string) {
	t.Helper()
	if _, err := db.Exec("INSERT INTO "+table+" VALUES(?,?,?,NULL,?,?)", id, "/work/demo", "Example", 1000, 2000); err != nil {
		t.Fatal(err)
	}
}
func loadRecords(t *testing.T, name string) []record {
	t.Helper()
	raw, err := os.ReadFile("../../../testdata/opencode/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var rs []record
	if err := json.Unmarshal(raw, &rs); err != nil {
		t.Fatal(err)
	}
	return rs
}
func populate(t *testing.T, db *sql.DB, id string, rs []record) {
	t.Helper()
	for i, r := range rs {
		if r.Kind != "" {
			if _, err := db.Exec("INSERT INTO session_message VALUES(?,?,?,?,?,?)", r.ID, id, r.Kind, i, r.Created, string(r.Raw)); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := db.Exec("INSERT INTO message VALUES(?,?,?,?)", r.ID, id, r.Created, string(r.Raw)); err != nil {
				t.Fatal(err)
			}
			for j, raw := range r.Parts {
				if _, err := db.Exec("INSERT INTO part VALUES(?,?,?,?)", fmt.Sprintf("%s-%03d", r.ID, j), r.ID, id, string(raw)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
func Test両バージョンの履歴と画像をWALから読み取れる(t *testing.T) {
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			p, db := fixtureDB(t)
			table := "session"
			if version == "v2" {
				table = "session_v2"
			}
			insertSession(t, db, table, "ses_test")
			populate(t, db, "ses_test", loadRecords(t, version))
			s, err := p.Summary(context.Background(), "ses_test", nil)
			if err != nil {
				t.Fatal(err)
			}
			if s.Model != "anthropic/claude-test" || s.Cost == nil || s.Cost.USD != 0.012 || s.LastTurnFailed {
				t.Fatalf("summary: %+v", s)
			}
			msgs, err := p.Messages(context.Background(), "ses_test", nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) != 2 || msgs[0].Role != model.RoleUser || msgs[1].Role != model.RoleAssistant {
				t.Fatalf("messages: %+v", msgs)
			}
			if len(msgs[0].Blocks) != 2 || msgs[0].Blocks[1].Type != model.BlockImage {
				t.Fatalf("image block: %+v", msgs[0])
			}
			mime, data, err := p.Image(context.Background(), "ses_test", msgs[0].ID, 0)
			if err != nil || mime != "image/png" || string(data) != "fixture-image" {
				t.Fatalf("image: %s %q %v", mime, data, err)
			}
			for _, b := range msgs[1].Blocks {
				if strings.Contains(b.Text, "hidden reasoning") {
					t.Fatal("reasoning exposed")
				}
			}
			ro, err := p.database()
			if err != nil {
				t.Fatal(err)
			}
			defer ro.Close()
			if _, err := ro.Exec("DELETE FROM " + table); err == nil {
				t.Fatal("database was writable")
			}
		})
	}
}
func Test移行済みセッションは重複せず空のv2も表示できる(t *testing.T) {
	p, db := fixtureDB(t)
	insertSession(t, db, "session", "ses_same")
	insertSession(t, db, "session_v2", "ses_same")
	insertSession(t, db, "session_v2", "ses_empty")
	recent, err := p.Recent(context.Background(), time.UnixMilli(0))
	if err != nil || len(recent) != 2 {
		t.Fatalf("recent: %+v %v", recent, err)
	}
	msgs, err := p.Messages(context.Background(), "ses_empty", nil)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("empty: %+v %v", msgs, err)
	}
	recent, err = p.RecentExcluding(context.Background(), time.UnixMilli(0), map[string]bool{"ses_same": true})
	if err != nil || len(recent) != 1 || recent[0].NativeID != "ses_empty" {
		t.Fatalf("exclude: %+v %v", recent, err)
	}
}
func Test未知イベントと壊れたレコードを記録して再生できる(t *testing.T) {
	sink := &deadletter.Recorder{}
	p := New(Config{}, nil, sink)
	c := p.convert("ses_test", []record{{ID: "bad", Kind: "assistant", Raw: json.RawMessage(`{"content":[{"type":"future"}]}`)}})
	if len(c.Messages) != 1 || len(sink.Entries) != 1 {
		t.Fatalf("%+v %+v", c, sink.Entries)
	}
	msgs, entries := Replay(sink.Entries[0].Raw)
	if len(msgs) != 1 || len(entries) != 1 {
		t.Fatal("replay lost unsupported record")
	}
}
func TestDB未作成ではファイルを作らず空一覧を返す(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.db")
	p := New(Config{Database: path}, nil, nil)
	if list, err := p.Recent(context.Background(), time.Time{}); err != nil || len(list) != 0 {
		t.Fatalf("%+v %v", list, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created database")
	}
	if _, err := p.Messages(context.Background(), "' OR 1=1 --", nil); !errors.Is(err, providers.ErrNotFound) {
		t.Fatal(err)
	}
}

type fakeTerminal struct{ text string }

func (f *fakeTerminal) Prompt(_ context.Context, _ string, text string) error {
	f.text = text
	return nil
}
func (*fakeTerminal) SendKeys(context.Context, string, ...string) error { return nil }
func (*fakeTerminal) SendText(context.Context, string, string) error    { return nil }
func (*fakeTerminal) ReadPane(context.Context, string, int) (*herdr.ReadResult, error) {
	return nil, nil
}
func Testサーバー未接続はテキスト送信と端末回答にフォールバックする(t *testing.T) {
	p, db := fixtureDB(t)
	insertSession(t, db, "session_v2", "ses_test")
	live := &providers.Live{PaneID: "p", Cwd: "/work/demo", HerdrStatus: herdr.StatusBlocked}
	msgs, err := p.Messages(context.Background(), "ses_test", live)
	if err != nil || len(msgs) != 1 || msgs[0].Blocks[0].Interaction.Supported {
		t.Fatalf("%+v %v", msgs, err)
	}
	if err := p.Send(context.Background(), "ses_test", nil, model.Input{Text: "hello"}); !errors.Is(err, providers.ErrNotLive) {
		t.Fatal(err)
	}
	live.HerdrStatus = herdr.StatusIdle
	if err := p.Send(context.Background(), "ses_test", live, model.Input{Text: "hello"}); err != nil || p.term.(*fakeTerminal).text != "hello" {
		t.Fatal(err)
	}
	if err := p.Send(context.Background(), "ses_test", live, model.Input{Images: []string{"/tmp/img.png"}}); err == nil {
		t.Fatal("image silently lost")
	}
}
func Test旧版の承認と質問と送信は対象セッションだけに作用する(t *testing.T) {
	var path string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			path = r.URL.Path
			json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(204)
			return
		}
		switch r.URL.Path {
		case "/permission":
			fmt.Fprint(w, `[{"id":"per_1","sessionID":"ses_test","permission":"bash","patterns":["echo hi"]},{"id":"per_other","sessionID":"other"}]`)
		case "/question":
			fmt.Fprint(w, `[{"id":"que_1","sessionID":"ses_test","questions":[{"question":"Pick","options":[{"label":"A"},{"label":"B"}],"custom":true}]}]`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer server.Close()
	p := New(Config{ServerURL: server.URL}, nil, nil)
	live := &providers.Live{Cwd: "/work/demo"}
	ctx := context.Background()
	if err := p.Respond(ctx, "ses_test", live, model.InteractionResponse{InteractionID: "opencode-permission:per_other", Decision: model.DecisionApprove}); !errors.Is(err, providers.ErrInteractionGone) {
		t.Fatal(err)
	}
	if err := p.Respond(ctx, "ses_test", live, model.InteractionResponse{InteractionID: "opencode-permission:per_1", Decision: model.DecisionApprove}); err != nil || path != "/permission/per_1/reply" || body["reply"] != "once" {
		t.Fatalf("%s %+v %v", path, body, err)
	}
	if err := p.Respond(ctx, "ses_test", live, model.InteractionResponse{InteractionID: "opencode-question:que_1", Answers: map[string]model.Answer{"0": {Selected: []string{"B"}}}}); err != nil || path != "/question/que_1/reply" {
		t.Fatal(path, err)
	}
	if err := p.Send(ctx, "ses_test", live, model.Input{Text: "hello", Images: []string{"/work/demo/a.png"}}); err != nil || path != "/session/ses_test/prompt_async" {
		t.Fatal(path, err)
	}
	if len(body["parts"].([]any)) != 2 {
		t.Fatal(body)
	}
}

func Test公式v2の接続情報とフォーム値と送信形式に対応する(t *testing.T) {
	var lastPath string
	var lastBody map[string]any
	formJSON := `{"id":"frm_1","sessionID":"ses_test","title":"Pick","fields":[{"key":"color","type":"string","title":"Color","options":[{"label":"Red","value":"red-value"},{"label":"Blue","value":"blue-value"}]},{"key":"note","type":"string","title":"Note"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != "test-password" {
			t.Error("missing auth")
		}
		if r.Method == "POST" {
			lastPath = r.URL.Path
			json.NewDecoder(r.Body).Decode(&lastBody)
			w.WriteHeader(204)
			return
		}
		switch r.URL.Path {
		case "/api/session/ses_test/permission":
			fmt.Fprint(w, `{"data":[{"id":"per_1","sessionID":"ses_test","action":"edit","resources":["a.go"]}]}`)
		case "/api/session/ses_test/form":
			fmt.Fprint(w, `{"data":[`+formJSON+`]}`)
		case "/api/session/ses_test/form/frm_1":
			var f map[string]any
			json.Unmarshal([]byte(formJSON), &f)
			f["state"] = map[string]string{"status": "pending"}
			json.NewEncoder(w).Encode(map[string]any{"data": f})
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	registration, _ := json.Marshal(map[string]any{"url": server.URL, "pid": 123, "password": "test-password", "version": "2.0.9"})
	os.WriteFile(filepath.Join(dir, "service.json"), registration, 0600)
	p, db := fixtureDB(t)
	insertSession(t, db, "session_v2", "ses_test")
	p.cfg.StateDir = dir
	c, err := p.connection()
	if err != nil || c.Version != 2 {
		t.Fatal(c.Version, err)
	}
	live := &providers.Live{Cwd: "/work/demo"}
	ctx := context.Background()
	pending, err := p.pending(ctx, c, "ses_test", live.Cwd)
	if err != nil || len(pending) != 2 || !pending[1].Supported {
		t.Fatalf("%+v %v", pending, err)
	}
	response := model.InteractionResponse{InteractionID: "opencode-form:frm_1", Answers: map[string]model.Answer{"color": {Selected: []string{"Blue"}}, "note": {Text: "hello"}}}
	if err := p.Respond(ctx, "ses_test", live, response); err != nil {
		t.Fatal(err)
	}
	if lastPath != "/api/session/ses_test/form/frm_1/reply" || lastBody["answer"].(map[string]any)["color"] != "blue-value" {
		t.Fatal(lastPath, lastBody)
	}
	response.Answers["color"] = model.Answer{Selected: []string{"not-an-option"}}
	if err := p.Respond(ctx, "ses_test", live, response); err == nil {
		t.Fatal("accepted invalid option")
	}
	if err := p.Send(ctx, "ses_test", live, model.Input{Text: "hi", Images: []string{"/work/demo/image a.png"}}); err != nil {
		t.Fatal(err)
	}
	prompt := lastBody
	file := prompt["files"].([]any)[0].(map[string]any)
	if lastPath != "/api/session/ses_test/prompt" || file["uri"] != "file:///work/demo/image%20a.png" {
		t.Fatal(lastPath, lastBody)
	}
}
func Test条件付きフォームは端末に誘導する(t *testing.T) {
	f := form{ID: "frm_1", Fields: []formField{{Key: "a", Type: "string", When: []any{map[string]string{"key": "x"}}}}}
	if f.interaction().Supported {
		t.Fatal("conditional form cannot be faithfully rendered")
	}
}
func Test発見した接続先へ資格情報を送る前にループバックを検証する(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "service.json"), []byte(`{"url":"https://example.com","password":"secret"}`), 0600)
	p := New(Config{StateDir: dir}, nil, nil)
	if _, err := p.connection(); err == nil {
		t.Fatal("accepted remote discovered endpoint")
	}
}
func Test送信エラー後に端末へ重複送信しない(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	term := &fakeTerminal{}
	p := New(Config{ServerURL: server.URL}, term, nil)
	if err := p.Send(context.Background(), "ses_test", &providers.Live{PaneID: "p"}, model.Input{Text: "hello"}); err == nil || term.text != "" {
		t.Fatal("ambiguous send retried")
	}
}

func Test公式v2のモデル選択はセッション作成APIを使う(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session" {
			t.Error(r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		fmt.Fprint(w, `{"data":{"id":"ses_created"}}`)
	}))
	defer server.Close()
	dir := t.TempDir()
	binary := filepath.Join(dir, "opencode")
	os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'opencode v2.0.9\\n'\n"), 0700)
	p := New(Config{Binary: binary, ServerURL: server.URL, ServerVersion: 2}, nil, nil)
	args, id, err := p.PrepareLaunch(context.Background(), providers.LaunchOptions{Model: "anthropic/claude-test", Cwd: "/work/demo"})
	if err != nil || id != "ses_created" || strings.Join(args, " ") != "--session ses_created" {
		t.Fatal(args, id, err)
	}
	if got["model"].(map[string]any)["id"] != "claude-test" || got["location"].(map[string]any)["directory"] != "/work/demo" {
		t.Fatal(got)
	}
	os.WriteFile(binary, []byte("#!/bin/sh\nprintf '1.2.0\\n'\n"), 0700)
	args, id, err = p.PrepareLaunch(context.Background(), providers.LaunchOptions{Model: "anthropic/claude-test"})
	if err != nil || id != "" || strings.Join(args, " ") != "--model anthropic/claude-test" {
		t.Fatal(args, id, err)
	}
}
func Test旧版セッションは別のv2デーモンへ送らない(t *testing.T) {
	p, db := fixtureDB(t)
	insertSession(t, db, "session", "ses_v1")
	os.WriteFile(filepath.Join(p.cfg.StateDir, "service.json"), []byte(`{"url":"http://127.0.0.1:18999","password":"test"}`), 0600)
	if _, err := p.connectionFor(context.Background(), "ses_v1"); !errors.Is(err, providers.ErrUnsupported) {
		t.Fatal(err)
	}
	if err := p.Send(context.Background(), "ses_v1", &providers.Live{PaneID: "p"}, model.Input{Text: "hi"}); err != nil || p.term.(*fakeTerminal).text != "hi" {
		t.Fatal(err)
	}
}

func Test壊れたJSONも元のバイト列を失わず再生できる(t *testing.T) {
	sink := &deadletter.Recorder{}
	p := New(Config{}, nil, sink)
	p.convert("ses_test", []record{{ID: "bad", Kind: "assistant", Raw: json.RawMessage(`{broken`)}})
	if len(sink.Entries) != 1 || !strings.Contains(string(sink.Entries[0].Raw), "{broken") {
		t.Fatal(sink.Entries)
	}
	_, entries := Replay(sink.Entries[0].Raw)
	if len(entries) != 1 || entries[0].Kind != deadletter.ParseError {
		t.Fatal(entries)
	}
}
