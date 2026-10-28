# ZapFast (Go UI) app budgets on the Mac mini

The D51 app budgets (`docs/GNUI-MIGRATION-PLAN.md`, "App budgets (D51)") measured on built binaries of `gnui/cmd/zapfast`, on 2026-10-21, branch `z3-budgets`.

## Machine and setup

- Mac mini, Mac16,10, Apple M4, 10 CPUs, 16 GB, macOS 27.2 (26B5101f), a 5K display at 120 Hz.
- Go 1.27.1, `CGO_ENABLED=0`, the demo core from `ZAPFAST_CORE` (a prebuilt `libzapfast_core.dylib`).
- Each binary was built with `go build -o … ./cmd/zapfast`, never `go run`. Every live run used `caffeinate -d`, with the screen unlocked.
- **Timings come from development builds.** Since z4-lean (2026-10-21), release builds (`gnui_release`) have no `-exit-first`, `-startup`, `-idle` or `-bench`, like upstream's. So startup and idle A/Bs use development builds, which differ from release only by the diagnostics code:
  - about 300 KB of ZapFast's own (z4-lean cut the release build from 12,382,514 to 12,080,818 B);
  - about 220 KB of the framework's Debug menu and inspector. Stripped at f1af948, a development build is 12,601,858 B against 12,080,818 B for release.
  - A plain `go build` also keeps its symbol tables (18.6 MB on disk).

  Every timing in this file comes from such builds; the sizes are release builds. The one release-build timing on record is in commit ca85c22's message (framework 45287b0: release `-demo -exit-first`, before z4-lean).
- **Load caveat.** The machine was shared with other agents' builds and tests, Brave and WindowServer, and the load average was 5–31 throughout. Timings are noisy, so startup is reported as interleaved runs beside a control: `pilot`, the framework's own app, run in the same loop.

## Results

Round 3 updated the frames and size rows, round 4 the memory row. The startup, scrolling and idle rows are still round 1's, measured after its last merge of origin/gnui (3bd030c and 6790ccd); since then lazy icons brought the warm startup to a 142 ms median by the coordinator's measurement, which was not repeated here. The before/after comparisons in the round 1 sections were measured before that merge.

