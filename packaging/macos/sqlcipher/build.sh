#!/bin/bash
# Build libsqlcipher.dylib for the Go backend's storage (gnui/gocore/store).
#
#   packaging/macos/sqlcipher/build.sh [target-dir]
#
# The target directory defaults to target/sqlcipher/ at the repository root,
# where development runs and tests find the library. A bundle copies it into
# Contents/Frameworks.
#
# SQLCipher comes from a pinned source release, checked against a pinned
# SHA-256 before anything in it runs. It is built for darwin/arm64 on Apple
# CommonCrypto (Security.framework), so the library links only system
# frameworks: no OpenSSL. Its install name is @rpath/libsqlcipher.dylib.
#
# The SQLite options are the ones an encrypted WAL archive needs, and match the
# Rust core's bundled SQLCipher (libsqlite3-sys 0.38.2, SQLCipher 4.14.0) in
# what touches the file format: the codec, the default cipher settings and
# JSON. Temporary tables and indices stay in memory (SQLITE_TEMP_STORE=3), so
# nothing decrypted reaches a temporary file. SQLITE_THREADSAFE=2 lets
# connections run on any thread, one caller at a time, as database/sql uses
# them. Extension loading is compiled out.
set -euo pipefail

version=4.14.0
sha256=67fb27e967a4a6968c0905691c89c908e7250dddc581b887c19ef981c737e473
url="https://github.com/sqlcipher/sqlcipher/archive/refs/tags/v${version}.tar.gz"
min_macos=11.0

here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../../.." && pwd)"
target="${1:-$root/target/sqlcipher}"
mkdir -p "$target"
target="$(cd "$target" && pwd)"

if [ "$(uname -s)" != Darwin ]; then
    echo "build.sh: CommonCrypto builds need macOS" >&2
    exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# The tarball is kept beside the library, so a rebuild needs no network. It is
# verified every time, cached or not.
tarball="$target/sqlcipher-${version}.tar.gz"
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
src="$work/sqlcipher-${version}"

# The amalgamation (sqlite3.c with SQLCipher in it), made out of tree.
mkdir "$work/amalgamation"
(
    cd "$work/amalgamation"
    "$src/configure" --disable-tcl --with-tempstore=always >"$work/configure.log" 2>&1 ||
        { cat "$work/configure.log" >&2; exit 1; }
    make sqlite3.c >"$work/make.log" 2>&1 || { tail -40 "$work/make.log" >&2; exit 1; }
)

flags=(
    -DSQLITE_HAS_CODEC
    -DSQLCIPHER_CRYPTO_CC
    -DSQLITE_EXTRA_INIT=sqlcipher_extra_init
    -DSQLITE_EXTRA_SHUTDOWN=sqlcipher_extra_shutdown
    -DSQLITE_TEMP_STORE=3
    -DSQLITE_THREADSAFE=2
    -DSQLITE_DEFAULT_FOREIGN_KEYS=1
    -DSQLITE_DEFAULT_MEMSTATUS=0
    -DSQLITE_ENABLE_API_ARMOR
    -DSQLITE_ENABLE_FTS5
    -DSQLITE_OMIT_LOAD_EXTENSION
    -DHAVE_USLEEP=1
    -DHAVE_STDINT_H=1
)

lib="$work/libsqlcipher.dylib"
xcrun clang -dynamiclib -arch arm64 -mmacosx-version-min="$min_macos" -O2 \
    -fvisibility=default -w \
    "${flags[@]}" \
    -I"$work/amalgamation" \
    "$work/amalgamation/sqlite3.c" \
    -framework Security -framework CoreFoundation \
    -install_name @rpath/libsqlcipher.dylib \
    -current_version "$version" -compatibility_version 4.0.0 \
    -Wl,-dead_strip \
    -o "$lib"
xcrun strip -x "$lib"

# Only system libraries may be linked, and the codec must be in.
otool -L "$lib" | tail -n +2 | awk '{print $1}' | while read -r dep; do
    case "$dep" in
    @rpath/libsqlcipher.dylib | /usr/lib/* | /System/Library/*) ;;
    *) echo "build.sh: unexpected dependency $dep" >&2; exit 1 ;;
    esac
done
nm -gU "$lib" | grep -q ' _sqlite3_key$' || { echo "build.sh: sqlite3_key is missing" >&2; exit 1; }

cp "$work/amalgamation/sqlite3.h" "$target/sqlite3.h"
mv -f "$lib" "$target/libsqlcipher.dylib"
# Ad-hoc signed, as arm64 requires; a bundle signs it again with its identity.
codesign --force --sign - "$target/libsqlcipher.dylib" >/dev/null 2>&1
echo "$target/libsqlcipher.dylib"
shasum -a 256 "$target/libsqlcipher.dylib"
