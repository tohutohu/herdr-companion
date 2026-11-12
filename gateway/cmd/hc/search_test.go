package main

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/search"
)

func searchGateway() *fakeGateway {
	now := time.Now()
	return &fakeGateway{
		sessions: []model.Session{
			{ID: "claude:aaaa1111-0000", Provider: "claude", Project: "shop", Title: "Login bug", Status: model.StatusIdle, UpdatedAt: now.Add(-time.Hour)},
			{ID: "codex:bbbb2222-0000", Provider: "codex", Project: "api", Title: "リファクタ", Cwd: "/w/api", LastMessage: "決済のテストを直しました", Status: model.StatusOffline, UpdatedAt: now.Add(-2 * time.Hour)},
		},
		archived: []model.Session{
			{ID: "claude:cccc3333-0000", Provider: "claude", Project: "shop", Title: "古い login 調査", Status: model.StatusOffline, UpdatedAt: now.Add(-72 * time.Hour)},
			{ID: "claude:dddd4444-0000", Provider: "claude", Project: "notes", Title: "メモ", Status: model.StatusOffline, UpdatedAt: now.Add(-96 * time.Hour)},
		},
		messages: map[string][]model.Message{
			"claude:dddd4444-0000": {
				{ID: "m1", Role: model.RoleUser, Blocks: []model.Block{model.TextBlock("買い物リストを作って")}},
				{ID: "m2", Role: model.RoleAssistant, Blocks: []model.Block{model.TextBlock("前置きです。\nPasskey の移行手順をまとめました。")}},
			},
		},
	}
}

func TestSearchは検索のないGatewayでもアーカイブ済みを含めて新しい順に出す(t *testing.T) {
	a, _ := newTestApp(t, searchGateway())
	out := a.mustRun(t, "search", "LOGIN")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("2 行のはず:\n%s", out)
	}
	if !strings.Contains(lines[0], "aaaa1111") || strings.Contains(lines[0], "(archived)") {
		t.Errorf("1 行目は実行中のセッション: %q", lines[0])
	}
	if !strings.Contains(lines[1], "cccc3333") || !strings.HasSuffix(lines[1], "(archived)") {
		t.Errorf("2 行目はアーカイブ済み: %q", lines[1])
	}
}

func TestSearchはすべての語を含むセッションだけを出す(t *testing.T) {
	a, _ := newTestApp(t, searchGateway())
	out := a.mustRun(t, "search", "login", "古い")
	if !strings.Contains(out, "cccc3333") || strings.Contains(out, "aaaa1111") {
		t.Errorf("両方の語を含むのは cccc3333 だけ:\n%s", out)
	}
}

func TestSearchは行に出ない項目で見つけたら抜粋を添える(t *testing.T) {
	a, _ := newTestApp(t, searchGateway())
	out := a.mustRun(t, "search", "決済")
	if !strings.Contains(out, "bbbb2222") || !strings.Contains(out, "lastMessage: 決済のテストを直しました") {
		t.Errorf("最後のメッセージの抜粋が出るはず:\n%s", out)
	}
}

func TestSearchは絞り込みで片方だけを見る(t *testing.T) {
	a, _ := newTestApp(t, searchGateway())
	if out := a.mustRun(t, "search", "login", "--archived"); strings.Contains(out, "aaaa1111") || !strings.Contains(out, "cccc3333") {
		t.Errorf("--archived:\n%s", out)
	}
	if out := a.mustRun(t, "search", "login", "--active"); !strings.Contains(out, "aaaa1111") || strings.Contains(out, "cccc3333") {
		t.Errorf("--active:\n%s", out)
	}
}

func TestSearchは本文をtext指定のときだけ探す(t *testing.T) {
	a, _ := newTestApp(t, searchGateway())
	if out := a.mustRun(t, "search", "passkey"); !strings.Contains(out, "no session matches") || !strings.Contains(out, "--text") {
		t.Errorf("本文は既定では探さず、--text を案内する:\n%s", out)
	}
	out := a.mustRun(t, "search", "passkey", "--text")
	if !strings.Contains(out, "dddd4444") || !strings.Contains(out, "(archived)") {
		t.Errorf("アーカイブ済みの本文から見つかるはず:\n%s", out)
	}
	if !strings.Contains(out, "assistant: 前置きです。 Passkey の移行手順をまとめました。") {
		t.Errorf("本文の抜粋が出るはず:\n%s", out)
	}
}

func TestSearchはlimitを超えた件数を知らせる(t *testing.T) {
	a, _ := newTestApp(t, searchGateway())
	out := a.mustRun(t, "search", "login", "--limit", "1")
	if !strings.Contains(out, "aaaa1111") || strings.Contains(out, "cccc3333") || !strings.Contains(out, "(+1 more: --limit 0)") {
		t.Errorf("新しい 1 件と残りの件数:\n%s", out)
	}
}

func TestSearchのJSONは一致箇所を含む(t *testing.T) {
	a, _ := newTestApp(t, searchGateway())
	out := a.mustRun(t, "--json", "search", "passkey", "--text")
	var res struct {
		Sessions []struct {
			ID       string
			Short    string
			Archived bool
			Match    *search.Match
		}
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(res.Sessions) != 1 || res.Sessions[0].ID != "claude:dddd4444-0000" || !res.Sessions[0].Archived ||
		res.Sessions[0].Match == nil || res.Sessions[0].Match.Field != "assistant" {
		t.Errorf("想定外の JSON:\n%s", out)
	}
}

func TestSearchは語がなければ使い方の誤りにする(t *testing.T) {
	a, _ := newTestApp(t, searchGateway())
	if code := a.run([]string{"search"}); code != 2 {
		t.Errorf("exit %d, 2 のはず", code)
	}
}

func TestSearchはGatewayの検索を使う(t *testing.T) {
	g := searchGateway()
	var asked url.Values
	g.search = func(q url.Values) any {
		asked = q
		hit := g.archived[1]
		hit.Archived = true
		return map[string]any{
			"sessions": []map[string]any{{"id": hit.ID, "provider": hit.Provider, "project": hit.Project, "title": hit.Title,
				"status": hit.Status, "updatedAt": hit.UpdatedAt, "archived": true,
				"match": map[string]string{"field": "assistant", "snippet": "Passkey の移行手順"}}},
			"more": 3, "indexing": true,
		}
	}
	a, _ := newTestApp(t, g)
	out := a.mustRun(t, "search", "passkey", "移行 手順", "--text", "--archived", "--limit", "1")
	if got := asked["q"]; len(got) != 2 || got[0] != "passkey" || got[1] != `"移行 手順"` {
		t.Errorf("q = %q。引用符で囲んだ引数はフレーズのまま送る", got)
	}
	if asked.Get("text") != "true" || asked.Get("scope") != "archived" || asked.Get("limit") != "1" {
		t.Errorf("問い合わせ: %v", asked)
	}
	for _, want := range []string{"dddd4444", "(archived)", "assistant: Passkey の移行手順", "(+3 more: --limit 0)", "still reading sessions"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q が出るはず:\n%s", want, out)
		}
	}
}

func TestSearchは検索のないGatewayでもフレーズを探す(t *testing.T) {
	a, _ := newTestApp(t, searchGateway())
	if out := a.mustRun(t, "search", "login 調査"); !strings.Contains(out, "cccc3333") || strings.Contains(out, "aaaa1111") {
		t.Errorf("フレーズは cccc3333 だけ:\n%s", out)
	}
	if out := a.mustRun(t, "search", "調査 login"); !strings.Contains(out, "no session matches") {
		t.Errorf("語順の違うフレーズは一致しない:\n%s", out)
	}
}
