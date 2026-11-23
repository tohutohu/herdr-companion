---
name: drive-sessions
description: From this session, start, instruct and watch other Claude Code, Codex or DeepSeek Harness sessions on this Mac with the hc CLI, and answer their questions and approvals. Use for "do it in another session", "hand it to another agent", "how is that session doing", "answer the waiting question", "leave a note on that session". Not for subagents within this conversation. For recurring work, see schedule-resident-tasks.
---

# Driving other sessions

```bash
hc ls                                  # list; "!" waits for an answer, "(you)" is this session
hc fav                                 # the user's favorite projects (number, folder name, path)
hc start --cwd DIR "instruction"       # → started claude:<id>. DIR may be a favorite's folder name. --provider codex|dsh; --model haiku for light work
hc until ID --timeout 9m               # wait for a question, approval, end of turn or failure
hc read ID                             # read what is new
hc send ID "next instruction"
hc show ID                             # pending questions and ready-to-run answer commands
hc answer ID 2                         # a number, label or free text; approvals take approve | deny
```

A short ID prefix is enough. For starting and joining sessions in parallel and watching every session, read `hc guide`. Do not guess flags: check `hc help COMMAND`.

## Pitfalls

- Mind the shell timeout while hc waits. Match `--timeout 9m` with a Bash `timeout: 600000`, or run it in the background.
- `hc until` timing out exits with code 3 and is not a failure. Keep waiting with the printed `continue:` command. It is fine right after `hc send` (the idle state before work starts is not taken for the end).
- `completed` can still have subagents or commands running behind it. `[N in background]` in `hc ls` shows it, and a new turn starts by itself when they finish. To wait for their result, `hc until ID --for settled`.
- Each `hc read` moves the read position, so nothing is shown twice. `--all` for everything, `--peek` to leave the position.
- Without `--cwd` the session starts in the current directory. Always pass it for another project; if unsure which, pick from `hc fav`. Adding or removing favorites (`hc fav add` / `rm`) changes the user's list: only when asked.
- Look at `hc show` before answering: the user may have answered in the app already.
- Do not send to a session the user is working in unless asked. `hc archive` stops the agent; use it only for finished work.
- A question that answers `not supported here`: stop with `hc interrupt ID`, then instruct again with `hc send`.
- `note n…(…): …` lines shown in full under a session are instructions the user or other agents left for whoever watches it ("have it answer the review", "archive once merged"). Follow them before touching that session. Leave one with `hc note add ID "…"`; once handled, `hc note rm NOTE`. Notes do not reach that session's agent (to note your own wait, see session-notes).
- Operations on your own session are refused. Resident task runs do not appear in `hc ls`.
- Without hc, install it: `curl -fsSL -o ~/.local/bin/hc "https://github.com/tohutohu/herdr-companion/releases/latest/download/hc-darwin-$(uname -m | sed s/x86_64/amd64/)" && chmod +x ~/.local/bin/hc`
