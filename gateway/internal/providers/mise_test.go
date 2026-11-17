package providers

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fakeMise points miseBinary at a shell script for the test.
func fakeMise(t *testing.T, script string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "mise")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := miseBinary
	miseBinary = func() string { return bin }
	t.Cleanup(func() { miseBinary = old })
}

func miseDir(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("[tools]\nnode = \"24\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestMiseの設定があるフォルダではツールのPATHと変数を足す(t *testing.T) {
	fakeMise(t, `[ "$1 $2" = "env --json" ] || exit 9
printf '{"PATH":"/mise/node24/bin:/mise/go/bin:%s:/bin","GOROOT":"/mise/go","FOO":"bar","SEEN_DIR":"%s","SEEN_DIFF":"%s","HC_SESSION_ID":"evil","bad name":"x"}' "$PATH" "$(pwd -P)" "$__MISE_DIFF"`)
	dir := miseDir(t, "mise.toml")
	env := ChildEnvIn(context.Background(), dir,
		[]string{"PATH=/mise/go/bin:/bin", "HOME=/h", "__MISE_DIFF=old", "MISE_SHELL=zsh"},
		map[string]string{"HC_SESSION_ID": "claude:new"})
	real, _ := filepath.EvalSymlinks(dir)
	want := []string{
		"FOO=bar", "GOROOT=/mise/go", "HC_SESSION_ID=claude:new", "HOME=/h", "MISE_SHELL=zsh",
		"PATH=/mise/node24/bin:/mise/go/bin:/bin", "SEEN_DIFF=", "SEEN_DIR=" + real, "__MISE_DIFF=old",
	}
	if !slices.Equal(env, want) {
		t.Errorf("env =\n%v\nwant\n%v", env, want)
	}
}

func TestMiseの設定がなければmiseを起動しない(t *testing.T) {
	fakeMise(t, `echo '{"FOO":"bar"}'`)
	if d := LoadMiseEnv(context.Background(), t.TempDir(), []string{"PATH=/bin"}); !d.Empty() {
		t.Errorf("loaded %v without a mise config", d)
	}
}

func Test信頼されていないmiseの設定は読み込まずそのまま始める(t *testing.T) {
	fakeMise(t, `echo 'mise ERROR Config files in /x/mise.toml are not trusted.' >&2; exit 1`)
	if d := LoadMiseEnv(context.Background(), miseDir(t, ".tool-versions"), []string{"PATH=/bin"}); !d.Empty() {
		t.Errorf("loaded %v from an untrusted config", d)
	}
}

func TestPATHを変えないmiseの出力ではPATHを設定しない(t *testing.T) {
	fakeMise(t, `printf '{"PATH":"%s:/bin"}' "$PATH"`)
	if d := LoadMiseEnv(context.Background(), miseDir(t, ".mise.toml"), []string{"PATH=/bin"}); !d.Empty() {
		t.Errorf("set = %v, want nothing", d.Set)
	}
}

func TestDirenvはmiseを反映した環境で実行され後勝ちになる(t *testing.T) {
	fakeMise(t, `echo '{"PATH":"/mise/bin:/bin","SHARED":"mise","GONE":"mise"}'`)
	fakeDirenv(t, `printf '{"PATH":"/proj/bin:%s","SHARED":"direnv","GONE":null}' "$PATH"`)
	dir := miseDir(t, "mise.toml")
	if err := os.WriteFile(filepath.Join(dir, ".envrc"), []byte("PATH_add bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := ChildEnvIn(context.Background(), dir, []string{"PATH=/bin"}, nil)
	want := []string{"PATH=/proj/bin:/mise/bin:/bin", "SHARED=direnv"}
	if !slices.Equal(env, want) {
		t.Errorf("env = %v, want %v", env, want)
	}
}
