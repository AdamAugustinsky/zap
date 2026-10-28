# Zap Go backend: map from zfcore to zapfast-go (read-only investigation, 2026-10-28)

Sources read: `~/Projects/zap/zfcore/{abi/zf.schema,src/*.rs,README.md}`, `~/Projects/zap/gnui/core/{core.go,fake/fake.go,voice.go,frames.go}`,
`~/Documents/projects/a3-lab/zapfast-go` at HEAD `3104c46` plus another session's uncommitted edits (in `internal/backend`, `internal/archive`, and
the untracked `internal/zzbackend`/`internal/zzbase`, which look like benchmark "before" copies that nothing imports). Abbreviations:
**B** = `internal/backend`, **A** = `internal/archive`, **M** = `internal/media`, **P** = `internal/playback`, **App** = `internal/app/controller.go`,
**Main** = `cmd/zapfast/main.go`. A quoted string such as `"send-text"` is a `model.Action.Kind` dispatched in `B/actions.go` `(*worker).execute`,
or a `model.Event.Kind` emitted by the worker. Every Zap command maps 1:1 to an upstream `zapfast::backend::Command` (`zfcore/src/dto.rs` `command()`).

## 1. Command and event map

### Linking and accounts
| Zap | zapfast-go | Notes / semantic differences |
|---|---|---|
| PairWithPhone 106 | `"pair-phone"` → `client.PairPhone` | Go emits `"pair-code"`; Zap carries the code in `Link{Unlinked{pair_code,pairing_phone}}` |
| Reconnect 11 | B/qr.go `connect` (`"connect"`) | |
| SetOnline 10 | `"presence"` → `SendPresence` | |
| Unlink 107 | `"logout"` → `client.Logout` | |
| RemoveAccount 108 | App `"remove-account"` + `config.DeleteArchiveKey` | app level, not worker level; Zap expects `AccountRemoved` from the core |
| StartOverArchive 110 | `internal/app/start_over.go` (untracked) | app level |
| SetProxy 109 | `"save-settings"` → `client.SetProxyAddress` | Go keeps proxy in `model.Settings` |
| Resumed 78 | gap | trivial: reconnect check after sleep |
| ev Link 10 | `"status"`/`"qr"`/`"pair-code"`/`"logged-out"` (B/qr.go, backend.go `handle`) | Go status strings → `LinkStatus` union |
| ev Me 11 | `"profile"` (name/about) + `client.Store.ID/LID` | partial: no single event |
| ev AccountRemoved 29 | App only | |
| ev Syncing 22, SyncProgress 23 | gap | `events.HistorySync` carries progress; Go only sends `"history-status"` text for on-demand requests |
| ev Info 25, Error 26 | `"error"` events + `Execute`'s returned `error` | Go reports most failures synchronously; Zap expects events |
| ev EventDropped 1, Fatal 2 | none (zfcore's own) | Go equivalents: a record over its bound, a recovered worker panic |

### Chats and messages
| Zap | zapfast-go | Notes |
|---|---|---|
| LoadChat 1 | `"select-chat"`/`"load-older"` → `A.Messages(chat, beforeID, 75)` | Zap pages by `PageKey{timestamp,id}`; upstream `PAGE=60` (`src/backend.rs:210`). Go pages by message id with 75 rows. Zap wants `Messages{older,complete}` in chunks |
| LoadUntil 28 | `"select-chat"`+MessageID → B/navigate.go `emitPageThrough` (`A.MessagesThrough`) | |
| FetchOlder 21 | B/actions.go `fetchOlder` (`BuildHistorySyncRequest`, 100) | `explicit` flag not modelled |
| ReloadHistory 22 | B/profile.go `"reload-history"` | |
| SetHistoryPrefetch 23 | gap | upstream `src/backend/worker/prefetch.rs` (5 pages, 10 recent chats) |
| EnsureChat 34 | B/local.go `openContact` (emits `"open-chat"`) | partial |
| MarkRead 6, MarkUnread 18 | queued `"mark-read"`/`"mark-unread"` (B/actions.go `queue`/`applyQueued`, B/read_receipts.go) | |
| SetMuted 7, SetArchived 99, SetPinned 103, SetFavorite 105, ClearChat 102, DeleteChat 101, DeleteLocal 37 | queued `"mute"`,`"archive"`,`"pin"`,`"favorite"`,`"clear-chat"`,`"delete-chat"`,`"delete-local"` | durable app-state outbox (`A.Enqueue`/`Pending`) |
| SetLocked 40 | queued `"set-chat-locked"` + B/chat_lock.go | Go adds a local folder code (PBKDF2) that Zap lacks: drop it or keep it behind the UI |
| SetKeepChatsArchived 77 | queued `"keep-archived"` | |
| SaveDraft 5 | `"draft"` → `A.SaveDraftMentions` | Zap's draft has no mentions |
| Composing 4 | `"typing"` (respects `Settings.SendTyping`) | |
| WatchReceipts 19 | `"message-info"` (tracks `w.infoChat/infoMessage`) | Zap's `None` stops following; Go has no stop |
| MarkPlayed 58 | B/read_receipts.go `markPlayed` | |
| EditText 35 | `"edit"` | Go drops mentions on edit |
| Revoke 36, React 98 | `"revoke"`, `"react"` | |
| Download 25 | B/media.go `downloadTarget` | Zap's `card: opt u64` becomes Go's target `"card:N"`; `manual` is ignored; 64 MiB cap in both |
| DownloadMotion 26 | gap | motion photos are not parsed |
| FetchAvatar 27 | B/media.go `avatar` (`GetProfilePictureInfo` preview) | `full=true` is a gap; Go stores the path on `Chat.Avatar` |
| ev Chats 12 | `"chats"` (whole list, one event) | Zap chunks: 256 rows/512 KiB, `batch`/`last` |
| ev ChatUpdated 13 | `"chat"` | `model.Chat` lacks `LastMessage` (has a `Preview` string), `group_subject_known`, `group_description`, `unread_mention`; `NotificationSound` is a string |
| ev Messages 14 | `"messages"`/`"older"` (+`HasOlder`) | |
| ev MessageUpdated 15, Incoming 16 | `"message"` (`Status:"update"` / `FromHistory:false`) | `model.Message` lacks LinkPreview, Motion, video note/gif flags, doc pages, PhoneOnly/view-once, Call content, rich `PollState`, `history_order` semantics, and persisted mentions (`Mentions` is `json:"-"`); `Quote` has 4 fields where Zap's `Quoted` has 7; Kind and Status are strings |
| ev Typing 18 | `"typing"` | |
| ev Presence 19 | gap | `events.Presence` is not handled and there is no `SubscribePresence` |
| ev Drafts 20 | `"draft"`, one per chat on select | Zap wants every draft in one batch at start |
| ev MessageDeleted 47 | `"deleted"` | |
| ev ChatRemoved 48, ChatCleared 49 | implicit (queue apply re-emits chats) | explicit event is a gap |
| ev OlderFetched 58 | `"older"` HasOlder + `"history-status"` | |
| ev Receipts 63 | `"message"` with `Message.Receipts` (`A.ReceiptDetails`) | |
| ev Avatar 46 | chat avatar path through `"chat"` | |

### Sending
| Zap | zapfast-go | Notes |
|---|---|---|
| SendText 2 | `"send-text"` → `sendWithID` (durable outbox, `"retry-send"`) | |
| SendLocation 3 | gap | parse only (B/parse.go:119) |
| SendImage 8 (RGBA blob) | gap → encode, then `"send-media"` | Go sends files only |
| SendVoice 9 (PCM f32 blob) | `"send-voice"` wants an Ogg path + Waveform + Seconds; P/opus_native.go is cgo | needs an encoder (§3) |
| SendFiles 39 | `"send-media"`, one path per action | Zap sends up to 64 paths with caption/mentions/quote; `documents` = MIME override |
| SendSticker 59 | B/stickers.go `sendSticker` | |
| SendGif 95 | Main `"send-gif"` (`M.DownloadGIF` → send-media) | |
| Forward 16 | `"forward"`: one message to one target | Zap: an ordered list of messages to a list of chats |
| ForwardLimit 17 | gap | |
| CreatePoll 13, VotePoll 14 | `"create-poll"`, `"vote"` (B/polls.go) | |
| RefreshPoll 12 | B/polls.go `refreshPoll`, B/poll_history.go | internal only; no action |
| ReplyInteractive 15 | `"interactive"` → B/interactive.go `InteractiveReply(index)` | Zap's `(button, choice)` flattens to Go's single index |
| ev SendRefused 28 | gap | Go queues offline sends, but Zap's UI expects a refusal and restores the composer |
| ev PollCreated 31, PollVoted 33 | `Execute` error | |
| ev InteractiveReplyState 30, ForwardLimit 32 | gap | |

### Media and blobs
| Zap | zapfast-go | Notes |
|---|---|---|
| PrepareClipboardImage 82 → ev ClipboardImage 27 | `cmd/zapfast/shell.go` → `M.PNGClipboard` | Zap returns a `DecodedImage` RGBA blob |
| InspectPicture 89 → ev PictureInspected 52; ApplyPictureEdit 90 → ev PictureEdited 53 | gap | pure Go: EXIF orientation, quarter turns, crop, JPEG/PNG out |
| SaveAttachmentAs 79, SaveAttachments 81 | `M.CopyFile` (UI-driven in Go) | Zap's core asks for a Save dialog via PickRequest, then copies |
| ev Media 56 | `"message"` update with `Media.Path` | |
| ev Motion 57 | gap | |
| LoadChatMedia 24 → ev ChatMedia 41 | gap | upstream `GALLERY_PAGE=200` (`src/archive.rs:427`) |
| ListChatShared 33 → ev ChatShared 39, ChatSharedChanged 40 | gap | needs media/doc/link indexes in A |
| LoadStorageStats 111, ClearMediaCache 112 → ev StorageStats 44 | gap | walk the cache dir |

### Search
| Zap | zapfast-go | Notes |
|---|---|---|
| SearchMessages 29 → ev SearchHits 45 | `"search"` Target=global → `A.SearchRange(..., 100)` | |
| SearchChatMessages 30 → ev ChatHits 38 | `"search"` with chat + Start/End | upstream `CHAT_SEARCH_LIMIT=80`, Go uses 100; `truncated` is supported |
| LoadStarred 32 → ev StarredList 37 | `"search"` Target=starred (needs a query) | Zap lists every starred message, newest star first (`STARRED_LIMIT=200`), with `starred_at`; Go has no star time |

### Stickers and GIFs
| Zap | zapfast-go | Notes |
|---|---|---|
| RecentStickers 97 → ev Stickers 55 | B/stickers.go `emitStickers`/`emitStickerPacks` | Zap's rows are Favorite/Pack/PackSticker/Recent/Received/Tagged; a Go `Sticker` has one `Emoji` string, not a list |
| SaveSticker 60, RemoveRecentSticker 61, ForgetSticker 62 | `"favorite-sticker"`/`"add-sticker"`, `"remove-recent-sticker"`, `"delete-sticker"` (`localSticker`) | |
| CreateStickerPack 91, DeleteStickerPack 83, SetStickerPack 92 | `"create-sticker-pack"`, `"delete-sticker-pack"`, `"add-sticker"`+PackID | Zap names packs by dir and stickers by path; Go uses ids |
| AddStickerPack 85, ViewStickerPack 84 → ev StickerPackPreview 54 | `"import-sticker-pack"`, `"download-sticker"` (B/sticker_packs.go) | partial |
| SendStickerPack 86 | `"share-sticker-pack"` → `shareStickerPack` (uses `M.PackArt`, which needs WebP) | |
| ImportStickerUrl 63 | Main `"signal-import"` → `M.ImportSignalPack` | |
| PickStickerArchive 64 | Main `"import-stickers"` → `M.ImportPack` | plus PickRequest |
| PickStickerPicture 87 → ev StickerPicture 51 | UI picker + `M.decodedPicture` | plus PickRequest |
| MakeSticker 88 | Main `"make-sticker"` → `M.StickerCrop` (WebP is cgo) | |
| SearchGifs 96 → ev Gifs 50 | Main `"gif-search"` → `M.SearchGIFs(key, query, proxy)` | Zap's `GifFailure.bad_key` |

### Labels, stars, pins
| Zap | zapfast-go | Notes |
|---|---|---|
| CreateLabel 41, UpdateLabel 42, DeleteLabel 43 → ev Labels 34 | B/local.go `localLabel` (`"label"`), max 20 | Go labels have no `created_at`; Zap's id is str(64) |
| SetChatLabels 44 | `"label-chat"` | |
| SetStar 31 → ev Stars 35, StarChanged 36 | queued `"star"`; `Message.Starred` | Zap wants a per-chat id list and a `local` flag |
| LoadPins 20, SetMessagePinned 104 → ev Pins 42, PinChanged 43, PinLimit 24 | gap | message pins (`PinInChatMessage`) are not implemented; `"pin"` is chat pinning |

### Groups, contacts, privacy
| Zap | zapfast-go | Notes |
|---|---|---|
| SetGroupName 73 | `"rename-group"` | |
| SetGroupPicture 75 (jpeg blob) | `"group-photo"` (path, `readBounded` 8 MiB) | change it to take bytes |
| PickGroupPicture 74 | UI-side (PickRequest, then the UI crops) | |
| LeaveGroup 100 | `"leave-group"` | no `archive` flag |
| PreviewInvite 115 → ev InvitePreview 68; JoinInvite 116 → ev InviteJoined 69 | `"group-invite-info"` (`"group-preview"`), `"join-group"` (B/profile.go) | |
| SaveContact 93 | `"contact"`+ChatID → `localContact` (`to_phone` → queued `"contact-sync"`) | |
| NewContact 94 → ev ContactReady 71, ContactFailed 70 | `"open-contact"` → `IsOnWhatsApp` | Zap correlates by `request` u64 |
| ev Contacts 17 | `"contacts"` | Go has `{ID, Name}`; Zap has full/first/push names |
| SetProfile 71 | `"profile-name"`, `"profile-about"` | |
| PickProfilePicture 72 | PickRequest + `"profile-photo"` | |
| FetchAccountPrivacy 113, SetAccountPrivacy 114 → ev AccountPrivacy 60, Saved 61, Failed 62, ReceiptsPrivacy 59 | `"privacy-get"`/`"privacy-set"` → `"privacy"` (`map[string]string`, B/profile.go `privacyMap`) | Saved/Failed come from the returned error |
| ev GroupSaving 75 | gap | trivial |

### Settings, pickers, files (mostly UI- or OS-side)
| Zap | zapfast-go | Notes |
|---|---|---|
| PickFiles 38 → ev Picked 21 | UI-side (MyGo dialogs) | the Go backend emits `PickRequest` 76 and waits for `PickAnswer` 117 |
| PickNotificationSound 65 → ev NotificationSoundPicked 67; PickChatSound 67 → ev ChatSoundPicked 64 | `config.ImportSound` (validates and copies privately) | plus PickRequest |
| SetChatSound 66 | `"set-chat-sound"` (B/chat_sound.go) | Zap's union becomes Go's string form |
| PickDownloadFolder 68 → ev DownloadFolderPicked 65; SetDownloadFolder 76 | `Settings.DownloadFolder` (config) | |
| PickWallpaperImage 69 → ev WallpaperImagePicked 66; RemoveWallpaperImage 70 | UI settings (`internal/ui/settings.go`) | copy into the state dir |
| OpenLog 80 → ev OpenPath 77 | UI/OS-side | the core only echoes the path |
| PickAnswer 117, ev PickRequest 76 | UI/OS-side | the Go backend needs a request broker (§4, WP6) |

### Calls (out of scope)
StartCall 45, JoinCallLink 46, CancelCall 47, AnswerCall 48, DeclineCall 49, HangupCall 50, SetCallMuted 51, SetCallCamera 52, SetCallMicrophone 53,
SetCallSpeaker 54, SetCallCameraDevice 55, RefreshCallDevices 56 and LoadCallLog 57, and the events Call 72, CallDevices 73 and CallLog 74, are gaps in zapfast-go.
whatsmeow provides signaling only (`events.CallOffer`/`CallTerminate`, `client.RejectCall`), with no media. Two cautions:

- **Rust calls cannot run beside the Go backend.** A Rust call engine would need the same linked device and its own socket, so the two would replace each other's stream (`StreamReplaced`). Keeping calls in Rust means a new bridge: the Go backend relays call stanzas to the Rust stack.
- **Phase 1:** refuse call commands with `ErrState`, and build CallLog entries from `CallOffer`/`CallTerminate` (partial).

### Demo
DemoIncoming 900, DemoTyping 901 and DemoPanic 902 have no zapfast-go equivalent. Its `internal/demo` is a static fixture that App `demoAction` pages through.

## 2. zapfast-go's structure, and the adapter

**Its model.** `model.Backend{Execute(ctx, model.Action) error; Events() <-chan model.Event; Close()}` (`internal/model/model.go`).

- **Worker.** One goroutine per account (`B/backend.go` `run`) selects over commands (capacity 32, a 60 s timeout each, and `Execute` blocks for the result), whatsmeow events, the outbox retry and the poll-history pump.
- **Events.** Loose string-kinded `model.Event`s.
- **Snapshot.** `App` folds those events into an immutable `model.Snapshot` that the MyGo UI reads.
- **Storage.** The archive keeps `model.Message` and `model.Chat` as JSON payloads plus the raw protobuf (`A/store.go:103`).

Zap is different: commands are fire-and-forget (`Send` must not block the UI thread), and results come back as typed events with bounded fields, chunked lists, request ids and blobs.

**Import is impossible.** All of zapfast-go's code is under `internal/`, which Go forbids importing from another module (`zapfast`). Making it public would mean restructuring a repository with another session's uncommitted work. A thin adapter (abi.Command → model.Action → model.Event → abi.Event) would also lose information: mentions, `PageKey`, request ids, multi-forward, blobs and chunk semantics. It also inherits the blocking single-queue worker, in which a 60 s upload stalls chat loading.

**Recommendation: copy and own.**

1. Wait for the other session to commit, then copy at a pinned hash, keeping the MIT attribution (zapfast-go `LICENSE`) and adding whatsmeow's MPL-2.0 (already on Zap's allow-list, `scripts/gnui/licences.py:40`).
2. Replace `execute`'s string switch with an `abi.Command` switch.
3. Replace `emit(model.Event)` with typed abi event builders.
4. Keep `model.Message`/`model.Chat` as the archive row types at first, extend them with Zap's missing fields, and convert in one file, the role `dto.rs` plays.
5. Split slow work (upload, download, history) off the worker loop.

