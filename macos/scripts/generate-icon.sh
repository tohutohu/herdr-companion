#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SOURCE="$ROOT/macos/assets/Herdr.svg"
OUTPUT="$ROOT/macos/assets/Herdr.icns"

command -v rsvg-convert >/dev/null || {
  echo "rsvg-convert is required to generate the macOS icon (brew install librsvg)." >&2
  exit 1
}
command -v iconutil >/dev/null || {
  echo "iconutil is required; run this script on macOS with Xcode Command Line Tools." >&2
  exit 1
}

TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/herdr-icon.XXXXXX")"
ICONSET="$TEMP_DIR/Herdr.iconset"
mkdir "$ICONSET"
trap 'rm -rf "$TEMP_DIR"' EXIT

for size in 16 32 128 256 512; do
  rsvg-convert -w "$size" -h "$size" "$SOURCE" -o "$ICONSET/icon_${size}x${size}.png"
  double=$((size * 2))
  rsvg-convert -w "$double" -h "$double" "$SOURCE" -o "$ICONSET/icon_${size}x${size}@2x.png"
done

iconutil --convert icns --output "$OUTPUT" "$ICONSET"
echo "Generated $OUTPUT"
