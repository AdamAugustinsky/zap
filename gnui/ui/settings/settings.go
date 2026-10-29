package settings

import (
	"bytes"
	"sync/atomic"

	"gonativeui/app"
	"gonativeui/icon"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/format"
	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

// Layout, in points (zf:src/ui/settings.rs COLUMN_WIDTH, SIDE_MARGIN).
const (
	columnPt = 640 // widest the settings column grows
	sidePt   = 32  // least space beside the column
	padPt    = 10  // a row's side padding (rexui.CardLook.RowPadX) // grid:ok mirrors rexui's RowPadX, which this must match
)

// Screen is the settings window.
type Screen struct {
	env       *screen.Env
	win       atomic.Pointer[app.Win]
	titleLoc  i18n.Locale // the language the window's title is in
	listening bool

	scroll base.ScrollState

	// The search: the field's buffer, what it was folded for, and the line
	// shown when nothing matches.
	query     ui.TextBuffer
	searchKey ui.Key
	needle    []byte
	needleRev uint32
	noMatch   string

	tx    textCache
	icons struct{ plus, minus, play, pencil icon.ID }

	// The text fields that are not the search; the locked chats code is
	// masked.
	proxy, giphy ui.TextBuffer
	chatLock     ui.TextBuffer
	search       struct { // the search hint, measured
		loc   i18n.Locale
		scale float32
		hint  float32 // its width in points
	}
	acctBtn struct { // the account buttons, for laying them out
		key     ui.Key
		w       float32 // their width in a row, last frame's
		stacked bool
	}
	proxyEditing  bool
	seeded        bool
	profile       profile
	lock          lockForm
	chatLockReady atomic.Bool // a verifier waits in async
	chatLockSeq   uint64      // the last request for a verifier: the only one whose result is stored

	acct   map[*state.Account]*acctInfo
	files  files
	themes themeList
	sounds [2]soundOpts
	up     *Updates
	async  async
	login  loginState

	privOpts        []string
	updKey          ui.Key
	scrollToUpdates bool

	innerPx float32 // the width inside a row's padding
	built   bool
}

var _ screen.WindowScreen = (*Screen)(nil)

// New returns the window's screen.
func New() *Screen {
	s := &Screen{acct: map[*state.Account]*acctInfo{}, up: NewUpdates()}
	s.up.SetWake(s.wake)
	s.up.tick = s.applyChatLock
	return s
}

// Updates is the updater, for the main window's "ZapFast X is available"
// banner and the daily check (TODO(shell): the banner and the schedule).
func (s *Screen) Updates() *Updates { return s.up }

// quitApp quits the app through its normal path (⌘Q's: the quit policy, then
// the OnQuit hooks and the settings' last write), as app.App.RequestQuit
// does, without keeping a window's App for it; tests replace it.
var quitApp = func() { platform.RequestQuit(platform.QuitApp) }

// Config is the window's configuration.
func (s *Screen) Config(st *state.State) app.WindowConfig {
	cfg := app.WindowConfig{Title: i18n.T("Settings"), Width: 760, Height: 640, MinWidth: 480, MinHeight: 360, Materials: true}
	cfg.Titlebar, cfg.TrafficLights = rexui.TitlebarFor(toolbarStyle)
	return cfg
}

const toolbarStyle = theme.ToolbarRex

// setWindowTitle titles a window; tests replace it.
var setWindowTitle = (*app.Win).SetTitle

// retitle gives the window the title in the language set since it was opened
// (the title bar, the Window menu and Mission Control keep the old one
// otherwise).
func (s *Screen) retitle() {
	loc := i18n.Current()
	if loc == s.titleLoc {
		return
	}
	s.titleLoc = loc
	if w := s.win.Load(); w != nil {
		setWindowTitle(w, i18n.T("Settings"))
	}
}

// Init readies the screen for its window.
func (s *Screen) Init(e *screen.Env) error {
	s.env = e
	s.win.Store(e.A.Window())
	s.titleLoc = i18n.Current()
	pt := theme.MacWindow.Capsule.Glyph
	s.icons.plus = e.K.AddIcon(icon.Spec{Symbol: "plus", Fallback: '+', Pt: pt})
	s.icons.minus = e.K.AddIcon(icon.Spec{Symbol: "minus", Fallback: '−', Pt: pt})
	s.icons.play = e.K.AddIcon(icon.Spec{Symbol: "play.fill", Fallback: '▶', Pt: pt})
	s.icons.pencil = e.K.AddIcon(icon.Spec{Symbol: "pencil", Fallback: '✎', Pt: pt})
	for _, in := range s.acct { // the window measures the cache each time it opens
		in.storageAsked = false
	}
	if !s.lock.busy {
		s.lock.reset()
	}
	s.chatLock.Wipe() // the code is stored; the field opens empty
	s.profile.editing = false
	if !s.listening {
		s.listening = true
		s.listen(e)
	}
	s.themes.scan(s.wake)
	s.up.attach(e.S)
	s.up.SetQuit(func() { quitApp() })
	return nil
}

// changed notes that a setting changed: the state's observers (the theme,
// the shell) see its generation, and the state saves the settings soon.
func (s *Screen) changed(e *screen.Env) {
	e.S.SettingsChanged()
	if a := e.Acct(); a != nil {
		a.SettingsChanged()
	}
}

// wake asks for a frame of this window from any goroutine. The window's
// handle is taken on the main thread (App.Window makes it lazily).
func (s *Screen) wake() {
	if w := s.win.Load(); w != nil {
		w.Wake()
	}
}

// Build builds the whole window.
func (s *Screen) Build(e *screen.Env) {
	s.env = e
	c := e.C()
	w, h := c.Size()
	if w <= 0 || h <= 0 {
		return
	}
	W, H := float32(w), float32(h)
	r := e.K.Theme()
	s.refresh(e)
	s.poll(e)
	s.up.CheckIfDue(&e.S.Settings)
	if a := e.Acct(); a != nil {
		s.ask(e, a)
	}
	e.Begin(screen.Rect{W: W, H: H}, "Settings")
	c.NextBG(r.Colors[theme.ControlBackground])
	theme.At(c, 0, 0, W, H)
	c.Push(c.Box(base.Floating|ui.DrawBackground, "window"))
	tbH := theme.Snap(r.Window.Toolbars[toolbarStyle].Height)
	s.mainPage(e, W, H, tbH)
	s.toolbar(e, tbH)
	c.Pop()
	e.End()
	s.keys(e)
	s.built = true
}

// Closed runs once after the window has closed (screen.WindowCloser): what
// was typed in a password field goes with it. The framework gives secure
// event input back by itself.
func (s *Screen) Closed(e *screen.Env) {
	s.lock.clearFields()
	s.chatLock.Wipe()
	s.win.Store(nil)
}

// refresh readies what depends on the language and the search.
func (s *Screen) refresh(e *screen.Env) {
	s.tx.ensure(i18n.Current())
	s.retitle()
	if !s.seeded {
		s.seeded = true
		s.proxy.SetBytes(e.S.Settings.Proxy)
		s.giphy.SetBytes(e.S.Settings.GiphyKey)
	}
	if e.S.Req.FindSettings {
		e.S.Req.FindSettings = false
		e.C().SetFocus(s.searchKey)
	}
	if rev := s.query.Rev(); rev != s.needleRev || (s.needle == nil && s.query.Len() > 0) {
		s.needleRev = rev
		s.needle = format.AppendFold(s.needle[:0], trim(s.query.String()))
		s.noMatch = ""
		if len(s.needle) > 0 {
			s.noMatch = i18n.Format(i18n.T("No settings match “{query}”."), "{query}", trim(s.query.String()))
		}
	}
}

func trim(q string) string {
	i, j := 0, len(q)
	for i < j && (q[i] == ' ' || q[i] == '\t' || q[i] == '\n') {
		i++
	}
	for j > i && (q[j-1] == ' ' || q[j-1] == '\t' || q[j-1] == '\n') {
		j--
	}
	return q[i:j]
}

// keys takes Escape: it clears the search first, then closes the window
// (SE-001, SE-003).
func (s *Screen) keys(e *screen.Env) {
	c := e.C()
	if c.ModalOpen() || c.TextFieldFocused() || !c.TakeKey(ui.KeyEscape, 0) {
		return
	}
	switch {
	case s.query.Len() > 0:
		s.query.Reset()
	default:
		e.S.CloseWindow(state.WinSettings)
	}
}

// toolbar is the window's band: the title and the search field.
func (s *Screen) toolbar(e *screen.Env, tbH float32) {
	k := e.K.Kit
	title := i18n.T("Settings")
	r := k.Theme()
	tb := rexui.BeginToolbar(k, "Toolbar", rexui.ToolbarOpts{Style: toolbarStyle, Title: title, ContentEdge: true,
		Traffic: traffic, BandFill: r.Colors[theme.ControlBackground]})
	tb.Title()
	tb.Flexible()
	was := s.query.Len()
	fs := tb.Search(i18n.T("Search settings"), &s.query, i18n.T("Search settings"), s.searchWidth(e))
	s.searchKey = fs.Key
	if fs.Cancelled && was == 0 { // Escape in an empty search field: step back out
		e.S.CloseWindow(state.WinSettings)
	}
	tb.End()
}

// searchWidth is the search field's width in points: 200, or what its hint
// needs in a language where that is longer (upstream's field takes the whole
// column), up to what the toolbar has left beside the traffic lights and the
// title. The hint's width is measured once per language and scale.
func (s *Screen) searchWidth(e *screen.Env) float32 {
	k := e.K.Kit
	loc, scale := i18n.Current(), k.Scale()
	if s.search.loc != loc || s.search.scale != scale || s.search.hint == 0 {
		s.search.loc, s.search.scale = loc, scale
		s.search.hint = k.TextWidth(k.Theme().Styles[theme.StyleBody].Font, i18n.T("Search settings")) / scale
	}
	w, _ := e.C().Size()
	room := float32(w)/scale - 240
	return min(max(200, s.search.hint+56), max(room, 120))
}

// traffic draws the traffic lights (offscreen renders; tests set it).
var traffic bool

// mainPage is the settings: sections of rows in a scroll view that runs
// under the band.
func (s *Screen) mainPage(e *screen.Env, W, H, tbH float32) {
	k := e.K.Kit
	c := e.C()
	k.NextSize(ui.Px(W), ui.Px(H))
	k.NextScrollInset(tbH)
	v := k.BeginScroll(i18n.T("Settings"), &s.scroll, base.ScrollVertical)
	if s.scrollToUpdates && s.updKey != 0 { // About's "Update to X": the update block
		s.scrollToUpdates = false
		tr, ir := c.RectOf(s.updKey), c.Rect(v.Inner)
		_, sy := c.Scroll(v.Inner)
		c.SetScroll(v.Inner, 0, float64(max(tr[1]-ir[1]+float32(sy)-e.Pt(12), 0)))
	}
	// the width the scroll view gives its content (legacy scroll bars take
	// some), as laid out last frame
	avail := W
	if ir := c.Rect(v.Inner); ir[2]-ir[0] > 0 {
		avail = min(avail, ir[2]-ir[0])
	}
	colW := min(avail-2*e.Pt(sidePt), e.Pt(columnPt))
	side := theme.Snap(max((avail-colW)/2, 0))
	s.innerPx = max(colW-2*e.Pt(padPt), 0)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextPadding(side, e.Pt(20), side, e.Pt(24))
	c.NextAxis(ui.AxisY)
	c.Push(c.BoxKeyed(0, ui.HashString(0, "form"), nil))
	f := rexui.BeginForm(k, i18n.T("Settings"), rexui.FormOpts{})
	found := false
	for sec := secID(0); sec < numSections; sec++ {
		if s.section(e, &f, sec) {
			found = true
		}
	}
	rexui.EndForm(k, &f)
	if !found {
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.NextPadding(0, e.Pt(24), 0, 0)
		c.Push(c.BoxKeyed(0, ui.HashString(0, "nomatch"), nil))
		rexui.Label(k, s.noMatch, rexui.LabelOpts{Color: rexui.Of(theme.Label2), Wrap: true})
		c.Pop()
	}
	c.Pop()
	k.CloseScrollContent(&v)
	rexui.ScrollBars(k, &v)
	k.CloseScroll(&v)
}

// section builds one section if the search leaves a row in it.
func (s *Screen) section(e *screen.Env, f *rexui.Form, sec secID) bool {
	secMatch := len(s.needle) == 0 || bytes.Contains(s.tx.secFold[sec], s.needle)
	n := 0
	for i := range rowDefs {
		if d := &rowDefs[i]; d.sec == sec && s.exists(e, d) && s.shows(d, secMatch) {
			n++
		}
	}
	if n == 0 {
		return false
	}
	k := e.K.Kit
	f.Section(k, s.tx.sec[sec])
	n, nForm := 0, 0
	for i := range rowDefs {
		if d := &rowDefs[i]; d.sec == sec && s.exists(e, d) && s.shows(d, secMatch) {
			s.row(e, f, d, n, &nForm)
			n++
		}
	}
	footer := ""
	if sec == secUpdates {
		footer = s.tx.feedNote
	}
	f.EndSection(k, footer)
	return true
}

// shows reports whether the search leaves the row.
func (s *Screen) shows(d *rowDef, secMatch bool) bool {
	return len(s.needle) == 0 || secMatch || d.always || bytes.Contains(s.tx.fold[d.id], s.needle)
}
