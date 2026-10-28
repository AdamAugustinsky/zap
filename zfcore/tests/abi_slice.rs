//! The vertical slice through the built library, by its C ABI alone: the
//! test dlopens libzapfast_core.dylib, as the Go side does, and opens the
//! demo core, reads the chat list, opens a conversation, sends a text and
//! receives an injected message, watching the wake fd each time. Records
//! are built and read with the generated codec, the Go side's twin.

use std::ffi::{CStr, c_void};
use std::time::{Duration, Instant};

use zapfast_core::abi_gen as w;

const ZF_OK: i32 = 0;
const ZF_EMPTY: i32 = 1;
const ZF_E_ARG: i32 = -1;
const ZF_E_SMALL: i32 = -2;
const ZF_E_MALFORMED: i32 = -3;
const ZF_E_VERSION: i32 = -5;
const ZF_E_POISONED: i32 = -6;
const ZF_E_PANIC: i32 = -7;
const ZF_E_NOT_FOUND: i32 = -8;
const ZF_E_RANGE: i32 = -9;
const ZF_E_STATE: i32 = -10;

#[repr(C)]
struct ZfErr {
    code: i32,
    len: u32,
    msg: [u8; 256],
}

impl ZfErr {
    fn new() -> Box<Self> {
        Box::new(Self {
            code: 12345,
            len: 0,
            msg: [0; 256],
        })
    }
    fn message(&self) -> &str {
        std::str::from_utf8(&self.msg[..self.len as usize]).unwrap()
    }
}

type Core = *mut c_void;

#[allow(clippy::type_complexity)]
struct Lib {
    abi_version: unsafe extern "C" fn() -> u32,
    schema_hash: unsafe extern "C" fn() -> u64,
    open: unsafe extern "C" fn(*const u8, usize, *mut Core, *mut ZfErr) -> i32,
    start: unsafe extern "C" fn(Core, *mut ZfErr) -> i32,
    event_fd: unsafe extern "C" fn(Core) -> i32,
    next_event: unsafe extern "C" fn(Core, *mut u8, usize, *mut usize, *mut ZfErr) -> i32,
    drain: unsafe extern "C" fn(Core, *mut u8, usize, *mut usize, *mut u32, *mut ZfErr) -> i32,
    send: unsafe extern "C" fn(Core, *const u8, usize, *mut ZfErr) -> i32,
    blob_read: unsafe extern "C" fn(Core, u64, u64, *mut u8, usize, *mut usize, *mut ZfErr) -> i32,
    blob_put: unsafe extern "C" fn(Core, *const u8, usize, *mut u64, *mut ZfErr) -> i32,
    blob_free: unsafe extern "C" fn(Core, u64),
    close: unsafe extern "C" fn(Core, u32, *mut ZfErr) -> i32,
}

fn library_path() -> std::path::PathBuf {
    if let Some(path) = std::env::var_os("ZF_CORE_LIB") {
        return path.into();
    }
    // Cargo builds the library for this test run beside the test binary,
    // in target/<profile>/deps; the copy one level up may be older.
    let exe = std::env::current_exe().unwrap();
    exe.parent().unwrap().join(if cfg!(target_os = "macos") {
        "libzapfast_core.dylib"
    } else {
        "libzapfast_core.so"
    })
}

