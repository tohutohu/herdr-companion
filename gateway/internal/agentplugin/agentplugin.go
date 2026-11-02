// Package agentplugin carries the skills the Gateway gives the agents it runs:
// how the Herdr Companion app shows their replies and files, how to drive other
// sessions with hc, and how to design resident tasks. They are one Claude Code
// plugin ("herdr-companion"); Codex reads the same skills directory and names
// them the same way ("herdr-companion:drive-sessions").
package agentplugin

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:plugin
var files embed.FS

// Install writes the plugin to dir, replacing what an earlier Gateway wrote
// there, and returns dir. The copy is built next to dir and renamed into
// place, so dir never holds a half-written plugin.
func Install(dir string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), filepath.Base(dir)+".tmp-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	sub, err := fs.Sub(files, "plugin")
	if err != nil {
		return "", err
	}
	if err := os.CopyFS(tmp, sub); err != nil {
		return "", fmt.Errorf("write agent plugin: %w", err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", err
	}
	old := tmp + ".old"
	if err := os.Rename(dir, old); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return "", err
	}
	os.RemoveAll(old)
	return dir, nil
}

// SkillsDir is the directory of skills inside an installed plugin.
func SkillsDir(dir string) string { return filepath.Join(dir, "skills") }