**Package reusability** (cgo packages measured with `go list -deps -f '{{.CgoFiles}}'`):

| Package | Reuse | Blockers |
|---|---|---|
| `internal/model` | copy, extend | none |
| `internal/archive` | copy, extend schema | `mutecomm/go-sqlcipher/v4` (cgo; its `static_mock.go` builds under CGO_ENABLED=0 but fails at runtime) |
| `internal/backend` | copy, rewrite dispatch/emit | sqlcipher (via archive), `internal/media` (cgo WebP), `zerolog` (3 uses, only `Nop().WithContext`, removable); no MyGo |
| `internal/media` | copy everything except `webp_native.{go,c,h}` | cgo libwebp static (`webp_native.go`); the `!cgo` stub returns errors, which breaks MakeSticker, PackArt and WebP decode-frames |
| `internal/migration` | copy for importing existing Zap (Rust) accounts | sqlcipher; `x/sys/{unix,windows}` (fine) |
| `internal/config` | copy `sound.go`, `lock.go`, `ArchiveKey` | `zalando/go-keyring` (darwin execs `/usr/bin/security`, no cgo); Zap has its own `KeyringService` |
| `internal/diagnostics` | optional | none |
| `internal/playback` | do not copy, except `ogg.go` (Ogg muxing, `voiceWaveform`) | cgo libopus (`opus_native.go`), AVFoundation (`native_darwin.go`), `gen2brain/malgo` (cgo miniaudio) |
| `internal/demo`, `internal/app`, `internal/ui`, `cmd` | no | MyGo (`egoist/mygo`), snapshot model |

