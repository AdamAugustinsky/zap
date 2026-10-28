/*
 * zfcore.h: ZapFast's core behind a C ABI (libzapfast_core.dylib), for the
 * Go UI. Contract: docs/GNUI-MIGRATION-PLAN.md, "The core ABI (D49)".
 *
 * Records. Every command and event is a little-endian record generated from
 * zfcore/abi/zf.schema (Rust: zfcore/src/abi_gen.rs; Go: gnui/abi): a u16
 * variant tag, a u16 that is zero, then the variant's fields. No record is
 * longer than ZF_MAX_RECORD; the core refuses to build one that would be,
 * and queues a small EventDropped instead. Long lists (the chat list,
 * history) arrive in chunks sharing a batch id, the last one flagged.
 * zf_core_open's configuration is a bare OpenConfig record.
 *
 * Errors. Every fallible call returns ZF_OK (or ZF_EMPTY) or a negative
 * ZF_E_* code, and on failure fills the caller's zf_err before returning:
 * the code, then a UTF-8 message cut to 256 bytes. On success the zf_err is
 * left untouched. There is no thread-local "last error": calls may move
 * between OS threads.
 *
 * Threads. Every function may be called from any thread, concurrently,
 * except zf_core_close, which must be the last call on its core. The core
 * runs on threads of its own (upstream's tokio runtime with two workers,
 * plus one forwarding thread) and never calls into the caller.
 *
 * Wakeups. zf_event_fd's descriptor is the non-blocking read end of a pipe,
 * readable while events wait. Drain with zf_drain (or zf_next_event) until
 * it returns ZF_EMPTY; that call re-arms the descriptor. No wakeup is lost.
 * Do not read the descriptor yourself; poll it only.
 *
 * Memory. Buffers passed in are read or written during the call only; the
 * core keeps no pointer. Blobs are bytes too large for a record:
 *  - A blob the caller puts (zf_blob_put) stays until a command that names
 *    it takes it (on a successful zf_send), or until zf_blob_free.
 *  - A blob zf_voice_decode makes is the caller's too: free it once read.
 *  - A blob an event names (a thumbnail, a decoded picture) is
 *    the core's, and the caller need not free it. The same bytes in several
 *    records share one id, so a message sent again keeps its thumbnail's id.
 *    The core keeps the event blobs touched last (made, named again or read)
 *    up to 64 MiB, and frees older ones itself (those over 1 MiB first),
 *    never one touched in the last 30 s. A read after that is
 *    ZF_E_NOT_FOUND: load the message again for a fresh blob. zf_blob_free
 *    on an event blob is optional: it lets go of one record's hold early,
 *    and the blob goes once every record naming it has been let go, or
 *    when it expires.
 *  - Frames: see zf_frame_acquire.
 * All of a core's blobs and frames go with zf_core_close.
 *
 * Panics. Every function catches panics. A panic inside a call returns
 * ZF_E_PANIC and poisons the core: zf_core_start, zf_send and the blob and
 * frame calls then return ZF_E_POISONED, while draining and zf_core_close
 * still work. A panic on a core thread is delivered as a Fatal event. An
 * abort (a panic while panicking, running out of memory) ends the process.
 */
#ifndef ZFCORE_H
#define ZFCORE_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define ZF_ABI 1
/* No command or event record is longer: 1 MiB. */
#define ZF_MAX_RECORD (1u << 20)
/* zf_drain's smallest buffer: one record and its length prefix. */
#define ZF_DRAIN_MIN (ZF_MAX_RECORD + 4u)

