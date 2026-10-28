//! A call's video frames, for `zc_frame_acquire`: the newest frame of each
//! stream, held until the caller releases it. zfcore's frames.rs for a 1:1
//! call: stream 0 is our camera (the corner preview), 1 the peer's.
//!
//! Frames are kept only while a call is live (between `begin` and `end` of
//! its generation), and a stream's frame goes when its picture stops: our
//! camera off, the peer's video gone. A held frame outlives all of that
//! until it is released, so the pointer the caller holds stays valid.

use std::sync::{Arc, Mutex, MutexGuard};
use std::time::Instant;

use zapfast::video_frames::CallFrame;

pub const LOCAL: u32 = 0;
pub const REMOTE: u32 = 1;

#[derive(Clone)]
struct Frame {
    picture: Arc<CallFrame>,
    pts_ns: i64,
}

#[derive(Default)]
struct Table {
    live: Option<u64>,
    local: Option<Frame>,
    remote: Option<Frame>,
    held: Vec<(u32, Arc<CallFrame>)>,
    last_pts: i64,
}

pub struct Frames {
    table: Mutex<Table>,
    epoch: Instant,
}

#[derive(Debug, PartialEq, Eq)]
pub enum AcquireError {
    /// The stream has no frame now.
    NotFound,
    /// The stream's last frame has not been released.
    Held,
}

/// One acquired frame: alive while the table holds it.
pub struct Acquired {
    pub picture: Arc<CallFrame>,
    pub pts_ns: i64,
}

impl Default for Frames {
    fn default() -> Self {
        Self {
            table: Mutex::default(),
            epoch: Instant::now(),
        }
    }
}

impl Frames {
    fn lock(&self) -> MutexGuard<'_, Table> {
        self.table.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// Call `generation` is live: nothing of an earlier call's is its picture.
    pub fn begin(&self, generation: u64) {
        let mut t = self.lock();
        t.live = Some(generation);
        t.local = None;
        t.remote = None;
    }

    /// Call `generation` is over; another call's end leaves the live one alone.
    pub fn end(&self, generation: u64) {
        let mut t = self.lock();
        if t.live == Some(generation) {
            t.live = None;
            t.local = None;
            t.remote = None;
        }
    }

    /// Keeps the newest frame of a stream of call `generation`.
    pub fn put(&self, generation: u64, stream: u32, picture: Arc<CallFrame>) {
        let now = self.epoch.elapsed().as_nanos().min(i64::MAX as u128) as i64;
        let mut t = self.lock();
        if t.live != Some(generation) {
            return;
        }
        // Strictly increasing, so a caller can tell a new frame by it.
        t.last_pts = now.max(t.last_pts + 1);
        let frame = Some(Frame {
            picture,
            pts_ns: t.last_pts,
        });
        match stream {
            LOCAL => t.local = frame,
            REMOTE => t.remote = frame,
            _ => {}
        }
    }

    /// A stream's picture stopped: its last frame is not drawn again.
    pub fn clear(&self, generation: u64, stream: u32) {
        let mut t = self.lock();
        if t.live != Some(generation) {
            return;
        }
        match stream {
            LOCAL => t.local = None,
            REMOTE => t.remote = None,
            _ => {}
        }
    }

    /// The newest frame of `stream`, held until `release`.
    pub fn acquire(&self, stream: u32) -> Result<Acquired, AcquireError> {
        let mut t = self.lock();
        if t.held.iter().any(|(s, _)| *s == stream) {
            return Err(AcquireError::Held);
        }
        let frame = match stream {
            LOCAL => t.local.clone(),
            REMOTE => t.remote.clone(),
            _ => None,
        }
        .ok_or(AcquireError::NotFound)?;
        t.held.push((stream, frame.picture.clone()));
        Ok(Acquired {
            picture: frame.picture,
            pts_ns: frame.pts_ns,
        })
    }

    /// Lets the frame of `stream` go; nothing when none is held.
    pub fn release(&self, stream: u32) {
        self.lock().held.retain(|(s, _)| *s != stream);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn picture(luma: u8) -> Arc<CallFrame> {
        let mut frame = CallFrame::new([4, 2]);
        frame.planes_mut().0.fill(luma);
        Arc::new(frame)
    }

    #[test]
    fn frames_live_only_with_their_call_and_stay_valid_while_held() {
        let f = Frames::default();
        f.put(1, LOCAL, picture(1));
        assert_eq!(f.acquire(LOCAL).err(), Some(AcquireError::NotFound));
        f.begin(1);
        f.put(1, LOCAL, picture(1));
        f.put(1, REMOTE, picture(2));
        let held = f.acquire(REMOTE).unwrap();
        assert_eq!(f.acquire(REMOTE).err(), Some(AcquireError::Held));
        let local = f.acquire(LOCAL).unwrap();
        assert!(held.pts_ns > local.pts_ns, "stamped in arrival order");
        f.release(LOCAL);
        f.clear(1, LOCAL);
        assert_eq!(f.acquire(LOCAL).err(), Some(AcquireError::NotFound));
        f.end(2); // another call's end
        f.end(1);
        assert_eq!(held.picture.data[0], 2, "the held picture outlives its call");
        f.release(REMOTE);
        assert_eq!(f.acquire(REMOTE).err(), Some(AcquireError::NotFound));
    }
}
