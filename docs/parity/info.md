# Parity: info panel (IN)

`docs/PARITY.md` holds the status of record for every row; this file holds the evidence (tests, captures, hand steps and notes).

Owner `gnui/ui/info`. Source of truth for the behaviours is `docs/PARITY.md`; this file only records status.
Status: done, partial, todo, waived, hand. Tests are in `gnui/ui/info/*_test.go` (fake core, `widgettest`, by role and name).
The demo core now has Ada's shared photos, clip, documents and links, and both groups with members and a description (`ui/info/demo_test.go`, against the real library; skipped without it or with `-short`); the commands that change a group (name, photo, leave) are refused by the demo, and it cannot mark other members as admins, so those rows keep their fake-core tests. IN-015 (a locked chat) has no demo chat.

| id | status | evidence | note |
|---|---|---|---|
| IN-001 | done | TestContactInfoShowsTheContact, TestGroupAndChannelTitles, TestCloseClosesThePanel, TestHeaderIsInTheToolbarBand | Contact info, Group info, Channel info, each with Close. The title row belongs in the window's toolbar band over the pane (see "Header in the toolbar band" below): captures `docs/parity/info/before-dark-info.png` and `after-dark-info.png` (and light). Until the shell hosts it the pane draws the row itself and the band test skips. |
| IN-002 | done | TestContactInfoShowsTheContact, TestOnlineAndLastSeen, TestMutedUntil, TestGroupShowsMembersAndOpensOne | Picture, name, number or "N members", online or last seen (live from the core's presence), Muted or "Muted until …". The picture shows the real avatar with the window's image cache (seen in the demo, offscreen). |
| IN-003 | done | TestDisappearingTimer | "Off" once turned off, nothing while unknown. |
| IN-004 | done | TestActionsSendTheirCommands, TestActionsFollowTheChat, TestPinLimit, TestMessageOpensTheChat | The buttons are one measured flow (`BeginFlow`, centred lines), so they wrap by their real widths; no estimates. |
| IN-005 | done | TestRenameEditsTheNameInPlace, TestAddToContactsAndEscape, TestEditorNames | |
| IN-006 | done | TestGroupRename, TestGroupRenameCancelAndUnchanged, TestGroupEditingNeedsPermission, TestGroupNameSitsBesideItsPencil, TestDemoGroupEditingPermissions | "Save name (Enter)", "Cancel (Escape)", "Saving…". Live with the demo: we are admin of Rust Berlin (info locked) and Family is open, so both show the pencil (capture `docs/parity/info/demo-dark-berlin-info.png`); the demo refuses SetGroupName, so the save itself and a group we may not edit stay with the fake core (the demo cannot mark other admins). Fixed with real data: a group name beside the pencil had no width and was not drawn. |
| IN-007 | done | TestGroupPhotoMenu, TestGroupEditingNeedsPermission, TestDemoGroupEditingPermissions | A native menu (`ContextMenu`, opened under the picture by a click, Enter or a right-click); no hand-drawn popover any more. A headless window has no native menu, so the test reads the menu request (items, anchor) and applies the choice through the panel's own handler. The framework's `TestOnContextMenu` covers the choice coming back. hand: open a group you administer, click its big picture, the menu offers Change photo (and Remove photo with a picture); Change photo opens the file picker. |
| IN-008 | done | TestDescriptionCopiesAndScrolls, TestGroupShowsMembersAndOpensOne, TestDemoGroupMembersAndDescription | Name, number and description are one selection scope. The description is a `ScrollUpTo` view: as tall as its text up to 120 pt, then it scrolls (TestShortDescriptionIsNotPadded for the short case). |
| IN-009 | done | TestGroupShowsMembersAndOpensOne, TestOurOwnRowOpensNothing, TestDemoGroupMembersAndDescription | Virtualised rows; names first, then numbers, You last. Live: Rust Berlin lists Ada, Grace, Katherine and You with their pictures (`demo-dark-berlin-info.png`); the demo has no per-member admin flag, so admins are not marked and that stays untested. |
| IN-010 | done | TestLeaveGroupConfirms | Group and channel. The demo refuses LeaveGroup, so only the button is seen live (TestDemoGroupMembersAndDescription). |
| IN-011 | done | TestOpeningAsksForTheListing, TestMediaDocsAndLinks, TestUnsafeAndInviteLinks, TestListingFollowsTheChat, TestStaleAnswersAreIgnored, TestDemoSharedMediaDocsAndLinks | Tabs "Media 4", "Docs 2", "Links 3" from Ada's demo chat, newest first; a document jumps to its message (`OpenMessage`), a link opens the browser through the shell. Seen live (`docs/parity/info/demo-dark-ada-media.png`, `demo-dark-ada-docs.png`, `demo-light-ada-links.png`): the photos are drawn as thumbnails; the clip is a play mark with 0:04 over a dark tile because the demo gives it no poster frame (a poster would be drawn by the same path). |
| IN-012 | done | TestEmptyAndErrorStates, TestErrorAndTryAgain, TestDemoGroupMembersAndDescription | |
| IN-013 | done | TestEscapeWalksBack | Back does what the first Escape does. |
| IN-014 | done | TestDraggingTheEdgeResizes, TestNarrowWindowCoversAndJumpClosesThePanel | The resize handle writes `Settings.InfoPaneWidth` (240..640). Stepping the chat list aside and the narrow page itself are the shell's (`Panes.Covering`); here Back shows and a jump closes the panel. |
| IN-015 | done | TestLockedChatMedia | |

Also covered: TestSteadyFramesAllocateNothing (group overview, media, documents, links, group name editor, contact, contact editor: exactly 0 allocations per frame; it found and fixed a per-frame allocation in the description's line estimate).

## Header in the toolbar band

Upstream (egui) has no toolbar: the info panel's header is the first row of its own column, level with the conversation's header. In this window the conversation's name and buttons are in the unified toolbar band, so a title row below the band left an empty band over the pane (title at y≈130 px at 2×, the chat header at y≈42). macOS puts an inspector's title and close button in the toolbar's inspector section, so the pane now offers them as `screen.ToolbarPart` (`Toolbar(e, tb)` on `ui/info` and `ui/msginfo`: the Close or Back item and the title) and leaves its own row out once the shell has hosted it (the pane notices the frame after).

**The shell must provide** (it owns the band; done in `ui/shell/shell.go` since c2bc22c: the `sideBand` lines in `Shell.main`): after the conversation's items, `tb.NextPane()` and `side.(screen.ToolbarPart).Toolbar(e, tb)` when a side pane with that method is open; and when the pane covers the window (narrow), `Toolbar` instead of `tb.Title()`. With it: Close, or Back, in the band at y≈42, the title level with the chat's name, narrow and light too (`after-*` captures); TestHeaderIsInTheToolbarBand asserts it (it runs, no longer skips). Tab stops (`StopInfoClose`) are kept on the band's button.

## Demo-core data still needed

- A per-member admin flag in the schema, to show and test admins in the members list (today the demo cannot mark them).
- A demo chat with a poster frame for its clip (the thumbnail path for videos), and a locked chat with media (IN-015).

## Decisions beyond the rows

- **Native menus:** upstream's info panel has no context menus (nothing on members or on media items); its only menu is the group picture's. That one is native now. No right-click menu was added where upstream has none.
- **Big picture:** upstream does not open a viewer from the picture of a contact or an unrenamable group, so it is a plain avatar there; it is a button only where the group may be edited.
- **Row details:** `RowOpts.Detail` is for grouped form rows. Upstream's two-line rows (documents, links) carry a leading icon the form row cannot, so they stay as press rows.
- **Not in upstream's panel, so not built:** block, report, lock, delete chat, add, remove or promote members, starred messages.
