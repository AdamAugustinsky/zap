//! A call's video frames, for `zf_frame_acquire`: the newest frame of each
//! stream, held until the caller releases it.
//!
//! The streams (`zfcore.h`): 0 is our own camera (the corner preview), 1 a
//! one-to-one peer's camera, and `2 + i` the camera of member `i` of the
//! newest `Call` event's group participants. A member's frames are kept by
//! their id, so a roster that reorders moves the picture with its person.
//!
//! Frames arrive as upstream's `Event::CallVideo` (YUV 4:2:0, upright, at the
//! size the call screen draws) and never become records. They are kept only
//! while the call is live, and a stream's frame goes when the call says its
//! picture stopped: our camera off, the peer's video gone, a member's video
//! stopped or the member gone, as upstream's window drops them
//! (`src/app/calls.rs`, `handle_call_update`).

use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::Instant;

use zapfast::video_frames::CallFrame;

use crate::wake::lock;

pub const LOCAL: u32 = 0;
pub const REMOTE: u32 = 1;
pub const MEMBER: u32 = 2;

#[derive(Clone)]
struct Frame {
    picture: Arc<CallFrame>,
    pts_ns: i64,
}

#[derive(Default)]
struct Table {
    /// The live call's generation, while one is live.
    live: Option<u64>,
    local: Option<Frame>,
    remote: Option<Frame>,
    members: HashMap<String, Frame>,
    /// The newest update's participants, in order: stream `MEMBER + i` is
    /// `order[i]`'s.
    order: Vec<String>,
    /// The frames the caller holds, at most one a stream.
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

/// Where a frame's pixels are and how they lie, for `zf_frame`: the
/// pointer, width, height, strides and format. The planes are the
/// picture's own, alive while the table holds it.
#[cfg(not(feature = "ui"))]
pub fn layout(picture: &CallFrame) -> (*const u8, u32, u32, [u32; 3], u32) {
    let [w, h] = picture.size;
    let s = picture.strides;
    (
        picture.data.as_ptr(),
        w as u32,
        h as u32,
        [s[0] as u32, s[1] as u32, s[2] as u32],
        crate::ZF_FRAME_I420,
    )
}

/// The same for the egui build's frames (RGBA), which only checks that
/// zfcore still compiles against upstream's `ui` configuration.
#[cfg(feature = "ui")]
pub fn layout(picture: &CallFrame) -> (*const u8, u32, u32, [u32; 3], u32) {
    let [w, h] = picture.size;
    (
        picture.as_raw().as_ptr(),
        w as u32,
        h as u32,
        [w as u32 * 4, 0, 0],
        crate::ZF_FRAME_RGBA,
    )
}

/// One acquired frame: the picture stays alive while the table holds it.
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
    /// Keeps the newest frames of `CallVideo`; dropped when no call is live.
    pub fn put(
        &self,
        local: Option<Arc<CallFrame>>,
        remote: Option<Arc<CallFrame>>,
        from: Option<String>,
    ) {
        let now = self.epoch.elapsed().as_nanos().min(i64::MAX as u128) as i64;
        let mut t = lock(&self.table);
        if t.live.is_none() {
            return;
        }
        let stamp = |t: &mut Table| {
            // Strictly increasing, so a caller can tell a new frame by it.
            t.last_pts = now.max(t.last_pts + 1);
            t.last_pts
        };
        if let Some(picture) = local {
            let pts_ns = stamp(&mut t);
            t.local = Some(Frame { picture, pts_ns });
        }
        if let Some(picture) = remote {
            let pts_ns = stamp(&mut t);
            let frame = Frame { picture, pts_ns };
            match from {
                Some(member) => {
                    t.members.insert(member, frame);
                }
                None => t.remote = Some(frame),
            }
        }
    }

    /// Follows one call update (before it is converted): which call is live,
    /// whose pictures still arrive, and the participants' order.
    pub fn on_update(&self, update: &zapfast::calls::CallUpdate) {
        let mut t = lock(&self.table);
        if !update.phase.is_live() {
            // Only the live call's own end clears it: a call refused while
            // another was up says nothing about that one.
            if t.live
                .is_none_or(|generation| generation == update.generation)
            {
                let held = std::mem::take(&mut t.held);
                let last_pts = t.last_pts;
                *t = Table {
                    held,
                    last_pts,
                    ..Table::default()
                };
            }
            return;
        }
        if t.live != Some(update.generation) {
            // Another call: nothing of the last one is its picture.
            t.local = None;
            t.remote = None;
            t.members.clear();
        }
        t.live = Some(update.generation);
        if !update.camera_on {
            t.local = None;
        }
        if !update.remote_video {
            t.remote = None;
        }
        let participants = update
            .group
            .as_ref()
            .map(|g| g.participants.as_slice())
            .unwrap_or_default();
        t.order.clear();
        t.order
            .extend(participants.iter().map(|person| person.id.clone()));
        t.members.retain(|id, _| {
            participants
                .iter()
                .any(|person| person.video && &person.id == id)
        });
    }

