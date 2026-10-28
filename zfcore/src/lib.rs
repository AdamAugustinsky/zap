//! `zapfast_core`: ZapFast's core (upstream's backend, archive and calls)
//! behind a C ABI for the Go UI. `include/zfcore.h` is the contract; this
//! file implements it.
//!
//! - Commands and events are records generated from `abi/zf.schema`
//!   (`abi_gen.rs`), at most `ZF_MAX_RECORD` bytes each.
//! - Errors are written into the caller's `zf_err` by the failing call.
//!   There is no thread-local last error.
//! - Every entry point catches panics. A panic poisons its core: commands
//!   are refused afterwards, while draining and closing still work. A
//!   panic on a core thread becomes a `Fatal` event. An abort (a panic
//!   while panicking, running out of memory) still ends the process.
//! - Nothing calls back into the caller: events wait in a queue, and a
//!   non-blocking pipe is readable while any wait.

#![allow(non_camel_case_types)]
#![allow(clippy::missing_safety_doc)]

#[rustfmt::skip]
pub mod abi_gen;
pub mod blobs;
pub mod cores;
pub mod demo;
pub mod dto;
pub mod frames;
pub mod logging;
pub mod voice;
pub mod wake;
pub mod wire;

#[cfg(test)]
mod tests;
#[cfg(test)]
mod tests_real;

use std::cell::{Cell, RefCell};
use std::panic::AssertUnwindSafe;
use std::sync::Arc;
use std::sync::atomic::Ordering;
use std::time::Duration;

use crate::abi_gen as w;
use crate::cores::{Core, OpenError, Shared};

pub const ZF_OK: i32 = 0;
pub const ZF_EMPTY: i32 = 1;
pub const ZF_E_ARG: i32 = -1;
pub const ZF_E_SMALL: i32 = -2;
pub const ZF_E_MALFORMED: i32 = -3;
pub const ZF_E_TOO_LARGE: i32 = -4;
pub const ZF_E_VERSION: i32 = -5;
pub const ZF_E_POISONED: i32 = -6;
pub const ZF_E_PANIC: i32 = -7;
pub const ZF_E_NOT_FOUND: i32 = -8;
pub const ZF_E_RANGE: i32 = -9;
pub const ZF_E_STATE: i32 = -10;
pub const ZF_E_IO: i32 = -11;
pub const ZF_E_TIMEOUT: i32 = -12;
pub const ZF_E_REFUSED: i32 = -13;

/// One core, opaque to the caller.
pub struct zf_core {
    _private: [u8; 0],
}

#[repr(C)]
pub struct zf_err {
    pub code: i32,
    pub len: u32,
    pub msg: [u8; 256],
}

#[repr(C)]
pub struct zf_frame {
    pub px: *const u8,
    pub w: u32,
    pub h: u32,
    pub stride: [u32; 3],
    pub fmt: u32,
    pub pts_ns: i64,
}

struct Fail {
    code: i32,
    msg: String,
}

fn fail<T>(code: i32, msg: impl Into<String>) -> Result<T, Fail> {
    Err(Fail {
        code,
        msg: msg.into(),
    })
}

thread_local! {
    /// Set on threads (and inside calls) where zfcore handles its own panics.
    static OWNED: Cell<bool> = const { Cell::new(false) };
    /// Where the last panic on this thread happened.
    static LAST_PANIC: RefCell<String> = const { RefCell::new(String::new()) };
}

fn install_panic_hook() {
    static ONCE: std::sync::Once = std::sync::Once::new();
    ONCE.call_once(|| {
        std::panic::set_hook(Box::new(|info| {
            // The location and thread only: a payload may quote user data.
            let location = info
                .location()
                .map(|l| format!("{}:{}", l.file(), l.line()))
                .unwrap_or_default();
            let thread = std::thread::current()
                .name()
                .unwrap_or("unnamed")
                .to_owned();
            eprintln!("zapfast_core: panic on thread {thread} at {location}");
            log::error!(target: "zapfast_core", "panic on thread {thread} at {location}");
            LAST_PANIC.with(|l| l.borrow_mut().clone_from(&location));
            if !OWNED.with(Cell::get) {
                crate::cores::fatal_to_real_cores(&thread, &location);
            }
        }));
    });
}

fn last_panic() -> String {
    LAST_PANIC.with(|l| l.borrow().clone())
}

