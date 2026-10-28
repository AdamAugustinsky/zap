# The screen contract

How a screen package plugs into ZapFast's Go UI. Read it with
`gnui/ui/screen/screen.go` (the slot interfaces), `gnui/state` and
`gnui/model`.

## 1. Your package, your slot

- **You edit only your own package** (`gnui/ui/<yours>`, or `gnui/theme`,
  `gnui/sys`, `gnui/logic`). Never edit `ui/shell`, `ui/screen`, `state`,
  `model`, `core`, `abi`, `i18n`, `markup`, `mentions`, another screen, or the
  framework. Whatever you need from them goes in your report under "framework
  gaps", "core needs" or "foundation needs"; work around it inside your package
  meanwhile.
- **Your entry points stay as the stub has them** (`New()`, and for
  `ui/conversation` `NewSearch()`, for `ui/calls` `NewWindow()` and
  `NewPage()`). `cmd/zapfast/screens.go` wires them into `screen.Set`; you
  never touch it unless your constructor's signature must change (then one
  line, yours).
- **The slots** (`screen.Set`):

  | Slot | Interface | Package | Called |
  |---|---|---|---|
  | `Login`, `Lock` | `Pane` | `ui/login`, `ui/lock` | full window under the toolbar band, when unlinked / adding an account, when `state.Lock.Locked`; `Lock` is also the `screen.Guard` (`Tick` every main-window frame after the pump and commands, before routing; `Tick(s, nil)` with no window; `Event` for every app event) |
  | `Accounts` | `Popover` | `ui/accounts` | while `state.AccountMenu`, anchored at the rail's avatar |
  | `ChatList` | `ListPane` (+ optional `Compact(e, r)`) | `ui/chatlist` | the list column; `Toolbar` over it; `Compact` in the rail while `Panes.ListHidden` |
  | `Conversation` | `ContentPane` (+ optional `SelectionBar`) | `ui/conversation` | header items (`Toolbar`) and transcript, while a chat is open; while `Panes.Selecting` its `BuildSelectionBar` takes the composer's place (`SelectionBarHeight` first) and the composer is not built |
  | `Search` | `SidePane` | `ui/conversation` | beside the conversation while `Panes.Pane == PaneSearch` |
  | `Composer` | `ComposerPane` | `ui/composer` | the band below the transcript; `Height` first |
  | `Info`, `MessageInfo` | `SidePane` | `ui/info`, `ui/msginfo` | beside the conversation (`PaneInfo`, `PaneMessageInfo`) |
  | side panes | optional `PaneCloser` | | `Closed(e)` once when the pane closes or its chat or account changes |
  | `CallsPage` | `Pane` | `ui/calls` | the list column while `state.Page == PageCalls` |
  | `CallsContent` | `Pane` | `ui/calls` | the content pane on the Calls page while no chat is open ("Start call", CA-023) |
  | `CallBar` | `Band` | `ui/calls` | a strip across the foot of the linked window on every page (`BandHeight` first, 0: none): the call put aside (CA-007) |
  | `Picker`, `Dialogs`, `Cropper`, `StickerMaker` | `Overlay` | `ui/picker`, `ui/dialogs`, `ui/cropper`, `ui/stickermaker` | every frame of the linked window, after the panes and the toolbar: build nothing when there is nothing to show |
  | `Settings`, `Viewer`, `Call` | `WindowScreen` (+ optional `WindowCloser`, `ImageTuner`) | `ui/settings`, `ui/viewer`, `ui/calls` | a window of its own, opened by `state.OpenWindow(state.WinSettings …)`; `Closed(e)` once after it closed |
  | `Theme` | `Theme` | `theme` | `Apply(k, s)` at the start of every window's frame |
  | `System` | `System` | `sys` | `Start`, every app event first, once per main-window frame |
  | `Logic` | `Reducer` | `logic` | `Init` once (register listeners), `Frame` once per main-window frame (and per drain with no window) |
  | `Updates` | `Updater` | `ui/settings` (`Updates()`) | `CheckIfDue` once per main-window frame and per drain with no window |

- **Rects are window pixels.** `Build(e, r)` is called in the window's root;
  start with `e.Begin(r, "<label>")` and end with `e.End()` (a floating box at
  r, the parent of what you build). Use `e.Pt(points)` for sizes. Don't draw
  outside r except in popovers and overlays.
