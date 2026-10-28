# Parity: logic

`docs/PARITY.md` holds the status of record for every row; this file holds the evidence (tests, captures, hand steps and notes).

The rows of `docs/PARITY.md` whose Go owner is `logic`, checked against upstream on 2026-10-21 (gnui `z2-foundation`, passes 2 to 4). Many of these reducers live in `state` or in the screen that draws them; the evidence names the package. Where the core does the work, the Go side only sends the setting.

| id | status | evidence | note |
|---|---|---|---|
| PR-007 | done | model TestPersonNameAddressBookFirst; logic TestNotificationNamesAddressBookFirst; conversation TestSenderAndQuoteUsePersonNamePrecedence, TestSenderAndQuoteNamesStayWithTheirAccount; composer TestReplyStripUsesPersonNamePrecedence, TestReplyStripNamesStayWithTheirAccount | Every person-name path uses the model's PersonName(jid, hint), matching upstream display_name_or (app.rs:2049-2058, :2547-2581): saved name, then the contact's profile name or the message's hint after a "~", then the chat name unless only a number, then the grouped phone number. Notifications (logic), transcript senders and quotes (conversation, z7-conv) and the reply strip (composer, z7-reply-name 79b04cd2, which closed the gap z7-conv reproduced: a message-only hint now reads "Replying to ~Public hint") are all on gnui; all six tests above pass together in one integrated run. Tests cover missing optional profile fields, renamed contacts and new hints, the same JID in two accounts, hidden-account updates and zero-allocation steady frames; conversation keeps a bounded account/generation/input-keyed cache for the allocating ~hint fallback, and the composer keeps its existing reply-label cache. Sender, quote and reply-strip tests each failed before their fixes. |
| LG-011 | hand | hand: link, quit, put the phone in flight mode, relaunch; the link holds and stored chats read | The core's archive. |
| AC-007 | done | ui/composer TestSwitchingAccountsParksTheComposer; state TestSwitchingDiscardsTheForwardSelection | Tests restore text, mention encoding, staged attachment and its reply after an account round trip, and discard the open forward dialog/message selection. Replies without staged attachments are deliberately not parked, as upstream app.rs:1559-1606 restores attachment-draft quoting. |
| AC-008 | hand | state TestAccountsKeepTheirOwnState; logic TestAHiddenAccountNotifiesUnderItsOwnNames; sys TestAClickOnAHiddenAccountsNoticeSwitchesToIt; HANDTEST §14 [AC-008] | Automated coverage proves account-local names, notification target and click routing with a fake desktop center. The real notification banner and click still require the existing two-number hand check; not performed here. |
| AC-010 | done | state TestAccountsKeepTheirOwnState, TestAccountSettings | |
| CL-013 | done | core: src/backend/worker.rs:4533-4550 (a reaction from someone else to our message touches the chat's activity and emits it), test a_reaction_to_our_message_raises_the_chat; ui/chatlist TestOrderFollowsPinsAndActivity | The Go list orders by the core's LastActivity. |
| CL-017 | done | logic TestReadOnFocusAndPresence; cmd/zapfast TestTheOpenChatsMessageNeverCountsAsUnread; model TestMarkReadClearsAtOnce | Read here at once (model.Chats.MarkRead), as upstream's mark_read, then MarkRead to the core: the badge never counts the open chat's message. Opening reads it whether or not the window is focused, as upstream. Withdrawing notifications: sys. |
| CL-032 | done | cmd/zapfast `TestUnreadKeyboardNavigationRetainsRowsLikeClicking` (click, Alt next/previous, WhatsApp next/previous and numbered shortcut); `TestExplicitChatOpenDoesNotRetainAnUnreadRow`; `TestUnreadKeyboardRetentionRespectsSearchFolderAndAccount` | Confirmed the verifier's keyboard-only defect: clicks retained the row, while shell stepping and numbered shortcuts bypassed retention. Both now call the list-owned `KeepUnread` hook before `OpenChat`, matching `src/ui/keys.rs:303-306` and `src/app.rs:7804`. The integration regression failed for all five keyboard cases before the fix while clicking passed. Reading no longer removes those rows until the filter changes. Searches/archive/direct jumps do not retain; account switching resets retention without losing a new account's first keyboard-opened row. Model policy and unrelated shortcuts are unchanged. |
| CL-033 | done | state persist.go (`ListFilter` saved and restored per account) | hand: pick Unread, quit, relaunch, and Unread is still picked. |
| CL-061 | done | ui/settings TestSwitchesChangeSettings; state `SetKeepChatsArchived` at start | The core unarchives. |
| LB-003 | done | ui/dialogs TestLabelsCreate | |
| LK-006 | done | ui/lock TestLockingClosesTheLockedFolderAndKeepsTheChatCodeSeparate; cmd/zapfast TestLeavingTheLockedFolderClosesItsChat (the rail's Chats, Escape, ⌘⇧1, ⌘K, the app lock and unlock, another account and back: the locked chat's message and name leave the window, its panel closes), TestALockedChatOutsideItsFolderShowsNothing (also: `OpenChatView` gives no view, ⌘F searches the chats, ⌘E archives nothing), TestLeavingTheLockedFolderClearsTheSearch, TestAChatLockedFromThePhoneWhileOpenCloses, TestALockedChatUpdatedInItsFolderStaysOpen; ui/shell TestClosingTheWindowClosesTheLockedFolder (the app's EvWindowClosed route, then a new window); TestAPollResultsDialogGoesWithItsLockedChat (locked from the phone; the app lock and unlock), TestADialogOfAHiddenLockedChatCloses; ui/info TestLockedChatMedia | A dialog about a chat that may no longer show (any `Dialog.Chat` that is `LockedAway`: poll results, an interactive list, forwarding its messages, a delete or leave confirmation, a sticker pack from it …) closes in `hideLocked`, and ui/dialogs closes such a dialog before drawing or acting on it (`loadMessage` also refuses it), as upstream's leave_chat; the chat's notifications are withdrawn too (`parity/sys.md` NT-004). Closing the main window closes the folder and its code prompt (`Shell.mainClosed`, upstream's window_gone). The cleanup also empties the chat list's search (`Req.ClearSearch`, upstream's search.clear()). A chat locked from the phone (`ChatUpdated`) or found locked in a `Chats` batch while open outside the folder closes through the same `hideLocked` (upstream's hide_locked_chat, app.rs:4215), its draft parked; an unlocked update changes nothing. Known deviation: upstream closes that chat without saving its draft; here the ordinary close parks it (OnChatClose). Kept deliberately: the chat still exists and stays locked, so saving loses nothing and shows nothing outside the folder. A chat that is gone (removed, missing from a reload, or with an unlinked session) closes through `State.OnChatGone` instead and stores nothing (LG-007, login.md). `Account.OpenChatView` itself refuses a locked chat outside the folder, so every caller gets no view. Every way out goes through `state.Account.CloseLockedFolder` (upstream's close_locked_folder, app.rs:4270): the mode, the open locked chat (draft parked through OnChatClose), its side panes, an info panel or the viewer on a locked chat. Routed from the rail (Chats, Starred, Archived), Escape, ⌘⇧1 and ⌘K, a chip or label and the list's way back (ui/chatlist), opening a chat that is not locked, the app lock (ui/lock afterLock), the main window closing and switching accounts (State.SwitchAccount, as upstream's clear_account_ui). Defence in depth: the shell builds no header, transcript, composer or side pane, and ui/conversation's transcript, header, search pane and selection bar draw nothing, for a locked chat outside the open folder (`Account.LockedAway`). Each path's test failed before the fix (the message stayed readable). |
| CV-012 | done | state `TestOpeningAChatJumpsToItsUnreadMention`; upstream `src/app.rs:6082-6092`, `opening_a_chat_goes_to_its_unread_mention` | Account.OpenChat takes the oldest unread mention before MarkRead clears it and uses the existing OpenMessage jump/paging path. Reopening the current chat does not jump again; reopening a read chat later has no stale mention. Explicit OpenMessage targets bypass mention redirection. Missing targets page when history arrives. No extra mention button, as upstream. Test failed before and with mention redirection deliberately disabled, passes restored. |
| CV-022 | done | ui/conversation `TestReceiptTicksRespectAccountPrivacy`; ui/chatlist rows.go `ticksOf`; ui/msginfo msginfo.go (`ReceiptsOff`) | Transcript read/played ticks now use the read colour; account read-receipts privacy masks Read to the ordinary delivered colour on outgoing DMs only, including after toggling while open. Groups and Played are unaffected, and stored delivery is unchanged. Upstream does not remove the two delivery ticks or gate their display on the local SendReadReceipts switch; there is no separate ticks-off setting. Test failed before the change, and also with the privacy condition deliberately disabled, then passed after restoration. |
| CV-031 | done | model TestAPinnedMessageThatIsRevokedStopsBeingAPin; ui/conversation TestExpiredAndDeletedPinsLeaveTheLine; Rust archive tests a_replayed_pin_of_a_deleted_message_stays_gone, a_message_deleted_here_or_for_everyone_stops_being_a_pin, pinning_caps_at_three_active_and_drops_expired | Go tests prove the visible expiry/deletion/revocation behavior. The three specific shared-core archive tests were executed successfully on 2026-10-22 from the existing SSD headless test artifact, including replayed pins of both deleted and revoked messages. See the shared-core evidence boundary below; this is not inferred from source inspection alone. |
| MK-032 | done | ui/conversation/kinds TestVisiblePhotosDownloadAutomaticallyOnceWithinTheLimit | |
| MK-033 | partial | ui/conversation TestAlbumGrid (failed media retries); Rust stalled_attachments_finish_with_a_retryable_error, the_download_deadline_grows_with_the_file; optional advanced-engineering HANDTEST appendix [MK-033] (not executed) | The two specific Rust deadline tests were executed successfully on 2026-10-22 using the existing SSD artifact. They prove the timeout helper and size formula, not the real network/re-upload flow. Neither the Go fake nor zfcore's demo/real tests expose a media HTTP/re-upload fault hook. End-to-end expired-media recovery remains unverified. The registered HANDTEST appendix links the detailed procedure below, with missing prerequisites reported blocked, not passed; adding a check does not close this evidence gap. |
| MM-002 | done | state TestReactionsAndRecentEmoji; ui/picker TestReactionCountsAndCloseEndsTheTarget | |
| MM-006 | done | ui/composer TestPrivateReply | |
| MM-007 | done | state TestAPrivateReplyFromALockedGroupWaits (`State.QuoteHidden`); ui/composer TestHiddenQuoteSendsNothing | While `QuoteHidden` is true ui/composer hides the quote ("Original group message unavailable") and sends neither text nor a recording; the reply waits and sends once the folder is open. |
| MM-011 | hand | hand: offline, Delete for me says it needs a connection; online, the copy goes once WhatsApp accepts | The core's. |
| SL-001 | done | ui/conversation TestSelectionMode | |
| SL-003 | done | logic TestADragSelectsWhatItSweeps (a drag starts a selection, grows, shrinks back, a second keeps the first's; Shift-click from the last one clicked), TestSelectionInChatOrder (a range skips what cannot be forwarded) | The reducers. The drag's edge scrolling and starting beside the bubbles are ui/conversation's. |
| SL-004 | done | logic TestSelectionDropsIneligibleMessages, TestSelectionRejectsWhatCannotBeForwarded | |
| SL-006 | done | logic TestSelectionInChatOrder ("the last one out keeps selecting"); cmd/zapfast TestEscapeTakesBackOneThingAtATime (Escape ends the selection) | |
| SL-007 | done | logic TestSelectionInChatOrder, TestForwardingTakesAtMostFiveChats | |
| SL-010 | done | ui/conversation TestCopyFormat | |
| CP-005 | done | ui/composer TestEnterSendsTheText, TestComposingStopsAfterAPause | Recording presence: ui/composer voice. |
| CP-019 | done | ui/composer TestStagedFilesStayWithTheirChat, TestRefusedFilesComeBack, TestRefusedPictureComesBack | ui/composer listens to `SendRefused`: refused files and pictures come back staged in their chat with caption and quote; for a chat not on screen they wait in its aside. |
| CP-021 | done | state TestARefusedSendComesBack; ui/composer TestRefusedTextComesBack, TestRefusedQuoteComesBack | Text goes back to the draft or to `Requests.Restore`, with the toast; ui/composer takes `Restore` (`takeRestore`) into an empty composer that isn't editing, or keeps it as the draft, and shows the refused send's reply again. |
| PL-008 | hand | hand: vote on the phone while ZapFast is closed, relaunch, and the votes are there | The core's. |
| IA-013 | done | logic TestSelectionRejectsWhatCannotBeForwarded | |
| LO-005 | done | state TestARefusedLocationReopensItsDialog (the dialog back with the spot; a newer dialog kept, the spot to the composer; moved on, to the draft), TestARefusedSendComesBack; cmd/zapfast TestARefusedLocationIsBackInItsDialog (the real dialog's field) | As upstream's send_refused: the dialog only while its chat is on screen and no dialog is up. |
| DL-004 | done | logic TestForwardingRechecksDestinations | |
| VO-001 | done | av TestSpeedChipCyclesAndIsRemembered (including a failed persistence fault); framework `media/player_darwin.go` | Verified at the player's owner: the chip cycles 1x, 1.5x, 2x and retains Settings.VoiceSpeed for later messages. Pitch preservation is delegated to the framework's AVAudioPlayer rate adjustment, enabled before prepareToPlay, not PCM resampling. No new acoustic pitch measurement. Same status and evidence as av.md. |
| VO-002 | done | logic TestAVoiceRunPlaysOn | |
| VO-003 | done | logic TestAVoiceRun (MarkPlayed) | |
| VO-004 | done | av TestVideoControls (the chip and the sound switch carry over to the next clip); viewer TestSoundAndSpeedStayFromClipToClip | `state.Video` (Rate, Muted): one speed and sound for the bubbles' clips and the viewer's, as upstream's one video::Player, until quitting, never saved. Pitch and stereo are media.Video's. |
| CA-019 | done | ui/calls `TestOneCallAtATimeAcrossNumbers`, `TestTheCallsPageCannotReplaceAnotherNumbersCall` | Reconciled with calls on z7-calls: cross-account decline, hidden-account names and the Calls page's second-call refusal are covered. Removing the decline/place guards makes the tests fail. |
| CA-020 | done | ui/calls `TestACallFiledWhileOpenJoinsTheList`; core `TestDemoCallLogDevicesAndAvatars` | Reconciled with calls on z7-calls: the page test checks the reload command and its reply's visible history row. Disabling reload makes it fail. |
| SE-072 | hand | hand: pick a download folder, download report.pdf twice, and the second is `report (1).pdf` | The core names the files. |
| AL-002 | done | ui/lock lock_test (AL-002) | |
| AL-008 | done | ui/lock TestLockingClosesTheLockedFolderAndKeepsTheChatCodeSeparate; state `Locked` (the OnLock hooks) | |
| AL-010 | done | sys TestLinksWaitForTheUnlockAndOpenInOrder; ui/lock/applock TestDeferredRequestsKeepTheirOrderAndReplaceTheirKind | |
| AL-011 | done | ui/lock TestAnyUnlinkTurnsTheLockOff | |
| AL-012 | done | lockhash TestAVerifierIsSaltedSlowAndAcceptsOnlyItsPassword | |
| NT-002 | done | logic TestNotifications | |
| NT-005 | done | logic `TestNotifications` (behind the lock: "New message"); sys `TestWithdrawalCancelsPendingNotifications`, `TestPostingRechecksPrivacy` | Rechecked 2026-10-22: lock withdraws held and inflight notes, including before the next frame. The final posting boundary re-checks lock/content settings, removes name/body/picture/Reply from stale notes, and rejects absent, removed, unlinked or notification-disabled accounts. Delayed permission and attachment-retry cases fail before the fix; disabling the final redaction makes the privacy test fail again. Inactive accounts still notify normally: this revision has no separate privacy-hidden account flag. |
| NT-006 | done | logic TestNotifications (a limited group) | |
| NT-007 | done | logic TestNotifications; sys TestTheSoundOfTheNoticeIsTheDesktopsOrNoneOrPlayedHere | |

## Shared-core evidence boundary (CV-031, MM-009, MK-033)

On 2026-10-22, ran existing headless Rust tests directly, without rebuilding Rust
or opening real accounts:

```sh
export TMPDIR=/Volumes/SSD/dev-toolchains/build/pz-verify/tmp
BIN=/Volumes/SSD/dev-toolchains/build/zf-headless-target/debug/deps/zapfast-150f18d8efd80cd9
"$BIN" archive::tests::a_replayed_pin_of_a_deleted_message_stays_gone --exact --nocapture
"$BIN" archive::tests::a_message_deleted_here_or_for_everyone_stops_being_a_pin --exact --nocapture
"$BIN" archive::tests::pinning_caps_at_three_active_and_drops_expired --exact --nocapture
"$BIN" backend::worker::tests::text_edits_notify_everyone_only_when_adding_the_mention --exact --nocapture
"$BIN" backend::worker::tests::stalled_attachments_finish_with_a_retryable_error --exact --nocapture
"$BIN" backend::worker::tests::the_download_deadline_grows_with_the_file --exact --nocapture
```

Each command ran **one test, one pass** (not a zero-match success). Logs:
`/Volumes/SSD/dev-toolchains/build/pz-verify/core-parity-tests.log`.
Binary SHA-256: `ec18b6c77c07c4f48a7073f8a11b014eb06d34fba6092051710dda83618ef8f2`.

The artifact's dep-info identifies the `headless` worktree; its fingerprint
records features `["headless"]`. Its relevant source files predate its
2026-10-19 build. Compared that source read-only with this worktree:
`src/archive/pins.rs` is byte-identical; all six test bodies and their relevant
implementations (`delete_message_aliases`, `message_removed`, `set_content`,
`prepare_text_edit`, `wants_mention_all`, `begin_edit`, `edit_settled`,
`outgoing_text`, `participant_mentions`, `attachment_deadline`,
`with_attachment_deadline`) are identical. Function hashes are recorded in
`/Volumes/SSD/dev-toolchains/build/pz-verify/core-source-match.txt`.

This is executed evidence for unchanged shared-core algorithms plus separate
Go UI tests. It is **not** a fresh build of this branch, a dylib ABI round-trip
of those algorithms, or a live WhatsApp delivery/download test. The ABI core
depends directly on this upstream library (`zfcore/Cargo.toml`).

## Advanced engineering procedure (MK-033)

**Optional, not part of ordinary hand testing. Not executed in this agent pass.**
Registered in the [HANDTEST advanced-engineering appendix](../HANDTEST.md#appendix-optional-advanced-engineering-checks).
Use only dedicated test accounts/media, not Adam's archive. MK-033 remains
partial until end-to-end evidence is obtained; missing prerequisites are blocked.

Prerequisites: a second test phone retaining the original file; a fresh
undownloaded 65 MiB attachment; an undownloaded attachment of known declared
size `S` (for example 1 MiB); a genuinely expired remote-media attachment
(403/404/410) whose original is still on the phone. A controlled network
shaper must stall an established media transfer without resetting its socket
or interrupting the WhatsApp control channel. No TLS interception or secret
logging. If that setup or an expired fixture is unavailable, report that part
**blocked**, not passed. An ordinary network disconnect can fail early and
does not prove the core deadline.

1. With automatic downloads enabled, bring the fresh 65 MiB attachment into
   view and wait five seconds. Expect no download. Click it (or choose
   **Download** in its menu): expect download to start despite the 64 MiB
   automatic limit and complete normally. Use a fresh copy, not a cached file.
2. Turn automatic downloads off. Start the known-size attachment and stall
   only its media transfer immediately. Record click time. Its core deadline
   is `T = 120 + floor(S / 131072)` seconds, including any re-upload request
   (1 MiB gives 128 s). Expect a downloading state until the deadline, then
   **Download timed out** and an available click-to-retry affordance, within
   five seconds of T. An earlier transport error is inconclusive for timeout.
   Restore traffic, click retry, and expect completion of that same message
   without resending or duplicating it. Bound this step to T + 15 seconds.
3. Click the expired attachment with the phone online and the original file
   still available. Expect the core's re-upload request to recover a fresh
   path and retry automatically once, without another click, then display
   the file. Completion must remain within the same size-based deadline.
   If expiration cannot be established without logging private URLs/keys,
   report expiration provenance unavailable rather than claiming this path.
4. Repeat with a separate expired fixture whose original the phone can no
   longer supply. Expect **No longer available on WhatsApp's servers** (or
   the timeout if the phone never answers), an available retry, and no endless
   spinner/automatic retry loop. Restore availability where possible and
   verify a reader-triggered retry can succeed. Bound to T + 15 seconds.

Record app/core version, synthetic fixture sizes, T, observed elapsed times,
the visible failure/retry outcomes and each blocked prerequisite. Do not
record media URLs, keys, phone numbers or personal message contents. A
successful old download without established expiration does not prove phone
re-upload; retain that evidence boundary.