/// Runs a core thread's body; a panic becomes that core's `Fatal` event.
pub(crate) fn owned_thread(shared: &Shared, origin: &str, body: impl FnOnce()) {
    OWNED.with(|o| o.set(true));
    if std::panic::catch_unwind(AssertUnwindSafe(body)).is_err() {
        shared.emit_record(w::Event::Fatal {
            origin: origin.into(),
            thread: std::thread::current().name().unwrap_or("unnamed").into(),
            location: last_panic(),
        });
    }
}

/// Writes an error into the caller's `zf_err`, its message cut to 256 bytes
/// on a character boundary.
unsafe fn set_err(err: *mut zf_err, code: i32, msg: &str) {
    if err.is_null() {
        return;
    }
    let mut n = msg.len().min(256);
    while !msg.is_char_boundary(n) {
        n -= 1;
    }
    // SAFETY: the caller passes a writable zf_err for the call's duration.
    unsafe {
        let err = &mut *err;
        err.code = code;
        err.len = n as u32;
        err.msg[..n].copy_from_slice(&msg.as_bytes()[..n]);
    }
}

/// Runs an entry point: panics are caught, failures written to `err`.
unsafe fn guard(err: *mut zf_err, f: impl FnOnce() -> Result<i32, Fail>) -> i32 {
    install_panic_hook();
    let was = OWNED.with(|o| o.replace(true));
    let result = std::panic::catch_unwind(AssertUnwindSafe(f));
    OWNED.with(|o| o.set(was));
    let outcome = match result {
        Ok(outcome) => outcome,
        Err(_) => fail(
            ZF_E_PANIC,
            format!("zapfast_core panicked at {}", last_panic()),
        ),
    };
    match outcome {
        Ok(code) => code,
        Err(Fail { code, msg }) => {
            // SAFETY: as the entry point's contract states for `err`.
            unsafe { set_err(err, code, &msg) };
            code
        }
    }
}

/// The core behind a handle, or a failure for a null one.
unsafe fn core_of<'a>(core: *mut zf_core) -> Result<&'a Core, Fail> {
    if core.is_null() {
        return fail(ZF_E_ARG, "the core is null");
    }
    // SAFETY: a non-null handle came from zf_core_open and is not closed.
    Ok(unsafe { &*core.cast::<Core>() })
}

/// Like `core_of`, but refuses a poisoned core.
unsafe fn live<'a>(core: *mut zf_core) -> Result<&'a Core, Fail> {
    // SAFETY: forwarded.
    let c = unsafe { core_of(core)? };
    if c.poisoned.load(Ordering::SeqCst) {
        return fail(ZF_E_POISONED, "an earlier call panicked; close this core");
    }
    Ok(c)
}

/// Runs `f` on a live core; a panic inside poisons it.
unsafe fn with_core(
    core: *mut zf_core,
    err: *mut zf_err,
    f: impl FnOnce(&Core) -> Result<i32, Fail>,
) -> i32 {
    // SAFETY: forwarded.
    unsafe {
        guard(err, || {
            let c = live(core)?;
            match std::panic::catch_unwind(AssertUnwindSafe(|| f(c))) {
                Ok(result) => result,
                Err(_) => {
                    let location = last_panic();
                    c.poisoned.store(true, Ordering::SeqCst);
                    c.shared.emit_record(w::Event::Fatal {
                        origin: "abi".into(),
                        thread: std::thread::current().name().unwrap_or("caller").into(),
                        location: location.clone(),
                    });
                    fail(
                        ZF_E_PANIC,
                        format!("zapfast_core panicked at {location}; the core is poisoned"),
                    )
                }
            }
        })
    }
}

unsafe fn slice<'a>(ptr: *const u8, len: usize) -> Result<&'a [u8], Fail> {
    if len == 0 {
        return Ok(&[]);
    }
    if ptr.is_null() {
        return fail(ZF_E_ARG, "a null buffer with a length");
    }
    // SAFETY: the caller passes `len` readable bytes for the call's duration.
    Ok(unsafe { std::slice::from_raw_parts(ptr, len) })
}

unsafe fn slice_mut<'a>(ptr: *mut u8, len: usize) -> Result<&'a mut [u8], Fail> {
    if ptr.is_null() {
        return fail(ZF_E_ARG, "a null destination");
    }
    // SAFETY: the caller passes `len` writable bytes for the call's duration.
    Ok(unsafe { std::slice::from_raw_parts_mut(ptr, len) })
}

