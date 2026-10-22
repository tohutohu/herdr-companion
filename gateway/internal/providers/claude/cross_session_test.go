package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

func Test別セッションのメッセージは送信元と本文を表示する(t *testing.T) {
	tr, rec, _ := loadFixture(t, "cross_session.jsonl")
	msgs := tr.Messages(ParseOptions{SessionID: "claude:test", Live: &providers.Live{HerdrStatus: herdr.StatusWorking}, Sink: rec})
	assertGolden(t, "cross_session.golden.json", msgs)
	if len(rec.Entries) != 0 {
		t.Fatalf("dead letters = %+v", rec.Entries)
	}
	if len(msgs) != 6 {
		t.Fatalf("messages = %d", len(msgs))
	}
	for _, i := range []int{1, 2, 3, 5} {
		b := msgs[i].Blocks[0]
		if msgs[i].Role != model.RoleSystem || b.Type != model.BlockSessionMessage || b.SenderName != "reviewer-a" || b.SenderAddress != "uds:/tmp/cc-socks/12345.sock" {
			t.Errorf("peer message = %+v", msgs[i])
		}
	}
	if msgs[4].Role != model.RoleUser || !strings.HasPrefix(msgs[4].Blocks[0].Text, "<cross-session-message ") {
		t.Errorf("引用が変換された: %+v", msgs[4])
	}
	if !msgs[5].Queued || msgs[1].Queued {
		t.Fatal("queued state lost")
	}
	if offline := tr.Messages(ParseOptions{}); len(offline) != 5 {
		t.Fatalf("offline messages = %d", len(offline))
	}
}

func Test不完全な別セッションフレームは解釈しない(t *testing.T) {
	for _, text := range []string{
		`<cross-session-message from="uds:/tmp/cc-socks/12345.sock">本文`,
		`引用: <cross-session-message from="uds:/tmp/cc-socks/12345.sock">本文</cross-session-message>`,
		`<cross-session-message from="../../secret">本文</cross-session-message>`,
		`<cross-session-message from="uds:/tmp/cc-socks/12345.sock"></cross-session-message>`,
	} {
		if got := crossSessionFrame(text); got != nil {
			t.Errorf("%q => %+v", text, got)
		}
	}
	if got := crossSessionFrame(`<cross-session-message from="uds:/tmp/cc-socks/12345.sock" from-name="A &amp; B">本文</cross-session-message>`); got == nil || got.Name != "A & B" {
		t.Fatalf("XML属性 = %+v", got)
	}
}

func Test送信元は現在の登録から解決しPID再利用や削除で誤ったリンクを残さない(t *testing.T) {
	p, _, write := cacheProvider(t)
	raw, err := os.ReadFile(filepath.Join(testdata, "cross_session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	write(raw)
	peerPath := filepath.Join(p.configDir, "projects", "p", "sender-session.jsonl")
	if err := os.WriteFile(peerPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(p.configDir, "sessions", "12345.json")
	if err := os.MkdirAll(filepath.Dir(registry), 0700); err != nil {
		t.Fatal(err)
	}
	writeRegistry := func(socket, name string, started time.Time) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"sessionId": "sender-session", "messagingSocketPath": socket, "name": name, "startedAt": started.UnixMilli()})
		if err := os.WriteFile(registry, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(want string) []model.Message {
		t.Helper()
		msgs, err := p.Messages(context.Background(), cacheSession, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := msgs[1].Blocks[0].SessionID; got != want {
			t.Fatalf("sender = %q, want %q", got, want)
		}
		return msgs
	}
	before := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	read("")
	writeRegistry("/tmp/cc-socks/12345.sock", "reviewer-a", before)
	linked := read("claude:sender-session")
	writeRegistry("/tmp/cc-socks/12345.sock", "reviewer-a", before.Add(2*time.Hour))
	read("")
	writeRegistry("/tmp/cc-socks/12345.sock", "another-peer", before)
	read("")
	writeRegistry("/tmp/other/12345.sock", "reviewer-a", before)
	read("")
	if err := os.Remove(registry); err != nil {
		t.Fatal(err)
	}
	read("")
	if linked[1].Blocks[0].SessionID != "claude:sender-session" {
		t.Fatal("共有した結果が後続の読み取りで変更された")
	}
}
