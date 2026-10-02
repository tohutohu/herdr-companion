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

const usageText = `hc — Herdr Companion CLI for agents (run "hc guide" first)

Sessions:
  hc ls [--archived]                  sessions, one line each ("!" = needs you)
  hc show ID                          status, pending question with answer hint, last report
  hc read ID [--all|--last N] [--tools] [--full] [--peek]
                                      messages new since your last read of ID
  hc read ID --msg MSG                one message in full
  hc export ID [--out FILE]           whole transcript as Markdown to a file; prints the path
Changes:
  hc changes [--peek]                 what changed in any session since your last call
  hc wait [ID...] [--timeout 110s] [--any]
                                      block until a session needs you (or IDs finish)
Acting:
  hc send ID TEXT... | -              send a prompt ("-" reads stdin) [--file F] [--wait]
  hc answer ID [ANSWER...]            answer the pending question/approval (see "hc show")
  hc start --cwd DIR [--provider claude] [--model M] [--effort E] [--mode M]
           [--worktree] [--no-trust] [--wait] PROMPT... | -
  hc term ID [--lines N]              raw terminal screen (fallback)
  hc keys ID [--text T] KEY...        type into the terminal (enter esc tab up down y n 1-9 ...)
  hc mode ID                          cycle the agent's mode (Plan, Accept edits, ...)
  hc archive ID... | hc resume ID     stop and archive / restart in a new pane
Other:
  hc usage                            subscription limits
  hc guide                            how to use hc as a resident agent

ID: any unique prefix of the session id, a full id (claude:…), or a Herdr pane id.
Global flags (anywhere): --json  --url URL  --token TOKEN  --cursor NAME
Environment: HC_URL, HC_TOKEN, HC_CURSOR, HC_STATE_DIR. Without HC_URL/HC_TOKEN the
local Gateway's config is used (HERDR_MOBILE_CONFIG or ~/.config/herdr-mobile/...).
`

// errUsage marks a mistake in the command line; it exits with 2.
var errUsage = errors.New("usage")

type usageError struct{ msg string }

func (e usageError) Error() string   { return e.msg }
func (e usageError) Is(t error) bool { return t == errUsage }

func usagef(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

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
	// selfPane is the Herdr pane this process runs in. The session in it is
	// the caller itself and never reported as a change.
	selfPane string
}

func main() {
	a := &app{out: os.Stdout, errOut: os.Stderr, in: os.Stdin}
	os.Exit(a.run(os.Args[1:]))
}

func (a *app) run(args []string) int {
	args, g, err := splitGlobal(args)
	if err == nil && len(args) == 0 {
		fmt.Fprint(a.out, usageText)
		return 0
	}
	if err == nil {
		err = a.dispatch(args[0], args[1:], g)
	}
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errUsage):
		fmt.Fprintln(a.errOut, "error:", err)
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
		fmt.Fprint(a.out, usageText)
		return nil
	case "guide":
		fmt.Fprint(a.out, guideText)
		return nil
	}
	cmds := map[string]func([]string) error{
		"ls":      a.cmdList,
		"show":    a.cmdShow,
		"read":    a.cmdRead,
		"export":  a.cmdExport,
		"changes": a.cmdChanges,
		"wait":    a.cmdWait,
		"send":    a.cmdSend,
		"answer":  a.cmdAnswer,
		"start":   a.cmdStart,
		"term":    a.cmdTerm,
		"keys":    a.cmdKeys,
		"mode":    a.cmdMode,
		"archive": a.cmdArchive,
		"resume":  a.cmdResume,
		"usage":   a.cmdUsage,
	}
	run, ok := cmds[cmd]
	if !ok {
		return usagef("unknown command %q", cmd)
	}
	if err := a.setup(g); err != nil {
		return err
	}
	return run(args)
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
	if a.selfPane == "" {
		a.selfPane = os.Getenv("HERDR_PANE_ID")
	}
	a.cursor = g.cursor
	if a.cursor == "" {
		a.cursor = os.Getenv("HC_CURSOR")
	}
	if a.cursor == "" {
		a.cursor = defaultCursor(a.selfPane)
	}
	return nil
}

// defaultCursor keeps agents in different panes from taking each other's
// changes: each pane reads with its own cursor unless told otherwise.
func defaultCursor(pane string) string {
	if pane == "" {
		return "default"
	}
	return "pane-" + pane
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
