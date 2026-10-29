package viewer

import (
	"bytes"
	"sync/atomic"
	"time"

	"gonativeui/app"
	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// Screen is the media viewer window: one picture or clip of the open chat's
// album at a time, the album along the bottom, the actions of its message in
// the toolbar (zf:src/ui/image_preview.rs).
//
// The window shows what the main window asked for with
// state.State.Win.ViewerChat, ViewerMsg (the message id) and, for a carousel,
// ViewerCard/ViewerHasCard. Stepping through the album writes the item on
// screen back to them, so asking for the one already shown changes nothing
// and any other moves the viewer there.
type Screen struct {
	// Traffic draws the traffic lights at the toolbar's place (offscreen
	// renders; in a window the system draws them).
	Traffic bool

	e     *screen.Env
	win   atomic.Pointer[app.Win] // the window's handle while it is open, for wakes from workers
	cache *img.Cache              // the window's own (screen.NewImages), nil without a renderer
	ic    icons

	registered bool // the listener is added once, whatever the windows
	live       bool // a window is open on the screen
	closing    bool // the window was asked to close
	acct       int  // the account the window belongs to
	dirty      bool // the album or the item changed: resolve again

	chat   model.ChatRef
	chatID []byte
	cur    current

	album    []item
	batch    uint32
	hasBatch bool
	resetGen uint32
	th       thumbs
	// thScratch is the thumbnails taken from th this frame.
	thScratch []thumbDone

	stk       sticker
	pb        playback
	endedSeen int           // the clip\'s end events taken (tests)
	canPan    bool          // the picture is larger than the stage: a drag moves it (last frame)
	wantPlay  bool          // the clip plays as soon as it is here (Win.ViewerPlay)
	wantAt    time.Duration // from where (Win.ViewerAt)
	scrubTo   float64       // where the seek bar's drag is, 0 to 1
	r         resolved
	v         view
	pic       picture
	probe     *probeResult
	cmd       zoomCmd
	drag      dragState
	dl        map[string]dlState // downloads by message id
	motion    motionState
	msg       abi.Message // scratch, to read the message's motion photo
	convG     uint32      // the conversation's generation r was resolved at

	hover        bool // the pointer is over the stage: the album's arrows show
	copyPending  bool
	clipPNG      clipboardPNG
	stripFor     current // the item the strip last centred on
	stripCentred bool
	mediaRect    [4]float32 // the picture, or the clip's poster, on screen
	prevRect     [4]float32 // the same, a frame ago
}

var _ screen.WindowScreen = (*Screen)(nil)

// New returns the window's screen.
func New() *Screen { return &Screen{} }

// current is the item on screen: a message of the chat, and the card of an
// interactive message's picture.
type current struct {
	id      []byte
	card    uint64
	hasCard bool
}

// resolved is what the item on screen is, from the album or, before the
// album holds it, from the message itself.
type resolved struct {
	have  bool
	pos   int // its place in the album, -1 if the album does not hold it
	path  string
	video bool
	thumb int // the album item whose thumbnail is its poster, -1 for none
}

type dlState struct {
	downloading bool
	err         string
}

// Config is the window's configuration.
func (s *Screen) Config(st *state.State) app.WindowConfig {
	cfg := app.WindowConfig{Title: i18n.T("Zap"), Width: 960, Height: 700, MinWidth: 480, MinHeight: 360, Materials: true}
	cfg.Titlebar, cfg.TrafficLights = rexui.TitlebarFor(theme.ToolbarRex)
	return cfg
}

type icons struct {
	prev, next, close, plus, minus, reply, react, forward, save, download icon.ID
	chat, open, copy, fullscreen, exitFull, play, photo, failed, video    icon.ID
	bigPlay, bigVideo, bigPhoto, star, starOff                            icon.ID
	playSm, pauseSm, vol, volOff                                          icon.ID
}

// Init readies the screen for its (new) window.
func (s *Screen) Init(e *screen.Env) error {
	s.e = e
	s.win.Store(e.A.Window())
	s.cache = e.K.Images()
	pt := theme.MacWindow.Capsule.Glyph + 2
	ic := &s.ic
	// Preserve registration order, including the differently sized glyphs.
	for _, it := range [...]struct {
		dst *icon.ID
		sym string
		fb  rune
		pt  float32
	}{
		{&ic.prev, "chevron.left", '‹', pt + 4}, {&ic.next, "chevron.right", '›', pt + 4},
		{&ic.close, "xmark", '✕', pt}, {&ic.plus, "plus", '+', pt}, {&ic.minus, "minus", '−', pt},
		{&ic.reply, "arrowshape.turn.up.left", '↩', pt}, {&ic.react, "face.smiling", '☺', pt},
		{&ic.forward, "arrowshape.turn.up.right", '↪', pt}, {&ic.save, "square.and.arrow.down", '⤓', pt},
		{&ic.download, "arrow.down.circle", '⤓', pt}, {&ic.chat, "bubble.left", '✉', pt},
		{&ic.open, "arrow.up.forward.square", '↗', pt}, {&ic.copy, "doc.on.doc", '⎘', pt},
		{&ic.fullscreen, "arrow.up.left.and.arrow.down.right", '⤢', pt},
		{&ic.exitFull, "arrow.down.right.and.arrow.up.left", '⤡', pt},
		{&ic.play, "play.circle", '▶', pt}, {&ic.photo, "photo", '▣', pt}, {&ic.failed, "exclamationmark.circle", '!', pt},
		{&ic.video, "video", '▶', pt},
		{&ic.bigPlay, "play.fill", '▶', 28}, {&ic.bigVideo, "video", '▶', 48}, {&ic.bigPhoto, "photo", '▣', 48},
		{&ic.playSm, "play.fill", '▶', 16}, {&ic.pauseSm, "pause.fill", '⏸', 16},
		{&ic.vol, "speaker.wave.2.fill", '♪', 16}, {&ic.volOff, "speaker.slash.fill", '✕', 16},
		{&ic.star, "star", '☆', pt}, {&ic.starOff, "star.slash", '★', pt},
	} {
		*it.dst = e.K.AddIcon(icon.Spec{Symbol: it.sym, Fallback: it.fb, Pt: it.pt})
	}
	if !s.registered {
		s.registered = true
		e.S.Listen(s, listenTags...)
		e.S.OnAppEvent(s, s.onAppEvent)
	}
	s.live, s.closing = true, false
	s.chat, s.chatID = 0, s.chatID[:0]
	s.restartAlbum()
	s.cur = current{id: s.cur.id[:0]}
	s.v, s.cmd, s.drag = newView(), zoomNone, dragState{}
	s.dl = map[string]dlState{}
	s.copyPending, s.stripCentred, s.hover = false, false, false
	s.pic, s.r, s.motion, s.convG = picture{}, resolved{pos: -1, thumb: -1}, motionState{}, 0
	s.clipPNG.reset()
	s.resetSticker()
	s.stopClip()
	s.adopt(e)
	return nil
}

// adopt takes what the main window asked to show (State.Win.Viewer*).
func (s *Screen) adopt(e *screen.Env) {
	st := e.S
	vm := &st.Win.ViewerMsg
	a := st.Acct()
	if a == nil || vm.Len() == 0 {
		s.close(e)
		return
	}
	ch, ok := a.Model.Chats.Get(st.Win.ViewerChat)
	if !ok {
		s.close(e)
		return
	}
	newChat := s.chat != st.Win.ViewerChat || s.acct != st.Active || !bytes.Equal(s.chatID, ch.ID())
	s.acct, s.chat = st.Active, st.Win.ViewerChat
	if newChat {
		s.chatID = append(s.chatID[:0], ch.ID()...)
		s.restartAlbum()
		clear(s.dl)
	}
	s.cur.id = append(s.cur.id[:0], vm.At(0)...)
	s.cur.card, s.cur.hasCard = 0, false
	if st.Win.ViewerHasCard {
		s.cur.card, s.cur.hasCard = uint64(max(st.Win.ViewerCard, 0)), true
	}
	s.show()
	s.askAlbum(a)
}

// show resets what belongs to the item on screen.
func (s *Screen) show() {
	s.stopClip()
	s.wantPlay, s.wantAt = false, 0
	s.stk.checked = false
	s.v.reset()
	s.pic = picture{}
	s.cmd = zoomNone
	s.motion = motionState{}
	s.dirty = true
	s.convG = 0
}

// requested reports whether the main window asks for another item than the
// one on screen.
func (s *Screen) requested(st *state.State) bool {
	vm := &st.Win.ViewerMsg
	if vm.Len() == 0 || st.Win.ViewerChat != s.chat || st.Active != s.acct {
		return vm.Len() != 0
	}
	if !bytes.Equal(vm.At(0), s.cur.id) {
		return true
	}
	if s.cur.hasCard != st.Win.ViewerHasCard {
		return true
	}
	return s.cur.hasCard && uint64(max(st.Win.ViewerCard, 0)) != s.cur.card
}

// setCur moves to another item of the album (or elsewhere in the chat) and
// tells the state, so the same request is not a request.
func (s *Screen) setCur(id []byte, card uint64, hasCard bool) {
	s.cur.id = append(s.cur.id[:0], id...)
	s.cur.card, s.cur.hasCard = card, hasCard
	w := &s.e.S.Win
	w.ViewerMsg.Reset()
	w.ViewerMsg.Add(s.cur.id)
	w.ViewerCard, w.ViewerHasCard = int(card), hasCard
	s.show()
}

// close closes the window; the shell calls Closed once it is gone.
func (s *Screen) close(e *screen.Env) {
	if s.closing {
		return
	}
	s.closing = true
	e.S.TakeViewerPlay()
	s.stopClip()
	if w := e.A.Window(); w != nil {
		w.Close()
	}
}

// ImageOptions gives the window's image cache the budgets of full-size
// pictures (screen.ImageTuner).
func (s *Screen) ImageOptions(o *img.Options) {
	o.GPUBytes, o.CPUBytes, o.Entries, o.Workers = 192<<20, 64<<20, 2048, 3
}

// Closed frees what the window held once it is gone (screen.WindowCloser):
// the album with its thumbnails and downloads, and the cache handles, which
// go with the window's image cache.
func (s *Screen) Closed(e *screen.Env) {
	s.stopClip()
	s.live, s.closing = false, true
	s.cache = nil
	s.restartAlbum() // not live: late thumbnails are refused
	s.album, s.thScratch = nil, nil
	s.dl = nil
	s.pic, s.r, s.motion = picture{}, resolved{pos: -1, thumb: -1}, motionState{}
	s.clipPNG.reset()
	s.resetSticker()
	s.msg.Release()
	s.win.Store(nil) // the Env keeps the state only (screen.Window.Closed)
}

// Closing reports whether the window was asked to close (tests).
func (s *Screen) Closing() bool { return s.closing }

func (s *Screen) wake() {
	if w := s.win.Load(); w != nil {
		w.Wake()
		return
	}
	if s.e != nil {
		s.e.S.Wake()
	}
}

// account is the account the window belongs to, nil if it is gone.
func (s *Screen) account() *state.Account {
	st := s.e.S
	if s.acct < 0 || s.acct >= len(st.Accounts) {
		return nil
	}
	return st.Accounts[s.acct]
}

// Build builds the whole window.
func (s *Screen) Build(e *screen.Env) {
	c, k := e.C(), e.K
	w, h := c.Size()
	if s.closing {
		return
	}
	if s.cache == nil || w <= 0 || h <= 0 {
		return
	}
	st := e.S
	a := s.account()
	if a == nil || st.Active != s.acct {
		s.close(e) // the viewer belongs to the account that was on screen
		return
	}
	// The viewer closes with its chat: removed, or locked outside the open
	// folder (app.rs:4827).
	ch, ok := a.Model.Chats.Get(s.chat)
	if !ok || (ch.Locked() && a.Mode != state.ListLocked) {
		s.close(e)
		return
	}
	if s.requested(st) {
		s.adopt(e)
		if s.closing {
			return
		}
	}
	if st.Win.ViewerPlay {
		// A double click on the clip in its bubble: it plays at once. Read
		// after the item is adopted, which clears what the last one wanted.
		s.wantPlay, s.wantAt = st.TakeViewerPlay(), st.Win.ViewerAt
		st.Win.ViewerAt = 0 // TakeViewerPlay leaves it
	}
	s.checkSticker(a)
	if s.stk.on {
		s.stickerWindow(e, ch)
		return
	}
	s.drainThumbs()
	s.resolve(a)
	s.autoPlay(e)

	W, H := float32(w), float32(h)
	r := k.Theme()
	tbH := theme.Snap(r.Window.Toolbars[theme.ToolbarRex].Height)
	stripH := float32(0)
	if len(s.album) > 1 {
		stripH = e.Pt(stripPt)
	}
	stage := screen.Rect{X: 0, Y: tbH, W: W, H: H - tbH - stripH}
	strip := screen.Rect{X: 0, Y: H - stripH, W: W, H: stripH}

	c.NextBG(r.Colors[theme.Field])
	theme.At(c, 0, 0, W, H)
	c.Push(c.Box(base.Floating|ui.DrawBackground, "viewer"))
	s.keys(e)
	if s.closing {
		c.Pop()
		return
	}
	s.stage(e, stage)
	if stripH > 0 {
		s.strip(e, strip)
	}
	s.arrows(e, stage)
	s.toolbar(e, ch)
	s.finishClipboard(e)
	c.Pop()
}

// resolve works out what the item on screen is, when the album, the message
// or the item changed.
func (s *Screen) resolve(a *state.Account) {
	var conv *model.Conversation
	if v := a.Model.Conversation(s.chatID); v != nil {
		conv = v
		if v.Gen() != s.convG {
			s.convG = v.Gen()
			s.dirty = true
		}
	}
	if !s.dirty {
		return
	}
	s.dirty = false
	rs := resolved{pos: s.position(), thumb: -1}
	if rs.pos >= 0 {
		it := &s.album[rs.pos]
		rs.have, rs.path, rs.video = true, it.path, it.video
		if it.thumb != nil {
			rs.thumb = rs.pos
		}
	}
	if conv != nil && !s.cur.hasCard {
		// The message holds what the album has not yet: a clip opened from
		// its own bubble before the album arrives, a file just downloaded.
		if i := conv.Find(s.cur.id); i >= 0 {
			m := conv.At(i)
			rs.have = true
			rs.video = rs.video || m.Kind() == abi.ContentTagVideo
			if md, ok := m.Media(); ok && rs.path == "" && len(md.Path) > 0 {
				rs.path = string(md.Path)
			}
			s.motion.read(conv, i, &s.msg)
		}
	}
	if rs.path != "" && rs.path != s.r.path {
		s.pic = picture{} // another file
	}
	if rs.path == "" && s.r.path != "" {
		s.pic = picture{}
	}
	s.r = rs
}

// isVideo reports whether the item on screen is a clip, from the album item,
// else the message, else the file's name.
func (s *Screen) isVideo() bool {
	return s.r.video || (s.r.pos < 0 && s.r.path != "" && isVideoName(s.r.path))
}

// dlOf is the download state of the item on screen.
func (s *Screen) dlOf() dlState { return s.dl[string(s.cur.id)] }

// onMedia takes the result of a download of the whole attachment.
func (s *Screen) onMedia(msg []byte, res *abi.PathResult) {
	key := string(msg)
	switch res.Tag {
	case abi.PathResultTagOk:
		delete(s.dl, key)
		path := string(res.Ok.Path)
		for i := range s.album {
			if it := &s.album[i]; !it.hasCard && bytes.Equal(it.id, msg) {
				it.path = path
			}
		}
		if !s.cur.hasCard && bytes.Equal(msg, s.cur.id) {
			// The viewer was waiting for this file: show it.
			s.pic = picture{}
		}
	default:
		s.dl[key] = dlState{err: string(res.Err.Error)}
	}
	s.dirty = true
	s.wake()
}

// ---- keys ----

// keys takes the keys of the viewer: Escape closes, ← and → step through the
// album, + − 0 zoom, ⌘C copies a photo. Typing never reaches the chat: this
// is a window of its own.
func (s *Screen) keys(e *screen.Env) {
	c := e.C()
	if c.ModalOpen() || c.TextFieldFocused() {
		return
	}
	switch {
	case c.TakeKey(ui.KeyEscape, 0):
		s.close(e)
		return
	case c.TakeKey(ui.KeyLeft, 0):
		if !s.jump(-5 * time.Second) {
			s.step(-1)
		}
	case c.TakeKey(ui.KeyRight, 0):
		if !s.jump(5 * time.Second) {
			s.step(1)
		}
	case c.TakeKey('=', 0), c.TakeKey('=', ui.ModShift), c.TakeKey('+', 0), c.TakeKey('+', ui.ModShift):
		s.cmd = zoomIn
	case c.TakeKey('-', 0):
		s.cmd = zoomOut
	case c.TakeKey('0', 0):
		s.cmd = zoomFit
	}
	// The window's View menu (⌘+ ⌘- ⌘0).
	switch {
	case e.K.Command(screen.CmdPictureZoomIn):
		s.cmd = zoomIn
	case e.K.Command(screen.CmdPictureZoomOut):
		s.cmd = zoomOut
	case e.K.Command(screen.CmdPictureActualSize):
		s.cmd = zoomActual
	}
	if s.wantsCopy(c) {
		s.copyImage(e)
	}
	s.videoKeys(e)
}

// wantsCopy reports ⌘C, as a key or as the Edit menu's Copy.
func (s *Screen) wantsCopy(c *ui.Context) bool {
	copied := c.TakeKey('C', ui.PrimaryMod())
	in := c.Input()
	for i := 0; i < in.NEdit; i++ {
		copied = copied || in.Edit[i] == ui.EditCopy
	}
	return copied
}

// step moves to the next or previous item of the album; it stops at either
// end (ViewerStep).
func (s *Screen) step(d int) {
	pos := s.position()
	if pos < 0 {
		return
	}
	n := pos + d
	if n < 0 || n >= len(s.album) {
		return
	}
	it := &s.album[n]
	s.setCur(it.id, it.card, it.hasCard)
	s.e.C().RequestFrame()
}

// ---- album along the bottom ----

const (
	stripPt = 78
	tilePt  = 56
	gapPt   = 8
)

func (s *Screen) strip(e *screen.Env, r screen.Rect) {
	c, k := e.C(), e.K
	th := k.Theme()
	c.NextBG(th.Colors[theme.TextBackground])
	theme.At(c, r.X, r.Y, r.W, r.H)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextRole(ui.RoleList)
	c.NextSemName(i18n.T("Photos and videos"))
	c.Push(c.Box(base.Floating|ui.DrawBackground|ui.Clip, "album"))
	tile, gap := e.Pt(tilePt), e.Pt(gapPt)
	n := len(s.album)
	stride := tile + gap
	row := float32(n)*tile + float32(n-1)*gap
	pad := max((r.W-row)/2, e.Pt(4))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(gap)
	c.NextPadding(pad, 0, pad, 0)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	scroller := c.Box(ui.ScrollX, "tiles")
	c.Push(scroller)
	// A long album builds the tiles in view and a little beyond, with a
	// spacer for the rest.
	first, last := 0, n-1
	if n > 40 {
		sx, _ := c.Scroll(scroller)
		first = max(int((float32(sx)-pad)/stride)-4, 0)
		last = min(first+int(r.W/stride)+9, n-1)
		if first > 0 {
			c.NextSize(ui.Px(float32(first)*stride-gap), ui.Px(tile))
			c.Box(0, "before")
		}
	}
	pos := s.position()
	for i := first; i <= last; i++ {
		s.tile(e, &s.album[i], i, i == pos, tile)
	}
	if last < n-1 {
		c.NextSize(ui.Px(float32(n-1-last)*stride-gap), ui.Px(tile))
		c.Box(0, "after")
	}
	c.Pop()
	if moved := !s.stripCentred || !s.stripFor.is(s.cur); moved && pos >= 0 {
		// Keep the picture being looked at in the middle of the strip.
		x := float64(float32(pos)*stride + tile/2 + pad - r.W/2)
		c.SetScroll(scroller, max(x, 0), 0)
		s.stripFor.set(s.cur)
		s.stripCentred = true
	}
	c.Pop()
}

func (c *current) is(o current) bool {
	return c.hasCard == o.hasCard && c.card == o.card && bytes.Equal(c.id, o.id)
}

func (c *current) set(o current) {
	c.id = append(c.id[:0], o.id...)
	c.card, c.hasCard = o.card, o.hasCard
}

// tile is one thumbnail of the strip; the current one is ringed.
func (s *Screen) tile(e *screen.Env, it *item, i int, cur bool, side float32) {
	c, k := e.C(), e.K
	th := k.Theme()
	kind := i18n.T("Photo")
	if it.video {
		kind = i18n.T("Video")
	}
	name := c.Fmt().Str(kind).Byte(' ').Int(i + 1).String()
	p := k.PressState(name, base.HoverOn)
	flags := ui.DrawBackground | ui.Clip
	c.NextBG(th.Colors[theme.Fill2])
	c.NextRadius(e.Pt(6))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	if cur {
		c.NextSemState(ui.SemSelected)
	}
	k.BeginPress(&p, ui.RoleButton, ui.Px(side), ui.Px(side), flags, nil)
	if it.thumb != nil {
		h, _ := s.cache.Request(img.Key{Hash: it.thumbHash}, img.Bytes(it.thumb))
		c.NextPos(0, 0)
		k.NextSize(ui.Px(side), ui.Px(side))
		c.NextFlags(base.Floating)
		k.Image(1, h, ui.FitFill, 0, "")
	} else {
		k.Icon(s.tileIcon(it), th.Colors[theme.Label3])
	}
	if it.video && it.thumb != nil {
		k.Icon(s.ic.bigPlay, 0xFFFFFFFF)
	}
	if cur {
		// The ring is laid over the thumbnail: the current one is ringed.
		theme.At(c, 0, 0, side, side)
		c.NextRadius(e.Pt(6))
		c.NextBorder(th.Colors[theme.Accent], e.Pt(2))
		c.Box(base.Floating|ui.DrawBorder, "current")
	}
	if k.EndPress(&p).Clicked {
		s.setCur(it.id, it.card, it.hasCard)
		c.RequestFrame()
	}
}

func (s *Screen) tileIcon(it *item) icon.ID {
	if it.video {
		return s.ic.video
	}
	return s.ic.photo
}

// ---- arrows over the stage ----

// arrows shows Previous and Next at the stage's sides while the pointer is
// over it and the album holds other items.
func (s *Screen) arrows(e *screen.Env, stage screen.Rect) {
	c, k := e.C(), e.K
	in := c.Input()
	s.hover = in.Inside && in.MouseX >= stage.X && in.MouseX < stage.X+stage.W && in.MouseY >= stage.Y && in.MouseY < stage.Y+stage.H
	if len(s.album) < 2 || !s.hover {
		return
	}
	pos := s.position()
	d, inset := e.Pt(44), e.Pt(12)
	y := stage.Y + (stage.H-d)/2
	prev, next := i18n.T("Previous (Left arrow)"), i18n.T("Next (Right arrow)")
	if s.isVideo() {
		prev, next = i18n.T("Previous"), i18n.T("Next")
	}
	if s.arrow(e, stage.X+inset, y, d, s.ic.prev, prev, "###prev") && pos > 0 {
		s.step(-1)
	}
	if s.arrow(e, stage.X+stage.W-inset-d, y, d, s.ic.next, next, "###next") && pos >= 0 && pos < len(s.album)-1 {
		s.step(1)
	}
	_ = k
}

func (s *Screen) arrow(e *screen.Env, x, y, d float32, ic icon.ID, name, id string) bool {
	c, k := e.C(), e.K
	theme.At(c, x, y, d, d)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.Push(c.Box(base.Floating, id))
	hit := rexui.IconButton(k.Kit, ic, name, rexui.IconButtonOpts{Variant: rexui.IconBordered, Size: theme.SizeLarge}).Clicked
	c.Pop()
	return hit
}

// ---- helpers ----

// place begins a floating box at r (window pixels, in the box it is built
// in) whose children are centred.
func place(e *screen.Env, x, y, w, h float32, label string) {
	c := e.C()
	theme.At(c, x, y, w, h)
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.Push(c.Box(base.Floating, label))
}

// isLive reports whether a window of the viewer is open: events are the
// window's business only while it is.
func (s *Screen) isLive() bool {
	if !s.live || s.closing || s.e == nil {
		return false
	}
	if w := s.win.Load(); w != nil && !w.Open() {
		s.live = false
		return false
	}
	return true
}