**cgo on the backend path.** Exactly two packages use cgo:

- `github.com/mutecomm/go-sqlcipher/v4` v4.4.2, which bundles SQLCipher and libtomcrypt C.
- zapfast-go's own `internal/media`, which links static libwebp.

whatsmeow `c386243a72ba`, `go.mau.fi/libsignal` v0.2.2, `go.mau.fi/util` v0.10.1, protobuf, `x/crypto`, `x/net/proxy` and go-keyring are all cgo-free. Off the backend path, `playback` adds libopus, AVFoundation and malgo. `CGO_ENABLED=0 go build ./internal/backend` succeeds, which hides the problem, because both cgo packages ship stubs that fail at runtime.

**How to stay CGO_ENABLED=0.**

- **SQLite: decision needed.** One option is to load a bundled `libsqlcipher` with purego behind a small `database/sql` driver, the framework's pattern. This keeps SQLCipher compatibility with existing Zap and upstream archives (required by `migration.Import`/`ImportRustSession`). The other is `ncruces/go-sqlite3` (wasm, pure Go) with its adiantum/xts VFS. That one is not SQLCipher-compatible, so migration would need a SQLCipher reader anyway. whatsmeow's `sqlstore.NewWithDB(db, "sqlite3", …)` works with any driver registered as `sqlite3`.
- **WebP encode, and Opus encode/decode:** load bundled `libwebp`/`libopus` dylibs with purego. AudioToolbox's Opus codec is a macOS-only alternative that needs checking.

