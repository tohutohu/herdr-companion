package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
)

func TestDirenvの変数をスレッドのシェル環境の上書きとして渡しCodex自身のPATHを残す(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '{\"FOO\":\"bar\",\"GONE\":null,\"PATH\":\"/proj/bin:%s\"}' \"$PATH\"\n"
	if err := os.WriteFile(filepath.Join(bin, "direnv"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".envrc"), []byte("export FOO=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fakeServer{t: t}
	pt := &pipeTransport{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
	go f.serve(pt, func(v any) {
		b, _ := json.Marshal(v)
		pt.in <- b
	})
	p := New("codex-not-used", deadletter.Nop{})
	p.owned = testOwned(pt)
	defer p.owned.c.close()

	cfg := dirEnvConfig(context.Background(), p.owned.c, dir)
	want := map[string]any{
		"shell_environment_policy.set.FOO":  "bar",
		"shell_environment_policy.set.PATH": "/codex/arg0:/proj/bin:" + bin + ":/usr/bin:/bin",
	}
	got, _ := json.Marshal(cfg)
	exp, _ := json.Marshal(want)
	if string(got) != string(exp) {
		t.Errorf("config = %s, want %s", got, exp)
	}
}

func TestEnvrcがないフォルダではスレッドの設定を上書きしない(t *testing.T) {
	if cfg := dirEnvConfig(context.Background(), nil, t.TempDir()); cfg != nil {
		t.Errorf("config = %v", cfg)
	}
}

func TestDirenvが変えたPATHにCodexが前に足したフォルダを重複なく残す(t *testing.T) {
	got := mergePath("/arg0:/codex-path:/a:/b", "/a:/b", "/proj/bin:/a:/b:/proj/bin")
	if want := "/arg0:/codex-path:/proj/bin:/a:/b"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}
