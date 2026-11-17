package shellenv

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fakeShell writes a stand-in login shell: it ignores -i -l -c, prints
// noise like a chatty .zshrc, sets what startup files would, and runs the
// command it was given.
func fakeShell(t *testing.T, rc string) string {
	t.Helper()
	sh := filepath.Join(t.TempDir(), "zsh")
	script := "#!/bin/sh\nshift 3\necho 'welcome!'\n" + rc + "\neval \"$1\"\n"
	if err := os.WriteFile(sh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return sh
}

func Testログインシェルの起動ファイルが設定した環境を読む(t *testing.T) {
	sh := fakeShell(t, `export PATH="/mise/node/bin:$HOME/go/bin:$PATH" GOPATH="$HOME/go" SEEN_RESOLVING="$HERDR_RESOLVING_ENVIRONMENT" SEEN_DIR="$(pwd -P)"
export MULTI='a
b'`)
	home := t.TempDir()
	env, err := Read(context.Background(), sh, home, []string{"PATH=/usr/bin:/bin", "HOME=" + home})
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(home)
	if env["PATH"] != "/mise/node/bin:"+home+"/go/bin:/usr/bin:/bin" || env["GOPATH"] != home+"/go" {
		t.Errorf("PATH=%q GOPATH=%q", env["PATH"], env["GOPATH"])
	}
	if env["SEEN_RESOLVING"] != "1" || env["SEEN_DIR"] != real || env["MULTI"] != "a\nb" {
		t.Errorf("resolving=%q dir=%q multi=%q", env["SEEN_RESOLVING"], env["SEEN_DIR"], env["MULTI"])
	}
}

func Testシェルが失敗したり環境を出さなければエラーにする(t *testing.T) {
	if _, err := Read(context.Background(), fakeShell(t, "exit 1"), t.TempDir(), nil); err == nil {
		t.Error("a shell that exits before printing must fail")
	}
	if _, err := Read(context.Background(), "zsh", t.TempDir(), nil); err == nil {
		t.Error("a relative shell path must fail")
	}
}

func Test取り込みはシェルを優先しPATHはGatewayの残りを後ろに足しGateway固有の変数は守る(t *testing.T) {
	environ := []string{
		"PATH=/Users/u/.local/bin:/opt/homebrew/bin:/usr/bin:/bin",
		"HOME=/Users/u", "HERDR_MOBILE_CONFIG=/c.json", "LANG=C",
	}
	shell := map[string]string{
		"PATH":                "/mise/bin:/Users/u/go/bin:/opt/homebrew/bin:/usr/bin:/bin",
		"GOPATH":              "/Users/u/go",
		"LANG":                "ja_JP.UTF-8",
		"HOME":                "/elsewhere",
		"PWD":                 "/Users/u",
		"SHLVL":               "2",
		"HERDR_MOBILE_CONFIG": "/other.json",
		"__MISE_DIFF":         "x",
		"MISE_SHELL":          "zsh",
		"DIRENV_DIR":          "-/x",
		"CLAUDECODE":          "1",
		"TERM_PROGRAM":        "iTerm.app",
	}
	got := Merge(environ, shell)
	slices.Sort(got)
	want := []string{
		"GOPATH=/Users/u/go", "HERDR_MOBILE_CONFIG=/c.json", "HOME=/Users/u", "LANG=ja_JP.UTF-8",
		"PATH=/mise/bin:/Users/u/go/bin:/opt/homebrew/bin:/usr/bin:/bin:/Users/u/.local/bin",
	}
	if !slices.Equal(got, want) {
		t.Errorf("merge =\n%v\nwant\n%v", got, want)
	}
}
