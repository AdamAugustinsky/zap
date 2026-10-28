//! A real (non-demo) core through the C entry points, on a temporary
//! account: the cached chat list at open, and closing with a dialog that
//! nobody answers. Archive keys stay in an in-memory keyring and the
//! connection goes to a closed local port, so nothing leaves this process.

use std::path::Path;
use std::time::{Duration, Instant};

use crate::abi_gen as w;
use crate::{ZF_EMPTY, ZF_OK, zf_core, zf_err};

/// The keyring service the tests' cores name: never upstream's.
const SERVICE: &str = "ai.a3lab.zap.test";

/// The in-memory keyring every test's archive key is kept in.
static KEYS: std::sync::OnceLock<std::sync::Arc<keyring_core::mock::Store>> =
    std::sync::OnceLock::new();

fn isolated() {
    static ONCE: std::sync::Once = std::sync::Once::new();
    ONCE.call_once(|| {
        let keys = keyring_core::mock::Store::new().unwrap();
        assert!(zapfast::archive::use_credential_store(keys.clone()));
        KEYS.set(keys).unwrap();
        zapfast::proxy::configure("socks5://127.0.0.1:9");
    });
}

fn no_err() -> zf_err {
    zf_err {
        code: 0,
        len: 0,
        msg: [0; 256],
    }
}

fn message(err: &zf_err) -> String {
    String::from_utf8_lossy(&err.msg[..err.len as usize]).into_owned()
}

fn archive_path(state: &Path) -> std::path::PathBuf {
    zapfast::paths::AppDirs {
        config: state.to_owned(),
        state: state.to_owned(),
        cache: state.join("cache"),
        runtime: std::env::temp_dir(),
    }
    .account(&zapfast::model::AccountId::parse("1").unwrap())
    .archive_db()
}

fn open(state: &Path) -> *mut zf_core {
    let cfg = crate::wire::encode_bare(&w::OpenConfig {
        abi: w::ABI,
        schema_hash: w::SCHEMA_HASH,
        demo: false,
        state_dir: state.to_owned(),
        cache_dir: state.join("cache"),
        account: "1".into(),
        keyring_service: SERVICE.into(),
        device_name: String::new(),
    })
    .unwrap();
    let mut core = std::ptr::null_mut();
    let mut err = no_err();
    // SAFETY: a valid configuration, result pointer and error for the call.
    let code = unsafe { crate::zf_core_open(cfg.as_ptr(), cfg.len(), &mut core, &mut err) };
    assert_eq!(code, ZF_OK, "{}", message(&err));
    core
}

/// Drains the core until `done` holds, waiting on its fd: the time it took,
/// or `None` once `limit` passes.
fn seen_within(
    core: *mut zf_core,
    limit: Duration,
    mut done: impl FnMut(&w::Event) -> bool,
) -> Option<Duration> {
    let start = Instant::now();
    let mut dst = vec![0u8; w::MAX_RECORD];
    // SAFETY: an open core.
    let fd = unsafe { crate::zf_event_fd(core) };
    loop {
        let left = limit.saturating_sub(start.elapsed());
        if left.is_zero() {
            return None;
        }
        let mut pfd = libc::pollfd {
            fd,
            events: libc::POLLIN,
            revents: 0,
        };
        // SAFETY: polls one descriptor of the open core.
        unsafe { libc::poll(&mut pfd, 1, left.as_millis().max(1) as i32) };
        loop {
            let mut len = 0;
            let mut err = no_err();
            // SAFETY: `dst` holds ZF_MAX_RECORD bytes.
            let code = unsafe {
                crate::zf_next_event(core, dst.as_mut_ptr(), dst.len(), &mut len, &mut err)
            };
            if code == ZF_EMPTY {
                break;
            }
            assert_eq!(code, ZF_OK, "{}", message(&err));
            if done(&w::Event::decode(&dst[..len]).unwrap()) {
                return Some(start.elapsed());
            }
        }
    }
}

/// Like `seen_within`, but fails the test after `limit`.
fn wait_for(core: *mut zf_core, limit: Duration, done: impl FnMut(&w::Event) -> bool) {
    assert!(
        seen_within(core, limit, done).is_some(),
        "the event did not arrive within {limit:?}"
    );
}

