#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUTPUT="$ROOT/macos/build"
VERSION_FILE="$ROOT/version.properties"
VERSION="$(awk -F= '$1 == "version" { print $2; exit }' "$VERSION_FILE")"
BUILD="$(awk -F= '$1 == "build" { print $2; exit }' "$VERSION_FILE")"
VERSION="${HERDR_VERSION:-$VERSION}"
BUILD="${HERDR_BUILD:-$BUILD}"
APP="$OUTPUT/Herdr Companion.app"
DMG_OUTPUT="${DMG_OUTPUT:-}"

if [[ ! "$VERSION" =~ ^[1-9][0-9]*(\.[0-9]+){0,2}$ ]]; then
  echo "Version must start with a positive integer and contain at most three components: $VERSION" >&2
  exit 1
fi
if [[ ! "$BUILD" =~ ^[1-9][0-9]*$ ]]; then
  echo "Build must be a positive integer: $BUILD" >&2
  exit 1
fi

if [[ ! -f "$ROOT/macos/assets/Herdr.icns" ]]; then
  echo "Missing macOS icon: $ROOT/macos/assets/Herdr.icns (run macos/scripts/generate-icon.sh)" >&2
  exit 1
fi

if pgrep -f "$APP/Contents/MacOS/HerdrMenu" >/dev/null; then
  echo "Quit Herdr Companion before rebuilding; replacing a running app invalidates its code signature." >&2
  exit 1
fi

mkdir -p "$OUTPUT"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

swift build --package-path "$ROOT/macos" -c release
cp "$ROOT/macos/.build/release/HerdrMenu" "$APP/Contents/MacOS/HerdrMenu"
sed -e "s/__HERDR_VERSION__/$VERSION/g" -e "s/__HERDR_BUILD__/$BUILD/g" \
  "$ROOT/macos/Info.plist.in" > "$APP/Contents/Info.plist"
cp "$ROOT/macos/assets/Herdr.icns" "$APP/Contents/Resources/Herdr.icns"

(
  cd "$ROOT/gateway"
  env -u GOROOT go build -trimpath -o "$APP/Contents/Resources/herdr-mobile-gateway" ./cmd/herdr-mobile-gateway
)

# The UI app is signed by Compose/jpackage. The SwiftUI companion and its Go
# helper are signed here in the required nested-code order.
IDENTITY="${MACOS_SIGNING_IDENTITY:-${SIGNING_IDENTITY:--}}"
KEYCHAIN="${MACOS_SIGNING_KEYCHAIN:-${SIGNING_KEYCHAIN:-}}"
sign_args=(--force --options runtime)
if [[ "$IDENTITY" != "-" ]]; then
  sign_args+=(--timestamp)
fi
if [[ -n "$KEYCHAIN" ]]; then
  sign_args+=(--keychain "$KEYCHAIN")
fi
codesign "${sign_args[@]}" --sign "$IDENTITY" "$APP/Contents/Resources/herdr-mobile-gateway"
codesign "${sign_args[@]}" --sign "$IDENTITY" "$APP"
codesign --verify --deep --strict "$APP"

if [[ -n "$DMG_OUTPUT" ]]; then
  DMG="$DMG_OUTPUT"
else
  # Keep the historical companion-only output for direct Gateway development.
  ARCH="$(uname -m)"
  DMG="$OUTPUT/Herdr-Companion-$ARCH.dmg"
fi
VOLUME_NAME="Herdr Companion"
mkdir -p "$(dirname "$DMG")"
rm -f "$DMG" "$DMG.sha256"

STAGING="$(mktemp -d "${TMPDIR:-/tmp}/herdr-dmg.XXXXXX")"
trap 'rm -rf "$STAGING"' EXIT
ditto "$APP" "$STAGING/Herdr Companion.app"
ln -s /Applications "$STAGING/Applications"
hdiutil create -volname "$VOLUME_NAME" -srcfolder "$STAGING" -ov -format UDZO "$DMG"

notary_count=0
[[ -n "${APPLE_ID:-}" ]] && notary_count=$((notary_count + 1))
[[ -n "${APPLE_APP_SPECIFIC_PASSWORD:-}" ]] && notary_count=$((notary_count + 1))
[[ -n "${APPLE_TEAM_ID:-}" ]] && notary_count=$((notary_count + 1))
if (( notary_count > 0 && notary_count < 3 )); then
  echo "APPLE_ID, APPLE_APP_SPECIFIC_PASSWORD, and APPLE_TEAM_ID must be provided together." >&2
  exit 1
fi
if (( notary_count == 3 )); then
  if [[ "$IDENTITY" == "-" ]]; then
    echo "Notarization requires a Developer ID signing identity." >&2
    exit 1
  fi
  xcrun notarytool submit "$DMG" \
    --apple-id "$APPLE_ID" \
    --password "$APPLE_APP_SPECIFIC_PASSWORD" \
    --team-id "$APPLE_TEAM_ID" \
    --wait
  xcrun stapler staple "$DMG"
  xcrun stapler validate "$DMG"
fi

(
  cd "$(dirname "$DMG")"
  shasum -a 256 "$(basename "$DMG")" > "$(basename "$DMG").sha256"
  cat "$(basename "$DMG").sha256"
)
echo "Created $DMG"
