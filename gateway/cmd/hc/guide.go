package main

const guideText = `hc guide — supervising coding-agent sessions through Herdr Companion

WHAT YOU SEE
  Every Claude Code / Codex / OpenCode / Devin session running in Herdr on the Mac.
  A session is "claude:<uuid>"; type any unique prefix (8 chars is shown) or its pane id.
  Statuses: running · waiting_input (question) · waiting_approval (tool/plan approval)
            · completed · idle · failed · offline (not running; hc resume restarts it).
  "!" marks a session that is waiting for an answer. Your own session is marked (you)
  and is never reported as a change.

THE LOOP (for a resident agent)
  1. hc wait --timeout 9m        blocks until some session needs you: a question, an
                                 approval, or a turn that ended. Prints those sessions,
                                 their last line and any pending question WITH the
                                 exact answer command. Returns "nothing needs you yet"
                                 on timeout; just call it again.
                                 Give your shell tool a timeout above --timeout
                                 (Claude Code Bash: timeout 600000), or run it in the
                                 background and act when it exits.
  2. hc read ID                  what the session said since you last read it (tool calls
                                 folded into one line; --tools lists them).
  3. Act:
       hc answer ID 2            pick option 2 of the pending question (or a label, or
                                 free text: hc answer ID "use sqlite")
       hc answer ID approve      approvals: approve | approve_session | deny
       hc send ID "next step"    a new prompt (long text: pipe it and use "-")
  4. Back to 1.

  Prefer hc changes (non-blocking) when you check in on your own schedule.
  Delegate and wait in one step: hc send ID "run the tests" --wait --timeout 9m
  Start new work:               hc start --cwd ~/workspace/app "fix the login bug" --wait

READING IS INCREMENTAL
  hc read / hc changes / hc wait remember what you were shown (per cursor), so calling
  them again prints only what is new. --peek reads without moving the cursor.
  The cursor is per Herdr pane by default; set HC_CURSOR (or --cursor NAME) to share
  or separate cursors. Cursors live in ~/.local/state/herdr-mobile/hc.
  First read of a session shows the last 8 rows; --last N / --all for more.
  Cut text ends with the command that shows it in full (hc read ID --msg MSG).
  Whole transcript to a file you can grep: hc export ID   (prints the path)

WHEN THE STRUCTURED ANSWER IS NOT ENOUGH
  A question marked "not supported here" (or an odd dialog) must be handled in the
  terminal: hc term ID shows the screen, hc keys ID down enter / hc keys ID --text "y" enter
  types into it. Check the screen again after each step.

MACHINE-READABLE OUTPUT
  Add --json to any command for the Gateway's own objects (ids in full).

RULES OF THUMB
  - Read before you answer: the question may have been answered by the user meanwhile;
    hc answer refuses when nothing is pending.
  - Do not send prompts to a session the user is driving unless asked to.
  - Archiving (hc archive) stops the agent and closes its pane.
`
