// Package screen is the contract between the shell (package ui/shell) and
// the screens (ui/login, ui/chatlist, ui/conversation …): what a screen is
// given each frame (Env) and the slot interfaces the shell calls it through
// (Set). The shell never imports a screen; cmd/zapfast's screens.go fills
// the Set. See ../CONTRACT.md.
package screen

import (
	"math"

	"gonativeui/app"
	"gonativeui/img"
	"gonativeui/menu"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/state"
)

// Rect is a place in the window, in pixels (window coordinates): the
// state's Rect, so that state fields (Panes.PickerAnchor) take it as is.
type Rect = state.Rect

// Env is what a screen is given: its window's kit and App, and the state.
type Env struct {
	// K is the window's kit: k.Kit is the *base.Kit rexui takes.
	K *widget.Kit
	// A is the window's App (alerts and panels, the clipboard, Window()).
	A *app.App
	// S is the app's state.
	S *state.State

	tabs   tabStops // the main window's Tab order (TabStop)
	narrow bool
	tabbed bool
	inset  float32
}

// Narrow reports whether the main window is narrow: the list and what it
// opens take turns at the whole window. An opened view may be sliding in
// (its Rect off its place) while it is.
func (e *Env) Narrow() bool { return e.narrow }

// SetNarrow is the shell's: it says whether the window is narrow this frame.
func (e *Env) SetNarrow(on bool) { e.narrow = on }

// Tabbed reports whether the titlebar's tabs are up this frame: the open
// chat's name is its tab's then, and its header need not say it again.
func (e *Env) Tabbed() bool { return e.tabbed }

// SetTabbed is the shell's: it says whether the tabs are up this frame.
func (e *Env) SetTabbed(on bool) { e.tabbed = on }

// ContentInset is how far below the top of the Rect it is given a pane's
// content starts at rest, in pixels: what floats over that strip (the pane's
// title row, a banner) has the content scroll up under it.
func (e *Env) ContentInset() float32 { return e.inset }

// SetContentInset is the shell's: it says how far the slot it builds next
// runs under its pane's title row.
func (e *Env) SetContentInset(px float32) { e.inset = max(px, 0) }

// C is the window's ui context.
func (e *Env) C() *ui.Context { return e.K.Context() }

// Acct is the account on screen (nil before there is one).
func (e *Env) Acct() *state.Account { return e.S.Acct() }

// Pt converts points to pixels at the window's scale.
func (e *Env) Pt(pt float32) float32 { return theme.Snap(pt * e.K.Scale()) }

// Begin makes a floating box at r, the parent of what is built until End:
// a pane's root. label names it among its siblings.
func (e *Env) Begin(r Rect, label string) ui.ID {
	c := e.C()
	theme.At(c, r.X, r.Y, r.W, r.H)
	id := c.Box(base.Floating, label)
	c.Push(id)
	return id
}

// End closes Begin's box.
func (e *Env) End() { e.C().Pop() }

// Placeholder is a stub screen's body: its name, centred in r. Screens
// replace it.
func (e *Env) Placeholder(r Rect, name string) {
	e.Begin(r, name)
	k := e.K
	k.BeginContainer(ui.AxisY, ui.CrossCenter, "placeholder", ui.Px(r.W), ui.Px(r.H), 0)
	k.NextHeight(widget.Fill)
	k.BeginContainer(ui.AxisY, ui.CrossCenter, "top", ui.Pct(1), widget.Fill, 0)
	k.EndContainer()
	rexui.Label(k.Kit, name, rexui.LabelOpts{Style: rexui.TextTitle3, Color: rexui.Of(theme.Label3)})
	k.NextHeight(widget.Fill)
	k.BeginContainer(ui.AxisY, ui.CrossCenter, "bottom", ui.Pct(1), widget.Fill, 0)
	k.EndContainer()
	k.EndContainer()
	e.End()
}

// Initer is a slot's setup, called once per window it draws in, after the
// window's kit exists and before its first frame: add icons
// (e.K.AddIcon), register listeners (e.S.Listen), size buffers.
type Initer interface {
	Init(e *Env) error
}

// Pane is a screen drawn in a rectangle of the main window.
type Pane interface {
	Initer
	// Build builds the screen at r (window pixels). Call e.Begin(r, …)
	// first: the shell does not.
	Build(e *Env, r Rect)
}

