// Package shell is ZapFast's main window and the app around it: the Rex
// chrome (the rail with the traffic lights overhanging it, the chat list
// pane, the conversation pane, the toolbar over them), routing between
// login, lock and the chats (zf:src/ui/mod.rs:35), the native menu bar
// (zf:src/macos.rs:47-110) and the shortcuts (zf:src/ui/keys.rs), and the
// Settings, media viewer and call windows. It calls every screen through a
// slot of screen.Set and imports none of them.
package shell

import (
	"bytes"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gonativeui/app"
	"gonativeui/app/winstate"
	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/accounts/acct"
	"zapfast/ui/screen"
)

// Layout, in points (zf:src/ui/rail.rs, zf:src/settings.rs defaults).
const (
	railPt  = 52 // the rail's width
	cellPt  = 48 // a destination's cell
	platePt = 36 // the lit plate behind a destination's icon
	// cornerAvatarPt is the switcher's button on the linking screen.
	cornerAvatarPt = 30
	minListPt      = 260 // the narrowest list, and the widest (zf:src/ui/chats.rs:21)
	maxListPt      = 520
	narrowPt       = 720 // a window narrower than this hides the rail and the list beside an open chat
)

// ToolbarStyle is the main window's toolbar: Rex's band.
const ToolbarStyle = theme.ToolbarRex

// Shell is the main window's builder (app.Builder, app.Initer) and the
// windows' shared chrome (screen.Chrome).
type Shell struct {
	S   *state.State
	Set *screen.Set

	// Traffic draws the traffic lights (offscreen renders; in a window the
	// system draws them).
	Traffic bool
	// Slides: the shell's panes move rather than jump: a narrow window
	// slides between the list and what it opens (SH-007), the list slides
	// away and back (⌘B), the side pane slides in and folds away. The app
	// sets it; a screen's test rig leaves it off, so that its steps meet
	// each view in its place at once.
	Slides bool

	a     *app.App
	env   screen.Env
	model *menu.Model
	ic    struct{ chats, calls, starred, archived, locked, settings, logo, copy, sidebar icon.ID }
	hint  struct { // the empty pane's shortcut hint
		text string
		loc  i18n.Locale
	}
	wins [state.NumWindows]*app.Win
	wbs  [state.NumWindows]*screen.Window
	imgs *img.Cache // the main window's (screen.NewImages)
	// me is our picture on the account button; elsewhere the unread chats
	// of the other accounts, for its dot.
	me        myAvatar
	elsewhere struct {
		ok  bool
		key uint64
		n   int
	}

	builds    int
	composerH float32
	counted   uint32 // the Chats.Gen the counts are of
	// countedAcct is the account (Serial) they are of: two accounts' lists
	// can be at the same generation.
	countedAcct uint64
	archived    int
	locked      int
	// archivedUnread is how many archived chats are unread (the badge).
	archivedUnread int
	inits          bool
	// toastBuf is this frame's copy of the toasts; toastKeys the box of each
	// shown (at most 4 info and 3 errors), for its height.
	toastBuf   []state.Toast
	toastKeys  [8]toastKey
	tb         rexui.Toolbar   // the titlebar band
	listTB     rexui.Toolbar   // the panes' title rows
	chatTB     []rexui.Toolbar // each chat pane slot's title row (the split view's others)
	chatLabel  []string        // and its name
	sideTB     rexui.Toolbar
	focus      paneID        // the pane in use, opaque (noteFocus)
	focusChat  model.ChatRef // the open chat noteFocus last saw
	phone      []byte        // our number, formatted for the account block
	tabs       chatTabs      // the titlebar's tabs of open chats
	railScroll base.ScrollState
	slide      narrowSlide
	sideFullW  float32       // the side pane's width while open, which it folds from
	kb         keyboard      // the single keys' layer (keyboard.go)
	spaces     []state.Space // the spaces, as last listed (spaces.go)
	sp         spaceUI
	bannerH    float32                // the open chat's banner, last frame, which the transcript runs under
	front      [state.NumWindows]bool // opened alone: to bring forward once open
	listDrag   float32                // the list's width as a drag of its edge goes
	split      splitUI                // the split view's (split.go)
	chatPane   [4]float32             // the chat pane this frame, for a tab dragged onto it
	placed     bool                   // the main window was put back where it was
	seenW      float32                // the window's size last frame (points)
	seenH      float32
	sizedAt    int64  // when the size last changed (Input.Time), 0 once noted
	said       []byte // the frame's announcement (reused)
	bannerKey  ui.Key // the banner's box, to read its height
	// When the banner's passing states (connecting, loading the history)
	// began, for which account: they show only once they last (bannerGrace).
	bannerFrom int64
	bannerAcct uint64
	bannerOn   bool // the banner showed last frame

	guard   screen.Guard          // the Lock slot's timer, when it has one
	selBar  screen.SelectionBar   // the Conversation slot's selection bar
	selBars []screen.SelectionBar // and the split view's other panes' (MorePanes)
	callsTB screen.ToolbarPart    // the CallsPage slot's toolbar items
	compact compactList           // the ChatList slot's avatars in the rail
	updates screen.UpdateNotice   // the Updates slot's newer version
	viewerM *menu.Model           // the viewer window's menu model
	menuLoc i18n.Locale           // the language the models are in
	menuRev int                   // moves with every rebuild (RefreshMenus)
	menus   int                   // the revision the main window has
	// The side pane last built, and the chat and account it was for: when
	// they change, the pane's PaneCloser runs.
	lastPane state.Pane
	lastChat model.ChatRef
	lastAcct int
	// raise brings the main window forward, opening it again if it closed
	// (App sets it; else the main window's own handle).
	raise func()
	// mainHandle is the main window's handle while it is open, for App.Wake
	// on the cores' goroutines (app.Win.Open is the main thread's): its
	// frames set it, mainClosed clears it.
	mainHandle atomic.Pointer[app.Win]
	// drainOwed is a drain of the cores whose post the app's full queue
	// refused (App.postDrain): the main window's next frame, or else the
	// next app event, pumps.
	drainOwed atomic.Bool
}

type compactList interface {
	Compact(e *screen.Env, r screen.Rect)
}

var (
	_ app.Builder        = (*Shell)(nil)
	_ app.Initer         = (*Shell)(nil)
	_ app.CloseRequester = (*Shell)(nil)
	_ screen.Chrome      = (*Shell)(nil)
)

// New returns the shell over s with the screens of set.
func New(s *state.State, set *screen.Set) (*Shell, error) {
	m, err := newModel()
	if err != nil {
		return nil, err
	}
	if set == nil {
		set = &screen.Set{}
	}
	vm, err := newViewerModel()
	if err != nil {
		return nil, err
	}
	sh := &Shell{S: s, Set: set, model: m, viewerM: vm, menuLoc: i18n.Current()}
	sh.guard, _ = set.Lock.(screen.Guard)
	sh.selBar, _ = set.Conversation.(screen.SelectionBar)
	sh.chatTB = make([]rexui.Toolbar, 1+len(set.MorePanes))
	sh.chatLabel = make([]string, 1+len(set.MorePanes))
	sh.chatLabel[0] = "Chat header"
	for i, p := range set.MorePanes {
		bar, _ := p.Conversation.(screen.SelectionBar)
		sh.selBars = append(sh.selBars, bar)
		sh.chatLabel[i+1] = "Chat header " + strconv.Itoa(i+2)
	}
	sh.callsTB, _ = set.CallsPage.(screen.ToolbarPart)
	sh.compact, _ = set.ChatList.(compactList)
	sh.updates, _ = set.Updates.(screen.UpdateNotice)
	return sh, nil
}

// Config is the main window: Rex's chrome over the system's materials.
func Config(s *state.State) app.Config {
	cfg := app.Config{Title: "Zap", Width: 1100, Height: 720, MinWidth: 600, MinHeight: 400, Clear: [4]float32{1, 1, 1, 1}}
	if s != nil && s.Settings.WindowW > 0 && s.Settings.WindowH > 0 {
		cfg.Width, cfg.Height = s.Settings.WindowW, s.Settings.WindowH
	}
	cfg.Titlebar, cfg.TrafficLights = rexui.TitlebarFor(ToolbarStyle)
	// Rex's glass behind the window, always: Settings.Transparency only
	// decides whether the field covers it (Build), so turning it off and on
	// takes effect at once.
	cfg.Backdrop = platform.BackdropGlass
	if s != nil {
		cfg.Place = func(w *platform.Window) { restoreFrame(&s.Settings, w) }
	}
	return cfg
}

// restoreFrame puts the main window back where it was, kept on a screen, and
// zooms it if it was zoomed, while it is still hidden (app.Config.Place): it
// shows with its first frame in its place and at its size, rather than
// centred and then moving and growing. Settings.WindowX and WindowY are the
// frame's top left in points from the primary display's, y down (upstream's
// window_x and window_y). A window that was zoomed when it closed zooms
// from its user frame, which unzooming returns it to (SH-014).
func restoreFrame(st *state.Settings, w *platform.Window) {
	x, y, fw, fh := w.FrameRect()
	if fw <= 0 || fh <= 0 {
		return
	}
	if st.HasWindowPos {
		saved := winstate.State{Frame: winstate.Rect{X: float64(st.WindowX), Y: float64(st.WindowY), W: fw, H: fh}}
		pl := winstate.Apply(winstate.Config{Width: fw, Height: fh}, saved, winstate.ScreensFromPlatform(platform.Screens()))
		if pl.Restored && pl.HasPosition && (pl.Frame.X != x || pl.Frame.Y != y) {
			w.SetFrameRect(pl.Frame.X, pl.Frame.Y, pl.Frame.W, pl.Frame.H)
		}
	}
	if st.WindowMaximized && !w.Zoomed() {
		w.Zoom()
	}
}