fn load() -> &'static Lib {
    static LIB: std::sync::OnceLock<Lib> = std::sync::OnceLock::new();
    LIB.get_or_init(|| {
        let path = std::ffi::CString::new(library_path().to_str().unwrap()).unwrap();
        // SAFETY: dlopen/dlsym with valid C strings; the symbols have the
        // signatures zfcore.h declares.
        unsafe {
            let handle = libc::dlopen(path.as_ptr(), libc::RTLD_NOW | libc::RTLD_LOCAL);
            assert!(
                !handle.is_null(),
                "dlopen {path:?}: {:?}",
                CStr::from_ptr(libc::dlerror())
            );
            macro_rules! sym {
                ($name:literal) => {{
                    let p = libc::dlsym(handle, concat!($name, "\0").as_ptr().cast());
                    assert!(!p.is_null(), "missing {}", $name);
                    p
                }};
            }
            Lib {
                abi_version: std::mem::transmute::<*mut c_void, unsafe extern "C" fn() -> u32>(
                    sym!("zf_abi_version"),
                ),
                schema_hash: std::mem::transmute::<*mut c_void, unsafe extern "C" fn() -> u64>(
                    sym!("zf_schema_hash"),
                ),
                open: std::mem::transmute::<
                    *mut c_void,
                    unsafe extern "C" fn(*const u8, usize, *mut Core, *mut ZfErr) -> i32,
                >(sym!("zf_core_open")),
                start: std::mem::transmute::<
                    *mut c_void,
                    unsafe extern "C" fn(Core, *mut ZfErr) -> i32,
                >(sym!("zf_core_start")),
                event_fd: std::mem::transmute::<*mut c_void, unsafe extern "C" fn(Core) -> i32>(
                    sym!("zf_event_fd"),
                ),
                next_event: std::mem::transmute::<
                    *mut c_void,
                    unsafe extern "C" fn(Core, *mut u8, usize, *mut usize, *mut ZfErr) -> i32,
                >(sym!("zf_next_event")),
                drain: std::mem::transmute::<
                    *mut c_void,
                    unsafe extern "C" fn(
                        Core,
                        *mut u8,
                        usize,
                        *mut usize,
                        *mut u32,
                        *mut ZfErr,
                    ) -> i32,
                >(sym!("zf_drain")),
                send: std::mem::transmute::<
                    *mut c_void,
                    unsafe extern "C" fn(Core, *const u8, usize, *mut ZfErr) -> i32,
                >(sym!("zf_send")),
                blob_read: std::mem::transmute::<
                    *mut c_void,
                    unsafe extern "C" fn(
                        Core,
                        u64,
                        u64,
                        *mut u8,
                        usize,
                        *mut usize,
                        *mut ZfErr,
                    ) -> i32,
                >(sym!("zf_blob_read")),
                blob_put: std::mem::transmute::<
                    *mut c_void,
                    unsafe extern "C" fn(Core, *const u8, usize, *mut u64, *mut ZfErr) -> i32,
                >(sym!("zf_blob_put")),
                blob_free: std::mem::transmute::<*mut c_void, unsafe extern "C" fn(Core, u64)>(
                    sym!("zf_blob_free"),
                ),
                close: std::mem::transmute::<
                    *mut c_void,
                    unsafe extern "C" fn(Core, u32, *mut ZfErr) -> i32,
                >(sym!("zf_core_close")),
            }
        }
    })
}

fn config(demo: bool, root: &std::path::Path, hash: u64) -> Vec<u8> {
    zapfast_core::wire::encode_bare(&w::OpenConfig {
        abi: w::ABI,
        schema_hash: hash,
        demo,
        state_dir: root.join("state"),
        cache_dir: root.join("cache"),
        account: "1".into(),
        keyring_service: "ai.a3lab.zap.test".into(),
        device_name: String::new(),
    })
    .unwrap()
}

/// A core and the buffer its events are drained into.
struct Session {
    lib: &'static Lib,
    core: Core,
    buf: Vec<u8>,
}

impl Session {
    fn open(demo: bool, root: &std::path::Path) -> Self {
        let lib = load();
        let cfg = config(demo, root, w::SCHEMA_HASH);
        let mut core: Core = std::ptr::null_mut();
        let mut err = ZfErr::new();
        // SAFETY: the C ABI, with live buffers.
        let rc = unsafe { (lib.open)(cfg.as_ptr(), cfg.len(), &mut core, &mut *err) };
        assert_eq!(rc, ZF_OK, "{}", err.message());
        assert_eq!(err.code, 12345, "zf_err is untouched on success");
        Self {
            lib,
            core,
            buf: vec![0; w::MAX_RECORD + 4],
        }
    }

    fn fd(&self) -> i32 {
        // SAFETY: a live core.
        unsafe { (self.lib.event_fd)(self.core) }
    }

    /// Whether the wake fd becomes readable within `ms`.
    fn readable(&self, ms: i32) -> bool {
        let mut pfd = libc::pollfd {
            fd: self.fd(),
            events: libc::POLLIN,
            revents: 0,
        };
        // SAFETY: polls one descriptor.
        unsafe { libc::poll(&mut pfd, 1, ms) == 1 }
    }

