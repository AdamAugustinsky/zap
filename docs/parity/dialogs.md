# Parity: ui/dialogs

`docs/PARITY.md` holds the status of record for every row; this file holds the evidence (tests, captures, hand steps and notes).

Rows of `docs/PARITY.md` owned by `ui/dialogs`. Evidence is in `gnui/ui/dialogs` (`rows_test.go`, `flows_test.go`, `confirm_test.go`, `wiring_test.go`, `alloc_test.go`). Run: `go test ./ui/dialogs` in `gnui`.

Confirmations are the system's alerts (`App.Alert`, answered with `App.TakeDialogFor(ticket)`, so a file panel's answer is never taken for one): the question is the message, the paragraphs the info text, Cancel the default button (Return never destroys anything) and the verb marked destructive. Locking a chat is not destructive, so its button is the default. Leaving a group offers "Leave group and archive" as a third button. Where the system shows no alert (no native window, or another sheet is open) the same question is drawn, as before. Upstream draws all of them (egui has no alerts); the plan asks for native ones. Tests give the window widgettest's `FakeDialogs` (`native_test.go`: TestQuestionsAreNativeAlerts for all eleven questions, TestLeaveAlertOffersArchive, TestLockAlertAndOnToTheCode, TestQuestionIsDrawnWhenThereIsNoAlert, TestAlertIsWithdrawnWithItsQuestion, TestAnotherPanelsAnswerIsLeftAlone, TestWaitingForAnAlertAllocatesNothing); the drawn path is the older tests in `confirm_test.go` and `rows_test.go`, which run where the fake gives no alert.

hand (native sheets, once): `zapfast -demo -demo-page delete-chat` (also `unlink`, `clear-media`, `clear-chat`, `leave-group`) shows a sheet on the window: bold "Delete chat?", the explanation under it, Cancel as the default button and a red Delete. Press Escape or Cancel: the sheet closes and nothing is deleted. Seen once on 2026-10-21 (sheet up, red Delete, Cancel default); not answered, since the framework has no way to press a sheet's button from outside the process.

