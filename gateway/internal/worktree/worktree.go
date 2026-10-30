// Package worktree creates the git worktrees sessions start in and removes
// them again once the session is archived, with git itself. The gateway keeps
// no record of them: a worktree it created carries a marker file in its git
// directory. Worktrees marked by 1.x for Herdr launches are removed the same
// way.
package worktree

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var ErrNotRepository = errors.New("the directory is not in a git repository")

// markerFile sits in a linked worktree's git directory (.git/worktrees/<name>).
const markerFile = "herdr-companion.json"

type checkout struct {
	top       string // root of this working tree
	gitDir    string // its git directory (.git/worktrees/<name> when linked)
	commonDir string // the repository's shared git directory
}

func (c *checkout) linked() bool { return c.gitDir != c.commonDir }

// main is where git commands about the whole repository run: the main
// working tree, or the repository itself when it is bare.
func (c *checkout) main() string {
	if filepath.Base(c.commonDir) == ".git" {
		return filepath.Dir(c.commonDir)
	}
	return c.commonDir
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

func inspect(ctx context.Context, dir string) (*checkout, error) {
	out, err := git(ctx, dir, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-dir", "--git-common-dir")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotRepository, err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		return nil, ErrNotRepository // bare repository: no working tree
	}
	c := &checkout{top: lines[0], gitDir: lines[1], commonDir: lines[2]}
	for _, p := range []*string{&c.top, &c.gitDir, &c.commonDir} {
		if real, err := filepath.EvalSymlinks(*p); err == nil {
			*p = real
		}
	}
	return c, nil
}

// Toplevel returns the root of the working tree dir is in.
func Toplevel(ctx context.Context, dir string) (string, error) {
	c, err := inspect(ctx, dir)
	if err != nil {
		return "", err
	}
	return c.top, nil
}

// MainCheckout returns the main working tree of the repository dir is in,
// which for a linked worktree lies somewhere else.
func MainCheckout(ctx context.Context, dir string) (string, error) {
	c, err := inspect(ctx, dir)
	if err != nil {
		return "", err
	}
	return c.main(), nil
}

// Mark records that the gateway had the linked worktree dir is in created
// for a new session, so archiving the session may remove it.
func mark(ctx context.Context, dir, provider, backend string) error {
	c, err := inspect(ctx, dir)
	if err != nil {
		return err
	}
	if !c.linked() {
		return fmt.Errorf("%s is not a linked worktree", c.top)
	}
	b, _ := json.Marshal(map[string]any{"version": 1, "provider": provider, "backend": backend, "createdAt": time.Now().UTC()})
	return os.WriteFile(filepath.Join(c.gitDir, markerFile), b, 0o644)
}

// Create makes a new linked worktree of the repository dir is in, on a new
// branch worktree/<name> from HEAD, under base/<repository>/<name>, and marks
// it as the gateway's.
func Create(ctx context.Context, dir, base, provider string) (string, error) {
	c, err := inspect(ctx, dir)
	if err != nil {
		return "", err
	}
	b := make([]byte, 3)
	rand.Read(b)
	name := provider + "-" + hex.EncodeToString(b)
	path := filepath.Join(base, filepath.Base(c.main()), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if _, err := git(ctx, c.top, "worktree", "add", "-b", "worktree/"+name, path, "HEAD"); err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if err := mark(ctx, path, provider, "direct"); err != nil {
		slog.Warn("marking worktree failed", "provider", provider, "path", path, "error", err)
	}
	return path, nil
}

// Discard removes a worktree Create made for a launch that did not go ahead.
// Nothing was done in it yet; the branch is kept like any other.
func Discard(ctx context.Context, path string) error {
	c, err := inspect(ctx, path)
	if err != nil || !c.linked() {
		return err
	}
	_, err = git(ctx, c.main(), "worktree", "remove", c.top)
	return err
}

// removeWithGit removes a clean worktree with git. git refuses one with
// modified or untracked files without --force, which is never given.
func removeWithGit(ctx context.Context, c *checkout, out *Outcome) (*Outcome, error) {
	status, err := git(ctx, c.top, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(status) != "" {
		out.Reason = "it has uncommitted changes"
		return out, nil
	}
	if _, err := git(ctx, c.main(), "worktree", "remove", c.top); err != nil {
		if strings.Contains(err.Error(), "modified or untracked") || strings.Contains(err.Error(), "contains modified") {
			out.Reason = "it has uncommitted changes"
			return out, nil
		}
		return nil, err
	}
	out.Removed = true
	return out, nil
}

// marked reports whether the gateway created the worktree.
func marked(c *checkout) bool {
	_, err := os.Stat(filepath.Join(c.gitDir, markerFile))
	return err == nil
}

// Outcome reports what Cleanup did with a session's worktree.
type Outcome struct {
	Path    string `json:"path"`
	Removed bool   `json:"removed"`
	// Reason says why the worktree was kept.
	Reason string `json:"reason,omitempty"`
}

// Warning is a sentence for the user when the worktree was kept.
func (o *Outcome) Warning() string {
	if o == nil || o.Removed {
		return ""
	}
	return "Worktree kept because " + o.Reason + ": " + o.Path
}

// Cleanup removes the worktree dir is in once its session is archived. It
// only touches linked worktrees the gateway created, and keeps any that
// another live session (inUse) still uses, or that has uncommitted or
// untracked changes (ignored files do not count). The branch is always kept,
// so commits are never lost. A nil Outcome means dir is not such a worktree.
func Cleanup(ctx context.Context, dir string, inUse func(path string) bool) (*Outcome, error) {
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, nil // already gone
	}
	c, err := inspect(ctx, dir)
	if err != nil || !c.linked() || !marked(c) {
		return nil, nil
	}
	out := &Outcome{Path: c.top}
	if inUse != nil && inUse(c.top) {
		out.Reason = "another session is still using it"
		return out, nil
	}
	return removeWithGit(ctx, c, out)
}
