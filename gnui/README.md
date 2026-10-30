# gnui: Zap

The Go module `zapfast`: the app on go-native-ui (`gonativeui`) and its
backend, gocore (the demo, or whatsmeow). The plans are
`../docs/GNUI-MIGRATION-PLAN.md` (the UI) and `../docs/go-backend/PLAN.md`
(the backend).

## Against upstream ZapFast

These are measured on macOS, Apple M4, with the same 0.19.0 core and the
demo's sample chats, before WP9: Zap then ran on ZapFast's Rust core, which
the bundle's 45.2 MB included. Each figure is the median of six interleaved
runs.

| | Zap (Go) | ZapFast (egui) |
|---|---:|---:|
| Launch to window on screen | **137 ms** | 274 ms |
| Memory at idle | **85 MiB** | 186 MiB |
| CPU and wakeups at idle | ≈ 0, 0.3/s | ≈ 0, 0.3/s |
| Apple-silicon code shipped | 45.2 MB | 48.8 MB |

The method, the harness and the raw rows are in
[the measurements](../docs/measurements/zap-vs-zapfast-2026-10-27.md).
Those runs had a locked screen, so the CPU and memory under load (the
launch tour) are still to be measured on a live screen.

| Path | What |
|---|---|
| `abi/` | the records, generated from `abi/zf.schema` (`go run ./tools/abigen`); `abi/testdata`, the encoder fixtures zfcore's Rust wrote |
| `core/` | what screens use of a backend: `core.Conn`, `Config`, `OpenAsync`/`Pending`, the errors, frames and voice types, `OpenPath` |
| `gocore/` | the backend: the Conn (`gocore.Conn`), the demo (`demo/`), WhatsApp on whatsmeow (`wa/`), the stores on SQLCipher (`store/`), audio and pictures (`av/`), calls (`calls/`, `voip/`), taking over an account the Rust core kept (`migrate/`) |
| `core/fake/` | a `core.Conn` without the library, for UI tests on any OS: replays scripted events and fixtures, records commands |
| `model/` | one account's pointer-free stores (chats, messages, contacts, link status) applied from the core's events |
| `state/` | the app's UI state: accounts, open chat, page, panes, dialog, windows, lock, settings; command helpers; event listeners |
| `ui/screen/` | the slot interfaces the shell calls screens through (`screen.Set`), `Env`, the shortcut table |
| `ui/shell/` | the main window (Rex chrome, rail, toolbar, routing), the menu bar and commands, the other windows, the app handler |
| `ui/<screen>/` | one package per screen, each owned by one agent: see `ui/CONTRACT.md` |
| `format/` | upstream's util.rs text helpers the screens share: phone numbers, stamps, dates, durations, the 12/24-hour clock |
| `lockhash/` | the app lock's and locked chats' password verifiers, the only implementation |
| `theme/`, `sys/`, `logic/` | upstream's themes on Rex; the process's services; upstream's reducers |
| `cmd/zapfast/` | the app: `app.Main`, the core opened async (`engine.go`), `screens.go` wires every screen |
| `i18n/` | the strings and their translations, generated from `i18n/po/*.po` (`go generate ./i18n`) |
| `third_party/whatsmeow/` | whatsmeow with Zap's call patch (`ZAP-PATCH.md`) |
| `internal/procinfo/` | exec-relative times for the startup numbers |

## The framework

`release-deps.json` pins the framework Zap is built against: go-native-ui at
commit `99380eb9ada8`, with that commit's tree hash and the hash of the
CI-built `shaders.metallib` it embeds. `go.mod` replaces
`gonativeui` with `../../go-native-ui`, the framework's checkout beside this
repository's (`~/Projects/go-native-ui` next to `~/Projects/zap`). A worktree
anywhere else uses a `go.work` here, which is ignored by git:

```
go 1.27.1

use .

replace gonativeui => /Users/you/Projects/go-native-ui

replace github.com/go-text/typesetting => /Users/you/Projects/go-native-ui/third_party/typesetting
```

`go build` and `go test` take whatever that resolves to. Packaging does not.
`packaging/macos/gnui-package.sh` (through `scripts/gnui/release_identity.py`)
refuses a framework at another commit or with uncommitted changes, and an
export without git unless its files hash to the pinned tree. To package
against another framework on purpose, set `ZAP_FRAMEWORK_DIR` to it: the
script warns, and the manifest records `"pin": "override"`. Moving the pin
means changing `release-deps.json`'s commit and tree together
(`git rev-parse <commit>^{tree}`).

Every bundle carries `Contents/Resources/build-manifest.json`
(`zap.build/v1`):

