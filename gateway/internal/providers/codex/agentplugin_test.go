package codex

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestCodexはGatewayのプラグインのスキルを追加のルートにする(t *testing.T) {
	p := New("codex", nil)
	p.SetAgentPlugin("/plugin")
	if got := p.currentSkillRoots(); !slices.Equal(got, []string{filepath.Join("/plugin", "skills")}) {
		t.Fatalf("roots = %v", got)
	}
	p.SetAgentPlugin("")
	if got := p.currentSkillRoots(); got != nil {
		t.Fatalf("roots = %v", got)
	}
}
