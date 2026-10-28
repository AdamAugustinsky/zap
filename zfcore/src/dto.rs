//! Conversions between upstream's `Command`/`Event` and the schema's records.
//!
//! The matches on upstream's enums are exhaustive with no `_` arm: a variant
//! upstream adds or changes breaks this build at the merge (the ABI drift
//! alarm). The variants that never cross the ABI (the updater's) are listed by
//! name in one arm each (`unported`); `zfcore/VARIANTS.md` says why. The call
//! video frames go to the frame table (`zf_frame_acquire`), never a record.

use zapfast::backend as up;
use zapfast::model as m;

use crate::abi_gen as w;
use crate::cores::Shared;
use crate::wire::{BlobRef, TooLarge};

/// Where converted events go: encoded and queued, or dropped as
/// `EventDropped`, with the blobs a dropped record named freed again.
pub struct Out<'a> {
    shared: &'a Shared,
    created: Vec<u64>,
}

impl<'a> Out<'a> {
    pub fn new(shared: &'a Shared) -> Self {
        Self {
            shared,
            created: Vec::new(),
        }
    }

    fn blob(&mut self, bytes: Vec<u8>) -> BlobRef {
        let blob = self.shared.blobs.put_event(bytes);
        self.created.push(blob.id);
        blob
    }

    /// Encodes and queues one record; blobs created since `mark` belong to it.
    fn send_from(&mut self, mark: usize, event: w::Event) -> bool {
        let blobs = self.created.split_off(mark);
        let mut record = Vec::new();
        match event.encode(&mut record) {
            Ok(()) => {
                self.shared.queue.push(record);
                true
            }
            Err(too_large) => {
                for id in blobs {
                    self.shared.blobs.free(id);
                }
                self.dropped(event.tag(), event.name(), too_large);
                false
            }
        }
    }

    /// Queues a record that names no blob, and says whether it was queued.
    fn send_checked(&mut self, event: w::Event) -> bool {
        self.send_from(self.created.len(), event)
    }

    /// Queues a record that names no blob.
    fn send(&mut self, event: w::Event) {
        self.send_checked(event);
    }

    /// Builds and queues a record whose conversion may create blobs: the
    /// mark is taken before it, so a dropped record frees them.
    fn send_with(&mut self, build: impl FnOnce(&mut Self) -> w::Event) {
        let mark = self.created.len();
        let event = build(self);
        self.send_from(mark, event);
    }

    fn dropped(&mut self, kind: u16, name: &str, too_large: TooLarge) {
        // The variant and the field, never the content.
        log::warn!("zapfast_core: dropped a {name} record: {too_large}");
        let mut record = Vec::new();
        let dropped = w::Event::EventDropped {
            kind,
            size: too_large.len as u64,
        };
        dropped
            .encode(&mut record)
            .expect("EventDropped is a few bytes");
        self.shared.queue.push(record);
    }