// ToolbarPart is a pane's share of the window's toolbar: the items over
// the pane (the list's over the list, the conversation's over the
// conversation). Build them with tb's methods; the shell opens and ends the
// toolbar and places the panes.
type ToolbarPart interface {
	Toolbar(e *Env, tb *rexui.Toolbar)
}

// ListPane is the chat list's slot (ui/chatlist): the list column and its
// toolbar items.
type ListPane interface {
	Pane
	ToolbarPart
}

// ContentPane is the conversation's slot (ui/conversation): the header and
// the transcript above the composer, and the toolbar items over them.
type ContentPane interface {
	Pane
	ToolbarPart
}

// ComposerPane is the composer's slot (ui/composer): the band at the foot
// of the conversation.
type ComposerPane interface {
	Pane
	// Height is the band's height in pixels for width w: the shell lays
	// the transcript out above it (last frame's height is fine).
	Height(e *Env, w float32) float32
}

// SidePane is a column beside the conversation (ui/info, ui/msginfo, the
// in-chat search of ui/conversation).
type SidePane interface {
	Pane
	// Width is the column's width in pixels.
	Width(e *Env) float32
}

// Overlay is a screen drawn over the whole main window after the panes:
// dialogs, pickers, the cropper and the sticker maker. Build is called
// every frame; it builds nothing while it has nothing to show.
type Overlay interface {
	Initer
	Build(e *Env)
}

// Popover is a screen anchored at a control of the shell: the account
// switcher (ui/accounts) at the rail's avatar, while state.AccountMenu.
type Popover interface {
	Initer
	Build(e *Env, anchor Rect)
}

// PaneCloser is implemented by a side pane (or the Calls content) that ends
// something when it stops showing: Closed runs once, in the main window's
// frame, when the pane closes or its chat changes (msginfo stops watching
// receipts).
type PaneCloser interface {
	Closed(e *Env)
}

// SelectionBar is implemented by the conversation's slot: while
// state.Panes.Selecting, the shell builds the selection bar (count, Star,
// Forward, Copy, Delete, Cancel) in the composer's place at the foot of the
// conversation, and does not build the composer.
type SelectionBar interface {
	// SelectionBarHeight is the bar's height in pixels for width w.
	SelectionBarHeight(e *Env, w float32) float32
	// BuildSelectionBar builds the bar at r (window pixels).
	BuildSelectionBar(e *Env, r Rect)
}

// Band is a strip the shell lays across the foot of the linked main window,
// under the rail, the list and the content, on every page: the call put
// aside (ui/calls, CA-007).
type Band interface {
	Initer
	// BandHeight is the strip's height in pixels for the window's width w;
	// 0 while it has nothing to show.
	BandHeight(e *Env, w float32) float32
	// BuildBand builds the strip at r (window pixels).
	BuildBand(e *Env, r Rect)
}

// Guard is the app lock's timer (ui/lock's Screen, in the Lock slot): the
// shell calls Tick once per main-window frame after the pump and the
// commands, before it routes, and with a nil c while no window is open (the
// resident path); Event sees every app event (the system going to sleep).
type Guard interface {
	Tick(s *state.State, c *ui.Context)
	Event(s *state.State, e *app.Event) bool
}

// Updater is the updater's daily check (ui/settings's Updates): the shell
// calls it once per main-window frame, and while no window is open.
type Updater interface {
	CheckIfDue(s *state.Settings)
}

// UpdateNotice is an Updater that says which newer version a check found:
// the banner over the chat reads "ZapFast X is available", with Update.
type UpdateNotice interface {
	Available() (version string, ok bool)
}

// The viewer window's View menu zooms the picture, as upstream's ⌘+ ⌘- ⌘0
// do over the picture: the window's own menu model (WindowMenus) has these
// commands where the shared one has the app zoom roles. ui/viewer reads them
// with e.K.Command.
const (
	CmdPictureZoomIn menu.ID = 1000 + iota
	CmdPictureZoomOut
	CmdPictureActualSize
)

// WindowScreen is a window of its own (ui/settings, ui/viewer, ui/calls):
// the shell opens it when state.Windows.Want asks, with Config, and gives it
// a kit of its own.
type WindowScreen interface {
	Initer
	// Config is the window's configuration (title, size, chrome).
	Config(s *state.State) app.WindowConfig
	// Build builds the whole window. The shell's helper has called
	// e.K.Begin and the shared commands; it calls e.K.End after.
	Build(e *Env)
}

