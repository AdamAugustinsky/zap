//! The demo core: no account and no network. It emits upstream's own
//! `Event`s, through the same conversions as the real backend, and answers
//! commands the way the worker does (a send is archived as pending, then
//! sent, delivered and read). `DemoIncoming` and `DemoTyping` inject what
//! another person would do, at a scripted moment; a `DemoIncoming` whose text
//! is `CALL_TEXT` or `VIDEO_CALL_TEXT` rings instead (`zfcore.h`, "The demo
//! core").
//!
//! Calls: one at a time, as the worker allows. A call placed here rings and
//! is answered; one rung in waits for `AnswerCall` or `DeclineCall`, and is
//! missed after `MISSED_AFTER`. While a video call is up, each stream that has
//! a picture gets a moving test pattern at about 15 frames a second, through
//! the same `CallVideo` path as the real core's frames. A finished call is
//! filed in its chat and the call log, as the worker files it.
//!
//! The people and lines are upstream's demo samples (`src/demo.rs`), and the
//! profile pictures upstream's CC0 and public-domain portraits
//! (`assets/demo/SOURCES.md`).

use std::collections::{BinaryHeap, HashMap};
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::mpsc;
use std::sync::{Arc, Mutex};
use std::thread::JoinHandle;
use std::time::{Duration, Instant};

use zapfast::backend::{Command, Event, LinkStatus};
use zapfast::calls::{
    AudioDevice, CallOutcome, CallPhase, CallUpdate, CameraDevice, DeviceList, GroupCall,
    Participant,
};
use zapfast::model::{
    CallDirection, Chat, ChatLink, ChatMedia, Contact, Content, Delivery, LastMessage, Media,
    MentionRef, Message, MessageReceipts, Quoted, Recipient, ReplyTarget, StorageStats,
};
use zapfast::privacy::{PrivacyChoice, PrivacyKind};
use zapfast::video_frames::CallFrame;

use crate::cores::Shared;
use crate::wake::lock;

/// What the demo core does with a command, decided before the command is
/// converted (so a refused one has taken no blob).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Disposition {
    /// It answers with events, as the worker would.
    Answers,
    /// It is taken and nothing comes back, as with the real core.
    Accepts,
    /// It is refused with `ZF_E_STATE` naming the command.
    Refuses,
}

/// An exhaustive match: a command added to the schema must be classified
/// here, so none can be ignored by accident.
pub fn disposition(command: &crate::abi_gen::Command) -> Disposition {
    use crate::abi_gen::Command as C;
    use Disposition::*;
    match command {
        C::LoadChat { .. }
        | C::SendText { .. }
        | C::SendLocation { .. }
        | C::SendImage { .. }
        | C::SendVoice { .. }
        | C::MarkRead { .. }
        | C::SetMuted { .. }
        | C::OpenLog { .. }
        | C::Reconnect
        | C::WatchReceipts { .. }
        | C::FetchAvatar { .. }
        | C::ListChatShared { .. }
        | C::StartCall { .. }
        | C::JoinCallLink { .. }
        | C::AnswerCall
        | C::DeclineCall
        | C::HangupCall
        | C::SetCallMuted { .. }
        | C::SetCallCamera { .. }
        | C::SetCallMicrophone { .. }
        | C::SetCallSpeaker { .. }
        | C::SetCallCameraDevice { .. }
        | C::RefreshCallDevices
        | C::LoadCallLog
        | C::LoadStorageStats { .. }
        | C::ClearMediaCache { .. }
        | C::FetchAccountPrivacy
        | C::SetAccountPrivacy { .. }
        | C::EnsureChat { .. }
        | C::SetLocked { .. }
        | C::VotePoll { .. }
        | C::LoadChatMedia { .. }
        | C::SearchGifs { .. }
        | C::SendGif { .. }
        | C::RecentStickers
        | C::SendSticker { .. }
        | C::SaveSticker { .. }
        | C::RemoveRecentSticker { .. }
        | C::ForgetSticker { .. }
        | C::SendStickerPack { .. }
        | C::ViewStickerPack { .. }
        | C::AddStickerPack { .. }
        | C::DemoIncoming { .. }
        | C::DemoTyping { .. }
        | C::DemoPanic { .. } => Answers,
        C::Composing { .. }
        | C::SaveDraft { .. }
        | C::SetOnline { .. }
        | C::Resumed
        | C::SetHistoryPrefetch { .. }
        | C::CancelCall { .. }
        // Upstream sends the played receipt and files nothing.
        | C::MarkPlayed { .. }
        | C::PickAnswer { .. } => Accepts,
        C::RefreshPoll { .. }
        | C::CreatePoll { .. }
        | C::ReplyInteractive { .. }
        | C::Forward { .. }
        | C::ForwardLimit { .. }
        | C::MarkUnread { .. }
        | C::LoadPins { .. }
        | C::FetchOlder { .. }
        | C::ReloadHistory { .. }
        | C::Download { .. }
        | C::DownloadMotion { .. }
        | C::LoadUntil { .. }
        | C::SearchMessages { .. }
        | C::SearchChatMessages { .. }
        | C::SetStar { .. }
        | C::LoadStarred
        | C::EditText { .. }
        | C::Revoke { .. }
        | C::DeleteLocal { .. }
        | C::PickFiles { .. }
        | C::SendFiles { .. }
        | C::CreateLabel { .. }
        | C::UpdateLabel { .. }
        | C::DeleteLabel { .. }
        | C::SetChatLabels { .. }
        | C::ImportStickerUrl { .. }
        | C::PickStickerArchive
        | C::PickNotificationSound { .. }
        | C::SetChatSound { .. }
        | C::PickChatSound { .. }
        | C::PickDownloadFolder
        | C::PickWallpaperImage
        | C::RemoveWallpaperImage
        | C::SetProfile { .. }
        | C::PickProfilePicture
        | C::SetGroupName { .. }
        | C::PickGroupPicture { .. }
        | C::SetGroupPicture { .. }
        | C::SetDownloadFolder { .. }
        | C::SetKeepChatsArchived { .. }
        | C::SaveAttachmentAs { .. }
        | C::SaveAttachments { .. }
        | C::PrepareClipboardImage { .. }
        | C::DeleteStickerPack { .. }
        | C::PickStickerPicture
        | C::MakeSticker { .. }
        | C::InspectPicture { .. }
        | C::ApplyPictureEdit { .. }
        | C::CreateStickerPack { .. }
        | C::SetStickerPack { .. }
        | C::SaveContact { .. }
        | C::NewContact { .. }
        | C::React { .. }
        | C::SetArchived { .. }
        | C::LeaveGroup { .. }
        | C::DeleteChat { .. }
        | C::ClearChat { .. }
        | C::SetPinned { .. }
        | C::SetMessagePinned { .. }
        | C::SetFavorite { .. }
        | C::PairWithPhone { .. }
        | C::Unlink
        | C::RemoveAccount
        | C::SetProxy { .. }
        | C::StartOverArchive
        | C::PreviewInvite { .. }
        | C::JoinInvite { .. } => Refuses,
    }
}

pub const ME: &str = "15550001111@s.whatsapp.net";

struct Sample {
    id: &'static str,
    name: &'static str,
    minutes_ago: i64,
    unread: u32,
    pinned: bool,
    muted: bool,
    lines: &'static [(bool, &'static str)],
}

const SAMPLES: &[Sample] = &[
    Sample {
        id: "393331234567@s.whatsapp.net",
        name: "Ada Lovelace",
        minutes_ago: 3,
        unread: 2,
        pinned: true,
        muted: false,
        lines: &[
            (false, "Did the analytical engine build finish?"),
            (true, "Yes! It compiles on stable now, no nightly needed."),
            (
                false,
                "That is wonderful news. Send me the branch when you can.",
            ),
        ],
    },
    Sample {
        id: "120363012345678901@g.us",
        name: "Rust Berlin",
        minutes_ago: 25,
        unread: 14,
        pinned: false,
        muted: true,
        lines: &[
            (false, "Anyone at the meetup tonight?"),
            (true, "I'll be there around 19:00"),
            (false, "Same. Bringing the egui demo"),
            (false, "Save me a seat 🙏"),
        ],
    },
    Sample {
        id: "441632960123@s.whatsapp.net",
        name: "Grace Hopper",
        minutes_ago: 90,
        unread: 0,
        pinned: false,
        muted: false,
        lines: &[
            (true, "Found the bug. It was a moth."),
            (false, "Literally?"),
            (true, "Literally. Taped it into the logbook."),
        ],
    },
    Sample {
        id: "4915112345678@s.whatsapp.net",
        name: "Katherine Johnson",
        minutes_ago: 60 * 26,
        unread: 0,
        pinned: false,
        muted: false,
        lines: &[
            (false, "Talk is cheap. Show me the code."),
            (true, "Pushed 😌"),
        ],
    },
    Sample {
        id: "120363098765432109@g.us",
        name: "Family",
        minutes_ago: 60 * 50,
        unread: 0,
        pinned: false,
        muted: false,
        lines: &[
            (false, "Dinner on Sunday at 13:00?"),
            (true, "We'll be there"),
            (false, "Bring the good bread 🥖"),
        ],
    },
    Sample {
        id: "14155550199@s.whatsapp.net",
        name: "Margaret Hamilton",
        minutes_ago: 60 * 24 * 4,
        unread: 0,
        pinned: false,
        muted: false,
        lines: &[
            (false, "The landing software held up."),
            (true, "Never doubted it."),
        ],
    },
    // Locked (`LOCKED`), with a photo, a clip and a voice note.
    Sample {
        id: DOROTHY,
        name: "Dorothy Vaughan",
        minutes_ago: 60 * 24 * 5,
        unread: 0,
        pinned: false,
        muted: false,
        lines: &[
            (false, "The trajectory numbers check out."),
            (true, "Thank you. I'll tell the team."),
            (false, "Keep these between us until the launch."),
        ],
    },
];

/// Dorothy's chat, the one the demo keeps locked: it lives in the locked
/// folder, whose code is the UI's.
const DOROTHY: &str = "15550004444@s.whatsapp.net";
/// The chats that start locked.
const LOCKED: &[&str] = &[DOROTHY];

/// The first frames of the stock clips, as their messages' thumbnails
/// (`zfcore/assets/posters`).
const LAUNCH_POSTER: &[u8] = include_bytes!("../assets/posters/launch.jpg");
const NOTE_POSTER: &[u8] = include_bytes!("../assets/posters/note.jpg");

/// The thumbnail a real video message carries, for the demo's clips: the
/// first frame of the stock clip it plays. A GIF's is its search still.
fn poster_of(content: &Content) -> Option<Vec<u8>> {
    match content {
        Content::Video {
            gif: false, note, ..
        } => Some(if *note { NOTE_POSTER } else { LAUNCH_POSTER }.to_vec()),
        _ => None,
    }
}