    fn send(&self, command: &w::Command) -> (i32, Box<ZfErr>) {
        let mut record = Vec::new();
        command.encode(&mut record).unwrap();
        self.send_raw(&record)
    }

    fn send_raw(&self, record: &[u8]) -> (i32, Box<ZfErr>) {
        let mut err = ZfErr::new();
        // SAFETY: the C ABI, with live buffers.
        let rc = unsafe { (self.lib.send)(self.core, record.as_ptr(), record.len(), &mut *err) };
        (rc, err)
    }

    /// One zf_drain call: the records it returned, or None when empty.
    fn drain_once(&mut self) -> Option<Vec<w::Event>> {
        let (mut len, mut count) = (0usize, 0u32);
        let mut err = ZfErr::new();
        // SAFETY: the C ABI, with live buffers.
        let rc = unsafe {
            (self.lib.drain)(
                self.core,
                self.buf.as_mut_ptr(),
                self.buf.len(),
                &mut len,
                &mut count,
                &mut *err,
            )
        };
        if rc == ZF_EMPTY {
            assert_eq!((len, count), (0, 0));
            return None;
        }
        assert_eq!(rc, ZF_OK, "{}", err.message());
        let mut out = Vec::new();
        let mut rest = &self.buf[..len];
        while !rest.is_empty() {
            let n = u32::from_le_bytes(rest[..4].try_into().unwrap()) as usize;
            assert!(n <= w::MAX_RECORD);
            out.push(w::Event::decode(&rest[4..4 + n]).expect("a record the core wrote decodes"));
            rest = &rest[4 + n..];
        }
        assert_eq!(out.len(), count as usize);
        Some(out)
    }

    /// Waits on the fd and drains until `done` has seen what it wants, the
    /// way the Go event goroutine does.
    fn until(&mut self, what: &str, mut done: impl FnMut(&w::Event) -> bool) -> Vec<w::Event> {
        let deadline = Instant::now() + Duration::from_secs(10);
        let mut all = Vec::new();
        loop {
            assert!(
                Instant::now() < deadline,
                "no {what} within 10 s; saw {all:#?}"
            );
            if !self.readable(1000) {
                continue;
            }
            while let Some(events) = self.drain_once() {
                for event in events {
                    let hit = done(&event);
                    all.push(event);
                    if hit {
                        return all;
                    }
                }
            }
        }
    }

    /// Drains what is waiting; the fd must be quiet afterwards.
    fn settle(&mut self) -> Vec<w::Event> {
        let mut all = Vec::new();
        while let Some(events) = self.drain_once() {
            all.extend(events);
        }
        assert!(
            !self.readable(0),
            "the fd stays readable with nothing queued"
        );
        all
    }

    fn close(self) {
        let mut err = ZfErr::new();
        // SAFETY: the handle's last use.
        let rc = unsafe { (self.lib.close)(self.core, 10_000, &mut *err) };
        assert_eq!(rc, ZF_OK, "{}", err.message());
    }
}

const ADA: &str = "393331234567@s.whatsapp.net";