## 3. What zfcore provides that is not WhatsApp protocol work, and must exist in Go

- **Conn mechanics** (`zfcore/src/lib.rs`, `cores.rs`, `wake.rs`). Per-account open with `Account`, `StateDir`, `CacheDir`, `KeyringService` and `DeviceName` (`core.Config`). Start versus open: `spawn_early` lists cached chats before start, in about 4 ms for 500 chats. Refusal codes ERefused, EState and ENotFound (`gnui/core/errors.go`). Close with a timeout, answering every unanswered PickRequest. In process, `Wake` becomes a direct callback, so the pipe and the event goroutine in `core.go` are not needed.
- **Event encoding and chunking** (`dto.rs` `Out::chunked`, `clip`, `toast`). Lists split at N rows or 512/576 KiB under one `batch`, with `last`, and `complete`/`truncated` only on the last chunk. Display text is cut at grapheme boundaries to `str(N)`. A record over its bound becomes `EventDropped`. Recommendation: encode each event with `abi.(*Event).Encode` into a byte queue and decode in `Drain`, as `core/fake/fake.go` does, so the existing golden tests and bounds still apply.
- **Blobs** (`blobs.rs`, 314 lines). Caller blobs (`PutBlob`) live until a command takes them or `FreeBlob`. Event blobs (thumbnails, `DecodedImage`, voice PCM) are deduplicated by content and kept to `EVENT_BUDGET` (64 MiB), largest-first expiry past a grace period. `ReadBlob` reads ranges.
- **Voice** (`voice.rs` plus upstream `src/voice.rs`).
  - **SendVoice:** f32 PCM at 48 kHz mono, at most 15 minutes, length a multiple of 4. Normalize (peak 0.89, gain at most 10×), measure seconds, compute a 64-bar waveform, then encode Ogg/Opus.
  - **DecodeVoice** (`core.VoiceDecoder`, used by `ui/conversation/av/decode.go`): Ogg/Opus → PCM blob plus waveform, at most 30 minutes / 64 MiB. Only paths under the account's own folders, the download folder or the demo media folder are read, after resolving symlinks.
