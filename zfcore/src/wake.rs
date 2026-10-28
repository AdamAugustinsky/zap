//! The event queue and its wake pipe.
//!
//! Records wait in a queue; the read end of a non-blocking pipe is readable
//! while any wait. A push writes one byte only when `pending` was clear, and
//! a drain that finds the queue empty empties the pipe, clears `pending`,
//! and looks once more, so no wakeup is ever lost and at most one byte is
//! ever in the pipe.

use std::collections::VecDeque;
use std::os::fd::{AsRawFd, FromRawFd, OwnedFd, RawFd};
use std::sync::Mutex;
use std::sync::atomic::{AtomicBool, Ordering};

pub struct Queue {
    records: Mutex<VecDeque<Vec<u8>>>,
    pending: AtomicBool,
    read: OwnedFd,
    write: OwnedFd,
}

/// What a drain did.
#[derive(Debug, PartialEq, Eq)]
pub enum Drained {
    /// `records` whole records, `len` bytes of `dst`, were written.
    Some {
        records: u32,
        len: usize,
    },
    Empty,
}

impl Queue {
    pub fn new() -> std::io::Result<Self> {
        let mut fds = [0 as RawFd; 2];
        // SAFETY: `fds` has room for the two descriptors pipe() writes.
        if unsafe { libc::pipe(fds.as_mut_ptr()) } != 0 {
            return Err(std::io::Error::last_os_error());
        }
        // SAFETY: pipe() just returned these descriptors, owned by no one else.
        let (read, write) = unsafe { (OwnedFd::from_raw_fd(fds[0]), OwnedFd::from_raw_fd(fds[1])) };
        for fd in [read.as_raw_fd(), write.as_raw_fd()] {
            set_flags(fd)?;
        }
        Ok(Self {
            records: Mutex::new(VecDeque::new()),
            pending: AtomicBool::new(false),
            read,
            write,
        })
    }

    pub fn fd(&self) -> RawFd {
        self.read.as_raw_fd()
    }

    /// Queues one encoded record. The caller has already checked it against
    /// `MAX_RECORD`.
    pub fn push(&self, record: Vec<u8>) {
        debug_assert!(record.len() <= crate::abi_gen::MAX_RECORD);
        lock(&self.records).push_back(record);
        if !self.pending.swap(true, Ordering::SeqCst) {
            self.signal();
        }
    }

    fn signal(&self) {
        let byte = 1u8;
        loop {
            // SAFETY: writes one byte from a live local to our own descriptor.
            let n = unsafe { libc::write(self.write.as_raw_fd(), (&raw const byte).cast(), 1) };
            if n == 1 {
                return;
            }
            let error = std::io::Error::last_os_error();
            match error.kind() {
                std::io::ErrorKind::Interrupted => continue,
                // Full: a byte is already waiting, which is all a wake needs.
                std::io::ErrorKind::WouldBlock => return,
                _ => {
                    log::error!("zapfast_core: the wake pipe refused a write: {error}");
                    return;
                }
            }
        }
    }

    fn clear_pipe(&self) {
        let mut sink = [0u8; 64];
        loop {
            // SAFETY: reads into a live local buffer of the length given.
            let n =
                unsafe { libc::read(self.read.as_raw_fd(), sink.as_mut_ptr().cast(), sink.len()) };
            if n > 0 {
                continue;
            }
            if n < 0 && std::io::Error::last_os_error().kind() == std::io::ErrorKind::Interrupted {
                continue;
            }
            return;
        }
    }

    /// Moves whole records into `dst`, each after a little-endian `u32`
    /// length, until the next would not fit. With `one`, at most one record
    /// and no length prefix. `dst` must hold the largest record (and its
    /// prefix), so every call that finds a record makes progress.
    pub fn drain(&self, dst: &mut [u8], one: bool) -> Drained {
        let mut records = lock(&self.records);
        if records.is_empty() {
            drop(records);
            self.clear_pipe();
            self.pending.store(false, Ordering::SeqCst);
            records = lock(&self.records);
            if records.is_empty() {
                return Drained::Empty;
            }
            // A record arrived between the look and the clear; its push saw
            // `pending` set and wrote nothing, so this drain takes it, and the
            // pipe is signalled again for whatever stays queued.
        }
        let mut len = 0;
        let mut count = 0u32;
        while let Some(record) = records.front() {
            let need = if one { record.len() } else { 4 + record.len() };
            if need > dst.len() - len {
                break;
            }
            if !one {
                dst[len..len + 4].copy_from_slice(&(record.len() as u32).to_le_bytes());
                len += 4;
            }
            dst[len..len + record.len()].copy_from_slice(record);
            len += record.len();
            count += 1;
            records.pop_front();
            if one {
                break;
            }
        }
        let left = !records.is_empty();
        drop(records);
        if left && !self.pending.swap(true, Ordering::SeqCst) {
            self.signal();
        }
        Drained::Some {
            records: count,
            len,
        }
    }
}

