package viewer

import (
	"math"

	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/i18n"
	"zapfast/ui/screen"
)

type zoomCmd uint8

const (
	zoomNone   zoomCmd = iota
	zoomIn             // Zoom in, +
	zoomOut            // Zoom out, -
	zoomFit            // Fit to the window, 0
	zoomActual         // Show at original size
)

// dragState is a press on the stage: dragging pans a zoomed picture; a click
// that did not move, on the empty field, closes the viewer.
type dragState struct {
	active, moved bool
	last          [2]float32 // the drag delta already applied
	delta         [2]float32 // this frame's share
}

// picture is the still on screen: its file, its pixel size and the image
// cache handle last drawn.
type picture struct {
	path    string
	hash    uint64
	nw, nh  float32
	natural bool // nw, nh are known
	probed  bool // the header probe has been started
	last    img.Handle
	failed  bool
}

// stage is the field under the toolbar: the picture, or the clip's poster,
// or an item that is not downloaded. It takes the pointer: the wheel, a
// pinch, a drag, a double click; a click on the empty field closes the
// viewer (as it does on the phone).
func (s *Screen) stage(e *screen.Env, r screen.Rect) {
	c := e.C()
	theme.At(c, r.X, r.Y, r.W, r.H)
	c.NextRole(ui.RoleGroup)
	c.NextSemName(i18n.T("Image"))
	// The open hand while the picture can be dragged, the closed one while it
	// is (image_preview.rs:579); what the last frame found.
	switch {
	case s.canPan && s.drag.active && s.drag.moved:
		c.NextCursor(ui.CursorGrabbing)
	case s.canPan:
		c.NextCursor(ui.CursorGrab)
	}
	s.canPan = false
	id := c.Box(base.Floating|ui.Clickable|ui.Hoverable|ui.Clip, "stage")
	c.Push(id)
	sig := c.Signal(id)
	in := c.Input()
	d := &s.drag
	d.delta = [2]float32{}
	switch {
	case sig.Pressed:
		*d = dragState{active: true}
	case sig.Dragging && d.active:
		d.delta = [2]float32{sig.DragDelta[0] - d.last[0], sig.DragDelta[1] - d.last[1]}
		d.last = sig.DragDelta
		if sig.DragDelta[0]*sig.DragDelta[0]+sig.DragDelta[1]*sig.DragDelta[1] > 16 {
			d.moved = true
		}
	}
	s.prevRect, s.mediaRect = s.mediaRect, [4]float32{}
	if s.r.path != "" && !s.showsClip() {
		s.pictureMenu(e, id, sig)
	}
	switch {
	case s.r.path == "":
		s.pending(e, r)
	case s.showsClip():
		s.videoStage(e, r, sig)
	default:
		s.still(e, r, sig)
	}
	c.Pop()
	if sig.Released {
		moved := d.moved
		*d = dragState{}
		x, y := in.MouseX, in.MouseY
		if sig.Clicked && !moved && !s.inMedia(x, y) {
			s.close(e)
		}
	}
}