// Context is the main window's ui context (nil before Init): the tests read
// its semantic tree.
func (sh *Shell) Context() *ui.Context {
	if sh.env.K == nil {
		return nil
	}
	return sh.env.K.Context()
}

// Init makes the main window's kit and initialises every slot.
func (sh *Shell) Init(a *app.App) error {
	sh.a = a
	sh.env = screen.Env{K: screen.NewKit(a, sh), A: a, S: sh.S}
	if sh.imgs != nil { // the main window opened again: the last one's is done
		sh.imgs.Close(0)
	}
	sh.imgs = screen.NewImages(a, sh.env.K, nil)
	a.UI.Reserve(4096)
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	k := sh.env.K
	// the rail's destinations and the titlebar's toggle as toolbar items
	// (theme/icons.go)
	add := func(sym string, fb rune) icon.ID { return k.AddIcon(ztheme.ToolIcon(sym, fb)) }
	sh.ic.chats = add("message", '✉')
	sh.ic.calls = add("phone", '☎')
	sh.ic.starred = add("star", '☆')
	sh.ic.archived = add("archivebox", '▤')
	sh.ic.locked = add("lock", '⚿')
	sh.ic.settings = add("gearshape", '⚙')
	sh.ic.sidebar = add("sidebar.left", '▤')
	sh.ic.copy = k.AddIcon(icon.Spec{Symbol: "doc.on.doc", Fallback: '⧉', Pt: 14})
	// The empty pane's logo: upstream's bubble at 0.674 of its 72 pt disc.
	sh.ic.logo = k.AddIcon(icon.Spec{Symbol: "message", Fallback: '✉', Pt: 44})
	if !sh.inits {
		sh.inits = true
		if sh.Set.Logic != nil {
			sh.Set.Logic.Init(sh.S)
		}
	}
	e := &sh.env
	sh.split.init(e)
	for _, p := range [...]screen.Initer{sh.Set.Login, sh.Set.Lock, sh.Set.Accounts, sh.Set.ChatList, sh.Set.Conversation,
		sh.Set.Search, sh.Set.Composer, sh.Set.Info, sh.Set.MessageInfo, sh.Set.CallsPage,
		sh.Set.Picker, sh.Set.Dialogs, sh.Set.Cropper, sh.Set.StickerMaker, sh.Set.CallsContent, sh.Set.CallBar, sh.Set.Spotlight, sh.Set.Peek} {
		if !nilSlot(p) {
			if err := p.Init(e); err != nil {
				return err
			}
		}
	}
	for _, p := range sh.Set.MorePanes {
		for _, x := range [...]screen.Initer{p.Conversation, p.Composer} {
			if !nilSlot(x) {
				if err := x.Init(e); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// nilSlot reports whether a slot is empty (a nil interface or a typed nil).
func nilSlot(p screen.Initer) bool { return p == nil }

// CloseRequested lets the main window close: the app keeps running (the
// status item), as upstream's Close Window does.
func (sh *Shell) CloseRequested(*app.App) bool {
	sh.noteFrame()
	return true
}

// Build builds the main window's frame.
func (sh *Shell) Build(c *ui.Context) {
	if sh.mainHandle.Load() == nil {
		sh.mainHandle.Store(sh.a.Window())
	}
	w, h := c.Size()
	if w <= 0 || h <= 0 || c.Scale() <= 0 {
		return
	}
	s, e, k := sh.S, &sh.env, sh.env.K
	screen.ApplyZoom(sh.a, s)
	screen.SyncMenu(k, sh.a, sh, state.NumWindows, &sh.menus)
	if sh.Set.Theme != nil {
		sh.Set.Theme.Apply(k, s)
	}
	k.Begin(c)
	// Over the window's material (Config) the field is a scrim and the panes
	// are translucent; with transparency off, or reduced by the system, it is
	// the opaque field over it.
	th := k.Theme()
	th.SetBackdrop(s.Settings.Transparency && sh.a.Win != nil && sh.a.Win.Backdrop() != platform.BackdropNone)
	screen.PollImages(k, c)
	// Nothing is read while the lock hides the chats, nor while the
	// window is hidden or in the Dock.
	s.Focused = !k.Inactive() && !s.Lock.Locked && !c.Input().Hidden
	sh.drainOwed.Store(false) // this pump is the drain a full queue refused
	if s.Pump(2 * time.Millisecond) {
		sh.WakeWindows()
	}
	sh.builds++
	if sh.builds == 3 { // after the first frame (two builds) is on glass
		s.StartCores()
	}
	sh.Commands(e)
	if sh.Set.System != nil {
		sh.Set.System.Frame(s)
	}
	if sh.Set.Logic != nil {
		sh.Set.Logic.Frame(s)
	}
	// The app lock: a finished password check, the idle time, a lock asked
	// for (⌘⇧L) or at launch; before routing, so a lock shows at once.
	if sh.guard != nil {
		sh.guard.Tick(s, c)
		if s.Lock.Locked {
			s.Focused = false
		}
	}
	if sh.Set.Updates != nil {
		sh.Set.Updates.CheckIfDue(&s.Settings)
	}
	for _, t := range sh.Set.Tickers {
		t.Frame(e)
	}
	W, H := float32(w), float32(h)
	// The window's size is remembered for the next start (points).
	// The window's size is in screen points: the zoom scales the content,
	// not the window.
	// It is noted once it has held for a moment: a zoom animates through
	// sizes that are neither the user's nor the zoomed one.
	now := c.Input().Time
	if pw, ph := sh.a.Unzoomed(W/c.Scale()), sh.a.Unzoomed(H/c.Scale()); pw != sh.seenW || ph != sh.seenH {
		sh.seenW, sh.seenH, sh.sizedAt = pw, ph, max(now, 1)
	}
	if sh.sizedAt != 0 {
		if now-sh.sizedAt >= sizeSettleNs || !sh.placed {
			sh.sizedAt = 0
			sh.noteSize(sh.seenW, sh.seenH)
		} else {
			c.RequestFrame()
		}
	}
	sh.place()
	r := k.Theme()
	theme.At(c, 0, 0, W, H)
	c.Push(c.Box(base.Floating, "window"))
	r.Chrome.Background(c, r) // Rex's field, under everything
	a := s.Acct()
	sh.composerH = 0
	switch {
	case s.Lock.Locked:
		sh.full(sh.Set.Lock, "Lock", W, H)
	case a == nil || !a.Model.Linked() || s.AddingAccount:
		sh.full(sh.Set.Login, "Login", W, H)
	default:
		sh.sideButtons(c)
		sh.main(a, W, H)
		s.RecordPlace()
	}
	if s.Lock.Locked {
		// The lock screen shows no toasts: one can name a contact or a file
		// (zf:src/ui/mod.rs:40-48).
		sh.ageToasts()
	} else {
		sh.toasts(W, H)
	}
	sh.announce(c)
	c.Pop()
	sh.windows()
	sh.escape(a)
	// Tab follows the deliberate order once the screens give their stops
	// (screen.TabStop, SH-016).
	e.TabOrder()
	k.End()
}

// full builds a full-window screen under the toolbar band (the traffic
// lights and the window's drag area).
func (sh *Shell) full(p screen.Pane, name string, W, H float32) {
	e := &sh.env
	tbH := sh.toolbarH()
	r := screen.Rect{X: 0, Y: tbH, W: W, H: H - tbH}
	if p == nil {
		e.Placeholder(r, name)
	} else {
		p.Build(e, r)
	}
	tb := rexui.BeginToolbar(e.K.Kit, "Toolbar", rexui.ToolbarOpts{Style: ToolbarStyle, Title: "Zap", Traffic: sh.Traffic})
	// With several accounts the linking screen has the switcher in its
	// top-left corner, after the window's buttons (zf:src/ui/accounts.rs:
	// 303-316, AC-006).
	var key ui.Key
	a := sh.S.Acct()
	corner := p == sh.Set.Login && a != nil && len(sh.S.Accounts) > 1
	if corner {
		c := e.C()
		tb.BeginCustom(i18n.T("Accounts")+"###corner", ui.Px(e.Pt(cornerAvatarPt)))
		c.NextSize(ui.Pct(1), ui.Pct(1))
		c.NextAxis(ui.AxisY)
		c.NextJustify(ui.JustifyCenter)
		c.Push(c.Box(0, "center"))
		key = sh.accountButton(a, cornerAvatarPt, theme.Window)
		c.Pop()
		tb.EndCustom()
	}
	tb.Title()
	tb.End()
	if corner && sh.S.AccountMenu {
		sh.accounts(sh.anchorOf(key, screen.Rect{X: e.Pt(80), Y: e.Pt(10), W: e.Pt(cornerAvatarPt), H: e.Pt(cornerAvatarPt)}))
	}
}

func (sh *Shell) toolbarH() float32 {
	return theme.Snap(sh.env.K.Theme().Window.Toolbars[ToolbarStyle].Height)
}

// main is the linked window: the rail, the list, the conversation and the
// pane beside it, the toolbar over them, then the overlays.
func (sh *Shell) main(a *state.Account, W, H float32) {
	s, e, k := sh.S, &sh.env, sh.env.K
	c, r := k.Context(), k.Theme()
	tbH := sh.toolbarH()
	narrow := W < e.Pt(narrowPt)
	e.SetNarrow(narrow)
	// A locked chat outside the open locked folder is not shown, whatever
	// left it open: no header, transcript, composer or side pane.
	_, open := a.OpenChatView()
	if !open {
		sh.noChatDrop(W, H)
	}
	sh.paneChanged(a)
	// The call put aside runs along the foot of every page.
	if b := sh.Set.CallBar; b != nil {
		if bh := b.BandHeight(e, W); bh > 0 {
			H -= bh
			b.BuildBand(e, screen.Rect{X: 0, Y: H, W: W, H: bh})
		}
	}
	railW, fullW := e.Pt(railPt), e.Pt(min(max(s.Settings.SidebarWidth, minListPt), maxListPt))
	// Hiding the list (⌘B) slides it away under the rail and the chat
	// widens into its place, over Motion.Standard (at once under Reduce
	// Motion); its share shownList eases, so dragging its edge follows the
	// pointer. A narrow window has no hidden list: with no chat open the list
	// is the page, and its rail stays to leave it by.
	hide := float32(1)
	if s.Panes.ListHidden && !narrow {
		hide = 0
	}
	shownList := hide
	if sh.Slides {
		shownList = c.AnimateValue(ui.HashString(0, "shell list shown"), hide, ztheme.Toward(r, ztheme.T.DurStandard, hide > 0))
	}
	listW := theme.Snap(fullW * shownList)
	if narrow && open {
		railW, listW = 0, 0
	}
	if narrow && !open {
		listW = W - railW
	}
	sideW := float32(0)
	side := sh.sideSlot(s.Panes.Pane)
	// A narrow window gives an open side pane the whole window: it shows
	// Back (Panes.Covering), and closing it shows the chat again.
	covering := narrow && open && s.Panes.Pane != state.PaneNone
	if covering != s.Panes.Covering {
		s.Panes.Covering = covering
		s.Touch()
	}
	switch {
	case covering:
		railW, listW, sideW = 0, 0, W
	case s.Panes.Pane != state.PaneNone && open:
		sideW = e.Pt(s.Settings.InfoPaneWidth)
		if side != nil {
			sideW = side.Width(e)
		}
		sideW = min(sideW, W-railW-listW-e.Pt(320))
		if sideW < e.Pt(240) {
			sideW = 0
		}
	}
	// Beside the chat, the side pane slides in from the window's edge and
	// folds away when it closes, the chat giving and taking back its room,
	// over Motion.Standard (at once under Reduce Motion). Covering a narrow
	// window, it comes and goes at once.
	shownSide := float32(1)
	if !covering && sh.Slides {
		on := float32(0)
		if sideW > 0 {
			on, sh.sideFullW = 1, sideW
		}
		shownSide = c.AnimateValue(ui.HashString(0, "shell side shown"), on, ztheme.Toward(r, ztheme.T.DurStandard, on > 0))
		if shownSide > 0 && shownSide < 1 {
			sideW = theme.Snap(sh.sideFullW * shownSide)
		}
	}
	// The panes float on the field (panes.go): Inset from the window's
	// edges, Gutter between them, under the titlebar band.
	in, gut := r.Px.Inset, r.Px.Gutter
	bottom := H - in
	var panes paneRects
	left := max(railW, in)
	if listW > 0 {
		x1 := left + listW
		if narrow && !open {
			x1 = W - in
		}
		panes[paneList] = [4]float32{left, tbH, x1, bottom}
		left = x1 + theme.Snap(gut*shownList)
	}
	right := W - in
	if sideW > 0 {
		x0 := max(right-sideW, left)
		if covering {
			x0 = in
		}
		panes[paneSide] = [4]float32{x0, tbH, right, bottom}
		right = x0 - gut
	}
	if !covering && right-left > e.Pt(120) {
		panes[paneChat] = [4]float32{left, tbH, right, bottom}
	}
	sh.chatPane = panes[paneChat]
	if ref := s.Req.UsePane; ref != 0 {
		s.Req.UsePane = 0
		a.UsePane(ref)
	}
	sh.followGrip(a)
	split := false
	if open && !narrow && !covering {
		split = sh.splitLayout(a, &panes, gut)
	}
	sh.noteFocus(a, &panes, split)
	// whether the tabs are up, for the chat's header, which builds before
	// them (it leaves the name to the tab)
	tabX0 := e.Pt(trafficPt+accountChipPt+16) + sh.sidebarW()
	if cp := panes[paneChat]; shown(cp) {
		tabX0 = max(tabX0, cp[0])
	}
	e.SetTabbed(sh.tabsUp(a, tabX0, W-in))

	// A narrow window slides between the list and what it opens (SH-007):
	// an opened chat comes in from the right over the list, dimmed beneath
	// it; going back brings the list in from the left. The titlebar is the
	// new view's from the start.
	view := narrowNone
	if narrow && sh.Slides {
		view = narrowList
		if open {
			view = narrowDetail
		}
	}
	dx := float32(0) // the sliding view's distance from its place
	p, sliding := sh.slide.step(view, c.Input().Time, ztheme.Dur(r, ztheme.T.DurStandard))
	if sliding {
		c.RequestFrame()
		if view == narrowDetail {
			sh.beneath(a, W, H, tbH, p)
			dx = (1 - p) * W
		} else {
			// What it covers is gone (the chat closed): the field.
			theme.At(c, 0, tbH, W, H-tbH)
			c.NextBG(r.Colors[theme.Field])
			c.Box(base.Floating|ui.DrawBackground, "slide backdrop")
			dx = -(1 - p) * W
		}
		for i := range panes {
			if shown(panes[i]) {
				panes[i][0] += dx
				panes[i][2] += dx
			}
		}
	}
	if railW > 0 {
		sh.rail(a, dx, railW, H, tbH)
	}
	for i := range panes {
		if shown(panes[i]) {
			sh.surface(paneID(i), panes[i])
		}
	}
	// A pane that puts none of its controls in the Tab order (a screen not
	// on it yet: the Calls page) leaves the frame's Tab to build order, so
	// that Tab still reaches them; so do selection mode (its checkboxes)
	// and the picker, as upstream's (zf:src/ui/mod.rs main_navigation).
	if s.Panes.Selecting || s.Panes.Picker != state.PickerNone {
		e.TabInBuildOrder()
	}
	// The slots build in the window's root, over their panes, so that their
	// Rects are window coordinates; each pane's title row after its content.
	// The list and the side pane take presses while they move (a sidebar on
	// macOS does): what is under the pointer is still where it is drawn.
	if shownList > 0 && shownList < 1 || shownSide > 0 && shownSide < 1 {
		c.RequestFrame()
	}
	if lp := panes[paneList]; shown(lp) {
		n := e.TabStops()
		sh.listIn(lp, fullW, shownList, W, H, !narrow && shownList < 1)
		if e.TabStops() == n {
			e.TabInBuildOrder()
		}
	}
	switch {
	case split:
		sh.splitPanes(a, gut, tbH, bottom)
	case shown(panes[paneChat]):
		sh.chat(a, panes[paneChat], open)
	}
	if sp := panes[paneSide]; shown(sp) && shownSide < 1 && (side == nil || s.Panes.Pane == state.PaneNone) {
		// folding away, the slot closed: the glass alone
	} else if sp := panes[paneSide]; shown(sp) {
		// sliding in, its content rides in at its full width from the
		// pane's moving edge, past the window's, as it lands
		if shownSide < 1 {
			sp[2] = sp[0] + sh.sideFullW
		}
		sr := sh.body(sp)
		if side != nil {
			n := e.TabStops()
			side.Build(e, sr)
			if e.TabStops() == n {
				e.TabInBuildOrder()
			}
		} else {
			e.Placeholder(sr, paneName(s.Panes.Pane))
		}
		// A side pane with items for its title row (an inspector's title)
		// builds them there.
		sh.header(&sh.sideTB, "Side header", paneName(s.Panes.Pane), sp)
		if band, ok := side.(screen.ToolbarPart); ok && side != nil {
			band.Toolbar(e, &sh.sideTB)
		} else {
			sh.sideTB.Title()
		}
		sh.sideTB.End()
	}
	sh.titlebar(a)
	// the tabs start over the conversation, after the account block
	sh.chatTabs(a, tabX0, W-in, tbH)
	if lp, cp := panes[paneList], panes[paneChat]; shown(lp) && shown(cp) && shownList == 1 {
		sh.listHandle(lp[2]+gut/2, tbH, bottom-tbH)
	}
	if sliding {
		// A slide moves what is drawn, so the window takes no press, hover
		// or scroll until it ends (zf:src/ui/mod.rs slide_shield).
		theme.At(c, 0, 0, W, H)
		c.Box(base.Floating|ui.Clickable, "slide shield")
	}

	for _, o := range [...]screen.Overlay{sh.Set.Picker, sh.Set.Cropper, sh.Set.StickerMaker, sh.Set.Dialogs, sh.Set.Peek, sh.Set.Spotlight} {
		if o != nil {
			o.Build(e)
		}
	}
}

// listIn builds the list in its pane r inside the one box it always has,
// at the window's corner so that its Rects stay window coordinates and its
// boxes keep their identities from frame to frame. While the list slides
// in or out (⌘B) the box clips it at the pane's moving edge: the list keeps
// its full width fullW where it is, the edge sweeps over it, and it fades
// with the share shown. At rest the box clips nothing, so what the list
// opens past its edge (a menu, a popover) is whole.
func (sh *Shell) listIn(r [4]float32, fullW, shown, W, H float32, sliding bool) {
	c := sh.env.C()
	w, fl := W, ui.Flags(0)
	if sliding {
		w, fl = r[2], ui.Clip
		r[2] = r[0] + fullW
	}
	// a space's list comes in from the side of the one it left (spaces.go)
	dx, fade := sh.spaceShift()
	if dx != 0 {
		w, fl = r[2], ui.Clip
		r[0], r[2] = r[0]+dx, r[2]+dx
	}
	sh.swipeSpaces(r)
	theme.At(c, 0, 0, w, H)
	c.NextOpacity(shown * fade)
	c.Push(c.Box(base.Floating|fl, "list pane"))
	sh.list(r)
	c.Pop()
}

// list builds the chat list (or the Calls page's) in its pane r, and the
// pane's title row: the list's items, or the Calls tab's when its slot gives
// them (New call for New chat, CL-001).
func (sh *Shell) list(r [4]float32) {
	s, e := sh.S, &sh.env
	lr := sh.body(r)
	// The list's foot keeps clear of the pane's rounded corners, and the
	// spaces' strip sits there when there are several (spaces.go).
	lr.H = max(lr.H-e.K.Theme().Px.RadiusLarge/2, 0)
	if sh.spaces = s.Spaces(sh.spaces[:0]); s.Page == state.PageChats && len(sh.spaces) > 1 {
		strip := e.Pt(spaceStripPt)
		lr.H = max(lr.H-strip, 0)
		defer sh.spaceStrip(screen.Rect{X: lr.X, Y: lr.Y + lr.H, W: lr.W, H: strip})
	}
	switch {
	case s.Page == state.PageCalls && sh.Set.CallsPage != nil:
		sh.Set.CallsPage.Build(e, lr)
	case s.Page == state.PageCalls:
		e.Placeholder(lr, "Calls")
	case sh.Set.ChatList != nil:
		sh.Set.ChatList.Build(e, lr)
	default:
		e.Placeholder(lr, "Chat list")
	}
	sh.header(&sh.listTB, "List header", "Zap", r)
	var part screen.ToolbarPart = sh.Set.ChatList
	if tp := sh.callsTB; tp != nil && s.Page == state.PageCalls {
		part = tp
	}
	if part != nil {
		part.Toolbar(e, &sh.listTB)
	} else {
		sh.listTB.Title()
	}
	sh.listTB.End()
}

// chat builds what the pane beside the list shows in r: the open chat (the
// banner, the transcript, the composer or the selection bar), the Calls page,
// or the empty pane; and the pane's title row.
func (sh *Shell) chat(a *state.Account, r [4]float32, open bool) {
	sh.chatIn(a, r, open, 0)
}

// chatIn is chat in slot i: 0 the first screens (and the one pane with no
// split), others the split view's other panes' (MorePanes). The account's
// banners show in the first.
func (sh *Shell) chatIn(a *state.Account, r [4]float32, open bool, i int) {
	s, e := sh.S, &sh.env
	conv, comp, bar := sh.Set.Conversation, sh.Set.Composer, sh.selBar
	if i > 0 && i <= len(sh.Set.MorePanes) {
		p := sh.Set.MorePanes[i-1]
		conv, comp, bar = p.Conversation, p.Composer, sh.selBars[i-1]
	}
	b := sh.body(r)
	top := b.Y
	switch {
	case open && i == 0:
		// the transcript runs under the title row and the banner, which
		// build after it, over it: the banner's height is the last frame's
		top += sh.bannerH
	case open:
	case s.Page != state.PageCalls || sh.Set.CallsContent == nil:
		top += sh.banner(a, screen.Rect{X: b.X, Y: b.Y, W: b.W})
	}
	switch {
	case open:
		// In selection mode the conversation's selection bar takes the
		// composer's place.
		selecting := s.Panes.Selecting && bar != nil
		ch := e.Pt(52)
		switch {
		case selecting:
			ch = bar.SelectionBarHeight(e, b.W)
		case comp != nil:
			ch = comp.Height(e, b.W)
		}
		if !s.Side {
			sh.composerH = ch
		}
		foot := r[3] - ch
		// from the pane's top: its content starts below the title row and
		// the banner at rest and scrolls up under them (ContentInset)
		tr := screen.Rect{X: b.X, Y: r[1], W: b.W, H: max(foot-r[1], 0)}
		if conv != nil {
			e.SetContentInset(top - r[1])
			conv.Build(e, tr)
			e.SetContentInset(0)
		} else {
			e.Placeholder(screen.Rect{X: b.X, Y: top, W: b.W, H: max(foot-top, 0)}, "Conversation")
		}
		if i == 0 {
			sh.bannerH = sh.banner(a, screen.Rect{X: b.X, Y: b.Y, W: b.W})
		}
		cr := screen.Rect{X: b.X, Y: foot, W: b.W, H: ch}
		switch {
		case selecting:
			bar.BuildSelectionBar(e, cr)
		case comp != nil:
			comp.Build(e, cr)
		default:
			e.Placeholder(cr, "Composer")
		}
	case s.Page == state.PageCalls && sh.Set.CallsContent != nil:
		n := e.TabStops()
		sh.Set.CallsContent.Build(e, b)
		if e.TabStops() == n {
			e.TabInBuildOrder()
		}
	default:
		sh.empty(screen.Rect{X: b.X, Y: top, W: b.W, H: r[3] - top})
	}
	tb := &sh.chatTB[min(i, len(sh.chatTB)-1)]
	sh.header(tb, sh.chatLabel[min(i, len(sh.chatLabel)-1)], "Zap", r)
	if open && conv != nil {
		conv.Toolbar(e, tb)
	} else {
		tb.Title()
	}
	tb.End()
}

// beneath builds a narrow window's list view under the chat sliding in
// over it, dimming as the chat comes (zf:src/ui/mod.rs slide_in).
func (sh *Shell) beneath(a *state.Account, W, H, tbH, p float32) {
	e, c := &sh.env, sh.env.K.Context()
	// The list beneath takes no key: the frame's keys are no one's
	// mid-slide, but for those a focused field took at Begin.
	for i := range c.KeyCount() {
		c.ClaimKey(i)
	}
	railW, in := e.Pt(railPt), e.K.Theme().Px.Inset
	sh.rail(a, 0, railW, H, tbH)
	lp := [4]float32{railW, tbH, W - in, H - in}
	sh.surface(paneList, lp)
	sh.list(lp)
	theme.At(c, 0, 0, W, H)
	c.NextBG(uint32(p * 0x50))
	c.Box(base.Floating|ui.DrawBackground, "slide dim")
}

// What a narrow window shows: the chat list, or what was opened from it
// (zf:src/ui/mod.rs NarrowView).
const (
	narrowNone = iota // the window is not narrow
	narrowList
	narrowDetail
)

// narrowSlide follows a narrow window's view from frame to frame
// (zf:src/ui/mod.rs narrow_slide).
type narrowSlide struct {
	view  int
	on    bool
	start int64
}

// step takes the frame's view at time now (ns) and reports how far the
// slide to it, dur seconds long (ztheme.T.DurStandard; 0 under Reduce
// Motion: none), has come (0 to 1, eased out) while one is under way. A view
// shown on widening or narrowing the window appears without one.
func (sl *narrowSlide) step(view int, now int64, dur float32) (float32, bool) {
	before := sl.view
	sl.view = view
	if view == narrowNone {
		sl.on = false
		return 1, false
	}
	if before != narrowNone && before != view {
		sl.on, sl.start = true, now
	}
	if !sl.on || dur <= 0 {
		sl.on = false
		return 1, false
	}
	t := float32(now-sl.start) / (dur * 1e9)
	if t >= 1 {
		sl.on = false
		return 1, false
	}
	return ztheme.EaseOut(t), true
}

// announce has the screen reader say what waits (State.Announce, AX-001):
// an error interrupts it, the rest waits its turn; while the app is locked
// only the connection and the history are said (zf:src/ui/mod.rs announce).
func (sh *Shell) announce(c *ui.Context) {
	said, assertive := sh.S.TakeAnnouncement(sh.said[:0], !sh.S.Lock.Locked)
	sh.said = said
	switch {
	case len(said) == 0:
	case assertive:
		c.Announce(string(said))
	default:
		c.AnnouncePolite(string(said))
	}
}

// sideButtons steps through the window's history with the mouse's side
// buttons, as a browser does: Back goes back (in a narrow window, from a
// chat opened from the list to the list), Forward forward
// (zf:src/ui/mod.rs history_buttons). Overlays are not places, and the
// buttons stay out of them while one is open, as the keyboard's shortcuts
// do: a dialog, a picker, a reaction, a selection, a menu or another modal.
func (sh *Shell) sideButtons(c *ui.Context) {
	in, s := c.Input(), sh.S
	back, fwd := in.Pressed[ui.ButtonBack], in.Pressed[ui.ButtonForward]
	if !back && !fwd {
		return
	}
	if s.Dialog.Open() || s.Panes.Picker != state.PickerNone || s.Panes.ReactTarget.Open() || s.Panes.Selecting || c.ModalOpen() {
		return
	}
	if back {
		s.Back()
	} else {
		s.Forward()
	}
}

// paneChanged runs the PaneCloser of the side pane that stopped showing: it
// closed, or its chat or account changed.
func (sh *Shell) paneChanged(a *state.Account) {
	p := sh.S.Panes.Pane
	if p == sh.lastPane && a.Open == sh.lastChat && a.Index == sh.lastAcct {
		return
	}
	if prev := sh.sideSlot(sh.lastPane); prev != nil {
		if pc, ok := prev.(screen.PaneCloser); ok {
			pc.Closed(&sh.env)
		}
	}
	sh.lastPane, sh.lastChat, sh.lastAcct = p, a.Open, a.Index
}

// sideSlot is the slot of side pane p, nil for none.
func (sh *Shell) sideSlot(p state.Pane) screen.SidePane {
	switch p {
	case state.PaneInfo:
		return sh.Set.Info
	case state.PaneSearch:
		return sh.Set.Search
	case state.PaneMessageInfo:
		return sh.Set.MessageInfo
	}
	return nil
}

func paneName(p state.Pane) string {
	switch p {
	case state.PaneInfo:
		return "Info"
	case state.PaneSearch:
		return "Search"
	}
	return "Message info"
}

// empty is the conversation pane with no chat open (zf:src/ui/conversation.rs:
// 274-315): the logo, ZapFast, where the chats are, and the shortcut hint
// while the hints show. Upstream draws the two middle lines untranslated.
func (sh *Shell) empty(r screen.Rect) {
	s, e, k := sh.S, &sh.env, sh.env.K
	c, rr := k.Context(), k.Theme()
	e.Begin(r, "empty")
	msg := i18n.T("Select a chat on the left.")
	if a := s.Acct(); a == nil || a.Model.Chats.Len() == 0 {
		msg = i18n.T("Your chats appear on the left as they load.")
	}
	c.NextSize(ui.Px(r.W), ui.Px(r.H))
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextPadding(0, 0, 0, e.Pt(60)) // upstream's centre is 30 pt up
	c.NextRole(ui.RoleGroup)
	c.NextSemName("Zap")
	c.NextSemValueBytes(base.StrBytes(msg))
	c.Push(c.Box(0, "empty column"))
	d := e.Pt(72)
	c.NextSize(ui.Px(d), ui.Px(d))
	c.NextBG(rr.Colors[theme.Fill2])
	c.NextRadius(d / 2)
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.Push(c.Box(ui.DrawBackground, "logo"))
	k.Icon(sh.ic.logo, rr.Colors[theme.Label3])
	c.Pop()
	c.NextSize(ui.Px(1), ui.Px(e.Pt(12)))
	c.Box(0, "logo gap")
	rexui.Label(k.Kit, "Zap", rexui.LabelOpts{Style: rexui.TextLargeTitle})
	c.NextSize(ui.Px(1), ui.Px(e.Pt(4)))
	c.Box(0, "title gap")
	rexui.Label(k.Kit, msg, rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	if s.Settings.ShowShortcutHints {
		c.NextSize(ui.Px(1), ui.Px(e.Pt(8)))
		c.Box(0, "hint gap")
		rexui.Label(k.Kit, sh.emptyHint(), rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label3)})
	}
	c.Pop()
	e.End()
}

// emptyHint is "Ctrl+K to search · ? for keyboard shortcuts" in the
// language shown, with macOS's key names (zf:src/ui/keys.rs label), made
// again only when the language changes.
func (sh *Shell) emptyHint() string {
	if loc := i18n.Current(); sh.hint.text == "" || sh.hint.loc != loc {
		t := i18n.T("Ctrl+K to search · ? for keyboard shortcuts")
		t = strings.ReplaceAll(t, "Ctrl", "⌘")
		t = strings.ReplaceAll(t, "Strg", "⌘")
		t = strings.ReplaceAll(t, "Alt", "⌥")
		sh.hint.text, sh.hint.loc = t, loc
	}
	return sh.hint.text
}

// railStops are the destinations' places in the Tab order.
var railStops = [...]screen.TabStop{destChats: screen.StopChats, destCalls: screen.StopCalls, destStarred: screen.StopStarred,
	destArchived: screen.StopArchived, destLocked: screen.StopLocked, destSettings: screen.StopSettings}

// The rail's destinations (zf:src/ui/rail.rs:62).
type destination uint8

const (
	destChats destination = iota
	destCalls
	destStarred
	destArchived
	destLocked
	destSettings
)

// rail builds the navigation rail on the field: Chats, Calls and Starred at
// the top below the traffic lights; Archived and Locked while there are
// such chats, and Settings at the foot (our account is the titlebar's
// account block). While the list is hidden the chats' avatars fill the space
// between (ui/chatlist's Compact).
func (sh *Shell) rail(a *state.Account, x, railW, H, tbH float32) {
	s, e, k := sh.S, &sh.env, sh.env.K
	c := k.Context()
	sh.count(a)
	theme.At(c, x, 0, railW, H)
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.Push(c.Box(base.Floating, "rail"))
	c.NextSize(ui.Pct(1), ui.Px(tbH))
	c.Box(0, "lights") // the traffic lights overhang the rail here
	// Below the lights the rail scrolls when the window is too short for
	// it, and Tab brings the focused button into view (SH-005,
	// zf:src/ui/rail.rs:32-33).
	sh.railScroll.HideBars = true
	k.NextWidth(ui.Pct(1))
	k.NextHeight(ui.Px(max(H-tbH, 0)))
	v := k.BeginScroll("Rail", &sh.railScroll, base.ScrollVertical)
	cell := e.Pt(cellPt)
	y := float32(0)
	at := func(focused bool, h float32) {
		if focused {
			c.ScrollIntoView(v.Inner, [4]float32{0, y, railW, y + h})
		}
		y += h
	}
	chats := s.Page == state.PageChats
	// The tooltips are upstream's labels, with the shortcut (rail.rs:286-333).
	at(sh.dest(destChats, sh.ic.chats, i18n.T("Chats"), c.Fmt().Str(i18n.T("Chats")).Str(" (⌘+Shift+1)").String(), chats && a.Mode == state.ListChats), cell)
	at(sh.dest(destCalls, sh.ic.calls, i18n.T("Calls"), c.Fmt().Str(i18n.T("Calls")).Str(" (⌘+Shift+2)").String(), s.Page == state.PageCalls), cell)
	at(sh.dest(destStarred, sh.ic.starred, i18n.T("Starred messages"), "", chats && a.Mode == state.ListStarred), cell)
	foot := e.K.Theme().Px.Inset // the panes' bottom margin
	bottom := cell + foot
	if sh.archived > 0 || a.Mode == state.ListArchived {
		bottom += cell
	}
	if sh.locked > 0 || a.Mode == state.ListLocked {
		bottom += cell
	}
	mid := H - tbH - 3*cell - bottom
	// The rail's empty middle: a double click shows or hides the chat list
	// (the avatars of a hidden list over it take their own presses).
	if s.Panes.ListHidden && sh.Set.ChatList != nil && mid > e.Pt(64) {
		c.NextSize(ui.Pct(1), ui.Px(mid))
		// (the pictures' list over it has its own empty part, chatlist)
		sh.toggleOnDoubleClick(c.Box(ui.Clickable, "compact"))
		if sh.compact != nil {
			// in the rail's scrolled content, under its three destinations
			sh.compact.Compact(e, screen.Rect{X: x, Y: 3 * cell, W: railW, H: mid})
		}
	} else {
		c.NextSize(ui.Pct(1), ui.Px(max(mid, 0)))
		c.NextRole(ui.RoleGroup)
		c.NextSemName(railHint(s.Panes.ListHidden))
		sh.toggleOnDoubleClick(c.Box(ui.Clickable, "spacer"))
	}
	at(false, max(mid, 0))
	if sh.archived > 0 || a.Mode == state.ListArchived {
		at(sh.dest(destArchived, sh.ic.archived, i18n.T("Archived"), "", chats && a.Mode == state.ListArchived), cell)
	}
	if sh.locked > 0 || a.Mode == state.ListLocked {
		at(sh.dest(destLocked, sh.ic.locked, i18n.T("Locked"), "", chats && a.Mode == state.ListLocked), cell)
	}
	// Upstream leaves these two untranslated (rail.rs:325-331).
	settingsTip := c.Fmt().Str(i18n.T("Settings")).Str(" (⌘+,)").String()
	if s.Win.Open[state.WinSettings] {
		settingsTip = c.Fmt().Str(i18n.T("Close settings")).Str(" (⌘+,)").String()
	}
	at(sh.dest(destSettings, sh.ic.settings, i18n.T("Settings"), settingsTip, s.Win.Open[state.WinSettings]), cell)
	c.NextSize(ui.Pct(1), ui.Px(foot))
	c.Box(0, "foot")
	k.CloseScrollContent(&v)
	k.CloseScroll(&v)
	c.Pop()
}

// railHint names the rail's empty part: what a double click there does.
func railHint(hidden bool) string {
	if hidden {
		return i18n.T("Double-click to show the chat list")
	}
	return i18n.T("Double-click to hide the chat list")
}

// toggleOnDoubleClick shows or hides the chat list (⌘B) on a double click
// on box id, an empty part of the sidebar.
func (sh *Shell) toggleOnDoubleClick(id ui.ID) {
	if sh.env.C().Signal(id).DoubleClicked {
		sh.S.Panes.ListHidden = !sh.S.Panes.ListHidden
		sh.S.Touch()
	}
}

// place remembers where the main window is, once, at its first frame (the
// window opened in its place: restoreFrame). After that the frame is read
// only when the window may have moved (noteFrame), never per frame: the
// platform's lookup is not free.
func (sh *Shell) place() {
	if sh.placed || sh.a == nil || sh.a.Win == nil {
		return
	}
	w := sh.a.Window()
	if w == nil {
		return
	}
	sh.placed = true
	if w.Fullscreen() || w.Zoomed() {
		return // the user frame is the one saved, which zooming kept
	}
	if x, y, fw, fh := w.Frame(); fw > 0 && fh > 0 {
		sh.keepFrame(x, y)
	}
}

// noteSize remembers the window's user size for the next start (points,
// Settings.WindowW and WindowH): a zoomed window keeps the size it unzooms
// to, and WindowMaximized says it is zoomed (zf:src/app.rs
// sync_window_state). The platform is asked only when the size changed.
func (sh *Shell) noteSize(pw, ph float32) {
	st := &sh.S.Settings
	if w := sh.mainWin(); w != nil && w.Zoomed() {
		st.WindowMaximized = true
		return
	}
	st.WindowW, st.WindowH = pw, ph
	sh.noteFrame() // a resize from a left or top edge moves it too
}

// sizeSettleNs is how long a new window size holds before it is noted.
const sizeSettleNs = 250_000_000

// mainWin is the main window's handle while it is open, nil otherwise.
func (sh *Shell) mainWin() *app.Win {
	if sh.a == nil || sh.a.Win == nil {
		return nil
	}
	if w := sh.a.Window(); w != nil && w.Open() {
		return w
	}
	return nil
}

// noteFrame remembers where the main window is now: its size changed, the
// app was left (EvDeactivated), the window is asked to close, or the app
// quits. Full screen leaves the place alone; a zoomed window keeps the
// place it unzooms to, and WindowMaximized says it is zoomed.
func (sh *Shell) noteFrame() {
	if !sh.placed {
		return
	}
	w := sh.mainWin()
	if w == nil || w.Fullscreen() {
		return
	}
	st := &sh.S.Settings
	if w.Zoomed() {
		st.WindowMaximized = true
		return
	}
	st.WindowMaximized = false
	if x, y, fw, fh := w.Frame(); fw > 0 && fh > 0 {
		sh.keepFrame(x, y)
	}
}

func (sh *Shell) keepFrame(x, y float64) {
	st := &sh.S.Settings
	if !st.HasWindowPos || float32(x) != st.WindowX || float32(y) != st.WindowY {
		st.WindowX, st.WindowY, st.HasWindowPos = float32(x), float32(y), true
	}
}

// listHandle is the chat list's right edge at x, from y down h: dragging it
// resizes the list within 260–520 points, and the width is saved
// (Settings.SidebarWidth, zf:src/ui/chats.rs:18-36).
func (sh *Shell) listHandle(x, y, h float32) {
	s, e := sh.S, &sh.env
	c := e.C()
	w := e.Pt(6)
	theme.At(c, x-w/2, y, w, h)
	c.NextCursor(ui.CursorResizeEW)
	c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask())
	c.NextRole(ui.RoleSplitter)
	c.NextSemName(i18n.T("Resize"))
	sig := c.Signal(c.BoxKeyed(ui.Clickable|base.Floating, ui.HashString(0, "zapfast/list-edge"), nil))
	switch {
	case sig.Dragging:
		// DragDelta runs from the press: the width the drag began at, and
		// as far again as the pointer has gone
		if sh.listDrag == 0 {
			sh.listDrag = s.Settings.SidebarWidth
		}
		if wd := min(max(sh.listDrag+sig.DragDelta[0]/e.K.Scale(), minListPt), maxListPt); wd != s.Settings.SidebarWidth {
			s.Settings.SidebarWidth = wd
			s.Touch()
		}
	case !sig.Dragging:
		sh.listDrag = 0
	}
}

// noChatDrop takes files and pictures dropped on the window while no chat
// is open: "Open a chat first", as staging with no chat says upstream
// (zf:src/app.rs:5498-5500, CP-020). The composer's own target takes drops
// once a chat is open.
func (sh *Shell) noChatDrop(W, H float32) {
	c := sh.env.K.Context()
	theme.At(c, 0, 0, W, H)
	id := c.BoxKeyed(base.Floating, ui.HashString(0, "zapfast/no-chat-drop"), nil)
	c.AcceptDrop(id, ui.DropSpec{Kinds: ui.DropFiles | ui.DropImage, Ops: ui.DropCopy})
	if c.DropSignal(id).Dropped {
		sh.S.Toast([]byte(i18n.T("Open a chat first")), true)
	}
}

// anchorOf is the box with key as laid out (the popover's anchor: a press
// on it closes the popover and does not reach the button, which would open
// it again), else def.
func (sh *Shell) anchorOf(key ui.Key, def screen.Rect) screen.Rect {
	if rr := sh.env.K.Context().RectOf(key); rr[2] > rr[0] && rr[3] > rr[1] {
		return screen.Rect{X: rr[0], Y: rr[1], W: rr[2] - rr[0], H: rr[3] - rr[1]}
	}
	return def
}

// accountButton is our avatar as the switcher's button, sizePt points on
// the backdrop role (zf:src/ui/accounts.rs:26-55): our picture, and a dot
// when another account has unread chats. A click opens or closes the
// switcher. It returns the button's key.
func (sh *Shell) accountButton(a *state.Account, sizePt float32, backdrop theme.Role) ui.Key {
	s, e, k := sh.S, &sh.env, sh.env.K
	c, r := k.Context(), k.Theme()
	p := k.PressState("Accounts", base.HoverOn)
	p.Name = i18n.T("Switch account") // zf:src/ui/accounts.rs:33
	size := e.Pt(sizePt)
	k.BeginPress(&p, ui.RoleButton, ui.Px(size), ui.Px(size), 0, nil)
	name := a.Model.Me.Name()
	if len(name) == 0 {
		name = a.Model.Me.ID()
	}
	rexui.Avatar(k.Kit, model.S(name), rexui.AvatarOpts{Size: sizePt, Image: sh.myPicture(a, int32(size)), Backdrop: rexui.Of(backdrop)})
	if sh.unreadElsewhere() > 0 {
		// Ringed in the backdrop's colour so it reads on any picture.
		ring, dot := e.Pt(12), e.Pt(9)
		theme.At(c, size-e.Pt(3)-ring/2, e.Pt(3)-ring/2, ring, ring)
		c.NextBG(r.Colors[backdrop])
		c.NextRadius(ring / 2)
		c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask())
		c.Push(c.Box(base.Floating|ui.DrawBackground, "elsewhere"))
		c.NextCenter()
		c.NextSize(ui.Px(dot), ui.Px(dot))
		c.NextBG(r.Colors[theme.Accent])
		c.NextRadius(dot / 2)
		c.Box(base.Floating|ui.DrawBackground, "dot")
		c.Pop()
	}
	sig := k.EndPress(&p)
	e.TabStop(screen.StopProfile, sig.ID)
	if sig.Clicked {
		s.AccountMenu = !s.AccountMenu
		s.Touch()
	}
	return p.Key
}

// myPicture is our profile picture at px pixels from the window's image
// cache (asked for once; it follows the file the core reports), 0 while
// there is none.
func (sh *Shell) myPicture(a *state.Account, px int32) img.Handle {
	m := &sh.me
	me := a.Model.Me.ID()
	// A relinked number is a new session (Serial) and, once it says so, a
	// new Me: its picture is asked for again.
	if m.acct != a || m.serial != a.Serial || m.me != a.Model.Me.Gen() || len(me) == 0 {
		*m = myAvatar{acct: a, serial: a.Serial, me: a.Model.Me.Gen(), path: m.path[:0]}
		if len(me) == 0 {
			return 0
		}
	}
	if l := a.Model.Link.Status(); !m.asked && l != abi.LinkStatusTagLoggedOut && l != abi.LinkStatusTagUnlinked {
		// An unlinked core has none to give, and would spend the one ask.
		m.asked = true
		a.FetchAvatar(me, false) // the file arrives as an event
	}
	if g := a.Model.Avatars.Gen(); !m.read || g != m.gen {
		m.read, m.gen = true, g
		path, id, ok := a.Model.Avatars.Picture(me, false)
		if !ok {
			path = nil
		}
		if string(path) != string(m.path) {
			m.path = append(m.path[:0], path...)
		}
		// the picture's identity: a new one reuses the last one's file
		m.key = img.Key{Hash: id}
	}
	cache := sh.env.K.Images()
	if len(m.path) == 0 || cache == nil {
		return 0
	}
	key := m.key
	key.W, key.H, key.Flags = px, px, img.Cover
	h, _ := cache.Request(key, img.File(model.S(m.path)))
	return h
}

// unreadElsewhere is how many unread chats the accounts not on screen have
// (acct.UnreadElsewhere), counted again when their chats change or the
// minute turns (mutes run out).
func (sh *Shell) unreadElsewhere() int {
	s := sh.S
	if len(s.Accounts) < 2 {
		return 0
	}
	now := s.Now().Unix()
	key := uint64(s.Active)<<56 ^ uint64(len(s.Accounts))<<48 ^ uint64(now/60)
	for i, a := range s.Accounts {
		if i != s.Active {
			key = key*31 + uint64(a.Model.Chats.Gen())
		}
	}
	if !sh.elsewhere.ok || sh.elsewhere.key != key {
		sh.elsewhere.ok, sh.elsewhere.key, sh.elsewhere.n = true, key, acct.UnreadElsewhere(s, now)
	}
	return sh.elsewhere.n
}

// count counts the archived and locked chats when the list changed.
func (sh *Shell) count(a *state.Account) {
	g := a.Model.Chats.Gen()
	if g == sh.counted && a.Serial == sh.countedAcct && sh.builds > 1 {
		return
	}
	sh.counted, sh.countedAcct, sh.archived, sh.archivedUnread, sh.locked = g, a.Serial, 0, 0, 0
	for _, ref := range a.Model.Chats.Order() {
		ch := a.Model.Chats.At(ref)
		if ch.Locked() {
			sh.locked++
		} else if ch.Archived() {
			sh.archived++
			if ch.Unread() > 0 {
				sh.archivedUnread++ // the Archived badge (app.rs archived_unread)
			}
		}
	}
}

// dest builds one destination: its icon on a plate that is lit while the
// window shows it, with tip as its tooltip ("": the name); Archived carries
// how many archived chats are unread.
func (sh *Shell) dest(d destination, ic icon.ID, name, tip string, lit bool) (focused bool) {
	s, e, k := sh.S, &sh.env, sh.env.K
	c, r := k.Context(), k.Theme()
	c.NextSize(ui.Pct(1), ui.Px(e.Pt(cellPt)))
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.Push(c.Box(0, name+"###cell"))
	plate := e.Pt(platePt)
	c.NextSize(ui.Px(plate), ui.Px(plate))
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextRadius(plate / 2) // round, as Rex's icon buttons
	flags := ui.Flags(0)
	if lit {
		c.NextBG(r.Colors[theme.Fill2])
		flags = ui.DrawBackground
	}
	c.Push(c.Box(flags, "plate"))
	var st ui.SemState
	if lit {
		st = ui.SemSelected
	}
	c.NextSemState(st)
	k.NextSize(ui.Px(plate), ui.Px(plate)) // the whole plate answers, not its glyph
	sig := rexui.IconButton(k.Kit, ic, name, rexui.IconButtonOpts{Variant: rexui.IconBorderless, Size: theme.SizeLarge})
	if tip == "" {
		tip = name
	}
	rexui.Tooltip(k.Kit, sig.ID, tip)
	if n := sh.archivedUnread; d == destArchived && n > 0 {
		theme.At(c, plate-e.Pt(14), -e.Pt(2), e.Pt(20), e.Pt(16))
		c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask())
		c.Push(c.Box(base.Floating, "badge"))
		rexui.BadgeCountN(k.Kit, n, rexui.BadgeOpts{Color: rexui.Of(theme.Accent)})
		c.Pop()
	}
	clicked := sig.Clicked
	focused = c.Focused(sig.ID) && c.FocusVisible()
	e.TabStop(railStops[d], sig.ID)
	c.Pop()
	c.Pop()
	if !clicked {
		return focused
	}
	a := s.Acct()
	switch d {
	case destChats:
		s.Page = state.PageChats
		a.SetMode(state.ListChats)
		s.Panes.ListHidden = false // Chats shows the chats, wherever the list went
	case destCalls:
		s.Page = state.PageCalls
	case destStarred:
		s.Page = state.PageChats
		a.SetMode(state.ListStarred)
	case destArchived:
		s.Page = state.PageChats
		a.SetMode(state.ListArchived)
	case destLocked:
		if a.Mode == state.ListLocked {
			s.Page = state.PageChats
		} else {
			s.OpenDialog(state.Dialog{Kind: state.DialogUnlockLockedChats})
		}
	case destSettings:
		s.OpenWindow(state.WinSettings)
	}
	s.Touch()
	return focused
}

