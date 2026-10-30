package main

import (
	"fmt"
	"sort"
	"strings"
)

// Help comes in layers so a reader loads only what it needs: "hc help" says
// what hc is for and indexes everything, "hc help COMMAND" (or "hc COMMAND
// --help") is one command's reference, "hc help TOPIC" explains one idea,
// and "hc guide" is the playbook for running as a supervising agent.

const overviewText = `hc — watch, answer and drive coding-agent sessions on the Mac (Claude Code, Codex)
     through the Herdr Companion Gateway, which runs these sessions itself.

USE IT WHEN
  - a session may be asking a question or waiting for a tool/plan approval
  - you hand work to a session and need to know when it is done, then read the result
  - you start new agent sessions and collect what they did
NOT FOR
  - your own session (hc never reports it and refuses to send to it)
  - a session the user is driving, unless the user asked you to step in

QUICK START
  hc ls                          what is running; "!" = waiting for an answer
  hc send ID "run the tests"     hand over work
  hc until ID --timeout 9m       block until ID needs input, is done or fails
  hc read ID                     what it said since you last read it
  hc answer ID 2                 answer its question (the exact command is printed)
  ID is the short id hc ls prints; any unique prefix works (hc help ids).

COMMANDS                         details: hc help COMMAND  (or hc COMMAND --help)
%s
TOPICS                           hc help TOPIC
%s
NEXT
  Supervising sessions as a resident agent: hc guide
  Machine-readable output: add --json to any command.
`

type helpPage struct {
	name    string
	summary string
	body    string
}

// commandGroups orders the command index in "hc help".
var commandGroups = []struct {
	title string
	names []string
}{
	{"Look", []string{"ls", "show", "read", "export"}},
	{"Wait", []string{"until", "wait", "changes"}},
	{"Act", []string{"send", "answer", "start", "archive", "resume"}},
	{"Settings", []string{"model", "effort", "mode", "interrupt"}},
	{"Other", []string{"usage", "guide", "version"}},
}

