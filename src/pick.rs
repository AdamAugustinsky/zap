//! The backend's file dialogs.
//!
//! The worker builds a dialog as it would build an `rfd::FileDialog` and
//! waits for the answer off the runtime. The egui client shows it with rfd.
//! Another interface, or a build without `ui`, gets the dialog as an
//! [`Event::PickRequest`] and answers with [`Command::PickAnswer`]; the
//! waiting thread then carries on as it would after rfd returned. A dialog
//! still waiting when its backend shuts down is answered with no paths.
//!
//! [`Command::PickAnswer`]: crate::backend::Command::PickAnswer

use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex, MutexGuard, mpsc};

use crate::backend::Event;
use crate::wake::Waker;

/// What a dialog asks for.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Kind {
    /// One existing file.
    #[default]
    File,
    /// One or more existing files.
    Files,
    /// One folder.
    Folder,
    /// A new file's name and place.
    Save,
}

/// A file dialog for the interface to show. Every request is answered once
/// with [`Dialogs::answer`], with no paths when the reader cancels it.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Request {
    pub id: u64,
    pub kind: Kind,
    pub title: String,
    /// A name and the extensions it allows, without dots.
    pub filters: Vec<(String, Vec<String>)>,
    /// The suggested name, for [`Kind::Save`].
    pub file_name: Option<String>,
    /// Where the dialog starts.
    pub directory: Option<PathBuf>,
}

static HOST: AtomicBool = AtomicBool::new(false);
static NEXT: AtomicU64 = AtomicU64::new(1);

/// Sends every dialog to the interface as an [`Event::PickRequest`] from now
/// on, even in a build with rfd. A build without `ui` always does.
pub fn use_host_dialogs() {
    HOST.store(true, Ordering::Release);
}

fn host_dialogs() -> bool {
    !cfg!(feature = "ui") || HOST.load(Ordering::Acquire)
}

/// The dialogs waiting on one backend's interface. The backend owns it and
/// closes it when it shuts down or is dropped: every waiting dialog is then
/// answered with no paths, and so is every dialog asked afterwards, so no
/// thread is left waiting on an interface that has gone.
#[derive(Default)]
pub struct Dialogs(Arc<Mutex<Waiting>>);

#[derive(Default)]
struct Waiting {
    closed: bool,
    answers: HashMap<u64, mpsc::Sender<Vec<PathBuf>>>,
}

fn lock(waiting: &Mutex<Waiting>) -> MutexGuard<'_, Waiting> {
    waiting.lock().unwrap_or_else(|p| p.into_inner())
}

impl Dialogs {
    /// Opens dialogs whose requests go out as `events`.
    pub fn picker(&self, events: mpsc::Sender<Event>, waker: Waker) -> Picker {
        Picker {
            events,
            waker,
            host: host_dialogs(),
            waiting: self.0.clone(),
        }
    }

    /// Answers the request `id` with the paths chosen, none when cancelled.
    /// An unknown or already answered id is ignored.
    pub fn answer(&self, id: u64, paths: Vec<PathBuf>) {
        let answer = lock(&self.0).answers.remove(&id);
        if let Some(answer) = answer {
            let _ = answer.send(paths);
        }
    }

    /// Answers every waiting dialog with no paths, and every later one at once.
    pub fn close(&self) {
        let answers = {
            let mut waiting = lock(&self.0);
            waiting.closed = true;
            std::mem::take(&mut waiting.answers)
        };
        // A dropped sender ends its dialog's wait with no paths.
        drop(answers);
    }

    #[cfg(test)]
    fn waiting(&self) -> usize {
        lock(&self.0).answers.len()
    }
}

impl Drop for Dialogs {
    fn drop(&mut self) {
        self.close();
    }
}

/// Opens dialogs for one backend, whose events carry its requests.
#[derive(Clone)]
pub struct Picker {
    events: mpsc::Sender<Event>,
    waker: Waker,
    host: bool,
    waiting: Arc<Mutex<Waiting>>,
}

impl Picker {
    /// Sends this picker's dialogs to the interface even in a build with rfd.
    #[cfg(test)]
    pub(crate) fn shown_by_host(mut self) -> Self {
        self.host = true;
        self
    }

