package calls

import (
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/i18n"
	"zapfast/ui/screen"
)

// Content is the Calls page's content pane while no chat is open beside the
// history: WhatsApp's start of a call, which opens the new-call list
// (zf:src/ui/calls.rs:642, CA-023). It is the shell's CallsContent slot.
type Content struct {
	h  *hub
	ic icon.ID
}

var _ screen.Pane = (*Content)(nil)

// NewContent returns the Calls page's content pane.
func NewContent() *Content { return &Content{} }

// Init readies the pane for the main window.
func (p *Content) Init(e *screen.Env) error {
	p.h = hubOf(e.S)
	p.ic = e.K.AddIcon(icon.Spec{Symbol: "phone.badge.plus", Fallback: '+', Pt: 28})
	return nil
}

// Build builds the pane at r.
func (p *Content) Build(e *screen.Env, r screen.Rect) {
	e.Begin(r, "Start call")
	defer e.End()
	a := e.Acct()
	if a == nil || p.h == nil {
		return
	}
	k := e.K
	pad := float32(24)
	k.NextPadding(pad, 0, pad, 0)
	k.BeginContainer(ui.AxisY, ui.CrossCenter, "start call", ui.Px(r.W), ui.Px(r.H), 0)
	k.Space(max(r.H/k.Scale()/2-110, 24)) // grid:ok 110 is the block's half-height offset for centring, not a gap
	label := i18n.T("Start call")
	if circleButton(e, p.ic, label, 72, stopStartCall) {
		p.h.acct(a).newCall = true
		p.h.gen++
		e.S.Touch()
	}
	k.Space(8)
	rexui.Label(k.Kit, label, rexui.LabelOpts{Style: rexui.TextTitle3})
	k.Space(16)
	k.NextWidth(ui.Px(min(e.Pt(360), r.W-e.Pt(2*pad))))
	k.NextAlign(ui.AlignCenter)
	rexui.Label(k.Kit, i18n.T("Call one person by voice or video, or choose a call in the list to see it."),
		rexui.LabelOpts{Color: rexui.Of(theme.Label2), Wrap: true})
	k.EndContainer()
}

// stopStartCall is Start call's place in the Tab order: after the page's
// own. Upstream leaves it to the pointer; a pane that registers no stop
// leaves the whole window's Tab to build order, so it takes the last place.
const stopStartCall = screen.StopManageLabels + 100

// circleButton is a round button of sizePt points in the accent, the icon
// ic in its middle (upstream's circle_button; Rex's round controls stop at
// the 36 pt capsule). It reports a click.
func circleButton(e *screen.Env, ic icon.ID, name string, sizePt float32, stop screen.TabStop) bool {
	k := e.K.Kit
	c, r := k.Context(), k.Theme()
	rc := &rexui.ButtonRecipes[rexui.ButtonPrimary]
	p := k.PressState(name, rc.Hover)
	fill, fg, _ := rc.Colors(r, p.Visual)
	side := theme.Snap(e.Pt(sizePt))
	c.NextBG(fill)
	c.NextRadius(side / 2)
	k.BeginPress(&p, ui.RoleButton, ui.Px(side), ui.Px(side), ui.DrawBackground, nil)
	if f, disp := k.Icons().Prepare(c, ic, fg); f != 0 {
		c.NextSize(ui.Pct(1), ui.Pct(1))
		c.BoxKeyed(f, ui.KeyN(p.Key, 1), disp)
	}
	k.FocusRing(p.ID, p.Visual, side/2)
	s := k.EndPress(&p)
	e.TabStop(stop, s.ID)
	return s.Clicked
}
