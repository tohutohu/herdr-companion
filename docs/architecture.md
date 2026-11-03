# Architecture

Herdr Companion lets an Android phone (and the Compose Desktop UI and the `hc` CLI) start,
follow and answer Claude Code and Codex sessions on a Mac. The Gateway runs every session
itself; it does not need or integrate with [Herdr](https://herdr.dev), which stays usable on its
own (agents can still call `herdr` from their tools).

```text
Android / Compose Desktop / hc
   │  HTTP(S) + Bearer token, over Tailscale, a trusted LAN, or a tunnel
   ▼
Herdr Companion Gateway (Go, on the Mac)
   ├─ Session manager (internal/sessions + internal/runtime)
   │    ├─ Claude ── herdr-claude-sidecar (Bun, Claude Agent SDK, stdio JSON lines)
   │    │            └─ the user's `claude` CLI, one query() per session
   │    └─ Codex ─── the gateway's own `codex app-server` (stdio JSON-RPC)
   ├─ Resident tasks (internal/resident) ── resident.db (SQLite)
   │    ├─ Claude ── a herdr-claude-sidecar per run (outputFormat; transcript kept for promotion)
   │    └─ Codex ─── a codex app-server per run (saved thread, outputSchema)
   ├─ Claude adapter ──── ~/.claude/projects/*/<session>.jsonl (history)
   ├─ Codex adapter ───── thread/read, thread/list on the same app-server, rollout files
   └─ FCM HTTP v1 ─────── data-only pushes on status transitions
```

**Removed in 2.0.** 1.x also followed sessions in Herdr panes and ran OpenCode and Devin there.
2.0 drops the Herdr client and everything built on panes (launching in Herdr workspaces, the
terminal endpoints, launch continue, moving sessions out of panes, pane folder-trust dialogs,
dialogs read off the screen, answers typed as keys, focus-based read state, the Herdr event
stream), the connection to Codex's shared app-server daemon, and the OpenCode and Devin
adapters. Their config keys (`herdrSocket`, `opencode`, `devin`, `codexDaemonSocket`) are
ignored. A session that ran in a Herdr pane is now an ordinary offline session; resume restarts
it in the gateway under the same id.

## Principles

- **Providers are the source of truth** for sessions. The gateway keeps no session,
  message, event or attachment database. Resident tasks are the exception by design:
  they are not sessions, and `resident.db` is their source of truth (see Resident tasks below). Every request reads the agents'
  own storage (Claude's transcript JSONL, Codex's rollouts through
  app-server) and the live runtime state, reusing bounded in-memory
  projections when their source is unchanged. Restarting the gateway loses
  nothing: its sessions are resumed in a new process under the same id when
  the next message arrives.
- **Persistent gateway state** is limited to `~/.config/herdr-mobile/config.json`
  (listen address, auth token, FCM device tokens, optional paths), the set of
  archived session ids (`~/.local/state/herdr-mobile/archive.json`), small
  message-delivery receipts (`message-delivery/`), and dead-letter / log files
  under `~/.local/state/herdr-mobile/`, plus `direct-sessions.json`: one small
  record per session the gateway runs (id, directory, requested model / effort /
  mode, whether a turn was running) and `worktrees/` for the worktrees it made.
  The menu-bar manager uses the separate
  `desktop/` config/state directories; `HERDR_MOBILE_STATE_DIR` selects the
  gateway state root. Delivery receipts contain hashes and status, never text.
  `resident.db` holds the resident tasks (settings, conversation, memory, wake-ups,
  runs, notification outbox).
- **In-memory only:** last seen status and completion revision per session
  (push de-duplication), bounded Codex display and rollout projections,
  the FCM access token, and the pending approvals and questions of running
  sessions (they end with their process).
- **Android caches persistently.** Room is the UI's read source. Gateway
  transcript/history projections are disposable and can be reconstructed.

## Identity

Sessions are `claude:<session-id>` and `codex:<thread-id>`. A Claude session gets its id from
the gateway before it starts (`sessionId` option, a UUID), a Codex thread from `thread/start`,
so a start returns the id at once. Sessions started elsewhere (a terminal, or a Herdr pane in
1.x) carry the same ids, so they are listed as offline and resume under that id.

## Runtime

`internal/runtime.Manager` tracks the sessions the gateway runs. Providers that implement
`providers.DirectRuntime` own the processes (start/resume, stop, interrupt, model, effort, mode);
`Send` and `Respond` take the session's `Live`. The manager maps their state to `Live.State`
(`working`, `blocked`, `done`, `idle`), from which the sessions service derives the public status,
`unread`, and push de-duplication:

| Runtime state | `Live.State` | Public status |
|---|---|---|
| turn running | `working` | `running` |
| permission request / question pending | `blocked` | `waiting_approval` / `waiting_input` |
| turn finished, not yet seen | `done` | `completed` (`failed` if the turn failed) |
| seen (`POST …/seen`), or no turn yet | `idle` | `idle` |
| process died during a turn, or the gateway stopped during one | – | `failed`, with `error` |
| no process (dormant) | `idle` | `idle`, `canSend: true` |
| not run by the gateway | (no `Live`) | `offline`, `canSend: false` |

Each finished turn changes the session's completion revision (`<startedAt>#<turns>`), so a
completion is pushed once even when the running state was never observed.

**One writer.** A session must run in one process at a time. The gateway cannot detect a session
the user resumes in a plain terminal (`claude --resume`, `codex resume`) while it runs it, nor one
still running in a terminal (or a Herdr pane) when it is resumed from the app; don't.

**Restart and crashes.** The records survive gateway restarts. A turn that was running when the
gateway stopped is shown as `failed` ("the turn was cut off"); its tool calls are never re-run
and nothing is re-sent. The next message (or a model/effort change, which is recorded) resumes
the session with `resume` / `thread/resume` under the same id. The sidecar and the app-server
are children of the gateway: they exit with it (closed stdin), and Claude Code processes are
closed by the sidecar. A runtime that exits on its own marks its sessions dormant (and a running
turn failed); the next operation starts a new one. Records idle longer than
`offlineSessionDays` are dropped; like any session not updated within that window, it then
leaves the list (an archived one stays in the archive and can be resumed).

Model, effort and mode changes, interrupt and `seen` work on every session the gateway runs. A
model, effort or mode id sent to a dormant session is recorded for the process that resumes it;
interrupt and mode cycling need a running process (`409`).

### Claude Code: Agent SDK sidecar

`gateway/claude-sidecar` is a small TypeScript program compiled with Bun into one executable
(`herdr-claude-sidecar`), bundled next to the gateway in the Mac app; it needs no Node.js. The
gateway starts it on first use and talks to it over stdio, one JSON object per line (requests
with ids, responses, and session events); nothing listens on a port.

- Each session is one stable `query()` in **streaming input mode**: messages, also during a
  turn, are pushed into its input stream, so model, effort and permission-mode changes,
  interrupts and permission answers reach the same Claude Code process.
- Options match the CLI: `settingSources: ["user","project","local"]` (CLAUDE.md, `.claude/`,
  skills, hooks, MCP servers, agents, permission rules), `systemPrompt: {preset: "claude_code"}`,
  `persistSession: true` (transcripts stay in `~/.claude/projects/…/<id>.jsonl`, which remains
  the history the gateway reads), `pathToClaudeCodeExecutable` = the user's `claude`
  (`claudeBinary`, else PATH, else `~/.local/bin/claude`), so login, version and subscription are
  the CLI's. The permission mode is passed only when chosen; otherwise Claude Code starts in the
  mode the user's settings choose, as the CLI does. Bypass permissions (`allowDangerouslySkipPermissions`
  allows it for every session but resident runs) is on only when chosen by id, at the start or
  later: cycling never lands on it, and a session that left it does not resume in it.
