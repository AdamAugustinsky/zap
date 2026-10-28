# Parity: ui/conversation

`docs/PARITY.md` holds the status of record for every row; this file holds the evidence (tests, captures, hand steps and notes).

The rows of `docs/PARITY.md` whose Go owner is `ui/conversation` and that the transcript, its header, its menus and the search pane implement. The rows drawn by the content blocks of `ui/conversation/kinds` are listed in [kinds.md](kinds.md). Tests are in `gnui/ui/conversation`. Captures are under `/private/var/folders/2p/gmsgpspx4xlg9gjphgh84c540000gn/T/opencode/zfcalllive/`, taken with the demo core.

Status values: **done**, **partial**, **todo**, **waived** (with the reason) and **hand** (implemented, and checked only by hand).

| id | status | evidence | note |
|---|---|---|---|
| PR-002 | done | TestContextMenu, TestCallEntryCallsBack, TestUnavailableMenuItemsAreDisabled, TestFailedMessageCannotReloadHistory, TestDownloadingMenuDoesNotRequestAnotherDownload, TestDownloadAllWaitsOnlyWhileEveryMissingItemDownloads | Semantic state and actual clicks verify failed-message Reload, Download versus disabled Downloading…, and mixed versus all-downloading albums (only the idle member sends a manual request). Matches upstream conversation.rs:5964-5986, :6007-6014, :7086-7120. The new tests fail with an SSD-only Go overlay that removes these guards. Existing tests cover offline/pending, Clear chat and group calls; kinds covers interactive connection/in-flight gating, composer covers read-only chats. No production edits. |
| PR-005 | done | TestBlocksAndTheirText | Blocks sit flat in the bubble, with no nested cards. |
| PR-008 | done | TestGroupSendersHavePictures, TestGroupHeaderAndSenders, TestSenderInitials; capture `p3-group.png`, `p3-group-messages.png` | Group senders show their picture (window image cache, or upstream's initials, semibold at 0.38 of the picture, on theme.AvatarColor of their hue) and their name in theme.Sender of their hue. Direct chats show neither. Drawn here, not with rexui.Avatar: its initials are Subheadline regular by its own rule, not upstream's. |
| LG-013 | done | TestScrollingUpLoadsOlderHistory, TestAnEmptyChatSaysSo | Paging goes through Account.LoadOlder and FetchOlder, with the spinner and its line at the top. |
| SH-008 | done | cmd/zapfast TestTheEmptyPaneHasTheLogoAndTheHint; `ui/shell` (`Shell.empty`) | The shell's empty pane: the logo on its disc, the title and the shortcut hint (cached per locale). |
| SH-020 | done | `TestDroppedRecordIsVisibleInTranscript` (empty/populated, account isolation); `i18n.TestEveryMessageOfTheCodeIsTranslated`; generated-catalog check | Conversation half: “Some records could not be shown” appears above the transcript while Model.Dropped() > 0. EventDropped has kind/size only, so this is an account-wide notice, not a fabricated per-message placeholder. No upstream msgid exists for this ABI-only condition. The Go-port procedure in parity/i18n.md adds the POT entry, machine translations in all ten PO files, and regenerates tables_gen.go with go generate. Tests fail without the notice and with its condition deliberately disabled, pass restored. Chat-list half remains separately owned; use the same msgid if appropriate. |
| CV-001 | done | TestHeader, TestGroupHeaderAndSenders, TestGroupSubtitleNamesMembers, TestTypersInAGroup | Shows the avatar (window image cache), the name, online or last seen, a group's members (sorted first names, "x2", numbers, then You), and typing in the accent: "typing…", "Ada is typing…" or "Ada, Bo and Cy are typing…" in a group, from `model.Store.Typers` in upstream's order. |
| CV-002 | done | TestHeader | |
| CV-003 | done | TestHeaderCalls, TestHeader, TestGroupHeaderAndSenders; capture `ring-*.png`, `active-*.png` | Placing a call goes through calls.Place and LiveHere. Live on the demo core, placing rang, then connected, with Hang up in the header. |
| CV-004 | done | TestHeader, TestSearchPane | ⌘F arrives as Req.FindInChat. |
| CV-005 | done | TestHeader, TestSelectionBarTakesTheComposersPlace | All eight items are in `moreMenu`. Clear chat is disabled while offline. |
| CV-006 | done | TestNarrowHeaderGoesBack | Back to chats, or "Back to the calls" on the Calls tab (that label is untested). |
| CV-007 | done | TestFourthPinIsRefused, TestPinBarFollowsTheMarks | The pins come from `model.ChatPin`, newest first. An unpinned message leaves at once, and a new pin reloads the list. |
| CV-008 | done | TestPrependAndDeleteSplice, TestScrollingUpLoadsOlderHistory | |
| CV-009 | hand | hand: scroll up, send a message, and the view stays put | The VList sticks to the bottom only while you are at the bottom. |
| CV-010 | done | TestNewestCountAndFloatingDay, TestPagingKeysAndNewest; captures `q-newest-grid.png` (light/dark, default/Messages, 2 unread) | As upstream: a 40 pt circle of the overlay colour centred 34 pt in from the transcript's bottom right, a soft lift shadow (offset 3), the arrow down, a little larger and lighter under the pointer, with a tooltip. The chat's unread count is on a 20 pt accent badge at its top right ("99+" past 99). |
| CV-011 | done | TestUnreadDivider | |
| CV-013 | done | TestOpeningAChatShowsItsMessages | |
| CV-014 | done | TestNewestCountAndFloatingDay | While you scroll, the top message's day floats at the top once its own divider has gone. It stays for 1 s after scrolling stops, then fades over 0.25 s. New messages arriving at the bottom do not count as scrolling. It is on glass in the Messages style. |
| CV-015 | done | TestAnEmptyChatSaysSo | |
| CV-016 | done | TestTypersInAGroup | A typing bubble (rexui.TypingIndicator) with the same line as the header, after the first three typers' pictures stacked 0.6 of a picture apart, each later one ringed 1.5 pt in the chat's colour, in a group, as upstream's typing_bubble (not rexui.AvatarStack, whose initials and overlap differ). |
| CV-017 | done | TestQuoteJumps, TestSharedJump, TestNarrowSearchFolds, TestJumpAsTheHistoryLands, TestDemoJumpAsTheChatOpens (demo core) | Panes.Jump scrolls to the message and flashes it with state.FlashStrength. A jump that comes with the chat's history keeps its place: the unread divider's first scroll runs before it and leaves a pending jump alone. |
| CV-018 | done | TestGroupHeaderAndSenders, TestGroupSendersHavePictures; capture `p3-group.png` | The name and picture on a run's first message, the tail on its last, and gaps between runs. |
| CV-019 | done | av TestBareMediaInTheTranscript; kinds TestNoBubble, TestRoundNoteHasNoBubble, TestBlocksAndTheirText; inspected `bare-sticker.png`, `bare-carousel.png`, `bare-picture.png`, `bare-round-note.png`, `bare-text.png` (AV_SHOT) | The implementation was complete: stickers, carousels, Messages-style lone pictures and round video messages draw without an enclosing bubble. The real-transcript test checks the draw list with ordinary text as a bubbled control; disabling each media classification makes it fail. Same status as av.md. |
| CV-020 | hand | TestVoiceNoteInTheTranscript, TestFooterBesideTheLastLine; capture `w-ada-zoom3.png`; hand: star and pin a message, edit one of yours | The footer shows the star, "edited", the pin, the time and our ticks. As upstream's rich_body, it sits beside the text's last line when it fits there, wrapped or not, at the bubble's right; else it gets a row of its own. The text is measured as its RichText lays it out (same paragraph, cached), so bold, code and mentions count. Large emojis alone, and right-to-left text that wraps, keep the row. A voice note shares its duration line. |
| CV-021 | done | TestBlocksAndTheirText | Time and ticks sit on a scrim over the picture, and Message info opens on our messages. |
| CV-023 | done | TestNotSentExplains | A failed send shows the "!" badge and "Not sent" in its footer. Hovering explains it, and VoiceOver reads the explanation. |
| CV-024 | done | TestBlocksAndTheirText | |
| CV-025 | done | TestDemoTranscriptShowsItsContent (demo core); capture `d-margaret.png` | "Forwarded" over the text. |
| CV-026 | done | TestQuoteJumps, TestPrivateReplyQuoteOpensItsGroup, TestQuoteAndCardsSettle, TestDemoOurReplyShowsItsQuote (demo core); capture `e-reply-q2.png`, `e-reply-m-q2.png` | Shows the bar and name in the quoted sender's colour made readable on the bubble (white ones on our Messages bubble), "You" for our own, and the text; a click jumps to the original, and a private reply's quote opens its group. A bubble with a quote or a card settles as upstream's settled_width: its text's width with the footer, at least CARD_WIDTH, at most the column; the quote and the card span it. |
| CV-027 | done | ui/composer TestPrivateReply (`strips.go`) | Upstream draws "Original group message unavailable" in the reply strip above the composer. That strip is ui/composer's, which has it. |
| CV-028 | done | TestReactionPills, TestReactionPillsSayWho, TestMenuReactions, TestDemoTranscriptShowsItsContent (demo core); capture `d-margaret-top.png` | Pills flow, ours are marked, and clicking reacts or takes a reaction back. Hovering a pill shows who added it ("Ada Lovelace, You"), as a tooltip over the pill. |
| CV-029 | done | TestHoverReactAndReply | A round React control beside a hovered message ("React to Ada's message" for VoiceOver), with Reply one step further out where the chat takes messages. They swap sides near an edge, and the keyboard reaches them. |
| CV-030 | done | TestPinNotices | "You pinned a message", "{} pinned a message" or "A message was pinned", above the first message after the pin. Pins after the newest message go below it. |
| CV-034 | hand | hand: open a chat with Hebrew, Arabic and mixed lines | The framework's bidi text. |
| CV-035 | done | capture `plain.png` (colour and doodles), `messages.png` (Messages: plain), `p3-group.png`; hand: Settings, pick a wallpaper picture | Draws the colour, the doodle tile from the window's origin at DoodleFor's ink, and the account's picture cover-fitted. |
| CV-036 | done | TestPrependAndDeleteSplice, TestDeletesShowAtOnce | Messages being deleted for you are hidden (Panes.PendingDelete), and ones deleted for everyone read as deleted (Panes.Revoking). |
| TH-003 | done | `TestMessagesBubbleColours`, `TestMessagesCurlOnlyEndsTheRun`, `TestMessagesTranscriptUnderTheHeader`; eight reviewed revision captures below | Coordinator corrected the shape decision: Rex itself supports the upstream Messages shape. Restored the exact 79cd936 recipe (15 pt corners, BubbleOpts.Look/Curl with two PathMasks), curled only on the last bubble of each run. Other themes have no curl. Kept OnBubble text, secondary and rich-text colours, gradient, frost/NextBackdrop, HeaderVeil, sender/avatar colours, bare photos and wallpaper. Tail tests failed before restoration and when tails were deliberately disabled; pass restored. |
| IA-006 | done | `TestUnavailableCallExplainsOnHover` checks native registration and drawn hover over both the nested Call us label and button padding; inspected offscreen captures (2026-10-23), framework `693d3b38` | The original c1338f1 reproduction is now a permanent regression: it fails over the nested label before the framework bump and passes after it. The real ZapFast interactive card shows the phone icon, muted button and upstream hover explanation at both pointer positions; clicking still sends no reply. See the integration evidence below. |
| MK-004 | done | TestTextLinks, kinds TestLinksTheAppCannotOpenSayTheyCannot | Links in text go through kinds.OpenLink: web and mail open in the browser, and anything else gets a toast. The test clicks the links themselves (Click(RoleLink, …)). |
| MK-005 | done | TestTextLinks | A number offers Message (StartChat) and Copy number as written. |
| MK-010 | done | TestBlocksAndTheirText | |
| MK-014 | done | TestVideoBlockAndCaption | The video plays in its bubble: `ui/conversation/av` on `media.Video`, see [av.md](av.md) MK-014. The transcript lays out the block, with the caption under it. |
| MK-015 | done | | av's: see [av.md](av.md) MK-015 (a clip that cannot play opens in the system's player). "Open in system player" is in the menu. |
| MK-016 | done | av TestADoubleClickPlaysTheClipInTheViewer, TestVideoControls; viewer TestAClipResumesFromTheBubblesPosition | Both ends are implemented: expand stops the bubble's player; the viewer waits for readiness, seeks to ViewerAt and plays. A zero-seek fault fails the viewer regression. See [av.md](av.md) MK-016. |
| MK-017 | done | | av's: see [av.md](av.md) MK-017 (the round note plays in its circle, with the ring). |
| MK-019 | done | av TestVoiceNotePlaysPausesAndResumes, TestWaveformSeeksOnClickAndDrag, TestSpeedChipCyclesAndIsRemembered, TestInTheTranscript; transcript TestVoiceNoteInTheTranscript; capture `p3-group.png` | av draws and plays the note. The transcript puts it in its bubble, with the time and ticks on the duration line. |
| MK-020 | done | av TestAudioFileHasATrackAndNoRun | Drawn by av; the transcript places it. |
| MK-028 | done | TestAlbumGrid | Items with the same album, sender and day show as one two-column grid in the first item's bubble. Each tile is its item's own block, so it downloads, fails and retries on its own. Captions go under the grid, and the footer is the last item's. |
| MK-029 | done | TestAlbumGrid | Download all asks for every missing item whatever its size, failed ones included. Save all… sends the downloaded items to the core's save. A failure marks only its own tile ("Retry download", the alert and "Download failed. Click to retry."), with its reason under the pointer ("expired · Click to retry", as upstream); the others go on, and a click on it asks again for it alone. |
| MK-030 | done | TestAlbumGrid | Each tile opens on its own, and right-clicking it opens that item's menu. |
| MM-001 | done | TestContextMenu, TestMenuReactions | A drawn popover: the quick reactions (most-used first), +, then the actions, ending with Message info, Reload earlier messages and Copy message ID. |
| MM-005 | done | TestDoubleClickBesideABubbleReplies | A double click beside a bubble, or on its padding, quotes it in the composer; on its text it selects a word (the framework's). |
| MM-012 | done | TestContextMenu | The toasts are the state's. |
| MM-013 | done | TestFourthPinIsRefused | |
| MM-014 | hand | hand: Copy text on a mention, a location, a contact and an interactive message | `copyMessageText`. |
| MM-015 | done | TestAttachmentMenuItems | Open file (Open in system player for video), Copy image, Save as… (a safe name) and Show in folder. |
| MM-016 | hand | hand: right-click a photo that is not downloaded | Download, or a disabled "Downloading…". |
| MM-017 | done | TestMenuOpensTheViewer | |
| MM-018 | done | TestAttachmentMenuItems | |
| MM-019 | done | TestVoiceNoteInTheTranscript | An audio message's menu has a row with every speed (av.Speeds), the current one marked. A choice sets it with av.SetSpeed. |
| MM-020 | done | TestBlocksAndTheirText | Shown for our own sent messages. |
| MM-021 | hand | hand: right-click, then Reload earlier messages (offline: disabled) | |
| MM-022 | done | TestContextMenu | |
| MM-023 | hand | hand: right-click a deleted message | Reply, Star and Pin are left out for a revoked message. |
| SL-002 | done | TestSelectionMode, TestShiftClickSelectsARange | A click anywhere on a row ticks it, its text too (the text is not selectable in selection mode). Shift-click ranges (SL-003, logic's) count Shift held at the press, as AppKit's tables read it, or at the release. |
| SL-005 | done | TestSelectionMode, TestSelectionBarTakesTheComposersPlace | The SelectionBar slot takes the composer's place. Delete for everyone appears only when every selected message can be revoked. |
| SL-009 | done | TestRightClickInSelectionDeletes | |
| SL-011 | done | TestCopyFormat | |
| SL-012 | hand | hand: VoiceOver, Select messages, move to a check box | Its name is "Select message from {sender}, {time}: {summary}". |
| CS-001 | done | TestSearchPane, TestNarrowSearchFolds | |
| CS-002 | hand | TestSearchPane; hand: search for a word in the middle of a line | Shows the time, the line with context and the sender in groups, with the match in bold. The bold is checked only by hand. |
| CS-003 | done | TestSearchPane | |
| CS-004 | done | TestSearchDayFilter; capture `cal.png`, `chip.png` | |
| CS-005 | done | TestSearchFindsNothing | |
| CS-006 | done | TestNarrowSearchFolds | |
| CS-007 | done | TestEscapeClosesTheCalendarFirst | |
| VO-005 | waived | | av's chip: see [av.md](av.md) VO-005 (nothing to prepare: the players apply a new rate at once). Nothing the transcript draws. |
| AX-003 | hand | `TestNewMessageIsAnnounced` (incl. locked); fault injection removes the announcement (fails), restored (passes); `docs/HANDTEST.md` §9 **AX-001 / AX-003** | Announcement implementation is complete: State.Announce(AnnounceMessage) says “Ada Lovelace: hello” politely; initial history and messages received while locked stay silent. The actual VoiceOver walk/read check is Adam's outstanding hand test, not claimed performed here. Reading the chat list is ui/chatlist's. No production change. |
| KB-003 | done | TestSearchPane | |
| KB-019 | done | TestPagingKeysAndNewest | End and ⌘End go to the newest message. Home is tested, and End shares the same code. |
| KB-020 | done | TestPagingKeysAndNewest | |
| KB-021 | done | TestPagingKeysAndNewest | |
| VW-006 | done | TestReactionFromTheViewerOpensBesideTheBubble; viewer TestTheHeaderActsOnTheMessage; inspected `viewer-header.png` (VIEWER_SHOT) | Both sides already exist. The viewer calls OpenMessage, OpenReaction with an empty anchor, and RaiseMain, then closes. The transcript waits for the jump and opens the picker beside the bubble. Removing the viewer's OpenReaction makes its test fail. Same status as viewer.md. |
| PK-009 | done | TestDemoOurStickerInItsChat (demo core), TestOldStickerOfOursAsksForItAgain | The conversation's half, as upstream: our sticker on disk clicked in its chat opens the sticker view (Action::PreviewSticker) and sends nothing, so one taken out of Recent stays out; one whose file is gone (its download failed, expired) asks for it again with a manual Download, the media retry after which upstream's core lists it in Recent. The demo cannot show that rejoin: all its stickers have files and it accepts Download without doing anything. |

## Close-out captures (2026-10-21)

Current evidence directory: `/private/var/folders/2p/gmsgpspx4xlg9gjphgh84c540000gn/T/opencode/parity-close/conv/revision/`.
The eight `before-{messages,messages-light}-{light,dark}.png` and `after-{messages,messages-light}-{light,dark}.png` replace the earlier comparison: the before build is 5fe9dbb, after restores Rex's Messages curl and 15 pt corner. The improved OnBubble text/secondary colours, blue gradient, grey incoming bubbles, wallpaper and frosted header remain. The first pass's captures in the parent directory show the text-colour improvement relative to 79cd936. No images are committed.

## VW-006: what the viewer sets

To react from the viewer, the viewer calls, in this order:

1. `a.OpenMessage(chat, msg)`, which opens the chat and sets `Panes.Jump`.
2. `s.OpenReaction(chatRef, msg, state.Rect{})`. The zero `Rect` means "beside its bubble in the transcript".
3. `s.RaiseMain()`. Then the viewer closes.

The conversation takes that request, waits until the bubble is on screen and has stopped moving, then opens the reaction picker anchored on the bubble. If the bubble doesn't appear within 5 s, it gives up. No new `state` field is needed.

## IA-006: integrated nested-tooltip proof (2026-10-23)

ZapFast `56f745c6` uses the reviewed shared framework
`693d3b38a740f2b726ff25058558dce939dd4212`, recorded by `7112c80` from the old
`c1338f12e65079a3092a200f4090c034745768fc` checkout. The prior final-rows
reproduction was extended in place, not replaced with a framework-only test.
`TestUnavailableCallExplainsOnHover` retains the exact native tooltip text and
region assertion, then disables native tooltips, hovers the nested RichText
label and button padding independently, and checks the same explanation. The
label case failed on the old pin; both pass on the new one, with no reply sent.

The actual offscreen ZapFast images `tooltip-nested.png` and
`tooltip-padding.png` were inspected. Both show the explanation on the muted
Call us card. Native registration retains its newline; the drawn fallback
remains single-line, as in the unchanged `rexui.Tooltip` renderer. Evidence is
in `/Volumes/SSD/dev-toolchains/build/zf-integ-final-bump-9lhvg2_w/`, including
`ia006-before.log`, `focused-after.log` and `ia006-capture.log`; no review images
are committed.

On that exact framework, `TestTooltipOverNestedLabel` passes for all three
declaration orders, including inner priority, removed-inner fallback, clipped
descendants and overlay/ordinary-cover blocking. `TestTooltipRemovalWakeAndReuse`
and `TestTooltipParentRecoveryRequestedFrames` also pass: removing the inner
declaration or box recovers its parent in two requested frames, then becomes
idle with zero allocations. Native region/modal/popup/hidden-flow checks pass.
This closes the missing hover evidence, not any pending hand check.