// accounts shows the account switcher at anchor.
func (sh *Shell) accounts(anchor screen.Rect) {
	if sh.Set.Accounts != nil && sh.S.AccountMenu {
		sh.Set.Accounts.Build(&sh.env, anchor)
	}
}

// toasts shows the waiting toasts stacked at the bottom right, above the
// composer, oldest on top (zf:src/ui/mod.rs:618-744): an info toast for
// 3.2 s (INFO_TOAST_LIFETIME), an error until dismissed, with "Copy this
// message". Workers may post while it runs (State.ToastsInto copies).
func (sh *Shell) toasts(W, H float32) {
	s, e, k := sh.S, &sh.env, sh.env.K
	sh.toastBuf = s.ToastsInto(sh.toastBuf[:0])
	if len(sh.toastBuf) == 0 {
		return
	}
	c := k.Context()
	m := e.Pt(rexui.ToastLook.Margin)
	w := min(e.Pt(rexui.ToastLook.MaxW), W-2*m)
	bottom := H - e.Pt(20)
	if sh.composerH > 0 {
		bottom = H - sh.composerH - e.Pt(12)
	}
	x := W - e.Pt(20) - w - m
	// Newest at the bottom: laid out from the last, each on the height it
	// had last frame.
	y := bottom
	for i := len(sh.toastBuf) - 1; i >= 0; i-- {
		t := &sh.toastBuf[i]
		h := sh.toastHeight(t.ID)
		hb := h + 2*m
		theme.At(c, x, y+m-hb, w+2*m, hb)
		c.Push(c.BoxKeyed(base.Floating, ui.KeyN(ui.HashString(0, "zapfast/toast"), t.ID), nil))
		o := rexui.ToastOpts{BannerOpts: rexui.BannerOpts{Level: rexui.BannerInfo, Dismissible: true}, Duration: 3.2}
		head := t.Text
		if t.Error {
			o.Level, o.Duration = rexui.BannerError, -1
			o.ActionIcon, o.ActionIconName = sh.ic.copy, i18n.T("Copy this message")
			// Upstream wraps a toast's whole text; the banner's title is one
			// line that fades, so an error's reason ("Could not open the
			// attachment: no such file") goes on the wrapped line below.
			if i := bytes.Index(head, colonSpace); i > 0 && i+len(colonSpace) < len(head) {
				head, o.Message = head[:i], model.S(head[i+len(colonSpace):])
			}
		}
		title := c.Fmt().Str(model.S(head)).Str("###toast").Uint64(t.ID).String()
		sig := rexui.Toast(k.Kit, title, o)
		sh.noteToast(t.ID, c.KeyOf(sig.ID))
		c.Pop()
		if sig.Action && c.ClipboardSet != nil {
			c.ClipboardSet(t.Text)
		}
		if sig.Dismissed {
			s.DismissToast(t.ID)
		}
		y -= h + e.Pt(8)
	}
}

