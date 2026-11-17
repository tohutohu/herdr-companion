// Package shellenv gives the gateway the environment the user's terminal
// has. A Gateway started by the menu-bar app (a login item) gets launchd's
// short environment, so tools the shell's startup files put on PATH (mise,
// ~/go/bin, aqua, pnpm, …) were missing for every agent it started.
package shellenv

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Timeout bounds one read of the shell. An interactive zsh with plugins
// usually needs well under a second.
const Timeout = 10 * time.Second

// DisableEnv turns the read off when set to "0".
const DisableEnv = "HERDR_LOGIN_SHELL_ENV"

// resolvingEnv is set while the shell runs, so startup files can skip work
// that only makes sense in a terminal (as VS Code's VSCODE_RESOLVING_ENVIRONMENT).
const resolvingEnv = "HERDR_RESOLVING_ENVIRONMENT"

const marker = "__HERDR_SHELL_ENV_7f3a__"

// Read runs the user's shell as an interactive login shell in home and
// returns the environment it ends up with.
func Read(ctx context.Context, shell, home string, environ []string) (map[string]string, error) {
	if shell == "" || !filepath.IsAbs(shell) {
		return nil, errors.New("no login shell")
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	script := "printf '%s' " + marker + "; /usr/bin/env -0; printf '%s' " + marker
	cmd := exec.CommandContext(ctx, shell, "-i", "-l", "-c", script)
	cmd.Dir = home
	cmd.Env = append(append([]string(nil), environ...), resolvingEnv+"=1")
	cmd.Stdin = nil
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// Startup files may print to stderr (or reveal secrets); it is dropped.
	cmd.Stderr = nil
	// Something the startup files start in the background must not keep it alive.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil && stdout.Len() == 0 {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("the login shell did not finish in time")
		}
		return nil, err
	}
	return parse(stdout.Bytes())
}

func parse(out []byte) (map[string]string, error) {
	start := bytes.Index(out, []byte(marker))
	if start < 0 {
		return nil, errors.New("no environment in the shell's output")
	}
	rest := out[start+len(marker):]
	end := bytes.Index(rest, []byte(marker))
	if end < 0 {
		return nil, errors.New("incomplete environment in the shell's output")
	}
	env := map[string]string{}
	for _, entry := range bytes.Split(rest[:end], []byte{0}) {
		k, v, ok := strings.Cut(string(entry), "=")
		if ok && namePattern.MatchString(k) {
			env[k] = v
		}
	}
	if env["PATH"] == "" {
		return nil, errors.New("no PATH in the shell's environment")
	}
	return env, nil
}

var namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// skip reports variables taken from the gateway, never from the shell:
// the shell's own bookkeeping, per-folder state of mise and direnv (loaded
// per session instead), and what the gateway and the menu-bar app set or
// strip on purpose.
func skip(k string) bool {
	switch k {
	case "PWD", "OLDPWD", "SHLVL", "_", "SHELL", "HOME", "USER", "LOGNAME", "TMPDIR",
		"TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "TERM_SESSION_ID", "COLORTERM",
		"TTY", "SSH_TTY", "MISE_SHELL", "CLAUDECODE", "TYPESAFE_API_KEY",
		"GOOGLE_APPLICATION_CREDENTIALS", resolvingEnv:
		return true
	}
	for _, p := range []string{"__MISE_", "DIRENV_", "HERDR_", "CLAUDE_CODE_", "HC_", "CODEX_", "ITERM_", "STARSHIP_", "TMUX", "ZELLIJ", "STY", "WINDOW", "KITTY_", "WEZTERM_", "GHOSTTY_", "VSCODE_"} {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

// Merge returns environ with the shell's variables added. The shell wins
// except for the variables skip lists. PATH is the shell's, followed by the
// entries of environ's PATH the shell did not have.
func Merge(environ []string, shell map[string]string) []string {
	have := map[string]int{}
	out := append([]string(nil), environ...)
	for i, kv := range out {
		k, _, _ := strings.Cut(kv, "=")
		have[k] = i
	}
	for k, v := range shell {
		if skip(k) {
			continue
		}
		if k == "PATH" {
			old := ""
			if i, ok := have[k]; ok {
				_, old, _ = strings.Cut(out[i], "=")
			}
			v = JoinPath(v, old)
		}
		if i, ok := have[k]; ok {
			out[i] = k + "=" + v
		} else {
			have[k] = len(out)
			out = append(out, k+"="+v)
		}
	}
	return out
}

// JoinPath joins PATH lists, dropping empty and repeated entries.
func JoinPath(lists ...string) string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, e := range filepath.SplitList(l) {
			if e != "" && !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// Apply reads the login shell and puts its environment into this process,
// so every agent, tool and lookup the gateway starts sees it. On failure
// the environment is left as it is.
func Apply(ctx context.Context) {
	if os.Getenv(DisableEnv) == "0" {
		return
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
		if _, err := os.Stat(shell); err != nil {
			shell = "/bin/bash"
		}
	}
	home, _ := os.UserHomeDir()
	start := time.Now()
	env, err := Read(ctx, shell, home, os.Environ())
	if err != nil {
		slog.Warn("login shell environment not loaded; agents get the gateway's PATH", "operation", "shellenv.read", "shell", shell, "error", err)
		return
	}
	before := os.Getenv("PATH")
	for _, kv := range Merge(os.Environ(), env) {
		k, v, _ := strings.Cut(kv, "=")
		if os.Getenv(k) != v {
			os.Setenv(k, v)
		}
	}
	slog.Info("login shell environment loaded", "operation", "shellenv.read", "shell", shell,
		"elapsed", time.Since(start).Round(time.Millisecond).String(),
		"pathEntries", len(filepath.SplitList(os.Getenv("PATH"))), "pathBefore", len(filepath.SplitList(before)))
}
