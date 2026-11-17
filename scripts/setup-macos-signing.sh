#!/bin/bash
# Makes the self-signed "Herdr Companion Code Signing" certificate that every
# macOS build (Gateway Manager, Mac UI, hc) is signed with, so macOS keeps the
# permissions it granted across updates. See scripts/macos-signing.sh.
#
#   scripts/setup-macos-signing.sh                   create it (once per Mac; no-op if it exists)
#   scripts/setup-macos-signing.sh --github-secrets  also store it as the release workflow's
#                                                    MACOS_CERTIFICATE_P12_BASE64 / MACOS_CERTIFICATE_PASSWORD
#   scripts/setup-macos-signing.sh --export FILE.p12 write a password-protected backup (password on stderr)
#
# The certificate is valid for 20 years. Replacing it changes the designated
# requirement, so macOS asks for every permission once more.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=macos-signing.sh
source "$ROOT/scripts/macos-signing.sh"
OPENSSL=/usr/bin/openssl # LibreSSL writes PKCS#12 that `security import` reads

github=0 export_file=""
while (($#)); do
  case "$1" in
    --github-secrets) github=1 ;;
    --export) export_file="${2:?--export needs a file}"; shift ;;
    *) echo "usage: $0 [--github-secrets] [--export FILE.p12]" >&2; exit 2 ;;
  esac
  shift
done

WORK="$(mktemp -d "${TMPDIR:-/tmp}/herdr-signing.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

if [[ ! -f "$HERDR_SIGNING_KEYCHAIN" ]]; then
  cat >"$WORK/cert.cnf" <<EOF
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no
[dn]
CN = $HERDR_SIGNING_NAME
[ext]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
subjectKeyIdentifier = hash
EOF
  "$OPENSSL" req -x509 -newkey rsa:3072 -nodes -sha256 -days 7305 \
    -keyout "$WORK/key.pem" -out "$WORK/cert.pem" -config "$WORK/cert.cnf" 2>/dev/null
  p12_password="$("$OPENSSL" rand -hex 24)"
  "$OPENSSL" pkcs12 -export -inkey "$WORK/key.pem" -in "$WORK/cert.pem" -name "$HERDR_SIGNING_NAME" \
    -out "$WORK/identity.p12" -passout "pass:$p12_password"

  keychain_password="$("$OPENSSL" rand -hex 24)"
  security create-keychain -p "$keychain_password" "$HERDR_SIGNING_KEYCHAIN"
  security set-keychain-settings "$HERDR_SIGNING_KEYCHAIN" # never locks on a timer
  security unlock-keychain -p "$keychain_password" "$HERDR_SIGNING_KEYCHAIN"
  security import "$WORK/identity.p12" -k "$HERDR_SIGNING_KEYCHAIN" -P "$p12_password" \
    -T /usr/bin/codesign -T /usr/bin/security
  # Lets codesign use the key without a dialog, also from agents' builds.
  security set-key-partition-list -S apple-tool:,apple:,codesign: -s \
    -k "$keychain_password" "$HERDR_SIGNING_KEYCHAIN" >/dev/null
  security add-generic-password -U -s "$HERDR_SIGNING_SERVICE" -a keychain \
    -l "Herdr Companion signing keychain" -w "$keychain_password" -T /usr/bin/security
  echo "created $HERDR_SIGNING_KEYCHAIN"
fi

herdr_use_signing
echo "identity: $MACOS_SIGNING_IDENTITY \"$HERDR_SIGNING_NAME\""

# export_identity FILE PASSWORD: the identity as PKCS#12.
export_identity() {
  security export -k "$HERDR_SIGNING_KEYCHAIN" -t identities -f pkcs12 -P "$2" -o "$1" >/dev/null
}

if [[ -n "$export_file" ]]; then
  password="$("$OPENSSL" rand -hex 24)"
  export_identity "$export_file" "$password"
  chmod 600 "$export_file"
  echo "wrote $export_file; its password: $password" >&2
fi

if ((github)); then
  password="$("$OPENSSL" rand -hex 24)"
  export_identity "$WORK/ci.p12" "$password"
  base64 -i "$WORK/ci.p12" | tr -d '\n' | gh secret set MACOS_CERTIFICATE_P12_BASE64 --repo tohutohu/herdr-companion
  printf '%s' "$password" | gh secret set MACOS_CERTIFICATE_PASSWORD --repo tohutohu/herdr-companion
  echo "stored MACOS_CERTIFICATE_P12_BASE64 and MACOS_CERTIFICATE_PASSWORD for tohutohu/herdr-companion"
fi
