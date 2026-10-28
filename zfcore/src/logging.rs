//! The core's log (`zf_set_log`): the `log` records of the core and of
//! upstream's backend, one line each, written to a descriptor the host owns
//! (a duplicate of it, so the host's own lines and these share one offset
//! and never overwrite each other).
//!
//! A line reads as Go's `slog` text handler writes one:
//!
//! ```text
//! time=2026-10-21T04:35:40.123Z level=WARN source=core target=zapfast::backend::worker msg="…"
//! ```
//!
//! `level` applies to the core's and upstream's own targets (`zapfast…`) and
//! to the WhatsApp library's, whose lines are reduced to fixed categories as
//! upstream's app does (`diagnostics::protocol_summary`); other crates log
//! warnings and errors only. One log for the process: every core's records.

use std::io::Write as _;
use std::sync::Mutex;
use std::sync::atomic::{AtomicUsize, Ordering};

use log::{Level, LevelFilter, Log, Metadata, Record};

struct Logger;

static LOGGER: Logger = Logger;
static FILE: Mutex<Option<std::fs::File>> = Mutex::new(None);
/// The `LevelFilter` as `usize` (0 off .. 5 trace).
static LEVEL: AtomicUsize = AtomicUsize::new(0);

#[derive(Debug, PartialEq, Eq)]
pub enum SetLogError {
    /// Another logger owns the `log` facade in this process.
    Taken,
    /// The level is not 0 to 5.
    Level,
    /// The descriptor could not be duplicated.
    Io(String),
}

fn filter_of(n: usize) -> LevelFilter {
    LevelFilter::iter().nth(n).unwrap_or(LevelFilter::Off)
}

fn protocol(target: &str) -> bool {
    zapfast::diagnostics::is_protocol_target(target)
}

fn threshold(target: &str) -> LevelFilter {
    let level = filter_of(LEVEL.load(Ordering::Relaxed));
    if target.starts_with("zapfast") || protocol(target) {
        level
    } else {
        level.min(LevelFilter::Warn)
    }
}

/// Logs to `fd` (duplicated) from `level` (0 off, 1 error, 2 warn, 3 info,
/// 4 debug, 5 trace) up; a negative `fd` stops. Again later, it moves the
/// log or changes its level.
pub fn set(fd: i32, level: i32) -> Result<(), SetLogError> {
    let level = usize::try_from(level)
        .ok()
        .filter(|&n| n <= 5)
        .ok_or(SetLogError::Level)?;
    let file = if fd < 0 {
        None
    } else {
        use std::os::fd::BorrowedFd;
        // SAFETY: the caller passes an open descriptor for the call's
        // duration; only a duplicate is kept.
        let owned = unsafe { BorrowedFd::borrow_raw(fd) }
            .try_clone_to_owned()
            .map_err(|error| SetLogError::Io(error.to_string()))?;
        Some(std::fs::File::from(owned))
    };
    static INSTALLED: Mutex<bool> = Mutex::new(false);
    {
        let mut installed = crate::wake::lock(&INSTALLED);
        if !*installed {
            log::set_logger(&LOGGER).map_err(|_| SetLogError::Taken)?;
            *installed = true;
        }
    }
    let on = file.is_some();
    *crate::wake::lock(&FILE) = file;
    let level = if on { level } else { 0 };
    LEVEL.store(level, Ordering::Relaxed);
    log::set_max_level(filter_of(level));
    if on {
        // Whatever the level, so the log shows where the core's lines begin.
        write(
            Level::Info,
            "zapfast_core",
            &format!("the core logs here, from {}", filter_of(level)),
        );
    }
    Ok(())
}

/// One line, in one write, so the host's lines and ours interleave whole.
fn write(level: Level, target: &str, message: &str) {
    let line = format!(
        "time={} level={} source=core target={target} msg={}\n",
        utc_now(),
        match level {
            Level::Error => "ERROR",
            Level::Warn => "WARN",
            Level::Info => "INFO",
            Level::Debug | Level::Trace => "DEBUG",
        },
        quote(message),
    );
    if let Some(file) = crate::wake::lock(&FILE).as_mut() {
        let _ = file.write_all(line.as_bytes());
    }
}

impl Log for Logger {
    fn enabled(&self, metadata: &Metadata<'_>) -> bool {
        metadata.level() <= threshold(metadata.target())
    }

