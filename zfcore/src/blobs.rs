//! Bytes too large for a record: thumbnails, decoded pictures and voice PCM
//! the core hands out, and pixels and samples the UI puts in. A blob is read
//! in ranges; a command that names one takes it.
//!
//! Who frees what (`zfcore.h`, "Memory"):
//!
//! - A blob the caller puts (`zf_blob_put`) stays until a command takes it or
//!   the caller frees it.
//! - A blob the core makes for an event is the core's. The same bytes in
//!   several records (a message's thumbnail each time the message is sent
//!   again) are one blob under one id. The core keeps the event blobs touched
//!   last (made, sent again or read) up to `EVENT_BUDGET` bytes, and frees
//!   older ones itself (blobs over 1 MiB first), never one touched in the
//!   last `GRACE`. The caller may free one early with `zf_blob_free` once
//!   done with the record that named it; one id named by several records
//!   goes when each is freed, or when it expires.

use std::collections::HashMap;
use std::hash::{Hash, Hasher};
use std::sync::Mutex;
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{Duration, Instant};

use crate::wire::BlobRef;

/// The event blobs kept before the oldest are freed.
pub const EVENT_BUDGET: usize = 64 << 20;
/// How long an event blob is kept at least after it was last touched.
pub const GRACE: Duration = Duration::from_secs(30);
/// Event blobs up to this size are shared by content.
const SHARED_MAX: usize = 1 << 20;

struct Held {
    bytes: Vec<u8>,
    /// Made for events (false: put by the caller, never expired).
    event: bool,
    /// The records naming an event blob that have not been freed.
    refs: u32,
    /// When it was last touched, and in what order.
    at: Instant,
    tick: u64,
    /// The content key of a shared event blob.
    key: Option<u64>,
}

struct Inner {
    held: HashMap<u64, Held>,
    /// Shared event blobs by content key.
    by_key: HashMap<u64, u64>,
    event_bytes: usize,
    tick: u64,
}

pub struct Blobs {
    inner: Mutex<Inner>,
    next: AtomicU64,
    budget: usize,
    grace: Duration,
}

impl Default for Blobs {
    fn default() -> Self {
        Self::with_limits(EVENT_BUDGET, GRACE)
    }
}

#[derive(Debug, PartialEq, Eq)]
pub enum BlobError {
    NotFound,
    /// The offset is past the blob's end.
    Range,
}

fn content_key(bytes: &[u8]) -> u64 {
    let mut hasher = std::collections::hash_map::DefaultHasher::new();
    bytes.hash(&mut hasher);
    hasher.finish()
}

impl Inner {
    fn touch(&mut self, id: u64) {
        self.tick += 1;
        let tick = self.tick;
        if let Some(blob) = self.held.get_mut(&id) {
            blob.at = Instant::now();
            blob.tick = tick;
        }
    }

    fn remove(&mut self, id: u64) -> Option<Held> {
        let blob = self.held.remove(&id)?;
        if blob.event {
            self.event_bytes -= blob.bytes.len();
        }
        if let Some(key) = blob.key {
            self.by_key.remove(&key);
        }
        Some(blob)
    }
}

impl Blobs {
    pub fn with_limits(budget: usize, grace: Duration) -> Self {
        Self {
            inner: Mutex::new(Inner {
                held: HashMap::new(),
                by_key: HashMap::new(),
                event_bytes: 0,
                tick: 0,
            }),
            next: AtomicU64::new(0),
            budget,
            grace,
        }
    }

    fn insert(&self, inner: &mut Inner, bytes: Vec<u8>, event: bool, key: Option<u64>) -> BlobRef {
        let id = self.next.fetch_add(1, Ordering::Relaxed) + 1;
        let len = bytes.len() as u64;
        if event {
            inner.event_bytes += bytes.len();
        }
        inner.tick += 1;
        inner.held.insert(
            id,
            Held {
                bytes,
                event,
                refs: 1,
                at: Instant::now(),
                tick: inner.tick,
                key,
            },
        );
        if let Some(key) = key {
            inner.by_key.insert(key, id);
        }
        BlobRef { id, len }
    }

    /// Keeps bytes the caller put, until a command takes them or they are
    /// freed.
    pub fn put(&self, bytes: Vec<u8>) -> BlobRef {
        let mut inner = crate::wake::lock(&self.inner);
        self.insert(&mut inner, bytes, false, None)
    }

    /// Keeps bytes for an event record: the blob already holding the same
    /// bytes, when there is one, under its id.
    pub fn put_event(&self, bytes: Vec<u8>) -> BlobRef {
        let mut inner = crate::wake::lock(&self.inner);
        let key = (bytes.len() <= SHARED_MAX).then(|| content_key(&bytes));
        if let Some(key) = key
            && let Some(&id) = inner.by_key.get(&key)
            && let Some(blob) = inner.held.get_mut(&id)
            && blob.bytes == bytes
        {
            blob.refs += 1;
            let len = blob.bytes.len() as u64;
            inner.touch(id);
            return BlobRef { id, len };
        }
        // A different blob under the same key keeps it; this one goes unshared.
        let key = key.filter(|key| !inner.by_key.contains_key(key));
        let blob = self.insert(&mut inner, bytes, true, key);
        self.expire(&mut inner);
        blob
    }

