# zapfast_core

ZapFast's core (upstream's backend, archive and calls) as a cdylib,
`libzapfast_core.dylib`, behind the C ABI in `include/zfcore.h`, for the Go
UI in `../gnui`. The contract is "The core ABI (D49)" in
`../docs/GNUI-MIGRATION-PLAN.md`.

| Path | What |
|---|---|
| `include/zfcore.h` | the C ABI |
| `abi/zf.schema` | every command and event record, the one source for both sides |
| `src/abi_gen.rs` | generated from the schema (Rust); `../gnui/abi/abi_gen.go` is the Go side |
| `src/wire.rs` | the record encoding the generated code uses |
| `src/lib.rs` | the C entry points: errors into `zf_err`, panic containment |
| `src/cores.rs` | one core per account: queue, blobs, and upstream's `Backend` or the demo |
| `src/wake.rs` | the event queue and its wake pipe; `Signal`, the backend's waker |
| `src/blobs.rs` | bytes too large for a record: the caller's until taken or freed, the events' shared by content and expired by the core past 64 MiB |
| `src/frames.rs` | a call's video frames for `zf_frame_acquire`: the newest I420 frame per stream, held until released |
| `src/dto.rs` | upstream's `Command`/`Event` to and from records (exhaustive matches) |
| `src/demo.rs` | the demo core, with `DemoIncoming`/`DemoTyping` injection (a `DemoIncoming` of `zf-demo:call` or `zf-demo:video-call` rings a call), simulated calls with test-pattern video, avatars, storage, privacy, shared media and receipts; `disposition` says for each command whether it answers, accepts without a reply, or is refused (`ZF_E_STATE`, before any blob is taken) |
| `abi/fixtures/` | sample records Rust writes and Go's golden test reads back |
| `VARIANTS.md` | all 247 variants: fields, size class, the record each has or why it has none |
| `src/tests_real.rs` | a real core through the C entry points: the cached chat list at open, closing with an unanswered dialog |
| `tests/abi_slice.rs` | the vertical slice through the built library, by dlopen |
| `tests/c/measure.c` | a C program on the header: open-to-chat-list time and footprint |

```sh
# from the repository root
cargo test -p zapfast_core                                  # unit, golden, slice, C header
cargo build -p zapfast_core --profile core-release          # panic = "unwind", stripped
ZF_WRITE_FIXTURES=1 cargo test -p zapfast_core --lib fixtures   # after a schema change

# from gnui/
go run ./tools/abigen          # regenerate both sides
go run ./tools/abigen -check   # fail when either side is stale
go test ./abi                  # golden round trip, allocations, malformed input
```

**Always build and test with `-p zapfast_core`.** The crate depends on
upstream's with `default-features = false, features = ["headless"]`, so the
dylib has no egui. A workspace-wide command (`cargo build --workspace`,
`cargo test --workspace`) also builds the root crate with its default `ui`,
Cargo unifies the two, and the result links egui and fails the DTO matches.
`cargo check -p zapfast_core --features ui` checks those matches against the
egui configuration (its updater variants) on purpose; it is never shipped.

```sh
scripts/gnui/check_headless_tree.py -p zapfast_core          # no UI crate in the tree
scripts/gnui/check_gpl_sounds.py target/core-release/libzapfast_core.dylib target/debug/libzapfast_core.dylib
python3 scripts/gnui/licences.py --check                     # the licence allow-list
```

**A real core** spawns upstream's backend with `Backend::spawn_early`: an
account whose archive is already encrypted and prepared by this build lists
its chats right after `zf_core_open`, before `zf_core_start`, through a
read-only connection that writes nothing (3.9 ms from just before
`zf_core_open` to the last chunk for 500 chats, release build, in-memory
keyring). A new account, an archive that needs migrating, or one prepared by
another build waits for the permit. Every `PickRequest` is answered: by
the UI with `PickAnswer`, by the core with no paths when the request cannot
be encoded, and by `zf_core_close` for any still waiting.