#[test]
fn the_demo_core_through_the_c_abi() {
    let lib = load();
    // SAFETY: argument-free calls.
    unsafe {
        assert_eq!((lib.abi_version)(), w::ABI);
        assert_eq!((lib.schema_hash)(), w::SCHEMA_HASH);
    }
    let root = tempfile::tempdir().unwrap();

    // Open: the cached chat list arrives before the startup permit.
    let opened = Instant::now();
    let mut s = Session::open(true, root.path());
    let events = s.until("the chat list", |e| {
        matches!(e, w::Event::Chats { last: true, .. })
    });
    let first_list = opened.elapsed();
    eprintln!("zf_core_open to the chat list: {first_list:?}");
    assert!(matches!(&events[0], w::Event::Me { id, .. } if id == "15550001111@s.whatsapp.net"));
    let w::Event::Chats { chats, .. } = events.last().unwrap() else {
        unreachable!()
    };
    assert_eq!(chats[0].name, "Ada Lovelace", "newest first");
    assert_eq!(chats[0].unread, 2);
    assert_eq!(
        chats[0].last.as_ref().unwrap().summary,
        "That is wonderful news. Send me the branch when you can."
    );
    assert!(s.settle().is_empty());

    // Start: the link connects and the contacts follow.
    let mut err = ZfErr::new();
    // SAFETY: a live core.
    assert_eq!(unsafe { (lib.start)(s.core, &mut *err) }, ZF_OK);
    let events = s.until("the contacts", |e| {
        matches!(e, w::Event::Contacts { last: true, .. })
    });
    assert!(matches!(
        events[..2],
        [
            w::Event::Link {
                status: w::LinkStatus::Connecting
            },
            w::Event::Link {
                status: w::LinkStatus::Connected
            }
        ]
    ));
    // SAFETY: a live core.
    assert_eq!(
        unsafe { (lib.start)(s.core, &mut *err) },
        ZF_E_STATE,
        "a second permit"
    );
    assert!(err.message().contains("already started"));
    s.settle();

    // Open a conversation.
    let (rc, _) = s.send(&w::Command::LoadChat {
        chat: ADA.into(),
        before: None,
    });
    assert_eq!(rc, ZF_OK);
    assert!(s.readable(2000), "the fd wakes for the answer");
    let events = s.until("the conversation", |e| {
        matches!(e, w::Event::Messages { .. })
    });
    let w::Event::Messages {
        chat,
        messages,
        last,
        complete,
        older,
        ..
    } = &events[0]
    else {
        panic!("{events:?}")
    };
    assert_eq!(
        (chat.as_str(), messages.len(), *last, *complete, *older),
        (ADA, 12, true, true, false)
    );
    // Her shared photos, clip, documents and links come first; then her
    // lines, the voice note before the last.
    let n = messages.len();
    assert!(
        matches!(
            &messages[n - 2].content,
            w::Content::Audio { media, voice_note: true, .. } if media.path.is_some()
        ),
        "the demo's voice note before the last line: {:?}",
        messages[n - 2].content
    );
    assert_eq!(
        messages[n - 3].content,
        w::Content::Text {
            text: "Yes! It compiles on stable now, no nightly needed.".into(),
            preview: None
        }
    );

    // Send a text: archived as pending, then sent, delivered and read.
    let (rc, _) = s.send(&w::Command::SendText {
        chat: ADA.into(),
        text: "On my way 🚲".into(),
        quoting: None,
        mentions: Vec::new(),
    });
    assert_eq!(rc, ZF_OK);
    let events = s.until("the read receipt", |e| {
        matches!(e, w::Event::MessageUpdated { message } if message.status == w::Delivery::Read)
    });
    let statuses: Vec<_> = events
        .iter()
        .filter_map(|e| match e {
            w::Event::Messages { messages, .. } => Some(messages[0].status),
            w::Event::MessageUpdated { message } => Some(message.status),
            _ => None,
        })
        .collect();
    assert_eq!(
        statuses,
        [
            w::Delivery::Pending,
            w::Delivery::Sent,
            w::Delivery::Delivered,
            w::Delivery::Read
        ]
    );
    assert!(events.iter().any(|e| matches!(
        e,
        w::Event::ChatUpdated { chat } if chat.id == ADA && chat.last.as_ref().unwrap().summary == "On my way 🚲"
    )));
    s.settle();

    // Receive an injected message: nothing waits until it is due.
    let (rc, _) = s.send(&w::Command::DemoTyping {
        delay_ms: 0,
        chat: ADA.into(),
        sender: ADA.into(),
        composing: true,
    });
    assert_eq!(rc, ZF_OK);
    let (rc, _) = s.send(&w::Command::DemoIncoming {
        delay_ms: 150,
        chat: ADA.into(),
        sender: ADA.into(),
        sender_name: Some("Ada".into()),
        text: "See you there".into(),
    });
    assert_eq!(rc, ZF_OK);
    let typing = s.until("typing", |e| {
        matches!(
            e,
            w::Event::Typing {
                composing: true,
                ..
            }
        )
    });
    assert_eq!(typing.len(), 1);
    assert!(s.settle().is_empty());
    assert!(!s.readable(50), "quiet before the injected message is due");
    let events = s.until("the incoming message", |e| {
        matches!(e, w::Event::Incoming { .. })
    });
    assert!(matches!(&events[0], w::Event::Messages { messages, .. } if !messages[0].from_me));
    assert!(matches!(&events[1], w::Event::ChatUpdated { chat } if chat.unread == 3));
    let w::Event::Incoming { message, .. } = &events[2] else {
        panic!("{events:?}")
    };
    assert_eq!(message.sender_name.as_deref(), Some("Ada"));
    assert_eq!(
        message.content,
        w::Content::Text {
            text: "See you there".into(),
            preview: None
        }
    );
    s.settle();

    // Mark it read.
    let (rc, _) = s.send(&w::Command::MarkRead {
        chat: ADA.into(),
        receipts: true,
    });
    assert_eq!(rc, ZF_OK);
    let events = s.until("the read chat", |e| {
        matches!(e, w::Event::ChatUpdated { .. })
    });
    assert!(matches!(&events[0], w::Event::ChatUpdated { chat } if chat.unread == 0));
    s.close();
}