- **Frames** (`frames.rs`) and `CallGeneration` (`core.Frames`/`CallNumbers`): calls only. The UI type-asserts them, so the Go backend can omit them unless the demo keeps simulated calls.
- **Logging** (`logging.rs`, `zf_set_log`). One slog-format log for the process, with WhatsApp library lines reduced to fixed categories. In Go: `slog` plus a redacting `waLog` adapter (zapfast-go uses `waLog.Noop` and `internal/diagnostics`). Never log numbers, message text, QR codes or keys.
- **Dialog and file brokering.** Every `Pick*` command becomes a `PickRequest` that waits for `PickAnswer`, then the backend does the work: copy the wallpaper or sound privately, import a sticker archive, save attachments. `OpenLog` becomes `OpenPath`.
- **Demo core** (`demo.rs`, 2,773 lines).
  - **Data and playback:** 7 upstream sample chats including the locked Dorothy chat, avatars and posters embedded from `assets/`, media and documents (`tiny_pdf`), synthesized speech voice notes (Opus encode needed, or committed `.ogg` fixtures), stock stickers, GIF answers, receipts, storage, privacy and shared media. Sends progress from pending to sent, delivered and read on a timer queue.
  - **Calls:** simulated, with I420 test-pattern frames at about 15 fps; `zf-demo:call`/`zf-demo:video-call` ring.
  - **Disposition:** an exhaustive per-command table of Answers / Accepts / Refuses with `ZF_E_STATE` before any blob is taken (`demo.rs:57-300`). 44 command variants answer, 8 accept, and the rest are refused.
  - **Who depends on it:** about 15 test files open `core.Open(core.Config{Demo:true})` (`ui/{picker,info,conversation,conversation/av,msginfo,calls}/demo_test.go`, `cmd/zapfast/shell_test.go`, …). They skip when the dylib is missing, so a Go demo makes them run everywhere. `-demo` in `cmd/zapfast/diag_dev.go:156` also depends on it.