// ageToasts is the lock screen's part in the toasts: upstream keeps them
// behind the lock and drops an info toast once its lifetime passed
// (zf:src/app.rs, INFO_TOAST_LIFETIME), so an error waits for the reader
// after the unlock and an old info toast does not come back.
func (sh *Shell) ageToasts() {
	s := sh.S
	sh.toastBuf = s.ToastsInto(sh.toastBuf[:0])
	now := s.Now()
	for i := range sh.toastBuf {
		if t := &sh.toastBuf[i]; !t.Error && now.Sub(t.At) >= infoToastLife {
			s.DismissToast(t.ID)
		}
	}
}

// infoToastLife is how long an info toast shows (upstream's
// INFO_TOAST_LIFETIME, the banner's Duration).
const infoToastLife = 3200 * time.Millisecond

var colonSpace = []byte(": ")

type myAvatar struct {
	acct        *state.Account
	serial      uint64
	me          uint32 // Me.Gen when asked
	asked, read bool
	gen         uint32
	path        []byte
	key         img.Key
}

type toastKey struct {
	id  uint64
	key ui.Key
}

// toastHeight is the height toast id had last frame (0 the first).
func (sh *Shell) toastHeight(id uint64) float32 {
	for _, t := range sh.toastKeys {
		if t.id == id {
			if r := sh.env.K.Context().RectOf(t.key); r[3] > r[1] {
				return r[3] - r[1]
			}
		}
	}
	return 0
}

