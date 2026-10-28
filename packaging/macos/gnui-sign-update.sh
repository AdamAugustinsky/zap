#!/usr/bin/env bash
# Writes the update feed's latest.json for a release zip made by
# `gnui package -zip` (packaging/macos/gnui-package.sh): the file's name and
# SHA-256, and the publisher's Ed25519 signature over "<sha256>  <file>\n" in
# hex, which is what gnui/ui/settings (updater.go manifestLine) checks before
# the framework's selfupdate.Prepare ever sees the archive.
#
#   gnui-sign-update.sh ZIP PRIVATE_KEY.pem VERSION URL [NOTES]
#
# PRIVATE_KEY.pem is the publisher's Ed25519 PKCS#8 key (never in the checkout;
# see ../UPDATE_SIGNING.md). It needs an OpenSSL that signs Ed25519 (3.x, as
# Homebrew's; the system's LibreSSL does not). The signature is checked against
# the keys embedded in the app (the current one and the next) before latest.json
# is written, so a wrong key fails here and not on users' computers. latest.json goes beside the zip.
set -euo pipefail
if [ $# -lt 4 ]; then
    sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
    exit 2
fi
zip=$1 key=$2 version=$3 url=$4 notes=${5:-}
here=$(cd "$(dirname "$0")" && pwd)
pub=${UPDATE_PUBLIC_KEY_FILE:-"$here/../../gnui/ui/settings/update-public-key.hex"} # the overrides are for tests
pubnext=${UPDATE_NEXT_PUBLIC_KEY_FILE:-"$here/../../gnui/ui/settings/update-public-key-next.hex"}
openssl=${OPENSSL:-$(command -v openssl)}
[ -f "$zip" ] || { echo "no such file: $zip" >&2; exit 1; }
file=$(basename "$zip")
# the app accepts only the name `gnui package -zip` gives a version's archive
# (updater.go releaseFileName): a signature must not make one release's file
# pass for another version
[ "$file" = "ZapFast-${version#v}.zip" ] || { echo "the file must be named ZapFast-${version#v}.zip, not $file" >&2; exit 1; }
sha=$(shasum -a 256 "$zip" | awk '{print $1}')
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
printf '%s  %s\n' "$sha" "$file" >"$tmp/manifest"
"$openssl" pkeyutl -sign -inkey "$key" -rawin -in "$tmp/manifest" -out "$tmp/sig"
[ "$(wc -c <"$tmp/sig" | tr -d ' ')" = 64 ] || { echo "the signature is not 64 bytes" >&2; exit 1; }
sig=$(xxd -p -c 256 "$tmp/sig")
# the embedded public keys (the current release key and the next one, both
# trusted by the app) are 32 raw bytes in hex; wrap each as a SubjectPublicKeyInfo
# to check with. The key signs if it matches either.
verified=
for pubfile in "$pub" "$pubnext"; do
    { printf '302a300506032b6570032100'; tr -d '[:space:]' <"$pubfile"; } | xxd -r -p >"$tmp/pub.der"
    if "$openssl" pkeyutl -verify -pubin -inkey "$tmp/pub.der" -keyform DER -rawin -in "$tmp/manifest" -sigfile "$tmp/sig" >/dev/null 2>&1; then
        verified=1
        break
    fi
done
[ -n "$verified" ] || { echo "the signature does not verify under either key embedded in the app (gnui/ui/settings/update-public-key.hex, update-public-key-next.hex): wrong key" >&2; exit 1; }
python3 - "$version" "$url" "$notes" "$file" "$sha" "$sig" >"$(dirname "$zip")/latest.json" <<'PY'
import json, sys
version, url, notes, file, sha, sig = sys.argv[1:]
print(json.dumps({"version": version, "url": url, "notes": notes, "file": file, "sha256": sha, "signature": sig}, indent=2))
PY
echo "wrote $(dirname "$zip")/latest.json ($file, sha256 $sha)"
