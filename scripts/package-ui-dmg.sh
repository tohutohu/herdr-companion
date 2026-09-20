#!/bin/bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "Usage: $0 HERDR_APP VERSION OUTPUT_DMG" >&2
  exit 2
fi

APP="$1"
VERSION="$2"
DMG="$3"

[[ -d "$APP" ]] || { echo "Missing Compose app: $APP" >&2; exit 1; }
[[ "$VERSION" =~ ^[1-9][0-9]*(\.[0-9]+){0,2}$ ]] || {
  echo "Version must start with a positive integer and contain at most three components: $VERSION" >&2
  exit 1
}

STAGING="$(mktemp -d "${TMPDIR:-/tmp}/herdr-ui-dmg.XXXXXX")"
trap 'rm -rf "$STAGING"' EXIT

mkdir -p "$(dirname "$DMG")"
rm -f "$DMG" "$DMG.sha256"
ditto "$APP" "$STAGING/Herdr Companion.app"
ln -s /Applications "$STAGING/Applications"
hdiutil create -volname "Herdr Companion" -srcfolder "$STAGING" -ov -format UDZO "$DMG"

(
  cd "$(dirname "$DMG")"
  shasum -a 256 "$(basename "$DMG")" > "$(basename "$DMG").sha256"
  cat "$(basename "$DMG").sha256"
)
echo "Created $DMG"
