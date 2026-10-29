package cropper

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/task"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/i18n"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// Layout, in points (zf:src/ui/image_editor.rs).
const (
	margin = 28 // room left around the cropper
	bar    = 44 // height of the rows of controls above and below the picture
	handle = 13 // side of a corner or edge handle
	grip   = 22 // how far around a handle a drag still counts as gripping it
	border = 2  // the crop's border
)

// source is where the picture being cropped comes from: a file, or straight
// RGBA rows (w × h) of a pasted picture.
type source struct {
	path string
	rgba []byte
	w, h int
}

func (s source) pasted() bool { return s.path == "" }

// origin is a staged picture as it was before it was cropped. Keeping it
// means cropping the same picture again opens on the original with its crop,
// so a second pass does not stack another lossy generation on the first
// (zf:src/model.rs PictureOrigin).
type origin struct {
	acct   int
	target uint64
	src    source
	edit   Edit
}

// maxOrigins bounds the originals kept; the oldest go first.
const maxOrigins = 8

type stage uint8

const (
	idle       stage = iota
	inspecting       // InspectPicture is out, the answer opens the editor
	loading          // a pasted picture's pixels are being read from the core
	editing
)

// grab is the drag in progress: which part of the crop, and the crop when the
// press began (a drag is measured from it, so no rounding builds up).
type grab struct {
	on    bool
	edge  Edge
	start Crop
}

// preview is the file a turned or pasted picture is shown from.
type preview struct {
	file    string // the written preview ("" until one is)
	turns   uint8  // the turns it holds
	out     string // the file the job in flight writes
	want    uint8  // the turns that job holds
	ticket  task.Ticket
	pending bool
	failed  bool
}

// Screen is the cropper.
type Screen struct {
	st   *state.State
	imgs *img.Cache
	pool *task.Pool[job, done]
	res  []task.Result[done]

	closeIc, keepIc, leftIc, rightIc icon.ID

	cur    uint64 // identity of the cropper on screen (0: none)
	acct   int
	target uint64
	stage  stage
	src    source
	edit   Edit
	grab   grab
	pv     preview
	seq    uint64

	origins  []origin
	applying []origin

	// A pasted picture's pixels are read off the main thread; the reader
	// leaves them here and wakes the window.
	mu       sync.Mutex
	loadGen  uint64 // which opening the reader belongs to
	loadDone bool
	loaded   []byte
}

var _ screen.Overlay = (*Screen)(nil)

// New returns the cropper.
func New() *Screen { return &Screen{} }

// Init readies it for a window.
func (s *Screen) Init(e *screen.Env) error {
	s.st = e.S
	s.closeIc = e.K.AddIcon(ztheme.ToolIcon("xmark", '✕'))
	s.keepIc = e.K.AddIcon(icon.Spec{Symbol: "checkmark", Fallback: '✓', Pt: 15})
	s.leftIc = e.K.AddIcon(icon.Spec{Symbol: "rotate.left", Fallback: '↺', Pt: 15})
	s.rightIc = e.K.AddIcon(icon.Spec{Symbol: "rotate.right", Fallback: '↻', Pt: 15})
	e.S.Listen((*listener)(s), abi.EventTagPictureInspected, abi.EventTagPictureEdited)
	return nil
}

// identity names the cropper that is open: another picture or account starts
// it afresh.
func identity(d *state.Dialog, a *state.Account) uint64 {
	return (uint64(d.N)<<8 ^ uint64(a.Index+1)) | 1<<63
}

// Build draws the cropper over the whole window while it is open.
func (s *Screen) Build(e *screen.Env) {
	c := e.C()
	s.imgs = e.K.Images() // the window's cache; the window polls it
	s.poll(c)
	s.takeLoaded(e)
	d := &e.S.Dialog
	a := e.Acct()
	if d.Kind != state.DialogCropper || a == nil {
		if s.cur != 0 {
			s.shut()
		}
		return
	}
	if id := identity(d, a); id != s.cur {
		s.shut()
		s.cur = id
		s.open(e, a, d)
	}
	if s.stage != idle && e.S.StagedByID(s.target) == nil {
		e.S.CloseDialog() // the attachment was removed
		return
	}
	if s.stage == editing && e.S.Dialog.Kind == state.DialogCropper {
		s.build(e, a)
	}
}