    pub fn dialog(&self) -> FileDialog {
        FileDialog {
            picker: self.clone(),
            request: Request::default(),
        }
    }

    /// Asks the interface and blocks until it answers or its backend closes.
    fn ask(&self, mut request: Request) -> Vec<PathBuf> {
        request.id = NEXT.fetch_add(1, Ordering::Relaxed);
        let id = request.id;
        let (answer, answered) = mpsc::channel();
        {
            let mut waiting = lock(&self.waiting);
            // Checked under the same lock `close` takes, so a dialog racing
            // with shutdown is either closed with the rest or never waits.
            if waiting.closed {
                return Vec::new();
            }
            waiting.answers.insert(id, answer);
        }
        // However this returns, the request is forgotten.
        let _forget = Forget {
            waiting: &self.waiting,
            id,
        };
        if self.events.send(Event::PickRequest(request)).is_err() {
            return Vec::new();
        }
        self.waker.wake();
        answered.recv().unwrap_or_default()
    }
}

struct Forget<'a> {
    waiting: &'a Mutex<Waiting>,
    id: u64,
}

impl Drop for Forget<'_> {
    fn drop(&mut self) {
        lock(self.waiting).answers.remove(&self.id);
    }
}

/// A dialog being described, with `rfd::FileDialog`'s builder methods.
pub struct FileDialog {
    picker: Picker,
    request: Request,
}

impl FileDialog {
    pub fn set_title(mut self, title: impl Into<String>) -> Self {
        self.request.title = title.into();
        self
    }

    pub fn add_filter(mut self, name: impl Into<String>, extensions: &[impl ToString]) -> Self {
        self.request.filters.push((
            name.into(),
            extensions.iter().map(ToString::to_string).collect(),
        ));
        self
    }

    pub fn set_file_name(mut self, name: impl Into<String>) -> Self {
        self.request.file_name = Some(name.into());
        self
    }

    pub fn set_directory(mut self, directory: impl Into<PathBuf>) -> Self {
        self.request.directory = Some(directory.into());
        self
    }

    pub fn pick_file(self) -> Option<PathBuf> {
        self.run(Kind::File).into_iter().next()
    }

    pub fn pick_files(self) -> Option<Vec<PathBuf>> {
        Some(self.run(Kind::Files)).filter(|paths| !paths.is_empty())
    }

    pub fn pick_folder(self) -> Option<PathBuf> {
        self.run(Kind::Folder).into_iter().next()
    }

    pub fn save_file(self) -> Option<PathBuf> {
        self.run(Kind::Save).into_iter().next()
    }

    fn run(mut self, kind: Kind) -> Vec<PathBuf> {
        self.request.kind = kind;
        if self.picker.host {
            return self.picker.ask(self.request);
        }
        #[cfg(feature = "ui")]
        return rfd(self.request);
        #[cfg(not(feature = "ui"))]
        unreachable!("without `ui` every dialog goes to the host")
    }
}

