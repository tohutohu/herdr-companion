package agentplugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

var skillNames = []string{"drive-sessions", "reply-in-app", "schedule-resident-tasks"}

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
	for _, name := range skillNames {
		if _, err := os.Stat(filepath.Join(SkillsDir(dir), name, "SKILL.md")); err != nil {
			t.Error(err)
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

// Claude Code and Codex read the same files, so the limits of the Agent Skills
// specification apply. Every description sits in every session's context, and
// Codex shortens them when many skills compete for its budget, so they stay
// far below the 1024-character limit.
func TestスキルはAgentSkillsの仕様と文字数の予算に収まる(t *testing.T) {
	const maxDescription, maxBodyLines = 300, 80
	frontmatter := regexp.MustCompile(`\A---\nname: ([a-z0-9-]{1,64})\ndescription: ([^\n]+)\n---\n`)
	entries, err := files.ReadDir("plugin/skills")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, e := range entries {
		found = append(found, e.Name())
		b, err := files.ReadFile("plugin/skills/" + e.Name() + "/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		m := frontmatter.FindStringSubmatch(string(b))
		if m == nil {
			t.Errorf("%s: no name and description frontmatter", e.Name())
			continue
		}
		if m[1] != e.Name() {
			t.Errorf("%s: name is %q", e.Name(), m[1])
		}
		if n := utf8.RuneCountInString(m[2]); n > maxDescription {
			t.Errorf("%s: description has %d characters (max %d)", e.Name(), n, maxDescription)
		}
		if strings.ContainsAny(m[2], "<>") {
			t.Errorf("%s: description contains XML-like text", e.Name())
		}
		if n := strings.Count(string(b), "\n"); n > maxBodyLines {
			t.Errorf("%s: %d lines (max %d): move details to hc help or a reference file", e.Name(), n, maxBodyLines)
		}
	}
	if strings.Join(found, " ") != strings.Join(skillNames, " ") {
		t.Errorf("skills = %v, want %v", found, skillNames)
	}
}
