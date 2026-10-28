# gnui: ZapFast's Go UI

The Go module `zapfast`: the app on go-native-ui (`gonativeui`) over the Rust
core in `../zfcore`. The plan is `../docs/GNUI-MIGRATION-PLAN.md`.

## Against upstream ZapFast

These are measured on macOS, Apple M4, with the same 0.19.0 core and the
demo's sample chats. Each figure is the median of six interleaved runs.

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
| `abi/` | the records, generated from `../zfcore/abi/zf.schema` (`go run ./tools/abigen`) |
| `core/` | the bindings: loading the library, `Send`, `Drain`, blobs, `OpenAsync`, `OpenPath`; `core.Conn` is what screens use |
| `core/fake/` | a `core.Conn` without the library, for UI tests on any OS: replays scripted events and fixtures, records commands |
| `model/` | one account's pointer-free stores (chats, messages, contacts, link status) applied from the core's events |
| `state/` | the app's UI state: accounts, open chat, page, panes, dialog, windows, lock, settings; command helpers; event listeners |
| `ui/screen/` | the slot interfaces the shell calls screens through (`screen.Set`), `Env`, the shortcut table |
| `ui/shell/` | the main window (Rex chrome, rail, toolbar, routing), the menu bar and commands, the other windows, the app handler |
| `ui/<screen>/` | one package per screen, each owned by one agent: see `ui/CONTRACT.md` |
| `format/` | upstream's util.rs text helpers the screens share: phone numbers, stamps, dates, durations, the 12/24-hour clock |
| `lockhash/` | the app lock's and locked chats' password verifiers, the only implementation |
| `theme/`, `sys/`, `logic/` | upstream's themes on Rex; the process's services; upstream's reducers |
| `cmd/zapfast/` | the app: `app.Main`, the core opened async, `screens.go` wires every screen |
| `internal/procinfo/` | exec-relative times for the startup numbers |

## The framework

`go.mod` replaces `gonativeui` with `../../go-native-ui`, the framework's
checkout beside this repository's (`~/Projects/go-native-ui` next to
`~/Projects/zap`). A worktree anywhere else uses a `go.work` here,
which is ignored by git:

```
go 1.27.1

use .

replace gonativeui => /Users/you/Projects/go-native-ui

replace github.com/go-text/typesetting => /Users/you/Projects/go-native-ui/third_party/typesetting
```

## Building and testing

Build the core first, from the repository root (always with `-p`, see
`../zfcore/README.md`):

```sh
cargo build -p zapfast_core                          # target/debug/libzapfast_core.dylib
cargo build -p zapfast_core --profile core-release   # what ships, and the startup numbers
```

Then, here, with `CGO_ENABLED=0`:

```sh
go test ./...                    # core's tests load the newest cargo build, demo core only
go run ./cmd/zapfast -demo              # the app on the demo core
go run ./cmd/zapfast -demo -o out.png   # the main window offscreen, once the chat list is in
```

The loading policy (`core.ResolveLibrary`): a bundled app (its executable
in `X.app/Contents/MacOS`) loads only `Contents/Frameworks/libzapfast_core.dylib`
and fails closed without it; `$ZAPFAST_CORE`, `-core` and the cargo search are
ignored or refused there. An unbundled run takes an explicit path (`-core`),
then `$ZAPFAST_CORE`, then the newest of `target/{core-release,debug,release}`
above the working directory or the executable. Without one, the tests skip
(unless `$ZAPFAST_CORE` is set). Without `-demo` the app opens the real account (its own data,
`ai.a3lab.zap`); the tests use the demo core only.
