// Package worktree finds the git worktrees sessions were started in and has
// Herdr, which creates them, remove them again. The gateway keeps no record
// of them: a worktree it created carries a marker file in its git directory.
package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
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
func Mark(ctx context.Context, dir, provider string) error {
	c, err := inspect(ctx, dir)
	if err != nil {
		return err
	}
	if !c.linked() {
		return fmt.Errorf("%s is not a linked worktree", c.top)
	}
	b, _ := json.Marshal(map[string]any{"version": 1, "provider": provider, "createdAt": time.Now().UTC()})
	return os.WriteFile(filepath.Join(c.gitDir, markerFile), b, 0o644)
}

// Herdr is the part of the Herdr client that removes worktrees.
type Herdr interface {
	OpenWorktree(ctx context.Context, path string) (workspaceID string, alreadyOpen bool, err error)
	RemoveWorktree(ctx context.Context, workspaceID string) error
	CloseWorkspace(ctx context.Context, workspaceID string) error
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

// Cleanup has Herdr remove the worktree dir is in once its session is
// archived. It only touches linked worktrees the gateway created, and keeps
// any that a Herdr workspace still has open or that Herdr refuses to remove
// because of uncommitted changes. Herdr keeps the branch, so commits are
// never lost. A nil Outcome means dir is not such a worktree.
func Cleanup(ctx context.Context, h Herdr, dir string) (*Outcome, error) {
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, nil // already gone
	}
	c, err := inspect(ctx, dir)
	if err != nil || !c.linked() {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(c.gitDir, markerFile)); err != nil {
		return nil, nil
	}
	out := &Outcome{Path: c.top}
	// Herdr removes a worktree through the workspace open in it.
	ws, alreadyOpen, err := h.OpenWorktree(ctx, c.top)
	if err != nil {
		return nil, err
	}
	if alreadyOpen {
		out.Reason = "another pane is still using it"
		return out, nil
	}
	err = h.RemoveWorktree(ctx, ws)
	if err == nil {
		out.Removed = true
		return out, nil
	}
	if cerr := h.CloseWorkspace(ctx, ws); cerr != nil {
		slog.Warn("closing the workspace opened to remove a worktree failed", "workspace", ws, "path", c.top, "error", cerr)
	}
	var herr *herdr.Error
	if errors.As(err, &herr) && herr.Code == herdr.ErrDirtyWorktree {
		out.Reason = "it has uncommitted changes"
		return out, nil
	}
	return nil, err
}
