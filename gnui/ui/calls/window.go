package calls

import (
	"bytes"

	"gonativeui/app"
	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The call window (zf:src/ui/call.rs): the prompt of a call that rings, the
// screen of a live call with its controls, a group call's tiles, and how a
// call ended. Closing it, Hide the call screen or Escape put the call aside;
// only Hang up ends it.

// Layout, in points (zf:src/ui/call.rs:21-33, :659, :864-869).
const (
	padPt       = 24   // between the window's edges and the content
	controlPt   = 36   // a round control: Rex's extra-large capsule, as a circle
	controlGap  = 16   // between the controls
	answerGap   = 72   // between Decline and Accept
	pickerPt    = 200  // a device picker's width
	pickerRowPt = 52   // the pickers' row
	previewPart = 0.24 // how much of the window our own camera's preview may take
	maxTiles    = 12   // the most tiles a group call lays out
	tileGap     = 10
	tileAspect  = 4.0 / 3.0
	facesMax    = 5  // the faces a ringing group call shows before it counts the rest
	facePt      = 36 // a face's side
)

// Window is the call window.
type Window struct {
	h  *hub
	ic winIcons

	devicesOpen bool
	full        bool // full screen where the window cannot say (offscreen)

	pics     pics
	pick     [3]picker
	tiles    []tile
	rects    []screen.Rect
	faces    []string
	faceIDs  [][]byte
	faceImgs []img.Handle

	buf, buf2, buf3 []byte
}

var _ screen.WindowScreen = (*Window)(nil)

type winIcons struct {
	mic, micOff, video, videoOff, devices, hangup, accept, hide, full, unfull, link, badge icon.ID
	micSmall, speakerSmall, videoSmall                                                     icon.ID
}

// picker is one device picker's options, rebuilt when the devices or the
// call's choice change.
type picker struct {
	sel     int
	opts    []string // views of b
	ids     []span   // the devices' ids in b, opts[i+1]'s
	b       []byte
	gen     uint32
	current []byte
	labels  []span
	ok, has bool
}

// tile is one tile of a group call's grid.
type tile struct {
	kind   tileKind
	member int // the participant's index (tilePerson)
	more   int // tileMore: how many more
}

type tileKind uint8

const (
	tilePerson tileKind = iota
	tileMe
	tileMore
)

// NewWindow returns the call window's screen.
func NewWindow() *Window { return &Window{} }

// Config is the call window's configuration: its title is who the call is
// with.
func (w *Window) Config(st *state.State) app.WindowConfig {
	title := "Zap"
	if h := hubOf(st); h.call.on {
		if n := h.appendName(nil); len(n) > 0 {
			title = string(n)
		}
	}
	// The smallest size is the main window's upstream (zf:src/main.rs:617).
	cfg := app.WindowConfig{Title: title, Width: 720, Height: 520, MinWidth: 400, MinHeight: 300, Materials: true}
	cfg.Titlebar, cfg.TrafficLights = rexui.TitlebarFor(theme.ToolbarRex)
	return cfg
}

// Init readies the screen for its window.
func (w *Window) Init(e *screen.Env) error {
	w.h = hubOf(e.S)
	tb := theme.MacWindow.Capsule.Glyph + 2
	ic := &w.ic
	for _, it := range [...]struct {
		dst *icon.ID
		sym string
		fb  rune
		pt  float32
	}{
		{&ic.mic, "mic.fill", '●', 18}, {&ic.micOff, "mic.slash.fill", '○', 18}, {&ic.video, "video.fill", '▶', 18}, {&ic.videoOff, "video.slash.fill", '▷', 18},
		{&ic.devices, "speaker.wave.2.fill", '♪', 18}, {&ic.hangup, "phone.down.fill", '✕', 18}, {&ic.accept, "phone.fill", '☎', 18},
		{&ic.hide, "chevron.left", '‹', tb}, {&ic.full, "arrow.up.left.and.arrow.down.right", '⤢', tb}, {&ic.unfull, "arrow.down.right.and.arrow.up.left", '⤡', tb},
		{&ic.link, "link", '∞', 48}, {&ic.badge, "mic.slash.fill", '○', 14},
		{&ic.micSmall, "mic", '●', 12}, {&ic.speakerSmall, "speaker.wave.2", '♪', 12}, {&ic.videoSmall, "video", '▶', 12},
	} {
		*it.dst = e.K.AddIcon(icon.Spec{Symbol: it.sym, Fallback: it.fb, Pt: it.pt})
	}
	w.devicesOpen = false
	w.pics.reset()
	w.h.vid.release()
	return nil
}

// Build builds the whole window.
func (w *Window) Build(e *screen.Env) {
	c, k := e.C(), e.K
	cw, ch := c.Size()
	W, H := float32(cw), float32(ch)
	h := w.h
	now := e.S.Now()
	h.tick(now)
	r := k.Theme()
	// A peer's picture is letterboxed in black, as any video is; otherwise
	// the call sits on Rex's field, as the main window's panes do.
	var flags ui.Flags
	if h.vid.has(core.StreamRemote) {
		c.NextBG(0x000000FF)
		flags = ui.DrawBackground
	} else if r.Chrome != nil {
		r.Chrome.Background(c, r)
	}
	theme.At(c, 0, 0, W, H)
	c.Push(c.Box(base.Floating|flags, "call"))
	if !h.showing() {
		// The call is over, put aside or behind the app lock: the window goes.
		h.winSeen = false
		c.Pop()
		w.close(e)
		return
	}
	h.winSeen = true
	w.keys(e)
	tbH := theme.Snap(r.Window.Toolbars[theme.ToolbarRex].Height)
	pad := e.Pt(padPt)
	area := screen.Rect{X: pad, Y: tbH, W: W - 2*pad, H: H - tbH - pad}
	u := &h.call.u
	switch {
	case u.Phase == abi.CallPhaseIncoming:
		w.ringing(e, area)
	case u.HasGroup && len(u.Group.Participants) > 0 && !h.private():
		w.group(e, area)
	default:
		w.live(e, area, W, H)
	}
	c.Pop()
	w.toolbar(e)
	wakeEachSecond(e, h, now)
}

// close closes the window (the shell notes it closed).
func (w *Window) close(e *screen.Env) {
	w.devicesOpen = false
	w.h.vid.release()
	if e.A == nil {
		return
	}
	if win := e.A.Window(); win != nil && win.Open() {
		win.Close()
	}
}

// fullscreen is whether the window is in full screen.
func (w *Window) fullscreen(e *screen.Env) bool {
	if e.A != nil {
		if win := e.A.Window(); win != nil {
			return win.Fullscreen()
		}
	}
	return w.full
}

func (w *Window) toggleFullscreen(e *screen.Env) {
	if e.A != nil {
		if win := e.A.Window(); win != nil {
			win.ToggleFullscreen()
			return
		}
	}
	w.full = !w.full
}

// leave puts the call aside: the window closes, full screen first
// (zf:src/app/calls.rs:561).
func (w *Window) leave(e *screen.Env) {
	if w.fullscreen(e) {
		w.toggleFullscreen(e)
	}
	w.full = false
	w.h.leave()
}

// keys: Escape leaves full screen first, then puts the call aside; neither
// ends it (CA-008).
func (w *Window) keys(e *screen.Env) {
	c := e.C()
	if c.ModalOpen() || e.K.MenuOpen() || !c.TakeKey(ui.KeyEscape, 0) {
		return
	}
	switch {
	case w.fullscreen(e):
		w.toggleFullscreen(e)
	case live(w.h.call.u.Phase) && w.h.call.u.Phase != abi.CallPhaseIncoming:
		w.leave(e)
	}
}

// toolbar is the band over the call: Hide the call screen at the start and
// Full screen at the end (zf:src/ui/call.rs:287). Neither ends the call.
func (w *Window) toolbar(e *screen.Env) {
	k := e.K
	u := &w.h.call.u
	if w.h.vid.has(core.StreamRemote) {
		// Over the picture the two buttons keep a band of the window's colour.
		c := e.C()
		cw, _ := c.Size()
		theme.At(c, 0, 0, float32(cw), theme.Snap(k.Theme().Window.Toolbars[theme.ToolbarRex].Height))
		c.NextBG(k.Theme().Colors[theme.TextBackground]&^0xFF | 0xE6)
		c.Box(base.Floating|ui.DrawBackground, "toolbar plate")
	}
	var band uint32 // over the field: none, the field shows
	if w.h.vid.has(core.StreamRemote) {
		band = k.Theme().Colors[theme.TextBackground]
	}
	tb := rexui.BeginToolbar(k.Kit, "Toolbar", rexui.ToolbarOpts{Style: theme.ToolbarRex, BandFill: band})
	if live(u.Phase) && u.Phase != abi.CallPhaseIncoming {
		if tb.Item(w.ic.hide, i18n.T("Hide the call screen")).Clicked {
			w.leave(e)
		}
	}
	tb.Flexible()
	ic, name := w.ic.full, i18n.T("Full screen")
	if w.fullscreen(e) {
		ic, name = w.ic.unfull, i18n.T("Exit full screen")
	}
	if tb.Item(ic, name).Clicked {
		w.toggleFullscreen(e)
	}
	tb.End()
}

// at builds a floating box at r (window pixels) and pushes it.
func at(e *screen.Env, r screen.Rect, axis ui.Axis, label string) {
	c := e.C()
	theme.At(c, r.X, r.Y, r.W, r.H)
	c.NextAxis(axis)
	c.NextCross(ui.CrossCenter)
	c.Push(c.Box(base.Floating, label))
}

// spacer is an empty box h pixels high.
func spacer(e *screen.Env, h float32) {
	c := e.C()
	c.NextSize(ui.Px(1), ui.Px(max(h, 0)))
	c.Box(0, "")
}

// text builds a centred line of b in style st and colour col.
func text(e *screen.Env, b []byte, st rexui.TextStyle, col rexui.Tint, wrap bool) {
	k := e.K
	if len(b) == 0 {
		return
	}
	rexui.LabelBytes(k.Kit, b, rexui.LabelOpts{Style: st, Color: col, Wrap: wrap})
}

// statusTint is the status line's colour: the accent, red for a call that
// failed, secondary for one that ended.
func statusTint(p abi.CallPhase) rexui.Tint {
	switch p {
	case abi.CallPhaseFailed:
		return rexui.Of(theme.Red)
	case abi.CallPhaseEnded:
		return rexui.Of(theme.Label2)
	}
	return rexui.Of(theme.Accent)
}

// minAvatarPt is the smallest the call's picture shrinks to when the window
// is too short for upstream's.
const minAvatarPt = 60

// lineH is a text style's line, in pixels.
func lineH(e *screen.Env, st rexui.TextStyle) float32 {
	return e.K.Theme().Styles[rexui.TextStyles[st]].Line
}

// avatarFit is the side (points) of the picture at the head of a column and
// the gap (pixels) above it. Upstream's side is a share of the height between
// 96 pt and hi, and its gap a share of the column; in a window at its
// smallest those do not leave room for the lines below (below, pixels) in the
// column (room, pixels), so the gap goes first and then the picture, down to
// minAvatarPt, as far as the column needs and no further.
func avatarFit(e *screen.Env, areaH, room, below, share, hi, topShare float32) (size, top float32) {
	sc := e.K.Scale()
	size = min(max(areaH*share/sc, 96), hi)
	top = room * topShare
	if need := top + size*sc + below - room; need > 0 {
		cut := min(need, top)
		top -= cut
		if need -= cut; need > 0 {
			size = max(size-need/sc, minAvatarPt)
		}
	}
	return float32(int(size/2) * 2), theme.Snap(top) // whole, even points: the initials sit in the middle
}

// ringing is a call that rings: who it is, and the two answers
// (zf:src/ui/call.rs:751).
func (w *Window) ringing(e *screen.Env, area screen.Rect) {
	h, k := w.h, e.K
	u := &h.call.u
	a := h.account()
	answerH := e.Pt(controlPt + 26)
	at(e, screen.Rect{X: area.X, Y: area.Y, W: area.W, H: area.H - answerH}, ui.AxisY, "ringing")
	below := e.Pt(18+6+10) + lineH(e, rexui.TextTitle1) + lineH(e, rexui.TextTitle3) // and a breath above the answers
	if a != nil && u.HasGroup && !h.private() {
		for i := range u.Group.Participants {
			if u.Group.Participants[i].Joined {
				below += e.Pt(16 + facePt)
				break
			}
		}
	}
	size, top := avatarFit(e, area.H, area.H-answerH, below, 0.24, 160, 0.14)
	spacer(e, top)
	w.buf = h.appendName(w.buf[:0])
	if h.link() {
		linkAvatar(e, size, w.ic.link)
	} else {
		rexui.Avatar(k.Kit, model.S(w.buf), rexui.AvatarOpts{Size: size, Image: w.peerPic(e, size)})
	}
	spacer(e, e.Pt(18))
	text(e, w.buf, rexui.TextTitle1, rexui.Tint{}, false)
	spacer(e, e.Pt(6))
	w.buf2 = appendIncoming(w.buf2[:0], u, h.caller())
	text(e, w.buf2, rexui.TextTitle3, rexui.Of(theme.Label2), false)
	// Who is on the group call already, by their pictures (+N past five).
	if a != nil && u.HasGroup && !h.private() {
		w.faces, w.faceIDs = w.faces[:0], w.faceIDs[:0]
		for i := range u.Group.Participants {
			if pp := &u.Group.Participants[i]; pp.Joined {
				w.faces = append(w.faces, model.S(a.Model.DisplayName(pp.ID)))
				w.faceIDs = append(w.faceIDs, pp.ID)
			}
		}
		if len(w.faces) > 0 {
			spacer(e, e.Pt(16))
			// The stack is named by who is on the call.
			w.buf3 = w.buf3[:0]
			for i, f := range w.faces {
				if i > 0 {
					w.buf3 = append(w.buf3, ", "...)
				}
				w.buf3 = append(w.buf3, f...)
			}
			w.faceImgs = w.faceImgs[:0]
			for i := range w.faces {
				var h img.Handle
				if i < facesMax {
					h = w.pics.of(e, a, w.faceIDs[i], facePt)
				}
				w.faceImgs = append(w.faceImgs, h)
			}
			rexui.AvatarStack(k.Kit, model.S(w.buf3), w.faces, facesMax, rexui.AvatarOpts{Size: facePt, Backdrop: rexui.Of(theme.Field), Images: w.faceImgs})
		}
	}
	e.C().Pop()

	at(e, screen.Rect{X: area.X, Y: area.Y + area.H - answerH, W: area.W, H: answerH}, ui.AxisX, "answers")
	e.K.NextJustify(ui.JustifyCenter)
	k.NextGap(answerGap)
	k.BeginContainer(ui.AxisX, ui.CrossStart, "answer row", ui.Pct(1), ui.Pct(1), 0)
	if w.answer(e, w.ic.hangup, i18n.T("Decline"), &recDanger) {
		h.decline()
	}
	if w.answer(e, w.ic.accept, i18n.T("Accept"), &recAccept) {
		h.answer()
	}
	k.EndContainer()
	e.C().Pop()
}

// answer is one of a ringing call's answers, its word under it.
func (w *Window) answer(e *screen.Env, ic icon.ID, label string, v *rexui.ButtonRecipe) bool {
	k := e.K
	k.NextGap(6)
	k.BeginContainer(ui.AxisY, ui.CrossCenter, label, ui.Children(1), ui.Children(1), 0)
	clicked := w.control(e, ic, label, v, true)
	rexui.Label(k.Kit, label, rexui.LabelOpts{Style: rexui.TextSubheadline, Color: rexui.Of(theme.Label2)})
	k.EndContainer()
	return clicked
}

// control is one round control; one that does not apply yet still reads as
// one (disabled).
func (w *Window) control(e *screen.Env, ic icon.ID, name string, v *rexui.ButtonRecipe, enabled bool) bool {
	k := e.K
	k.BeginDisabled(!enabled)
	side := ui.Px(e.Pt(controlPt))
	k.NextSize(side, side)
	s := rexui.IconButton(k.Kit, ic, name, rexui.IconButtonOpts{Variant: rexui.IconBordered, Recipe: v, Size: theme.SizeXLarge})
	rexui.Tooltip(k.Kit, s.ID, name)
	k.EndDisabled()
	return s.Clicked && enabled
}

// live is a call that is up, coming up or just over, one to one
// (zf:src/ui/call.rs:163).
func (w *Window) live(e *screen.Env, area screen.Rect, W, H float32) {
	h, k := w.h, e.K
	u := &h.call.u
	// The peer's picture fills the window when there is one, and the name
	// and the length sit over it on a plate of their own.
	if u.Video && h.drawVideo(e, W, H, core.StreamRemote, ui.FitContain, 0) {
		w.overPicture(e, area)
		return
	}
	bottom := w.bottom(e, area, false)
	at(e, screen.Rect{X: area.X, Y: area.Y, W: area.W, H: bottom - area.Y}, ui.AxisY, "peer")
	below := e.Pt(18+6) + lineH(e, rexui.TextTitle1) + lineH(e, rexui.TextTitle3)
	foot := e.Pt(4) + lineH(e, rexui.TextFootnote)
	if u.VideoRequested {
		below += foot
	}
	if u.Video && u.Phase == abi.CallPhaseActive && !u.RemoteVideo {
		below += foot
	}
	below += foot * float32(len(u.LostDevices)+b2i(u.HasPeerAudio))
	size, top := avatarFit(e, area.H, bottom-area.Y, below, 0.26, 168, area.H*0.12/(bottom-area.Y))
	spacer(e, top)
	w.buf = h.appendName(w.buf[:0])
	if h.link() {
		linkAvatar(e, size, w.ic.link)
	} else {
		rexui.Avatar(k.Kit, model.S(w.buf), rexui.AvatarOpts{Size: size, Image: w.peerPic(e, size)})
	}
	spacer(e, e.Pt(18))
	text(e, w.buf, rexui.TextTitle1, rexui.Tint{}, false)
	spacer(e, e.Pt(6))
	w.buf2 = h.appendStatus(w.buf2[:0], e.S.Now())
	text(e, w.buf2, rexui.TextTitle3, statusTint(u.Phase), false)
	if u.VideoRequested {
		// The camera button below answers it.
		spacer(e, e.Pt(4))
		rexui.Label(k.Kit, i18n.T("Wants to switch to video: turn on your camera to answer"), rexui.LabelOpts{Style: rexui.TextFootnote, Color: rexui.Of(theme.Accent)})
	}
	if u.Video && u.Phase == abi.CallPhaseActive && !u.RemoteVideo {
		spacer(e, e.Pt(4))
		rexui.Label(k.Kit, i18n.T("Their camera is off"), rexui.LabelOpts{Style: rexui.TextFootnote, Color: rexui.Of(theme.Label2)})
	}
	w.notes(e)
	e.C().Pop()
	w.preview(e, area)
}

// overPicture is the live call over the peer's picture: the name and the
// call's line on a dark plate, the notes under it, our camera and the
// controls.
func (w *Window) overPicture(e *screen.Env, area screen.Rect) {
	h, c := w.h, e.C()
	bottom := w.bottom(e, area, true)
	at(e, screen.Rect{X: area.X, Y: area.Y, W: area.W, H: bottom - area.Y}, ui.AxisY, "peer")
	c.NextBG(0x00000096)
	c.NextRadius(e.Pt(14))
	c.NextPadding(e.Pt(14), e.Pt(6), e.Pt(14), e.Pt(6))
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextSize(ui.Children(1), ui.Children(1))
	c.Push(c.Box(ui.DrawBackground, "plate"))
	white := rexui.OfRGBA(0xFFFFFFFF) // the picture is not themed: white over it in either mode
	w.buf = h.appendName(w.buf[:0])
	text(e, w.buf, rexui.TextHeadline, white, false)
	w.buf2 = h.appendStatus(w.buf2[:0], e.S.Now())
	text(e, w.buf2, rexui.TextSubheadline, white, false)
	c.Pop()
	w.notes(e)
	c.Pop()
	w.preview(e, area)
}

// bottom builds the controls along the bottom of area and, while they are
// open, the device pickers above them; it returns the top of what it built.
func (w *Window) bottom(e *screen.Env, area screen.Rect, over bool) float32 {
	h := w.h
	ctl := e.Pt(controlPt)
	y := area.Y + area.H - ctl
	if over {
		// Over a picture the controls sit on a plate of the window's colour, so
		// they read in either mode.
		c := e.C()
		pw, pad := 4*ctl+3*e.Pt(controlGap)+2*e.Pt(14), e.Pt(8)
		theme.At(c, area.X+(area.W-pw)/2, y-pad, pw, ctl+2*pad)
		c.NextBG(e.K.Theme().Colors[theme.TextBackground]&^0xFF | 0xE6)
		c.NextRadius((ctl + 2*pad) / 2)
		c.Box(base.Floating|ui.DrawBackground, "controls plate")
	}
	at(e, screen.Rect{X: area.X, Y: y, W: area.W, H: ctl}, ui.AxisX, "controls")
	w.controls(e)
	e.C().Pop()
	top := y - e.Pt(20)
	if w.devicesOpen && live(h.call.u.Phase) {
		ph := e.Pt(pickerRowPt)
		at(e, screen.Rect{X: area.X, Y: top - ph, W: area.W, H: ph}, ui.AxisX, "pickers")
		w.devices(e)
		e.C().Pop()
		top -= ph + e.Pt(16)
	}
	return top
}

// controls are Mute, the camera, the device pickers and Hang up
// (zf:src/ui/call.rs:505).
func (w *Window) controls(e *screen.Env) {
	h, k := w.h, e.K
	u := &h.call.u
	on := live(u.Phase)
	conn := connected(u.Phase)
	e.K.NextJustify(ui.JustifyCenter)
	k.NextGap(controlGap)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "control row", ui.Pct(1), ui.Pct(1), 0)
	// The engine's own mute, which also tells the peer.
	if u.Muted {
		if w.control(e, w.ic.micOff, i18n.T("Unmute"), &recInverse, conn) {
			h.setMuted(false)
		}
	} else if w.control(e, w.ic.mic, i18n.T("Mute"), &recSurface, conn) {
		h.setMuted(true)
	}
	// A group call takes video only when it began as a video call: the
	// library refuses to turn a group's voice call into one.
	if !u.HasGroup || u.Video {
		switch {
		case !u.Video:
			if w.control(e, w.ic.video, i18n.T("Start video"), &recSurface, conn) {
				h.setCamera(true)
			}
		case u.CameraOn:
			if w.control(e, w.ic.video, i18n.T("Turn the camera off"), &recSurface, conn) {
				h.setCamera(false)
			}
		default:
			if w.control(e, w.ic.videoOff, i18n.T("Turn the camera on"), &recInverse, conn) {
				h.setCamera(true)
			}
		}
	}
	v := &recSurface
	if w.devicesOpen {
		v = &recInverse
	}
	if w.control(e, w.ic.devices, i18n.T("Devices"), v, on) {
		w.devicesOpen = !w.devicesOpen
		if w.devicesOpen {
			h.refreshDevices()
		}
	}
	if w.control(e, w.ic.hangup, i18n.T("Hang up"), &recDanger, on) {
		h.hangup()
	}
	k.EndContainer()
}

