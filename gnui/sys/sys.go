package sys

import (
	"log/slog"

	"gonativeui/app"
	"gonativeui/app/tray"

	"zapfast/abi"
	"zapfast/state"
	"zapfast/ui/screen"
)

// Dock is what ui/calls rings through (calls.Dock): the Dock icon bounces
// until the app is active, while a call to us rings (CA-029).
type Dock interface {
	Ring()
	Stop()
}

// Hooks connect what only other packages know. The packages that own the
// things hooked cannot be imported from here (ui/CONTRACT.md), so
// cmd/zapfast/screens.go sets them, each a line. A nil hook does nothing.
type Hooks struct {
	// Sleep tells the app lock the machine is going to sleep
	// (lock.Screen.Sleep); LockNow locks the app (lock.Screen.LockNow), for
	// the status item's Lock ZapFast.
	Sleep, LockNow func(s *state.State)
	// Save writes the settings waiting for their save, before sleep.
	Save func(s *state.State)
	// CallDock installs the Dock ui/calls rings through (calls.SetDock).
	CallDock func(s *state.State, d Dock)
	// JoinCall joins a call link (the hub's joinLink, exported by ui/calls).
	JoinCall func(s *state.State, a *state.Account, token []byte, video bool)
	// DeferOpen holds a clicked notification until the app is unlocked
	// (lock.Screen.Defer with applock.DeferMessage; message is empty for a
	// call). It reports whether it was held; without the hook a click
	// behind the lock waits in this package and opens after the unlock.
	DeferOpen func(s *state.State, account, chat string, message []byte) bool
}

// The posts to the app's own loop.
const (
	postInstance = 0x7a66_5359_0001 // a second launch's request is waiting
)

// System is the process's services (screen.System): notifications and the
// Dock, the status item and resident mode, links and the command line, the
// login item, the machine's sleep and wake.
type System struct {
	Hooks

	// The services. New fills the system's; tests replace them.
	center center
	dock   dockPort
	login  loginItem
	play   func(path string)
	tone   tonePort
	panel  panelPort // the sheet panels; nil: the app's
	// makeItem makes the status item; nil for none.
	makeItem func(tray.Options) (statusItem, error)
	// mainFn is the main window, nil with none open; quit asks the app to
	// quit; urlsOf reads the URLs of an open-URLs event; reload refreshes
	// the themes. Tests replace them.
	mainFn func() window
	quit   func()
	// keep applies the "Keep running" setting to the app.
	keep   func(bool)
	urlsOf func(*app.Event) []string
	reload func()

	app       *app.App
	s         *state.State
	listening bool
	// lateTray makes the status item at the first safe point, after the
	// first frame, instead of before the window: making it takes 30–45 ms
	// (Mac mini), a fifth of the startup budget (D51).
	lateTray bool

	// Window state: the app is the active one; the main window was shown
	// (false while it starts hidden or after the status item hid it).
	active bool
	shown  bool
	// kept is the Keep running setting the app was last given.
	kept bool

	n notifications
	d dockState
	t trayState
	l loginState
	k links
	i instances

	picks []picking
	watch []acctWatch
}

var _ screen.System = (*System)(nil)

// New returns the services.
func New() *System {
	y := &System{center: osCenter{}, dock: osDock{}, login: loginAgent(), play: playFile, tone: &osTone{}, makeItem: newStatusItem,
		urlsOf: (*app.Event).Strings, reload: reloadThemes, active: true, shown: true}
	y.mainFn, y.quit = y.mainWindow, func() { y.app.RequestQuit() }
	y.keep = func(on bool) {
		if y.app != nil {
			y.app.SetKeepRunning(on)
		}
	}
	return y
}

// window is the main window's handle (*app.Win).
type window interface {
	Show()
	Hide()
	Focus()
}

// Start runs before the main window opens: it registers the notification
// categories, reads the login item and starts listening to the second
// launches; the status item follows the first frame.
func (y *System) Start(a *app.App, s *state.State) error {
	y.app, y.lateTray = a, true
	y.begin(s, a.StartHidden())
	y.openArgs(launched.args)
	return nil
}

// begin starts the services over s. A hidden start (a login item) opens no
// window to the user and does not bounce: the window is not shown and the
// app is not the active one until something shows it.
func (y *System) begin(s *state.State, hidden bool) {
	y.s = s
	y.shown, y.active = !hidden, !hidden
	y.kept = s.Settings.KeepRunning // Config gave it to the app
	if !y.listening {
		y.listening = true
		s.Listen(y, abi.EventTagCall, abi.EventTagLink, abi.EventTagPickRequest, abi.EventTagAccountRemoved)
	}
	y.startNotifications()
	if y.lateTray && y.app != nil {
		y.app.AtSafePoint(y.startTray)
	} else {
		y.startTray()
	}
	y.startLogin()
	y.startInstance()
	if y.CallDock != nil {
		y.CallDock(s, y.Dock())
	}
}