    /// The newest frame of `stream`, held until `release`.
    pub fn acquire(&self, stream: u32) -> Result<Acquired, AcquireError> {
        let mut t = lock(&self.table);
        if t.held.iter().any(|(s, _)| *s == stream) {
            return Err(AcquireError::Held);
        }
        let frame = match stream {
            LOCAL => t.local.clone(),
            REMOTE => t.remote.clone(),
            member => t
                .order
                .get((member - MEMBER) as usize)
                .and_then(|id| t.members.get(id))
                .cloned(),
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
        lock(&self.table).held.retain(|(s, _)| *s != stream);
    }

    /// How many frames the caller holds (tests).
    pub fn held(&self) -> usize {
        lock(&self.table).held.len()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use zapfast::calls::{CallPhase, CallUpdate, GroupCall, Participant};

    fn picture(luma: u8) -> Arc<CallFrame> {
        let mut frame = CallFrame::new([4, 2]);
        frame.planes_mut().0.fill(luma);
        Arc::new(frame)
    }

    fn update(generation: u64, phase: CallPhase) -> CallUpdate {
        let mut u = CallUpdate::placing(generation, "1@s.whatsapp.net".into(), true);
        u.phase = phase;
        u.camera_on = true;
        u.remote_video = true;
        u
    }

    fn group(u: &mut CallUpdate, people: &[(&str, bool)]) {
        u.group = Some(GroupCall {
            caller: None,
            participants: people
                .iter()
                .map(|(id, video)| Participant {
                    id: (*id).into(),
                    joined: true,
                    video: *video,
                })
                .collect(),
            link: false,
        });
    }

    #[test]
    fn nothing_is_kept_before_a_call_is_live_or_after_it_ends() {
        let frames = Frames::default();
        frames.put(Some(picture(1)), Some(picture(2)), None);
        assert_eq!(frames.acquire(LOCAL).err(), Some(AcquireError::NotFound));
        frames.on_update(&update(1, CallPhase::Active));
        frames.put(Some(picture(1)), Some(picture(2)), None);
        assert_eq!(frames.acquire(REMOTE).unwrap().picture.data[0], 2);
        frames.release(REMOTE);
        frames.on_update(&update(1, CallPhase::Ended));
        assert_eq!(frames.acquire(LOCAL).err(), Some(AcquireError::NotFound));
        assert_eq!(frames.acquire(REMOTE).err(), Some(AcquireError::NotFound));
    }

    #[test]
    fn one_frame_a_stream_is_held_and_stays_valid_until_released() {
        let frames = Frames::default();
        frames.on_update(&update(1, CallPhase::Active));
        frames.put(Some(picture(1)), None, None);
        let first = frames.acquire(LOCAL).unwrap();
        assert_eq!(frames.acquire(LOCAL).err(), Some(AcquireError::Held));
        // Newer frames and the call's end leave the held one alone.
        frames.put(Some(picture(9)), None, None);
        frames.on_update(&update(1, CallPhase::Ended));
        assert_eq!(first.picture.data[0], 1);
        assert_eq!(frames.held(), 1);
        frames.release(LOCAL);
        frames.release(LOCAL);
        assert_eq!(frames.held(), 0);
    }

    #[test]
    fn acquire_returns_the_newest_frame_with_a_growing_timestamp() {
        let frames = Frames::default();
        frames.on_update(&update(1, CallPhase::Active));
        frames.put(None, Some(picture(1)), None);
        let a = frames.acquire(REMOTE).unwrap();
        frames.release(REMOTE);
        let again = frames.acquire(REMOTE).unwrap();
        frames.release(REMOTE);
        assert_eq!(a.pts_ns, again.pts_ns, "the same frame");
        frames.put(None, Some(picture(2)), None);
        let b = frames.acquire(REMOTE).unwrap();
        assert!(b.pts_ns > a.pts_ns);
        assert_eq!(b.picture.data[0], 2);
    }

    #[test]
    fn a_camera_that_stops_takes_its_frame_with_it() {
        let frames = Frames::default();
        frames.on_update(&update(1, CallPhase::Active));
        frames.put(Some(picture(1)), Some(picture(2)), None);
        let mut u = update(1, CallPhase::Active);
        u.camera_on = false;
        frames.on_update(&u);
        assert_eq!(frames.acquire(LOCAL).err(), Some(AcquireError::NotFound));
        assert!(frames.acquire(REMOTE).is_ok());
        frames.release(REMOTE);
        u.remote_video = false;
        frames.on_update(&u);
        assert_eq!(frames.acquire(REMOTE).err(), Some(AcquireError::NotFound));
    }

    #[test]
    fn members_are_streams_by_their_place_in_the_newest_roster() {
        let frames = Frames::default();
        let mut u = update(1, CallPhase::Active);
        group(&mut u, &[("a", true), ("b", true), ("c", false)]);
        frames.on_update(&u);
        frames.put(None, Some(picture(10)), Some("a".into()));
        frames.put(None, Some(picture(11)), Some("b".into()));
        assert_eq!(frames.acquire(MEMBER).unwrap().picture.data[0], 10);
        assert_eq!(frames.acquire(MEMBER + 1).unwrap().picture.data[0], 11);
        assert_eq!(
            frames.acquire(MEMBER + 2).err(),
            Some(AcquireError::NotFound)
        );
        assert_eq!(
            frames.acquire(MEMBER + 9).err(),
            Some(AcquireError::NotFound)
        );
        assert_eq!(frames.acquire(REMOTE).err(), Some(AcquireError::NotFound));
        frames.release(MEMBER);
        frames.release(MEMBER + 1);
        // b moves first and a stops its video: the picture follows b.
        group(&mut u, &[("b", true), ("a", false)]);
        frames.on_update(&u);
        assert_eq!(frames.acquire(MEMBER).unwrap().picture.data[0], 11);
        assert_eq!(
            frames.acquire(MEMBER + 1).err(),
            Some(AcquireError::NotFound)
        );
    }

    #[test]
    fn another_calls_end_leaves_the_live_one_alone() {
        let frames = Frames::default();
        frames.on_update(&update(2, CallPhase::Active));
        frames.put(Some(picture(1)), None, None);
        // A call refused as busy, under its own generation.
        frames.on_update(&update(3, CallPhase::Failed));
        assert!(frames.acquire(LOCAL).is_ok());
    }
}