    /// Frees event blobs until those kept are a quarter under the budget,
    /// sparing any touched within the grace: first the large ones (decoded
    /// pictures and sound, read once when their event arrives), then the
    /// rest, each kind touched longest ago first.
    fn expire(&self, inner: &mut Inner) {
        if inner.event_bytes <= self.budget {
            return;
        }
        let mut old: Vec<(bool, u64, u64)> = inner
            .held
            .iter()
            .filter(|(_, blob)| blob.event && blob.at.elapsed() >= self.grace)
            .map(|(&id, blob)| (blob.bytes.len() <= SHARED_MAX, blob.tick, id))
            .collect();
        old.sort_unstable();
        let target = self.budget - self.budget / 4;
        for (_, _, id) in old {
            if inner.event_bytes <= target {
                break;
            }
            inner.remove(id);
        }
    }

    /// Copies from `offset` into `dst`, returning how many bytes; 0 at the end.
    pub fn read(&self, id: u64, offset: u64, dst: &mut [u8]) -> Result<usize, BlobError> {
        let mut inner = crate::wake::lock(&self.inner);
        let bytes = &inner.held.get(&id).ok_or(BlobError::NotFound)?.bytes;
        let offset = usize::try_from(offset).map_err(|_| BlobError::Range)?;
        if offset > bytes.len() {
            return Err(BlobError::Range);
        }
        let n = dst.len().min(bytes.len() - offset);
        dst[..n].copy_from_slice(&bytes[offset..offset + n]);
        inner.touch(id);
        Ok(n)
    }

    pub fn len(&self, id: u64) -> Option<u64> {
        crate::wake::lock(&self.inner)
            .held
            .get(&id)
            .map(|b| b.bytes.len() as u64)
    }

    /// Removes the blob a command names; `blob.len` is the length the
    /// command says it has.
    pub fn take(&self, blob: BlobRef) -> Result<Vec<u8>, BlobError> {
        let mut inner = crate::wake::lock(&self.inner);
        match inner.held.get(&blob.id) {
            Some(held) if held.bytes.len() as u64 == blob.len => {
                Ok(inner.remove(blob.id).expect("present").bytes)
            }
            Some(_) => Err(BlobError::Range),
            None => Err(BlobError::NotFound),
        }
    }

    /// Frees a caller's blob, or one record's hold on an event blob.
    pub fn free(&self, id: u64) {
        let mut inner = crate::wake::lock(&self.inner);
        let Some(blob) = inner.held.get_mut(&id) else {
            return;
        };
        if blob.event && blob.refs > 1 {
            blob.refs -= 1;
            return;
        }
        inner.remove(id);
    }

    pub fn count(&self) -> usize {
        crate::wake::lock(&self.inner).held.len()
    }

    /// The bytes held for events (tests).
    pub fn event_bytes(&self) -> usize {
        crate::wake::lock(&self.inner).event_bytes
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_same_bytes_in_several_records_are_one_blob_freed_with_the_last() {
        let blobs = Blobs::default();
        let a = blobs.put_event(vec![1, 2, 3]);
        let b = blobs.put_event(vec![1, 2, 3]);
        let c = blobs.put_event(vec![4, 5]);
        assert_eq!(a, b);
        assert_ne!(a.id, c.id);
        assert_eq!(blobs.count(), 2);
        blobs.free(a.id);
        assert_eq!(blobs.len(a.id), Some(3), "the second record still names it");
        blobs.free(b.id);
        assert_eq!(blobs.len(a.id), None);
        assert_eq!(blobs.event_bytes(), 2);
    }

    #[test]
    fn a_callers_blob_is_never_shared_or_expired() {
        let blobs = Blobs::with_limits(4, Duration::ZERO);
        let mine = blobs.put(vec![9; 100]);
        let event = blobs.put_event(vec![9; 100]);
        assert_ne!(mine.id, event.id);
        blobs.put_event(vec![7; 100]);
        assert_eq!(blobs.len(mine.id), Some(100));
        assert_eq!(blobs.take(mine).unwrap().len(), 100);
    }

    #[test]
    fn past_the_budget_the_event_blobs_touched_longest_ago_go() {
        let blobs = Blobs::with_limits(300, Duration::ZERO);
        let first = blobs.put_event(vec![1; 100]);
        let second = blobs.put_event(vec![2; 100]);
        let third = blobs.put_event(vec![3; 100]);
        // Reading the first makes the second the oldest.
        blobs.read(first.id, 0, &mut [0; 1]).unwrap();
        blobs.put_event(vec![4; 100]);
        assert_eq!(blobs.len(second.id), None, "the oldest went");
        assert!(blobs.event_bytes() <= 225);
        assert_eq!(blobs.len(first.id), Some(100), "read since");
        assert_eq!(blobs.len(third.id), None);
    }

    #[test]
    fn a_large_blob_goes_before_small_ones_touched_earlier() {
        let blobs = Blobs::with_limits(SHARED_MAX + 300, Duration::ZERO);
        let small = blobs.put_event(vec![1; 100]);
        let large = blobs.put_event(vec![2; SHARED_MAX + 1]);
        blobs.put_event(vec![3; 300]);
        assert_eq!(blobs.len(large.id), None);
        assert_eq!(blobs.len(small.id), Some(100));
    }

    #[test]
    fn within_the_grace_nothing_expires() {
        let blobs = Blobs::with_limits(10, Duration::from_secs(60));
        let first = blobs.put_event(vec![1; 100]);
        blobs.put_event(vec![2; 100]);
        assert_eq!(blobs.len(first.id), Some(100));
    }
}
