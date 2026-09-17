# Architecture

Herdr Mobile lets an Android phone follow and answer Claude Code / Codex
sessions that run inside [Herdr](https://herdr.dev) on a Mac.

```text
Android (Compose, Room, WorkManager, FCM)
   │  HTTP + Bearer token, over Tailscale
   ▼
herdr-mobile-gateway (Go, on the Mac)
   ├─ Herdr socket API ── which pane runs which native session, agent status,
   │                      pane.read / send_keys / send_text / agent.prompt
   ├─ Claude adapter ──── ~/.claude/projects/*/<session>.jsonl (+ PTY for dialogs)
   ├─ Codex adapter ───── codex app-server (thread/read, thread/list)
   │                      + shared daemon socket for approvals/questions/turns
   └─ FCM HTTP v1 ─────── data-only pushes on status transitions
```

## Principles

- **Providers are the source of truth.** The gateway keeps no session,
  message, event or attachment database. Every request reads Herdr and the
  provider's own storage and converts on the fly. Restarting the gateway loses
  nothing.
- **Persistent gateway state** is limited to `~/.config/herdr-mobile/config.json`
  (listen address, auth token, FCM device tokens, optional paths), the set of
  archived session ids (`~/.local/state/herdr-mobile/archive.json`) and
  dead-letter / log files under `~/.local/state/herdr-mobile/`.
- **In-memory only:** last seen status per session (push de-duplication),
  the FCM access token, and — for Codex — pending server requests received on
  the daemon connection (the daemon replays them on reconnect).
- **Android caches**, the gateway does not. Room is the UI's read source.

## Identity

Sessions are `claude:<session-id>` and `codex:<thread-id>`. Herdr's official
integrations (`herdr integration install claude|codex`) report the native id
for each pane (`agent_session` in `session.snapshot`). A pane id is only
*where* a session currently runs; a resumed session in another pane keeps its
id. A stored `agent_session` is trusted only while the same agent still
occupies the pane.

## Status

| Herdr `agent_status` | Provider hint | Public status |
|---|---|---|
| `working` | | `running` |
| `blocked` | pending approval | `waiting_approval` |
| `blocked` | question / unknown | `waiting_input` |
| `done` | last turn failed | `failed` |
| `done` | | `completed` |
| `idle`, `unknown` | last turn failed | `failed` |
| `idle`, `unknown` | | `idle` |
| (not in Herdr) | | `offline` |

A Codex thread loaded on the shared daemon may report its own status
(`waitingOnApproval`, `waitingOnUserInput`, `systemError`), which wins.

## Provider adapters

`internal/providers.Provider` is the whole abstraction:
`Summary`, `Recent`, `Messages`, `Image`, `Send`, `Respond`.
Adding OpenCode means one more package implementing it and one line in
`main.go`.

### Claude Code

- History: the transcript JSONL. `user` / `assistant` / `system` entries become
  messages; metadata entries (`attachment`, `mode`, `ai-title`, …) are skipped.
  Tool calls and results are rendered as short text; thinking is hidden.
- `AskUserQuestion`: an `interaction` block built from the tool input. It is
  `pending` only while the pane is `blocked` and the tool has no result.
- Approvals: any other unanswered tool call while `blocked`.
- Answers are translated to key presses in the adapter
  (verified against Claude Code 2.1.x): approve = Enter, deny = Esc;
  single-select = Down×n + Enter; free text = move to "Type something",
  type, Enter; multi-select = Space/Down per option, then "Submit";
  multiple questions end with the "Submit answers" review tab.
- Sending: `agent.prompt` (bracketed paste + Enter). Images are passed as file
  paths that Claude reads.

### Codex

- History: `thread/read {includeTurns:true}` through a private
  `codex app-server` (stdio) or the shared daemon when connected.
- When the TUI runs on the shared daemon (`codex --remote unix://`), the
  gateway subscribes to loaded threads (`thread/loaded/list` +
  `thread/resume`). Server requests (`item/tool/requestUserInput`, command /
  file-change / permission approvals) are kept in memory and shown as
  interactions; answers are sent as JSON-RPC responses. Messages are sent with
  `turn/start` (or `turn/steer` during an active turn) including `localImage`.
- Without the daemon, messages go through the pane, and a blocked pane shows an
  "unsupported" interaction that links to the terminal.

## Message model

```text
Message { id, role (user|assistant|tool|system), timestamp, blocks[] }
Block   { type: text | image | file | interaction, ... }
```

Unknown provider data never fails a request: it becomes a
`Unsupported event: …` text block and is written to the dead-letter log with
the raw payload. `GET /messages?after=<id>` returns messages from that id on
(the anchor is included so in-progress updates are seen).

## Dead letters

`~/.local/state/herdr-mobile/errors/YYYY-MM-DD.jsonl`, one JSON object per
line: `timestamp, provider, sessionId, kind, error, raw`. Kinds:
`unknown_event, unknown_content, unsupported_interaction, parse_error,
provider_error, file_error, send_error`. Identical entries are written once
per process. `herdr-mobile-gateway debug replay FILE` re-runs the current
parsers on stored payloads.

## Notifications

The watcher subscribes to `pane.agent_status_changed` for all panes (plus
pane topology events to resubscribe) and re-evaluates every 15 s. Transitions
into `completed` / `waiting_input` / `waiting_approval` / `failed` send an FCM
data message `{sessionId, status, title, body, provider, project}` with
Android priority `HIGH`. `running → idle` counts as completion because Herdr
turns `done` into `idle` once the pane was looked at. The first observation
after start never pushes.

On Android, `PushService` shows the notification (channels *Completed*,
*Needs attention*, *Errors*) and enqueues an expedited `PrefetchWorker` that
fetches the session into Room, so tapping the notification opens a populated
conversation. Foreground screens poll every 3 s (detail) / 5 s (list).

## Starting sessions

`internal/launcher` creates a Herdr workspace in the chosen directory and runs
`agent.start`. Providers implement `providers.Launchable`: extra CLI arguments
(Codex attaches to the shared daemon when present) and the keys that accept
their folder-trust dialog. The dialog is answered only when the request says
`trust: true`. The native session id is then read from the pane's
`agent_session`, which the Herdr integration hook reports at startup.
An optional model id is passed as the CLI's `--model`. Claude Code has no
catalog API, so its list is the CLI aliases (`fable`, `opus`, `sonnet`,
`haiku`); Codex's comes from `model/list`. Switching the model of a running
Claude session is not offered because `/model` also changes the user's
default for new sessions.

An optional reasoning effort goes with it: Claude takes `--effort` (the levels
its `/effort` command offers, the same for every model), Codex has no flag for
it, so the TUI is given `-c model_reasoning_effort="…"`, which it forwards to
the shared daemon. Codex lists the efforts per model in `model/list`, so
`/v1/models` returns them on each model plus a catalog-level list (Claude's
levels, or the default model's for Codex) used when no model is picked.

The app does not ask for the agent, the model and the effort separately on
every start: the combinations the user keeps coming back to are saved as
favorites (agent id, model id, effort id, plus the names the catalog gave them
so a favorite of the other agent can be labelled without loading its catalog)
in the `agent_presets` DataStore, and offered as one-tap chips. Anything else
is picked in the full pickers behind *Other…*, which is also where a
combination is starred or unstarred. The combination a session was started
with is remembered and preselected next time.

Sessions report the model they last used (`model`), the reasoning effort
(`effort`) and a display label for the mode (`mode`):

- Claude: `model` / `effort` of the newest assistant reply (or the display
  name from a later `/model`); `permissionMode` of the latest entry that has
  one. A Shift+Tab change is recorded with the next prompt, and
  `permission-mode` entries are re-appended later with the current mode.
- Codex: the thread's `model` / `reasoningEffort`. The mode is only known for
  threads loaded on the shared daemon: `thread/resume` gives the approval
  policy and sandbox, `thread/settings/updated` also gives Plan mode.

A new pane's shell may still be running its startup files; Herdr then
answers `agent.start` with `agent_pane_busy`, which is retried until the
start timeout. A workspace whose agent fails to start is closed again.

Codex TUIs on the shared daemon run their hooks inside the daemon, so Herdr
never learns the thread id. The launcher then finds the newest non-ephemeral
thread created in that directory (`providers.SessionLocator`) and reports it
with `pane.report_agent_session`.

## Archive and resume

- Archive: a running session's pane is closed (the whole workspace when it
  was the only pane) and the id is stored in `archive.json`. Archived
  sessions are left out of the offline part of `GET /v1/sessions`; a live
  one is still listed (e.g. resumed from a terminal).
- Resume opens a session that is not in Herdr in a new workspace in its
  working directory (`claude --resume <id>`, `codex resume <id>`) and
  removes it from the archive. Both keep the native id.

Directories are restricted to `workspaceRoots` with the same checks as file
access; new folder names must be a single, non-hidden path segment.

## Files

`GET /v1/sessions/{id}/files[/content]?path=` serves files under the
session's working directory (and the upload directory, for sent images).
Paths are cleaned, symlink-resolved and must stay inside an allowed root;
`.ssh`, `.gnupg`, `.aws` are always refused. Text is served as `text/plain`.

## Subscription limits

Neither Claude Code nor Codex prints how much of the plan's rate limits is
left: Claude Code only shows it in `/usage`, and Codex only inside the TUI.
Reading either would mean re-implementing an OAuth flow and handling their
credentials in the gateway, so `internal/usage` shells out to the CodexBar CLI
(`brew install --cask codexbar`), which already talks to both dashboards, and
normalises its JSON into windows (`5h`, `7d`, and Claude's per-model weekly
bucket).

A read takes several seconds, so it runs in the background every
`usageRefreshMinutes` (default 5) and `GET /v1/usage` answers from the cache.
`POST /v1/usage/refresh` forces a read; concurrent callers share one run and a
client that hangs up does not cancel it. A failed read keeps the last good
providers and reports the error next to them. `usageCommand: "off"` turns the
whole thing off, and `herdr-mobile-gateway usage` prints one read.

## HTTP API

| Method | Path | |
|---|---|---|
| GET | `/healthz` | no auth |
| GET | `/v1/sessions` | live + recent offline sessions |
| POST | `/v1/sessions` | start `{provider, cwd, prompt, model?, effort?, trust}` in a new Herdr workspace → `{sessionId?, paneId, warning?}` |
| GET | `/v1/models?provider=` | models and efforts offered for new sessions `{models[{id, name, description?, default?, efforts?[]}], efforts?[{id, name, description?, default?}]}` |
| GET | `/v1/directories?path=` | workspace roots, or subfolders of `path` |
| POST | `/v1/directories` | create `{parent, name}` under a root |
| GET | `/v1/sessions?archived=true` | archived sessions |
| GET | `/v1/sessions/{id}` | one session |
| POST / DELETE | `/v1/sessions/{id}/archive` | archive (stops a running session) / unarchive |
| POST | `/v1/sessions/{id}/resume` | `{trust}` reopen in a new Herdr workspace → `{sessionId?, paneId, warning?}` |
| GET | `/v1/sessions/{id}/messages?after=` | `{session, messages}` |
| POST | `/v1/sessions/{id}/messages` | `{text, uploads[]}` |
| POST | `/v1/sessions/{id}/respond` | `{interactionId, answers{qid:{selected[],text}}, decision}` |
| GET | `/v1/sessions/{id}/messages/{mid}/images/{n}` | inline image bytes |
| GET | `/v1/sessions/{id}/files?path=` | directory listing |
| GET | `/v1/sessions/{id}/files/content?path=` | file bytes |
| GET | `/v1/sessions/{id}/terminal?lines=` | recent pane text |
| POST | `/v1/sessions/{id}/terminal` | `{text, keys[]}` (allow-listed keys) |
| POST | `/v1/uploads` | raw image body → `{id}` (deleted after 24 h) |
| GET | `/v1/usage` | cached subscription limits `{providers[{provider, displayName, plan?, account?, windows[{key, label, scope?, usedPercent, windowMinutes?, resetsAt?}], updatedAt?, error?}], fetchedAt?, error?}` |
| POST | `/v1/usage/refresh` | re-read the limits now (takes ~10 s) → same shape |
| POST / DELETE | `/v1/devices` | FCM token registration |