- `canUseTool` forwards every permission request (tool approvals, `AskUserQuestion`,
  `ExitPlanMode`) to the gateway and waits; the gateway approves nothing on its own. In Bypass
  permissions Claude Code asks only `AskUserQuestion`-style questions. The gateway shows
  them as interactions with the tool call's id, replacing the transcript's closed card for the
  same call, so a question is shown once. Answers: `AskUserQuestion` → `allow` with
  `updatedInput.answers`; approvals → `allow` / `deny`, "for this session" → the SDK's suggested
  rules with destination `session` (never the settings files); plans → `allow` plus the chosen
  permission mode, or `deny` with the feedback.
- `includePartialMessages` streams the reply; the gateway shows the text so far as a
  `streaming:<message id>` message until the transcript has that message.
- Model: `Query.setModel()`; effort: `applyFlagSettings({effortLevel})`; both apply from the next
  model request (possibly within the running turn) and are session-scoped — the user's
  `settings.json` is not changed. Until a request has used them they are `pendingModel` /
  `pendingEffort` with `settingsApply: "next_request"`.
- Billing: `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` are removed from Claude Code's
  environment and a session whose account reports an API key source, or no subscription and no
  login, is refused with the reason, unless `claudeAllowApiKey` is set. Bedrock/Vertex
  configured by the user are left alone.
- The agent's environment drops the gateway's own Claude Code / Herdr pane variables
  (`CLAUDECODE`, `CLAUDE_CODE_CHILD_SESSION`, `HERDR_PANE_ID`, `HERDR_SOCKET_PATH`, …), in case the
  gateway runs inside one during development, and sets `HC_SESSION_ID=claude:<id>`, so `hc` knows
  its own session. Herdr's own configuration (`HERDR_SESSION`, the default socket) is kept, so the
  agent can still use Herdr from its Bash tool as a separate tool.
