# The Go UI's panes: an inventory

What `gnui/ui/shell/panes.go` and the main window's layout in
`gnui/ui/shell/shell.go` do today, so that a framework workspace API (pane
focus, layout, surfaces) can be matched against it later. This describes the
current code only and adopts no framework API. Line numbers refer to commit
`4f36cc4`. Paths are relative to `gnui/`.

## Pane identities and roles

| Pane | `paneID` | What builds in it | Slot |
|---|---|---|---|
| Rail | none (not a surface) | the destinations, our avatar, the compact list while the list is hidden | the shell itself, `ui/shell/shell.go:924` |
| List | `paneList` (`ui/shell/panes.go:35`) | the chat list, or the Calls page's list | `Set.ChatList` / `Set.CallsPage` (`ui/shell/shell.go:613-635`) |
| Chat | `paneChat` (`panes.go:36`) | banner, transcript, composer or selection bar; or the Calls content; or the empty pane | `Set.Conversation`, `Set.Composer`, `Set.CallsContent` (`shell.go:637-701`) |
| Side | `paneSide` (`panes.go:37`) | info, in-chat search, message info | `sideSlot` (`shell.go:822-832`): `Set.Info`, `Set.Search`, `Set.MessageInfo` |
| Titlebar band | none | traffic lights, account block, chat tabs | `titlebar` (`panes.go:126-140`), `chatTabs` (`shell.go:595`) |

The pane ids also serve as their surfaces' cache slots (`theme.Surface.Slot`, `panes.go:29-31`). There is at most one side pane at a time: `state.Panes.Pane` (`state/state.go:64-72`, `435-486`).

## Widths, minimums, maximums, defaults

All widths are in points, converted to pixels with `e.Pt`.

| What | Default | Clamp | Where |
|---|---|---|---|
| Rail | 52 fixed | none | `railPt`, `shell.go:38` |
| List | 300 (`state/settings.go:237`) | 260 to 520 at draw time and while dragging | `minListPt`/`maxListPt`, `shell.go:43-44`, `453`; drag handle `shell.go:1074-1100` |
| Info / message info side pane | 380 | 240 to 640 while dragging (`ui/info/info.go:28-29`, `414`); not clamped on load or draw | `Width` reads the setting directly (`ui/info/info.go:129`, `ui/msginfo/msginfo.go:153`) |
| Search side pane | 380 | none in Go. Upstream clamps 300 to 520 (`src/ui/pane.rs:22-23`, `65`) | `ui/conversation/search.go:448` |
| Side pane at draw | the slot's `Width`, else `InfoPaneWidth` | `min(width, W - rail - list - 320)`; dropped (0) below 240 | `shell.go:474-483` |
| Chat pane | the rest | shown only when wider than 120 | `shell.go:507-509` |
| Pane header (title row) | 40 high | none | `paneHeaderPt`, `panes.go:20`, `headerH` `panes.go:52` |
| Titlebar account block | 200 wide | none | `accountChipPt`, `panes.go:24` |
| Window | 1100 x 720; minimum 600 x 400 | restored size and position, kept on a screen | `Config`, `shell.go:184-197`; `restoreFrame` `shell.go:207-223` |

The gaps come from the theme: `Px.Inset` from the window's edges and `Px.Gutter` between panes (`shell.go:487-509`).

## Collapse, hide, and the narrow window

- **List hidden** (`Panes.ListHidden`, ⌘B, `ui/shell/menu.go:319-320`): list width 0 (`shell.go:454-456`). The rail then shows the compact list between its top and bottom destinations when there is room, more than 64 (`shell.go:963-971`). ⌘K/Find shows the list again (`menu.go:326-327`).
- **Narrow window**: below 720 wide (`narrowPt`, `shell.go:45`, `437-438`).
  - With a chat open, or the list hidden, the rail and the list go (`shell.go:457-459`).
  - With no chat open, the list fills the window beside the rail (`shell.go:460-462`, `493-495`).
  - An open side pane covers the whole window (`Panes.Covering`, `shell.go:465-474`, `501-503`). It shows Back, and closing it shows the chat again.
  - Views slide between the list and the chat (`narrowSlide`, `shell.go:513-547`, `724-760`; pinned by `ui/shell/slide_test.go`). Input is shielded while a view slides (`shell.go:600-605`).
- **Side pane**: shown only while a chat is open (`shell.go:475`). A side pane closes when its chat changes or its account switches, because `openChat` resets `Panes.Pane` (`state/state.go:244-259`) and `clearAccountUI` resets `Panes` (`state/accounts.go:181-182`). When the side pane, chat or account changes, the previous slot gets `PaneCloser.Closed` (`shell.go:808-819`).
- **No chat open**: the chat pane shows the empty state (`shell.go:697-698`). A locked chat outside its folder counts as not open (`shell.go:441-446`).

## Focus and keyboard navigation

- **Pane in use** (`sh.focus`, `ui/shell/shell.go:104-105`; `noteFocus` `panes.go:64-89`):
  - A newly opened chat focuses the chat pane.
  - A left press inside a shown pane focuses that pane.
  - If the focused pane is not shown, focus falls back to the chat pane when a chat is open, else to the list.
  - The focused pane's surface animates toward opaque; when the window is inactive every pane is unfocused (`panes.go:91-114`).
  - Pane focus is visual only. It does not move keyboard focus.