fn set_flags(fd: RawFd) -> std::io::Result<()> {
    // SAFETY: fcntl on a descriptor we own, with integer arguments only.
    unsafe {
        let flags = libc::fcntl(fd, libc::F_GETFL);
        if flags < 0 || libc::fcntl(fd, libc::F_SETFL, flags | libc::O_NONBLOCK) < 0 {
            return Err(std::io::Error::last_os_error());
        }
        let fdflags = libc::fcntl(fd, libc::F_GETFD);
        if fdflags < 0 || libc::fcntl(fd, libc::F_SETFD, fdflags | libc::FD_CLOEXEC) < 0 {
            return Err(std::io::Error::last_os_error());
        }
    }
    Ok(())
}

pub fn lock<T>(mutex: &Mutex<T>) -> std::sync::MutexGuard<'_, T> {
    mutex
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
}

/// The backend's waker: upstream's threads call [`Signal::wake`] after each
/// event they queue, and the forwarding thread waits on it, then drains the
/// backend. A wake that lands while the forwarder drains is kept for its next
/// wait, so none is lost.
#[derive(Default)]
pub struct Signal {
    state: Mutex<(bool, bool)>, // (woken, closing)
    ring: std::sync::Condvar,
}

impl Signal {
    pub fn close(&self) {
        lock(&self.state).1 = true;
        self.ring.notify_all();
    }

    /// Waits for a wake; false once closing.
    pub fn wait(&self) -> bool {
        let mut state = lock(&self.state);
        while !state.0 && !state.1 {
            state = self
                .ring
                .wait(state)
                .unwrap_or_else(std::sync::PoisonError::into_inner);
        }
        state.0 = false;
        !state.1
    }
}

impl zapfast::wake::Wake for Signal {
    fn wake(&self) {
        lock(&self.state).0 = true;
        self.ring.notify_all();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn readable(q: &Queue) -> bool {
        let mut b = [0u8; 1];
        // SAFETY: peeks one byte into a live local.
        let n = unsafe { libc::recv(q.fd(), b.as_mut_ptr().cast(), 1, libc::MSG_PEEK) };
        if n == 1 {
            return true;
        }
        let mut pfd = libc::pollfd {
            fd: q.fd(),
            events: libc::POLLIN,
            revents: 0,
        };
        // SAFETY: polls one descriptor without waiting.
        unsafe { libc::poll(&mut pfd, 1, 0) == 1 }
    }

    #[test]
    fn a_wake_before_the_wait_is_not_lost() {
        let signal = std::sync::Arc::new(Signal::default());
        let waker = zapfast::wake::Waker::new(signal.clone());
        waker.wake();
        assert!(signal.wait(), "the earlier wake returns at once");
        let waiting = {
            let signal = signal.clone();
            std::thread::spawn(move || signal.wait())
        };
        waker.wake();
        assert!(waiting.join().unwrap());
        signal.close();
        assert!(!signal.wait());
    }

    #[test]
    fn a_push_after_an_empty_drain_wakes_again() {
        let q = Queue::new().unwrap();
        let mut dst = vec![0u8; 64];
        assert!(!readable(&q));
        q.push(vec![1, 2, 3]);
        q.push(vec![4]);
        assert!(readable(&q));
        assert_eq!(
            q.drain(&mut dst, false),
            Drained::Some {
                records: 2,
                len: 12
            }
        );
        assert_eq!(&dst[..12], &[3, 0, 0, 0, 1, 2, 3, 1, 0, 0, 0, 4]);
        // Still readable until a drain finds nothing.
        assert_eq!(q.drain(&mut dst, false), Drained::Empty);
        assert!(!readable(&q));
        q.push(vec![9]);
        assert!(readable(&q));
    }

    #[test]
    fn records_that_do_not_fit_stay_queued_and_keep_the_pipe_readable() {
        let q = Queue::new().unwrap();
        q.push(vec![0; 10]);
        q.push(vec![0; 10]);
        let mut dst = vec![0u8; 20];
        assert_eq!(
            q.drain(&mut dst, false),
            Drained::Some {
                records: 1,
                len: 14
            }
        );
        assert!(readable(&q));
        assert_eq!(
            q.drain(&mut dst, true),
            Drained::Some {
                records: 1,
                len: 10
            }
        );
        assert_eq!(q.drain(&mut dst, true), Drained::Empty);
    }

    #[test]
    fn concurrent_pushes_are_never_lost() {
        let q = std::sync::Arc::new(Queue::new().unwrap());
        let producers: Vec<_> = (0..4)
            .map(|p| {
                let q = q.clone();
                std::thread::spawn(move || {
                    for i in 0..5000u32 {
                        q.push((p * 100_000 + i).to_le_bytes().to_vec());
                    }
                })
            })
            .collect();
        let mut seen = 0;
        let mut dst = vec![0u8; 4096];
        while seen < 20_000 {
            let mut pfd = libc::pollfd {
                fd: q.fd(),
                events: libc::POLLIN,
                revents: 0,
            };
            // SAFETY: polls one descriptor; a lost wakeup would hang here.
            let ready = unsafe { libc::poll(&mut pfd, 1, 5000) };
            assert_eq!(ready, 1, "the pipe stayed silent with {seen} records seen");
            while let Drained::Some { records, .. } = q.drain(&mut dst, false) {
                seen += records;
            }
        }
        for p in producers {
            p.join().unwrap();
        }
        assert_eq!(q.drain(&mut dst, false), Drained::Empty);
    }
}