- `framework` is the framework's identity (`module`, `revision`, `dirty`,
  `source`), read by one adapter. When the gnui CLI has `gnui provenance
  -json` (schema `gnui.provenance/v1`, still provisional), its framework
  block is used (`source` `git` or `stamp`), and the whole JSON is kept
  verbatim as `frameworkProvenance`. Otherwise the adapter reads git
  (`source: "git"`), or the tree hash of an export that matched the pin
  (`tree-hash`, Zap's own value; the framework's reader refuses an export
  without a `.gnui-revision` stamp, so an unstamped one takes this path). The framework's Ghostty pin stays inside
  `frameworkProvenance`: Zap links neither libghostty-vt nor the terminal
  widget, so it has no `ghostty` entry. `shaders` is the framework's report,
  or without one the embedded `internal/metallib/lib/shaders.metallib`'s
  hash (`{"artifact": "none"}` when there is none); the file must match the
  report, and a pinned build refuses any artifact or hash
  `release-deps.json` does not expect.
- Zap's own fields: `app` (revision, dirty), `build` (variant, tags), `go`,
  and `whatsmeow`. `whatsmeow` gives go.mod's required version, the
  `./third_party/whatsmeow` replace (checked), and the patched copy's tree
  in Zap's commit with a dirty flag (`third_party/whatsmeow/ZAP-PATCH.md`
  says what the patch is). `nativeLibs` has each bundled library's
  `builtSha256` and the source release its build script pins and verifies.
  The hash is of the library as built: `gnui package` signs the bundle's
  copy again, which changes its bytes.
- `basis` names which fields are build-verified (computed from the
  artifacts: the libraries' `builtSha256`) and which are packager-asserted
  (read from git, go.mod, the build scripts, `go env` or the framework's
  reader).

The executable carries the same revisions, stamped with `-ldflags -X` into
`internal/buildinfo` once they are checked. The framework's
`gonativeui/provenance.FrameworkRevision` is stamped too wherever the
framework has that package. The first line of each run's log names them
(`msg=build revision=... framework=... framework_pin=...`), and About shows
Zap's short revision. `debug.ReadBuildInfo` supplies only the go version,
the tags and Zap's own VCS revision for an unstamped build: a replaced
module records no framework revision.

## Building and testing

With `CGO_ENABLED=0`, here:

```sh
go test ./...                           # the demo, fakes and fixtures; no account, no network
go run ./cmd/zapfast -demo              # the app on the demo core
go run ./cmd/zapfast -demo -o out.png   # the main window offscreen, once the chat list is in
```

A real account (without `-demo`) and the store tests need SQLCipher, and
stickers need libwebp: `../packaging/macos/sqlcipher/build.sh` and
`../packaging/macos/libwebp/build.sh` build them into `../target/`, where
unbundled runs find them (or `$ZAPFAST_SQLCIPHER`, `$ZAPFAST_LIBWEBP`); a
bundled app loads only its own `Contents/Frameworks` copies. Without them
those tests skip. Without `-demo` the app opens the real account (its own
data, `ai.a3lab.zap`); the tests use the demo core only.

### The simulation

`gocore/wa/sim_test.go` is a deterministic simulation of the WhatsApp
engine, after TigerBeetle's VOPR and Turso's simulator. One seed drives a
whole run of synthetic whatsmeow events and user commands into a real
engine, Conn, archive and whatsmeow store over the fake client. Its clock is
`testing/synctest`'s, so timers and hours of simulated time cost nothing.
It injects faults: failed writes and a full disk through
`gocore/store/sqlite`'s fault hook, crashes and quits that reopen the store
from what was on disk (at times with a torn WAL frame), dropped, repeated,
held and reordered events. After every step and reopen it checks that no
acknowledged message is lost or duplicated, that each chat keeps its order,
that unread counts and the chat list agree with the archive and the UI, and
that the archive passes its integrity check. It also checks that a wrong
key, a newer schema or a damaged file is refused without being changed.

`go test` runs a few fixed seeds (`TestSim`, about a second) and the seeds
that found bugs (`TestSimRegressions`). A failure prints its seed and the
command that replays it (`-run 'TestSimSeed$' -sim.seed=N`). Zap has no CI,
so the long exploration of random seeds runs on a developer's Mac with
SQLCipher built: `../scripts/sim-explore.sh 8h` overnight, before a release
and after any change to `gocore/wa` or `gocore/store`, once per fault mode
(`default`, `queued`, `ingest`, `lostack`). It logs to `../target/sim/`.
Until [issue #2](https://github.com/AdamAugustinsky/zap/issues/2) is
fixed, `queued` and `ingest` stop within a few seeds on that issue's
data loss.
