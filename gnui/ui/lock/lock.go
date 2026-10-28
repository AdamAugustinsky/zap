package lock

import (
	"strconv"
	"time"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/lock/applock"
	"zapfast/ui/login"
	"zapfast/ui/screen"
)

func tr(s string) string { return i18n.T(s) }

// Screen is the lock screen, and the lock's timer (Tick).
type Screen struct {
	al *applock.Lock
	pw ui.TextBuffer // the password: a secure field's buffer, wiped once used
	st *state.State

	card    rexui.Card
	scroll  login.Scroller
	started bool
	focus   bool       // the password field takes the keyboard when it can
	mouse   [2]float32 // the pointer at the last Tick, to see it move
	timer   *time.Timer

	// secs is the seconds the wait text was made for; waitText the text.
	secs     int
	waitText string

	// OnLock runs after the window is locked, for what only other packages
	// know how to end: stop voice and video, discard a recording, withdraw
	// the notifications shown, save the open draft (zf:src/app.rs lock_app).
	OnLock func(s *state.State)
	// OnUnlock runs after a right password, with what was asked for from
	// outside while locked, in the order it came: a clicked notification, a
	// link's chat, a group invite. The last one asked for ends up on screen.
	OnUnlock func(s *state.State, held []applock.Deferred)
	// InUse reports that ZapFast is being used without input (a voice message
	// being recorded), which restarts the inactivity count.
	InUse func(s *state.State) bool
	// Save persists the settings after the lock's password changes.
	Save func(s *state.State)
}

var _ screen.Pane = (*Screen)(nil)

// New returns the screen. Whoever owns the main window's frame calls its Tick
// each frame (see Tick).
func New() *Screen {
	s := &Screen{al: applock.New(false)}
	s.al.SetClock(func() time.Time {
		if s.st != nil {
			return s.st.Now()
		}
		return time.Now()
	})
	return s
}

// Init readies the screen for a window: with a password set, ZapFast starts
// locked.
func (s *Screen) Init(e *screen.Env) error {
	s.st = e.S
	if !s.started {
		s.started = true
		if len(e.S.Settings.AppLockHash) > 0 && !e.S.Lock.Locked {
			s.lockNow(e.S)
		}
	}
	return nil
}

// Build builds the lock screen at r: everything the lock hides is left
// undrawn, the window shows only this card.
func (s *Screen) Build(e *screen.Env, r screen.Rect) {
	st := e.S
	s.st = st
	s.poll(st)
	if !st.Lock.Locked {
		return
	}
	s.sync(st)
	k := e.K
	e.Begin(r, "Lock")
	s.beginCard(e, r, tr("Zap is locked"))
	if s.al.Forgetting != applock.ForgetNo {
		s.pw.Wipe() // the password field is gone: nothing half typed stays
	}
	switch s.al.Forgetting {
	case applock.ForgetNo:
		s.password(e)
	case applock.ForgetConfirming:
		s.confirmUnlink(e)
	default:
		rexui.Spinner(k.Kit, tr("Unlinking")+"###spin", rexui.SpinnerOpts{Size: theme.SizeSmall})
		rexui.Label(k.Kit, tr("Unlinking this computer…"), rexui.LabelOpts{Style: rexui.TextHeadline})
	}
	s.endCard(e)
	e.End()
}

