package settings

import "zapfast/ui/screen"

// LoginItem turns "open at login" on or off: the login agent that starts
// ZapFast in the tray (zf:src/autostart.rs), and whether the system wants the
// person to approve it in System Settings → Login Items. It runs on a
// goroutine of its own and may block.
//
// cmd/zapfast wires it to sys.LoginItem. Left nil (tests), the switch only
// keeps the setting.
var LoginItem func(enable bool) (needsApproval bool, err error)

// loginState is what the Start at login row shows of the last change.
type loginState struct {
	approval bool
	on       bool // the switch's value while it is built (a pointer that outlives the frame)
}

// startAtLogin is the Start at login switch (SE-051).
func (s *Screen) startAtLogin(e *screen.Env, title string) {
	st := &e.S.Settings
	s.login.on = st.StartAtLogin
	if !s.flip(e, title, &s.login.on) {
		return
	}
	on := s.login.on
	st.StartAtLogin = on
	if !on {
		s.login.approval = false
	}
	hook := LoginItem
	if hook == nil {
		return
	}
	go func() {
		approval, err := hook(on)
		s.async.mu.Lock()
		s.async.login.has, s.async.login.on, s.async.login.approval = true, on, approval
		s.async.login.err = ""
		if err != nil {
			s.async.login.err = err.Error()
		}
		s.async.mu.Unlock()
		s.wake()
	}()
}
