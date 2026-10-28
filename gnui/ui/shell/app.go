package shell

import (
	"sync/atomic"

	"gonativeui/app"
	"gonativeui/platform"

	"zapfast/state"
)

// postCore is the app post that drains the cores while no window is open.
const postCore = 1

// App is the application (app.Handler for app.Main): it opens the main
// window at Start and again when the Dock icon or a menu asks with none
// open, and keeps the cores drained while no window is (resident mode).
type App struct {
	Shell *Shell
	S     *state.State
	// Terminate runs last as the app ends (app.EvWillTerminate), after the
	// screens' OnQuit hooks and the settings' last write: cmd/zapfast
	// closes the cores there. On macOS the process exits when that event
	// returns, so main's deferred calls never run.
	Terminate func()

	a    *app.App
	main atomic.Pointer[app.Win]
}

var _ app.Handler = (*App)(nil)

// NewApp is the application over the shell.
func NewApp(sh *Shell) *App {
	h := &App{Shell: sh, S: sh.S}
	sh.raise = h.raiseMain
	return h
}

// terminate ends the app's state: the screens' quit hooks (the open
// draft), the settings written at once, then Terminate (the cores).
func (h *App) terminate() {
	if w := h.main.Load(); w != nil && w.Open() {
		h.Shell.noteFrame()
	}
	h.S.Quit()
	if h.Terminate != nil {
		h.Terminate()
	}
}

// raiseMain brings the main window forward, or opens it again when it
// closed (State.RaiseMain).
func (h *App) raiseMain() {
	if w := h.main.Load(); w != nil && w.Open() {
		w.Show()
		w.Focus()
		return
	}
	if h.a != nil {
		if err := h.openMain(h.a); err != nil {
			h.S.Toast([]byte(err.Error()), true)
		}
	}
}

// Wake is the cores' Config.Wake: a frame of the main window while it is
// open, else a post that drains them. Any goroutine: it reads the shell's
// mainHandle, not the window (app.Win.Open is the main thread's). A wake that
// reaches the window as it closes is drained by the post the close makes.
func (h *App) Wake() {
	if w := h.Shell.mainHandle.Load(); w != nil {
		w.Wake()
		return
	}
	h.postDrain()
}

// postDrain asks for the resident path's drain of the cores (postCore).
// When the app's post queue is full it owes the drain instead: the loop
// wakes for the posts waiting, and the next app event or main window frame
// pumps (platform.Wake alone would drain nothing, and a core wakes once
// until it is drained).
func (h *App) postDrain() {
	if !app.Post(app.Event{ID: postCore}, nil) {
		h.Shell.drainOwed.Store(true)
		platform.Wake()
	}
}

// drainCores is the resident path's drain of the cores, with no main window
// open: postCore, or a drain the full queue refused.
func (h *App) drainCores(a *app.App) {
	s := h.S
	h.Shell.drainOwed.Store(false) // this pump is the one owed
	if s.Pump(0) {
		h.Shell.WakeWindows() // a call window open alone shows the news
	}
	if sys := h.Shell.Set.System; sys != nil {
		sys.Frame(s)
	}
	if l := h.Shell.Set.Logic; l != nil {
		l.Frame(s)
	}
	// The lock keeps counting with no window: hidden in the tray counts as
	// unused.
	if g := h.Shell.guard; g != nil {
		g.Tick(s, nil)
	}
	if u := h.Shell.Set.Updates; u != nil {
		u.CheckIfDue(&s.Settings)
	}
	if s.Req.RaiseMain {
		s.Req.RaiseMain = false
		h.raiseMain()
	} else if h.Shell.WindowsAlone(a) {
		// A window opened alone comes forward once it is open.
		app.Post(app.Event{ID: postCore}, nil)
	}
}

// Start starts the services and opens the main window (hidden when the app
// starts hidden: a login item).
func (h *App) Start(a *app.App) error {
	h.a = a
	if sys := h.Shell.Set.System; sys != nil {
		if err := sys.Start(a, h.S); err != nil {
			return err
		}
	}
	return h.openMain(a)
}

func (h *App) openMain(a *app.App) error {
	// A main window asked for during a frame opens at the next safe point
	// (no id yet): asking again would open two.
	if w := h.main.Load(); w != nil && !w.Open() && w.ID() == 0 && w.Err() == nil {
		return nil
	}
	w, err := a.OpenWindow(Config(h.S), h.Shell)
	if err != nil {
		return err
	}
	h.main.Store(w)
	return nil
}

// Event handles the app events: the services see each first.
func (h *App) Event(a *app.App, e *app.Event) {
	s := h.S
	if e.Kind == app.EvWillTerminate {
		// After the services have seen it, whatever they answer.
		defer h.terminate()
	} else if h.Shell.drainOwed.Load() && (e.Kind != app.EvPost || e.ID != postCore) {
		// A drain the full queue refused (postDrain), whoever this event
		// is for: the main window's next frame pumps, else this event.
		if w := h.main.Load(); w != nil && w.Open() {
			w.Wake()
		} else {
			h.drainCores(a)
		}
	}
	if sys := h.Shell.Set.System; sys != nil && sys.AppEvent(a, s, e) {
		return
	}
	mainOpen := false
	if w := h.main.Load(); w != nil && w.Open() {
		mainOpen = true
	}
	// The screens' own (a permission's answer, a player's end); the
	// shell's below still see the app's.
	if e.Kind == app.EvService && s.AppEvent(e) {
		return
	}
	if g := h.Shell.guard; g != nil && g.Event(s, e) {
		return
	}
	switch e.Kind {
	case app.EvWindowClosed:
		if w := h.main.Load(); w != nil && w.ID() == e.Window {
			h.Shell.mainClosed()
			// A wake it took for the cores and never drew a frame for would
			// be lost (a core wakes once until it is drained): the resident
			// path drains what it left.
			h.postDrain()
		} else {
			h.Shell.WindowClosed(e.Window)
		}
	case app.EvDeactivated:
		s.Focused = false // the window's next frame says again
		if mainOpen {
			h.Shell.noteFrame() // it may have moved while the app was in front
		}
	case app.EvPost:
		if e.ID == postCore && !mainOpen {
			h.drainCores(a)
		}
		return
	case app.EvReopen:
		if !mainOpen {
			h.openMain(a)
			return
		}
	case app.EvCommand:
		// A menu item with no window open (the app's bar).
		switch e.ID {
		case uint64(CmdFind):
			s.Req.FocusSearch = true
			if !mainOpen {
				h.openMain(a)
			}
		case uint64(CmdShowWindow), uint64(CmdNewChat), uint64(CmdAbout), uint64(CmdShortcuts):
			if !mainOpen {
				h.openMain(a)
			}
		case uint64(CmdSettings):
			s.OpenWindow(state.WinSettings)
			if !mainOpen {
				h.openMain(a) // the shell opens windows from the main window's frame
			}
		case uint64(CmdQuit):
			a.RequestQuit()
		}
		return
	}
	a.DefaultEvent(e)
}