// Event is the core's events the services follow (state.Listener).
func (y *System) Event(a *state.Account, e *abi.Event) {
	switch e.Tag {
	case abi.EventTagCall:
		y.call(a, &e.Call.Update)
	case abi.EventTagPickRequest:
		y.pick(a, &e.PickRequest)
	case abi.EventTagLink:
		if e.Link.Status.Tag == abi.LinkStatusTagLoggedOut || e.Link.Status.Tag == abi.LinkStatusTagUnlinked {
			y.forgetAccount(a)
		}
	case abi.EventTagAccountRemoved:
		y.forgetAccount(a)
	}
}

// AppEvent sees every app event first; true means it was handled.
func (y *System) AppEvent(a *app.App, s *state.State, e *app.Event) bool {
	y.app, y.s = a, s
	return y.appEvent(e)
}

func (y *System) appEvent(e *app.Event) bool {
	switch e.Kind {
	case app.EvService:
		if y.tone != nil && y.tone.Ended(e) {
			return true
		}
		if y.pickEnded(e) {
			return true
		}
		if r, ok := y.center.Result(e); ok {
			y.result(r)
			return true
		}
		if r, ok := y.center.Response(e); ok {
			y.respond(&r)
			return true
		}
	case app.EvPost:
		if e.ID == postInstance {
			y.pollInstance()
			return true
		}
	case app.EvCommand:
		return y.command(e)
	case app.EvOpenURLs:
		for _, u := range y.urlsOf(e) {
			y.openArg(u)
		}
		y.showMain()
		return true
	case app.EvActivated:
		y.active = true
	case app.EvDeactivated:
		y.active = false
	case app.EvReopen:
		y.shown = true
	case app.EvWillSleep:
		if y.Save != nil {
			y.Save(y.s)
		}
		if y.Sleep != nil {
			y.Sleep(y.s)
		}
	case app.EvDidWake:
		y.wake()
	case app.EvWillTerminate:
		y.stop()
	}
	return false
}

// showMain brings the main window forward, opening it again when it was
// closed (State.RaiseMain: the shell acts on it, with or without a window).
func (y *System) showMain() {
	y.shown = true
	y.s.RaiseMain()
}

// mainWindow is the main window, nil with none open.
func (y *System) mainWindow() window {
	if y.app == nil {
		return nil
	}
	if w := y.app.Window(); w != nil && w.Open() {
		return w
	}
	return nil
}

// Frame runs once per main-window frame (and for each wake while no window
// is open): it follows what the model says about unread chats and opens what
// links and notifications asked for.
func (y *System) Frame(s *state.State) {
	y.s = s
	y.syncLogin()
	y.watchChats(s)
	y.syncTrayMenu(s)
	y.syncKeepRunning(s)
	y.pollInstance()
	y.drain(s)
	y.flush(s)
}

// syncKeepRunning gives the app the Keep running setting as it changes, so
// the next close of the last window follows it (NT-010).
func (y *System) syncKeepRunning(s *state.State) {
	if on := s.Settings.KeepRunning; on != y.kept {
		y.kept = on
		y.keep(on)
	}
}

// wake is the machine waking: every account reconnects (zf:src/app.rs
// handle_power). The network was cut while it slept.
func (y *System) wake() {
	for _, a := range y.s.Accounts {
		if a.Conn != nil {
			a.Send(a.Cmd(abi.CommandTagResumed))
		}
	}
}

// stop ends the services with the app.
func (y *System) stop() {
	y.endTone()
	if y.t.item != nil {
		y.t.item.Remove()
		y.t.item = nil
	}
	y.stopInstance()
}

// acctWatch is what the services last saw of an account.
type acctWatch struct {
	countGen uint32 // Chats.Gen the unread count was last made at
	counted  bool
}

func (y *System) watchOf(a *state.Account) *acctWatch {
	for len(y.watch) <= a.Index {
		y.watch = append(y.watch, acctWatch{})
	}
	return &y.watch[a.Index]
}

// watchChats keeps the Dock badge (zf:src/app.rs sync_badge). It does
// nothing while nothing changed.
func (y *System) watchChats(s *state.State) {
	recount := false
	for _, a := range s.Accounts {
		w := y.watchOf(a)
		if g := a.Model.Chats.Gen(); !w.counted || g != w.countGen {
			w.counted, w.countGen = true, g
			recount = true
		}
	}
	if recount {
		y.setBadge(s)
	}
}

// post asks the app's loop for a turn, from any goroutine. postFn is post;
// tests replace it.
func post(id uint64) bool { return app.Post(app.Event{ID: id}, nil) }

var postFn = post

// logWarn is slog.Warn with the package's prefix.
func logWarn(msg string, args ...any) { slog.Warn("zapfast: "+msg, args...) }
