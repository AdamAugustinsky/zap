// Package sys is the process's services.
//
// It ports zf:src/notify.rs, zf:src/macos.rs, zf:src/single_instance.rs,
// zf:src/autostart.rs and zf:src/wake.rs, and plays the ringtone of
// zf:src/call_tones.rs:
//
//   - notifications: package logic decides (State.Notices, NoticeClears,
//     Attention) and this package posts them through app/notify, takes them
//     back, and opens a click's chat; calls raise their own, with the ringtone;
//   - the Dock badge and the call bounce, the status item and resident mode;
//   - the command line, the single instance, whatsapp:// and wa.me links;
//   - the login item, reconnect on wake, the core's file panels for
//     PickRequest;
//   - the process's folders (0700), the log of the run and panic.log.
//
// Entry points (the shell's slots, wired in cmd/zapfast/screens.go through
// newSys): New() *System is screen.System; Launch, Config, Restrict and
// StartLog run from main before the window exists.
package sys
