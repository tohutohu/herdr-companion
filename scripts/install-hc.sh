#!/bin/bash
# Builds hc from source and installs it signed with the same certificate as
# the releases (scripts/macos-signing.sh), so hc update can later replace it
# and macOS keeps what it allowed hc.
#
#   scripts/install-hc.sh [DEST]   default ~/.local/bin/hc
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="${1:-$HOME/.local/bin/hc}"
mkdir -p "$(dirname "$DEST")"
TMP="$(mktemp "$(dirname "$DEST")/.hc-build.XXXXXX")"
trap 'rm -f "$TMP"' EXIT

(cd "$ROOT/gateway" && env -u GOROOT go build -trimpath -o "$TMP" ./cmd/hc)
if [[ "$(uname -s)" == Darwin ]]; then
  # shellcheck source=macos-signing.sh
  source "$ROOT/scripts/macos-signing.sh"
  herdr_use_signing
  herdr_codesign --options runtime --identifier com.tohutohu.herdrcompanion.hc "$TMP"
fi
chmod 755 "$TMP"
mv -f "$TMP" "$DEST" # a new file, so a running hc keeps its own copy
trap - EXIT
echo "installed $("$DEST" version) at $DEST"
