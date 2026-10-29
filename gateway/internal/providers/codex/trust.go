package codex

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// FolderTrusted reports whether cwd or a parent directory has
// trust_level = "trusted" in Codex's config.toml, as the Codex TUI records
// its trust answer. It only reads the file.
func (p *Provider) FolderTrusted(cwd string) bool {
	f, err := os.Open(filepath.Join(codexHome(), "config.toml"))
	if err != nil {
		return false
	}
	defer f.Close()
	trusted := map[string]bool{}
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			section = ""
			if rest, ok := strings.CutPrefix(line, "[projects."); ok {
				if dir, err := strconv.Unquote(strings.TrimSuffix(rest, "]")); err == nil {
					section = dir
				}
			}
			continue
		}
		if section == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == "trust_level" && strings.Trim(strings.TrimSpace(v), `"'`) == "trusted" {
			trusted[filepath.Clean(section)] = true
		}
	}
	for _, d := range providers.ParentDirs(cwd) {
		if trusted[d] {
			return true
		}
	}
	return false
}
