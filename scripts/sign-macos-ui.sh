#!/bin/bash
# Signs the Compose Mac UI (Herdr Companion.app) with the identity from
# scripts/macos-signing.sh: every Mach-O inside first, then the bundled Java
# runtime, then the app, as jpackage does. Compose signs only with a
# Developer ID; this covers the self-signed certificate and ad hoc.
set -euo pipefail

if [[ $# -ne 1 || ! -d "$1" ]]; then
  echo "usage: $0 'Herdr Companion.app'" >&2
  exit 2
fi
APP="$1"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=macos-signing.sh
source "$ROOT/scripts/macos-signing.sh"
herdr_use_signing
ENTITLEMENTS="$ROOT/macos/assets/desktop-ui.entitlements"
MAIN="$APP/Contents/MacOS/$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$APP/Contents/Info.plist")"

while IFS= read -r -d '' file; do
  [[ "$file" == "$MAIN" ]] && continue
  if file -b "$file" | grep -q 'Mach-O'; then
    if [[ -x "$file" && "$file" != *.dylib ]]; then
      herdr_codesign --options runtime --entitlements "$ENTITLEMENTS" "$file"
    else
      herdr_codesign --options runtime "$file"
    fi
  fi
done < <(find "$APP/Contents" -type f -print0)

if [[ -d "$APP/Contents/runtime" ]]; then
  herdr_codesign --options runtime "$APP/Contents/runtime"
fi
herdr_codesign --options runtime --entitlements "$ENTITLEMENTS" "$APP"
codesign --verify --deep --strict "$APP"
echo "signed $APP ($(codesign -d -r- "$APP" 2>/dev/null | sed -n 's/^designated => //p'))"
