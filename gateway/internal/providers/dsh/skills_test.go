package dsh

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/resident"
)

// loggingProvider is testProvider whose dsh records its arguments and
// HC_SESSION_ID, one line per child, before running the protocol helper.
func loggingProvider(t *testing.T) (*Provider, string) {
	t.Helper()
	p, cfg, _ := testProvider(t)
	log := filepath.Join(t.TempDir(), "launches")
	wrapper := filepath.Join(t.TempDir(), "dsh")
	script := "#!/bin/sh\necho \"$* session=${HC_SESSION_ID-unset}\" >> '" + log + "'\nexec '" + cfg.Binary + "' \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	p.cfg.Binary = wrapper
	return p, log
}

func launches(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestDSHはGatewayのスキルをパッチで読み込みセッションに自分のIDを渡す(t *testing.T) {
	p, log := loggingProvider(t)
	plugin := filepath.Join(t.TempDir(), "agent plugin")
	p.SetAgentPlugin(plugin)

	id := startTest(t, p, "hello")
	patch := filepath.Join(p.dir, patchName)
	got := launches(t, log)
	if want := "--profile sdk --patch " + patch + " session=dsh:" + id; len(got) != 1 || got[0] != want {
		t.Fatalf("launches = %q, want %q", got, want)
	}
	yml, err := os.ReadFile(patch)
	if err != nil {
		t.Fatal(err)
	}
	want := "- id: skill-filesystem\n  config:\n    customSkillDirs:\n      - \"" + filepath.Join(plugin, "skills") + "\"\n"
	if !strings.Contains(string(yml), want) || strings.Contains(string(yml), "llm-pi-ai") {
		t.Fatalf("patch:\n%s", yml)
	}
}

func TestDSHの常駐実行もGatewayのスキルを読み込みセッションIDは持たない(t *testing.T) {
	p, log := loggingProvider(t)
	p.SetAgentPlugin(t.TempDir())

	if _, err := p.ResidentRunner().Run(context.Background(), residentRequestForTest(t, "hello"), resident.RunHooks{}); err != nil {
		t.Fatal(err)
	}
	got := launches(t, log)
	if want := "--profile sdk --patch " + filepath.Join(p.dir, patchName) + " session=unset"; len(got) != 1 || got[0] != want {
		t.Fatalf("launches = %q, want %q", got, want)
	}
}

func TestDSHのパッチはキーとスキルを両方持ちスキルを外すとキーだけ残る(t *testing.T) {
	isolatedHome(t)
	dir := t.TempDir()
	p, err := New(Config{Binary: "/bin/true", ProviderKeys: map[string]string{"opencode-go": "k"}}, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.SetAgentPlugin("/plugin")
	yml, _ := os.ReadFile(filepath.Join(dir, patchName))
	if !strings.Contains(string(yml), "apiKeyEnv: HERDR_DSH_KEY_OPENCODE_GO") || !strings.Contains(string(yml), `- "/plugin/skills"`) {
		t.Fatalf("patch:\n%s", yml)
	}
	p.SetAgentPlugin("")
	yml, _ = os.ReadFile(filepath.Join(dir, patchName))
	if !strings.Contains(string(yml), "apiKeyEnv:") || strings.Contains(string(yml), "skill-filesystem") {
		t.Fatalf("patch:\n%s", yml)
	}
}
