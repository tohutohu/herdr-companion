package main

const guideText = `hc guide — playbook for supervising coding-agent sessions through Herdr Companion

PURPOSE
  You are an agent that delegates to, watches or answers other agent sessions (Claude Code,
  Codex) on the Mac. The Gateway runs these sessions itself. hc does the polling and the
  diffing, so each call prints only what you have to act on, plus the exact command for the
  next step.

BEFORE YOU START
  hc ls                     sessions and their short IDs ("!" = waiting for an answer;
                            "(you)" = your own session, which hc never acts on)
  Long waits: give your shell tool a timeout above hc's --timeout (Claude Code Bash:
  timeout 600000 with --timeout 9m), or run the wait in the background.  hc help timeouts

WORKFLOW 1 — delegate a task and collect the result
  hc send ID "run the tests and fix failures"
  hc until ID --timeout 9m          returns when it asks something, is done or fails
  hc read ID                        what it said (only what is new to you)
  One step for short tasks:  hc send ID "..." --wait --timeout 9m

WORKFLOW 2 — start new work
  hc start --cwd ~/workspace/app "fix the login bug"
  hc until ID --timeout 9m   →  hc read ID
  The id is printed at once ("started claude:<id>").
  Add --worktree for a separate checkout, --mode plan to review a plan first.

WORKFLOW 3 — fan out and join
  hc start --cwd DIR "part A"  ·  hc start --cwd DIR "part B"  ·  ...
  hc until A B --all --for done --timeout 9m     all finished
  hc until A B --for needs-you                   the first one that asks something

WORKFLOW 4 — resident supervisor (watch every session)
  1. hc wait --timeout 9m     blocks until any session needs you: a question, an approval,
                              a turn that ended, a failure. Prints the pending question with
                              its answer command. "nothing needs you yet": call it again.
  2. hc read ID               what that session said since you last read it
  3. act (workflow 5, or hc send ID "next step")
  4. back to 1
  Check in on your own schedule instead: hc changes (does not block).

WORKFLOW 5 — answer a question or an approval
  hc show ID                  the pending question, its options, and "answer: …"
  hc answer ID 2              option 2 (or a label, or free text: hc answer ID "use sqlite")
  hc answer ID approve        approvals: approve | approve_session | deny
  Add --wait to see what the agent does with the answer.

WORKFLOW 6 — steer a running session
  hc model ID sonnet          another model for the rest of the session (same id, same
  hc effort ID high           history); Codex applies it from the next turn, Claude Code
                              from its next request. hc show marks it pending until then.
  hc mode ID plan             set a mode (hc mode ID cycles)
  hc interrupt ID             stop the running turn; the session stays

WORKFLOW 7 — when the structured answer is not enough
  A question marked "not supported here", or a stuck agent:
  hc interrupt ID             stop the turn, then hc send ID with new instructions

RULES OF THUMB
  - Read before you answer: the user may have answered meanwhile (hc answer refuses when
    nothing is pending).
  - Do not send prompts to a session the user is driving unless asked to.
  - hc archive stops the agent; it is for finished work.
  - Long transcripts: hc export ID writes Markdown to a file you can grep.
  - Scripts and exact ids: add --json to any command.

GOING DEEPER
  hc help                     command index and topics
  hc help COMMAND             one command: when to use it, flags, output, examples
  hc help states | ids | cursors | timeouts | connect | output
`