#define ZF_OK 0
#define ZF_EMPTY 1            /* zf_next_event, zf_drain: nothing waiting */
#define ZF_E_ARG (-1)         /* a null pointer, or an argument out of range */
#define ZF_E_SMALL (-2)       /* the event buffer is under ZF_MAX_RECORD (ZF_DRAIN_MIN) */
#define ZF_E_MALFORMED (-3)   /* a record that does not decode */
#define ZF_E_TOO_LARGE (-4)   /* a command over ZF_MAX_RECORD */
#define ZF_E_VERSION (-5)     /* the caller's ZF_ABI or schema hash differs */
#define ZF_E_POISONED (-6)    /* an earlier call on this core panicked */
#define ZF_E_PANIC (-7)       /* this call panicked; the core is now poisoned */
#define ZF_E_NOT_FOUND (-8)   /* no such blob (or not at that length), or no frame */
#define ZF_E_RANGE (-9)       /* a blob offset past its end */
#define ZF_E_STATE (-10)      /* started twice, or a demo-only command to a real core */
#define ZF_E_IO (-11)         /* the core could not create its pipe or threads */
#define ZF_E_TIMEOUT (-12)    /* zf_core_close's timeout passed; closing goes on */
#define ZF_E_REFUSED (-13)    /* the command's fields are inconsistent */

typedef struct zf_core zf_core;

/* Caller-owned; filled by the failing call. */
typedef struct {
    int32_t code;
    uint32_t len;
    uint8_t msg[256];
} zf_err;

#define ZF_FRAME_I420 1u
#define ZF_FRAME_RGBA 2u
/* A video frame, valid until zf_frame_release. For I420 the three planes
 * follow each other from px, each stride[i] bytes per row: Y has h rows, U
 * and V (h + 1) / 2 rows of (w + 1) / 2 samples (limited-range BT.601).
 * pts_ns is when the core received the frame, on a monotonic clock that
 * starts with the core: it grows with every new frame, so a caller can skip
 * uploading a frame it already has. */
typedef struct {
    const uint8_t *px;
    uint32_t w, h, stride[3], fmt;
    int64_t pts_ns;
} zf_frame;

/* Must equal ZF_ABI, or the caller refuses to start. */
uint32_t zf_abi_version(void);
/* The schema hash both generated sides embed (FNV-1a 64). */
uint64_t zf_schema_hash(void);

