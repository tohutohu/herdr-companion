package claude

import (
	"os"
	"strings"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

type claudeModeOverride struct {
	label      string
	transcript os.FileInfo
}

// claudeModeFromPane extracts the mode from Claude Code's status line. The
// status line includes the shortcut hint, which distinguishes it from the
// same words in conversation output.
func claudeModeFromPane(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.ToLower(strings.TrimSpace(lines[i]))
		if !strings.Contains(line, "shift+tab") {
			continue
		}
		switch {
		case strings.Contains(line, "plan mode"):
			return "Plan"
		case strings.Contains(line, "accept edits"):
			return "Accept edits"
		case strings.Contains(line, "don't ask") || strings.Contains(line, "dont ask"):
			return "Don't ask"
		case strings.Contains(line, "bypass permissions"):
			return "Bypass permissions"
		case strings.Contains(line, "default mode") || strings.Contains(line, "manual mode"):
			return "Default"
		}
	}
	return ""
}

func (p *Provider) applyModeOverride(nativeID, path string, summary *providers.Summary) {
	p.modeMu.Lock()
	defer p.modeMu.Unlock()
	override, ok := p.modeOverride[nativeID]
	if !ok {
		return
	}
	if override.transcript != nil {
		current, err := os.Stat(path)
		if err != nil || !sameTranscript(override.transcript, current) {
			delete(p.modeOverride, nativeID)
			return
		}
	}
	summary.Mode = override.label
}