// open prepares the cropper that just opened on the staged attachment N: a
// pasted picture (its pixels read from the core), the original of one cropped
// before, or a file whose size the core reads first.
func (s *Screen) open(e *screen.Env, a *state.Account, d *state.Dialog) {
	s.acct, s.target = a.Index, uint64(d.N)
	s.stage, s.grab = idle, grab{}
	s.pv = preview{}
	st := e.S.StagedByID(s.target)
	switch {
	case st == nil:
		e.S.CloseDialog()
	case st.Failed:
		s.fail(e.S, "")
	case st.Kind == state.StagedPicture:
		w, h := int(st.W), int(st.H)
		if !st.Ready || w <= 0 || h <= 0 || w > 1<<15 || h > 1<<15 || st.Blob.Len != uint64(w*h*4) {
			e.S.CloseDialog()
			return
		}
		s.stage = loading
		s.src = source{w: w, h: h}
		s.read(a.Conn, st.Blob, e.S.Wake)
	case st.Kind != state.StagedFile:
		e.S.CloseDialog()
	case s.find(s.origins, a.Index, s.target) >= 0:
		o := s.origins[s.find(s.origins, a.Index, s.target)]
		s.src, s.edit, s.stage = o.src, o.edit, editing
	case isPicture(string(st.Path)):
		cmd := a.Cmd(abi.CommandTagInspectPicture)
		cmd.InspectPicture.Target, cmd.InspectPicture.Path = s.target, st.Path
		if a.Send(cmd) != nil {
			s.fail(e.S, "")
			return
		}
		s.src = source{path: string(st.Path)}
		s.stage = inspecting
	default:
		e.S.CloseDialog()
	}
}

// read reads a pasted picture's pixels off the main thread, and wakes the
// window when they are in.
func (s *Screen) read(conn core.Conn, ref abi.BlobRef, wake func()) {
	s.mu.Lock()
	s.loadGen++
	gen := s.loadGen
	s.loaded, s.loadDone = nil, false
	s.mu.Unlock()
	go func() {
		buf := make([]byte, ref.Len)
		for off := 0; off < len(buf); {
			n, err := conn.ReadBlob(ref.ID, uint64(off), buf[off:min(off+1<<20, len(buf))])
			if err != nil || n == 0 {
				buf = nil
				break
			}
			off += n
		}
		s.mu.Lock()
		if s.loadGen == gen {
			s.loaded, s.loadDone = buf, true
		}
		s.mu.Unlock()
		wake()
	}()
}

// takeLoaded opens the editor on pixels the reader finished.
func (s *Screen) takeLoaded(e *screen.Env) {
	if s.stage != loading {
		return
	}
	s.mu.Lock()
	buf, done := s.loaded, s.loadDone
	s.loaded = nil
	s.mu.Unlock()
	if !done {
		return
	}
	if buf == nil {
		s.fail(e.S, "")
		return
	}
	s.src.rgba = buf
	s.edit, s.stage = NewEdit(uint32(s.src.w), uint32(s.src.h)), editing
	e.C().RequestFrame()
}

// fail closes a cropper that cannot start, saying why when the core did.
func (s *Screen) fail(st *state.State, why string) {
	msg := i18n.T("Could not use this image")
	if why != "" {
		msg += ": " + why
	}
	st.Toast([]byte(msg), true)
	s.stage = idle
	st.CloseDialog()
}

func (s *Screen) find(list []origin, acct int, target uint64) int {
	for i := range list {
		if list[i].acct == acct && list[i].target == target {
			return i
		}
	}
	return -1
}

// shut ends the cropper on screen: what it asked for is forgotten and its
// preview file removed.
func (s *Screen) shut() {
	s.cur, s.stage, s.grab = 0, idle, grab{}
	s.mu.Lock()
	s.loadGen++ // a reader still running is not wanted
	s.loaded = nil
	s.mu.Unlock()
	for _, f := range []string{s.pv.file, s.pv.out} {
		if f != "" {
			s.submit(job{drop: f}, 0)
		}
	}
	s.pv = preview{}
	s.src = source{}
}

func (s *Screen) submit(j job, key uint64) task.Ticket {
	if s.pool == nil {
		s.pool = newPool()
	}
	t, _ := s.pool.Submit(j, key)
	return t
}