    /// Splits `items` over records of at most `limits` (count, bytes), all
    /// with one batch id; the last carries `last`. An element too large for
    /// a record on its own is dropped by itself.
    fn chunked<T, U: crate::wire::Wire>(
        &mut self,
        kind: (u16, &'static str),
        items: Vec<T>,
        limits: (usize, usize),
        mut convert: impl FnMut(&mut Self, T) -> U,
        mut record: impl FnMut(u32, bool, Vec<U>) -> w::Event,
    ) {
        let batch = self.shared.next_batch();
        let mut chunk: Vec<U> = Vec::new();
        let mut bytes = 0;
        let mut chunk_mark = self.created.len();
        let mut scratch = Vec::new();
        for item in items {
            let element_mark = self.created.len();
            let element = convert(self, item);
            // Encoded alone first, so a field over its bound costs this
            // element and not the chunk around it.
            let mut writer = crate::wire::Writer::reuse(&mut scratch);
            let fits = element.put(&mut writer).and_then(|()| {
                let len = writer.len();
                if len > limits.1 {
                    Err(TooLarge {
                        field: "chunk element",
                        len,
                        max: limits.1,
                    })
                } else {
                    Ok(len)
                }
            });
            scratch = writer.finish();
            let len = match fits {
                Ok(len) => len,
                Err(too_large) => {
                    for id in self.created.split_off(element_mark) {
                        self.shared.blobs.free(id);
                    }
                    self.dropped(kind.0, kind.1, too_large);
                    continue;
                }
            };
            if !chunk.is_empty() && (chunk.len() == limits.0 || bytes + len > limits.1) {
                // The blobs of this element were created after the chunk's;
                // hold them back while the chunk is sent.
                let held = self.created.split_off(element_mark);
                self.send_from(chunk_mark, record(batch, false, std::mem::take(&mut chunk)));
                chunk_mark = self.created.len();
                self.created.extend(held);
                bytes = 0;
            }
            bytes += len;
            chunk.push(element);
        }
        self.send_from(chunk_mark, record(batch, true, chunk));
    }
}

/// Converts and queues one upstream event.
pub fn event(out: &mut Out<'_>, event: up::Event) {
    convert(out, event);
    // Every blob an event made went with a record, or was freed with the one
    // that was dropped.
    debug_assert!(out.created.is_empty(), "an event left blobs unclaimed");
}

fn convert(out: &mut Out<'_>, event: up::Event) {
    use up::Event as E;
    match event {
        E::Link(status) => out.send(w::Event::Link {
            status: link(status),
        }),
        E::Me {
            id,
            lid,
            name,
            about,
        } => out.send(w::Event::Me {
            id,
            lid,
            name: name.map(|n| clip(n, 1024)),
            about: about.map(|a| clip(a, 1024)),
        }),
        E::Chats(chats) => out.chunked(
            (12, "Chats"),
            chats,
            w::EVENT_CHATS_CHATS,
            |out, c| chat(out, c),
            |batch, last, chats| w::Event::Chats { batch, last, chats },
        ),
        E::ChatUpdated(c) => {
            let c = chat(out, *c);
            out.send(w::Event::ChatUpdated { chat: c });
        }
        E::Messages {
            chat: id,
            messages,
            older,
            complete,
        } => out.chunked(
            (14, "Messages"),
            messages,
            w::EVENT_MESSAGES_MESSAGES,
            |out, m| message(out, m),
            |batch, last, messages| w::Event::Messages {
                chat: id.clone(),
                batch,
                last,
                older,
                // Only the whole batch can say the archive has no more.
                complete: complete && last,
                messages,
            },
        ),
        E::MessageUpdated(m) => out.send_with(|out| w::Event::MessageUpdated {
            message: message(out, *m),
        }),
        E::Incoming { chat, message: m } => out.send_with(|out| w::Event::Incoming {
            chat,
            message: message(out, *m),
        }),
        E::Contacts(contacts) => out.chunked(
            (17, "Contacts"),
            contacts,
            w::EVENT_CONTACTS_CONTACTS,
            |_, c| contact(c),
            |batch, last, contacts| w::Event::Contacts {
                batch,
                last,
                contacts,
            },
        ),
        E::Typing {
            chat,
            sender,
            composing,
        } => out.send(w::Event::Typing {
            chat,
            sender,
            composing,
        }),
        E::Presence {
            id,
            online,
            last_seen,
        } => out.send(w::Event::Presence {
            id,
            online,
            last_seen,
        }),
        E::Drafts(drafts) => out.chunked(
            (20, "Drafts"),
            drafts,
            w::EVENT_DRAFTS_DRAFTS,
            |_, (chat, text)| w::Draft { chat, text },
            |batch, last, drafts| w::Event::Drafts {
                batch,
                last,
                drafts,
            },
        ),
        E::Picked {
            chat,
            paths,
            documents,
        } => out.send(w::Event::Picked {
            chat,
            paths,
            documents,
        }),
        E::Syncing(syncing) => out.send(w::Event::Syncing { syncing }),
        E::SyncProgress(percent) => out.send(w::Event::SyncProgress { percent }),
        E::PinLimit(limit) => out.send(w::Event::PinLimit {
            limit: limit as u64,
        }),
        E::OpenPath(path) => out.send(w::Event::OpenPath { path }),
        E::Info(text) => out.send(w::Event::Info { text: toast(text) }),
        E::Error(text) => out.send(w::Event::Error { text: toast(text) }),
        E::ClipboardImage(result) => out.send_with(|out| w::Event::ClipboardImage {
            result: match result {
                Ok(image) => w::ClipboardImageResult::Ok {
                    image: w::DecodedImage {
                        width: image.width as u32,
                        height: image.height as u32,
                        rgba: out.blob(image.bytes),
                    },
                },
                Err(error) => w::ClipboardImageResult::Err {
                    error: toast(error),
                },
            },
        }),
        E::SendRefused {
            chat,
            quoting,
            unsent: u,
            reason,
        } => out.send_with(|out| w::Event::SendRefused {
            chat,
            quoting: quoting.map(reply_target),
            unsent: unsent(out, u),
            reason: match reason {
                up::Refusal::Offline => w::Refusal::Offline,
                up::Refusal::QuoteUnavailable => w::Refusal::QuoteUnavailable,
            },
        }),
        E::AccountRemoved => out.send(w::Event::AccountRemoved),

        E::InteractiveReplyState {
            chat,
            message,
            pending,
        } => out.send(w::Event::InteractiveReplyState {
            chat,
            message,
            pending,
        }),
        E::PollCreated { chat, error } => out.send(w::Event::PollCreated {
            chat,
            error: error.map(toast),
        }),
        E::ForwardLimit {
            from_chat,
            messages,
            limit,
        } => out.send(w::Event::ForwardLimit {
            from_chat,
            messages,
            limit: limit as u64,
        }),
        E::PollVoted {
            chat,
            message,
            error,
        } => out.send(w::Event::PollVoted {
            chat,
            message,
            error: error.map(toast),
        }),
        E::Labels(labels) => out.chunked(
            (34, "Labels"),
            labels,
            w::EVENT_LABELS_LABELS,
            |_, l| w::Label {
                id: l.id,
                name: l.name,
                color_hex: l.color_hex,
                created_at: l.created_at,
            },
            |batch, last, labels| w::Event::Labels {
                batch,
                last,
                labels,
            },
        ),
        E::Stars { chat, ids } => out.chunked(
            (35, "Stars"),
            ids,
            w::EVENT_STARS_IDS,
            |_, id| w::MessageId { id },
            |batch, last, ids| w::Event::Stars {
                chat: chat.clone(),
                batch,
                last,
                ids,
            },
        ),
        E::StarChanged {
            chat,
            message,
            starred,
            local,
        } => out.send(w::Event::StarChanged {
            chat,
            message,
            starred,
            local,
        }),
        E::StarredList(list) => out.chunked(
            (37, "StarredList"),
            list,
            w::EVENT_STARRED_LIST_STARRED,
            |out, s| w::Starred {
                message: message(out, s.message),
                starred_at: s.starred_at,
            },
            |batch, last, starred| w::Event::StarredList {
                batch,
                last,
                starred,
            },
        ),
        E::ChatHits {
            chat,
            query,
            from,
            until,
            messages,
            truncated,
        } => out.chunked(
            (38, "ChatHits"),
            messages,
            w::EVENT_CHAT_HITS_MESSAGES,
            |out, m| message(out, m),
            |batch, last, messages| w::Event::ChatHits {
                chat: chat.clone(),
                query: query.clone(),
                from,
                until,
                batch,
                last,
                // Only the whole batch can say the archive held more.
                truncated: truncated && last,
                messages,
            },
        ),
        E::ChatShared { request, result } => match result {
            Ok(shared) => {
                let rows = shared
                    .media
                    .into_iter()
                    .map(SharedRow::Media)
                    .chain(shared.docs.into_iter().map(SharedRow::Doc))
                    .chain(shared.links.into_iter().map(SharedRow::Link))
                    .collect();
                let chat = shared.chat;
                let (media_truncated, docs_truncated, links_truncated) = (
                    shared.media_truncated,
                    shared.docs_truncated,
                    shared.links_truncated,
                );
                out.chunked(
                    (39, "ChatShared"),
                    rows,
                    w::EVENT_CHAT_SHARED_ENTRIES,
                    |out, row| match row {
                        SharedRow::Media(m) => w::SharedEntry::Media {
                            message: message(out, m),
                        },
                        SharedRow::Doc(m) => w::SharedEntry::Doc {
                            message: message(out, m),
                        },
                        SharedRow::Link(l) => w::SharedEntry::Link {
                            message: l.message,
                            timestamp: l.timestamp,
                            url: l.url,
                            title: l.title,
                        },
                    },
                    |batch, last, entries| w::Event::ChatShared {
                        request,
                        chat: chat.clone(),
                        error: None,
                        batch,
                        last,
                        media_truncated: media_truncated && last,
                        docs_truncated: docs_truncated && last,
                        links_truncated: links_truncated && last,
                        entries,
                    },
                );
            }
            Err(error) => out.send(w::Event::ChatShared {
                request,
                chat: String::new(),
                error: Some(toast(error)),
                batch: 0,
                last: true,
                media_truncated: false,
                docs_truncated: false,
                links_truncated: false,
                entries: Vec::new(),
            }),
        },
        E::ChatSharedChanged(chat) => out.send(w::Event::ChatSharedChanged { chat }),
        E::ChatMedia {
            chat,
            around,
            items,
        } => out.chunked(
            (41, "ChatMedia"),
            items,
            w::EVENT_CHAT_MEDIA_ITEMS,
            |out, item| w::MediaItem {
                id: item.id,
                card: item.card.map(|n| n as u64),
                timestamp: item.timestamp,
                video: item.video,
                path: item.path,
                thumbnail: item.thumbnail.map(|t| out.blob(t)),
            },
            |batch, last, items| w::Event::ChatMedia {
                chat: chat.clone(),
                around: around.clone(),
                batch,
                last,
                items,
            },
        ),
        E::Pins { chat, items } => out.chunked(
            (42, "Pins"),
            items,
            w::EVENT_PINS_ITEMS,
            |_, p| w::Pinned {
                chat: p.chat,
                id: p.id,
                pinned_at: p.pinned_at,
                expires_at: p.expires_at,
                text: p.text,
                from_me: p.from_me,
                sent_at: p.sent_at,
                pinner: w::Pinner {
                    by_me: p.pinner.by_me,
                    sender: p.pinner.sender,
                },
            },
            |batch, last, items| w::Event::Pins {
                chat: chat.clone(),
                batch,
                last,
                items,
            },
        ),
        E::PinChanged {
            chat,
            message,
            pinned,
        } => out.send(w::Event::PinChanged {
            chat,
            message,
            pinned,
        }),
        E::StorageStats {
            stats,
            token,
            cleared,
        } => out.send(w::Event::StorageStats {
            stats: stats.map(|s| w::StorageStats {
                images: s.images,
                image_bytes: s.image_bytes,
                videos: s.videos,
                video_bytes: s.video_bytes,
                stickers: s.stickers,
                sticker_bytes: s.sticker_bytes,
                other: s.other,
                other_bytes: s.other_bytes,
            }),
            token,
            cleared,
        }),
        E::SearchHits { query, messages } => out.chunked(
            (45, "SearchHits"),
            messages,
            w::EVENT_SEARCH_HITS_MESSAGES,
            |out, m| message(out, m),
            |batch, last, messages| w::Event::SearchHits {
                query: query.clone(),
                batch,
                last,
                messages,
            },
        ),
        E::Avatar { id, full, path } => out.send(w::Event::Avatar { id, full, path }),
        E::MessageDeleted { chat, id } => out.send(w::Event::MessageDeleted { chat, id }),
        E::ChatRemoved { chat } => out.send(w::Event::ChatRemoved { chat }),
        E::ChatCleared { chat, through } => out.send(w::Event::ChatCleared { chat, through }),
        E::Gifs { query, results } => match results {
            Ok(gifs) => out.chunked(
                (50, "Gifs"),
                gifs,
                w::EVENT_GIFS_GIFS,
                |_, g| gif(g),
                |batch, last, gifs| w::Event::Gifs {
                    query: query.clone(),
                    error: None,
                    batch,
                    last,
                    gifs,
                },
            ),
            Err(error) => out.send(w::Event::Gifs {
                query,
                error: Some(w::GifFailure {
                    message: toast(error.message),
                    bad_key: error.bad_key,
                }),
                batch: 0,
                last: true,
                gifs: Vec::new(),
            }),
        },
        E::StickerPicture {
            path,
            width,
            height,
            transparent,
        } => out.send(w::Event::StickerPicture {
            path,
            width,
            height,
            transparent,
        }),
        E::PictureInspected { target, result } => out.send(w::Event::PictureInspected {
            target,
            result: match result {
                Ok((width, height)) => w::SizeResult::Ok { width, height },
                Err(error) => w::SizeResult::Err {
                    error: toast(error),
                },
            },
        }),
        E::PictureEdited { target, result } => out.send(w::Event::PictureEdited {
            target,
            result: path_result(result),
        }),
        E::StickerPackPreview(result) => out.send(w::Event::StickerPackPreview {
            result: match result {
                Ok((pack, publisher)) => w::StickerPackResult::Ok {
                    pack: sticker_pack(pack),
                    publisher,
                },
                Err(error) => w::StickerPackResult::Err {
                    error: toast(error),
                },
            },
        }),
        E::Stickers {
            favorites,
            packs,
            recent,
            received,
            emojis,
        } => {
            // The picker's order, then the emoji tags in path order so the
            // same state always makes the same records.
            let mut rows: Vec<w::StickerEntry> = Vec::new();
            rows.extend(
                favorites
                    .into_iter()
                    .map(|path| w::StickerEntry::Favorite { path }),
            );
            for pack in packs {
                rows.push(w::StickerEntry::Pack {
                    name: pack.name,
                    dir: pack.dir,
                    local: pack.local,
                });
                rows.extend(
                    pack.stickers
                        .into_iter()
                        .map(|path| w::StickerEntry::PackSticker { path }),
                );
            }
            rows.extend(
                recent
                    .into_iter()
                    .map(|path| w::StickerEntry::Recent { path }),
            );
            rows.extend(
                received
                    .into_iter()
                    .map(|path| w::StickerEntry::Received { path }),
            );
            let mut tagged: Vec<_> = emojis.into_iter().collect();
            tagged.sort();
            rows.extend(
                tagged
                    .into_iter()
                    .map(|(path, emojis)| w::StickerEntry::Tagged { path, emojis }),
            );
            out.chunked(
                (55, "Stickers"),
                rows,
                w::EVENT_STICKERS_ENTRIES,
                |_, row| row,
                |batch, last, entries| w::Event::Stickers {
                    batch,
                    last,
                    entries,
                },
            );
        }
        E::Media {
            card,
            chat,
            message,
            result,
        } => out.send(w::Event::Media {
            card: card.map(|n| n as u64),
            chat,
            message,
            result: path_result(result),
        }),
        E::Motion {
            chat,
            message,
            result,
        } => out.send(w::Event::Motion {
            chat,
            message,
            result: path_result(result),
        }),
        E::OlderFetched { chat, more, silent } => {
            out.send(w::Event::OlderFetched { chat, more, silent })
        }
        E::ReceiptsPrivacy { disabled } => out.send(w::Event::ReceiptsPrivacy { disabled }),
        E::AccountPrivacy { values, failed } => out.send(w::Event::AccountPrivacy {
            values: values
                .into_iter()
                .map(|(kind, choice)| w::PrivacyValue {
                    kind: privacy_kind(kind),
                    choice: privacy_choice(choice),
                })
                .collect(),
            failed,
        }),
        E::AccountPrivacySaved { kind } => out.send(w::Event::AccountPrivacySaved {
            kind: privacy_kind(kind),
        }),
        E::AccountPrivacyFailed { kind } => out.send(w::Event::AccountPrivacyFailed {
            kind: privacy_kind(kind),
        }),
        E::Receipts(r) => out.send(w::Event::Receipts {
            chat: r.chat,
            message: r.message,
            recipients: r
                .recipients
                .into_iter()
                .map(|x| w::Recipient {
                    id: x.id,
                    expected: x.expected,
                    delivered_at: x.delivered_at,
                    read_at: x.read_at,
                    played_at: x.played_at,
                })
                .collect(),
        }),
        E::ChatSoundPicked { chat, path } => out.send(w::Event::ChatSoundPicked { chat, path }),
        E::DownloadFolderPicked(path) => out.send(w::Event::DownloadFolderPicked { path }),
        E::WallpaperImagePicked(result) => out.send(w::Event::WallpaperImagePicked {
            result: path_result(result),
        }),
        E::NotificationSoundPicked { mention, path } => {
            out.send(w::Event::NotificationSoundPicked { mention, path })
        }
        E::InvitePreview { code, result } => out.send(w::Event::InvitePreview {
            code,
            result: match result {
                Ok(info) => w::InviteResult::Ok {
                    info: w::InviteInfo {
                        id: info.id,
                        subject: info.subject,
                        description: info.description,
                        members: info.members as u64,
                        approval: info.approval,
                    },
                },
                Err(error) => w::InviteResult::Err {
                    error: toast(error),
                },
            },
        }),
        E::InviteJoined { code, result } => out.send(w::Event::InviteJoined {
            code,
            result: match result {
                Ok((chat, pending)) => w::JoinResult::Ok { chat, pending },
                Err(error) => w::JoinResult::Err {
                    error: toast(error),
                },
            },
        }),
        E::ContactFailed { request, error } => out.send(w::Event::ContactFailed {
            request,
            error: toast(error),
        }),
        E::ContactReady { request, id, name } => {
            out.send(w::Event::ContactReady { request, id, name })
        }
        E::Call(update) => {
            out.shared.frames.on_update(&update);
            out.send(w::Event::Call {
                update: call_update(*update),
            });
        }
        E::CallDevices(list) => {
            let device = |d: zapfast::calls::AudioDevice| w::Device {
                id: d.id,
                label: d.label,
            };
            out.send(w::Event::CallDevices {
                devices: w::DeviceList {
                    microphones: list.microphones.into_iter().map(device).collect(),
                    speakers: list.speakers.into_iter().map(device).collect(),
                    cameras: list
                        .cameras
                        .into_iter()
                        .map(|c| w::Device {
                            id: c.id,
                            label: c.label,
                        })
                        .collect(),
                },
            });
        }
        E::CallLog(messages) => out.chunked(
            (74, "CallLog"),
            messages,
            w::EVENT_CALL_LOG_MESSAGES,
            |out, m| message(out, m),
            |batch, last, messages| w::Event::CallLog {
                batch,
                last,
                messages,
            },
        ),
        E::GroupSaving { chat, saving } => out.send(w::Event::GroupSaving { chat, saving }),
        E::PickRequest(request) => {
            use zapfast::pick::Kind;
            let id = request.id;
            // The worker waits for the answer, so the record must be built:
            // text over its bound is cut and a directory that cannot be
            // carried is left off, never the whole request dropped.
            let event = w::Event::PickRequest {
                id,
                kind: match request.kind {
                    Kind::File => w::PickKind::File,
                    Kind::Files => w::PickKind::Files,
                    Kind::Folder => w::PickKind::Folder,
                    Kind::Save => w::PickKind::Save,
                },
                title: clip(request.title, 1024),
                filters: request
                    .filters
                    .into_iter()
                    .take(16)
                    .map(|(name, extensions)| w::PickFilter {
                        name: clip(name, 256),
                        extensions: extensions
                            .into_iter()
                            .take(32)
                            .map(|e| clip(e, 32))
                            .collect(),
                    })
                    .collect(),
                file_name: request.file_name.map(|n| clip(n, 1024)),
                directory: request.directory.filter(|d| d.as_os_str().len() <= 4096),
            };
            // Should it still not encode, the dialog is cancelled for the
            // reader instead of blocking the worker's thread for good.
            if !out.send_checked(event) {
                out.shared.refuse_pick(id);
            }
        }

        // Frames go through zf_frame_acquire, never a record.
        E::CallVideo {
            local,
            remote,
            from,
        } => out.shared.frames.put(local, remote, from),
        // The egui client's updater, compiled only with upstream's `ui`; the
        // Go app has its own.
        #[cfg(feature = "ui")]
        E::UpdateAvailable { .. } => unported("UpdateAvailable"),
        #[cfg(feature = "ui")]
        E::UpdateSupport(_) => unported("UpdateSupport"),
        #[cfg(feature = "ui")]
        E::UpdateProgress { .. } => unported("UpdateProgress"),
        #[cfg(feature = "ui")]
        E::UpdateDownloaded(_) => unported("UpdateDownloaded"),
        #[cfg(feature = "ui")]
        E::UpdateInstalling(_) => unported("UpdateInstalling"),
    }
}

/// A row of a chat's shared media, before it is a record.
enum SharedRow {
    Media(m::Message),
    Doc(m::Message),
    Link(m::ChatLink),
}

fn path_result(result: Result<std::path::PathBuf, String>) -> w::PathResult {
    match result {
        Ok(path) => w::PathResult::Ok { path },
        Err(error) => w::PathResult::Err {
            error: toast(error),
        },
    }
}

fn gif(g: m::Gif) -> w::Gif {
    w::Gif {
        id: g.id,
        still: g.still,
        mp4: g.mp4,
        width: g.width,
        height: g.height,
    }
}

fn sticker_pack(pack: m::StickerPack) -> w::StickerPackInfo {
    w::StickerPackInfo {
        name: pack.name,
        dir: pack.dir,
        stickers: pack.stickers,
        local: pack.local,
    }
}

fn privacy_kind(kind: zapfast::privacy::PrivacyKind) -> w::PrivacyKind {
    use zapfast::privacy::PrivacyKind as K;
    match kind {
        K::LastSeen => w::PrivacyKind::LastSeen,
        K::Online => w::PrivacyKind::Online,
        K::Profile => w::PrivacyKind::Profile,
        K::About => w::PrivacyKind::About,
        K::GroupAdd => w::PrivacyKind::GroupAdd,
        K::ReadReceipts => w::PrivacyKind::ReadReceipts,
        K::CallAdd => w::PrivacyKind::CallAdd,
    }
}

fn privacy_choice(choice: zapfast::privacy::PrivacyChoice) -> w::PrivacyChoice {
    use zapfast::privacy::PrivacyChoice as C;
    match choice {
        C::Everyone => w::PrivacyChoice::Everyone,
        C::Contacts => w::PrivacyChoice::Contacts,
        C::Except => w::PrivacyChoice::Except,
        C::Nobody => w::PrivacyChoice::Nobody,
        C::SameAsLastSeen => w::PrivacyChoice::SameAsLastSeen,
        C::Known => w::PrivacyChoice::Known,
    }
}

fn call_status(status: m::CallStatus) -> w::CallStatus {
    match status {
        m::CallStatus::Answered => w::CallStatus::Answered,
        m::CallStatus::AnsweredElsewhere => w::CallStatus::AnsweredElsewhere,
        m::CallStatus::Missed => w::CallStatus::Missed,
        m::CallStatus::Declined => w::CallStatus::Declined,
        m::CallStatus::DeclinedElsewhere => w::CallStatus::DeclinedElsewhere,
        m::CallStatus::Busy => w::CallStatus::Busy,
        m::CallStatus::Failed => w::CallStatus::Failed,
        m::CallStatus::NoAnswer => w::CallStatus::NoAnswer,
        m::CallStatus::ConnectionLost => w::CallStatus::ConnectionLost,
    }
}

fn call_update(u: zapfast::calls::CallUpdate) -> w::CallUpdate {
    use zapfast::calls as c;
    w::CallUpdate {
        generation: u.generation,
        chat: u.chat,
        direction: match u.direction {
            m::CallDirection::Incoming => w::CallDirection::Incoming,
            m::CallDirection::Outgoing => w::CallDirection::Outgoing,
        },
        video: u.video,
        phase: match u.phase {
            c::CallPhase::Placing => w::CallPhase::Placing,
            c::CallPhase::Dialing => w::CallPhase::Dialing,
            c::CallPhase::Ringing => w::CallPhase::Ringing,
            c::CallPhase::Incoming => w::CallPhase::Incoming,
            c::CallPhase::Connecting => w::CallPhase::Connecting,
            c::CallPhase::Accepted => w::CallPhase::Accepted,
            c::CallPhase::Active => w::CallPhase::Active,
            c::CallPhase::Ended => w::CallPhase::Ended,
            c::CallPhase::Failed => w::CallPhase::Failed,
        },
        active_for_ms: u
            .started
            .map(|at| at.elapsed().as_millis().min(u128::from(u64::MAX)) as u64),
        muted: u.muted,
        camera_on: u.camera_on,
        remote_video: u.remote_video,
        video_requested: u.video_requested,
        outcome: u.outcome.map(call_status),
        peer_audio: u.peer_audio.map(|p| match p {
            c::PeerAudio::Silent(reason) => w::PeerAudio::Silent {
                reason: match reason {
                    c::SilenceReason::NoDecoder => w::SilenceReason::NoDecoder,
                    c::SilenceReason::AuthenticationFailing => {
                        w::SilenceReason::AuthenticationFailing
                    }
                    c::SilenceReason::UnexpectedPayloadType => {
                        w::SilenceReason::UnexpectedPayloadType
                    }
                    c::SilenceReason::CodecRejectingFrames => {
                        w::SilenceReason::CodecRejectingFrames
                    }
                    c::SilenceReason::CodecFlapping => w::SilenceReason::CodecFlapping,
                    c::SilenceReason::Unknown => w::SilenceReason::Unknown,
                },
            },
            c::PeerAudio::Stalled => w::PeerAudio::Stalled,
        }),
        lost_devices: u
            .lost_devices
            .into_iter()
            .map(|d| w::LostDevice {
                kind: match d.kind {
                    c::DeviceKind::Microphone => w::DeviceKind::Microphone,
                    c::DeviceKind::Speaker => w::DeviceKind::Speaker,
                    c::DeviceKind::Camera => w::DeviceKind::Camera,
                },
                name: d.name,
            })
            .collect(),
        microphone: u.microphone,
        speaker: u.speaker,
        camera: u.camera,
        group: u.group.map(|g| w::GroupCall {
            caller: g.caller,
            participants: g
                .participants
                .into_iter()
                .map(|p| w::Participant {
                    id: p.id,
                    joined: p.joined,
                    video: p.video,
                })
                .collect(),
            link: g.link,
        }),
    }
}

#[cfg(feature = "ui")]
fn unported(name: &'static str) {
    log::debug!("zapfast_core: event {name} is not exported");
}

fn link(status: up::LinkStatus) -> w::LinkStatus {
    match status {
        up::LinkStatus::Starting => w::LinkStatus::Starting,
        up::LinkStatus::Unlinked {
            qr,
            pair_code,
            pairing_phone,
        } => w::LinkStatus::Unlinked {
            qr,
            pair_code,
            pairing_phone,
        },
        up::LinkStatus::Connecting => w::LinkStatus::Connecting,
        up::LinkStatus::Connected => w::LinkStatus::Connected,
        up::LinkStatus::Disconnected { reason } => w::LinkStatus::Disconnected {
            reason: clip(reason, 1024),
        },
        up::LinkStatus::LoggedOut => w::LinkStatus::LoggedOut,
        up::LinkStatus::Failed(error) => w::LinkStatus::Failed {
            error: toast(error),
        },
    }
}

fn delivery(d: m::Delivery) -> w::Delivery {
    match d {
        m::Delivery::None => w::Delivery::None,
        m::Delivery::Pending => w::Delivery::Pending,
        m::Delivery::Sent => w::Delivery::Sent,
        m::Delivery::Delivered => w::Delivery::Delivered,
        m::Delivery::Read => w::Delivery::Read,
        m::Delivery::Played => w::Delivery::Played,
        m::Delivery::Failed => w::Delivery::Failed,
    }
}

fn chat(out: &mut Out<'_>, c: m::Chat) -> w::Chat {
    let _ = out;
    w::Chat {
        id: c.id,
        name: clip(c.name, 1024),
        group_subject_known: c.group_subject_known,
        kind: match c.kind {
            m::ChatKind::Direct => w::ChatKind::Direct,
            m::ChatKind::Group => w::ChatKind::Group,
            m::ChatKind::Broadcast => w::ChatKind::Broadcast,
        },
        last_activity: c.last_activity,
        unread: c.unread,
        marked_unread: c.marked_unread,
        archived: c.archived,
        pinned: c.pinned,
        pinned_at: c.pinned_at,
        muted_until: c.muted_until,
        last: c.last.map(|l| w::LastMessage {
            from_me: l.from_me,
            sender: l.sender,
            sender_name: l.sender_name.map(|n| clip(n, 1024)),
            // Upstream's summary is the whole first line of the message.
            summary: clip(l.summary, 1024),
            full: clip(l.full, 196_608),
            status: delivery(l.status),
        }),
        favorite: c.favorite,
        favorite_position: c.favorite_position,
        // A group larger than the bound shows its first members only, which
        // beats losing the chat.
        participants: c.participants.into_iter().take(1024).collect(),
        group_description: c.group_description.map(|d| clip(d, 8192)),
        read_only: c.read_only,
        left: c.left,
        info_locked: c.info_locked,
        admin: c.admin,
        locked: c.locked,
        ephemeral_expiration: c.ephemeral_expiration,
        labels: c.labels.into_iter().take(64).collect(),
        notification_sound: c.notification_sound.map(|s| {
            use zapfast::settings::NotificationSound as S;
            match s {
                S::Receive => w::NotificationSound::Receive,
                S::Alert => w::NotificationSound::Alert,
                S::System => w::NotificationSound::System,
                S::None => w::NotificationSound::None,
                S::Custom(path) => w::NotificationSound::Custom { path },
            }
        }),
        unread_mention: c.unread_mention,
    }
}

fn contact(c: m::Contact) -> w::Contact {
    w::Contact {
        id: c.id,
        full_name: c.full_name.map(|n| clip(n, 1024)),
        first_name: c.first_name.map(|n| clip(n, 1024)),
        push_name: c.push_name.map(|n| clip(n, 1024)),
    }
}

/// The most mentions a command sends and a message or quote carries.
pub const MENTIONS: usize = 256;

/// A message's or quote's mentions. Any a command sent fit; one received
/// with more keeps the first `MENTIONS` rather than losing the message.
fn mentions(list: Vec<m::MentionRef>) -> Vec<w::MentionRef> {
    list.into_iter().take(MENTIONS).map(mention).collect()
}

fn mention(r: m::MentionRef) -> w::MentionRef {
    w::MentionRef {
        user: r.user,
        id: r.id,
    }
}

fn media_state(s: m::MediaState) -> w::MediaState {
    match s {
        m::MediaState::Idle => w::MediaState::Idle,
        m::MediaState::Downloading => w::MediaState::Downloading,
        m::MediaState::Failed(error) => w::MediaState::Failed {
            error: clip(error, 1024),
        },
    }
}

fn media(x: m::Media) -> w::Media {
    w::Media {
        mime: x.mime,
        size: x.size,
        width: x.width,
        height: x.height,
        album: x.album,
        path: x.path,
        state: media_state(x.state),
    }
}

fn button(b: m::InteractiveButton) -> w::InteractiveButton {
    w::InteractiveButton {
        label: b.label,
        url: b.url,
        action: match b.action {
            m::InteractiveAction::Unavailable => w::InteractiveAction::Unavailable,
            m::InteractiveAction::Reply => w::InteractiveAction::Reply,
            m::InteractiveAction::Copy(code) => w::InteractiveAction::Copy { code },
            m::InteractiveAction::Select(options) => w::InteractiveAction::Select {
                options: options
                    .into_iter()
                    .map(|o| w::InteractiveOption {
                        section: o.section,
                        title: o.title,
                        description: o.description,
                    })
                    .collect(),
            },
        },
    }
}

fn card(out: &mut Out<'_>, c: m::InteractiveCard) -> w::InteractiveCard {
    w::InteractiveCard {
        body: c.body,
        buttons: c.buttons.into_iter().map(button).collect(),
        image: c.image.map(media),
        needs_phone: c.needs_phone,
        // Carousel cards hold no carousel of their own.
        carousel: c
            .carousel
            .into_iter()
            .map(|k| w::CarouselCard {
                body: k.body,
                buttons: k.buttons.into_iter().map(button).collect(),
                image: k.image.map(media),
                needs_phone: k.needs_phone,
                thumbnail: k.thumbnail.map(|t| out.blob(t)),
            })
            .collect(),
        thumbnail: c.thumbnail.map(|t| out.blob(t)),
    }
}

fn content(out: &mut Out<'_>, c: m::Content) -> w::Content {
    use m::Content as C;
    match c {
        C::Text { text, preview } => w::Content::Text {
            text,
            // A preview is decoration: one whose address cannot be carried
            // is left off, and its text is cut, rather than losing the
            // message around it.
            preview: preview
                .filter(|p| p.url.len() <= 4096)
                .map(|p| w::LinkPreview {
                    url: p.url,
                    title: p.title.map(|t| clip(t, 1024)),
                    description: p.description.map(|d| clip(d, 4096)),
                }),
        },
        C::Interactive { text, card: k } => w::Content::Interactive {
            text,
            card: k.map(|k| card(out, *k)),
        },
        C::Image {
            caption,
            media: x,
            motion,
        } => w::Content::Image {
            caption,
            media: media(x),
            motion: motion.map(|mo| w::Motion {
                path: mo.path,
                state: media_state(mo.state),
            }),
        },
        C::Video {
            caption,
            media: x,
            seconds,
            gif,
            note,
        } => w::Content::Video {
            caption,
            media: media(x),
            seconds,
            gif,
            note,
        },
        C::Audio {
            media: x,
            seconds,
            voice_note,
            waveform,
        } => w::Content::Audio {
            media: media(x),
            seconds,
            voice_note,
            waveform,
        },
        C::Document {
            media: x,
            file_name,
            caption,
            pages,
        } => w::Content::Document {
            media: media(x),
            file_name,
            caption,
            pages,
        },
        C::Sticker { media: x, animated } => w::Content::Sticker {
            media: media(x),
            animated,
        },
        C::StickerPack {
            name,
            publisher,
            count,
            caption,
        } => w::Content::StickerPack {
            name,
            publisher,
            count,
            caption,
        },
        C::Location {
            latitude,
            longitude,
            name,
            address,
        } => w::Content::Location {
            latitude,
            longitude,
            name,
            address,
        },
        C::LiveLocation {
            latitude,
            longitude,
            accuracy_m,
            speed_mps,
            heading_deg,
            sequence,
            ended,
            updated,
            newer_on_phone,
        } => w::Content::LiveLocation {
            latitude,
            longitude,
            accuracy_m,
            speed_mps,
            heading_deg,
            sequence,
            ended,
            updated,
            newer_on_phone,
        },
        C::Contact {
            display_name,
            vcard,
        } => w::Content::Contact {
            display_name,
            vcard,
        },
        C::Poll {
            question,
            options,
            state,
        } => w::Content::Poll {
            question,
            options,
            state: w::PollState {
                selectable: state.selectable as u64,
                counts: state.counts.into_iter().map(|n| n as u64).collect(),
                selected: state.selected.into_iter().map(|n| n as u64).collect(),
                voters: state.voters as u64,
                can_vote: state.can_vote,
                history_complete: state.history_complete,
                refresh_needed: state.refresh_needed,
                refreshing: state.refreshing,
                refresh_failed: state.refresh_failed,
                // The counts carry every vote; the voters listed are the
                // first ones.
                votes: state
                    .votes
                    .into_iter()
                    .take(256)
                    .map(|v| w::PollVoter {
                        id: v.id,
                        name: clip(v.name, 256),
                        from_me: v.from_me,
                        timestamp: v.timestamp,
                        choices: v.choices.into_iter().map(|n| n as u64).collect(),
                    })
                    .collect(),
            },
        },
        C::Revoked => w::Content::Revoked,
        C::Unsupported { what } => w::Content::Unsupported {
            what: clip(what, 1024),
        },
        C::PhoneOnly {
            view_once,
            live_location,
            once,
        } => w::Content::PhoneOnly {
            view_once,
            live_location,
            once: once.map(|o| match o {
                m::OnceMedia::Photo => w::OnceMedia::Photo,
                m::OnceMedia::Video => w::OnceMedia::Video,
                m::OnceMedia::Voice => w::OnceMedia::Voice,
                m::OnceMedia::Audio => w::OnceMedia::Audio,
            }),
        },
        C::Call {
            video,
            status,
            duration,
            group,
        } => w::Content::Call {
            video,
            status: match status {
                m::CallStatus::Answered => w::CallStatus::Answered,
                m::CallStatus::AnsweredElsewhere => w::CallStatus::AnsweredElsewhere,
                m::CallStatus::Missed => w::CallStatus::Missed,
                m::CallStatus::Declined => w::CallStatus::Declined,
                m::CallStatus::DeclinedElsewhere => w::CallStatus::DeclinedElsewhere,
                m::CallStatus::Busy => w::CallStatus::Busy,
                m::CallStatus::Failed => w::CallStatus::Failed,
                m::CallStatus::NoAnswer => w::CallStatus::NoAnswer,
                m::CallStatus::ConnectionLost => w::CallStatus::ConnectionLost,
            },
            duration,
            group,
        },
    }
}

pub fn message(out: &mut Out<'_>, x: m::Message) -> w::Message {
    w::Message {
        id: x.id,
        chat: x.chat,
        sender: x.sender,
        sender_name: x.sender_name.map(|n| clip(n, 1024)),
        from_me: x.from_me,
        timestamp: x.timestamp,
        history_order: x.history_order,
        content: content(out, x.content),
        status: delivery(x.status),
        delivered_at: x.delivered_at,
        read_at: x.read_at,
        quoted: x.quoted.map(|q| w::Quoted {
            chat: q.chat,
            id: q.id,
            sender: q.sender,
            sender_name: q.sender_name.map(|n| clip(n, 1024)),
            summary: clip(q.summary, 1024),
            mentions: mentions(q.mentions),
        }),
        // A message in a large group can hold more reactions than the bound;
        // the first ones show, and the message stays.
        reactions: x
            .reactions
            .into_iter()
            .take(256)
            .map(|r| w::Reaction {
                sender: r.sender,
                from_me: r.from_me,
                emoji: r.emoji,
            })
            .collect(),
        edited: x.edited,
        mentions: mentions(x.mentions),
        forwarded: x.forwarded,
        thumbnail: x.thumbnail.map(|t| out.blob(t)),
    }
}

fn reply_target(r: m::ReplyTarget) -> w::ReplyTarget {
    w::ReplyTarget {
        id: r.id,
        chat: r.chat,
    }
}

fn unsent(out: &mut Out<'_>, u: up::Unsent) -> w::Unsent {
    match u {
        up::Unsent::Text(text) => w::Unsent::Text { text },
        up::Unsent::Location {
            latitude,
            longitude,
        } => w::Unsent::Location {
            latitude,
            longitude,
        },
        up::Unsent::Voice(samples) => w::Unsent::Voice {
            samples: out.blob(samples.iter().flat_map(|s| s.to_le_bytes()).collect()),
        },
        up::Unsent::Files {
            paths,
            documents,
            caption,
        } => w::Unsent::Files {
            paths,
            documents,
            caption,
        },
        up::Unsent::Image {
            width,
            height,
            rgba,
            caption,
        } => w::Unsent::Image {
            width,
            height,
            rgba: out.blob(rgba),
            caption,
        },
        up::Unsent::Sticker => w::Unsent::Sticker,
        up::Unsent::Gif => w::Unsent::Gif,
    }
}

/// Display text cut to at most `max` bytes: at a grapheme boundary, with an
/// ellipsis. Only for text a person reads and nothing parses (summaries,
/// names, errors); ids, paths, URLs and message bodies are never cut.
pub fn clip(text: String, max: usize) -> String {
    use unicode_segmentation::UnicodeSegmentation;
    const ELLIPSIS: &str = "…";
    if text.len() <= max {
        return text;
    }
    let room = max.saturating_sub(ELLIPSIS.len());
    let end = text
        .grapheme_indices(true)
        .map(|(at, g)| at + g.len())
        .take_while(|end| *end <= room)
        .last()
        .unwrap_or(0);
    let mut out = String::with_capacity(end + ELLIPSIS.len());
    out.push_str(text[..end].trim_end());
    out.push_str(ELLIPSIS);
    out
}

/// An error or toast: at most the 4096 bytes the records carry.
fn toast(text: String) -> String {
    clip(text, 4096)
}

/// A command that failed before it was queued, for `zf_err`.
#[derive(Debug)]
pub enum Refused {
    /// A blob the command names is not held (or not at that length).
    Blob(u64),
    /// The fields do not make a valid command.
    Invalid(&'static str),
    /// A demo-only command sent to a real core.
    DemoOnly,
    /// A command the demo core does not answer, refused before any blob it
    /// names is taken.
    Unsupported(&'static str),
}

/// Where a decoded command goes.
pub enum Routed {
    Upstream(up::Command),
    Demo(crate::demo::Injected),
    /// Handled here: it needs nothing from the engine (`CancelCall`).
    Done,
}

/// Turns a record into upstream's command, taking the blobs it names. A
/// refused command takes nothing.
/// Every cancel flag is still held by a call being placed.
const TOO_MANY_CALLS: Refused = Refused::Invalid("too many calls are still being placed");

pub fn command(shared: &Shared, cmd: w::Command) -> Result<Routed, Refused> {
    use w::Command as C;
    Ok(Routed::Upstream(match cmd {
        C::LoadChat { chat, before } => up::Command::LoadChat {
            chat,
            before: before.map(|k| (k.timestamp, k.id)),
        },
        C::SendText {
            chat,
            text,
            quoting,
            mentions,
        } => up::Command::SendText {
            chat,
            text,
            quoting: quoting.map(model_reply),
            mentions,
        },
        C::SendLocation {
            chat,
            latitude,
            longitude,
            quoting,
        } => {
            if !(latitude.is_finite() && longitude.is_finite()) {
                return Err(Refused::Invalid("latitude and longitude must be finite"));
            }
            up::Command::SendLocation {
                chat,
                latitude,
                longitude,
                quoting: quoting.map(model_reply),
            }
        }
        C::Composing { chat, composing } => up::Command::Composing { chat, composing },
        C::SaveDraft { chat, text } => up::Command::SaveDraft { chat, text },
        C::MarkRead { chat, receipts } => up::Command::MarkRead { chat, receipts },
        C::SetMuted { chat, until } => up::Command::SetMuted(chat, until),
        C::SendImage {
            chat,
            width,
            height,
            rgba,
            caption,
            mentions,
            quoting,
        } => {
            if !is_rgba(width, height, rgba.len) {
                return Err(Refused::Invalid("rgba is not width × height × 4 bytes"));
            }
            let rgba = shared
                .blobs
                .take(rgba)
                .map_err(|_| Refused::Blob(rgba.id))?;
            up::Command::SendImage {
                chat,
                width,
                height,
                rgba,
                caption,
                mentions,
                quoting: quoting.map(model_reply),
            }
        }
        C::SendVoice {
            chat,
            samples,
            quoting,
        } => {
            if samples.len % 4 != 0 {
                return Err(Refused::Invalid("samples are whole little-endian f32s"));
            }
            if samples.len
                > crate::voice::LONGEST_RECORDING_SECONDS * 4 * u64::from(crate::voice::RATE)
            {
                return Err(Refused::Invalid("a voice note is at most 15 minutes"));
            }
            let bytes = shared
                .blobs
                .take(samples)
                .map_err(|_| Refused::Blob(samples.id))?;
            up::Command::SendVoice {
                chat,
                samples: bytes
                    .as_chunks::<4>()
                    .0
                    .iter()
                    .map(|b| f32::from_le_bytes(*b))
                    .collect(),
                quoting: quoting.map(model_reply),
            }
        }
        C::SetOnline { online } => up::Command::SetOnline(online),
        C::Reconnect => up::Command::Reconnect,
        C::RefreshPoll { chat, message } => up::Command::RefreshPoll { chat, message },
        C::CreatePoll { chat, draft } => up::Command::CreatePoll {
            chat,
            draft: m::PollDraft {
                question: draft.question,
                options: draft.options,
                multiple: draft.multiple,
            },
        },
        C::VotePoll {
            chat,
            message,
            choices,
        } => up::Command::VotePoll {
            chat,
            message,
            choices: choices.into_iter().map(index).collect::<Result<_, _>>()?,
        },
        C::ReplyInteractive {
            chat,
            message,
            button,
            choice,
        } => up::Command::ReplyInteractive {
            chat,
            message,
            button: index(button)?,
            choice: choice.map(index).transpose()?,
        },
        C::Forward {
            from_chat,
            messages,
            to_chats,
        } => up::Command::Forward {
            from_chat,
            messages,
            to_chats,
        },
        C::ForwardLimit {
            from_chat,
            messages,
        } => up::Command::ForwardLimit {
            from_chat,
            messages,
        },
        C::MarkUnread { chat } => up::Command::MarkUnread(chat),
        C::WatchReceipts { target } => {
            up::Command::WatchReceipts(target.map(|t| (t.chat, t.message)))
        }
        C::LoadPins { chat } => up::Command::LoadPins(chat),
        C::FetchOlder { chat, explicit } => up::Command::FetchOlder { chat, explicit },
        C::ReloadHistory { chat, message } => up::Command::ReloadHistory { chat, message },
        C::SetHistoryPrefetch { on, focused } => up::Command::SetHistoryPrefetch { on, focused },
        C::LoadChatMedia { chat, around } => up::Command::LoadChatMedia { chat, around },
        C::Download {
            manual,
            card,
            chat,
            message,
        } => up::Command::Download {
            manual,
            card: card.map(index).transpose()?,
            chat,
            message,
        },
        C::DownloadMotion { chat, message } => up::Command::DownloadMotion { chat, message },
        C::FetchAvatar { id, full } => up::Command::FetchAvatar { id, full },
        C::LoadUntil { chat, id, before } => up::Command::LoadUntil {
            chat,
            id,
            before: (before.timestamp, before.id),
        },
        C::SearchMessages { query } => up::Command::SearchMessages { query },
        C::SearchChatMessages {
            chat,
            query,
            from,
            until,
        } => up::Command::SearchChatMessages {
            chat,
            query,
            from,
            until,
        },
        C::SetStar {
            chat,
            message,
            starred,
        } => up::Command::SetStar {
            chat,
            message,
            starred,
        },
        C::LoadStarred => up::Command::LoadStarred,
        C::ListChatShared { chat, request } => up::Command::ListChatShared { chat, request },
        C::EnsureChat { chat, name } => up::Command::EnsureChat { chat, name },
        C::EditText {
            chat,
            id,
            text,
            mentions,
        } => up::Command::EditText {
            chat,
            id,
            text,
            mentions,
        },
        C::Revoke { chat, id } => up::Command::Revoke { chat, id },
        C::DeleteLocal { chat, id } => up::Command::DeleteLocal { chat, id },
        C::PickFiles { chat, documents } => up::Command::PickFiles { chat, documents },
        C::SendFiles {
            chat,
            paths,
            documents,
            caption,
            mentions,
            quoting,
        } => up::Command::SendFiles {
            chat,
            paths,
            documents,
            caption,
            mentions,
            quoting: quoting.map(model_reply),
        },
        C::SetLocked { chat, locked } => up::Command::SetLocked(chat, locked),
        C::CreateLabel { name, color_hex } => up::Command::CreateLabel { name, color_hex },
        C::UpdateLabel {
            id,
            name,
            color_hex,
        } => up::Command::UpdateLabel {
            id,
            name,
            color_hex,
        },
        C::DeleteLabel { id } => up::Command::DeleteLabel(id),
        C::SetChatLabels { chat, labels } => up::Command::SetChatLabels { chat, labels },
        C::StartCall {
            chat,
            video,
            generation,
            cancel,
        } => up::Command::StartCall {
            chat,
            video,
            generation,
            cancel: shared.cancel_flag(cancel).ok_or(TOO_MANY_CALLS)?,
        },
        C::JoinCallLink {
            token,
            video,
            generation,
            cancel,
        } => up::Command::JoinCallLink {
            token,
            video,
            generation,
            cancel: shared.cancel_flag(cancel).ok_or(TOO_MANY_CALLS)?,
        },
        C::CancelCall { token } => {
            shared
                .cancel_flag(token)
                .ok_or(TOO_MANY_CALLS)?
                .store(true, std::sync::atomic::Ordering::SeqCst);
            return Ok(Routed::Done);
        }
        C::AnswerCall => up::Command::AnswerCall,
        C::DeclineCall => up::Command::DeclineCall,
        C::HangupCall => up::Command::HangupCall,
        C::SetCallMuted { muted } => up::Command::SetCallMuted(muted),
        C::SetCallCamera { on } => up::Command::SetCallCamera(on),
        C::SetCallMicrophone { device } => up::Command::SetCallMicrophone(device),
        C::SetCallSpeaker { device } => up::Command::SetCallSpeaker(device),
        C::SetCallCameraDevice { device } => up::Command::SetCallCameraDevice(device),
        C::RefreshCallDevices => up::Command::RefreshCallDevices,
        C::LoadCallLog => up::Command::LoadCallLog,
        C::MarkPlayed {
            chat,
            message,
            sender,
            receipts,
        } => up::Command::MarkPlayed {
            chat,
            message,
            sender,
            receipts,
        },
        C::SendSticker {
            chat,
            path,
            quoting,
        } => up::Command::SendSticker {
            chat,
            path,
            quoting: quoting.map(model_reply),
        },
        C::SaveSticker { path } => up::Command::SaveSticker { path },
        C::RemoveRecentSticker { path } => up::Command::RemoveRecentSticker { path },
        C::ForgetSticker { path } => up::Command::ForgetSticker { path },
        C::ImportStickerUrl { url } => up::Command::ImportStickerUrl { url },
        C::PickStickerArchive => up::Command::PickStickerArchive,
        C::PickNotificationSound { mention } => up::Command::PickNotificationSound { mention },
        C::SetChatSound { chat, sound } => up::Command::SetChatSound {
            chat,
            sound: sound.map(|s| {
                use zapfast::settings::NotificationSound as S;
                match s {
                    w::NotificationSound::Receive => S::Receive,
                    w::NotificationSound::Alert => S::Alert,
                    w::NotificationSound::System => S::System,
                    w::NotificationSound::None => S::None,
                    w::NotificationSound::Custom { path } => S::Custom(path),
                }
            }),
        },
        C::PickChatSound { chat } => up::Command::PickChatSound(chat),
        C::PickDownloadFolder => up::Command::PickDownloadFolder,
        C::PickWallpaperImage => up::Command::PickWallpaperImage,
        C::RemoveWallpaperImage => up::Command::RemoveWallpaperImage,
        C::SetProfile { name, about } => up::Command::SetProfile { name, about },
        C::PickProfilePicture => up::Command::PickProfilePicture,
        C::SetGroupName { chat, name } => up::Command::SetGroupName { chat, name },
        C::PickGroupPicture { chat } => up::Command::PickGroupPicture(chat),
        C::SetGroupPicture { chat, jpeg } => up::Command::SetGroupPicture {
            chat,
            jpeg: jpeg
                .map(|blob| shared.blobs.take(blob).map_err(|_| Refused::Blob(blob.id)))
                .transpose()?,
        },
        C::SetDownloadFolder { path } => {
            // Voice notes downloaded there may be decoded (zf_voice_decode).
            crate::wake::lock(&shared.download_folder).clone_from(&path);
            up::Command::SetDownloadFolder(path)
        }
        C::SetKeepChatsArchived { keep } => up::Command::SetKeepChatsArchived(keep),
        C::Resumed => up::Command::Resumed,
        C::SaveAttachmentAs { source, name } => up::Command::SaveAttachmentAs { source, name },
        C::OpenLog { path } => up::Command::OpenLog(path),
        C::SaveAttachments { files } => up::Command::SaveAttachments {
            files: files.into_iter().map(|f| (f.source, f.name)).collect(),
        },
        C::PrepareClipboardImage { path } => up::Command::PrepareClipboardImage(path),
        C::DeleteStickerPack { dir } => up::Command::DeleteStickerPack { dir },
        C::ViewStickerPack { chat, message } => up::Command::ViewStickerPack { chat, message },
        C::AddStickerPack { dir, name } => up::Command::AddStickerPack { dir, name },
        C::SendStickerPack { chat, dir } => up::Command::SendStickerPack { chat, dir },
        C::PickStickerPicture => up::Command::PickStickerPicture,
        C::MakeSticker {
            source,
            crop,
            transparent,
            emojis,
            chat,
            quoting,
        } => up::Command::MakeSticker {
            source,
            crop: m::StickerCrop {
                x: crop.x,
                y: crop.y,
                side: crop.side,
            },
            transparent,
            emojis,
            chat,
            quoting: quoting.map(model_reply),
        },
        C::InspectPicture { target, path } => up::Command::InspectPicture { target, path },
        C::ApplyPictureEdit {
            target,
            source,
            width,
            height,
            crop,
            turns,
        } => {
            let source = match source {
                w::PictureSource::File { path } => m::PictureSource::File(path),
                w::PictureSource::Pasted { rgba } => {
                    if !is_rgba(width, height, rgba.len) {
                        return Err(Refused::Invalid("rgba is not width × height × 4 bytes"));
                    }
                    let bytes = shared
                        .blobs
                        .take(rgba)
                        .map_err(|_| Refused::Blob(rgba.id))?;
                    m::PictureSource::Pasted(std::sync::Arc::new(bytes))
                }
            };
            up::Command::ApplyPictureEdit {
                target,
                source,
                width,
                height,
                crop: m::PictureCrop {
                    x: crop.x,
                    y: crop.y,
                    width: crop.width,
                    height: crop.height,
                },
                turns,
            }
        }
        C::CreateStickerPack { name } => up::Command::CreateStickerPack { name },
        C::SetStickerPack {
            pack,
            sticker,
            member,
        } => up::Command::SetStickerPack {
            pack,
            sticker,
            member,
        },
        C::SaveContact {
            id,
            full_name,
            first_name,
            to_phone,
        } => up::Command::SaveContact {
            id,
            full_name,
            first_name,
            to_phone,
        },
        C::NewContact {
            request,
            phone,
            full_name,
            first_name,
            to_phone,
        } => up::Command::NewContact {
            request,
            phone,
            full_name,
            first_name,
            to_phone,
        },
        C::SendGif { chat, gif, quoting } => up::Command::SendGif {
            chat,
            gif: m::Gif {
                id: gif.id,
                still: gif.still,
                mp4: gif.mp4,
                width: gif.width,
                height: gif.height,
            },
            quoting: quoting.map(model_reply),
        },
        C::SearchGifs { query, key } => up::Command::SearchGifs { query, key },
        C::RecentStickers => up::Command::RecentStickers,
        C::React {
            chat,
            message,
            emoji,
        } => up::Command::React {
            chat,
            message,
            emoji,
        },
        C::SetArchived { chat, archived } => up::Command::SetArchived(chat, archived),
        C::LeaveGroup { chat, archive } => up::Command::LeaveGroup { chat, archive },
        C::DeleteChat { chat } => up::Command::DeleteChat(chat),
        C::ClearChat { chat } => up::Command::ClearChat(chat),
        C::SetPinned { chat, pinned } => up::Command::SetPinned(chat, pinned),
        C::SetMessagePinned {
            chat,
            message,
            pinned,
        } => up::Command::SetMessagePinned {
            chat,
            message,
            pinned,
        },
        C::SetFavorite { chat, favorite } => up::Command::SetFavorite(chat, favorite),
        C::PairWithPhone { phone } => up::Command::PairWithPhone(phone),
        C::Unlink => up::Command::Unlink,
        C::RemoveAccount => up::Command::RemoveAccount,
        C::SetProxy { proxy } => up::Command::SetProxy(proxy),
        C::StartOverArchive => up::Command::StartOverArchive,
        C::LoadStorageStats { token } => up::Command::LoadStorageStats { token },
        C::ClearMediaCache { token } => up::Command::ClearMediaCache { token },
        C::FetchAccountPrivacy => up::Command::FetchAccountPrivacy,
        C::SetAccountPrivacy { kind, choice } => up::Command::SetAccountPrivacy {
            kind: privacy_kind_from(kind),
            choice: privacy_choice_from(choice),
        },
        C::PickAnswer { id, paths } => up::Command::PickAnswer { id, paths },
        C::PreviewInvite { code } => up::Command::PreviewInvite(code),
        C::JoinInvite { code } => up::Command::JoinInvite(code),
        C::DemoIncoming {
            delay_ms,
            chat,
            sender,
            sender_name,
            text,
        } => {
            if !shared.demo {
                return Err(Refused::DemoOnly);
            }
            return Ok(Routed::Demo(crate::demo::Injected::Incoming {
                delay_ms,
                chat,
                sender,
                sender_name,
                text,
            }));
        }
        C::DemoTyping {
            delay_ms,
            chat,
            sender,
            composing,
        } => {
            if !shared.demo {
                return Err(Refused::DemoOnly);
            }
            return Ok(Routed::Demo(crate::demo::Injected::Typing {
                delay_ms,
                chat,
                sender,
                composing,
            }));
        }
        C::DemoPanic { on_thread } => {
            if !shared.demo {
                return Err(Refused::DemoOnly);
            }
            if !on_thread {
                panic!("DemoPanic inside zf_send");
            }
            return Ok(Routed::Demo(crate::demo::Injected::Panic));
        }
    }))
}

/// Whether `len` bytes are exactly a `width` × `height` RGBA picture. The
/// product is checked: a width and height of 2^31 each wrap a plain `u64`
/// multiplication to zero.
fn is_rgba(width: u32, height: u32, len: u64) -> bool {
    width != 0
        && height != 0
        && u64::from(width)
            .checked_mul(u64::from(height))
            .and_then(|pixels| pixels.checked_mul(4))
            == Some(len)
}

/// A count or position from the wire as `usize`.
fn index(n: u64) -> Result<usize, Refused> {
    usize::try_from(n).map_err(|_| Refused::Invalid("an index too large for this platform"))
}

fn privacy_kind_from(kind: w::PrivacyKind) -> zapfast::privacy::PrivacyKind {
    use zapfast::privacy::PrivacyKind as K;
    match kind {
        w::PrivacyKind::LastSeen => K::LastSeen,
        w::PrivacyKind::Online => K::Online,
        w::PrivacyKind::Profile => K::Profile,
        w::PrivacyKind::About => K::About,
        w::PrivacyKind::GroupAdd => K::GroupAdd,
        w::PrivacyKind::ReadReceipts => K::ReadReceipts,
        w::PrivacyKind::CallAdd => K::CallAdd,
    }
}

fn privacy_choice_from(choice: w::PrivacyChoice) -> zapfast::privacy::PrivacyChoice {
    use zapfast::privacy::PrivacyChoice as C;
    match choice {
        w::PrivacyChoice::Everyone => C::Everyone,
        w::PrivacyChoice::Contacts => C::Contacts,
        w::PrivacyChoice::Except => C::Except,
        w::PrivacyChoice::Nobody => C::Nobody,
        w::PrivacyChoice::SameAsLastSeen => C::SameAsLastSeen,
        w::PrivacyChoice::Known => C::Known,
    }
}

fn model_reply(r: w::ReplyTarget) -> m::ReplyTarget {
    m::ReplyTarget {
        id: r.id,
        chat: r.chat,
    }
}

/// Who sends each upstream command. An exhaustive match: a command upstream
/// adds breaks the build until it is classified here (and, if the UI sends
/// it, given a record).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Origin {
    /// Sent by the interface: needs a command record.
    Ui,
    /// The worker's own completion, sent back into its queue: never exported.
    Internal,
    /// The egui build's updater, only with the `ui` feature: the Go app has
    /// its own (Z4).
    Updater,
}

pub fn origin(c: &up::Command) -> Origin {
    use Origin::*;
    use up::Command as C;
    match c {
        C::RefreshPoll { .. } => Ui,
        C::PollHistoryFailed { .. } => Internal,
        C::CreatePoll { .. } => Ui,
        C::PollCreated { .. } => Internal,
        C::VotePoll { .. } => Ui,
        C::PollVoted { .. } => Internal,
        C::PollDecoded { .. } => Internal,
        C::SendText { .. } => Ui,
        C::SendLocation { .. } => Ui,
        C::ReplyInteractive { .. } => Ui,
        C::Forward { .. } => Ui,
        C::ForwardLimit { .. } => Ui,
        C::Composing { .. } => Ui,
        C::SaveDraft { .. } => Ui,
        C::MarkRead { .. } => Ui,
        C::MarkUnread(_) => Ui,
        C::WatchReceipts(_) => Ui,
        C::ReadSyncFinished { .. } => Internal,
        C::UnreadSyncFinished { .. } => Internal,
        C::LoadChat { .. } => Ui,
        C::LoadPins(_) => Ui,
        C::FetchOlder { .. } => Ui,
        C::ReloadHistory { .. } => Ui,
        C::SetHistoryPrefetch { .. } => Ui,
        C::LoadChatMedia { .. } => Ui,
        C::Download { .. } => Ui,
        C::DownloadMotion { .. } => Ui,
        C::FetchAvatar { .. } => Ui,
        C::LoadUntil { .. } => Ui,
        C::SearchMessages { .. } => Ui,
        C::SearchChatMessages { .. } => Ui,
        C::SetStar { .. } => Ui,
        C::LoadStarred => Ui,
        C::Starred { .. } => Internal,
        C::ListChatShared { .. } => Ui,
        C::EnsureChat { .. } => Ui,
        C::OlderSent { .. } => Internal,
        C::OlderFailed { .. } => Internal,
        C::GroupInfoFailed { .. } => Internal,
        C::EditText { .. } => Ui,
        C::EditSettled { .. } => Internal,
        C::Revoke { .. } => Ui,
        C::DeleteLocal { .. } => Ui,
        C::MessageDeletedForMe { .. } => Internal,
        C::PickFiles { .. } => Ui,
        C::SendFiles { .. } => Ui,
        C::SendImage { .. } => Ui,
        C::SetMuted(..) => Ui,
        C::SetLocked(..) => Ui,
        C::CreateLabel { .. } => Ui,
        C::UpdateLabel { .. } => Ui,
        C::DeleteLabel(_) => Ui,
        C::SetChatLabels { .. } => Ui,
        C::SendVoice { .. } => Ui,
        C::StartCall { .. } => Ui,
        C::JoinCallLink { .. } => Ui,
        C::AnswerCall => Ui,
        C::DeclineCall => Ui,
        C::HangupCall => Ui,
        C::SetCallMuted(_) => Ui,
        C::SetCallCamera(_) => Ui,
        C::SetCallMicrophone(_) => Ui,
        C::SetCallSpeaker(_) => Ui,
        C::SetCallCameraDevice(_) => Ui,
        C::RefreshCallDevices => Ui,
        C::LoadCallLog => Ui,
        C::MarkPlayed { .. } => Ui,
        C::SendSticker { .. } => Ui,
        C::SaveSticker { .. } => Ui,
        C::FavoritePushed { .. } => Internal,
        C::FavoritesPushed => Internal,
        C::FavoritesRecovered { .. } => Internal,
        C::FirstNamesRecovered { .. } => Internal,
        C::FavoriteFetched { .. } => Internal,
        C::RemoveRecentSticker { .. } => Ui,
        C::ForgetSticker { .. } => Ui,
        C::ImportStickerUrl { .. } => Ui,
        C::PickStickerArchive => Ui,
        C::PickNotificationSound { .. } => Ui,
        C::SetChatSound { .. } => Ui,
        C::PickChatSound(_) => Ui,
        C::PickDownloadFolder => Ui,
        C::PickWallpaperImage => Ui,
        C::RemoveWallpaperImage => Ui,
        C::SetProfile { .. } => Ui,
        C::PickProfilePicture => Ui,
        C::SetProfilePicture(_) => Internal,
        C::SetGroupName { .. } => Ui,
        C::PickGroupPicture(_) => Ui,
        C::SetGroupPicture { .. } => Ui,
        C::GroupEdited { .. } => Internal,
        C::ProfileSaved { .. } => Internal,
        C::SetDownloadFolder(_) => Ui,
        C::SetKeepChatsArchived(_) => Ui,
        C::Resumed => Ui,
        C::SaveAttachmentAs { .. } => Ui,
        C::OpenLog(_) => Ui,
        C::SaveAttachments { .. } => Ui,
        C::PrepareClipboardImage(_) => Ui,
        C::DeleteStickerPack { .. } => Ui,
        C::StickerPackImported { .. } => Internal,
        C::ViewStickerPack { .. } => Ui,
        C::StickerPackViewed { .. } => Internal,
        C::AddStickerPack { .. } => Ui,
        C::SendStickerPack { .. } => Ui,
        C::PickStickerPicture => Ui,
        C::StickerPicturePicked { .. } => Internal,
        C::MakeSticker { .. } => Ui,
        C::StickerMade { .. } => Internal,
        C::InspectPicture { .. } => Ui,
        C::PictureInspected { .. } => Internal,
        C::ApplyPictureEdit { .. } => Ui,
        C::PictureEdited { .. } => Internal,
        C::CreateStickerPack { .. } => Ui,
        C::SetStickerPack { .. } => Ui,
        C::SaveContact { .. } => Ui,
        C::ContactSaved { .. } => Internal,
        C::NewContact { .. } => Ui,
        C::ContactChecked { .. } => Internal,
        C::SendGif { .. } => Ui,
        C::SearchGifs { .. } => Ui,
        C::RecentStickers => Ui,
        C::React { .. } => Ui,
        C::SetArchived(..) => Ui,
        C::LeaveGroup { .. } => Ui,
        C::DeleteChat(_) => Ui,
        C::ChatDeleted { .. } => Internal,
        C::ClearChat(_) => Ui,
        C::ChatCleared { .. } => Internal,
        C::SetPinned(..) => Ui,
        C::SetMessagePinned { .. } => Ui,
        C::MessagePinned { .. } => Internal,
        C::SetFavorite(..) => Ui,
        C::FavoritesSent { .. } => Internal,
        C::PairWithPhone(_) => Ui,
        C::Unlink => Ui,
        C::RemoveAccount => Ui,
        C::Reconnect => Ui,
        C::SetProxy(_) => Ui,
        C::StartOverArchive => Ui,
        C::SetOnline(_) => Ui,
        // zf_core_close sends it.
        C::Shutdown => Ui,
        C::Sent { .. } => Internal,
        C::Downloaded { .. } => Internal,
        C::MotionDownloaded { .. } => Internal,
        C::StickerFetched { .. } => Internal,
        C::AvatarFetched { .. } => Internal,
        C::AvatarFailed { .. } => Internal,
        C::MeInfo { .. } => Internal,
        C::GifResults { .. } => Internal,
        C::LoadStorageStats { .. } => Ui,
        C::ClearMediaCache { .. } => Ui,
        C::StorageMeasured { .. } => Internal,
        C::Picked { .. } => Internal,
        C::Outbound { .. } => Internal,
        C::GroupRecipients { .. } => Internal,
        C::GroupInfo { .. } => Internal,
        C::PairCode { .. } => Internal,
        C::ReceiptsPrivacy { .. } => Internal,
        C::AccountPrivacy { .. } => Internal,
        C::FetchAccountPrivacy => Ui,
        C::SetAccountPrivacy { .. } => Ui,
        C::AccountPrivacySaved { .. } => Internal,
        C::AccountPrivacyFailed { .. } => Internal,
        C::ChannelMutes(_) => Internal,
        C::ChannelPictures(_) => Internal,
        C::PreviewInvite(_) => Ui,
        C::PickAnswer { .. } => Ui,
        C::JoinInvite(_) => Ui,
        C::InviteJoined { .. } => Internal,
        #[cfg(feature = "ui")]
        C::CheckForUpdates => Updater,
        #[cfg(feature = "ui")]
        C::InspectUpdate => Updater,
        #[cfg(feature = "ui")]
        C::DownloadUpdate { .. } => Updater,
        #[cfg(feature = "ui")]
        C::InstallUpdate { .. } => Updater,
    }
}
