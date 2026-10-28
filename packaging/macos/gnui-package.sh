#!/bin/bash
# Build Zap.app, the Go UI, for Apple silicon: the release core, then
# `gnui package` over gnui/gnui.json, then the bundle checks.
#
#   packaging/macos/gnui-package.sh [--dev] [--dmg] [--notarize] [--skip-core]
#
# Writes dist/Zap.app (bundleName; and with --dmg dist/ZapFast-<version>.dmg, as
# upstream's: the app and an Applications link; with the framework's -zip,
# dist/ZapFast-<version>.zip, the self-update archive). --notarize (a
# Developer ID and $GNUI_NOTARY_PROFILE) notarizes and staples the app and
# the dmg: docs/HANDTEST-BUILD.md.
#
# --dev writes the hand test's development bundle to dist/dev/ instead: the
# same bundle (id, signature, entitlements, core), with the executable built
# without gnui_release, so it keeps the demo and measurement flags and the
# framework's Debug menu. Not for distribution: it refuses --notarize.
#
# Needs on PATH: cargo (rust-toolchain.toml's version) and go. Environment:
#   CODESIGN_IDENTITY  signing identity (a Developer ID for distribution);
#                      unset: the framework's development identity, gnui-dev,
#                      for this Mac and hand tests (step 3)
#   CARGO_TARGET_DIR   cargo's target directory (default: target/)
#   GNUI               a built gnui CLI (default: built from the framework
#                      checkout gnui/go.mod resolves gonativeui to)
#
# The core is copied to target/core-release/, where gnui.json's `frameworks`
# entry points.
set -euo pipefail

dev=0
dmg=0
notarize=0
skip_core=0
for arg in "$@"; do
    case "$arg" in
    --dev) dev=1 ;;
    --dmg) dmg=1 ;;
    --notarize) notarize=1 ;;
    --skip-core) skip_core=1 ;;
    *) echo "usage: $0 [--dev] [--dmg] [--notarize] [--skip-core]" >&2; exit 2 ;;
    esac
done
if [ "$dev" = 1 ] && [ "$notarize" = 1 ]; then
    echo "--dev builds the hand test's bundle, which is not notarized" >&2
    exit 2
fi

root="$(cd "$(dirname "$0")/../.." && pwd)"
here="$root/packaging/macos"
tools=""
derived=""
gowrap=""
trap 'rm -rf "$tools" "$derived" "$gowrap"' EXIT
dist="$root/dist"
if [ "$dev" = 1 ]; then
    dist="$root/dist/dev"
fi
lib_name=libzapfast_core.dylib
staged="$root/target/core-release/$lib_name"

if [ "$(uname -s)/$(uname -m)" != "Darwin/arm64" ]; then
    echo "ZapFast's Go UI packages on Apple silicon macOS only" >&2
    exit 1
fi

# 1. The core: the cdylib alone (-p, so upstream's egui client is not unified
# in), the core-release profile (panic = "unwind" for the C entry points).
if [ "$skip_core" = 0 ]; then
    echo "building the core (cargo build -p zapfast_core --profile core-release)"
    (cd "$root" && cargo build --locked -p zapfast_core --profile core-release)
    built="${CARGO_TARGET_DIR:-$root/target}/core-release/$lib_name"
    if ! [ "$built" -ef "$staged" ]; then
        mkdir -p "$(dirname "$staged")"
        cp -f "$built" "$staged.tmp"
        mv -f "$staged.tmp" "$staged"
    fi
fi
if [ ! -f "$staged" ]; then
    echo "$staged is missing: build the core (drop --skip-core)" >&2
    exit 1
fi
# dlopen takes the bundle's path, and the install name keeps any link to it
# relative to Contents/Frameworks (zfcore/build.rs).
install_name="$(otool -D "$staged" | sed -n 2p)"
if [ "$install_name" != "@rpath/$lib_name" ]; then
    echo "$staged: install name $install_name, want @rpath/$lib_name" >&2
    exit 1