/* Opens one account's core. cfg is a bare OpenConfig record: the caller's
 * ZF_ABI and schema hash (refused with ZF_E_VERSION on a mismatch), demo,
 * the state and cache roots, the account id (digits), the OS keyring
 * service the archive key is kept under, and the app's name for the
 * linked-device entry the phone shows (empty: upstream's "ZapFast"). That service is the app's own: a
 * real core refuses an empty one or upstream ZapFast's with ZF_E_ARG, and
 * one process keeps one service. A real core touches nothing on disk until
 * zf_core_start, except to read an archive that is ready (its cached chat
 * list arrives at open).
 *
 * The demo core emits a synthetic account and answers commands; it never
 * touches the network, the directories or the keyring, and emits its chat
 * list at once. Besides messages it answers:
 *  - Voice notes: Ada's chat, Rust Berlin (from Grace) and Grace's (ours,
 *    played) and Dorothy's each have one before their last line (ids
 *    DEMOVOICE0..3), real
 *    Ogg/Opus files encoded once by upstream's voice::encode into
 *    zapfast-demo-media under the system temporary directory, with their
 *    waveforms and seconds. A SendVoice is encoded there too, and filed
 *    with its path; a stopped demo core removes the ones it was sent.
 *  - Sends (SendText, SendLocation, SendImage, SendVoice, SendSticker,
 *    SendGif) carry their quote, resolved from the demo's messages (from
 *    ReplyTarget.chat for a cross-chat reply; an unknown target sends
 *    unquoted), and SendText and SendImage their mentions.
 *  - Media, from upstream's stock files (assets/demo), written once into
 *    zapfast-demo-media: Ada's chat opens with three photos and a clip
 *    three days back, a PDF and a CSV two days back, and three links
 *    yesterday (ids Ada-media-0..7, her info panel); Katherine
 *    sends a sticker (DEMOSTICKER0) and a GIF (DEMOGIF0, a gif video with a
 *    thumbnail), Margaret one in Family (DEMOSTICKER1), and Grace shares the
 *    stock pack in Rust Berlin (DEMOPACK0), each before its chat's last
 *    line. LoadChatMedia lists a chat's photos and clips in one page.
 *  - Margaret's chat, for the bubble pages: a poll (DEMOPOLL0; VotePoll
 *    moves our vote, answering MessageUpdated and PollVoted), a contact
 *    card, a named location, a round video, an edited and a forwarded line
 *    (DEMOCONTACT0, DEMOLOCATION0, DEMONOTE0, DEMOEDITED0, DEMOFORWARDED0),
 *    and two reactions on her first line.
 *  - Video messages carry a JPEG thumbnail, the first frame of the stock
 *    clip they play (zfcore/assets/posters), as real ones carry one; a
 *    GIF's is its search still.
 *  - A locked chat: Dorothy Vaughan's (15550004444@s.whatsapp.net, locked
 *    in Chats) has a voice note, a photo and a clip (DEMOVOICE3,
 *    DEMOLOCKED0, DEMOLOCKED1) before her last line. SetLocked locks or
 *    unlocks any chat, answering ChatUpdated; the code is the UI's.
 *  - Groups: Rust Berlin and Family carry their participants (us first),
 *    their description and info_locked; we are admin of Rust Berlin only.
 *    (The schema has no per-member admin flag.)
 *  - GIFs: SearchGifs finds six stock stills by word (launch, party, pug,
 *    frog, goat, raccoon...; an empty query lists all), with an empty mp4;
 *    the key "zf-demo:bad-key" is rejected (bad_key). SendGif files a gif
 *    video of a stock clip with the result's still as its thumbnail.
 *  - Stickers: RecentStickers lists four Recent, two Favorites, the two
 *    others sent (Received), the stock pack of eight and a local pack, with
 *    their emojis. SendSticker files it and makes it Recent's newest, even
 *    one removed from Recent before; RemoveRecentSticker, SaveSticker and
 *    ForgetSticker change the shelves; each answers with Stickers.
 *    SendStickerPack files a pack message; ViewStickerPack previews a
 *    shared pack; AddStickerPack adds one (Info, Stickers).
 *  - EnsureChat: a chat it does not have is added, with ChatUpdated.
 *    MarkPlayed is taken with no answer, as upstream sends only a receipt.
 *  - FetchAvatar: its people's and groups' portraits, written once under
 *    the system temporary directory (zapfast-demo-avatars); none for others.
 *  - LoadStorageStats, ClearMediaCache, FetchAccountPrivacy,
 *    SetAccountPrivacy, ListChatShared (the chat's media, documents and
 *    links; two links on its first line for a chat with none),
 *    WatchReceipts (sent again as the followed message's status changes).
 *    Receipts are full for our messages, with played_at for a played voice
 *    note, except upstream's three partial cases: Rust-Berlin-1 has a saved
 *    audience with one outside it (Ada read, Grace delivered, Katherine not
 *    expected), DEMORECEIPTS0 (our earlier line in Rust Berlin) none saved
 *    (Ada read, Grace delivered: "not recorded"), and Family-1 none
 *    recorded.
 *  - Calls, one at a time: StartCall goes Placing, Dialing, Ringing (after
 *    0.55 s), Connecting (2.05 s), Active (2.35 s); JoinCallLink goes
 *    Placing, Connecting, Active with two members; CancelCall before the offer fails it unplaced; a
 *    second call is refused (Failed). AnswerCall, DeclineCall, HangupCall,
 *    the mute, camera and device commands answer with Call updates;
 *    RefreshCallDevices with three fake speakers, two microphones and two
 *    cameras; LoadCallLog with five past calls and the ones made since. A
 *    finished call is filed in its chat (Messages, ChatUpdated) and the log.
 *  - A ringing call: DemoIncoming whose text is "zf-demo:call" (voice) or
 *    "zf-demo:video-call" rings from its sender in its chat instead of
 *    filing a message; a group chat rings a group call with the group's
 *    members. Unanswered, it is missed after 30 s.
 *  - Video: while a call is Active, about 15 frames a second of a moving
 *    I420 test pattern on each stream with a picture: ours (320 × 180)
 *    while the camera is on, the peer's (640 × 360) on a one-to-one video
 *    call, and the first two joined members' (320 × 180) on a group video
 *    call. Each picture's first frame sets its flag (remote_video, a
 *    participant's video) in a Call update that follows it. */
