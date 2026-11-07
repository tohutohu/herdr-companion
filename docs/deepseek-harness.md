# DeepSeek Harness

The `dsh` provider runs one `dsh --profile sdk` child process per ordinary
Gateway session or resident run. Android, the Compose Mac UI and `hc start --provider dsh`
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
| `dshBinary` | found automatically | Absolute path to `dsh`. Without it the Gateway uses `dsh` on PATH, then the global npm commands of mise, nvm, fnm and asdf Node installs (newest Node first), Volta, pnpm, Bun, `~/.local/bin` and Homebrew. |
| `dshHome` | `<Gateway state>/dsh/home` | Dedicated Harness home, native history, credentials and SDK profile. |
| `dshModel` | `deepseek-flash` | Default catalog model ID. |
| `dshModels` | built-in DeepSeek models | Additional catalog entries with `id`, `name`, `provider`, `model`, optional `efforts`. |
| `dshApiKey` | unset | Private API key passed only to the DSH child as `DEEPSEEK_API_KEY`. |
| `dshProviderKeys` | unset | API keys for DSH's pi-ai provider routes, by route (for example `{"opencode-go": "…"}`). See [Model routes](#model-routes-and-subscriptions). |

The Gateway starts `dsh` with its own directory first on PATH, so the
package's `#!/usr/bin/env node` uses the Node it was installed with even when
the Gateway's PATH has no Node (the menu-bar app's case).

**On the Mac, use the menu-bar app's "DeepSeek Harness" section** instead of
editing the config: it shows the `dsh` it found (or lets you pick one), saves
an OpenCode Go or DeepSeek API key, lists OpenCode Go's tool-capable models to
offer in the app, and sets the default model. It runs
`herdr-mobile-gateway dsh status` and `herdr-mobile-gateway dsh configure`
(JSON on stdin: `binary`, `keys`, `models`, `defaultModel`), which refuse
settings that would stop the Gateway from starting, then restarts its Gateway.

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
`read-only` and `danger-full-access` are explicit selectable modes. Configure project
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
  Subscription-limit/cost reporting, automatic CLI updates, and Gateway
  companion-skill injection are not implemented in this provider.

## Resident tasks

Select **DeepSeek Harness** in the resident task form, or use
`hc resident create --provider dsh --cwd DIR --instruction "..."` with the
existing schedule options. Scheduling, memory, notifications, limits, answers
and conversation promotion use the same [resident service](resident.md).

Each run gets its own SDK process and native session. It never enters the
ordinary session runtime or notification watcher. `session/wait` waits for
settled output, including managed descendants, using the task's deadline
instead of the 30-second control-RPC timeout. Completion, cancellation and
timeout dispose this process and its descendants. Its native history and
display journal remain available for **Continue as session**.

- `read_only` maps to DSH `read-only`; `workspace_write` maps to
  `workspace-write`. Native sandbox rules enforce these restrictions. Any
  additional approval request is rejected and recorded. Claude `allowedTools`
  rules and full-access resident runs are rejected.
- Native questions become a `blocked` result with a resident question; the
  SDK process ends. The resident service stores the answer and starts a new
  run with the prior checkpoint. It never replays an old RPC approval.
- The result schema is included in the prompt. DSH's SDK has no native output
  schema parameter; the service validates the final JSON using `ParseResult`.
  Malformed output fails the run without resending a potentially effectful task.
- Root-session token usage is recorded; subscription quotas and monetary cost
  are not inferred. Subagent usage is not included in this total.

## Model routes and subscriptions

**Claude and ChatGPT subscriptions already run through their native agents**, in
both ordinary sessions and resident tasks: select **Claude Code** for a Claude
subscription, or **Codex** for a ChatGPT plan, and authenticate in the respective
CLI on this Mac. Claude's existing API-key billing guard also applies to resident
runs. Codex uses its existing account configuration; ensure it is logged in with
ChatGPT if you intend subscription billing. Plan limits still apply.

These are different from using those accounts as DSH's own language model.
The Gateway does not copy `~/.claude` or `~/.codex` tokens into DSH. Anthropic
limits subscription OAuth to Claude Code and its own apps; use the native Claude
Code route for that subscription ([Anthropic documentation](https://code.claude.com/docs/en/legal-and-compliance)).
The pinned DSH pi-ai adapter explicitly does not offer ChatGPT sign-in: its
`openai` route uses an API key. OpenAI's [Sign in with ChatGPT](https://developers.openai.com/siwc)
is a separate integration; the Gateway does not implement that login or install
community OAuth plugins. Do not label an `openai` API-key route as a subscription.

**Keyed pi-ai routes.** For each route in `dshProviderKeys` the Gateway
enables DSH's bundled pi-ai adapter: it writes
`<state>/dsh/gateway-providers.patch.yml`, which names only an environment
variable (`HERDR_DSH_KEY_<ROUTE>`, e.g. `HERDR_DSH_KEY_OPENCODE_GO`), passes it
with `dsh --patch`, and sets that variable in the child's environment. The key
is never written to DSH's home. The installed pi-ai catalog supplies the
route's endpoint and models. This overlay replaces any `llm-pi-ai`
configuration made in the DSH home or its Web Models page; without keys no
overlay is passed. OpenCode Go example:

```json
{
  "dshProviderKeys": { "opencode-go": "…" },
  "dshModel": "go-deepseek-v4.1-flash",
  "dshModels": [
    { "id": "go-deepseek-v4.1-flash", "name": "DeepSeek V4.1 Flash (Go)", "provider": "opencode-go", "model": "deepseek-v4.1-flash" },
    { "id": "go-kimi-k3", "name": "Kimi K3 (Go)", "provider": "opencode-go", "model": "kimi-k3" }
  ]
}
```

OpenCode Go serves DeepSeek models only after **Global** is allowed under
Settings › Privacy › Regions in the OpenCode Console. A model OpenCode lists
but the installed pi-ai catalog does not know fails at initialization.

DSH can also use other **already installed and authenticated** provider routes
through `dshModels`. For example, for an OpenAI **API-key** route configured in
DSH's SDK profile:

```json
{
  "dshModels": [
    {
      "id": "openai-api",
      "name": "OpenAI API",
      "provider": "openai",
      "model": "gpt-5.4"
    }
  ]
}
```

Use a model ID supported by your installed adapter and account. `id` is the
selection shown to the app and `hc --model`; `provider` and `model` are passed
unchanged to DSH's `initialize`. Add `efforts` only for values supported by that
route, and set `dshModel` to the catalog ID to make it the default. IDs must be
unique, including the built-in DeepSeek entries. DSH config/credentials must be
available in **`dshHome` and the `sdk` profile**; configuring only the `web`
profile does not install a plugin into `sdk`.

This catalog config does not install an adapter, log in, verify an account's
billing plan, or fall back to a different provider. Native initialization errors
are reported as failures. The selected route and native model are persisted with
ordinary conversations, so changing a catalog alias does not silently redirect a
resumed conversation. New resident runs use the currently configured catalog.

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
conversation against a local Messages-compatible SSE server, plus resident
completion, native questions and actual read-only filesystem enforcement.
No live subscription account or paid model request is used by these tests.

Protocol sources: [SDK protocol](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/sdk/protocol/README.md),
[SDK server](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/sdk/server/README.md).
