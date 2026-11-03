#!/bin/bash
# Builds both Mac apps from this checkout and installs them into /Applications.
# The Mac UI is replaced right away. The Gateway Manager is replaced by a
# detached job once no session is running or waiting for an answer and no
# resident run is running or queued, because quitting it stops every agent turn,
# including the one that ran this script.
# Progress of the detached job: /tmp/herdr-gateway-swap.log
#
#   scripts/update-local-apps.sh            build and install both apps
#   scripts/update-local-apps.sh --gateway  only the Gateway Manager
#   scripts/update-local-apps.sh --ui       only the Mac UI
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GATEWAY_APP="/Applications/Herdr Companion Gateway.app"
UI_APP="/Applications/Herdr Companion.app"
LOG=/tmp/herdr-gateway-swap.log
RESIDENT_DB="$HOME/.local/state/herdr-mobile/desktop/resident.db"

busy_work() {
  local sessions
  # Unreachable gateway: nothing to protect, and its "running" rows are stale.
  sessions="$(hc ls --json 2>/dev/null)" || return 0
  jq -r '.sessions[]
    | select(.status == "running" or .status == "waiting_input" or .status == "waiting_approval")
    | "\(.short) \(.status) \(.project)"' <<<"$sessions" 2>/dev/null || true
  [[ -f "$RESIDENT_DB" ]] || return 0
  # Read last, right before the quit. A queued run counts because it starts as
  # soon as a slot frees up. hc resident ls can't say: a task's state hides its
  # queued run behind an open question or an interrupted run.
  sqlite3 -readonly "$RESIDENT_DB" "SELECT 'resident ' || substr(task_id, 6, 8) || ' ' || status
    FROM runs WHERE status IN ('running', 'queued') ORDER BY seq" 2>/dev/null \
    || echo "resident runs unreadable"
}

swap_gateway() {
  exec >>"$LOG" 2>&1
  echo "== $(date '+%F %T') waiting for sessions and resident runs to finish"
  while busy="$(busy_work)"; [[ -n "$busy" ]]; do
    [[ "$busy" != "${last:-}" ]] && printf '%s\n' "$busy"
    last="$busy"
    sleep 10
  done
  echo "== $(date '+%F %T') replacing the Gateway Manager"
  osascript -e 'tell application id "com.tohutohu.herdrcompanion.gateway" to quit' || true
  for _ in $(seq 1 40); do pgrep -f "$GATEWAY_APP/" >/dev/null || break; sleep 0.5; done
  if pgrep -fl "$GATEWAY_APP/"; then echo "still running after quit; not replaced"; exit 1; fi
  ditto "$ROOT/macos/build/Herdr Companion Gateway.app" "$GATEWAY_APP"
  open "$GATEWAY_APP"
  for _ in $(seq 1 30); do
    if hc ls >/dev/null 2>&1; then echo "gateway is up"; break; fi
    sleep 1
  done
  echo "== $(date '+%F %T') done ($(defaults read "$GATEWAY_APP/Contents/Info" CFBundleShortVersionString))"
}

if [[ "${1:-}" == "--swap-gateway-when-idle" ]]; then
  swap_gateway
  exit
fi

gateway=1 ui=1
case "${1:-}" in
  --gateway) ui=0 ;;
  --ui) gateway=0 ;;
  "") ;;
  *) echo "usage: $0 [--gateway|--ui]" >&2; exit 2 ;;
esac

if ((gateway)); then
  # bun is managed by mise and is not on the default PATH.
  bun_dir="$(ls -d "$HOME"/.local/share/mise/installs/bun/*/bin 2>/dev/null | sort -V | tail -n 1 || true)"
  PATH="${bun_dir:+$bun_dir:}$PATH" bash "$ROOT/macos/scripts/build-dmg.sh"
fi

if ((ui)); then
  (
    cd "$ROOT/android"
    export JAVA_HOME="$HOME/Library/Java/JavaVirtualMachines/jbr-17.0.14/Contents/Home"
    ./gradlew --console=plain :desktopApp:createReleaseDistributable
  )
  built="$(find "$ROOT/android/desktopApp/build/compose/binaries" -type d -name 'Herdr Companion.app' -print | sort | tail -n 1)"
  osascript -e 'tell application id "com.tohutohu.herdrcompanion.desktop" to quit' || true
  for _ in $(seq 1 40); do pgrep -f "$UI_APP/" >/dev/null || break; sleep 0.5; done
  ditto "$built" "$UI_APP"
  open "$UI_APP"
  echo "Mac UI replaced ($(defaults read "$UI_APP/Contents/Info" CFBundleShortVersionString))"
fi

if ((gateway)); then
  # Detach from this shell's session so quitting the Gateway, which ends the
  # agent that started this script, cannot take the job down with it.
  python3 -c 'import os, sys
if os.fork() == 0:
    os.setsid()
    devnull = os.open(os.devnull, os.O_RDWR)
    for fd in (0, 1, 2):
        os.dup2(devnull, fd)
    os.execv("/bin/bash", ["bash", sys.argv[1], "--swap-gateway-when-idle"])' "$0"
  echo "Gateway Manager will be replaced once no session or resident run is active (log: $LOG)"
  busy_work
fi
