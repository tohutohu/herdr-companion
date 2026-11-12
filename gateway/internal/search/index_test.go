package search

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

type fakeSource struct {
	mu       sync.Mutex
	active   []model.Session
	archived []model.Session
	messages map[string][]model.Message
	failing  map[string]error
	reads    map[string]int
	// gate, when set, holds every conversation read until it is closed.
	gate chan struct{}
}

func (f *fakeSource) List(context.Context) ([]model.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.Session{}, f.active...), nil
}

func (f *fakeSource) ArchivedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for _, s := range f.archived {
		ids = append(ids, s.ID)
	}
	return ids
}

func (f *fakeSource) Conversation(_ context.Context, id string) (model.Session, []model.Message, error) {
	f.mu.Lock()
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reads == nil {
		f.reads = map[string]int{}
	}
	f.reads[id]++
	if err := f.failing[id]; err != nil {
		return model.Session{}, nil, err
	}
	for _, s := range append(append([]model.Session{}, f.active...), f.archived...) {
		if s.ID == id {
			return s, f.messages[id], nil
		}
	}
	return model.Session{}, nil, providers.ErrNotFound
}

func (f *fakeSource) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *fakeSource) readCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads[id]
}

func text(role model.Role, s string) model.Message {
	return model.Message{Role: role, Blocks: []model.Block{model.TextBlock(s)}}
}

func newSource() *fakeSource {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	return &fakeSource{
		active: []model.Session{
			{ID: "claude:a", Provider: "claude", Project: "shop", Title: "Login bug", Status: model.StatusIdle, UpdatedAt: now.Add(-time.Hour)},
			{ID: "codex:b", Provider: "codex", Project: "api", Title: "リファクタ", Cwd: "/w/api", LastMessage: "決済のテストを直しました", Status: model.StatusOffline, UpdatedAt: now.Add(-2 * time.Hour)},
		},
		archived: []model.Session{
			{ID: "claude:c", Provider: "claude", Project: "shop", Title: "古い login 調査", Status: model.StatusOffline, UpdatedAt: now.Add(-72 * time.Hour)},
			{ID: "claude:d", Provider: "claude", Project: "notes", Title: "メモ", Status: model.StatusOffline, UpdatedAt: now.Add(-96 * time.Hour)},
		},
		messages: map[string][]model.Message{
			"claude:a": {text(model.RoleUser, "100% 再現する_バグ")},
			"claude:d": {
				text(model.RoleUser, "買い物リストを作って"),
				text(model.RoleAssistant, "前置きです。\nPasskey の移行手順をまとめました。"),
			},
		},
	}
}

func openIndex(t *testing.T, src Source, path string) *Index {
	t.Helper()
	x, err := Open(context.Background(), path, src)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	return x
}