#[test]
fn errors_arrive_in_the_callers_zf_err_in_the_same_call() {
    let lib = load();
    let root = tempfile::tempdir().unwrap();

    // A schema mismatch is refused at open, saying so.
    let cfg = config(true, root.path(), w::SCHEMA_HASH ^ 1);
    let mut core: Core = std::ptr::null_mut();
    let mut err = ZfErr::new();
    // SAFETY: the C ABI, with live buffers.
    let rc = unsafe { (lib.open)(cfg.as_ptr(), cfg.len(), &mut core, &mut *err) };
    assert_eq!((rc, err.code), (ZF_E_VERSION, ZF_E_VERSION));
    assert!(err.message().contains("schema hash"), "{}", err.message());
    assert!(core.is_null());
    // SAFETY: a bad configuration record.
    let rc = unsafe { (lib.open)([1u8, 2, 3].as_ptr(), 3, &mut core, &mut *err) };
    assert_eq!(rc, ZF_E_MALFORMED);

    let mut s = Session::open(true, root.path());
    s.until("the chat list", |e| {
        matches!(e, w::Event::Chats { last: true, .. })
    });

    // A malformed command, a too-small buffer and a null pointer, each from
    // its own thread with its own zf_err.
    let core = s.core as usize;
    let threads: Vec<_> = (0..8)
        .map(|i| {
            std::thread::spawn(move || {
                let core = core as Core;
                let mut err = ZfErr::new();
                let rc = match i % 3 {
                    // SAFETY: the C ABI; a record cut short.
                    0 => unsafe { (lib.send)(core, [2u8, 0, 0, 0, 9].as_ptr(), 5, &mut *err) },
                    1 => {
                        let mut small = vec![0u8; 1024];
                        let mut len = 0;
                        // SAFETY: the C ABI with a small but live buffer.
                        unsafe {
                            (lib.next_event)(
                                core,
                                small.as_mut_ptr(),
                                small.len(),
                                &mut len,
                                &mut *err,
                            )
                        }
                    }
                    // SAFETY: the C ABI; a null length pointer.
                    _ => unsafe {
                        (lib.blob_read)(
                            core,
                            1,
                            0,
                            std::ptr::null_mut(),
                            0,
                            std::ptr::null_mut(),
                            &mut *err,
                        )
                    },
                };
                (i, rc, err.code, err.message().to_owned())
            })
        })
        .collect();
    for t in threads {
        let (i, rc, code, msg) = t.join().unwrap();
        let want = [ZF_E_MALFORMED, ZF_E_SMALL, ZF_E_ARG][i % 3];
        assert_eq!((rc, code), (want, want), "{msg}");
        assert!(!msg.is_empty());
    }

    // A demo-only command to... the demo core is fine; an unknown tag is not.
    let (rc, err) = s.send_raw(&[0xff, 0x7f, 0, 0]);
    assert_eq!(rc, ZF_E_MALFORMED);
    assert!(err.message().contains("Command tag"), "{}", err.message());

    // Blobs: a put, read in ranges, then taken by the command that names it.
    let pixels: Vec<u8> = (0..2 * 3 * 4).map(|i| i as u8).collect();
    let mut id = 0;
    let mut err = ZfErr::new();
    // SAFETY: the C ABI, with live buffers.
    assert_eq!(
        unsafe { (lib.blob_put)(s.core, pixels.as_ptr(), pixels.len(), &mut id, &mut *err) },
        ZF_OK
    );
    let mut piece = [0u8; 10];
    let mut got = Vec::new();
    loop {
        let mut n = 0;
        // SAFETY: the C ABI, with live buffers.
        let rc = unsafe {
            (lib.blob_read)(
                s.core,
                id,
                got.len() as u64,
                piece.as_mut_ptr(),
                piece.len(),
                &mut n,
                &mut *err,
            )
        };
        assert_eq!(rc, ZF_OK);
        if n == 0 {
            break;
        }
        got.extend_from_slice(&piece[..n]);
    }
    assert_eq!(got, pixels);
    let mut n = 0;
    // SAFETY: the C ABI, with live buffers.
    let rc = unsafe {
        (lib.blob_read)(
            s.core,
            id,
            99,
            piece.as_mut_ptr(),
            piece.len(),
            &mut n,
            &mut *err,
        )
    };
    assert_eq!(rc, ZF_E_RANGE);
    let image = |len| w::Command::SendImage {
        chat: ADA.into(),
        width: 2,
        height: 3,
        rgba: zapfast_core::wire::BlobRef { id, len },
        caption: None,
        mentions: Vec::new(),
        quoting: None,
    };
    let (rc, _) = s.send(&image(5));
    assert_eq!(rc, -13, "a length that is not width × height × 4");
    let (rc, _) = s.send(&image(24));
    assert_eq!(rc, ZF_OK);
    // SAFETY: the C ABI, with live buffers.
    let rc = unsafe {
        (lib.blob_read)(
            s.core,
            id,
            0,
            piece.as_mut_ptr(),
            piece.len(),
            &mut n,
            &mut *err,
        )
    };
    assert_eq!(rc, ZF_E_NOT_FOUND, "the send took the blob");
    let (rc, err) = s.send(&image(24));
    assert_eq!(rc, ZF_E_NOT_FOUND, "{}", err.message());
    // SAFETY: freeing an id that is gone is a no-op.
    unsafe { (lib.blob_free)(s.core, id) };
    s.close();
}

