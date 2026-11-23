package claude

import (
	"slices"
	"testing"
)

func TestClaudeはGatewayのプラグインを設定の入れ替え後も読み込む(t *testing.T) {
	p := New(t.TempDir(), nil)
	p.SetAgentPlugin("/plugin")
	p.SetDirect(DirectConfig{Sidecar: "/sidecar", Claude: "/claude"})
	p.direct.mu.Lock()
	cfg := p.direct.cfg
	p.direct.mu.Unlock()
	if got := cfg.plugins(); !slices.Equal(got, []string{"/plugin"}) {
		t.Fatalf("plugins = %v", got)
	}
	p.SetAgentPlugin("")
	p.direct.mu.Lock()
	cfg = p.direct.cfg
	p.direct.mu.Unlock()
	if got := cfg.plugins(); got != nil {
		t.Fatalf("plugins = %v", got)
	}
}
