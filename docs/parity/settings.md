# Parity: Settings and updates (`gnui/ui/settings`)

`docs/PARITY.md` holds the status of record for every row; this file holds the evidence (tests, captures, hand steps and notes).

Status of every row of `docs/PARITY.md` whose Go owner is `ui/settings`, checked against upstream's `src/ui/settings.rs`, `src/ui/update.rs` and `src/updates.rs` on 2026-10-21. Written for pass 2 of the Settings port; `docs/PARITY.md` is untouched.

Tests are in `gnui/ui/settings` unless a package is named. Captures: `ZF_SHOTS=<dir> go test ./ui/settings -run TestShots` writes the top of the page and the wallpaper page. Status is one of done, partial, todo, waived, hand.

| id | status | evidence | note |
|---|---|---|---|
| SE-001 | done | TestEscapeStepsBack | Escape leaves the wallpaper page, then clears the search, then closes the window (also in an empty search field). The rail button and ⌘, belong to the shell. |
| SE-002 | done | TestSwitchesChangeSettings, TestSettingsPersistAcrossARestart | Edits are in memory; the state saves them (`State.SettingsChanged`), and a change survives a restart on a temp data dir. |
| SE-003 | done | TestSearchFindsSettings, TestSearchInTheInterfaceLanguage, TestFindFocusesTheSearch, TestEscapeStepsBack | Name or description, interface language or English, accents folded; "No settings match “…”."; ⌘F focuses the search. |
| SE-010 | done | TestChoicesChangeSettings | Dark, Light, Follow system, Messages and local palettes, Open themes folder, How to make a theme. Loading palettes is `theme`'s. |
| SE-011 | done | TestChoicesChangeSettings | System or Inter. |
| SE-012 | done | TestWallpaperPage, TestWallpaperPreview, capture (TestShots, wallpaper.png) | All 50 colours (the table is `theme`'s), Add doodles (with its tooltip), Choose image…, Remove image; the preview paints the colour, the doodle tile over it, or the picture, as the conversation will. |
| SE-013 | done | TestChoicesChangeSettings | Larger and Smaller, 0.6–2.0. |
| SE-014 | done | TestChoicesChangeSettings | Auto and the 11 languages. |
| SE-020 | done | TestSwitchesChangeSettings | |
| SE-021 | done | TestSwitchesChangeSettings | |
| SE-022 | done | TestSwitchesChangeSettings | The core is told (SetHistoryPrefetch). |
| SE-023 | done | TestSwitchesChangeSettings | |
| SE-024 | done | TestLockedChatsCode | Stored as a salted PBKDF2 verifier (`lockhash`), never the code. The field is the lock agent's secure field. |
| SE-030 | done | TestSwitchesChangeSettings | |
| SE-031 | done | TestSwitchesChangeSettings | |
| SE-032 | hand | TestSounds (menus, custom file, choices) | The Play preview shells out to `afplay` with Glass and Ping for Pidgin's two sounds (not shipped, GPL-2.0). `media.Player` takes PCM only, with no file decoding (aiff, mp3, m4a), so it cannot play the system sounds or a chosen file. Hand: Settings > Notifications, choose Message sound = Pidgin alert, press Play; hear a sound; choose a file (.mp3), press Play; hear it. |
| SE-033 | done | TestSwitchesChangeSettings | |
| SE-034 | done | TestChoicesChangeSettings | Off, Direct messages, Groups, both. |
| SE-040 | done | TestSwitchesChangeSettings, TestReceiptsNote | "Off for your account, so only groups get them." |
| SE-041 | done | TestSwitchesChangeSettings | |
| SE-042 | done | TestAccountPrivacy, TestSteadyFramesAllocateNothing | Seven kinds with WhatsApp's choices, notes while offline or loading, error and retry. |
| SE-050 | done | TestSwitchesChangeSettings | The setting only; keeping the process alive is the shell's and `sys`'s. |
| SE-051 | hand | TestStartAtLogin (fake hook: on, off, refusal undone, approval text) | `cmd/zapfast` wires `settings.LoginItem = sys.LoginItem`. Hand: switch Start at login on in Settings; System Settings > General > Login Items lists ZapFast (if approval is needed Settings says "Allow ZapFast in System Settings, under Login Items."); switch it off; the item is gone. |
| SE-052 | done | TestSwitchesChangeSettings | |
| SE-053 | done | TestProxy, TestProxyFromTheEnvironment | Checked, saved, sent to every core; the environment's proxy is named when empty. |
| SE-054 | done | TestGiphyKey | The description is the "Get a free key at developers.giphy.com." variant: the Go build has no built-in GIPHY key (upstream's `BUILT_IN_GIPHY_KEY`). |
| SE-060 | done | TestAccount, TestProfilePicture | Name and About edited in place, the linked-device line, and the real profile picture (full, else small) from the window's image cache on a pressable avatar. The pressable avatar is named "Change profile picture" (`AvatarOpts.Label`), its initials come from the person's name. |
| SE-061 | done | TestAccount | Unlink this computer, Add account, Remove this account, each through the shell's dialogs. |
| SE-070 | done | TestFiles | Open folder, Change…, Use default, Open log, through `e.S.Shell` and the core. |
| SE-071 | done | TestStorage, TestSteadyFramesAllocateNothing | Images, videos and GIFs, stickers, other; Clear… asks first. |
| SE-073 | done | TestAbout | Version, Keyboard shortcuts, About, Source code, the credit, "Update to X". The tagline is new text (upstream's names Rust and egui). |
| SE-074 | done | `state`: TestUnknownAndMissingFieldsAndTheWindow | The file format is the state's; Settings has nothing to add. |
| AL-001 | done | TestAppLock | At least six characters, twice; change with the current one; turn off with it; toasts "App lock password saved" and "App lock turned off". The fields are the lock agent's secure fields. |
| UP-001 | done | TestUpdateCheckIsDailyAndSendsOnlyTheVersion, TestUpdateChecksByItself, TestUpdaterProxy | Daily, only with the setting on, sends only the version. The transfers use the framework's `app/fetch` (NSURLSession), through the app's proxy setting, else ALL_PROXY or HTTPS_PROXY unless NO_PROXY names the feed's host (zf:src/proxy.rs). Release builds read the latest GitHub release of AdamAugustinsky/zap; `ZAPFAST_UPDATE_FEED` names a test feed instead, and the Updates footer then says so. The titlebar's update button (`screen.UpdatePill`) offers the version, downloads it, then restarts into it. |
| UP-002 | done | TestUpdateFlow; shell: `updates.Available` in cmd/zapfast/screens.go | The update block in Settings shows "ZapFast X is available" with the notes and Update; the main window's banner reads the same updater through the shell's slot. |
| UP-003 | done | TestRestartInstallsAndQuits, TestInstallRefusals, TestUnsupportedWording, TestUnsupportedCopyOffersTheDownloadPage, TestUpdateFlow; hand: install a signed update (steps below) | Checking download…, Downloading update…, Ready to install, Restart to update, Preparing to restart…, Retry download, Download from GitHub. Restart to update runs the publisher checks (digest and Ed25519 signature, again), `selfupdate.Supported`, `Prepare`, `InstallAndRelaunch`, then quits through `RequestQuit` (⌘Q's path: the quit policy, OnQuit hooks, the last settings write). Each refusal has its own text (see UP-006). The two-bundle swap itself is covered by the framework's `app/selfupdate` end-to-end tests; hand: with a notarized build in /Applications and a feed holding a newer signed zip (`packaging/macos/gnui-sign-update.sh`), Check now, Download update, Restart to update; the new version opens and Settings shows its version. |
| UP-004 | done | TestUpdateIsVerified, TestTamperedDownloadNeverReachesPrepare, TestInstallRefusals, TestLastResultAfterARestart | Digest and signature are checked at download and again before `Prepare`; a bad one never reaches it. `ErrNotNewer`, `ErrRejected` and failures of Prepare or the helper are shown, none quits. The rollback is the framework's: after a restore the app toasts upstream's "The update could not start. The previous version has been restored." and the Updates section says why; a failed update toasts its reason; an installed one toasts "ZapFast was updated to X." (ours, upstream says nothing). |
| UP-005 | done | TestAutomaticDownload | Fetches and verifies in the background; restarting stays a click. |
| UP-006 | done | TestUnsupportedWording, TestCaskOwnedBundle, TestUnsupportedCopyOffersTheDownloadPage | Before offering Restart to update the copy is probed (off the main thread): upstream's refusals first (cargo, Nix, Homebrew by path or by a Caskroom link to the bundle) in upstream's words, then `selfupdate.Supported`: not bundled, translocated or read-only: "Move the app to Applications, then open it to update."; a dev bundle: "This app bundle is not a release of this app."; unknown: "Cannot inspect this installation: …". Ours, where upstream has no word for macOS: a bundle this user cannot write, an ad-hoc or unsigned copy. A refused copy gets the reason and the "Download from GitHub" link. |
| FL-006 | done | `state`: TestRoundTripsThroughDisk, TestSettingsPersistAcrossARestart | Plain JSON in upstream's files and format. |

SE-072 (download names) is `logic`'s, not listed here.

## Counts

done 38, partial 0, hand 2, todo 0, waived 0.

## Security review fixes (794dafd)

1. **Chat-lock code (Medium).** Each edit's verifier is numbered; a late, older result never replaces a newer pending one, and the window stores only the latest issued request's result, zeroing the rest. TestChatLockKeepsTheLatestCode (gated hashers, reversed completion, no frame between; it fails without the fix).
2. **Release URL (Medium).** `latest.json`'s `url` is used only if it is an absolute `https://` address with a host and no credentials; anything else (file:, a custom scheme, relative, plain http, empty) becomes the releases page, in `parseRelease` and again where the link is opened. Upstream's address is GitHub's `html_url`, which its transport vouches for and does not check by host, so the host is not restricted. TestReleaseURLIsHTTPSOrTheReleasesPage, TestReleaseNotesNeverOpensAFileURL.
3. **Download before authentication (Medium).** The manifest's Ed25519 signature over `<sha256>  <file>\n` (and the checksum's form) is verified before the archive is requested; the archive is capped at 200 MB (`fetch.Options.MaxBytes`, also for a folder feed), over it the transfer stops with its own wording and the file is removed. TestBadSignatureNeverStartsTheDownload (a recording feed sees no fetch, no file), TestOversizedUpdateStopsAtTheCap (announced length, unknown length, a folder).
4. **Rotation key (Low).** `update-public-key-next.hex` is embedded byte for byte from upstream and a signature by either key is accepted at the download check and at the re-verify before `Prepare`; `gnui-sign-update.sh` signs with a key matching either. TestBothPublisherKeysAreEmbedded, TestNextPublisherKeyIsAcceptedAtBothChecks (test keys through `SetKeys`).

### Second independent review (GPT-6 Astra Fast, on 794dafd)

5. **Signed package not bound to the version (Medium).** The manifest signature covers `<sha256>  <file>\n` but the version in `latest.json` was trusted separately, so an older release's authentic package, name and signature could be replayed under a newer announced version (the framework's `Prepare` only compares with the installed one). (a) `verifyManifest`, at the download check and again before `Prepare`, requires the file name to be the one `gnui package -zip` gives the version, `ZapFast-<version>.zip` (`releaseFileName`; upstream derives the asset name from the version likewise); `gnui-sign-update.sh` refuses any other name. (b) `selfupdate.Staged` does expose the staged bundle's `Version()`: after `Prepare` it must equal the announced version, else `Discard` and nothing installs. TestAnOlderReleaseReplayedUnderANewerVersionIsRefused (refused before any fetch), TestStagedVersionMustBeTheAnnouncedOne (the fake's staged version older, newer, equal, `0.20`).
6. **Closing Settings before the chat-lock hash lands (Low/Medium).** The finished verifier was stored only by `poll`, which runs while the Settings window builds. It is now stored by the updater's hook (`Updates.CheckIfDue`, which the shell calls on every main-window frame and every drain with no window), with the sequence rule kept (only the latest request's result; every discarded one zeroed); the worker wakes the main window. TestChatLockIsAppliedWithSettingsClosed (no Settings frame is built; fails without the hook).

