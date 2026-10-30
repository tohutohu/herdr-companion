# AGENTS.md

Herdr Companion: Go gateway on the Mac (`gateway/`) + native Android app (`android/`).
The gateway runs Claude Code (Agent SDK sidecar, `gateway/claude-sidecar/`) and Codex (its own
`codex app-server`) directly. Since 2.0 these are the only agents and the only way sessions run:
Herdr panes, OpenCode and Devin are no longer supported (agents may still use Herdr as a tool).
Design: `docs/architecture.md`. Setup for humans: `README.md`.

用語: 「Mac UI」はCompose Desktop版の`Herdr Companion.app`
（`android/desktopApp/`）を指す。メニューバーのSwiftUI Gateway Manager
（`Herdr Companion Gateway.app`）とは別アプリであり、「Mac UI」の再ビルド・再起動ではGateway Managerを更新しない。

## Commands

```bash
# Gateway
cd gateway
go test ./...                                  # must pass
go test ./internal/providers/claude ./internal/providers/codex -update  # regenerate golden JSON after intended parser changes
go vet ./...
env -u GOROOT go test ./...                    # avoid inherited GOROOT/toolchain mismatch
# Production updates: rebuild the Mac Gateway app, replace /Applications/Herdr Companion Gateway.app, reopen it.
herdr-mobile-gateway token | devices | usage | notify-test | debug replay FILE
env -u GOROOT go build -o ~/.local/bin/hc ./cmd/hc  # agent-facing client CLI (docs/cli.md); `hc guide`

# Claude sidecar (Bun + Claude Agent SDK; bundled next to the gateway in the Mac app)
cd gateway/claude-sidecar
bun install && bun run typecheck && bun run build    # → dist/herdr-claude-sidecar

# Android (JDK 17 required; the default `java` is 24)
cd android
export JAVA_HOME=$HOME/Library/Java/JavaVirtualMachines/jbr-17.0.14/Contents/Home
./gradlew --console=plain assembleRelease          # default for APK build requests (R8 + resource shrinking)
./gradlew --console=plain assembleDebug testDebugUnitTest  # includes Robolectric API 35 Room/worker tests
./gradlew --console=plain :shared:allTests :gatewayClient:test :desktopApp:test :app:lintDebug :app:lintRelease
ANDROID_SERIAL=<serial> ./gradlew installDebug

# Compose Mac UI (JDK 17; separate from the menu-bar Gateway Manager)
cd android
export JAVA_HOME=$HOME/Library/Java/JavaVirtualMachines/jbr-17.0.14/Contents/Home
./gradlew --console=plain :desktopApp:createReleaseDistributable
osascript -e 'tell application id "com.tohutohu.herdrcompanion.desktop" to quit'
ditto "$(find desktopApp/build/compose/binaries -type d -name 'Herdr Companion.app' -print | sort | tail -n 1)" "/Applications/Herdr Companion.app"
open "/Applications/Herdr Companion.app"
# Linux .deb of the same UI: must be built on Linux, with libegl1 installed so jpackage records it as a dependency (Skiko links it on arm64)
./gradlew --console=plain :desktopApp:packageReleaseDeb

# Mac menu-bar app (macOS 13+, Swift and Go)
bash macos/scripts/build-dmg.sh                 # native-architecture DMG, ad-hoc signed by default
swift test --package-path macos                 # updater logic tests
```

Resident tasks (常駐, `gateway/internal/resident`, `docs/resident.md`) keep their own state in `resident.db`; their runs never go
through `internal/runtime`. The opt-in Mac UI flow test against a throwaway gateway is
`HERDR_RESIDENT_E2E_URL=… HERDR_RESIDENT_E2E_TOKEN=… HERDR_RESIDENT_E2E_CWD=… ./gradlew :desktopApp:test --tests '*DesktopResidentFlowTest*' --rerun`.

Both Mac apps update themselves from the latest GitHub Release (`docs/macos-release.md#アプリ内アップデート`); keep the release asset names stable.

### APK build requests

- 「APKをビルドして」 means `assembleRelease`: R8 (`isMinifyEnabled = true`) and resource shrinking (`isShrinkResources = true`) must both be enabled. Do not deliver a debug APK unless the user explicitly requests one.
- Before reporting completion, confirm the release build succeeded and link `android/app/build/outputs/apk/release/app-release.apk`.
- Local release builds use the existing debug signing key. Release CI restores the persistent Mac key from ANDROID_KEYSTORE_* / ANDROID_KEY_* secrets; never publish with a fresh runner's generated key. This does not disable R8 or resource shrinking.
- 実機が adb に接続されていない場合は、USB 接続を依頼したり接続待ちしたりせず、ビルド済み release APK のパスを提示する。