/// Collects one whole chat list into `names`; true at its last chunk.
fn chat_list(names: &mut Vec<String>) -> impl FnMut(&w::Event) -> bool + '_ {
    |event| match event {
        w::Event::Chats { chats, last, .. } => {
            names.extend(chats.iter().map(|chat| chat.name.clone()));
            *last
        }
        _ => false,
    }
}

fn send(core: *mut zf_core, command: w::Command) {
    let mut record = Vec::new();
    command.encode(&mut record).unwrap();
    let mut err = no_err();
    // SAFETY: an open core and a whole record.
    let code = unsafe { crate::zf_send(core, record.as_ptr(), record.len(), &mut err) };
    assert_eq!(code, ZF_OK, "{}", message(&err));
}

fn close(core: *mut zf_core) -> Duration {
    let start = Instant::now();
    let mut err = no_err();
    // SAFETY: the core's last use.
    let code = unsafe { crate::zf_core_close(core, 10_000, &mut err) };
    assert_eq!(code, ZF_OK, "{}", message(&err));
    start.elapsed()
}

const CHATS: usize = 500;

fn expected() -> Vec<String> {
    (0..CHATS).map(|n| format!("Chat {n}")).collect()
}

/// An account whose archive holds `CHATS` chats, opened and prepared by this
/// build, as an account in use is. Returns the archive's path.
fn seeded(state: &Path) -> std::path::PathBuf {
    let path = archive_path(state);
    let archive = zapfast::archive::Archive::open(&path).unwrap();
    // An archive whose lock state is known, as any in use is: until it is,
    // upstream keeps chats off the interface.
    archive
        .set_meta("chat_privacy_ready_v1", "complete")
        .unwrap();
    // 500 chats, as the startup budget's fixture has: more than one chunk.
    for n in 0..CHATS {
        let id = if n % 10 == 9 {
            format!("1203630000{n:08}@g.us")
        } else {
            format!("3933{n:08}@s.whatsapp.net")
        };
        let mut chat = zapfast::model::Chat::new(id, format!("Chat {n}"));
        chat.last_activity = 1_000_000 - n as i64;
        archive.upsert_chat(&chat).unwrap();
    }
    path
}

/// A connection of our own to the archive at `path`, unlocked with its key.
fn writer(path: &Path) -> rusqlite::Connection {
    use keyring_core::api::CredentialStoreApi;
    let identity = zapfast::archive::archive_key_identity(path).unwrap();
    let key = KEYS
        .get()
        .unwrap()
        .build(SERVICE, &identity, None)
        .unwrap()
        .get_secret()
        .unwrap();
    let hex: String = key.iter().map(|byte| format!("{byte:02x}")).collect();
    let connection = rusqlite::Connection::open(path).unwrap();
    connection
        .pragma_update(None, "key", format!("x'{hex}'"))
        .unwrap();
    connection
}

/// The archive's content: its file and whatever its write-ahead log holds.
/// A reader may create an empty log and the shared-memory index (`-shm`),
/// which hold no data.
fn files(path: &Path) -> (Vec<u8>, Vec<u8>) {
    let wal = std::fs::read(format!("{}-wal", path.display())).unwrap_or_default();
    (std::fs::read(path).unwrap(), wal)
}

fn start(core: *mut zf_core) {
    let mut err = no_err();
    // SAFETY: an open core.
    assert_eq!(unsafe { crate::zf_core_start(core, &mut err) }, ZF_OK);
}

#[test]
fn a_seeded_archive_lists_its_chats_at_open_before_start() {
    isolated();
    let state = tempfile::tempdir().unwrap();
    let path = seeded(state.path());
    let before = files(&path);
    let opening = Instant::now();
    let core = open(state.path());
    // No zf_core_start: the list comes from the archive alone.
    let mut names = Vec::new();
    wait_for(core, Duration::from_secs(10), chat_list(&mut names));
    let took = opening.elapsed();
    assert_eq!(names, expected(), "every chat, newest first");
    assert_eq!(
        files(&path),
        before,
        "nothing was written before the permit"
    );
    eprintln!("zf_core_open -> last chat-list chunk (seeded archive, open included): {took:?}");
    close(core);
}

