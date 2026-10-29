package calls

import (
	"time"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/ui/screen"
)

// barPt is the call bar's height, in points.
const barPt = 36

// Bar is the call the reader put aside (CA-007): a strip across the foot of
// the main window on every page, with who is on the call, how long it has
// been up, the way back and Hang up (zf:src/ui/call.rs:99). It is the
// shell's CallBar slot.
type Bar struct {
	h  *hub
	ic struct{ voice, video, hangup icon.ID }

	buf, buf2 []byte
}

var _ screen.Band = (*Bar)(nil)

// NewBar returns the call bar.
func NewBar() *Bar { return &Bar{} }

// Init readies the bar for the main window.
func (b *Bar) Init(e *screen.Env) error {
	b.h = hubOf(e.S)
	add := func(sym string, fb rune, pt float32) icon.ID {
		return e.K.AddIcon(icon.Spec{Symbol: sym, Fallback: fb, Pt: pt})
	}
	b.ic.voice, b.ic.video, b.ic.hangup = add("phone.fill", '☎', 15), add("video.fill", '▶', 15), add("phone.down.fill", '✕', 15)
	return nil
}

// BandHeight is the bar's height: none unless a call is put aside. The shell
// asks every frame of the main window, on every page, so the call's upkeep
// runs here too while the call window is closed.
func (b *Bar) BandHeight(e *screen.Env, w float32) float32 {
	if b.h == nil {
		return 0
	}
	b.h.tick(e.S.Now())
	if !b.h.call.on || !b.h.call.hidden {
		return 0
	}
	return e.Pt(barPt)
}

// BuildBand builds the bar at r.
func (b *Bar) BuildBand(e *screen.Env, r screen.Rect) {
	e.Begin(r, "Call bar")
	defer e.End()
	h := b.h
	if h == nil || !h.call.on {
		return
	}
	k, c := e.K, e.C()
	th := k.Theme()
	now := e.S.Now()
	b.buf = h.appendName(b.buf[:0])
	b.buf2 = h.appendStatus(b.buf2[:0], now)
	// Rex's quiet tint of the accent, as the open chat's row, rather than
	// upstream's band of solid green
	c.NextBG(theme.WithAlpha(th.Colors[theme.Accent], theme.Lerp(0.14, 0.24, th.T)))
	c.NextRole(ui.RoleGroup)
	c.NextSemNameBytes(b.buf)
	k.NextPadding(16, 0, 12, 0)
	k.NextGap(8)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "call bar", ui.Px(r.W), ui.Px(r.H), ui.DrawBackground)
	ic := b.ic.voice
	if h.call.u.Video {
		ic = b.ic.video
	}
	k.Kit.Icon(ic, th.Colors[theme.Accent])
	k.NextGap(8)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "who", ui.Pct(1), ui.Children(1), 0)
	text(e, b.buf, rexui.TextSemibold, rexui.Of(theme.Label), false)
	text(e, b.buf2, rexui.TextBody, rexui.Of(theme.Label2), false)
	k.EndContainer()
	if live(h.call.u.Phase) {
		if rexui.Button(k.Kit, i18n.T("Return to the call"), rexui.ButtonOpts{Variant: rexui.ButtonPrimary, Size: theme.SizeSmall}).Clicked {
			h.returnTo()
		}
		s := rexui.IconButton(k.Kit, b.ic.hangup, i18n.T("Hang up"), rexui.IconButtonOpts{Variant: rexui.IconDestructive, Size: theme.SizeSmall})
		rexui.Tooltip(k.Kit, s.ID, i18n.T("Hang up"))
		if s.Clicked {
			h.hangup()
		}
	}
	k.EndContainer()
	wakeEachSecond(e, h, now)
}

// wakeEachSecond asks for a frame when the call's length next changes, at
// the farewell's end, and when a call being placed gives up.
func wakeEachSecond(e *screen.Env, h *hub, now time.Time) {
	c := e.C()
	in := c.Input().Time
	if in == 0 {
		return
	}
	if !h.call.until.IsZero() {
		c.Deadline(ui.Key(0xCA11_0001), in+int64(h.call.until.Sub(now))+int64(time.Millisecond))
	}
	if h.call.u.Phase == abi.CallPhaseActive && h.call.hasStarted {
		left := time.Second - now.Sub(h.call.started)%time.Second
		c.Deadline(ui.Key(0xCA11_0002), in+int64(left)+int64(time.Millisecond))
	}
	if !h.call.placed.IsZero() {
		c.Deadline(ui.Key(0xCA11_0003), in+int64(h.call.placed.Add(placingTimeout).Sub(now)))
	}
}
