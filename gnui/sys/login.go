package sys

import (
	"sync"

	"gonativeui/app/login"

	"zapfast/state"
)

// Start at login (zf:src/autostart.rs): the login agent the bundle declares,
// which starts ZapFast with --start-hidden, so it runs in the status item
// with no window and no bounce. The system's own record is the only state:
// Settings reads it back rather than keeping a copy that could disagree.

// agentName is the loginAgents entry of gnui.json.
const agentName = "autostart"

func loginAgent() loginItem { return login.Agent(agentName) }

// LoginItem turns start at login on or off. It may block: call it from a
// goroutine, as Settings does (settings.LoginItem). needsApproval is true
// when the system wants the person to switch the item on in System Settings →
// General → Login Items.
func LoginItem(enable bool) (needsApproval bool, err error) {
	return setLogin(loginAgent(), enable)
}

func setLogin(it loginItem, enable bool) (bool, error) {
	var st login.State
	var err error
	if enable {
		st, err = it.Enable()
	} else {
		st, err = it.Disable()
	}
	return st == login.RequiresApproval, err
}

type loginState struct {
	mu      sync.Mutex
	has, on bool // the record, read at start
}

// startLogin reads the record on a goroutine of its own.
func (y *System) startLogin() {
	if y.login == nil || y.s == nil {
		return
	}
	l, it, wake := &y.l, y.login, y.s.Wake
	go func() {
		st, err := it.Status()
		if err != nil {
			return // not a bundled app, or not macOS: the setting keeps its value
		}
		l.mu.Lock()
		l.has, l.on = true, st == login.Enabled || st == login.RequiresApproval
		l.mu.Unlock()
		wake()
	}()
}

// syncLogin puts the record in the setting, once it was read.
func (y *System) syncLogin() {
	l := &y.l
	l.mu.Lock()
	has, on := l.has, l.on
	l.has = false
	l.mu.Unlock()
	if has && y.s.Settings.StartAtLogin != on {
		y.s.Settings.StartAtLogin = on
		y.s.Touch()
	}
}

var _ = (*state.State)(nil)
