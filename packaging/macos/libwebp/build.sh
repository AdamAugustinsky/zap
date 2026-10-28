#!/bin/bash
# Build libzfwebp.dylib, the WebP encoder of the Go backend's stickers
# (gnui/gocore/av).
#
#   packaging/macos/libwebp/build.sh [target-dir]
#
# The target directory defaults to target/libwebp/ at the repository root,
# where development runs and tests find the library. A bundle copies it into
# Contents/Frameworks.
#
# ImageIO decodes WebP but cannot write it, and golang.org/x/image only
# decodes, so stickers are encoded with libwebp, as upstream's Rust backend
# does (libwebp-sys). libwebp comes from a pinned source release, checked
# against a pinned SHA-256 before anything in it runs, and is compiled with
# its mux library (animated stickers) and zf_webp.c, a small shim that keeps
# libwebp's structs on the C side. It is built for darwin/arm64 and links
# only system libraries. Its install name is @rpath/libzfwebp.dylib.
set -euo pipefail

version=1.6.0
sha256=93a852c2b3efafee3723efd4636de855b46f9fe1efddd607e1f42f60fc8f2136
url="https://github.com/webmproject/libwebp/archive/refs/tags/v${version}.tar.gz"
min_macos=11.0

here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../../.." && pwd)"
target="${1:-$root/target/libwebp}"
mkdir -p "$target"
target="$(cd "$target" && pwd)"

if [ "$(uname -s)" != Darwin ]; then
    echo "build.sh: this builds the macOS library" >&2
    exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# The tarball is kept beside the library, so a rebuild needs no network. It is
# verified every time, cached or not.
tarball="$target/libwebp-${version}.tar.gz"
if [ ! -f "$tarball" ]; then
    curl -fsSL --retry 3 -o "$work/download.tar.gz" "$url"
    mv "$work/download.tar.gz" "$tarball"
fi
got="$(shasum -a 256 "$tarball" | awk '{print $1}')"
if [ "$got" != "$sha256" ]; then
    rm -f "$tarball"
    echo "build.sh: $url has SHA-256 $got, expected $sha256" >&2
    exit 1
fi

tar -xzf "$tarball" -C "$work"
src="$work/libwebp-${version}"

sources=()
for dir in sharpyuv src/dec src/dsp src/enc src/utils src/mux src/demux; do
    for file in "$src/$dir"/*.c; do
        sources+=("$file")
    done
done
sources+=("$here/zf_webp.c")

lib="$work/libzfwebp.dylib"
# NEON is chosen by the sources themselves on arm64.
xcrun clang -dynamiclib -arch arm64 -mmacosx-version-min="$min_macos" -O2 \
    -DNDEBUG -w \
    -I"$src" \
    "${sources[@]}" \
    -install_name @rpath/libzfwebp.dylib \
    -current_version "$version" -compatibility_version 1.0.0 \
    -Wl,-dead_strip \
    -o "$lib"
xcrun strip -x "$lib"

# Only system libraries may be linked, and the shim must be in.
otool -L "$lib" | tail -n +2 | awk '{print $1}' | while read -r dep; do
    case "$dep" in
    @rpath/libzfwebp.dylib | /usr/lib/*) ;;
    *) echo "build.sh: unexpected dependency $dep" >&2; exit 1 ;;
    esac
done
for sym in zf_webp_version zf_webp_encode zf_webp_free zf_webp_anim_new zf_webp_anim_add \
    zf_webp_anim_finish zf_webp_anim_delete; do
    nm -gU "$lib" | grep -q " _${sym}\$" || { echo "build.sh: $sym is missing" >&2; exit 1; }
done

mv -f "$lib" "$target/libzfwebp.dylib"
# Ad-hoc signed, as arm64 requires; a bundle signs it again with its identity.
codesign --force --sign - "$target/libzfwebp.dylib" >/dev/null 2>&1
echo "$target/libzfwebp.dylib"
shasum -a 256 "$target/libzfwebp.dylib"