/// A `DemoIncoming` text that rings a voice call from its sender in its chat
/// (a group chat rings a group call) instead of filing a message.
pub const CALL_TEXT: &str = "zf-demo:call";
/// The same for a video call.
pub const VIDEO_CALL_TEXT: &str = "zf-demo:video-call";

/// How long a call rung in waits before it is missed.
pub const MISSED_AFTER: Duration = Duration::from_secs(30);
/// A placed call: until its offer goes out, until the peer's phone rings,
/// until they answer, and until the media is up.
const PLACING: Duration = Duration::from_millis(150);
const DIALING: Duration = Duration::from_millis(400);
const RINGING: Duration = Duration::from_millis(1500);
const CONNECTING: Duration = Duration::from_millis(300);
/// About 15 frames a second.
const FRAME_EVERY: Duration = Duration::from_millis(66);

/// The members of the demo's groups besides us, for their calls.
const GROUP_MEMBERS: &[(&str, &[&str])] = &[
    (
        "120363012345678901@g.us",
        &[
            "393331234567@s.whatsapp.net",
            "441632960123@s.whatsapp.net",
            "4915112345678@s.whatsapp.net",
        ],
    ),
    (
        "120363098765432109@g.us",
        &["14155550199@s.whatsapp.net", "441632960123@s.whatsapp.net"],
    ),
];

/// Who is on a call link the demo joins.
const LINK_MEMBERS: &[&str] = &[
    "393331234567@s.whatsapp.net",
    "4915112345678@s.whatsapp.net",
];

fn members_of(chat: &str) -> &'static [&'static str] {
    GROUP_MEMBERS
        .iter()
        .find(|(id, _)| *id == chat)
        .map_or(&[], |(_, members)| members)
}

/// Upstream's demo portraits (`src/demo/stock.rs`), by owner.
const AVATARS: &[(&str, &str, &[u8])] = &[
    (
        ME,
        "astronaut.jpg",
        include_bytes!("../../assets/demo/avatars/astronaut.jpg"),
    ),
    (
        "393331234567@s.whatsapp.net",
        "ada.jpg",
        include_bytes!("../../assets/demo/avatars/ada.jpg"),
    ),
    (
        "441632960123@s.whatsapp.net",
        "grace.jpg",
        include_bytes!("../../assets/demo/avatars/grace.jpg"),
    ),
    (
        "4915112345678@s.whatsapp.net",
        "katherine.jpg",
        include_bytes!("../../assets/demo/avatars/katherine.jpg"),
    ),
    (
        "14155550199@s.whatsapp.net",
        "earthrise.jpg",
        include_bytes!("../../assets/demo/avatars/earthrise.jpg"),
    ),
    (
        "120363012345678901@g.us",
        "berlin.jpg",
        include_bytes!("../../assets/demo/avatars/berlin.jpg"),
    ),
    (
        "120363098765432109@g.us",
        "bread.jpg",
        include_bytes!("../../assets/demo/avatars/bread.jpg"),
    ),
];

/// Where the demo's profile pictures are written for `Avatar` to name: the
/// system's temporary directory, never the account's.
pub fn avatar_dir() -> PathBuf {
    std::env::temp_dir().join("zapfast-demo-avatars")
}

/// The picture of `id` as a file, written once (through a temporary name, so
/// a reader never sees half of one); `None` for anyone without one.
fn avatar_path(id: &str) -> Option<PathBuf> {
    let (_, name, bytes) = AVATARS.iter().find(|(owner, _, _)| *owner == id)?;
    write_once(&avatar_dir(), name, bytes)
}

/// `bytes` as the file `name` in `dir`, written once: kept when it is there
/// with their length, else written through a name unique to this write (demo
/// cores in one process may write the same file at once), so a reader never
/// sees half of one.
fn write_once(dir: &std::path::Path, name: &str, bytes: &[u8]) -> Option<PathBuf> {
    let path = dir.join(name);
    if std::fs::metadata(&path).is_ok_and(|file| file.len() == bytes.len() as u64) {
        return Some(path);
    }
    std::fs::create_dir_all(dir).ok()?;
    static WRITES: AtomicU64 = AtomicU64::new(0);
    let n = WRITES.fetch_add(1, Ordering::Relaxed);
    let partial = dir.join(format!(".{name}.{}.{n}", std::process::id()));
    std::fs::write(&partial, bytes).ok()?;
    std::fs::rename(&partial, &path).ok()?;
    Some(path)
}

/// A stock file of the demo's in `media_dir`, by name.
fn stock(name: &str, bytes: &[u8]) -> Option<PathBuf> {
    write_once(&media_dir(), name, bytes)
}

/// The GIF results the demo finds: upstream's stock stills (320 × 240),
/// with the words a search finds each by. An empty search lists them all,
/// as GIPHY's trending does.
const GIFS: &[(&str, &[u8], &[&str])] = &[
    (
        "launch",
        include_bytes!("../../assets/demo/gifs/launch.jpg"),
        &["launch", "rocket", "space", "ship it", "go"],
    ),
    (
        "fireworks",
        include_bytes!("../../assets/demo/gifs/fireworks.jpg"),
        &["fireworks", "party", "celebrate", "yay", "happy"],
    ),
    (
        "pug",
        include_bytes!("../../assets/demo/gifs/pug.jpg"),
        &["pug", "dog", "puppy", "cute", "aww"],
    ),
    (
        "frog",
        include_bytes!("../../assets/demo/gifs/frog.jpg"),
        &["frog", "ribbit", "hello", "hi"],
    ),
    (
        "goat",
        include_bytes!("../../assets/demo/gifs/goat.jpg"),
        &["goat", "no", "nope", "stop"],
    ),
    (
        "raccoon",
        include_bytes!("../../assets/demo/gifs/raccoon.jpg"),
        &["raccoon", "sneaky", "night", "trash"],
    ),
];

/// The GIPHY key the demo rejects, for the bad-key state.
pub const BAD_GIF_KEY: &str = "zf-demo:bad-key";

/// The clip a GIF the demo sends plays: upstream's stock launch cut.
const GIF_CLIP: &[u8] = include_bytes!("../../assets/demo/videos/launch.mp4");

/// Upstream's stock stickers: name, file, whether animated, and emojis.
const STICKERS: &[(&str, &[u8], bool, &[&str])] = &[
    (
        "astronaut",
        include_bytes!("../../assets/demo/stickers/astronaut.webp"),
        false,
        &["🧑‍🚀", "🚀"],
    ),
    (
        "astronaut-float",
        include_bytes!("../../assets/demo/stickers/astronaut-float.webp"),
        true,
        &["🧑‍🚀", "✨"],
    ),
    (
        "cat",
        include_bytes!("../../assets/demo/stickers/cat.webp"),
        false,
        &["🐱", "😴"],
    ),
    (
        "frog",
        include_bytes!("../../assets/demo/stickers/frog.webp"),
        false,
        &["🐸"],
    ),
    (
        "goat",
        include_bytes!("../../assets/demo/stickers/goat.webp"),
        false,
        &["🐐", "🙅"],
    ),
    (
        "owl",
        include_bytes!("../../assets/demo/stickers/owl.webp"),
        false,
        &["🦉"],
    ),
    (
        "pug",
        include_bytes!("../../assets/demo/stickers/pug.webp"),
        false,
        &["🐶", "🥺"],
    ),
    (
        "raccoon",
        include_bytes!("../../assets/demo/stickers/raccoon.webp"),
        false,
        &["🦝"],
    ),
];

/// The demo's pack of all its stickers, shared in Rust Berlin.
const PACK_NAME: &str = "Launch party";

fn sticker_file(name: &str) -> Option<PathBuf> {
    let (_, bytes, _, _) = STICKERS.iter().find(|(n, ..)| *n == name)?;
    write_once(
        &media_dir().join("stickers"),
        &format!("{name}.webp"),
        bytes,
    )
}

fn sticker_media(path: &std::path::Path) -> Media {
    let animated = STICKERS
        .iter()
        .find(|(name, ..)| path.file_stem().is_some_and(|stem| stem == *name))
        .is_some_and(|(_, _, animated, _)| *animated);
    Media {
        mime: "image/webp".into(),
        size: std::fs::metadata(path).map_or(0, |m| m.len()),
        width: Some(if animated { 256 } else { 512 }),
        height: Some(if animated { 256 } else { 512 }),
        album: None,
        path: Some(path.to_owned()),
        state: Default::default(),
    }
}

fn sticker_content(path: &std::path::Path) -> Content {
    let media = sticker_media(path);
    Content::Sticker {
        animated: media.width == Some(256),
        media,
    }
}

/// The picker's shelves, as the worker's `emit_stickers` lists them:
/// Recent newest first, Favorites, Received (others' stickers in neither),
/// and the packs.
#[derive(Default)]
struct StickerShelves {
    favorites: Vec<PathBuf>,
    recent: Vec<PathBuf>,
    received: Vec<PathBuf>,
    packs: Vec<zapfast::model::StickerPack>,
    emojis: HashMap<PathBuf, Vec<String>>,
}

impl StickerShelves {
    /// Written once into `media_dir`: Recent has four, Favorites two,
    /// Received the two others sent in their chats, and there is one pack
    /// of all eight and one local pack of our own.
    fn stock() -> Self {
        let file = |name: &str| sticker_file(name);
        let all: Vec<PathBuf> = STICKERS.iter().filter_map(|(n, ..)| file(n)).collect();
        let mut emojis = HashMap::new();
        for ((_, _, _, tags), path) in STICKERS.iter().zip(&all) {
            emojis.insert(path.clone(), tags.iter().map(|&t| t.to_owned()).collect());
        }
        let pick = |names: &[&str]| names.iter().filter_map(|n| file(n)).collect::<Vec<_>>();
        let mine = media_dir().join("packs").join("mine");
        let _ = std::fs::create_dir_all(&mine);
        Self {
            recent: pick(&["pug", "frog", "astronaut-float", "cat"]),
            favorites: pick(&["owl", "astronaut"]),
            received: pick(&["goat", "raccoon"]),
            packs: vec![
                zapfast::model::StickerPack {
                    name: PACK_NAME.into(),
                    dir: media_dir().join("stickers"),
                    stickers: all,
                    local: false,
                },
                zapfast::model::StickerPack {
                    name: "My stickers".into(),
                    dir: mine,
                    stickers: pick(&["astronaut", "owl"]),
                    local: true,
                },
            ],
            emojis,
        }
    }

    fn event(&self) -> Event {
        Event::Stickers {
            favorites: self.favorites.clone(),
            packs: self.packs.clone(),
            recent: self.recent.clone(),
            received: self.received.clone(),
            emojis: self.emojis.clone(),
        }
    }

