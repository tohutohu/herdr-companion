// Command hc is a Herdr Companion client made for AI agents that watch and
// drive other coding-agent sessions through the Gateway.
//
// Output is short plain text meant to be read by a model: one line per
// session, tool calls folded, long text cut with a note on how to get the
// rest, and the exact command to answer a pending question. Reads are
// incremental: a cursor per reader remembers what was already shown, so
// repeated calls print only what changed.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// version is set at release build time (-ldflags "-X main.version=1.3.0").
var version = "dev"

// errUsage marks a mistake in the command line; it exits with 2.
var errUsage = errors.New("usage")

type usageError struct{ msg string }

func (e usageError) Error() string   { return e.msg }
func (e usageError) Is(t error) bool { return t == errUsage }

func usagef(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

// exitStatus ends hc with a status other than 0 without printing an error:
// the command has already said what happened.
type exitStatus int

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// app is what every command gets: where to send requests, where to keep
// cursors and where to print.
type app struct {
	out    io.Writer
	errOut io.Writer
	in     io.Reader
	json   bool
	client *client
	state  *stateStore
	cursor string
	// selfID is the session that runs this process when the agent itself
	// reports it (HC_SESSION_ID from the Gateway, CLAUDE_CODE_SESSION_ID, or
	// CODEX_THREAD_ID). That session is the caller itself: never reported as
	// a change, never sent to, archived or waited for.
	selfID string
}

func main() {
	a := &app{out: os.Stdout, errOut: os.Stderr, in: os.Stdin}
	os.Exit(a.run(os.Args[1:]))
}

func (a *app) run(args []string) int {
	args, g, err := splitGlobal(args)
	if err == nil && len(args) == 0 {
		fmt.Fprint(a.out, overview())
		return 0
	}
	if err == nil {
		err = a.dispatch(args[0], args[1:], g)
	}
	var status exitStatus
	switch {
	case err == nil:
		return 0
	case errors.As(err, &status):
		return int(status)
	case errors.Is(err, errUsage):
		fmt.Fprintln(a.errOut, "error:", err)
		if len(args) > 0 {
			if _, ok := findHelp(commandHelp, args[0]); ok && args[0] != "help" {
				fmt.Fprintf(a.errOut, "run \"hc help %s\" for usage\n", args[0])
				return 2
			}
		}
		fmt.Fprintln(a.errOut, `run "hc help" for usage`)
		return 2
	default:
		fmt.Fprintln(a.errOut, "error:", err)
		return 1
	}
}

type globalFlags struct {
	json   bool
	url    string
	token  string
	cursor string
}

// splitGlobal takes the global flags out of args wherever they are, so
// "hc read abc --json" and "hc --json read abc" both work.
func splitGlobal(args []string) ([]string, globalFlags, error) {
	var g globalFlags
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if !strings.HasPrefix(arg, "-") {
			rest = append(rest, arg)
			continue
		}
		switch name {
		case "json":
			g.json = true
			continue
		case "url", "token", "cursor":
			if !hasValue {
				if i+1 >= len(args) {
					return nil, g, usagef("--%s needs a value", name)
				}
				i++
				value = args[i]
			}
			switch name {
			case "url":
				g.url = value
			case "token":
				g.token = value
			case "cursor":
				g.cursor = value
			}
			continue
		}
		rest = append(rest, arg)
	}
	return rest, g, nil
}

func (a *app) dispatch(cmd string, args []string, g globalFlags) error {
	switch cmd {
	case "help", "-h", "--help":
		if len(args) == 0 {
			fmt.Fprint(a.out, overview())
			return nil
		}
		page, err := helpFor(args[0])
		if err != nil {
			return err
		}
		fmt.Fprint(a.out, page)
		return nil
	case "guide":
		fmt.Fprint(a.out, guideText)
		return nil
	case "version", "--version", "-v":
		fmt.Fprintf(a.out, "hc %s\n", version)
		return nil
	}
	run, ok := a.commands()[cmd]
	if !ok {
		return usagef("unknown command %q", cmd)
	}
	if wantsHelp(args) {
		page, _ := helpFor(cmd)
		fmt.Fprint(a.out, page)
		return nil
	}
	if err := a.setup(g); err != nil {
		return err
	}
	return run(args)
}

// commands are the commands that talk to the Gateway.
func (a *app) commands() map[string]func([]string) error {
	return map[string]func([]string) error{
		"ls":        a.cmdList,
		"show":      a.cmdShow,
		"search":    a.cmdSearch,
		"read":      a.cmdRead,
		"export":    a.cmdExport,
		"changes":   a.cmdChanges,
		"wait":      a.cmdWait,
		"until":     a.cmdUntil,
		"send":      a.cmdSend,
		"answer":    a.cmdAnswer,
		"start":     a.cmdStart,
		"mode":      a.cmdMode,
		"model":     a.cmdModel,
		"effort":    a.cmdEffort,
		"interrupt": a.cmdInterrupt,
		"archive":   a.cmdArchive,
		"resume":    a.cmdResume,
		"usage":     a.cmdUsage,
		"resident":  a.cmdResident,
	}
}

func (a *app) setup(g globalFlags) error {
	a.json = g.json
	if a.client == nil {
		conn, err := resolveConnection(g.url, g.token)
		if err != nil {
			return err
		}
		a.client = newClient(conn)
	}
	if a.state == nil {
		a.state = &stateStore{dir: stateDir()}
	}
	if a.selfID == "" {
		a.selfID = selfSession(os.Getenv)
	}
	a.cursor = g.cursor
	if a.cursor == "" {
		a.cursor = os.Getenv("HC_CURSOR")
	}
	if a.cursor == "" {
		a.cursor = defaultCursor(a.selfID)
	}
	return nil
}

// selfSession is the gateway id of the session this process runs in, as
// far as its environment says: the Gateway sets HC_SESSION_ID for the
// sessions it runs; Claude Code and Codex export their own ids to the
// commands they run.
func selfSession(getenv func(string) string) string {
	if id := getenv("HC_SESSION_ID"); strings.Contains(id, ":") {
		return id
	}
	if id := getenv("CLAUDE_CODE_SESSION_ID"); id != "" {
		return "claude:" + id
	}
	if id := getenv("CODEX_THREAD_ID"); id != "" {
		return "codex:" + id
	}
	return ""
}

// defaultCursor keeps agents in different sessions from taking each other's
// changes: each reads with its own cursor unless told otherwise.
func defaultCursor(session string) string {
	if session != "" {
		return "session-" + session
	}
	return "default"
}

// parseFlags parses fs from args that may mix flags and positional
// arguments ("hc read abc --peek"). Everything after "--" is positional.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var tail []string
	for i, arg := range args {
		if arg == "--" {
			args, tail = args[:i], args[i+1:]
			break
		}
	}
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, usagef("%s: %v", fs.Name(), err)
		}
		args = fs.Args()
		if len(args) == 0 {
			return append(pos, tail...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}