// devices are the microphone, speaker and (on a video call) camera pickers:
// each moves the running call, without changing the system's defaults
// (zf:src/ui/call.rs:595).
func (w *Window) devices(e *screen.Env) {
	h, k := w.h, e.K
	u := &h.call.u
	e.K.NextJustify(ui.JustifyCenter)
	k.NextGap(16)
	k.BeginContainer(ui.AxisX, ui.CrossStart, "picker row", ui.Pct(1), ui.Pct(1), 0)
	d := &h.devs.list
	w.picker(e, 0, abi.DeviceKindMicrophone, w.ic.micSmall, i18n.T("Microphone"), i18n.T("System default"), d.Microphones, u.Microphone, u.HasMicrophone)
	w.picker(e, 1, abi.DeviceKindSpeaker, w.ic.speakerSmall, i18n.T("Speaker"), i18n.T("System default"), d.Speakers, u.Speaker, u.HasSpeaker)
	if u.Video {
		w.picker(e, 2, abi.DeviceKindCamera, w.ic.videoSmall, i18n.T("Camera"), i18n.T("First camera"), d.Cameras, u.Camera, u.HasCamera)
	}
	k.EndContainer()
}

// picker is one device picker: its title with an icon, and what the machine
// has, the call's choice chosen.
func (w *Window) picker(e *screen.Env, i int, kind abi.DeviceKind, ic icon.ID, title, def string, devs []abi.Device, cur []byte, has bool) {
	k := e.K
	pk := &w.pick[i]
	if !has {
		cur = nil
	}
	if !pk.ok || pk.gen != w.h.devs.gen || pk.has != has || !bytes.Equal(pk.current, cur) {
		pk.rebuild(def, devs, cur, has)
		pk.gen = w.h.devs.gen
	}
	k.NextGap(4)
	k.BeginContainer(ui.AxisY, ui.CrossStart, title, ui.Px(e.Pt(pickerPt)), ui.Children(1), 0)
	k.NextGap(4)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "title", ui.Children(1), ui.Children(1), 0)
	k.Kit.Icon(ic, k.Theme().Colors[theme.Label2])
	rexui.Label(k.Kit, title, rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
	k.EndContainer()
	k.NextWidth(ui.Pct(1))
	was := pk.sel
	if s := k.Select(title, &pk.sel, pk.opts); (s.Changed || pk.sel != was) && pk.sel >= 0 {
		var id []byte
		if pk.sel > 0 {
			id = pk.ids[pk.sel-1].of(pk.b)
		}
		w.h.setDevice(kind, id)
	}
	k.EndContainer()
}