    /// A sticker we sent is Recent's newest, whatever was removed before:
    /// an old one rejoins Recent once sent again from its chat.
    fn used(&mut self, path: &std::path::Path) {
        self.recent.retain(|p| p != path);
        self.received.retain(|p| p != path);
        self.recent.insert(0, path.to_owned());
    }
}

/// A one-page PDF of `title` and `lines`, in Helvetica: the demo's own
/// document, so it needs no file of anyone else's.
fn tiny_pdf(title: &str, lines: &[&str]) -> Vec<u8> {
    let text = |s: &str| {
        s.replace('\\', "\\\\")
            .replace('(', "\\(")
            .replace(')', "\\)")
    };
    let mut content = format!("BT /F1 20 Tf 72 720 Td ({}) Tj ET\n", text(title));
    for (i, line) in lines.iter().enumerate() {
        content += &format!(
            "BT /F1 12 Tf 72 {} Td ({}) Tj ET\n",
            684 - 18 * i,
            text(line)
        );
    }
    let objects = [
        "<< /Type /Catalog /Pages 2 0 R >>".to_owned(),
        "<< /Type /Pages /Kids [3 0 R] /Count 1 >>".to_owned(),
        "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>".to_owned(),
        format!("<< /Length {} >>\nstream\n{content}endstream", content.len()),
        "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>".to_owned(),
    ];
    let mut pdf = b"%PDF-1.4\n".to_vec();
    let mut offsets = Vec::new();
    for (i, object) in objects.iter().enumerate() {
        offsets.push(pdf.len());
        pdf.extend_from_slice(format!("{} 0 obj\n{object}\nendobj\n", i + 1).as_bytes());
    }
    let xref = pdf.len();
    pdf.extend_from_slice(
        format!("xref\n0 {}\n0000000000 65535 f \n", objects.len() + 1).as_bytes(),
    );
    for offset in offsets {
        pdf.extend_from_slice(format!("{offset:010} 00000 n \n").as_bytes());
    }
    pdf.extend_from_slice(
        format!(
            "trailer\n<< /Size {} /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF\n",
            objects.len() + 1
        )
        .as_bytes(),
    );
    pdf
}

/// The microphones, speakers and cameras the demo offers.
fn devices() -> DeviceList {
    let audio = |id: &str, label: &str| AudioDevice {
        id: id.into(),
        label: label.into(),
    };
    DeviceList {
        microphones: vec![
            audio("BuiltInMicrophoneDevice", "MacBook Pro Microphone"),
            audio("demo-headset-mic", "Demo Headset"),
        ],
        speakers: vec![
            audio("BuiltInSpeakerDevice", "MacBook Pro Speakers"),
            audio("desk-speakers", "Desk Speakers"),
            audio("demo-headset", "Demo Headset"),
        ],
        cameras: vec![
            CameraDevice {
                id: "0".into(),
                label: "FaceTime HD Camera".into(),
            },
            CameraDevice {
                id: "1".into(),
                label: "Desk Camera".into(),
            },
        ],
    }
}

/// The egui build's frames are RGBA, and that build only checks that zfcore
/// compiles: a flat grey picture.
#[cfg(feature = "ui")]
pub fn test_pattern(size: [usize; 2], _tint: usize, _frame: u64) -> CallFrame {
    CallFrame::from_rgba_unmultiplied(size, &vec![128; size[0] * size[1] * 4])
}

/// A moving I420 test pattern: eight grey bars sliding sideways, a white
/// square crossing them, and the stream's own tint.
#[cfg(not(feature = "ui"))]
pub fn test_pattern(size: [usize; 2], tint: usize, frame: u64) -> CallFrame {
    const TINTS: [(u8, u8); 6] = [
        (90, 240),
        (240, 110),
        (54, 34),
        (200, 180),
        (110, 60),
        (160, 90),
    ];
    let mut picture = CallFrame::new(size);
    let [width, height] = size;
    if width == 0 || height == 0 {
        return picture;
    }
    let strides = picture.strides;
    let (y, u, v) = picture.planes_mut();
    let shift = (frame as usize * 4) % width;
    let mut row = vec![0u8; width];
    for (x, luma) in row.iter_mut().enumerate() {
        *luma = 40 + 24 * (((x + shift) * 8 / width) % 8) as u8;
    }
    let side = (height / 3).max(1);
    let travel = |span: usize, step: usize| {
        let room = span.saturating_sub(side).max(1);
        let at = (frame as usize * step) % (2 * room);
        if at < room { at } else { 2 * room - at }
    };
    let (bx, by) = (travel(width, 7), travel(height, 3));
    for line in 0..height {
        let out = &mut y[line * strides[0]..line * strides[0] + width];
        out.copy_from_slice(&row);
        if (by..by + side).contains(&line) {
            out[bx..(bx + side).min(width)].fill(235);
        }
    }
    let (cu, cv) = TINTS[tint % TINTS.len()];
    u.fill(cu);
    v.fill(cv);
    picture
}

/// Where the demo keeps its voice notes and the ones it is sent: the
/// system's temporary directory, never the account's. `zf_voice_decode`
/// reads only here on a demo core.
pub fn media_dir() -> PathBuf {
    std::env::temp_dir().join("zapfast-demo-media")
}

/// Something like speech: a voiced tone whose pitch drifts, in syllables of
/// about a fifth of a second, with a pause every few.
pub fn speech(seconds: f32, pitch: f32) -> Vec<f32> {
    let rate = zapfast::voice::RATE as f32;
    let mut phase = 0.0f32;
    (0..(seconds * rate) as usize)
        .map(|i| {
            let t = i as f32 / rate;
            let syllable = (t * 5.0).fract();
            let word = (t / 1.6).fract();
            let envelope = if word > 0.82 {
                0.0
            } else {
                (syllable * std::f32::consts::PI).sin().powf(0.7)
                    * (0.6 + 0.4 * (t * 0.9).sin().abs())
            };
            let f0 = pitch * (1.0 + 0.12 * (t * 2.3).sin() + 0.05 * (t * 7.0).sin());
            phase += f0 / rate;
            let tau = std::f32::consts::TAU;
            let voiced = (tau * phase).sin()
                + 0.5 * (2.0 * tau * phase).sin()
                + 0.25 * (3.0 * tau * phase).sin()
                + 0.12 * (5.0 * tau * phase).sin();
            0.3 * envelope * voiced
        })
        .collect()
}

/// A voice note's content for `samples` (mono 48 kHz), its file `name` in
/// `media_dir`: encoded with upstream's `voice::encode` when the file is not
/// there yet (or `fresh`), and written whole or not at all. Without a file
/// the note has no path, as one not downloaded.
fn voice_note(name: &str, samples: &[f32], fresh: bool) -> Content {
    let dir = media_dir();
    let path = dir.join(name);
    let size = match std::fs::metadata(&path) {
        Ok(file) if file.is_file() && !fresh => Some(file.len()),
        _ => zapfast::voice::encode(samples).ok().and_then(|bytes| {
            std::fs::create_dir_all(&dir).ok()?;
            // Unique to this write: demo cores in one process may write the
            // same note at once.
            static WRITES: AtomicU64 = AtomicU64::new(0);
            let n = WRITES.fetch_add(1, Ordering::Relaxed);
            let partial = dir.join(format!(".{name}.{}.{n}", std::process::id()));
            std::fs::write(&partial, &bytes).ok()?;
            std::fs::rename(&partial, &path).ok()?;
            Some(bytes.len() as u64)
        }),
    };
    Content::Audio {
        media: Media {
            mime: "audio/ogg; codecs=opus".into(),
            size: size.unwrap_or_default(),
            width: None,
            height: None,
            album: None,
            path: size.map(|_| path),
            state: Default::default(),
        },
        // As the worker counts them.
        seconds: Some(
            (samples.len() as f64 / f64::from(zapfast::voice::RATE))
                .round()
                .max(1.0) as u32,
        ),
        voice_note: true,
        waveform: zapfast::voice::waveform(samples),
    }
}

/// A demo-only command, routed here instead of to a backend.
#[derive(Debug)]
pub enum Injected {
    Incoming {
        delay_ms: u32,
        chat: String,
        sender: String,
        sender_name: Option<String>,
        text: String,
    },
    Typing {
        delay_ms: u32,
        chat: String,
        sender: String,
        composing: bool,
    },
    /// Panics on the demo thread.
    Panic,
}

enum Input {
    Start,
    Command(Box<Command>),
    Inject(Injected),
    Stop,
}

pub struct Demo {
    inbox: mpsc::Sender<Input>,
    thread: Mutex<Option<JoinHandle<()>>>,
}

impl Demo {
    pub fn spawn(shared: Arc<Shared>) -> std::io::Result<Self> {
        let (inbox, rx) = mpsc::channel();
        let thread = std::thread::Builder::new()
            .name("zf-demo".into())
            .spawn(move || {
                let owner = shared.clone();
                crate::owned_thread(&owner, "demo", move || State::new(shared).run(rx));
            })?;
        Ok(Self {
            inbox,
            thread: Mutex::new(Some(thread)),
        })
    }

    pub fn start(&self) {
        let _ = self.inbox.send(Input::Start);
    }

    pub fn command(&self, command: Command) {
        let _ = self.inbox.send(Input::Command(Box::new(command)));
    }

    pub fn inject(&self, injected: Injected) {
        let _ = self.inbox.send(Input::Inject(injected));
    }

    pub fn stop(&self) {
        let _ = self.inbox.send(Input::Stop);
        if let Some(thread) = lock(&self.thread).take() {
            let _ = thread.join();
        }
    }
}

/// Something the demo does later.
enum Due {
    Status {
        chat: String,
        id: String,
        status: Delivery,
    },
    Inject(Injected),
    /// The next step of call `generation`, if it is still the call.
    Call {
        generation: u64,
        step: Step,
    },
}

#[derive(Clone, Copy, Debug)]
enum Step {
    /// The offer goes out, unless the call was cancelled first.
    Offer,
    /// The peer's phone rings.
    Ring,
    /// The peer answers.
    Answer,
    /// The media is up.
    Connect,
    /// Nobody answered the call rung in.
    Missed,
    /// The next video frames.
    Frame,
}

/// The call the demo has up.
struct LiveCall {
    update: CallUpdate,
    cancel: Option<Arc<AtomicBool>>,
    /// Who rang, for a call rung in.
    caller: Option<String>,
    /// When it began, in Unix seconds, for its entry.
    began: i64,
    frame: u64,
    /// Whether its end is filed: not for a call that was never placed.
    file: bool,
}

struct Timer {
    at: Instant,
    seq: u64,
    due: Due,
}

