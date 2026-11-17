# shellcheck shell=bash
# Code signing for the macOS builds (Gateway Manager, Mac UI, hc). Source it.
#
# macOS remembers permissions (keychain items, folders, screen recording) by
# an app's designated requirement. An ad-hoc signature makes that the hash of
# the build, so every update asks again. Signing every build with the same
# certificate keeps it at `identifier … and certificate leaf = H"…"`.
#
# Identity, in order:
#   1. MACOS_SIGNING_IDENTITY (+ MACOS_SIGNING_KEYCHAIN) from the caller, e.g. CI.
#   2. The self-signed "Herdr Companion Code Signing" identity in its own
#      keychain, made once by scripts/setup-macos-signing.sh. Its password sits
#      in the login keychain, so builds unlock it without a dialog.
#   3. Ad hoc ("-"), with a warning.

HERDR_SIGNING_NAME="Herdr Companion Code Signing"
HERDR_SIGNING_KEYCHAIN="${HERDR_SIGNING_KEYCHAIN:-$HOME/Library/Keychains/herdr-companion-signing.keychain-db}"
HERDR_SIGNING_SERVICE="herdr-companion-signing"

# herdr_add_keychain_to_search_list PATH: codesign only finds identities in
# keychains on the user search list.
herdr_add_keychain_to_search_list() {
  local keychain="$1" line
  local -a current=()
  while IFS= read -r line; do
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line#\"}"
    line="${line%\"}"
    [[ -n "$line" ]] && current+=("$line")
  done < <(security list-keychains -d user)
  for line in "${current[@]}"; do
    [[ "$line" == "$keychain" ]] && return 0
  done
  security list-keychains -d user -s "${current[@]}" "$keychain"
}

# herdr_identity_hash KEYCHAIN NAME: SHA-1 of the code signing identity NAME.
# Self-signed identities are not "valid" (not trusted), so no -v.
herdr_identity_hash() {
  security find-identity -p codesigning "$1" \
    | awk -v name="\"$2\"" 'index($0, name) && $2 ~ /^[0-9A-F]{40}$/ { print $2; exit }'
}

# herdr_use_signing: sets and exports MACOS_SIGNING_IDENTITY and
# MACOS_SIGNING_KEYCHAIN unless the caller already chose an identity.
herdr_use_signing() {
  if [[ -n "${MACOS_SIGNING_IDENTITY:-}" ]]; then
    export MACOS_SIGNING_IDENTITY MACOS_SIGNING_KEYCHAIN
    return 0
  fi
  if [[ ! -f "$HERDR_SIGNING_KEYCHAIN" ]]; then
    echo "warning: $HERDR_SIGNING_KEYCHAIN not found; signing ad hoc, so macOS will ask for its permissions again." >&2
    echo "warning: run scripts/setup-macos-signing.sh once to sign every build with the same certificate." >&2
    MACOS_SIGNING_IDENTITY="-"
    export MACOS_SIGNING_IDENTITY
    return 0
  fi
  local password identity
  password="$(security find-generic-password -s "$HERDR_SIGNING_SERVICE" -a keychain -w)" || {
    echo "The password of $HERDR_SIGNING_KEYCHAIN is missing from the login keychain ($HERDR_SIGNING_SERVICE)." >&2
    return 1
  }
  security unlock-keychain -p "$password" "$HERDR_SIGNING_KEYCHAIN"
  herdr_add_keychain_to_search_list "$HERDR_SIGNING_KEYCHAIN"
  identity="$(herdr_identity_hash "$HERDR_SIGNING_KEYCHAIN" "$HERDR_SIGNING_NAME")"
  if [[ -z "$identity" ]]; then
    echo "No \"$HERDR_SIGNING_NAME\" identity in $HERDR_SIGNING_KEYCHAIN." >&2
    return 1
  fi
  MACOS_SIGNING_IDENTITY="$identity"
  MACOS_SIGNING_KEYCHAIN="$HERDR_SIGNING_KEYCHAIN"
  export MACOS_SIGNING_IDENTITY MACOS_SIGNING_KEYCHAIN
}

# herdr_is_developer_id: whether MACOS_SIGNING_IDENTITY is an Apple Developer
# ID (timestamped, notarizable, signed through Compose) rather than ad hoc or
# the self-signed certificate.
herdr_is_developer_id() {
  local identity="${MACOS_SIGNING_IDENTITY:--}"
  [[ "$identity" == "-" ]] && return 1
  [[ "$identity" == "Developer ID Application:"* ]] && return 0
  security find-identity -p codesigning ${MACOS_SIGNING_KEYCHAIN:+"$MACOS_SIGNING_KEYCHAIN"} \
    | grep -F "$identity" | grep -q '"Developer ID Application:'
}

# herdr_codesign ARGS... PATH: codesign with the chosen identity.
herdr_codesign() {
  local -a args=(--force --sign "${MACOS_SIGNING_IDENTITY:--}")
  if herdr_is_developer_id; then
    args+=(--timestamp)
  else
    args+=(--timestamp=none)
  fi
  if [[ -n "${MACOS_SIGNING_KEYCHAIN:-}" ]]; then
    args+=(--keychain "$MACOS_SIGNING_KEYCHAIN")
  fi
  codesign "${args[@]}" "$@"
}