// poll takes the previews the worker finished.
func (s *Screen) poll(c *ui.Context) {
	if s.pool == nil {
		return
	}
	s.res = s.pool.Poll(s.res[:0])
	for _, r := range s.res {
		if r.Ticket != s.pv.ticket || !s.pv.pending {
			continue
		}
		s.pv.pending = false
		if r.Out.ok {
			s.pv.file, s.pv.turns = s.pv.out, s.pv.want
		} else {
			s.pv.failed = true
		}
		s.pv.out = ""
		c.RequestFrame()
	}
}

// isPicture says whether the staged file is one the cropper can open
// (upstream asks whether its type is an image).
func isPicture(path string) bool {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")) {
	case "png", "jpg", "jpeg", "jpe", "gif", "webp", "bmp", "tif", "tiff", "heic", "heif", "avif", "ico":
		return true
	}
	return false
}

// canTurn says whether a preview of the picture can be made here: the
// system's decoder reads it (img.Decode: on macOS whatever ImageIO reads, so
// HEIC and TIFF too; elsewhere PNG, JPEG and GIF).
func (s *Screen) canTurn() bool {
	if s.src.pasted() {
		return true
	}
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(s.src.path), ".")) {
	case "png", "jpg", "jpeg", "jpe", "gif":
		return true
	case "webp", "heic", "heif", "tif", "tiff", "bmp":
		return runtime.GOOS == "darwin"
	}
	return false
}

// needFile says whether the picture is shown from a preview made here.
func (s *Screen) needFile() bool {
	return s.src.pasted() || s.edit.Turns != 0
}

// ensurePreview asks for the preview of the turns on screen when there is not
// one yet.
func (s *Screen) ensurePreview() {
	if !s.needFile() || s.pv.failed {
		return
	}
	if (s.pv.file != "" && s.pv.turns == s.edit.Turns) || (s.pv.pending && s.pv.want == s.edit.Turns) {
		return
	}
	s.seq++
	out := filepath.Join(os.TempDir(), "zapfast-crop-"+strconv.Itoa(os.Getpid())+"-"+strconv.FormatUint(s.seq, 10)+".png")
	// The preview this one replaces is removed by a job of its own, so that a
	// render superseded in the queue cannot take the removal with it.
	for _, old := range []string{s.pv.file, s.pv.out} {
		if old != "" {
			s.submit(job{drop: old}, 0)
		}
	}
	t := s.submit(job{path: s.src.path, rgba: s.src.rgba, w: s.src.w, h: s.src.h, turns: int(s.edit.Turns), out: out}, 1)
	s.pv = preview{out: out, want: s.edit.Turns, ticket: t, pending: t != 0, failed: t == 0}
}

// keep sends the crop to the core and closes the cropper
// (zf:src/app.rs, Action::ApplyPictureEdit).
func (s *Screen) keep(e *screen.Env, a *state.Account) {
	cmd := a.Cmd(abi.CommandTagApplyPictureEdit)
	ap := &cmd.ApplyPictureEdit
	ap.Target, ap.Width, ap.Height, ap.Turns = s.target, s.edit.W, s.edit.H, s.edit.Turns
	ap.Crop = abi.PictureCrop{X: s.edit.Crop.X, Y: s.edit.Crop.Y, Width: s.edit.Crop.W, Height: s.edit.Crop.H}
	o := origin{acct: a.Index, target: s.target, src: s.src, edit: s.edit}
	var blob abi.BlobRef
	if s.src.pasted() {
		ref, err := a.Conn.PutBlob(s.src.rgba)
		if err != nil {
			e.S.Toast([]byte(err.Error()), true)
			return
		}
		blob = ref
		ap.Source.Tag, ap.Source.Pasted.RGBA = abi.PictureSourceTagPasted, ref
		// The pixels outlive the dialog that was lent them.
		o.src.rgba = append([]byte(nil), s.src.rgba...)
	} else {
		ap.Source.Tag, ap.Source.File.Path = abi.PictureSourceTagFile, []byte(s.src.path)
	}
	if err := a.Send(cmd); err != nil {
		if blob.ID != 0 {
			a.Conn.FreeBlob(blob.ID)
		}
		e.S.Toast([]byte(err.Error()), true)
		return
	}
	// Held against the attachment until the file lands, so a second edit
	// started meanwhile cannot take its origin.
	if i := s.find(s.applying, o.acct, o.target); i >= 0 {
		s.applying = append(s.applying[:i], s.applying[i+1:]...)
	}
	s.applying = append(s.applying, o)
	e.S.CloseDialog()
}

