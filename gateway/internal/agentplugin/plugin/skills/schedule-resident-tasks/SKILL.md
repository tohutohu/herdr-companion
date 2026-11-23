---
name: schedule-resident-tasks
description: Recurring runs, monitoring and checking again later can really be set up from this session as resident tasks (hc resident). Read before saying it cannot be done, and before using cron, launchd, /loop or /schedule. Use for "every morning", "every hour", "tell me when something new comes in", "check again later", i.e. checks done while the user is away that report only when needed. Also for changing, stopping or debugging an existing resident task. Not for cron syntax or editing CI schedules.
---

# Creating a resident task

Each run is a fresh conversation. The Gateway puts the memory, the previous result and the result JSON format into the prompt, so the instruction only says what to check, what counts as a change and what to report.

1. Check `hc resident ls` for a task with the same purpose.
2. Write the instruction to a file, in the user's language:

   ```text
   Check main's CI with `gh run list --branch main --limit 5`.
   Only when there is a failure newer than last time, report the failed job and a guess at the cause.
   ```

3. Create it. Routine checks are fine on a small model:

   ```bash
   hc resident preview --weekly mon,thu@07:00          # shows the next 3 runs
   hc resident create --name ci-watch --provider claude --model haiku --cwd DIR \
     --instruction-file task.md --weekly mon,thu@07:00
   ```

4. Try it once before finishing, and tell the user the result and the next run:

   ```bash
   hc resident run ci-watch     # → queued run RUN. No command waits for a run to end
   hc resident log RUN          # status, result, denied operations, warnings; while running, look again after a while
   ```

Schedules: `--daily HH:MM`, `--weekly`, `--cron`, one-off `--once RFC3339`, manual-only `--manual`. Everything else: `hc help resident`.

## Pitfalls

- Keep the default permissions. Add `--profile read_only`, narrow `--allow` rules or prohibitions in the instruction ("do not edit") only when the user asks. Add an `--allow` only for an operation `hc resident log` reports as denied.
- Pass `--name`. Without it the first line of the instruction becomes the name, which is awkward in later commands.
- Automatic runs are capped at 12 a day by default. For schedules more frequent than every 2 hours, raise `--max-runs-per-day` too.
- For frequent watching, filter cheaply with `--precheck 'CMD'`: exit 0 starts the agent (stdout goes into the prompt), exit 1 skips the run, anything else is a failure. The memory JSON arrives on stdin. CMD runs outside the sandbox.
- A folder the agent has not trusted needs `--trust`. The project's hooks and MCP servers then run unattended: make sure that is within what the user asked. Folders must be inside workspaceRoots.
- A scheduled run that finds no change shows nothing in the conversation or notifications. When no report comes, check `hc resident runs TASK`.
- A run waiting for a decision: `hc resident answer TASK "answer"`. To talk about a report, `hc resident promote RUN` turns it into a session.
- A run cut off by a Gateway restart is not resent and becomes `needs_reconciliation`. Check the situation, then `hc resident dismiss RUN`.
- A task that watches other sessions (e.g. reads PR comments and has them answered) should say in its instruction: "Read each session's notes with `hc note ls` and follow what they say (answer reviews, wait, archive after merge). Remove handled notes with `hc note rm`." How a session is handled then changes through its notes, not the task's instruction.
