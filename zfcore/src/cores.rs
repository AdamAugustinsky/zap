//! One core per linked account: the event queue, the blobs, and the engine
//! behind them, which is upstream's `Backend` or the demo.

use std::sync::atomic::{AtomicBool, AtomicU32, Ordering};
use std::sync::{Arc, Mutex, Weak};
use std::thread::JoinHandle;

use zapfast::backend as up;

use crate::abi_gen as w;
use crate::blobs::Blobs;
use crate::wake::{Queue, Signal, lock};

/// What every thread of a core shares.
pub struct Shared {
    pub queue: Queue,
    pub blobs: Blobs,
    /// The call's video frames (`zf_frame_acquire`).
    pub frames: crate::frames::Frames,
    pub demo: bool,
    batches: AtomicU32,
    /// The cancel flags of calls being placed, by the UI's token. Past
    /// `CANCEL_FLAGS` the oldest one no call holds any more is forgotten.
    cancels: Mutex<Vec<(u64, Arc<AtomicBool>)>>,
    /// Pick requests that could not reach the UI, for the forwarder to
    /// answer with no paths so their dialogs stop waiting.
    refused_picks: Mutex<Vec<u64>>,
    /// The download folder `SetDownloadFolder` last named, where the
    /// worker files downloads instead of the cache.
    pub download_folder: Mutex<Option<std::path::PathBuf>>,
}

const CANCEL_FLAGS: usize = 16;

impl Shared {
    pub fn new(demo: bool) -> std::io::Result<Self> {
        Ok(Self {
            queue: Queue::new()?,
            blobs: Blobs::default(),
            frames: Default::default(),
            demo,
            batches: AtomicU32::new(0),
            cancels: Mutex::new(Vec::new()),
            refused_picks: Mutex::new(Vec::new()),
            download_folder: Mutex::new(None),
        })
    }

    /// Cancels the dialog `id`, whose request the UI never got.
    pub fn refuse_pick(&self, id: u64) {
        lock(&self.refused_picks).push(id);
    }

    fn take_refused_picks(&self) -> Vec<u64> {
        std::mem::take(&mut *lock(&self.refused_picks))
    }

    /// The flag the UI's `token` stands for, made on first use, whether by
    /// the call command or by an early `CancelCall`. A full table forgets
    /// its oldest flag that nothing else holds: one a queued call command or
    /// a call being placed still reads is never forgotten. `None` when all
    /// `CANCEL_FLAGS` are held so.
    pub fn cancel_flag(&self, token: u64) -> Option<Arc<AtomicBool>> {
        let mut flags = lock(&self.cancels);
        if let Some((_, flag)) = flags.iter().find(|(t, _)| *t == token) {
            return Some(flag.clone());
        }
        if flags.len() == CANCEL_FLAGS {
            let free = flags
                .iter()
                .position(|(_, flag)| Arc::strong_count(flag) == 1)?;
            flags.remove(free);
        }
        let flag = Arc::new(AtomicBool::new(false));
        flags.push((token, flag.clone()));
        Some(flag)
    }

    pub fn next_batch(&self) -> u32 {
        self.batches.fetch_add(1, Ordering::Relaxed) + 1
    }

    /// Converts and queues one upstream event.
    pub fn emit(&self, event: up::Event) {
        crate::dto::event(&mut crate::dto::Out::new(self), event);
    }

    /// Queues one of zfcore's own events.
    pub fn emit_record(&self, event: w::Event) {
        let mut record = Vec::new();
        if event.encode(&mut record).is_ok() {
            self.queue.push(record);
        }
    }
}

pub enum Engine {
    Demo(crate::demo::Demo),
    Real(Real),
}

pub struct Core {
    pub shared: Arc<Shared>,
    pub engine: Engine,
    pub poisoned: AtomicBool,
    started: AtomicBool,
    /// The account's own folders, the only ones `zf_voice_decode` reads.
    roots: Vec<std::path::PathBuf>,
}