- **What you may import:** the framework (`gonativeui/...`: `rexui` first,
  `widget`, `ui`, `theme`, `img`, `app`, `menu`, `media`, `qr` …), `zapfast/state`,
  `zapfast/model`, `zapfast/abi`, `zapfast/core` (types only: `core.Conn`,
  `core.Shell`, `core.Revealer`), `zapfast/i18n`, `zapfast/markup`,
  `zapfast/mentions`, `zapfast/ui/screen`, `zapfast/logic` (its pure
  functions), `zapfast/format` (phone numbers, stamps, dates, durations),
  `zapfast/lockhash` (the app lock's and locked chats' verifiers),
  `zapfast/emoji` (the emoji table the picker and the composer share) and
  `zapfast/theme` (the app's look over rexui's: the palette's colours). Password fields are the framework's secure field
  (`rexui.FieldOpts{Secure: true}`, `TextBuffer.Wipe`). Never another
  screen or `ui/shell`. `zapfast/core/fake` is for tests, and for dev
  tools: `ui/viewer/preview` (a standalone viewer preview) runs on it.

## 2. Reading the model

- `e.Acct()` is the account on screen (nil before there is one): its `Model`
  (`*model.Store`), its `Open` chat (`model.ChatRef`), `Mode`, `Filter`.
  `e.S` is the whole state (`state.State`): `Page`, `Panes`, `Dialog`, `Win`,
  `Lock`, `Settings`, `Req`, `Toasts`, `Accounts`.
- **Views are valid until the next apply** (the next frame's `Pump`). Keep a
  `model.ChatRef` or a message id (`model.IDList` for several), never a
  `model.Chat`, `model.Message` or their bytes. `model.S(b)` views bytes as a
  string for a label without copying (valid as long as the bytes).
- **Rebuild on generations**, not every frame, for anything costly:
  `Model.Gen()`, `Chats.Gen()`, `Chats.OrderGen()`, `Chat.Gen()`,
  `Conversation.Gen()`, `Contacts.Gen()`, `Link.Gen()`, `Avatars.Gen()`,
  `Model.TypingGen()`, `state.State.Gen()`.
- **Everything the core sent is reachable.** The hot fields have accessors;
  for any other field decode the row on demand into an `abi` value you keep
  and reuse (no allocation): `chat.Decode(&myChat)`, `msg.Decode(&myMsg)`,
  `Contacts.Decode(i, &c)`.
- **Per-account state the model keeps** (all on `*model.Store`):

  | What | Accessors | Gen |
  |---|---|---|
  | labels | `Labels()`, `LabelAt(i)`, `FindLabel(id)` | `LabelGen()` |
  | drafts | `Draft(chat)`, `SetDraft(chat, text)` (model only); `a.SaveDraft` also stores it | `DraftGen()` |
  | presence | `Presence(jid) (online, lastSeen, ok)` | `PresenceGen()` |
  | history sync | `Syncing() (on, percent, ok)` | `SyncGen()` |
  | stars, pins | `Starred(chat, msg)`, `PinnedMessage(chat, msg)`, `ChatPins(chat)`, `ChatPin(chat, i, &abi.Pinned)` | `MarksGen()` |
  | limits, receipts | `PinLimit()`, `ReceiptsOff()` | — |
  | privacy | `Privacy.Get(kind)`, `.Pending`, `.Editable`, `.Loaded`, `.FetchFailed`, `model.PrivacyChoices(kind)`; write with `a.SetPrivacy(kind, choice)` | `Privacy.Gen()` |
  | downloads | the message's `Media().State`/`Path` (loaded ones); `File(chat, msg, part)` for any message (viewer album, info listings); `part` is `model.PartMain`, `model.PartMotion` or a carousel card's index; start one with `a.Download(chat, msg, part, manual)` | `FilesGen()`, `Conversation.Gen()` |
  | storage | `Storage.Stats`, `.Has`, `.Clearing`; ask with `a.LoadStorageStats()`, `a.ClearMediaCache()` | `Storage.Gen()` |
  | in flight | `GroupSaving(chat)`, `InteractiveSending(chat, msg)`, `PollVoting(chat, msg)` + `MarkPollVoting`, `PollCreating()` + `MarkPollCreating` | `Gen()` |
  | paging | `Conversation.LoadingOlder()`, `FetchingPhone()`, `PhoneExhausted()`, `PhoneMisses()`; page with `a.LoadOlder(explicit)` / `a.FetchOlder(chat, explicit)`, which set `Panes.Anchor` (the row to keep in view: ui/conversation scrolls to it and clears it) | `Conversation.Gen()` |
  | the call | `CallUpdate()` (*abi.CallUpdate, nil before any), `model.CallLive(phase)` | `Answer(abi.EventTagCall).Gen()` |

  Message edits, revokes, reactions and poll votes/results arrive as
  `MessageUpdated` and replace the row (decode `Content.Poll` for a poll's
  state); download states survive the update. Group metadata (participants,
  description, admin) is in the chat row: `chat.Decode(&abi.Chat)`.
- **Answers** (`Store.Answer(tag)`): the latest answer of every other tag,
  kept encoded. A batched list (StarredList, ChatHits, ChatShared, ChatMedia,
  SearchHits, Gifs, Stickers, CallLog) shows whole once its last chunk is in;
  any other tag (Receipts, CallDevices, Call, ForwardLimit, SendRefused,
  Picked, ClipboardImage, StickerPicture, PictureInspected, PictureEdited,
  StickerPackPreview, InvitePreview, InviteJoined, ContactReady,
  ContactFailed, ChatSharedChanged, StorageStats …) keeps its latest event.
  `a := m.Answer(abi.EventTagSearchHits)`: `a.Has()`, `a.Gen()`, `a.Len()`,
  `ev, j := a.Item(k)` (item k is `ev.SearchHits.Messages[j]`), `a.Event()`
  (the first chunk: compare its query/chat/request with what you asked, and
  ignore a stale answer, as upstream does), `a.Clear()`. Views are valid
  until the next apply or the next `Item` of another chunk.
- **Listeners** for what must happen once per event (staging a picked file,
  restoring a refused send, opening a joined chat): register in `Init` with
  `e.S.Listen(l, abi.EventTagX, …)`. It is called after the model applied
  the event, with the account it came from; the event's bytes are valid
  during the call only (copy what you keep). **An account that is not on
  screen** (`!a.Active()`) updates only itself: never touch the composer,
  dialogs, playback or read state for it. A listener for `PickRequest` must
  answer it (`PickAnswer`); without one the state answers with no paths.
- **The state already does** (don't repeat it): the toasts for a confirmed
  star ("Starred"/"Star removed"), a pin change, the end of history sync, a
  cleared media cache, poll errors, core `Error`/`Info` (unless you listen
  for them); reloading the starred list when a starred message changes;
  dropping a deleted label's filter; closing the create-poll dialog when the
  poll is sent; the picked download folder, notification sounds and chat
  sound going into the settings; LoadPins and LoadUntil when a chat opens or an anchor is
  missing; the phone fetch for a bare chat; LoadCallLog when a call is filed
  on the Calls page.

- **Settings** are `e.S.Settings` (the app's) and `a.Settings` (the
  account's), every field of upstream's `src/settings.rs` (see
  `state/settings.go`). Edit them in memory; the state notices a change
  within a quarter second and saves it half a second later, off the main
  thread, in upstream's files and format (`settings.json`, `accounts.json`,
  `accounts/<id>/settings.json`). `e.S.SettingsChanged()` /
  `a.SettingsChanged()` save sooner. Settings that a core must hear go
  through their helper (`SetDownloadFolder`, `SetKeepChatsArchived`,
  `SetProxy`, `a.SetPrivacy`). `a.Settings.ListFilter` follows `a.Filter` and
  `a.Label` by itself; `LastChat` follows `OpenChat`. The window's size is
  the shell's. `StartAtLogin` is the system's (app/login), not saved. The
  demo saves nothing.

- **The reducers** are package `logic`'s functions over the state (import it
  for them): selection (`StartSelection`, `SelectMessage`, `SelectRange`,
  `SweepMessages`/`EndSweep`, `ToggleSelected`, `CancelSelection`,
  `CanSelect`), deleting (`DeleteForMe` marks `Panes.PendingDelete` until
  the core confirms, `DeleteForEveryone` marks `Panes.Revoking`: draw it as
  deleted; `ConfirmDelete` runs the delete dialogs; upstream has no undo),
  forwarding (`OpenForward`, `ToggleForwardRecipient`, `ReviewForward`,
  `BackToForwardSelection`, `ConfirmForward`, `CanSend`; the state is
  `e.S.Forwarding`), voice runs (`PlayVoice` when the player starts,
  `TellPlayed`, `VoiceEnded` when a clip ends, `StopVoiceRun`,
  `FetchingNext`; play `e.S.Voice.Next` — id, path — and reset it).
  The reducer marks the open chat read while the window has the focus,
  sends SetOnline, and queues notifications in `e.S.Notices` /
  `e.S.NoticeClears` (with `e.S.Attention`) for `sys` to post and empty.

## 3. Sending commands

```go
a := e.Acct()
cmd := a.Cmd(abi.CommandTagReact)   // the account's reused command, cleared
cmd.React.Chat, cmd.React.Message, cmd.React.Emoji = chatID, msgID, emoji
a.Send(cmd)                          // copies; never blocks; logs and returns a refusal
```

Helpers: `a.OpenChat(ref)` (LoadChat, LoadPins, read state, history),
`a.MarkRead`, `a.SendText`, `a.FetchAvatar(jid, full)` (once per JID),
`a.SaveDraft`, `a.Download`, `a.LoadOlder`, `a.FetchOlder`, `a.SetPrivacy`,
`a.LoadStorageStats`, `a.ClearMediaCache`; app-wide `e.S.SetDownloadFolder`,
`e.S.SetKeepChatsArchived`, `e.S.SetProxy` (they tell every core).
Accounts: `e.S.Accounts` in the switcher's order, `e.S.SwitchAccount(i)`,
`e.S.AddNewAccount()` (a new core, its login on screen,
`e.S.AddingAccount`), `e.S.CancelAddAccount()` (back; the unlinked new
account goes), `e.S.RemoveAccount(i)` (after its confirmation: the core
logs out, then the account and its folders go; the last one is only
unlinked), `a.Name()` (the switcher's label), `a.UnreadChats(now)` (the
badge). Window state goes
through `e.S`: `OpenDialog`, `CloseDialog`, `OpenWindow`, `Toast`,
`SwitchAccount`, `Touch()` after changing fields directly. Blobs:
`a.Conn.ReadBlob` (on a worker for large ones), `a.Conn.PutBlob`.

## 3a. Shared requests and hooks

- **Rects.** `screen.Rect` is `state.Rect` (`Empty()` for none), so state
  fields take a rect as is.
- **Jump to a message:** `e.S.OpenMessage(jid, msgID)` (the account on
  screen), `a.OpenMessage(jid, msgID)`, `e.S.OpenMessageIn(accountID, jid,
  msgID)` (a notification; no msgID only switches account). It opens the
  chat (not a locked chat outside its folder), sets `Panes.Anchor` and
  `Panes.Jump{Chat, Msg, Seq}`, and pages toward a message not loaded
  (LoadUntil, here or when the first page comes). ui/conversation scrolls to
  `Panes.Jump.Msg` once loaded, flashes it (`state.FlashStrength(elapsed)`
  over `state.JumpFlash`) and clears `Msg`; a new `Seq` flashes again.
  Opening another chat drops it.
- **Windows:** `e.S.RaiseMain()` brings the main window forward (opening it
  again if it closed); `e.S.CloseWindow(w)`; `e.S.OpenViewer(chatRef, msgID,
  card)` (card: a carousel card's index, `model.PartMain` for none) sets
  `Win.ViewerChat`, `ViewerMsg`, `ViewerCard`/`ViewerHasCard`.
  `e.S.OpenViewerPlayingAt(chatRef, msgID, at)` opens it on a video playing
  at once from `at` (`OpenViewerPlaying(chatRef, msgID)`: from the start) (a double click on a clip, or its "Fill the window":
  upstream's ExpandVideo; `at` is the bubble's clip's position when that
  clip was loaded, 0 otherwise, as upstream's one player carries on): it
  sets `Win.ViewerPlay` and `Win.ViewerAt`. The viewer takes the first with
  `e.S.TakeViewerPlay()` once it shows the item and seeks to `ViewerAt`
  (a plain `OpenViewer` and closing the viewer clear both). The viewer
  window has its own menu model: View's Zoom In / Zoom Out / Actual Size
  fire `screen.CmdPictureZoomIn`, `CmdPictureZoomOut`,
  `CmdPictureActualSize` in its frame (`e.K.Command(id)`), not the app zoom.
- **Picker and reactions:** the composer sets `Panes.PickerAnchor` (its
  emoji button) every frame it shows; `e.S.InsertEmoji(e)` asks the composer
  to insert (it reads `e.S.TakeInsert()`, which clears); the conversation
  opens the reaction picker with `e.S.OpenReaction(chatRef, msgID, anchor)`
  (`Panes.ReactTarget{Chat, Msg, Anchor}`, `.Open()`); `a.React(jid, msgID,
  emoji)` sends, closes it and counts the emoji (`Settings.ReactionEmoji`,
  most used first); `e.S.UseEmoji(e)` is the one writer of
  `Settings.RecentEmoji` (InsertEmoji and React call it).
- **Composer:** `Panes.ReplyChat` is the quoted message's chat when it is
  not the open one (a private reply to a group message: `ReplyTarget.Chat`);
  `e.S.Staged` is the open chat's attachments (`state.Staged`, the composer
  owns the list, `e.S.StagedByID(id)` for the cropper); `e.S.Req.Prefill
  {Chat, Text}` is a link's text, which the composer takes into an empty
  composer of that chat and clears (`e.S.PrefillComposer`). `a.StartChat(jid,
  name, text)` opens a chat, asking the core for it first (EnsureChat) and
  opening it once it is in; text, when not nil, prefills.
- **Panes:** opening another chat closes the side panes. In a narrow window
  an open side pane gets the whole window under the toolbar and
  `Panes.Covering` is set: show Back rather than Close, and close the pane
  when an item goes to a message. `Settings.InfoPaneWidth` and
  `a.Settings.CallFilter` are saved like every setting.
- **The app lock:** register in `Init` with your screen as owner:
  `e.S.OnLock(s, func(st))` (save the open draft, discard a recording, stop
  playback) and `e.S.OnInUse(s, func(st) bool)` (a recording). Locking
  itself (`State.Locked`, which ui/lock calls) clears `Focused`, logic's
  voice run, the queued notices, asks `sys` to withdraw every notification
  (`NoticeClear{All: true}`), closes the reaction picker and the picker, and
  closes the viewer and Settings windows. The unlock replays what the lock
  held (`Screen.Defer`), in order: `OpenMessageIn`, `StartChat` with
  prefill, `DialogJoinGroup` with the invite code, each with `RaiseMain`.
  `Focused` stays false while locked.
- **Accounts:** a new number stays on the login screen ("Linked. Waiting
  for your chats…") until it is connected and its chats are in; then
  `AddingAccount` ends and the chats show.
- **Listeners and hooks are added once** per listener (or owner): a
  window opened again may call `Init` again.
- **Identity:** `state.Version` (Cargo.toml's; a test keeps them equal),
  `state.AppID`, `state.AccountDirs(id)`.
- **Demo:** `e.S.Demo`; the state does not send the demo core the settings
  commands it refuses.

## 3b. Pass 2: quit, chat close, refused sends, windows

- **Toasts from any goroutine:** `e.S.Toast(text, isError)` is safe from a
  worker and wakes the window; nothing else on `State` is (post the result
  to the main thread). Upstream's limits: four info toasts, three errors, an
  error shown again moves to the end. They stack at the bottom right above
  the composer. Read them with `e.S.ToastsInto(buf)`, dismiss with
  `e.S.DismissToast(id)`; tests may read `e.S.Toasts` when no worker posts.
- **Announcements (AX-001):** a toast says itself to the screen reader.
  For news with no toast, `e.S.Announce(kind, text)` (any goroutine):
  `state.AnnounceMessage` (a new message in the open chat, AX-003),
  `AnnounceTyping`, `AnnounceSent`, `AnnounceStatus`, `AnnounceToast`,
  `AnnounceError`. The shell speaks what waits once a frame, errors first
  and interrupting, the rest politely; stale items (3.2 s, errors excepted)
  are dropped, and while the app is locked only `AnnounceStatus` and
  `AnnounceSent` are said. The focus never moves.
- **App events:** `e.S.OnAppEvent(s, func(st *state.State, ev *app.Event)
  bool)` (register in Init; owner dedupes) sees every `app.EvService` event
  after package sys: decode yours with `perm.ResultOf(ev)` (a permission's
  answer), `media.EventOf(ev)` (Ended, Failed, Recorded), the video's
  `VideoEnded` when it lands, and return true to take it. It runs on the
  main thread, maybe with no window: change state, call `st.Wake()`, build
  nothing. ui/conversation/av's `av.Event` is registered by cmd/zapfast.
- **Tickers:** a package with no box of its own that must run every
  main-window frame goes in `Set.Tickers` (`screen.Ticker`, `Frame(e
  *Env)`, or `screen.TickFunc(f)`), called after the events are applied and
  before anything builds. It must not allocate. `av.Frame` is one
  (cmd/zapfast `avTicker`, which also registers `av.Event`).
- **The Calls tab's toolbar (CL-001):** a `CallsPage` slot that also
  implements `screen.ToolbarPart` (`Toolbar(e, tb *rexui.Toolbar)`) gives
  the list's toolbar items while `s.Page == state.PageCalls` (its title and
  New call where New chat was); otherwise the chat list's show.
- **Busy (KB-032):** ⌘L, ⌘E, ⌘⇧A and ⌘1–9 wait while a dialog, the picker,
  a reaction or a recording is up. A recording is read through `OnInUse`
  (`e.S.InUse()`), so the composer's hook is the flag.
- **No chat open (CP-020):** the shell takes drops, and ⌘V of a picture or
  files, while no chat is open, and says "Open a chat first". The
  composer's own drop target takes them once a chat is open.
- **Voice seek:** `logic.SeekVoice(s, a)` after moving a voice message's
  place: the run goes to the open chat, a clip being fetched is dropped,
  and no played receipt goes (PlayVoice sends one).
- **Private replies from locked chats (MM-007):** `e.S.QuoteHidden(a)` is
  true while the composer's private reply quotes a locked chat with the
  locked folder closed, or a chat that is gone: hide the quote and send
  nothing; the reply stays and shows again once allowed.
- **Demo pages:** `zapfast -demo-page NAME[,NAME…]` and `-demo-shot FILE`
  (cmd/zapfast/demopage.go) set upstream's demo states through `State`. A
  screen whose state a page needs can ask for a part there.
- **The account button** (rail, and the linking screen's corner with
  several accounts) is the shell's: our picture and the unread-elsewhere
  dot. ui/accounts draws only the switcher (`Popover.Build(e, anchor)`).

- **Quit:** `e.S.OnQuit(s, func(st))` runs at `app.EvWillTerminate`, on the
  main thread, before the settings' last write and before the cores close.
  The composer saves the open chat's draft here, as upstream does at quit.
  Keep it quick and synchronous. On macOS the process ends when that event
  returns, so nothing deferred in `main` runs.
- **Chat close:** `e.S.OnChatClose(s, func(st, a *state.Account, chat
  []byte))` runs whenever an account's open chat stops being the open one,
  by any route: another chat opens, Escape, archiving, the history's Back,
  the chat removed or locked away, or the account leaving the screen (its
  chat stays parked as its open one). It runs before the change, with the
  chat's JID, which is valid during the call. The composer flushes that
  chat's draft here. It does not run at quit (use `OnQuit`) or for a removed
  account. When the chat closes because it is gone (removed, missing from a
  reload of the list, as the core's logout empties it, or with an unlinked
  session), `e.S.OnChatGone(s, func(st, a, chat))` runs first: drop what
  belonged to the chat, storing nothing (the composer drops its text, quote
  and attachments, so its OnChatClose then has nothing to park).
- **Unlink:** when a number is unlinked (LoggedOut) the model forgets its
  session (chats, conversations, contacts, avatars, drafts, stars, pins,
  privacy, answers; generations keep rising) and the account gets a new
  `Serial`: key a cache of an account's data by `Serial`, never by the
  `*Account` alone. `e.S.OnUnlinked(s, func(st, a))` runs then, before the
  open chat closes, for what a screen keeps of the account itself (the
  composer drops its text, asides and attachments unstored).
- **Locked chats:** `a.OpenChat(ref)` refuses a locked chat unless
  `a.Mode == state.ListLocked`. To open one in a test, set the mode first.
- **Refused sends:** the state answers `SendRefused` for text, and for a
  location as its coordinates (`"-23.55052, -46.63331"`). For the open chat
  it sets `e.S.Req.Restore{Chat, Text}`. Otherwise, if the chat's draft is
  empty, the text goes into it (and is saved). Either way an error toast
  says why. The composer takes `Req.Restore` into an empty composer that is
  not editing, keeps it as the draft (unlike a Prefill), focuses, and clears
  the request. If the chat closes first, the text goes to the draft. Refused
  files, pictures, voice and the reply's quote are ui/composer's: listen to
  `abi.EventTagSendRefused`.
- **Windows:** a window is retired when `app.EvWindowClosed` arrives
  (`Shell.WindowClosed`), however it closed. `WindowCloser.Closed` runs
  then, and its image cache is freed. Nothing polls `Win.Open`. An offscreen
  main window (tests, `-o`) opens no real windows: the request stays in
  `Win.Want`.
- **Focus:** `e.S.Focused` is false while the main window is inactive,
  hidden or in the Dock, after the app deactivates, once it has closed, and
  while locked.
- **Zoom:** `Settings.Zoom` (60–200 %, steps of 0.1) is the whole
  interface's, in every window. The menu's Zoom In / Zoom Out / Actual Size
  change it, and `screen.ApplyZoom(e.A, e.S)` applies it before `Kit.Begin`;
  `screen.Window` and the shell call it. ui/settings' zoom control writes the
  same setting.
- **Links:** open web links with `e.S.Shell.Open(url)`, off the main
  thread. The app's Shell opens http, https and mailto with app/shell, and
  absolute paths with open(1); any other scheme, `file:` included, is
  refused. Tests set a fake Shell, so no browser opens.
- **Files:** open a file in its application only with `e.S.OpenFile(path)`
  (any goroutine; upstream's Action::OpenFile): a regular file of a format
  `state.CanOpenAttachment` allows opens; anything else (a script, an app
  bundle, a symbolic link) is shown in its folder with "For safety, open
  this file yourself from its folder". Never hand a file to `Shell.Open`;
  opening a folder and `core.Revealer` stay as they are.
- **Updates:** a `Set.Updates` that also implements `screen.UpdateNotice`
  (`Available() (version string, ok bool)`) puts "ZapFast X is available"
  with Update in the banner. cmd/zapfast adapts `*settings.Updates`.
- **The banner** (connection, history loading, a newer version) is the
  shell's, under the toolbar over the conversation or the empty pane.
  Screens draw nothing for it.

## 3c. Pass 4: read at once, typing, insets, windows

- **Read at once:** opening a chat (`a.OpenChat`), a message arriving in
  the open chat while it is looked at, and the window regaining the focus
  clear the chat's unread state in the model at once
  (`model.Chats.MarkRead`, as upstream's `mark_read`) and send MarkRead.
  The badge and the list never count the message being read. The unread
  divider reads `a.UnreadAtOpen(ref)` (the count when the chat opened), not
  `Chat.Unread()`, which is 0 by then.
- **Typing:** `a.Model.Typers(ref, buf[:0])` appends every sender typing in
  the chat (JIDs, the store's bytes, valid until the next event), in
  upstream's order: a refresh moves its sender to the end. Keep `buf` on
  the screen and it allocates nothing; name them with `a.Model.DisplayName`.
  `Typing(ref)` (the first and the count) stays.
- **Video speed and sound (VO-004):** `e.S.Video.Rate` (0 is 1x; read it
  with `e.S.Video.Speed()`) and `e.S.Video.Muted` are the one speed and
  sound switch of every clip, the bubble's and the viewer's (upstream's one
  video::Player): set them where the user switches them, and give a clip
  them when it opens or resumes. Session-only, never saved.
- **A narrow window (SH-007):** below 720 pt the list and what it opens
  take turns at the whole window, and `e.Narrow()` says so. With
  `Shell.Slides` (the app sets it; screen test rigs leave it off) an opened
  chat slides in from the right over the dimmed list for 0.22 s, and going
  back slides the list in from the left: the sliding view's Rect is off its
  place meanwhile (its `X` is not 0), so decide narrow layout from
  `e.Narrow()`, not from the Rect. Mid-slide the window takes no press,
  hover or scroll, and the list beneath takes no key.
- **A window on its own (CA-004):** `e.S.OpenWindowAlone(w)` opens window
  `w` in front without the main window: while the main window is closed it
  opens by itself (the app's events drive it, and the cores are drained
  and its window woken as usual); with the main window open it opens as
  `OpenWindow` does, and comes forward. Other wanted windows still wait for
  the main window. A ringing call uses it instead of `RaiseMain`.
- **Tab order (SH-016):** after building a focusable control of the main
  window that is one of upstream's stops, call `e.TabStop(stop, sig.ID)`
  every frame it shows (`screen.StopComposer`, `StopSend`, `StopAttach`,
  `StopEmoji`, `StopPhoneLink+n`, `StopChatSearch…`, `StopInfoClose`,
  `StopInfoControl+n`, `StopBack`, `StopNewChat`, `StopSidebar`,
  `StopSearch`, the chips `StopAll`…`StopManageLabels`, `StopLabel+n`; the
  shell registers the rail and the avatar). Once any screen registers a
  stop of its own, Tab and Shift+Tab walk the stops in that order,
  wrapping; controls that register none are skipped by Tab (pointer and
  VoiceOver still reach them). ui/composer, ui/chatlist, ui/info and
  ui/conversation register theirs (pass 5). A text field's stop goes by
  the key that takes its focus: `e.TabStopKey(stop, fs.Key)`. A part that
  keeps its own order among its controls while it shows (a calendar, a
  recording, a name being typed) calls `e.TabInBuildOrder()`; the shell
  does so itself for a list pane, the Calls content or a side pane that
  registered no stop (the Calls page until ui/calls registers `StopAll`,
  `StopMissed`, `StopNewChat`, `StopSearch`), selection mode and the
  picker. Tab stays the framework's while a modal, a menu or a popup is
  open, when a field takes Tab itself, and with macOS keyboard navigation
  off.

## 4. Strings

Every string a person reads goes through `i18n.T("…")` with **upstream's
exact English msgid** (copy it from the Rust source), `i18n.P(ctx, "…")` for
pgettext and `i18n.N(one, many, n)` for ngettext. Build dynamic text with
`c.Fmt()` (`e.C().Fmt().Str(…).Int(…).String()`), never `fmt.Sprintf` or `+`
in Build. Accessible names are the visible labels (tests find widgets by them).

## 5. Frames and animation

A frame is drawn only when something is owed. The rexui widgets ask for their
own animation frames. Animating a value of your own: `e.C().RequestFrame()`
while it moves. From a goroutine (a decode, a blob read): put the result
somewhere the next Build reads (a channel, an atomic) and call `e.S.Wake()`
(the main window) or `e.A.Window().Wake()` (your window). Never block Build;
`zf_*` calls other than `Send` and blob reads of a few KB belong on workers.
The model is drained 2 ms per frame before the screens build.

**Images.** Every window has one image cache, made by the shell (the main
window) or the window helper (Settings, viewer, call): `e.K.Images()`. Request
with `e.K.Images().Request(key, src)`, draw with `e.K.Image(…)` or read
`Use`. Never make a cache of your own and never `Poll` it: the window polls it
once per frame after `Kit.Begin` and closes it when the window closes. A
window screen that needs other budgets implements `screen.ImageTuner`
(`ImageOptions(o *img.Options)`, the viewer's 192 MiB).

**Zero allocations.** A steady frame of the main window, every screen wired,
allocates nothing (`cmd/zapfast`'s test). A screen's own allocation test may
compare against 0, not an "empty window" baseline.

## 6. Keys and menus

The shell owns the menu bar and the app commands (`ui/shell/menu.go`,
`screen.Shortcuts`, the 29 shortcuts with their owners). Commands that belong
to a screen arrive as one-shot `e.S.Req` flags (`FocusSearch`,
`FocusComposer`, `FindInChat`, `FindSettings`, `JumpToBottom`, and the
shell's own `RaiseMain`): act and clear yours. Keys your screen handles itself (Enter, ↑, PgUp/PgDn, ←/→ in the
viewer, Escape to dismiss your own state) take them with `c.TakeKey(code,
mods)`; Escape you don't take falls to the shell (closes the pane, leaves
Calls/Starred/Archived, closes the chat). The chat list publishes its visible
rows in `e.S.Visible` each frame (⌥↑/↓, ⌘1…9).

## 7. Tests

- **Behaviour tests on the fake core** (`zapfast/core/fake`): script the
  events (`f.Emit`, `f.Reply(tag, events…)`), apply them, assert on
  `f.Sent()` / `f.SentTags()`. No library, no clock, deterministic.
- **UI tests with `widget/widgettest`**, by role and name, never pixels:
  build a `state.State` with `AddAccount("1", nil, f)`, a `shell.New(s, set)`
  whose `screen.Set` has your screen in its slot (the others may be nil), and
  `widgettest.Open(t, shell.Config(s), w, h, 1, sh)`; then `r.Play(widgettest.Click(ui.RoleButton, "…"), …)`,
  `r.Find(role, name)`, `r.Tree()`. See `cmd/zapfast/shell_test.go`, and the
  framework's `examples/notes/notes/notes_test.go` and `cmd/pilot/pilot/*_test.go`.
- **The demo core** (the real library, `$ZAPFAST_CORE`) for an end-to-end
  test where it helps: `core.Open(core.Config{Demo: true})`, skipped without
  the library. Never a real account.
- Gates per branch: `gofmt -l`, `go vet ./...`, `go test ./...` in `gnui/`.
  No pixel or benchmark gates; keep Build allocation-free anyway (reserve,
  reuse, no closures per row).

## 8. Your report

Head commit; what works; what is left; **framework gaps** (what you worked
around, and how); **core needs** (a command or event the ABI lacks, used
through the fake core meanwhile); **foundation needs** (state or model API).