| Budget | Target | Measured | Verdict |
|---|---|---|---|
| Startup, warm | ≤ 150 ms, exec → first complete frame on glass | median **371 ms** [309–422]; pilot control 265 [234–371]. Before/after the fixes: 358 → 344 ms | **FAIL** |
| Frames | 0 allocations in steady frames; frames with or just after an action may allocate (D27, as decided in round 3) | round 3, 12 sessions: steady frames **8 mallocs in 3,686**, all in one frame after the pre-session collection (a one-off, which passes); action frames 102 mallocs in 5,076, the link click 7.4 a session. Round 1, counting whole sessions: fewest 2 mallocs | **PASS** (round 3's gate) |
| Scrolling | < 4 ms CPU per frame, 10k-message transcript | main thread **0.81 ms**/frame (1.08 ms at a load of 31); frame CPU P50 0.50, P99 0.88, max 1.16 ms; 0 mallocs | **PASS** |
| Idle | 0 presents, ≤ 1 wakeup/s | window open: 0 builds, 0.1 package-idle wakeups/s; closed: 0 builds, 0/s | **PASS** (see the wakeup note) |
| Memory | `phys_footprint` ≤ 150 MB (stretch 120) | round 4, full session at bc53b60: **132.5–133.1 MiB**, against 148.4–150.6 at the same head without the fix (one of those runs failed the gate). Scroll gate: 128.7–131.0 MiB. With the framework's spare drawables gone, about 108 (round 4). Idle (round 1): 96–117 MB with the window open; 55 MB resident (window closed) | **PASS** (stretch fails) |
| Size | stripped arm64 executable < 10 MB | **12,473,650 B** at round 3's final head (12,456,162 B at 8044123) (the release build, as `gnui package` makes it), after `app/fetch`, the shared emoji table and the single doodle mask. Round 2's head: 15,292,642 B | **FAIL** (see round 3: what 10 MB would take) |

**Size decision, 2026-10-21.** Adam accepted the release binary at about 12.4 MB, which is over the framework's < 10 MB per-architecture budget, because the Go executable carries the whole app's code (every screen, the framework, `encoding/json/v2`, `regexp`, the image codecs, `log/slog` and the i18n tables; see "Where the size goes" below). The Rust core is a separate dylib in the bundle and is not counted. Today's release build is 12,413,602 B. The working ceiling is about 12.5 MB; a larger build needs a new decision.

### Startup

Final: `zapfast -demo -exit-first`, 8 runs interleaved with pilot (load 6–10). Median 371.2 ms [309–422]:

- exec → main: 14.6 ms.
- app.Main → Start: 139.3 ms.
- Open: 92.4 ms.
- Shell.Init: 17.9 ms.
- First frame: 121.9 ms.

The fixes were compared before the last merge: 10 interleaved runs per binary, after one unmeasured run each (load 11–14), against the merge head 0415dfb.

| ms (median) | before | after | pilot |
|---|---|---|---|
| exec → first frame on glass | 358.4 [302–436] | 343.8 [332–415] | 276.9 [240–407] |
| exec → main | 15.3 | 16.0 | — |
| app.Main → Handler.Start (NSApplication, finishLaunching, sys.Start) | 137.3 | 124.3 | — |
| platform.Open (NSWindow, view, display link) | 62.9 | 93.5 | 112.7 |
| Shell.Init | 18.5 | 18.7 | 1.6 |
| first frame (two builds, encode, commit) | 123.4 | 116.5 | 72.1 |

- Every run's first frame was complete: 6 chat rows and no skeleton row.
- Earlier runs before the fixes, at a load of 5–16, measured 408–461 ms.
- Cold startup, the first exec of a freshly written binary, is ≈ 0.8 s (785–835 ms) because the code signature is checked. It is reported, not gated.

Where the time goes, from traces with an instrumented copy of the framework:

| Stage | Time |
|---|---|
| `[NSApplication sharedApplication]` (AppKit loading and initialising) | ≈ 90 ms |
| finishLaunching | ≈ 23 ms |
| NSWindow | 39–42 ms |
| view and setup → display link | 16–18 ms |
| Shell.Init, of which the composer's NSTextView takes ≈ 9 ms | 15–19 ms |
| The window's icon atlas at the first Begin: 137 SF Symbols rasterised on the main thread | 70–125 ms cold, 33–37 ms warm |
| Last build → first present | 27–40 ms |

### Frames: allocations

The scripted session is `zapfast -demo -bench -gate`:

- 12 measured windows of 725–730 frames each, after 1,200 warm-up frames and another 600 after a GC.
- The session switches chats (Grace Hopper, Katherine Johnson, then Ada Lovelace) and scrolls the 10k-message transcript up and back down.
- It sweeps over the transcript, hovers over the newest link and clicks it. The link opens nothing: a recorder replaces the shell.
- It drag-selects text and copies it with ⌘C.
- It types "on my way 👍" into the composer and clears it.
- It receives a message from the demo core, Ada's typing on and off, and a reaction added and removed.
- Main-thread CPU in the full session is 0.67–0.85 ms per frame.

The fewest allocations in a session went from 31 to 2, and most sessions now make 10–14. Before the last merge the fewest was 7. The merge brought the header avatar's per-switch string (3 per session), which is now fixed.

What remains, aggregated over 12 windows with `-allocsites`:

| Allocations | Owner | Why |
|---|---|---|
| ≈ 6 per session, in the sessions where the badge flickers | the framework (`app/dock.osSetBadge`, `nsApp`) | `SetBadgeCount` looks up its class and selector through `objc.Cls`/`objc.Sel` on each call, and each lookup allocates a C string. The app calls it twice when a message arrives in the open chat, because the badge goes 0 → 1 → 0. Upstream's `mark_read` clears the chat's unread state locally at once (`zf:src/app.rs:5057`); the Go port waits for the core's answer, so the count rises until the next drain. When that answer arrives in the same drain, the badge does not change: those are the sessions with 2–3 allocations. |
| 2 per session | the app (`ui/conversation.follow`) | Clicking a link hands its URL to the shell on a goroutine: one string and one closure. The real shell then runs open(1) through os/exec, which allocates in any case. |
| ≈ 1–3 per session | the runtime | New Ms (`newm`/`allocm`, their profiling stacks), new Gs, and the timer heap. These grow with machine load. |
| < 1 per session, occasional | the app and the framework | The drafts arena (`model.kv` through `SetDraft`) growing while typing. The copy buffer growing for a longer selection. The framework's `RichText`, cursor rectangles and semantic links. |

### Scrolling

Command: `zapfast -demo -bench -gate -session scroll`. The transcript has 10,000 messages. Every 10th is a JPEG photo, and links are scattered through it. The session wheels up through about 300 messages and clicks "Newest message".

- 323 frames, paced at 120 Hz: interval P50 8.34 ms.
- 0 mallocs.
- Main-thread CPU 0.81 ms per frame.
- Frame CPU P50 0.50 ms, P99 0.88 ms, max 1.16 ms.
- Input → commit P50 0.49 ms.
- After the last merge, at a load of 31: 0 mallocs, main thread 1.08 ms per frame, frame CPU P99 1.58 ms.

### Idle

Command: `zapfast -demo -idle 10s [-idle-closed]`. The app settles for 3 s, then measures for 10 s, with the status item made.

| | builds (= presents) | package-idle wakeups | all wakeups | CPU | footprint |
|---|---|---|---|---|---|
| window open | 0 | 1 (0.1/s) | 11 (1.1/s) | 7.5 ms | 104.2 MB |
| window closed (resident mode) | 0 | 0 | 11 (1.1/s) | 30.3 ms | 55.1 MB |

These are the final figures. Before the last merge they were: open 0 / 14 (1.4/s) / 1.3 ms / 116.7 MB; closed 1 / 21 (2.1/s) / 1.0 ms / 54.6 MB.

The gate counts package-idle wakeups, as the framework's idle measurement does. On a busy machine that number stays low because other processes keep the package awake, so all wakeups are reported too. Counted that way, the process sits just above 1/s (1.1–2.1/s). Resident-mode CPU rose from 1.0 to 30 ms per 10 s after the merge; this was not investigated, and neither were the wakeup sources.

### Memory

- After the full scripted session: 136–141 MB `phys_footprint` (85–135 MB resident), 137 MB at the final head. The 10k-message transcript and about 1,000 photo references are loaded.
- Idle with the window open: 96–117 MB. Resident mode: 55 MB.
- The 64 MB GPU and 32 MB decoded-image budgets were not measured separately.

### Size

- Built with `go build -trimpath -ldflags="-s -w" [-tags gnui_release] ./cmd/zapfast` at the final head: 15,907,362 B (15.2 MiB), or 15,890,834 B with `gnui_release`.
- Before the last merge it was 15,260,050 B; the merge's features added 0.65 MB.
- Framework apps built the same way: pilot 6,213,522 B, notes 5,555,986 B.
- The largest single cost is `net/http`. Only the updater's `httpFeed` (`ui/settings/updater.go`) imports it, and it brings in `net/http/internal/http2`, `crypto/tls` and `crypto/x509`. A copy without it measured 12.2 MB before the last merge (12,215,618 B with `gnui_release`).
- After that, the size is code volume: the screens, `encoding/json/v2`, `regexp` (app/login), the image codecs, `log/slog` and the i18n tables.
- The measurement modes cost about 0.1 MB: `-bench` links `widgettest`, which imports `testing`.

## What was changed

All changes are on `z3-budgets`.

- **The chat list's rows (`ui/chatlist`): ≈ 40 allocations per session removed.**
  - Each row now formats its time, preview, group prefix, full preview, unread announcement and accessible value into buffers that `rowInfo` keeps; those fields are now `[]byte`, not strings (`rows.go`).
  - The title is a string allocated only when it changes; `titleBytes` is compared with it first.
  - New helpers:
    - `appendStamp` (`format.go`).
    - `appendPlain`: `markup.ParseMentions` and `markup.AppendText`, with a span scratch on the Screen (`chatlist.go`).
    - `shortNameBytes`: a view, and the first field without `bytes.Fields`' slice (`names.go`).
    - `appendAnnounce`/`appendCount`: `i18n.Format` into a buffer.
  - The tooltip receives `model.S(previewFull)`.
  - `compact.go` and `body.go` were adjusted to the byte fields.
- **`abi/wire.go` `grow`.** It now grows as `append` does instead of to exactly *n*. A chat's page that grows by one message used to reallocate at every decode.
- **`model/messages.go` `upsert`.** A message re-sent exactly as stored is dropped before it reaches the arena. Each reopened chat receives its page again, so this means no arena growth and no row-generation bump, and therefore no relayout.
- **`state/state.go` `history.visit`.** The back list is made at its full length (65) on first use, instead of doubling inside frames.
- **`sys/sys.go`: the status item now follows the window.**
  - `Start` sets `lateTray`, and `begin` queues `startTray` with `app.AtSafePoint` instead of making the item before the window. Tests that call `begin` directly are unchanged.
  - Startup is about 15 ms faster in the median. Part of the item's cost was AppKit setup that NSWindow now pays: NSApplication → NSWindow grew by 27 ms, while app.Main → Start shrank by 13 ms and the timeline's start moved 44 ms earlier.
- **`ui/conversation/header.go`: 3 allocations per session removed.** Opening a chat reset the header avatar's state and made its path string again. The last eight paths are now kept (`avatarPaths`), as strings the image cache may hold.
- **`cmd/zapfast/bench.go`.**
  - `noPins` drops LoadPins, which the demo core refuses.
  - The link step aims at the newest link that is in view.
  - Added the `-session full|scroll` flag.
  - Removed an Escape step, which closed the open chat.

**Tried and reverted.** Rasterising the standard SF Symbols on a goroutine while AppKit starts saved 10–20 ms of the first frame. However, it crashed about 1 run in 10 with SIGSEGV PC=0. The framework's `internal/objc` calls through one shared call frame (`objc.F`), so any AppKit call made through it off the main thread races the main thread. A safe pre-warm needs framework support: per-thread frames, or a pre-warm API.

## What is still over, and why

- **Startup: about 2.5× over.**
  - On this machine under load, the framework's own floor is 265–277 ms (pilot). NSApplication alone takes about 90 ms.
  - The app's own costs:
    - The icon atlas: 137 symbols on the main thread at the first Begin, 70–125 ms cold.
    - Shell.Init: about 19 ms.
    - The status item: now after the first frame.
  - The levers are a framework pre-warm, or an atlas that rasterises only the first screen's icons, plus a re-measurement on an idle machine.
  - The fixture also differs: the demo core has 6 chats, not the seeded 500-chat archive. That archive needs a real core build, which is out of this branch's scope.
- **Frames: 2–14 allocations per session.**
  - About 6 are the framework's Dock call, made when the badge flickers. Fix it by caching the class and selector in `app/dock` (framework), or by clearing unread locally in `state.MarkRead` as upstream's `mark_read` does (a parity change in state/model).
  - 2 are the link-click handoff.
  - The rest are the runtime's thread creation under load, plus occasional buffer growth.
- **Size: 15.9 MB against 10 MB.**
  - `net/http` for the updater's HTTP feed accounts for 2.8 MB. Removing it means a different transport, which is a decision about updater security and proxies.
  - Even without it, an estimated 12.8 MB would remain (12.2 MB before the merge), against a framework floor of 5.6–6.2 MB.
- **Memory: stretch goal missed.** After the scripted session, footprint is 136–141 MB against the 120 MB stretch.

**Session coverage gaps.** The demo core refuses React, PreviewInvite, FetchOlder and LoadPins, and it cannot send images. So:

- The 10k history is injected through `Account.Event` in pages of 250.
- Others' reactions arrive as `MessageUpdated` events.
- Playing a voice note is not part of the session.

## Round 2 (2026-10-21): size, resident-mode CPU, wakeups

These were measured after merging origin/gnui at 940b3ed, with the framework at main 2ec1ca5.

**Size: what counts.** The size is the release executable, built as `gnui package` builds it: `-trimpath -ldflags="-s -w" -tags gnui_release` (`cmd/gnui/bundle.go`). `gnui.json` has no build-flag field, and none is needed: those flags are already all the toolchain offers.

### Where the size goes

This is the final binary, 15,141,410 B:

| Section | Size |
|---|---|
| `__text` (code) | 6.24 MB |
| `__gopclntab` (function tables, for stacks and the GC) | 5.35 MB |
| `__go_type` (type descriptors) | 1.43 MB |
| `__noptrdata` + `__data` | 0.97 MB |
| `__rodata` (string literals and constants) | 0.61 MB |
| `__LINKEDIT` | 0.49 MB |

How it was attributed by package:

- Each package's sizes are summed from `go tool nm -size -type` on the same build with symbols.
- The pclntab and the funcdata (`go:func.*`) are spread over packages in proportion to their code.
- Type descriptors and string literals are pooled by the linker, so they cannot be attributed to a package.

| By owner | Size |
|---|---|
| Go standard library and x/ | ≈ 6.1 MB, of which the runtime is 1.0 MB and the HTTP stack (net/http, http2, crypto/tls, x509, nistec) ≈ 1.6 MB of code |
| ZapFast's packages | ≈ 3.6 MB |
| Framework (gonativeui, go-text, purego) | ≈ 2.9 MB |
| Type descriptors | 1.43 MB |
| String literals | 0.49 MB |

The top 15 contributors:

| # | Contributor | Size |
|---|---|---|
| 1 | type descriptors (all packages) | 1.43 MB |
| 2 | runtime | 1.02 MB |
| 3 | gonativeui/platform | 0.61 MB |
| 4 | string literals (all packages) | 0.49 MB |
| 5 | crypto/tls | 0.46 MB |
| 6 | zapfast/abi | 0.41 MB |
| 7 | zapfast/theme, of which the two doodle PNGs are 349 KB | 0.40 MB |
| 8 | slices (generic instantiations) | 0.38 MB |
| 9 | gonativeui/rexui | 0.37 MB |
| 10 | gonativeui/ui | 0.35 MB |
| 11 | zapfast/ui/conversation | 0.30 MB |
| 12 | zapfast/ui/composer, of which `emojiTable` is 122 KB | 0.29 MB |
| 13 | net/http | 0.29 MB |
| 14 | encoding/json/v2 | 0.28 MB |
| 15 | net | 0.28 MB |

Next come http2 0.23, gonativeui/app 0.22, chatlist 0.22, reflect 0.21, settings 0.20, x509 0.19 and nistec 0.19 MB.

Embedded data (`//go:embed`):

| File | Size |
|---|---|
| `theme/doodles-light-ink.png` | 174,644 B |
| `theme/doodles-dark-ink.png` | 174,110 B |
| `ui/picker/emoji.tsv` (before this round) | 189,931 B |
| `ui/login/mark.png` | 27,720 B |
| `theme/presets/*.json` | 3.3 KB |
| `ui/settings/update-public-key.hex` | — |

Large tables that are not embedded files:

- The i18n catalogs: 10 × 31.8 KB of entry headers, plus their forms and 180 KB of translated text.
- `composer.emojiTable`: 122 KB.
- `crypto/internal/fips140/nistec`'s P-256 table: 88 KB.

### Cuts made (no behaviour change)

| Change | Release size | Δ |
|---|---|---|
| round 2's starting point (940b3ed) | 16,025,906 B | |
| `cmd/zapfast`: `-bench`, `-allocsites` and `-cpuprofile` left out of release builds (`//go:build !gnui_release`, with a stub that says so; `-exit-first`, `-startup` and `-idle` stay). This drops widgettest, runtime/pprof and their dependencies. | 15,823,762 B | −202,144 |
| `i18n`: each language's table is one deflated string constant, unpacked at its first lookup. The ten tables cost ≈ 118 KB instead of ≈ 650 KB of headers and text. Unpacking takes 0.25–0.41 ms per language, once; lookups still allocate nothing. Generator: `tools/i18ngen` `pack`; `-check` still passes. | 15,290,018 B | −533,744 |
| `ui/picker`: `emoji.tsv` embedded deflated (`emoji.tsv.z`, 36 KB), inflated when the table is first used. `TestEmojiTableIsPacked` keeps it equal to `emoji.tsv`. | **15,141,410 B** | −148,608 |
| **total** | | **−884,496 (−5.5 %)** |
| after merging origin/gnui at 33ab0f9 (new features: foundation pass 3, composer pass 3, calls, viewer) | **15,292,642 B** | +151,232 |

**Projected with `app/fetch`:**

- With the updater's `httpFeed` removed (a stand-in for `app/fetch`), the same tree measures **12,338,882 B**. That removes net/http, http2, crypto/tls, crypto/x509 and math/big's last users: −2.80 MB.
- So with `app/fetch` the binary should be about 12.35–12.4 MB, plus `app/fetch`'s own code; after the closing merge's +151 KB, about 12.5 MB.

**Tried and dropped.**

- Removing the `testing` import from `theme` and `format` (`testing.Testing()`) saves 32 B: the linker drops the package anyway.
- Re-encoding the doodle PNGs losslessly as grey+alpha would save 69 KB, but ImageIO colour-matches grey images. The ink colour could shift, so it is not behaviour-neutral.

**Levers left, and who owns them:**

| Lever | Saving | Owner and what it takes |
|---|---|---|
| The doodles are one alpha mask in two inks (white and 30, 30, 30; the alpha channels are identical) | 174 KB | One mask, tinted where the wallpaper is drawn (`theme.DoodleFor` and its use in `ui/conversation`, which is being edited now) |
| The composer's `emojiTable` (122 KB) overlaps the picker's emoji table | ≈ 100 KB | Share one table (composer, soon being edited) |
| `golang.org/x/image/webp` in `ui/cropper` | 134,640 B (measured) | Decode with the framework's system decoder (ImageIO reads WebP) instead of `image.Decode` |
| `regexp` | ≈ 0.15 MB | Imported by the framework's `app/login` |

### Resident-mode CPU (1 → 30 ms per 10 s)

**This is not a regression in ZapFast's code.** The pre-merge binary (0415dfb), run now, shows the same 16–23 ms, and the framework's pilot shows 39 ms in the same bursts.

What the evidence shows:

- In the measured window there are 0–1 Go mallocs, 0 GCs and 0 MB scavenged, with 5 goroutines and no Go thread running. The `-idle` report now prints Mallocs, GCs, ScavengedMB and Goroutines, and `-idle -cpuprofile` profiles just the window.
- Per-thread CPU, polled every 20–50 ms with `proc_pidinfo` from outside the process, shows the time going to the main thread and `com.apple.NSEventThread` together. They run in bursts of 0.5–1 s, every 1–3 s, and some runs have none.
- `sample` during a burst shows the main thread in `dockClientNotificationProc → DockCallback → GetDockRectAndReason → SLSGetDockRectWithReason`. That is a synchronous WindowServer round trip on every "Dock rect changed" notification, which reaches every app with a Dock tile whenever Dock tiles come and go. On this machine, other agents were launching and quitting test apps; the load average reached 100–250 during this round.

This is the cost of keeping a Dock icon while other apps launch and quit; the HID idle time was 83 min, so nobody was using the machine. On a quiet run the closed window measures 0.4–1.5 ms, as before. The only in-app avoidance is dropping the Dock icon in resident mode (the accessory activation policy), which upstream deliberately does not do (`sys/dock.go`), so nothing was changed.

### The wakeups, attributed

These are interrupt wakeups, demo core, 10 s windows. Window open: 13–18 on quiet runs (bursts make it 27–69). Window closed: 9–12.

| Source | Wakeups per 10 s | Evidence |
|---|---|---|
| the measurement's own goroutine | 1 | it sleeps through the window |
| `sys`'s instance ticker (2 s) | **0 in this measurement**; 5 (0.5/s) in a real run | `sys.Launch` takes the single-instance lock only without `-demo`, so the demo has no ticker. A build with the ticker at 1 h measured the same (11 vs 10 closed, 14.5 vs 16 open). |
| Go runtime (sysmon, netpoller, scavenger, GC) | ≈ 0 | no Go thread ran in a 9 s window; 0 GCs, 0 scavenging |
| the Rust demo core (`zf-demo`) | 0 | it waits on an untimed dispatch semaphore and never ran |
| the status item | ≈ 0 | a build without it: medians 12 open and 10 closed, against 13 and 12 with it |
| Apple frameworks' GCD worker threads (`start_wqthread`), and the main thread 2–3 times | the rest: ≈ 8–10 closed, ≈ 12–14 open | Two or three pool threads run for under 100 µs each, every 1.5–3 s, too briefly for `sample`; once they were seen in CoreAnalytics' XPC reply queue and in Core Animation's backing-store collection. The framework itself schedules nothing (one `dispatch_async_f`, no timers). Pilot, the framework's app, measured 5 per 10 s with its window open by the same counter, so this is mostly the AppKit + Metal baseline. |

WindowServer and Dock notifications cost CPU but are not counted as interrupt wakeups: pilot's 39 ms burst came with 5 wakeups.

## Round 3 (2026-10-21): the allocation gate, the memory step, size

These were measured after merging origin/gnui at 8044123, with the framework at aeaa47a. At the round's final head (after merging origin/gnui at 228dc5e, framework 3510641):

- The release executable is 12,473,650 B.
- `-bench -gate` passes: 0 mallocs in 3,668 steady frames, and 105 in 5,088 action frames.
- The footprint after the full session is 144.9 MB, the same at 2 s.
- Main-thread CPU is 0.47 ms a frame.

### The allocation gate

The coordinator's decision (D27 applied to a session with user actions): a user action that hands work to the OS, such as opening a link or building a notification, may allocate; D27 is about steady frames.

**The rule** (`gnui/cmd/zapfast/gate.go`):

- An **action frame** has an event, or follows a frame that had one. The events are:
  - input, the OS's or the script's: the pointer moving, entering or leaving, a button, the wheel, a key, text, an edit, a command, a paste, focus or visibility;
  - a step of the session that acts through code: typing into the editor, clearing it, asking the demo core for a message or for typing, a reaction arriving;
  - core events drained into the state (`State.Pump` → `Conn.Drain`, counted by the bench's core wrapper).

  The frame after an event is included because what an action hands off (a goroutine, open(1)) may still be running in it.
- Every other frame is **steady**, including the frames of an animation, a hover held still, and the pauses while the session waits for the core.
- **Steady frames must not allocate.** The gate fails when the steady frames allocated in every session, or when a steady frame at the same point of the session allocated in two sessions or more. A runtime one-off (a new thread under load, the pools refilled after the collection before the first session) lands once, at some frame, and passes, as in the framework's own gate.
- **Allocations in action frames are reported, never failed**: frame by frame, with their events and the number of sessions, on stderr and in the JSON (`Gate`). The run's allocation sites (`AllocSites`, the framework's attribution over all the sessions) are listed with them. While the steady frames are clean, every site there belongs to an action frame or to the runtime.

**How it counts.** Each frame's allocations run from the start of its script to the start of the next frame's, read with `runtime.ReadMemStats`. That call is exact, unlike `runtime/metrics`, which counts small objects only when a P's cache is refilled. It stops the world for a few µs a frame, so only the full session counts per frame. The scrolling session keeps its CPU figures clean and is gated on CPU as before.

**Tests of the gate:**

- `cmd/zapfast/gate_test.go`, on the counter with a scripted input sequence:
  - a link click allocating 7 times in the release frame and twice in the next passes;
  - so do core events and script steps that allocate;
  - an allocation in a steady frame of every session fails, and so does one two frames after the click;
  - a one-off in one session passes, but the same frame allocating in two sessions fails.
- Live, the whole bench at 8044123 plus this round:

| Run | Steady frames | Action frames | Verdict |
|---|---|---|---|
| `-bench -gate`, 12 sessions of 730 frames | 3,686 frames, **8 mallocs**, all in the first frame of the first session (the pools refilled after the collection) | 5,076 frames, 102 mallocs: the link click's frame 483, 89 in 12 sessions (7.4 a session); the rest one-offs in wheel, script and core-event frames | **pass** (exit 0) |
| `-bench -gate -gate-test-alloc` (one `new([4]uint64)` in steady frame 3 of each session) | 3,683 frames, 19 mallocs: frame 3 in 12 of 12 sessions | 5,077 frames, 100 mallocs | **fail** (exit 3): "the steady frames allocated in every one of 12 sessions (fewest 1 mallocs); steady frame 3 of the session allocated in 12 sessions (12 mallocs)" |

The link click's sites are `kinds.OpenLink`, `kinds.opener`, `conversation.(*Screen).follow`, `net/url.parse` (three) and a `bytealg.MakeNoZero` string, 7 objects a click.

**A live check with a real window.** No new one is needed:

- The shell's `TestLiveSteadyFramesDoNotAllocate` (`ZAPFAST_LIVE=1`) counts allocations over steady frames of a real window with a fake core.
- The bench gate covers the whole app in a real window, the demo core, every scripted action and the frames around them, frame by frame.

### Memory: the 141 / 157 MB step

**Cause: free Go heap waiting for the scavenger.** It is not Metal, the icon atlas or the image cache. Six scroll-gate runs at 8044123 (`-bench -session scroll -memdetail`):

| Run | Footprint | Go heap in use | Go heap free but kept | IOSurface | Malloc Small | Malloc Large |
|---|---|---|---|---|---|---|
| low (3 runs) | 141.5–145.5 MB | 51.1–51.3 MB | **5.1–5.2 MB** | 49.2 MB | 40.3–41.4 MB | 5.9 MB |
| high (3 runs) | 156.9–157.2 MB | 51.1–51.2 MB | **20.5–20.8 MB** | 49.2 MB | 40.5–41.5 MB | 5.9 MB |

- Every native category is the same in both modes, to 0.1 MB: Metal and Core Animation surfaces, malloc, and the atlases, which live in IOSurface and malloc. The only difference is 15.6 MB of Go heap pages that are free but not yet returned to the OS. The footprint counts them because they are dirty.
- `footprint(1)`, run a moment after the reading, already showed less: the pages were on their way out.

**Where the free heap comes from.** `GODEBUG=gctrace=1` shows the collection before the measured session going 70 → 49 MB: 21 MB of garbage. An allocation profile of the history load (10,000 messages in 40 pages of 250) found that it allocates 24.4 MB, of which 5.0 MB stays. Most of the garbage is two slices growing by `append`, which enlarges a large slice by about a quarter at a time and so leaves about four times its final size behind:

- the conversation's arena (`model.(*Conversation).upsert` → `abi.(*Message).Encode`): 9.9 MB allocated for 2.1 MB kept;
- its record slice: 8.1 MB allocated for 2.0 MB kept.

The bench's own message building adds 3.8 MB.

**Why it is bimodal.** After a collection, the runtime's background scavenger aims to keep free pages up to 10 % of the heap in use, scaled by heap goal / previous heap goal. The history load had just raised the goal from 74 to 99 MB, so after a single collection that ratio is 1.34 and the scavenger keeps the 20 MB. A second collection with the same live heap brings the ratio back to 1, and the scavenger returns the pages, at about 1 % of a CPU. Whether a second collection happened before the reading was timing: the framework's bench collects again at the start of the first window only if the warm-up allocated after the first collection. In four traced runs:

- The run that read 141.9 MB had 4 forced collections.
- The runs that read 152.4, 152.4 and 160.7 MB had 2.
- The two 152 MB runs had had a background collection in between, so their ratio was smaller.

**Fixes:**

1. **The reading** (`cmd/zapfast/bench.go`): two collections, then the footprint is read once the free heap is down to the scavenger's own goal (10 % of the heap in use plus 1 MB; at most 20 s). The old 2 s reading is kept as `FootprintAt2sMB`. Eight interleaved runs read **140.5–142.0 MB**, every one at 2 s.
2. **Less garbage** (`model/arena.go` `reserve`, `model/messages.go`): the arena reserves 4 KB ahead and doubles when it must grow, and the record slice doubles too. Behaviour is the same and allocations stay in action frames (core events).
   - The collection before the session now frees 6–11 MB instead of 21: 59–60 → 49 MB, against 70 → 49 for the same tree without it, in 7 of 7 runs each.
   - The heap's high-water mark is 10 MB lower.
   - With the old single-collection reading, the build with this change read 141.1–143.3 MB in 3 runs, and the build without it 140.9–156.9 MB.

Memory budget: **≈ 141 MB, deterministic; PASS** (budget 150, stretch 120 still missed).

The 141 MB is roughly:

- IOSurface 49 MB: four 2200 × 1440 drawables of 12.3 MiB each (round 4; this said two plus other surfaces);
- malloc 47 MB: AppKit, Core Text, Metal and the demo core;
- Go heap 51 MB in use: the 10k-message transcript and its views, caches, the atlases' CPU copies.

### Size at 8044123

The release executable, built as `gnui package` builds it, is **12,456,162 B**.

| Section | Size |
|---|---|
| `__text` | 5.38 MB |
| `__gopclntab` | 4.66 MB |
| `__go_type` | 1.20 MB |
| `__rodata` | 0.44 MB |
| `__noptrdata` + `__data` | 0.35 MB |
| `__LINKEDIT` | 0.38 MB |

By owner, attributed as in round 2:

| Owner | Size |
|---|---|
| Go standard library and x/ | 4.60 MB (runtime 1.02) |
| ZapFast | 3.73 MB |
| Framework | 3.00 MB |
| Type descriptors | 1.20 MB |
| String literals | 0.34 MB |

Code costs about twice its size: each byte of `__text` brings about 0.9 B of pclntab and funcdata.

The top 15 contributors:

| # | Contributor | Size |
|---|---|---|
| 1 | type descriptors | 1.20 MB |
| 2 | runtime | 1.02 MB |
| 3 | gonativeui/platform | 0.62 MB |
| 4 | zapfast/ui/conversation | 0.46 MB |
| 5 | zapfast/abi | 0.41 MB |
| 6 | gonativeui/rexui | 0.38 MB |
| 7 | slices (sort instantiations) | 0.36 MB |
| 8 | gonativeui/ui | 0.35 MB |
| 9 | string literals | 0.34 MB |
| 10 | encoding/json/v2 | 0.28 MB |
| 11 | net | 0.27 MB |
| 12 | zapfast/ui/chatlist | 0.24 MB |
| 13 | gonativeui/app | 0.23 MB |
| 14 | zapfast/ui/calls | 0.22 MB |
| 15 | zapfast/ui/settings | 0.20 MB |

Next come composer 0.20, dialogs 0.19, reflect 0.19, gonativeui/text 0.19, kinds 0.19, widget/base 0.17, info 0.17 and img 0.17 MB.

The largest embedded data left is the doodle mask (85 KB) and the login mark (28 KB).

**Cuts this round: none.** Nothing behaviour-neutral and cheap was left in the packages open to edits:

- **`-demo-tour` out of release builds** saves 50,688 B (`widgettest` leaves the binary; `testing` is dropped by the linker anyway). Not taken: `-demo-tour` is upstream's demo recorder, a user-visible flag, unlike `-bench`.
- **The sort instantiations**: `slices.SortFunc`/`SortStableFunc` on about 14 struct types, 166 KB of code. Several of these sorts run in frames and must stay allocation-free, so `sort.Slice` (which allocates) is no substitute. Sorting index slices instead would merge them into a few instantiations: about 0.25 MB, across chatlist, composer, info, dialogs, msginfo, model and conversation.

**What 10 MB would take** (−2.46 MB):

| Lever | Saving | Owner and cost |
|---|---|---|
| `regexp` out of the framework: `app/login` (`p6-inputgaps`) **and** `app/winstate` (`nameRE`, `store.go:36`) | 0.17 MB (regexp + syntax) | framework; `p6-inputgaps` alone leaves it linked through winstate |
| WebP through the system decoder (`p6-batch4`) | 0.135 MB (measured) | framework |
| `net` off darwin: `app/instance` uses it for its Unix socket, and `app/shell` links `internal/dbus` on darwin (`showitems.go` and `shell_unix.go` build for `unix`, which includes darwin) | ≈ 0.35 MB (net, dnsmessage, netip, routebsd, singleflight) | framework: raw socket syscalls; dbus behind a linux tag |
| `encoding/json` replaced by small hand-written codecs (settings, prefs, theme presets, instance, slog's handler) | ≈ 0.5 MB (json/v2 0.28, jsontext 0.13, jsonwire, v1; part of reflect) | app and framework; a large change |
| `log/slog` replaced by a small logger | ≈ 0.1 MB | app (core, state, theme, sys, calls, composer, shell) |
| sorting index slices (above) | ≈ 0.25 MB | app, many packages |
| icon registration written as tables instead of unrolled calls (`viewer.Init` 20 KB, `conversation.addIcons` 18 KB, `info.Init` 16 KB, `chatlist.icons.add` 12 KB, `calls.Init` 12 KB of code) | ≈ 0.1–0.15 MB | app; mostly in the packages the video round is editing |
| **all of the above** | **≈ 1.6–1.7 MB → ≈ 10.8 MB** | still ≈ 0.8 MB short |
| **no inlining**: `-gcflags=all=-l` | **−1.99 MB → 10,461,778 B** | see below |
| no inlining in ZapFast's packages only (`-gcflags=zapfast/...=-l`) | −1.20 MB → 11,253,666 B | |
| no inlining in the framework only | −1.11 MB → 11,350,962 B | |

No inlining is the one lever of that size, and it is a build flag: `gnui package` would need a `-gcflags` option, since `buildArgs` passes only `-tags` and `-ldflags`. What it costs:

- **Allocations:** none. The gate passed with 0 steady-frame mallocs over 3,691 steady frames, and scrolling made 0 mallocs.
- **CPU:** frames cost 2–3.5 times as much.
  - Full sessions run back to back at a load of 6–9: main thread 1.34 against 0.69 ms a frame, frame CPU P50 1.10 against 0.32 ms, P99 2.11 against 1.81 ms.
  - An earlier pair that was not run back to back read 2.51 against 0.47 ms.
  - The scrolling session was too noisy on this machine to separate them: 1.2–1.8 ms a frame for both builds, and for the build without inlining in ZapFast's packages only.
  - The budgets would still pass (scrolling < 4 ms), but at two to three times the energy per frame.

Honestly, then, 10 MB needs one of two things. Either no inlining, if the CPU A/B on a quiet machine allows it, plus the framework's in-flight cuts. Or every structural lever above (framework branches, JSON and logging rewrites, the sort and icon refactors) plus about 0.8 MB of code removed. Linker flags are exhausted (`-s -w -trimpath`), and compressing the executable would break code signing and slow the start.

## Round 4 (2026-10-21): the memory creep, attributed and cut

The coordinator read the full session's footprint at 140.9–146 MB on 9433bb7 (round 3 on the trunk), 145–146 at 7ad45c7 and 148–150 at cc713fa, against a 150 MB budget; the target for this round is ≤ 142 MB.

### Method

- The same full session and reading as round 3: `-demo -bench -gate -memdetail`, two collections, the footprint read once the scavenger is done. Two interleaved runs a binary, at a load of 2.8–3.7. Sizes in MiB (footprint(1)'s unit).
- Each tree came out of `git archive` and was built against the same framework checkout (78e7872). 9433bb7 was also built against 3510641, from a `git archive` of the framework with the module's `replace` pointed at it. `bench.go` is the same file in all three trees.
- Categories are footprint(1)'s. The named IOSurfaces come from `footprint -v`, the malloc blocks from heap(1) (started with `MallocStackLogging=1`, heap(1) names the function that made each kind of block), and the Go heap from `runtime.MemStats` and a heap profile (`-memprofile`). `-memdetail` now prints all of them.

### Attribution

| MiB, median of 2 | 9433bb7, framework 3510641 | 9433bb7 | 7ad45c7 | cc713fa |
|---|---|---|---|---|
| **Footprint** | **145.6** | **145.8** | **145.9** | **148.6** |
| Go heap in use (runtime) | 52.3 | 52.3 | 52.4 | 52.5 |
| Go heap free, not returned | 5.3 | 5.3 | 5.2 | 5.3 |
| Untagged (the Go heap's dirty pages) | 33.8 | 33.6 | 33.9 | 34.4 |
| IOSurface | 49.2 | 49.2 | 49.2 | 49.2 |
| IOAccelerator (Metal) | 1.0 | 1.0 | 1.0 | 1.0 |
| Malloc Small | 43.0 | 43.2 | 43.0 | 45.3 |
| Malloc Large | 5.9 | 5.9 | 5.9 | 5.9 |
| Other (Core Animation, CG, the binaries' `__DATA`, malloc tiny, stacks…) | 12.7 | 12.9 | 12.9 | 12.8 |

The steps:

- **Framework 3510641 → 78e7872: +0.2**, every category within 0.2 (noise).
- **9433bb7 → 7ad45c7** (av: inline video, round notes, motion photos): **+0.1**.
- **7ad45c7 → cc713fa: +2.7**:
  - Malloc Small +2.3: two more decoded photos kept in the image cache's CPU tier (heap(1): 1,200 KB blocks, 13 → 15).
  - Untagged +0.5: the Go heap. Its in-use bytes grew 0.2 MiB from 9433bb7 to cc713fa, so the wrapped-time layout cache and the announcement queue are not where the memory went.

What the large categories hold, at cc713fa:

- **IOSurface 49.2 MiB is four 2200 × 1440 BGRA "CAMetalLayer Display Drawable" surfaces** of 12.3 MiB each, and nothing else of any size. There are no backdrop or fade targets and no video or YUV surfaces (the session plays no video). Round 3's note of two drawables plus other surfaces was wrong; see framework item 1.
- **Metal textures are not in `phys_footprint`.** IOAccelerator is 1.0 MiB, while the image cache alone has 28.5 MiB on the GPU. The atlases, the backdrop's targets and the video textures do not count against this budget.
- **Malloc Small 45 MiB.** heap(1) with stack logging shows:
  - 33 blocks, 28.3 MiB, "malloc in gonativeui_ffi_call": the image cache's decoded pixels (`img/pool.go` `allocMem`, C memory on macOS);
  - 8.4 MiB of ColorSync tables (60 × 137 KB, `CMMParaCurveTag::MakeInvertedTRC`/`MakeTRC`);
  - the rest is AppKit, Core Text and the demo core.
- **Go heap in use, 52.5 MiB, over 34.4 MiB dirty.** The text run cache's reserve counts 18.4 MiB in use, mostly untouched pages. The framework bench's allocation-site report keeps 1.8 MiB of strings (`internal/allocsite.classify`) at the reading; that is measurement only.

### The fix: the image cache keeps 8 MiB of decoded pixels, not 32 (`ui/screen/images.go`)

The main window's cache keeps decoded pixels after their upload, up to `CPUBytes`, to upload them again after a GPU eviction. On macOS those pixels are C memory, counted whole in the footprint, while the GPU copy is not counted at all. At 32 MiB the tier held 26.9 MiB after the scrolling session: a second copy of photos the GPU already had.

At 8 MiB, `trimCPU` drops only the copies of images already on the GPU. It keeps pixels waiting for their upload, and the frame an animation in view shows, whatever the budget.

- **No re-decodes.** Scrolling session: 27 decodes, 26 uploads and 0 GPU evictions with either budget; 21 CPU copies dropped at 8 MiB. Full session: 31 decodes and 0 GPU evictions with either budget; 25 CPU copies dropped at 8 MiB. The session shows no animated image.
- **Cost:** an image decodes again only after a GPU eviction (budget 128 MiB; the sessions use 28.5), and an animation out of view drops its frames sooner.
- **Saving**, cc713fa plus this branch, interleaved, load 3–40:
  - full session: 148.6 → 132.9 MiB (2 runs each), and again 148.3 [147.3–149.3] → 134.0 [133.8–134.2]. A 16 MiB budget read 141.2, too close to 142;
  - Malloc Small 45.4 → 30.4; nothing else moves;
  - scrolling: 143.4–144.1 → 128.8–131.9 MiB;
  - steady frames 0–1 mallocs before, 0 after; the gate passes in every run.
- **Scrolling**, at bc53b60, 3 interleaved pairs in each style (`-scrollers`; the report confirms the style drawn), load 4–16:
  - 0 mallocs and 27 decodes in every run, every frame paced.
  - Main thread per frame, before → after: overlay 548 → 455, 1,605 → 1,577, 1,866 → 1,749 µs; legacy 705 → 585, 1,477 → 1,657, 1,272 → 1,590 µs.
  - Summed over the six pairs, +1.9 %. That is inside this machine's noise (the same binary varied threefold between rounds), and every run is under 4 ms.
  - The only main-thread work the fix adds is returning 21 pixel buffers to the pool in a 323-frame session.

### Final reading (bc53b60: trunk 2df1e9b plus this branch)

Full session, `-demo -bench -gate -memdetail`, 2 interleaved runs each at a load of 10–16:

| | before (32 MiB) | after (8 MiB) |
|---|---|---|
| Footprint, MiB | 150.6, 148.4 | **133.1, 132.5** |
| Gate | run 1 **failed**: phys_footprint 151 MB, budget 150 | pass, pass |
| Steady frames, mallocs | 8 in 3,619 (frame 0 after the pre-session collection, in one session: a one-off), 0 in 3,645 | 8 in 3,654 (the same one-off), 0 in 3,654 |
| Action frames, mallocs | 130, 144 | 121, 121 |
| Decodes; GPU evictions | 31; 0 | 31; 0 |

`-gate-test-alloc` still exits 3. `gofmt`, `go vet ./...` (both with and without `gnui_release`) and `go test ./...` pass.

### Framework-side (not changed here)

1. **Four drawables where two would do: 24.6 MiB** at the default window (2 × 12.3), and more in a larger one (`gpu/metal_darwin.go`, `platform/view_darwin.go`).
   - The first frames are drawn through the layer's `nextDrawable` (draw-first startup). Then the `CAMetalDisplayLink` hands out drawables of its own pair, and the layer's pair stays allocated for the window's life.
   - pilot shows the same: 4 drawables, 108.7 MB. pilot with `SetFrameSource` made a no-op (no link): 2 drawables, 79.0 MB.
   - Tried in a scratch copy of the framework: setting `drawableSize` after the link took over frees nothing, and `setMaximumDrawableCount:` throws under a `CAMetalDisplayLink`.
   - Suggested fix: when the link attaches, give it a fresh `CAMetalLayer` set up like the first. Swap it into the view in the transaction of the link's first present, which releases the old layer and its two drawables. A live resize presumably does the same again (the layer's own pool is used during the resize, then the link attaches); not measured.
   - With this, the full session would read about 108 MiB, under the 120 MB stretch.
2. **ColorSync tables, 8.4 MiB** (60 × 137 KB). Not traced to a caller. The decoder draws into an sRGB bitmap context (`img/decode_darwin.go`), which builds a transform for each source profile; the window's colour matching is the other candidate.
3. **Bench only:** the allocation-site report keeps 1.8 MiB of strings in the Go heap when the footprint is read (`app/bench.go`, `internal/allocsite`).

## Idle: steady idle and the drawable trim (2026-10-21, framework 45287b0)

Framework 45287b0 gives back an idle window's spare drawables 5 s after its last frame (`platform/view_darwin.go`, `trimAfterNs`): 4 → 1 drawables of 2200 × 1440, which settles item 1 of "Framework-side" above. That changes two things about the idle measurements.

### The window measures steady idle

The trim costs a few wakeups, once. With the old default (`-settle 3s`, then 10 s measured), it fell inside the window. On 6d27234, three runs each, alternated:
- idle CPU was 4.0–4.5 ms per 10 s on 45287b0, against 2.5–3.5 on 11346d0;
- one run of three failed the budget: 1.30 package-idle wakeups/s (7.5 wakeups/s).

The idle budget is for steady idle (coordinator's decision, 2026-10-21). `-idle` now settles for 8 s by default, and the window starts no sooner than 7 s after the last frame. The report's `QuietS` says how long after.

At b906f76 with that change (`-demo -idle 10s`):

| run | `QuietS` | builds | package-idle wakeups/s | all wakeups/s | CPU | verdict |
|---|---|---|---|---|---|---|
| 1 | 8.0 s | 0 | 0.2 | 1.0 | 0.9 ms | pass |
| 2 | 8.0 s | 0 | 0.3 | 1.1 | 1.1 ms | pass |
| 3 | 8.0 s | 0 | 0.3 | 0.8 | 0.8 ms | pass |
| resident mode (`-idle-closed`) | 16.0 s | 0 | 0.2 | 5.1 | 0.4 ms | pass |
| an app wake every 500 ms in the window (a throwaway build) | 8.0 s | 20 | 46.7 and 54.9 | 157 and 153 | 54–95 ms | **fail** (exit 3), twice |

The last row shows the later window still catches real wakes.

### Read the idle footprint at 60 s or later

The trimmed drawables leave footprint(1)'s IOSurface category at once (37 → 12 MB). But they stay in "Owned physical footprint (unmapped)" (25 MB) for a while, and `phys_footprint` reads the same until they go:
- one launch read 99 MB at 14 s and 30 s, then 75 MB from 32 s;
- of the three runs above, read at 18 s, two had already dropped (74.3, 76.8 MiB) and one had not (99.3).

So the idle footprint is read 60 s or more after launch: `-idle 10s -settle 50s`. On 6d27234, three runs each, alternated:

| idle footprint | 11346d0 | 45287b0 |
|---|---|---|
| read at 60 s (`-idle 10s -settle 50s`) | 99.6 MiB | **75.3 MiB** |
| read at 13 s (`-idle 10s -settle 3s`, the old default) | 99.6 MiB | 99.8 MiB |

At 60 s, wakeups and CPU were the same on both (0.3–0.4 ms per 10 s), and every run passed.

## Frame-0 allocation boundary (2026-10-22)

Frame-0 setup allocations came from the gate taking its baseline before the framework's first-window GC/profile setup; the baseline now starts at the first frame, before its script actions.
Scavenger timer-heap growth inside a frame is runtime-internal, classified by its origin, and still counted by the gate.

## Re-running

Final-pair update (2026-10-23): [verified packages and current budget status](final-delivery-2026-10-23.md)
records source `b8789025` / framework `693d3b38`, the release executable's
12,180,960 bytes and the separate MacBook measurements: idle/memory/vlist pass,
corrected fresh-child startup 10/10 pass, Pilot pass, and a ZapFast full-script
execution failure with no allocation report. Original contaminated startup
samples remain invalid evidence. Results are not inferred from package verification.
The `/tmp` binary paths in the historical examples below should be replaced
with an approved SSD build directory; do not rebuild GnuiShot.

```sh
cd gnui
export CGO_ENABLED=0 ZAPFAST_CORE=/path/to/libzapfast_core.dylib
go build -o /tmp/zapfast ./cmd/zapfast
caffeinate -d /tmp/zapfast -demo -exit-first            # startup report (JSON); -startup waits for the chat list
caffeinate -d /tmp/zapfast -demo -idle 10s               # steady idle, window open (8 s settle, 7 s past the last frame); add -idle-closed for resident mode, -cpuprofile f to profile the window
caffeinate -d /tmp/zapfast -demo -idle 10s -settle 50s   # the idle footprint, read at 60 s (freed drawables count until about 30 s after launch)
caffeinate -d /tmp/zapfast -demo -bench -gate -timeout 300s                  # the full session, with the allocation gate
caffeinate -d /tmp/zapfast -demo -bench -gate -gate-test-alloc -timeout 300s # the gate's own test: must exit 3
caffeinate -d /tmp/zapfast -demo -bench -gate -session scroll                # scrolling only; -scrollers overlay|legacy forces the scroll bars' style
caffeinate -d /tmp/zapfast -demo -bench -gate -memdetail -memprofile heap.prof  # footprint(1)'s categories, the named IOSurfaces, heap(1), malloc zones, a Go heap profile
MallocStackLogging=1 caffeinate -d /tmp/zapfast -demo -bench -memdetail          # heap(1) then names the function that made each kind of malloc block
caffeinate -d /tmp/zapfast -demo -bench -allocsites -timeout 300s 2>sites.txt  # allocation stacks
go build -trimpath -ldflags="-s -w" -tags gnui_release -o /tmp/zapfast-release ./cmd/zapfast   # size, as gnui package builds it
# every measurement mode (-exit-first, -startup, -idle, -bench, -allocsites, -cpuprofile) is development-only: gnui_release builds have none (z4-lean)
```