- **Tab order** is deliberate, not build order (SH-016, `ui/screen/tabstops.go:5-58`):
  - composer 100-130, phone links 200+, chat search 300-340, info 400+;
  - rail 500-560 (`railStops`, `shell.go:904`; profile `panes.go:172`, `shell.go:1157`, `1287`);
  - list header 600-630, chips 700-900.
  - A pane whose screen registers no stops falls back to build order for that frame (`shell.go:562-580`, `687-692`). Selection mode and the picker use build order too (`shell.go:553-559`). `e.TabOrder()` applies the order at frame end (`shell.go:386-388`).
- **Escape** walks back in this order: account menu, then side pane, then a page or list mode other than Chats, then the open chat (`shell.go:1772-1791`). It is ignored while a text field or a modal has focus.
- **⌥↑ / ⌥↓** and ⌘⇧[ / ⌘⇧] step through `State.Visible` (the list's rows as shown); ⌥↑ / ⌥↓ work even from a text field that claimed the key (`menu.go:126-129`, `348-360`, `453-474`). ⌘1…9 also use `State.Visible` (`state/state.go:620-624`).
- **Find** (⌘K / ⌘⇧F) sets `Req.FocusSearch`, which the chat list consumes. It works with no window too (`ui/shell/app.go:206-210`, `menu.go:296-327`).

## What is persisted, and where

Pane state is persisted only in `settings.json` (`state/persist.go`, `state/settings_file.go:27-30`, `68`). Saving is debounced (500 ms; changes are checked for every 250 ms) and off the main thread, and is flushed at quit (`State.Quit`, `state/hooks.go:96-106`).

| Persisted | Field |
|---|---|
| List width | `Settings.SidebarWidth` |
| Side pane widths | `Settings.InfoPaneWidth`, `Settings.SearchPaneWidth` |
| Window | `WindowW/H`, `WindowX/Y`, `HasWindowPos`, `WindowMaximized` (`noteSize` `shell.go:1015-1027`, `keepFrame` `shell.go:1066-1071`) |
| Transparency | `Settings.Transparency`, default on (`state/settings.go:192`, `238`) |
| Account on screen, account order | `accounts.json` (`Roster`, `state/persist.go:340-357`) |
| Per account | `accounts/<id>/settings.json`: `LastChat` (reopened when the chat list is in, `state/events.go:45-58`), `ListFilter`, `CallFilter` |
| Drafts | not in settings. Kept by the core's archive (`SaveDraft` command, `Drafts` event; `state/commands.go:15-25`, `model/store.go:192-200`, `637-657`) |

Not persisted: `Panes.ListHidden`, which side pane is open, pane focus, the narrow slide, `Panes.Covering`, the chat tabs, and the navigation history.

## Per-account and per-chat state attached to panes

- **Per account** (`state.Account`, `state/state.go:104-166`): the open chat (`Open`), list mode (`Mode`), chip (`Filter`, `Label`), and titlebar tabs (`tabs`). An account off screen keeps its open chat parked (`SwitchAccount`, `state/state.go:699-727`). `ChatRef` and JIDs are unique only within one account.
- **Per window, reset on account switch** (`clearAccountUI`, `state/accounts.go:181-193`): all of `Panes` except `ListHidden`, plus the dialog, account menu, `Visible`, history and viewer.
- **Per chat, reset when a chat opens** (`openChat`, `state/state.go:244-259`): selection, side pane, info target, message info, jump, react target, anchor.
- **Shell-side trackers** (`shell.go:104-109`): `focus`, `focusChat`, `lastPane`/`lastChat`/`lastAcct`, `listDrag`, `slide`.

## Surfaces, headers, material, appearance

- **Surface**: each shown pane is a `theme.Surface` built with `SurfaceShadow`, `BeginSurface` and `EndSurface`, with `Focus` animated at rate 14, `Opacity` 1 and `Top` set (`panes.go:91-114`). The slot content builds over it, in window coordinates.
- **Header**: a `rexui.Toolbar` over the pane's top `headerH` (`panes.go:116-124`). The screen fills its items through `ToolbarPart`, or the pane shows its title (`shell.go:583-590`, `618-635`, `693-700`). The list's resize handle sits in the gutter between the list and the chat (`shell.go:597-599`, `1074-1100`).
- **Material**: the window always opens with `platform.BackdropGlass` (`shell.go:191-194`). Each frame, `th.SetBackdrop(Settings.Transparency && window.Backdrop() != BackdropNone)` (`shell.go:301-305`). Off means the opaque field covers the glass, so switching it applies at once.
- **Reduce Transparency**: Zap reads no system flag itself. It relies on the framework (the platform's backdrop and appearance, `GNUI_REDUCE_TRANSPARENCY` to force it) to give the opaque field (`shell.go:300-302`).
- **Light and dark**: `Settings.Theme` (dark by default, light, or system) maps to Rex's preference in `theme/theme.go:47-60`, `240-262`; the palettes are in `theme/palette.go`. `Set.Theme.Apply` runs before `Kit.Begin` every frame (`shell.go:297-299`).

## What a framework swap must keep

- the list clamp of 260 to 520, the hidden list, and the compact rail;
- the narrow rules at 720 (rail and list hidden beside an open chat, the side pane covering the window, the slide);
- the side pane's room rule (at least 320 left for the chat, hidden below 240) and side panes existing only with an open chat;
- pane focus following an opened chat and a press, with the fallback when the focused pane hides;
- the deliberate Tab order and the Escape order;
- per-account parking of the open chat;
- the persisted widths and window frame;
- transparency on by default, its opt-out, and the system's Reduce Transparency.
