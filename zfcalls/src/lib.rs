//! `zfcalls`: Zap's 1:1 call media behind a C ABI, for the Go backend.
//! `include/zfcalls.h` is the contract; this file implements it.
//!
//! whatsmeow owns the WhatsApp socket and the Signal sessions; Go's
//! signaling (gnui/gocore/calls) decrypts or generates the callKey and reads
//! the relay, and hands both here. This library holds no socket to WhatsApp
//! and no Signal state: it runs whatsapp-rust's media engine over the relay
//! (host.rs), the audio devices, the camera and the H.264 codec, and keeps
//! the decoded frames for the UI (frames.rs).
//!
//! - Errors are written into the caller's `zc_err` by the failing call.
//! - Every entry point catches panics (ZC_E_PANIC); a panic on the call's
//!   own task ends that call with a `failed` event.
//! - Nothing calls back into the caller: events wait in a queue that
//!   `zc_next_event` drains, blocking up to a timeout.

#![allow(non_camel_case_types)]
#![allow(clippy::missing_safety_doc)]

pub mod frames;
pub mod host;
mod log_sink;
pub mod wire;

use std::panic::AssertUnwindSafe;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use crate::frames::{AcquireError, Frames};
use crate::host::Live;

pub const ZC_ABI: u32 = 1;

pub const ZC_OK: i32 = 0;
pub const ZC_E_ARG: i32 = -1;
pub const ZC_E_SMALL: i32 = -2;
pub const ZC_E_MALFORMED: i32 = -3;
pub const ZC_E_PANIC: i32 = -7;
pub const ZC_E_NOT_FOUND: i32 = -8;
pub const ZC_E_STATE: i32 = -10;
pub const ZC_E_IO: i32 = -11;
pub const ZC_E_REFUSED: i32 = -13;

/// The frame format (zfcore's ZF_FRAME_I420).
pub const ZC_FRAME_I420: u32 = 1;

#[repr(C)]
pub struct zc_err {
    pub code: i32,
    pub len: u32,
    pub msg: [u8; 256],
}

/// zfcore's `zf_frame`, field for field, so gnui reads both the same way.
#[repr(C)]
pub struct zc_frame {
    pub px: *const u8,
    pub w: u32,
    pub h: u32,
    pub stride: [u32; 3],
    pub fmt: u32,
    pub pts_ns: i64,
}