// ---- the editor ----

type rect struct{ x, y, w, h float32 }

// at makes the next box a floating one at r, in its parent's pixels.
func at(c *ui.Context, r rect) { theme.At(c, r.x, r.y, r.w, r.h) }

func (s *Screen) build(e *screen.Env, a *state.Account) {
	k, c := e.K, e.C()
	pw, ph := c.Size()
	W, H := float32(pw), float32(ph)
	m, barH := e.Pt(margin), e.Pt(bar)

	s.ensurePreview()

	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisY)
	c.NextRole(ui.RoleDialog)
	c.NextSemName(i18n.T("Crop picture"))
	okey := ui.HashString(c.ParentKey(), "cropper")
	c.BeginOverlay(okey, ui.OverlayModal, [4]float32{0, 0, W, H}, ui.PlaceCenter)
	defer c.EndOverlay()

	// The window takes the theme's own colour, so the controls on it read in a
	// light theme as in a dark one.
	at(c, rect{0, 0, W, H})
	c.NextBG(k.Theme().Colors[theme.Window])
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.HashString(c.ParentKey(), "window"), nil)

	inner := rect{m, m + barH, W - 2*m, H - 2*m - 2*barH}
	dw, dh := s.edit.DisplayedSize()
	scale := min(inner.w/float32(dw), inner.h/float32(dh))
	pic := rect{w: theme.Snap(float32(dw) * scale), h: theme.Snap(float32(dh) * scale)}
	pic.x, pic.y = theme.Snap(inner.x+(inner.w-pic.w)/2), theme.Snap(inner.y+(inner.h-pic.h)/2)
	scale = pic.w / float32(dw)

	s.drag(c, scale)
	s.picture(e, pic)
	crop := s.onScreen(pic, scale)
	s.shade(k, c, pic, crop)
	s.frame(e, crop)

	// The rows of controls. Keep sits left of the close button, as upstream's
	// right-to-left row has it.
	e.Begin(screen.Rect{X: m, Y: m, W: W - 2*m, H: barH}, "top")
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "row", ui.Pct(1), ui.Pct(1), 0)
	k.NextGap(8)
	filler(k)
	kept := rexui.IconButton(k.Kit, s.keepIc, i18n.T("Keep this crop"), rexui.IconButtonOpts{Variant: rexui.IconPrimary}).Clicked
	left := rexui.IconButton(k.Kit, s.closeIc, i18n.T("Cancel (Esc)"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked
	k.EndContainer()
	e.End()

	e.Begin(screen.Rect{X: m, Y: H - m - barH, W: W - 2*m, H: barH}, "bottom")
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "row", ui.Pct(1), ui.Pct(1), 0)
	k.NextGap(8)
	filler(k)
	k.BeginDisabled(!s.canTurn())
	var turn int
	if rexui.IconButton(k.Kit, s.leftIc, i18n.T("Turn left"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		turn = -1
	}
	if rexui.IconButton(k.Kit, s.rightIc, i18n.T("Turn right"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		turn = 1
	}
	k.EndDisabled()
	reset := rexui.Button(k.Kit, i18n.T("Reset"), rexui.ButtonOpts{}).Clicked
	k.EndContainer()
	e.End()

	switch {
	case turn != 0:
		s.edit = s.edit.Turned(turn > 0)
	case reset:
		s.edit = s.edit.Reset()
	}

	// Enter keeps and Escape leaves, unless a control took the key.
	if c.OverlayTopmost(okey) {
		if c.TakeKey(ui.KeyEscape, 0) {
			left = true
		} else if c.TakeKey(ui.KeyEnter, 0) {
			kept = true
		}
	}
	switch {
	case kept:
		s.keep(e, a)
	case left:
		e.S.CloseDialog()
	}
}

// filler is a container that takes the room the controls leave.
func filler(k *widget.Kit) {
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "fill", widget.Fill, ui.Pct(1), 0)
	k.EndContainer()
}

// onScreen is the crop's place in the window, given where the picture is
// drawn: the crop is kept in the picture's pixels.
func (s *Screen) onScreen(pic rect, scale float32) rect {
	cr := s.edit.Crop
	return rect{
		pic.x + float32(cr.X)*scale, pic.y + float32(cr.Y)*scale,
		float32(cr.W) * scale, float32(cr.H) * scale,
	}
}

// edgeKey is the key of the box that grips an edge, the same in every frame
// so the drag can read last frame's signal before the boxes are built.
func edgeKey(c *ui.Context, e Edge) ui.Key { return ui.KeyN(c.ParentKey(), 9200+uint64(e)) }

var edges = [...]Edge{Inside, TopLeft, Top, TopRight, Right, BottomRight, Bottom, BottomLeft, Left}

// drag applies the pointer to the crop: a press on an edge, a corner or the
// inside grabs it, and the drag is measured from where the crop was then.
func (s *Screen) drag(c *ui.Context, scale float32) {
	dw, dh := s.edit.DisplayedSize()
	for _, e := range edges {
		sg := c.PeekSignal(edgeKey(c, e))
		if sg.Pressed {
			s.grab = grab{true, e, s.edit.Crop}
		}
		if !s.grab.on || s.grab.edge != e {
			continue
		}
		if sg.Dragging || sg.Released {
			dx := int64(math.Round(float64(sg.DragDelta[0] / scale)))
			dy := int64(math.Round(float64(sg.DragDelta[1] / scale)))
			s.edit.Crop = s.grab.start.Drag(e, dx, dy, dw, dh)
		}
		if !sg.Pressed && !sg.Dragging {
			s.grab.on = false
		}
	}
}

// picture draws the picture, turned as the edit has it. A picture the
// system's decoder can show is drawn from its file; a turned or pasted one
// from the preview made here, a track-coloured box until that is ready.
func (s *Screen) picture(e *screen.Env, pic rect) {
	k, c := e.K, e.C()
	key := ui.KeyN(c.ParentKey(), 9100)
	at(c, pic)
	path := s.src.path
	if s.needFile() {
		path = ""
		if s.pv.file != "" && s.pv.turns == s.edit.Turns {
			path = s.pv.file
		}
	}
	if path != "" && s.imgs != nil {
		h, _ := s.imgs.Request(img.Key{Hash: img.HashString(path), W: 1600, H: 1600}, img.File(path))
		tex, uv, st := s.imgs.Use(h)
		if st == img.Ready {
			c.NextImageFit(tex, uv, ui.FitContain)
			c.NextTextColor(0xFFFFFFFF)
			c.BoxKeyed(ui.DrawImage|base.Floating, key, nil)
			return
		}
	}
	c.NextBG(k.Theme().Colors[theme.Fill3])
	c.NextRadius(e.Pt(4))
	c.BoxKeyed(ui.DrawBackground|base.Floating, key, nil)
}

// shade fades what the crop leaves out toward the window.
func (s *Screen) shade(k *widget.Kit, c *ui.Context, pic, crop rect) {
	col := theme.WithAlpha(k.Theme().Colors[theme.Window], 0.7)
	for i, r := range [...]rect{
		{pic.x, pic.y, pic.w, crop.y - pic.y},
		{pic.x, crop.y + crop.h, pic.w, pic.y + pic.h - crop.y - crop.h},
		{pic.x, crop.y, crop.x - pic.x, crop.h},
		{crop.x + crop.w, crop.y, pic.x + pic.w - crop.x - crop.w, crop.h},
	} {
		if r.w <= 0 || r.h <= 0 {
			continue
		}
		at(c, r)
		c.NextBG(col)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(c.ParentKey(), 9110+uint64(i)), nil)
	}
}