New strings for the catalogues: "The update's file does not belong to the version it announces, so it was not installed." and "The update is version {got}, not the {announced} it announced, so it was not installed."

## Long languages

German, Russian, French and Indonesian (and English) were rendered whole at 760 and 480 points wide, linked, with several accounts, with the app lock form and the profile editor open, and on the wallpaper page, and looked at (`ZF_SHOTS=<dir> go test ./ui/settings -run TestLanguageShots`); `TestEverySectionFitsInTheLongestLanguages` runs the framework's layout audit (overflow, overlap, clipping) on each, and fails on the old layout in 27 of them.
- **Account buttons.** Upstream's line (egui) neither wraps nor shrinks. Ours measures the buttons and, when they and the name do not both fit, puts them on a line of their own under the name, stacked when that is too narrow too (German "Diesen Computer abmelden" overflowed by 35 pt; English overflowed at 480).
- **Search field.** 200 pt, or what its hint needs (French "Rechercher dans les paramètres"), up to what the toolbar has left; upstream's field takes the column.
- **Controls** (pop-ups, the proxy, GIPHY and chat-lock fields) take at most about two fifths of a row, so the label and its description keep a column to wrap in.
- **Left as it is:** a row title longer than its column fades out at 480 pt (e.g. "Wiederholte Benachrichtigungen begrenzen"): `Form.Row`'s label is one line, with no wrap. A pop-up's choice that does not fit ends in "…" (the catalogue's "Meine Kontakte, außer…" itself ends so). No translation was shortened.

## Gaps left

- Sound preview uses `afplay`: waiting for `media.OpenPlayer(path)`.
- Updater proxy limits (`app/fetch`, D60): `socks5://` resolves names at the proxy like `socks5h://`; an HTTP proxy's credentials do not work for a plain-http feed (407). (Fixed: when NO_PROXY names the feed's host and the Proxy setting is empty, the transfer is direct, `fetch.ProxyNone`, as upstream's updater does; TestUpdaterProxy.)
- **Security review, TOCTOU:** the archive is re-hashed and its signature re-checked just before `selfupdate.Prepare(path)` opens it by path, so a process of the same user could swap the file in that small window. It would close if `Prepare` took an open file (an fd) instead of a path; not worked around here. The same-signer check inside `Prepare` and the helper still stands behind the swap.
- The release feed's address and format are undecided (a local test feed; `packaging/UPDATE_SIGNING.md` says how a feed entry is signed). No live swap was run here: it needs a Developer ID signed build, and the framework's tests cover the swap.
- Rows: every row with a title and a description is a form row with `RowOpts.Detail`. The blocks (theme links, app lock form, privacy note, update block, account, storage, about) keep their own layout: they are not label-and-control rows.
