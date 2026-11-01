package agentplugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallはプラグインとスキルを書き出して古い内容を置き換える(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent-plugin")
	if err := os.MkdirAll(filepath.Join(dir, "skills", "removed"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Install(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("Install = %q, want %q", got, dir)
	}
	var manifest struct{ Name string }
	b, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &manifest); err != nil || manifest.Name != "herdr-companion" {
		t.Fatalf("plugin.json = %s (%v)", b, err)
	}
	for _, name := range []string{"hc", "app"} {
		b, err := os.ReadFile(filepath.Join(SkillsDir(dir), name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(b), "---\nname: "+name+"\ndescription: ") {
			t.Errorf("%s/SKILL.md has no frontmatter naming it:\n%.80s", name, b)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "removed")); !os.IsNotExist(err) {
		t.Errorf("the old copy is still there: %v", err)
	}
	left, _ := filepath.Glob(dir + ".tmp-*")
	if len(left) > 0 {
		t.Errorf("temporary directories left: %v", left)
	}
}
