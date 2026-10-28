# ZapFast on go-native-ui: the migration plan (phase 7)

Drafted 2026-10-18, for your approval. It carries out D33 (the framework's DECISIONS.md):
- ZapFast keeps its Rust core, built as a library inside the app bundle behind a small C ABI;
- the whole UI is rewritten in Go on go-native-ui, with the Rex design system (`rexui`), and it should feel native on macOS. Other OSes come afterwards.

Your request (2026-10-18): "clone the codebase of zapfast in this computer and migrate it to our framework + using the rexlook theme/design system/components, it should feel macos native (we take care of other operating systems afterwards)."

**Your decisions:**
- the Rust core is bundled as a library, with a Go UI (D33);
- **full parity before your first look**;
- a private repository (`AdamAugustinsky/zapfast-gnui`, branch `gnui`, with crmne/zapfast as the `upstream` remote). Since 2026-10-28 the work happens in this public repository, `AdamAugustinsky/zap`, which started from zapfast-gnui 23c1b7d without its history; the private repository is archived. Upstream is merged up to crmne/zapfast a95d8f0 (2026-10-19, v0.19.0-198);
- the development loop uses the capture helper now; you create an Apple signing identity later;
- the defaults for the five former open questions (2026-10-18, revisable): a separate identity with its own data, the updater on a local test feed, the split kept private, full Rex, no plaintext chat-list snapshot ("Open questions").

Revised 2026-10-18 after an independent review (GPT-6.1 Sol), whose findings you accepted, and again after its scoped re-review; the fixes are in the sections they concern.

**How the two plans fit.** The framework work is phase 6 (`gnui:docs/PHASE6-PLAN.md`). This plan runs beside it from the start: the Rust ABI (Z0) needs nothing from the framework, and each later step starts as the framework pieces it needs land.

**Paths below:**
- `zf:` is this repository;
- `gnui:` is go-native-ui (public; `~/Projects/go-native-ui`);
- `research:` is `gnui:docs/research/`. The main input is `research:zapfast.md`, read at upstream d1fdf55; this branch is at a95d8f0, three commits later (audio now plays and records through `fastframe-audio`).

**Not in this phase:**
- the Go UI on Windows and Linux (upstream's egui build stays the client there);
- real WhatsApp accounts in any automated test (`zf:AGENTS.md`, "Privacy": synthetic data only);
- upstreaming anything: the `headless` split stays private (decided 2026-10-18).

## Layout and upstream merges (D50)

```
zf: (branch gnui)
├── src/, assets/, packaging/, …   upstream's crate, changed as little as possible (below)
├── Cargo.toml                     + a `headless` feature, + `[workspace] members = ["zfcore"]`
├── zfcore/                        new: the cdylib crate `zapfast_core` (C ABI v1)
│   ├── abi/zf.schema              the one schema of every command and event record
│   ├── src/abi_gen.rs             generated from the schema (checked in; `go run ./tools/abigen -check`)
│   ├── src/{lib,cores,dto,wake,wire,blobs,demo}.rs   (the pick bridge itself is upstream's `src/pick.rs`)
│   └── include/zfcore.h
├── gnui/                          new: the Go module (the app)
│   ├── go.mod                     requires gonativeui; replace → ./framework
│   ├── framework/                 git submodule: go-native-ui at a pinned commit
│   ├── cmd/zapfast/               main
│   ├── core/                      bindings on gonativeui/clib; the fake core for tests
│   ├── abi/                       generated from ../zfcore/abi/zf.schema
│   ├── model/                     pointer-free stores per account (chats, messages, contacts) and byte arenas
│   ├── logic/                     the reducers ported from zf:src/app.rs (notification policy, read state, voice autoplay …)
│   ├── markup/, mentions/         ports of zf:src/markup.rs and mentions.rs, producing rich-text spans
│   ├── i18n/                      tables generated from zf:assets/i18n/*.po (11 languages)
│   ├── ui/                        screens on rexui: shell, rail, chats, conversation, composer, info, dialogs,
│   │                              settings, login, picker, preview, calls, lock
│   └── gnui.json                  packaging (gnui package)
└── docs/GNUI-MIGRATION-PLAN.md, docs/PARITY.md
```

- **The framework is pinned as a submodule.** gonativeui's module path is the placeholder `gonativeui` (D1), so it cannot be fetched by path. The submodule makes CI reproducible. Locally, a `go.work` may point at `~/Projects/go-native-ui` while phase 6 moves.
- **The changes to upstream files are confined.** Egui-free code that lives in a UI module today moves to a new file, and the old module re-exports it, so upstream's diff in its own files is mostly moved-out code and `cfg` lines. The target is under about 600 changed lines in upstream's files, measured at each merge; everything else lives in new files.
- **The seam inventory.** Every path from the core (backend, worker, archive, calls, call audio, model and what they use) to egui, eframe, winit, glow, wgpu, the egui-dependent fastframe crates (`fastframe-shell`, `-titlebar`, `-text`, `-theme`, `-fonts`, `-icons`, `-emoji`, `-scroll`, each checked in `Cargo.lock`), rfd, or a UI module. Found with `grep` over a95d8f0 (rg is not installed here), test modules excluded; Z0 re-runs it at the commit it starts from, and the gate below catches anything missed.

| Seam (upstream file and lines) | What crosses | Change under `headless` | New file it moves to |
|---|---|---|---|
| `backend.rs:1255-1256`; `calls.rs:21`, `:524-529` (`VideoTick`), `:1192`, `:1308`, `:1323`; `backend/worker/calls.rs:1168` | `Arc<egui::ColorImage>` call frames; `crate::video::rgb_image` and `orientation_turns` | frames are raw I420/RGBA buffers with a stream id, handed out by `zf_frame_acquire`; the openh264 decode and orientation code move out of `video.rs` (which imports egui) | `src/video_frames.rs`; the `ui` build converts to `ColorImage` in `video.rs` as before |
| `backend/worker.rs:11311-11340` (sending a video) and `animation.rs:516-558` | `crate::animation::poster`: it reads an **MP4**'s width, height and duration with the `mp4` crate and decodes about a second of frames through `decode_mp4_from` (`animation.rs:456-514`: openh264, with `push_annex_b` and `avcc_to_annex_b` at `:651-668`), whose frames are `egui::ColorImage` from `frame_of` (`:622-649`) and `to_color_image` (`:424-440`), with `MAX_WIDTH` (`:18`); the last frame becomes the `image::RgbImage` thumbnail. The worker uses all three results: the JPEG thumbnail, `width`/`height` and `duration_seconds` on the outgoing video message | the MP4 reader, the openh264 decode loop, the Annex B helpers, the scaling and `Poster` move out, producing `image::RgbaImage` frames instead of `ColorImage`; `animation.rs` keeps its egui decoding by converting those frames with `to_color_image`. Upstream's poster tests (`animation.rs:778`, `:786`, `:1572`) move with it, and Z0 adds one that checks a sent video's thumbnail, size and duration are unchanged byte for byte against the `ui` build. Demo posters (`animation::video_frame`, `:561-568`, used by `demo/stock.rs:235` and `demo/tour/whats_new.rs:88`) use the extracted decoder under `headless` | `src/mp4_frames.rs` |
| `backend.rs:1354` | `pub use fastframe_shell::Waker` (`fastframe-shell` pulls eframe, winit, glow and wgpu) | **Built:** a core `Wake` trait and `Waker` handle; `ui` converts `fastframe_shell::Waker` into it; `WakePipe` is a lossless wake on a socket (an interrupted write is retried; only a full socket counts as signalled). `zfcore` wakes its forwarder through `wake::Signal`, a condition variable implementing `Wake` | `src/wake.rs` |
| `backend.rs:657-677`, `:1144-1150`; `model.rs:1862`, `:1875`, `:2852` | `crate::app::AttachmentId` (`app.rs:910`, a `u64` alias inside the UI's `App`) | defined in the model; `app.rs` re-exports it | `src/model.rs` (one line) |
| `model.rs:2847` | `crate::app::ComposerMention` (`app.rs:964`) | the struct moves to the model; `app.rs` re-exports it | `src/model.rs` |
| `model.rs:2226-2859` (`Action`, the egui UI's action enum, unused by the backend) | `app_lock::FormMode` (`:2812`), which pulls in `app_lock.rs`, whose `:227` uses `notify::NotificationTarget`, which pulls in `notify.rs`: `fastframe-notifications`, `fastframe-dock` and the GPL-2.0 Pidgin sounds embedded with `include_bytes!` (`notify.rs:314-315`) | **Built: gated in place.** `Action` stays in `model.rs` behind `#[cfg(feature = "ui")]`; `app_lock` and `notify` are not compiled under `headless` (the Go UI has its own lock screen and notifications). The GPL sounds stay out of the dylib (gate below) | — (`model.rs`, `ui` only) |
| `backend/worker.rs:60` | `crate::app::PAGE` (`app.rs:37`) | a backend constant; `app.rs` uses it | `src/backend.rs` |
| `paths.rs:60`, used by `backend.rs:15`, `:82`, the worker and the archive | `eframe::storage_dir` | our own `storage_dir` with the same result per OS (eframe's rule, tested against it in the `ui` build) | `src/paths.rs` (local function) |
| `settings.rs` (egui at `:315-675`), used by `backend.rs:540`, `archive.rs:618`, `calls.rs:2649`, `app_lock.rs:15`, `model.rs:308`, `:2723-2816` | `NotificationSound`, `ThemeChoice`, `FontChoice`, `WallpaperColor`, `AutoLock`, `hex`, `unhex` | **Built: gated in place.** The plain types and hex helpers stay where they are; the egui parts (`Settings` and its UI) are `#[cfg(feature = "ui")]` | — (`settings.rs`) |
| `markup.rs` (imports egui and `fastframe-text`), used by `backend/worker.rs:12728-12730` and `model.rs:1163` | `markup::plain`, `markup::links`, `Mention` | **Built: gated in place.** The plain-text functions stay; the layout code is `#[cfg(feature = "ui")]` | — (`markup.rs`) |
| `wallpaper.rs` (egui at `:7-168`), used by `backend/worker.rs:6332`, `:6337` | `wallpaper::import`, `remove` | the file operations move out | `src/wallpaper_files.rs` |
| `backend/worker.rs:6189-6662` (11 `rfd::FileDialog` sites) | native file dialogs | **Built:** one `crate::pick` helper: without `ui` (or after `pick::use_host_dialogs`) it emits `Event::PickRequest` and blocks its blocking-pool thread until `Command::PickAnswer`; under `ui` it calls rfd as before. Each `Backend` owns its waiting dialogs (`pick::Dialogs`): shutting down, dropping the backend or the worker returning answers every one with no paths, and a dialog asked after that is answered at once. `zfcore` answers a request it cannot encode with no paths, and `zf_core_close` cancels the rest | `src/pick.rs` |
| `backend/worker.rs:6496` | `crate::opener::open_or_reveal` (`opener.rs` imports eframe and egui) | **Built (Z1): the core opens nothing under `headless`.** The worker answers `OpenLog` with `Event::OpenPath(path)` (the demo too), and Go opens it (`core.Shell`: `open`, then `open -R`; `app/shell` once it exists). The `ui` build still opens through `open_or_reveal` and ignores the event | — (`opener.rs`, `ui` only in use) |
| `qr.rs` (the `qrcode` crate) | the pairing QR code, drawn by the egui login screen | `qrcode` is a `ui`-only optional dependency; the core sends the QR payload string in `LinkStatus::Unlinked { qr }` and **Go draws the QR** | — |
| `backend.rs:964-968`, `:1270`, `:1275`; `backend/worker.rs:6918-6964` | the updater commands and events | compiled only with `ui`; the Go app has its own updater (Z4) | — |
| `util.rs:902-908`, `audio.rs:949-955` | egui in test modules only | those tests move under `#[cfg(all(test, feature = "ui"))]` | — |

  **The optional dependencies (built).** `ui` enables 23 optional dependencies on every platform (eframe, egui, egui_extras, skrifa, rfd, arboard, symphonia, qrcode and 15 fastframe crates: icons, fonts, emoji, text, theme, tray, shell, titlebar, instance, scroll, dock, autostart, notifications, power, update) and 5 platform-specific ones (winit, raw-window-handle, tray-icon, objc2, objc2-app-kit). `headless` itself enables nothing; it names the configuration.

  Checked and clean: `i18n.rs` (`fastframe-i18n`), `audio.rs` outside its tests and `call_audio.rs` (`fastframe-audio`), `mentions.rs`, `privacy.rs`, `sticker_search.rs` and the picker's emoji data (the `emojis` crate) have no UI dependency in `Cargo.lock`.
- **The gate (Z0 exit, then CI on every merge):**
  - `cargo build -p zapfast_core` and `cargo test -p zapfast_core`, where `zfcore` depends on the crate with `default-features = false, features = ["headless"]`. **Always with `-p zapfast_core`:** a workspace-wide build (`--workspace`) also builds the root crate with its default `ui`, Cargo unifies the features, and the dylib would link egui (the DTO matches then fail to compile, which is the alarm). `cargo check -p zapfast_core --features ui` checks the updater arms against the egui configuration on purpose and is never shipped;
  - `scripts/gnui/check_headless_tree.py -p zapfast_core`, a script over `cargo tree -p zapfast_core -e normal,build --prefix none` for both macOS architectures, that fails if any of egui, eframe, egui_extras, egui-winit, egui_glow, egui-wgpu, winit, glow, wgpu, rfd, arboard, or the egui-dependent fastframe crates above appears;
  - **the built dylib contains none of the GPL-2.0 sounds** (`scripts/gnui/check_gpl_sounds.py`, on the release and debug `libzapfast_core.dylib`), checked on the payload bytes: for each WAV in `assets/sounds/`, the gate parses the RIFF file, takes a distinctive 64-byte window from inside its `data` chunk (the first window, after the first 4 KiB of samples, that occurs nowhere else in either file and is not silence), and searches the built library for it. Hashing whole files would miss an embedded copy. The gate's own test builds a fixture library that embeds one sound with `include_bytes!` and must fail;
  - upstream's own `cargo test` with its default features (the `ui` build) still passes, so the egui client is unchanged. `--all-features` is not the gate: it turns `ui` on and would hide a missing seam.
- **Merging upstream.**
  - `upstream/main` is merged into `gnui` (merge commits, never a rebase: `gnui` is published). Upstream's linear-history rule (`zf:AGENTS.md`, "Branches") governs upstream's `main`, not this fork.
  - The cadence is weekly, at each upstream release tag, and before each Z milestone and checkpoint 2.
  - Each merge runs upstream's tests, the ABI tests and the Go tests against the demo core.
  - Nothing is ever pushed to `upstream`.
- **Two drift alarms:**
  1. **ABI drift.** The DTO conversions between upstream's `Command`/`Event` and the schema's records are exhaustive `match`es with no `_` arm. A variant upstream adds or changes breaks the `zfcore` build at the merge, and the person merging adds it to the schema and regenerates both sides.
  2. **UI drift.** Upstream changes to its UI do not merge into a Go UI by themselves. After each merge, the upstream commits since the last merge that touch `src/ui/`, `src/app.rs`, `docs/_guide/` or `DEMO.md` are summarised (Luna) and triaged into `docs/PARITY.md` rows: new, changed or removed. Parity is a moving target, and this keeps it honest.
- **Upstream's `AGENTS.md` rules apply to Rust changes:** privacy, the definition of done, no reading users' archives.

## Architecture

```
Go process (one per app; LaunchServices keeps one instance; app/instance covers CLI launches)
├── main thread: AppKit pump (app.Main), Build per window, model updates, zf_send, zf_frame_acquire
├── per account: an event goroutine blocked on the core's pipe fd in the netpoller (no pinned thread)
│     └─ zf_next_event → decode records (generated, allocation-free) → pointer-free batch in a lease → ring → Wake
├── workers (task): blob reads, image decodes (img), voice PCM preparation
└── libzapfast_core.dylib (Contents/Frameworks), loaded with gonativeui/clib
      └── per account: one Backend (zf:src/backend.rs): its own thread and tokio runtime (2 workers),
          whatsapp-rust, the SQLCipher archive, downloads, calls (audio, camera, H.264) as upstream does
```

- **Windows.**
  - The main window: rail, chat list and conversation.
  - **Settings** in its own window, ⌘, (the macOS convention).
  - The **media viewer** in its own window, fullscreen-capable.
  - **Calls** in their own window.

  These are D30 windows (phase-6 F4). The info panel, message info and the search pane stay in the main window, as upstream has them.
- **Accounts.** Several accounts can be linked; each has its own core and its own `model` store. Events from an account not on screen update only that account (`zf:AGENTS.md`, `events_hidden`).
- **Media.**
  - Media arrives as files in the account cache, as upstream already does, and is opened by path: `img`, `media.Video`, `app/shell`.
  - Blobs are used only where upstream keeps bytes in memory: inline thumbnails, voice PCM, the emoji table.
  - Call media stays entirely in the core (D33); only video frames cross.
- **Voice notes.**
  - **Playback:** the core decodes Ogg/Opus to PCM plus a 64-bar waveform (a command answered with a blob), and `media.Player` plays it with the pitch kept at 1.5× and 2× (phase-6 S7). AudioToolbox Opus is unverified, so it is not relied on (`research:zapfast.md` §8).
  - **Recording:** `media.Recorder` captures PCM, `zf_blob_put` hands it to the core, and the core encodes Opus and sends.
  - **Other audio** (ringtones, ringback, call tones) stays in the core's `fastframe-audio` path. Two audio paths in one process are acceptable: the OS mixes them.

## Contracts

### The core ABI (D49)

```c
#define ZF_ABI 1
typedef struct zf_core zf_core;
#define ZF_MAX_RECORD (1u << 20)              // no command or event record is larger: 1 MiB
#define ZF_DRAIN_MIN (ZF_MAX_RECORD + 4u)     // zf_drain's smallest buffer
typedef struct { const uint8_t *px; uint32_t w, h, stride[3], fmt; int64_t pts_ns; } zf_frame; // fmt: ZF_FRAME_I420 or ZF_FRAME_RGBA
typedef struct { int32_t code; uint32_t len; uint8_t msg[256]; } zf_err; // caller-owned; filled by the failing call
// ZF_OK 0, ZF_EMPTY 1; errors ZF_E_ARG … ZF_E_REFUSED (-1 … -13), listed in zfcore.h

uint32_t zf_abi_version(void);
uint64_t zf_schema_hash(void);                                          // both sides embed it; checked at open
int32_t  zf_core_open(const uint8_t *cfg, size_t len, zf_core **out, zf_err *err); // cfg: an OpenConfig record (ABI, schema hash, demo, state and cache dirs, account)
int32_t  zf_core_start(zf_core *c, zf_err *err);                        // the startup permit: migrations, connect (after the first frame)
int32_t  zf_event_fd(zf_core *c);                                       // O_NONBLOCK read end; readable when events wait
int32_t  zf_next_event(zf_core *c, uint8_t *dst, size_t cap, size_t *len, zf_err *err); // one record; 0 ok, 1 empty, <0 error; cap ≥ ZF_MAX_RECORD
int32_t  zf_drain(zf_core *c, uint8_t *dst, size_t cap, size_t *len, uint32_t *count, zf_err *err); // whole records, each after a u32 length; cap ≥ ZF_DRAIN_MIN
int32_t  zf_send(zf_core *c, const uint8_t *cmd, size_t len, zf_err *err); // copies and enqueues; never blocks
int32_t  zf_blob_read(zf_core *c, uint64_t id, uint64_t off, uint8_t *dst, size_t cap, size_t *len, zf_err *err); // a range: large blobs are read in pieces
int32_t  zf_blob_put(zf_core *c, const uint8_t *src, size_t len, uint64_t *id, zf_err *err);
void     zf_blob_free(zf_core *c, uint64_t id);
int32_t  zf_frame_acquire(zf_core *c, uint32_t stream, zf_frame *out, zf_err *err); // the newest I420 frame, valid until release; 0 ours, 1 the peer, 2 + i group participant i (Z3)
void     zf_frame_release(zf_core *c, uint32_t stream);
uint64_t zf_call_generation(void);                                      // the generation StartCall/JoinCallLink carry (Z3)
int32_t  zf_core_close(zf_core *c, uint32_t timeout_ms, zf_err *err);   // blocks: never on the main thread
```

**Built (Z0):** these names are `zfcore/include/zfcore.h`, implemented in `zfcore/src/lib.rs`; the schema has records for 117 commands and 68 events (`zfcore/VARIANTS.md`; `OpenPath` added in Z1).

This refines `research:zapfast.md` §3 ("C ABI sketch") as follows.

- **Records, not JSON.**
  - Every command and event is a little-endian record with length-prefixed byte fields, defined once in `zfcore/abi/zf.schema`.
  - A small Go generator (in `gnui/abi`, our own code) emits both the Go decoder and encoder and `zfcore/src/abi_gen.rs`.
  - **Why:** the research proposed JSON decoded off the main thread. The framework's allocation gate counts the whole process (D27), and receiving messages is a chat app's steady state, so decoding must allocate nothing on any thread.
  - Decoding writes into the model's byte arenas and pointer-free records.
  - The schema has about 245 variants: about 173 commands, about 110 of them from the UI, and about 72 events (`research:zapfast.md` §3, "What exists"; ±5). The worker's internal completions are not exported.
- **Errors come back in the same call.** Every fallible function fills the caller's `zf_err` (code, then a UTF-8 message truncated to 256 bytes) before it returns, and leaves it untouched on success. There is no thread-local "last error": `clib` calls may run on a different OS thread from one call to the next (phase-6 `clib` contract, "Errors come back in the same call"). The Go side keeps one `zf_err` per call site in the same Go-heap struct as its `clib.Call`, so reading an error allocates nothing until it is turned into a Go `error`.
- **Record sizes are bounded before anything is queued.**
  - **Every event and command record is at most `ZF_MAX_RECORD` (1 MiB).** The schema gives each variable field a maximum, and the generator checks that the sum fits; a variant that cannot be bounded does not compile.
  - **Large payloads never travel inline.** Each field that can be large is a blob id instead, or the event is chunked:
    - decoded pictures in events (`Event::ClipboardImage`, today `Result<DecodedImage, String>` with the RGBA bytes inline, `zf:src/backend.rs:1264`, sent at `zf:src/backend/worker.rs:6536-6551`; the RGBA payload at `backend.rs:1346`; sticker-maker previews): a blob id plus width, height and format;
    - pixels and bytes in commands (`Command::SendImage`'s `rgba`, `backend.rs:405-409`; `SetProfilePicture(Vec<u8>)`, `:558`; the group picture's JPEG, `:569`): put with `zf_blob_put` first, then referenced by id;
    - media files: a path in the account cache, as upstream already does (the worker's internal byte-carrying commands, such as `Outbound` at `:880-884`, are not exported);
    - history and chat-list batches: chunked into records of at most 256 messages or 512 KiB (576 KiB for lists that hold messages, since Z0: the largest message is over 512 KiB), with a batch id and a last-chunk flag, and applied as the chunks arrive;
    - lists of paths and search results: chunked the same way.
  - **The decided bounds (built):** message text is at most 196,608 bytes (`str(196608)` in the schema). Display text derived from a message (a chat row's summary, a quoted reply, toasts, previews, a pick dialog's title) is **cut at a grapheme boundary, never dropped** with its record (`dto::clip`). A record still over its bound becomes `EventDropped`, which the **Go UI shows as a placeholder** (a message or row "could not be shown") rather than skipping silently. Mentions: a command sends at most 256, and a message and its quote carry as many, so an edit or a quote round-trips; one received with more keeps the first 256.
  - **Enforced in Rust before enqueueing.** The encoder returns `Err(TooLarge)` instead of building an oversized record. The DTO layer turns that into an `EventDropped{kind, size}` record (always small) plus a log line with the variant, never the content, and keeps going. A debug build also asserts, so a test catches the variant.
  - **Malformed records are terminal for that record only.** The Go decoder checks lengths and the schema hash; a record it cannot decode is skipped (its length prefix is trusted only up to `ZF_MAX_RECORD`), counted, and reported once as an internal error in the log. A bad length prefix — the one case where the stream cannot be resynchronised — marks the core failed: the account shows "Restart required", and the event goroutine stops draining rather than spinning. Nothing ever leaves a record pending forever and blocks the ones after it.
- **Threading.**
  - Every function is thread-safe unless noted.
  - Commands go into the existing tokio `UnboundedSender`. Events go into a queue with `try_recv`.
  - **Wakeups are never lost.** Rust writes one byte to the pipe when `pending.swap(true)` was false. `zf_next_event` returning *empty* clears `pending` and checks the queue once more before returning.
  - Rust never calls into Go.
- **Ownership.**
  - Command and event buffers belong to the caller, which passes them for the duration of the call only: Go heap memory, never a stack, under the `clib` rules.
  - Blobs and frames stay in Rust until they are freed or released.
  - No pointer crosses the boundary and is kept.
- **Panics and aborts.**
  - Every `extern "C"` function is wrapped in `catch_unwind`. The cdylib is built with a profile that has `panic = "unwind"`; upstream's release profile aborts, and `catch_unwind` cannot catch an abort.
  - A panic in a function returns an error and marks the core poisoned. A panicked worker thread becomes a `Fatal` event, which the UI shows.
  - An abort (a double panic, out of memory) still ends the process, and the documentation says so.
- **Startup.**
  - `zf_core_open` runs on a goroutine started first thing in `main`, before AppKit, as Pilot's listings do (`gnui:DECISIONS.md` D27). It reads the Keychain, opens the archive with its raw key (no KDF, `zf:src/archive/encryption.rs:233`) and, when no migration is pending, emits the cached chat list at once.
  - **Built (Z0):** a real core spawns upstream's backend with `Backend::spawn_early`. Before the permit the worker only reads: `Archive::open_cached` opens the archive with a read-only SQLCipher connection, and only when it is already encrypted, has chats, and carries this build's schema stamp (a digest of every schema and column migration, written by `prepare` once it has prepared the archive). The worker then runs its `load_state`, which sends `Me`, `Contacts`, `Chats` (chunked), `Labels` and `Drafts`; the confirmed-deletion retry is skipped on a read-only archive. Nothing is written and no writer lock is taken: another connection holding the writer lock does not delay the list. With the permit the archive reopens for writing (preparation, legacy-favourite adoption, receipt pruning, the retry), and backfills, the device store and the connection follow. A new account, an archive that needs migrating, or one prepared by another build (no matching stamp) waits for the permit and shows skeleton rows; once prepared, the next open is early. Upstream's `Backend::spawn` is unchanged. An archive whose lock state is still being recovered (`chat_privacy_ready_v1` unset) keeps its chats back, as upstream does, so that case also shows skeleton rows. Tested with a seeded 500-chat archive (`zfcore/src/tests_real.rs`): every chat arrives without `zf_core_start`, **3.9 ms from just before `zf_core_open` to the last chunk** (3.8–4.8 ms over six runs) in a release build with an in-memory keyring (the Keychain read is not in that number).
  - `zf_core_start`, the existing startup permit (`take_startup`, `zf:src/backend.rs:1495`), runs after the first frame, so migrations and the network never delay the window.
  - **The one startup rule: skeleton rows only when the archive is slow.** The first frame waits for the cached chat list until a cap, the 150 ms budget minus the shell's own measured cost (the D27 pattern). If the list is ready by then, the first frame has complete content, and that is the normal case, gated. If it is not (a slow disk, a large archive, or a pending migration, which holds the list until after the permit), the first frame shows the shell with skeleton rows and a status line, and the list replaces them when it lands. There is no other trigger.
  - **No plaintext chat-list snapshot** (decided 2026-10-18): names and previews never leave SQLCipher for the sake of startup.
- **The demo core.**
  - `zf_core_open` with `demo` set emits a synthetic stream: link connected, me, contacts, chats, messages.
  - It answers commands as the server would: send → sent and updated, reactions, edits, deletes, polls, stickers, voice, and a call with a synthetic tone and test-pattern frames.
  - It uses upstream's CC0 demo fixtures (`zf:assets/demo/SOURCES.md`). Upstream's demo writes `App` state directly and emits no events (`research:zapfast.md` §3, "Driving the UI without an account"), so its data is reused and the event stream is new.
  - A demo-only `Inject` command makes it emit a chosen event (an incoming message, typing, a ringing call) at a scripted moment.
- **Versioning.** `zf_abi_version` must equal the Go side's `ZF_ABI`, or the app refuses to start with a clear message. The schema carries a hash that both generated sides embed and check at open.

### The Go bindings (`gnui/core`)

- **Loading.** `core.Load(path)` loads the library once with `clib.Open(clib.BundleFramework(…))` and looks every symbol up at setup. Loading happens on the startup goroutine, never before the first frame on the main thread.
- **Opening.** `core.Open(cfg) (*Core, error)` starts the event goroutine. The goroutine blocks on `os.NewFile(fd)` in the netpoller, which pins no thread (the `core.Parker` pattern, `gnui:docs/PHASE5-PLAN.md` Status). It drains with `zf_next_event` into one reused buffer of `ZF_MAX_RECORD`, allocated once, which every record fits by contract; history arrives as chunks. It decodes into a lease, pushes the lease on the ring, and calls `platform.Wake`.
- **Applying events.** On the main thread, `c.Poll(dst)` returns the ready batches. The app applies them to `model` within a time slice of 2 ms per frame and releases the leases; the rest waits for the next frame (D27's slicing).
- **Sending.** `c.Send(cmd)` encodes into a reused buffer and calls `zf_send` with its four arguments (core, buffer, length, `zf_err *`), exactly as the `clib` example in `gnui:docs/PHASE6-PLAN.md` ("Calling C from an app") shows: the `zf_err` is heap-resident in the same struct as the call site's `clib.Call`, so it has one caller at a time, and it becomes a Go `error` only on failure. `zf_send` takes microseconds and never blocks. Typing sends `Composing` and `SaveDraft` (`zf:src/ui/conversation.rs:1729-1733`) without allocating.
- **Blobs** are read with `c.ReadBlob(id, dst)` on workers. **Frames** use `c.Frame(stream)` and `c.ReleaseFrame(stream)` on the main thread inside a frame: the I420 planes are uploaded through phase-6 C7, then released.
- **Closing.** `c.Close()` runs on a worker, with the timeout.
- **The fake core.** A pure-Go `Core` with the same interface replays fixtures and records commands. It is for unit tests on every OS, without the library. The real core is tested in macOS CI in demo mode.
- **Allocations:** 0 per event and per command once warm, gated together with the UI.
- **Built (Z1), as specified for Z1 rather than as above:** the event goroutine only waits (in the netpoller, on a duplicate of the event fd it never reads) and wakes the loop, then sleeps on a `core.Parker` until the UI has drained to empty; on the UI thread `c.Drain(h, budget)` calls `zf_drain` into one buffer of `ZF_DRAIN_MIN` and decodes each record into one reused `abi.Event` for a `Handler` (its bytes valid during the call), within an optional time budget. There are no leases or ring yet; a pointer-free store (`model/`) can take them over in Z2. `core.OpenAsync` does the load and the open on a goroutine and hands the core over through `Pending`, polled each frame. Screens take a `core.Conn`; `core/fake` implements it without the library (scripted events, zfcore's fixtures, recorded commands), with no threads or clock, on any OS. A bundled app loads only `Contents/Frameworks/libzapfast_core.dylib` and fails closed without it; `$ZAPFAST_CORE`, an explicit path and the cargo search serve unbundled development runs only (`core.ResolveLibrary`).

### The UI

- **Screens are built from `rexui` components** (phase-6 D3, D7): the chat pieces, Avatar, Badge, Popover, Toast. A component the framework lacks is written in the app first and offered to `rexui` if it is generic.
- **The model is pointer-free:**
  - chats, messages and contacts are fixed records with offsets into per-account byte arenas;
  - message bodies keep the raw text plus the spans produced by the markup and mentions ports;
  - nothing holds Go pointers per row.
- **`logic/` ports upstream's reducers** from `zf:src/app.rs`: notification policy, read state while focused, autoplaying the next voice note, forwarding batches, selection mode, pending deletes. Each port names its source lines, and gets the upstream tests rewritten in Go where they exist.
- **Look: full Rex** (decided 2026-10-18): native controls with Rex identity, the Rex field and glass panes around the chat list and conversation, the rail on the field, and system-coloured controls.
- **Themes.** Upstream's custom themes (`zf:docs/_reference/themes.md`) map onto Rex colour roles.
- **The composer** is the hosted `NSTextView` (phase-6 N4), used through its contract (`gnui:docs/PHASE6-PLAN.md`, "The composer"):
  - the draft, mentions and edits live in an app buffer in UTF-8; the editor maps to and from AppKit's UTF-16 ranges;
  - **completion** (`@` mentions, `:emoji:` shortcodes): `logic/` decides from `Changed` and the byte `Selection` when a trigger is active, and the conversation screen draws the popup at `CaretRect`, above the composer (or below it when there is no room), never overlapping it (D41). While it is open the app holds `SetCompletionActive(true)`, so Up, Down, Tab, Enter and Esc reach it as host commands through `doCommandBySelector:`, not the text. Accepting calls `Replace` on the trigger range, which is one undo step, and adds the mention span;
  - **marked text** (an IME composition) closes the popup and blocks `Replace`; the send key commits the composition rather than sending;
  - Enter policy, drafts (`SaveDraft`) and `Composing` follow upstream (`zf:src/ui/conversation.rs:1729-1733`).

## Work

Effort and line counts come from `research:zapfast.md` §5: a Go UI of about 45–55k lines for parity, plus 3–5k new lines of Rust. Sizes are assumed elapsed weeks for each item's lead, used only for the scheduling scenario below; Z0 grew with the seam inventory.

| Item | What | Needs (phase 6) | Who | Size (weeks) |
|---|---|---|---|---|
| **Z0** core | the `headless` feature and every seam in the inventory above, with their new files; the `zfcore` crate; the schema and generator with record bounds; DTO conversions, with blob ids and chunking for large payloads; `zf_err` on every fallible call; the wake pipe, blobs (ranged reads) and frames; the pick bridge; the opener and updater moved out; panic containment; the demo core with `Inject`; the fast cached chat list; Rust tests and a fuzzer for the decoder; a universal dylib build; a size measurement; the licence audit | nothing | Opus (high) leads the ABI and the headless split; Luna inventories the variants and re-runs the seam grep; two Sonnet agents write the conversions (commands, events) | 6 |
| **Z1** bindings | `gnui/core` and `gnui/abi`; the fake core; a thread and signal stress test with the real core under `SIGPROF` and preemption, with errors read from `zf_err` across goroutine moves | F2 `clib` | Opus (high) | 3 |
| **Z2** shell and first screens | `app.Main` with `KeepRunning`; the main window's chrome (Toolbar, the rail with the traffic lights overhanging it, `zf:AGENTS.md` "Architecture"); the native menu bar from upstream's (`zf:src/macos.rs:47-110`); the account switcher; login and pairing (QR, phone code); the chat list (fixed rows, avatars, badges, typing, muted, the @ marker; reordered with `VListState.Move` as chats are pinned or get messages); search; label chips; Archived, Locked, Favorites, Starred; the Settings window; app lock with secure input; the ported settings model | F3, F4, F5, N1, N2, **D3, D4** (the components and the window chrome), C1, C3, C5, C8, S1, S2 | Opus (high) leads the model and shell; Sonnet per screen | 5 |
| **Z3a** conversation | the transcript on `VList` (anchoring, prepending history, stick to bottom, the unread divider, jump to a message, deletes for everyone and backfilled history inserted mid-list with `Splice`); bubbles with markup, links, mentions and emoji-only sizing; text selection over the whole transcript and copy in upstream's format (the app walks `Selected()` through its model); dividers; reactions (flow layout); replies and quotes; pins; interactive messages (buttons, lists, carousels); polls; selection mode, forward, delete with NSAlert; message info; the info panel; media (photos, stickers, GIFs, inline video and round video notes, voice notes, documents, locations, contacts); the pickers (emoji from the core's table, GIF search through the core, stickers); the media viewer window with pinch zoom on `EvMagnify` and smart zoom; **the cropper** and **the sticker maker** (below) | F4, **F5** (gestures), C1–C6, N5, N6, S6, S7, S8, **D7a** (Popover, Toast, Badge, Avatar, the chat pieces) | Opus (high) for the transcript; Sonnet per message kind and dialog | 6 |
| **Z3b** composer | the composer on N4's contract: Enter policy, drafts, edits, `@` and `:emoji:` completion through the handoff, marked text, the attachment row, pasting and dropping images and files | N4, **D7b** (the composer's styling), N5, S6 | Opus (high) | 3 |
| **Z4** system and the rest | notifications (the policy port; a click opens the chat and message; reply from the notification; withdrawn when read; system sounds, since upstream's Pidgin sounds are GPL-2.0, `research:zapfast.md` §4); the Dock badge and bounce; the status item and resident mode; the login item starting hidden (a generated agent, below); single instance; `whatsapp://` and `wa.me` links (a URL type macOS upstream lacks); reconnect on wake; calls (the call window, frames from the core, Dock attention, device pickers through the core); accessibility (labels; new messages announced through Live); the 11 languages; upstream theme files; updates against a local test feed (decided) | S3, S4, S5, F3, F4, C7, N7 | Opus (high) for calls; Sonnet for the rest | 5 |
| **Z5** parity close-out | sweeping the checklist, budgets, packaging, the independent review, checkpoint 2 | everything | me | 2 |

- **Named parity owners for the two image editors** (`zf:docs/_guide/using-zapfast.md:189-195`, `:223-234`), which have no screen of their own:
  - **The cropper** (crop a staged picture by edges, corners or whole; Turn left and right; Reset; Enter keeps, Escape leaves; reopening starts from the original with the chosen crop; transparency kept): the Go screen `gnui/ui/cropper.go` (from `zf:src/ui/image_editor.rs`) owns the interaction; the writing stays in the core's existing `Command::ApplyPictureEdit` → `picture_edit::write` (`zf:src/backend/worker.rs:6639-6652`). Its parity rows name both.
  - **The sticker maker** (choose a PNG, JPEG, WebP or GIF; drag the square and Size; keep or drop transparency; emojis for search and WhatsApp's suggestions; Send or Add to favorites; a 512 × 512 WebP under 100 KB): the Go dialog `gnui/ui/stickermaker.go` (from `zf:src/ui/dialogs.rs:1110`) owns the interaction and preview; encoding stays in the core's `Command::MakeSticker` → `backend/sticker_maker.rs` (`worker.rs:6583`), with the preview returned as a blob.

**Waves** (shared with the phase-6 plan):

| Phase-6 wave | Phase 7 |
|---|---|
| 0–1 | Z0 |
| 2 | Z1 (as soon as F2 lands); Z0's measurements reported |
| 3 | Z2 (once F4, D3 and D4 land) |
| 4 | Z3a (once D7a, F5 and C4 land), Z3b (once N4 and D7b land) |
| 5 | Z4, Z5 → checkpoint 2 |

- **The critical path in one scheduling scenario** (`gnui:docs/PHASE6-PLAN.md`, "Waves and the dependency graph", which states the scenario's assumptions and gives the full table): with the sizes above taken as elapsed weeks, and L-sized items assumed at 5 weeks since the research gives L no upper bound, the longest chain is **wave 0 → F1 → F3 → F4 → N3b → N4 → D7b → Z3b → Z4 → Z5: 32 weeks in that scenario.** It orders the work; it is not an estimate.
- **Near-critical:** D1 → D2 → D3 → Z2 → Z3a (2 weeks of slack: design gates ZapFast, so D3's components Z2 needs go first); N7 (3); Z0 → Z1 (4); D4 and D7a (4).
- **The path is re-derived after phase 6's wave 0 from measured agent throughput** (how long the first items actually took per size), and again after Z0 and each checkpoint.
- **An internal slice:** Z3a's first deliverable is a vertical slice on the demo core: pair (demo), chat list, conversation, send (through a plain drawn field until Z3b), receive. It is checked internally and not shown to you, per "full parity first".

## The parity checklist (`docs/PARITY.md`)

**Built by Luna, checked by me.** One row per behaviour, with:
- id;
- the behaviour;
- its source (file and line);
- a macOS note;
- the Go owner (screen or package);
- how it is verified: a scripted step id, a capture, or a hand test;
- status.

**The sources:**
- `zf:PRODUCT.md`, and `zf:README.md` "What it does";
- `zf:docs/_guide/using-zapfast.md`, all sections: Writing, Selecting messages, Starred, Pinned, Stickers, Attachments, Locations, Interactive messages (lists, polls, carousels), Videos and photos, Polls, Voice messages, Calls, Group calls, Copying, Chats, Links, Locked chats, Labels, Notifications and the tray, Screen readers;
- `zf:docs/_guide/getting-started.md` (linking, history);
- `zf:docs/_reference/settings-and-files.md` (Settings, App lock, Updates, The log, Archive encryption, file locations), `themes.md`, `how-it-links.md`;
- `zf:DEMO.md`'s demo pages and both tours;
- the 29 shortcuts (`zf:src/ui/keys.rs:622`) and the macOS menu (`zf:src/macos.rs:47-110`);
- `zf:AGENTS.md` "Product boundaries", for what ZapFast deliberately does not do.

**Rows are re-triaged at every upstream merge.**

**Not parity items:**
- what upstream does not do on macOS: pausing other media is a no-op (`zf:src/media_pause.rs:14-20`); deep links are CLI-only, and we add a URL type;
- Linux- and Windows-only features (Omarchy theme integration, Flatpak, the drawn titlebar);
- Pidgin's sounds, which are replaced by system sounds and listed as a known difference.

**Done** means the row's verification passes, or you waive the row in writing.

## Testing without an account

1. **Unit tests** on every OS: the Go side against the fake core.
2. **Integration** in macOS CI: the real core in demo mode, `widgettest` scripted sessions (by role and name), the exact allocation gate over whole sessions (D27), and `Inject` for incoming events. These run on the fork's private-repo minutes, which are limited for macOS runners; see Risks.
3. **Content reference:** upstream's `--demo-shot` of each demo page (`zf:DEMO.md`), next to our offscreen render and a GnuiShot capture of the same page. The comparison is of **content and behaviour, not pixels**: the look differs on purpose.
4. **Hand tests** by you at checkpoint 2, with your own account only if you choose to (risk 8).

## Packaging and signing

`gnui package` reads `gnui/gnui.json`:
- the id `ai.a3lab.zap` (decided 2026-10-18; `.dev` appended by `gnui run`), the name ZapFast, the version from `zf:Cargo.toml`;
- `NSMicrophoneUsageDescription`, `NSCameraUsageDescription`, and the `device.audio-input` and `device.camera` entitlements (as `zf:packaging/macos/Info.plist` and `entitlements.plist` have them);
- `CFBundleURLTypes` for `whatsapp`;
- the core in `Contents/Frameworks/libzapfast_core.dylib`, built for arm64 only (Apple silicon, the framework's D55); the Rust toolchain already needs the Command Line Tools;
- the icon from `zf:packaging/macos/icon-1024.png`;
- **the login item:** a `loginAgents` entry (`{"name": "autostart", "args": ["--start-hidden"]}`). `gnui package` generates the agent plist into `Contents/Library/LaunchAgents/` with the label `<id>.autostart` and the program `/usr/bin/open -g -b <id> --args --start-hidden`, from whichever id is being built (phase-6 `app/login`). Upstream's `zf:packaging/macos/autostart.plist` is not used: it hard-codes `me.paolino.fastsapp` and would launch upstream's app. Settings → "Open at login" calls `login.Agent("autostart").Enable()`, because `SMAppService.mainAppService` cannot pass `--start-hidden`. Z4's test registers the agent on a `gnui run` bundle, starts it with `launchctl kickstart`, and checks that ZapFast runs hidden in resident mode with no window and no bounce.

**Licences of the bundled core.** Z0 adds `scripts/gnui/licences.py`, our own script over `cargo metadata --format-version 1` for the `zapfast_core` target (normal and build dependencies, for both macOS architectures). It lists every crate with its version, source and SPDX licence expression, fails on a crate with no licence or with one outside an allow-list (MIT, Apache-2.0, BSD-2/3-Clause, ISC, Zlib, Unicode-3.0, MPL-2.0 for files used unmodified, CC0-1.0, BSL-1.0, OpenSSL, and, approved in Z0, MIT-0 and CDLA-Permissive-2.0), and fails on a git dependency it cannot resolve to a licence file. Its output is appended, generated and clearly marked, as a "zapfast_core (Go UI build)" section of `zf:THIRD-PARTY-NOTICES.md`, after upstream's sections (the Pidgin sounds are not in the Go UI build, which uses system sounds). The reviewer has already checked the pinned `wacore-libsignal`: MIT. The script runs in CI at every upstream merge; a new licence fails the merge until it is reviewed. Native code compiled inside crates (SQLCipher, OpenSSL, openh264, opus, libwebp, ring) is listed from the crates' own notices, checked by hand once in Z0. `licences.py --check` passes. **The notices record the OpenH264 patent risk** (risk 6): it is built from source, so Cisco's patent licence does not cover it; this must be settled before public distribution.

**Signing.**
- Signing is inside out: the dylib, then the app.
- **Development** uses `gnui run` with the `gnui-dev` identity, a `.dev` bundle id and no hardened runtime, so the core loads without a Team ID and grants survive rebuilds.
- **Release** uses the Developer ID of A3 Direct Solutions LLP (team JPXN94NG33, issued 2026-10-27, expires 2027-11-26), with the hardened runtime, library validation satisfied by one Team ID for both binaries, then notarization, stapling and the DMG. The key is only in the MacBook's login keychain, so releases are signed there (`docs/HANDTEST-BUILD.md`, Before distribution). No Zap build has been signed with it or notarized yet. Windows has no signing certificate yet.

## App budgets (D51)

| Budget | Value | How it is measured |
|---|---|---|
| Startup | **Normal case, gated:** ≤ 150 ms warm, exec → a first frame with complete content: the shell and the chat list's visible rows with names, previews and avatars or their placeholders, and no skeleton row. **Slow-archive case, reported:** the shell with skeleton rows and a status line by 150 ms, and the list when it lands (time reported). Content-first and half-bounce hold in both | the framework's startup report, run on two fixtures: a seeded archive of 500 chats with no migration pending (the gate: the first presented frame's semantic tree must contain the chat rows, and any skeleton row fails it), and the same archive behind an injected 1 s open delay plus one with a migration pending (each must show skeleton rows by 150 ms and the list afterwards); BounceRec |
| Frames | 0 allocations in steady state over a scripted demo session: switch chats; scroll a 10k-message transcript; hover and click links; select and copy; type in the composer (Go side); receive injected messages, typing and reactions; play a voice note | the exact gate (D27), CI and the MacBook |
| Scrolling | < 4 ms CPU per frame scrolling a 10k-message transcript with images (the framework gates `VList` at 1M items) | bench JSON |
| Idle | 0 presents and ≤ 1 wakeup/s for the whole process with the demo core, the status item, notifications and the window open or closed. With a real connection, the core's keepalives are measured and reported, not gated | the framework's idle measurement |
| Memory | `phys_footprint` ≤ 150 MB idle after the scripted session (upstream: 150–200 MB PSS on Linux, `zf:docs/_reference/benchmarks.md`); stretch 120 MB. Image budgets: 64 MB GPU, 32 MB decoded | footprint |
| Size | the Go executable < 10 MB stripped per architecture; the dylib measured in Z0 (below: **31.4 MB** stripped, arm64), with its ceiling then set by you; the bundle and the DMG reported | the CI size table |

**Z0's measurements** (2026-10-19, arm64 MacBook, `core-release` profile: thin LTO, one codegen unit, `panic = "unwind"`, stripped; `zfcore/tests/c/measure.c` with resident size added; "cold" is a fresh copy of the file, loaded once, since `purge` needs root; warm is the median of ten loads):

| | before (egui linked) | after (`headless`) |
|---|---|---|
| `libzapfast_core.dylib`, stripped | 36,491,584 B (34.8 MiB) | 31,438,976 B (30.0 MiB), −13.8 % |
| `dlopen`, cold | ≈ 360 ms (355–375) | ≈ 330 ms (330–430) |
| `dlopen`, warm | 4.7 ms | 4.4 ms |
| RSS after `dlopen` | 14.8 MB | 14.5 MB |
| RSS, real core open (no permit) | 16.6 MB | 16.1 MB |
| `phys_footprint`, real core open | 3.7 MB | 3.5 MB |
| `zf_core_open` (included) → last chat-list chunk | demo: 0.06 ms | demo: 0.06 ms; seeded 500-chat archive: 3.9 ms (in-memory keyring, read-only open) |

The cold `dlopen` is the first load of a newly written file (the code-signature check on 30 MB); it is the first launch after an install or an update, and alone it exceeds the 150 ms startup budget. Warm loads fit easily. Startup is gated warm (D51); the cold case should be reported, and the dylib's ceiling set, with this in mind.

## Exit criteria (checkpoint 2)

1. **Parity:** every row of `docs/PARITY.md` is done, or waived by you.
2. **Budgets:** every budget above holds and is reported in `docs/measurements/zapfast-*.md`.
3. **The ABI:**
   - every variant round-trips between Rust and Go;
   - every record stays under `ZF_MAX_RECORD`: a test builds each variant at its schema maximum, and an oversized payload becomes `EventDropped` without blocking the events after it; a malformed record is skipped, and a bad length prefix fails the core without spinning;
   - errors arrive in `zf_err` from calls made on different goroutines and threads;
   - the decoder survives fuzzing;
   - a panic is contained;
   - the stress test with the real core under profiling signals passes;
   - a mismatched ABI or schema is refused at open.
4. **The headless split and licences** (from Z0, then at every merge): the dependency-tree check finds no egui, eframe, winit, glow, wgpu or egui-dependent fastframe crate in `zapfast_core`; the dylib carries none of the GPL sounds; upstream's own `cargo test` still passes; the licence audit passes and its section of `THIRD-PARTY-NOTICES.md` is current.
5. **Merges:** the branch includes an upstream merge from within the last week before the checkpoint, with both drift alarms clear.
6. **Packaging:** a signed arm64 bundle (`gnui-dev`, or Developer ID once it exists) runs from `/Applications`, and its login agent launches it hidden. Notarization is verified once the identity exists, and listed as untested until then.
7. **The composer:** phase 6's real-editor matrix passes against ZapFast's composer (mentions and `:emoji:` completion included), and your hand checklist (a real Japanese or Chinese input method, dictation, Writing Tools) is done.
8. **Accessibility:** VoiceOver reads the chat list and the transcript, and announces a new message (your hand test).
9. **Review:** an independent review of the whole app, with its findings fixed.

## Decisions (D49–D51)

The entries are in the framework's `gnui:DECISIONS.md` (next to D33, which they refine), written on 2026-10-18 at your request for you to revise:

| # | Summary |
|---|---|
| D49 | The ZapFast core ABI: a `zapfast_core` cdylib with C ABI v1; records generated from one schema, each at most 1 MiB, large payloads behind blob ids or chunks; errors returned in caller-owned `zf_err` in the same call; a pipe fd and a drain call with lossless wakeups; blobs and frames owned by Rust until freed; `catch_unwind` under a `panic = "unwind"` profile; open (fast cached chat list) split from start (the permit); a demo core with injection |
| D50 | The fork's layout and upstream merges: a `headless` feature with the seams moved to new files (under about 600 changed lines in upstream's files), gated by a dependency-tree check and a payload-byte check for the GPL sounds; the MP4 poster decoder extracted with the rest; `zfcore/` and `gnui/`; the framework pinned as a submodule; weekly merge commits from `upstream/main`; ABI and UI drift alarms; the transitive licence audit; **the split stays private** (decided 2026-10-18) |
| D51 | ZapFast's app budgets (the table above) and identity: **`ai.a3lab.zap` with its own data** (decided 2026-10-18); the updater against a local test feed until your Apple identity exists; full Rex; no plaintext chat-list snapshot, with skeleton rows only when the archive is slow and a complete-content startup gate otherwise |

## Risks

1. **The pace of upstream.** Upstream is trunk-based and moves daily. Weekly merges and the drift alarms bound the cost, but a large upstream refactor of the worker can cost days.
2. **A Rust runtime inside Go:** signals on foreign threads under fakecgo, and aborts. Mitigated by the `clib` fixture test, Z1's stress test and `catch_unwind`; an abort remains fatal.
3. **Keychain prompts.** A new app identity reading a Keychain item may prompt. The stable `gnui-dev` identity keeps that to one prompt per item, not one per build.
4. **Startup.** The Keychain read and the SQLCipher open before the chat list are measured in Z0. If they do not fit the cap on the seeded archive, the normal-case gate fails; skeleton rows are the fallback only for a slow archive, not a way to pass it.
5. **Animated WebP stickers through ImageIO** are unverified (`research:go-and-others.md` §6). The fallback is frames decoded by the core, which has `image` with WebP, sent as blobs.
6. **openh264** is built from source inside the core for calls, so Cisco's patent licence does not cover it (`research:zapfast.md` §4). It is unchanged from upstream; VideoToolbox in the core is a later option.
7. **CI minutes.** macOS runners on a private repository have a limited allowance. The plan runs the fast unit tests on Linux and the integration job on macOS per merge and per Z milestone, not on every push.
8. **Account safety.** An unofficial client can get an account banned (`research:zapfast.md` §7, risk 13). Tests use only the demo core; a real account is your choice at checkpoint 2.
9. **Parity scope.** The checklist grows with upstream. A cut-off for the checkpoint is your call when we get there.

## Open questions

None open. **Decided on 2026-10-18** (you were given these defaults and did not object; each is revisable, and recorded in D50 and D51):

1. **The bundle id and data directory:** a separate identity, `ai.a3lab.zap`, with its own Application Support folder and Keychain items, so this app never opens upstream ZapFast's archive while ZapFast may be running. The login agent's label and target are generated from it (Packaging).
2. **The updater:** built against a local test feed, with Settings → Updates saying so, until your Apple identity and a release feed exist.
3. **The `headless` split stays private.** Nothing is offered to crmne/zapfast; the merge cost is carried by the drift alarms.
4. **Full Rex:** the Rex field and glass panes around the chat list and conversation, with Rex controls.
5. **No plaintext chat-list snapshot:** the first frame shows the archive's cached list; skeleton rows appear only when the archive is slow (including a pending migration), by the startup rule above.

## Status (2026-10-20, Z2 foundation: the contract)

- **Built on `z2-foundation`:** `gnui/model` (pointer-free stores: each row keeps its encoded abi record in a byte arena plus offsets of the hot fields; chats, messages per chat, contacts, link status, me, typing, avatars; generations; 0 allocations per applied event once warm), `gnui/state` (accounts with their `core.Conn` and model, the open chat, page and list mode, panes, selection, the one dialog, windows, lock, the settings' shape, command helpers, event listeners, the navigation history), `gnui/ui/screen` (the slots), `gnui/ui/shell` (the main window's Rex chrome, the rail, routing, the menu bar and commands, the other windows, `app.Main`'s handler), every screen package as a stub, `cmd/zapfast` on `app.Main` with `KeepRunning`, and `gnui/ui/CONTRACT.md`.
- **Not yet:** the model of the other events and the settings' loading and saving (deliverable 2).

## Status (2026-10-20, speed mode)

- **Speed mode** (Adam, 2026-10-20 about 22:30): full parity within about 24 hours.
  - No per-item review; one independent review of the whole app at the end, plus a focused one of app lock, the keychain and links.
  - Light gates per branch: `gofmt`, `go vet` and `go test ./...` in `gnui/` (the fake core, and the demo core through `$ZAPFAST_CORE`). No pixel or benchmark gates per screen; the budgets are measured once in Z5.
  - The framework is frozen to what ZapFast needs; S7 audio and S8 video are unparked.
  - No separate checkpoint 1: Adam hand-tests ZapFast once, at checkpoint 2.
- **Apple silicon only** (the framework's D55, 2026-10-20): the dylib and the app are arm64; no `lipo`.
- **The work runs as one foundation (`z2-foundation`: model, state, shell, slots) and then about a dozen screen slices in parallel**, each owning its own package under `gnui/ui/`, merged into `gnui` by one integrator.
- **The shared framework checkout** for ZapFast worktrees under `~/Projects/zf-wt/` is `~/Projects/zf-wt/go-native-ui` (a detached worktree of the framework at a recent `main`, which `replace gonativeui => ../../go-native-ui` resolves to without a `go.work`). The shared core for tests is `/Volumes/SSD/dev-toolchains/build/zf-shared/libzapfast_core.dylib` (`core-release`, built from Z1's head).

## Status (2026-10-19, Z1)

- **Z1 is built** on branch `z1-bindings` (from `gnui` at 621a466): `gnui/core` (loading, version check, `Open`/`OpenAsync`, `Send`, `Drain`, blobs, `Start`, `Close`, `OpenPath`), the smoke app `gnui/cmd/zapfast` on the demo core, and two core changes: `OpenConfig.keyring_service` (a real core refuses an empty service or upstream's `rocks.zapfast.ZapFast`; set process-wide through `zapfast::archive::set_keyring_service`) and the `OpenPath` event.
- **Tests** (`go test ./...` in `gnui/`, real dylib, demo core only): a whole session, `OpenPath`, schema and keyring refusals, ranged blobs, the event goroutine ending on `Close`, wakes neither lost nor spinning, budgeted drains, errors in their own `zf_err` from 8 goroutines under `SIGPROF`, and **0 allocations over whole sessions of 220 commands and 260 events**.
- **Startup** (`zapfast -startup` / `-exit-first`, `core-release` dylib, MacBook, three runs each): warm, the first frame is on glass in 129–150 ms with the chat list in it (the core is open 14 ms after exec, the list applied in the first frame's build at 108 ms); cold (a newly written dylib), the first frame is on glass in 117–134 ms with skeleton rows and the list follows at 314–332 ms. **A cold `dlopen` stalls the main thread even off it:** the kernel's first check of a new executable file (300–320 ms on the MacBook, about 210 here) ran inside `dlopen`, under dyld's lock, and the window's own loading waited (first frame 430 ms cold). `core.Load` now pays it first with a one-page executable `mmap` (refused, but the check is done), and `dlopen` takes 4 ms.
- **After the review (2026-10-19):** `Close` takes the call sites' locks with `drainS` first (a handler's `Send` deadlocked against it); `-core=PATH` works; the bundle loading policy; the thread and signal stress test (8 senders, 4 blob goroutines, a locked draining thread, `SIGPROF`, then `Close` mid-traffic); the fake core.
- **Not built:** leases and the ring (Z2's `model/`); call frames (`zf_frame_acquire` returns `ZF_E_NOT_FOUND` until the calls work).

## Status (2026-10-19)

- **Z0 is built** on branch `z0-integrate` (from `gnui`): the `headless` feature and its seams (table above), the `zapfast_core` cdylib on the headless core with no egui, the schema and generator (117 command and 67 event records), the DTO conversions, `zf_err`, the wake pipe, blobs, the pick bridge with backend-owned dialogs, panic containment, the demo core with injection, the fast cached chat list (`Backend::spawn_early`), the gates and the licence audit, and the measurements above.
- **Gates on the real dylib:** the `cargo tree` gate (`-p zapfast_core`, both architectures) finds no UI crate; the GPL-sounds gate passes on the release and debug `libzapfast_core.dylib` (and its self-test fixture fails it); `cargo test -p zapfast_core`, upstream's headless `cargo test --lib`, and upstream's own `cargo test` (the `ui` build) pass; `licences.py --check` passes.
- **Upstream's files:** +455 −467 lines in files upstream has (most of it moved code and `cfg` lines), and 1,117 lines in six new files.
- **Not yet built:** call video frames through `zf_frame_acquire` (the calls work) and the universal (lipo) dylib. The decoder fuzzer is Go's `FuzzEventDecode` (`gnui/abi`).

## Status (2026-10-18)


- **Planned; not started.** Revised after the independent review (GPT-6.1 Sol, 2026-10-18), all findings accepted by you and fixed in place, then revised again after the scoped re-review. Z0 starts with phase 6's wave 0.
- **Branch:** `gnui` at upstream a95d8f0 (0.19.0 plus later commits), identical to `upstream/main` before this document.
- **Not yet built or run here:** upstream ZapFast, its demo, and the Rust toolchain on this machine.