var commandHelp = []helpPage{
	{"ls", "list sessions, one line each (\"!\" = needs you)", `hc ls — list sessions, one line each

USE WHEN
  You need the session IDs, or a quick look at what is running and what waits for you.

USAGE
  hc ls [--all | --archived]

FLAGS
  --all        include sessions that are not running (offline); they are only counted otherwise
  --archived   list archived sessions instead

OUTPUT
  ! a9b7233a waiting_input      now  claude proj · 色の選択ツール
    5b27a61d running            now  claude herdr-android-client · CLI設計  (you)
  (+47 offline: hc ls --all)
  Columns: "!" when it waits for an answer, short ID, status, last update, provider,
  project · title. "(you)" is the session hc runs in.

SEE ALSO
  hc show ID · hc help ids · hc help states
`},
	{"show", "one session: status, pending question with answer command, last report", `hc show — one session at a glance

USE WHEN
  Before answering or sending: what the session is doing, what it asks, what it last
  reported. Does not move your read cursor.

USAGE
  hc show ID [--full]

FLAGS
  --full   do not shorten long text

OUTPUT
  id and status; project, cwd and title; model, mode, context use and cost;
  each pending question or approval with the exact "answer:" command;
  "last report:" the agent's latest prose; "now:" the tool calls of a running turn.

SEE ALSO
  hc read ID · hc answer ID
`},
	{"read", "messages new since your last read of a session", `hc read — what a session said since you last read it

USE WHEN
  A session finished, asked something, or you want its progress. Calling it again prints
  only what is new (see "hc help cursors").

USAGE
  hc read ID [--all | --last N] [--tools] [--full] [--max N] [--peek]
  hc read ID --msg MSG

FLAGS
  --all       the whole transcript
  --last N    the last N rows, whatever was read before (first read shows the last 8)
  --tools     list every tool call and its output instead of folding them into one line
  --full      do not shorten long text (same as --max 0)
  --max N     characters per text block before it is cut (default 2000)
  --peek      do not move the read cursor
  --msg MSG   one message in full (MSG is the id prefix printed after "──")

OUTPUT
  a9b7 idle · proj · 色の選択ツール
     ⋯ 7 tool calls, latest: Write hello.txt
  ── assistant 17:15 · c36a1f47
  hello.txt ファイルを作成しました。
  Cut text ends with the command that prints it in full. Questions still pending from
  before the new rows are repeated at the end.

SEE ALSO
  hc export ID (whole transcript to a file) · hc help cursors
`},
	{"export", "whole transcript as Markdown to a file; prints the path", `hc export — write a whole transcript to a Markdown file

USE WHEN
  The conversation is long and you want to grep it or read parts of it with your file tools
  instead of printing it.

USAGE
  hc export ID [--out FILE]

FLAGS
  --out FILE   where to write (default: exports/ in the hc state directory; rewritten each time)

OUTPUT
  The path, then the message and byte counts.
`},
	{"until", "block until sessions reach a state: needs input, done, failed ...", `hc until — block until sessions reach a state

USE WHEN
  You handed work to a session (hc send / hc start, or work the user started) and want to
  continue when it asks something or finishes. Use it for the sessions you care about;
  to be told about any session that needs you, use hc wait.

USAGE
  hc until ID... [--for STATE[,STATE...]] [--all] [--timeout 110s]

STATES  (--for; default: stopped)
  input       a question is pending (waiting_input)
  approval    a tool or plan approval is pending (waiting_approval)
  needs-you   input or approval
  done        the turn is over (completed or idle)
  failed      the agent reported an error
  offline     the agent is not running, or the session was archived
  stopped     anything but running: needs-you, done, failed or offline
  running     the agent is working (e.g. a queued prompt has started)
  A raw status (waiting_input, idle, ...) matches exactly. See "hc help states".

FLAGS
  --for S     states to wait for; comma separated or repeated
  --all       with several IDs, return when all are there (default: when the first is)
  --timeout D give up after D (default 110s; see "hc help timeouts")

BEHAVIOR
  Returns at once if a session is already there ("already there:"). It checks the status,
  not changes, so it is safe to call right after hc send: an idle session whose newest
  message is a prompt under a minute old has not started that turn and is not done.

OUTPUT
  Each session's line. One that got there also shows its pending question with the exact
  hc answer command, or its last message; then "next: hc read ID".
  Exit status: 0 reached, 3 timed out (prints where each session is and "continue: …").

EXAMPLES
  hc send a9b7 "run the tests" && hc until a9b7 --timeout 9m
  hc until a9b7 --for needs-you              only stop for a question or an approval
  hc until a9b7 c3d4 e5f6 --all --for done   join work fanned out to three sessions

SEE ALSO
  hc wait · hc send --wait · hc help states
`},
	{"wait", "block until any session needs you (a change since your last look)", `hc wait — block until something changes that needs you

USE WHEN
  You supervise all sessions: it returns when any session (other than yours) asks a
  question, waits for approval, ends a turn, fails or stops, since your last
  hc wait / hc changes. To wait for given sessions to reach a state, use hc until.

USAGE
  hc wait [ID...] [--any] [--timeout 110s]

FLAGS
  ID...        only these sessions; changes in others stay for your next wait/changes.
               A given session already waiting for an answer returns at once.
  --any        return on any change, not only those that need you
  --timeout D  give up after D (default 110s; see "hc help timeouts")

OUTPUT
  ! a9b7233a idle → waiting_approval  proj · 色の選択ツール
      [approval PENDING] Allow Write?
        answer: hc answer a9b7233a approve | deny
  next: hc read a9b7233a   (or hc show ID)
  On timeout (exit 0): "nothing needs you yet …" and the sessions still running.
  The first call for a cursor saves a baseline and reports what already waits for you.

SEE ALSO
  hc until · hc changes · hc help cursors · hc guide
`},
	{"changes", "what changed in any session since your last call (does not block)", `hc changes — what changed since your last look, without waiting

USE WHEN
  You check in on your own schedule instead of blocking in hc wait.

USAGE
  hc changes [--peek]

FLAGS
  --peek   show without moving the cursor

OUTPUT
  One line per change ("new", "A → B", "(new activity)", "gone"); "!" marks those that
  need you, with the last message. The first call saves a baseline.

SEE ALSO
  hc wait · hc help cursors
`},
	{"send", "send a prompt to a session", `hc send — send a prompt to a session

USE WHEN
  You hand a session its next task, or follow up on what it said. If the agent is busy the
  prompt is queued until its current turn ends.

USAGE
  hc send ID TEXT... [--file F]... [--wait [--timeout 110s]]
  hc send ID - < prompt.md            read the prompt from stdin (for long or quoted text)

FLAGS
  --file F     attach a local file (repeatable; uploaded, so it may be on another machine)
  --wait       wait until that turn ends, then print what the agent said (like hc read)
  --timeout D  how long --wait waits (default 110s)

OUTPUT
  "sent to ID" and how to follow up (hc until ID, then hc read ID).
  Refuses to send to your own session.

EXAMPLES
  hc send a9b7 "run the tests and fix failures" --wait --timeout 9m
  hc send a9b7 - <<'EOF'
  multi-line prompt
  EOF

SEE ALSO
  hc until · hc answer (to reply to a pending question, not send)
`},
	{"answer", "answer the pending question or approval", `hc answer — answer a session's pending question or approval

USE WHEN
  hc show / hc wait / hc until printed "[question PENDING]" or "[approval PENDING]".
  The exact command is printed under it as "answer: …".

USAGE
  hc answer ID ANSWER... [--ia IA] [--wait [--timeout 110s]]

ANSWERS
  approvals        approve | approve_session | deny  (also: yes, y, no, n, always)
  one question     an option number (2), a label ("SQLite"), or free text when allowed;
                   several choices for multi-select: 1,3
  several          QID=answer per question: q1=2 q2="use sqlite"
  The answer is checked against the options before it is sent; a mismatch prints them.

FLAGS
  --ia IA      which pending interaction (id prefix) when there are several
  --wait       wait until the turn ends and print what the agent said
  --timeout D  how long --wait waits (default 110s)

NOT SUPPORTED HERE
  Some requests cannot be answered through the Gateway ("answer: not supported here").
  Interrupt the turn (hc interrupt ID) and send new instructions instead.

SEE ALSO
  hc show ID · hc interrupt
`},
	{"start", "start a new agent session with a prompt", `hc start — start a new agent session

USE WHEN
  New work needs its own agent: a separate task, a parallel attempt, a fresh context.

USAGE
  hc start --cwd DIR [--provider claude] [--model M] [--effort E] [--mode M]
           [--worktree] [--no-trust] [--file F]... [--wait [--timeout 110s]] PROMPT... | -

FLAGS
  --cwd DIR      working directory on the Gateway's Mac (default: current directory; required
                 when hc talks to a Gateway on another machine)
  --provider P   claude (default) or codex
  --model M      model id (as in the app); --effort E reasoning effort id
  --mode M       start mode id, e.g. plan
  --worktree     start in a new git worktree of DIR
  --no-trust     do not trust a folder the agent was never trusted in (the start is then
                 not made)
  --file F       attach a local file to the first prompt (repeatable)
  --wait         wait for the first turn to end and print what the agent said
  --timeout D    how long --wait waits (default 110s)

OUTPUT
  "started claude:<id>" — the id is known at once.

EXAMPLES
  hc start --cwd ~/workspace/app "fix the login bug" --wait --timeout 9m
  hc start --cwd ~/workspace/app --worktree --mode plan - < task.md

SEE ALSO
  hc until · hc archive
`},
	{"archive", "stop sessions", `hc archive — stop sessions

USE WHEN
  A session's work is finished and it should no longer run. This stops the agent; ask first
  if the user may still be using it. Refuses to archive your own session. A worktree the
  Gateway made for it is removed unless it has uncommitted or untracked changes.

USAGE
  hc archive ID...

SEE ALSO
  hc resume · hc ls --archived
`},
	{"resume", "restart a stopped or archived session under its id", `hc resume — restart a session under its id

USE WHEN
  A session is offline or archived and you need it to continue. Works for sessions started
  anywhere, also in a plain terminal (claude --resume / codex resume elsewhere at the same
  time would write the same history twice; don't). A session the Gateway still lists as
  running needs no resume: hc send restarts its process.

USAGE
  hc resume ID

OUTPUT
  "resumed ID". Then send it a prompt.
`},
	{"mode", "cycle or set the agent's mode (Plan, Accept edits, ...)", `hc mode — cycle or set the agent's mode

USE WHEN
  The session should switch between its modes (Claude Code: default, Accept edits, Plan, …;
  Codex: default, plan). Without MODE each call moves one step; it prints the mode before and
  after. MODE (a mode id, e.g. plan, acceptEdits, manual) sets it directly. Codex applies a
  mode from its next turn.

USAGE
  hc mode ID [MODE]
`},
	{"model", "change a running session's model (same session, history kept)", `hc model — change a session's model

USE WHEN
  A session should continue on another model, e.g. a stronger one for a hard step or a
  cheaper one for routine work. The session keeps its id and history.

USAGE
  hc model ID MODEL

  MODEL is a model id as hc start --model takes it (claude: fable, opus, sonnet, haiku;
  codex: an id from its catalog). An unknown id is refused with the list of valid ones;
  nothing else is picked instead.

WHEN IT APPLIES
  Claude Code: from its next model request, which can be inside the running turn.
  Codex: from the next turn; a running turn keeps its model.
  Until then hc show lists it as pending.

SEE ALSO
  hc effort · hc show
`},
	{"effort", "change a running session's reasoning effort", `hc effort — change a session's reasoning effort

USAGE
  hc effort ID LEVEL

  LEVEL is an effort the session's model offers (low, medium, high, xhigh, max, ...). One
  the model does not offer is refused with the valid ones. Applies like hc model.

EXAMPLES
  hc effort a9b7 high
`},
	{"interrupt", "stop the running turn of a session (it stays open)", `hc interrupt — stop the running turn

USE WHEN
  A session is going the wrong way. The turn stops; the session stays and
  takes the next message. Refuses to interrupt your own session.

USAGE
  hc interrupt ID
`},
	{"usage", "subscription limits of the agents", `hc usage — subscription limits

USE WHEN
  Before starting heavy work: how much of each provider's session/weekly limit is used.

USAGE
  hc usage

OUTPUT
  One line per provider: each window's use in percent and when it resets.
`},
	{"guide", "playbook for supervising sessions as a resident agent", `hc guide — playbook for supervising sessions

USAGE
  hc guide

Prints the workflows (delegate, supervise, fan out, answer, steer) and the
rules of thumb. Read it once before working with hc for a long time.
`},
	{"version", "the release this hc was built for", `hc version — the release this hc was built for

USAGE
  hc version
`},
}

