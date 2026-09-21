package devin

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	_ "modernc.org/sqlite"
)

type fakeTerminal struct {
	promptPane string
	promptText string
}

func (f *fakeTerminal) SendKeys(context.Context, string, ...string) error { return nil }
func (f *fakeTerminal) SendText(context.Context, string, string) error    { return nil }
func (f *fakeTerminal) Prompt(_ context.Context, pane, text string) error {
	f.promptPane, f.promptText = pane, text
	return nil
}
func (f *fakeTerminal) ReadPane(context.Context, string, int) (*herdr.ReadResult, error) {
	return &herdr.ReadResult{}, nil
}

func fixture(t *testing.T) (*Provider, *sql.DB, *deadletter.Recorder) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE sessions (
			id TEXT PRIMARY KEY, working_directory TEXT NOT NULL, backend_type TEXT NOT NULL,
			model TEXT NOT NULL, agent_mode TEXT NOT NULL, created_at INTEGER NOT NULL,
			last_activity_at INTEGER NOT NULL, title TEXT, main_chain_id INTEGER,
			shell_last_seen_index INTEGER DEFAULT 0, cogs_json TEXT, workspace_dirs TEXT,
			hidden INTEGER NOT NULL DEFAULT 0, metadata TEXT)`,
		`CREATE TABLE message_nodes (
			row_id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
			node_id INTEGER NOT NULL, parent_node_id INTEGER, chat_message TEXT NOT NULL,
			created_at INTEGER NOT NULL, metadata TEXT)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	rec := &deadletter.Recorder{}
	term := &fakeTerminal{}
	return New(Config{Database: path}, term, rec), db, rec
}

func addSession(t *testing.T, db *sql.DB, id string, now int64, hidden int) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO sessions
		(id, working_directory, backend_type, model, agent_mode, created_at, last_activity_at, title, main_chain_id, hidden)
		VALUES (?, ?, 'local', ?, ?, ?, ?, ?, ?, ?)`,
		id, "/work/demo", "gpt-6-astra", "accept-edits", now-10, now, "Example", 3, hidden)
	if err != nil {
		t.Fatal(err)
	}
}

func addNode(t *testing.T, db *sql.DB, session string, id int64, parent any, raw string, created int64, metadata string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO message_nodes(session_id,node_id,parent_node_id,chat_message,created_at,metadata) VALUES(?,?,?,?,?,?)`, session, id, parent, raw, created, metadata); err != nil {
		t.Fatal(err)
	}
}

func Test履歴はmain_chainだけを表示しセッション情報と画像を保持する(t *testing.T) {
	p, db, rec := fixture(t)
	now := time.Now().Truncate(time.Second).Unix()
	addSession(t, db, "devin-test", now, 0)
	addNode(t, db, "devin-test", 1, nil, `{"role":"user","content":"Implement feature"}`, now-9, "")
	addNode(t, db, "devin-test", 2, 1, `{"role":"assistant","content":[{"type":"text","text":"I will inspect the project."},{"type":"tool_use","name":"read_file","input":{"path":"README.md"}}],"images":[{"mime_type":"image/png","base64_data":"`+base64.StdEncoding.EncodeToString([]byte{1, 2, 3})+`"}]}`, now-8, `{"context_usage":{"used_tokens":100,"window_tokens":1000}}`)
	addNode(t, db, "devin-test", 3, 2, `{"role":"tool","content":"README content"}`, now-7, "")
	addNode(t, db, "devin-test", 4, 1, `{"role":"assistant","content":"This abandoned branch must not appear."}`, now-6, "")

	s, err := p.Summary(context.Background(), "devin-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Cwd != "/work/demo" || s.Model != "gpt-6-astra" || s.Mode != "Accept edits" || s.Title != "Example" || !strings.Contains(s.LastMessage, "README content") {
		t.Fatal(s)
	}
	if s.Context == nil || s.Context.UsedTokens != 100 || s.Context.WindowTokens != 1000 {
		t.Fatal(s.Context)
	}

	messages, err := p.Messages(context.Background(), "devin-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || messages[0].Role != model.RoleUser || messages[1].Role != model.RoleAssistant || messages[2].Role != model.RoleTool {
		t.Fatalf("messages=%+v", messages)
	}
	if len(messages[1].Blocks) < 2 || !strings.Contains(messages[1].Blocks[1].Text, "read_file") {
		t.Fatalf("assistant blocks=%+v", messages[1].Blocks)
	}
	mime, data, err := p.Image(context.Background(), "devin-test", "node-2", 0)
	if err != nil || mime != "image/png" || string(data) != string([]byte{1, 2, 3}) {
		t.Fatalf("image=%q %v %v", mime, data, err)
	}
	if len(rec.Entries) != 0 {
		t.Fatal(rec.Entries)
	}
}

func TestRecentとLocateLaunchedはhiddenを除外する(t *testing.T) {
	p, db, _ := fixture(t)
	now := time.Now().Truncate(time.Second)
	addSession(t, db, "visible", now.Unix(), 0)
	addSession(t, db, "hidden", now.Unix(), 1)
	list, err := p.Recent(context.Background(), now.Add(-time.Minute))
	if err != nil || len(list) != 1 || list[0].NativeID != "visible" {
		t.Fatal(list, err)
	}
	if got := p.LocateLaunched(context.Background(), "/work/demo", now.Add(-time.Minute)); got != "visible" {
		t.Fatal(got)
	}
}

func TestSendはHerdrのペインへファイルパス付きで送る(t *testing.T) {
	p, _, _ := fixture(t)
	term := p.term.(*fakeTerminal)
	live := &providers.Live{PaneID: "w1:p1", HerdrStatus: herdr.StatusIdle}
	if err := p.Send(context.Background(), "devin-test", live, model.Input{Text: "Continue", Files: []string{"/tmp/notes.txt"}}); err != nil {
		t.Fatal(err)
	}
	if term.promptPane != "w1:p1" || term.promptText != "Continue\n/tmp/notes.txt" {
		t.Fatal(term)
	}
}

func TestModelsはDevinのモデルファミリーを読む(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "devin")
	output := "Available models (2 families)\n\nClaude Opus 5 (claude-opus-5)\n  aliases: opus\nGPT-6 Astra (gpt-6-astra)\n"
	script := fmt.Sprintf("#!/bin/sh\ncat <<'EOF'\n%sEOF\n", output)
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	p := New(Config{Binary: bin, Database: filepath.Join(dir, "missing.db")}, &fakeTerminal{}, nil)
	cat, err := p.Models(context.Background())
	if err != nil || len(cat.Models) != 2 || cat.Models[0].ID != "claude-opus-5" || cat.Models[1].ID != "gpt-6-astra" {
		t.Fatal(cat, err)
	}
}