    fn log(&self, record: &Record<'_>) {
        if !self.enabled(record.metadata()) {
            return;
        }
        let mut message = record.args().to_string();
        if protocol(record.target()) || protocol(record.module_path().unwrap_or_default()) {
            message = zapfast::diagnostics::protocol_summary(&message).into_owned();
        }
        write(record.level(), record.target(), &message);
    }

    fn flush(&self) {}
}

/// `text` in double quotes, with quotes, backslashes and control characters
/// escaped, as Go's `strconv.Quote` writes them.
fn quote(text: &str) -> String {
    let mut out = String::with_capacity(text.len() + 2);
    out.push('"');
    for c in text.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            c if c.is_control() => out.push_str(&format!("\\u{:04x}", c as u32)),
            c => out.push(c),
        }
    }
    out.push('"');
    out
}

/// Now, in UTC, to the millisecond: `2026-10-21T04:35:40.123Z`.
fn utc_now() -> String {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default();
    utc(now.as_secs() as i64, now.subsec_millis())
}

fn utc(seconds: i64, millis: u32) -> String {
    let (days, rest) = (seconds.div_euclid(86_400), seconds.rem_euclid(86_400));
    // Howard Hinnant's civil_from_days.
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let day = doy - (153 * mp + 2) / 5 + 1;
    let month = if mp < 10 { mp + 3 } else { mp - 9 };
    let year = yoe + era * 400 + i64::from(month <= 2);
    format!(
        "{year:04}-{month:02}-{day:02}T{:02}:{:02}:{:02}.{millis:03}Z",
        rest / 3600,
        rest % 3600 / 60,
        rest % 60
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn times_and_quotes_read_as_go_writes_them() {
        assert_eq!(utc(0, 0), "1970-01-01T00:00:00.000Z");
        assert_eq!(utc(1_792_557_340, 123), "2026-10-21T04:35:40.123Z");
        assert_eq!(utc(951_782_400, 5), "2000-02-29T00:00:00.005Z");
        assert_eq!(quote("a \"b\"\n\\c"), r#""a \"b\"\n\\c""#);
    }

    // The only test that installs the logger: it is the process's.
    #[test]
    fn records_reach_the_descriptor_at_their_level_and_protocol_lines_are_summarised() {
        use std::os::fd::AsRawFd as _;
        let file = tempfile::NamedTempFile::new().unwrap();
        assert_eq!(set(file.as_file().as_raw_fd(), 9), Err(SetLogError::Level));
        set(file.as_file().as_raw_fd(), 2).unwrap();
        // Other tests log too, in parallel: these lines are told apart by
        // their targets.
        log::warn!(target: "zapfast::logtest", "could not \"file\" it");
        log::info!(target: "zapfast::logtest", "below the level");
        log::warn!(target: "some_crate::logtest", "another crate's warning");
        set(file.as_file().as_raw_fd(), 4).unwrap();
        log::debug!(target: "zapfast_core::logtest", "debug now");
        log::info!(target: "some_crate::logtest", "another crate's info stays out");
        log::warn!(target: "whatsapp_rust::logtest", "sent to 4915112345678@s.whatsapp.net");
        set(-1, 4).unwrap();
        log::error!(target: "zapfast_core::logtest", "after the log stopped");

        let text = std::fs::read_to_string(file.path()).unwrap();
        let lines: Vec<&str> = text
            .lines()
            .filter(|l| l.contains("logtest") || l.contains("the core logs here"))
            .collect();
        assert!(
            lines[0].contains(
                "level=INFO source=core target=zapfast_core msg=\"the core logs here, from WARN\""
            ),
            "{text}"
        );
        assert!(lines[0].starts_with("time=") && lines[0].contains("Z level="));
        assert!(
            lines[1].ends_with(
                r#"level=WARN source=core target=zapfast::logtest msg="could not \"file\" it""#
            ),
            "{text}"
        );
        assert!(
            lines[2].ends_with(r#"target=some_crate::logtest msg="another crate's warning""#),
            "{text}"
        );
        assert!(lines[3].contains("from DEBUG"), "{text}");
        assert!(
            lines[4].ends_with(
                r#"level=DEBUG source=core target=zapfast_core::logtest msg="debug now""#
            ),
            "{text}"
        );
        assert!(lines[5].contains("target=whatsapp_rust::logtest"), "{text}");
        assert!(!lines[5].contains("4915112345678"), "summarised: {text}");
        assert_eq!(lines.len(), 6, "{text}");
    }
}