fi
if [ "$(lipo -archs "$staged")" != arm64 ]; then
    echo "$staged: architectures $(lipo -archs "$staged"), want arm64" >&2
    exit 1
fi
python3 "$root/scripts/gnui/check_gpl_sounds.py" "$staged"

# 2. The gnui CLI of the framework this module builds against.
if [ -z "${GNUI:-}" ]; then
    framework="$(cd "$root/gnui" && go list -m -f '{{.Dir}}' gonativeui)"
    tools="$(mktemp -d)"
    echo "building gnui from $framework"
    (cd "$framework" && CGO_ENABLED=0 go build -o "$tools/gnui" ./cmd/gnui)
    GNUI="$tools/gnui"
fi

# 3. The bundle: gnui package builds darwin/arm64 with -trimpath,
# -ldflags "-s -w" and -tags gnui_release (--dev: without that tag), signs inside out (the core, then
# the app) with the hardened runtime and gnui.json's entitlements, and
# verifies with codesign --verify --deep --strict.
config="$root/gnui/gnui.json"
if [ -n "${CODESIGN_IDENTITY:-}" ]; then
    args=(-identity "$CODESIGN_IDENTITY")
else
    args=(-dev-cert)
    # gnui-dev is self-signed, so neither binary has a Team ID, and the
    # hardened runtime's library validation refuses the core ("mapping process
    # and mapped file (non-platform) have different Team IDs"). This build
    # keeps the hardened runtime and lets the app load its own core (the app
    # loads only Contents/Frameworks' copy: core.ResolveLibrary). A Developer
    # ID signs both with one Team ID and gets no such exception. The derived
    # manifest sits beside gnui.json, whose relative paths it keeps.
    derived="$root/gnui/.gnui-package.json"
    python3 - "$config" "$derived" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    config = json.load(source)
config["entitlements"]["com.apple.security.cs.disable-library-validation"] = True
with open(sys.argv[2], "w", encoding="utf-8") as target:
    json.dump(config, target, indent="\t", ensure_ascii=False)
PY
    config="$derived"
fi
args=(package -c "$config" -o "$dist" "${args[@]}")
if [ "$dmg" = 1 ]; then
    args+=(-dmg)
fi
if [ "$notarize" = 1 ]; then
    args+=(-notarize) # the profile is $GNUI_NOTARY_PROFILE
fi
usage="$("$GNUI" package -h 2>&1 || true)"
if grep -q -- '^ *-zip' <<<"$usage"; then
    args+=(-zip)
else
    echo "note: this gnui has no -zip (framework p6-selfupdate): no update archive"
fi
if [ "$dev" = 1 ]; then
    # gnui package always adds gnui_release (cmd/gnui/bundle.go) and runs go
    # build through $GNUI_GO: this go drops that one tag and changes nothing
    # else.
    gowrap="$(mktemp -d)"
    cat > "$gowrap/go" <<'GO'
#!/bin/bash
args=()
while [ $# -gt 0 ]; do
    case "$1" in
    -tags) tags="$2"; shift 2 ;;
    -tags=*) tags="${1#-tags=}"; shift ;;
    *) args+=("$1"); shift; continue ;;
    esac
    tags="$(tr ', ' '\n\n' <<<"$tags" | grep -vx -e gnui_release -e '' | paste -sd, -)"
    if [ -n "$tags" ]; then
        args+=(-tags "$tags")
    fi
done
exec "$GNUI_DEV_GO" "${args[@]}"
GO
    chmod +x "$gowrap/go"
    GNUI_DEV_GO="${GNUI_GO:-$(command -v go)}" GNUI_GO="$gowrap/go" "$GNUI" "${args[@]}"
else
    "$GNUI" "${args[@]}"
fi

# 4. What users get, checked.
"$here/gnui-verify.sh" "$dist/Zap.app"
