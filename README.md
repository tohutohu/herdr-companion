# Herdr Companion

Japanese version: [README.ja.md](README.ja.md)

Herdr Companion lets you inspect and control Claude Code, Codex, OpenCode, and Devin sessions running inside [Herdr](https://herdr.dev) on your Mac from Android. Keep an AI agent running on the Mac while checking its progress, answering questions, and sending the next instruction from your phone.

## Why this app is useful

The main benefit is that you can follow a session without keeping the app open on screen.

- Android notifications for agent completion, questions, approval requests, and errors
- The conversation is fetched in the background when a notification arrives and saved to the device's Room cache
- Tapping a notification opens the relevant session with its latest log ready (with retries when the network is temporarily unavailable)
- Send ordinary messages directly from the inline reply action in a notification
- Answer AskUserQuestion, Codex's requestUserInput, and approvals in native UI
- Send images and files, view images inline, and open file references from messages
- Start new sessions from the app and choose the working directory and model

The notification-to-screen flow looks like this:

```text
Gateway on the Mac watches Herdr
        │ FCM notification
        ▼
Android fetches the conversation in the background → saves it to Room → shows it when tapped
```

Firebase project and service-account setup is, honestly, the most cumbersome part of this project. The rest of the app still works without notifications: you can browse, send messages, answer interactions, and start sessions. When notifications are enabled, the recommended menu-bar app only requires a one-time setup, and shared APKs do not need to be rebuilt.

The Gateway runs on the Mac and does not keep a separate cloud conversation database. It reads Herdr and each agent's own data, and caches only what Android needs locally.

```text
Android ──(Tailscale recommended / trusted LAN / HTTPS tunnel, HTTP(S) + Bearer)──▶ Herdr Companion Gateway (Mac) ──▶ Herdr / Claude Code / Codex / OpenCode / Devin
```

See [docs/architecture.md](docs/architecture.md) for the detailed design.

## Quick start

1. Set up Herdr and the required Herdr integrations for Claude Code, Codex, OpenCode, and/or Devin on the Mac.
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
| Mac | Go 1.24+, Herdr 0.9+, Claude Code, Codex CLI, OpenCode and/or Devin CLI, and network connectivity (Tailscale recommended) |
| Usage limits | CodexBar (brew install --cask codexbar, optional) |
| Android | Android 10 (API 29) or newer, with a network route to the Mac (Tailscale recommended) |
| Building | JDK 17 and Android SDK (compileSdk 37) |
| Notifications | A Firebase project (the free tier is sufficient) |

## 1. Set up Herdr

The Gateway gets the mapping between panes and running sessions from Herdr's official integrations. This is required.

```bash
herdr integration install claude
herdr integration install codex
# If you use OpenCode, start OpenCode once, then run:
herdr integration install opencode
# If you use Devin CLI:
herdr integration install devin
herdr integration status   # the integrations you use should be installed
```

Sessions started after the integration is installed are recognized. Restart existing sessions once.

### Structured Codex operations (recommended)

For structured approval, requestUserInput, and message operations in Codex, connect the Codex TUI to the shared app-server daemon:

```bash
codex app-server daemon start     # shared daemon (~/.codex/app-server-control/app-server-control.sock)
codex --remote unix://            # start Codex in the Herdr pane this way
```

A regular codex process still supports history and message sending through the pane, but approvals and questions must be handled from the terminal unless the shared daemon is used.

## 2. Set up the Gateway

### Menu-bar app (recommended)

With a distributed DMG, drag Herdr Companion Gateway.app into Applications, launch it, and start the Gateway from the menu-bar icon. Confirm the displayed address, then choose **Show pairing QR** and scan it from **Scan Mac QR** in Android.

Herdr Companion.app (the Compose Desktop UI) is a separate app and DMG. Install it separately if you want to use it. The menu-bar app manages Firebase import, login-item startup, and Gateway start/stop. See [macos/README.md](macos/README.md) for details.

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

Optional fields include fcmCredentialsFile, herdrSocket, claudeConfigDir, opencode, devin, codexBinary, codexDaemonSocket, uploadDir, workspaceRoots, usageCommand, and usageRefreshMinutes. The Gateway automatically reads Devin's `~/.local/share/devin/cli/sessions.db`; set `devin.database` or `devin.binary` only when using a custom location or executable.

workspaceRoots (default: ~/workspace, or the home directory if it does not exist) limits the directories where the app can browse, create folders, and start sessions.

### Subscription usage limits

The app can show Claude and Codex plan usage on the session list. Neither Claude Code nor Codex exposes these limits directly through the CLI, so the Gateway uses the CodexBar CLI, which reads both dashboards. If a Codex account reports a Luna Reserve (gpt-reserve) allowance, that separate window is shown under Limits too.

```bash
brew install --cask codexbar     # installs /opt/homebrew/bin/codexbar
herdr-mobile-gateway usage       # verify that usage can be read
```

The Gateway refreshes and caches usage every five minutes by default, and the app displays the cached result. The refresh button forces an immediate read. Set usageRefreshMinutes to change the interval or usageCommand: "off" to disable it. Without CodexBar, only this feature is disabled.

### Starting sessions from the app

From **New session**, choose Claude Code, Codex, OpenCode, or Devin, a working directory under workspaceRoots (browse or create one), and the first prompt.

The Gateway creates a Herdr workspace and starts the agent with agent.start. If the agent shows a folder-trust dialog, the app asks whether to trust it. **Trust** answers the dialog and continues; **Cancel** closes the workspace that was created. If the Codex shared daemon is running, the Gateway starts Codex with codex --remote unix://….

Codex requires the Herdr integration hook to be trusted once. Press t on the Hooks confirmation screen shown when Codex starts.

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

Run the headless Herdr server and the Gateway as separate LaunchAgents. The herdr TUI attaches to the running server.

```bash
mkdir -p ~/Library/LaunchAgents ~/.local/state/herdr-mobile
# Tailscale (recommended):
LISTEN=$(tailscale ip -4):8765
# Without Tailscale, replace the line above with:
# LISTEN=$(ipconfig getifaddr en0):8765
# e.g. 192.168.1.20:8765
sed -e "s#__HOME__#$HOME#g" -e "s#__HERDR__#$(which herdr)#g" \
  gateway/deploy/com.herdr-mobile.herdr-server.plist > ~/Library/LaunchAgents/com.herdr-mobile.herdr-server.plist
sed -e "s#__HOME__#$HOME#g" -e "s#__LISTEN__#$LISTEN#g" \
  gateway/deploy/com.herdr-mobile.gateway.plist > ~/Library/LaunchAgents/com.herdr-mobile.gateway.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.herdr-mobile.herdr-server.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.herdr-mobile.gateway.plist

# status / restart / stop
launchctl list | grep herdr-mobile
launchctl kickstart -k gui/$(id -u)/com.herdr-mobile.gateway
launchctl bootout gui/$(id -u)/com.herdr-mobile.gateway
```

The Herdr server plist deliberately keeps PATH minimal. Pane login shells build their PATH from the usual terminal environment; a long PATH can cause an older Homebrew claude to be selected first.

The Herdr server automatically restarts only after crashes and respects herdr server stop.

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
# Gateway
cd gateway
go run ./cmd/herdr-mobile-gateway --debug --log-file ""   # development server (:8765)
go test ./...
go test ./internal/providers/... -update                 # update fixture goldens
go vet ./...

# Use a separate Herdr session
HERDR_SESSION=mytest go run ./cmd/herdr-mobile-gateway

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

- Claude Code AskUserQuestion and approval answers are translated into pane key presses because there is no structured API (verified with Claude Code 2.1.x). If the UI changes, use the app's **Terminal** screen.
- Structured Codex answers work only for sessions running through the shared daemon (codex --remote unix://).
- Session identity is the session ID reported by the Herdr integration. Panes without an integration do not appear in the list.
- Notification de-duplication is in memory only, so the Gateway does not notify for the initial observation immediately after a restart.
- Background fetching requires the Gateway to be running and reachable from Android. FCM notifications are not delivered while the Android app is force-stopped.

## Devin CLI

Choose **Devin** in the app's agent picker. Install and authenticate the local
CLI on the Mac, then install its Herdr integration:

```bash
curl -fsSL https://cli.devin.ai/install.sh | bash
devin auth login
herdr integration install devin
```

The Gateway reads Devin's local conversation database at
`~/.local/share/devin/cli/sessions.db` in read-only mode, follows the active
message chain, and sends new prompts through the Herdr pane. The model picker
uses the model families returned by `devin models list`; Devin's own default is
used when no model is selected. Devin workspace dialogs that are not recognized
by the current CLI version remain available through the app's **Terminal**
screen.

For a custom installation, keep the other Gateway settings and add:

```json
{
  "devin": {
    "database": "/absolute/path/to/sessions.db",
    "binary": "devin"
  }
}
```

The Settings screen also exposes `devin update` as **Check and update**.

## OpenCode (v1 / official v2)

Choose **OpenCode** in the app's agent picker. The Gateway starts the opencode executable found on Herdr's PATH.
The [official v2 documentation](https://opencode.ai/v2/docs) was tested with v2.0.9.
For Herdr session identity and status notifications, start OpenCode once and then run
herdr integration install opencode. If it is already installed, update it to the latest Herdr integration including v2 TUI support.

- Conversations are read-only from ~/.local/share/opencode/opencode.db.
  v1 uses session / message / part; v2 uses session_v2 / session_message.
  XDG_DATA_HOME and OPENCODE_DB are also supported.
- The v2 shared service is discovered from ~/.local/state/opencode/service.json.
  Sending, image/file attachments, approvals, and ordinary choice/free-text forms are supported.
  Conditional forms, numeric inputs, and other unsupported fields fall back to the terminal.
- v1 uses Herdr for ordinary text sending. Structured approvals/questions and image attachments require a connection to the same v1 server as the TUI.
- Selecting a model for v2 requires the shared service. Fetching the model list starts the OpenCode CLI shared service. For v2, the Gateway creates a session with the selected model through the API and opens the TUI with --session.

For a custom database location or an existing v1 server, add opencode to the Gateway's private config (keep the other settings):

```json
{
  "opencode": {
    "database": "/absolute/path/opencode.db",
    "binary": "opencode",
    "serverUrl": "http://127.0.0.1:4096",
    "serverVersion": 1,
    "username": "opencode",
    "password": "your-server-password"
  }
}
```

If serverUrl is omitted, the official v2 shared service is discovered. To specify a v2 endpoint explicitly, set serverVersion: 2. Set stateDir to change the service-registration directory. binary is used to list models and check the version; use the same executable version that Herdr starts.
Old v1 JSON storage from before the SQLite migration is not supported.

### Update OpenCode from Settings

**Agent updates on Mac** shows the OpenCode version and **Check and update**. It runs the official [upgrade command](https://opencode.ai/v2/docs/cli/commands#upgrade), then reads the version again. Both v1 and official v2 are supported.
If opencode.binary is set, that executable is updated.
If the CLI cannot determine the installation method, the update output and error are shown.
Updates continue when you leave the screen, and duplicate updates for the same agent are not started.
