package lock

import (
	"time"

	"gonativeui/app"
	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/state"
	"zapfast/ui/lock/applock"
)

// AfterDuration is how long ZapFast may go unused before the app lock locks
// it (zf:src/settings.rs AutoLock::duration).
func AfterDuration(a state.AutoLock) time.Duration {
	switch a {
	case state.LockOneMinute:
		return time.Minute
	case state.LockOneHour:
		return time.Hour
	}
	return 15 * time.Minute
}

// AL returns the lock's logic: the Settings screen's password form is on it
// (OpenForm, SubmitForm), and tests read it.
func (s *Screen) AL() *applock.Lock { return s.al }

// Tick is upstream's tick_app_lock, once per frame of the main window (the
// shell, or the reducers, call it after the events are applied; with no window
// open, whoever drains the cores calls it with a nil c):
//
//   - it collects a finished password check and applies its answer;
//   - with no password set it never leaves the window locked;
//   - it counts the window's input (c's Input) as use, and locks once ZapFast
//     has gone unused for Settings' "Lock after", the time hidden in the tray
//     included;
//   - it starts locked when a password is set (the first call);
//   - it notices a lock someone else asked for (State.Lock.Locked set by the
//     shell's ⌘⇧L) and an unlink that took the last account (the lock goes with
//     the password).
//
// It asks for a frame when the inactivity time will have passed (c.WakeIn, or
// a timer calling State.Wake with no c).
func (s *Screen) Tick(st *state.State, c *ui.Context) {
	s.st = st
	s.poll(st)
	hasHash := len(st.Settings.AppLockHash) > 0
	if !s.started {
		s.started = true
		if hasHash && !st.Lock.Locked {
			s.lockNow(st) // ZapFast starts locked
		}
	}
	s.unlinked(st)
	hasHash = len(st.Settings.AppLockHash) > 0
	if !hasHash {
		// Nothing to unlock with; never leave the window stuck.
		if st.Lock.Locked || s.al.IsLocked() {
			s.release(st)
		}
		return
	}
	s.sync(st)
	if c != nil && s.sawInput(st, c.Input()) {
		s.al.NoteInput()
		st.Lock.LastInput = s.al.LastInput()
	}
	if st.Lock.LastInput.After(s.al.LastInput()) { // the shell saw input
		s.al.SetLastInput(st.Lock.LastInput)
	}
	if st.Lock.Locked {
		s.cancelTimer()
		return
	}
	// Recording a voice message is using ZapFast, keys or not.
	if s.InUse != nil && s.InUse(st) {
		s.al.NoteInput()
		st.Lock.LastInput = s.al.LastInput()
	}
	left := s.al.IdleLeft(AfterDuration(st.Settings.AppLockAfter))
	if left <= 0 {
		s.lockNow(st)
		return
	}
	if c != nil {
		c.WakeIn(int64(left) + 1)
		return
	}
	s.setTimer(st, left)
}

// setTimer wakes the app when the inactivity time has passed, for the
// time with no window to ask a frame of.
func (s *Screen) setTimer(st *state.State, left time.Duration) {
	s.cancelTimer()
	wake := st.Wake
	s.timer = time.AfterFunc(left+time.Millisecond, func() {
		if wake != nil {
			wake()
		}
	})
}

func (s *Screen) cancelTimer() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

// sync brings the lock's own state to State.Lock.Locked, which the shell can
// set (⌘⇧L) and the screens read.
func (s *Screen) sync(st *state.State) {
	switch {
	case st.Lock.Locked && !s.al.IsLocked():
		s.al.Lock()
		s.pw.Wipe()
		s.focus = true
		s.afterLock(st)
	case !st.Lock.Locked && s.al.IsLocked():
		s.al.Release()
		s.pw.Wipe()
	}
}