## 4. Proposed layout and work packages

```
gnui/gocore/            Open(core.Config) core.Conn: command queue, Drain, wake, Close, PickRequest broker, ErrState/ERefused   (WP1)
gnui/gocore/blobs/      put/take/read/free, event budget, content dedup                                                         (WP1)
gnui/gocore/out/        typed event builders: chunking, batch ids, clip/toast, EventDropped                                    (WP1)
gnui/gocore/demo/       port of zfcore/src/demo.rs + embedded assets (go:embed from ../../zfcore/assets copies)                (WP2)
gnui/gocore/store/      archive (copied A, extended) + sqlite driver choice + whatsmeow sqlstore container + keyring key        (WP3)
gnui/gocore/av/         libopus/libwebp via purego, Ogg mux (from P/ogg.go), normalize/waveform, DecodeVoice, picture ops,
                        sticker maker, GIF search, Signal/.wastickers import (copied M minus cgo)                                (WP5)
gnui/gocore/wa/         whatsmeow engine (copied B): abi dispatch, convert.go (model⇄abi), sub-workers for transfers           (WP4)
gnui/gocore/migrate/    existing Zap/upstream Rust archive + session → Go (copied internal/migration)                          (WP6)
gnui/cmd/zapfast/       engine switch (-engine=go|rust) in main_release.go / diag_dev.go                                        (WP6)
```