func search(t *testing.T, x *Index, q Query) Result {
	t.Helper()
	// Tests change the source between searches.
	x.mu.Lock()
	x.fresh = time.Time{}
	x.mu.Unlock()
	res, err := x.Search(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func ids(res Result) []string {
	out := []string{}
	for _, h := range res.Hits {
		out = append(out, h.ID)
	}
	return out
}

func TestSearchはアーカイブ済みも含めて新しい順に返す(t *testing.T) {
	x := openIndex(t, newSource(), filepath.Join(t.TempDir(), "search.db"))
	res := search(t, x, Query{Terms: ParseQuery("LOGIN")})
	if got := ids(res); !reflect.DeepEqual(got, []string{"claude:a", "claude:c"}) {
		t.Fatalf("got %v", got)
	}
	if res.Hits[0].Archived || !res.Hits[1].Archived {
		t.Errorf("archived は claude:c だけのはず: %+v", res.Hits)
	}
	if res.Indexing {
		t.Error("読み終えているので indexing は false のはず")
	}
}

func TestSearchはすべての語を含むセッションだけを返す(t *testing.T) {
	x := openIndex(t, newSource(), filepath.Join(t.TempDir(), "search.db"))
	if got := ids(search(t, x, Query{Terms: ParseQuery("login 古い")})); !reflect.DeepEqual(got, []string{"claude:c"}) {
		t.Errorf("got %v", got)
	}
	if got := ids(search(t, x, Query{Terms: ParseQuery(`"login 調査"`)})); !reflect.DeepEqual(got, []string{"claude:c"}) {
		t.Errorf("フレーズ: got %v", got)
	}
	if got := ids(search(t, x, Query{Terms: ParseQuery(`"調査 login"`)})); len(got) != 0 {
		t.Errorf("語順の違うフレーズは一致しないはず: got %v", got)
	}
}

func TestSearchは範囲で絞る(t *testing.T) {
	x := openIndex(t, newSource(), filepath.Join(t.TempDir(), "search.db"))
	if got := ids(search(t, x, Query{Terms: []string{"login"}, Scope: ScopeActive})); !reflect.DeepEqual(got, []string{"claude:a"}) {
		t.Errorf("active: got %v", got)
	}
	if got := ids(search(t, x, Query{Terms: []string{"login"}, Scope: ScopeArchived})); !reflect.DeepEqual(got, []string{"claude:c"}) {
		t.Errorf("archived: got %v", got)
	}
}

func TestSearchは本文をText指定のときだけ探し抜粋を返す(t *testing.T) {
	x := openIndex(t, newSource(), filepath.Join(t.TempDir(), "search.db"))
	if got := ids(search(t, x, Query{Terms: []string{"passkey"}})); len(got) != 0 {
		t.Fatalf("本文は既定では探さない: got %v", got)
	}
	res := search(t, x, Query{Terms: []string{"passkey"}, Text: true})
	if got := ids(res); !reflect.DeepEqual(got, []string{"claude:d"}) {
		t.Fatalf("got %v", got)
	}
	want := &Match{Field: "assistant", Snippet: "前置きです。 Passkey の移行手順をまとめました。"}
	if !reflect.DeepEqual(res.Hits[0].Match, want) {
		t.Errorf("match = %+v, want %+v", res.Hits[0].Match, want)
	}
}

func TestSearchは一覧に出ない項目の一致に抜粋を付ける(t *testing.T) {
	x := openIndex(t, newSource(), filepath.Join(t.TempDir(), "search.db"))
	res := search(t, x, Query{Terms: []string{"決済"}})
	if len(res.Hits) != 1 || res.Hits[0].Match == nil || res.Hits[0].Match.Field != "lastMessage" {
		t.Fatalf("lastMessage の抜粋が付くはず: %+v", res.Hits)
	}
	if res := search(t, x, Query{Terms: []string{"login"}}); res.Hits[0].Match != nil {
		t.Errorf("タイトルの一致に抜粋は要らない: %+v", res.Hits[0].Match)
	}
}

func TestSearchはLIKEの記号を文字として扱う(t *testing.T) {
	x := openIndex(t, newSource(), filepath.Join(t.TempDir(), "search.db"))
	if got := ids(search(t, x, Query{Terms: []string{"100%"}, Text: true})); !reflect.DeepEqual(got, []string{"claude:a"}) {
		t.Errorf("%%: got %v", got)
	}
	if got := ids(search(t, x, Query{Terms: []string{"る_バ"}, Text: true})); !reflect.DeepEqual(got, []string{"claude:a"}) {
		t.Errorf("_: got %v", got)
	}
	if got := ids(search(t, x, Query{Terms: []string{"_"}})); len(got) != 0 {
		t.Errorf("_ は任意の 1 文字ではない: got %v", got)
	}
}

func TestSearchはLimitを超えた件数を返す(t *testing.T) {
	x := openIndex(t, newSource(), filepath.Join(t.TempDir(), "search.db"))
	res := search(t, x, Query{Terms: []string{"login"}, Limit: 1})
	if got := ids(res); !reflect.DeepEqual(got, []string{"claude:a"}) || res.More != 1 {
		t.Errorf("got %v, more %d", got, res.More)
	}
}

func TestIndexはアーカイブ済みの会話を読み直さない(t *testing.T) {
	src := newSource()
	path := filepath.Join(t.TempDir(), "search.db")
	x := openIndex(t, src, path)
	search(t, x, Query{Terms: []string{"login"}})
	search(t, x, Query{Terms: []string{"login"}})
	if n := src.readCount("claude:d"); n != 1 {
		t.Errorf("claude:d を %d 回読んだ。1 回のはず", n)
	}
	if n := src.readCount("claude:a"); n != 1 {
		t.Errorf("更新のない claude:a を %d 回読んだ。1 回のはず", n)
	}
	x.Close()

	// A restarted gateway keeps what it read.
	y := openIndex(t, src, path)
	if got := ids(search(t, y, Query{Terms: []string{"passkey"}, Text: true})); !reflect.DeepEqual(got, []string{"claude:d"}) {
		t.Errorf("再起動後: got %v", got)
	}
	if n := src.readCount("claude:d"); n != 1 {
		t.Errorf("再起動後に claude:d を読み直した（%d 回）", n)
	}
}

func TestIndexは更新されたセッションを読み直す(t *testing.T) {
	src := newSource()
	x := openIndex(t, src, filepath.Join(t.TempDir(), "search.db"))
	search(t, x, Query{Terms: []string{"login"}})
	src.set(func() {
		src.active[0].UpdatedAt = src.active[0].UpdatedAt.Add(time.Minute)
		src.active[0].Status = model.StatusRunning
		src.messages["claude:a"] = append(src.messages["claude:a"], text(model.RoleAssistant, "原因は webhook でした"))
	})
	res := search(t, x, Query{Terms: []string{"webhook"}, Text: true})
	if got := ids(res); !reflect.DeepEqual(got, []string{"claude:a"}) || res.Hits[0].Status != model.StatusRunning {
		t.Errorf("新しい発言と状態が反映されるはず: %+v", res.Hits)
	}
}

func TestIndexは会話が同じでも一覧の変化を反映する(t *testing.T) {
	src := newSource()
	x := openIndex(t, src, filepath.Join(t.TempDir(), "search.db"))
	search(t, x, Query{Terms: []string{"login"}})
	src.set(func() { src.active[0].Title = "Signup bug" })
	if got := ids(search(t, x, Query{Terms: []string{"signup"}})); !reflect.DeepEqual(got, []string{"claude:a"}) {
		t.Errorf("got %v", got)
	}
	if n := src.readCount("claude:a"); n != 1 {
		t.Errorf("会話は読み直さないはず（%d 回）", n)
	}
}

func TestIndexはアーカイブと一覧から消えたセッションに追従する(t *testing.T) {
	src := newSource()
	x := openIndex(t, src, filepath.Join(t.TempDir(), "search.db"))
	search(t, x, Query{Terms: []string{"login"}})
	src.set(func() {
		src.archived = append(src.archived, src.active[0])
		src.active = src.active[1:]
	})
	res := search(t, x, Query{Terms: []string{"login"}, Scope: ScopeArchived})
	if got := ids(res); !reflect.DeepEqual(got, []string{"claude:a", "claude:c"}) {
		t.Errorf("アーカイブ後: got %v", got)
	}
	src.set(func() { src.active = nil })
	if got := ids(search(t, x, Query{Terms: []string{"決済"}})); len(got) != 0 {
		t.Errorf("一覧から消えたセッションは出ない: got %v", got)
	}
}

func TestIndexはエージェントが持たないセッションを出さない(t *testing.T) {
	src := newSource()
	src.failing = map[string]error{"claude:c": providers.ErrNotFound, "claude:d": errors.New("app-server down")}
	x := openIndex(t, src, filepath.Join(t.TempDir(), "search.db"))
	if got := ids(search(t, x, Query{Terms: []string{"login"}})); !reflect.DeepEqual(got, []string{"claude:a"}) {
		t.Errorf("got %v", got)
	}
	search(t, x, Query{Terms: []string{"login"}})
	if n := src.readCount("claude:c"); n != 1 {
		t.Errorf("無いセッションを %d 回読んだ。1 回のはず", n)
	}
	if n := src.readCount("claude:d"); n != 1 {
		t.Errorf("読めなかったセッションをすぐ読み直した（%d 回）", n)
	}
}

func TestSearchは読み込みが長いとき途中の結果で答える(t *testing.T) {
	src := newSource()
	src.gate = make(chan struct{})
	x := openIndex(t, src, filepath.Join(t.TempDir(), "search.db"))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := x.Search(ctx, Query{Terms: []string{"login"}}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("待ちは呼び出し側の期限で終わるはず: %v", err)
	}
	close(src.gate)
	<-x.Refresh()
	if got := ids(search(t, x, Query{Terms: []string{"login"}})); len(got) != 2 {
		t.Errorf("読み終えたあと: got %v", got)
	}
}

func TestParseQueryは語と引用符のフレーズに分ける(t *testing.T) {
	got := ParseQuery(`Login  "Fix the Bug" 検索　全角`)
	want := []string{"login", "fix the bug", "検索", "全角"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := ParseQuery(`  ""  `); len(got) != 0 {
		t.Errorf("空: got %q", got)
	}
}

func TestSnippetは一致箇所の前後を切り出す(t *testing.T) {
	long := ""
	for i := 0; i < 50; i++ {
		long += "あ"
	}
	tail := ""
	for i := 0; i < 200; i++ {
		tail += "い"
	}
	got := []rune(snippet(long+"needle"+tail, "needle"))
	if string(got[:37]) != "…"+long[:30*3]+"needle" {
		t.Errorf("前は 30 文字まで: %q", string(got))
	}
	if len(got) != 102 || got[len(got)-1] != '…' {
		t.Errorf("全体は 100 文字と前後の …: %d 文字", len(got))
	}
}