- The gateway's own skills (`gateway/internal/agentplugin`) are one Claude Code plugin,
  `herdr-companion`: `reply-in-app` (showing images and files in the app), `drive-sessions` (other
  sessions through hc) and `schedule-resident-tasks` (designing resident tasks; what a run itself must
  do stays in its prompt, `resident.BuildPrompt`). At start the gateway writes it to
  `<state>/agent-plugin` and passes it as the SDK's `plugins` option; the Codex app-servers get its
  `skills/` directory with `skills/extraRoots/set` and name the skills the same way. Change a
  description only with `internal/agentplugin/evals/trigger.py`, which measures whether requests load
  the right skill.

### Codex: the gateway's app-server

The gateway's private `codex app-server` (stdio) serves history reads and hosts the threads it runs:
`thread/start {cwd, model?}` (everything else from the user's Codex config, as for the CLI),
`thread/resume`, `turn/start` / `turn/steer` (images as `localImage`), `turn/interrupt`,
`thread/unsubscribe`, and server requests (`requestUserInput`, approvals) answered as JSON-RPC
responses. Model, effort and collaboration mode (default / plan) are kept per thread and given
with every `turn/start` (`model`, `effort`, `collaborationMode`), the way Codex's own clients and
PASEO do: a change never touches a running turn and applies from the next one on the same
thread id (`settingsApply: "next_turn"`). Available models and efforts come from `model/list`.
The Codex TUI's own dialogs (plan "Implement this plan?", async question queue, rate limit
prompt) do not exist for these threads. Codex exports `CODEX_THREAD_ID` to its commands, which
`hc` uses to recognize its own session.

### Folder trust and worktrees

The SDK and app-server show no folder-trust dialog, but project settings (hooks, MCP servers)
run code. A start without `trust: true` in a folder the agent never trusted (Claude:
`hasTrustDialogAccepted` in `~/.claude.json`; Codex: `trust_level = "trusted"` in
`config.toml`, both only read) returns `trustRequired` with a `launchId`, answered through
`POST /v1/launches/{launchId}/trust`. `trust: true` writes no trust entry. Worktrees are made with git
(`git worktree add -b worktree/<name>` under the state directory's `worktrees/`) and marked as
the gateway's; archiving removes them with `git worktree remove` (never `--force`), keeping one
another live session uses or that has uncommitted or untracked changes. Branches are kept.
Worktrees 1.x had Herdr make for its launches carry the same marker and are removed the same way.

## Status

The runtime table above is the whole mapping: a session the gateway runs gets its status from its
`Live.State` and the provider's hints (a pending approval or question, a failed last turn), and
any other recently updated session is `offline`.

## Provider adapters

`internal/providers.Provider` is the whole abstraction:
`Summary`, `Recent`, `Messages`, `Image`, `Send`, `Respond`, plus `DirectRuntime` for the
processes. Claude Code and Codex are the only providers.

### Claude Code

- History: the transcript JSONL. `user` / `assistant` / `system` entries become
  messages; metadata entries (`attachment`, `mode`, `ai-title`, …) are skipped.
  Tool calls and results are rendered as short text; thinking is hidden.
- Polled transcripts stay decoded and only appended lines are read. Up to 16
  transcripts are kept within `historyCacheMB` (default 2048 MiB), image
  payloads excluded. A long session keeps well over 100 MiB, and one dropped
  from the cache is decoded again from the start on its next poll.
- Conversion is incremental too, for the detail screen and the list summary
  alike. An entry's messages depend on its own line plus what it looks up
  about tool calls: the result, whether the call is pending, the tool name.
  Each lookup is recorded, and when a later line changes one, only the
  entries that asked are converted again. File links and sizes are those of
  the entry's conversion, as for file references; a reload refreshes them.
- Cross-session peer messages are visible even in meta user entries and
  queued-command attachments. The body and sender become a `session_message`
  notice, separate from user prompts. Sender links are resolved on each read
  from the public `sessions/<pid>.json` record, with socket, name and start-time
  checks to avoid PID reuse. Missing peers remain readable without a link.
- Pending `AskUserQuestion` calls, approvals and plans are the sidecar's
  permission requests (see above), shown as `interaction` blocks; the
  transcript gives the answered cards.
- `ExitPlanMode`: the plan text, a file block linking the saved plan
  (`planFilePath` under `~/.claude/plans`, which the file API also serves for
  Claude sessions) and a `questions` interaction with `kind: "plan"` that
  counts as an approval for the session status. Its options are "Yes,
  auto-accept edits" (or "Yes, and use auto mode" for a model that supports
  auto mode), "Yes, manually approve edits" and "No, keep planning"; free
  text is "Tell Claude what to change" (`otherLabel`). The answer summary
  comes from the tool result (approved, rejected, or the feedback).
