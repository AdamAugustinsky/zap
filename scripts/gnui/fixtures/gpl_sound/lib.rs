//! Embeds a GPL-2.0 sound the way `notify.rs` does, so the gate has something to find.

#[used]
#[unsafe(no_mangle)]
pub static GPL_SOUND_FIXTURE: &[u8] = include_bytes!("../../../../assets/sounds/alert.wav");