// noteToast keeps toast id's box key, to read its height next frame: the
// slot of a toast no longer shown is reused.
func (sh *Shell) noteToast(id uint64, key ui.Key) {
	free := -1
	for i := range sh.toastKeys {
		t := &sh.toastKeys[i]
		if t.id == id {
			t.key = key
			return
		}
		if free < 0 && !sh.showing(t.id) {
			free = i
		}
	}
	if free >= 0 {
		sh.toastKeys[free] = toastKey{id, key}
	}
}

func (sh *Shell) showing(id uint64) bool {
	for i := range sh.toastBuf {
		if sh.toastBuf[i].ID == id {
			return true
		}
	}
	return false
}

// bannerAction is the banner button's label for the action it stands for
// ("Retry", "Update"), which the caller still switches on in English.
func bannerAction(action string) string {
	switch action {
	case "Retry":
		return i18n.T("Retry")
	case "Update":
		return i18n.T("Update")
	}
	return action
}

// fillInt appends tmpl to f with key replaced by n, without a string for n.
func fillInt(f *ui.Fmt, tmpl, key string, n int) *ui.Fmt {
	for len(tmpl) > 0 {
		i := strings.Index(tmpl, key)
		if i < 0 {
			return f.Str(tmpl)
		}
		f = f.Str(tmpl[:i]).Int(n)
		tmpl = tmpl[i+len(key):]
	}
	return f
}

