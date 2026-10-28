# Zap's backend in Go

Decided 2026-10-28 (Adam):

- **Remove Rust in stages.** Messaging, linking, the archive and media move
  to a Go backend on whatsmeow. Calls stay on a media-only Rust library,
  bridged from Go, until the call engine is ported to Go. Then Rust goes.
- **Licence.** whatsmeow depends on `go.mau.fi/libsignal` (GPL-3.0), so
  distributed Zap builds are GPL-3.0 once the Go backend ships. Upstream's
  MIT notice stays.
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
- The Go backend speaks the existing ABI (`zfcore/abi/zf.schema`,
  `gnui/abi`). Neither changes, so the Rust core stays a fallback for
  side-by-side runs until the Go backend reaches parity.
- Code copied from ZapFast Go (`~/Documents/projects/a3-lab/zapfast-go`, MIT,
  read only) comes from its commit 3104c46. Each copied file says so in its
  header.
- Tests run without a live WhatsApp account: the demo, `core/fake`, injected
  whatsmeow events, and synthetic fixtures.

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
| WP8 Call engine in Go | | WP7 | the Rust crate is gone |

The details of each package, the gaps and the order are in
[backend-map.md §4](backend-map.md#4-proposed-layout-and-work-packages).