unsafe fn out<T>(ptr: *mut T, value: T) -> Result<(), Fail> {
    if ptr.is_null() {
        return fail(ZF_E_ARG, "a null result pointer");
    }
    // SAFETY: the caller passes a writable T for the call's duration.
    unsafe { ptr.write(value) };
    Ok(())
}

#[unsafe(no_mangle)]
pub extern "C" fn zf_abi_version() -> u32 {
    w::ABI
}

#[unsafe(no_mangle)]
pub extern "C" fn zf_schema_hash() -> u64 {
    w::SCHEMA_HASH
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zf_core_open(
    cfg: *const u8,
    len: usize,
    out_core: *mut *mut zf_core,
    err: *mut zf_err,
) -> i32 {
    // SAFETY: the arguments follow zfcore.h.
    unsafe {
        guard(err, || {
            if out_core.is_null() {
                return fail(ZF_E_ARG, "a null result pointer");
            }
            let bytes = slice(cfg, len)?;
            let config: w::OpenConfig = match wire::decode_bare(bytes) {
                Ok(config) => config,
                Err(e) => return fail(ZF_E_MALFORMED, format!("the open configuration: {e}")),
            };
            let core = match Core::open(config) {
                Ok(core) => core,
                Err(OpenError::Version(why)) => return fail(ZF_E_VERSION, why),
                Err(OpenError::Account) => {
                    return fail(ZF_E_ARG, "the account id is not a folder name of digits");
                }
                Err(OpenError::Keyring(why) | OpenError::DeviceName(why)) => {
                    return fail(ZF_E_ARG, why);
                }
                Err(OpenError::Io(e)) => {
                    return fail(ZF_E_IO, format!("could not start the core: {e}"));
                }
            };
            out(out_core, Arc::into_raw(core).cast_mut().cast::<zf_core>())?;
            Ok(ZF_OK)
        })
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zf_core_start(core: *mut zf_core, err: *mut zf_err) -> i32 {
    // SAFETY: the arguments follow zfcore.h.
    unsafe {
        with_core(core, err, |c| match c.start() {
            Ok(()) => Ok(ZF_OK),
            Err(crate::cores::StartError::Twice) => {
                fail(ZF_E_STATE, "the core has already started")
            }
        })
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zf_event_fd(core: *mut zf_core) -> i32 {
    // SAFETY: the arguments follow zfcore.h.
    unsafe {
        guard(std::ptr::null_mut(), || {
            Ok(core_of(core)?.shared.queue.fd())
        })
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zf_next_event(
    core: *mut zf_core,
    dst: *mut u8,
    cap: usize,
    len: *mut usize,
    err: *mut zf_err,
) -> i32 {
    // SAFETY: the arguments follow zfcore.h.
    unsafe {
        guard(err, || {
            let c = core_of(core)?;
            if cap < w::MAX_RECORD {
                return fail(ZF_E_SMALL, "the buffer is smaller than ZF_MAX_RECORD");
            }
            if len.is_null() {
                return fail(ZF_E_ARG, "a null length pointer");
            }
            let dst = slice_mut(dst, cap)?;
            match c.shared.queue.drain(dst, true) {
                wake::Drained::Empty => {
                    out(len, 0)?;
                    Ok(ZF_EMPTY)
                }
                wake::Drained::Some { len: n, .. } => {
                    out(len, n)?;
                    Ok(ZF_OK)
                }
            }
        })
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zf_drain(
    core: *mut zf_core,
    dst: *mut u8,
    cap: usize,
    len: *mut usize,
    count: *mut u32,
    err: *mut zf_err,
) -> i32 {
    // SAFETY: the arguments follow zfcore.h.
    unsafe {
        guard(err, || {
            let c = core_of(core)?;
            if cap < w::MAX_RECORD + 4 {
                return fail(ZF_E_SMALL, "the buffer is smaller than ZF_DRAIN_MIN");
            }
            if len.is_null() || count.is_null() {
                return fail(ZF_E_ARG, "a null length or count pointer");
            }
            let dst = slice_mut(dst, cap)?;
            match c.shared.queue.drain(dst, false) {
                wake::Drained::Empty => {
                    out(len, 0)?;
                    out(count, 0)?;
                    Ok(ZF_EMPTY)
                }
                wake::Drained::Some { records, len: n } => {
                    out(len, n)?;
                    out(count, records)?;
                    Ok(ZF_OK)
                }
            }
        })
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zf_send(
    core: *mut zf_core,
    cmd: *const u8,
    len: usize,
    err: *mut zf_err,
) -> i32 {
    // SAFETY: the arguments follow zfcore.h.
    unsafe {
        with_core(core, err, |c| {
            if len > w::MAX_RECORD {
                return fail(ZF_E_TOO_LARGE, "the command is longer than ZF_MAX_RECORD");
            }
            let bytes = slice(cmd, len)?;
            let command = match w::Command::decode(bytes) {
                Ok(command) => command,
                Err(e) => return fail(ZF_E_MALFORMED, format!("the command: {e}")),
            };
            match c.send(command) {
                Ok(()) => Ok(ZF_OK),
                Err(dto::Refused::Blob(id)) => fail(
                    ZF_E_NOT_FOUND,
                    format!("blob {id} is not held at the length named"),
                ),
                Err(dto::Refused::Invalid(why)) => fail(ZF_E_REFUSED, why),
                Err(dto::Refused::DemoOnly) => fail(ZF_E_STATE, "that command needs the demo core"),
                Err(dto::Refused::Unsupported(name)) => {
                    fail(ZF_E_STATE, format!("the demo core does not answer {name}"))
                }
            }
        })
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zf_blob_read(
    core: *mut zf_core,
    id: u64,
    off: u64,
    dst: *mut u8,
    cap: usize,
    len: *mut usize,
    err: *mut zf_err,
) -> i32 {
    // SAFETY: the arguments follow zfcore.h.
    unsafe {
        with_core(core, err, |c| {
            if len.is_null() {
                return fail(ZF_E_ARG, "a null length pointer");
            }
            let dst = if cap == 0 {
                &mut [][..]
            } else {
                slice_mut(dst, cap)?
            };
            match c.shared.blobs.read(id, off, dst) {
                Ok(n) => {
                    out(len, n)?;
                    Ok(ZF_OK)
                }
                Err(blobs::BlobError::NotFound) => fail(ZF_E_NOT_FOUND, format!("no blob {id}")),
                Err(blobs::BlobError::Range) => {
                    fail(ZF_E_RANGE, "the offset is past the blob's end")
                }
            }
        })
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zf_blob_put(
    core: *mut zf_core,
    src: *const u8,
    len: usize,
    id: *mut u64,
    err: *mut zf_err,
) -> i32 {
    // SAFETY: the arguments follow zfcore.h.
    unsafe {
        with_core(core, err, |c| {
            if id.is_null() {
                return fail(ZF_E_ARG, "a null id pointer");
            }
            let bytes = slice(src, len)?.to_vec();
            out(id, c.shared.blobs.put(bytes).id)?;
            Ok(ZF_OK)
        })
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zf_blob_free(core: *mut zf_core, id: u64) {
    // SAFETY: the arguments follow zfcore.h.
    unsafe {
        guard(std::ptr::null_mut(), || {
            core_of(core)?.shared.blobs.free(id);
            Ok(ZF_OK)
        });
    }
}

pub const ZF_FRAME_I420: u32 = 1;
pub const ZF_FRAME_RGBA: u32 = 2;

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zf_frame_acquire(
    core: *mut zf_core,
    stream: u32,
    frame: *mut zf_frame,
    err: *mut zf_err,
) -> i32 {
    // SAFETY: the arguments follow zfcore.h.
    unsafe {
        with_core(core, err, |c| {
            if frame.is_null() {
                return fail(ZF_E_ARG, "a null frame pointer");
            }
            match c.shared.frames.acquire(stream) {
                Ok(acquired) => {
                    // Held by the frame table until zf_frame_release.
                    let (px, w, h, stride, fmt) = frames::layout(&acquired.picture);
                    out(
                        frame,
                        zf_frame {
                            px,
                            w,
                            h,
                            stride,
                            fmt,
                            pts_ns: acquired.pts_ns,
                        },
                    )?;
                    Ok(ZF_OK)
                }
                Err(frames::AcquireError::NotFound) => {
                    fail(ZF_E_NOT_FOUND, format!("no frame on stream {stream}"))
                }
                Err(frames::AcquireError::Held) => fail(
                    ZF_E_STATE,
                    format!("stream {stream}'s frame is still held: release it first"),
                ),
            }
        })
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zf_frame_release(core: *mut zf_core, stream: u32) {
    // SAFETY: the arguments follow zfcore.h.
    unsafe {
        guard(std::ptr::null_mut(), || {
            core_of(core)?.shared.frames.release(stream);
            Ok(ZF_OK)
        });
    }
}

#[repr(C)]
pub struct zf_voice {
    pub blob: u64,
    pub len: u64,
    pub frames: u64,
    pub rate: u32,
    pub channels: u32,
    pub waveform: [u8; 64],
}

const _: () =
    assert!(size_of::<zf_voice>() == 96 && std::mem::offset_of!(zf_voice, waveform) == 32);

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zf_voice_decode(
    core: *mut zf_core,
    path: *const u8,
    path_len: usize,
    voice: *mut zf_voice,
    err: *mut zf_err,
) -> i32 {
    // SAFETY: the arguments follow zfcore.h.
    unsafe {
        with_core(core, err, |c| {
            if voice.is_null() {
                return fail(ZF_E_ARG, "a null result pointer");
            }
            use std::os::unix::ffi::OsStrExt as _;
            let path = std::path::Path::new(std::ffi::OsStr::from_bytes(slice(path, path_len)?));
            if path_len == 0 || !path.is_absolute() {
                return fail(ZF_E_ARG, "the path is not absolute");
            }
            match c.decode_voice(path) {
                Ok(decoded) => {
                    out(
                        voice,
                        zf_voice {
                            blob: decoded.blob.id,
                            len: decoded.blob.len,
                            frames: decoded.frames,
                            rate: voice::RATE,
                            channels: 1,
                            waveform: decoded.waveform,
                        },
                    )?;
                    Ok(ZF_OK)
                }
                Err(voice::VoiceError::Outside) => {
                    fail(ZF_E_ARG, "the file is not in the account's folders")
                }
                Err(voice::VoiceError::NotFound) => fail(ZF_E_NOT_FOUND, "no such file"),
                Err(voice::VoiceError::TooLong) => fail(
                    ZF_E_TOO_LARGE,
                    "the voice note is over 30 minutes or 64 MiB",
                ),
                Err(voice::VoiceError::Malformed(why)) => {
                    fail(ZF_E_MALFORMED, format!("not a voice note: {why}"))
                }
                Err(voice::VoiceError::Io(why)) => {
                    fail(ZF_E_IO, format!("could not read the file: {why}"))
                }
            }
        })
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zf_set_log(fd: i32, level: i32, err: *mut zf_err) -> i32 {
    // SAFETY: err follows zfcore.h.
    unsafe {
        guard(err, || match logging::set(fd, level) {
            Ok(()) => Ok(ZF_OK),
            Err(logging::SetLogError::Level) => fail(ZF_E_ARG, "the level is not 0 to 5"),
            Err(logging::SetLogError::Taken) => {
                fail(ZF_E_STATE, "another logger owns this process's Rust log")
            }
            Err(logging::SetLogError::Io(why)) => fail(
                ZF_E_IO,
                format!("could not duplicate the descriptor: {why}"),
            ),
        })
    }
}

/// A call generation no call has had yet: the number a `StartCall` or
/// `JoinCallLink` carries, from the counter the core numbers incoming calls
/// with (upstream's `calls::next_generation`, shared by every core in the
/// process).
#[unsafe(no_mangle)]
pub extern "C" fn zf_call_generation() -> u64 {
    zapfast::calls::next_generation()
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zf_core_close(
    core: *mut zf_core,
    timeout_ms: u32,
    err: *mut zf_err,
) -> i32 {
    // SAFETY: the arguments follow zfcore.h.
    unsafe {
        guard(err, || {
            if core.is_null() {
                return fail(ZF_E_ARG, "the core is null");
            }
            // SAFETY: the handle came from Arc::into_raw in zf_core_open, and
            // this is its last use.
            let core = Arc::from_raw(core.cast_const().cast::<Core>());
            let (done, finished) = std::sync::mpsc::channel();
            std::thread::Builder::new()
                .name("zf-close".into())
                .spawn(move || {
                    OWNED.with(|o| o.set(true));
                    let _ = std::panic::catch_unwind(AssertUnwindSafe(|| core.shutdown()));
                    drop(core);
                    let _ = done.send(());
                })
                .map_err(|e| Fail {
                    code: ZF_E_IO,
                    msg: format!("could not start closing: {e}"),
                })?;
            match finished.recv_timeout(Duration::from_millis(timeout_ms.into())) {
                Ok(()) => Ok(ZF_OK),
                Err(_) => fail(
                    ZF_E_TIMEOUT,
                    "the core is still closing; it finishes in the background",
                ),
            }
        })
    }
}