#[test]
fn an_archive_prepared_by_an_older_build_waits_for_start() {
    isolated();
    let state = tempfile::tempdir().unwrap();
    let path = seeded(state.path());
    // Another build's schema: preparing it writes, so it waits.
    zapfast::archive::Archive::open(&path)
        .unwrap()
        .set_meta("schema_prepared", "an older schema")
        .unwrap();
    let before = files(&path);
    let core = open(state.path());
    let mut names = Vec::new();
    assert_eq!(
        seen_within(core, Duration::from_millis(500), chat_list(&mut names)),
        None,
        "no chats before the permit: skeleton rows"
    );
    assert_eq!(
        files(&path),
        before,
        "nothing was written before the permit"
    );
    start(core);
    names.clear();
    wait_for(core, Duration::from_secs(10), chat_list(&mut names));
    assert_eq!(names, expected());
    close(core);
    // Prepared once the permit came: the next open is early.
    let core = open(state.path());
    names.clear();
    wait_for(core, Duration::from_secs(10), chat_list(&mut names));
    close(core);
}

#[test]
fn another_writer_holding_the_archive_does_not_delay_the_list() {
    isolated();
    let state = tempfile::tempdir().unwrap();
    let path = seeded(state.path());
    let other = writer(&path);
    other.execute_batch("BEGIN IMMEDIATE").unwrap();
    let core = open(state.path());
    let mut names = Vec::new();
    // Well inside SQLite's 5 s busy timeout, which a write would wait out.
    let took = seen_within(core, Duration::from_secs(3), chat_list(&mut names));
    assert!(took.is_some(), "the list waited for the writer lock");
    assert_eq!(names, expected());
    other.execute_batch("ROLLBACK").unwrap();
    close(core);
}

#[test]
fn a_new_account_lists_nothing_and_touches_no_storage_before_start() {
    isolated();
    let state = tempfile::tempdir().unwrap();
    let core = open(state.path());
    std::thread::sleep(Duration::from_millis(200));
    assert!(!archive_path(state.path()).exists());
    close(core);
    assert!(!archive_path(state.path()).exists());
}

#[test]
fn closing_answers_a_dialog_nobody_answered() {
    isolated();
    let state = tempfile::tempdir().unwrap();
    let core = open(state.path());
    start(core);
    send(core, w::Command::PickDownloadFolder);
    wait_for(core, Duration::from_secs(20), |event| {
        matches!(
            event,
            w::Event::PickRequest {
                kind: w::PickKind::Folder,
                ..
            }
        )
    });
    // A dialog still waiting would hold the runtime's shutdown for its full
    // three seconds and leave the thread behind.
    let took = close(core);
    assert!(
        took < Duration::from_millis(2500),
        "closing took {took:?}: the dialog kept a thread waiting"
    );
}

#[test]
fn the_log_is_handed_to_the_ui_to_open_never_opened_by_the_core() {
    isolated();
    let state = tempfile::tempdir().unwrap();
    let log = state.path().join("zapfast.log");
    let core = open(state.path());
    start(core);
    send(core, w::Command::OpenLog { path: log.clone() });
    wait_for(
        core,
        Duration::from_secs(20),
        |event| matches!(event, w::Event::OpenPath { path } if *path == log),
    );
    close(core);
}

#[test]
fn a_real_core_refuses_upstreams_keyring_service_and_an_empty_one() {
    isolated();
    let state = tempfile::tempdir().unwrap();
    for service in [zapfast::archive::DEFAULT_KEYRING_SERVICE, ""] {
        let cfg = crate::wire::encode_bare(&w::OpenConfig {
            abi: w::ABI,
            schema_hash: w::SCHEMA_HASH,
            demo: false,
            state_dir: state.path().to_owned(),
            cache_dir: state.path().join("cache"),
            account: "1".into(),
            keyring_service: service.into(),
            device_name: String::new(),
        })
        .unwrap();
        let mut core = std::ptr::null_mut();
        let mut err = no_err();
        // SAFETY: a valid configuration, result pointer and error for the call.
        let code = unsafe { crate::zf_core_open(cfg.as_ptr(), cfg.len(), &mut core, &mut err) };
        assert_eq!(code, crate::ZF_E_ARG, "{service:?}: {}", message(&err));
        assert!(core.is_null());
    }
    assert!(!archive_path(state.path()).exists());
}
