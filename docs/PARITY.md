# ZapFast parity checklist

> Since WP9 (2026-10-28) Zap's backend is Go (gocore) and the Rust core is
> gone from the repository. Rows whose evidence ran on the Rust core keep it
> as recorded; the Go demo answers as zfcore's did
> (`gnui/gocore/demo/testdata`).

What the Go UI (`gnui/`) must do to match upstream ZapFast's egui client, on macOS. It follows `docs/GNUI-MIGRATION-PLAN.md`, "The parity checklist", and was built on 2026-10-20 from upstream as merged into `gnui` at `ba4a1d9`. This file holds the status of record for every row; the evidence for each is in the package doc named in its Evidence column (`docs/parity/<package>.md`). Rows are re-triaged at every upstream merge (the plan's UI drift alarm): new rows get the next free id in their area, changed rows are edited in place, and a removed behaviour keeps its row with the status `removed`.

**Done** means the row's verification passes, or Adam waives the row in writing (exit criterion 1 of checkpoint 2).

## Summary

Status of record as of 2026-10-23, gnui `56f745c6`. Each row's status comes from the package docs in `docs/parity/` (the evidence files); the Evidence column of every row points at them. Where two package docs give one id different statuses, the lowest is kept (see "Cross-checks" below).

| Area | Rows | done | partial | hand | todo | waived | removed |
|---|---|---|---|---|---|---|---|
| PR: Product-wide | 8 | 6 | 0 | 2 | 0 | 0 | 0 |
| LG: Linking and history | 15 | 14 | 0 | 1 | 0 | 0 | 0 |
| AC: Accounts | 10 | 9 | 0 | 1 | 0 | 0 | 0 |
| SH: Shell, window and rail | 20 | 20 | 0 | 0 | 0 | 0 | 0 |
| CL: Chat list | 48 | 48 | 0 | 0 | 0 | 0 | 0 |
| LB: Labels | 8 | 8 | 0 | 0 | 0 | 0 | 0 |
| LK: Locked chats | 9 | 8 | 0 | 0 | 0 | 1 | 0 |
| CV: Conversation header and transcript | 36 | 33 | 0 | 3 | 0 | 0 | 0 |
| MK: Message kinds | 33 | 32 | 1 | 0 | 0 | 0 | 0 |
| MM: Message menu and actions | 23 | 18 | 0 | 5 | 0 | 0 | 0 |
| SL: Selecting and copying | 12 | 11 | 0 | 1 | 0 | 0 | 0 |
| CP: Composer | 26 | 23 | 0 | 3 | 0 | 0 | 0 |
| PK: Picker (emoji, GIFs, stickers) | 18 | 18 | 0 | 0 | 0 | 0 | 0 |
| SM: Sticker maker | 6 | 5 | 0 | 0 | 0 | 1 | 0 |
| CR: Cropper | 8 | 8 | 0 | 0 | 0 | 0 | 0 |
| VW: Media viewer | 14 | 14 | 0 | 0 | 0 | 0 | 0 |
| PL: Polls | 8 | 7 | 0 | 1 | 0 | 0 | 0 |
| IA: Interactive messages | 13 | 11 | 0 | 2 | 0 | 0 | 0 |
| LO: Locations | 5 | 5 | 0 | 0 | 0 | 0 | 0 |
| DL: Dialogs | 17 | 17 | 0 | 0 | 0 | 0 | 0 |
| IN: Info panel | 15 | 15 | 0 | 0 | 0 | 0 | 0 |
| MI: Message info | 5 | 5 | 0 | 0 | 0 | 0 | 0 |
| CS: Chat search pane | 7 | 6 | 0 | 1 | 0 | 0 | 0 |
| VO: Voice and video playback | 5 | 4 | 0 | 0 | 0 | 1 | 0 |
| CA: Calls | 30 | 23 | 0 | 7 | 0 | 0 | 0 |
| SE: Settings | 33 | 30 | 0 | 3 | 0 | 0 | 0 |
| AL: App lock | 12 | 12 | 0 | 0 | 0 | 0 | 0 |
| UP: Updates | 6 | 6 | 0 | 0 | 0 | 0 | 0 |
| FL: Files, archive and log | 6 | 5 | 0 | 1 | 0 | 0 | 0 |
| TH: Themes | 8 | 8 | 0 | 0 | 0 | 0 | 0 |
| NT: Notifications, Dock and status item | 15 | 6 | 0 | 9 | 0 | 0 | 0 |
| LN: Links | 7 | 5 | 0 | 2 | 0 | 0 | 0 |
| AX: Accessibility | 4 | 3 | 0 | 1 | 0 | 0 | 0 |
| I18N: Languages | 5 | 5 | 0 | 0 | 0 | 0 | 0 |
| KB: Keyboard shortcuts | 33 | 31 | 0 | 2 | 0 | 0 | 0 |
| MB: macOS menu bar | 29 | 14 | 0 | 15 | 0 | 0 | 0 |
| DM: Demo pages and tours | 8 | 5 | 3 | 0 | 0 | 0 | 0 |
| **Total** | **565** | **498** | **4** | **60** | **0** | **3** | **0** |

### Cross-checks (2026-10-23)

- **Rows no package doc mentions** (kept `todo`): none.
- **Ids in a package doc that this file lacks:** none.
- **Ids with different statuses in two package docs:** none.
- **Waived rows without Adam's approval:** none. The waived rows are LK-002, SM-006, VO-005. The fourth approved waiver, "pause other media", is not a row: it is listed under "Not parity items" below, and no package doc marks a row for it.
- **Hand rows whose HANDTEST id does not exist:** none.

## Legend

| Column | Meaning |
|---|---|
| **ID** | Area prefix and number. Ids are never reused. |
| **Behaviour** | One user-visible behaviour, in one line. |
| **Source** | Where upstream defines it. Docs by short name and line: `guide` = `docs/_guide/using-zapfast.md`, `start` = `docs/_guide/getting-started.md`, `what` = `docs/_guide/what-is-zapfast.md`, `files` = `docs/_reference/settings-and-files.md`, `themes` = `docs/_reference/themes.md`, `links` = `docs/_reference/how-it-links.md`, `demo` = `DEMO.md`, `product` = `PRODUCT.md`, `readme` = `README.md`, `agents` = `AGENTS.md`, `plan` = `docs/GNUI-MIGRATION-PLAN.md`. Code paths are under upstream's `src/` (`ui/chats.rs:1989` is `src/ui/chats.rs`, line 1989). |
| **macOS** | Only where the Mac app differs from the docs' wording or the egui build: ⌘ for Ctrl, ⌥ for Alt, the native menu bar, the status item instead of a tray, Settings, the viewer and calls in their own windows. Blank means no difference. |
| **Owner** | The Go package under `gnui/` that owns the behaviour. |
| **Verify** | `test`: an automated behaviour test (unit test on the fake core, or a `widgettest` script on the demo core, by role and name). `capture`: an offscreen render or GnuiShot of a demo page, compared for content (not pixels) with upstream's `--demo-shot` of the same page. `hand`: checked by hand at checkpoint 2. A name in parentheses is the `--demo-page` that shows the state. |
| **Status** | `todo`, `partial`, `hand`, `done`, `waived` or `removed`; see "Status words" below. |
| **Evidence** | Where the status comes from: the package doc in `docs/parity/` that checked the row (with its status when two docs differ), and for a row with a hand check the group and id in `docs/HANDTEST.md`. The doc holds the tests, captures and notes; this file does not copy them. |

**Conventions.**
- Every `Ctrl+…` in a behaviour means `⌘…` on macOS and every `Alt+…` means `⌥…` (upstream's `ui/keys.rs:666-675` relabels them); the macOS column repeats this only on keyboard rows.
- "The tray" in upstream's docs is the menu-bar status item on macOS.
- Features the guide marks "Development builds" (delete-for-me sync, stars, message pins, Keep chats archived, the group description, the remembered window frame, message times on click) are on upstream main and are parity items.
- Confirmations that upstream draws as dialogs may be `NSAlert`s in the Go app (plan Z3a); the row still holds.

**Status words.**
- `todo`: not started, or no package doc mentions the row.
- `partial`: part of the row is built; the package doc's note says what is missing.
- `hand`: built, and the automated checks that exist pass, but the part only a real screen, speaker or OS shows is checked by hand (`docs/HANDTEST.md`). Not `done` until Adam ticks the check.
- `done`: built, and the row's verification passes.
- `waived`: Adam waived the row in writing (VO-005, LK-002, SM-006).
- `removed`: upstream no longer has the behaviour; the row stays so its id is never reused.

## Not done

64 rows are neither `done`, `waived` nor `removed`: 4 `partial`, 60 `hand`, 0 `todo`. Each reason is the start of the package doc's note (shortened where long); the package doc has the rest. A `hand` row waits for its check in `docs/HANDTEST.md`.

### PR: Product-wide (2)

- **PR-004** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [PR-004]): Shortcuts: TestTheShellsShortcuts. Zoom: SH-013.
- **PR-006** (hand, `sys`): Hand check pending (HANDTEST §16 [PR-006]).

### LG: Linking and history (1)

- **LG-011** (hand, `logic`): Hand check pending (HANDTEST §10 [LG-011]): The core's archive.

### AC: Accounts (1)

- **AC-008** (hand, `logic`): Hand check pending (HANDTEST §14 [AC-008]): Automated coverage proves account-local names, notification target and click routing with a fake desktop center. The real notification banner and click still require the existing two-number hand check; not performed here.

### CV: Conversation header and transcript (3)

- **CV-009** (hand, `ui/conversation`): Hand check pending (HANDTEST §4 [CV-009]): The VList sticks to the bottom only while you are at the bottom.
- **CV-020** (hand, `ui/conversation`): Hand check pending (HANDTEST §4 [CV-020]): The footer shows the star, "edited", the pin, the time and our ticks. As upstream's rich_body, it sits beside the text's last line when it fits there, wrapped or not, at the bubble's right; else it gets a row of its own. …
- **CV-034** (hand, `ui/conversation`): Hand check pending (HANDTEST §12 [CV-034]): The framework's bidi text.

### MK: Message kinds (1)

- **MK-033** (partial, `logic`): The two specific Rust deadline tests were executed successfully on 2026-10-22 using the existing SSD artifact. They prove the timeout helper and size formula, not the real network/re-upload flow. …

### MM: Message menu and actions (5)

- **MM-011** (hand, `logic`): Hand check pending (HANDTEST §12 [MM-011]): The core's.
- **MM-014** (hand, `ui/conversation`): Hand check pending (HANDTEST §4, §12 [MM-014]): `copyMessageText`.
- **MM-016** (hand, `ui/conversation`): Hand check pending (HANDTEST §12 [MM-016]): Download, or a disabled "Downloading…".
- **MM-021** (hand, `ui/conversation`): Hand check pending (HANDTEST §4 [MM-021]).
- **MM-023** (hand, `ui/conversation`): Hand check pending (HANDTEST §12 [MM-023]): Reply, Star and Pin are left out for a revoked message.

### SL: Selecting and copying (1)

- **SL-012** (hand, `ui/conversation`): Hand check pending (HANDTEST §9 [SL-012]): Its name is "Select message from {sender}, {time}: {summary}".

### CP: Composer (3)

- **CP-009** (hand, `ui/composer`): Hand check pending (HANDTEST §5 [CP-009]): No widgettest step exists for IME preedit (framework gap).
- **CP-013** (hand, `ui/composer`): Hand check pending (HANDTEST §12 [CP-013]).
- **CP-023** (hand, `ui/composer`): Hand check pending (HANDTEST §13 [CP-023]): The microphone can't be granted to a test on this Mac. The core makes quiet recordings louder. …

### PL: Polls (1)

- **PL-008** (hand, `logic`): Hand check pending (HANDTEST §12 [PL-008]): The core's.

### IA: Interactive messages (2)

- **IA-008** (hand, `ui/conversation`): Hand check pending (HANDTEST §12 [IA-008]): The answer shows as an ordinary reply with its quote (transcript).
- **IA-009** (hand, `ui/conversation`): Hand check pending (HANDTEST §12 [IA-009]).

### CS: Chat search pane (1)

- **CS-002** (hand, `ui/conversation`): Hand check pending (HANDTEST §4 [CS-002]): Shows the time, the line with context and the sender in groups, with the match in bold. The bold is checked only by hand.

### CA: Calls (7)

- **CA-001** (hand, `ui/calls`): Hand check pending (HANDTEST §7, §15 [CA-001]): The core plays no tones, so `ui/calls` synthesises upstream's ringback (`call_tones.rs`: 425 Hz, 1 s on and 4 s off, level 0.18, 20 ms raised-cosine edges) and plays it with `media.Player`, as `sys` does the ringtone. The demo never plays it. …
- **CA-004** (hand, `sys`): Hand check pending (HANDTEST §7, §15 [CA-004]): Rechecked on z7-calls: the missing-window note was stale. Calls already uses `OpenWindowAlone`; the fake-core native test proves resident opening, immediate decline closure and missed-call farewell closure with no main window. …
- **CA-009** (hand, `ui/calls`): Hand check pending (HANDTEST §7, §15 [CA-009]): Native full screen is `app.Win.ToggleFullscreen`, which an offscreen window does not have.
- **CA-014** (hand, `ui/calls`): Hand check pending (HANDTEST §7, §15 [CA-014]): Picking only sends SetCallMicrophone/Speaker/CameraDevice with the device id; the pickers are the framework's pop-up menus (native), nothing is drawn by this package.
- **CA-017** (hand, `ui/calls`): Hand check pending (HANDTEST §7, §15 [CA-017]): Tiles, names, pictures, cameras (cropped to the tile from the middle), the name plate, the muted badge, "Invited" and the grid are done and tested on the fake core and the demo core. …
- **CA-029** (hand, `sys`): Hand check pending (HANDTEST §15 [CA-029]).
- **CA-030** (hand, `sys`): Hand check pending (HANDTEST §15 [CA-030]): Locked app: the click waits for the unlock through the lock's `Defer`.

### SE: Settings (3)

- **SE-032** (hand, `ui/settings`): Hand check pending (HANDTEST §8 [SE-032]): The Play preview shells out to `afplay` with Glass and Ping for Pidgin's two sounds (not shipped, GPL-2.0). `media.Player` takes PCM only, with no file decoding (aiff, mp3, m4a), so it cannot play the system sounds or a chosen file. …
- **SE-051** (hand, `ui/settings`): Hand check pending (HANDTEST §16 [SE-051]): `cmd/zapfast` wires `settings.LoginItem = sys.LoginItem`. …
- **SE-072** (hand, `logic`): Hand check pending (HANDTEST §12 [SE-072]): The core names the files.

### FL: Files, archive and log (1)

- **FL-003** (hand, `sys`): Hand check pending (HANDTEST §10, §16 [FL-003]): The core owns the key; `sys` has no code for it. Nothing in `sys` can write a plaintext archive.

### NT: Notifications, Dock and status item (9)

- **NT-001** (hand, `sys`): Hand check pending (HANDTEST §14 [NT-001]): `sys` shows what `logic` queues (title, body, picture, sound). Permission grants and attachment retries re-check withdrawal and current privacy/account state before posting (2026-10-22, fake center only).
- **NT-003** (hand, `sys`): Hand check pending (HANDTEST §14 [NT-003]): `OpenMessageIn` + `RaiseMain`. macOS carries the message in the banner itself, so the click works for every banner, not only upstream's 32 most recent.
- **NT-008** (hand, `sys`): Hand check pending (HANDTEST §14 [NT-008]): Counts unread chats that are not archived, locked or muted, across accounts.
- **NT-009** (hand, `sys`): Hand check pending (HANDTEST §14 [NT-009]).
- **NT-011** (hand, `sys`): Hand check pending (HANDTEST §16 [NT-011]): The request reaches the first copy as an app post from `instance.Options.Notify`, window or not (`TestARealSecondProcessReachesTheRunningCopyWithoutAWindow`: a real second process).
- **NT-012** (hand, `sys`): Hand check pending (HANDTEST §16 [NT-012]): The Dock menu's Quit and ⌘Q are the system's and the shell's.
- **NT-013** (hand, `sys`): Hand check pending (HANDTEST §16 [NT-013]).
- **NT-014** (hand, `sys`): Hand check pending (HANDTEST §16 [NT-014]): Wake sends the core `Resumed`; sleep saves (`Hooks.Save`) then tells the lock (`Hooks.Sleep`).
- **NT-015** (hand, `sys`): Hand check pending (HANDTEST §14 [NT-015]): A Go-app addition; hidden banners (lock or content off) offer no Reply.

### LN: Links (2)

- **LN-005** (hand, `sys`): Hand check pending (HANDTEST §16 [LN-005]).
- **LN-007** (hand, `sys`): Hand check pending (HANDTEST §16 [LN-007]): The CLI is the bundle's own executable (`Contents/MacOS/zapfast`).

### AX: Accessibility (1)

- **AX-003** (hand, `ui/conversation`): Hand check pending (HANDTEST §9 [AX-003]): Announcement implementation is complete: State.Announce(AnnounceMessage) says “Ada Lovelace: hello” politely; initial history and messages received while locked stay silent. …

### KB: Keyboard shortcuts (2)

- **KB-028** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [KB-028]): The offscreen rig cannot close a window. Retiring is EvWindowClosed.
- **KB-029** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [KB-029]): Quit hooks: state TestChatCloseAndQuitHooks.

### MB: macOS menu bar (15)

- **MB-003** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [MB-003]): A system role; its title follows the interface language (`ui/shell TestMenuBarFollowsTheLanguage`).
- **MB-004** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [MB-004]): A system role; its title follows the interface language.
- **MB-005** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [MB-005]): A system role; its title follows the interface language.
- **MB-006** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [MB-006]): A system role; its title follows the interface language.
- **MB-007** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [MB-007]).
- **MB-009** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [MB-009]): See KB-028.
- **MB-010** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [MB-010]): A framework role, sent to the focused field.
- **MB-011** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [MB-011]): A framework role.
- **MB-012** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [MB-012]): A framework role.
- **MB-015** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [MB-015]): A framework role.
- **MB-022** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [MB-022]): A system role.
- **MB-023** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [MB-023]): A system role.
- **MB-025** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [MB-025]): A system role.
- **MB-028** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [MB-028]): A system role.
- **MB-029** (hand, `ui/shell`): Hand check pending (HANDTEST §2 [MB-029]): Upstream `src/macos.rs:240-263` queues `ShowWindow` before a hidden-window `FocusSearch` action (`action("search")`). The missing closed-window Find case is fixed in `App.Event`: retain `Req.FocusSearch` and open the main window. …

### DM: Demo pages and tours (3)

