#!/bin/bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "Usage: $0 APP DMG" >&2
  exit 2
fi

APP="$1"
DMG="$2"
REQUIRE_SIGNED="${REQUIRE_SIGNED:-0}"
EXPECT_NOTARIZED="${EXPECT_NOTARIZED:-0}"
ALLOW_UNSIGNED="${ALLOW_UNSIGNED:-0}"
APP_NAME="$(basename "$APP")"

for path in "$APP" "$DMG"; do
  [[ -e "$path" ]] || { echo "Missing release artifact: $path" >&2; exit 1; }
done

if codesign --verify --deep --strict "$APP" >/dev/null 2>&1; then
  if [[ "$REQUIRE_SIGNED" == 1 ]]; then
    if ! codesign -dv --verbose=4 "$APP" 2>&1 | grep -q '^Authority=Developer ID Application:'; then
      echo "Developer ID Application signature missing: $APP" >&2
      exit 1
    fi
    echo "Developer ID signature valid: $APP"
  else
    echo "codesign valid: $APP"
  fi
elif [[ "$REQUIRE_SIGNED" == 1 && "$ALLOW_UNSIGNED" != 1 ]]; then
  echo "codesign verification failed: $APP" >&2
  exit 1
else
  echo "unsigned local app allowed: $APP"
fi

if find "$APP" \( -name '.env' -o -name 'local.properties' -o -name '*service-account*.json' \
  -o -name 'google-services.json' -o -name '*.p12' -o -name '*.pem' -o -name '*.key' \) \
  -print -quit | grep -q .; then
  echo "Credential-like file found inside $APP" >&2
  exit 1
fi

hdiutil verify "$DMG" >/dev/null
MOUNT_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/herdr-mounted.XXXXXX")"
trap 'hdiutil detach "$MOUNT_ROOT" >/dev/null 2>&1 || true; rmdir "$MOUNT_ROOT" 2>/dev/null || true' EXIT
hdiutil attach -nobrowse -readonly -mountpoint "$MOUNT_ROOT" "$DMG" >/dev/null
[[ -d "$MOUNT_ROOT/$APP_NAME" ]] || { echo "$APP_NAME missing from DMG" >&2; exit 1; }
[[ -L "$MOUNT_ROOT/Applications" ]] || { echo "Applications shortcut missing from DMG" >&2; exit 1; }
if find "$MOUNT_ROOT" \( -name '.env' -o -name 'local.properties' -o -name '*service-account*.json' \
  -o -name 'google-services.json' -o -name '*.p12' -o -name '*.pem' -o -name '*.key' \) \
  -print -quit | grep -q .; then
  echo "Credential-like file found inside DMG" >&2
  exit 1
fi

if [[ "$EXPECT_NOTARIZED" == 1 ]]; then
  xcrun stapler validate "$DMG"
  spctl --assess --type execute --verbose=4 "$MOUNT_ROOT/$APP_NAME"
else
  echo "Gatekeeper and stapler checks skipped: notarization was not configured."
fi

hdiutil detach "$MOUNT_ROOT" >/dev/null
trap - EXIT
rmdir "$MOUNT_ROOT" 2>/dev/null || true
echo "DMG structure and credential scan valid: $DMG"
