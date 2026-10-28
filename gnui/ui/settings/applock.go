package settings

import (
	"bytes"
	"sync"
	"unicode/utf8"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/i18n"
	"zapfast/lockhash"
	"zapfast/ui/screen"
)

type lockMode uint8

const (
	lockSet lockMode = iota
	lockChange
	lockTurnOff
)

type lockErr uint8

const (
	lockNoErr lockErr = iota
	lockTooShort
	lockMismatch
	lockWrongCurrent
)

type lockOutcome uint8

const (
	outcomeNone lockOutcome = iota
	outcomeWrong
	outcomeSet
	outcomeOff
)

// lockForm is the password form under the App lock row. Its fields are
// masked (rexui secure fields); the passwords leave them only for the checking
// goroutine, which zeroes them.
type lockForm struct {
	open                     bool
	mode                     lockMode
	current, password, again ui.TextBuffer
	err                      lockErr
	busy                     bool
	firstKey                 ui.Key
	focus                    bool

	mu       sync.Mutex
	outcome  lockOutcome
	verifier []byte
}

// openForm opens the form for mode: setting needs no password yet, changing
// and turning off need one.
func (s *Screen) openForm(e *screen.Env, mode lockMode) {
	if s.lock.busy {
		return
	}
	if (mode == lockSet) != (len(e.S.Settings.AppLockHash) == 0) {
		return
	}
	s.lock.reset()
	s.lock.open, s.lock.mode, s.lock.focus = true, mode, true
}

func (l *lockForm) reset() {
	l.clearFields()
	l.err, l.busy, l.open, l.firstKey = lockNoErr, false, false, 0
}

// clearFields forgets what was typed.
func (l *lockForm) clearFields() {
	l.current.Wipe()
	l.password.Wipe()
	l.again.Wipe()
}

// appLockButtons are the App lock row's buttons: Set password…, or Change
// password… and Turn off….
func (s *Screen) appLockButtons(e *screen.Env) {
	if len(e.S.Settings.AppLockHash) > 0 {
		if button(e, s.tx.changePassword) {
			s.openForm(e, lockChange)
		}
		if button(e, s.tx.turnOffEllipsis) {
			s.openForm(e, lockTurnOff)
		}
		return
	}
	if button(e, s.tx.setPassword) {
		s.openForm(e, lockSet)
	}
}

// lockFormBlock is the form: a title, the fields, the error, and the buttons
// (zf:src/ui/settings.rs app_lock_form).
func (s *Screen) lockFormBlock(e *screen.Env) {
	k := e.K.Kit
	c := e.C()
	l := &s.lock
	rexui.Label(k, s.tx.lockTitle[l.mode], rexui.LabelOpts{Style: rexui.TextSemibold})
	submit := false
	k.BeginDisabled(l.busy)
	field := func(f *ui.TextBuffer, label string) {
		k.NextWidth(k.Fixed(320))
		sig := rexui.TextField(k, label, f, label, rexui.FieldOpts{Secure: true})
		if l.firstKey == 0 {
			l.firstKey = sig.Key
		}
		submit = submit || sig.Submitted
	}
	if l.mode != lockSet {
		field(&l.current, s.tx.currentPassword)
	}
	if l.mode != lockTurnOff {
		field(&l.password, s.tx.newPassword)
		field(&l.again, s.tx.againPassword)
	}
	k.EndDisabled()
	if l.focus && l.firstKey != 0 && !l.busy {
		l.focus = false
		c.SetFocus(l.firstKey)
	}
	if l.err != lockNoErr {
		rexui.Label(k, s.tx.lockErr[l.err], rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Danger), Wrap: true})
	}
	k.NextGap(8)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "form buttons", ui.Pct(1), ui.Children(1), 0)
	k.BeginDisabled(l.busy)
	if rexui.Button(k, s.tx.lockConfirm[l.mode], rexui.ButtonOpts{Variant: rexui.ButtonPrimary}).Clicked {
		submit = true
	}
	k.EndDisabled()
	if l.busy {
		rexui.Spinner(k, s.tx.working, rexui.SpinnerOpts{})
	} else if button(e, s.tx.cancel) {
		l.reset()
	}
	k.EndContainer()
	if submit && !l.busy && l.open {
		s.submitForm(e)
	}
}

// submitForm checks the form and, on a goroutine, the current password and
// the new verifier (zf:src/app_lock.rs submit_form). The passwords are taken
// out of the fields and zeroed once used.
func (s *Screen) submitForm(e *screen.Env) {
	l := &s.lock
	if l.mode != lockTurnOff {
		if utf8.RuneCount(l.password.Bytes()) < lockhash.MinPasswordChars { // characters, as upstream counts
			l.err = lockTooShort
			return
		}
		if !bytes.Equal(l.password.Bytes(), l.again.Bytes()) {
			l.err = lockMismatch
			return
		}
	}
	l.err, l.busy = lockNoErr, true
	stored := string(e.S.Settings.AppLockHash)
	mode := l.mode
	current, password := bytes.Clone(l.current.Bytes()), bytes.Clone(l.password.Bytes()) // the goroutine's: it zeroes them
	l.clearFields()
	go func() {
		defer clear(current)
		defer clear(password)
		out, verifier := outcomeWrong, []byte(nil)
		switch {
		case mode != lockSet && !lockhash.VerifiesApp(stored, current):
		case mode == lockTurnOff:
			out = outcomeOff
		default:
			out, verifier = outcomeSet, []byte(lockhash.AppVerifier(password))
		}
		l.mu.Lock()
		l.outcome, l.verifier = out, verifier
		l.mu.Unlock()
		s.wake()
	}()
}

// pollLock applies a finished check (zf:src/app.rs app_lock_outcome).
func (s *Screen) pollLock(e *screen.Env) {
	l := &s.lock
	l.mu.Lock()
	out, verifier := l.outcome, l.verifier
	l.outcome, l.verifier = outcomeNone, nil
	l.mu.Unlock()
	if out == outcomeNone {
		return
	}
	st := &e.S.Settings
	switch {
	case !l.open:
	case out == outcomeWrong:
		l.busy, l.err = false, lockWrongCurrent
	case out == outcomeSet:
		st.AppLockHash = verifier
		e.S.Lock.LastInput = e.S.Now()
		l.reset()
		s.changed(e)
		e.S.Toast([]byte(i18n.T("App lock password saved")), false)
	case out == outcomeOff:
		st.AppLockHash = nil
		l.reset()
		s.changed(e)
		e.S.Toast([]byte(i18n.T("App lock turned off")), false)
	}
}
