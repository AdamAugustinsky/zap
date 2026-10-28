// Package lock is the app lock screen, shown alone while state.Lock.Locked, and
// the lock's timer.
//
// It ports zf:src/ui/lock.rs and the app-lock parts of zf:src/app.rs (the
// tick, lock_app, the outcome of a password check, forget_app_lock). The
// logic of zf:src/app_lock.rs is package applock; the password field, which
// keeps what is typed out of sight and out of reach of other processes, is
// package secure.
//
// Entry points (the shell's slots, wired in cmd/zapfast/screens.go):
//
//	New() *Screen  // screen.Pane: Set.Lock
//
// # What the app must call
//
// The lock has to run every frame, also while unlocked, which the Pane slot
// does not give it. The owner of the main window's frame calls, after the
// events are applied and before it routes between the screens:
//
//	ls.Tick(s, c) // ls is lock.New(); c is the window's ui.Context, nil with no window open
//
// Tick starts the app locked when a password is set, counts the window's input
// as use, locks after Settings' "Lock after", collects a finished password
// check, and lifts the lock when an unlink took the last number. The system
// hooks are Sleep (the framework's app.EvWillSleep, or Event with the app
// event) and LockNow (the status item's Lock ZapFast, ⌘⇧L). OnLock, OnUnlock,
// InUse and Save connect what only other packages know: media, recordings,
// notifications and drafts at the lock, the requests held until the unlock, a
// recording counting as use, and saving settings.
//
// The Settings screen's password form is OpenForm, SubmitForm and
// SetAutoLock, with the form on AL().Form.
package lock
