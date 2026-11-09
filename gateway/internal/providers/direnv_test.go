package providers

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeDirenv points direnvBinary at a shell script for the test.
func fakeDirenv(t *testing.T, script string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "direnv")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := direnvBinary
	direnvBinary = func() string { return bin }
	t.Cleanup(func() { direnvBinary = old })
}

func envrcDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".envrc"), []byte("export FOO=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestDirenvが読み込む変数を足し消す変数を外しGatewayの変数を優先する(t *testing.T) {
	fakeDirenv(t, `cat <<'EOF'
{"FOO":"bar","PATH":"/proj/bin:/bin","OLD":null,"HC_SESSION_ID":"claude:evil","CLAUDECODE":"1","DIRENV_DIR":"-/proj","bad name":"x"}
EOF
`)
	env := ChildEnvIn(context.Background(), envrcDir(t), []string{"PATH=/bin", "OLD=1", "HOME=/h"},
		map[string]string{"HC_SESSION_ID": "claude:new"})
	want := []string{"DIRENV_DIR=-/proj", "FOO=bar", "HC_SESSION_ID=claude:new", "HOME=/h", "PATH=/proj/bin:/bin"}
	if !slices.Equal(env, want) {
		t.Errorf("env = %v, want %v", env, want)
	}
}

func TestDirenvには前のdirenvの状態を渡さず作業フォルダで実行する(t *testing.T) {
	fakeDirenv(t, `printf '{"SEEN_DIR":"%s","SEEN_DIFF":"%s"}' "$(pwd -P)" "$DIRENV_DIFF"`)
	dir := envrcDir(t)
	d := LoadDirEnv(context.Background(), dir, []string{"DIRENV_DIFF=old", "DIRENV_DIR=-/elsewhere"})
	real, _ := filepath.EvalSymlinks(dir)
	if d.Set["SEEN_DIR"] != real || d.Set["SEEN_DIFF"] != "" {
		t.Errorf("set = %v, want SEEN_DIR=%s and no DIRENV_DIFF", d.Set, real)
	}
}

func Test許可されていないenvrcや失敗したdirenvでは環境を変えない(t *testing.T) {
	fakeDirenv(t, `echo '{"DIRENV_DIR":"-/x","FOO":"bar"}'; echo 'direnv: error /x/.envrc is blocked. Run direnv allow' >&2; exit 1`)
	env := ChildEnvIn(context.Background(), envrcDir(t), []string{"PATH=/bin"}, nil)
	if !slices.Equal(env, []string{"PATH=/bin"}) {
		t.Errorf("env = %v", env)
	}
}

func TestEnvrcがないフォルダではdirenvを実行しない(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	fakeDirenv(t, "touch '"+marker+"'; echo '{\"FOO\":\"bar\"}'")
	d := LoadDirEnv(context.Background(), t.TempDir(), nil)
	if !d.Empty() {
		t.Errorf("d = %+v", d)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("direnv ran in a folder without .envrc")
	}
}

func TestDirenvがないときは環境を変えない(t *testing.T) {
	old := direnvBinary
	direnvBinary = func() string { return "" }
	t.Cleanup(func() { direnvBinary = old })
	if d := LoadDirEnv(context.Background(), envrcDir(t), nil); !d.Empty() {
		t.Errorf("d = %+v", d)
	}
}

func Test終わらないenvrcは打ち切って環境を変えない(t *testing.T) {
	fakeDirenv(t, `sleep 30 & sleep 30; echo '{"FOO":"bar"}'`)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	d := LoadDirEnv(ctx, envrcDir(t), nil)
	if !d.Empty() {
		t.Errorf("d = %+v", d)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func Test本物のdirenvで許可したenvrcを読み込む(t *testing.T) {
	bin, err := exec.LookPath("direnv")
	if err != nil {
		t.Skip("direnv is not installed")
	}
	dir := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".envrc"), []byte("export HC_DIRENV_TEST=yes\nPATH_add bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	environ := []string{"HOME=" + home, "XDG_DATA_HOME=" + filepath.Join(home, "data"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "PATH=/usr/bin:/bin"}
	if d := LoadDirEnv(context.Background(), dir, environ); !d.Empty() {
		t.Errorf("an .envrc that is not allowed was loaded: %+v", d)
	}
	allow := exec.Command(bin, "allow", dir)
	allow.Env = environ
	if out, err := allow.CombinedOutput(); err != nil {
		t.Fatalf("direnv allow: %v %s", err, out)
	}
	d := LoadDirEnv(context.Background(), dir, environ)
	real, _ := filepath.EvalSymlinks(dir)
	if d.Set["HC_DIRENV_TEST"] != "yes" || !strings.HasPrefix(d.Set["PATH"], filepath.Join(real, "bin")+":") {
		t.Errorf("set = %v", d.Set)
	}
}