var topicHelp = []helpPage{
	{"states", "session statuses, \"needs you\", and when a turn counts as done", `hc help states — session statuses

STATUSES
  running            the agent is working on a turn
  waiting_input      it asked a question (hc answer)                    "!" needs you
  waiting_approval   it waits for a tool or plan approval (hc answer)   "!" needs you
  completed          the turn ended
  idle               nothing to do; a finished turn becomes idle once someone looked at
                     it (the session was opened in the app)
  failed             the agent reported an error, or its process stopped during the turn
  offline            the Gateway does not run the session (hc resume restarts it). A
                     running session whose process stopped is not offline: hc send
                     restarts it.

WHAT NEEDS YOU
  hc wait / hc changes report a session that moved to waiting_input, waiting_approval,
  completed or failed; that went from running to idle or offline; or that is newer with
  the same status (a whole turn ran between two looks).

WHAT IS DONE
  hc until --for done: completed or idle, except right after a prompt was sent (an idle
  session whose newest message is a prompt under a minute old has not started yet).
  hc send/answer/start --wait: the turn after the action ended (seen running, or a newer
  outcome, or a new reply from the agent).
`},
	{"ids", "how to name a session", `hc help ids — naming a session

  Any of these works wherever a command takes ID:
    a unique prefix of the session id     a9b7  (hc shows the shortest unique one, 8+ chars)
    the full id                           claude:a9b7233a-…  or the part after "claude:"
  An ambiguous prefix lists the sessions it matches. Archived sessions are found too.
`},
	{"cursors", "incremental reads, per-session cursors, --peek", `hc help cursors — reading only what is new

  hc read, hc changes and hc wait remember what they showed you, per cursor, so the next
  call prints only what is new. hc until moves the cursor for the sessions it reported.
  --peek (read, changes) shows without moving it.

  The cursor is per session by default (HC_SESSION_ID, which the Gateway sets for the
  sessions it runs, or the agent's own CLAUDE_CODE_SESSION_ID / CODEX_THREAD_ID), so agents
  do not take each other's changes. --cursor NAME or
  HC_CURSOR shares or separates them. The same variables tell hc which session is you.
  State lives in ~/.local/state/herdr-mobile/hc/<cursor>/ (HC_STATE_DIR to move it).

  The first read of a session shows its last 8 rows; --last N or --all for more.
`},
	{"timeouts", "long waits and your shell tool's own timeout", `hc help timeouts — long waits

  hc wait, hc until and --wait default to --timeout 110s, which fits the 2-minute
  default timeout of Claude Code's Bash tool. For longer waits either
    - raise both: hc until ID --timeout 9m with the Bash tool's timeout set to 600000, or
    - run the command in the background and act when it exits.
  A timeout is not an error for hc wait (exit 0, "nothing needs you yet"); hc until exits
  with 3 and prints the command that continues the wait. Calling again is always safe.
`},
	{"connect", "Gateway URL and token, environment, other machines", `hc help connect — reaching the Gateway

  On the Gateway's Mac hc reads the URL and token from the Gateway config
  (HERDR_MOBILE_CONFIG, else ~/.config/herdr-mobile/desktop/config.json).
  Elsewhere set HC_URL (Tailscale http://100.99.15.34:8765 or the Cloudflare Tunnel URL)
  and HC_TOKEN, or pass --url / --token.
  From another machine hc start needs --cwd with a path on the Mac; --file uploads local
  files and hc export writes locally.

ENVIRONMENT
  HC_URL, HC_TOKEN   Gateway address and token
  HC_CURSOR          cursor name (see "hc help cursors")
  HC_STATE_DIR       where cursors and exports live
`},
	{"output", "text vs --json, cut text, exit statuses", `hc help output — what hc prints

  Text is short and meant for a model to read: one line per session, tool calls folded
  into "⋯ N tool calls, latest: …", long text cut with the command that prints it in full,
  and the exact command for the next step ("answer:", "next:", "continue:").
  --json (any command, anywhere on the line) prints the Gateway's own objects, ids in full.

EXIT STATUS
  0  done (a timeout of hc wait included)
  1  the request failed (the error is on stderr)
  2  the command line is wrong (stderr names the help page to read)
  3  hc until timed out
`},
}

