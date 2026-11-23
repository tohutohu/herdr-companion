---
name: session-notes
description: Leave a note on this session itself (hc note add self); the user and watching agents read it to decide when to resume or archive it. Read before ending a turn that waits on a review, a merge, or a reply or decision from others or the user, and before holding on with sleep or a loop. Read first when asked to "note what you are waiting for" or "write that it can be archived once merged", and on "continue" to check what you were waiting for. Notes on other sessions: drive-sessions.
---

# Leaving a note on your own session

`hc note add self "…"` leaves a note on this session. The Gateway keeps it; the user reads it in full right above the composer in the app, and watching agents in the output of `hc ls` / `hc wait`. A watcher looks only at the note: when its conditions are met it sends the instruction to resume, and archives the session if allowed. Notes are not delivered back to you automatically.

## When to write one

- Ending a turn to wait for an outside event: someone else's review, a PR merge, a reply or decision from another team or the user, a deploy, a set date and time.
- A wait that may take hours or days: write a note and end the turn instead of holding on with sleep, a loop or `hc wait`. Waiting a few minutes for CI in place is fine.
- Finished work with a conditional cleanup ("archive once merged", "leave it as is").
- The user has to check, try or approve something before you can continue (e.g. mark a PR ready for review).
- Not for: work completed on the spot, repeating the final reply, or a question the user will answer right away in this conversation.

## What to write

The watcher does not read this conversation. Be specific enough to decide from the note alone:

1. Waiting for: what you wait for.
2. Resume when: a checkable condition. A PR URL or `owner/repo#number`, whose review, the CI workflow name, a date and time.
3. Then send: the one sentence to send when the condition is met, ready to use with `hc send`.
4. Cleanup: what is allowed ("archive once merged", "fine to leave").
5. Do not: anything you do not want ("do not resume before Bob replies").

```text
Waiting for: alice's review of owner/app#123.
Resume when: review comments arrive; then send "Address the review comments on #123 and push".
Cleanup: archive once it is approved and merged.
```

Bad: "waiting for review" (which PR, whose, and what to do then are missing).

- Write the note in the user's language, briefly. One matter per note; separate matters get separate notes.
- Give conditions that go stale on their own an expiry, like `--ttl 3d`.
- Never write secrets such as tokens or passwords: the user and other agents read notes.

## Commands

```bash
hc note add self "…"           # TEXT "-" reads several lines from stdin. Expiry: --ttl 90m / 12h / 3d
hc note ls self                # your notes
hc note edit NOTE "…"          # rewrite when things change; --ttl D / --no-expiry
hc note rm NOTE                # remove once settled
```

## Pitfalls

- When resumed, read `hc note ls self` first. `rm` what is settled, `edit` what changed. Stale notes mislead watchers.
- If a note on the same matter exists, rewrite it instead of adding another.
- A note is an instruction, not a lock. The user or a watcher may have acted already.
- A work log the project keeps (e.g. `tasks/<id>/log.md`) does not replace the note: watchers read only notes.
- Mention in your final reply that you left a note and what you wait for.
- `"self" needs HC_SESSION_ID` means you run outside a Gateway session; `update the Gateway` means a Gateway without notes. In both cases skip the note and put the wait in your final reply.
- Reading or leaving notes on other sessions belongs to drive-sessions.