// Theme maps upstream's themes onto Rex (package theme). Apply runs at the
// start of every window's frame, before Kit.Begin: it must be cheap when
// nothing changed.
type Theme interface {
	Apply(k *widget.Kit, s *state.State)
}

// System is the process's services (package sys): notifications, the Dock,
// the status item and resident mode, single instance, links, wake.
type System interface {
	// Start runs in the app's Start, before the main window opens.
	Start(a *app.App, s *state.State) error
	// AppEvent sees every app event first; true means it was handled.
	AppEvent(a *app.App, s *state.State, e *app.Event) bool
	// Frame runs once per main-window frame, after the events are applied.
	Frame(s *state.State)
}

// Ticker is a package with no box of its own that must run every frame of
// the main window, after the events are applied and before the screens
// build (ui/conversation/av: a note that ended, the next note of a run, the
// account switched). It must not allocate in a steady frame.
type Ticker interface {
	Frame(e *Env)
}

// TickFunc is a function as a Ticker.
type TickFunc func(e *Env)

// Frame calls f.
func (f TickFunc) Frame(e *Env) { f(e) }

// Reducer is upstream's reducers (package logic): it listens for events
// (Init registers) and runs once per frame.
type Reducer interface {
	Init(s *state.State)
	Frame(s *state.State)
}

// Set is every slot. cmd/zapfast/screens.go fills it; a nil slot shows the
// shell's placeholder.
type Set struct {
	Login    Pane    // ui/login: QR and phone-code linking
	Lock     Pane    // ui/lock: the app lock screen
	Accounts Popover // ui/accounts: the switcher

	ChatList     ListPane     // ui/chatlist
	Conversation ContentPane  // ui/conversation: header and transcript
	Search       SidePane     // ui/conversation: the in-chat search pane
	Composer     ComposerPane // ui/composer
	Info         SidePane     // ui/info
	MessageInfo  SidePane     // ui/msginfo
	CallsPage    Pane         // ui/calls: the Calls page's history
	// CallsContent is the Calls page's content pane while no chat is open
	// ("Start call", CA-023): ui/calls.
	CallsContent Pane
	// CallBar is the call put aside, along the foot of every page (CA-007):
	// ui/calls.
	CallBar Band

	Picker       Overlay // ui/picker: emoji, GIFs, stickers
	Dialogs      Overlay // ui/dialogs
	Spotlight    Overlay // ui/spotlight: ⌘K, the search over everything
	Cropper      Overlay // ui/cropper
	StickerMaker Overlay // ui/stickermaker

	Settings WindowScreen // ui/settings
	Viewer   WindowScreen // ui/viewer
	Call     WindowScreen // ui/calls: the call window
	// Playground is the design playground's window (ui/playground), a dev
	// build's only: nil otherwise.
	Playground WindowScreen

	Theme  Theme   // theme
	System System  // sys
	Logic  Reducer // logic
	// Updates is the updater's daily check (cmd/zapfast fills it from
	// ui/settings's Updates).
	Updates Updater
	// Tickers run every main-window frame (Ticker).
	Tickers []Ticker
}

// Chrome is what every window shares with the shell: the menu model and the
// app-wide commands, run in whichever window's frame they arrive.
type Chrome interface {
	Menu() *menu.Model
	Commands(e *Env)
}

// WindowMenus is a Chrome that gives some windows a menu model of their own
// (the viewer's View menu zooms the picture): MenuOf is window w's model,
// nil for the shared one.
type WindowMenus interface {
	MenuOf(w state.Window) *menu.Model
}

// MenuRefresher is a Chrome whose menu models follow the interface language:
// RefreshMenus rebuilds them when the language changed and returns the
// models' revision, which moves with every rebuild. A window that sees a new
// revision gives its kit and app the new model (SyncMenu).
type MenuRefresher interface {
	RefreshMenus() int
}

// SyncMenu gives kit k and app a the menu model of window w (the shared one
// for any w without its own) once ch has rebuilt them: *seen is the revision
// the window has. It does nothing for a Chrome that does not refresh.
func SyncMenu(k *widget.Kit, a *app.App, ch Chrome, w state.Window, seen *int) {
	mr, ok := ch.(MenuRefresher)
	if !ok {
		return
	}
	rev := mr.RefreshMenus()
	if rev == *seen {
		return
	}
	*seen = rev
	m := ch.Menu()
	if wm, ok := ch.(WindowMenus); ok {
		if v := wm.MenuOf(w); v != nil {
			m = v
		}
	}
	k.SetMenu(m)
	a.SetMenu(m)
}

