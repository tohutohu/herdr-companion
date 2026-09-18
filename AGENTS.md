# AGENTS.md

Herdr Mobile: Go gateway on the Mac (`gateway/`) + native Android app (`android/`).
Design: `docs/architecture.md`. Setup for humans: `README.md`.

## Commands

```bash
# Gateway
cd gateway
go test ./...                                  # must pass
go test ./internal/providers/... -update       # regenerate golden JSON after intended parser changes
go vet ./...
go build -o ~/.local/bin/herdr-mobile-gateway ./cmd/herdr-mobile-gateway   # installs the binary launchd runs
herdr-mobile-gateway token | devices | usage | notify-test | debug replay FILE

# Android (JDK 17 required; the default `java` is 24)
cd android
export JAVA_HOME=$HOME/Library/Java/JavaVirtualMachines/jbr-17.0.14/Contents/Home
./gradlew --console=plain assembleRelease          # default for APK build requests (R8 + resource shrinking)
./gradlew --console=plain assembleDebug testDebugUnitTest  # development checks
ANDROID_SERIAL=<serial> ./gradlew installDebug
```

### APK build requests

- 「APKをビルドして」 means `assembleRelease`: R8 (`isMinifyEnabled = true`) and resource shrinking (`isShrinkResources = true`) must both be enabled. Do not deliver a debug APK unless the user explicitly requests one.
- Before reporting completion, confirm the release build succeeded and link `android/app/build/outputs/apk/release/app-release.apk`.
- Release currently uses the debug signing key so it is installable; this does not disable R8 or resource shrinking.

`compileSdk` is 37 because Navigation 2.10 / Coil 3.6 require it (targetSdk stays 36).

## What runs on this Mac (persistent)

| What | How | Notes |
|---|---|---|
| Herdr server | LaunchAgent `com.herdr-mobile.herdr-server` | from `gateway/deploy/*.plist` (placeholders replaced with `sed`, see README) |
| Gateway | LaunchAgent `com.herdr-mobile.gateway` | listens on the Tailscale IP only: `100.99.15.34:8765` |

```bash
launchctl list | grep herdr-mobile
launchctl kickstart -k gui/$(id -u)/com.herdr-mobile.gateway      # after installing a new binary
launchctl bootout gui/$(id -u)/com.herdr-mobile.<name>             # stop
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.herdr-mobile.<name>.plist
```

- Keep the Herdr server plist `PATH` minimal (`/usr/bin:/bin:/usr/sbin:/sbin`). With a long PATH, pane login shells put `/opt/homebrew/bin` first and start an old Homebrew `claude` (2.1.1), which rejects `~/.claude/settings.json`.
- Restarting the Herdr server (`bootout`/`bootstrap`) kills the user's panes. Don't do it without asking.
- Gateway logs: `~/.local/state/herdr-mobile/gateway.log`. Dead letters: `~/.local/state/herdr-mobile/errors/*.jsonl`.

## Persistent state and secrets (never commit)

- `~/.config/herdr-mobile/config.json`: auth token, FCM device tokens, optional `workspaceRoots` etc. Mode 600.
- `~/.config/herdr-mobile/firebase-service-account.json`: FCM key (Firebase project `herdr-client-android`, SA `firebase-adminsdk-fbsvc`).
- `android/app/google-services.json`: gitignored. Without it the app builds with push disabled.
- Recreate them with `firebase` / `gcloud`. gcloud's default account is a work account, so always pass `--account tohu.soy@gmail.com --project herdr-client-android`. Don't change the gcloud config.
- Herdr integrations are installed in `~/.claude/settings.json` and `~/.codex/hooks.json`. Codex needs the Herdr `SessionStart` hook marked trusted (Hooks review screen → `t`) or no session id is reported.

## Testing against real agents safely

- Use a separate Herdr session. Never experiment on the user's default session.
- Start it with the Claude Code env vars removed. Otherwise the spawned Claude inherits `CLAUDE_CODE_CHILD_SESSION` and doesn't save transcripts.
  ```bash
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_SESSION_ID -u CLAUDE_CODE_CHILD_SESSION \
      -u CLAUDE_CODE_EXECPATH -u CLAUDE_CODE_MESSAGING_SOCKET -u CLAUDE_CODE_MESSAGING_TOKEN \
      herdr --session hmtest server          # run in background
  HERDR_SESSION=hmtest herdr ...             # CLI against it
  ```
