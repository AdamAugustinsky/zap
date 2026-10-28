//! The library's log: warnings and errors to stderr, which the app's own log
//! captures. `ZAPFAST_CALLS_LOG=info` (or `debug`) says more while a call is
//! investigated. The messages are upstream's call logs, which name call ids
//! and numbers only, never a key or a phone number.

use log::{LevelFilter, Log, Metadata, Record};

struct Stderr;

impl Log for Stderr {
    fn enabled(&self, metadata: &Metadata<'_>) -> bool {
        metadata.level() <= log::max_level()
    }

    fn log(&self, record: &Record<'_>) {
        if self.enabled(record.metadata()) {
            eprintln!("zfcalls {} {}: {}", record.level(), record.target(), record.args());
        }
    }

    fn flush(&self) {}
}

pub fn install() {
    static ONCE: std::sync::Once = std::sync::Once::new();
    ONCE.call_once(|| {
        let level = match std::env::var("ZAPFAST_CALLS_LOG").as_deref() {
            Ok("debug") => LevelFilter::Debug,
            Ok("info") => LevelFilter::Info,
            _ => LevelFilter::Warn,
        };
        if log::set_logger(&Stderr).is_ok() {
            log::set_max_level(level);
        }
    });
}
