package sessionmatch

import (
	"reflect"
	"testing"
)

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