/// One account's calls: a runtime, the frame table, the event queue, and at
/// most one live call.
pub struct zc_ctx {
    rt: tokio::runtime::Runtime,
    frames: Arc<Frames>,
    events: async_channel::Sender<wire::Event>,
    inbox: async_channel::Receiver<wire::Event>,
    /// An event too large for the caller's last buffer, kept for the next.
    pending: Mutex<Option<Vec<u8>>>,
    live: Mutex<Option<Live>>,
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

fn install_panic_hook() {
    static ONCE: std::sync::Once = std::sync::Once::new();
    ONCE.call_once(|| {
        let previous = std::panic::take_hook();
        std::panic::set_hook(Box::new(move |info| {
            // The location only: a payload may quote call data.
            let location = info
                .location()
                .map(|l| format!("{}:{}", l.file(), l.line()))
                .unwrap_or_default();
            eprintln!("zfcalls: panic at {location}");
            let _ = &previous;
        }));
    });
}

/// Writes an error into the caller's `zc_err`, its message cut to 256 bytes
/// on a character boundary.
unsafe fn set_err(err: *mut zc_err, code: i32, msg: &str) {
    if err.is_null() {
        return;
    }
    let mut n = msg.len().min(256);
    while !msg.is_char_boundary(n) {
        n -= 1;
    }
    // SAFETY: the caller passes a writable zc_err for the call's duration.
    unsafe {
        let err = &mut *err;
        err.code = code;
        err.len = n as u32;
        err.msg[..n].copy_from_slice(&msg.as_bytes()[..n]);
    }
}

/// Runs an entry point: panics are caught, failures written to `err` and
/// turned into the entry point's failure value by `failed`.
unsafe fn guard<T>(
    err: *mut zc_err,
    failed: impl FnOnce(i32) -> T,
    f: impl FnOnce() -> Result<T, Fail>,
) -> T {
    install_panic_hook();
    let outcome = match std::panic::catch_unwind(AssertUnwindSafe(f)) {
        Ok(outcome) => outcome,
        Err(_) => fail(ZC_E_PANIC, "zfcalls panicked"),
    };
    match outcome {
        Ok(value) => value,
        Err(Fail { code, msg }) => {
            // SAFETY: as the entry point's contract states for `err`.
            unsafe { set_err(err, code, &msg) };
            failed(code)
        }
    }
}

unsafe fn ctx_of<'a>(ctx: *mut zc_ctx) -> Result<&'a zc_ctx, Fail> {
    if ctx.is_null() {
        return fail(ZC_E_ARG, "the context is null");
    }
    // SAFETY: a non-null context came from zc_new and is not freed.
    Ok(unsafe { &*ctx })
}

unsafe fn bytes<'a>(ptr: *const u8, len: usize) -> Result<&'a [u8], Fail> {
    if ptr.is_null() {
        return fail(ZC_E_ARG, "a null buffer");
    }
    // SAFETY: the caller passes `len` readable bytes for the call's duration.
    Ok(unsafe { std::slice::from_raw_parts(ptr, len) })
}

/// Copies `data` into the caller's buffer: its length, or ZC_E_SMALL.
unsafe fn copy_out(data: &[u8], buf: *mut u8, cap: usize) -> Result<i64, Fail> {
    if buf.is_null() {
        return fail(ZC_E_ARG, "a null buffer");
    }
    if data.len() > cap {
        return fail(
            ZC_E_SMALL,
            format!("{} bytes do not fit a buffer of {cap}", data.len()),
        );
    }
    // SAFETY: the caller passes `cap` writable bytes for the call's duration.
    unsafe { std::ptr::copy_nonoverlapping(data.as_ptr(), buf, data.len()) };
    Ok(data.len() as i64)
}

#[unsafe(no_mangle)]
pub extern "C" fn zc_abi_version() -> u32 {
    ZC_ABI
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zc_new(err: *mut zc_err) -> *mut zc_ctx {
    // SAFETY: the arguments follow zfcalls.h.
    unsafe {
        guard(err, |_| std::ptr::null_mut(), || {
            log_sink::install();
            let rt = tokio::runtime::Builder::new_multi_thread()
                .worker_threads(2)
                .thread_name("zfcalls")
                .enable_all()
                .build()
                .map_err(|e| Fail {
                    code: ZC_E_IO,
                    msg: format!("the call runtime: {e}"),
                })?;
            let (events, inbox) = async_channel::bounded(256);
            Ok(Box::into_raw(Box::new(zc_ctx {
                rt,
                frames: Arc::default(),
                events,
                inbox,
                pending: Mutex::default(),
                live: Mutex::default(),
            })))
        })
    }
}

/// Ends the live call and frees the context. No frame may be held.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn zc_free(ctx: *mut zc_ctx) {
    if ctx.is_null() {
        return;
    }
    let _ = std::panic::catch_unwind(AssertUnwindSafe(|| {
        // SAFETY: the context came from zc_new; this is its last call.
        let ctx = unsafe { Box::from_raw(ctx) };
        if let Some(live) = ctx.live.lock().unwrap_or_else(|e| e.into_inner()).take() {
            live.close();
        }
        let zc_ctx { rt, .. } = *ctx;
        // The call's task releases its devices on the way out.
        rt.shutdown_timeout(Duration::from_secs(2));
    }));
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zc_open(
    ctx: *mut zc_ctx,
    params: *const u8,
    len: usize,
    err: *mut zc_err,
) -> i32 {
    // SAFETY: the arguments follow zfcalls.h.
    unsafe {
        guard(err, |code| code, || {
            let c = ctx_of(ctx)?;
            let p: wire::Open = serde_json::from_slice(bytes(params, len)?).map_err(|e| Fail {
                code: ZC_E_MALFORMED,
                msg: format!("the call's parameters: {e}"),
            })?;
            let mut live = c.live.lock().unwrap_or_else(|e| e.into_inner());
            if let Some(other) = live.as_ref().filter(|l| !l.finished()) {
                return fail(
                    ZC_E_STATE,
                    format!("call {} is still open: close it first", other.generation),
                );
            }
            let prepared = host::prepare(&p).map_err(|msg| Fail {
                code: ZC_E_REFUSED,
                msg,
            })?;
            let _enter = c.rt.enter();
            *live = Some(Live::start(
                &c.rt,
                p,
                prepared,
                c.events.clone(),
                Arc::clone(&c.frames),
            ));
            Ok(ZC_OK)
        })
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zc_command(
    ctx: *mut zc_ctx,
    generation: u64,
    command: *const u8,
    len: usize,
    err: *mut zc_err,
) -> i32 {
    // SAFETY: the arguments follow zfcalls.h.
    unsafe {
        guard(err, |code| code, || {
            let c = ctx_of(ctx)?;
            let command: wire::Command =
                serde_json::from_slice(bytes(command, len)?).map_err(|e| Fail {
                    code: ZC_E_MALFORMED,
                    msg: format!("the command: {e}"),
                })?;
            let live = c.live.lock().unwrap_or_else(|e| e.into_inner());
            match live.as_ref() {
                Some(live) if live.generation == generation => {
                    live.command(command);
                    Ok(ZC_OK)
                }
                _ => fail(ZC_E_NOT_FOUND, format!("call {generation} is not open")),
            }
        })
    }
}

/// Ends call `generation`'s media; its `ended` event follows once every
/// device is released. Nothing when it is not open.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn zc_close(ctx: *mut zc_ctx, generation: u64, err: *mut zc_err) -> i32 {
    // SAFETY: the arguments follow zfcalls.h.
    unsafe {
        guard(err, |code| code, || {
            let c = ctx_of(ctx)?;
            let mut live = c.live.lock().unwrap_or_else(|e| e.into_inner());
            if live.as_ref().is_some_and(|l| l.generation == generation)
                && let Some(l) = live.take()
            {
                l.close();
            }
            Ok(ZC_OK)
        })
    }
}

/// The next event as JSON: its length, 0 when none came within `timeout_ms`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn zc_next_event(
    ctx: *mut zc_ctx,
    timeout_ms: u32,
    buf: *mut u8,
    cap: usize,
    err: *mut zc_err,
) -> i64 {
    // SAFETY: the arguments follow zfcalls.h.
    unsafe {
        guard(err, i64::from, || {
            let c = ctx_of(ctx)?;
            let mut pending = c.pending.lock().unwrap_or_else(|e| e.into_inner());
            let data = match pending.take() {
                Some(data) => data,
                None => {
                    // The timer is the runtime's: build it inside.
                    let next = c.rt.block_on(async {
                        tokio::time::timeout(
                            Duration::from_millis(u64::from(timeout_ms)),
                            c.inbox.recv(),
                        )
                        .await
                    });
                    let Ok(Ok(event)) = next else { return Ok(0) };
                    serde_json::to_vec(&event).expect("events serialize")
                }
            };
            match copy_out(&data, buf, cap) {
                Ok(n) => Ok(n),
                Err(f) => {
                    *pending = Some(data);
                    Err(f)
                }
            }
        })
    }
}

/// The microphones, speakers and cameras as JSON. Opens every device to
/// name it: call it off the UI thread.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn zc_devices(
    ctx: *mut zc_ctx,
    buf: *mut u8,
    cap: usize,
    err: *mut zc_err,
) -> i64 {
    // SAFETY: the arguments follow zfcalls.h.
    unsafe {
        guard(err, i64::from, || {
            ctx_of(ctx)?;
            let found = zapfast::calls::devices();
            let audio = |list: Vec<zapfast::calls::AudioDevice>| {
                list.into_iter()
                    .map(|d| wire::Device {
                        id: d.id,
                        label: d.label,
                    })
                    .collect()
            };
            let devices = wire::Devices {
                microphones: audio(found.microphones),
                speakers: audio(found.speakers),
                cameras: found
                    .cameras
                    .into_iter()
                    .map(|d| wire::Device {
                        id: d.id,
                        label: d.label,
                    })
                    .collect(),
            };
            copy_out(&serde_json::to_vec(&devices).expect("devices serialize"), buf, cap)
        })
    }
}

/// The newest frame of `stream` (0: our camera, 1: the peer's), held until
/// zc_frame_release. ZC_E_NOT_FOUND while it has none; ZC_E_STATE while its
/// last frame is held.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn zc_frame_acquire(
    ctx: *mut zc_ctx,
    stream: u32,
    frame: *mut zc_frame,
    err: *mut zc_err,
) -> i32 {
    // SAFETY: the arguments follow zfcalls.h.
    unsafe {
        guard(err, |code| code, || {
            let c = ctx_of(ctx)?;
            if frame.is_null() {
                return fail(ZC_E_ARG, "a null frame pointer");
            }
            match c.frames.acquire(stream) {
                Ok(acquired) => {
                    let p = &acquired.picture;
                    let s = p.strides;
                    // Held by the frame table until zc_frame_release.
                    frame.write(zc_frame {
                        px: p.data.as_ptr(),
                        w: p.size[0] as u32,
                        h: p.size[1] as u32,
                        stride: [s[0] as u32, s[1] as u32, s[2] as u32],
                        fmt: ZC_FRAME_I420,
                        pts_ns: acquired.pts_ns,
                    });
                    Ok(ZC_OK)
                }
                Err(AcquireError::NotFound) => {
                    fail(ZC_E_NOT_FOUND, format!("no frame on stream {stream}"))
                }
                Err(AcquireError::Held) => fail(
                    ZC_E_STATE,
                    format!("stream {stream}'s frame is still held: release it first"),
                ),
            }
        })
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn zc_frame_release(ctx: *mut zc_ctx, stream: u32) {
    let _ = std::panic::catch_unwind(AssertUnwindSafe(|| {
        // SAFETY: the arguments follow zfcalls.h.
        if let Ok(c) = unsafe { ctx_of(ctx) } {
            c.frames.release(stream);
        }
    }));
}

#[cfg(test)]
mod tests;
