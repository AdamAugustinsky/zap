# ui/chatlist parity

`docs/PARITY.md` holds the status of record for every row; this file holds the evidence (tests, captures, hand steps and notes).

Status of the rows of `docs/PARITY.md` that `ui/chatlist` owns (CL, LB, LK-002, LK-005, AX-002), checked against upstream
(`src/ui/chats.rs`, `src/ui/labels.rs`, `src/app.rs`) on branch `z3-chatlist`. This file is the only place the status is
written; `docs/PARITY.md` is not edited.

- **Tests** are in `gnui/ui/chatlist` (`go test ./ui/chatlist`). `live:` tests open a real window and run with
  `ZAPFAST_LIVE=1` (screen unlocked): `TestLiveNativeContextMenu`, `TestLiveChipsScroll`.
- **Captures** are in `docs/parity/chatlist/*.png` (the list's left 520 px of the main window, light, 1x, fake data), written
  by `ZAPFAST_CAPTURE=<dir> go test ./ui/chatlist -run CaptureScenes`.
- The `CS-*` rows belong to `ui/conversation` (the in-chat search pane, including its day calendar): none is owned here.

| id | status | evidence | note |
|---|---|---|---|
| CL-001 | done | `TestToolbarNewChatAndHide`; `TestToolbarButtonsFollowTheWindowAndThePage` (no Hide in a narrow window, no buttons and a Back over the starred messages); calls `TestTheCallsTabsToolbar` (Calls: title and New call, no Hide; New call: title and Back); captures `chat.png`, `calls.png`, `new-call.png`; offscreen render viewed | Title, New chat (⌘N) and Hide the chat list (⌘B, left out in a narrow window) are the chat list's toolbar items. On the Calls tab the shell asks the Calls page's `ToolbarPart` instead (`ui/calls`, see `calls.md`): "Calls" and New call, no Hide; the earlier note that New call lived in the page's own header was stale. |
| CL-002 | done | capture `chat.png`; `TestSwitchWithEqualGenerationsShowsTheOtherAccountsRows`; `TestANewPictureAtTheSamePathShows` (update, relink) | Avatar (real pictures live), bold name when unread, accent time when unread and not muted, preview. Rows are cached per account (`Account.Serial`): a switch to an account whose chat, contacts and avatar generations equal the last one's shows its own title, preview, unread state and picture (z11-fix-identity; failed before). Pictures are keyed in the image cache by `Avatars.PictureID`, new with every Avatar event, not by path: the core writes a JID's picture to the same file, so a new picture (an ordinary update, or after unlink and relink) shows its new pixels, as upstream forgets the image on Avatar events (zf:src/app.rs:3559). The same key is used by every avatar consumer: calls, the rail, the switcher, info, message info, new chat, the header, sender pictures, Settings (z12-avatar; failed before). |
| CL-003 | done | `TestLastMessageTicksAndGroupSender`; capture `chat.png` | Ticks are drawn glyphs, seen in the capture only. |
| CL-004 | done | `TestTypingReplacesThePreview` | "typing…" and, in a group, "Name is typing…". |
| CL-005 | done | `TestDraftShowsInTheRow`, `TestLastMessageAndDraft`; capture `chat.png` | Drafts come from the model (`Store.Draft`). |
| CL-006 | done | `TestUnreadBadgeCapsAndMentionShows`, `TestRowShowsWhatTheChatHolds`; capture `chat.png` | 99+ cap, dot for marked unread, grey badge when muted. |
| CL-007 | done | `TestUnreadBadgeCapsAndMentionShows`, `TestRowShowsWhatTheChatHolds`; capture `chat.png` | "@" shows on a muted group too. |
| CL-008 | done | capture `chat.png` | Muted bell and pin icons. |
| CL-009 | done | capture `chat.png` | Timer badge on the avatar (Katherine Johnson). |
| CL-010 | done | `TestCutPreviewHasATooltip` | Native tooltips; only a cut-short preview has one. The drawn-tooltip fallback is not tested. |
| CL-011 | done | `TestOrderFollowsPinsAndActivity`, `TestListFollowsAMovedChatOnlyNearTheTop` | Rows move with `VListState.Move`; no rebuild. |
| CL-012 | done | `TestListFollowsAMovedChatOnlyNearTheTop` | At the top the moved chat shows first; scrolled down the reader keeps their rows. The "at most one row down still follows" boundary is not tested. |
| CL-014 | done | `TestFavoritesKeepThePhonesOrder` | |
| CL-015 | done | `TestEmptyStatesOfTheFilters`, `TestNoPrivateChats`, `TestEmptyStateForAnEmptyArchive`, `TestSkeletonWhileLoading`; capture `archived.png` | "Loading your chats" is skeleton rows with that name. |
| CL-016 | done | `TestMessageYourselfInSearch`, `TestAvatarsTakeTheTitleAndRexuiInitials`; capture `new-chat.png` | "Name (You)" over "Message yourself"; the avatar reads "DY" (`rexui.Avatar` takes its initials from `rexui.Initials`; the list has no initials code of its own). Two cases still differ from upstream's `util::initials`, both in the framework: it splits words on "-" too ("Ada Lovelace-King" is "AK", here "AL"), and a blank name shows "#" (here nothing). Names the list hands over are never blank. |
| CL-020 | done | `TestSearchFindsChatsIncludingArchived`, `TestSearchMatchesNumberAndLatestMessage`, `TestMessageHitsListUnderMessages`, `TestContactResultStartsAChat`, `TestFocusSearchRequestFocusesTheField`; capture `search.png` | Chats, Messages (from the model's SearchHits answer) and Contacts sections. The keys are the shell's `FocusSearch`. |
| CL-021 | done | `TestArrowsWalkTheMatches`, `TestSearchEnterOpensTheFirstHit` | |
| CL-022 | done | `TestMessageHitsListUnderMessages` (`OpenMessage` jump), `TestContactResultStartsAChat` (`EnsureChat`) | |
| CL-023 | done | `TestSearchWithNothingShowsNoResults` | The hint text is upstream's. |
| CL-024 | done | `TestEscapeClearsTheSearch`, `TestEscapeReturnsToTheComposer` | |
| CL-025 | done | `TestChipsHideWhileSearching`, `TestSearchIgnoresTheChipAndSkipsLockedChats`, `TestSearchFindsChatsIncludingArchived` | |
| CL-026 | done | `TestSearchIgnoresTheChipAndSkipsLockedChats` | Search hits and starred rows of locked chats are left out as well. |
| CL-030 | done | capture `chat.png`, `unread.png` | Counts are read-only badges on the chips. |
| CL-031 | done | `TestChipsFilterAndSecondClickReturnsToAll` | |
| CL-034 | done | `TestPlainWheelScrollsEachChipRow`; `TestChipScrollbarAppearsOnHoverAndDrags` (both system scroller styles, chip clicks remain reachable, zero allocations in steady hovered frames); `TestChipEdgeFadesInFlatThemes` (flat/Messages, light/dark); existing Tab tests and live swipe/Shift-wheel tests; eight viewed rest/hover captures `chip-parity-{flat,messages}-{light,dark}[-hover].png` outside the repository | Matches `src/ui/widgets.rs:1047-1066`, `chats.rs:658-667`, `labels.rs:94-112`, `theme.rs:745-759`. Plain vertical wheel notches route horizontally only on their chip row; precise touchpad and Shift-wheel input retain framework routing. A draggable Rex floating bar appears on hover, reserves no room even with system Always scrollers, and has upstream's 8-point track height so it does not cover chip centres. Overflow fades through 16-point transparent-to-Pane strips: filters omit this in Messages mode; labels fade in both modes, as upstream. No framework edits or waivers. |
| SH-020 (chat-list side) | done | `TestChatsTooLargeToShowGetAPlaceholder` | Owner row `ui/shell` (`shell.md`). A line "Some chats could not be shown" at the foot of Chats > All when the core dropped a chat (`model.Store.DroppedOf(abi.EventTagChats)` + `ChatUpdated`); not a row, not in a filter, search, the archive or the locked folder. |
| CL-035 | done | `TestChannelsMenuMutesAllChannels`, `TestNativeChannelsChipMenuAndUnstar` | |
| CL-040 | done | `TestMenuMarkReadAndUnread`, `TestNativeMenuHasUpstreamsItemsInOrder` | |
| CL-041 | done | `TestMenuPinFavoriteArchive`, `TestPinPastTheLimitToastsInstead` | |
| CL-042 | done | `TestMenuPinFavoriteArchive`, `TestChannelMenuItemsAndEnabledStates` | The channel's real context-menu request matches upstream's items and order (src/ui/chats.rs:1989-2106), omits both favorite actions even for a stale favorite channel, and disables only Delete chat while disconnected. Existing behavior proved; deliberately offering favorites to channels makes the new test fail, restored code passes. |
| CL-043 | done | `TestMenuPinFavoriteArchive`, `TestArchivingClosesTheOpenChat` | |
| CL-044 | done | `TestMenuMuteAndDelete`, `TestMutedChatOffersUnmute` | |
| CL-045 | done | `TestChatSoundChoice` | System sounds stand in for Pidgin's, as the table notes. The checked item follows the chat's own sound. |
| CL-046 | done | `TestLockAsksUnlockDoesNot` | The confirmation is `ui/dialogs`'; the list opens it. |
| CL-047 | done | `TestCopyInfoAndLeave` | |
| CL-048 | done | `TestCopyInfoAndLeave` | |
| CL-049 | done | `TestCopyInfoAndLeave` |  |
| CL-050 | done | `TestMenuMuteAndDelete`, `TestDeleteIsDisabledOffline`; state `TestARemovedChatLeavesNothingBehind`; model `TestARemovedChatsBytesAreWiped`; composer `TestARemovedChatsComposerGoesUnstored` | Destructive and disabled offline; the confirmation is `ui/dialogs`'. When the chat leaves (ChatRemoved), everything of it goes as upstream's `forget_chat`/`leave_chat` (app.rs:2235-2316): its row is wiped and its ref never reused, so the same JID returns clean. |
| CL-060 | done | `TestArchivedHasBackToChats`, `TestArchivedModeListsArchivedChats`; capture `archived.png` | |
| CL-062 | done | `TestCmdEArchivesTheOpenChat` | The command is the shell's (`CmdArchive`); the test runs it through the shell with the list. |
| CL-070 | done | `TestStarredModeListsStarredMessages`; capture `starred.png`; `TestRemovingAnEarlierAccountKeepsAnswersWithTheirAccount` | From the model's StarredList answer, newest star first. The mirror of the model's answers (labels, starred, message hits) is found by `Account.Serial` and retired with its account: removing an earlier account no longer shows a later one another account's starred list (z11-fix-identity; failed before). |
| CL-071 | done | `TestStarredModeListsStarredMessages` (jump), `TestUnstarFromTheStarredList` | Right-click offers Unstar only: the message's own menu belongs to `ui/conversation`. |
| CL-072 | done | `TestStarredModeListsStarredMessages`; capture `starred.png` | |
| CL-073 | done | `TestEscapeLeavesStarred` | Shell's Chats button and Escape. |
| LB-001 | done | `TestLabelManagerTexts` | The list only sends CreateLabel, UpdateLabel, DeleteLabel and SetChatLabels; that they stay off the phone is the core's. |
| LB-002 | done | `TestLabelManagerCreatesAndDeletes`, `TestLabelRenameAndRecolour`, `TestLabelManagerTexts` (ten colours); capture `labels-dialog.png` | |
| LB-004 | done | `TestNativeLabelsSubmenuMarksTheLabelsTheChatWears`, `TestMenuLabelsSubmenuTogglesALabel` | |
| LB-005 | done | `TestChipRowsScrollAndLabelsAreTheirOwnRow`; capture `label-chips.png` | |
| LB-006 | done | `TestLabelChipFiltersToItsChats`, `TestLabelAndChipLetGoOfEachOther` | |
| LB-007 | done | `TestLabelAndChipLetGoOfEachOther` | |
| LB-008 | done | `TestLabelManagerTexts`; capture `labels-dialog.png` | |
| LK-002 | waived | `TestLockedInTheRailAsksForTheCode` | The prompt and the set-up form are drawn by `ui/dialogs` (`DialogUnlockLockedChats`); the list's part is that the rail opens it and the folder opens after it. Upstream's secret code typed into the search ("retained for compatibility", `app.rs:2790`) is not built: the documented way in is the dialog. Capture "locked-prompt" is that package's. |
| LK-005 | done | `TestLockedFolder`; capture `locked-empty.png` | Archived chats included, title search, "No locked chats". |
| AX-002 | done | `TestRowShowsWhatTheChatHolds` | "N unread messages, mentioned you"; "unread". Upstream does not pluralise, nor does the list. |

## Counts

done 52, partial 0, waived 1 (LK-002), todo 0, hand 0, excluding the cross-owner SH-020 entry above.

## Beyond the table

- The row menu, the Channels chip's menu and the starred row's menu are native (`rexui.OnContextMenu`) where the window has native menus, and the
  same items are drawn as a popup where it has not. `TestLiveNativeContextMenu` pops the real menu and chooses an item.
