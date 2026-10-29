package providers

import (
	"sort"
	"strings"
)

// childEnvDrop are variables a directly managed agent must not inherit from
// the gateway. The gateway may run inside a Claude Code session or a Herdr
// pane during development: a nested Claude Code would then believe it is a
// child session (and skip saving its transcript), and hc / herdr in the
// agent's tools would act on the gateway's pane. Herdr's own configuration
// (HERDR_SESSION, the default socket) is kept, so the agent can still use
// Herdr as a separate tool.
var childEnvDrop = map[string]bool{
	"CLAUDECODE":                   true,
	"CLAUDE_CODE_ENTRYPOINT":       true,
	"CLAUDE_CODE_SESSION_ID":       true,
	"CLAUDE_CODE_CHILD_SESSION":    true,
	"CLAUDE_CODE_EXECPATH":         true,
	"CLAUDE_CODE_MESSAGING_SOCKET": true,
	"CLAUDE_CODE_MESSAGING_TOKEN":  true,
	"CODEX_THREAD_ID":              true,
	"CODEX_SESSION_ID":             true,
	"HERDR_PANE_ID":                true,
	"HERDR_SOCKET_PATH":            true,
	"HERDR_WORKSPACE_ID":           true,
	"HERDR_TAB_ID":                 true,
	"HC_SESSION_ID":                true,
	"HC_CURSOR":                    true,
}

// ChildEnv returns environ without the variables above, plus set (which
// wins). The result is sorted for stable logs and tests.
func ChildEnv(environ []string, set map[string]string) []string {
	var out []string
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if childEnvDrop[k] {
			continue
		}
		if _, ok := set[k]; ok {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
