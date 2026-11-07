package dsh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
}

// A home with no dsh on PATH, as the menu-bar app's Gateway sees it.
func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	return home
}

func TestDSH実行ファイルは設定値を優先する(t *testing.T) {
	isolatedHome(t)
	if got := FindBinary("/opt/custom/dsh"); got != "/opt/custom/dsh" {
		t.Fatalf("got %q", got)
	}
}

func TestDSH実行ファイルをPATHになければmiseの新しいNodeから探す(t *testing.T) {
	home := isolatedHome(t)
	root := filepath.Join(home, ".local", "share", "mise", "installs", "node")
	for _, v := range []string{"9.0.0", "24.1.0", "26.12.0"} {
		writeExec(t, filepath.Join(root, v, "bin", "dsh"), "#!/bin/sh\n")
	}
	if err := os.MkdirAll(filepath.Join(root, "27.0.0", "bin"), 0700); err != nil { // dsh not installed for this Node
		t.Fatal(err)
	}
	want := filepath.Join(root, "26.12.0", "bin", "dsh")
	if got := FindBinary(""); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDSH実行ファイルはPATH上のものを使う(t *testing.T) {
	isolatedHome(t)
	dir := t.TempDir()
	writeExec(t, filepath.Join(dir, "dsh"), "#!/bin/sh\n")
	t.Setenv("PATH", dir)
	if got := FindBinary(""); got != filepath.Join(dir, "dsh") {
		t.Fatalf("got %q", got)
	}
}

func TestDSH実行ファイルが見つからなければ空(t *testing.T) {
	home := isolatedHome(t)
	// Not executable: an unfinished install must not be chosen.
	if err := os.MkdirAll(filepath.Join(home, ".volta", "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".volta", "bin", "dsh"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if got := FindBinary(""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestDSHのバージョン順は数値で比べ別名は後ろ(t *testing.T) {
	root := t.TempDir()
	for _, v := range []string{"latest", "9.11.0", "24", "24.21.0", "lts", "v25.2.1"} {
		if err := os.Mkdir(filepath.Join(root, v), 0700); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(newestFirst(root), " ")
	if want := "v25.2.1 24.21.0 24 9.11.0 latest lts"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDSH子プロセスのPATHは実行ファイルの場所を先頭にする(t *testing.T) {
	if got := ChildPath("/n/26/bin/dsh", "/usr/bin:/bin"); got != "/n/26/bin:/usr/bin:/bin" {
		t.Fatalf("got %q", got)
	}
	if got := ChildPath("dsh", "/usr/bin"); got != "/usr/bin" {
		t.Fatalf("relative binary changed PATH: %q", got)
	}
}

func TestDSHプロバイダーのキーはパッチと環境変数で渡しファイルに書かない(t *testing.T) {
	isolatedHome(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	bin := filepath.Join(t.TempDir(), "bin", "dsh")
	writeExec(t, bin, "#!/bin/sh\n{ echo \"$*\"; /usr/bin/env; } > '"+out+"'\n")
	p, err := New(Config{Binary: bin, APIKey: "ds-key", ProviderKeys: map[string]string{"opencode-go": "go-key"},
		Models: []ModelRoute{{ID: "go-kimi", Provider: "opencode-go", Model: "kimi-k3"}}}, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	c, err := launch(p.launchSpec(t.TempDir(), workspaceMode))
	if err != nil {
		t.Fatal(err)
	}
	c.cmd.Wait()
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	patch := filepath.Join(dir, patchName)
	for _, want := range []string{"--profile sdk --patch " + patch + "\n", "\nHERDR_DSH_KEY_OPENCODE_GO=go-key\n", "\nDEEPSEEK_API_KEY=ds-key\n", "\nPATH=" + filepath.Dir(bin) + ":"} {
		if !strings.Contains(got, want) {
			t.Errorf("child is missing %q:\n%s", want, got)
		}
	}
	yml, err := os.ReadFile(patch)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(yml), "go-key") || !strings.Contains(string(yml), "      opencode-go:\n        apiKeyEnv: HERDR_DSH_KEY_OPENCODE_GO\n") {
		t.Fatalf("patch:\n%s", yml)
	}
}

func TestDSHキーを消すとパッチも消えて付けない(t *testing.T) {
	isolatedHome(t)
	dir := t.TempDir()
	keyed, err := New(Config{Binary: "/bin/true", ProviderKeys: map[string]string{"openrouter": "k"}}, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	keyed.Close()
	p, err := New(Config{Binary: "/bin/true"}, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := os.Stat(filepath.Join(dir, patchName)); !os.IsNotExist(err) {
		t.Fatalf("stale patch: %v", err)
	}
	if spec := p.launchSpec(dir, workspaceMode); spec.patch != "" || len(spec.env) != 0 {
		t.Fatalf("spec: %+v", spec)
	}
}

func TestDSH設定の検証は起動できない設定を拒む(t *testing.T) {
	for name, cfg := range map[string]Config{
		"公式ルートのキー":   {ProviderKeys: map[string]string{deepSeekRoute: "k"}},
		"ルート名が不正":    {ProviderKeys: map[string]string{"Open Code": "k"}},
		"キーに改行":      {ProviderKeys: map[string]string{"opencode-go": "a\nb"}},
		"既定モデルがない":   {Model: "kimi-k3"},
		"組み込みとIDが重複": {Models: []ModelRoute{{ID: "deepseek-v4-pro", Provider: "opencode-go", Model: "deepseek-v4-pro"}}},
	} {
		if Validate(cfg) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := Config{Model: "go-glm", ProviderKeys: map[string]string{"opencode-go": "k"}, Models: []ModelRoute{{ID: "go-glm", Provider: "opencode-go", Model: "glm-5.3"}}}
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
}