// NewKit makes a window's kit the way every ZapFast window has it: Rex, the
// system's materials where the window hosts them, the shared menu model.
func NewKit(a *app.App, ch Chrome) *widget.Kit {
	k := widget.NewKit(a.UI, a.Text, rex.Theme())
	k.Materials = a.Win != nil && a.Win.HasMaterials()
	if ch != nil {
		k.SetMenu(ch.Menu())
		a.SetMenu(ch.Menu())
	}
	return k
}

// Window is a WindowScreen's app.Builder: the window's kit, its image
// cache, the theme, the shared commands, then the screen.
type Window struct {
	s     *state.State
	w     state.Window
	scr   WindowScreen
	th    Theme
	ch    Chrome
	env   Env
	imgs  *img.Cache
	menus int // the revision of the menu models the window has (SyncMenu)
	ready bool
	gone  bool
}

// WindowCloser is implemented by a WindowScreen that frees what its window
// held (textures, players, watches) when the window has closed: Closed runs
// once, after the window is gone, from the main window's frame (the shell
// notices it).
type WindowCloser interface {
	Closed(e *Env)
}

// NewWindow is the app.Builder of window w, which scr builds: its kit, the
// theme, the shared commands, then scr.Build.
func NewWindow(s *state.State, w state.Window, scr WindowScreen, th Theme, ch Chrome) *Window {
	return &Window{s: s, w: w, scr: scr, th: th, ch: ch}
}

func (w *Window) Init(a *app.App) error {
	w.env = Env{K: NewKit(a, w.ch), A: a, S: w.s}
	if mc, ok := w.ch.(WindowMenus); ok && w.ch != nil {
		if m := mc.MenuOf(w.w); m != nil {
			w.env.K.SetMenu(m)
			a.SetMenu(m)
		}
	}
	if mr, ok := w.ch.(MenuRefresher); ok && w.ch != nil {
		w.menus = mr.RefreshMenus()
	}
	var tune func(o *img.Options)
	if t, ok := w.scr.(ImageTuner); ok {
		tune = t.ImageOptions
	}
	w.imgs = NewImages(a, w.env.K, tune)
	a.UI.Reserve(2048)
	w.ready = true
	return w.scr.Init(&w.env)
}

func (w *Window) Build(c *ui.Context) {
	if cw, ch := c.Size(); cw <= 0 || ch <= 0 {
		return
	}
	e := &w.env
	ApplyZoom(e.A, w.s)
	if w.ch != nil {
		SyncMenu(e.K, e.A, w.ch, w.w, &w.menus)
	}
	if w.th != nil {
		w.th.Apply(e.K, w.s)
	}
	e.K.Begin(c)
	PollImages(e.K, c)
	if w.ch != nil {
		w.ch.Commands(e)
	}
	w.scr.Build(e)
	e.K.End()
}

// Closed ends the window's screen and frees its image cache, once, after the
// window has closed (the shell calls it). The screen may keep its Env for
// background callbacks (a listener, a worker's wake): from then on it holds
// the state only, not the closed window's kit and App.
func (w *Window) Closed() {
	if w.gone {
		return
	}
	w.gone = true
	if c, ok := w.scr.(WindowCloser); ok && w.ready {
		c.Closed(&w.env)
	}
	if w.imgs != nil {
		if w.env.K != nil && w.env.K.Images() == w.imgs {
			w.env.K.SetImages(nil)
		}
		w.imgs.Close(0)
		w.imgs = nil
	}
	w.env.K, w.env.A = nil, nil
}

// CloseRequested clears the window's state when it closes.
func (w *Window) CloseRequested(*app.App) bool {
	w.s.Win.Open[w.w], w.s.Win.Want[w.w] = false, false
	w.s.Touch()
	return true
}

// ApplyZoom gives window a the interface zoom of the settings
// (Settings.Zoom, upstream's zoom: the whole interface, every window), when
// it changed; the window keeps its size. Every window calls it before
// Kit.Begin.
func ApplyZoom(a *app.App, s *state.State) {
	z := s.Settings.Zoom
	if a == nil || z <= 0 {
		return
	}
	if d := a.Zoom() - float32(math.Round(float64(z)*100))/100; d > 0.001 || d < -0.001 {
		a.SetZoom(z)
	}
}
