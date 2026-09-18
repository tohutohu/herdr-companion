#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUTPUT="$ROOT/macos/build"
APP="$OUTPUT/Herdr Mobile.app"
ARCH="$(uname -m)"
if pgrep -f "$APP/Contents/MacOS/HerdrMenu" >/dev/null; then
  echo 'Quit the app in macos/build before rebuilding; replacing a running signed executable invalidates its code signature.' >&2
  exit 1
fi
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
swift build --package-path "$ROOT/macos" -c release
cp "$ROOT/macos/.build/release/HerdrMenu" "$APP/Contents/MacOS/HerdrMenu"
cp "$ROOT/macos/Info.plist" "$APP/Contents/Info.plist"
(
  cd "$ROOT/gateway"
  env -u GOROOT go build -trimpath -o "$APP/Contents/Resources/herdr-mobile-gateway" ./cmd/herdr-mobile-gateway
)
# Set SIGNING_IDENTITY to a Developer ID Application identity for public distribution.
IDENTITY="${SIGNING_IDENTITY:--}"
codesign --force --options runtime --sign "$IDENTITY" "$APP/Contents/Resources/herdr-mobile-gateway"
codesign --force --options runtime --sign "$IDENTITY" "$APP"
codesign --verify --deep --strict "$APP"
STAGING="$(mktemp -d "$OUTPUT/dmg.XXXXXX")"
trap 'rm -rf "$STAGING"' EXIT
ditto "$APP" "$STAGING/Herdr Mobile.app"
ln -s /Applications "$STAGING/Applications"
DMG="$OUTPUT/Herdr-Mobile-$ARCH.dmg"
hdiutil create -volname "Herdr Mobile" -srcfolder "$STAGING" -ov -format UDZO "$DMG"
if [[ -n "${NOTARY_PROFILE:-}" ]]; then
  if [[ "$IDENTITY" == "-" ]]; then echo 'Notarization requires SIGNING_IDENTITY.' >&2; exit 1; fi
  xcrun notarytool submit "$DMG" --keychain-profile "$NOTARY_PROFILE" --wait
  xcrun stapler staple "$DMG"
fi
shasum -a 256 "$DMG"