impl PartialEq for Timer {
    fn eq(&self, other: &Self) -> bool {
        (self.at, self.seq) == (other.at, other.seq)
    }
}
impl Eq for Timer {}
impl PartialOrd for Timer {
    fn partial_cmp(&self, other: &Self) -> Option<std::cmp::Ordering> {
        Some(self.cmp(other))
    }
}
impl Ord for Timer {
    // A min-heap on (at, seq).
    fn cmp(&self, other: &Self) -> std::cmp::Ordering {
        (other.at, other.seq).cmp(&(self.at, self.seq))
    }
}

struct State {
    shared: Arc<Shared>,
    chats: Vec<Chat>,
    messages: HashMap<String, Vec<Message>>,
    contacts: Vec<Contact>,
    timers: BinaryHeap<Timer>,
    seq: u64,
    sent: u64,
    call: Option<LiveCall>,
    /// The call entries, newest first.
    call_log: Vec<Message>,
    /// The message whose receipts are followed.
    watching: Option<(String, String)>,
    privacy: Vec<(PrivacyKind, PrivacyChoice)>,
    cache_cleared: bool,
    /// The voice notes this core was sent, removed when it stops.
    sent_voice: Vec<PathBuf>,
    stickers: StickerShelves,
}

fn now() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |d| d.as_secs() as i64)
}

fn text_message(
    chat: &str,
    id: String,
    from_me: bool,
    sender: &str,
    timestamp: i64,
    text: &str,
) -> Message {
    Message {
        id,
        chat: chat.to_owned(),
        sender: sender.to_owned(),
        sender_name: None,
        from_me,
        timestamp,
        history_order: None,
        content: Content::text(text),
        status: if from_me {
            Delivery::Read
        } else {
            Delivery::None
        },
        delivered_at: None,
        read_at: None,
        quoted: None,
        reactions: Vec::new(),
        edited: false,
        mentions: Vec::new(),
        forwarded: false,
        thumbnail: None,
    }
}

fn last_of(message: &Message) -> LastMessage {
    let summary = message.summary();
    LastMessage {
        from_me: message.from_me,
        sender: message.sender.clone(),
        sender_name: message.sender_name.clone(),
        full: summary.clone(),
        summary,
        status: message.status,
    }
}

/// One call entry, as the worker files it (`file_call_entry`).
#[allow(clippy::too_many_arguments)]
fn call_entry(
    id: String,
    chat: &str,
    sender: &str,
    from_me: bool,
    timestamp: i64,
    video: bool,
    status: CallOutcome,
    duration: u64,
    group: bool,
) -> Message {
    let mut message = text_message(chat, id, from_me, sender, timestamp, "");
    message.status = Delivery::None;
    message.content = Content::Call {
        video,
        status,
        duration,
        group,
    };
    message
}

/// A few calls from before the demo began, newest first.
fn seeded_call_log(now: i64) -> Vec<Message> {
    const ADA: &str = "393331234567@s.whatsapp.net";
    const GRACE: &str = "441632960123@s.whatsapp.net";
    const KATHERINE: &str = "4915112345678@s.whatsapp.net";
    const MARGARET: &str = "14155550199@s.whatsapp.net";
    const BERLIN: &str = "120363012345678901@g.us";
    let hour = 3600;
    let entries: [(&str, &str, bool, i64, bool, CallOutcome, u64, bool); 5] = [
        (
            ADA,
            ME,
            true,
            2 * hour,
            false,
            CallOutcome::Answered,
            312,
            false,
        ),
        (
            BERLIN,
            GRACE,
            false,
            26 * hour,
            true,
            CallOutcome::Answered,
            1260,
            true,
        ),
        (
            GRACE,
            GRACE,
            false,
            50 * hour,
            false,
            CallOutcome::Missed,
            0,
            false,
        ),
        (
            KATHERINE,
            ME,
            true,
            74 * hour,
            true,
            CallOutcome::NoAnswer,
            0,
            false,
        ),
        (
            MARGARET,
            MARGARET,
            false,
            122 * hour,
            false,
            CallOutcome::Declined,
            0,
            false,
        ),
    ];
    entries
        .iter()
        .enumerate()
        .map(
            |(i, &(chat, sender, from_me, ago, video, status, duration, group))| {
                call_entry(
                    format!("{}demo-seed-{i}", zapfast::model::CALL_ENTRY_PREFIX),
                    chat,
                    sender,
                    from_me,
                    now - ago,
                    video,
                    status,
                    duration,
                    group,
                )
            },
        )
        .collect()
}

impl State {
    fn new(shared: Arc<Shared>) -> Self {
        let now = now();
        let mut chats = Vec::new();
        let mut messages = HashMap::new();
        let mut contacts = Vec::new();
        for sample in SAMPLES {
            let at = now - sample.minutes_ago * 60;
            let lines: Vec<Message> = sample
                .lines
                .iter()
                .enumerate()
                .map(|(i, (mine, line))| {
                    let ts = at - (sample.lines.len() - 1 - i) as i64 * 60;
                    let sender = if *mine { ME } else { sample.id };
                    text_message(
                        sample.id,
                        format!("{}-{i}", sample.name.replace(' ', "-")),
                        *mine,
                        sender,
                        ts,
                        line,
                    )
                })
                .collect();
            let mut chat = Chat::new(sample.id.to_owned(), sample.name.to_owned());
            chat.group_subject_known = chat.is_group();
            chat.last_activity = at;
            chat.unread = sample.unread;
            chat.pinned = sample.pinned;
            chat.pinned_at = if sample.pinned { at * 1000 } else { 0 };
            chat.muted_until = sample.muted.then_some(0);
            chat.last = lines.last().map(last_of);
            chat.locked = LOCKED.contains(&sample.id);
            // The groups' metadata: we are an admin of Rust Berlin, whose
            // info only admins edit; Family is open to everyone.
            if let Some((admin, locked, about)) = match sample.id {
                "120363012345678901@g.us" => {
                    Some((true, true, "Rust meetups in Berlin, every second Tuesday."))
                }
                "120363098765432109@g.us" => Some((false, false, "")),
                _ => None,
            } {
                chat.participants = std::iter::once(ME)
                    .chain(members_of(sample.id).iter().copied())
                    .map(str::to_owned)
                    .collect();
                chat.admin = admin;
                chat.info_locked = Some(locked);
                chat.group_description = Some(about.into());
            }
            if !chat.is_group() {
                contacts.push(Contact {
                    id: sample.id.to_owned(),
                    full_name: Some(sample.name.to_owned()),
                    first_name: sample.name.split(' ').next().map(str::to_owned),
                    push_name: None,
                });
            }
            chats.push(chat);
            messages.insert(sample.id.to_owned(), lines);
        }
        chats.sort_by_key(|c| std::cmp::Reverse(c.last_activity));
        Self {
            shared,
            chats,
            messages,
            contacts,
            timers: BinaryHeap::new(),
            seq: 0,
            sent: 0,
            call: None,
            call_log: seeded_call_log(now),
            watching: None,
            privacy: vec![
                (PrivacyKind::LastSeen, PrivacyChoice::Contacts),
                (PrivacyKind::Online, PrivacyChoice::SameAsLastSeen),
                (PrivacyKind::Profile, PrivacyChoice::Contacts),
                (PrivacyKind::About, PrivacyChoice::Everyone),
                (PrivacyKind::GroupAdd, PrivacyChoice::Contacts),
                (PrivacyKind::ReadReceipts, PrivacyChoice::Everyone),
                (PrivacyKind::CallAdd, PrivacyChoice::Everyone),
            ],
            cache_cleared: false,
            sent_voice: Vec::new(),
            stickers: StickerShelves::default(),
        }
    }

