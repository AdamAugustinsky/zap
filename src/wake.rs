//! How the core tells its interface that events are waiting.
//!
//! The backend, call audio and players hold a [`Waker`] and call
//! [`Waker::wake`] after each event they queue. The egui client's waker
//! repaints its window (`fastframe_shell::Waker`, converted with `From`);
//! another interface implements [`Wake`], for example with a [`WakePipe`]
//! whose read end it polls.

use std::sync::Arc;

/// Something that can be told, from any thread, that events are waiting.
pub trait Wake: Send + Sync + 'static {
    fn wake(&self);
}

impl<W: Wake> Wake for Arc<W> {
    fn wake(&self) {
        W::wake(self);
    }
}

/// A cloneable handle to a [`Wake`]. The default wakes nothing, for tests and
/// detached backends.
#[derive(Clone, Default)]
pub struct Waker(Option<Arc<dyn Wake>>);

impl Waker {
    pub fn new(wake: impl Wake) -> Self {
        Self(Some(Arc::new(wake)))
    }

    pub fn wake(&self) {
        if let Some(wake) = &self.0 {
            wake.wake();
        }
    }
}

impl std::fmt::Debug for Waker {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Waker")
            .field("attached", &self.0.is_some())
            .finish()
    }
}

#[cfg(feature = "ui")]
impl Wake for fastframe_shell::Waker {
    fn wake(&self) {
        fastframe_shell::Waker::wake(self);
    }
}

#[cfg(feature = "ui")]
impl From<fastframe_shell::Waker> for Waker {
    fn from(waker: fastframe_shell::Waker) -> Self {
        Self::new(waker)
    }
}

/// A wake that writes one byte to a socket while nothing is pending, so a
/// host can block on [`WakePipe::fd`] in its own poller.
///
/// Wakeups are never lost: [`Wake::wake`] writes only when it is the first
/// since the last [`WakePipe::rearm`]. The reader drains its queue until it is
/// empty, calls `rearm`, then checks the queue once more before it sleeps,
/// because an event queued between the last check and `rearm` wrote nothing.
#[cfg(unix)]
pub struct WakePipe {
    pending: std::sync::atomic::AtomicBool,
    reader: std::os::unix::net::UnixStream,
    writer: std::os::unix::net::UnixStream,
}

#[cfg(unix)]
impl WakePipe {
    pub fn new() -> std::io::Result<Self> {
        let (reader, writer) = std::os::unix::net::UnixStream::pair()?;
        reader.set_nonblocking(true)?;
        writer.set_nonblocking(true)?;
        Ok(Self {
            pending: std::sync::atomic::AtomicBool::new(false),
            reader,
            writer,
        })
    }

    /// The read end, non-blocking, readable while a wake is pending.
    pub fn fd(&self) -> std::os::fd::RawFd {
        std::os::fd::AsRawFd::as_raw_fd(&self.reader)
    }

    /// Empties the read end and lets the next wake write again.
    pub fn rearm(&self) {
        use std::io::Read;
        let mut buffer = [0u8; 64];
        while matches!((&self.reader).read(&mut buffer), Ok(read) if read > 0) {}
        self.pending
            .store(false, std::sync::atomic::Ordering::Release);
    }
}

#[cfg(unix)]
impl WakePipe {
    /// Writes the wake byte with `write`, again after an interrupted write. A
    /// full socket already holds a byte, which is all a reader needs; any
    /// other failure clears `pending`, so the next wake writes again.
    fn signal(&self, mut write: impl FnMut() -> std::io::Result<usize>) {
        loop {
            match write() {
                Ok(written) if written > 0 => return,
                Err(error) if error.kind() == std::io::ErrorKind::Interrupted => {}
                Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => return,
                result => {
                    log::error!("the wake pipe refused a write: {:?}", result.err());
                    self.pending
                        .store(false, std::sync::atomic::Ordering::Release);
                    return;
                }
            }
        }
    }
}

#[cfg(unix)]
impl Wake for WakePipe {
    fn wake(&self) {
        use std::io::Write;
        if !self.pending.swap(true, std::sync::atomic::Ordering::AcqRel) {
            self.signal(|| (&self.writer).write(&[1]));
        }
    }
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;

    /// The bytes waiting on the read end, taken.
    fn take(pipe: &WakePipe) -> usize {
        use std::io::Read;
        let mut buffer = [0u8; 64];
        match (&pipe.reader).read(&mut buffer) {
            Ok(read) => read,
            Err(error) => {
                assert_eq!(error.kind(), std::io::ErrorKind::WouldBlock);
                0
            }
        }
    }

    #[test]
    fn a_pipe_writes_once_until_it_is_rearmed() {
        let pipe = Arc::new(WakePipe::new().unwrap());
        let waker = Waker::new(pipe.clone());
        assert_eq!(take(&pipe), 0);
        waker.wake();
        waker.wake();
        assert_eq!(take(&pipe), 1, "one byte per wait");
        // Until the reader rearms, it is still draining and needs no byte.
        waker.wake();
        assert_eq!(take(&pipe), 0);
        pipe.rearm();
        assert_eq!(take(&pipe), 0);
        // A wake after rearming is not lost.
        waker.wake();
        assert_eq!(take(&pipe), 1);
    }

    #[test]
    fn an_interrupted_write_is_written_again() {
        use std::io::Write;
        let pipe = WakePipe::new().unwrap();
        pipe.pending
            .store(true, std::sync::atomic::Ordering::Release);
        let mut interruptions = 2;
        pipe.signal(|| {
            if interruptions > 0 {
                interruptions -= 1;
                return Err(std::io::ErrorKind::Interrupted.into());
            }
            (&pipe.writer).write(&[1])
        });
        assert_eq!(interruptions, 0);
        assert_eq!(take(&pipe), 1, "the wake reached the reader");
    }

    #[test]
    fn a_failed_write_lets_the_next_wake_write() {
        let pipe = Arc::new(WakePipe::new().unwrap());
        pipe.pending
            .store(true, std::sync::atomic::Ordering::Release);
        pipe.signal(|| Err(std::io::Error::other("refused")));
        assert_eq!(take(&pipe), 0);
        // Nothing was written, so the wake is not pending.
        Waker::new(pipe.clone()).wake();
        assert_eq!(take(&pipe), 1);
    }

    #[test]
    fn the_default_waker_wakes_nothing() {
        Waker::default().wake();
    }
}
