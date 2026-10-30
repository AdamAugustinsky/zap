#!/bin/bash
# Build Zap.app for Apple silicon: the two bundled C libraries, then
# `gnui package` over gnui/gnui.json, then the bundle checks.
#
#   packaging/macos/gnui-package.sh [--dev] [--dmg] [--notarize] [--check]
#
# Writes dist/Zap.app (bundleName; and with --dmg dist/ZapFast-<version>.dmg, as
# upstream's: the app and an Applications link; with the framework's -zip,
# dist/ZapFast-<version>.zip, the self-update archive). --notarize (a
# Developer ID and $GNUI_NOTARY_PROFILE) notarizes and staples the app and
# the dmg: docs/HANDTEST-BUILD.md.
#
# --dev writes the hand test's development bundle to dist/dev/ instead: the
# same bundle (id, signature, entitlements, libraries), with the executable built
# without gnui_release, so it keeps the demo and measurement flags and the
# framework's Debug menu. Not for distribution: it refuses --notarize.
#
# The framework must be the one gnui/release-deps.json pins: what gnui/
# resolves gonativeui to (go.mod's replace, or an ignored gnui/go.work) is
# checked by scripts/gnui/release_identity.py, which fails on another commit,
# uncommitted changes, or an export whose files are not the pinned tree. The
# bundle carries Contents/Resources/build-manifest.json (the revisions of Zap,
# the framework and the whatsmeow patch, the libraries' hashes, the go and
# tags), and the executable the same revisions (zapfast/internal/buildinfo).
# --check stops after the check and writes the manifest to dist/ (or
# dist/dev/) alone: it builds the gnui CLI to read the framework, and nothing
# that ships, and signs nothing.
#
# Needs on PATH: go (1.27) and the Xcode Command Line Tools (cc, for the two
# libraries). Environment:
#   CODESIGN_IDENTITY  signing identity (a Developer ID for distribution);
#                      unset: the framework's development identity, gnui-dev,
#                      for this Mac and hand tests (step 3)
#   GNUI               a built gnui CLI (default: built from the checked
#                      framework); a development override, announced and
#                      recorded as one
#   ZAP_FRAMEWORK_DIR  build against this framework directory instead of the
#                      pinned one (a generated go.work): a development
#                      override, announced and recorded in the manifest
#
# Everything but two C libraries is Go, in the executable (calls included:
# gocore/voip/engine). Those two, libsqlcipher.dylib and libzfwebp.dylib, are
# built by their pinned scripts (sqlcipher/build.sh, libwebp/build.sh) into
# target/sqlcipher/ and target/libwebp/, gnui.json's `frameworks`, unless a
# library there is newer than its script.
set -euo pipefail

dev=0
dmg=0
notarize=0
check=0
for arg in "$@"; do
    case "$arg" in
    --dev) dev=1 ;;
    --dmg) dmg=1 ;;
    --notarize) notarize=1 ;;
    --check) check=1 ;;
    *) echo "usage: $0 [--dev] [--dmg] [--notarize] [--check]" >&2; exit 2 ;;
    esac
done
if [ "$dev" = 1 ] && [ "$notarize" = 1 ]; then
    echo "--dev builds the hand test's bundle, which is not notarized" >&2
    exit 2
fi

root="$(cd "$(dirname "$0")/../.." && pwd)"
here="$root/packaging/macos"
derived=""
gowrap=""
mkdir -p "$root/.cache"
work="$(mktemp -d "$root/.cache/gnui-package.XXXXXX")" # never /tmp (AGENTS.md)
trap 'rm -rf "$derived" "$gowrap" "$work"' EXIT
dist="$root/dist"
if [ "$dev" = 1 ]; then
    dist="$root/dist/dev"
fi

if [ "$(uname -s)/$(uname -m)" != "Darwin/arm64" ]; then
    echo "Zap packages on Apple silicon macOS only" >&2
    exit 1
fi