#[test]
fn a_panic_is_contained_and_poisons_its_core() {
    let root = tempfile::tempdir().unwrap();
    let mut s = Session::open(true, root.path());
    s.until("the chat list", |e| {
        matches!(e, w::Event::Chats { last: true, .. })
    });

    // On the demo's own thread: a Fatal event, and the process lives.
    let (rc, _) = s.send(&w::Command::DemoPanic { on_thread: true });
    assert_eq!(rc, ZF_OK);
    let events = s.until("Fatal", |e| matches!(e, w::Event::Fatal { .. }));
    assert!(matches!(events.last(), Some(w::Event::Fatal { origin, .. }) if origin == "demo"));

    // Inside a call: that call fails, the core is poisoned, and it can
    // still be drained and closed.
    let (rc, err) = s.send(&w::Command::DemoPanic { on_thread: false });
    assert_eq!(rc, ZF_E_PANIC, "{}", err.message());
    assert!(err.message().contains("poisoned"), "{}", err.message());
    let (rc, _) = s.send(&w::Command::SetOnline { online: true });
    assert_eq!(rc, ZF_E_POISONED);
    let events = s.until("Fatal", |e| matches!(e, w::Event::Fatal { .. }));
    assert!(
        matches!(events.last(), Some(w::Event::Fatal { origin, location, .. }) if origin == "abi" && location.contains("dto.rs"))
    );
    s.close();
}

#[test]
fn a_real_core_opens_and_closes_without_touching_storage_before_start() {
    let root = tempfile::tempdir().unwrap();
    let s = Session::open(false, root.path());
    let (rc, err) = s.send(&w::Command::DemoTyping {
        delay_ms: 0,
        chat: ADA.into(),
        sender: ADA.into(),
        composing: true,
    });
    assert_eq!(rc, ZF_E_STATE, "{}", err.message());
    assert!(!s.readable(100));
    s.close();
    let account = root.path().join("state/accounts/1");
    assert!(!account.join("session.db").exists());
    assert!(!account.join("archive.db").exists());
}