// beginCard opens the centred card of the screen: the name, a line under it,
// then the body the caller builds until endCard.
func (s *Screen) beginCard(e *screen.Env, r screen.Rect, tagline string) {
	k := e.K
	s.scroll.Begin(e, r, "Lock")
	k.NextWidth(ui.Px(min(e.Pt(460), max(r.W-e.Pt(24), 0))))
	s.card = rexui.BeginCard(k.Kit, "card", rexui.CardOpts{Variant: rexui.CardBox})
	k.NextPad(20)
	k.NextGap(8)
	k.BeginContainer(ui.AxisY, ui.CrossCenter, "body", ui.Pct(1), ui.Children(1), 0)
	login.Mark(e)
	k.Space(4)
	rexui.Label(k.Kit, "Zap", rexui.LabelOpts{Style: rexui.TextTitle1})
	rexui.Label(k.Kit, tagline, rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	k.Space(8)
}

func (s *Screen) endCard(e *screen.Env) {
	k := e.K
	k.EndContainer() // body
	rexui.EndCard(k.Kit, &s.card)
	s.scroll.End(e)
}

// password is the password field, Unlock and the way out.
func (s *Screen) password(e *screen.Env) {
	k, st := e.K, e.S
	c := e.C()
	rexui.Label(k.Kit, tr("Enter your password to unlock."), rexui.LabelOpts{Color: rexui.Of(theme.Label2), Wrap: true})
	checking := s.al.Checking()
	key := base.FieldKey(c.ParentKey(), tr("Password"))
	// The field takes the keyboard as the screen appears, and again after a
	// try, unless the person has gone elsewhere.
	if s.focus && !checking {
		s.focus = false
		c.SetFocus(key)
	}
	k.BeginDisabled(checking)
	sig := rexui.TextField(k.Kit, tr("Password"), &s.pw, tr("Password"), rexui.FieldOpts{Size: theme.SizeLarge, Secure: true})
	k.EndDisabled()
	if wait, ok := s.al.WaitLeft(); ok {
		secs := max(int((wait+time.Second-1)/time.Second), 1)
		if secs != s.secs || s.waitText == "" {
			s.secs = secs
			s.waitText = i18n.Format(tr("Too many wrong passwords. Try again in {seconds} s."), "{seconds}", strconv.Itoa(secs))
		}
		rexui.Label(k.Kit, s.waitText, rexui.LabelOpts{Color: rexui.DangerText, Wrap: true})
		c.WakeIn(int64(min(wait, 250*time.Millisecond)))
	} else if s.al.Wrong {
		rexui.Label(k.Kit, tr("Wrong password. Try again."), rexui.LabelOpts{Color: rexui.DangerText, Wrap: true})
	}
	k.Space(4)
	ready := s.al.CanTry(s.pw.Len() > 0)
	submit := sig.Submitted
	if checking {
		rexui.Spinner(k.Kit, tr("Checking")+"###spin", rexui.SpinnerOpts{Size: theme.SizeSmall})
	} else {
		k.BeginDisabled(!ready)
		if rexui.Button(k.Kit, tr("Unlock"), rexui.ButtonOpts{Variant: rexui.ButtonPrimary, Size: theme.SizeLarge}).Clicked {
			submit = true
		}
		k.EndDisabled()
	}
	if submit && ready {
		s.submit(st)
	}
	k.Space(6)
	if rexui.Link(k.Kit, tr("Forgot password? Unlink this computer"), rexui.LinkOpts{}).Clicked {
		s.forgot(st, true)
	}
}

// submit is Action::UnlockApp: the password goes to the checking goroutine and
// the field is emptied.
func (s *Screen) submit(st *state.State) {
	pw := append([]byte(nil), s.pw.Bytes()...) // the checking goroutine's: it zeroes it
	s.pw.Wipe()
	s.al.TryUnlock(string(st.Settings.AppLockHash), pw, st.Wake)
	st.Touch()
}

// forgot is Action::ForgotAppPassword(open).
func (s *Screen) forgot(st *state.State, open bool) {
	if s.al.Forgetting == applock.ForgetUnlinking {
		return
	}
	s.al.Forgetting = applock.ForgetNo
	if open {
		s.al.Forgetting = applock.ForgetConfirming
	} else {
		s.focus = true
	}
	st.Touch()
}

func (s *Screen) confirmUnlink(e *screen.Env) {
	k, st := e.K, e.S
	c := e.C()
	rexui.Label(k.Kit, tr("Unlink this computer?"), rexui.LabelOpts{Style: rexui.TextHeadline})
	rexui.Label(k.Kit, tr("Without the password, the only way back in is to unlink. This removes the device from WhatsApp, deletes the chats stored here, and turns the app lock off. You can link again with a new code."), rexui.LabelOpts{Wrap: true})
	k.Space(8)
	k.NextGap(8)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "actions", ui.Pct(1), ui.Children(1), 0)
	k.Spacer()
	if rexui.Button(k.Kit, tr("Cancel"), rexui.ButtonOpts{Size: theme.SizeLarge}).Clicked || c.TakeKey(ui.KeyEscape, 0) {
		s.forgot(st, false)
	}
	if rexui.Button(k.Kit, tr("Unlink"), rexui.ButtonOpts{Variant: rexui.ButtonDestructive, Size: theme.SizeLarge}).Clicked {
		s.UnlinkLocked(st)
	}
	k.EndContainer()
}