// fillStr appends tmpl to f with key replaced by val (a translated pattern).
func fillStr(f *ui.Fmt, tmpl, key, val string) *ui.Fmt {
	for len(tmpl) > 0 {
		i := strings.Index(tmpl, key)
		if i < 0 {
			return f.Str(tmpl)
		}
		f = f.Str(tmpl[:i]).Str(val)
		tmpl = tmpl[i+len(key):]
	}
	return f
}

// bannerGrace is how long connecting or loading the history goes without
// the banner: a start that connects quickly never shows it, so the chat does
// not move down and back up as the window opens.
const bannerGrace = 1_500_000_000

// bannerHeld reports whether the banner is held back this frame: the account
// is connecting, or loading its chats or history, and has been for less than
// bannerGrace. A warning, an error or an update shows at once.
func (sh *Shell) bannerHeld(a *state.Account, update bool) bool {
	c := sh.env.K.Context()
	syncing, _, _ := a.Model.Syncing()
	passing := false
	switch a.Model.Link.Status() {
	case abi.LinkStatusTagStarting, abi.LinkStatusTagConnecting:
		passing = true
	case abi.LinkStatusTagConnected:
		passing = (syncing || !a.Model.Chats.Loaded()) && !update
	}
	now := c.Input().Time
	if !passing || now == 0 {
		sh.bannerFrom = 0
		return false
	}
	if sh.bannerFrom == 0 || sh.bannerAcct != a.Serial {
		sh.bannerFrom, sh.bannerAcct = now, a.Serial
		if sh.bannerOn {
			sh.bannerFrom -= bannerGrace // a banner on screen (Offline) gives way to this one at once
		}
	}
	if left := sh.bannerFrom + bannerGrace - now; left > 0 {
		c.WakeIn(left)
		return true
	}
	return false
}