- **DM-002** (partial, `ui/shell`): About 90 of upstream's ~150 parts (`demoParts` in demopage.go). …
- **DM-004** (partial, `ui/shell`): A widgettest.Player drives the live window through the real handlers (app.RunBench's loop, nothing measured). The opening shot is set while it waits and settles before the first frame. Space starts and replays it, and it holds its last frame. …
- **DM-005** (partial, `ui/shell`): `-demo-tour-script whats-new`, about 53 s on the demo core: the plus menu and the poll dialog, the chat search pane and its date calendar, the sticker shelves (Recent, Favorites, Received) and their search, a group message's info (read and delivered), the …

## PR: Product-wide

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| PR-001 | The conversation is the main workspace, with the chats beside it and the composer below it, always immediately available. | product:7-8, :19 | | ui/shell | capture (chat) | done | `parity/shell.md` |
| PR-002 | Working and unavailable actions look different; only unsupported actions, or replies blocked by the connection, chat permissions or an in-flight send, are disabled. | product:21-26 | | ui/conversation | test | done | `parity/conversation.md` |
| PR-003 | Every screen stays usable in a narrow window (the list and the chat take turns below the breakpoint). | product:27; ui/mod.rs:295-300 | | ui/shell | capture (short-chat) | done | `parity/shell.md` |
| PR-004 | Keyboard operation, zoom, text selection, and light and dark themes work on every screen. | product:36 | | ui/shell | hand | hand | `parity/shell.md`; HANDTEST §2 [PR-004] |
| PR-005 | No decorative dashboards and no extra nested cards inside message bubbles. | product:35 | | ui/conversation | capture (bubbles) | done | `parity/conversation.md` |
| PR-006 | The app contacts only WhatsApp, GIPHY (GIF search), Signal's sticker CDN (imports) and the update feed; no telemetry, no hosted backend, no ZapFast accounts. | product:32; links:30-37; agents:9-11 | | sys | hand | hand | `parity/sys.md`; HANDTEST §16 [PR-006] |
| PR-007 | Address-book names take priority over public profile names in chats, mentions, replies and notifications; public names show with a leading `~`. | what:45-46; files:238-239; app.rs:2441-2471 | | logic | test | done | `parity/logic.md` |
| PR-008 | Sender pictures and names appear on incoming messages in groups only. | files:238; ui/conversation.rs:2386-2388 | | ui/conversation | capture (bubbles) | done | `parity/conversation.md` |

## LG: Linking and history

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| LG-001 | An unlinked account shows the linking screen: a QR code (drawn by Go from the core's payload) and three steps (open WhatsApp; Menu or Settings, Linked devices; Link a device and point the phone at the code). | readme:73-76; start:60-64; ui/login.rs:183-200; plan seam `qr.rs` | | ui/login | capture (login) | done | `parity/login.md` |
| LG-002 | The QR code is replaced as the core sends new payloads; "Requesting a new code…" shows while one is awaited. | ui/login.rs:96 | | ui/login | test | done | `parity/login.md` |
| LG-003 | "Link with phone number instead" asks for the number with its country code (no plus or leading zero), validates it, and offers "Get a code". | start:65-66; ui/login.rs:217; ui/dialogs.rs:1524-1588 | | ui/login | capture (phone) | done | `parity/login.md` |
| LG-004 | Phone linking says "Requesting a code for +N…", then "Waiting for a code from WhatsApp…", then shows the eight-character code with its steps and "Copy code". | ui/login.rs:146-277 | | ui/login | capture (pair) | done | `parity/login.md` |
| LG-005 | A failed code request offers "Try again". | ui/login.rs:114, :284 | | ui/login | test | done | `parity/login.md` |
| LG-006 | Linking states read "Connecting to WhatsApp…" and "Linked. Waiting for your chats…". | ui/login.rs:82-85 | | ui/login | test | done | `parity/login.md` |
| LG-007 | When the phone unlinks this computer, the screen says so and requests a new code. | ui/login.rs:91; app.rs:4182 | | ui/login | test | done | `parity/login.md` |
| LG-008 | The linking card carries the unofficial-client warning. | ui/login.rs:159 | | ui/login | capture (login) | done | `parity/login.md` |
| LG-009 | A locked or unavailable keyring shows its error and "Try again"; the archive stays intact and nothing connects until it opens. | files:69-71; ui/login.rs:283-304 | The Keychain; a new identity may prompt once per item (plan risk 3) | ui/login | capture (keyring) | done | `parity/login.md`; HANDTEST §10 [LG-009] |
| LG-010 | A missing archive key offers "Start over…": a confirmation, then the archive is renamed `archive-unreadable-<date>.db`, the session forgotten, and the linking screen shown. | files:73-80; ui/login.rs:125; ui/dialogs.rs:1255-1266 | | ui/login | test | done | `parity/login.md`; HANDTEST §10 [LG-010] |
| LG-011 | The link survives restarts, and stored messages can be read with the phone offline. | start:68-69; links:27-28 | | logic | hand | hand | `parity/logic.md`; HANDTEST §10 [LG-011] |
| LG-012 | After linking, recent chats arrive within seconds; a banner reads "Loading chat history… N%" while history syncs, then a "History loaded" toast. | start:81-82; ui/mod.rs:511-520; app.rs:3705-3710 | | ui/shell | capture (syncing) | done | `parity/shell.md` |
| LG-013 | Scrolling past stored history asks the phone for older messages, showing "Loading older messages from your phone…"; the phone must be online. | start:83-84; links:22-26; ui/conversation.rs:3408-3424, :3494-3531 | | ui/conversation | test | done | `parity/conversation.md` |
| LG-014 | The first frame shows the cached chat list; skeleton rows and a status line appear only when the archive is slow (or a migration is pending). | plan "Startup" | | ui/shell | test | done | `parity/shell.md` |
| LG-015 | The linking and lock screens have a draggable strip at the top. | ui/mod.rs:746-775 | Room for the traffic lights | ui/login | capture (login) | done | `parity/login.md`; HANDTEST §10 [LG-015] |

## AC: Accounts

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| AC-001 | Your picture at the bottom of the rail opens the account switcher; a dot on it means another number has unread chats. | start:71-75; ui/accounts.rs:21-57 | | ui/accounts | capture (account-menu) | done | `parity/accounts.md` |
| AC-002 | Each switcher row shows the picture, name, number and unread chat count, with a check on the account on screen; an unlinked one reads "Not linked". | ui/accounts.rs:58-232 | | ui/accounts | capture (accounts) | done | `parity/accounts.md` |
| AC-003 | Choosing a row switches to that number: its chats, settings, wallpaper and last chat. | start:71-77; app.rs:1608-1642 | | ui/accounts | test | done | `parity/accounts.md` |
| AC-004 | "Add account" (switcher, or Settings > Account) starts linking another number; the linking screen offers Cancel back to the previous account. | start:73-74; ui/accounts.rs:123, :256-270; app.rs:1698 | | ui/accounts | test | done | `parity/accounts.md` |
| AC-005 | A number the phone unlinked can be removed from its linking screen ("Remove this account"). | ui/accounts.rs:256-270 | | ui/accounts | test | done | `parity/accounts.md`; HANDTEST §16 [AC-005] |
| AC-006 | With several accounts, the linking screen has the switcher in its top-left corner. | ui/accounts.rs:303-305 | | ui/accounts | capture (accounts-closed) | done | `parity/accounts.md` |
| AC-007 | Switching parks each chat's draft, mentions, staged attachments and reply and restores them on return; an open forward selection is discarded. | guide:46, :218-220; app.rs:1559-1606 | | logic | test | done | `parity/logic.md` |
| AC-008 | Events for an account not on screen update only that account; its notifications still show under its own names and switch to it when clicked. | plan "Accounts"; guide:502-503; app.rs:1969-1997 | | logic | test | hand | `parity/logic.md`; HANDTEST §14 [AC-008] |
| AC-009 | "Remove this account" confirms ("unlinks the number on this computer and deletes its local chats"), and a failed file cleanup says so. | ui/dialogs.rs:1410-1415; app.rs:1743-1805 | | ui/dialogs | test | done | `parity/accounts.md`; `parity/dialogs.md` |
| AC-010 | Each number keeps its own keys, archive, media, notification, receipt, typing, download, background-history, wallpaper and chip or label settings. | start:76-77; files:19 | | logic | test | done | `parity/logic.md` |

## SH: Shell, window and rail

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| SH-001 | The main window holds the rail, the chat list and the conversation (info panel, message info and the search pane stay in it). | plan "Windows" | Settings, the media viewer and calls open their own windows | ui/shell | capture (chat) | done | `parity/shell.md` |
| SH-002 | The rail's top holds Chats, Calls and Starred, the one on screen lit, each with its name and shortcut as a tooltip. | guide:724-727; ui/rail.rs:274-308 | | ui/shell | capture (rail) | done | `parity/shell.md` |
| SH-003 | The rail's bottom holds Archived and Locked (only while there are archived or locked chats), Settings, and your picture. | guide:727-728; ui/rail.rs:296-331 | | ui/shell | capture (rail) | done | `parity/shell.md` |
| SH-004 | Archived in the rail shows how many archived chats are unread. | guide:600-601; app.rs:2900 | | ui/shell | capture (archived) | done | `parity/shell.md` |
| SH-005 | In a short window the rail scrolls; Tab and Shift+Tab bring the focused button into view. | guide:733-734; ui/rail.rs:32-33 | | ui/shell | test | done | `parity/shell.md` |
| SH-006 | Hiding the chat list (header button or Ctrl+B) fills the rail with the chats' avatars (unread badges, hover names, click opens); the button under Starred brings the list back. | guide:729-732; files:239-240; ui/chats.rs:1734-1981; ui/rail.rs:251-263 | View > Toggle Sidebar | ui/shell | capture (nosidebar) | done | `parity/shell.md`; HANDTEST §2 [SH-006] |
| SH-007 | A narrow window shows the list or what was opened from it, sliding between them (the list dimmed beneath); presses are ignored mid-slide. | ui/mod.rs:195-300; ui/chats.rs:41-42 | | ui/shell | test | done | `parity/shell.md`; HANDTEST §2 [SH-007] |
| SH-008 | With no chat open, the conversation shows the logo, "Select a chat on the left." (or "Your chats appear on the left as they load.") and the shortcut hint line. | ui/conversation.rs:274-315 | Hint reads ⌘K | ui/conversation | capture (empty) | done | `parity/conversation.md` |
| SH-009 | The connection banner reads "Connecting to WhatsApp…", "Offline (reason). Reconnecting…" with Retry, a failure with Retry, or "Not linked to a phone". | ui/mod.rs:502-611 | | ui/shell | capture (offline) | done | `parity/shell.md` |
| SH-010 | Info toasts sit at the bottom right above the composer and fade after 3.2 s; error toasts stay, with Dismiss and "Copy this message". | ui/mod.rs:618-744; app.rs:163, :8321-8379 | | ui/shell | capture (toasts) | done | `parity/shell.md` |
| SH-011 | Dragging files over the window shows "Drop to send to {name}"; dropping stages them in the open chat. | guide:199; ui/mod.rs:468-500; app.rs:9212 | | ui/shell | hand | done | `parity/shell.md` |
| SH-012 | The mouse's Back and Forward side buttons step through visited places (chats, the list, Settings, the wallpaper picker); they do nothing while an overlay is open. | guide:570-574; ui/mod.rs:333-338; app.rs:5103-5150 | | ui/shell | test | done | `parity/shell.md`; HANDTEST §2 [SH-012] |
| SH-013 | Zoom scales the whole interface; the window opens at the same size whatever the zoom. | files:116, :229-232 | | ui/shell | test | done | `parity/shell.md` |
| SH-014 | The window's size, position and maximized state are remembered, including when it reopens from the status item or a notification. | files:229-234; settings.rs:541-552 | | ui/shell | hand | done | `parity/shell.md`; HANDTEST §16 [SH-014] |
| SH-015 | The chat list, search pane and info panel widths are remembered. | files:226-227; settings.rs:448-452 | | ui/shell | test | done | `parity/shell.md` |
| SH-016 | Tab shows a focus ring (a pointer press hides it) and follows a deliberate order: composer, send, attach, emoji, phone links, the search pane, the info panel, the rail, the list header, the chips. | ui/mod.rs:381-417; ui/focus.rs:1-60 | | ui/shell | test | done | `parity/shell.md`; HANDTEST §2 [SH-016] |
| SH-017 | Escape cancels the topmost state, in upstream's order: update, dialog, recording, picker, selection, Starred, day filter, chat search, suggestions, staged files, edit, reply, call full screen, call screen, group rename, contact edit, info media then info, wallpaper, settings search, new call, narrow call chat, Settings or Calls, list search, chat search, the chat, the locked folder. | keys.rs:164-247 | | ui/shell | test | done | `parity/shell.md` |
| SH-018 | The last open chat of each account reopens at start. | files:19; settings.rs:473 | | ui/shell | test | done | `parity/shell.md` |
| SH-019 | The traffic lights overhang the rail, and headers beside it leave them room (none in full screen). | plan Z2; ui/rail.rs:37-54 | macOS only | ui/shell | capture (chat) | done | `parity/shell.md`; HANDTEST §2 [SH-019] |
| SH-020 | A record too large for the ABI shows as a "could not be shown" placeholder (row or message) instead of vanishing. | plan "Record sizes" | | ui/shell | test | done | `parity/conversation.md`; `parity/shell.md` |

## CL: Chat list

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| CL-001 | The list header shows the page's title, New chat, and Hide the chat list (not in a narrow window); on the Calls tab New call takes New chat's place. | guide:728-730; ui/chats.rs:243-430 | Tooltips show ⌘N, ⌘B | ui/chatlist | capture (chat) | done | `parity/calls.md`; `parity/chatlist.md` |
| CL-002 | A row shows the avatar, the name (heavier when unread), the time (accent when unread and not muted) and the latest message. | ui/chats.rs:1411-1490 | | ui/chatlist | capture (chat) | done | `parity/chatlist.md` |
| CL-003 | Our own last message carries its ticks; a group's carries the sender's first name. | ui/chats.rs:1568-1600 | | ui/chatlist | capture (chat) | done | `parity/chatlist.md` |
| CL-004 | A typing contact replaces the preview with "typing…" or "Name is typing…" in the accent. | ui/chats.rs:1532-1545 | | ui/chatlist | test (typing) | done | `parity/chatlist.md` |
| CL-005 | Unsent text shows as "Draft: …" in the accent. | ui/chats.rs:1546-1567; app.rs:2996 | | ui/chatlist | capture (drafts) | done | `parity/chatlist.md` |
| CL-006 | Unread chats show a count badge (99+ at most), a chat marked unread a dot; both dim in muted chats. | ui/chats.rs:1494-1503, :1983-1987 | | ui/chatlist | capture (unread) | done | `parity/chatlist.md` |
| CL-007 | A group whose unread messages mention you or everyone shows "@" beside the count, even when muted. | guide:24-26; ui/chats.rs:1505-1510 | | ui/chatlist | capture (mention) | done | `parity/chatlist.md` |
| CL-008 | Muted and pinned chats show their icons beside the preview. | ui/chats.rs:1511-1524 | | ui/chatlist | capture (chat) | done | `parity/chatlist.md` |
| CL-009 | A chat with disappearing messages has a timer badge on its avatar. | ui/chats.rs:1456-1458 | | ui/chatlist | capture (disappearing) | done | `parity/chatlist.md` |
| CL-010 | Resting the pointer on a cut-short preview shows the whole last message as a tooltip (bounded lines). | ui/chats.rs:1684-1730 | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-011 | Pinned chats come first, then the latest activity; rows move as chats get messages or are pinned. | app.rs:2807-2862; plan Z2 (`VListState.Move`) | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-012 | After sending, the chat moves to the top, and the list follows it only when it was at or near the top. | guide:578-580; ui/chats.rs:1042-1054 | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-013 | A reaction to one of your messages moves the chat to the top. | guide:33 | | logic | test | done | `parity/logic.md` |
| CL-014 | Under Favorites the list keeps the phone's order and stays in place. | guide:581; app.rs:2862 | | ui/chatlist | test (favorites) | done | `parity/chatlist.md` |
| CL-015 | Each filter has its empty state ("No unread chats", "No private chats", "No favorites yet", "No groups", "No channels", "Nothing archived", "Loading your chats", "No chats yet"). | ui/chats.rs:929-947 | | ui/chatlist | capture (unread) | done | `parity/chatlist.md` |
| CL-016 | The chat with yourself reads "Name (You)" over "Message yourself". | ui/chats.rs:1353-1359; app.rs:2516-2549 | | ui/chatlist | capture (new-chat) | done | `parity/chatlist.md` |
| CL-017 | Opening a chat marks it read (sending receipts as the settings allow) and withdraws its notifications. | guide:761; app.rs:5057, :1999-2002 | | logic | test | done | `parity/logic.md` |
| CL-020 | The search field (Ctrl+K, Ctrl+Shift+F) finds chats by name, number or latest message, every stored message, and contacts without a chat, grouped as Chats, Messages and Contacts. | guide:560-561; ui/chats.rs:1176-1250 | ⌘K, ⌘⇧F | ui/chatlist | capture (search) | done | `parity/chatlist.md` |
| CL-021 | Up and Down walk the matching chats while the field keeps focus; Enter opens the reached chat ready for typing. | guide:562; ui/chats.rs:65-68 | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-022 | A message result opens its chat at that message; a contact result starts a chat. | guide:563; ui/chats.rs:1250, :1340 | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-023 | No match reads "No results" with a hint. | ui/chats.rs:1193-1194 | | ui/chatlist | capture (search) | done | `parity/chatlist.md` |
| CL-024 | Escape clears the search and returns focus to the open chat's composer. | keys.rs:233-239 | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-025 | While searching the chips hide; search ignores the chip, the label and the archive. | guide:602-603, :720-721; ui/chats.rs:585-593 | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-026 | Locked chats never appear in search outside the open locked folder. | guide:693 | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-030 | Chips under the search: All, Unread, Private, Favorites, Groups, Channels; a chip with unread chats shows how many. | guide:597-602; ui/chats.rs:587-669 | | ui/chatlist | capture (unread) | done | `parity/chatlist.md` |
| CL-031 | Clicking the active chip again, or All, shows every chat. | guide:602-603; ui/chats.rs:649-653 | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-032 | Stepping through the Unread list with the keyboard does not make it shift underfoot. | keys.rs:303-306; app.rs:7804 | | logic | test | done | `parity/logic.md` |
| CL-033 | Each account's last chip or label is remembered and restored at the next start. | guide:604-605; app.rs:2405-2430 | | logic | test | done | `parity/logic.md`; HANDTEST §12 [CL-033] |
| CL-034 | Chips that do not fit fade at the edge and scroll with the wheel or touchpad, or a scroll bar while hovered; the label row does too. | guide:606-608; ui/chats.rs:582-583, :658-667 | | ui/chatlist | hand | done | `parity/chatlist.md`; HANDTEST §4 [CL-034] |
| CL-035 | Right-clicking the Channels chip offers "Mute all channels" or "Unmute all channels". | ui/chats.rs:627-645; app.rs:7714 | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-040 | Chat menu: Mark as read, or Mark as unread. | guide:610-611; ui/chats.rs:1989-1999 | | ui/chatlist | test (chat-menu) | done | `parity/chatlist.md` |
| CL-041 | Chat menu: Pin to top or Unpin, synced; past the pin limit a toast says "You can only pin N chats". | guide:610-612; ui/chats.rs:2000-2008; app.rs:7515-7518 | | ui/chatlist | test (chat-menu) | done | `parity/chatlist.md` |
| CL-042 | Chat menu: Add to favorites or Remove from favorites (not for channels), synced. | guide:610-612; ui/chats.rs:2009-2021 | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-043 | Chat menu: Archive or Unarchive, synced; archiving closes the open chat. | guide:610-612; keys.rs:587-588; ui/chats.rs:2022-2034 | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-044 | Chat menu: Mute for 8 hours, a week or indefinitely, or Unmute, synced with the phone. | guide:610-612; what:51; ui/chats.rs:2049-2064 | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-045 | Chat menu: Notification sound, the chat's own sound (Default, Pidgin, Pidgin alert, System sound, No sound, a file), overriding Settings. | ui/chats.rs:2120-2170; app.rs:8103 | System sounds stand in for Pidgin's (see Not parity) | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-046 | Chat menu: Lock chat (with confirmation) or Unlock chat. | guide:692-694; ui/chats.rs:2067-2086 | | ui/chatlist | test (chat-menu) | done | `parity/chatlist.md` |
| CL-047 | Chat menu: Copy number copies `+digits` for a one-to-one chat. | ui/chats.rs:2088-2092 | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-048 | Chat menu: Info opens the chat and its info panel. | guide:591-592; ui/chats.rs:2093-2097 | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-049 | Chat menu: Leave group or Leave channel (with confirmation) where you can leave. | guide:614-615; ui/chats.rs:2035-2048 | | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-050 | Chat menu: Delete chat, disabled offline, confirms; the chat leaves once the phone confirms. | guide:612-614; ui/chats.rs:2098-2105; app.rs:7509-7511 | | ui/chatlist | test (delete-chat) | done | `parity/chatlist.md` |
| CL-060 | Archived (rail) lists the archived chats in the list pane with "Back to chats". | guide:599-601; ui/chats.rs:197-212, :529-544 | | ui/chatlist | capture (archived) | done | `parity/chatlist.md` |
| CL-061 | Archived chats stay archived when messages arrive; with Keep chats archived off, a new message received or sent unarchives (older messages, duplicates and history replay do not). | guide:618-623; files:149-151 | | logic | test | done | `parity/logic.md` |
| CL-062 | Ctrl+E archives the open chat, or unarchives it from Archived. | guide:615-616; keys.rs:587-601 | ⌘E | ui/chatlist | test | done | `parity/chatlist.md` |
| CL-070 | Starred (rail) lists every starred message, newest star first, with its chat's name and when it was starred; text as written, media by its list label; locked chats left out. | guide:117-122; ui/chats.rs:677-724 | | ui/chatlist | capture (starred) | done | `parity/chatlist.md` |
| CL-071 | Clicking a starred row opens its chat at the message; right-clicking gives the message's own menu, with Unstar. | guide:120-122; ui/conversation.rs:5509-5580 | | ui/chatlist | test (star-menu) | done | `parity/chatlist.md` |
| CL-072 | No stars reads "No starred messages" with how to add one. | ui/chats.rs:686-689 | | ui/chatlist | capture (starred) | done | `parity/chatlist.md` |
| CL-073 | Chats in the rail, or Escape, leaves Starred. | guide:123; keys.rs:179-180 | | ui/chatlist | test | done | `parity/chatlist.md` |

## LB: Labels

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| LB-001 | Labels are local: kept in the archive, never sent to the phone or anyone, and separate from WhatsApp Business labels. | guide:705-708; files:236-238; ui/labels.rs:246 | | ui/chatlist | test | done | `parity/chatlist.md` |
| LB-002 | The label manager creates a label with a name and one of the ten offered colours, renames or recolours one, and deletes one (its chats keep their messages). | guide:707-714; ui/labels.rs:19-78, :217-490 | | ui/chatlist | capture (labels-dialog) | done | `parity/chatlist.md` |
| LB-003 | At most twenty labels, and no duplicate names; refusals say why. | guide:709; app.rs:2430-2438, :7760-7766 | | logic | test | done | `parity/logic.md` |
| LB-004 | The chat menu's Labels submenu lists every label with a check on those the chat wears; clicking toggles; "Manage labels…" opens the manager. | guide:707, :711-713; ui/labels.rs:151-215 | | ui/chatlist | test (labels) | done | `parity/chatlist.md` |
| LB-005 | Once a label exists, a row of label chips (colour, unread count) follows the chips, ending in + for the manager. | guide:716-718; ui/labels.rs:91-150 | | ui/chatlist | capture (label-chips) | done | `parity/chatlist.md` |
| LB-006 | Picking a label lists only its chats, channels included, and lets go of the built-in chip; picking a chip lets go of the label; clicking it again or All shows every chat. | guide:718-722; ui/labels.rs:91-95 | | ui/chatlist | test (label-filter) | done | `parity/chatlist.md` |
| LB-007 | Label chips read as unchosen in the archive and the locked folder, which they do not filter. | guide:720-721; ui/labels.rs:82-89 | | ui/chatlist | test | done | `parity/chatlist.md` |
| LB-008 | The empty manager explains how to start ("No labels yet…"). | ui/labels.rs:311 | | ui/chatlist | capture (labels-dialog) | done | `parity/chatlist.md` |

## LK: Locked chats

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| LK-001 | Lock chat asks first ("Lock this chat?"), then moves the chat to the locked folder: out of the list, search and unread counts, never notifying; the lock syncs with the phone. | guide:692-694; ui/dialogs.rs:377-392; app.rs:7147 | | ui/dialogs | test (locked) | done | `parity/dialogs.md` |
| LK-002 | Locked in the rail asks for the local code; the first time it offers to set one (code and confirmation, "Set code and open"). | guide:696-699; ui/dialogs.rs:294-361 | | ui/chatlist | capture (locked-prompt) | waived | `parity/chatlist.md` |
| LK-003 | The set-up form explains the code is separate from the phone's and adds no encryption. | guide:698-699; ui/dialogs.rs:302 | | ui/dialogs | capture (locked-setup) | done | `parity/dialogs.md` |
| LK-004 | A wrong code reads "That code did not match. Try again." | ui/dialogs.rs:338 | | ui/dialogs | test | done | `parity/dialogs.md` |
| LK-005 | The open folder lists the locked chats, archived ones included, with title search; with none, "No locked chats". | ui/chats.rs:1091-1121 | | ui/chatlist | capture (locked-open) | done | `parity/chatlist.md` |
| LK-006 | Leaving the tab, changing the code, locking the app or closing the window hides them again. | guide:700; app.rs:4270-4300, :8604-8608 | | logic | test | done | `parity/logic.md` |
| LK-007 | Locked chats are read-only: the composer reads "Locked chats are read-only in ZapFast". | guide:701; ui/conversation.rs:1370 | | ui/composer | capture (locked-open) | done | `parity/composer.md` |
| LK-008 | A locked chat's call waits in the strip as "Locked chat" until the folder is opened. | guide:439-441; app/calls.rs:116 | | ui/calls | test | done | `parity/calls.md` |
| LK-009 | The app lock and the locked-chats code are independent: unlocking one never opens the other. | files:267-268 | | ui/lock | test | done | `parity/lock.md` |

## CV: Conversation header and transcript

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| CV-001 | The header shows the avatar, the name and a subtitle: online or last seen; a group's member names or "Group"; "typing…", "Ada is typing…" or "Ada, Bo and Cy are typing…" in the accent. | ui/conversation.rs:395-599, :871-917; util.rs:335-348 | | ui/conversation | capture (typers) | done | `parity/conversation.md` |
| CV-002 | Clicking the header opens the info panel. | guide:628; ui/conversation.rs:594-599 | | ui/conversation | test | done | `parity/conversation.md` |
| CV-003 | One-to-one chats have voice and video call buttons; while that chat has the call, a hang-up button replaces them; groups, channels, broadcasts and the self chat have none. | guide:428-429, :488-490; ui/conversation.rs:722-784 | | ui/conversation | test | done | `parity/conversation.md` |
| CV-004 | The header's search button (Ctrl+F) opens or closes the chat's search pane. | guide:625; ui/conversation.rs:785-818 | ⌘F | ui/conversation | test | done | `parity/conversation.md` |
| CV-005 | The header's More menu: Info, Select messages, Pin to top or Unpin, Archive or Unarchive, Clear chat (disabled offline), Leave group or channel, Copy number, Close chat. | guide:94, :612-613; ui/conversation.rs:600-721 | | ui/conversation | test (chat-header-menu) | done | `parity/conversation.md` |
| CV-006 | In a narrow window the header has Back to the chats (or "Back to the calls" on the Calls tab). | ui/conversation.rs:819-851 | | ui/conversation | test | done | `parity/conversation.md` |
| CV-007 | A line under the header shows the newest pin, cut with an ellipsis, beside a bar with a segment per pin; clicking opens that message and moves to the next older pin, wrapping. | guide:127-133; ui/conversation.rs:133-267 | | ui/conversation | test (pinned) | done | `parity/conversation.md` |
| CV-008 | The transcript is virtualized: it keeps its anchor, prepends history without jumping, and sticks to the bottom while at the bottom. | plan Z3a; ui/conversation.rs:2289-2292, :3340-3430 | | ui/conversation | test | done | `parity/conversation.md` |
| CV-009 | Sending while reading older messages keeps your place. | guide:576-577 | | ui/conversation | test (scrolled) | hand | `parity/conversation.md`; HANDTEST §4 [CV-009] |
| CV-010 | Away from the bottom, a round "Newest message" button floats bottom right, with the chat's unread count. | guide:577; ui/conversation.rs:3430-3473 | | ui/conversation | capture (scrolled) | done | `parity/conversation.md` |
| CV-011 | Opening a chat with unread messages places an unread divider ("N unread messages") and opens there. | ui/conversation.rs:2886, :3360-3369 | | ui/conversation | capture (unread-divider) | done | `parity/conversation.md` |
| CV-012 | Opening a group with an unread mention shows the oldest unread mention. | guide:24-26 | | logic | test (mention) | done | `parity/logic.md` |
| CV-013 | Messages are separated by day (Today, Yesterday, weekday, date). | util.rs:245-425 | | ui/conversation | capture (chat) | done | `parity/conversation.md` |
| CV-014 | While scrolling, the day of the first message on screen stays at the top, fading a moment after scrolling stops; following new messages does not count. | guide:583-584; ui/conversation.rs:2392-2456 | | ui/conversation | capture (history-date) | done | `parity/conversation.md` |
| CV-015 | The top of the transcript shows a spinner while loading, "Loading messages from your phone…", or "No messages here yet". | ui/conversation.rs:3493-3531 | | ui/conversation | capture (empty) | done | `parity/conversation.md` |
| CV-016 | A typing bubble with animated dots (and stacked avatars in groups) sits at the end of the transcript. | ui/conversation.rs:3533-3620 | | ui/conversation | capture (typing) | done | `parity/conversation.md` |
| CV-017 | Jumping to a message (search, quote, pin, notification, starred row) scrolls to it and highlights it briefly. | ui/conversation.rs:3370-3392; app.rs `JumpHighlight` | | ui/conversation | test (quote-jump) | done | `parity/conversation.md` |
| CV-018 | Messages group into runs: the sender's name and picture on a group run's first message, the tail on its last, wider gaps between runs. | ui/conversation.rs:29-33, :3788-3797, :4343-4370 | | ui/conversation | capture (bubbles) | done | `parity/conversation.md` |
| CV-019 | Stickers, round video messages and carousels draw without a bubble. | ui/conversation.rs:4333-4342 | | ui/conversation | capture (bubbles) | done | `parity/av.md`; `parity/conversation.md` |
| CV-020 | The footer shows the time, "edited", the star and pin marks, and our ticks (sending, sent, delivered, read, played). | ui/conversation.rs:5010-5060, :5276-5395 | | ui/conversation | capture (chat) | hand | `parity/conversation.md`; HANDTEST §4 [CV-020] |
| CV-021 | A captionless picture carries its time and ticks over its corner on a dark scrim. | ui/conversation.rs:5061-5121 | | ui/conversation | capture (photos) | done | `parity/conversation.md` |
| CV-022 | With account read receipts off, read ticks are hidden on outgoing direct messages (transcript, list, message info); group and played receipts are unaffected. | files:182-186; ui/conversation.rs:5067-5079 | | logic | test (receipts-off) | done | `parity/logic.md` |
| CV-023 | A failed send reads "Not sent", explaining on hover that ZapFast will not retry it. | ui/conversation.rs:57-60, :5272 | | ui/conversation | capture (failed) | done | `parity/conversation.md` |
| CV-024 | Clicking your message's time or ticks opens Message info. | guide:37-38; ui/conversation.rs:5243-5270 | | ui/conversation | test | done | `parity/conversation.md` |
| CV-025 | Forwarded messages carry a "Forwarded" label at the top of the bubble. | ui/conversation.rs:4946-5008 | | ui/conversation | capture (quotes) | done | `parity/conversation.md` |
| CV-026 | A quoted reply shows the sender's colour bar, name and text; clicking it goes to the original, in its own chat for a private reply. | ui/conversation.rs:4792-4928 | | ui/conversation | test (quotes) | done | `parity/conversation.md` |
| CV-027 | A private reply whose group is locked or gone hides the quote preview ("Original group message unavailable"). | guide:50-52; ui/conversation.rs:1438 | | ui/conversation | capture (private-reply-locked-source) | done | `parity/conversation.md` |
| CV-028 | Reactions sit under the bubble in a flowing row, ours marked; hovering one shows who added it. | guide:32; ui/conversation.rs:5397-5460 | | ui/conversation | capture (reactions) | done | `parity/conversation.md` |
| CV-029 | Hovering a message shows a react control beside it (switching sides near an edge), labelled "React to {sender}'s message" for screen readers. | ui/conversation.rs:3622-3787 | | ui/conversation | test | done | `parity/conversation.md` |
| CV-030 | Pins leave a notice in the transcript where they were made ("You pinned a message", "Ada pinned a message", "A message was pinned"). | guide:134-136; ui/conversation.rs:2324-2372 | | ui/conversation | capture (pinned) | done | `parity/conversation.md` |
| CV-031 | A pin leaves after its seven days; a deleted message stops being a pin even if the pin arrives again. | guide:136-138; ui/conversation.rs:2374-2381 | | logic | test | done | `parity/logic.md` |
| CV-032 | Read-only chats replace the composer with a notice: "You left this group", "You left this channel", "Channels are read-only in ZapFast", or "Only admins can send messages". | ui/conversation.rs:1350-1400 | | ui/composer | capture (left-group) | done | `parity/composer.md` |
| CV-033 | Calls appear in the transcript (who, voice or video, how long, or missed, declined, not answered); right-clicking one calls back, and a group's says "Group calls happen on the phone". | guide:505-510; ui/conversation.rs:4177-4208 | | ui/conversation | capture (call-entries) | done | `parity/kinds.md` |
| CV-034 | Right-to-left text lays out in reading order, including mixed Hebrew, Arabic and English lines. | demo:65-66 | | ui/conversation | capture (rtl-self) | hand | `parity/conversation.md`; HANDTEST §12 [CV-034] |
| CV-035 | The wallpaper (theme colour, WhatsApp colour with or without doodles, or an image) is drawn behind the transcript. | files:114-115, :21 | | ui/conversation | capture (wallpaper-image) | done | `parity/conversation.md`; HANDTEST §8 [CV-035] |
| CV-036 | Deletes for everyone and backfilled history change the middle of the transcript without moving the view. | plan Z3a (`Splice`) | | ui/conversation | test | done | `parity/conversation.md` |

## MK: Message kinds

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| MK-001 | Text renders WhatsApp markup: `*bold*`, `_italic_`, `~strike~`, ```` ```monospace``` ````. | guide:13-14; markup.rs | | markup | test | done | `parity/markup.md` |
| MK-002 | A message of nothing but emoji shows large. | guide:14 | | markup | capture (chat) | done | `parity/markup.md` |
| MK-003 | Mentions show as highlighted names (address-book first); `@all` is highlighted like any mention. | guide:15-17; mentions.rs | | mentions | capture (mention-everyone) | done | `parity/mentions.md` |
| MK-004 | Web links are clickable and open in the browser; a link type ZapFast will not open says "This link type cannot be opened from ZapFast". | ui/conversation.rs:7633-7880; app.rs:6608-6622 | | ui/conversation | test | done | `parity/conversation.md` |
| MK-005 | A phone number in a message is a link offering "Message" (a new chat) or "Copy number" as written. | guide:651-652; ui/conversation.rs:7785-7786 | | ui/conversation | test (phone-menu) | done | `parity/conversation.md` |
| MK-006 | A `chat.whatsapp.com` invite link opens the join dialog in the app. | guide:631-633; app.rs:6611-6617 | | ui/conversation | test (invite) | done | `parity/kinds.md` |
| MK-007 | A `call.whatsapp.com/voice/…` or `/video/…` link joins that call in the app. | guide:492-496; app.rs:6609-6610 | | ui/calls | test | done | `parity/calls.md` |
| MK-008 | Link previews show their image, title and description; clicking opens the link. | ui/conversation.rs:7884-7970 | | ui/conversation | capture (chat) | done | `parity/kinds.md` |
| MK-009 | Photos show their preview until downloaded; a downloaded one opens the viewer; failures read "Could not display this picture. Click to open it." or "Download failed. Click to retry." | guide:384-386; ui/conversation.rs:8106-8290 | | ui/conversation | capture (photos) | done | `parity/kinds.md` |
| MK-010 | Captions show under their media, with the same markup as text. | guide:206-207 | | ui/conversation | capture (photos) | done | `parity/conversation.md` |
| MK-011 | Stickers draw unframed and animated ones play; clicking a downloaded sticker shows it large with Add to or Remove from favorites; clicking it, outside it, or Escape closes. | guide:160-162; keys.rs:562-575; ui/image_preview.rs:1263-1268 | | ui/conversation | capture (sticker-view) | done | `parity/kinds.md` |
| MK-012 | A shared WhatsApp sticker pack shows its name, publisher and size with "View stickers". | guide:175-177; ui/conversation.rs:6571-6584 | | ui/conversation | capture (sticker-pack-message) | done | `parity/kinds.md` |
| MK-013 | GIFs play in place, muted and looping; one that cannot play here opens in the system viewer. | readme:54; ui/conversation.rs:8412-8414, :8647-8652 | | ui/conversation | capture (chat) | done | `parity/av.md`; `parity/kinds.md` |
| MK-014 | Clicking a video downloads it to the cache and plays it in the bubble with sound, play and pause, time, a seek bar, the speed chip, a mute switch and "Fill the window". | guide:370-375; ui/conversation.rs:8683-8800 | | ui/conversation | capture (video-playing) | done | `parity/av.md`; `parity/conversation.md`; HANDTEST §6 [MK-014] |
| MK-015 | Only H.264 in MP4 plays in the app; other videos open in the system player ("This video opens in your system player"). | guide:381-382; app.rs:8832 | | ui/conversation | test | done | `parity/av.md`; `parity/conversation.md` |
| MK-016 | Double-clicking a video, or the button at the end of its controls, plays it in the full-window viewer. | guide:378-379; ui/conversation.rs:8755-8757 | The viewer window | ui/conversation | test (video-expanded) | done | `parity/av.md`; `parity/conversation.md`; HANDTEST §6 [MK-016] |
| MK-017 | Round video messages play inside their circle, with a progress ring. | guide:372; ui/conversation.rs:8877-9020 | | ui/conversation | capture (note-playing) | done | `parity/av.md`; `parity/conversation.md` |
| MK-018 | A motion photo has a play badge: clicking downloads the clip and loops it muted over the photo, clicking again returns; clips over 64 MiB are not downloaded. | guide:397-401; ui/conversation.rs:8338-8406 | | ui/conversation | capture (motion-playing) | done | `parity/av.md`; `parity/kinds.md`; HANDTEST §6 [MK-018] |
| MK-019 | Voice messages show a seekable waveform, play and pause, the duration and the speed chip. | guide:413-416; ui/conversation.rs:9159-9450 | | ui/conversation | capture (voice) | done | `parity/av.md`; `parity/conversation.md` |
| MK-020 | Audio files show a music icon and a seek bar instead of a waveform. | guide:413-414; ui/conversation.rs:9381 | | ui/conversation | capture (audio-types) | done | `parity/av.md`; `parity/conversation.md` |
| MK-021 | Documents show their icon, name and size, download state, and open when clicked. | ui/conversation.rs:9044-9155 | | ui/conversation | capture (chat) | done | `parity/kinds.md` |
| MK-022 | A location is a card: the map preview the sender sent, the place name and address or the coordinates, and "Open in a map". | guide:278-280; ui/conversation.rs:7972-8036 | | ui/conversation | capture (location) | done | `parity/kinds.md` |
| MK-023 | A live location shows the position this device received, "Updated {time}" or "Live location ended", and that newer positions are on the phone. | guide:252-254; ui/conversation.rs:6392-6464, :6698-6706 | | ui/conversation | capture (live-phone) | done | `parity/kinds.md` |
| MK-024 | A shared contact shows the vCard's name; "Chat" opens a conversation with its WhatsApp account and "Add" saves it; a local-only number just shows. | guide:586-589; ui/conversation.rs:6073-6200, :6508-6523 | | ui/conversation | capture (shared-contact) | done | `parity/kinds.md` |
| MK-025 | A deleted message reads "This message was deleted". | ui/conversation.rs:6675 | | ui/conversation | capture (chat) | done | `parity/kinds.md` |
| MK-026 | View-once photos, videos, voice and audio say they open only on the phone; other phone-only content says "This message can only be seen on your phone." | ui/conversation.rs:6724-6742 | | ui/conversation | capture (chat) | done | `parity/kinds.md` |
| MK-027 | Unsupported content reads "Unsupported: …", and content ZapFast cannot show is marked "More content in WhatsApp Web or on your phone". | guide:329-331; ui/conversation.rs:6766, :7622 | | ui/conversation | capture (interactive) | done | `parity/kinds.md` |
| MK-028 | Album items (same album id, sender and local date) show as one grid; each item downloads, fails and retries on its own. | guide:236-243; ui/conversation.rs:2259-2290, :7004-7270 | | ui/conversation | capture (media-album-failed) | done | `parity/conversation.md` |
| MK-029 | An album's "Download all" fetches every missing item whatever its size, listing failures; "Save all…" copies downloaded files to a folder without replacing any. | guide:238-241; ui/conversation.rs:7079-7101 | | ui/conversation | test (media-album-partial) | done | `parity/conversation.md` |
| MK-030 | Each album item opens on its own and its right-click menu acts on that item alone. | guide:241-243 | | ui/conversation | test (media-album) | done | `parity/conversation.md` |
| MK-031 | Meta AI replies render code blocks and tables. | demo:94 | | markup | capture (meta-ai) | done | `parity/markup.md` |
| MK-032 | With automatic downloads on, attachments up to 64 MiB download as they come into view; visible stickers always do. | guide:245-248; files:137-139; ui/conversation.rs:8406-8410 | | logic | test | done | `parity/logic.md` |
| MK-033 | Larger attachments download only on click or "Download"; a download gives up after 2 min plus 1 s per 128 KiB and offers a retry; an expired one is re-requested from the phone. | guide:247-252; app.rs:6411 | | logic | test | partial | `parity/logic.md`; HANDTEST appendix [MK-033] |

## MM: Message menu and actions

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| MM-001 | Right-clicking a message opens its menu: a quick-reaction row, then the actions the message supports, ending with Message info, Reload earlier messages and Copy message ID. | guide:30-31; ui/conversation.rs:5686-6023 | | ui/conversation | capture (react-menu) | done | `parity/conversation.md` |
| MM-002 | Quick reactions are your most-used ones plus your current one, which is ringed; clicking it removes it ("Remove your reaction"). | ui/conversation.rs:5461-5475, :5580-5600, :5705-5738; settings.rs:480 | | logic | test | done | `parity/logic.md` |
| MM-003 | The reaction row's + opens the full emoji picker to react with any emoji. | guide:31-32; ui/conversation.rs:5739-5766 | | ui/picker | test (react-picker) | done | `parity/picker.md` |
| MM-004 | Reply sets a strip above the composer ("Replying to {who}") with Cancel reply (Esc). | ui/conversation.rs:2161-2205 | | ui/composer | test (reply) | done | `parity/composer.md` |
| MM-005 | Double-clicking beside a message or on its edge replies; double-clicking its text selects a word. | guide:55-56; ui/conversation.rs:4210-4217 | | ui/conversation | test | done | `parity/conversation.md` |
| MM-006 | Reply privately (group messages) opens the sender's chat quoting the group message; the quote survives closing that chat or switching accounts. | guide:48-50; ui/conversation.rs:5781-5796; app.rs:6661 | | logic | test (private-reply) | done | `parity/logic.md` |
| MM-007 | A private reply whose group is locked or inaccessible cannot be sent until authorized again; deleting the recipient chat discards it, including late refusals. | guide:50-53 | | logic | test (private-reply-locked-source) | done | `parity/logic.md` |
| MM-008 | Edit (your text messages, within 15 minutes) puts the text in the composer under "Editing message" with Stop editing (Esc); a refused edit keeps the earlier text. | guide:34-35; app.rs:40, :6913; ui/conversation.rs:2135-2160 | | ui/composer | test (edit) | done | `parity/composer.md` |
| MM-009 | Edits update the visible mentions; editing an existing `@all` does not notify again, adding one does. | guide:21-23 | | mentions | test | done | `parity/mentions.md` |
| MM-010 | Delete for everyone (yours, within two days) and Delete for me each confirm, explaining what happens; neither can be undone. | guide:58-60, :69; app.rs:42; ui/dialogs.rs:1274-1335 | | ui/dialogs | test (delete-message-mine) | done | `parity/dialogs.md` |
| MM-011 | Delete for me syncs with the phone: it needs a connection, the copy goes once WhatsApp accepts, and interrupted requests retry on reconnect. | guide:62-69 | | logic | test | hand | `parity/logic.md`; HANDTEST §12 [MM-011] |
| MM-012 | Star or Unstar: once WhatsApp confirms, a star shows beside the time; stars sync both ways; removing one toasts "Star removed". | guide:114-118; app.rs:3163-3168, :6706 | | ui/conversation | test (star-menu) | done | `parity/conversation.md` |
| MM-013 | Pin message or Unpin message: kept seven days ("Pinned for 7 days", "Pin removed"); a fourth pin is refused ("This chat already has three pinned messages"). | guide:127-130; app.rs:3201-3206, :4021-4026, :7530 | | ui/conversation | test | done | `parity/conversation.md` |
| MM-014 | Copy text copies plain text with names for mentions; a location copies `lat,lon`, a contact its vCard, an interactive message its option labels too. | guide:286-287; ui/conversation.rs:5865-5888 | | ui/conversation | test | hand | `parity/conversation.md`; HANDTEST §4, §12 [MM-014] |
| MM-015 | A downloaded attachment offers Open file (or Open in system player), Copy image for photos, Save as… with a safe suggested name, and Show in folder. | guide:394-395; ui/conversation.rs:5930-5962, :9616-9630 | Show in folder reveals in Finder | ui/conversation | test | done | `parity/conversation.md` |
| MM-016 | An attachment not downloaded offers Download (or a disabled "Downloading…"). | guide:248-249; ui/conversation.rs:5963-5983 | | ui/conversation | test | hand | `parity/conversation.md`; HANDTEST §12 [MM-016] |
| MM-017 | Photos and videos in the album offer Open in the viewer. | guide:386-387; ui/conversation.rs:5603-5610, :5836-5853 | | ui/conversation | test | done | `parity/conversation.md` |
| MM-018 | A downloaded sticker offers Add to favorites. | guide:156-158; ui/conversation.rs:5919-5929 | | ui/conversation | test | done | `parity/conversation.md` |
| MM-019 | A voice message's menu offers every speed: 1x, 1.25x, 1.5x, 1.75x and 2x. | guide:415-417; ui/conversation.rs:4291-4332 | | ui/conversation | test (voice-menu) | done | `parity/av.md`; `parity/conversation.md` |
| MM-020 | Message info is offered for our own sent messages. | ui/conversation.rs:5233-5243, :5992-6006 | | ui/conversation | test | done | `parity/conversation.md` |
| MM-021 | Reload earlier messages asks the phone for up to 50 messages before this one and merges them; disabled offline and on pending or failed messages. | guide:81-90; ui/conversation.rs:6007-6018; app.rs:6345-6362 | | ui/conversation | test | hand | `parity/conversation.md`; HANDTEST §4 [MM-021] |
| MM-022 | Copy message ID copies the message's id. | ui/conversation.rs:6019-6022 | | ui/conversation | test | done | `parity/conversation.md` |
| MM-023 | Deleted messages offer no Reply, Star or Pin. | ui/conversation.rs:5770-5827 | | ui/conversation | test | hand | `parity/conversation.md`; HANDTEST §12 [MM-023] |

## SL: Selecting and copying

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| SL-001 | Selection starts from the header menu's Select messages, a message menu's Select, or Ctrl-click on a message. | guide:94-96; app.rs:6789 | ⌘-click | logic | test (select) | done | `parity/logic.md` |
| SL-002 | Selectable messages get a check box on the left; clicking anywhere on a row toggles it. | guide:96-97; ui/conversation.rs:9538-9552; app.rs:6879 | | ui/conversation | capture (select) | done | `parity/conversation.md` |
| SL-003 | Shift-click adds every message up to the one clicked; a drag adds every message it passes, scrolling at the top and bottom edges; a drag beside the bubbles starts a selection. | guide:98-101; app.rs:339-360, :6813; ui/conversation.rs:3946-3949, :4164-4176 | | logic | test | done | `parity/logic.md` |
| SL-004 | Deleted, unsupported and phone-only messages, polls, interactive messages and calls have no box; a selected message that gets deleted leaves the selection. | guide:106-108; app.rs:327-337, :4038 | | logic | test | done | `parity/logic.md` |
| SL-005 | A bar replaces the composer: the count, Cancel selection, Forward…, and Delete… (Delete for everyone only when every selected message can still be revoked; Delete for me). | guide:71-74, :102-104; ui/conversation.rs:9553-9612 | | ui/conversation | capture (select-delete) | done | `parity/conversation.md` |
| SL-006 | Unticking the last message keeps the selection open; Escape or the close button ends it. | guide:103-104; keys.rs:175-178 | | logic | test | done | `parity/logic.md` |
| SL-007 | Forward… sends the selection together in chat order, one at a time, each after the one before it reached WhatsApp. | guide:102-106 | | logic | test (forward-selected) | done | `parity/logic.md` |
| SL-008 | The delete confirmation counts the messages, and each is deleted as it would be alone. | guide:74-76; ui/dialogs.rs:922, :1276-1277 | | ui/dialogs | test (delete-selection) | done | `parity/dialogs.md` |
| SL-009 | Right-clicking any selected message offers the selection's delete choices. | guide:72-73; ui/conversation.rs:5643-5683 | | ui/conversation | test | done | `parity/conversation.md` |
| SL-010 | Ctrl+C copies the selected messages in chat order in WhatsApp's sharing format, including ones scrolled away; attachments give a label and caption; the selection stays. | guide:544-549; keys.rs:109-133; ui/conversation.rs:4031-4075 | ⌘C, Edit > Copy | logic | test | done | `parity/logic.md` |
| SL-011 | Text can be selected and copied across messages, starting in the padding around text; a cross-message copy uses `[HH:MM, M/D/YYYY] Name: text` lines. | guide:535-542; ui/conversation.rs:4021-4030, :4076-4163, :7680-7690 | | ui/conversation | test | done | `parity/conversation.md` |
| SL-012 | A focused box is outlined, and screen readers read "Select message from {sender}, {time}: {summary}". | guide:109-110; ui/conversation.rs:3080 | VoiceOver | ui/conversation | hand | hand | `parity/conversation.md`; HANDTEST §9 [SL-012] |

## CP: Composer

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| CP-001 | Enter sends and Shift+Enter adds a line; with Enter sends off, Enter adds a line and Ctrl+Enter sends. | guide:11-12; files:135-136 | ⌘↩ | ui/composer | test | done | `parity/composer.md` |
| CP-002 | The field grows from one to six lines, then scrolls; its buttons stay centred on the last line. | ui/conversation.rs:1326-1700, :1919-1941 | | ui/composer | capture (typing) | done | `parity/composer.md` |
| CP-003 | The placeholder reads "Type a message", or "Add a caption" while files are staged. | ui/conversation.rs:1657-1660 | | ui/composer | capture (staged) | done | `parity/composer.md` |
| CP-004 | Each chat keeps its unsent text as a draft across chat switches and restarts. | plan composer; app.rs:4793, :9408 | | ui/composer | test (drafts) | done | `parity/composer.md` |
| CP-005 | Typing and recording send presence when "Show when you are typing" is on. | files:187; app.rs:5309-5335 | | logic | test | done | `parity/logic.md` |
| CP-006 | `@` in a group opens member suggestions above the composer (Up, Down, Tab, Enter, Esc); accepting inserts the mention as one undo step. | guide:15; ui/conversation.rs:935-945, :1180-1325; plan composer | | ui/composer | test (mention) | done | `parity/composer.md` |
| CP-007 | "all" heads the suggestions when the member list is known, also by its translation (`@todos`…); in groups over 32 only admins get it; the message shows `@all` and notifies like a mention. | guide:15-21; mentions.rs:80 | | mentions | test (mention-everyone) | done | `parity/mentions.md` |
| CP-008 | `:` plus letters opens emoji suggestions that narrow as you type; colons in times and URLs and `@` in addresses stay text. | ui/conversation.rs:915-1180 | | ui/composer | test (emoji-complete) | done | `parity/composer.md` |
| CP-009 | An input-method composition closes the suggestions, and the send key commits it instead of sending. | plan composer | Real IME, dictation, Writing Tools (plan exit 7) | ui/composer | hand | hand | `parity/composer.md`; HANDTEST §5 [CP-009] |
| CP-010 | The send button becomes a microphone when the composer is empty. | guide:421-422; ui/conversation.rs:1809-1822 | | ui/composer | capture (chat) | done | `parity/composer.md` |
| CP-011 | The plus menu offers Send files, Send as document, Send location and Create poll. | guide:212-213, :258, :405; ui/conversation.rs:2007-2050 | | ui/composer | capture (composer-tools) | done | `parity/composer.md` |
| CP-012 | The smiley opens the picker on emoji, GIFs or stickers. | guide:26-28; ui/conversation.rs:1577 | | ui/composer | test (picker) | done | `parity/composer.md` |
| CP-013 | Send files and Send as document choose files with the system file dialog. | guide:199-200, :212-215 | `NSOpenPanel` through the core's pick bridge | ui/composer | hand | hand | `parity/composer.md`; HANDTEST §12 [CP-013] |
| CP-014 | Pasting a picture or copied files stages them, even on a quick tap; other fields paste only text; nothing is staged while a menu, the picker, a recording or a full-size video is open. | guide:199-205; app.rs:9212-9363 | ⌘V | ui/composer | test | done | `parity/composer.md`; HANDTEST §5 [CP-014] |
| CP-015 | A paste with nothing to attach says "Ctrl+V found nothing to attach: …". | app.rs:9267 | ⌘V in the text | ui/composer | test | done | `parity/composer.md` |
| CP-016 | Staged files wait above the composer with close buttons; Escape removes them; the typed text becomes the caption, and with several files the caption and quote go with the first. | guide:206-209; keys.rs:190-191 | | ui/composer | capture (staged) | done | `parity/composer.md` |
| CP-017 | A staged picture goes as a photo (compressed); files chosen with Send as document go as documents with their own name and bytes. | guide:211-216 | | ui/composer | test | done | `parity/composer.md` |
| CP-018 | A pasted or dropped attachment is ready at once: the send key sends it immediately. | guide:201-202 | | ui/composer | test | done | `parity/composer.md` |
| CP-019 | Staged files, caption and reply stay with their chat across chat and account switches while the app runs; a refused attachment send returns to its chat. | guide:218-221; app.rs:4738-4767 | | logic | test (attachment-draft-return) | done | `parity/logic.md` |
| CP-020 | Sending files toasts "Sending N files…"; staging with no chat open says "Open a chat first". | app.rs:5500, :5605-5612 | | ui/composer | test | done | `parity/composer.md` |
| CP-021 | A refused text send returns to its chat's composer, with "Not sent: ZapFast is not connected to WhatsApp." or a missing-quote explanation. | app.rs:4634-4731 | | logic | test | done | `parity/logic.md` |
| CP-022 | The shortcut hints bar under the composer shows the Enter policy, formatting and paste hints and "All shortcuts (Ctrl+/)"; its × hides it, and the shortcuts dialog brings it back. | files:223-226; ui/conversation.rs:1860-1891 | Hints read ⌘ | ui/composer | capture (chat) | done | `parity/composer.md` |
| CP-023 | Recording a voice message shows a live waveform with Discard and Send; Enter sends, Escape discards; a reply started first is quoted; quiet recordings are made louder. | guide:421-424; ui/conversation.rs:9450-9533; keys.rs:248-253 | Microphone permission | ui/composer | capture (recording) | hand | `parity/composer.md`; HANDTEST §13 [CP-023] |
| CP-024 | A refused voice message waits above the composer ("Voice message (0:12) not sent") with Discard; Send tries it again. | ui/conversation.rs:2055-2133 | | ui/composer | capture (unsent-voice) | done | `parity/composer.md`; HANDTEST §13 [CP-024] |
| CP-025 | Recording without a microphone says "Could not record: no microphone" (or the error). | ui/mod.rs:1019; app.rs:9111-9116 | | ui/composer | test | done | `parity/composer.md`; HANDTEST §13 [CP-025] |
| CP-026 | Emoji typed in the draft show in colour. | ui/conversation.rs:1628-1640 | | ui/composer | capture (compose-emoji) | done | `parity/composer.md` |

## PK: Picker (emoji, GIFs, stickers)

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| PK-001 | The picker opens above the composer with Emoji, GIF and Stickers tabs, and closes on a click outside it. | ui/picker.rs:48-129 | | ui/picker | capture (picker) | done | `parity/picker.md` |
| PK-002 | The emoji tab has a search field, recent emoji ("Frequently Used"), and Unicode groups with category tabs that jump to them. | guide:26-27; ui/picker.rs:142-330, :627 | | ui/picker | capture (picker) | done | `parity/picker.md` |
| PK-003 | Choosing an emoji inserts it at the cursor and records it as recent. | app.rs:9177-9210 | | ui/picker | test | done | `parity/picker.md` |
| PK-004 | As a reaction picker it reacts to the message ("React to message") and closes. | ui/picker.rs:437-448 | | ui/picker | test (react-custom) | done | `parity/picker.md` |
| PK-005 | The GIF tab searches GIPHY through the core, shows trending results first, and sends the chosen GIF ("Sending GIF…"). | links:34; ui/picker.rs:1012-1076; app.rs:7423 | | ui/picker | capture (gifs) | done | `parity/picker.md` |
| PK-006 | Without a GIPHY key, or with a rejected one, the GIF tab asks for one and saves it to the settings. | files:215-217; ui/picker.rs:951-979 | | ui/picker | capture (gifs-badkey) | done | `parity/picker.md` |
| PK-007 | The sticker tabs are Recent (clock), Favorites (star), Received (speech bubble), each pack (its first sticker), and +. | guide:142-145; ui/picker.rs:1105-1192 | | ui/picker | capture (stickers) | done | `parity/picker.md` |
| PK-008 | Clicking a sticker sends it; animated stickers play only while hovered. | guide:145; ui/picker.rs:1686-1730 | | ui/picker | test (sticker-animated) | done | `parity/picker.md` |
| PK-009 | Recent holds the stickers you sent; "Remove from recents" removes one here and on the phone; an old sticker rejoins once clicked in its chat. | guide:147-150; ui/picker.rs:1623 | | ui/picker | test | done | `parity/conversation.md`; `parity/picker.md` |
| PK-010 | Received holds downloaded stickers people sent you, newest first, each once, leaving out Recent, Favorites and locked chats. | guide:152-154 | | ui/picker | capture (sticker-received) | done | `parity/picker.md` |
| PK-011 | Favorites sync with the phone both ways; right-click adds or removes. | guide:156-158; ui/picker.rs:1621-1622 | | ui/picker | test (sticker-favorites) | done | `parity/picker.md` |
| PK-012 | Sticker search finds by emoji, a word naming an emoji, or a pack's name. | guide:164-166; ui/picker.rs:1227, :1266 | | ui/picker | test (sticker-search) | done | `parity/picker.md` |
| PK-013 | Empty shelves explain themselves ("Loading your stickers…", "Stickers you send appear here.", …). | ui/picker.rs:1212, :1280-1297, :1340 | | ui/picker | capture (stickers) | done | `parity/picker.md` |
| PK-014 | The + page imports a pack from a pasted `signal.art` link (or Find packs) or a `.wastickers` file, keeping Signal's emoji ("Importing the pack…"). | guide:168-171; ui/picker.rs:1406-1574 | | ui/picker | capture (sticker-add) | done | `parity/picker.md` |
| PK-015 | A pack's tab has a delete button that asks first ("Remove sticker pack?") and deletes its files. | guide:171-173; ui/picker.rs:1312; ui/dialogs.rs:1349-1357 | | ui/picker | test (remove-pack) | done | `parity/picker.md` |
| PK-016 | A pack's send arrow sends it to the open chat as a WhatsApp sticker pack of up to 60 stickers with their emoji ("Sending the sticker pack…"). | guide:177-179; ui/picker.rs:1326; app.rs:7382 | | ui/picker | test | done | `parity/picker.md` |
| PK-017 | "Make your own" names and creates a pack; any sticker's menu lists your packs with checks, and choosing one adds or removes it (one copy per content). | guide:181-187; ui/picker.rs:1435-1481, :1610-1685, :1814-1849 | | ui/picker | test | done | `parity/picker.md` |
| PK-018 | "View stickers" on a shared pack downloads it into a dialog; "Add to my stickers" keeps it as a pack. | guide:175-177; ui/dialogs.rs:1060-1096 | | ui/dialogs | capture (sticker-pack-view) | done | `parity/dialogs.md` |

## SM: Sticker maker

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| SM-001 | "Make a sticker from a picture…" chooses a PNG, JPEG, WebP or GIF. | guide:189-190; ui/picker.rs:1455 | System file dialog | ui/stickermaker | hand | done | `parity/stickermaker.md` |
| SM-002 | Dragging the square picks the part to keep and Size resizes it, over a preview the core renders. | guide:190-191; ui/dialogs.rs:1110-1192; plan Z3a | | ui/stickermaker | capture (sticker-maker) | done | `parity/stickermaker.md`; HANDTEST §6 [SM-002] |
| SM-003 | A transparent background is kept unless "Keep the transparent background" is turned off, which makes it white. | guide:191-193; ui/dialogs.rs:1192 | | ui/stickermaker | test | done | `parity/stickermaker.md` |
| SM-004 | An emoji field describes the sticker, for search here and WhatsApp's suggestions. | guide:193-194; ui/dialogs.rs:1209 | | ui/stickermaker | test | done | `parity/stickermaker.md` |
| SM-005 | Send sends it to the open chat; Add to favorites keeps it ("Added to favorites"); Cancel leaves. | guide:194; ui/dialogs.rs:1226-1241; app.rs:7305 | | ui/stickermaker | test | done | `parity/stickermaker.md` |
| SM-006 | The result is a 512 × 512 WebP under 100 KB (the core's `MakeSticker`). | guide:195; worker.rs:6583 | | ui/stickermaker | test | waived | `parity/stickermaker.md` |

## CR: Cropper

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| CR-001 | Clicking a staged picture opens the cropper over the window with the region to keep marked. | guide:223-225; ui/image_editor.rs:32-33 | | ui/cropper | capture (crop) | done | `parity/cropper.md`; HANDTEST §6 [CR-001] |
| CR-002 | The region is dragged by its edges and corners, or moved whole, with fitting cursors. | guide:225-226; ui/image_editor.rs:307-341 | | ui/cropper | test | done | `parity/cropper.md`; HANDTEST §6 [CR-002] |
| CR-003 | Turn left and Turn right turn the picture a quarter at a time; Reset restores it. | guide:226-227; ui/image_editor.rs:140-157 | | ui/cropper | test | done | `parity/cropper.md`; HANDTEST §6 [CR-003] |
| CR-004 | Enter or the check keeps the crop; Escape or the close button leaves the picture as it was. | guide:227-228; keys.rs:492-505; ui/image_editor.rs:106-119 | | ui/cropper | test | done | `parity/cropper.md` |
| CR-005 | Keeping writes a cropped copy, transparency kept, beside the other media and sends that; the original file is untouched. | guide:230-232; plan Z3a (`ApplyPictureEdit`) | | ui/cropper | test | done | `parity/cropper.md` |
| CR-006 | Reopening the cropper starts from the original with the chosen crop. | guide:232-234 | | ui/cropper | test | done | `parity/cropper.md` |
| CR-007 | A pasted picture with no file crops from its pixels. | ui/image_editor.rs:188-243 | | ui/cropper | test | done | `parity/cropper.md` |
| CR-008 | A picture that cannot be used says "Could not use this image". | app.rs:3799-3804 | | ui/cropper | test | done | `parity/cropper.md` |

## VW: Media viewer

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| VW-001 | Clicking a downloaded JPEG, PNG or WebP photo opens it in the viewer. | guide:384-386 | The viewer is its own window and can go full screen | ui/viewer | capture (preview) | done | `parity/viewer.md`; HANDTEST §6 [VW-001] |
| VW-002 | Zoom works with the wheel, Ctrl+wheel, a pinch, the header's Zoom in, Fit or original size, Zoom out, and + − 0 keys; a double-click switches between fit and original size. | guide:385-386; ui/image_preview.rs:300-333; image_preview.rs:48-58 | Pinch on `EvMagnify`, smart zoom (plan Z3a) | ui/viewer | hand | done | `parity/viewer.md`; HANDTEST §6 [VW-002] |
| VW-003 | Copy image (header or Ctrl+C) copies a photo ("Copied image" or "Failed to copy image"), never a clip. | guide:386; keys.rs:444-455; app.rs:9422-9437 | ⌘C | ui/viewer | test | done | `parity/viewer.md`; HANDTEST §6 [VW-003] |
| VW-004 | The chat's photos, videos and card images run along the bottom, the current one ringed; clicking one shows it. | guide:388-389; ui/image_preview.rs:944-1060 | | ui/viewer | capture (preview) | done | `parity/viewer.md` |
| VW-005 | Left and Right, or the arrows shown at the picture's sides on hover, step through the album. | guide:389-390; image_preview.rs:61-62; ui/image_preview.rs:1185-1187 | | ui/viewer | test (preview) | done | `parity/viewer.md` |
| VW-006 | The header replies, reacts, forwards, saves (Save as…) or downloads, shows the message in the chat, opens in another app, and closes (Esc). | guide:390-391; ui/image_preview.rs:190-444 | | ui/viewer | capture (preview) | done | `parity/conversation.md`; `parity/viewer.md` |
| VW-007 | A click on the empty space around the picture, or Escape, closes the viewer. | guide:391-392; keys.rs:441-443 | | ui/viewer | test | done | `parity/viewer.md`; HANDTEST §6 [VW-007] |
| VW-008 | The viewer closes when its chat is removed, becomes locked outside the open folder, or the folder closes. | guide:392-394; app.rs:4827 | | ui/viewer | test | done | `parity/viewer.md` |
| VW-009 | A video in the viewer plays with sound; Space plays and pauses, Left and Right jump five seconds, M mutes, S changes the speed, Escape closes and stops it. | guide:379-381; keys.rs:507-560 | | ui/viewer | test (video-expanded) | done | `parity/viewer.md`; HANDTEST §6 [VW-009] |
| VW-010 | A motion photo's play button plays its clip over the photo with sound, and the photo button goes back; a failed download says why and retries on click. | guide:399-401; ui/image_preview.rs:473-503 | | ui/viewer | capture (motion-viewer) | done | `parity/viewer.md`; HANDTEST §6 [VW-010] |
| VW-011 | An item not downloaded shows its own thumbnail and a Download button. | ui/image_preview.rs:716-800 | | ui/viewer | test | done | `parity/viewer.md` |
| VW-012 | A picture that cannot be shown says so, with "Open externally". | ui/image_preview.rs:701-704 | | ui/viewer | test | done | `parity/viewer.md` |
| VW-013 | While the viewer is up, typing and clipboard keys never reach the chat. | keys.rs:435-490; image_preview.rs:67-90 | Separate window | ui/viewer | test | done | `parity/viewer.md` |
| VW-014 | A carousel card's picture opens among the card's other pictures. | demo:78-79; ui/conversation.rs:7273 | | ui/viewer | capture (carousel-viewer) | done | `parity/viewer.md` |

## PL: Polls

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| PL-001 | Create poll asks a question with 2 to 12 answers (Add answer, Remove answer) and "Allow multiple answers"; Send poll shows "Sending…". | guide:405-408; ui/polls.rs:13-129 | | ui/dialogs | capture (poll-create) | done | `parity/dialogs.md` |
| PL-002 | Polls cannot be created in chats with disappearing messages. | guide:408-409 | | ui/dialogs | test | done | `parity/dialogs.md` |
| PL-003 | A poll shows each option with a result track even before votes, a checkmark on your choice, and "Select one answer" or "Select answers". | guide:341-342; ui/polls.rs:180-182 | | ui/conversation | capture (poll-voted) | done | `parity/kinds.md` |
| PL-004 | Clicking an answer votes and clicking it again takes the vote back ("Sending vote…"). | guide:406-407; ui/polls.rs:284 | | ui/conversation | test (poll-empty) | done | `parity/kinds.md` |
| PL-005 | Voting is blocked with a reason: "Voting key unavailable · use your phone" or "Reconnect to vote". | ui/polls.rs:313-318 | | ui/conversation | test | done | `parity/kinds.md` |
| PL-006 | While phone history arrives, the poll says earlier votes may be missing or are loading. | guide:342-347; ui/polls.rs:286-290 | | ui/conversation | capture (poll) | done | `parity/kinds.md` |
| PL-007 | Show votes opens the results: participants per option with vote times, an incomplete-history notice, "Participant details are not available yet", or "This poll is no longer available." | guide:342-343; ui/polls.rs:333-518 | | ui/dialogs | capture (poll-results) | done | `parity/dialogs.md` |
| PL-008 | Polls received live start at zero; polls from history or received offline recover their votes from the phone. | guide:344-347 | | logic | test | hand | `parity/logic.md`; HANDTEST §12 [PL-008] |

## IA: Interactive messages

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| IA-001 | Templates and button messages show their image above the formatted text, with options in separate full-width rows below the timestamp. | guide:284-286; product:14-15; ui/conversation.rs:7414-7630 | | ui/conversation | capture (interactive-media) | done | `parity/kinds.md` |
| IA-002 | Web-link options have an external-link icon and open in the browser, by click or Enter when focused. | guide:289-290 | | ui/conversation | test (interactive) | done | `parity/kinds.md` |
| IA-003 | Reply buttons send at once, quoting the message and carrying the option id (legacy buttons, hydrated templates, native-flow quick replies). | guide:291-294; ui/conversation.rs:7594 | | ui/conversation | test (interactive-actions) | done | `parity/kinds.md` |
| IA-004 | Lists open a centred "Choose an option" dialog with the title, sections, options and descriptions; choosing sends, and closing or Escape sends nothing. | guide:295-297, :336-339; ui/dialogs.rs:128-170 | | ui/dialogs | capture (interactive-list-dialog) | done | `parity/dialogs.md` |
| IA-005 | Copy-code buttons copy the code without sending anything. | guide:298-299; ui/conversation.rs:7601 | | ui/conversation | test (interactive-actions) | done | `parity/kinds.md` |
| IA-006 | Unavailable options (forms, payments, shopping, calls, carousel choices) show a phone icon and muted text and explain themselves on hover. | guide:300-303; ui/conversation.rs:7578 | | ui/conversation | capture (interactive-actions) | done | `parity/conversation.md`; `parity/kinds.md` |
| IA-007 | Replies need a connection and a writable chat, never go to your own cards, wait for one in flight ("Sending reply…"), and allow another try after a failure. | guide:305-308; ui/conversation.rs:7570-7576; ui/dialogs.rs:150-168 | | ui/conversation | test | done | `parity/kinds.md` |
| IA-008 | Replies from your other devices appear as ordinary replies, quoting the original when included. | guide:310-311 | | ui/conversation | test | hand | `parity/kinds.md`; HANDTEST §12 [IA-008] |
| IA-009 | The body can be selected and found by search, and Copy text includes the option labels. | guide:286-287 | | ui/conversation | test | hand | `parity/kinds.md`; HANDTEST §12 [IA-009] |
| IA-010 | Carousels show separate cards in a horizontal strip, each with its image, text and actions; the time sits below the last card when the strip fits. | guide:349-351; ui/conversation.rs:6777-6946 | | ui/conversation | capture (carousel-pair) | done | `parity/kinds.md` |
| IA-011 | Overflowing carousels get round previous and next arrows (Enter when focused) that disappear at the ends; Shift+wheel and horizontal touchpad scrolling work, with no scrollbar. | guide:351-354; ui/conversation.rs:6946-7000 | Trackpad | ui/conversation | test (carousel) | done | `parity/kinds.md` |
| IA-012 | Card images follow the automatic-download setting and retry rules, and open in the viewer. | guide:313-314, :354-355; ui/conversation.rs:7274-7290 | | ui/conversation | test | done | `parity/kinds.md` |
| IA-013 | Interactive messages cannot be forwarded or selected yet. | guide:332; what:74-75 | | logic | test | done | `parity/logic.md` |

## LO: Locations

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| LO-001 | Send location takes a spot as text: coordinates (comma or space between them, dot or comma decimals) or a Google Maps, Apple Maps, OpenStreetMap or `geo:` link. | guide:258-262; ui/locations.rs:16-29 | | ui/dialogs | capture (location) | done | `parity/dialogs.md` |
| LO-002 | The dialog shows the spot and an "Open in a map" link before sending, and refuses text it cannot read ("That is not a location."). | guide:262-263; ui/locations.rs:45, :67 | | ui/dialogs | test | done | `parity/dialogs.md` |
| LO-003 | Short links and other sites are not read, and nothing is fetched; a short link asks for the full link or coordinates. | guide:265-270; ui/locations.rs:58 | | ui/dialogs | test | done | `parity/dialogs.md` |
| LO-004 | It sends a static spot with no name or address. | guide:271-273 | | ui/dialogs | test | done | `parity/dialogs.md` |
| LO-005 | If sending fails the dialog comes back with the spot, or the coordinates wait in that chat's composer if you moved on. | guide:274-276; app.rs:5088-5154, :5354 | | logic | test | done | `parity/dialogs.md`; `parity/logic.md` |

## DL: Dialogs

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| DL-001 | Forward opens a recipient picker with search over writable chats, up to five ("You can forward to up to 5 chats"); "No writable chats found" when none. | guide:40-41; ui/dialogs.rs:483-690 | | ui/dialogs | capture (forward) | done | `parity/dialogs.md` |
| DL-002 | A message forwarded many times goes to one chat at a time, and the picker says so. | guide:41-42; ui/dialogs.rs:679 | | ui/dialogs | capture (forward-once) | done | `parity/dialogs.md` |
| DL-003 | With one chat chosen Forward sends at once; with several, Review recipients asks "Forward to these chats?", and Back keeps the choice. | guide:42-44; ui/dialogs.rs:497-529, :713-729 | | ui/dialogs | test (forward-review) | done | `parity/dialogs.md` |
| DL-004 | If a destination becomes locked or read-only, the picker returns to selection rather than send part of the batch. | guide:44-46; app.rs:6767-6772 | | logic | test | done | `parity/dialogs.md`; `parity/logic.md` |
| DL-005 | New chat (Ctrl+N) offers Message yourself, Add contact, and contact search ("No matching contacts"). | guide:553-554; ui/dialogs.rs:400-433; keys.rs:53-59 | ⌘N, File > New Chat… | ui/dialogs | capture (new-chat) | done | `parity/dialogs.md` |
| DL-006 | Add contact takes a number with its country code (a leading +, spaces, hyphens and brackets allowed; anything else refused), first name, surname, and "Also save to your phone's contacts" (remembered). | guide:553-558, :589; files:226; ui/dialogs.rs:1597-1716 | | ui/dialogs | capture (new-contact-number) | done | `parity/dialogs.md` |
| DL-007 | Message with no name checks the number first ("Checking the number…"); "This number is not on WhatsApp", "Not connected to WhatsApp" or a retryable failure. | guide:556-558; app.rs:872-885 | | ui/dialogs | test (new-contact) | done | `parity/dialogs.md` |
| DL-008 | Save contact with a name saves it, and on the phone when chosen. | guide:558, :588-589; app.rs:7621 | | ui/dialogs | test | done | `parity/dialogs.md` |
| DL-009 | Delete chat? explains it deletes the chat here and on the phone and cannot be undone. | ui/dialogs.rs:932-945 | | ui/dialogs | capture (delete-chat) | done | `parity/dialogs.md` |
| DL-010 | Clear chat? explains it clears the messages and downloaded media here and on the phone, keeping the chat; done once the phone confirms. | guide:612-614; ui/dialogs.rs:953-966 | | ui/dialogs | capture (clear-chat) | done | `parity/dialogs.md` |
| DL-011 | Leave this group/channel? keeps the local history and offers "Leave … and archive". | guide:614-615; ui/dialogs.rs:1465-1511 | | ui/dialogs | capture (leave-group) | done | `parity/dialogs.md` |
| DL-012 | Unlink this computer? removes the device from WhatsApp and deletes the chats and caches here. | links:39-40; ui/dialogs.rs:1367-1372 | | ui/dialogs | capture (unlink) | done | `parity/dialogs.md` |
| DL-013 | Clear the media cache? names the account and keeps files in a chosen download folder; then "Media cache cleared". | files:30-34; ui/dialogs.rs:1388-1400; app.rs:3086-3089 | | ui/dialogs | capture (clear-media) | done | `parity/dialogs.md` |
| DL-014 | A group invite looks the group up, shows it, and offers Join group, Request to join (admin approval), or Open chat when already a member ("Joining…"; "Request sent…"). | guide:631-633, :672-674; ui/dialogs.rs:980-1048; app.rs:3841 | | ui/dialogs | capture (invite) | done | `parity/dialogs.md` |
| DL-015 | Keyboard shortcuts lists the 29 shortcuts with this platform's key names, and the switch "Show shortcut hints under the message box". | guide:774-775; ui/dialogs.rs:810-861; keys.rs:622-675 | ⌘ and ⌥ names | ui/dialogs | capture (shortcuts) | done | `parity/dialogs.md` |
| DL-016 | About shows the version, what ZapFast is, the unofficial-client warning, and a Source code link. | ui/dialogs.rs:871-902 | ZapFast > About ZapFast | ui/dialogs | capture (about) | done | `parity/dialogs.md` |
| DL-017 | Escape closes the open dialog, and chat shortcuts do not run beneath it. | keys.rs:169-170, :67-74 | | ui/dialogs | test | done | `parity/dialogs.md`; HANDTEST §2 [DL-017] |

## IN: Info panel

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| IN-001 | The info panel opens beside the chat as Contact info, Group info or Channel info, with Close. | guide:591-592, :628-630; ui/info.rs:244-254 | | ui/info | capture (info) | done | `parity/info.md` |
| IN-002 | It shows the picture, name, number or member count, online or last seen, and "Muted" or "Muted until …". | guide:629-630; ui/info.rs:440-480 | | ui/info | capture (info) | done | `parity/info.md` |
| IN-003 | It shows the disappearing-messages timer, "Off" once turned off, and nothing when unknown; the timer is changed on the phone. | guide:591-595; ui/info.rs:481-500 | | ui/info | capture (disappearing-off) | done | `parity/info.md` |
| IN-004 | Its buttons, centred in rows: Message, Rename or Add to contacts, Copy number, Mute or Unmute, Pin or Unpin, Archive or Unarchive. | guide:630; ui/info.rs:568-683 | | ui/info | test (info) | done | `parity/info.md` |
| IN-005 | Rename or Add to contacts edits the first name and surname in place. | ui/info.rs:396-420, :502-509 | | ui/info | test | done | `parity/info.md` |
| IN-006 | Where WhatsApp allows, a group is renamed with the pencil ("Save name (Enter)", "Cancel (Escape)", "Saving…"). | guide:630-631; ui/info.rs:316, :1393-1448 | | ui/info | capture (group-info-rename) | done | `parity/info.md` |
| IN-007 | Where WhatsApp allows, clicking the group photo offers Change photo and Remove photo. | guide:631; ui/info.rs:1347-1355 | | ui/info | test (group-info) | done | `parity/info.md`; HANDTEST §6 [IN-007] |
| IN-008 | A group's description can be selected and copied, and scrolls when long. | guide:645-647; ui/info.rs:517-535 | | ui/info | capture (group-info) | done | `parity/info.md` |
| IN-009 | Members (N) lists the members in a scrolling list; each opens their own panel. | ui/info.rs:685-770 | | ui/info | test (group-info) | done | `parity/info.md` |
| IN-010 | Groups and channels you can leave have a Leave button, with confirmation. | ui/info.rs:546-565 | | ui/info | test | done | `parity/info.md` |
| IN-011 | Media, links and docs lists photos and videos, documents, and links, newest first, in tabs; a photo or document goes to its message, a link opens the browser. | guide:635-637; ui/info.rs:39, :775-1310 | | ui/info | capture (info-media) | done | `parity/info.md` |
| IN-012 | Its empty and error states: "No media", "No documents", "No links", "Only the newest items are listed.", "Could not list the media." with Try again. | ui/info.rs:875-883, :995-1219, :1292 | | ui/info | capture (info-docs) | done | `parity/info.md` |
| IN-013 | Escape goes from the media list back to the overview, then closes the panel. | guide:637-638; keys.rs:209-220 | | ui/info | test | done | `parity/info.md` |
| IN-014 | Dragging the panel's edge resizes it; the conversation narrows, the chat list steps aside when tight, and a narrow window shows the panel as a page with Back. | guide:638-642; ui/pane.rs:24-134 | | ui/info | test | done | `parity/info.md` |
| IN-015 | A locked chat's media is listed only while the locked folder is open. | guide:642-643; ui/info.rs:536-541 | | ui/info | test (group-info-locked) | done | `parity/info.md` |

## MI: Message info

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| MI-001 | Message info shows the message as its bubble does, shortened. | ui/message_info.rs:72-102 | | ui/msginfo | capture (message-info) | done | `parity/msginfo.md` |
| MI-002 | In a direct chat it shows when the message was delivered and read (or played), or "Time not recorded". | ui/message_info.rs:173-218 | | ui/msginfo | capture (message-info-direct) | done | `parity/msginfo.md` |
| MI-003 | In a group it lists Played by, Read by and Delivered to with times, "N remaining" and "No one yet", updating live while open. | ui/message_info.rs:262-316; app.rs:8756-8773 | | ui/msginfo | capture (message-info) | done | `parity/msginfo.md` |
| MI-004 | It says when no receipts, or only some, were recorded. | ui/message_info.rs:240-252 | | ui/msginfo | capture (message-info-partial) | done | `parity/msginfo.md` |
| MI-005 | It shows "Loading…" first, and "This message is no longer available." if the message goes. | ui/message_info.rs:44, :93 | | ui/msginfo | test (message-info-unknown) | done | `parity/msginfo.md` |

## CS: Chat search pane

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| CS-001 | The chat's search pane docks at the right, or lies over a conversation too narrow to share, and lists matches newest first. | guide:625-626; ui/pane.rs:24-134 | | ui/conversation | capture (chat-search) | done | `parity/conversation.md` |
| CS-002 | A result shows when it was said and the matching line with the match in bold, the sender in groups, and context before a deep match. | ui/pane.rs:632-800 | | ui/conversation | capture (chat-search-rtl) | hand | `parity/conversation.md`; HANDTEST §4 [CS-002] |
| CS-003 | Up and Down walk the results from the field, and Enter opens the one reached, or the newest. | keys.rs:625-628; ui/pane.rs:372-374 | | ui/conversation | test | done | `parity/conversation.md` |
| CS-004 | The calendar narrows matches to one day (month arrows); the chosen day shows with a cross to clear it. | guide:626; ui/pane.rs:414-520 | | ui/conversation | capture (chat-search-day) | done | `parity/conversation.md` |
| CS-005 | No matches reads "No messages found"; a capped list says only the newest are listed. | ui/pane.rs:318-346 | | ui/conversation | capture (chat-search) | done | `parity/conversation.md` |
| CS-006 | Over a narrow conversation, picking a result folds the pane away, and Search brings it back as it was. | ui/pane.rs:200-202 | | ui/conversation | test | done | `parity/conversation.md` |
| CS-007 | Escape closes the day filter first, then the pane. | keys.rs:181-185, :240-241 | | ui/conversation | test | done | `parity/conversation.md` |

## VO: Voice and video playback

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| VO-001 | The speed chip cycles 1x, 1.5x and 2x; the voice speed is remembered for later messages and the pitch stays the same. | guide:415-420; settings.rs:513; app.rs:7112 | | logic | test | done | `parity/av.md`; `parity/logic.md` |
| VO-002 | When a voice message ends, the unheard voice messages right after it play on; any other message ends the run. | guide:417-419; app.rs:8979-9045 | | logic | test | done | `parity/logic.md` |
| VO-003 | The first play sends a played receipt. | guide:420-421; app.rs:8948 | | logic | test | done | `parity/logic.md` |
| VO-004 | The video speed (1x, 1.5x, 2x) stays for the next video until quitting, with pitch kept and stereo kept. | guide:372-374 | | logic | test | done | `parity/av.md`; `parity/logic.md` |
| VO-005 | A speed chip reads "Preparing playback speed" while the new speed is prepared. | ui/conversation.rs:4235-4237, :9418 | | ui/conversation | test | waived | `parity/av.md`; `parity/conversation.md` |

## CA: Calls

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| CA-001 | Starting a call opens the call screen at once: "Connecting…", then "Calling…", then "Ringing…" with a ringback tone. | guide:430-433; ui/call.rs:373-388 | The call window | ui/calls | capture (call-outgoing) | hand | `parity/calls.md`; HANDTEST §7, §15 [CA-001] |
| CA-002 | Hanging up before it rings cancels the call before it reaches them. | guide:433 | | ui/calls | test (call-connecting) | done | `parity/calls.md` |
| CA-003 | An incoming call takes over with the caller's picture and name, "Incoming voice call" or "Incoming video call", and Accept and Decline. | guide:433-435; ui/call.rs:732-812 | The call window comes forward | ui/calls | capture (call) | done | `parity/calls.md`; HANDTEST §7, §15 [CA-003] |
| CA-004 | An incoming call also raises a notification and rings until answered, declined or given up, even with the window closed. | guide:435-437 | | sys | hand | hand | `parity/calls.md`; `parity/sys.md`; HANDTEST §7, §15 [CA-004] |
| CA-005 | The ringtone follows the notification sound: None or notifications off rings silently; muted, archived and locked chats stay quiet. | guide:437-440; app/calls.rs:444-509 | | sys | test | done | `parity/calls.md`; `parity/sys.md`; HANDTEST §15 [CA-005] |
| CA-006 | A connected call shows its length (m:ss, or h:mm:ss) and Mute (the other side is told), Devices, and Hang up along the bottom. | guide:443-446; ui/call.rs:478-590 | | ui/calls | capture (call-active) | done | `parity/calls.md` |
| CA-007 | "Hide the call screen" or Escape puts the call aside in a strip at the bottom with "Return to the call" and hang up; any chat can be used meanwhile. | guide:446-449; ui/call.rs:99-160, :311 | | ui/calls | capture (call-strip) | done | `parity/calls.md` |
| CA-008 | Escape leaves full screen first; only hanging up ends a call, and closing the window keeps it running. | guide:449-451; keys.rs:196-203 | | ui/calls | test | done | `parity/calls.md` |
| CA-009 | The call screen can go full screen and back. | ui/call.rs:285-330 | Native full screen | ui/calls | test | hand | `parity/calls.md`; HANDTEST §7, §15 [CA-009] |
| CA-010 | A call you end leaves at once; one the other side ends, declines or misses, or that fails, says so for 1.5 s ("Call ended", "Missed call", "Declined", "Busy", "Could not connect", "No answer", "Connection lost", answered or declined on another device). | guide:451-452; ui/call.rs:392-425 | | ui/calls | capture (call-ended) | done | `parity/calls.md` |
| CA-011 | In a video call their picture fills the window and yours sits in the top corner. | guide:454-455; ui/call.rs:332-358 | Camera permission, asked once | ui/calls | capture (call-video) | done | `parity/calls.md` |
| CA-012 | The camera button turns your camera off and on; in a voice call it asks to switch to video, and their request reads "Wants to switch to video: turn on your camera to answer". | guide:455-457; ui/call.rs:218, :543-557 | | ui/calls | capture (call-video-incoming) | done | `parity/calls.md` |
| CA-013 | Without a usable camera you still join video calls with yours off; their camera being off reads "Their camera is off". | guide:457-459; ui/call.rs:228 | | ui/calls | capture (call-video-off) | done | `parity/calls.md` |
| CA-014 | The microphone, speaker and camera pickers move the running call without changing the system defaults; a device that goes away falls back, and the screen says so. | guide:466-469; ui/call.rs:427-441, :593-720 | Core Audio and AVFoundation | ui/calls | capture (call-devices) | hand | `parity/calls.md`; HANDTEST §7, §15 [CA-014] |
| CA-015 | The screen says when no audio arrives, it cannot be decoded, or ours is not getting through. | ui/call.rs:461-474 | | ui/calls | test | done | `parity/calls.md` |
| CA-016 | A group call rings under the group's name and picture ("Group voice call from Jonas") with the faces already on it (+N). | guide:479-481; ui/call.rs:732-860 | | ui/calls | capture (call-group) | done | `parity/calls.md` |
| CA-017 | On a group call everyone has a tile (you as "You", with a red microphone while muted; "Invited" dimmed); tiles follow joins and leaves, cameras fill tiles, and hanging up leaves the call to the others. | guide:481-488; ui/call.rs:864-1225 | | ui/calls | capture (call-group-video) | hand | `parity/calls.md`; HANDTEST §7, §15 [CA-017] |
| CA-018 | A call link joins with "Connecting…" until admitted, then shows the call as a group call; hanging up stops the join. | guide:492-497; app/calls.rs:388 | | ui/calls | capture (call-link) | done | `parity/calls.md` |
| CA-019 | One call at a time: "Hang up the current call first"; a call to another of your numbers meanwhile is declined there; a number not on screen still rings under its own names. | guide:501-503; app/calls.rs:388-392, :585-592 | | logic | test | done | `parity/calls.md`; `parity/logic.md` |
| CA-020 | Calls are filed in their chats when they end, including the phone's, those missed while closed, and the call history sent at linking. | guide:505-510 | | logic | test | done | `parity/calls.md`; `parity/logic.md` |
| CA-021 | The Calls tab (rail, Ctrl+Shift+2) lists the number's calls newest first, folding runs with a count; each row shows the direction (missed in red), when and how it went, and a button that calls back the same way. | guide:512-518; ui/calls.rs:126-470 | ⌘⇧2 | ui/calls | capture (calls) | done | `parity/calls.md` |
| CA-022 | Calls search by name or number within the chip, All or Missed, which is remembered per number. | guide:518-521; ui/calls.rs:269-300 | | ui/calls | capture (calls-missed) | done | `parity/calls.md` |
| CA-023 | Clicking a row opens that person's chat beside the list at the call; with no chat open, the pane offers Start call. | guide:521-524; ui/calls.rs:642-678 | | ui/calls | capture (calls-chat) | done | `parity/calls.md` |
| CA-024 | New call (header, or Start call) lists the most-called people, then all contacts, searchable, each with voice and video buttons; a row alone starts nothing. | guide:524-526; ui/calls.rs:476-640 | | ui/calls | capture (new-call) | done | `parity/calls.md` |
| CA-025 | A call-link call shows as "Call link" with a link picture, with no chat to open and no one to call back. | guide:497-499; ui/calls.rs:165-169 | | ui/calls | test | done | `parity/calls.md` |
| CA-026 | Empty states: "No calls yet", "Loading…", "No missed calls", "No matching calls", "No matching contacts". | ui/calls.rs:199-255, :543 | | ui/calls | capture (calls) | done | `parity/calls.md` |
| CA-027 | Chats, Ctrl+Shift+1 or Escape leaves Calls; switching numbers keeps the Calls tab on the other number. | guide:526-529; keys.rs:225-232 | ⌘⇧1 | ui/calls | test | done | `parity/calls.md` |
| CA-028 | In a narrow window the call list takes the window beside the rail, and a picked call's chat opens over both with Back. | guide:529-531; keys.rs:227-230 | | ui/calls | test | done | `parity/calls.md` |
| CA-029 | A ringing call asks for attention in the Dock. | plan Z4 | macOS only | sys | hand | hand | `parity/sys.md`; HANDTEST §15 [CA-029] |
| CA-030 | Clicking a call's notification brings the window to the call. | app.rs:1991-1993 | | sys | hand | hand | `parity/sys.md`; HANDTEST §15 [CA-030] |

## SE: Settings

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| SE-001 | Settings opens from the rail or Ctrl+, and closes with Back (Esc). | files:94; keys.rs:81; ui/settings.rs:265-273 | Its own window, ⌘, and ZapFast > Settings… | ui/settings | test (settings) | done | `parity/settings.md` |
| SE-002 | Changes are saved immediately. | files:94 | | ui/settings | test | done | `parity/settings.md` |
| SE-003 | The search field (Ctrl+F) finds a setting by name or description, in the interface language or English; no match reads "No settings match “…”."; Escape clears it first. | files:95-96; ui/settings.rs:290-326; keys.rs:223-224 | ⌘F | ui/settings | test | done | `parity/settings.md` |
| SE-010 | Appearance > Theme: dark, light, follow the system, Messages, or a local palette, with "Open themes folder" and "How to make a theme". | files:100-113; themes:17-19; ui/settings.rs:1123-1142 | | ui/settings | capture (themes) | done | `parity/settings.md` |
| SE-011 | Appearance > Font: System or Inter. | ui/settings.rs:357-360, :1160 | System is San Francisco | ui/settings | test | done | `parity/settings.md` |
| SE-012 | Appearance > Wallpaper opens its page: WhatsApp's light and dark colours or the theme's, "Add doodles", "Choose image…" and "Remove image", with a preview. | files:114-115, :21; ui/settings.rs:1249-1400 | | ui/settings | capture (wallpaper) | done | `parity/settings.md` |
| SE-013 | Appearance > Zoom: Larger and Smaller, beside the shortcuts. | files:116; ui/settings.rs:379-409 | | ui/settings | test | done | `parity/settings.md` |
| SE-014 | Appearance > Language: Auto or one of the 11 languages. | files:117-120; ui/settings.rs:417, :1183-1192 | | ui/settings | test | done | `parity/settings.md` |
| SE-020 | Chats > Enter sends. | files:135-136; ui/settings.rs:432-433 | | ui/settings | test | done | `parity/settings.md` |
| SE-021 | Chats > Download files automatically. | files:137-139; ui/settings.rs:437-440 | | ui/settings | test | done | `parity/settings.md` |
| SE-022 | Chats > Fetch older history, per number, off by default (behaviour in LG-013 and logic). | files:140-148; ui/settings.rs:445-448 | | ui/settings | test | done | `parity/settings.md` |
| SE-023 | Chats > Keep chats archived, on by default, for all accounts. | files:149-151; ui/settings.rs:461-470 | | ui/settings | test | done | `parity/settings.md` |
| SE-024 | Chats > Locked chats code: set or clear the local code. | files:155-156; ui/settings.rs:485-509 | | ui/settings | test | done | `parity/settings.md` |
| SE-030 | Notifications > Desktop notifications, per number. | files:160-161; ui/settings.rs:521-524 | | ui/settings | test | done | `parity/settings.md` |
| SE-031 | Notifications > Show message content in notifications, on by default, for all accounts. | files:162-166; ui/settings.rs:531-532 | | ui/settings | test | done | `parity/settings.md` |
| SE-032 | Notifications > Message sound and Mention sound: Pidgin, Pidgin alert, System default, None, or a file, with a Play preview. | files:167-171; ui/settings.rs:1928-1989 | System sounds replace Pidgin's; they play with the notification, so Focus silences them | ui/settings | hand | hand | `parity/settings.md`; HANDTEST §8 [SE-032] |
| SE-033 | Notifications > Play sounds for group messages. | files:172-173; ui/settings.rs:566-569 | | ui/settings | test | done | `parity/settings.md` |
| SE-034 | Notifications > Limit repeat notifications: Off, Direct messages, Groups, or both, per number. | files:174-178; settings.rs:381-383; ui/settings.rs:537-540 | | ui/settings | test | done | `parity/settings.md` |
| SE-040 | Privacy > Send read receipts, with "Off for your account, so only groups get them." when the account setting is off. | files:182-186; ui/settings.rs:577-584 | | ui/settings | test | done | `parity/settings.md` |
| SE-041 | Privacy > Show when you are typing. | files:187; ui/settings.rs:589 | | ui/settings | test | done | `parity/settings.md` |
| SE-042 | Privacy > account privacy (Last seen, Online, Profile photo, About, Groups, Read receipts, Calls) with WhatsApp's choices, changeable only while connected, with loading and error states. | files:188-190; privacy.rs:42-144; ui/settings.rs:600-640 | | ui/settings | capture (settings) | done | `parity/settings.md` |
| SE-050 | System > Keep running when the window closes. | files:195; ui/settings.rs:655-656; app.rs:1851 | Keeps the status item | ui/settings | test | done | `parity/settings.md` |
| SE-051 | System > Start at login; when approval is needed it says "Allow ZapFast in System Settings, under Login Items."; a failure toasts. | files:196-201; app.rs:8527-8546 | Generated login agent with `--start-hidden` (plan Packaging) | ui/settings | hand | hand | `parity/settings.md`; HANDTEST §16 [SE-051] |
| SE-052 | System > Check for updates and Download updates automatically. | files:202-205; ui/settings.rs:685-696 | | ui/settings | test | done | `parity/settings.md` |
| SE-053 | System > Proxy: `socks5h://`, `socks5://` or `http://` with optional `user:password@`, a bare `host:port` as HTTP; it reconnects at once; empty uses the environment and says which. | files:206-214; ui/settings.rs:709-720 | | ui/settings | test | done | `parity/settings.md` |
| SE-054 | System > GIPHY API key. | files:215-217; ui/settings.rs:740-749 | | ui/settings | test | done | `parity/settings.md` |
| SE-060 | Account: your name and About (edit, Save, Cancel), Change profile picture, and the linked-device line. | files:219-220; ui/settings.rs:765-776, :1618-1742 | | ui/settings | test | done | `parity/settings.md` |
| SE-061 | Account: Unlink this computer, Add account, Remove this account. | files:219-220; ui/settings.rs:774-776 | | ui/settings | test | done | `parity/settings.md` |
| SE-070 | Files: the message archive with Open folder, the downloads folder (Change…, Use default), and the log (Open). | files:220-221, :291; ui/settings.rs:781-859 | Opens in Finder through the core's `OpenPath` | ui/settings | test | done | `parity/settings.md` |
| SE-071 | Files > Storage used: the account's media cache split into images, videos and GIFs, stickers, and other, with Clear…. | files:30-34; ui/settings.rs:840-846, :1454-1526 | | ui/settings | capture (clear-media) | done | `parity/settings.md` |
| SE-072 | Downloads to a chosen folder keep documents' names, never replace a file (`name (1).pdf`), and turn refused characters into `_`; earlier downloads stay. | files:35-39 | | logic | test | hand | `parity/logic.md`; HANDTEST §12 [SE-072] |
| SE-073 | About: the version, Keyboard shortcuts, Source code, "Update to X" when one is ready, and the credit line. | ui/settings.rs:869-874, :1791-1848; ui/widgets.rs:880-893 | | ui/settings | capture (about) | done | `parity/settings.md` |
| SE-074 | Unknown settings from older versions are ignored and dropped at the next save. | files:242-244 | | ui/settings | test | done | `parity/settings.md` |

## AL: App lock

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| AL-001 | Settings > Privacy > App lock sets a password of at least six characters (typed twice), changes it (with the current one) or turns it off; errors are explained; "App lock password saved" or "App lock turned off". | files:248-249; ui/settings.rs:899-1047; app.rs:8683-8699 | Secure text fields | ui/settings | capture (app-lock-setup) | done | `parity/lock.md`; `parity/settings.md`; HANDTEST §11 [AL-001] |
| AL-002 | Lock after 1 minute, 15 minutes (default) or 1 hour without input; time hidden counts, and recording counts as use. | files:249-251; ui/settings.rs:926-937; app.rs:8548-8577 | | logic | test (app-lock-settings) | done | `parity/lock.md`; `parity/logic.md` |
| AL-003 | With a password set, ZapFast starts locked. | files:249-250 | | ui/lock | test | done | `parity/lock.md` |
| AL-004 | "Lock ZapFast" in the status item and Ctrl+Shift+L lock at once. | files:252; keys.rs:42-48; app.rs:1084 | ⌘⇧L | ui/lock | test | done | `parity/lock.md` |
| AL-005 | The lock screen alone shows: "ZapFast is locked", a focused password field and Unlock. | files:254; ui/lock.rs:12-97 | Secure input | ui/lock | capture (app-lock) | done | `parity/lock.md`; HANDTEST §11 [AL-005] |
| AL-006 | A wrong password says so, and each try waits longer, up to half a minute ("Too many wrong passwords. Try again in N s."). | files:259; ui/lock.rs:76-85 | | ui/lock | capture (app-lock-wrong) | done | `parity/lock.md` |
| AL-007 | "Forgot password? Unlink this computer" confirms, then unlinks, deletes the chats here and turns the lock off ("Unlinking this computer…"). | files:259-262; ui/lock.rs:23, :108-142 | | ui/lock | capture (app-lock-forgot) | done | `parity/lock.md` |
| AL-008 | Locking keeps messages arriving unread, stops voice and video, discards a recording, closes the locked folder, and withdraws shown notifications. | files:254-257; app.rs:8579-8613 | | logic | test | done | `parity/av.md`; `parity/logic.md` |
| AL-009 | While locked only unlocking, showing or hiding the window, closing, quitting and zoom work. | ui/lock.rs:30-31; app.rs:9734-9753 | Other menu items disabled | ui/lock | test | done | `parity/lock.md` |
| AL-010 | Notification clicks and links wait for the unlock, then open in the order they came. | guide:680-681; files:256; app.rs:8615-8655 | | logic | test | done | `parity/logic.md` |
| AL-011 | Any unlink, including from the phone, turns the app lock off. | files:261-262; app.rs:8703-8712 | | logic | test | done | `parity/lock.md`; `parity/logic.md` |
| AL-012 | Only a salted PBKDF2 verifier of the password is stored. | files:264-266 | | logic | test | done | `parity/lock.md`; `parity/logic.md` |

## UP: Updates

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| UP-001 | With Check for updates on, ZapFast asks once a day for a newer release, sending only its version. | files:272-273; ui/settings.rs:688 | A local test feed until a release feed exists, and Settings says so (plan, decided) | ui/settings | test | done | `parity/settings.md` |
| UP-002 | A newer release shows the banner "ZapFast X is available"; Update opens the update screen with the release notes. | files:273; ui/mod.rs:521-530, :589-595; ui/update.rs:24-57 | | ui/settings | capture (update) | done | `parity/settings.md` |
| UP-003 | The update screen moves through "Checking download…", "Downloading update…", "Ready to install" (finish unsent work first) with "Restart to update", "Preparing to restart…", and on failure "Retry download" or "Download from GitHub". | files:273-274; ui/update.rs:57-137 | | ui/settings | capture (update-ready) | done | `parity/settings.md`; HANDTEST §17 [UP-003] |
| UP-004 | Downloads are checked against the release's SHA-256 manifest with a valid Ed25519 signature, or the update stops; a backup comes back if the new version cannot start. | files:274-278 | | ui/settings | test (update-failed) | done | `parity/settings.md` |
| UP-005 | Download updates automatically fetches and verifies in the background; restarting stays your choice. | files:203-205; app.rs:5682-5722 | | ui/settings | test (update-downloading) | done | `parity/settings.md` |
| UP-006 | The app in Applications updates itself; a Homebrew install updates through Homebrew. | files:281-285 | | ui/settings | hand | done | `parity/settings.md`; HANDTEST §17 [UP-006] |

## FL: Files, archive and log

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| FL-001 | Each linked number has its own folder for session, archive, settings, stickers, wallpaper and cache; caches are safe to delete. | files:9-24 | Application Support and Caches under `ai.a3lab.zap`, never upstream's data (plan D51) | sys | test | done | `parity/sys.md` |
| FL-002 | The configuration, state and cache folders are restricted to the user (0700), or ZapFast stops at startup. | files:57-59 | | sys | test | done | `parity/sys.md` |
| FL-003 | The archive's key lives in the Keychain; a plaintext archive is never written. | files:63-71, :82-86 | Keychain items of the new identity | sys | hand | hand | `parity/sys.md`; HANDTEST §10, §16 [FL-003] |
| FL-004 | Each run replaces the log, which holds warnings and errors and never message contents, numbers or keys. | files:287-291; agents Privacy | | sys | test | done | `parity/sys.md`; HANDTEST §16 [FL-004] |
| FL-005 | Panics are written to `panic.log`. | files:24 | | sys | hand | done | `parity/sys.md` |
| FL-006 | `settings.json` and `accounts.json` are plain JSON and safe to edit. | files:15-16 | | ui/settings | test | done | `parity/settings.md` |

## TH: Themes

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| TH-001 | Built-in dark and light themes. | files:100; readme:58 | Rex roles (plan "Look: full Rex") | theme | capture (light) | done | `parity/theme.md` |
| TH-002 | Follow system switches with the system appearance as it changes. | files:100; themes:88-92 | `NSApp.effectiveAppearance` | theme | hand | done | `parity/theme.md`; HANDTEST §8 [TH-002] |
| TH-003 | Messages: the system's light or dark in macOS greys, bubbles shaped and coloured as Messages draws them (yours in iMessage blue, deeper down the window), system blue, a frosted header the messages scroll under, photos without a bubble, a translucent sidebar. | files:100-113; ui/conversation.rs:29-47, :317-394; ui/glass.rs | AppKit's own materials | theme | capture (messages) | done | `parity/conversation.md`; `parity/theme.md` |
| TH-004 | A JSON file in the themes folder is a theme named after its file: `base` dark or light, colours as `#RRGGBB` or `#RRGGBBAA`, missing ones from the base. | themes:7-45 | Themes folder under the new identity's Application Support | theme | test | done | `parity/theme.md` |
| TH-005 | The 21 colour roles map onto Rex roles; missing chat colours are derived (chat from window, bubbles from surface and accent, link and read from accent, overlay from panel). | themes:47-76; plan "Themes" | | theme | test | done | `parity/theme.md` |
| TH-006 | The built-in palettes (Catppuccin, Nord, Rose Pine, …) are written once as files at first start and never rewritten; a deleted one stays deleted. | themes:21-24 | | theme | test | done | `parity/theme.md` |
| TH-007 | `zapfast reload-themes` refreshes the list and the chosen theme in the running app, and never starts a stopped one. | themes:80-82 | | sys | test | done | `parity/sys.md` |
| TH-008 | A broken theme file is skipped with its reason in the log, and the last working palette stays. | themes:84-86 | | theme | test | done | `parity/theme.md` |

## NT: Notifications, Dock and status item

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| NT-001 | A new message in a chat you are not looking at raises a notification with the chat, the sender, the text and a picture. | guide:757-759; files:160-161; app.rs:2010-2083 | `UNUserNotificationCenter`, asking permission once | sys | hand | hand | `parity/sys.md`; HANDTEST §14 [NT-001] |
| NT-002 | No notification while you read that chat in the focused window, for muted, archived or locked chats, or for messages over a minute old (history). | guide:765-766; app.rs:2022-2029, :9776-9781 | | logic | test | done | `parity/logic.md` |
| NT-003 | Clicking a notification opens its chat at the message it announced (for the 32 most recent), switching accounts and showing the window. | guide:758-760; app.rs:1966-1997 | | sys | hand | hand | `parity/sys.md`; HANDTEST §14 [NT-003] |
| NT-004 | Reading a chat, here or on another device, withdraws its notifications. | guide:761, :771-772; app.rs:1999-2008 | | sys | test | done | `parity/sys.md`; HANDTEST §14 [NT-004] |
| NT-005 | With content off, or behind the app lock, a notification says only "New message" and plays only the message sound; clicking still opens the message. | files:162-166, :255-256; app.rs:2085-2117 | | logic | test | done | `parity/logic.md` |
| NT-006 | A limited chat notifies once, then stays quiet for 10 minutes or until read; a mention of you or a reply to you in a group always notifies. | guide:769-772; files:174-178; app.rs:2030-2045 | | logic | test | done | `parity/logic.md` |
| NT-007 | The sound is the chat's own, else the mention sound for mentions and replies in groups, else the message sound; with group sounds off, only mentions and replies sound in groups. | files:167-173; app.rs:9717-9732 | System sounds; Focus silences them | logic | test | done | `parity/logic.md` |
| NT-008 | The Dock icon shows the number of unread chats across accounts and keeps it when the window closes and reopens. | guide:754-756; app.rs:8714-8721 | macOS only | sys | hand | hand | `parity/sys.md`; HANDTEST §14 [NT-008] |
| NT-009 | The menu-bar status item (template icon) has Show or hide ZapFast, Lock ZapFast (only with an app-lock password), and Quit; its menu never names a chat. | guide:738-740; app.rs:1066-1090 | `NSStatusItem` | sys | hand | hand | `parity/sys.md`; HANDTEST §14 [NT-009] |
| NT-010 | Closing the window keeps ZapFast linked in the background when "Keep running" is on, and quits otherwise. | guide:738; files:195; app.rs:1851-1853 | | sys | hand | done | `parity/sys.md`; HANDTEST §14 [NT-010] |
| NT-011 | Launching the app again reopens the existing window instead of a second copy. | guide:740-741; links:16-18 | LaunchServices and `app/instance` | sys | hand | hand | `parity/sys.md`; HANDTEST §16 [NT-011] |
| NT-012 | Quit works from the status item, the Dock menu, Ctrl+Q, and `zapfast quit` (which does nothing when none runs). | guide:741-746 | Dock menu Quit; ⌘Q | sys | hand | hand | `parity/sys.md`; HANDTEST §16 [NT-012] |
| NT-013 | Started at login, ZapFast runs hidden; the first window opens from the status item. | guide:756-757; files:196-201; app.rs:1028 | `--start-hidden`, no window, no bounce (plan Packaging) | sys | hand | hand | `parity/sys.md`; HANDTEST §16 [NT-013] |
| NT-014 | After the machine wakes every account reconnects, and settings are saved before sleep. | app.rs:8477-8499 | Wake comes from NSWorkspace (upstream relies on the worker's link watch) | sys | hand | hand | `parity/sys.md`; HANDTEST §16 [NT-014] |
| NT-015 | Reply from the notification. | plan Z4 | A Go-app addition: upstream has no notification reply | sys | hand | hand | `parity/sys.md`; HANDTEST §14 [NT-015] |

## LN: Links

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| LN-001 | A bare number, `wa.me/…`, `api.whatsapp.com/send?phone=…` and `whatsapp://send?phone=…` all name the same chat, which opens. | guide:654-666 | Registered as a URL type (`CFBundleURLTypes`, plan Packaging), which upstream's Mac app lacks | sys | test | done | `parity/sys.md` |
| LN-002 | The `wa:` scheme opens chats too. | guide:654 | The plan registers `whatsapp`; `wa` needs the same entry | sys | test | done | `parity/sys.md` |
| LN-003 | A `text=` template lands in the composer unsent, keeps what you were typing, and never replaces a draft. | guide:666-670; app.rs:1550-1557 | | ui/composer | test | done | `parity/composer.md` |
| LN-004 | A `chat.whatsapp.com` link given to ZapFast shows the invite and lets you choose whether to join. | guide:672-674; app.rs:1941-1945 | | sys | test | done | `parity/sys.md` |
| LN-005 | With ZapFast running, a link brings its window forward on that chat; otherwise ZapFast starts and opens it. | guide:676-678; app.rs:1929-1964 | | sys | hand | hand | `parity/sys.md`; HANDTEST §16 [LN-005] |
| LN-006 | An unreadable argument opens nothing and says so. | guide:687-688 | | sys | test | done | `parity/sys.md` |
| LN-007 | The command line takes a link or number, `quit` and `reload-themes`, talking to the running copy. | guide:658-662, :744-746; themes:80-82 | A CLI entry inside the bundle | sys | hand | hand | `parity/sys.md`; HANDTEST §16 [LN-007] |

## AX: Accessibility

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| AX-001 | Confirmations, errors, connection changes and history loading are announced without moving focus; while locked only connection and history changes are. | guide:779-781; ui/mod.rs:134-140 | VoiceOver live regions | ui/shell | hand | done | `parity/shell.md`; HANDTEST §9, §11 [AX-001] |
| AX-002 | Chat rows read their name and unread state ("3 unread messages, mentioned you"; "unread" for a chat marked unread). | ui/chats.rs:2108-2118 | | ui/chatlist | test | done | `parity/chatlist.md` |
| AX-003 | VoiceOver reads the chat list and the transcript and announces a new message. | plan exit criterion 8 | VoiceOver | ui/conversation | hand | hand | `parity/conversation.md`; HANDTEST §9 [AX-003] |
| AX-004 | Icon-only controls carry labels (header buttons, the pin line "Pinned message 1 of 3: …", the react control, account rows, label colours). | ui/conversation.rs:252-258, :3659-3668; ui/accounts.rs:222; ui/labels.rs:65 | | ui/shell | test | done | `parity/shell.md` |

## I18N: Languages

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| I18N-001 | The interface is in English, Portuguese (Brazil), German, Spanish, Italian, French, Russian, Chinese (Simplified and Traditional), Turkish and Indonesian, from upstream's catalogs, falling back to English. | readme:31-33; files:117-119; i18n.rs:13-56 | | i18n | test | done | `parity/i18n.md` |
| I18N-002 | Auto follows the system's language. | files:117 | The preferred-languages list | i18n | test | done | `parity/i18n.md` |
| I18N-003 | Message contents, names and logs are never translated. | files:120 | | i18n | test | done | `parity/i18n.md` |
| I18N-004 | Plurals and contexts follow the catalogs (`ngettext`, `pgettext`). | i18n.rs:8 | | i18n | test | done | `parity/i18n.md` |
| I18N-005 | Key names in labels and tooltips show ⌘ and ⌥. | keys.rs:666-675 | macOS only | i18n | test | done | `parity/i18n.md` |

## KB: Keyboard shortcuts

The 29 rows KB-001 to KB-029 are upstream's `SHORTCUTS` table (`ui/keys.rs:622-664`), in order; KB-030 onward are keys it handles without listing them.

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| KB-001 | Ctrl+K or Ctrl+Shift+F focuses the chat search. | keys.rs:623, :34-49 | ⌘K, ⌘⇧F | ui/shell | test | done | `parity/shell.md` |
| KB-002 | Ctrl+F searches the open chat, or the chat list with none open, or the settings on Settings. | keys.rs:624, :577-585 | ⌘F; Edit > Find… | ui/shell | test | done | `parity/shell.md` |
| KB-003 | Up and Down then Enter walk the open chat's search results and jump to one. | keys.rs:625-628 | | ui/conversation | test | done | `parity/conversation.md` |
| KB-004 | Ctrl+L focuses the message field (a chat open, no overlay). | keys.rs:629, :67-76 | ⌘L | ui/shell | test | done | `parity/shell.md` |
| KB-005 | Alt+Up and Alt+Down open the previous or next chat in the visible list, wrapping, without leaving the composer. | keys.rs:630, :254-311 | ⌥↑, ⌥↓ | ui/shell | test | done | `parity/shell.md` |
| KB-006 | Ctrl+Shift+[ and ] do the same, as in WhatsApp (both bracket spellings). | keys.rs:631, :258-274 | ⌘⇧[, ⌘⇧] | ui/shell | test | done | `parity/shell.md` |
| KB-007 | Ctrl+1 to Ctrl+9 open the chat at that place in the list. | keys.rs:632, :349-380 | ⌘1 to ⌘9 | ui/shell | test | done | `parity/shell.md` |
| KB-008 | Ctrl+Shift+1 shows Chats (by the key's position, whatever the layout). | keys.rs:633, :94-103, :382-408 | ⌘⇧1 | ui/shell | test | done | `parity/shell.md` |
| KB-009 | Ctrl+Shift+2 shows Calls. | keys.rs:634, :94-103 | ⌘⇧2 | ui/shell | test | done | `parity/shell.md` |
| KB-010 | Up in an empty composer edits your most recent editable message. | keys.rs:635, :312-336 | | ui/composer | test | done | `parity/composer.md` |
| KB-011 | Enter sends; Shift+Enter adds a line. | keys.rs:636 | | ui/composer | test | done | `parity/composer.md` |
| KB-012 | Escape dismisses the current action, returns from search, Calls or Starred, or closes the chat (full order in SH-017). | keys.rs:637-640, :164-247 | | ui/shell | test | done | `parity/shell.md` |
| KB-013 | Ctrl+N opens New chat (when linked). | keys.rs:641, :53-59 | ⌘N | ui/shell | test | done | `parity/shell.md` |
| KB-014 | Ctrl+E archives or unarchives the open chat. | keys.rs:642, :587-601 | ⌘E | ui/shell | test | done | `parity/shell.md` |
| KB-015 | Ctrl+Shift+A switches to the next linked number. | keys.rs:643, :603-619 | ⌘⇧A | ui/shell | test | done | `parity/shell.md` |
| KB-016 | Ctrl+V pastes text, or stages a picture from the clipboard. | keys.rs:644-647; app.rs:9276-9363 | ⌘V | ui/composer | test | done | `parity/composer.md` |
| KB-017 | Ctrl+C copies the selected messages. | keys.rs:648, :109-133 | ⌘C | ui/shell | test | done | `parity/shell.md` |
| KB-018 | Ctrl+B collapses or expands the chat list. | keys.rs:649, :80 | ⌘B | ui/shell | test | done | `parity/shell.md` |
| KB-019 | Ctrl+End jumps to the newest message. | keys.rs:650, :93 | ⌘End (fn⌘→ on a laptop keyboard) | ui/conversation | test | done | `parity/conversation.md` |
| KB-020 | Page Up and Page Down scroll the open chat by a page, even from the composer. | keys.rs:651, :134-163 | fn↑, fn↓ | ui/conversation | test | done | `parity/conversation.md` |
| KB-021 | Home and End go to the top or bottom of the open chat when the field is empty. | keys.rs:652-655, :146-161 | fn←, fn→ | ui/conversation | test | done | `parity/conversation.md` |
| KB-022 | Ctrl+, opens or closes Settings. | keys.rs:656, :81 | ⌘, opens the Settings window | ui/shell | test | done | `parity/shell.md`; HANDTEST §2 [KB-022] |
| KB-023 | Ctrl++ and Ctrl+- zoom in and out (Ctrl+= too). | keys.rs:657, :89-91 | ⌘=, ⌘- | ui/shell | test | done | `parity/shell.md` |
| KB-024 | Ctrl+0 resets the zoom. | keys.rs:658, :92 | ⌘0 | ui/shell | test | done | `parity/shell.md` |
| KB-025 | Left and Right step through the media viewer. | keys.rs:659; image_preview.rs:59-62 | | ui/viewer | test | done | `parity/viewer.md` |
| KB-026 | ? (when not typing) or Ctrl+/ lists the shortcuts. | keys.rs:660, :60-66, :84-88 | ⌘/ | ui/shell | test | done | `parity/shell.md` |
| KB-027 | Ctrl+Shift+L locks ZapFast when an app-lock password is set. | keys.rs:661, :42-48 | ⌘⇧L | ui/lock | test | done | `parity/lock.md` |
| KB-028 | Ctrl+W closes the window; ZapFast stays running. | keys.rs:662, :83 | ⌘W; the status item stays | ui/shell | test | hand | `parity/shell.md`; HANDTEST §2 [KB-028] |
| KB-029 | Ctrl+Q quits. | keys.rs:663, :82 | ⌘Q | ui/shell | test | hand | `parity/shell.md`; HANDTEST §2 [KB-029] |
| KB-030 | While recording, Enter sends the recording and Escape discards it. | keys.rs:171-172, :248-253 | | ui/composer | test | done | `parity/composer.md` |
| KB-031 | In the viewer, + − 0 zoom with or without Ctrl, and Ctrl+C copies the photo. | image_preview.rs:48-58; keys.rs:444-455 | ⌘ | ui/viewer | test | done | `parity/viewer.md` |
| KB-032 | Chat shortcuts are ignored while a dialog, the picker, a reaction, a recording or the update screen is up. | keys.rs:67-80, :589-619 | | ui/shell | test | done | `parity/shell.md` |
| KB-033 | Mention and emoji suggestions take Up, Down, Tab, Enter and Escape before the composer. | plan composer; ui/conversation.rs:1042-1066, :1482-1490 | Host commands through `doCommandBySelector:` | ui/composer | test | done | `parity/composer.md` |

## MB: macOS menu bar

Upstream's menu, `macos.rs:47-110`, one row per item. The Go app builds it natively (plan Z2); a text field's Edit commands go through the responder chain.

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| MB-001 | ZapFast > About ZapFast opens About. | macos.rs:50, :223 | macOS only | ui/shell | test | done | `parity/shell.md` |
| MB-002 | ZapFast > Settings… (⌘,) opens Settings. | macos.rs:52, :224 | Settings window | ui/shell | test | done | `parity/shell.md` |
| MB-003 | ZapFast > Services is the system's Services menu. | macos.rs:54 | macOS only | ui/shell | hand | hand | `parity/shell.md`; HANDTEST §2 [MB-003] |
| MB-004 | ZapFast > Hide ZapFast (⌘H). | macos.rs:56 | macOS only | ui/shell | hand | hand | `parity/shell.md`; HANDTEST §2 [MB-004] |
| MB-005 | ZapFast > Hide Others (⌥⌘H). | macos.rs:57 | macOS only | ui/shell | hand | hand | `parity/shell.md`; HANDTEST §2 [MB-005] |
| MB-006 | ZapFast > Show All. | macos.rs:58 | macOS only | ui/shell | hand | hand | `parity/shell.md`; HANDTEST §2 [MB-006] |
| MB-007 | ZapFast > Quit ZapFast (⌘Q). | macos.rs:60, :227 | macOS only | ui/shell | test | hand | `parity/shell.md`; HANDTEST §2 [MB-007] |
| MB-008 | File > New Chat… (⌘N). | macos.rs:64, :225 | macOS only | ui/shell | test | done | `parity/shell.md` |
| MB-009 | File > Close Window (⌘W). | macos.rs:66, :226 | macOS only | ui/shell | test | hand | `parity/shell.md`; HANDTEST §2 [MB-009] |
| MB-010 | Edit > Undo (⌘Z), in the focused field. | macos.rs:72, :158-219 | macOS only | ui/shell | test | hand | `parity/shell.md`; HANDTEST §2 [MB-010] |
| MB-011 | Edit > Redo (⇧⌘Z). | macos.rs:73 | macOS only | ui/shell | test | hand | `parity/shell.md`; HANDTEST §2 [MB-011] |
| MB-012 | Edit > Cut (⌘X). | macos.rs:75 | macOS only | ui/shell | test | hand | `parity/shell.md`; HANDTEST §2 [MB-012] |
| MB-013 | Edit > Copy (⌘C), which copies the selected messages when a selection exists. | macos.rs:76; keys.rs:126-133 | macOS only | ui/shell | test | done | `parity/shell.md` |
| MB-014 | Edit > Paste (⌘V), which stages pictures and files in the composer. | macos.rs:77, :191-198 | macOS only | ui/shell | test | done | `parity/shell.md` |
| MB-015 | Edit > Select All (⌘A). | macos.rs:78 | macOS only | ui/shell | test | hand | `parity/shell.md`; HANDTEST §2 [MB-015] |
| MB-016 | Edit > Find… (⌘F) focuses the chat search. | macos.rs:80, :228 | macOS only | ui/shell | test | done | `parity/shell.md` |
| MB-017 | View > Toggle Sidebar (⌘B) hides or shows the chat list. | macos.rs:84, :229 | macOS only | ui/shell | test | done | `parity/shell.md` |
| MB-018 | View > Zoom In (⌘=). | macos.rs:86, :230 | macOS only | ui/shell | test | done | `parity/shell.md` |
| MB-019 | View > Zoom Out (⌘-). | macos.rs:87, :231 | macOS only | ui/shell | test | done | `parity/shell.md` |
| MB-020 | View > Actual Size (⌘0). | macos.rs:88, :232 | macOS only | ui/shell | test | done | `parity/shell.md` |
| MB-021 | View > Enter Full Screen (⌃⌘F). | macos.rs:90 | macOS only | ui/shell | hand | done | `parity/shell.md`; HANDTEST §2 [MB-021] |
| MB-022 | Window > Minimize (⌘M). | macos.rs:94 | macOS only | ui/shell | hand | hand | `parity/shell.md`; HANDTEST §2 [MB-022] |
| MB-023 | Window > Zoom. | macos.rs:95 | macOS only | ui/shell | hand | hand | `parity/shell.md`; HANDTEST §2 [MB-023] |
| MB-024 | Window > Show ZapFast shows the window. | macos.rs:97, :235 | macOS only | ui/shell | test | done | `parity/shell.md`; HANDTEST §2 [MB-024] |
| MB-025 | Window is the app's Windows menu, listing its windows. | macos.rs:105 | macOS only | ui/shell | hand | hand | `parity/shell.md`; HANDTEST §2 [MB-025] |
| MB-026 | Help > Keyboard Shortcuts (⌘/). | macos.rs:101, :233 | macOS only | ui/shell | test | done | `parity/shell.md` |
| MB-027 | Help > ZapFast Help opens `https://zapfast.rocks/using-zapfast/`. | macos.rs:102, :234 | macOS only | ui/shell | test | done | `parity/shell.md` |
| MB-028 | Help is the app's Help menu, with the system's search field. | macos.rs:106 | macOS only | ui/shell | hand | hand | `parity/shell.md`; HANDTEST §2 [MB-028] |
| MB-029 | A menu command that needs the window (a dialog, a page, Find) shows the hidden window first; edit commands are dropped while it is hidden. | macos.rs:240-263 | macOS only | ui/shell | test | hand | `parity/shell.md`; HANDTEST §2 [MB-029] |

## DM: Demo pages and tours

| ID | Behaviour | Source | macOS | Owner | Verify | Status | Evidence |
|---|---|---|---|---|---|---|---|
| DM-001 | `--demo` runs on the demo core: offline sample chats, no connection, no archive, no status item, beside the real app. | demo:3-8, :31-35; plan "The demo core" | | ui/shell | test | done | `parity/shell.md`; HANDTEST §1 [DM-001] |
| DM-002 | `--demo-page NAME[,light]` opens a page in a given state; every page below has a Go capture. | demo:9-10, :56-112 | | ui/shell | capture | partial | `parity/shell.md` |
| DM-003 | `--demo-shot FILE` renders a page without desktop content, for the content comparison. | demo:10, :113-119; plan "Testing" 3 | GnuiShot | ui/shell | capture | done | `parity/shell.md` |
| DM-004 | The launch tour (`--demo-tour`, 41 s, Space starts or replays): search, keyboard chat switching, scrolling, right-click Reply, fast typing, emoji and mention completion, GIF search, a still sticker, group info, the shortcut list, themes through Settings; silent; holds its last frame. | demo:11, :42-54 | | ui/shell | hand | partial | `parity/shell.md` |
| DM-005 | The what's-new tour (`--demo-tour-script whats-new`, 86 s): the plus menu and poll dialog, chat search by day, the photo viewer, videos and round videos in place, sticker shelves and search, group message info, Favorites and label chips and a chat's menu, recording and speeds, the avatar rail, hover controls, Ctrl-click and Shift-click with Forward, Settings languages, search and light theme. | demo:56-63; demo/tour/whats_new.rs:1-3 | | ui/shell | hand | partial | `parity/shell.md` |
| DM-006 | Tour options: `--demo-tour-delay`, `--demo-tour-events` (pointer and shortcut labels only, no typed text), `--demo-tour-frames DIR` with `--demo-fps` on a virtual clock, `--demo-size`, `--demo-hover X,Y`. | demo:12-13, :39, :53, :127-149 | | ui/shell | hand | done | `parity/shell.md` |
| DM-007 | Demo recording plays a synthetic tone and never opens the microphone. | demo:64 | | ui/composer | test | done | `parity/composer.md`; HANDTEST §5 [DM-007] |
| DM-008 | Demo GIF search and stickers use local CC0 fixtures; a local responder answers outgoing messages. | demo:46-52; demo/tour/session.rs:1 | | ui/picker | test | done | `parity/picker.md` |

**Demo page coverage.** Each upstream page (`demo.rs:1958-4300`) needs a Go equivalent for DM-002; most are named in the rows above.
- Chats and list: `chat`, `empty`, `short-chat`, `wide`, `nosidebar`, `rail`, `drafts`, `unread`, `private`, `favorites`, `groups`, `archived`, `labels`, `label-chips`, `label-filter`, `labels-dialog`, `search`, `starred`, `star-menu`, `chat-menu`, `pinned`, `account-menu`, `accounts`, `accounts-closed`, `offline`, `syncing`, `toasts`.
- Conversation: `chat-header-menu`, `history-date`, `bubbles`, `photos`, `quotes`, `quote-jump`, `unread-divider`, `scrolled`, `reactions`, `typing`, `typers`, `failed`, `channel`, `left-group`, `meta-ai`, `rtl`, `rtl-self`, `arabic-reply`, `receipts-off`, `receipts-played`, `disappearing`, `disappearing-direct`, `disappearing-off`, `live`, `live-phone`, `shared-contact`, `phone-menu`, `invite`, `select`, `select-delete`, `delete-selection`, `delete-message`, `delete-message-mine`, `call-entries`.
- Media: `motion`, `motion-playing`, `motion-preview`, `motion-viewer`, `video`, `video-playing`, `video-expanded`, `note-playing`, `media-album`, `media-album-failed`, `media-album-partial`, `media-album-oversized`, `media-album-midnight`, `media-album-before`, `preview`, `audio-types`, `voice`, `voice-menu`, `recording`, `unsent-voice`.
- Interactive and polls: `interactive`, `interactive-media`, `interactive-actions`, `interactive-list`, `interactive-list-dialog`, `carousel`, `carousel-pair`, `carousel-viewer`, `poll`, `poll-create`, `poll-empty`, `poll-voted`, `poll-results`.
- Composer and pickers: `composer-tools`, `typing`, `mention`, `mention-everyone`, `emoji-complete`, `compose-emoji`, `reply`, `private-reply`, `private-reply-menu`, `private-reply-locked-source`, `edit`, `staged`, `crop`, `attachment-draft-away`, `attachment-draft-return`, `location`, `picker`, `gifs`, `gifs-badkey`, `stickers`, `sticker-favorites`, `sticker-received`, `sticker-pack`, `sticker-search`, `sticker-animated`, `sticker-add`, `sticker-view`, `sticker-maker`, `sticker-pack-message`, `sticker-pack-view`, `react-menu`, `react-picker`, `react-picker-empty`, `react-custom`, `react-other`.
- Dialogs and panels: `new-chat`, `new-contact`, `new-contact-number`, `unnamed-group`, `forward`, `forward-selected`, `forward-review`, `forward-full`, `forward-once`, `forward-portuguese`, `delete-chat`, `clear-chat`, `leave-group`, `leave-channel`, `unlink`, `clear-media`, `remove-pack`, `shortcuts`, `about`, `info`, `info-media`, `info-docs`, `info-links`, `group-info`, `group-info-rename`, `group-info-locked`, `group-info-saving`, `message-info`, `message-info-unknown`, `message-info-partial`, `message-info-direct`, `chat-search`, `chat-search-rtl`, `chat-search-day`.
- Locks and linking: `locked`, `locked-open`, `locked-prompt`, `locked-setup`, `keyring`, `app-lock`, `app-lock-wrong`, `app-lock-forgot`, `app-lock-settings`, `app-lock-setup`, `login`, `pair`, `phone`.
- Calls: `call`, `call-active`, `call-outgoing`, `call-connecting`, `call-failed`, `call-devices`, `call-ended`, `call-group`, `call-group-active`, `call-group-video`, `call-group-muted`, `call-link`, `call-strip`, `call-video`, `call-video-incoming`, `call-video-off`, `calls`, `calls-chat`, `calls-info`, `new-call`, `calls-missed`.
- Settings and themes: `settings`, `wallpaper`, `wallpaper-image`, `themes`, `light`, `messages`, `messages-light`, `update`, `update-downloading`, `update-ready`, `update-failed`. (`omarchy` and `omarchy-light` are not parity items.)

## Not parity items

**What ZapFast deliberately does not do** (`agents:7-34`, "Product boundaries"; `product:32-35`):
- No browser engine, telemetry, hosted backend or ZapFast-operated account system, and no feature sends message content to a third party. These are constraints the Go app keeps (PR-006), not features to build.
- No reimplementing pieces of the WhatsApp protocol outside whatsapp-rust, and no capability advertised just because a protobuf field exists.
- Current limitations are not exclusions, but until upstream adds them they are not parity items either (`what:60-77`, `guide:330-332`, `:701`): starting group calls, creating call links, joining a group's ongoing call from its chat, screen sharing; status posts, communities, publishing to channels, group administration beyond the name, photo and description; forms, payments, shopping, carousel selections and forwarding interactive messages; embedded videos, documents and text-less templates; in-app playback of anything but H.264 in MP4; sending from locked chats; following a live location.

**The plan's own list** (`plan:288-291`, and the platform scope at `plan:25-28`):
- What upstream does not do on macOS: pausing other media is a no-op there (`media_pause.rs:14-20`), so Settings > Chats > "Pause other media while recording or playing" is not built for the Mac; deep links are CLI-only upstream, and the URL type the Go app adds is tracked as LN-001.
- Linux- and Windows-only features: Omarchy theme integration (and the `omarchy` demo pages), Flatpak and its portals, the drawn title bar (Settings > Appearance > Custom title bar), Unity launcher badges, the Windows taskbar overlay, KDE taskbar attention, Wayland placement, and the 30-second wait for a desktop tray.
- Pidgin's GPL-2.0 sounds: replaced by system sounds, a known difference (CL-045, SE-032, NT-007).
- Also outside this phase: the Go UI on Windows and Linux; real WhatsApp accounts in automated tests; upstream's other install channels (portable builds, the Windows installer, AppImage, AUR, DEB, RPM, Flatpak, Nix, Cargo); and moving FastsApp or pre-0.19 data (`files:48-55`), since the Go app has its own identity and data (plan D51).