`compileSdk` is 37 because Coil 3.6 requires it (targetSdk stays 36).

Screens are shown with Navigation 3 (`ui/AppNavigation.kt`): the back stack is a list of `Route` keys, and screen motion, including the predictive back animation, lives in `ui/Motion.kt`. Use `NavigationBackHandler` (navigationevent) rather than `BackHandler` for in-screen back handling.

## What runs on this Mac (persistent)

| What | How | Notes |
|---|---|---|
| Herdr server | LaunchAgent `com.herdr-mobile.herdr-server` | the user's own Herdr; the Gateway no longer uses it (2.0+) |
| Gateway | `/Applications/Herdr Companion Gateway.app` (menu-bar app, login item) | bundled child process; `100.99.15.34:8765` |
| Cloudflare tunnel | LaunchAgent `com.herdr-mobile.cloudflare-tunnel` | HTTPS route to the Gateway from outside the tailnet (`docs/api.md`) |

```bash
launchctl list | grep herdr-mobile
# Gateway updates (quit the menu-bar app before replacing its signed bundle):
osascript -e 'tell application id "com.tohutohu.herdrcompanion.gateway" to quit'
bash macos/scripts/build-dmg.sh
ditto "macos/build/Herdr Companion Gateway.app" "/Applications/Herdr Companion Gateway.app"
open "/Applications/Herdr Companion Gateway.app"
```

- Restarting the Herdr server (`bootout`/`bootstrap`) kills the user's panes. Don't do it without asking.
- Gateway LaunchAgent is retired; do not bootstrap it or update the old standalone binary for production. The menu-bar app auto-starts its bundled Gateway.
- Gateway logs: `~/.local/state/herdr-mobile/desktop/gateway.log`. Dead letters: `~/.local/state/herdr-mobile/desktop/errors/*.jsonl`.

## Persistent state and secrets (never commit)

- Active config: `~/.config/herdr-mobile/desktop/config.json`: auth token, FCM device tokens, optional `workspaceRoots` etc. Mode 600. For CLI diagnostics set `HERDR_MOBILE_CONFIG` to this path and use the app-bundled executable. The old root config is a migration backup, not active state.
- FCM key (Firebase project `herdr-client-android`, SA `firebase-adminsdk-fbsvc`): the file named by `fcmCredentialsFile` in the active config (`~/.config/herdr-mobile/desktop/firebase-credentials-*.json`, written by `import-firebase`). The bundled Gateway is started without `GOOGLE_APPLICATION_CREDENTIALS`, so `desktop/firebase-service-account.json` is not what it reads.
- `android/app/google-services.json`: gitignored. Shared APKs use Firebase settings received at QR pairing; only `-PbundleFirebase=true` embeds this file in a personal build.
- Mac menu-bar app uses separate `~/.config/herdr-mobile/desktop/config.json` and `~/.local/state/herdr-mobile/desktop/`, port 8766 by default (this Mac retains 8765 after migration). It manages only its own bundled Gateway. Firebase and Jev keys are optional. See `macos/README.md`.
- Recreate them with `firebase` / `gcloud`. gcloud's default account is a work account, so always pass `--account tohu.soy@gmail.com --project herdr-client-android`. Don't change the gcloud config.
- Old 1.x config keys (`herdrSocket`, `opencode`, `devin`, `codexDaemonSocket`) are ignored. The Herdr integrations in `~/.claude/settings.json` / `~/.codex/hooks.json` belong to the user's Herdr; leave them.

## Testing against real agents safely

- Run a throwaway gateway with its own config/state
  (`HERDR_MOBILE_CONFIG=…`, `HERDR_MOBILE_STATE_DIR=…`, `serve --listen 127.0.0.1:18799 --log-file …`)
  and `HERDR_CLAUDE_SIDECAR=gateway/claude-sidecar/dist/herdr-claude-sidecar`. Put
  `workspaceRoots` on a scratch git repo. Another session may already be using a port; pick your own.