int32_t zf_core_open(const uint8_t *cfg, size_t len, zf_core **out, zf_err *err);

/* The startup permit: migrations, then the connection. Call after the
 * first frame, once. */
int32_t zf_core_start(zf_core *c, zf_err *err);

/* The wake descriptor (O_NONBLOCK, FD_CLOEXEC); -1 for a null core. It
 * stays open until zf_core_close. */
int32_t zf_event_fd(zf_core *c);

/* One record into dst; cap >= ZF_MAX_RECORD. 0 with *len set, 1 when
 * empty (*len = 0), < 0 on error. */
int32_t zf_next_event(zf_core *c, uint8_t *dst, size_t cap, size_t *len, zf_err *err);

/* As many whole records as fit, each after a little-endian u32 length;
 * cap >= ZF_DRAIN_MIN, so at least one always fits. 0 with *len bytes and
 * *count records written, 1 when empty, < 0 on error. A length over
 * ZF_MAX_RECORD cannot occur; a reader seeing one must treat the core as
 * failed. */
int32_t zf_drain(zf_core *c, uint8_t *dst, size_t cap, size_t *len, uint32_t *count, zf_err *err);

/* Decodes, copies and queues one command record; never blocks. A command
 * naming a blob takes it on success. */
int32_t zf_send(zf_core *c, const uint8_t *cmd, size_t len, zf_err *err);

/* Copies the blob's bytes from off into dst, up to cap; *len is how many,
 * 0 at the end. Large blobs are read in pieces. */
int32_t zf_blob_read(zf_core *c, uint64_t id, uint64_t off, uint8_t *dst, size_t cap, size_t *len, zf_err *err);

/* Copies len bytes into a new blob, for a command to name. */
int32_t zf_blob_put(zf_core *c, const uint8_t *src, size_t len, uint64_t *id, zf_err *err);

/* Frees a blob; an unknown id is ignored. */
void zf_blob_free(zf_core *c, uint64_t id);

/* Call video. Streams:
 *   ZF_STREAM_LOCAL (0)   our own camera, the corner preview
 *   ZF_STREAM_REMOTE (1)  the peer's camera on a one-to-one call
 *   ZF_STREAM_MEMBER (2)  + i: the camera of participant i of the newest
 *                         Call event's group (update.group.participants)
 * A member's frames are kept by their id, so a reordered roster moves the
 * picture with its person; until the caller has applied the same Call event
 * the core has, for a frame or so, stream 2 + i may be another member's.
 *
 * zf_frame_acquire fills *out with the newest frame of stream, which stays
 * the core's and valid until zf_frame_release(stream) or zf_core_close. At
 * most one frame a stream is held: acquiring a stream again before
 * releasing it is ZF_E_STATE. A stream with no frame now is ZF_E_NOT_FOUND:
 * no call is live, its camera is off (camera_on, remote_video, or the
 * participant's video), or none has arrived yet. Acquiring returns the
 * newest frame whether or not it was acquired before; compare pts_ns. All
 * frames are I420 (ZF_FRAME_I420), upright, at the size the call screen
 * draws them: the peer's at its own size, a member's within 640 × 360 and
 * our preview within 320 × 180 (either way round). Frames are kept only while the call is
 * live; a frame held across the call's end stays valid until released.
 * zf_frame_release of a stream with nothing held does nothing. */
#define ZF_STREAM_LOCAL 0u
#define ZF_STREAM_REMOTE 1u
#define ZF_STREAM_MEMBER 2u
int32_t zf_frame_acquire(zf_core *c, uint32_t stream, zf_frame *out, zf_err *err);
void zf_frame_release(zf_core *c, uint32_t stream);

