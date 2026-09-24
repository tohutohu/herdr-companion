// Package worktree finds and removes the git worktrees sessions were started
// in. The gateway keeps no record of them: a worktree it created carries a
// marker file in its git directory, the way Codex marks the worktrees it
// manages with the thread that owns them.
package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var ErrNotRepository = errors.New("the directory is not in a git repository")

const (
	// markerFile sits in a linked worktree's git directory (.git/worktrees/<name>).
	markerFile = "herdr-companion.json"
	// codexOwnerFile is where Codex records the thread that owns a worktree
	// it created with --worktree.
	codexOwnerFile = "codex-thread.json"
)

// LockWait is how long Cleanup waits for an agent that holds the worktree
// lock to exit after its pane was closed.
var LockWait = 5 * time.Second

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

// List returns the working trees of the repository dir is in.
func List(ctx context.Context, dir string) ([]string, error) {
	out, err := git(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotRepository, err)
	}
	var paths []string
	for line := range strings.SplitSeq(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			if real, err := filepath.EvalSymlinks(p); err == nil {
				p = real
			}
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// Mark records that the gateway created the linked worktree dir is in for a
// new session, so archiving the session may remove it.
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

// Owner is the session a worktree is cleaned up for.
type Owner struct {
	Provider string
	NativeID string
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
	return "Kept the worktree at " + o.Path + " because " + o.Reason + "."
}

func (c *checkout) ownedBy(o Owner) bool {
	if _, err := os.Stat(filepath.Join(c.gitDir, markerFile)); err == nil {
		return true
	}
	if o.Provider != "codex" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(c.gitDir, codexOwnerFile))
	if err != nil {
		return false
	}
	var f struct {
		OwnerThreadID string `json:"ownerThreadId"`
	}
	return json.Unmarshal(b, &f) == nil && f.OwnerThreadID != "" && f.OwnerThreadID == o.NativeID
}

// Cleanup removes the worktree dir is in once its session is archived. It
// only touches linked worktrees the gateway created (or, for Codex, the one
// Codex made for this thread), and keeps any that other panes (inUse
// directories) still use or that hold work: uncommitted changes, or commits
// no branch points to. Its branch is deleted only when merged, as `git
// branch -d` decides, so commits are never lost. A nil Outcome means dir is
// not such a worktree.
func Cleanup(ctx context.Context, dir string, owner Owner, inUse []string) (*Outcome, error) {
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, nil // already gone
	}
	c, err := inspect(ctx, dir)
	if err != nil || !c.linked() || !c.ownedBy(owner) {
		return nil, nil
	}
	out := &Outcome{Path: c.top}
	for _, d := range inUse {
		if real, err := filepath.EvalSymlinks(d); err == nil {
			d = real
		}
		if within(c.top, d) {
			out.Reason = "another pane is still using it"
			return out, nil
		}
	}
	if reason := c.releaseLock(ctx); reason != "" {
		out.Reason = reason
		return out, nil
	}
	status, err := git(ctx, c.top, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	if status != "" {
		out.Reason = "it has uncommitted changes"
		return out, nil
	}
	branch, _ := git(ctx, c.top, "symbolic-ref", "--quiet", "--short", "HEAD")
	if branch == "" {
		// A detached HEAD's commits are only reachable from this worktree.
		lost, err := git(ctx, c.top, "rev-list", "--max-count=1", "HEAD", "--not", "--branches", "--tags", "--remotes")
		if err != nil {
			return nil, err
		}
		if lost != "" {
			out.Reason = "it has commits that are not on any branch"
			return out, nil
		}
	}
	if _, err := git(ctx, c.main(), "worktree", "remove", c.top); err != nil {
		return nil, err
	}
	out.Removed = true
	if branch != "" {
		// Unmerged work stays on its branch.
		git(ctx, c.main(), "branch", "-d", branch)
	}
	if owner.Provider == "codex" {
		// Codex keeps each worktree in a bucket directory of its own.
		os.Remove(filepath.Dir(c.top))
	}
	return out, nil
}

// claudeLock matches the reason Claude Code locks its --worktree checkouts
// with: "claude session <name> (pid <pid> start <time>)".
var claudeLock = regexp.MustCompile(`^claude session .*\(pid (\d+)\b`)

// releaseLock unlocks a worktree locked by an agent that has exited and
// returns why the worktree must be kept otherwise.
func (c *checkout) releaseLock(ctx context.Context) string {
	b, err := os.ReadFile(filepath.Join(c.gitDir, "locked"))
	if err != nil {
		return ""
	}
	m := claudeLock.FindSubmatch(b)
	if m == nil {
		return "it is locked"
	}
	pid, _ := strconv.Atoi(string(m[1]))
	// The pane was just closed; give the agent a moment to exit.
	deadline := time.Now().Add(LockWait)
	for alive(pid) {
		if time.Now().After(deadline) {
			return "its agent is still running"
		}
		select {
		case <-ctx.Done():
			return "its agent is still running"
		case <-time.After(100 * time.Millisecond):
		}
	}
	if _, err := git(ctx, c.main(), "worktree", "unlock", c.top); err != nil {
		return "it could not be unlocked"
	}
	return ""
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// within reports whether p is root or inside it.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
