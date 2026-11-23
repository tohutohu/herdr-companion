package favorites

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "favorites.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func intp(n int) *int { return &n }

func Testファイルがなければsavedがfalseで空(t *testing.T) {
	s, _ := open(t)
	got := s.List()
	if got.Saved || len(got.Paths) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func Test追加と削除が順序を保って保存され再読込で戻る(t *testing.T) {
	s, path := open(t)
	for _, p := range []string{"/w/a", "/w/b", "/w/c/"} {
		if _, err := s.Add(p, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Add("/w/b", nil); err != nil { // already there: stays in place
		t.Fatal(err)
	}
	if _, err := s.Add("/w/d", intp(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remove("/w/a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remove("/w/none"); err != nil {
		t.Fatal(err)
	}
	want := []string{"/w/d", "/w/b", "/w/c"}
	if got := s.List(); !got.Saved || !reflect.DeepEqual(got.Paths, want) {
		t.Fatalf("got %+v", got)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.List(); !got.Saved || !reflect.DeepEqual(got.Paths, want) {
		t.Fatalf("reloaded %+v", got)
	}
}

func Test位置を指定した追加は既存の項目を移動する(t *testing.T) {
	s, _ := open(t)
	s.Set([]string{"/a", "/b", "/c"})
	got, err := s.Add("/c", intp(0))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/c", "/a", "/b"}; !reflect.DeepEqual(got.Paths, want) {
		t.Fatalf("got %v", got.Paths)
	}
	got, _ = s.Add("/c", intp(99))
	if want := []string{"/a", "/b", "/c"}; !reflect.DeepEqual(got.Paths, want) {
		t.Fatalf("got %v", got.Paths)
	}
}

func Test取り込みは一度も保存されていないときだけ効く(t *testing.T) {
	s, _ := open(t)
	got, imported, err := s.Import([]string{"/mac/a", "/mac/b", "/mac/a"})
	if err != nil || !imported {
		t.Fatalf("first import: %v %v", imported, err)
	}
	if want := []string{"/mac/a", "/mac/b"}; !reflect.DeepEqual(got.Paths, want) {
		t.Fatalf("got %v", got.Paths)
	}
	got, imported, err = s.Import([]string{"/phone/x"})
	if err != nil || imported {
		t.Fatalf("second import: %v %v", imported, err)
	}
	if want := []string{"/mac/a", "/mac/b"}; !reflect.DeepEqual(got.Paths, want) {
		t.Fatalf("got %v", got.Paths)
	}
}

func Test空にしたあとは取り込みで上書きされない(t *testing.T) {
	s, _ := open(t)
	if _, err := s.Set(nil); err != nil {
		t.Fatal(err)
	}
	if _, imported, _ := s.Import([]string{"/a"}); imported {
		t.Fatal("an emptied list was replaced by an import")
	}
}

func Test相対パスや上限を超える一覧は拒否する(t *testing.T) {
	s, _ := open(t)
	if _, err := s.Add("relative/dir", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("relative: %v", err)
	}
	many := make([]string, MaxCount+1)
	for i := range many {
		many[i] = filepath.Join("/w", string(rune('a'+i%26)), string(rune('a'+i/26)))
	}
	if _, err := s.Set(many); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too many: %v", err)
	}
	if s.List().Saved {
		t.Fatal("a refused change was saved")
	}
}

func Testプロジェクトの名前と通知を保存し一覧から外すと設定も消える(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Set([]string{"/w/bot", "/w/klon"})
	name, off := "Bot 群", NotifyOff
	if p, err := st.Update("/w/bot", &name, &off); err != nil || p.Name != "Bot 群" || p.Notify != "off" {
		t.Fatal(p, err)
	}
	bad := "loud"
	if _, err := st.Update("/w/bot", nil, &bad); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid level accepted:", err)
	}
	if _, err := st.Update("/w/none", &name, nil); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown folder accepted:", err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := again.Projects()
	if len(got) != 2 || got[0] != (Project{Path: "/w/bot", Name: "Bot 群", Notify: "off"}) || got[1] != (Project{Path: "/w/klon", Name: "klon", Notify: "all"}) {
		t.Fatalf("projects = %+v", got)
	}
	// Reordering keeps the settings; removing drops them.
	again.Set([]string{"/w/klon", "/w/bot"})
	if again.Projects()[1].Notify != "off" {
		t.Fatal("reorder lost the settings")
	}
	again.Remove("/w/bot")
	again.Add("/w/bot", nil)
	if p := again.Projects()[1]; p.Notify != "all" || p.Name != "bot" {
		t.Fatalf("settings came back: %+v", p)
	}
	// Back to the defaults leaves nothing stored.
	all, empty := NotifyAll, ""
	again.Update("/w/klon", &name, &off)
	again.Update("/w/klon", &empty, &all)
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), `"projects"`) {
		t.Fatalf("defaults stored: %s", b)
	}
}
