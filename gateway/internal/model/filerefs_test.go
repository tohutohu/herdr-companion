package model

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func Test長い出力は先頭の指定文字数だけを切り出す(t *testing.T) {
	for _, tc := range []struct {
		text string
		n    int
		want string
	}{
		{"abc", 3, "abc"},
		{"あいうえお", 3, "あいう\n… (truncated)"},
		{"abc", 0, "\n… (truncated)"},
		{"", 0, ""},
		{string([]byte{0xff, 0xff, 'x'}), 2, "��\n… (truncated)"},
		{strings.Repeat("あ", 1<<20), 3, "あああ\n… (truncated)"},
	} {
		if got := Truncate(tc.text, tc.n); got != tc.want {
			t.Errorf("truncate=%q, want %q", got, tc.want)
		}
	}
}

func BenchmarkTruncateLarge(b *testing.B) {
	text := strings.Repeat("tool output\n", 1<<17)
	b.ReportAllocs()
	for b.Loop() {
		_ = Truncate(text, 1500)
	}
}

func Testテキスト中の存在するファイル参照だけをリンクにする(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "src", "auth"), 0o755)
	os.WriteFile(filepath.Join(root, "src", "auth", "middleware.go"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# readme"), 0o644)

	text := "修正しました。src/auth/middleware.go:42 と `README.md` を参照。" +
		"存在しない nope.go や v1.2.3、https://example.com/a.go、../outside.go は無視。" +
		"絶対パス " + filepath.Join(root, "README.md") + " も可。src/auth/middleware.go:42 は重複。"
	got := ExtractFileRefs(text, root)
	want := []Block{
		{Type: BlockFile, Path: "src/auth/middleware.go", Line: 42, Size: 1},
		{Type: BlockFile, Path: "README.md", Size: 8},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func Test日本語を含むファイル参照をリンクにする(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		".local/monthly-report/reports/261101_月次業務報告_山田太郎.pdf",
		".local/monthly-report/invoices/34_請求書_山田太郎.pdf",
		"資料/報告書.pdf",
		"資料/か\u3099いよう.pdf",
		"résumé/报告书.pdf",
	}
	for _, p := range paths {
		abs := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("pdf"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		text string
		want []Block
	}{
		{
			name: "報告書と請求書のコード表記",
			text: "- **報告書:** `" + paths[0] + "`（2ページ）\n- **請求書:** `" + paths[1] + "`（34番）",
			want: []Block{{Type: BlockFile, Path: paths[0], Size: 3}, {Type: BlockFile, Path: paths[1], Size: 3}},
		},
		{
			name: "日本語の句読点と行番号",
			text: "作成しました。資料/報告書.pdf:12。確認してください。",
			want: []Block{{Type: BlockFile, Path: paths[2], Line: 12, Size: 3}},
		},
		{
			name: "日本語の作業フォルダを含む絶対パス",
			text: "[報告書](" + filepath.Join(root, paths[2]) + ")",
			want: []Block{{Type: BlockFile, Path: paths[2], Size: 3}},
		},
		{
			name: "結合文字を含むファイル名",
			text: "`" + paths[3] + "`",
			want: []Block{{Type: BlockFile, Path: paths[3], Size: 3}},
		},
		{
			name: "他の言語の文字も検出する",
			text: "`" + paths[4] + "`",
			want: []Block{{Type: BlockFile, Path: paths[4], Size: 3}},
		},
		{
			name: "存在しない日本語パスの一部を誤検出しない",
			text: "`別の" + paths[2] + "` `資料/未作成.pdf` `../資料/報告書.pdf`",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractFileRefs(tc.text, root); !slices.Equal(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestTruncateは長い文字列を切り詰める(t *testing.T) {
	if got := Truncate("あいうえお", 3); got != "あいう\n… (truncated)" {
		t.Errorf("got %q", got)
	}
	if got := Truncate("abc", 3); got != "abc" {
		t.Errorf("got %q", got)
	}
}