// banner is the line under the open chat's header, or over the empty pane,
// at r (its height is the banner's): history loading, a newer version, the
// connection. Upstream draws it there when the window's buttons share the
// headers' edge, as on macOS (zf:src/ui/mod.rs:88-91, :507-611,
// zf:src/ui/conversation.rs:62-108); its words are English there too. It
// returns the height it took (last frame's, laid out).
func (sh *Shell) banner(a *state.Account, r screen.Rect) float32 {
	s, e, k := sh.S, &sh.env, sh.env.K
	c := k.Context()
	l := &a.Model.Link
	f := c.Fmt()
	level, action := rexui.BannerInfo, ""
	syncing, pct, hasPct := a.Model.Syncing()
	version, update := "", false
	if u := sh.updates; u != nil {
		version, update = u.Available()
	}
	switch l.Status() {
	case abi.LinkStatusTagConnected:
		switch {
		case syncing && hasPct:
			f = fillInt(f, i18n.T("Loading chat history… {percent}%"), "{percent}", pct)
		case syncing || !a.Model.Chats.Loaded():
			f = f.Str(i18n.T("Loading chat history…"))
		case update:
			f, action = fillStr(f, i18n.T("Zap {version} is available"), "{version}", version), "Update"
		default:
			sh.bannerOn = false
			return 0
		}
	case abi.LinkStatusTagStarting, abi.LinkStatusTagConnecting:
		f = f.Str(i18n.T("Connecting to WhatsApp…"))
	case abi.LinkStatusTagDisconnected:
		f = fillStr(f, i18n.T("Offline ({reason}). Reconnecting…"), "{reason}", model.S(l.Reason()))
		level, action = rexui.BannerWarning, "Retry"
	case abi.LinkStatusTagFailed:
		f = f.Str(model.S(l.Error()))
		level, action = rexui.BannerError, "Retry"
	default: // Unlinked, LoggedOut
		f = f.Str(i18n.T("Not linked to a phone"))
		level = rexui.BannerWarning
	}
	if sh.bannerHeld(a, update) {
		sh.bannerOn = false
		return 0
	}
	sh.bannerOn = true
	title := f.Str("###banner").String()
	h := e.Pt(44)
	if rr := c.RectOf(sh.bannerKey); rr[3] > rr[1] {
		h = rr[3] - rr[1]
	}
	// as tall as the banner (h, last frame's, places what is under it); the
	// context's padding, as a raw box takes no kit padding, which would wait
	// for the next kit container (the composer's row)
	c.NextPos(r.X, r.Y)
	c.NextSize(ui.Px(r.W), ui.Children(1))
	c.NextPadding(e.Pt(8), e.Pt(8), e.Pt(8), e.Pt(8))
	id := c.Box(base.Floating, "banner")
	sh.bannerKey = c.KeyOf(id)
	c.Push(id)
	if rexui.Banner(k.Kit, title, rexui.BannerOpts{Level: level, Action: bannerAction(action)}).Action {
		switch action {
		case "Retry":
			a.Send(a.Cmd(abi.CommandTagReconnect))
		case "Update":
			s.OpenWindow(state.WinSettings)
		}
	}
	c.Pop()
	return h
}