| id | status | evidence | note |
|---|---|---|---|
| DL-001 | done | TestForwardStopsAtTheLimitAndSaysSo, TestForwardWithNoWritableChats, TestForwardSearch | Recipients, limit and review are logic's (`state.Forwarding`); the dialog is its view. |
| DL-002 | done | TestForwardOneChatOnlyWhenTheCoreSaysSo | Limit 1 from the core's `ForwardLimit`. |
| DL-003 | done | TestForwardReviewsSeveralRecipients, TestForwardChoosesAndSends | |
| DL-004 | done | TestForwardReturnsToChoosingWhenADestinationClosed; logic TestForwardingRechecksDestinations | Reconciled with logic: ConfirmForward rechecks all destinations before sending, returns to selection and toasts when one becomes locked/read-only (app.rs:6767-6772). |
| DL-005 | done | TestNewChatOffersYourselfAndContacts, TestNewChatOpensTheChosenChat | |
| DL-006 | done | TestNewContactNumberAndPhoneChoice, TestNewContactRejectsShortNumber, TestPhoneAndName | The reason for a refused number shows as a caption under the field (upstream: on the disabled buttons' tooltip). |
| DL-007 | done | TestNewContactChecksTheNumber, TestNewContactSendsLookup, TestNewContactWithoutNameMessagesOnly, TestContactReadyStartsTheChat | The failure text is the core's. |
| DL-008 | done | TestSaveContactSendsTheName | |
| DL-009 | done | TestDeleteChatAsksFirst, TestConfirmationsExplainThemselves; cmd/zapfast TestNativeConfirmationsWithdrawAtPrivacyTransitions, TestNativeConfirmationSurvivesUnrelatedPrivacyEvents | Native privacy lifecycle below. |
| DL-010 | done | TestClearChat, TestConfirmationsExplainThemselves; cmd/zapfast TestNativeConfirmationsWithdrawAtPrivacyTransitions, TestNativeConfirmationSurvivesUnrelatedPrivacyEvents | Native privacy lifecycle below. |
| DL-011 | done | TestLeaveGroupOffersArchive, TestLeaveChannelWords | |
| DL-012 | done | TestAccountQuestions, TestConfirmationsExplainThemselves, TestQuestionsAreNativeAlerts | The only account is unlinked rather than removed (`State.RemoveAccount`). |
| DL-013 | done | TestAccountQuestions, TestClearMediaCacheNamesTheAccount | "Media cache cleared" is the state's toast on the core's answer. |
| DL-014 | done | TestInviteLookupAndJoin, TestInviteNeedingApproval, TestInviteForAMemberOpensTheChat, TestJoinGroupLooksTheCodeUp | |
| DL-015 | done | TestShortcutsListAndHintSwitch, TestShortcutsListsThem | |
| DL-016 | done | TestAboutSaysWhatItIs, TestAboutShowsTheStateVersion, TestAboutLinksOpenThroughTheShell | The link goes through the shell, never a real opener in tests. |
| DL-017 | done | TestEscapeClosesAnyDialog, TestDialogIsModal; cmd/zapfast TestChatShortcutsDoNothingBeneathADialog | The wired offscreen rig sends ⌘F, ⌘N and ↑ with About open and composer focus beneath it, checks no search/edit/dialog replacement, then verifies search and editing work after closing. HANDTEST §2 [DL-017] remains the real desktop check. |
| AC-009 | done | TestAccountQuestions, TestRemoveAccountCancel; state TestAFailedFileCleanupSaysSo | Reconciled with accounts: confirmation matches ui/dialogs.rs:1410-1415; a permission-denied local cleanup reports an error toast including the cause (app.rs:1743-1805). |
| LK-001 | done | TestLockChat; ui/chatlist TestLockedModeHidesLockedChatsFromTheMainList, TestSearchIgnoresTheChipAndSkipsLockedChats, TestLockedChatsAreOutOfTheUnreadCounts; logic TestALockedChatNeverNotifies; sys TestTheDockBadgeCountsUnreadChats | Question, SetLocked, closing the chat and code setup were already tested; list/search exclusion, chip/Dock unread exclusion and notification policy are now covered. Phone synchronization is the shared upstream core's SetLocked path (app.rs:7147), not a newly implemented Go protocol. |
| LK-003 | done | TestUnlockOffersToSetACode, TestChatLockCode | The code fields are the framework's secure field; the lock agent owns that switch. |
| LK-004 | done | TestChatLockCode | |
| MM-010 | done | TestDeleteMessages, TestConfirmationsExplainThemselves | Goes through `logic.ConfirmDelete` (marks, sends, closes). The two-day window is in the message menu (ui/conversation). |
| SL-008 | done | TestDeleteMessages | The count is in the title/body; each message is deleted through `logic.ConfirmDelete`. |
| PK-018 | done | TestStickerPackIsViewedAndAdded, TestStickerPreviewErrorsKeepTheirAccount | Preview errors always toast, with the off-screen account's label in speech, but only close the on-screen account's pack dialog (src/app.rs:3967-3975). Off-screen success leaves the visible dialog alone; locked speech stays silent. The regression test fails before the error handler moves ahead of the account/dialog guards and passes after. |
| PL-001 | done | TestPollAnswersAndSending, TestPollNeedsQuestionAndAnswers | "Sending…" follows `Model.PollCreating()`. |
| PL-002 | done | TestPollRefusedByTheCore | Matches upstream: src/ui/conversation.rs:2043-2046 offers Create poll without an ephemeral check; src/ui/polls.rs:111-141 enables Send for a valid draft/connection, not the timer. src/backend/worker/polls.rs:336-358 refuses disappearing-message chats. The test now uses the exact upstream refusal and verifies the toast, retained dialog and subsequent retry. |
| PL-007 | done | TestPollResults | Vote times use the shared moment-stamp format. |
| IA-004 | done | TestInteractiveListDialog, TestCarouselCardListReplies, TestSplitCard | Titled by the button's label ("Choose an option" without one). |
| LO-001 | done | TestLocationInputs, TestParseSpot | |
| LO-002 | done | TestLocationInputs, TestLocationSendsSpot | |
| LO-003 | done | TestLocationInputs, TestParseSpot | Nothing is fetched. |
| LO-004 | done | TestLocationInputs, TestLocationSendsSpot | |
| LO-005 | done | TestLocationComesBackWithItsSpot; cmd/zapfast TestARefusedLocationIsBackInItsDialog | A refused spot reopens the dialog with its coordinates in the field, while the chat is on screen and no other dialog is up (as upstream); otherwise the state puts them in the composer or the chat's draft. |

Other rows with a dialog half (owned elsewhere, not counted here): LG-003, LG-010 (ui/login), LK-002 (ui/chatlist), PK-015 (ui/picker), LB-002, LB-008 (ui/chatlist; the labels manager is here: TestLabelsCreate; its label sets are kept by `Account.Serial` and retired with their account: TestRemovingAnEarlierAccountKeepsLabelsWithTheirAccount, z11-fix-identity, failed before), LB-003, PL-008, IA-013 (logic), IA-007..009 (ui/conversation).

Layout: every dialog, in English, German, Russian and French at 760 and 480 points, passes the framework's layout audit (TestEveryDialogFitsInTheLongestLanguages; also TestCropperFitsInTheLongestLanguages, TestStickerMakerFitsInTheLongestLanguages). The button row is a right-aligned flow: buttons that do not fit wrap onto a second line, in build order (Cancel first, the default action last). The shortcuts dialog's hint checkbox wraps its label. hand: `ZF_SHOTS=dir go test -run TestDialogShots ./ui/dialogs` writes the New contact dialog in de, ru and fr to look at.

Steady frames of every dialog, and of waiting for an alert, allocate nothing: TestSteadyFramesAllocateNothing, TestWaitingForAnAlertAllocatesNothing.

## Native privacy lifecycle (2026-10-22)

The full shell does not build Dialogs on the app-lock or login screen. Native
confirmation tickets therefore cancel directly in `OnLock` and core-event
lifecycle callbacks, not just in `Build`: an inaccessible remote-locked chat
(update or snapshot), unlink/session end, and account removal withdraw the
sheet. An app unlock asks a retained question afresh; session teardown closes
the question. Unrelated accounts/chats and a chat still authorized inside its
locked folder leave the sheet alone. This also completes LK-006's native-sheet
half (see logic.md); the previous semantic-tree tests could not see a native
sheet left above the lock.

Full-shell `FakeDialogs` tests inspect `Withdrawn` and the open ticket before
unlock, and before any subsequent Build for core events. Both Delete and Clear
fail on 7a6be1d6 for all six transitions and pass with lifecycle cancellation.
No real account, Keychain access or native prompt is used. Upstream draws its
confirmations inside egui (`src/ui/dialogs.rs`), so skipping its UI hides them;
our native sheets require explicit cancellation.

Integration verification (2026-10-22): `cd2506c8` includes this lifecycle fix and
the notification/call withdrawal follow-ups in [sys.md](sys.md). The full default
and release suites and 20 live checks passed; the independent final Astra review
closed all three privacy P1s and the first-seen locked-caller follow-up with no
actionable findings. This does not substitute for any unperformed hand check.
LK-006's deliberate draft-parking deviation in [logic.md](logic.md) is unchanged.
