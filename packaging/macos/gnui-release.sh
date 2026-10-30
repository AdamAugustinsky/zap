#!/usr/bin/env bash
# Builds, notarizes and signs a Zap release, and with --publish publishes it as
# the latest GitHub release of AdamAugustinsky/zap, which every release build's
# updater reads (gnui/ui/settings/feed_release.go).
#
#   packaging/macos/gnui-release.sh [--notes TEXT] [--publish]
#
# Runs on the Mac that holds the a3-release keychain: it unlocks it with
# ~/.a3-signing/keychain.pass (never printed), runs gnui-package.sh --dmg
# --notarize with A3's Developer ID and the keychain's a3-notary profile, reads
# the version and revision from the bundle's build-manifest.json, and refuses
# a dirty checkout or a signer other than A3's team. It then writes the update
# key from the keychain (zap-update-ed25519, or $ZAP_UPDATE_KEY_ITEM) to a
# 0600 file under .cache/release, which is removed on exit, error included,
# and runs gnui-sign-update.sh to write latest.json. Never run this with shell
# tracing on.
#
# Without --publish it stops there and prints the gh command: publishing
# starts every installed copy updating, so it is a separate, deliberate step
# (../UPDATE_SIGNING.md). The release's assets are ZapFast-<version>.zip (the
# update archive), latest.json, Zap.dmg and SHA256SUMS, under .cache/release.
set -euo pipefail
set +x

publish=0
notes=
while [ $# -gt 0 ]; do
    case "$1" in
    --publish) publish=1; shift ;;
    --notes) notes=$2; shift 2 ;;
    --notes=*) notes=${1#--notes=}; shift ;;
    *) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2 ;;
    esac
done

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
repo=AdamAugustinsky/zap
team=JPXN94NG33
keychain=${A3_RELEASE_KEYCHAIN:-$HOME/Library/Keychains/a3-release.keychain-db}
passfile=${A3_RELEASE_KEYCHAIN_PASS:-$HOME/.a3-signing/keychain.pass}
keyitem=${ZAP_UPDATE_KEY_ITEM:-zap-update-ed25519}
export CODESIGN_IDENTITY=${CODESIGN_IDENTITY:-"Developer ID Application: A3 Direct Solutions LLP ($team)"}
export GNUI_NOTARY_PROFILE=${GNUI_NOTARY_PROFILE:-a3-notary}
export GNUI_NOTARY_KEYCHAIN=${GNUI_NOTARY_KEYCHAIN:-$keychain}
export OPENSSL=${OPENSSL:-/opt/homebrew/opt/openssl@3/bin/openssl}

[ -x "$OPENSSL" ] || { echo "no OpenSSL 3 at $OPENSSL (brew install openssl@3, or set OPENSSL)" >&2; exit 1; }
if [ -n "$(git -C "$root" status --porcelain --untracked-files=normal)" ]; then
    echo "the checkout has uncommitted changes: a release is built from a commit" >&2
    exit 1
fi

# The keychain locks again at every reboot.
security unlock-keychain -p "$(cat "$passfile")" "$keychain"

"$here/gnui-package.sh" --dmg --notarize

app="$root/dist/Zap.app"
read -r version revision dirty < <(python3 - "$app/Contents/Resources/build-manifest.json" <<'PY'
import json, sys
a = json.load(open(sys.argv[1]))["app"]
print(a["version"], a["revision"], str(a["dirty"]).lower())
PY
)
[ "$dirty" = false ] && [ "$revision" = "$(git -C "$root" rev-parse HEAD)" ] || {
    echo "the bundle was built from $revision (dirty: $dirty), not HEAD" >&2
    exit 1
}
codesign -dv "$app" 2>&1 | grep -qx "TeamIdentifier=$team" || {
    echo "dist/Zap.app is not signed by team $team" >&2
    exit 1
}
tag=v$version
if gh release view "$tag" --repo "$repo" >/dev/null 2>&1; then
    echo "$repo already has a release $tag: bump gnui/gnui.json and state.Version first" >&2
    exit 1
fi

out="$root/.cache/release/$version"
rm -rf "$out"
mkdir -p "$out"
zip="$out/ZapFast-$version.zip"
cp "$root/dist/ZapFast-$version.zip" "$zip"
cp "$root/dist/ZapFast-$version.dmg" "$out/Zap.dmg"

# The private key exists on disk only while gnui-sign-update.sh runs.
key=
cleanup() { [ -z "$key" ] || rm -f "$key"; }
trap cleanup EXIT
trap 'exit 1' INT TERM HUP
key=$(umask 077 && mktemp "$root/.cache/release/key.XXXXXX")
{
    echo "-----BEGIN PRIVATE KEY-----"
    security find-generic-password -s "$keyitem" -w "$keychain" | fold -w 64
    echo "-----END PRIVATE KEY-----"
} >"$key"
"$here/gnui-sign-update.sh" "$zip" "$key" "$version" "https://github.com/$repo/releases/tag/$tag" "$notes"
rm -f "$key"
key=

(cd "$out" && shasum -a 256 "ZapFast-$version.zip" Zap.dmg >SHA256SUMS)
assets=("$zip" "$out/latest.json" "$out/Zap.dmg" "$out/SHA256SUMS")
cmd=(gh release create "$tag" --repo "$repo" --target "$revision" --title "Zap $version" --notes "${notes:-Zap $version}" --latest "${assets[@]}")
if [ "$publish" = 0 ]; then
    echo "signed $tag in $out; to publish it (every installed copy then updates):"
    printf ' %q' "${cmd[@]}"
    echo
    exit 0
fi
"${cmd[@]}"
echo "published $tag: https://github.com/$repo/releases/tag/$tag"