# 1. The bundled C libraries (not for --check), loaded with purego from Contents/Frameworks
# (docs/go-backend/PLAN.md "Bundled libraries"). Each script verifies its
# pinned source and that the library links only system libraries; a library
# newer than its script (and the WebP shim) is kept.
build_lib() { # build_lib <script> <library> [other inputs...]
    local script="$1" lib="$2"
    shift 2
    local stale=0
    if [ ! -f "$lib" ]; then
        stale=1
    else
        for input in "$script" "$@"; do
            if [ "$input" -nt "$lib" ]; then
                stale=1
            fi
        done
    fi
    if [ "$stale" = 1 ]; then
        echo "building $(basename "$lib") ($script)"
        "$script" >/dev/null
    fi
    local name
    name="$(basename "$lib")"
    if [ "$(otool -D "$lib" | sed -n 2p)" != "@rpath/$name" ]; then
        echo "$lib: install name $(otool -D "$lib" | sed -n 2p), want @rpath/$name" >&2
        exit 1
    fi
    if [ "$(lipo -archs "$lib")" != arm64 ]; then
        echo "$lib: architectures $(lipo -archs "$lib"), want arm64" >&2
        exit 1
    fi
}
if [ "$check" = 0 ]; then
    build_lib "$here/sqlcipher/build.sh" "$root/target/sqlcipher/libsqlcipher.dylib"
    build_lib "$here/libwebp/build.sh" "$root/target/libwebp/libzfwebp.dylib" "$here/libwebp/zf_webp.c"
fi

# 2. The framework gnui/ resolves (or $ZAP_FRAMEWORK_DIR's), the gnui CLI
# built from it, then its identity checked against gnui/release-deps.json
# (through the CLI's `gnui provenance` where it has one) and the build
# manifest written.
identity="$root/scripts/gnui/release_identity.py"
python3 "$identity" resolve --shell "$work/resolved.sh"
framework="" gowork="" ldflags=""
# shellcheck source=/dev/null
. "$work/resolved.sh"
if [ -n "$gowork" ]; then
    export GOWORK="$gowork" # ZAP_FRAMEWORK_DIR: gnui package's go build uses it too
fi
cli_override=()
if [ -n "${GNUI:-}" ]; then
    cli_override=(--gnui-cli-override)
else
    echo "building gnui from $framework"
    # The framework's own module: no go.work of Zap's applies to it.
    (cd "$framework" && GOWORK=off CGO_ENABLED=0 go build -o "$work/gnui" ./cmd/gnui)
    GNUI="$work/gnui"
fi
variant=release
if [ "$dev" = 1 ]; then
    variant=dev
fi
python3 "$identity" manifest --variant "$variant" --framework "$framework" --gnui "$GNUI" \
    --manifest "$work/build-manifest.json" --shell "$work/resolved.sh" ${cli_override[@]+"${cli_override[@]}"}
# shellcheck source=/dev/null
. "$work/resolved.sh"
if [ "$check" = 1 ]; then
    mkdir -p "$dist"
    cp -f "$work/build-manifest.json" "$dist/build-manifest.json"
    echo "checked: $dist/build-manifest.json (nothing shipped was built)"
    exit 0
fi

# 3. The bundle: gnui package builds darwin/arm64 with -trimpath,
# -ldflags "-s -w" and -tags gnui_release (--dev: without that tag), signs
# inside out (the two libraries, then the app) with the hardened runtime and
# gnui.json's entitlements, and verifies with codesign --verify --deep
# --strict. The derived config sits beside gnui.json, whose relative paths
# it keeps, and adds the build manifest to the resources, so the signature
# covers it.
config="$root/gnui/gnui.json"
derived="$root/gnui/.gnui-package.json"
library_validation=1
if [ -n "${CODESIGN_IDENTITY:-}" ]; then
    args=(-identity "$CODESIGN_IDENTITY")
else
    args=(-dev-cert)
    library_validation=0
    # gnui-dev is self-signed, so no binary has a Team ID, and the hardened
    # runtime's library validation refuses the libraries ("mapping process
    # and mapped file (non-platform) have different Team IDs"). This build
    # keeps the hardened runtime and lets the app load its own libraries (the
    # app loads only Contents/Frameworks' copies: sqlite.FindLibrary, av's
    # WebP loader). A Developer
    # ID signs them all with one Team ID and gets no such exception.
    # No release is signed so, so the app's updater could only refuse one:
    # it says this copy updates by being rebuilt instead (buildinfo.DevSigned).
    ldflags="$ldflags -X zapfast/internal/buildinfo.devSigned=true"
fi
python3 - "$config" "$derived" "$work/build-manifest.json" "$library_validation" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    config = json.load(source)
if sys.argv[4] == "0":
    config["entitlements"]["com.apple.security.cs.disable-library-validation"] = True
config["resources"].append({"from": sys.argv[3], "to": "build-manifest.json"})
with open(sys.argv[2], "w", encoding="utf-8") as target:
    json.dump(config, target, indent="\t", ensure_ascii=False)
PY
config="$derived"
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
if grep -q -- '^ *-ldflags' <<<"$usage"; then
    args+=(-ldflags "$ldflags") # zapfast/internal/buildinfo's revisions
else
    echo "warning: this gnui has no -ldflags: the executable does not carry its revisions" >&2
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