// frame marks the crop's border and handles in the accent, and builds the
// boxes that grip them.
func (s *Screen) frame(e *screen.Env, crop rect) {
	k, c := e.K, e.C()
	accent := k.Theme().Colors[theme.Accent]
	at(c, crop)
	c.NextBorder(accent, e.Pt(border))
	c.BoxKeyed(ui.DrawBorder|base.Floating, ui.KeyN(c.ParentKey(), 9120), nil)

	cx, cy := crop.x+crop.w/2, crop.y+crop.h/2
	r, b := crop.x+crop.w, crop.y+crop.h
	type spot struct {
		x, y   float32
		cursor ui.Cursor
		name   string
	}
	places := [...]spot{
		Inside:      {},
		TopLeft:     {crop.x, crop.y, ui.CursorResizeNWSE, i18n.T("Top left corner")},
		Top:         {cx, crop.y, ui.CursorResizeNS, i18n.T("Top edge")},
		TopRight:    {r, crop.y, ui.CursorResizeNESW, i18n.T("Top right corner")},
		Right:       {r, cy, ui.CursorResizeEW, i18n.T("Right edge")},
		BottomRight: {r, b, ui.CursorResizeNWSE, i18n.T("Bottom right corner")},
		Bottom:      {cx, b, ui.CursorResizeNS, i18n.T("Bottom edge")},
		BottomLeft:  {crop.x, b, ui.CursorResizeNESW, i18n.T("Bottom left corner")},
		Left:        {crop.x, cy, ui.CursorResizeEW, i18n.T("Left edge")},
	}
	// The region itself moves, whole.
	at(c, crop)
	if s.grab.on && s.grab.edge == Inside {
		c.NextCursor(ui.CursorGrabbing)
	} else {
		c.NextCursor(ui.CursorGrab)
	}
	c.NextRole(ui.RoleGroup)
	c.NextSemName(i18n.T("Crop"))
	c.BoxKeyed(ui.Clickable|ui.Hoverable|base.Floating, edgeKey(c, Inside), nil)

	gp, hp := e.Pt(grip), e.Pt(handle)
	off := theme.Snap((gp - hp) / 2)
	for _, edge := range edges[1:] {
		p := places[edge]
		at(c, rect{theme.Snap(p.x - gp/2), theme.Snap(p.y - gp/2), gp, gp})
		c.NextCursor(p.cursor)
		c.NextRole(ui.RoleGroup)
		c.NextSemName(p.name)
		id := c.BoxKeyed(ui.Clickable|ui.Hoverable|base.Floating, edgeKey(c, edge), nil)
		c.Push(id)
		at(c, rect{off, off, hp, hp})
		c.NextBG(accent)
		c.NextRadius(e.Pt(2))
		c.NextBorder(0xFFFFFFFF, 1)
		c.BoxKeyed(ui.DrawBackground|ui.DrawBorder|base.Floating, ui.HashString(c.ParentKey(), "dot"), nil)
		c.Pop()
	}
}

