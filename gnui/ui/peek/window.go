package peek

import (
	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/chatview"
	"zapfast/ui/screen"
)

// Window is the floating peek (state.WinPeek, State.FloatPeek): the same
// panel as the overlay's, in a window of its own over the desktop, so that a
// clicked notification, the Dock's menu or the global shortcut show a chat
// without bringing the main window forward. It floats on every Space on
// the system's glass, near the corner where notifications come in. Escape
// or the close button close it; Open closes it and opens the chat in the
// main window. The app going to the background closes it too (sys).
type Window struct{ v view }

// Floating window size, in points.
const (
	floatW = 400
	floatH = 520
	// floatMargin keeps it off the screen's edge and the menu bar.
	floatMargin = 12
)

// NewWindow is the floating peek's screen.
func NewWindow() *Window { return &Window{} }

// Init readies its icon.
func (w *Window) Init(e *screen.Env) error {
	w.v.icons(e)
	return nil
}

// Config is a floating panel on glass at the top right of the main screen.
func (w *Window) Config(*state.State) app.WindowConfig {
	return app.WindowConfig{
		Title: i18n.T("Peek"), Width: floatW, Height: floatH, Backdrop: platform.BackdropGlass,
		Panel: &platform.Panel{Floating: true, AllSpaces: true, Shadow: true, Radius: ztheme.WindowRadius},
		Place: placeFloat,
	}
}

// placeFloat puts the panel at the top right of the main screen, where the
// notification it may come from was.
func placeFloat(win *platform.Window) {
	scr := app.Screens(nil)
	if len(scr) == 0 {
		return
	}
	v := scr[0]
	win.SetFrameRect(v.VisibleX+v.VisibleW-floatW-floatMargin, v.VisibleY+floatMargin, floatW, floatH)
}

// Build builds the panel, or closes the window when the peek has gone.
func (w *Window) Build(e *screen.Env) {
	st := e.S
	a, ch, ok := w.v.start(st, st.FloatPeek)
	if !ok {
		st.CloseFloatPeek()
		return
	}
	c, k := e.C(), e.K
	r := k.Theme()
	r.SetBackdrop(e.A != nil && e.A.Win != nil && e.A.Win.Backdrop() != platform.BackdropNone)
	cw, chh := c.Size()
	W, H := float32(cw), float32(chh)
	c.NextAxis(ui.AxisY)
	c.NextRole(ui.RoleDialog)
	c.NextSemName(i18n.Format(i18n.T("Peek: {chat}"), "{chat}", model.S(chatview.Title(a, ch))))
	var fl ui.Flags
	if !r.Backdrop {
		// No glass (Reduce transparency): the panel paints its own shape.
		c.NextBG(r.Colors[theme.Raised] | 0xFF)
		c.NextRadius(e.Pt(ztheme.WindowRadius))
		fl = ui.DrawBackground
	}
	theme.At(c, 0, 0, W, H)
	c.Push(c.Box(fl, "float peek"))
	did := w.v.build(e, a, ch, W, true)
	c.Pop()
	if c.TakeKey(ui.KeyEscape, 0) {
		did = actClose
	}
	switch did {
	case actClose:
		st.CloseFloatPeek()
	case actOpen:
		st.CloseFloatPeek()
		openChat(st, a, ch)
		st.RaiseMain()
	case actSplit:
		st.CloseFloatPeek()
		splitChat(st, a, ch)
		st.RaiseMain()
	case actFloat:
		st.CloseFloatPeek()
		st.FloatChat(a, ch.Ref())
	}
}

// Closed forgets the peek once its window has gone (closed from the panel,
// by the app, or by the system).
func (w *Window) Closed(e *screen.Env) {
	w.v.reset()
	if e.S.FloatPeek.Open() {
		e.S.FloatPeek = state.Peek{}
		e.S.Touch()
	}
}