- Sending: the prompt goes into the session's input stream; images are
  attached as image content blocks, other attachments are appended to the
  text as paths for Claude to read.

### Codex

- History: `thread/read {includeTurns:true}` through the gateway's private
  `codex app-server` (stdio).
- Display reads reuse history and converted messages while the rollout file
  identity, size, mtime and the app-server's event/connection generation remain unchanged.
  The cache holds at most 64 entries and approximately `historyCacheMB`
  (default 2048 MiB) of history and message projections, with a 30-second reconciliation deadline. Small metadata
  (`includeTurns:false`) has a 5-second deadline. Actions always read fresh data
  and invalidate display reads. Pending requests and mode are
  recomputed rather than cached with the history.
- Lists and notification watching consume an incremental rollout projection:
  latest text, turn state, token accounting and question/plan hints. Oversized
  tool records are skipped without retaining output. Finished content revisions
  are reconciled against app-server once per content change, preserving detection
  of background results within the same turn. Missing/unrecognized rollouts,
  compaction, reversion and reconnects fall back to canonical reads. Partial
  records are retried; file replacement, truncation and same-size rewrites reset
  the projection. Projection entries are bounded to 64 and question data to
  1 MiB per entry; oversized relevant records use canonical reconciliation.
- Server requests (`item/tool/requestUserInput`, command / file-change /
  permission approvals) are kept in memory and shown as interactions;
  answers are sent as JSON-RPC responses. Messages are sent with
  `turn/start` (or `turn/steer` during an active turn) including `localImage`.
  Non-image attachments have no structured form, so their paths are appended to
  the text item.
- Tagged `send_user_message_question_reply` user messages (answers to Codex's
  async questions) are rendered as answered question cards, retaining the
  original options when available. Invalid or quoted payloads remain ordinary
  text.
- Answered `requestUserInput` prompts are not in thread items. They are
  rebuilt from the rollout's `request_user_input` call and its output and
  shown as answered cards before the agent message that followed the answer.

## Message model

```text
Message { id, role (user|assistant|tool|system), timestamp, blocks[] }
Block   { type: text | image | file | interaction, ... }
```

A user message the agent has received but not taken into the conversation yet
carries `queued: true` (Claude Code's queue while it works; the real entry
replaces it when the agent picks it up).

### Sending from the app

The app shows a message the moment it is sent and follows it until the
conversation has it: *Sending…* (upload and POST in flight), *Delivered*
(the gateway answered: the agent has the text), *Queued* (the conversation shows it with `queued`), and finally the
real message. Sends run in the app's scope in an in-memory `Outbox`, one at a
time per session, so leaving the screen does not cancel them; the gateway also
finishes a send whose client hung up (`context.WithoutCancel`, 2 min). A
pending message is matched to the first user message that was not in the
conversation when it was sent and contains the start of its text (whitespace
collapsed; a message of attachments alone takes the next new one). Failed
sends offer *Retry* / *Discard*; one delivered but never seen is dropped after
30 min.

Notification replies use a separate persistent path: a UUID identifies a
private Android payload file, while WorkManager Data contains only that UUID.
Replies are chained per connection/session. A permanent delivery error ends
that work with scheduler success after reporting the error, so replies already
appended to the chain still run. Atomic payload replacement records a send
attempt before the POST. Startup reconciles payload IDs with work tags to
recover the save/enqueue gap; cancelled work is not resurrected. Accepted
payloads and old-connection payloads are removed, terminal failures are retained
for 30 days, and pending offline payloads do not expire. The terminal files do
not yet have an in-app draft-recovery view.

`GET /v1/message-delivery` advertises durable request-ID handling. Notification
replies use the same `requestId` on retries only when this capability is present;
an older gateway may ignore the field, so an ambiguous old-gateway send is
never replayed automatically. Before calling the provider, the gateway durably
claims the ID after validating the payload and resolving the live session.
Accepted duplicates return 202 without another provider call. A provider error,
unfinished receipt after restart, or inability to persist acceptance returns
409 with an unconfirmed outcome. This provides at-most-once provider invocation,
not proof that an interrupted send reached the agent. Receipt files are synced
before execution and never automatically expired, since deleting an unknown
outcome would permit replay. The guarantee requires retaining the same state
directory. Android Outbox and Desktop sends use the same request ID and receipt for
retries of one logical send (`MessageDeliveryAttempt`).

Unknown provider data never fails a request: it becomes a
`Unsupported event: …` text block and is written to the dead-letter log with
the raw payload. `GET /messages?after=<id>` returns messages from that id on
(the anchor is included so in-progress updates are seen).

## Dead letters