// sawInput reports whether in is someone using the window: keys, typed text,
// a button, the wheel, the pointer moving. Focus changes and the pointer
// leaving are the desktop's doing, not the person's.
func (s *Screen) sawInput(st *state.State, in *ui.Input) bool {
	moved := in.Inside && (in.MouseX != s.mouse[0] || in.MouseY != s.mouse[1])
	s.mouse = [2]float32{in.MouseX, in.MouseY}
	if moved || in.NKeys > 0 || in.NText > 0 || in.NPreedit > 0 || in.ScrollX != 0 || in.ScrollY != 0 || in.NScrolls > 0 || in.HasPaste {
		return true
	}
	for i := range in.Pressed {
		if in.Pressed[i] || in.Released[i] {
			return true
		}
	}
	return false
}

// Event is the tick's answer to an app event: the system is going to sleep
// (the framework's app.EvWillSleep, delivered by package sys) locks, as the
// monotonic clock stops while the Mac sleeps and the time asleep would not
// count. It reports whether it handled the event (it never does: others see
// it too).
func (s *Screen) Event(st *state.State, e *app.Event) bool {
	if e.Kind == app.EvWillSleep {
		s.Sleep(st)
	}
	return false
}

// Sleep locks for the system's sleep (the hook of the framework's power
// events): a computer that goes to sleep is left alone, so waking it should
// ask for the password.
func (s *Screen) Sleep(st *state.State) { s.LockNow(st) }

// LockNow is Action::LockApp: the status item's "Lock ZapFast", ⌘⇧L and the
// sleep hook. With no password set, or when already locked, it does nothing.
func (s *Screen) LockNow(st *state.State) {
	if len(st.Settings.AppLockHash) == 0 || st.Lock.Locked {
		return
	}
	s.lockNow(st)
}

func (s *Screen) lockNow(st *state.State) {
	s.st = st
	s.al.Lock()
	s.pw.Wipe()
	s.focus = true
	st.Lock.Locked = true
	st.Touch()
	s.afterLock(st)
}

// afterLock hides what the lock screen must not leave readable (zf:src/app.rs
// lock_app): the account menu, the unlock-locked-chats dialog and the locked
// chats folder. What plays, records or shows beyond the window is for the
// hooks (OnLock): media, a recording, shown notifications, the open draft.
func (s *Screen) afterLock(st *state.State) {
	st.AccountMenu = false
	if st.Dialog.Kind == state.DialogUnlockLockedChats {
		st.CloseDialog()
	}
	// Its open locked chat and panes go with it, so the unlock does not
	// show them again outside the folder.
	for _, a := range st.Accounts {
		a.CloseLockedFolder()
	}
	st.Focused = false
	st.Touch()
	if s.OnLock != nil {
		s.OnLock(st)
	}
}

// release lifts the lock without a password.
func (s *Screen) release(st *state.State) {
	s.al.Release()
	s.pw.Wipe()
	st.Lock.Locked = false
	st.Lock.LastInput = s.al.Now()
	st.Touch()
}

// poll collects a finished check and applies it (app_lock_outcome).
func (s *Screen) poll(st *state.State) {
	out, ok := s.al.Poll()
	if !ok {
		return
	}
	switch out.Kind {
	case applock.OutcomeUnlock:
		if !st.Lock.Locked && !s.al.IsLocked() {
			return
		}
		s.al.Unlocked(out.Matched)
		if out.Matched {
			st.Lock.Locked = false
			st.Lock.LastInput = s.al.Now()
			s.pw.Wipe()
			st.Touch()
			// In the order they were asked for, so the last one is what the
			// reader finds on screen.
			held := s.al.TakeDeferred()
			if s.OnUnlock != nil {
				s.OnUnlock(st, held)
			}
		} else {
			s.focus = true
		}
		st.Touch()
	case applock.OutcomeWrongCurrent:
		if f := s.al.Form; f != nil {
			f.Busy = false
			f.Error = applock.FormWrongCurrent
		}
		st.Touch()
	case applock.OutcomeSet:
		if s.al.Form == nil {
			return
		}
		s.al.Form = nil
		st.Settings.AppLockHash = []byte(out.Verifier)
		s.al.NoteInput()
		st.Lock.LastInput = s.al.LastInput()
		s.save(st)
		st.Toast([]byte(tr("App lock password saved")), false)
	case applock.OutcomeTurnOff:
		if s.al.Form == nil {
			return
		}
		s.al.Form = nil
		st.Settings.AppLockHash = nil
		s.save(st)
		st.Toast([]byte(tr("App lock turned off")), false)
	}
}