// still draws the photo at its zoom and takes the pointer's zoom and pan.
func (s *Screen) still(e *screen.Env, r screen.Rect, sig ui.Signal) {
	c, k := e.C(), e.K
	p := &s.pic
	if p.path != s.r.path {
		*p = picture{path: s.r.path, hash: img.HashString(s.r.path)}
	}
	if !p.probed {
		p.probed = true
		s.startProbe(p.path)
	}
	if !p.natural {
		if w, h, done, ok := s.takeProbe(p.path); done && ok {
			p.nw, p.nh, p.natural = float32(w), float32(h), true
		} else if done {
			p.natural = false
		}
	}
	sw, sh := r.W, r.H
	longest := max(sw, sh)
	// What is asked of the cache: the stage's size while fitting, more while
	// zoomed (decodes happen on workers, at the size shown).
	var key img.Key
	if p.natural {
		scale := s.v.scale()
		if s.v.fit {
			scale = fitScaleOf(p.nw, p.nh, sw, sh)
		}
		px := int32(math.Ceil(float64(max(p.nw, p.nh) * scale)))
		b := bucket(px, int32(max(p.nw, p.nh)))
		key = img.Key{Hash: p.hash, W: b, H: b}
	} else if s.probeFailed(p.path) {
		key = img.Key{Hash: p.hash} // the whole picture: its size is what it decodes to
	} else {
		b := bucket(int32(longest), 1<<30)
		key = img.Key{Hash: p.hash, W: b, H: b}
	}
	h, st := s.cache.Request(key, img.File(p.path))
	if st == img.Failed {
		p.failed = true
		s.failure(e, r)
		return
	}
	if st == img.Ready {
		p.last = h
		if !p.natural && s.probeFailed(p.path) {
			if info, ok := s.cache.Info(h); ok {
				p.nw, p.nh, p.natural = float32(info.W), float32(info.H), true
			}
		}
	}
	shown := h
	if st != img.Ready {
		shown = p.last
		if shown == 0 || s.cache.State(shown) != img.Ready {
			shown = 0
		}
	}
	if !p.natural || shown == 0 {
		s.spinner(e, r)
		return
	}
	v := &s.v
	v.fitScale = fitScaleOf(p.nw, p.nh, sw, sh)
	mid := [2]float32{r.X + r.W/2, r.Y + r.H/2}
	in := c.Input()
	q := [2]float32{in.MouseX - mid[0], in.MouseY - mid[1]}
	// Header buttons and keys.
	switch s.cmd {
	case zoomIn:
		v.zoomAt(zoomStep, [2]float32{}, p.nw, p.nh, sw, sh)
	case zoomOut:
		v.zoomAt(1/zoomStep, [2]float32{}, p.nw, p.nh, sw, sh)
	case zoomFit:
		v.reset()
	case zoomActual:
		v.actualAt([2]float32{}, p.nw, p.nh, sw, sh)
	}
	s.cmd = zoomNone
	ptr := pointer{
		scroll: [2]float32{in.ScrollX, in.ScrollY}, precise: in.Precise, smooth: in.Mods&(ui.ModCtrl|ui.ModSuper) != 0,
		hover: sig.Hovering, magnify: sig.Magnify, at: [2]float32{sig.GestureAt[0] - mid[0], sig.GestureAt[1] - mid[1]},
		smart: sig.SmartMagnify, double: sig.DoubleClicked && s.inMedia(in.MouseX, in.MouseY), q: q, drag: s.drag.delta,
	}
	s.applyPointer(&ptr, p.nw, p.nh, sw, sh)
	v.clamp(p.nw, p.nh, sw, sh)

	scale := v.scale()
	dw, dh := theme.Snap(p.nw*scale), theme.Snap(p.nh*scale)
	s.canPan = !v.fit && (dw > sw+0.5 || dh > sh+0.5)
	x := theme.Snap(mid[0] + v.pan[0] - dw/2)
	y := theme.Snap(mid[1] + v.pan[1] - dh/2)
	s.mediaRect = [4]float32{x, y, x + dw, y + dh}
	c.NextPos(x-r.X, y-r.Y)
	k.NextSize(ui.Px(dw), ui.Px(dh))
	c.NextFlags(base.Floating)
	k.Image(1, shown, ui.FitContain, 0, i18n.T("Photo"))
}

// inMedia reports whether the point is on the picture (or poster) drawn the
// frame before.
func (s *Screen) inMedia(x, y float32) bool {
	m := s.prevRect
	return x >= m[0] && x < m[2] && y >= m[1] && y < m[3]
}

// spinner centres a spinner on the stage.
func (s *Screen) spinner(e *screen.Env, r screen.Rect) {
	c, k := e.C(), e.K
	side := e.Pt(48)
	place(e, (r.W-side)/2, (r.H-side)/2, side, side, "loading")
	rexui.Spinner(k.Kit, i18n.T("Loading"), rexui.SpinnerOpts{})
	c.Pop()
}

// failure says the picture cannot be shown, with "Open externally"
// (image_preview.rs:701).
func (s *Screen) failure(e *screen.Env, r screen.Rect) {
	c, k := e.C(), e.K
	w, h := min(r.W, e.Pt(420)), e.Pt(88)
	place(e, (r.W-w)/2, (r.H-h)/2, w, h, "failure")
	c.NextGap(e.Pt(10))
	rexui.Label(k.Kit, i18n.T("This image could not be displayed in Zap."), rexui.LabelOpts{Wrap: true})
	if rexui.Button(k.Kit, i18n.T("Open externally"), rexui.ButtonOpts{}).Clicked {
		s.openExternal(e)
	}
	c.Pop()
}

