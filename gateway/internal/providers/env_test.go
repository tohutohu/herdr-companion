package providers

import (
	"slices"
	"testing"
)

func Test直接起動するエージェントにはGatewayのセッションやペインの変数を渡さない(t *testing.T) {
	env := ChildEnv([]string{
		"PATH=/bin", "CLAUDECODE=1", "CLAUDE_CODE_CHILD_SESSION=1", "HERDR_PANE_ID=w1:p1",
		"HERDR_SOCKET_PATH=/s", "HERDR_SESSION=main", "HC_SESSION_ID=claude:old", "HOME=/h",
	}, map[string]string{"HC_SESSION_ID": "claude:new"})
	want := []string{"HC_SESSION_ID=claude:new", "HERDR_SESSION=main", "HOME=/h", "PATH=/bin"}
	if !slices.Equal(env, want) {
		t.Errorf("env = %v, want %v", env, want)
	}
}
