# DeepSeek Harness

The `dsh` provider runs one `dsh --profile sdk` child process per ordinary
Gateway session. Android, the Compose Mac UI and `hc start --provider dsh`
use the same Gateway API as Claude Code and Codex.

## Install and configure

The supported baseline is **`@deepseek-ai/dsh@0.2.2-rc.1`**. The npm `latest`
tag currently points to 0.2.1-rc.5, whose SDK lacks client approval requests,
native user questions and `session/query`. Pin the version explicitly:

```sh
npm install --global @deepseek-ai/dsh@0.2.2-rc.1
command -v dsh
```

Use a Node version supported by that package and its dependencies. With npm
versions that gate dependency install scripts, review the install output and
enable the native dependencies required by the DSH tools you use.

The Gateway's private config accepts these optional fields:

| Field | Default | Purpose |
|---|---|---|
| `dshBinary` | `dsh` on PATH | Absolute executable path is recommended for the Mac menu-bar app. |
| `dshHome` | `<Gateway state>/dsh/home` | Dedicated Harness home, native history, credentials and SDK profile. |
| `dshModel` | `deepseek-flash` | Default model on the `deepseek-official` route. |
| `dshApiKey` | unset | Private API key passed only to the DSH child as `DEEPSEEK_API_KEY`. |

The active Mac config is `~/.config/herdr-mobile/desktop/config.json` (mode
600). Keep keys out of source control, command output and shared config
examples. Existing `DEEPSEEK_API_KEY` in the Gateway environment also works;
DSH's native credential service can supply the key from its private home.
This uses DeepSeek API billing, independently of Claude/Codex subscriptions.
Restart the Gateway through the normal safe update flow after changing config.

Alternatively, open DSH's Models settings with the same dedicated home:

```sh
DSH_HOME="$HOME/.local/state/herdr-mobile/desktop/dsh/home" dsh web
```

Select **DeepSeek Harness** in New session, choose a workspace and send a
prompt. Folder trust is confirmed before the first start in the same way as
the other providers. `workspace-write` is the default permission preset;
`danger-full-access` is an explicit selectable mode. Configure project
instructions, skills and MCP through DSH's own supported configuration.

## Behavior and boundaries

- Conversation text, tool calls/results, approvals, questions and plan-review
  questions use the existing native UI. A normal approval grants one operation
  only; unsupported session-wide grants are rejected.
- SDK events are displayed when committed. The SDK does not expose the Web
  client's transient token stream. Thinking and internal context events are
  omitted from the conversation.
- **Interrupt** ends this session's process. EOF asks DSH to dispose and flush;
  after five seconds a stuck process is killed. Sending again resumes the same
  native ID. A turn cut off before its final settlement can lose that unsettled
  output; no prompt or tool call is automatically replayed by the Gateway.
- Model/effort changes apply on the next send through a process restart and
  `session/resume`. A send with a pending model change waits for the current
  turn to finish by returning an actionable error instead of interrupting it.
  The SDK validates the selected model/effort during initialization; no effort
  catalog is fabricated. Permission changes use the native `/permission` command.
- Files are supplied by local path. Raster image inputs are encoded for the SDK
  with a 20 MiB aggregate limit; the existing 500 MiB streaming upload path is
  unchanged. Native image references currently appear as `[Image]` in history.
- Gateway-created sessions have a private append-only display journal and small
  metadata file under `<state>/dsh`. DSH's own history remains the authoritative
  model context under `dshHome`; both must be retained to display and resume a
  conversation. Pending RPC approvals are closed after a restart. A process
  nonce prevents a stale UI reply from answering a newly reused RPC ID.
- Sessions created by an independent DSH terminal/Web client are not imported.
  DSH resident tasks, subscription-limit/cost reporting, automatic CLI updates,
  and Gateway companion-skill injection are not implemented in this provider.
  The existing resident-task selector continues to offer Claude Code and Codex.

## Validation

```sh
cd gateway
env -u GOROOT go test -race ./internal/providers/dsh
# Real SDK process, private temporary home, local mock model; no paid API calls:
env -u GOROOT HERDR_DSH_E2E_BINARY=/absolute/path/to/dsh \
  go test -race ./internal/providers/dsh
```

The subprocess tests cover receipt handling, persisted history/restart,
approval scope and duplicate/stale replies, structured questions, isolated
interrupts, model changes and unexpected exits. The opt-in tests additionally
exercise the actual runtime handshake, permission command, resume and a model
conversation against a local Messages-compatible SSE server.

Protocol sources: [SDK protocol](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/sdk/protocol/README.md),
[SDK server](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/sdk/server/README.md).