/// Shows `request` with rfd, as the worker did before dialogs could go to
/// the interface.
#[cfg(feature = "ui")]
fn rfd(request: Request) -> Vec<PathBuf> {
    let mut dialog = rfd::FileDialog::new().set_title(&request.title);
    for (name, extensions) in &request.filters {
        dialog = dialog.add_filter(name, extensions.as_slice());
    }
    if let Some(name) = &request.file_name {
        dialog = dialog.set_file_name(name);
    }
    if let Some(directory) = &request.directory {
        dialog = dialog.set_directory(directory);
    }
    match request.kind {
        Kind::File => dialog.pick_file().into_iter().collect(),
        Kind::Files => dialog.pick_files().unwrap_or_default(),
        Kind::Folder => dialog.pick_folder().into_iter().collect(),
        Kind::Save => dialog.save_file().into_iter().collect(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn host(dialogs: &Dialogs) -> (Picker, mpsc::Receiver<Event>) {
        let (events, inbox) = mpsc::channel();
        (
            dialogs.picker(events, Waker::default()).shown_by_host(),
            inbox,
        )
    }

    fn request(inbox: &mpsc::Receiver<Event>) -> Request {
        match inbox.recv_timeout(std::time::Duration::from_secs(5)) {
            Ok(Event::PickRequest(request)) => request,
            _ => panic!("a pick request"),
        }
    }

    /// Joins `waiting`, failing instead of hanging if it never returns.
    fn finished<T: Send + 'static>(waiting: std::thread::JoinHandle<T>) -> T {
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(5);
        while !waiting.is_finished() {
            assert!(
                std::time::Instant::now() < deadline,
                "the dialog is still waiting"
            );
            std::thread::sleep(std::time::Duration::from_millis(5));
        }
        waiting.join().unwrap()
    }

    #[test]
    fn a_host_answer_reaches_the_waiting_dialog() {
        let dialogs = Dialogs::default();
        let (picker, inbox) = host(&dialogs);
        let waiting = std::thread::spawn(move || {
            picker
                .dialog()
                .set_title("Choose a picture")
                .add_filter("Images", &["png", "jpg"])
                .pick_file()
        });
        let request = request(&inbox);
        assert_eq!(request.kind, Kind::File);
        assert_eq!(request.title, "Choose a picture");
        assert_eq!(
            request.filters,
            [(
                "Images".to_owned(),
                vec!["png".to_owned(), "jpg".to_owned()]
            )]
        );
        dialogs.answer(request.id, vec![PathBuf::from("/tmp/a.png")]);
        assert_eq!(finished(waiting), Some(PathBuf::from("/tmp/a.png")));
        // A second answer to the same request goes nowhere.
        dialogs.answer(request.id, Vec::new());
        assert_eq!(dialogs.waiting(), 0);
    }

    #[test]
    fn a_cancelled_pick_of_files_is_none() {
        let dialogs = Dialogs::default();
        let (picker, inbox) = host(&dialogs);
        let waiting = std::thread::spawn(move || picker.dialog().pick_files());
        let request = request(&inbox);
        assert_eq!(request.kind, Kind::Files);
        dialogs.answer(request.id, Vec::new());
        assert_eq!(finished(waiting), None);
    }

    #[test]
    fn a_dialog_whose_interface_is_gone_returns_and_leaves_nothing_behind() {
        let dialogs = Dialogs::default();
        let (picker, inbox) = host(&dialogs);
        // Lost before the request: the dialog cannot be shown.
        drop(inbox);
        assert_eq!(picker.dialog().pick_file(), None);
        assert_eq!(dialogs.waiting(), 0);

        // Lost after the request was delivered: the backend that owned the
        // receiver goes with it, and dropping it closes its dialogs.
        let (picker, inbox) = host(&dialogs);
        let waiting = std::thread::spawn(move || picker.dialog().pick_folder());
        request(&inbox);
        assert_eq!(dialogs.waiting(), 1);
        drop(inbox);
        drop(dialogs);
        assert_eq!(finished(waiting), None);
    }

    #[test]
    fn closing_answers_waiting_dialogs_and_every_later_one() {
        let dialogs = Dialogs::default();
        let (picker, inbox) = host(&dialogs);
        let waiting = {
            let picker = picker.clone();
            std::thread::spawn(move || picker.dialog().save_file())
        };
        request(&inbox);
        dialogs.close();
        assert_eq!(finished(waiting), None);
        assert_eq!(dialogs.waiting(), 0);
        // Asked after the close, as a dialog racing with shutdown may be: it
        // is answered at once, without reaching the interface.
        assert_eq!(picker.dialog().pick_files(), None);
        assert!(inbox.try_recv().is_err());
        assert_eq!(dialogs.waiting(), 0);
    }

    #[test]
    fn dialogs_racing_with_close_never_wait() {
        for _ in 0..200 {
            let dialogs = Dialogs::default();
            let (picker, inbox) = host(&dialogs);
            let waiting: Vec<_> = (0..4)
                .map(|_| {
                    let picker = picker.clone();
                    std::thread::spawn(move || picker.dialog().pick_file())
                })
                .collect();
            dialogs.close();
            for waiting in waiting {
                assert_eq!(finished(waiting), None);
            }
            assert_eq!(dialogs.waiting(), 0);
            drop(inbox);
        }
    }
}
