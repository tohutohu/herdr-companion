#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION_FILE="$ROOT/version.properties"
VERSION="$(awk -F= '$1 == "version" { print $2; exit }' "$VERSION_FILE")"
FILE_BUILD="$(awk -F= '$1 == "build" { print $2; exit }' "$VERSION_FILE")"
BUILD="${HERDR_BUILD:-${GITHUB_RUN_NUMBER:-$FILE_BUILD}}"

if [[ -z "$VERSION" || -z "$BUILD" ]]; then
  echo "version.properties must define version and build." >&2
  exit 1
fi
if [[ "${GITHUB_REF_NAME:-}" == v* && "${GITHUB_REF_NAME#v}" != "$VERSION" ]]; then
  echo "Git tag ${GITHUB_REF_NAME} does not match version.properties ($VERSION)." >&2
  exit 1
fi
if [[ "$(uname -m)" != "arm64" ]]; then
  echo "Herdr Desktop release packaging currently supports Apple Silicon (arm64) only." >&2
  exit 1
fi

if ! JAVA_HOME="$(/usr/libexec/java_home -v 17 2>/dev/null)" || [[ ! -x "$JAVA_HOME/bin/java" ]]; then
  echo "JDK 17 is required for Compose Desktop packaging." >&2
  exit 1
fi
export JAVA_HOME

if [[ ! -f "$ROOT/macos/assets/Herdr.icns" ]]; then
  "$ROOT/macos/scripts/generate-icon.sh"
fi

if [[ -z "${MACOS_SIGNING_IDENTITY:-}" && -n "${SIGNING_IDENTITY:-}" ]]; then
  MACOS_SIGNING_IDENTITY="$SIGNING_IDENTITY"
  export MACOS_SIGNING_IDENTITY
fi
if [[ -z "${MACOS_SIGNING_KEYCHAIN:-}" && -n "${SIGNING_KEYCHAIN:-}" ]]; then
  MACOS_SIGNING_KEYCHAIN="$SIGNING_KEYCHAIN"
  export MACOS_SIGNING_KEYCHAIN
fi

GRADLE_ARGS=(
  --console=plain
  :desktopApp:createReleaseDistributable
  "-Pherdr.version=$VERSION"
  "-Pherdr.build=$BUILD"
)
if [[ -n "${MACOS_SIGNING_IDENTITY:-}" ]]; then
  GRADLE_ARGS+=(
    -Pcompose.desktop.mac.sign=true
    "-Pcompose.desktop.mac.signing.identity=$MACOS_SIGNING_IDENTITY"
  )
fi
if [[ -n "${MACOS_SIGNING_KEYCHAIN:-}" ]]; then
  GRADLE_ARGS+=("-Pcompose.desktop.mac.signing.keychain=$MACOS_SIGNING_KEYCHAIN")
fi

NOTARY_COUNT=0
[[ -n "${APPLE_ID:-}" ]] && NOTARY_COUNT=$((NOTARY_COUNT + 1))
[[ -n "${APPLE_APP_SPECIFIC_PASSWORD:-}" ]] && NOTARY_COUNT=$((NOTARY_COUNT + 1))
[[ -n "${APPLE_TEAM_ID:-}" ]] && NOTARY_COUNT=$((NOTARY_COUNT + 1))
if (( NOTARY_COUNT > 0 && NOTARY_COUNT < 3 )); then
  echo "APPLE_ID, APPLE_APP_SPECIFIC_PASSWORD, and APPLE_TEAM_ID must be provided together." >&2
  exit 1
fi
if (( NOTARY_COUNT == 3 )) && [[ -z "${MACOS_SIGNING_IDENTITY:-}" || "${MACOS_SIGNING_IDENTITY}" == "-" ]]; then
  echo "Notarization requires a Developer ID signing identity." >&2
  exit 1
fi

(
  cd "$ROOT/android"
  ./gradlew "${GRADLE_ARGS[@]}"
)

UI_APP="$(find "$ROOT/android/desktopApp/build/compose/binaries" -type d -name 'Herdr.app' -print | sort | tail -n 1)"
if [[ -z "$UI_APP" || ! -d "$UI_APP" ]]; then
  echo "Compose release app was not found under desktopApp/build/compose/binaries." >&2
  exit 1
fi

DIST="$ROOT/dist"
mkdir -p "$DIST"
rm -rf "$DIST/Herdr.app" "$DIST/Herdr Companion.app"
ditto "$UI_APP" "$DIST/Herdr.app"

"$ROOT/scripts/package-ui-dmg.sh" \
  "$DIST/Herdr.app" "$VERSION" "$DIST/Herdr-$VERSION.dmg"

if (( NOTARY_COUNT == 3 )); then
  xcrun notarytool submit "$DIST/Herdr-$VERSION.dmg" \
    --apple-id "$APPLE_ID" \
    --password "$APPLE_APP_SPECIFIC_PASSWORD" \
    --team-id "$APPLE_TEAM_ID" \
    --wait
  xcrun stapler staple "$DIST/Herdr-$VERSION.dmg"
  xcrun stapler validate "$DIST/Herdr-$VERSION.dmg"
fi

HERDR_VERSION="$VERSION" HERDR_BUILD="$BUILD" DMG_OUTPUT="$DIST/Herdr-Companion-$VERSION.dmg" \
  bash "$ROOT/macos/scripts/build-dmg.sh"
ditto "$ROOT/macos/build/Herdr Companion.app" "$DIST/Herdr Companion.app"

REQUIRE_SIGNED=0
if [[ -n "${MACOS_SIGNING_IDENTITY:-}" && "${MACOS_SIGNING_IDENTITY}" != "-" ]]; then
  REQUIRE_SIGNED=1
fi
EXPECT_NOTARIZED=0
if (( NOTARY_COUNT == 3 )); then
  EXPECT_NOTARIZED=1
fi
ALLOW_UNSIGNED=0
[[ "$REQUIRE_SIGNED" == 0 ]] && ALLOW_UNSIGNED=1
REQUIRE_SIGNED="$REQUIRE_SIGNED" EXPECT_NOTARIZED="$EXPECT_NOTARIZED" ALLOW_UNSIGNED="$ALLOW_UNSIGNED" \
  "$ROOT/scripts/verify-macos-release.sh" \
  "$DIST/Herdr.app" "$DIST/Herdr-$VERSION.dmg"
REQUIRE_SIGNED="$REQUIRE_SIGNED" EXPECT_NOTARIZED="$EXPECT_NOTARIZED" ALLOW_UNSIGNED="$ALLOW_UNSIGNED" \
  "$ROOT/scripts/verify-macos-release.sh" \
  "$DIST/Herdr Companion.app" "$DIST/Herdr-Companion-$VERSION.dmg"

echo "Release artifacts:"
du -sh "$DIST/Herdr.app" "$DIST/Herdr Companion.app" \
  "$DIST/Herdr-$VERSION.dmg" "$DIST/Herdr-Companion-$VERSION.dmg"
echo "SHA-256: $DIST/Herdr-$VERSION.dmg.sha256"
echo "SHA-256: $DIST/Herdr-Companion-$VERSION.dmg.sha256"