// pending is an item whose attachment is not here yet: its own thumbnail, or
// the kind's symbol, and the button that fetches it. Nothing stands in for
// another file.
func (s *Screen) pending(e *screen.Env, r screen.Rect) {
	c, k := e.C(), e.K
	th := k.Theme()
	disc := min(e.Pt(220), r.W-e.Pt(32), r.H-e.Pt(96))
	disc = max(disc, e.Pt(64))
	x, y := (r.W-disc)/2, (r.H-disc)/2-e.Pt(24)
	s.mediaRect = [4]float32{r.X + x, r.Y + y, r.X + x + disc, r.Y + y + disc}
	if i := s.r.thumb; i >= 0 && i < len(s.album) && s.album[i].thumb != nil {
		it := &s.album[i]
		h, _ := s.cache.Request(img.Key{Hash: it.thumbHash}, img.Bytes(it.thumb))
		c.NextPos(x, y)
		k.NextSize(ui.Px(disc), ui.Px(disc))
		c.NextFlags(base.Floating)
		k.Image(1, h, ui.FitContain, e.Pt(8), i18n.T("Photo"))
	} else {
		place(e, x, y, disc, disc, "kind")
		ic := s.ic.bigPhoto
		if s.isVideo() {
			ic = s.ic.bigVideo
		}
		k.Icon(ic, th.Colors[theme.Label3])
		c.Pop()
	}
	dl := s.dlOf()
	bw, bh := e.Pt(200), e.Pt(76)
	by := y + disc + e.Pt(8)
	if dl.downloading {
		side := e.Pt(32)
		place(e, (r.W-side)/2, y+(disc-side)/2, side, side, "downloading")
		rexui.Spinner(k.Kit, i18n.T("Downloading"), rexui.SpinnerOpts{})
		c.Pop()
		return
	}
	place(e, (r.W-bw)/2, by, bw, bh, "download")
	c.NextGap(e.Pt(8))
	if dl.err != "" {
		rexui.Label(k.Kit, dl.err, rexui.LabelOpts{Color: rexui.Of(theme.Red), Wrap: true})
	}
	if rexui.Button(k.Kit, i18n.T("Download"), rexui.ButtonOpts{}).Clicked {
		s.download()
	}
	c.Pop()
}

// pointer is what the pointer asked of the picture this frame, relative to
// the stage's centre.
type pointer struct {
	scroll  [2]float32 // the wheel or the trackpad's two fingers
	precise bool       // a trackpad: pixels, not notches
	smooth  bool       // ⌘ or Ctrl held: the scroll zooms
	hover   bool       // the pointer is over the stage
	magnify float32    // a pinch's change in scale (0.1 = 10 % bigger)
	at      [2]float32 // where the pinch is
	smart   bool       // the two-finger double tap
	double  bool       // a double click on the picture
	q       [2]float32 // the pointer
	drag    [2]float32 // the drag since the last frame
}

// applyPointer zooms and pans the picture as the pointer asks: a mouse wheel
// zooms a step a notch, a trackpad's two fingers pan, ⌘ or Ctrl with either
// zooms smoothly, a pinch zooms (EvMagnify), smart zoom and a double click
// switch between fitting and the original size, and a drag pans.
func (s *Screen) applyPointer(ptr *pointer, nw, nh, sw, sh float32) {
	v := &s.v
	if ptr.hover && (ptr.scroll[0] != 0 || ptr.scroll[1] != 0) {
		switch {
		case ptr.precise && !ptr.smooth:
			v.panBy(ptr.scroll, nw, nh, sw, sh)
		case ptr.precise:
			v.zoomAt(float32(math.Exp(float64(ptr.scroll[1])*0.01)), ptr.q, nw, nh, sw, sh)
		default:
			v.zoomAt(float32(math.Pow(zoomStep, float64(ptr.scroll[1]))), ptr.q, nw, nh, sw, sh)
		}
	}
	if ptr.magnify != 0 {
		v.zoomAt(1+ptr.magnify, ptr.at, nw, nh, sw, sh)
	}
	if ptr.smart {
		v.toggleAt(ptr.q, nw, nh, sw, sh)
	}
	if ptr.drag != ([2]float32{}) && !v.fit {
		v.panBy(ptr.drag, nw, nh, sw, sh)
	}
	if ptr.double {
		v.toggleAt(ptr.q, nw, nh, sw, sh)
	}
}