`~/.local/state/herdr-mobile/errors/YYYY-MM-DD.jsonl`, one JSON object per
line: `timestamp, provider, sessionId, kind, error, raw`. Kinds:
`unknown_event, unknown_content, unsupported_interaction, parse_error,
provider_error, file_error, send_error, missed_interaction`. Identical
entries are written once per process.

`missed_interaction` comes from the app's Reload action
(`POST /v1/sessions/{id}/reload`). The gateway reads the conversation once
with its caches, drops them and reads again, then records a question or
approval that the app was not showing (`not_shown`), that the cached read
lacked (`stale_cache`), or a session waiting for an answer with nothing
answerable pending (`uncaptured`). These are the cases where the agent waits
for an answer the app does not show. `herdr-mobile-gateway debug replay FILE` re-runs the current
parsers on stored payloads.

## Notifications

The watcher re-evaluates every live session when the runtime reports a change
(`Watcher.Kick`, debounced) and every 15 s, which also reconciles anything missed. Transitions
into `completed` / `waiting_input` / `waiting_approval` / `failed` send an FCM
data message `{sessionId, status, title, body, provider, providerName, project, canSend, report}`
with Android priority `HIGH` and no collapse key: FCM keeps only four distinct
collapse keys per device, which can discard other sessions' queued alerts.
Android still replaces the displayed notification for each session.
`body` starts with the session title (the
project name only stands in until there is one) because that first line is all
a collapsed notification shows, then the last message. `report` is the last
assistant text as message JSON, cut so the whole payload stays under 3800 bytes
(FCM allows 4096); the app shows it in the conversation before its fetch
succeeds, under a `push:` id that is never an incremental anchor. `running → idle` counts as completion because a finished turn
becomes `idle` instead of `completed` once someone has looked at it. The first observation after start never
pushes. For Codex, the watcher also remembers the latest finished turn's
content revision (turn ID, status, error, and items); returning to completion or failure with the
same revision does not send another push. A new turn still notifies even if
its final text matches the previous turn, even when the running state was
missed between reads. A changed revision in a terminal state also catches a
transcript that flushed after the status change. Background task results that
update the same turn can notify again when the session returns to completion.

On Android, `PushService` shows the notification (channels *Completed*,
*Needs attention*, *Errors* in the *Sessions* channel group; resident tasks have
their own group, see `resident.md`) and enqueues an expedited `PrefetchWorker`
that fetches the session into Room, so tapping the notification opens a
populated conversation. Sessions and resident tasks are also separate bundles in
the shade, labelled by the notification's sub text; a summary notification holds
each bundle together and goes away with its last child. A failure's title starts
with ⚠, and a reply that could not be delivered moves to *Errors* whatever the
session's state is.

RemoteInput replies persist their full text privately before enqueueing, with
only a short notification-history preview. Delivery failures distinguish a
known rejection from an unconfirmed outcome; the latter removes the immediate
reply action and asks the user to check the conversation first.

Read state is the gateway's: a finished turn is `unread` (`done`) until
`POST /v1/sessions/{id}/seen` turns it `idle`, and the session-list row shows a
marker and heavier text while it is set. Android marks the session seen while
its detail screen is open; the Mac UI does so when the user opens or selects a
session, not while it merely stays open.
Foreground screens poll every 3 s (detail) / 5 s (list). On Android, dragging
the conversation up past its newest message fetches it at once, without
waiting for the next poll; a fling that reaches the end does not.

A notification carries an inline *Reply* field when `canSend` is set and the
session is not waiting on a question or an approval — those expect that exact
answer, which only the in-app interaction UI can give. `ReplyReceiver` re-posts
the notification with the typed text and hands it to `ReplyWorker`, which POSTs
the message. Failures before sending and same-ID requests to a receipt-capable
gateway can retry; an ambiguous send to an old gateway asks for conversation
confirmation. The notification reports pending, done, failed, or unconfirmed.

## Starting sessions

`internal/launcher` checks the directory, the folder trust (see above) and the model, effort and
mode against the provider's catalog, then has the runtime start the session; the start returns
the session id. The first prompt can carry uploads like a message (Claude: images as content
blocks, other paths in the text; Codex: `localImage` items, other paths in the text).

Claude Code has no catalog API, so its model list is the CLI aliases (`fable`, `opus`, `sonnet`,
`haiku`); Codex's comes from `model/list`, with the efforts per model, so `/v1/models` returns
them on each model plus a catalog-level list (Claude's levels, or the default model's for Codex)
used when no model is picked. A running session's model and effort can be changed
(`POST /v1/sessions/{id}/model`, `/effort`), checked against the same catalog.

The session mode can be picked for the new session too, and `/v1/models` returns the modes a
provider offers (`modes`, with one marked `default`). Claude Code's are its permission modes,
named as its status line names them;
Codex's are its two collaboration modes (default, plan), given with every `turn/start`.

