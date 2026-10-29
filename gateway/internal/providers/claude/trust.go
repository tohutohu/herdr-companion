package claude

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// FolderTrusted reports whether the folder-trust dialog was accepted for cwd
// or a parent directory, as Claude Code records it in its global config. It
// only reads the file.
func (p *Provider) FolderTrusted(cwd string) bool {
	path := filepath.Join(p.configDir, ".claude.json")
	if os.Getenv("CLAUDE_CONFIG_DIR") == "" && p.configDir == DefaultConfigDir() {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".claude.json")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var cfg struct {
		Projects map[string]struct {
			HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return false
	}
	for _, d := range providers.ParentDirs(cwd) {
		if cfg.Projects[d].HasTrustDialogAccepted {
			return true
		}
	}
	return false
}