func (pk *picker) rebuild(def string, devs []abi.Device, cur []byte, has bool) {
	pk.ok = true
	pk.b, pk.ids, pk.opts = pk.b[:0], pk.ids[:0], pk.opts[:0]
	pk.current, pk.has = append(pk.current[:0], cur...), has
	labels := pk.labels[:0]
	pk.sel = 0
	for i := range devs {
		pk.ids = append(pk.ids, put(&pk.b, devs[i].ID))
		l := devs[i].Label
		if len(l) == 0 {
			l = devs[i].ID
		}
		labels = append(labels, put(&pk.b, l))
		if has && bytes.Equal(devs[i].ID, cur) {
			pk.sel = i + 1
		}
	}
	if has && pk.sel == 0 && len(cur) > 0 {
		// A device the list does not name shows as itself.
		pk.ids = append(pk.ids, put(&pk.b, cur))
		labels = append(labels, put(&pk.b, cur))
		pk.sel = len(pk.ids)
	}
	pk.opts = append(pk.opts, def)
	for _, l := range labels {
		pk.opts = append(pk.opts, model.S(l.of(pk.b)))
	}
	pk.labels = labels
}

// notes are what moved under the call: a device that went away, or the
// peer's audio not arriving, said rather than swallowed (CA-014, CA-015).
func (w *Window) notes(e *screen.Env) {
	k := e.K
	u := &w.h.call.u
	for i := range u.LostDevices {
		spacer(e, e.Pt(4))
		w.buf3 = appendLost(w.buf3[:0], &u.LostDevices[i])
		text(e, w.buf3, rexui.TextFootnote, rexui.Of(theme.Orange), false)
	}
	if u.HasPeerAudio {
		spacer(e, e.Pt(4))
		rexui.Label(k.Kit, peerAudioText(&u.PeerAudio), rexui.LabelOpts{Style: rexui.TextFootnote, Color: rexui.Of(theme.Orange)})
	}
}