The app does not ask for the agent, the model, the effort and the mode
separately on every start: named combinations are stored in the
`agent_presets` DataStore (agent/model/effort/mode ids, catalog display names,
and the user's preset name), managed from Settings, and offered from a
drop-down in New Session. Older unnamed favorites remain readable and can be
renamed from Settings. The combination a session was started with is
remembered and preselected next time. The working directory used by the last
session is restored too; if that directory is a favorite, its favorite chip
is selected.

Sessions report the model they last used (`model`), the reasoning effort
(`effort`) and a display label for the mode (`mode`):

- Claude: `model` / `effort` of the newest assistant reply; `permissionMode`
  of the latest entry that has one, or the mode the runtime reports for the
  running session.
- Codex: the thread's `model` / `reasoningEffort`. The permission preset comes
  from the app-server; Plan mode also comes from the newest
  `collaboration_mode` in the rollout.

They also report how full the context window is (`context`: used tokens,
window size, percent), omitted until an agent has accounted for a turn:

- Claude: the `usage` of the newest main-chain assistant reply, whose input
  side is everything the model saw. Sidechains (subagents) fill their own
  window and are skipped. Current Fable, Opus, and Sonnet models use a 1M
  window by default; older or other models use 200k unless an `attachment`
  entry of type `model` identifies an explicit 1M variant (`[1m]`).
- Codex: the app-server reports token counts only to the client owning the
  turn, but writes them to the thread's rollout file, whose path `thread/list`
  and `thread/read` both return. The last `token_count` record there gives
  `last_token_usage.total_tokens` and `model_context_window`.

Sessions also carry what they cost (`cost`: dollars, plus `estimated`).
Neither agent reports a running total, so `internal/pricing` prices the token
counts the transcripts keep, with the providers' list rates per million
tokens - money a subscription is not actually billed. An unknown model is
left unpriced rather than guessed, and the field is omitted entirely.

- Claude: every assistant reply's `usage`, per model. Claude Code writes an
  entry per content block, all repeating the same usage, so replies are
  counted once by `message.id`; subagents bill to the session that started
  them, so sidechains count too. Cache writes are priced per TTL
  (`cache_creation`), reads at the cheaper rate. When the session ends Claude
  Code appends a `cost-state` entry with its own exact total, which then
  replaces the estimate (`estimated: false`).
- Codex: `total_token_usage` from the last `token_count` record of the
  rollout file. Its input side includes the cached tokens, which bill less.
  Codex keeps one total per thread rather than per model, so the model the
  thread runs on now prices all of it.

### Worktrees

A start can ask for a new git worktree of the chosen directory's repository
(`worktree: true`; a directory outside a repository is refused). The gateway
creates it with git (`git worktree add -b worktree/<name>` under the state
directory's `worktrees/`) and starts the agent in it. The agents' own worktree
options (Claude Code's and Codex's `--worktree`) are not used.

The gateway keeps no list of them. A worktree it had created gets
`herdr-companion.json` in its git directory (`.git/worktrees/<name>/`), so
archiving only removes those. A Codex TUI started with `--worktree` from a
terminal runs in its worktree without changing its process directory, so for
a thread in `$CODEX_HOME/worktrees` the thread's cwd is the session directory
(`Summary.PinnedCwd`).

## Archive and resume

- Archive: a running session's process is ended and its runtime record
  dropped. The id is stored in `archive.json`. Archived sessions are left
  out of the offline part of `GET /v1/sessions`; a live one is still listed.
- The worktree the session ran in is then removed with git
  (`internal/worktree`) when it carries the gateway's marker. One another
  live session still uses, or with uncommitted or untracked changes (ignored
  files do not count), is kept, and the response carries a `warning` saying
  why. The branch is kept, so commits are never lost.
- Resume restarts a session that runs nowhere under its native id in a new
  process (SDK `resume`, `thread/resume`; also for sessions started in a
  terminal or, in 1.x, a Herdr pane) and removes it from the archive. Its body
  is ignored. A worktree outside `workspaceRoots` may be resumed when its
  repository is inside them; one that was removed answers 409.
- Successfully sending a message to an archived session also removes it from
  the archive. Failed sends leave it archived.

Directories are restricted to `workspaceRoots` with the same checks as file
access; new folder names must be a single, non-hidden path segment.

## Files

`GET /v1/sessions/{id}/files[/content]?path=` serves files under the
session's working directory, the upload directory (for sent attachments) and
any directory the provider links files from (`providers.FileRooter`: Claude's
`plans` directory, for saved plans).
Paths are cleaned, symlink-resolved and must stay inside an allowed root;
`.ssh`, `.gnupg`, `.aws` are always refused. Text is served as `text/plain`.

## Subscription limits

Neither Claude Code nor Codex prints how much of the plan's rate limits is
left: Claude Code only shows it in `/usage`, and Codex only inside the TUI.
Reading either would mean re-implementing an OAuth flow and handling their
credentials in the gateway, so `internal/usage` shells out to the CodexBar CLI
(`brew install --cask codexbar`), which already talks to both dashboards, and
normalises its JSON into windows (`5h`, `7d`, and Claude's per-model weekly
bucket). When Codex reports the separate Luna Reserve allocation,
`internal/usage` reads `base_model_inference` from the existing Codex
app-server connection and appends it as a `gpt-reserve` window.

A read takes several seconds, so it runs in the background every
`usageRefreshMinutes` (default 5) and `GET /v1/usage` answers from the cache.
`POST /v1/usage/refresh` forces a read; concurrent callers share one run and a
client that hangs up does not cancel it. A failed read keeps the last good
providers and reports the error next to them. `usageCommand: "off"` turns the
whole thing off, and `herdr-mobile-gateway usage` prints one read.

## Resident tasks

Standing tasks ("常駐") the gateway runs on a schedule or on request, each with its own
instruction, conversation, memory, wake-ups and run history. Full description (Japanese):
[resident.md](resident.md).

- `internal/resident` owns them in `resident.db` (SQLite through `modernc.org/sqlite`, pure Go,
  WAL, mode 600, schema version in `PRAGMA user_version`). It is independent of
  `internal/runtime`: runs are identified by run id and never appear in `direct-sessions.json`,
  the session list (offline entries of resident attempts are filtered out too), the watcher or
  its pushes. Each run keeps its agent conversation as a session would, hidden from the list;
  the user can promote it (`POST /v1/resident/runs/{id}/promote`) to an ordinary session,
  which is then listed and resumed (`docs/resident.md`).
- Each run is a fresh agent conversation built from the instruction, the task's memory, the
  recent conversation, pending follow-ups and the reason it started, and must end with a JSON
  result (`resident.ResultSchema`: outcome, summary, report, notification request and key,
  memory patch, optional `nextWakeAt`, question, artifacts). A turn without a valid result is a
  contract failure, never a success.
- Claude: a sidecar started for the run, `query()` with `persistSession: true`,
  `outputFormat` and permission mode / tool rules from the task's profile; other permission
  requests are denied and reported. Codex: an app-server started for the run, a saved
  (not ephemeral) thread with the profile's sandbox and approvals (`workspace_write`: `on-request`
  decided by Codex's auto review, `approvalsReviewer: "auto_review"`; `read_only`: `never`), `outputSchema` on
  `turn/start`. When a run ends, is stopped or times out, its runtime and every process it
  started (including commands in their own process groups) are ended; nothing shared with
  sessions is touched. Login, CLI path and the API-key billing guard are the sessions'.
- Scheduling (once / daily / weekly / cron in an IANA timezone), catch-up after sleep, one
  queued run per task, global concurrency, daily caps, self wake-ups and the notification
  outbox are plain database state driven by a 30 s tick; no model is involved in waiting.
- Restarts: a run whose prompt was not sent is queued again; one with a saved result is
  finalized from it; any other cut-off run becomes `needs_reconciliation` and is never re-sent.
- Pushes are `{type: "resident", taskId, runId, notificationId, kind, title, body}`, decided
  by the gateway (agent request, task rules, keys already sent, cooldown, quiet hours) and
  delivered from a retrying outbox with a stable id.

## HTTP API

Full request and response shapes, auth and connection details: [api.md](api.md).

| Method | Path | |
|---|---|---|
| GET | `/healthz` | no auth |
| GET | `/v1/sessions` | live + recent offline sessions |
| POST | `/v1/sessions` | start `{provider, cwd, prompt, uploads?[], model?, effort?, mode?, trust, worktree?}` → `{sessionId?, launchId?, warning?, trustRequired?}` |
| POST | `/v1/launches/{launchId}/trust` | `{trust}` answer a start that returned `trustRequired`: continue it or cancel it → StartResult |
| GET | `/v1/agents` | installed CLI versions and latest update status/output |
| POST | `/v1/agents/{provider}/update` | start the provider's update command (`claude update` or `codex update`); repeated requests while running share the same job |
| GET | `/v1/models?provider=` | models and efforts offered for new sessions `{models[{id, name, description?, default?, efforts?[]}], efforts?[{id, name, description?, default?}]}` |
| GET | `/v1/directories?path=` | workspace roots, or subfolders of `path` (`git` when `path` is in a repository) |
| POST | `/v1/directories` | create `{parent, name}` under a root |
| GET | `/v1/sessions?archived=true` | archived sessions |
| GET | `/v1/sessions/{id}` | one session |
| POST / DELETE | `/v1/sessions/{id}/archive` | archive (stops a running session, removes its worktree) → session + `warning?` / unarchive |
| POST | `/v1/sessions/archive` | archive `{ids[]}` in one request (stops running sessions) → `{sessions[]}` (each with `warning?`) |
| POST | `/v1/sessions/{id}/resume` | `{}` restart under the same id in a new process → StartResult |
| GET | `/v1/sessions/{id}/messages?after=` | `{session, messages}` |
| POST | `/v1/sessions/{id}/messages` | `{text, uploads[], requestId?}`; optional durable at-most-once delivery |
| GET | `/v1/message-delivery` | `{idempotency}`; required capability check before automatic message retries |
| POST | `/v1/sessions/{id}/respond` | `{interactionId, answers{qid:{selected[],text}}, decision}` |
| POST | `/v1/sessions/{id}/mode` | `{mode?}` advance to the next mode, or set one |
| POST | `/v1/sessions/{id}/model` | `{model}` change a session's model → session (`pendingModel`, `settingsApply`) |
| POST | `/v1/sessions/{id}/effort` | `{effort, model?}` change a session's reasoning effort → session |
| POST | `/v1/sessions/{id}/interrupt` | stop a session's running turn |
| POST | `/v1/sessions/{id}/seen` | mark a finished turn seen (clears `unread`); `{ok}` |
| POST | `/v1/sessions/{id}/reload` | `{shown[]}` drop the provider's caches, read again → `{session, messages, findings[]}` (see Dead letters) |
| GET | `/v1/sessions/{id}/messages/{mid}/images/{n}` | inline image bytes |
| GET | `/v1/sessions/{id}/files?path=` | directory listing |
| GET | `/v1/sessions/{id}/files/content?path=` | file bytes |
| POST | `/v1/uploads` | streamed raw body up to 500 MiB, optional `Content-Disposition` file name → `{id, name}` (deleted after 24 h) |
| GET | `/v1/usage` | cached subscription limits `{providers[{provider, displayName, plan?, account?, windows[{key, label, scope?, usedPercent, windowMinutes?, resetsAt?}], updatedAt?, error?}], fetchedAt?, error?}` |
| POST | `/v1/usage/refresh` | re-read the limits now (takes ~10 s) → same shape |
| POST / DELETE | `/v1/devices` | FCM token registration |
| GET | `/v1/capabilities` | `{messageIdempotency, resident?}`: what the gateway offers |
| * | `/v1/resident/…` | resident tasks (see api.md) |

## Advisory directory check

`POST /v1/directories/check {cwd, prompt}` validates cwd with the same root and
symlink restrictions as launch, then returns `{verdict, historyCount}`. It never
starts anything. The Android new-session screen calls it before launch;
only a high-confidence `mismatch` opens a confirmation. The exact checked
request is retained for "Start anyway". Cancellation does not proceed to launch.
Errors, old gateways (404), and disabled checks preserve the normal start flow.

`internal/directorycheck` calls TypeSafe's `POST /v1/systemone` (`jev-latest`)
with two Noul questions over the same state: `related` (should this request be
carried out in this project?) and `other` (does it name or describe a different
project, product or subject domain?). A warning requires `other >= 0.55` and
`related < 0.75`. The earlier single match/mismatch/unknown Choice, told to pick
mismatch only on positive evidence, scored clearly unrelated requests (an
article about a contest in this repository) at mismatch ~0.45 and never warned.
The thresholds come from a 31-case evaluation against real workspaces: unrelated
requests scored `other` 0.51-0.96, same-project requests (follow-ups, new
features, new libraries or integrations) at most 0.43. They are a starting
point, not a measured guarantee; a request for another product the project's
files never mention (e.g. a Herdr feature asked in an unrelated app) can still
pass. State contents are evidence, not evaluation instructions.

The optional `jevApiKey` setting (overridden by `TYPESAFE_API_KEY`) enables
external transmission of bounded project-file and conversation excerpts.
History uses existing provider Recent scans over 30 days, matches canonical cwd
exactly, and reads up to three newest sessions across providers, independently
of the app's archive filter. Provider scan limits still apply. There is no
persistent derived history or decision cache. The request has a 10-second context
budget, of which history gets at most 3 seconds; provider filesystem reads may
finish after cancellation. API errors return unavailable without logging payloads.

## Agent updates

Updates are explicit in Android Settings. The authenticated update endpoints execute only the
configured Codex binary or `claude` on the Gateway PATH, with fixed `--version` / `update`
arguments, no shell and no interactive stdin. Native CLI installations supporting these commands
are required; package-manager-only installations report their CLI error/output and may require
updating on the Mac. Update jobs survive HTTP disconnects and leaving the Android screen, are
serialized per provider, have a ten-minute limit, and retain at most 32 KiB of output. Status
lives until Gateway restart. Running sessions keep the version they started with until their
process is restarted (archive and resume, or a gateway restart and the next message), and the
gateway's Codex app-server keeps its version until it is started again.
