# Herdr Companion

Japanese version: [README.ja.md](README.ja.md)

Herdr Companion lets you start, inspect and control Claude Code, Codex and DeepSeek Harness sessions on your Mac from Android (and from a Compose Desktop UI and the `hc` CLI for other agents). Keep an AI agent running on the Mac while checking its progress, answering questions, switching its model, and sending the next instruction from your phone.

The Gateway runs Claude Code (through the official Claude Agent SDK), Codex (through its own private app-server), and DeepSeek Harness (through its SDK stdio profile) itself, so **Herdr is not required**. Since 2.0 the Gateway no longer integrates with [Herdr](https://herdr.dev) at all, and OpenCode and Devin are no longer supported. You can still use Herdr on its own, and agents can still call `herdr` from their own tools. Upgrading from 1.x: see [Upgrading from 1.x](#upgrading-from-1x).

## Why this app is useful

The main benefit is that you can follow a session without keeping the app open on screen.

- Android notifications for agent completion, questions, approval requests, and errors
- The conversation is fetched in the background when a notification arrives and saved to the device's Room cache
- Tapping a notification opens the relevant session with its latest log ready (with retries when the network is temporarily unavailable)
- Send ordinary messages directly from the inline reply action in a notification
- Answer AskUserQuestion, Codex's requestUserInput, and approvals in native UI
- Send images and files, view images inline, and open file references from messages
- Upload attachments up to 500 MiB with streaming on Android, Desktop, and the updated Gateway
- Start new sessions from the app and choose the working directory, model, reasoning effort and mode
- Change a running session's model and reasoning effort without losing its history
- **Resident tasks (常駐)**: a top-level tab for standing instructions the Gateway runs on a schedule (daily, weekly, cron, once, in an IANA timezone) or on request, remembering state between runs and notifying only when something needs you; follow-ups, questions and run history stay with the task. Runs use their own Claude Code / Codex processes and never show up among sessions. See [docs/resident.md](docs/resident.md)

The notification-to-screen flow looks like this:

```text
Gateway on the Mac watches the sessions it runs
        │ FCM notification
        ▼
Android fetches the conversation in the background → saves it to Room → shows it when tapped
```

Firebase project and service-account setup is, honestly, the most cumbersome part of this project. The rest of the app still works without notifications: you can browse, send messages, answer interactions, and start sessions. When notifications are enabled, the recommended menu-bar app only requires a one-time setup, and shared APKs do not need to be rebuilt.

The Gateway runs on the Mac and does not keep a separate cloud conversation database. It reads Claude Code's transcripts and Codex's threads. For DeepSeek Harness, it keeps a local display journal alongside DSH's native model history.

```text
Android / Desktop / hc ──(Tailscale recommended / trusted LAN / HTTPS tunnel, HTTP(S) + Bearer)──▶ Herdr Companion Gateway (Mac)
    ├─▶ Claude Agent SDK sidecar ──▶ Claude Code
    ├─▶ codex app-server (stdio) ──▶ Codex
    └─▶ dsh --profile sdk (stdio) ──▶ DeepSeek Harness
```

See [docs/architecture.md](docs/architecture.md) for the detailed design.

## Quick start

1. Install and log in to Claude Code (`claude`, then `/login` with your subscription) and/or Codex on the Mac. Herdr is not needed.
2. Install and launch Herdr Companion Gateway.app on the Mac, then start the Gateway.
3. In the Android app, use **Scan Mac QR** to scan the pairing QR shown by the menu-bar app.
4. If you want notifications, configure Firebase and pair again with a newly generated QR code.

Tailscale is recommended for the Mac and Android to reach each other over the same tailnet. A trusted LAN or an HTTPS tunnel also works. See [macos/README.md](macos/README.md) for the Mac menu-bar app and [docs/macos-release.md](docs/macos-release.md) for building and distributing DMGs.

The Firebase service account key and the Jev API key are optional. You can pair and use the app without either one.

## Repository layout

```text
gateway/   Go Gateway (cmd/herdr-mobile-gateway, internal/...)
android/   Android app and Compose Desktop UI (Kotlin / Compose)
macos/     SwiftUI Gateway Manager and Gateway DMG build scripts
docs/      architecture and macOS release documentation
```

## Prerequisites

| Target | Requirements |
|---|---|
| Mac | Claude Code and/or Codex CLI (logged in), and network connectivity (Tailscale recommended) |
| Building the Gateway | Go 1.27+, Bun 1.3+ (for the Claude sidecar) |
| Usage limits | CodexBar (brew install --cask codexbar, optional) |
| Android | Android 10 (API 29) or newer, with a network route to the Mac (Tailscale recommended) |
| Building | JDK 17 and Android SDK (compileSdk 37) |
| Notifications | A Firebase project (the free tier is sufficient) |

## 1. Agents

Claude Code and Codex need nothing besides being installed and logged in: the Gateway runs them itself. Claude Code sessions use your existing login and subscription (the Gateway never switches to per-token API billing: `ANTHROPIC_API_KEY` is not passed to Claude Code unless you set `claudeAllowApiKey`, and a session that would bill an API key is refused with the reason), and load the same settings as the CLI: CLAUDE.md, `.claude/`, skills, hooks, MCP servers, agents and permission rules. Codex runs in a `codex app-server` the Gateway starts for itself. Their history stays in Claude Code's and Codex's own local storage.

DeepSeek Harness is available for ordinary sessions and resident tasks after installing the supported SDK runtime and configuring its model route. Claude/ChatGPT subscription-backed resident tasks use the existing Claude Code/Codex logins. See [DeepSeek Harness setup and limitations](docs/deepseek-harness.md).

**Agent updates on Mac** in the app's Settings shows the installed Claude Code and Codex versions and runs `claude update` / `codex update` with **Check and update**.

### Upgrading from 1.x

2.0 removes everything built on Herdr panes, and the OpenCode and Devin support:

- Claude Code and Codex sessions that ran in Herdr panes now show as offline. **Resume** (or `hc resume`) reopens one under the same id, run by the Gateway. End the agent in its pane first: two processes would write the same history. OpenCode and Devin sessions are no longer listed.
- The config keys `herdrSocket`, `opencode`, `devin` and `codexDaemonSocket` are ignored; you can delete them.
- Herdr Companion no longer needs the Herdr server, its LaunchAgent (`com.herdr-mobile.herdr-server`), `herdr integration install`, or Codex's shared app-server daemon. Keep them only if you use Herdr on its own.
- The **Terminal** screen, opening Herdr panes from the Mac UI, and `hc term`, `hc keys`, `hc migrate` and `--backend` are gone.
- Worktrees 1.x created for Herdr launches are removed with git on archive, like any other.

## 2. Set up the Gateway

### Menu-bar app (recommended)

With a distributed DMG, drag Herdr Companion Gateway.app into Applications, launch it, and start the Gateway from the menu-bar icon. Confirm the displayed address, then choose **Show pairing QR** and scan it from **Scan Mac QR** in Android.

Herdr Companion.app (the Compose Desktop UI) is a separate app and DMG. Install it separately if you want to use it. The menu-bar app manages Firebase import, login-item startup, and Gateway start/stop. See [macos/README.md](macos/README.md) for details.

The same desktop UI also runs on Linux. Each GitHub Release has `Herdr-Companion-Linux-x.y.z-amd64.deb` and `-arm64.deb`; install one with `sudo apt install ./Herdr-Companion-Linux-*.deb`. To build it yourself: jpackage only packages for the OS it runs on, so build the `.deb` on Linux (JDK 17, plus an Android SDK because the Gradle build includes the Android app). Install `libegl1` before building: jpackage derives the package's dependencies from the libraries installed at build time.

```bash
cd android
./gradlew :desktopApp:packageReleaseDeb   # desktopApp/build/compose/binaries/main-release/deb/herdr-companion_*.deb
sudo apt install ./desktopApp/build/compose/binaries/main-release/deb/herdr-companion_*.deb
```

On a machine without the menu-bar app, open **Settings** and enter the Gateway address (for example `http://100.x.y.z:8766`) and the token printed by `herdr-mobile-gateway token` on the Mac. Notifications use `notify-send`. The Linux app does not update itself.

### CLI / launchd

If you do not use the distributed app and want to run the Gateway from source:

```bash
cd gateway
go build -o ~/.local/bin/herdr-mobile-gateway ./cmd/herdr-mobile-gateway
~/.local/bin/herdr-mobile-gateway token  # creates the config and auth token on first run
```

The generated ~/.config/herdr-mobile/config.json has mode 600:

```json
{
  "listen": ":8765",
  "authToken": "…",
  "devices": [],
  "offlineSessionDays": 3
}
```

Optional fields include fcmCredentialsFile, claudeConfigDir, claudeBinary (the Claude Code CLI direct sessions run; default `claude` on PATH, then `~/.local/bin/claude`), claudeSidecar (default: `herdr-claude-sidecar` next to the gateway, then PATH), claudeAllowApiKey, codexBinary, uploadDir, workspaceRoots, usageCommand, usageRefreshMinutes, and historyCacheMB (memory each agent may keep for decoded conversation histories, default 2048 MiB).

workspaceRoots (default: ~/workspace, or the home directory if it does not exist) limits the directories where the app can browse, create folders, and start sessions.

### Subscription usage limits

The app can show Claude and Codex plan usage on the session list. Neither Claude Code nor Codex exposes these limits directly through the CLI, so the Gateway uses the CodexBar CLI, which reads both dashboards. If a Codex account reports a Luna Reserve (gpt-reserve) allowance, that separate window is shown under Limits too.

```bash
brew install --cask codexbar     # installs /opt/homebrew/bin/codexbar
herdr-mobile-gateway usage       # verify that usage can be read
```

The Gateway refreshes and caches usage every five minutes by default, and the app displays the cached result. The refresh button forces an immediate read. Set usageRefreshMinutes to change the interval or usageCommand: "off" to disable it. Without CodexBar, only this feature is disabled.

### Starting sessions from the app

From **New session**, choose Claude Code, Codex or DeepSeek Harness, a working directory under workspaceRoots (browse or create one), and the first prompt.

The Gateway starts the agent itself and the session id is known immediately. If the agent was never trusted in that folder, the app asks first (project hooks and MCP servers run code); **Trust** starts it, **Cancel** does not. With **worktree**, the Gateway creates a git worktree on a new `worktree/<name>` branch; archiving the session removes it unless it has uncommitted or untracked changes (the branch is kept).

On a running session you can change the model and reasoning effort, switch the mode and interrupt the current turn from the session screen or `hc`. Claude Code applies a model or effort change from its next model request (it may be within the running turn); Codex applies it from the next turn. The app shows the change as pending until then. Choices the agent does not offer are refused with the reason, never replaced.

If the Gateway stops while a session is working, that turn is cut off (the agent process ends with the Gateway) and the session shows as failed after the restart; nothing is re-run. History stays readable, and the next message resumes the session under the same id in a new process.

Sessions the Gateway does not run, such as ones started in a terminal, are listed as offline when they were updated within `offlineSessionDays`. **Resume** restarts one in the Gateway under the same id. A session the Gateway runs whose process has stopped is not offline: the next message restarts it. A finished turn stays unread until you open the session.

### Pre-flight folder check (Jev, optional)

Set the TypeSafe API key in the Gateway's TYPESAFE_API_KEY environment variable or in jevApiKey inside ~/.config/herdr-mobile/config.json, then restart the Gateway. The environment variable takes precedence. Since launchd does not inherit terminal environment variables, putting the key in the config file is usually easier. The key is never sent to Android.

When you press **Start** for a new session, the check compares the instruction with the selected folder. It only shows **Change folder / Start anyway** for a clear mismatch. If Jev is not configured, cannot decide, or cannot be reached, the session starts normally; an empty instruction skips the check. The check also works with an older Gateway.

**When enabled, the following excerpts are sent to the TypeSafe API:**

- The instruction, folder name, and up to 60 non-hidden names directly inside the folder
- The first 4 KiB of the root README.md, AGENTS.md, and representative project configuration files
- From up to three sessions in the same real directory from the last 30 days: the latest three instructions and the last report, up to 1,000 characters each

History is searched only within the recent sessions already available to the Claude/Codex provider. Conversations from other directories, images, tool results, and full source files are not sent. However, text in instructions, reports, and README files is included verbatim in the excerpts, so use this only for projects that may send that information externally. The check input and API response are not written to logs.

Instructions longer than 6,000 characters skip the check to avoid judging from a partial prompt. Jev can be wrong; it never blocks a start or changes the directory automatically.

### Authentication token

Every /v1/* API requires Authorization: Bearer <token> regardless of the network path.

```bash
herdr-mobile-gateway token            # print the token
herdr-mobile-gateway token --rotate   # rotate it (update the app settings too)
```

### Network connection (Tailscale recommended)

Tailscale lets the Mac and Android connect even when they are on different networks without exposing the Gateway directly to the public internet. Put both devices on the same tailnet and enter the Mac's MagicDNS name or Tailscale IP in the app.

```bash
tailscale status              # find the Mac host name, e.g. my-mac
tailscale ip -4               # e.g. 100.101.102.103
```

Example Gateway URLs: http://my-mac.<tailnet-name>.ts.net:8765 or http://100.101.102.103:8765.

Without Tailscale, connect the Mac and Android to the same trusted home or office LAN and enter the Mac's IP. The menu-bar app detects a local IPv4 address when Tailscale is not available.

With Cloudflare Tunnel or another tunnel, run the Gateway normally and enter the tunnel's https://... URL in Android Settings. Use the same Gateway token. Use an HTTPS tunnel on a public route; do not expose plain HTTP directly to the internet.

To bind the CLI Gateway only to the Tailscale IP:

```bash
herdr-mobile-gateway serve --listen 100.101.102.103:8765
```

HTTP over Tailscale is encrypted by WireGuard. Restrict HTTP on a LAN to a trusted network. For direct public exposure, use HTTPS and additional access controls.

### Run it continuously with launchd

Run the Gateway as a LaunchAgent:

```bash
mkdir -p ~/Library/LaunchAgents ~/.local/state/herdr-mobile
# Tailscale (recommended):
LISTEN=$(tailscale ip -4):8765
# Without Tailscale, replace the line above with:
# LISTEN=$(ipconfig getifaddr en0):8765
# e.g. 192.168.1.20:8765
sed -e "s#__HOME__#$HOME#g" -e "s#__LISTEN__#$LISTEN#g" \
  gateway/deploy/com.herdr-mobile.gateway.plist > ~/Library/LaunchAgents/com.herdr-mobile.gateway.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.herdr-mobile.gateway.plist

# status / restart / stop
launchctl list | grep herdr-mobile
launchctl kickstart -k gui/$(id -u)/com.herdr-mobile.gateway
launchctl bootout gui/$(id -u)/com.herdr-mobile.gateway
```

Logs: ~/.local/state/herdr-mobile/gateway.log (JSON, rotated at 10 MB × 3 generations).

## 3. Firebase (notifications, optional)

Firebase is only required if you want push notifications. It is a little cumbersome, but it lets the Gateway send FCM HTTP v1 messages from the Mac while Android fetches the conversation in the background after receiving the push. That is what makes the “tap a notification and the latest log is already there” experience possible.

Do **not** commit google-services.json or the service-account private-key JSON to the repository (.gitignore already excludes them). Both files must belong to the same Firebase project. Keep the service-account key only on the Mac running the Gateway; it is not sent to Android or included in the QR payload.

### Recommended: import from the Mac menu-bar app

1. Create a project in the [Firebase console](https://console.firebase.google.com/).
2. Add an Android app. The package name is com.tohutohu.herdrcompanion.
3. Download the Android google-services.json.
4. In Project settings → Service accounts, choose **Generate new private key** and download the service-account JSON.
5. In Herdr Companion Gateway.app, open **Notifications · Import Firebase**, select both JSON files, and click **Import**.
6. Restart the Gateway, generate a new pairing QR, and pair Android again.

The menu-bar app passes only the public Android settings (API key, App ID, Project ID, and Sender ID) during pairing. Shared APKs do not need to be rebuilt. On Android 13 and newer, allow notifications when Android asks on first launch.

### Import from the CLI

Stop the Gateway and pass it both files using the bundled or built Gateway:

```bash
herdr-mobile-gateway import-firebase \
  --service-account "$HOME/Downloads/<project>-firebase-adminsdk-....json" \
  --android-config "$PWD/android/app/google-services.json"
```

The command verifies that both files belong to the same Firebase project and saves the configuration to the Gateway. With the usual CLI/launchd setup, placing the service account at the following path makes it available to the default launchd plist:

```bash
mkdir -p "$HOME/.config/herdr-mobile"
cp "$HOME/Downloads/<project>-firebase-adminsdk-....json" \
  "$HOME/.config/herdr-mobile/firebase-service-account.json"
chmod 600 "$HOME/.config/herdr-mobile/firebase-service-account.json"
```

Restart the Gateway and save the Android connection settings, then verify the registration and send a test notification:

```bash
herdr-mobile-gateway devices
herdr-mobile-gateway notify-test
```

Example using the Firebase CLI and gcloud:

```bash
PROJECT=herdr-client-android
firebase apps:create android "Herdr Companion" \
  --package-name com.tohutohu.herdrcompanion --project "$PROJECT"
firebase apps:sdkconfig ANDROID <App ID from the previous command> \
  --project "$PROJECT" --out android/app/google-services.json
gcloud iam service-accounts keys create \
  "$HOME/.config/herdr-mobile/firebase-service-account.json" \
  --iam-account firebase-adminsdk-fbsvc@"$PROJECT".iam.gserviceaccount.com \
  --project "$PROJECT"
```

There is also a legacy personal-APK mode that bundles Firebase settings at build time. Only for that mode, place android/app/google-services.json in the project and build with -PbundleFirebase=true. For shared APKs, use QR pairing to provide the client settings without putting secrets in the APK.

## 4. Android app

```bash
cd android
./gradlew --console=plain assembleRelease   # app/build/outputs/apk/release/app-release.apk
./gradlew installRelease                   # install on a connected device
```

Use the **release** build for normal APK builds; both R8 and resource shrinking are enabled. Release is currently signed with the debug key so it can be installed directly, but it is still an optimized release build. Use assembleDebug / installDebug only when you explicitly need a debug build.

Use JDK 17 (for example, export JAVA_HOME=$(/usr/libexec/java_home -v 17)). If local.properties does not contain sdk.dir, open the project once in Android Studio or create it manually.

On first launch, scan the Mac QR with **Scan Mac QR**, or enter the Gateway URL and token manually and choose **Test connection** → **Save**. For Tailscale, use a MagicDNS name or Tailscale IP (http://100.x.y.z:8765); on a LAN, use the Mac's IP (for example, http://192.168.1.20:8765); through a tunnel, use the https://... tunnel URL.

A debug build can receive its settings through adb, which avoids Japanese IME conversion when typing a token:

```bash
adb shell am broadcast -n com.tohutohu.herdrcompanion/.DebugConfigReceiver \
  --es gateway_url "http://100.x.y.z:8765" --es token "$(herdr-mobile-gateway token)"
adb shell am start -n com.tohutohu.herdrcompanion/.MainActivity
```

Android 13 and newer ask for notification permission.

## Development

```bash
# Claude sidecar (Bun): the Gateway finds it next to its executable, on PATH, or via HERDR_CLAUDE_SIDECAR
cd gateway/claude-sidecar
bun install && bun run typecheck && bun run build   # → dist/herdr-claude-sidecar

# Gateway
cd gateway
HERDR_CLAUDE_SIDECAR=$PWD/claude-sidecar/dist/herdr-claude-sidecar \
  go run ./cmd/herdr-mobile-gateway --debug --log-file ""   # development server (:8765)
go test ./...
go test ./internal/providers/claude ./internal/providers/codex -update  # update fixture goldens
go vet ./...

# Re-parse data that could not be handled
go run ./cmd/herdr-mobile-gateway debug replay ~/.local/state/herdr-mobile/errors/2026-09-17.jsonl

# Call the API
TOKEN=$(go run ./cmd/herdr-mobile-gateway token)
curl -H "Authorization: Bearer $TOKEN" localhost:8765/v1/sessions

# Android
cd android
./gradlew testDebugUnitTest
./gradlew assembleDebug
```

### Test data

gateway/testdata/{claude,codex}/ contains anonymized real-data fixtures and golden JSON results. When a Claude or Codex format change creates a dead letter, add the raw payload as a fixture and update the adapter.

## Limitations and known behavior

- Sessions have no terminal. TUI-only dialogs (Codex's "Implement this plan?" prompt, its async question queue) do not exist there: send the next instruction instead. A request the app cannot answer is shown read-only; interrupt the turn and send new instructions.
- Do not resume a session the Gateway is running in a separate terminal (`claude --resume`, `codex resume`), and do not resume from the app a session that is still running in a terminal or a Herdr pane; two processes would write the same history.
- Notification de-duplication is in memory only, so the Gateway does not notify for the initial observation immediately after a restart.
- Background fetching requires the Gateway to be running and reachable from Android. FCM notifications are not delivered while the Android app is force-stopped.