func findHelp(pages []helpPage, name string) (helpPage, bool) {
	for _, p := range pages {
		if p.name == name {
			return p, true
		}
	}
	return helpPage{}, false
}

func overview() string {
	var cmds, topics strings.Builder
	for _, g := range commandGroups {
		fmt.Fprintf(&cmds, "  %s\n", g.title)
		for _, name := range g.names {
			p, _ := findHelp(commandHelp, name)
			fmt.Fprintf(&cmds, "    %-9s %s\n", name, p.summary)
		}
	}
	for _, p := range topicHelp {
		fmt.Fprintf(&topics, "  %-11s %s\n", p.name, p.summary)
	}
	return fmt.Sprintf(overviewText, cmds.String(), topics.String())
}

// helpFor is the page "hc help NAME" prints: a command, a topic, or the guide.
func helpFor(name string) (string, error) {
	if name == "guide" {
		return guideText, nil
	}
	if p, ok := findHelp(commandHelp, name); ok {
		return p.body, nil
	}
	if p, ok := findHelp(topicHelp, name); ok {
		return p.body, nil
	}
	var names []string
	for _, p := range append(append([]helpPage{}, commandHelp...), topicHelp...) {
		names = append(names, p.name)
	}
	sort.Strings(names)
	return "", usagef("no help for %q; try one of: %s", name, strings.Join(names, " "))
}

// wantsHelp reports whether a command's own arguments ask for its help page.
func wantsHelp(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--":
			return false
		case "-h", "-help", "--help":
			return true
		}
	}
	return false
}