- Run a throwaway gateway with its own config/state (`HERDR_MOBILE_CONFIG=…`, `HERDR_MOBILE_STATE_DIR=…`, `HERDR_SESSION=hmtest`, `serve --listen 127.0.0.1:18765 --log-file ""`). Point `workspaceRoots` / `codexDaemonSocket` at scratch paths.
- Inside a Herdr pane `HERDR_SOCKET_PATH` is already set and wins over `HERDR_SESSION`, so the gateway would silently attach to the user's default session. Pass `HERDR_SOCKET_PATH=~/.config/herdr/sessions/hmtest/herdr.sock` explicitly and check the `herdr_socket` field in the startup log. The CLI needs `herdr --session hmtest …` for the same reason.
- Another session may already be using port 18765 or the `hmtest` name; pick your own when they are taken.
- Use `claude --model haiku` for cheap runs.
- Codex structured features need a shared app-server. For tests run `codex app-server --listen unix:///private/tmp/<dir>/cx.sock` and start the TUI with `codex --remote unix://…`. The socket's parent must be a real directory, not `/tmp` (a symlink). `-a untrusted` isn't accepted with `--remote`; use `-a on-request -s read-only` to force approvals. `request_user_input` only appears in Plan mode (`shift+tab` in the TUI).
- Pane state inspection: `herdr pane read <pane> --source visible`, `herdr pane send-keys <pane> <keys>`.
- Herdr's raw socket rejects JSON `null` for array params (the connection just closes). Send `[]`.
- Clean up afterwards:
  - `herdr --session hmtest server stop`
  - test workspaces
  - folders created in `~/workspace`
  - `[projects."…"] trust_level` entries Codex adds to `~/.codex/config.toml`

## Android device / emulator

- Emulator: `emulator -avd Pixel_9_Pro -no-window -no-audio -no-snapshot-save`. It reaches host loopback services at `10.0.2.2` (a throwaway gateway on `127.0.0.1:18765` is `http://10.0.2.2:18765`), but the real gateway listens on the Tailscale IP only, so use `http://100.99.15.34:8765` for it. Stop the emulator with `adb emu kill`.
- `adb shell screenrecord` dies with the shell that started it; start it, drive the UI and `adb pull` within one command.
- Real phone: Pixel 7a over USB. It reaches the gateway via Tailscale IP `100.99.15.34`; the MagicDNS name doesn't resolve on the phone.
- `adb shell input text` goes through the phone's Japanese IME and gets converted. Configure debug builds with:
  ```bash
  adb shell am start -n com.tohutohu.herdrmobile/.MainActivity --es gateway_url http://100.99.15.34:8765 --es token "$(herdr-mobile-gateway token)"
  ```
- Automating the user's phone is risky: taps landed in Developer options once and toggled "Pointer location". Before tapping, check `adb shell dumpsys window | grep mCurrentFocus` is the app, and find coordinates with `uiautomator dump`.
- FCM is not delivered to force-stopped apps. For push tests, background the app with HOME instead.

## Conventions

- Herdr API changes: run `herdr api schema --output /private/tmp/herdr-api-schema.json` and check request/response types and `protocol` before editing the client. `herdr api snapshot` reports live runtime state; do not rely only on CLI help or an older client struct.
- Test names are in Japanese. Parser tests use fixtures in `gateway/testdata/{claude,codex}` (anonymized real data) with golden JSON.
- New agent output formats: add the raw payload from dead letters as a fixture, then update the adapter.
- Session costs are priced by `gateway/internal/pricing` from list rates as of
  2026-09; add a row when an agent starts using a new model (an unknown model
  is left unpriced, so the app then shows no cost).
- Subscription limits come from the CodexBar CLI (`brew install --cask codexbar`); neither agent reports them itself. `herdr-mobile-gateway usage` prints one read.
- Claude dialog key sequences and trust-dialog texts were verified against Claude Code 2.1.274 and Codex 0.154. Re-verify with a test session when those versions change.
- Commit at each logical step. Commit messages end with the Co-Authored-By trailer; no "Generated with Claude Code" line.

## TypeSafe / Jev

- For Jev integration, prompt design, or evaluation work, read and use
  [the TypeSafe skill](.agents/skills/typesafe-ai/SKILL.md).
- It is installed project-locally for Codex with `npx skills add typesafe-ai/skills --skill typesafe-ai --agent codex --yes`;
  `skills-lock.json` records the installed source.
- Keep the API key in the existing private Gateway config (`jevApiKey`) or
  `TYPESAFE_API_KEY`; never put it in source, test fixtures, or command output.