| WP | Owns | Depends on | Definition of done (all Go tests, no live account, `CGO_ENABLED=0 go test -race`) |
|---|---|---|---|
| **WP1 Conn shell** | `gocore/`, `gocore/blobs`, `gocore/out` | none (first; freeze the `Engine` interface early) | `var _ core.Conn = (*gocore.Conn)(nil)`. Port of `core/core_test.go`/`close_test.go` semantics against a stub engine. Chunking: 1,000 chats → chunks of ≤256 rows/≤512 KiB under one batch, `last` once. A string over its bound is clipped, and an unclippable record becomes `EventDropped`. Blob take/free/expiry/range tests. A test iterating every `abi.CommandTag` proves each is dispatched or refused |
| **WP2 Go demo** | `gocore/demo` | WP1 interface; WP5 for voice (or `.ogg` fixtures) | Disposition table equals zfcore's (golden list from `demo.rs:57`). The `Demo:true` tests (`ui/*/demo_test.go`, `core/demo_round_test.go`) pass with `gocore` and no dylib. DemoIncoming/DemoTyping timing, and DemoPanic giving `Fatal`. Optional: simulated calls with `core.Frames` |
| **WP3 Store** | `gocore/store` | none (parallel with WP1) | Ported zapfast-go `A/store_test.go` passes. `sqlstore.Upgrade` succeeds on the chosen driver. Wrong key fails closed, accounts are isolated. Schema additions for pins, label `created_at`, `starred_at`, mentions, call log, shared-media/link index. `go list -deps` shows no CgoFiles |
| **WP4 WhatsApp engine** | `gocore/wa` | WP1, WP3 (WP5 for voice/sticker sends) | whatsmeow calls sit behind a small client interface with a fake. Injected `events.Message`/`HistorySync`/`Receipt`/`AppState` fixtures produce the expected abi event sequences. Every non-call command has a test. The §1 gaps are closed: SendLocation, SendImage, multi-forward, message pins, Presence, Sync progress, SendRefused, Drafts batch, ChatShared/ChatMedia, StorageStats, ForwardLimit, GroupSaving. Could split into 4a (link/chats/messages/send) and 4b (labels/stars/pins/groups/privacy/stickers) |
| **WP5 AV and pictures** | `gocore/av` | none | PCM → Ogg/Opus → PCM round-trip within tolerance. `DecodeVoice` passes `core/fake/voice_test.go`'s contract. Normalization and 64-bar waveform match `voice.rs` fixtures. WebP sticker is 512×512 and ≤100 KiB. Inspect/ApplyPictureEdit EXIF cases (orientation 6, turns, PNG when transparent). Missing dylibs give a typed error |
| **WP6 Pickers, settings, migration, wiring** | `gocore/migrate`, broker hooks in `gocore/wa`'s settings file, `cmd/zapfast` switch, logging | WP1 (broker), WP3 (migration) | Pick* → PickRequest → PickAnswer → *Picked flows tested with scripted answers. Close answers outstanding picks. Synthetic Rust archive/session imports (ported `migration/*_test.go`). `-engine=go -demo` opens. A log redaction test |