// ---- the core's answers ----

// listener is the Screen as a state.Listener.
type listener Screen

func (l *listener) Event(a *state.Account, ev *abi.Event) {
	s := (*Screen)(l)
	switch ev.Tag {
	case abi.EventTagPictureInspected:
		p := &ev.PictureInspected
		// Only the picture still being waited for may open the cropper: any
		// other was cancelled or overtaken while its size was being read, and
		// opening on it would take the window off whoever has it now.
		if s.stage != inspecting || p.Target != s.target || a.Index != s.acct || s.st.Dialog.Kind != state.DialogCropper {
			return
		}
		if p.Result.Tag == abi.SizeResultTagOk {
			s.edit, s.stage = NewEdit(p.Result.Ok.Width, p.Result.Ok.Height), editing
			return
		}
		s.fail(s.st, string(p.Result.Err.Error))
	case abi.EventTagPictureEdited:
		p := &ev.PictureEdited
		i := s.find(s.applying, a.Index, p.Target)
		var o origin
		if i >= 0 {
			o = s.applying[i]
			s.applying = append(s.applying[:i], s.applying[i+1:]...)
		}
		if p.Result.Tag == abi.PathResultTagOk {
			if i >= 0 {
				s.remember(o)
			}
			return
		}
		s.st.Toast(p.Result.Err.Error, true)
	}
}

// remember keeps the original of a picture that was cropped.
func (s *Screen) remember(o origin) {
	if i := s.find(s.origins, o.acct, o.target); i >= 0 {
		s.origins = append(s.origins[:i], s.origins[i+1:]...)
	}
	if len(s.origins) >= maxOrigins {
		s.origins = append(s.origins[:0], s.origins[1:]...)
	}
	s.origins = append(s.origins, o)
}
