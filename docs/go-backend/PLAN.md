# Zap's backend in Go

Decided 2026-10-28 (Adam):

- **Remove Rust in stages.** Messaging, linking, the archive and media move
  to a Go backend on whatsmeow. Calls stay on a media-only Rust library,
  bridged from Go, until the call engine is ported to Go. Then Rust goes.
  **Done (WP9, 2026-10-28):** the repository has no Rust; the app always
  runs gocore, and an account the Rust core kept is taken over by
  gocore/migrate (pure Go) on its first open.
- **Licence.** whatsmeow depends on `go.mau.fi/libsignal` (GPL-3.0), so
  distributed Zap builds are GPL-3.0 once the Go backend ships. Upstream's
  MIT notice stays. Since WP9: `COPYING` (GPL-3.0), `LICENSE` (the notice
  and upstream's MIT text) and `THIRD-PARTY-NOTICES.md`, all three in the
  bundle's Resources.
- **Workers.** Claude Code subagents, one per work package, each in its own
  worktree and directories.

Research (read-only, 2026-10-28): [backend-map.md](backend-map.md) (all 120
commands and 70 events against ZapFast Go's backend, the layout and the
packages), [storage.md](storage.md) (the archive, the session store, the
no-cgo storage choice, migrating an existing install),
[calls-bridge.md](calls-bridge.md) (keeping the Rust call engine behind
whatsmeow, and the size of a Go port).

## Rules for every package

- `CGO_ENABLED=0`, always. Native libraries are loaded at runtime with
  purego, as go-native-ui does. A new bundled dylib needs a build script
  under `packaging/macos/` and a line in this file.
- The Go backend speaks the existing ABI (`gnui/abi/zf.schema`, `gnui/abi`;
  the schema was `zfcore/abi/zf.schema` until WP9). Until WP9 the Rust core
  was a fallback for side-by-side runs.
- Code copied from ZapFast Go (`~/Documents/projects/a3-lab/zapfast-go`, MIT,
  read only) comes from its commit 3104c46. Each copied file says so in its
  header.
- Tests run without a live WhatsApp account: the demo, `core/fake`, injected
  whatsmeow events, and synthetic fixtures.

## Bundled libraries

All arm64, built by pinned, checksum-verified scripts into `target/`, linking
only system libraries, loaded with purego from `Contents/Frameworks` in a
bundle (and from `$ZAPFAST_*` or `target/` in development):

- `libsqlcipher.dylib`: SQLCipher 4.14.0 on CommonCrypto
  (`packaging/macos/sqlcipher/build.sh`). The archive and the whatsmeow store.
- `libzfwebp.dylib`: libwebp 1.6.0 plus a small shim
  (`packaging/macos/libwebp/build.sh`). Sticker encoding; ImageIO cannot
  write WebP.

Opus voice encoding and decoding use the system's AudioToolbox, so nothing is
bundled for it. `packaging/macos/gnui-package.sh` builds both (when missing or
older than their scripts) and bundles them; they are the bundle's only
libraries (`libzapfast_core.dylib` went with WP9, and the call media, WP7's
`libzfcalls.dylib`, is Go since WP8d: gocore/voip/engine).
`gnui-verify.sh` checks each is signed by the app's signer and links only
system libraries.

## Choosing the backend (WP6b, then WP9)

Since WP9 there is no choice: every account, and the demo, runs on gocore
(`-engine` and `$ZAPFAST_ENGINE` are gone). An account folder the Rust core
kept (session.db and no whatsmeow.db), or one whose migration began
(migration.json), is taken over first (`migrate.Account`, holding the
single-instance lock); a failure is the account's open error, and Try again
retries it, or links afresh when the session itself cannot be converted.
Calls, one to one and group, and call links run through WP7's bridge on
the Go call engine ([calls-bridge.md](calls-bridge.md) section 5, WP8e for
groups and links). The wiring is `gnui/cmd/zapfast/engine.go`.

## Layout and packages

| Package | Directory | Depends on | Done when |
|---|---|---|---|
| WP1 Conn shell | `gnui/gocore/` (`blobs/`, `out/`) | none | `gocore.Conn` implements `core.Conn` in process; chunked, bounded events; blobs; the Pick broker; every command dispatched or refused; the `Engine` interface is fixed for WP2 and WP4 |
| WP3 Store | `gnui/gocore/store/`, `packaging/macos/sqlcipher/` | none | a bundled SQLCipher 4.14 dylib on CommonCrypto, loaded with purego behind a `database/sql` driver; the Go archive; an encrypted whatsmeow store; wrong keys fail closed |
| WP5 Audio and pictures | `gnui/gocore/av/` | none | voice encode and decode, normalization and the 64-bar waveform; WebP stickers; picture inspect and edit; missing libraries give typed errors |
| WP2 Go demo | `gnui/gocore/demo/` | WP1 | a Go port of `zfcore/src/demo.rs`; the `Demo: true` tests pass with no dylib |
| WP4 WhatsApp engine | `gnui/gocore/wa/` | WP1, WP3, WP5 | whatsmeow behind a fakeable client; every non-call command, tested with injected events |
| WP6 Wiring and migration | `gnui/gocore/migrate/`, `gnui/cmd/zapfast/` | WP1, WP3 | `-engine=go|rust`; pickers; taking over an existing session and archive, copy-first and failing closed |
| WP7 Call bridge | `gnui/gocore/calls/`, a media-only Rust crate | WP4 | 1:1 calls through whatsmeow signaling and the Rust media engine |
| WP8 Call engine in Go (**done**) | `gnui/gocore/voip/` | WP7 | the Rust crate is gone: the call engine, MLow and the media pipeline are Go |
| WP8e Group calls and call links (**done**) | `gnui/gocore/calls/`, `gnui/gocore/voip/` | WP8 | group calls rung, answered, placed from a group and keyed by epochs; call links joined through their waiting room; filed as 0.19.0 filed them |
| WP9 No Rust (**done**) | the whole repository | WP8 | no `.rs` or Cargo file; no `-engine=rust`; the core binding (`gnui/core`'s purego loader of `libzapfast_core.dylib`) gone, `core.Conn` and its types kept; the schema, ABI fixtures and translations under `gnui/`; the demo checked against zfcore's recorded answers (`gocore/demo/testdata`); the bundle carries SQLCipher and libwebp only; GPL-3.0 |

The details of each package, the gaps and the order are in
[backend-map.md §4](backend-map.md#4-proposed-layout-and-work-packages).