#[derive(Debug)]
pub enum OpenError {
    Version(&'static str),
    Account,
    /// The keyring service is missing, upstream's, or not the one this
    /// process already uses.
    Keyring(&'static str),
    /// The device name is not the one this process already links under.
    DeviceName(&'static str),
    Io(std::io::Error),
}

#[derive(Debug)]
pub enum StartError {
    Twice,
}

impl Core {
    pub fn open(config: w::OpenConfig) -> Result<Arc<Self>, OpenError> {
        if config.abi != w::ABI {
            return Err(OpenError::Version(
                "the caller's ZF_ABI differs from this core's",
            ));
        }
        if config.schema_hash != w::SCHEMA_HASH {
            return Err(OpenError::Version(
                "the caller's schema hash differs from this core's",
            ));
        }
        let shared = Arc::new(Shared::new(config.demo).map_err(OpenError::Io)?);
        let roots;
        let engine = if config.demo {
            roots = vec![crate::demo::media_dir()];
            Engine::Demo(crate::demo::Demo::spawn(shared.clone()).map_err(OpenError::Io)?)
        } else {
            let account =
                zapfast::model::AccountId::parse(&config.account).ok_or(OpenError::Account)?;
            // This app's own keyring items, never upstream ZapFast's.
            if config.keyring_service.is_empty()
                || config.keyring_service == zapfast::archive::DEFAULT_KEYRING_SERVICE
            {
                return Err(OpenError::Keyring(
                    "a real core needs the app's own keyring service, not empty or upstream's",
                ));
            }
            if !zapfast::archive::set_keyring_service(&config.keyring_service) {
                return Err(OpenError::Keyring(
                    "this process already keeps archive keys under another keyring service",
                ));
            }
            if !config.device_name.is_empty()
                && !zapfast::backend::set_device_app_name(&config.device_name)
            {
                return Err(OpenError::DeviceName(
                    "this process already links under another device name",
                ));
            }
            let dirs = zapfast::paths::AppDirs {
                config: config.state_dir.clone(),
                state: config.state_dir,
                cache: config.cache_dir,
                runtime: std::env::temp_dir(),
            }
            .account(&account);
            roots = vec![dirs.state.clone(), dirs.cache.clone()];
            Engine::Real(Real::spawn(dirs, shared.clone()).map_err(OpenError::Io)?)
        };
        let core = Arc::new(Self {
            shared,
            engine,
            poisoned: AtomicBool::new(false),
            started: AtomicBool::new(false),
            roots,
        });
        register(&core);
        Ok(core)
    }

    /// The startup permit: migrations and the connection, after the UI's
    /// first frame.
    pub fn start(&self) -> Result<(), StartError> {
        if self.started.swap(true, Ordering::SeqCst) {
            return Err(StartError::Twice);
        }
        match &self.engine {
            Engine::Demo(demo) => demo.start(),
            Engine::Real(real) => {
                if let Some(permit) = lock(&real.backend).take_startup() {
                    let _ = permit.send(());
                }
            }
        }
        Ok(())
    }

    pub fn send(&self, cmd: w::Command) -> Result<(), crate::dto::Refused> {
        // Before conversion, which takes the blobs a command names.
        if matches!(self.engine, Engine::Demo(_))
            && crate::demo::disposition(&cmd) == crate::demo::Disposition::Refuses
        {
            return Err(crate::dto::Refused::Unsupported(cmd.name()));
        }
        match crate::dto::command(&self.shared, cmd)? {
            crate::dto::Routed::Upstream(command) => match &self.engine {
                Engine::Demo(demo) => demo.command(command),
                Engine::Real(real) => lock(&real.backend).send(command),
            },
            crate::dto::Routed::Done => {}
            crate::dto::Routed::Demo(injected) => match &self.engine {
                Engine::Demo(demo) => demo.inject(injected),
                Engine::Real(_) => return Err(crate::dto::Refused::DemoOnly),
            },
        }
        Ok(())
    }

    /// Decodes a voice note of the account's (`zf_voice_decode`), on the
    /// caller's thread.
    pub fn decode_voice(
        &self,
        path: &std::path::Path,
    ) -> Result<crate::voice::Decoded, crate::voice::VoiceError> {
        let mut roots = self.roots.clone();
        roots.extend(lock(&self.shared.download_folder).clone());
        crate::voice::decode(path, &roots, &self.shared.blobs)
    }

    /// Stops the engine's threads; blocks until they have finished.
    pub fn shutdown(&self) {
        match &self.engine {
            Engine::Demo(demo) => demo.stop(),
            Engine::Real(real) => real.stop(),
        }
    }
}

/// Upstream's backend, its events forwarded into the queue by a thread of
/// ours that the backend's waker wakes.
pub struct Real {
    backend: Arc<Mutex<up::Backend>>,
    signal: Arc<Signal>,
    forwarder: Mutex<Option<JoinHandle<()>>>,
}

impl Real {
    fn spawn(dirs: zapfast::paths::AccountDirs, shared: Arc<Shared>) -> std::io::Result<Self> {
        let signal = Arc::new(Signal::default());
        // Early: a ready archive's chats arrive before `zf_core_start`.
        let backend = Arc::new(Mutex::new(up::Backend::spawn_early(
            dirs,
            zapfast::wake::Waker::new(signal.clone()),
        )));
        let forwarder = {
            let backend = backend.clone();
            let signal = signal.clone();
            std::thread::Builder::new()
                .name("zf-events".into())
                .spawn(move || {
                    crate::owned_thread(&shared, "events", || {
                        loop {
                            let open = signal.wait();
                            let events = lock(&backend).poll();
                            for event in events {
                                shared.emit(event);
                            }
                            for id in shared.take_refused_picks() {
                                lock(&backend).send(up::Command::PickAnswer {
                                    id,
                                    paths: Vec::new(),
                                });
                            }
                            if !open {
                                return;
                            }
                        }
                    });
                })?
        };
        Ok(Self {
            backend,
            signal,
            forwarder: Mutex::new(Some(forwarder)),
        })
    }

    fn stop(&self) {
        // Joins the backend thread, then lets the forwarder drain once more.
        // The lock is held only by this call and short `send`/`poll`s.
        let mut backend = std::mem::replace(&mut *lock(&self.backend), up::Backend::detached().0);
        backend.shutdown();
        *lock(&self.backend) = backend;
        self.signal.close();
        if let Some(forwarder) = lock(&self.forwarder).take() {
            let _ = forwarder.join();
        }
    }
}

/// Live cores, so a panic on a thread we do not own (upstream's runtime) can
/// reach them as `Fatal`.
static CORES: Mutex<Vec<Weak<Core>>> = Mutex::new(Vec::new());

fn register(core: &Arc<Core>) {
    let mut cores = lock(&CORES);
    cores.retain(|c| c.strong_count() > 0);
    cores.push(Arc::downgrade(core));
}

/// Tells every real core that one of upstream's threads panicked. Upstream's
/// own build aborts on any panic, so none of them promises to recover; with
/// several accounts the thread cannot be attributed and each is told.
pub fn fatal_to_real_cores(thread: &str, location: &str) {
    let cores: Vec<_> = lock(&CORES).iter().filter_map(Weak::upgrade).collect();
    for core in cores {
        if matches!(core.engine, Engine::Real(_)) {
            core.shared.emit_record(w::Event::Fatal {
                origin: "backend".into(),
                thread: thread.into(),
                location: location.into(),
            });
        }
    }
}
