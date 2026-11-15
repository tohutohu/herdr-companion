package favorites

import (
	"errors"
	"path/filepath"
	"reflect"
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
