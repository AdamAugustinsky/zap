# Command and Event variants: the ABI inventory

Every variant of upstream's `Command` (174) and `Event` (74) in
`src/backend.rs` at bda924d (upstream a95d8f0, the plan, and the headless
core's file-dialog bridge), with what crosses zfcore's C ABI. Each exported
variant is one schema entry in `zfcore/abi/zf.schema` plus one `match` arm in
`zfcore/src/dto.rs`, written from the rules below.

Totals: of the 174 commands, 117 come from the interface (`Ui`), 53 are the
worker's own completions (`Internal`, never exported) and 4 are the egui
build's updater (`Updater`, replaced by the Go app's own in Z4). **Every UI
command and every event has a record, except:** `Shutdown` (not a record:
`zf_core_close` sends it), `CallVideo` (its frames go to the frame table, `src/frames.rs`, and out through `zf_frame_acquire`)
and the five `Update*` events (the Go app's own updater, Z4). That is 116 of
117 UI commands (plus zfcore's own `CancelCall`), 68 of 74 events and
nothing exported that is not listed here. The exhaustive matches
(`dto::origin` for commands, `dto::event`'s `unported` arms for the events
that never cross) mean a variant upstream adds breaks the build.

## Adding a variant

1. Add the record to `zfcore/abi/zf.schema` with the next free tag (events
   and commands each have their own tag space; never renumber). Shared
   types go in the top part.
2. Run `go run ./tools/abigen` in `gnui/`. It writes
   `zfcore/src/abi_gen.rs` and `gnui/abi/abi_gen.go`, and refuses a record
   that could exceed `ZF_MAX_RECORD` (`-sizes` lists every bound).
3. For an event: add its arm to `dto::event`. For a command: add an arm to
   `dto::command` (check everything that can be refused *before* a blob is
   taken) and classify it in `dto::origin`. A command also needs its
   `demo::disposition`: answered, accepted without a reply, or refused.
4. Regenerate the fixtures (`ZF_WRITE_FIXTURES=1 cargo test -p
   zapfast_core --lib fixtures`), then run `cargo test -p zapfast_core` and
   `go test ./...` in `gnui/`: the Go side decodes and re-encodes every
   variant byte for byte and fails when a command or event has no fixture.
5. If the demo core should answer it, add that to `zfcore/src/demo.rs`.

## Mapping rules

| Upstream | Schema |
|---|---|
| `String`, `ChatId` | `str(N)`: chat and message ids `str(128)`, names `str(1024)`, message text `str(196608)` (64 Ki UTF-16 units at 3 bytes), errors and toasts `str(4096)` |
| `PathBuf` | `path(4096)` |
| `bool`, `u8`…`u64`, `i64`, `f32`, `f64` | the same; `usize` is `u64` |
| `Option<T>` | `opt T` (Go: the value plus `HasX`) |
| `Option<Vec<T>>` | not allowed: a `list` plus a `bool` saying whether it was given |
| `Vec<T>` | `list(N) T` with a real bound; a list that can be long (messages, chats, contacts, paths, search hits, media items) is `chunk(N, 524288)` (576 KiB, `589824`, for lists holding messages) in an event of its own with `batch: u32` and `last: bool`, and `Out::chunked` splits it |
| `Box<T>` | `T` |
| tuple variants `V(a, b)` | named fields |
| tuples `(A, B)` and maps | a struct; a map is a list of `{key, value}` |
| `Result<T, String>` | a union `{ Ok = 0 { … } Err = 1 { error: str(4096) } }` |
| plain enums (`Delivery`, `PrivacyKind`) | `enum Name u8` |
| data enums (`Content`, `LinkStatus`) | `union` |
| `Vec<u8>` of pixels or files, `Vec<f32>` samples, `DecodedImage` | `blob`: events put the bytes with `Out::blob`; commands name a blob the UI put with `zf_blob_put`, and `dto::command` takes it |
| `Arc<AtomicBool>` cancel flags | a `u64` token; a separate command sets it |
| `egui::ColorImage` frames | not a record: `zf_frame_acquire` |
| `crate::app::AttachmentId` | `u64` |

A string over its bound is not cut: the record is not built, and the queue
gets `EventDropped { kind, size }` instead (an element of a chunked list is
dropped alone). Choose bounds above anything WhatsApp sends.

**Display text is the exception.** A field a person reads and nothing parses
(a chat row's or quote's summary, names, error and toast text, a link
preview's title and description, a failed download's reason) is cut to its
bound in the conversion (`dto::clip`): at a grapheme boundary, ending in
`…`. Upstream's summaries are the whole first line of a message, so dropping
on overflow would lose the chat. The full content still travels where the
message does. A list of display rows that can exceed its bound (a group's
participants, reactions on one message, the voters a poll lists) is cut to
its first elements, the counts that matter travelling separately. Ids,
paths, URLs, message bodies and anything else the UI acts on are never cut.
A `PickRequest` is never dropped, because the worker waits for the answer
(its text is cut, an over-long directory omitted).

**Records with several lists** (a chat's shared media, the sticker picker)
are one chunked list of rows, each row a union (`SharedEntry`,
`StickerEntry`); the rows arrive in order and a row belongs to the pack or
group the rows before it opened. **Results that carry a long list** (`Gifs`,
`ChatShared`) put the failure in an `error` field with one empty last chunk.
**Times** upstream holds as an `Instant` travel as the milliseconds since
(`CallUpdate.active_for_ms`). **Cancel flags** are tokens the UI chooses once
per call (`StartCall.cancel`); `CancelCall` sets the flag.

## Field shapes the generator handles

All of these occur in the schema now and round-trip in the golden test:
every fixed-width scalar (`bool`, `u8` via enums, `u16`, `u32`, `u64`,
`i64`, `f32`, `f64`), `str`, `bytes`, `path`, `blob`, `opt` of a scalar, a
string, an enum, a struct, a union and a blob, `list` of strings, paths,
scalars and structs, `chunk` of structs, nested structs, unions with empty
and non-empty variants, unions inside structs inside unions (`MediaState` in
`Media` in `Content`), and a struct standing for a recursive one
(`CarouselCard` for `InteractiveCard`'s carousel). `i32` is supported but
unused. Unsupported on purpose: recursion, `opt` of a list, lists of lists
(wrap the inner list in a struct).

Size class: `inline` fits a record as is; `chunked` needs a chunk field;
`blob` carries bytes behind a blob id; `frame` uses the frame calls.
"Record now" is whether the schema and `dto.rs` already carry it; "Slice
test" is whether `zfcore/tests/abi_slice.rs` drives it through the C ABI.

zfcore's own records are not upstream variants: events `EventDropped` and
`Fatal`; the command `CancelCall`; demo-only commands `DemoIncoming`,
`DemoTyping` and `DemoPanic`.

## Commands (174)

| # | Variant | Fields | Origin | Size class | Record now | Slice test | Notes |
|---|---|---|---|---|---|---|---|
| C1 | `RefreshPoll` (`backend.rs:131`) | chat: ChatId<br>message: String | Ui | inline | yes |  |  |
| C2 | `PollHistoryFailed` (`backend.rs:135`) | chat: ChatId<br>message: String<br>requested: std::time::Instant | Internal | inline | not exported |  | carries an Instant |
| C3 | `CreatePoll` (`backend.rs:140`) | chat: ChatId<br>draft: PollDraft | Ui | inline | yes |  |  |
| C4 | `PollCreated` (`backend.rs:144`) | chat: ChatId<br>draft: PollDraft<br>result: Result&lt;CreatedPoll, String&gt; | Internal | — | not exported |  |  |
| C5 | `VotePoll` (`backend.rs:149`) | chat: ChatId<br>message: String<br>choices: Vec&lt;usize&gt; | Ui | inline | yes |  |  |
| C6 | `PollVoted` (`backend.rs:154`) | chat: ChatId<br>message: String<br>choices: Vec&lt;usize&gt;<br>at: i64<br>result: Result&lt;String, String&gt; | Internal | — | not exported |  |  |
| C7 | `PollDecoded` (`backend.rs:161`) | vote: crate::archive::PollVote<br>choices: Option&lt;Vec&lt;usize&gt;&gt; | Internal | — | not exported |  |  |
| C8 | `SendText` (`backend.rs:165`) | chat: ChatId<br>text: String<br>quoting: Option&lt;crate::model::ReplyTarget&gt;<br>mentions: Vec&lt;String&gt; | Ui | inline | yes | yes |  |
| C9 | `SendLocation` (`backend.rs:171`) | chat: ChatId<br>latitude: f64<br>longitude: f64<br>quoting: Option&lt;crate::model::ReplyTarget&gt; | Ui | inline | yes |  |  |
| C10 | `ReplyInteractive` (`backend.rs:177`) | chat: ChatId<br>message: String<br>button: usize<br>choice: Option&lt;usize&gt; | Ui | inline | yes |  |  |
| C11 | `Forward` (`backend.rs:186`) | from_chat: ChatId<br>messages: Vec&lt;String&gt;<br>to_chats: Vec&lt;ChatId&gt; | Ui | inline | yes |  |  |
| C12 | `ForwardLimit` (`backend.rs:193`) | from_chat: ChatId<br>messages: Vec&lt;String&gt; | Ui | inline | yes |  |  |
| C13 | `Composing` (`backend.rs:198`) | chat: ChatId<br>composing: bool | Ui | inline | yes |  |  |
| C14 | `SaveDraft` (`backend.rs:203`) | chat: ChatId<br>text: String | Ui | inline | yes |  |  |
| C15 | `MarkRead` (`backend.rs:208`) | chat: ChatId<br>receipts: bool | Ui | inline | yes | yes |  |
| C16 | `MarkUnread` (`backend.rs:213`) | 0: ChatId | Ui | inline | yes |  |  |
| C17 | `WatchReceipts` (`backend.rs:216`) | 0: Option&lt;(ChatId, String)&gt; | Ui | inline | yes |  | `Option&lt;(ChatId, String)&gt;`: an `opt` of a pair struct |
| C18 | `ReadSyncFinished` (`backend.rs:218`) | chat: ChatId<br>through: i64<br>success: bool | Internal | — | not exported |  |  |
| C19 | `UnreadSyncFinished` (`backend.rs:225`) | chat: ChatId<br>marked_at: i64<br>success: bool | Internal | — | not exported |  |  |
| C20 | `LoadChat` (`backend.rs:231`) | chat: ChatId<br>before: Option&lt;PageKey&gt; | Ui | inline | yes | yes |  |
| C21 | `LoadPins` (`backend.rs:236`) | 0: ChatId | Ui | inline | yes |  |  |
| C22 | `FetchOlder` (`backend.rs:241`) | chat: ChatId<br>explicit: bool | Ui | inline | yes |  |  |
| C23 | `ReloadHistory` (`backend.rs:247`) | chat: ChatId<br>message: String | Ui | inline | yes |  |  |
| C24 | `SetHistoryPrefetch` (`backend.rs:253`) | on: bool<br>focused: Option&lt;ChatId&gt; | Ui | inline | yes |  |  |
| C25 | `LoadChatMedia` (`backend.rs:263`) | chat: ChatId<br>around: Option&lt;String&gt; | Ui | inline | yes |  |  |
| C26 | `Download` (`backend.rs:267`) | manual: bool<br>card: Option&lt;usize&gt;<br>chat: ChatId<br>message: String | Ui | inline | yes |  |  |
| C27 | `DownloadMotion` (`backend.rs:276`) | chat: ChatId<br>message: String | Ui | inline | yes |  |  |
| C28 | `FetchAvatar` (`backend.rs:281`) | id: String<br>full: bool | Ui | inline | yes |  |  |
| C29 | `LoadUntil` (`backend.rs:286`) | chat: ChatId<br>id: String<br>before: PageKey | Ui | inline | yes |  |  |
| C30 | `SearchMessages` (`backend.rs:292`) | query: String | Ui | inline | yes |  |  |
| C31 | `SearchChatMessages` (`backend.rs:296`) | chat: ChatId<br>query: String<br>from: Option&lt;i64&gt;<br>until: Option&lt;i64&gt; | Ui | inline | yes |  |  |
| C32 | `SetStar` (`backend.rs:303`) | chat: ChatId<br>message: String<br>starred: bool | Ui | inline | yes |  |  |
| C33 | `LoadStarred` (`backend.rs:309`) | — | Ui | inline | yes |  |  |
| C34 | `Starred` (`backend.rs:311`) | chat: ChatId<br>message: String<br>starred: bool<br>generation: u64<br>session: u64<br>result: Result&lt;(), String&gt; | Internal | — | not exported |  |  |
| C35 | `ListChatShared` (`backend.rs:326`) | chat: ChatId<br>request: u64 | Ui | inline | yes |  |  |
| C36 | `EnsureChat` (`backend.rs:331`) | chat: ChatId<br>name: String | Ui | inline | yes |  |  |
| C37 | `OlderSent` (`backend.rs:337`) | chat: ChatId<br>serial: u64<br>request: String | Internal | — | not exported |  |  |
| C38 | `OlderFailed` (`backend.rs:343`) | chat: ChatId<br>serial: u64<br>error: String | Internal | — | not exported |  |  |
| C39 | `GroupInfoFailed` (`backend.rs:349`) | chat: ChatId<br>request_generation: u64<br>permanent: bool | Internal | — | not exported |  |  |
| C40 | `EditText` (`backend.rs:355`) | chat: ChatId<br>id: String<br>text: String<br>mentions: Vec&lt;String&gt; | Ui | inline | yes |  |  |
| C41 | `EditSettled` (`backend.rs:363`) | chat: ChatId<br>id: String<br>generation: u64<br>accepted: Result&lt;(crate::model::Content, Vec&lt;crate::model::MentionRef&gt;), String&gt; | Internal | — | not exported |  |  |
| C42 | `Revoke` (`backend.rs:371`) | chat: ChatId<br>id: String | Ui | inline | yes |  |  |
| C43 | `DeleteLocal` (`backend.rs:375`) | chat: ChatId<br>id: String | Ui | inline | yes |  |  |
| C44 | `MessageDeletedForMe` (`backend.rs:380`) | generation: u64<br>chat: ChatId<br>id: String<br>outcome: MessageRemovalOutcome | Internal | — | not exported |  |  |
| C45 | `PickFiles` (`backend.rs:388`) | chat: ChatId<br>documents: bool | Ui | inline | yes |  | the dialog is a `PickRequest` event the UI answers with `PickAnswer` (the pick bridge, `src/pick.rs`) |
| C46 | `SendFiles` (`backend.rs:393`) | chat: ChatId<br>paths: Vec&lt;PathBuf&gt;<br>documents: bool<br>caption: Option&lt;String&gt;<br>mentions: Vec&lt;String&gt;<br>quoting: Option&lt;crate::model::ReplyTarget&gt; | Ui | chunked | yes |  |  |
| C47 | `SendImage` (`backend.rs:405`) | chat: ChatId<br>width: u32<br>height: u32<br>rgba: Vec&lt;u8&gt;<br>caption: Option&lt;String&gt;<br>mentions: Vec&lt;String&gt;<br>quoting: Option&lt;crate::model::ReplyTarget&gt; | Ui | blob | yes | yes |  |
| C48 | `SetMuted` (`backend.rs:415`) | 0: ChatId<br>1: Option&lt;i64&gt; | Ui | inline | yes |  |  |
| C49 | `SetLocked` (`backend.rs:417`) | 0: ChatId<br>1: bool | Ui | inline | yes |  |  |
| C50 | `CreateLabel` (`backend.rs:419`) | name: String<br>color_hex: String | Ui | inline | yes |  |  |
| C51 | `UpdateLabel` (`backend.rs:424`) | id: String<br>name: String<br>color_hex: String | Ui | inline | yes |  |  |
| C52 | `DeleteLabel` (`backend.rs:430`) | 0: String | Ui | inline | yes |  |  |
| C53 | `SetChatLabels` (`backend.rs:432`) | chat: ChatId<br>labels: Vec&lt;String&gt; | Ui | inline | yes |  |  |
| C54 | `SendVoice` (`backend.rs:437`) | chat: ChatId<br>samples: Vec&lt;f32&gt;<br>quoting: Option&lt;crate::model::ReplyTarget&gt; | Ui | blob | yes |  |  |
| C55 | `StartCall` (`backend.rs:446`) | chat: ChatId<br>video: bool<br>generation: u64<br>cancel: std::sync::Arc&lt;std::sync::atomic::AtomicBool&gt; | Ui | inline | yes |  | `cancel` is a u64 token the UI chooses; `CancelCall { token }` (zfcore's own command) sets it |
| C56 | `JoinCallLink` (`backend.rs:454`) | token: String<br>video: bool<br>generation: u64<br>cancel: std::sync::Arc&lt;std::sync::atomic::AtomicBool&gt; | Ui | inline | yes |  | as StartCall; `token` is the link's |
| C57 | `AnswerCall` (`backend.rs:461`) | — | Ui | inline | yes |  |  |
| C58 | `DeclineCall` (`backend.rs:463`) | — | Ui | inline | yes |  |  |
| C59 | `HangupCall` (`backend.rs:465`) | — | Ui | inline | yes |  |  |
| C60 | `SetCallMuted` (`backend.rs:467`) | 0: bool | Ui | inline | yes |  |  |
| C61 | `SetCallCamera` (`backend.rs:469`) | 0: bool | Ui | inline | yes |  |  |
| C62 | `SetCallMicrophone` (`backend.rs:471`) | 0: Option&lt;String&gt; | Ui | inline | yes |  |  |
| C63 | `SetCallSpeaker` (`backend.rs:473`) | 0: Option&lt;String&gt; | Ui | inline | yes |  |  |
| C64 | `SetCallCameraDevice` (`backend.rs:475`) | 0: Option&lt;String&gt; | Ui | inline | yes |  |  |
| C65 | `RefreshCallDevices` (`backend.rs:477`) | — | Ui | inline | yes |  |  |
| C66 | `LoadCallLog` (`backend.rs:479`) | — | Ui | inline | yes |  |  |
| C67 | `MarkPlayed` (`backend.rs:481`) | chat: ChatId<br>message: String<br>sender: String<br>receipts: bool | Ui | inline | yes |  |  |
| C68 | `SendSticker` (`backend.rs:488`) | chat: ChatId<br>path: PathBuf<br>quoting: Option&lt;crate::model::ReplyTarget&gt; | Ui | inline | yes |  |  |
| C69 | `SaveSticker` (`backend.rs:494`) | path: PathBuf | Ui | inline | yes |  |  |
| C70 | `FavoritePushed` (`backend.rs:498`) | hash: String<br>updated_at: i64<br>result: Result&lt;Vec&lt;u8&gt;, String&gt; | Internal | — | not exported |  |  |
| C71 | `FavoritesPushed` (`backend.rs:504`) | — | Internal | — | not exported |  |  |
| C72 | `FavoritesRecovered` (`backend.rs:506`) | complete: bool | Internal | — | not exported |  |  |
| C73 | `FirstNamesRecovered` (`backend.rs:511`) | complete: bool | Internal | — | not exported |  |  |
| C74 | `FavoriteFetched` (`backend.rs:515`) | hash: String<br>result: Result&lt;PathBuf, String&gt; | Internal | — | not exported |  |  |
| C75 | `RemoveRecentSticker` (`backend.rs:520`) | path: PathBuf | Ui | inline | yes |  |  |
| C76 | `ForgetSticker` (`backend.rs:524`) | path: PathBuf | Ui | inline | yes |  |  |
| C77 | `ImportStickerUrl` (`backend.rs:528`) | url: String | Ui | inline | yes |  |  |
| C78 | `PickStickerArchive` (`backend.rs:532`) | — | Ui | inline | yes |  | dialog through `PickRequest`/`PickAnswer` |
| C79 | `PickNotificationSound` (`backend.rs:534`) | mention: bool | Ui | inline | yes |  | dialog through `PickRequest`/`PickAnswer` |
| C80 | `SetChatSound` (`backend.rs:538`) | chat: ChatId<br>sound: Option&lt;crate::settings::NotificationSound&gt; | Ui | inline | yes |  |  |
| C81 | `PickChatSound` (`backend.rs:543`) | 0: ChatId | Ui | inline | yes |  | dialog through `PickRequest`/`PickAnswer` |
| C82 | `PickDownloadFolder` (`backend.rs:545`) | — | Ui | inline | yes |  | dialog through `PickRequest`/`PickAnswer` |
| C83 | `PickWallpaperImage` (`backend.rs:547`) | — | Ui | inline | yes |  | dialog through `PickRequest`/`PickAnswer` |
| C84 | `RemoveWallpaperImage` (`backend.rs:549`) | — | Ui | inline | yes |  |  |
| C85 | `SetProfile` (`backend.rs:551`) | name: Option&lt;String&gt;<br>about: Option&lt;String&gt; | Ui | inline | yes |  |  |
| C86 | `PickProfilePicture` (`backend.rs:556`) | — | Ui | inline | yes |  | dialog through `PickRequest`/`PickAnswer` |
| C87 | `SetProfilePicture` (`backend.rs:558`) | 0: Vec&lt;u8&gt; | Internal | blob | not exported |  | internal: `PickProfilePicture` picks and crops in the worker. If the Go side crops instead, add `SetProfilePicture { jpeg: blob }` |
| C88 | `SetGroupName` (`backend.rs:560`) | chat: ChatId<br>name: String | Ui | inline | yes |  |  |
| C89 | `PickGroupPicture` (`backend.rs:565`) | 0: ChatId | Ui | inline | yes |  | dialog through `PickRequest`/`PickAnswer` |
| C90 | `SetGroupPicture` (`backend.rs:567`) | chat: ChatId<br>jpeg: Option&lt;Vec&lt;u8&gt;&gt; | Ui | blob | yes |  | `jpeg` is a blob put with zf_blob_put |
| C91 | `GroupEdited` (`backend.rs:572`) | chat: ChatId<br>edit: GroupEdit<br>result: Result&lt;(), String&gt; | Internal | — | not exported |  |  |
| C92 | `ProfileSaved` (`backend.rs:578`) | name: Option&lt;String&gt;<br>about: Option&lt;String&gt;<br>picture: bool | Internal | — | not exported |  |  |
| C93 | `SetDownloadFolder` (`backend.rs:584`) | 0: Option&lt;std::path::PathBuf&gt; | Ui | inline | yes |  |  |
| C94 | `SetKeepChatsArchived` (`backend.rs:587`) | 0: bool | Ui | inline | yes |  |  |
| C95 | `Resumed` (`backend.rs:590`) | — | Ui | inline | yes |  |  |
| C96 | `SaveAttachmentAs` (`backend.rs:592`) | source: std::path::PathBuf<br>name: String | Ui | inline | yes |  | its save dialog is a `PickRequest` |
| C97 | `OpenLog` (`backend.rs:598`) | 0: PathBuf | Ui | inline | yes |  | the `ui` build's worker opens the file through `opener`; the headless worker (and the demo) answers with an `OpenPath` event that Go opens |
| C98 | `SaveAttachments` (`backend.rs:600`) | files: Vec&lt;(std::path::PathBuf, String)&gt; | Ui | chunked | yes |  | its folder dialog is a `PickRequest`; `list(128)` of `SaveFile` |
| C99 | `PrepareClipboardImage` (`backend.rs:604`) | 0: PathBuf | Ui | inline | yes |  |  |
| C100 | `DeleteStickerPack` (`backend.rs:606`) | dir: PathBuf | Ui | inline | yes |  |  |
| C101 | `StickerPackImported` (`backend.rs:610`) | result: Result&lt;String, String&gt; | Internal | — | not exported |  |  |
| C102 | `ViewStickerPack` (`backend.rs:614`) | chat: ChatId<br>message: String | Ui | inline | yes |  |  |
| C103 | `StickerPackViewed` (`backend.rs:619`) | result: Result&lt;(StickerPack, String), String&gt; | Internal | — | not exported |  |  |
| C104 | `AddStickerPack` (`backend.rs:623`) | dir: PathBuf<br>name: String | Ui | inline | yes |  |  |
| C105 | `SendStickerPack` (`backend.rs:628`) | chat: ChatId<br>dir: PathBuf | Ui | inline | yes |  |  |
| C106 | `PickStickerPicture` (`backend.rs:633`) | — | Ui | inline | yes |  | dialog through `PickRequest`/`PickAnswer` |
| C107 | `StickerPicturePicked` (`backend.rs:636`) | result: Result&lt;(PathBuf, u32, u32, bool), String&gt; | Internal | — | not exported |  |  |
| C108 | `MakeSticker` (`backend.rs:641`) | source: PathBuf<br>crop: crate::model::StickerCrop<br>transparent: bool<br>emojis: Vec&lt;String&gt;<br>chat: Option&lt;ChatId&gt;<br>quoting: Option&lt;crate::model::ReplyTarget&gt; | Ui | inline | yes |  | the sticker maker; `StickerPicture` is the picture's path and upright size (EXIF orientation applied without `ui`), no blob; no preview comes back (the dialog draws the source file) |
| C109 | `StickerMade` (`backend.rs:650`) | result: Result&lt;PathBuf, String&gt;<br>chat: Option&lt;ChatId&gt;<br>quoting: Option&lt;crate::model::ReplyTarget&gt; | Internal | — | not exported |  |  |
| C110 | `InspectPicture` (`backend.rs:656`) | target: crate::app::AttachmentId<br>path: PathBuf | Ui | inline | yes |  | AttachmentId is a u64 |
| C111 | `PictureInspected` (`backend.rs:661`) | target: crate::app::AttachmentId<br>result: Result&lt;(u32, u32), String&gt; | Internal | inline | not exported |  | AttachmentId is a u64 |
| C112 | `ApplyPictureEdit` (`backend.rs:667`) | target: crate::app::AttachmentId<br>source: crate::model::PictureSource<br>width: u32<br>height: u32<br>crop: crate::model::PictureCrop<br>turns: u8 | Ui | inline | yes |  | `source` is a union: a path, or pasted RGBA as a blob (width × height × 4, checked before the blob is taken) |
| C113 | `PictureEdited` (`backend.rs:676`) | target: crate::app::AttachmentId<br>result: Result&lt;PathBuf, String&gt; | Internal | inline | not exported |  | AttachmentId is a u64 |
| C114 | `CreateStickerPack` (`backend.rs:681`) | name: String | Ui | inline | yes |  |  |
| C115 | `SetStickerPack` (`backend.rs:686`) | pack: PathBuf<br>sticker: PathBuf<br>member: bool | Ui | inline | yes |  |  |
| C116 | `SaveContact` (`backend.rs:693`) | id: String<br>full_name: String<br>first_name: Option&lt;String&gt;<br>to_phone: bool | Ui | inline | yes |  |  |
| C117 | `ContactSaved` (`backend.rs:700`) | generation: u64<br>id: String<br>name: String<br>first_name: Option&lt;String&gt;<br>error: Option&lt;String&gt; | Internal | — | not exported |  |  |
| C118 | `NewContact` (`backend.rs:708`) | request: u64<br>phone: String<br>full_name: Option&lt;String&gt;<br>first_name: Option&lt;String&gt;<br>to_phone: bool | Ui | inline | yes |  |  |
| C119 | `ContactChecked` (`backend.rs:716`) | generation: u64<br>request: u64<br>phone: String<br>full_name: Option&lt;String&gt;<br>first_name: Option&lt;String&gt;<br>to_phone: bool<br>registered: Result&lt;bool, String&gt; | Internal | — | not exported |  |  |
| C120 | `SendGif` (`backend.rs:726`) | chat: ChatId<br>gif: Gif<br>quoting: Option&lt;crate::model::ReplyTarget&gt; | Ui | inline | yes |  |  |
| C121 | `SearchGifs` (`backend.rs:732`) | query: String<br>key: String | Ui | inline | yes |  |  |
| C122 | `RecentStickers` (`backend.rs:737`) | — | Ui | inline | yes |  |  |
| C123 | `React` (`backend.rs:738`) | chat: ChatId<br>message: String<br>emoji: String | Ui | inline | yes |  |  |
| C124 | `SetArchived` (`backend.rs:743`) | 0: ChatId<br>1: bool | Ui | inline | yes |  |  |
| C125 | `LeaveGroup` (`backend.rs:745`) | chat: ChatId<br>archive: bool | Ui | inline | yes |  |  |
| C126 | `DeleteChat` (`backend.rs:750`) | 0: ChatId | Ui | inline | yes |  |  |
| C127 | `ChatDeleted` (`backend.rs:752`) | chat: ChatId<br>deleted: bool<br>through: i64 | Internal | — | not exported |  |  |
| C128 | `ClearChat` (`backend.rs:759`) | 0: ChatId | Ui | inline | yes |  |  |
| C129 | `ChatCleared` (`backend.rs:761`) | chat: ChatId<br>cleared: bool<br>through: i64 | Internal | — | not exported |  |  |
| C130 | `SetPinned` (`backend.rs:766`) | 0: ChatId<br>1: bool | Ui | inline | yes |  |  |
| C131 | `SetMessagePinned` (`backend.rs:768`) | chat: ChatId<br>message: String<br>pinned: bool | Ui | inline | yes |  |  |
| C132 | `MessagePinned` (`backend.rs:774`) | chat: ChatId<br>message: String<br>pinned: bool<br>at: i64<br>generation: u64<br>session: u64<br>expires_at: i64<br>result: Result&lt;(), String&gt; | Internal | — | not exported |  |  |
| C133 | `SetFavorite` (`backend.rs:789`) | 0: ChatId<br>1: bool | Ui | inline | yes |  |  |
| C134 | `FavoritesSent` (`backend.rs:792`) | through: i64<br>at: i64<br>success: bool | Internal | — | not exported |  |  |
| C135 | `PairWithPhone` (`backend.rs:797`) | 0: String | Ui | inline | yes |  |  |
| C136 | `Unlink` (`backend.rs:799`) | — | Ui | inline | yes |  |  |
| C137 | `RemoveAccount` (`backend.rs:801`) | — | Ui | inline | yes |  |  |
| C138 | `Reconnect` (`backend.rs:802`) | — | Ui | inline | yes |  |  |
| C139 | `SetProxy` (`backend.rs:804`) | 0: String | Ui | inline | yes |  |  |
| C140 | `StartOverArchive` (`backend.rs:807`) | — | Ui | inline | yes |  |  |
| C141 | `SetOnline` (`backend.rs:810`) | 0: bool | Ui | inline | yes | yes |  |
| C142 | `Shutdown` (`backend.rs:811`) | — | Ui | inline | zf_core_close sends it |  | not a record: zf_core_close sends it |
| C143 | `Sent` (`backend.rs:813`) | chat: ChatId<br>id: String<br>error: Option&lt;String&gt; | Internal | — | not exported |  |  |
| C144 | `Downloaded` (`backend.rs:819`) | card: Option&lt;usize&gt;<br>chat: ChatId<br>id: String<br>result: Result&lt;PathBuf, String&gt; | Internal | — | not exported |  |  |
| C145 | `MotionDownloaded` (`backend.rs:826`) | chat: ChatId<br>id: String<br>result: Result&lt;PathBuf, String&gt; | Internal | — | not exported |  |  |
| C146 | `StickerFetched` (`backend.rs:832`) | hash: String<br>result: Result&lt;PathBuf, String&gt; | Internal | — | not exported |  |  |
| C147 | `AvatarFetched` (`backend.rs:837`) | id: String<br>full: bool<br>path: Option&lt;PathBuf&gt; | Internal | — | not exported |  |  |
| C148 | `AvatarFailed` (`backend.rs:843`) | id: String<br>full: bool | Internal | — | not exported |  |  |
| C149 | `MeInfo` (`backend.rs:848`) | about: Option&lt;String&gt; | Internal | — | not exported |  |  |
| C150 | `GifResults` (`backend.rs:852`) | query: String<br>results: Result&lt;Vec&lt;Gif&gt;, GifError&gt; | Internal | chunked | not exported |  |  |
| C151 | `LoadStorageStats` (`backend.rs:858`) | token: u64 | Ui | inline | yes |  |  |
| C152 | `ClearMediaCache` (`backend.rs:864`) | token: u64 | Ui | inline | yes |  |  |
| C153 | `StorageMeasured` (`backend.rs:869`) | stats: Option&lt;StorageStats&gt;<br>token: u64<br>cleared: bool | Internal | — | not exported |  |  |
| C154 | `Picked` (`backend.rs:875`) | chat: ChatId<br>paths: Vec&lt;PathBuf&gt;<br>documents: bool | Internal | chunked | not exported |  | not needed: the Go side answers dialogs with `PickAnswer`, and the worker emits `Event::Picked` itself |
| C155 | `Outbound` (`backend.rs:881`) | chat: ChatId<br>row: Box&lt;Message&gt;<br>raw: Vec&lt;u8&gt; | Internal | blob | not exported |  | Message: inline, its thumbnail a blob |
| C156 | `GroupRecipients` (`backend.rs:887`) | chat: ChatId<br>id: String<br>recipients: Vec&lt;String&gt;<br>lids: Vec&lt;(String, String)&gt;<br>stored: tokio::sync::mpsc::UnboundedSender&lt;bool&gt; | Internal | inline | not exported |  | carries a channel |
| C157 | `GroupInfo` (`backend.rs:895`) | chat: ChatId<br>request_generation: u64<br>name: Option&lt;String&gt;<br>description: String<br>description_generation: u64<br>participants: Vec&lt;String&gt;<br>read_only: bool<br>ephemeral_expiration: Option&lt;u32&gt;<br>ephemeral_setting_timestamp: Option&lt;i64&gt;<br>leave_generation: u64<br>info_locked: bool<br>admin: bool<br>subject_generation: u64 | Internal | — | not exported |  |  |
| C158 | `PairCode` (`backend.rs:919`) | result: Result&lt;String, String&gt; | Internal | — | not exported |  |  |
| C159 | `ReceiptsPrivacy` (`backend.rs:923`) | disabled: bool | Internal | — | not exported |  |  |
| C160 | `AccountPrivacy` (`backend.rs:927`) | values: Vec&lt;(crate::privacy::PrivacyKind, crate::privacy::PrivacyChoice)&gt;<br>failed: bool | Internal | — | not exported |  |  |
| C161 | `FetchAccountPrivacy` (`backend.rs:932`) | — | Ui | inline | yes |  |  |
| C162 | `SetAccountPrivacy` (`backend.rs:934`) | kind: crate::privacy::PrivacyKind<br>choice: crate::privacy::PrivacyChoice | Ui | inline | yes |  |  |
| C163 | `AccountPrivacySaved` (`backend.rs:939`) | kind: crate::privacy::PrivacyKind | Internal | — | not exported |  |  |
| C164 | `AccountPrivacyFailed` (`backend.rs:943`) | kind: crate::privacy::PrivacyKind | Internal | — | not exported |  |  |
| C165 | `ChannelMutes` (`backend.rs:947`) | 0: Vec&lt;(String, bool)&gt; | Internal | — | not exported |  |  |
| C166 | `ChannelPictures` (`backend.rs:950`) | 0: Option&lt;Vec&lt;(ChatId, ChannelPicture)&gt;&gt; | Internal | chunked | not exported |  |  |
| C167 | `PreviewInvite` (`backend.rs:952`) | 0: String | Ui | inline | yes |  |  |
| C168 | `JoinInvite` (`backend.rs:954`) | 0: String | Ui | inline | yes |  |  |
| C169 | `InviteJoined` (`backend.rs:956`) | code: String<br>result: Result&lt;(ChatId, bool), String&gt; | Internal | — | not exported |  |  |
| C170 | `CheckForUpdates` (`backend.rs:961`) | — | Updater | — | not exported (Go updater, Z4) |  |  |
| C171 | `InspectUpdate` (`backend.rs:962`) | — | Updater | — | not exported (Go updater, Z4) |  |  |
| C172 | `DownloadUpdate` (`backend.rs:963`) | release: crate::updates::Release<br>source: crate::updates::Source | Updater | inline | not exported (Go updater, Z4) |  | updater types |
| C173 | `InstallUpdate` (`backend.rs:967`) | prepared: Box&lt;crate::updates::Prepared&gt;<br>arguments: Vec&lt;String&gt; | Updater | inline | not exported (Go updater, Z4) |  | updater types |
| C174 | `PickAnswer` (`backend.rs:885`) | id: u64<br>paths: Vec&lt;PathBuf&gt; | Ui | inline | yes |  | the UI's answer to `PickRequest` `id`; no paths is a cancel. Added by the headless core; at most 64 paths |

## Events (74)

| # | Variant | Fields | Size class | Record now | Slice test | Notes |
|---|---|---|---|---|---|---|
| E1 | `InteractiveReplyState` (`backend.rs:975`) | chat: ChatId<br>message: String<br>pending: bool | inline | yes |  |  |
| E2 | `PollCreated` (`backend.rs:980`) | chat: ChatId<br>error: Option&lt;String&gt; | inline | yes |  |  |
| E3 | `ForwardLimit` (`backend.rs:986`) | from_chat: ChatId<br>messages: Vec&lt;String&gt;<br>limit: usize | inline | yes |  |  |
| E4 | `PollVoted` (`backend.rs:991`) | chat: ChatId<br>message: String<br>error: Option&lt;String&gt; | inline | yes |  |  |
| E5 | `Link` (`backend.rs:996`) | 0: LinkStatus | inline | yes | yes |  |
| E6 | `Me` (`backend.rs:998`) | id: String<br>lid: Option&lt;String&gt;<br>name: Option&lt;String&gt;<br>about: Option&lt;String&gt; | inline | yes | yes |  |
| E7 | `Chats` (`backend.rs:1006`) | 0: Vec&lt;Chat&gt; | chunked | yes | yes |  |
| E8 | `Labels` (`backend.rs:1008`) | 0: Vec&lt;crate::model::Label&gt; | chunked | yes |  |  |
| E9 | `Drafts` (`backend.rs:1010`) | 0: Vec&lt;(ChatId, String)&gt; | chunked | yes |  |  |
| E10 | `Stars` (`backend.rs:1012`) | chat: ChatId<br>ids: Vec&lt;String&gt; | inline | yes |  |  |
| E11 | `StarChanged` (`backend.rs:1018`) | chat: ChatId<br>message: String<br>starred: bool<br>local: bool | inline | yes |  |  |
| E12 | `StarredList` (`backend.rs:1025`) | 0: Vec&lt;crate::archive::Starred&gt; | chunked | yes |  |  |
| E13 | `ChatHits` (`backend.rs:1028`) | chat: ChatId<br>query: String<br>from: Option&lt;i64&gt;<br>until: Option&lt;i64&gt;<br>messages: Vec&lt;Message&gt;<br>truncated: bool | chunked | yes |  |  |
| E14 | `ChatShared` (`backend.rs:1040`) | request: u64<br>result: Result&lt;crate::model::ChatMedia, String&gt; | chunked | yes |  | three lists of up to 500: one chunked list of `SharedEntry` rows |
| E15 | `ChatSharedChanged` (`backend.rs:1047`) | 0: ChatId | inline | yes |  |  |
| E16 | `ChatMedia` (`backend.rs:1050`) | chat: ChatId<br>around: Option&lt;String&gt;<br>items: Vec&lt;crate::model::MediaItem&gt; | chunked | yes |  |  |
| E17 | `ChatUpdated` (`backend.rs:1055`) | 0: Box&lt;Chat&gt; | inline | yes | yes |  |
| E18 | `Pins` (`backend.rs:1057`) | chat: ChatId<br>items: Vec&lt;crate::archive::Pinned&gt; | chunked | yes |  |  |
| E19 | `PinChanged` (`backend.rs:1062`) | chat: ChatId<br>message: String<br>pinned: bool | inline | yes |  |  |
| E20 | `Messages` (`backend.rs:1069`) | chat: ChatId<br>messages: Vec&lt;Message&gt;<br>older: bool<br>complete: bool | chunked | yes | yes |  |
| E21 | `MessageUpdated` (`backend.rs:1075`) | 0: Box&lt;Message&gt; | inline | yes | yes | Message: inline, its thumbnail a blob |
| E22 | `StorageStats` (`backend.rs:1079`) | stats: Option&lt;StorageStats&gt;<br>token: u64<br>cleared: bool | inline | yes |  |  |
| E23 | `Picked` (`backend.rs:1085`) | chat: ChatId<br>paths: Vec&lt;PathBuf&gt;<br>documents: bool | chunked | yes |  | not needed: the Go side answers dialogs with `PickAnswer`, and the worker emits `Event::Picked` itself |
| E24 | `Incoming` (`backend.rs:1091`) | chat: ChatId<br>message: Box&lt;Message&gt; | inline | yes | yes | Message: inline, its thumbnail a blob |
| E25 | `Contacts` (`backend.rs:1095`) | 0: Vec&lt;Contact&gt; | chunked | yes | yes |  |
| E26 | `SearchHits` (`backend.rs:1097`) | query: String<br>messages: Vec&lt;Message&gt; | chunked | yes |  |  |
| E27 | `Typing` (`backend.rs:1101`) | chat: ChatId<br>sender: String<br>composing: bool | inline | yes | yes |  |
| E28 | `Presence` (`backend.rs:1106`) | id: String<br>online: bool<br>last_seen: Option&lt;i64&gt; | inline | yes |  |  |
| E29 | `Avatar` (`backend.rs:1111`) | id: String<br>full: bool<br>path: Option&lt;PathBuf&gt; | inline | yes |  |  |
| E30 | `MessageDeleted` (`backend.rs:1116`) | chat: ChatId<br>id: String | inline | yes |  |  |
| E31 | `ChatRemoved` (`backend.rs:1121`) | chat: ChatId | inline | yes |  |  |
| E32 | `ChatCleared` (`backend.rs:1125`) | chat: ChatId<br>through: i64 | inline | yes |  |  |
| E33 | `Gifs` (`backend.rs:1130`) | query: String<br>results: Result&lt;Vec&lt;Gif&gt;, GifError&gt; | chunked | yes |  |  |
| E34 | `StickerPicture` (`backend.rs:1136`) | path: PathBuf<br>width: u32<br>height: u32<br>transparent: bool | inline | yes |  |  |
| E35 | `PictureInspected` (`backend.rs:1143`) | target: crate::app::AttachmentId<br>result: Result&lt;(u32, u32), String&gt; | inline | yes |  | AttachmentId is a u64 |
| E36 | `PictureEdited` (`backend.rs:1149`) | target: crate::app::AttachmentId<br>result: Result&lt;PathBuf, String&gt; | inline | yes |  | AttachmentId is a u64 |
| E37 | `StickerPackPreview` (`backend.rs:1155`) | 0: Result&lt;(StickerPack, String), String&gt; | inline | yes |  |  |
| E38 | `Stickers` (`backend.rs:1158`) | favorites: Vec&lt;PathBuf&gt;<br>packs: Vec&lt;StickerPack&gt;<br>recent: Vec&lt;PathBuf&gt;<br>received: Vec&lt;PathBuf&gt;<br>emojis: std::collections::HashMap&lt;PathBuf, Vec&lt;String&gt;&gt; | chunked | yes |  | one chunked list of `StickerEntry` rows: favorites, each pack with its stickers, recent, received, then the emoji tags (a map as `Tagged` rows) |
| E39 | `Media` (`backend.rs:1165`) | card: Option&lt;usize&gt;<br>chat: ChatId<br>message: String<br>result: Result&lt;PathBuf, String&gt; | inline | yes |  | a download result: path or error |
| E40 | `Motion` (`backend.rs:1172`) | chat: ChatId<br>message: String<br>result: Result&lt;PathBuf, String&gt; | inline | yes |  |  |
| E41 | `Syncing` (`backend.rs:1178`) | 0: bool | inline | yes |  |  |
| E42 | `SyncProgress` (`backend.rs:1180`) | 0: u32 | inline | yes |  |  |
| E43 | `OlderFetched` (`backend.rs:1184`) | chat: ChatId<br>more: bool<br>silent: bool | inline | yes |  |  |
| E44 | `ReceiptsPrivacy` (`backend.rs:1190`) | disabled: bool | inline | yes |  |  |
| E45 | `AccountPrivacy` (`backend.rs:1194`) | values: Vec&lt;(crate::privacy::PrivacyKind, crate::privacy::PrivacyChoice)&gt;<br>failed: bool | inline | yes |  |  |
| E46 | `AccountPrivacySaved` (`backend.rs:1199`) | kind: crate::privacy::PrivacyKind | inline | yes |  |  |
| E47 | `AccountPrivacyFailed` (`backend.rs:1203`) | kind: crate::privacy::PrivacyKind | inline | yes |  |  |
| E48 | `PinLimit` (`backend.rs:1207`) | 0: usize | inline | yes |  |  |
| E49 | `Receipts` (`backend.rs:1210`) | 0: crate::model::MessageReceipts | inline | yes |  |  |
| E50 | `ChatSoundPicked` (`backend.rs:1212`) | chat: ChatId<br>path: std::path::PathBuf | inline | yes |  |  |
| E51 | `DownloadFolderPicked` (`backend.rs:1217`) | 0: std::path::PathBuf | inline | yes |  |  |
| E52 | `WallpaperImagePicked` (`backend.rs:1219`) | 0: Result&lt;std::path::PathBuf, String&gt; | inline | yes |  |  |
| E53 | `NotificationSoundPicked` (`backend.rs:1221`) | mention: bool<br>path: std::path::PathBuf | inline | yes |  |  |
| E54 | `InvitePreview` (`backend.rs:1226`) | code: String<br>result: Result&lt;crate::model::InviteInfo, String&gt; | inline | yes |  |  |
| E55 | `InviteJoined` (`backend.rs:1232`) | code: String<br>result: Result&lt;(ChatId, bool), String&gt; | inline | yes |  |  |
| E56 | `ContactFailed` (`backend.rs:1237`) | request: u64<br>error: String | inline | yes |  |  |
| E57 | `ContactReady` (`backend.rs:1242`) | request: u64<br>id: String<br>name: Option&lt;String&gt; | inline | yes |  |  |
| E58 | `Call` (`backend.rs:1248`) | 0: Box&lt;crate::calls::CallUpdate&gt; | inline | yes |  | `CallUpdate` struct; `started` (an Instant) is `active_for_ms` |
| E59 | `CallDevices` (`backend.rs:1250`) | 0: Box&lt;crate::calls::DeviceList&gt; | inline | yes |  | `DeviceList`: three lists of `Device`, at most 64 each |
| E60 | `CallLog` (`backend.rs:1252`) | 0: Vec&lt;Message&gt; | chunked | yes |  |  |
| E61 | `CallVideo` (`backend.rs:1254`) | local: Option&lt;Arc&lt;egui::ColorImage&gt;&gt;<br>remote: Option&lt;Arc&lt;egui::ColorImage&gt;&gt;<br>from: Option&lt;String&gt; | frame | no record: zf_frame_acquire |  | I420 (`video_frames::CallFrame` without `ui`) into `src/frames.rs`, out through zf_frame_acquire by stream (0 ours, 1 the peer, 2 + i group participant i), never a record |
| E62 | `Info` (`backend.rs:1262`) | 0: String | inline | yes |  |  |
| E63 | `ClipboardImage` (`backend.rs:1264`) | 0: Result&lt;crate::model::DecodedImage, String&gt; | blob | yes |  | the RGBA bytes are a blob |
| E64 | `UpdateAvailable` (`backend.rs:1266`) | version: String<br>url: String | inline | not exported (Go updater, Z4) |  |  |
| E65 | `UpdateSupport` (`backend.rs:1270`) | 0: Result&lt;crate::updates::Installation, String&gt; | inline | not exported (Go updater, Z4) |  | updater types |
| E66 | `UpdateProgress` (`backend.rs:1271`) | received: u64<br>total: u64 | inline | not exported (Go updater, Z4) |  |  |
| E67 | `UpdateDownloaded` (`backend.rs:1275`) | 0: Result&lt;Box&lt;crate::updates::Prepared&gt;, String&gt; | inline | not exported (Go updater, Z4) |  | updater types |
| E68 | `UpdateInstalling` (`backend.rs:1276`) | 0: Result&lt;(), String&gt; | inline | not exported (Go updater, Z4) |  |  |
| E69 | `SendRefused` (`backend.rs:1279`) | chat: ChatId<br>quoting: Option&lt;crate::model::ReplyTarget&gt;<br>unsent: Unsent<br>reason: Refusal | inline | yes |  | voice samples and RGBA come back as blobs |
| E70 | `AccountRemoved` (`backend.rs:1286`) | — | inline | yes |  |  |
| E71 | `Error` (`backend.rs:1287`) | 0: String | inline | yes |  |  |
| E72 | `GroupSaving` (`backend.rs:1290`) | chat: ChatId<br>saving: bool | inline | yes |  |  |
| E73 | `PickRequest` (`backend.rs:1105`) | 0: crate::pick::Request | inline | yes |  | a file dialog for the UI to show; every request must be answered once with `PickAnswer`. Added by the headless core |
| E74 | `OpenPath` (`backend.rs:1368`) | 0: PathBuf | inline | yes |  | a file for the UI to open (or show in its folder), sent by the headless worker for `OpenLog`; the `ui` build never sends it. Added by the headless core |