// preview is our own camera in the top corner at its own shape, while it
// runs (zf:src/ui/call.rs:333): its picture once frames are drawn (video.go),
// our picture meanwhile.
func (w *Window) preview(e *screen.Env, area screen.Rect) {
	h, c, k := w.h, e.C(), e.K
	u := &h.call.u
	if !u.Video || !u.CameraOn {
		h.vid.drop(core.StreamLocal)
		return
	}
	// A tile's shape until a picture is in, then its own, fit inside the
	// share of the window it may take.
	bw, bh := area.W*previewPart, area.H*previewPart
	pw, ph := bw, bw/tileAspect
	if ph > bh {
		pw, ph = bh*tileAspect, bh
	}
	if iw, ih := h.vid.size(core.StreamLocal); iw > 0 {
		pw, ph = fitInside(bw, bh, iw, ih)
	}
	r := screen.Rect{X: area.X + area.W - pw, Y: area.Y + e.Pt(8), W: pw, H: ph}
	th := k.Theme()
	theme.At(c, r.X, r.Y, r.W, r.H)
	c.NextBG(th.Colors[theme.Fill2])
	c.NextBorder(th.Colors[theme.Separator], th.Lines.Hairline)
	c.NextRadius(e.Pt(12))
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextGap(e.Pt(4))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(i18n.T("You"))
	c.Push(c.Box(base.Floating|ui.DrawBackground|ui.DrawBorder|ui.Clip, "own camera"))
	if !h.drawVideo(e, r.W, r.H, core.StreamLocal, ui.FitFill, e.Pt(12)) {
		side := min(max(ph/k.Scale()*0.4, 24), 56)
		var mine []byte
		if a := h.account(); a != nil {
			mine = a.Model.Me.ID()
		}
		rexui.Avatar(k.Kit, w.me(), rexui.AvatarOpts{Size: side, Image: w.pics.of(e, h.account(), mine, side)})
		rexui.Label(k.Kit, i18n.T("You"), rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
	}
	c.Pop()
}

// peerPic is the call's person's picture, none while a locked chat hides who
// it is with.
func (w *Window) peerPic(e *screen.Env, size float32) img.Handle {
	if w.h.private() {
		return 0
	}
	return w.pics.of(e, w.h.account(), w.h.call.u.Chat, size)
}

// me is our own name, for our avatar's initials.
func (w *Window) me() string {
	a := w.h.account()
	if a == nil {
		return i18n.T("You")
	}
	if n := a.Model.Me.Name(); len(n) > 0 {
		return model.S(n)
	}
	return i18n.T("You")
}

// group is a group call that is up, coming up or just over: the group's
// name and how the call goes on top, a tile for each person on it, and the
// controls (zf:src/ui/call.rs:926).
func (w *Window) group(e *screen.Env, area screen.Rect) {
	h := w.h
	u := &h.call.u
	a := h.account()
	headH := e.Pt(52)
	at(e, screen.Rect{X: area.X, Y: area.Y, W: area.W, H: headH}, ui.AxisY, "group header")
	w.buf = h.appendName(w.buf[:0])
	text(e, w.buf, rexui.TextTitle2, rexui.Tint{}, false)
	spacer(e, e.Pt(2))
	w.buf2 = h.appendStatus(w.buf2[:0], e.S.Now())
	text(e, w.buf2, rexui.TextSubheadline, statusTint(u.Phase), false)
	e.C().Pop()
	bottom := w.bottom(e, area, false)
	// What moved under the call, between the tiles and the controls.
	if n := len(u.LostDevices) + b2i(u.HasPeerAudio); n > 0 {
		nh := float32(n) * e.Pt(22)
		at(e, screen.Rect{X: area.X, Y: bottom - nh, W: area.W, H: nh}, ui.AxisY, "notes")
		w.notes(e)
		e.C().Pop()
		bottom -= nh + e.Pt(8)
	}
	// Those in the call first, then ourselves, then those only asked to it;
	// past the most a grid holds, the last tile counts the rest.
	w.tiles = w.tiles[:0]
	for i := range u.Group.Participants {
		if u.Group.Participants[i].Joined {
			w.tiles = append(w.tiles, tile{kind: tilePerson, member: i})
		}
	}
	w.tiles = append(w.tiles, tile{kind: tileMe})
	for i := range u.Group.Participants {
		if !u.Group.Participants[i].Joined {
			w.tiles = append(w.tiles, tile{kind: tilePerson, member: i})
		}
	}
	if len(w.tiles) > maxTiles {
		rest := len(w.tiles) - (maxTiles - 1)
		w.tiles = append(w.tiles[:maxTiles-1], tile{kind: tileMore, more: rest})
	}
	top := area.Y + headH + e.Pt(12)
	grid := screen.Rect{X: area.X, Y: top, W: area.W, H: max(bottom, top+e.Pt(60)) - top}
	w.rects = tileGrid(w.rects[:0], len(w.tiles), grid, e.Pt(tileGap))
	for i, t := range w.tiles {
		if i >= len(w.rects) {
			break
		}
		r := w.rects[i]
		switch t.kind {
		case tilePerson:
			p := &u.Group.Participants[t.member]
			var name []byte
			if a != nil {
				name = a.Model.DisplayName(p.ID)
			}
			w.personTile(e, i, r, name, p.ID, p.Joined, core.StreamMember+uint32(t.member), false)
		case tileMe:
			var mine []byte
			if a != nil {
				mine = a.Model.Me.ID()
			}
			w.personTile(e, i, r, []byte(nil), mine, true, core.StreamLocal, u.Muted)
		case tileMore:
			w.moreTile(e, i, r, t.more)
		}
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// tileBox opens tile i's box at r, a group named name.
func (w *Window) tileBox(e *screen.Env, i int, r screen.Rect, name string) {
	c, th := e.C(), e.K.Theme()
	theme.At(c, r.X, r.Y, r.W, r.H)
	c.NextBG(th.Colors[theme.Fill2])
	c.NextRadius(e.Pt(14))
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextGap(e.Pt(8))
	c.NextPadding(e.Pt(10), e.Pt(10), e.Pt(10), e.Pt(10))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(name)
	c.Push(c.BoxKeyed(base.Floating|ui.DrawBackground|ui.Clip, ui.KeyN(c.ParentKey(), 0x711E00+uint64(i)), nil))
}

// personTile is one person's tile: their camera while it runs, else their
// picture and name, dimmed while they are only asked to the call; ours is
// "You", with a red microphone while we are muted (CA-017).
func (w *Window) personTile(e *screen.Env, i int, r screen.Rect, name, jid []byte, joined bool, stream uint32, muted bool) {
	k, c := e.K, e.C()
	me := name == nil
	label := i18n.T("You")
	if !me {
		label = model.S(name)
	}
	w.tileBox(e, i, r, label)
	if w.h.drawVideo(e, r.W, r.H, stream, ui.FitFill, e.Pt(14)) {
		w.namePlate(e, r, label)
	} else {
		lh := e.Pt(22)
		if !joined {
			lh = e.Pt(40)
		}
		size := min(max(min(r.H-lh-e.Pt(24), r.W*0.5)/k.Scale(), 28), 120)
		avatarName := label
		if me {
			avatarName = w.me()
		}
		if !joined {
			c.NextOpacity(0.45) // asked, not in yet: the face waits behind a veil
			c.NextSize(ui.Children(1), ui.Children(1))
			c.Push(c.Box(0, "veil"))
		}
		rexui.Avatar(k.Kit, avatarName, rexui.AvatarOpts{Size: size, Backdrop: rexui.Of(theme.Fill2), Image: w.pics.of(e, w.h.account(), jid, size)})
		if !joined {
			c.Pop()
		}
		col := rexui.Tint{}
		if !joined {
			col = rexui.Of(theme.Label2)
		}
		rexui.Label(k.Kit, label, rexui.LabelOpts{Style: rexui.TextSemibold, Color: col})
		if !joined {
			rexui.Label(k.Kit, i18n.T("Invited"), rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label3)})
		}
	}
	if muted {
		// Only ours: the library says nothing of anyone else's microphone.
		th := k.Theme()
		d := e.Pt(30)
		theme.At(c, r.W-d-e.Pt(10), e.Pt(10), d, d)
		c.NextBG(th.Colors[theme.Red])
		c.NextRadius(d / 2)
		c.NextCross(ui.CrossCenter)
		c.NextJustify(ui.JustifyCenter)
		c.NextRole(ui.RoleImage)
		c.NextSemName(i18n.T("Unmute"))
		c.Push(c.Box(base.Floating|ui.DrawBackground, "muted"))
		k.Kit.Icon(w.ic.badge, 0xFFFFFFFF) // white on the red disc in either mode
		c.Pop()
	}
	c.Pop()
}

// namePlate is a person's name on a dark plate in the corner of a tile that
// shows their camera (zf:src/ui/call.rs:1116).
func (w *Window) namePlate(e *screen.Env, r screen.Rect, label string) {
	c := e.C()
	ph := e.Pt(26)
	c.NextPos(e.Pt(10), r.H-e.Pt(10)-ph)
	c.NextSize(ui.Children(1), ui.Px(ph))
	c.NextBG(0x00000096)
	c.NextRadius(e.Pt(8))
	c.NextPadding(e.Pt(8), 0, e.Pt(8), 0)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.Push(c.Box(base.Floating|ui.DrawBackground, "name"))
	rexui.Label(e.K.Kit, label, rexui.LabelOpts{Style: rexui.TextSemibold, Color: rexui.OfRGBA(0xFFFFFFFF)})
	c.Pop()
}

// moreTile counts the people the grid has no room for.
func (w *Window) moreTile(e *screen.Env, i int, r screen.Rect, n int) {
	w.buf3 = appendInt(append(w.buf3[:0], '+'), int64(n))
	w.tileBox(e, i, r, model.S(w.buf3))
	text(e, w.buf3, rexui.TextTitle1, rexui.Of(theme.Label2), false)
	e.C().Pop()
}

// tileGrid lays out count tiles of tileAspect in area, as large as they fit:
// the column count that leaves the biggest tile wins, the grid sits in the
// middle, and a short last row is centred under the others
// (zf:src/ui/call.rs:874).
func tileGrid(dst []screen.Rect, count int, area screen.Rect, gap float32) []screen.Rect {
	if count == 0 || area.W <= 0 || area.H <= 0 {
		return dst
	}
	bestCols, bestW, bestH := 0, float32(0), float32(0)
	for cols := 1; cols <= count; cols++ {
		rows := (count + cols - 1) / cols
		cw := (area.W - gap*float32(cols-1)) / float32(cols)
		ch := (area.H - gap*float32(rows-1)) / float32(rows)
		if cw <= 0 || ch <= 0 {
			continue
		}
		tw, th := cw, cw/tileAspect
		if cw/ch > tileAspect {
			tw, th = ch*tileAspect, ch
		}
		if bestCols == 0 || tw*th > bestW*bestH {
			bestCols, bestW, bestH = cols, tw, th
		}
	}
	if bestCols == 0 {
		return dst
	}
	rows := (count + bestCols - 1) / bestCols
	height := float32(rows)*bestH + gap*float32(rows-1)
	top := area.Y + area.H/2 - height/2
	for i := 0; i < count; i++ {
		row, col := i/bestCols, i%bestCols
		inRow := bestCols
		if row == rows-1 {
			inRow = count - row*bestCols
		}
		width := float32(inRow)*bestW + gap*float32(inRow-1)
		left := area.X + area.W/2 - width/2
		dst = append(dst, screen.Rect{X: left + float32(col)*(bestW+gap), Y: top + float32(row)*(bestH+gap), W: bestW, H: bestH})
	}
	return dst
}