    /// The demo's media, written once into `media_dir` after the chat list
    /// went out: Ada's shared photos, clip, documents and links (her info
    /// panel), before her lines; a sticker from Katherine and one from
    /// Margaret in Family, a GIF from Katherine, and the stock pack shared
    /// by Grace in Rust Berlin, each before its chat's last line; the
    /// sticker shelves.
    fn add_media(&mut self) {
        const ADA: &str = "393331234567@s.whatsapp.net";
        const GRACE: &str = "441632960123@s.whatsapp.net";
        const KATHERINE: &str = "4915112345678@s.whatsapp.net";
        const MARGARET: &str = "14155550199@s.whatsapp.net";
        const BERLIN: &str = "120363012345678901@g.us";
        const FAMILY: &str = "120363098765432109@g.us";
        let media = |mime: &str, path: Option<PathBuf>, size: [u32; 2]| Media {
            mime: mime.into(),
            size: path
                .as_ref()
                .and_then(|p| std::fs::metadata(p).ok())
                .map_or(0, |m| m.len()),
            width: (size[0] > 0).then_some(size[0]),
            height: (size[1] > 0).then_some(size[1]),
            album: None,
            path,
            state: Default::default(),
        };
        let photo = |name: &str, bytes: &[u8], size, caption: Option<&str>| Content::Image {
            caption: caption.map(str::to_owned),
            media: media("image/jpeg", stock(name, bytes), size),
            motion: None,
        };
        let pdf = tiny_pdf(
            "Notes on the Analytical Engine",
            &[
                "Note G: computing the Bernoulli numbers.",
                "The engine weaves algebraic patterns",
                "as the Jacquard loom weaves flowers and leaves.",
            ],
        );
        let csv = b"n,B(n)\n0,1\n1,-1/2\n2,1/6\n4,-1/30\n6,1/42\n8,-1/30\n10,5/66\n";
        let link = |text: &str, preview: Option<(&str, &str)>| Content::Text {
            text: text.into(),
            preview: preview.map(|(url, title)| zapfast::model::LinkPreview {
                url: url.into(),
                title: Some(title.into()),
                description: None,
            }),
        };
        let ada_media: Vec<(bool, Content)> = vec![
            (
                false,
                photo(
                    "engine.jpg",
                    include_bytes!("../../assets/demo/photos/engine.jpg"),
                    [900, 1200],
                    Some("The Difference Engine at the museum"),
                ),
            ),
            (
                true,
                photo(
                    "dusk.jpg",
                    include_bytes!("../../assets/demo/photos/dusk.jpg"),
                    [1200, 800],
                    None,
                ),
            ),
            (
                false,
                photo(
                    "launch.jpg",
                    include_bytes!("../../assets/demo/photos/launch.jpg"),
                    [1024, 683],
                    Some("Launch day!"),
                ),
            ),
            (
                false,
                Content::Video {
                    caption: Some("The replay".into()),
                    media: media(
                        "video/mp4",
                        stock(
                            "replay.mp4",
                            include_bytes!("../../assets/demo/videos/launch.mp4"),
                        ),
                        [640, 360],
                    ),
                    seconds: Some(4),
                    gif: false,
                    note: false,
                },
            ),
            (
                false,
                Content::Document {
                    media: media(
                        "application/pdf",
                        stock("Notes on the Analytical Engine.pdf", &pdf),
                        [0, 0],
                    ),
                    file_name: "Notes on the Analytical Engine.pdf".into(),
                    caption: Some("My notes, as promised".into()),
                    pages: Some(1),
                },
            ),
            (
                true,
                Content::Document {
                    media: media("text/csv", stock("bernoulli.csv", csv), [0, 0]),
                    file_name: "bernoulli.csv".into(),
                    caption: None,
                    pages: None,
                },
            ),
            (
                false,
                link(
                    "Background reading: https://en.wikipedia.org/wiki/Analytical_engine",
                    Some((
                        "https://en.wikipedia.org/wiki/Analytical_engine",
                        "Analytical engine - Wikipedia",
                    )),
                ),
            ),
            (
                true,
                link(
                    "The museum's page https://www.computerhistory.org/babbage/ and the language I used https://www.rust-lang.org/",
                    None,
                ),
            ),
        ];
        if let Some(lines) = self.messages.get_mut(ADA) {
            let first = lines.first().map_or_else(now, |m| m.timestamp);
            let earlier: Vec<Message> = ada_media
                .into_iter()
                .enumerate()
                .map(|(i, (mine, content))| {
                    // Before her lines, a day apart: the photos and clip
                    // three days back, the documents two, the links
                    // yesterday, each about two hours earlier in the day
                    // than her first line, ten minutes apart.
                    let (days, nth) = match i {
                        0..=3 => (3, i),
                        4 | 5 => (2, i - 4),
                        _ => (1, i - 6),
                    };
                    let at = first - days * 86_400 - 2 * 3600 + nth as i64 * 600;
                    let mut m = text_message(
                        ADA,
                        format!("Ada-media-{i}"),
                        mine,
                        if mine { ME } else { ADA },
                        at,
                        "",
                    );
                    m.thumbnail = poster_of(&content);
                    m.content = content;
                    m
                })
                .collect();
            lines.splice(0..0, earlier);
        }

        self.stickers = StickerShelves::stock();
        let gif_still = GIFS[0].1.to_vec();
        type Line<'a> = (&'a str, &'a str, &'a str, Content, Option<Vec<u8>>);
        let before_last: Vec<Line> = vec![
            (
                KATHERINE,
                KATHERINE,
                "DEMOSTICKER0",
                sticker_file("goat").map_or(Content::text(""), |p| sticker_content(&p)),
                None,
            ),
            (
                KATHERINE,
                KATHERINE,
                "DEMOGIF0",
                Content::Video {
                    caption: None,
                    media: media("video/mp4", stock("gif-launch.mp4", GIF_CLIP), [320, 240]),
                    seconds: Some(4),
                    gif: true,
                    note: false,
                },
                Some(gif_still),
            ),
            (
                FAMILY,
                MARGARET,
                "DEMOSTICKER1",
                sticker_file("raccoon").map_or(Content::text(""), |p| sticker_content(&p)),
                None,
            ),
            (
                BERLIN,
                GRACE,
                "DEMOPACK0",
                Content::StickerPack {
                    name: PACK_NAME.into(),
                    publisher: "Grace Hopper".into(),
                    count: STICKERS.len() as u32,
                    caption: Some("For the meetup".into()),
                },
                None,
            ),
        ];
        // For upstream's bubble pages: Margaret's chat has a poll, a shared
        // contact, a location, a round video, an edited and a forwarded
        // line, and her first line our reaction and hers.
        let (poll, card, place, note, edited, forwarded) = (
            Content::Poll {
                question: "Which mission next?".into(),
                options: vec!["Apollo 11".into(), "Apollo 13".into(), "Skylab".into()],
                state: zapfast::model::PollState {
                    selectable: 1,
                    counts: vec![2, 1, 0],
                    voters: 3,
                    can_vote: true,
                    history_complete: true,
                    ..Default::default()
                },
            },
            Content::Contact {
                display_name: "Dorothy Vaughan".into(),
                vcard: "BEGIN:VCARD\nVERSION:3.0\nFN:Dorothy Vaughan\nTEL;TYPE=CELL;waid=15550003333:+1 555-000-3333\nEND:VCARD".into(),
            },
            Content::Location {
                latitude: 28.5729,
                longitude: -80.649,
                name: Some("Kennedy Space Center".into()),
                address: Some("Merritt Island, FL, USA".into()),
            },
            Content::Video {
                caption: None,
                media: media(
                    "video/mp4",
                    stock(
                        "note.mp4",
                        include_bytes!("../../assets/demo/videos/note.mp4"),
                    ),
                    [360, 360],
                ),
                seconds: Some(4),
                gif: false,
                note: true,
            },
            Content::text("Restart the guidance computer, it clears the alarms."),
            Content::text("Houston, Tranquility Base here. The Eagle has landed."),
        );
        let mut before_last = before_last;
        before_last.push((
            DOROTHY,
            DOROTHY,
            "DEMOLOCKED0",
            photo(
                "launch.jpg",
                include_bytes!("../../assets/demo/photos/launch.jpg"),
                [1024, 683],
                Some("From the launch site"),
            ),
            None,
        ));
        before_last.push((
            DOROTHY,
            DOROTHY,
            "DEMOLOCKED1",
            Content::Video {
                caption: Some("The test firing".into()),
                media: media(
                    "video/mp4",
                    stock(
                        "replay.mp4",
                        include_bytes!("../../assets/demo/videos/launch.mp4"),
                    ),
                    [640, 360],
                ),
                seconds: Some(4),
                gif: false,
                note: false,
            },
            None,
        ));
        for (id, content) in [
            ("DEMOPOLL0", poll),
            ("DEMOCONTACT0", card),
            ("DEMOLOCATION0", place),
            ("DEMONOTE0", note),
            ("DEMOEDITED0", edited),
            ("DEMOFORWARDED0", forwarded),
        ] {
            before_last.push((MARGARET, MARGARET, id, content, None));
        }
        if let Some(lines) = self.messages.get_mut("120363012345678901@g.us")
            && lines.len() > 1
        {
            let at = (lines[0].timestamp + lines[1].timestamp) / 2;
            let message = text_message(
                "120363012345678901@g.us",
                "DEMORECEIPTS0".into(),
                true,
                ME,
                at,
                "Who's bringing the projector?",
            );
            lines.insert(1, message);
        }
        for (chat, sender, id, content, thumbnail) in before_last {
            let Some(lines) = self.messages.get_mut(chat) else {
                continue;
            };
            let at = lines.len().saturating_sub(1);
            let timestamp = lines.get(at).map_or_else(now, |m| m.timestamp - 20);
            let mut message = text_message(chat, id.into(), false, sender, timestamp, "");
            message.thumbnail = thumbnail.or_else(|| poster_of(&content));
            message.content = content;
            message.edited = id == "DEMOEDITED0";
            message.forwarded = id == "DEMOFORWARDED0";
            lines.insert(at, message);
        }
        if let Some(first) = self
            .messages
            .get_mut(MARGARET)
            .and_then(|lines| lines.first_mut())
        {
            first.reactions = vec![
                zapfast::model::Reaction {
                    sender: ME.into(),
                    from_me: true,
                    emoji: "❤️".into(),
                },
                zapfast::model::Reaction {
                    sender: MARGARET.into(),
                    from_me: false,
                    emoji: "🚀".into(),
                },
            ];
        }
    }

    /// Files the demo's voice notes, before each chat's last line so the
    /// chat list does not change: one from Ada, one from Grace in Rust
    /// Berlin, and one of ours to Grace, already played.
    fn add_voice_notes(&mut self) {
        const NOTES: [(&str, &str, bool, f32, f32); 4] = [
            (
                "393331234567@s.whatsapp.net",
                "393331234567@s.whatsapp.net",
                false,
                4.2,
                210.0,
            ),
            (
                "120363012345678901@g.us",
                "441632960123@s.whatsapp.net",
                false,
                6.5,
                175.0,
            ),
            ("441632960123@s.whatsapp.net", ME, true, 2.8, 125.0),
            (DOROTHY, DOROTHY, false, 5.0, 195.0),
        ];
        for (i, (chat, sender, mine, seconds, pitch)) in NOTES.into_iter().enumerate() {
            let samples = speech(seconds, pitch);
            let content = voice_note(&format!("note-{i}-v1.ogg"), &samples, false);
            let Some(lines) = self.messages.get_mut(chat) else {
                continue;
            };
            let at = lines.len().saturating_sub(1);
            let timestamp = lines.get(at).map_or_else(now, |m| m.timestamp - 30);
            let mut message =
                text_message(chat, format!("DEMOVOICE{i}"), mine, sender, timestamp, "");
            message.content = content;
            if mine {
                message.status = Delivery::Played;
            }
            lines.insert(at, message);
        }
    }

    fn run(mut self, rx: mpsc::Receiver<Input>) {
        // What open has without the network: the archive's identity and
        // chat list.
        self.shared.emit(Event::Me {
            id: ME.into(),
            lid: None,
            name: Some("Demo".into()),
            about: None,
        });
        self.shared.emit(Event::Chats(self.chats.clone()));
        // After the chat list, so encoding them never delays it.
        self.add_voice_notes();
        self.add_media();
        loop {
            let input = match self.timers.peek() {
                None => rx.recv().ok(),
                Some(next) => {
                    match rx.recv_timeout(next.at.saturating_duration_since(Instant::now())) {
                        Ok(input) => Some(input),
                        Err(mpsc::RecvTimeoutError::Timeout) => {
                            let due = self.timers.pop().expect("peeked").due;
                            self.fire(due);
                            continue;
                        }
                        Err(mpsc::RecvTimeoutError::Disconnected) => None,
                    }
                }
            };
            match input {
                None | Some(Input::Stop) => {
                    for path in &self.sent_voice {
                        let _ = std::fs::remove_file(path);
                    }
                    return;
                }
                Some(Input::Start) => {
                    self.shared.emit(Event::Link(LinkStatus::Connecting));
                    self.shared.emit(Event::Link(LinkStatus::Connected));
                    self.shared.emit(Event::Contacts(self.contacts.clone()));
                }
                Some(Input::Command(command)) => self.command(*command),
                Some(Input::Inject(Injected::Panic)) => panic!("DemoPanic on the demo thread"),
                Some(Input::Inject(injected)) => {
                    let delay = match &injected {
                        Injected::Incoming { delay_ms, .. } | Injected::Typing { delay_ms, .. } => {
                            *delay_ms
                        }
                        Injected::Panic => 0,
                    };
                    self.later(Duration::from_millis(delay.into()), Due::Inject(injected));
                }
            }
        }
    }

    fn later(&mut self, after: Duration, due: Due) {
        self.seq += 1;
        self.timers.push(Timer {
            at: Instant::now() + after,
            seq: self.seq,
            due,
        });
    }

    fn chat_mut(&mut self, id: &str) -> Option<&mut Chat> {
        self.chats.iter_mut().find(|c| c.id == id)
    }

    fn emit_chat(&self, id: &str) {
        if let Some(chat) = self.chats.iter().find(|c| c.id == id) {
            self.shared.emit(Event::ChatUpdated(Box::new(chat.clone())));
        }
    }

    /// Files a message as the worker does: the row, then its chat.
    fn file(&mut self, message: Message, incoming: bool) {
        let chat_id = message.chat.clone();
        if let Some(chat) = self.chat_mut(&chat_id) {
            chat.last_activity = message.timestamp;
            chat.last = Some(last_of(&message));
            if incoming {
                chat.unread += 1;
            }
        }
        self.messages
            .entry(chat_id.clone())
            .or_default()
            .push(message.clone());
        self.chats
            .sort_by_key(|c| std::cmp::Reverse(c.last_activity));
        self.shared.emit(Event::Messages {
            chat: chat_id.clone(),
            messages: vec![message.clone()],
            older: false,
            complete: false,
        });
        self.emit_chat(&chat_id);
        if incoming {
            self.shared.emit(Event::Incoming {
                chat: chat_id,
                message: Box::new(message),
            });
        }
    }

    /// The quote a reply carries, resolved from the demo's messages as
    /// upstream's demo does (`src/demo/tour/session.rs`, `quote`): from the
    /// target's own chat for a cross-chat reply. A target the demo does not
    /// have sends the message without a quote.
    fn quote(&self, chat: &str, target: Option<ReplyTarget>) -> Option<Quoted> {
        let target = target?;
        let row = self
            .messages
            .get(target.source_chat(chat))?
            .iter()
            .find(|m| m.id == target.id)?;
        Some(Quoted {
            chat: target.chat,
            sender: row.sender.clone(),
            sender_name: row.sender_name.clone(),
            summary: row.summary(),
            mentions: row.mentions.clone(),
            id: target.id,
        })
    }

    fn send_own(&mut self, chat: String, content: Content, quoting: Option<ReplyTarget>) {
        self.send_mentioning(chat, content, quoting, Vec::new());
    }

    fn send_mentioning(
        &mut self,
        chat: String,
        content: Content,
        quoting: Option<ReplyTarget>,
        mentions: Vec<String>,
    ) {
        self.send_full(chat, content, quoting, mentions, None);
    }

    /// Files our message as pending, then moves it through sent, delivered
    /// and read, as the worker does.
    fn send_full(
        &mut self,
        chat: String,
        content: Content,
        quoting: Option<ReplyTarget>,
        mentions: Vec<String>,
        thumbnail: Option<Vec<u8>>,
    ) {
        if self.chats.iter().all(|c| c.id != chat) {
            self.shared
                .emit(Event::Error("Demo: there is no such chat.".into()));
            return;
        }
        self.sent += 1;
        let id = format!("DEMO{:08}", self.sent);
        let mut message = text_message(&chat, id.clone(), true, ME, now(), "");
        message.content = content;
        message.status = Delivery::Pending;
        message.quoted = self.quote(&chat, quoting);
        message.mentions = mentions
            .into_iter()
            .map(|id| MentionRef {
                user: id.split('@').next().unwrap_or_default().to_owned(),
                id,
            })
            .collect();
        message.thumbnail = thumbnail;
        self.file(message, false);
        for (ms, status) in [
            (20, Delivery::Sent),
            (60, Delivery::Delivered),
            (150, Delivery::Read),
        ] {
            self.later(
                Duration::from_millis(ms),
                Due::Status {
                    chat: chat.clone(),
                    id: id.clone(),
                    status,
                },
            );
        }
    }

    fn command(&mut self, command: Command) {
        match command {
            Command::LoadChat { chat, before } => {
                let messages = if before.is_some() {
                    Vec::new()
                } else {
                    self.messages.get(&chat).cloned().unwrap_or_default()
                };
                self.shared.emit(Event::Messages {
                    chat,
                    messages,
                    older: before.is_some(),
                    complete: true,
                });
            }
            Command::SendText {
                chat,
                text,
                quoting,
                mentions,
            } => self.send_mentioning(chat, Content::text(text), quoting, mentions),
            Command::SendLocation {
                chat,
                latitude,
                longitude,
                quoting,
            } => self.send_own(
                chat,
                Content::Location {
                    latitude,
                    longitude,
                    name: None,
                    address: None,
                },
                quoting,
            ),
            Command::SendImage {
                chat,
                width,
                height,
                rgba,
                caption,
                quoting,
                mentions,
            } => self.send_mentioning(
                chat,
                Content::Image {
                    caption,
                    media: Media {
                        mime: "image/png".into(),
                        size: rgba.len() as u64,
                        width: Some(width),
                        height: Some(height),
                        album: None,
                        path: None,
                        state: Default::default(),
                    },
                    motion: None,
                },
                quoting,
                mentions,
            ),
            Command::SendVoice {
                chat,
                mut samples,
                quoting,
            } => {
                // As the worker does: normalized, measured, encoded, and
                // filed where it can be played back.
                zapfast::voice::normalize(&mut samples);
                static SENT: AtomicU64 = AtomicU64::new(0);
                let n = SENT.fetch_add(1, Ordering::Relaxed) + 1;
                let name = format!("sent-{}-{n}.ogg", std::process::id());
                let content = voice_note(&name, &samples, true);
                if let Content::Audio {
                    media:
                        Media {
                            path: Some(path), ..
                        },
                    ..
                } = &content
                {
                    self.sent_voice.push(path.clone());
                }
                self.send_own(chat, content, quoting)
            }
            // As the headless worker does: the host opens the file.
            Command::OpenLog(path) => self.shared.emit(Event::OpenPath(path)),
            Command::Reconnect => {
                self.shared.emit(Event::Link(LinkStatus::Connecting));
                self.shared.emit(Event::Link(LinkStatus::Connected));
            }
            Command::MarkRead { chat, .. } => {
                if let Some(c) = self.chat_mut(&chat) {
                    c.unread = 0;
                    c.marked_unread = false;
                }
                self.emit_chat(&chat);
            }
            Command::SetMuted(chat, until) => {
                if let Some(c) = self.chat_mut(&chat) {
                    c.muted_until = until;
                }
                self.emit_chat(&chat);
            }
            Command::WatchReceipts(target) => {
                self.watching = target;
                self.emit_receipts();
            }
            Command::FetchAvatar { id, full } => {
                let path = avatar_path(&id);
                self.shared.emit(Event::Avatar { id, full, path });
            }
            Command::ListChatShared { chat, request } => {
                let result = match self.messages.get(&chat) {
                    Some(messages) => {
                        let mut shared = ChatMedia::collect(chat.clone(), messages.clone(), 200);
                        // A chat with no link of its own lists two, on its
                        // first line.
                        if shared.links.is_empty()
                            && let Some(first) = messages.first()
                        {
                            for (url, title) in [
                                ("https://www.rust-lang.org/", "Rust Programming Language"),
                                ("https://github.com/crmne/zapfast", "ZapFast"),
                            ] {
                                shared.links.push(ChatLink {
                                    message: first.id.clone(),
                                    timestamp: first.timestamp,
                                    url: url.into(),
                                    title: Some(title.into()),
                                });
                            }
                        }
                        Ok(shared)
                    }
                    None => Err("Demo: there is no such chat.".to_owned()),
                };
                self.shared.emit(Event::ChatShared { request, result });
            }
            Command::LoadStorageStats { token } => self.shared.emit(Event::StorageStats {
                stats: Some(self.storage()),
                token,
                cleared: false,
            }),
            Command::ClearMediaCache { token } => {
                self.cache_cleared = true;
                self.shared.emit(Event::StorageStats {
                    stats: Some(self.storage()),
                    token,
                    cleared: true,
                });
            }
            Command::FetchAccountPrivacy => self.shared.emit(Event::AccountPrivacy {
                values: self.privacy.clone(),
                failed: false,
            }),
            Command::SetAccountPrivacy { kind, choice } => {
                match self.privacy.iter_mut().find(|(k, _)| *k == kind) {
                    Some(value) => value.1 = choice,
                    None => self.privacy.push((kind, choice)),
                }
                self.shared.emit(Event::AccountPrivacySaved { kind });
            }
            Command::StartCall {
                chat,
                video,
                generation,
                cancel,
            } => self.place(chat, video, generation, cancel, false),
            Command::JoinCallLink {
                video,
                generation,
                cancel,
                ..
            } => self.place(String::new(), video, generation, cancel, true),
            Command::AnswerCall => {
                let generation = match self.call.as_mut() {
                    Some(call) if call.update.phase == CallPhase::Incoming => {
                        call.update.phase = CallPhase::Accepted;
                        call.update.camera_on = call.update.video;
                        call.update.generation
                    }
                    _ => return,
                };
                self.shared.emit(Event::CallDevices(Box::new(devices())));
                self.emit_call();
                self.later(
                    CONNECTING,
                    Due::Call {
                        generation,
                        step: Step::Connect,
                    },
                );
            }
            Command::DeclineCall => {
                if let Some(call) = self.call.as_mut()
                    && call.update.phase == CallPhase::Incoming
                {
                    call.update.phase = CallPhase::Ended;
                    call.update.outcome = Some(CallOutcome::Declined);
                    self.emit_call();
                }
            }
            Command::HangupCall => {
                if let Some(call) = self.call.as_mut()
                    && call.update.phase.is_live()
                {
                    let update = &mut call.update;
                    update.outcome = Some(if update.started.is_some() {
                        CallOutcome::Answered
                    } else if update.direction == CallDirection::Incoming {
                        CallOutcome::Missed
                    } else {
                        CallOutcome::NoAnswer
                    });
                    update.phase = CallPhase::Ended;
                    self.emit_call();
                }
            }
            Command::SetCallMuted(muted) => self.change_call(|u| u.muted = muted),
            Command::SetCallCamera(on) => self.change_call(|u| {
                u.camera_on = on;
                // Our camera turns a voice call into a video call, and the
                // demo's peer answers with theirs.
                u.video |= on;
            }),
            Command::SetCallMicrophone(device) => self.change_call(|u| u.microphone = device),
            Command::SetCallSpeaker(device) => self.change_call(|u| u.speaker = device),
            Command::SetCallCameraDevice(device) => self.change_call(|u| u.camera = device),
            Command::RefreshCallDevices => {
                self.shared.emit(Event::CallDevices(Box::new(devices())));
            }
            Command::LoadCallLog => self.shared.emit(Event::CallLog(self.call_log.clone())),
            Command::SearchGifs { query, key } => {
                let results = if key == BAD_GIF_KEY {
                    Err(zapfast::model::GifError {
                        message: "GIPHY rejected the API key (error 401).".into(),
                        bad_key: true,
                    })
                } else {
                    let words = query.trim().to_lowercase();
                    let dir = media_dir().join("gifs");
                    Ok(GIFS
                        .iter()
                        .filter(|(name, _, tags)| {
                            words.is_empty()
                                || name.contains(&words)
                                || tags.iter().any(|t| t.contains(&words) || words.contains(t))
                        })
                        .map(|(name, still, _)| zapfast::model::Gif {
                            id: format!("demo-gif-{name}"),
                            still: write_once(&dir, &format!("{name}.jpg"), still),
                            mp4: String::new(),
                            width: 320,
                            height: 240,
                        })
                        .collect())
                };
                self.shared.emit(Event::Gifs { query, results });
            }
            Command::SendGif { chat, gif, quoting } => {
                // The still the result showed, over upstream's stock clip.
                let still = GIFS
                    .iter()
                    .find(|(name, ..)| gif.id == format!("demo-gif-{name}"))
                    .map(|(_, still, _)| still.to_vec());
                let path = stock("gif-launch.mp4", GIF_CLIP);
                let content = Content::Video {
                    caption: None,
                    media: Media {
                        mime: "video/mp4".into(),
                        size: GIF_CLIP.len() as u64,
                        width: Some(gif.width),
                        height: Some(gif.height),
                        album: None,
                        path,
                        state: Default::default(),
                    },
                    seconds: Some(4),
                    gif: true,
                    note: false,
                };
                self.send_full(chat, content, quoting, Vec::new(), still);
            }
            // The whole chat's photos and clips, oldest first: the demo's
            // chats are short enough to be one page around any of them.
            Command::LoadChatMedia { chat, around } => {
                let items = self
                    .messages
                    .get(&chat)
                    .into_iter()
                    .flatten()
                    .flat_map(|m| {
                        zapfast::model::MediaItem::of_content(
                            &m.id,
                            m.timestamp,
                            &m.content,
                            m.thumbnail.as_deref(),
                            std::path::Path::exists,
                        )
                    })
                    .collect();
                self.shared.emit(Event::ChatMedia {
                    chat,
                    around,
                    items,
                });
            }
            // As upstream's demo counts a vote: ours moves from the old
            // choices to the new ones.
            Command::VotePoll {
                chat,
                message,
                choices,
            } => {
                let row = self
                    .messages
                    .get_mut(&chat)
                    .and_then(|ms| ms.iter_mut().find(|m| m.id == message));
                if let Some(row) = row
                    && let Content::Poll { state, .. } = &mut row.content
                {
                    for &old in &state.selected {
                        if let Some(count) = state.counts.get_mut(old) {
                            *count = count.saturating_sub(1);
                        }
                    }
                    if !state.selected.is_empty() {
                        state.voters = state.voters.saturating_sub(1);
                    }
                    for &new in &choices {
                        if let Some(count) = state.counts.get_mut(new) {
                            *count += 1;
                        }
                    }
                    if !choices.is_empty() {
                        state.voters += 1;
                    }
                    state.selected = choices;
                    let updated = row.clone();
                    self.shared.emit(Event::MessageUpdated(Box::new(updated)));
                    self.shared.emit(Event::PollVoted {
                        chat,
                        message,
                        error: None,
                    });
                } else {
                    self.shared.emit(Event::PollVoted {
                        chat,
                        message,
                        error: Some("Demo: that message is not a poll.".into()),
                    });
                }
            }
            Command::RecentStickers => self.shared.emit(self.stickers.event()),
            Command::SendSticker {
                chat,
                path,
                quoting,
            } => {
                if !path.is_file() {
                    self.shared
                        .emit(Event::Error("Demo: there is no such sticker.".into()));
                    return;
                }
                self.send_own(chat, sticker_content(&path), quoting);
                self.stickers.used(&path);
                self.shared.emit(self.stickers.event());
            }
            Command::RemoveRecentSticker { path } => {
                self.stickers.recent.retain(|p| *p != path);
                self.shared.emit(self.stickers.event());
            }
            Command::SaveSticker { path } => {
                if !self.stickers.favorites.contains(&path) {
                    self.stickers.favorites.insert(0, path.clone());
                }
                self.stickers.received.retain(|p| *p != path);
                self.shared.emit(self.stickers.event());
            }
            Command::ForgetSticker { path } => {
                self.stickers.favorites.retain(|p| *p != path);
                self.shared.emit(self.stickers.event());
            }
            Command::SendStickerPack { chat, dir } => {
                if let Some(pack) = self.stickers.packs.iter().find(|p| p.dir == dir) {
                    let content = Content::StickerPack {
                        name: pack.name.clone(),
                        publisher: "Demo".into(),
                        count: pack.stickers.len() as u32,
                        caption: None,
                    };
                    self.send_own(chat, content, None);
                }
            }
            Command::ViewStickerPack { chat, message } => {
                let shared = self
                    .messages
                    .get(&chat)
                    .and_then(|ms| ms.iter().find(|m| m.id == message));
                let result = match shared.map(|m| &m.content) {
                    Some(Content::StickerPack {
                        name, publisher, ..
                    }) => self
                        .stickers
                        .packs
                        .iter()
                        .find(|p| p.name == *name)
                        .map(|pack| (pack.clone(), publisher.clone()))
                        .ok_or_else(|| "Demo: the pack's stickers are not here.".to_owned()),
                    _ => Err("Demo: that message is not a sticker pack.".to_owned()),
                };
                self.shared.emit(Event::StickerPackPreview(result));
            }
            Command::AddStickerPack { dir, name } => {
                if let Some(pack) = self.stickers.packs.iter_mut().find(|p| p.dir == dir) {
                    pack.name.clone_from(&name);
                } else {
                    self.stickers.packs.push(zapfast::model::StickerPack {
                        name: name.clone(),
                        stickers: std::fs::read_dir(&dir)
                            .map(|entries| {
                                let mut files: Vec<PathBuf> = entries
                                    .filter_map(|e| Some(e.ok()?.path()))
                                    .filter(|p| p.extension().is_some_and(|x| x == "webp"))
                                    .collect();
                                files.sort();
                                files
                            })
                            .unwrap_or_default(),
                        dir,
                        local: false,
                    });
                }
                self.shared
                    .emit(Event::Info(format!("Added sticker pack \"{name}\"")));
                self.shared.emit(self.stickers.event());
            }
            // As the worker files it: the chat, then its update.
            Command::SetLocked(chat, locked) => {
                if let Some(c) = self.chat_mut(&chat) {
                    c.locked = locked;
                }
                self.emit_chat(&chat);
            }
            Command::EnsureChat { chat, name } => {
                if self.chats.iter().all(|c| c.id != chat) {
                    let mut new = Chat::new(chat.clone(), name);
                    new.last_activity = now();
                    self.chats.insert(0, new);
                    self.messages.entry(chat.clone()).or_default();
                    self.emit_chat(&chat);
                }
            }
            // Accepted with no reply, as the real core gives none either
            // (see `disposition`); a command the demo does not answer never
            // gets this far, because `Core::send` refuses it first.
            _ => {}
        }
    }

    fn storage(&self) -> StorageStats {
        if self.cache_cleared {
            return StorageStats::default();
        }
        StorageStats {
            images: 128,
            image_bytes: 48_300_000,
            videos: 12,
            video_bytes: 211_000_000,
            stickers: 64,
            sticker_bytes: 2_900_000,
            other: 9,
            other_bytes: 15_400_000,
        }
    }

    /// The followed message's receipts: who it went to, and when it was
    /// delivered and read as far as its status says.
    fn emit_receipts(&self) {
        let Some((chat, id)) = &self.watching else {
            return;
        };
        let Some(message) = self
            .messages
            .get(chat)
            .and_then(|ms| ms.iter().find(|m| &m.id == id))
        else {
            return;
        };
        let people: Vec<&str> = if !message.from_me {
            Vec::new()
        } else if chat.ends_with("@g.us") {
            members_of(chat).to_vec()
        } else {
            vec![chat.as_str()]
        };
        let delivered = matches!(
            message.status,
            Delivery::Delivered | Delivery::Read | Delivery::Played
        )
        .then(|| message.delivered_at.unwrap_or(message.timestamp + 2));
        let read = matches!(message.status, Delivery::Read | Delivery::Played)
            .then(|| message.read_at.unwrap_or(message.timestamp + 40));
        let played = (message.status == Delivery::Played).then(|| message.timestamp + 90);
        let mut recipients: Vec<Recipient> = people
            .into_iter()
            .map(|person| Recipient {
                id: person.into(),
                expected: true,
                delivered_at: delivered,
                read_at: read,
                played_at: played,
            })
            .collect();
        // Upstream's message info has three partial paths (src/ui/
        // message_info.rs `group`): no receipts at all, receipts with no
        // saved audience (its "not recorded" note), and a saved audience
        // with a recipient outside it.
        let who = |r: &Recipient| match r.id.as_str() {
            "393331234567@s.whatsapp.net" => 'a',
            "441632960123@s.whatsapp.net" => 'g',
            "4915112345678@s.whatsapp.net" => 'k',
            _ => '?',
        };
        match (chat.as_str(), id.as_str()) {
            // A saved audience with one outside it: Ada read it, Grace has
            // it, and Katherine joined after it was sent, so she was never
            // expected and is in no section.
            ("120363012345678901@g.us", "Rust-Berlin-1") => {
                for r in &mut recipients {
                    match who(r) {
                        'g' => r.read_at = None,
                        'k' => {
                            (r.expected, r.delivered_at, r.read_at, r.played_at) =
                                (false, None, None, None)
                        }
                        _ => {}
                    }
                }
            }
            // Sent while only some receipts were kept: no saved audience,
            // Ada's read and Grace's delivery came in, Katherine's not.
            ("120363012345678901@g.us", "DEMORECEIPTS0") => {
                for r in &mut recipients {
                    r.expected = false;
                    match who(r) {
                        'g' => r.read_at = None,
                        'k' => (r.delivered_at, r.read_at) = (None, None),
                        _ => {}
                    }
                }
            }
            // Sent before receipts were recorded: none are known.
            ("120363098765432109@g.us", "Family-1") => recipients.clear(),
            _ => {}
        }
        self.shared.emit(Event::Receipts(MessageReceipts {
            chat: chat.clone(),
            message: id.clone(),
            recipients,
        }));
    }

    /// Publishes the call's state; a call that has ended is filed and let go.
    fn emit_call(&mut self) {
        let Some(call) = &self.call else {
            return;
        };
        self.shared.emit(Event::Call(Box::new(call.update.clone())));
        if !call.update.phase.is_live() {
            let call = self.call.take().expect("checked above");
            self.file_call(&call);
        }
    }

    /// Changes the live call as `change` says and publishes it.
    fn change_call(&mut self, change: impl FnOnce(&mut CallUpdate)) {
        if let Some(call) = self.call.as_mut()
            && call.update.phase.is_live()
        {
            change(&mut call.update);
            self.emit_call();
        }
    }

    /// Places a call, or joins a call link, under the UI's `generation`.
    fn place(
        &mut self,
        chat: String,
        video: bool,
        generation: u64,
        cancel: Arc<AtomicBool>,
        link: bool,
    ) {
        if self.call.is_some() {
            // One call at a time: this one is refused as the worker refuses
            // it, and the call that is up goes on.
            let refused = CallUpdate::placing(generation, chat, video).not_placed();
            self.shared.emit(Event::Call(Box::new(refused)));
            return;
        }
        let mut update = CallUpdate::placing(generation, chat.clone(), video);
        update.camera_on = video;
        let person = |id: &&str| Participant {
            id: (*id).into(),
            joined: false,
            video: false,
        };
        update.group = if link {
            Some(GroupCall {
                caller: None,
                participants: LINK_MEMBERS.iter().map(person).collect(),
                link: true,
            })
        } else if chat.ends_with("@g.us") {
            Some(GroupCall {
                caller: None,
                participants: members_of(&chat).iter().map(person).collect(),
                link: false,
            })
        } else {
            None
        };
        self.call = Some(LiveCall {
            update,
            cancel: Some(cancel),
            caller: None,
            began: now(),
            frame: 0,
            file: true,
        });
        self.shared.emit(Event::CallDevices(Box::new(devices())));
        self.emit_call();
        self.later(
            PLACING,
            Due::Call {
                generation,
                step: Step::Offer,
            },
        );
    }

    /// Rings a call from `caller` in `chat`, numbered as the worker numbers
    /// the calls it is offered. Busy with another call, it is missed.
    fn ring(&mut self, chat: String, caller: String, name: Option<String>, video: bool) {
        if self.chats.iter().all(|c| c.id != chat) {
            self.chats.push(Chat::new(
                chat.clone(),
                name.unwrap_or_else(|| caller.clone()),
            ));
        }
        let group = chat.ends_with("@g.us");
        let generation = zapfast::calls::next_generation();
        if self.call.is_some() {
            let message = call_entry(
                format!(
                    "{}demo-busy-{generation}",
                    zapfast::model::CALL_ENTRY_PREFIX
                ),
                &chat,
                &caller,
                false,
                now(),
                video,
                CallOutcome::Missed,
                0,
                group,
            );
            self.call_log.insert(0, message.clone());
            self.file(message, false);
            return;
        }
        let mut update = CallUpdate::placing(generation, chat.clone(), video);
        update.direction = CallDirection::Incoming;
        update.phase = CallPhase::Incoming;
        update.group = group.then(|| {
            let mut ids: Vec<&str> = members_of(&chat).to_vec();
            if !ids.contains(&caller.as_str()) {
                ids.insert(0, &caller);
            }
            GroupCall {
                caller: Some(caller.clone()),
                participants: ids
                    .into_iter()
                    .map(|id| Participant {
                        joined: id == caller,
                        id: id.into(),
                        video: false,
                    })
                    .collect(),
                link: false,
            }
        });
        self.call = Some(LiveCall {
            update,
            cancel: None,
            caller: Some(caller),
            began: now(),
            frame: 0,
            file: true,
        });
        self.emit_call();
        self.later(
            MISSED_AFTER,
            Due::Call {
                generation,
                step: Step::Missed,
            },
        );
    }

    /// One step of call `generation`, when it is still the call.
    fn call_step(&mut self, generation: u64, step: Step) {
        let Some(call) = self
            .call
            .as_mut()
            .filter(|call| call.update.generation == generation)
        else {
            return;
        };
        if matches!(step, Step::Frame) {
            self.frame_tick(generation);
            return;
        }
        if matches!(step, Step::Offer)
            && call
                .cancel
                .as_ref()
                .is_some_and(|c| c.load(Ordering::SeqCst))
        {
            // Cancelled before the offer went out: never placed.
            call.update = call.update.clone().not_placed();
            call.file = false;
            self.emit_call();
            return;
        }
        let update = &mut call.update;
        let next = match step {
            Step::Offer => {
                if update.group.as_ref().is_some_and(|g| g.link) {
                    update.phase = CallPhase::Connecting;
                    Some((CONNECTING, Step::Connect))
                } else {
                    update.phase = CallPhase::Dialing;
                    Some((DIALING, Step::Ring))
                }
            }
            Step::Ring if update.phase == CallPhase::Dialing => {
                update.phase = CallPhase::Ringing;
                Some((RINGING, Step::Answer))
            }
            Step::Answer if update.phase == CallPhase::Ringing => {
                update.phase = CallPhase::Connecting;
                Some((CONNECTING, Step::Connect))
            }
            Step::Connect
                if matches!(update.phase, CallPhase::Connecting | CallPhase::Accepted) =>
            {
                update.phase = CallPhase::Active;
                update.started = Some(Instant::now());
                if let Some(group) = update.group.as_mut() {
                    for person in &mut group.participants {
                        person.joined = true;
                    }
                }
                Some((FRAME_EVERY, Step::Frame))
            }
            Step::Missed if update.phase == CallPhase::Incoming => {
                update.phase = CallPhase::Ended;
                update.outcome = Some(CallOutcome::Missed);
                None
            }
            _ => return,
        };
        self.emit_call();
        if let Some((after, step)) = next {
            self.later(after, Due::Call { generation, step });
        }
    }

    /// The next frame of every stream that has a picture: ours while our
    /// camera is on, the peer's on a video call, and on a group's video call
    /// the first two members'. A picture's first frame turns its flag on, as
    /// the worker's does.
    fn frame_tick(&mut self, generation: u64) {
        let Some(call) = self.call.as_mut() else {
            return;
        };
        if call.update.phase != CallPhase::Active {
            return;
        }
        call.frame += 1;
        let n = call.frame;
        let update = &mut call.update;
        let local = update
            .camera_on
            .then(|| Arc::new(test_pattern([320, 180], 0, n)));
        let mut first = false;
        let mut remote = None;
        let mut members = Vec::new();
        match update.group.as_mut() {
            None if update.video => {
                remote = Some(Arc::new(test_pattern([640, 360], 1, n)));
                first |= !std::mem::replace(&mut update.remote_video, true);
            }
            None => {}
            Some(group) if update.video => {
                for (i, person) in group
                    .participants
                    .iter_mut()
                    .filter(|person| person.joined)
                    .take(2)
                    .enumerate()
                {
                    first |= !std::mem::replace(&mut person.video, true);
                    members.push((
                        person.id.clone(),
                        Arc::new(test_pattern([320, 180], 2 + i, n)),
                    ));
                }
            }
            Some(_) => {}
        }
        if local.is_some() || remote.is_some() {
            self.shared.emit(Event::CallVideo {
                local,
                remote,
                from: None,
            });
        }
        for (id, picture) in members {
            self.shared.emit(Event::CallVideo {
                local: None,
                remote: Some(picture),
                from: Some(id),
            });
        }
        if first {
            self.emit_call();
        }
        self.later(
            FRAME_EVERY,
            Due::Call {
                generation,
                step: Step::Frame,
            },
        );
    }

    /// Files a finished call in its chat and the call log, as the worker
    /// does (`file_finished_call`).
    fn file_call(&mut self, call: &LiveCall) {
        let Some(status) = call.update.outcome.filter(|_| call.file) else {
            return;
        };
        let update = &call.update;
        let link = update.group.as_ref().is_some_and(|g| g.link);
        let chat = if link {
            zapfast::model::CALL_LINK_CHAT.to_owned()
        } else {
            update.chat.clone()
        };
        let from_me = update.direction == CallDirection::Outgoing;
        let sender = if from_me {
            ME.to_owned()
        } else {
            call.caller.clone().unwrap_or_else(|| chat.clone())
        };
        let message = call_entry(
            format!(
                "{}demo-{}",
                zapfast::model::CALL_ENTRY_PREFIX,
                update.generation
            ),
            &chat,
            &sender,
            from_me,
            call.began,
            update.video,
            status,
            update.started.map_or(0, |at| at.elapsed().as_secs()),
            update.group.is_some(),
        );
        self.call_log.insert(0, message.clone());
        if link {
            self.shared.emit(Event::CallLog(self.call_log.clone()));
        } else {
            self.file(message, false);
        }
    }

    fn fire(&mut self, due: Due) {
        match due {
            Due::Call { generation, step } => self.call_step(generation, step),
            Due::Inject(Injected::Incoming {
                chat,
                sender,
                sender_name,
                text,
                ..
            }) if text == CALL_TEXT || text == VIDEO_CALL_TEXT => {
                self.ring(chat, sender, sender_name, text == VIDEO_CALL_TEXT)
            }
            Due::Status { chat, id, status } => {
                let Some(message) = self
                    .messages
                    .get_mut(&chat)
                    .and_then(|ms| ms.iter_mut().find(|m| m.id == id))
                else {
                    return;
                };
                message.status = status;
                let at = now();
                match status {
                    Delivery::Delivered => message.delivered_at = Some(at),
                    Delivery::Read => message.read_at = Some(at),
                    _ => {}
                }
                let message = message.clone();
                if let Some(c) = self.chat_mut(&chat)
                    && let Some(last) = &mut c.last
                    && last.from_me
                {
                    last.status = status;
                }
                self.shared.emit(Event::MessageUpdated(Box::new(message)));
                self.emit_chat(&chat);
                if self.watching.as_ref() == Some(&(chat, id)) {
                    self.emit_receipts();
                }
            }
            Due::Inject(Injected::Incoming {
                chat,
                sender,
                sender_name,
                text,
                ..
            }) => {
                if self.chats.iter().all(|c| c.id != chat) {
                    let name = sender_name.clone().unwrap_or_else(|| sender.clone());
                    self.chats.push(Chat::new(chat.clone(), name));
                }
                self.seq += 1;
                let mut message = text_message(
                    &chat,
                    format!("DEMOIN{:08}", self.seq),
                    false,
                    &sender,
                    now(),
                    &text,
                );
                message.sender_name = sender_name;
                self.file(message, true);
            }
            Due::Inject(Injected::Typing {
                chat,
                sender,
                composing,
                ..
            }) => self.shared.emit(Event::Typing {
                chat,
                sender,
                composing,
            }),
            Due::Inject(Injected::Panic) => {}
        }
    }
}