// windows opens the windows the screens asked for, from the main window's
// frame, brings forward the ones open already, and closes the ones asked
// to close. A window that closed is retired by its app.EvWindowClosed
// (WindowClosed); one asked for during a frame opens at the next safe
// point, and one that failed to then is retired here. An offscreen main
// window (tests, -o) opens no real windows: the ask stays in Win.Want.
func (sh *Shell) windows() {
	var op opener
	if sh.a.Win != nil {
		op = sh.a
	}
	sh.manage(op, false)
}

// WindowsAlone is windows with no main window open (the app's own
// events, App): only the windows asked for by State.OpenWindowAlone open,
// through the app's App; the others wait for the main window. It reports
// whether a window opened now is still to come forward (ask again).
func (sh *Shell) WindowsAlone(a *app.App) bool {
	if a == nil {
		return false
	}
	return sh.manage(a, true)
}

// opener opens a window (app.App; a fake in tests).
type opener interface {
	OpenWindow(cfg app.WindowConfig, b app.Builder) (*app.Win, error)
}

func (sh *Shell) manage(op opener, alone bool) (pending bool) {
	s := sh.S
	for w := state.Window(0); w < state.NumWindows; w++ {
		if h := sh.wins[w]; h != nil {
			switch {
			case h.Err() != nil:
				s.Toast([]byte(h.Err().Error()), true)
				sh.closed(w)
			case s.Win.Shut[w] && h.ID() != 0:
				h.Close()
				s.Win.Shut[w] = false
			case sh.front[w] && h.ID() != 0:
				// opened since it was asked for alone: forward now
				sh.front[w] = false
				h.Show()
				h.Focus()
			}
		}
		if sh.wins[w] == nil {
			s.Win.Shut[w] = false
			sh.front[w] = false
		}
		pending = pending || sh.front[w]
		if !s.Win.Want[w] || op == nil || (alone && !s.Win.Alone[w]) {
			continue
		}
		s.Win.Want[w], s.Win.Alone[w] = false, false
		if h := sh.wins[w]; h != nil {
			h.Show()
			h.Focus()
			continue
		}
		var scr screen.WindowScreen
		switch w {
		case state.WinSettings:
			scr = sh.Set.Settings
		case state.WinViewer:
			scr = sh.Set.Viewer
		case state.WinCall:
			scr = sh.Set.Call
		case state.WinPlayground:
			scr = sh.Set.Playground
		case state.WinPeek:
			scr = sh.Set.PeekWindow
		case state.WinPip:
			scr = sh.Set.PipWindow
		}
		if scr == nil {
			continue
		}
		b := screen.NewWindow(s, w, scr, sh.Set.Theme, sh)
		cfg := scr.Config(s)
		win, err := op.OpenWindow(cfg, b)
		if err != nil {
			s.Toast([]byte(err.Error()), true)
			continue
		}
		sh.wins[w], sh.wbs[w] = win, b
		s.Win.Open[w] = true
		if alone || cfg.Panel != nil {
			// It opens at the app's next safe point; then it comes forward.
			// A panel opens hidden however it opens (the floating peek).
			sh.front[w] = true
			pending = true
		}
	}
	return pending
}

// WindowClosed retires the window with id, which has closed whichever way
// (app.EvWindowClosed): a window of the screens' has its state cleared,
// its screen's Closed run and its image cache freed. It reports whether the
// window was one of them.
func (sh *Shell) WindowClosed(id uint32) bool {
	if id == 0 {
		return false
	}
	for w, h := range sh.wins {
		if h != nil && h.ID() == id {
			sh.closed(state.Window(w))
			return true
		}
	}
	return false
}

// mainClosed is the main window gone (⌘W, the close button, quitting): it
// no longer has the focus, and its image cache goes (Init makes another).
func (sh *Shell) mainClosed() {
	sh.placed = false // put back where it was when it opens again
	// Closing the window hides the locked chats again (LK-006;
	// zf:src/app.rs window_gone): the folder, its prompt and its chat.
	if sh.S.Dialog.Kind == state.DialogUnlockLockedChats {
		sh.S.CloseDialog()
	}
	for _, a := range sh.S.Accounts {
		a.CloseLockedFolder()
	}
	sh.S.Focused = false
	sh.S.Touch()
	if sh.imgs != nil {
		sh.imgs.Close(0)
		sh.imgs = nil
	}
	sh.mainHandle.Store(nil) // App.Wake posts from here on
}

// closed ends window w, which has closed.
func (sh *Shell) closed(w state.Window) {
	s := sh.S
	if b := sh.wbs[w]; b != nil {
		b.Closed()
	}
	sh.wins[w], sh.wbs[w] = nil, nil
	s.Win.Open[w], s.Win.Want[w], s.Win.Shut[w] = false, false, false
	if w == state.WinViewer {
		s.Win.ViewerHasCard, s.Win.ViewerPlay, s.Win.ViewerAt = false, false, 0
	}
	s.Touch()
}

// WakeWindows gives every open secondary window a frame (the model
// changed under them).
func (sh *Shell) WakeWindows() {
	for _, w := range sh.wins {
		if w != nil {
			w.Wake()
		}
	}
}

// raiseMain brings the main window forward.
func (sh *Shell) raiseMain() {
	if sh.raise != nil {
		sh.raise()
		return
	}
	if sh.a != nil {
		if w := sh.a.Window(); w != nil {
			w.Show()
			w.Focus()
		}
	}
}

// escape is Escape's last resort, when no screen took it
// (zf:src/ui/keys.rs): close the account switcher, leave Calls, Starred,
// Archived or Locked for Chats, or close the open chat.
func (sh *Shell) escape(a *state.Account) {
	s, c := sh.S, sh.env.K.Context()
	if a == nil || s.Lock.Locked || c.ModalOpen() || c.TextFieldFocused() || !c.TakeKey(ui.KeyEscape, 0) {
		return
	}
	switch {
	case s.AccountMenu:
		s.AccountMenu = false
	case s.Panes.Pane != state.PaneNone:
		s.Panes.Pane = state.PaneNone
	case s.Page != state.PageChats || a.Mode != state.ListChats:
		s.Page = state.PageChats
		a.SetMode(state.ListChats)
	case a.Open != 0:
		a.OpenChat(0)
	default:
		return
	}
	s.Touch()
}