/* The core's log, for the whole process (every core's records): the Rust
 * `log` records of the core and of upstream's backend, written to fd (a
 * duplicate of it is kept, so lines the host writes to the same open file
 * and these share one offset; each line is one write). level: 0 off,
 * 1 error, 2 warn, 3 info, 4 debug, 5 trace; it applies to the core's and
 * upstream's targets (zapfast...) and the WhatsApp library's, whose lines
 * are reduced to fixed categories as upstream's app does; other crates
 * log warnings and errors only. A negative fd stops logging. Call it again
 * to move the log or change the level. A line reads as Go's slog text
 * handler writes one, in UTC:
 *   time=2026-10-21T04:35:40.123Z level=WARN source=core target=zapfast::backend::worker msg="..."
 * The first line after a start says so ("the core logs here, from WARN").
 * A panic is logged with its thread and location, never its payload.
 * Errors: ZF_E_ARG for a level out of range, ZF_E_IO when fd cannot be
 * duplicated, ZF_E_STATE when another Rust logger owns this process.
 * Optional, like zf_call_generation. */
int32_t zf_set_log(int32_t fd, int32_t level, zf_err *err);

/* Voice-note playback. Decodes the Ogg/Opus file at path (path_len bytes,
 * absolute, no NUL) with upstream's voice::decode, on the calling thread
 * (never the core's: a minute of audio takes some milliseconds, so call it
 * off the UI thread), into a new blob of PCM:
 *   mono (channels 1), 48,000 samples a second (rate), each a
 *   little-endian IEEE-754 f32 in about -1.0 ..= 1.0; len = frames * 4.
 * The encoder's pre-skip is trimmed; the last 20 ms frame is whole. The
 * blob is the caller's, as one it put: it never expires; read it, then free
 * it with zf_blob_free. waveform is the note's 64 bars, 0 to 100, as
 * upstream computes them for a note it sends.
 *
 * Only files under the account's own folders are read: its state and cache
 * folders (downloads and sent media), and the download folder the last
 * SetDownloadFolder named; for the demo core, its media folder
 * (zapfast-demo-media under the system temporary directory). Symbolic links
 * are resolved first. Errors: ZF_E_ARG for a relative path or one outside
 * those folders, ZF_E_NOT_FOUND for no such file, ZF_E_TOO_LARGE for a file
 * over 64 MiB or a stream over 30 minutes (told by its last page, before
 * decoding), ZF_E_MALFORMED for one that is not Ogg/Opus, ZF_E_IO.
 *
 * Optional, like zf_call_generation: a library built before it lacks the
 * symbol. */
typedef struct {
    uint64_t blob;        /* the PCM blob's id */
    uint64_t len;         /* its length in bytes: frames * 4 */
    uint64_t frames;      /* samples */
    uint32_t rate;        /* 48000 */
    uint32_t channels;    /* 1 */
    uint8_t waveform[64]; /* 0 ..= 100 */
} zf_voice;
int32_t zf_voice_decode(zf_core *c, const uint8_t *path, size_t path_len, zf_voice *out, zf_err *err);

/* A call generation no call has had: the number StartCall or JoinCallLink
 * carries, so that the Call updates of the call the UI draws at once carry
 * it too. It comes from the counter the core numbers calls it is offered
 * with (one per process, shared by every core), so the UI's calls and the
 * core's never collide, and a later call always has a larger generation:
 * "the newer call" can be decided by comparing them, as upstream does.
 * Thread-safe; never 0. */
uint64_t zf_call_generation(void);

/* Stops the core and frees it; c is invalid afterwards, whatever this
 * returns. Blocks up to timeout_ms (upstream's runtime takes up to 3 s to
 * stop): never call it on the main thread. ZF_E_TIMEOUT means closing
 * finishes in the background. */
int32_t zf_core_close(zf_core *c, uint32_t timeout_ms, zf_err *err);

#ifdef __cplusplus
}
#endif

#endif /* ZFCORE_H */