- Start it with the Claude Code env vars removed (the gateway also strips them for the agents it
  starts; a Claude that inherits `CLAUDE_CODE_CHILD_SESSION` doesn't save transcripts):
  ```bash
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_SESSION_ID -u CLAUDE_CODE_CHILD_SESSION \
      -u CLAUDE_CODE_EXECPATH -u CLAUDE_CODE_MESSAGING_SOCKET -u CLAUDE_CODE_MESSAGING_TOKEN \
      herdr-mobile-gateway serve --listen 127.0.0.1:18799 --log-file ""
  ```
- Claude sessions write real transcripts under `~/.claude/projects/` for the scratch path, and Codex
  threads go to `~/.codex/sessions`; use `claude` model `haiku` and a small Codex model with tiny prompts.
- A start in a folder the agent never trusted returns `trustRequired`; pass `trust: true`.
  It does not write any trust entry.
- `request_user_input` only appears in Codex's Plan mode (start with `mode: "plan"`).
- Clean up afterwards:
  - the scratch config/state directories and test sessions (archive them)
  - folders created in `~/workspace`
  - `[projects."…"] trust_level` entries Codex adds to `~/.codex/config.toml`

## Android device / emulator

- Emulator: follow the shared contract in `~/workspace/AGENTS.md`: create an isolated AVD with `~/.local/bin/android-emulator-standard HERDR_<PURPOSE>_API37`, start it on its own port with `-no-window -no-audio -no-snapshot-save`, pass `ANDROID_SERIAL` explicitly and stop it with `adb -s <serial> emu kill`. It reaches host loopback services at `10.0.2.2` (a throwaway gateway on `127.0.0.1:18765` is `http://10.0.2.2:18765`), but the real gateway listens on the Tailscale IP only, so use `http://100.99.15.34:8765` for it.
- `adb shell screenrecord` dies with the shell that started it; start it, drive the UI and `adb pull` within one command.
- Real phone: Pixel 7a over USB. It reaches the gateway via Tailscale IP `100.99.15.34`; the MagicDNS name doesn't resolve on the phone.
- `adb shell input text` goes through the phone's Japanese IME and gets converted. Configure debug builds with the DUMP-guarded receiver (only adb can send it), then launch the app:
  ```bash
  adb shell am broadcast -n com.tohutohu.herdrcompanion/.DebugConfigReceiver --es gateway_url http://100.99.15.34:8765 --es token "$(HERDR_MOBILE_CONFIG="$HOME/.config/herdr-mobile/desktop/config.json" "/Applications/Herdr Companion Gateway.app/Contents/Resources/herdr-mobile-gateway" token)"
  adb shell am start -n com.tohutohu.herdrcompanion/.MainActivity
  ```
- Automating the user's phone is risky: taps landed in Developer options once and toggled "Pointer location". Before tapping, check `adb shell dumpsys window | grep mCurrentFocus` is the app, and find coordinates with `uiautomator dump`.
- FCM is not delivered to force-stopped apps. For push tests, background the app with HOME instead.

## Conventions

- Test names are in Japanese. Parser tests use fixtures in `gateway/testdata/{claude,codex}` (anonymized real data) with golden JSON.
- New agent output formats: add the raw payload from dead letters as a fixture, then update the adapter.
- Session costs are priced by `gateway/internal/pricing` from list rates as of
  2026-09; add a row when an agent starts using a new model (an unknown model
  is left unpriced, so the app then shows no cost).
- Subscription limits come from the CodexBar CLI (`brew install --cask codexbar`); neither agent reports them itself. `herdr-mobile-gateway usage` prints one read.
- Commit at each logical step. Commit messages end with the Co-Authored-By trailer; no "Generated with Claude Code" line.

## Android skills

- Official Android skills live in `.agents/skills/` (Codex) and `.claude/skills/` (Claude Code):
  android-cli, navigation-3, navigation-event, edge-to-edge, testing-setup, r8-analyzer,
  android-profiler, android-intent-security.
- They come from the Android CLI (`/opt/homebrew/bin/android`). List with `android skills list --long`;
  install or update with `android skills add --skill=<name> --project=. --agent=codex,claude-code`.

## TypeSafe / Jev

- For Jev integration, prompt design, or evaluation work, read and use
  [the TypeSafe skill](.agents/skills/typesafe-ai/SKILL.md).
- It is installed project-locally for Codex with `npx skills add typesafe-ai/skills --skill typesafe-ai --agent codex --yes`;
  `skills-lock.json` records the installed source.
- Keep the API key in the existing private Gateway config (`jevApiKey`) or
  `TYPESAFE_API_KEY`; never put it in source, test fixtures, or command output.

### Quality audit follow-up

- Current findings and validation: `docs/engineering-audit.md`.
- Uploads are streamed in 64 KiB buffers, up to 500 MiB on Android/Desktop/Gateway; do not restore whole-file `readBytes()` for uploads.
- Run only one Gradle process per checkout: concurrent builds can corrupt shared module jar outputs.
- App JVM tests use Robolectric API 35 with a plain Application for isolated Room migration/refresh and WorkManager Data checks; they never connect to the user's Gateway.
- Notification replies persist their body under private `files/notification-replies`; WorkManager Data carries only the UUID. Gateway message-delivery receipts are durable state: do not delete them as disposable cache, since delayed retries must not execute a send twice.
