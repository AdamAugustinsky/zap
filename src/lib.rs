//! ZapFast internals exposed for diagnostics and tests.
//!
//! The `ui` feature (on by default) is the egui desktop client. Without it the
//! crate is the core alone: the backend, the archive, calls and what they use.

#[cfg(feature = "ui")]
pub mod account;
#[cfg(feature = "ui")]
pub mod animation;
#[cfg(feature = "ui")]
pub mod announcer;
#[cfg(feature = "ui")]
pub mod app;
#[cfg(feature = "ui")]
pub mod app_lock;
#[cfg(all(target_os = "linux", feature = "ui"))]
pub mod appearance;
pub mod archive;
pub mod audio;
#[cfg(feature = "ui")]
pub mod autostart;
#[cfg(feature = "ui")]
pub mod backdrop;
pub mod backend;
#[cfg(feature = "ui")]
pub mod bidi;
pub mod call_audio;
pub mod call_tones;
pub mod calls;
pub mod camera;
#[cfg(all(feature = "ui", any(test, feature = "demo")))]
pub mod demo;
pub mod diagnostics;
#[cfg(feature = "ui")]
pub mod emoji;
pub mod geo;
pub mod i18n;
#[cfg(feature = "ui")]
pub mod image_cache;
#[cfg(feature = "ui")]
pub mod image_preview;
#[cfg(all(target_os = "macos", feature = "ui"))]
pub mod macos;
pub mod markup;
#[cfg(feature = "ui")]
pub mod media_pause;
pub mod mentions;
pub mod model;
#[cfg(feature = "ui")]
pub mod motion;
pub mod mp4_frames;
pub mod nav;
#[cfg(feature = "ui")]
pub mod notify;
pub mod opener;
pub mod paths;
pub mod pick;
pub mod privacy;
pub mod proxy;
#[cfg(feature = "ui")]
pub mod qr;
pub mod safety;
pub mod settings;
#[cfg(feature = "ui")]
pub mod single_instance;
pub mod sticker_meta;
pub mod sticker_search;
pub mod target;
#[cfg(feature = "ui")]
pub mod theme;
pub mod timestretch;
#[cfg(feature = "ui")]
pub mod transcript;
pub mod transport;
#[cfg(feature = "ui")]
pub mod ui;
#[cfg(feature = "ui")]
pub mod updates;
pub mod util;
#[cfg(feature = "ui")]
pub mod video;
pub mod video_frames;
pub mod video_rate;
pub mod voice;
pub mod wake;
#[cfg(feature = "ui")]
pub mod wallpaper;
pub mod wallpaper_files;