**Order.** Start WP1, WP3 and WP5 in parallel. WP2 starts once WP1's engine interface is fixed. WP4 follows WP1 and WP3. WP6 last, with the migration part after WP3.

**Conflict rules.** Each WP writes only its own directories. `abi/` and `zf.schema` are not changed: the Go backend speaks the existing ABI, so the Rust core stays a fallback for A/B runs until parity.

## Summary
1. All 120 commands and 70 events are mapped. zapfast-go covers about 60% directly. The real gaps are calls, message pins, shared/chat media, storage stats, presence, sync progress, SendLocation/SendImage, multi-forward, SendRefused, motion photos and picture edit; pickers and OpenLog are UI/OS-side.
2. zapfast-go's code is all under `internal/` with a blocking `Execute(Action)` and snapshot model. Copy and own it (pinned hash, MIT attribution) with an abi dispatch and a single model⇄abi converter, rather than importing it or wrapping it in a thin adapter.
3. The cgo blockers on the backend path are `mutecomm/go-sqlcipher/v4` and `internal/media`'s libwebp. Off that path, playback adds libopus, AVFoundation and malgo. All of them should become purego-loaded dylibs; whatsmeow, libsignal and go-keyring are cgo-free.
4. The Go backend must re-create zfcore's non-protocol parts: chunked bounded events, the blob table, the Pick broker, Opus voice encode/decode, logging redaction, and a full Go port of the 2,773-line demo core that about 15 test files rely on.
5. Six WPs in `gnui/gocore/{,blobs,out,demo,store,av,wa,migrate}`: start WP1 Conn, WP3 Store and WP5 AV in parallel, then WP2 Demo and WP4 WhatsApp, then WP6 wiring and migration. Each is checked by CGO_ENABLED=0 Go tests against the demo or injected whatsmeow fixtures.
