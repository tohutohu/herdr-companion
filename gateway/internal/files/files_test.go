package files

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) (root, outside string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "project")
	outside = filepath.Join(base, "other-project")
	for _, d := range []string{root + "/src", root + "/.ssh", outside} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".ssh", "id_rsa"), []byte("secret"), 0o600)
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644)
	os.WriteFile(filepath.Join(root, "logo.png"), []byte("\x89PNG\r\n\x1a\n\x00\x00"), 0o644)
	os.Symlink(outside, filepath.Join(root, "link-out"))
	os.Symlink(filepath.Join(root, "src"), filepath.Join(root, "link-in"))
	return root, outside
}

func Testワークスペース配下のファイルは読める(t *testing.T) {
	root, _ := setup(t)
	for _, p := range []string{"src/main.go", "./src/../src/main.go", filepath.Join(root, "src/main.go"), "link-in/main.go"} {
		f, _, ctype, err := Open([]string{root}, p, MaxFileSize)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		b, _ := io.ReadAll(f)
		f.Close()
		if string(b) != "package main\n" || !strings.HasPrefix(ctype, "text/plain") {
			t.Errorf("%s: content %q type %q", p, b, ctype)
		}
	}
	_, _, ctype, err := Open([]string{root}, "logo.png", MaxFileSize)
	if err != nil || ctype != "image/png" {
		t.Errorf("png: %q %v", ctype, err)
	}
}

func Testワークスペース外やシンボリックリンク経由のアクセスは拒否される(t *testing.T) {
	root, outside := setup(t)
	cases := map[string]error{
		"../other-project/secret.txt":            ErrForbidden,
		filepath.Join(outside, "secret.txt"):     ErrForbidden,
		"link-out/secret.txt":                    ErrForbidden,
		"/etc/hosts":                             ErrForbidden,
		".ssh/id_rsa":                            ErrForbidden,
		"src/missing.go":                         ErrNotFound,
		"src/main.go\x00.png":                    ErrForbidden,
		strings.Repeat("../", 20) + "etc/passwd": ErrForbidden,
	}
	for p, want := range cases {
		if _, err := Resolve([]string{root}, p); !errors.Is(err, want) {
			t.Errorf("%q: err = %v, want %v", p, err, want)
		}
	}
	if _, err := Resolve(nil, "src/main.go"); !errors.Is(err, ErrNoRoot) {
		t.Errorf("no root: %v", err)
	}
}

func Test追加ルートのファイルも読める(t *testing.T) {
	root, outside := setup(t)
	if _, err := Resolve([]string{root, outside}, filepath.Join(outside, "secret.txt")); err != nil {
		t.Errorf("extra root: %v", err)
	}
}

func Testディレクトリ一覧は機密ディレクトリを隠す(t *testing.T) {
	root, _ := setup(t)
	entries, err := List([]string{root}, "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	got := strings.Join(names, ",")
	if strings.Contains(got, ".ssh") || !strings.Contains(got, "src") {
		t.Errorf("entries = %s", got)
	}
	if entries[0].Name != "link-in" && !entries[0].IsDir {
		t.Errorf("directories should come first: %+v", entries)
	}
}
