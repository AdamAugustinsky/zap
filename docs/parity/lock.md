# Parity: app lock (`gnui/ui/lock`)

`docs/PARITY.md` holds the status of record for every row; this file holds the evidence (tests, captures, hand steps and notes).

Rows of `docs/PARITY.md` whose Go owner is `ui/lock`, checked against `src/ui/lock.rs` and `src/app_lock.rs` on 2026-10-21; then the AL rows I touched whose owner is elsewhere (AL-001, AL-002, AL-011, AL-012). Tests are in `gnui/ui/lock/lock_test.go`, `ui/lock/applock`, `gnui/lockhash`, `ui/settings/flows_test.go` and `ui/dialogs/flows_test.go`. Captures are offscreen renders viewed by hand, not committed.

| id | status | evidence | note |
|---|---|---|---|
| AL-003 | done | TestItStartsLockedOnlyWithAPassword; cmd/zapfast `TestSlowSettingsNeverBypassTheStartupLock`, `TestFailedSettingsStayPrivateUntilRetry`, `TestClosingFailedSettingsDoesNotStartAccounts`, `TestReadySettingsKeepTheFastStartupPath`, `TestLiveSettingsStartupTransitionsToLockedWindow`, `TestMalformedRosterStaysBehindStartupRetry`; state `TestTransientSettingsFailurePreservesFileAndRetry`, `TestMalformedStartupSettingsKeepUpstreamDefaultsAndBackup`, `TestMalformedRosterStaysBlockedUntilRepaired`, `TestMissingRosterStillStartsAndPersistsTheFirstAccount` | Startup no longer abandons the settings read after two seconds. A slow read shows only a loading window, and a transient global-settings read error shows a translated error with Retry; neither constructs the shell or opens an account. A successful retry applies the saved verifier before the first application frame. Malformed rosters now also block startup without renaming or overwriting the original file. The slow/error/close tests and real permission-error test failed with the original fallback restored; the malformed-roster regressions failed before KeepCorrupt adoption. See the startup policy below. |
| AL-004 | done | TestLockZapFastLocksAtOnce | ⌘⇧L is the shell's Lock ZapFast command; the status-item entry calls the same command. |
| KB-027 | done | cmd/zapfast TestTheShellsShortcuts ("⌘⇧L locks with a password", "⌘⇧L does nothing without a password": the real key press, not the command) | Upstream's Ctrl+Shift+L is Command+Shift+L on macOS (egui's `Modifiers::COMMAND`; the shortcut list labels it ⌘), which is the menu item Lock ZapFast's shortcut (`ui/shell/menu.go` `CmdLock`). Bound only while an app-lock password is set (`keys.rs:42-48`). The literal Control key is not bound on macOS, as upstream. |
| AL-005 | done | TestTheLockScreenShowsAFocusedPasswordFieldAndUnlock; TestThePasswordFieldIsSecureAndWiped; capture: offscreen light and dark renders of the empty, typed, wrong, wait and forgot states | The field is the framework's secure field: bullets only in the semantic tree, the secure subrole, secure keyboard entry while focused. hand: lock, then `ioreg -l -w 0 \| grep kCGSSessionSecureInputPID` shows ZapFast's pid while the field is focused and no pid after unlocking. |
| AL-006 | done | TestWrongPasswordsSayWhyAndBackOff; applock TestWrongPasswordsBackOffAndARightOneResets, TestRepeatedFailuresWaitLongerUpToALimit | |
| AL-007 | done | TestForgotPasswordUnlinksAndLiftsTheLockWhenLoggedOut | Unlinks every number and lifts the lock only once WhatsApp has confirmed. |
| AL-009 | done | TestWhileLockedOtherCommandsDoNothing | |
| LK-009 | done | TestLockingClosesTheLockedFolderAndKeepsTheChatCodeSeparate; cmd/zapfast TestLeavingTheLockedFolderClosesItsChat ("the app lock and the unlock": the locked chat's message is gone behind the lock and stays gone after the unlock) | Locking closes every account's locked folder with its open chat through `Account.CloseLockedFolder`, so unlocking the app shows no locked chat. |
| AL-001 | done | TestThePasswordFormSetsChangesAndTurnsOff; ui/settings TestPasswordFieldsAreSecureAndWiped | Owner `ui/settings`; the three fields are secure fields and are wiped on submit, Cancel and window close. No capture compared. |
| AL-002 | done | TestInactivityLocksAfterTheChosenTime; TestTheTickLocksWithNoWindowAndARecordingCountsAsUse; applock TestInactivityIsCountedOnTheInjectedClock | Needs `Tick` each frame (the foundation calls it). |
| AL-011 | done | TestAnyUnlinkTurnsTheLockOff | |
| AL-012 | done | lockhash TestAVerifierIsSaltedSlowAndAcceptsOnlyItsPassword, TestAVerifierFromUpstreamsSchemeVerifies, TestVerifiersFromTheReplacedCopiesStillVerify | Stored in `Settings.AppLockHash` as `pbkdf2-sha256$600000$salt$hash`, as upstream; the hash must be 32 bytes. |

## Startup settings policy (AL-003, 2026-10-22)

Upstream `src/main.rs:333` loads settings synchronously before constructing the
app; `src/app.rs:1278` creates the lock from the loaded verifier. The Go fast
path still uses the completed read immediately. If the existing two-second
wait expires, startup now shows only a placeholder and keeps the same pending
read; its eventual result is not discarded. The shell, account cores, and their
private content do not exist until settings have been accepted.

Per the coordinator's explicit policy for this fix, a transient global-settings
read failure is stricter than upstream: it keeps the original file untouched,
closes the unused preferences store, and offers Retry without opening accounts.
Retry and a fresh load remain blocked while access is denied. The regression
uses a permission-denied fixture, then restores access and verifies that Retry
recovers its original verifier.

Successfully read but malformed JSON deliberately retains upstream's defaults
policy (`src/settings.rs:687–715`). `prefs.Open` preserves the bytes as
`settings.json.corrupt`, and startup logs the existing corruption notice; there
is no new corruption UI. This is an explicit parity exception, not a guarantee
that malformed settings retain an app lock. The new recovery message for
transient errors is translated in all ten catalogs.

Validation: the original fallback restored temporarily failed the slow-read,
error/retry, cancellation, and permission-error regressions. Their fixed runs
pass, including under the race detector. Deliberately forcing a fast-path
placeholder and giving malformed settings non-default values also failed the
corresponding guards; both faults were reverted. The live placeholder-to-application handoff shows the password field on
the first application frame, with no chat rows. The shared live set passed
16/16, and the existing archive-startup live test passed all three cases.
The full Go suite passed, the layout sweep passed 356/356, and normal/release
vet passed on darwin/arm64, linux/amd64 and windows/amd64. One direct
`-demo -bench -gate -allocsites` run exited 0, with zero steady allocations
across 3,668 steady frames. The requested single
dev-build startup samples (under the shared lock) were 492.1 ms before and
472.9 ms after, both complete with six rows and no skeleton. **Both missed the
150 ms budget**; exec-to-Go-init alone was 288.2/340.6 ms. These samples do not
establish compliance with the warm-start timing budget.

### Follow-up: per-account settings (2026-10-22)

`src/settings.rs:828–869` and `gnui/state/settings.go` put read receipts, typing,
notifications, notification limiting, automatic downloads, and saving contacts
to the phone in each account's settings. Their defaults enable the first,
second, third, fifth and sixth options, and disable notification limiting.
History prefetch defaults off. Notification-content previews, the app-lock
verifier and the locked-chat verifier remain global settings, not per-account.
Upstream `AccountSettings::load` (`src/settings.rs:888–896`) falls back to
defaults for both malformed content and read failures; `src/account.rs:230–243`
loads the settings before spawning the backend.

The coordinator explicitly requires stricter transient-error handling. The
simplest startup rule is now all-or-nothing: if any roster account's settings
cannot be read, close the attempt's preferences stores and keep every account
behind the same error/Retry window. No partially configured core is opened.
Malformed account settings still log the existing notice and use defaults,
with the corrupt bytes preserved. If a new account is added after startup and
its settings cannot be read, its privacy-affecting options stay off, its saved
chat/wallpaper stay unset, and no writer is attached. Restart retries that
account without overwriting the original file.

Evidence: `TestTransientAccountSettingsFailureBlocksStartupAndRetries` and
`TestUnreadableAccountSettingsStayBehindStartupRetry` failed before the fix;
`TestNewAccountReadFailureUsesPrivateOptionsWithoutAWriter` failed with the
original read-error fallback restored. All pass after the fix, along with
`TestMalformedAccountSettingsKeepUpstreamDefaultsAndBackup` (unchanged policy).
Deliberately classifying malformed account content as a blocking read error
also failed that policy guard; the fault was reverted.

### Follow-up: settings-file arrival race (2026-10-22)

The previous loader used a separate `Stat` to decide whether global settings
were defaults and whether account 1 needed legacy migration. If a file arrived
after that `Stat` but before `prefs.Open`, a successful read could lose its
verifier or have its account privacy choices overwritten by migration.
Both settings formats now retain an unexported, non-serialized presence bit
set by their actual successful JSON decode. Absence and legacy migration use
that bit, never the earlier filesystem observation. Existing `{}` files still
count as present; missing files still get the previous defaults/migration, and
malformed files still follow the explicitly approved policy. The framework is
unchanged.

`TestSettingsArrivingAtTheReadKeepTheirLockVerifier` and
`TestAccountSettingsArrivingAtTheReadAreNotMigratedOver` inject file creation
immediately before the real preferences read. Both failed with the old `Stat`
decisions and pass after the fix; the account test also verifies that the
arriving file is not rewritten. Deliberately clearing the decoded presence bit
made `TestExistingEmptyAccountSettingsDoNotInheritLegacyChoices` fail; the fault
was reverted. `TestAccountSettings` continues to cover real missing-file legacy
migration.

Follow-up validation: full `go test -count=1 -json ./...` passed, including
356/356 layout cases; focused race and release-tag tests passed; normal and
release vet passed on darwin/arm64, linux/amd64 and windows/amd64. The shared
live set passed 16/16, and both startup live tests passed (the archive test's
three cases included). The one direct `-demo -bench -gate -allocsites` run
exited 0: 3,664 steady frames across 12 sessions, with one isolated allocation
at frame 0 in one session and none in the other eleven. The existing gate
accepts that non-repeating one-off; this is not a claim of literally zero
allocations. No new startup timing sample was taken.

### Follow-up: transient roster failure (2026-10-22)

A transient `accounts.json` read error now returns `ReadErr` before selecting
any roster, attaching its writer, loading account settings, or scheduling a
roster save. Both stores opened by the failed attempt are closed without ever
calling `Set` on the roster. Startup shows the translated message "Could not
read the account list. Try again." and Retry re-reads the same untouched file.
This protection is in ZapFast itself, independent of future framework guards.

`TestTransientRosterFailureBlocksStartupAndPreservesFile` and
`TestUnreadableRosterStaysBehindStartupRetry` failed before this fix and pass
afterward. They cover repeated failure/Retry, a fresh load, no default-account
settings or roster writer, unchanged original bytes, recovery of the real
account 7, and unchanged roster bytes after shutdown. Deliberately inserting
`Set(DefaultRoster())` on the failed path made the preservation test fail;
that fault was reverted.

The initial transient-only change left malformed rosters on the framework's
default rename-and-default behavior until KeepCorrupt became available. That
remaining difference is closed by the adoption below.

Validation for this staged change: state/i18n and focused startup tests passed
with normal and release tags; the roster regressions passed under the race
detector. All six platform/tag vet checks and both startup live tests passed,
including the archive test's three cases. Catalog generation and formatting
checks passed. No timing runs were taken.

### KeepCorrupt roster adoption (2026-10-22)

Upstream `AccountRoster::load` (`src/settings.rs:932–939`) rejects malformed
rosters as well as transient errors, and `src/app.rs:1126` propagates the error.
Only the roster now opts into `prefs.Options.KeepCorrupt`: all roster read/parse
errors take the existing `ReadErr`/Retry path before any accounts or roster
writer are attached. The failed store is closed; its expected `ErrNotLoaded`
does not replace the original useful parse/read error. Repeated Retry and a
fresh load keep the original malformed bytes in place, with no `.corrupt`
rename or default roster write. A valid replacement recovers normally.

The options are copied for the roster, so malformed global and per-account
settings retain their approved defaults/`.corrupt` behavior. A missing roster
still supports a legitimate fresh install and persists its first account.

Evidence: state `TestMalformedRosterStaysBlockedUntilRepaired` (syntax and type
errors, repeated Retry/restart, original error retained, no writer/accounts,
untouched bytes, repaired roster and shutdown) and cmd/zapfast
`TestMalformedRosterStaysBehindStartupRetry` (private UI withheld, existing
account-list error and Retry, valid replacement) both failed before adoption.
`TestMissingRosterStillStartsAndPersistsTheFirstAccount` and the existing
transient-roster/global-settings/account-settings regressions pass unchanged.
Deliberately disabling KeepCorrupt failed the original-byte preservation guard;
deliberately leaking the option into account settings failed their `.corrupt`
policy guard. Both faults were reverted.

Implementation/validation uses isolated sibling worktrees: ZapFast base
`9f3d065e` (source ancestor `cd2506c8`) and framework `9b536dee`. No go.mod changes
or changes to the shared `c1338f1` framework were needed. Broad integration
gates remain the parent's responsibility on the final tooltip framework head.

Focused validation passed: all state tests and startup/settings/roster/language
tests in default and release builds, the focused roster/settings race tests,
and affected-package vet on darwin/arm64, linux/amd64 and windows/amd64 with both
tags. Formatting and reference checks passed. The live
`TestLiveSettingsStartupTransitionsToLockedWindow` passed on the unlocked Mac
under the shared lock with caffeinate. No broad suite or timing batch was run.

### Required roster shape (2026-10-23)

Independent review found that KeepCorrupt alone was insufficient: Go's default
decoder accepted `null`, incomplete objects, null fields and null string-array
elements. Those successful decodes could still inherit/default to account 1.
Roster now has its own decoder; the global/account settings decoders and the
persistence/Retry wiring are unchanged.

Required field semantics follow upstream `AccountRoster` (`src/settings.rs:914–938`): its
derived Deserialize has no `serde(default)`, and the fields are non-optional
`u32`, `String`, and `Vec<String>`.

- Required: an object containing exactly named `next_id`, `active`, and
  `order`, each once and non-null. `next_id` is an integer in 0–4294967295;
  `active` is a string; `order` is an array of non-null strings.
- Rejected: null/non-object roots, missing/null required fields, wrong types,
  out-of-range/non-integral `next_id`, null order elements, and duplicate required
  fields. Case variants do not satisfy the required field names.
- Accepted: unknown fields (including repeated unknown fields and case
  variants alongside the real fields), empty string fields/order arrays and zero `next_id`.
  Existing sanitization remains responsible for unsafe/duplicate account IDs,
  the active account and next ID. A complete empty roster still takes upstream's
  existing default-account fallback (`src/app.rs:1126–1129`). A truly absent
  file still uses the legitimate fresh-install path without invoking decoding.

`TestMalformedRosterStaysBlockedUntilRepaired` now covers 23 rejected shapes
through Retry/restart, unchanged bytes/no rename, no writer/accounts and valid
replacement recovery. Thirteen previously accepted invalid shapes failed
before this fix. The UI regression now includes null/incomplete rosters;
its three added shapes failed before the fix. Six accepted shapes in
`TestValidRosterShapesRetainUpstreamSanitization` verify compatibility; the
case-variant-as-unknown case also failed on the old decoder. Deliberately
removing the null-order check failed both state and UI guards; the fault was
reverted. These checks remain on the isolated framework `9b536dee`.

Validation passed: all state tests and focused settings/roster/startup/language
tests in default and release builds, focused race tests, and affected-package
vet on darwin/arm64, linux/amd64 and windows/amd64 in both tags. Formatting and
reference checks passed. No broad or live reruns: startup wiring is unchanged.

### Shared-framework integration (2026-10-23)

Reviewed roster head `1833f6cc` merged without conflicts as `4d17ea6f`, after
the shared framework moved to `693d3b38` (recording commit `7112c80`). The
KeepCorrupt prerequisite is resolved; no go.mod changes were needed. Focused
state/UI roster and malformed-settings regressions pass on this shared pair.
Restoring the old roster implementation through a temporary Go overlay fails
both the state preservation and startup Retry tests; the current implementation
passes. Global/account malformed-settings defaults remain unchanged. These
checks use temporary fixtures only, never a real account or archive.
