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
