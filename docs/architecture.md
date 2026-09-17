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
  (listen address, auth token, FCM device tokens, optional paths) and
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

## Files

`GET /v1/sessions/{id}/files[/content]?path=` serves files under the
session's working directory (and the upload directory, for sent images).
Paths are cleaned, symlink-resolved and must stay inside an allowed root;
`.ssh`, `.gnupg`, `.aws` are always refused. Text is served as `text/plain`.

## HTTP API

| Method | Path | |
|---|---|---|
| GET | `/healthz` | no auth |
| GET | `/v1/sessions` | live + recent offline sessions |
| GET | `/v1/sessions/{id}` | one session |
| GET | `/v1/sessions/{id}/messages?after=` | `{session, messages}` |
| POST | `/v1/sessions/{id}/messages` | `{text, uploads[]}` |
| POST | `/v1/sessions/{id}/respond` | `{interactionId, answers{qid:{selected[],text}}, decision}` |
| GET | `/v1/sessions/{id}/messages/{mid}/images/{n}` | inline image bytes |
| GET | `/v1/sessions/{id}/files?path=` | directory listing |
| GET | `/v1/sessions/{id}/files/content?path=` | file bytes |
| GET | `/v1/sessions/{id}/terminal?lines=` | recent pane text |
| POST | `/v1/sessions/{id}/terminal` | `{text, keys[]}` (allow-listed keys) |
| POST | `/v1/uploads` | raw image body → `{id}` (deleted after 24 h) |
| POST / DELETE | `/v1/devices` | FCM token registration |