func (s *Screen) save(st *state.State) {
	st.Touch()
	if s.Save != nil {
		s.Save(st)
	}
}

// unlinked turns the lock off when an unlink took the last number: the
// password guarded chats that are gone now, and a forgotten one is recovered
// exactly this way (zf:src/app.rs, the LoggedOut link status).
func (s *Screen) unlinked(st *state.State) {
	if len(st.Settings.AppLockHash) == 0 && !st.Lock.Locked {
		return
	}
	loggedOut := false
	for _, a := range st.Accounts {
		if a.Conn == nil {
			// Not open yet: it says nothing about the link.
			if a.Pending != nil {
				return
			}
			continue
		}
		if a.Model.Linked() {
			return
		}
		if a.Model.Link.Known() && a.Model.Link.Status() == abi.LinkStatusTagLoggedOut {
			loggedOut = true
		}
	}
	if loggedOut {
		s.ForgetLock(st)
	}
}

// ForgetLock forgets the app lock password and lifts the lock: after
// unlinking, when nothing is left behind it.
func (s *Screen) ForgetLock(st *state.State) {
	s.al.Form = nil
	s.al.TakeDeferred()
	s.release(st)
	if len(st.Settings.AppLockHash) > 0 {
		st.Settings.AppLockHash = nil
		s.save(st)
	}
}

// Defer holds a request from outside (a clicked notification, a link, a group
// invite) until the unlock. It reports whether it was held: with the lock off
// the request is the caller's to carry out at once.
func (s *Screen) Defer(st *state.State, d applock.Deferred) bool {
	if !st.Lock.Locked {
		return false
	}
	s.al.Defer(d)
	return true
}

// DeferText adds a link's text to the chat request waiting for the unlock.
func (s *Screen) DeferText(chat, text string) { s.al.DeferText(chat, text) }

// OpenForm is Action::AppLockForm: open the Settings password form in mode, or
// close it (ok false). Setting a password must not replace one without it, and
// changing or removing one needs one to exist; a form is not closed while a
// check runs.
func (s *Screen) OpenForm(st *state.State, mode applock.FormMode, open bool) {
	if s.al.Checking() {
		return
	}
	hasHash := len(st.Settings.AppLockHash) > 0
	if !open || (mode == applock.FormSet) == hasHash {
		s.al.Form = nil
		return
	}
	s.al.Form = applock.NewForm(mode)
}

// SubmitForm is Action::SubmitAppLockForm: check the form and work out the
// new verifier. A form that no longer fits the stored password is closed.
func (s *Screen) SubmitForm(st *state.State) {
	f := s.al.Form
	if f == nil {
		return
	}
	hasHash := len(st.Settings.AppLockHash) > 0
	if (f.Mode == applock.FormSet) != !hasHash {
		s.al.Form = nil
		return
	}
	s.al.SubmitForm(string(st.Settings.AppLockHash), hasHash, st.Wake)
}

// SetAutoLock is Action::SetAutoLock: "Lock after".
func (s *Screen) SetAutoLock(st *state.State, after state.AutoLock) {
	st.Settings.AppLockAfter = after
	s.al.NoteInput()
	st.Lock.LastInput = s.al.LastInput()
	s.save(st)
}

// UnlinkLocked is Action::UnlinkLockedApp: "Forgot password? Unlink this
// computer" confirmed. The lock lifts only once WhatsApp has unlinked and the
// chats are gone (the LoggedOut link status), never before. Every number
// linked here goes: the lock guards them all.
func (s *Screen) UnlinkLocked(st *state.State) {
	if !st.Lock.Locked {
		return
	}
	s.al.Forgetting = applock.ForgetUnlinking
	for _, a := range st.Accounts {
		a.Send(a.Cmd(abi.CommandTagUnlink))
	}
	st.Touch()
}
