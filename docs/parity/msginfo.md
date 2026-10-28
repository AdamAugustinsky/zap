# Parity: message info pane (MI)

`docs/PARITY.md` holds the status of record for every row; this file holds the evidence (tests, captures, hand steps and notes).

Owner `gnui/ui/msginfo`. Tests are in `gnui/ui/msginfo/*_test.go` (fake core, `widgettest`, by role and name).
The demo core answers `WatchReceipts` for Rust Berlin's own message and gives Family-1 no receipts (`ui/msginfo/demo_test.go`, against the real library; skipped without it or with `-short`). The "Some receipts … were not recorded" note and the gone message have no demo case, so they keep their fake-core tests.

| id | status | evidence | note |
|---|---|---|---|
| MI-001 | done | TestShowsTheMessage, TestCloseClosesThePane, TestBackWhenCovering, TestHeaderIsInTheToolbarBand | The bubble with its first line, long text shortened, "Photo: Sunset" summaries. Close, or Back where the shell covers the window. |
| MI-002 | done | TestDirectChatTimes | Delivered, Read or Played with times, "Time not recorded", "—" before; nothing is followed in a direct chat. The clock is the 12/24-hour switch of the system (`format.Hour12`). |
| MI-003 | done | TestGroupSections, TestGroupLoadsThenFollows, TestDemoPartialAudience | Played by, Read by and Delivered to, newest first, "N remaining", "No one yet", live updates. Live: Rust-Berlin-1 has Ada under Read by and Grace under Delivered to; Katherine, never expected, is in no section and nothing remains (`docs/parity/info/demo-light-berlin-receipts.png`). |
| MI-004 | done | TestNoReceiptsAndPartial, TestDemoNoReceipts, TestDemoPartialAudience | A zero time is unknown, never the epoch. Live: Family-1 shows "No receipts were recorded…" (`demo-dark-family-receipts.png`). The demo's "partial" Rust-Berlin-1 has two expected recipients, so its audience is known and, as upstream's `audience_known` says, there is no "Some receipts …" note; that note is for a message with receipts but nobody expected, which only the fake core has. |
| MI-005 | done | TestGroupLoadsThenFollows, TestMessageGone, TestStopsFollowingWhenClosed | "Loading…", "This message is no longer available."; the core follows exactly the shown message and stops (`WatchReceipts(None)`) when the pane closes or its chat changes (`PaneCloser.Closed`). |

Also covered: TestSteadyFramesAllocateNothing (direct times and group receipts: exactly 0 allocations per frame; it found and fixed a per-frame string build for "N remaining").

## Demo-core data still needed

- A group message whose recipients are all `expected: false` but have receipts, to show "Some receipts for this message were not recorded…" live; the demo's Rust-Berlin-1 is a normal known audience.
- A gone message needs no data (the deletion path is the fake core's).

## Notes

- The title row ("Message info" and Close, or Back) belongs in the toolbar band over the pane, like the info panel's; see `docs/parity/info.md`, "Header in the toolbar band" (capture `docs/parity/info/after-dark-msginfo.png`). It needs the same shell patch; until then the pane draws the row itself and TestHeaderIsInTheToolbarBand skips.

- `DialogMessageInfo` (a request for message info where upstream had no room) is turned into this pane by `ui/dialogs`; the pane itself is the only place the message info is drawn.
