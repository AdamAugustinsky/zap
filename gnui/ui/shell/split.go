package shell

import (
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// The split view (state/split.go), as Zen's split tabs but of two chats:
// both panes are the whole conversation and composer, the one in use the
// open chat; a press in the other puts it in use. They share the chat
// pane's room while each has minSplitPt.
//
//   - A tab dragged down onto the conversation splits there, as in Zen: the
//     chat shown makes room for it at once, giving the side the pointer is
//     on to a pane that waits for the tab, and the drop puts it there. The
//     chat shown is the one open before the tab's press, or, for the open
//     chat's own tab, the chat open before it (the last one the person was
//     on). Dropped on a pane of a split, the tab takes that pane.
//   - A divider between the panes resizes them; under the pointer it shows
//     its bar, and a double click halves them again.
//   - Each pane has a dock at its top, under the pointer: a grip that drags
//     the pane onto the other to trade their places, and Expand, which
//     gives the window to that pane's chat.
//   - ⌃⌥V splits the open chat with its neighbour tab, ⌃⌥U closes the split.

// minSplitPt is the narrowest a pane of the split gets.
const minSplitPt = 300

// splitUI is the split view's state between frames.
type splitUI struct {
	drag     float32          // the share as a drag of the divider goes
	held     model.ChatRef    // the tab held over the conversation, 0 for none
	from     model.ChatRef    // the chat shown beside it while it is held
	side     int              // the side it would land on: 0 first, 1 second, -1 none
	pane     int              // the pane a grip drags (0, 1), -1 for none
	icons    [2]icon.ID       // the dock's grip and Expand
	dockKeys [2]ui.Key        // each pane's dock
	placeKey ui.Key           // the pane that waits for a tab
	rects    [2][4]float32    // the panes last frame, window pixels
	chats    [2]model.ChatRef // their chats
}

func (sp *splitUI) init(k *screen.Env) {
	sp.icons[0] = k.K.AddIcon(ztheme.SmallIcon("line.3.horizontal", '≡'))
	sp.icons[1] = k.K.AddIcon(ztheme.SmallIcon("arrow.up.left.and.arrow.down.right", '⤢'))
	sp.dockKeys[0] = ui.HashString(0, "zapfast/split dock 0")
	sp.dockKeys[1] = ui.HashString(0, "zapfast/split dock 1")
	sp.placeKey = ui.HashString(0, "zapfast/split waiting pane")
	sp.side, sp.pane = -1, -1
}

// splitLayout divides the chat pane of panes between a's split chats, or,
// while a tab is held over it, between the chat shown and the pane that
// waits for the tab. It reports whether there are two panes.
func (sh *Shell) splitLayout(a *state.Account, panes *paneRects, gut float32) bool {
	e := &sh.env
	c := e.C()
	r := e.K.Theme()
	sp := &sh.split
	cp := panes[paneChat]
	if !shown(cp) {
		return false
	}
	minW := e.Pt(minSplitPt)
	w := cp[2] - cp[0] - gut
	if w < 2*minW {
		return false
	}
	_, live := a.SplitChat()
	// a held tab's preview: the chat shown gives the side the pointer is on
	// a share that eases toward half
	want := float32(0)
	if !live && sp.side >= 0 {
		want = 1
	}
	t := want
	if sh.Slides {
		t = c.AnimateValue(ui.HashString(0, "zapfast/split preview"), want, ztheme.Toward(r, ztheme.T.DurShort, want > 0))
	}
	share := sh.S.Share()
	switch {
	case live:
	case t > 0.001:
		share = 1 - 0.5*t // the side the tab lands on grows from nothing
		if sp.side == 0 {
			share = 0.5 * t
		}
		if t < 1 {
			c.RequestFrame()
		}
	default:
		return false
	}
	mid := theme.Snap(cp[0] + w*share)
	if live {
		mid = theme.Snap(cp[0] + min(max(w*share, minW), w-minW))
	}
	panes[paneChat][2] = mid
	panes[paneSplit] = [4]float32{mid + gut, cp[1], cp[2], cp[3]}
	if !live && sp.side == 0 {
		// the waiting pane is the first: the chat shown is the second
		panes[paneSplit], panes[paneChat] = [4]float32{cp[0], cp[1], mid, cp[3]}, [4]float32{mid + gut, cp[1], cp[2], cp[3]}
		if w*share < 1 {
			panes[paneSplit] = [4]float32{}
		}
	} else if cp[2]-(mid+gut) < 1 {
		panes[paneSplit] = [4]float32{}
	}
	return live
}

// splitPanes builds the split view's two panes in their places, each with
// its chat's state (the one not in use traded in while it builds), the
// divider between them and their docks.
func (sh *Shell) splitPanes(a *state.Account, panes *paneRects, gut, tbH, bottom float32) {
	sp := &sh.split
	first, second := a.Panes()
	rects := [2][4]float32{panes[paneChat], panes[paneSplit]}
	chats := [2]model.ChatRef{first, second}
	// each pane names its chat: the tabs choose one only
	tabbed := sh.env.Tabbed()
	sh.env.SetTabbed(false)
	defer sh.env.SetTabbed(tabbed)
	for i := range 2 {
		live := chats[i] == a.Open
		if !live {
			a.SideBegin()
		}
		sh.chatIn(a, rects[i], true, i)
		if !live {
			a.SideEnd()
		}
	}
	sp.rects, sp.chats = rects, chats
	sh.splitHandle(rects[0][2]+gut/2, tbH, bottom-tbH, rects[0][0], rects[1][2], gut)
	for i := range 2 {
		sh.dock(a, i, rects, chats)
	}
}

// waiting builds the pane that waits for a held tab at r: its glass, and
// the chat it would show.
func (sh *Shell) waiting(a *state.Account, r [4]float32) {
	sp := &sh.split
	e := &sh.env
	k := e.K
	c, th := k.Context(), k.Theme()
	ch, ok := a.Model.Chats.Get(sp.held)
	if !ok {
		return
	}
	in := e.Pt(ztheme.Space2)
	theme.At(c, r[0], r[1], r[2]-r[0], r[3]-r[1])
	c.NextBG(theme.WithAlpha(th.Colors[theme.Accent], 0.08))
	c.NextBorder(theme.WithAlpha(th.Colors[theme.Accent], 0.5), th.Lines.Hairline*2)
	c.NextRadius(e.Pt(ztheme.Pane()))
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextGap(in)
	c.NextRole(ui.RoleGroup)
	c.NextSemName(i18n.T("Drop to split"))
	c.Push(c.BoxKeyed(base.Floating|ui.DrawBackground|ui.DrawBorder|ui.Clip, sp.placeKey, nil))
	name := ch.Name()
	if len(name) == 0 {
		name = a.Model.DisplayName(ch.ID())
	}
	side := e.Pt(48)
	rexui.Avatar(k.Kit, model.S(name), rexui.AvatarOpts{Size: 48, Image: screen.Avatar(sh.imgs, a, ch.ID(), false, int32(side))})
	rexui.Label(k.Kit, model.S(name), rexui.LabelOpts{Style: rexui.TextHeadline})
	rexui.Label(k.Kit, i18n.T("Drop to split"), rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
	c.Pop()
}

// splitHandle is the divider between the panes at x: a drag moves it, a
// double click halves the panes again, and under the pointer (or while it
// is dragged) it shows its bar. x0 and x1 are the split's outer edges.
func (sh *Shell) splitHandle(x, y, h, x0, x1, gut float32) {
	s, e := sh.S, &sh.env
	c, th := e.C(), e.K.Theme()
	w := e.Pt(8)
	theme.At(c, x-w/2, y, w, h)
	c.NextCursor(ui.CursorResizeEW)
	c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask())
	c.NextRole(ui.RoleSplitter)
	c.NextSemName(i18n.T("Resize the split"))
	id := c.BoxKeyed(ui.Clickable|ui.Hoverable|base.Floating, ui.HashString(0, "zapfast/split-edge"), nil)
	sig := c.Signal(id)
	total := x1 - x0 - gut
	switch {
	case sig.DoubleClicked:
		s.SplitShare = 0
		s.Touch()
	case sig.Dragging && sig.DragDelta[0] != 0 && total > 0:
		if sh.split.drag == 0 {
			sh.split.drag = s.Share()
		}
		sh.split.drag += sig.DragDelta[0] / total
		s.SplitShare = sh.split.drag
		s.Touch()
	case !sig.Dragging:
		sh.split.drag = 0
	}
	on := float32(0)
	if sig.Hovering || sig.Dragging {
		on = 1
	}
	t := c.AnimateValue(ui.HashString(0, "zapfast/split bar"), on, th.Motion.Dim)
	if t < 0.01 {
		return
	}
	// the bar: a short pill on the divider
	bw, bh := e.Pt(4), e.Pt(40)
	theme.At(c, x-bw/2, y+(h-bh)/2, bw, bh)
	c.NextBG(theme.WithAlpha(th.Colors[theme.Label], 0.45*t))
	c.NextRadius(bw / 2)
	c.Box(base.Floating|ui.DrawBackground, "zapfast/split bar")
}

// dock is pane i's dock at the top of its pane: a grip that drags the pane
// onto the other (their places trade on the drop) and Expand. It shows
// while the pointer is over the pane, or its grip is held.
func (sh *Shell) dock(a *state.Account, i int, rects [2][4]float32, chats [2]model.ChatRef) {
	sp := &sh.split
	e := &sh.env
	k := e.K
	c, th := k.Context(), k.Theme()
	in := c.Input()
	r := rects[i]
	over := in.MouseX >= r[0] && in.MouseX < r[2] && in.MouseY >= r[1] && in.MouseY < r[3]
	on := float32(0)
	if over || sp.pane == i {
		on = 1
	}
	t := c.AnimateValue(ui.KeyN(sp.dockKeys[i], 1), on, th.Motion.Dim)
	if t < 0.01 {
		return
	}
	hit := e.Pt(ztheme.T.HitTool)
	pad := e.Pt(ztheme.Space1)
	w, h := 2*hit+2*pad, hit+2*pad
	x := theme.Snap((r[0]+r[2])/2 - w/2)
	y := theme.Snap(r[1] + (sh.headerH()-h)/2)
	theme.At(c, x, y, w, h)
	c.NextOpacity(t)
	c.NextBG(th.Colors[theme.Raised] | 0xFF)
	c.NextBorder(th.Colors[theme.Separator], th.Lines.Hairline)
	c.NextRadius(h / 2)
	c.NextShadow(ui.Shadow{OffY: e.Pt(2), Sigma: e.Pt(6), Color: 0x00000030})
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextPadding(pad, pad, pad, pad)
	c.NextRole(ui.RoleToolbar)
	c.NextSemName(i18n.T("Split pane"))
	c.Push(c.BoxKeyed(base.Floating|ui.DrawBackground|ui.DrawBorder, sp.dockKeys[i], nil))
	// the grip: held and dragged onto the other pane, the panes trade places
	ps := k.PressState(c.Fmt().Str("grip ").Int(i).String(), base.HoverOn)
	ps.Name = i18n.T("Drag to move the pane")
	c.NextCursor(ui.CursorGrab)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	k.BeginPress(&ps, ui.RoleButton, ui.Px(hit), ui.Px(hit), 0, nil)
	k.Icon(sp.icons[0], th.Colors[theme.Label2])
	sig := k.EndPress(&ps)
	rexui.Tooltip(k.Kit, sig.ID, i18n.T("Drag to move the pane"))
	hitTool := ui.Px(hit)
	k.NextSize(hitTool, hitTool)
	if rexui.IconButton(k.Kit, sp.icons[1], i18n.T("Fill the window with this chat"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		a.Expand(chats[i])
	}
	c.Pop()
	switch {
	case c.Signal(sig.ID).Dragging:
		sp.pane = i
		o := rects[1-i]
		if in.MouseX >= o[0] && in.MouseX < o[2] && in.MouseY >= o[1] && in.MouseY < o[3] {
			sh.dropZone(o, i18n.T("Drop to move here"))
		}
	case sp.pane == i:
		sp.pane = -1
		o := rects[1-i]
		if in.MouseX >= o[0] && in.MouseX < o[2] && in.MouseY >= o[1] && in.MouseY < o[3] {
			a.SwapSplit()
		}
	}
}

// dropZone marks pane r as where a drag would land.
func (sh *Shell) dropZone(r [4]float32, label string) {
	e := &sh.env
	k := e.K
	c, th := k.Context(), k.Theme()
	in := e.Pt(ztheme.Space2)
	theme.At(c, r[0]+in, r[1]+in, r[2]-r[0]-2*in, r[3]-r[1]-2*in)
	c.NextBG(theme.WithAlpha(th.Colors[theme.Accent], 0.12))
	c.NextBorder(theme.WithAlpha(th.Colors[theme.Accent], 0.6), th.Lines.Hairline*2)
	c.NextRadius(e.Pt(ztheme.Pane()))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextRole(ui.RoleGroup)
	c.NextSemName(label)
	c.Push(c.Box(base.Floating|ui.DrawBackground|ui.DrawBorder, "zapfast/split drop zone"))
	rexui.Label(k.Kit, label, rexui.LabelOpts{Style: rexui.TextHeadline, Color: rexui.Of(theme.Accent)})
	c.Pop()
	c.RequestFrame()
}

// splitWithTab is ⌃⌥V: the open chat beside its neighbour tab (the one
// after it, else the one before), or the split closed when there is one.
func (sh *Shell) splitWithTab(a *state.Account) {
	if a.Split != 0 {
		a.CloseSplit()
		return
	}
	tabs := a.ChatTabs()
	at := -1
	for i, ref := range tabs {
		if ref == a.Open {
			at = i
		}
	}
	if at < 0 || len(tabs) < 2 {
		sh.S.Toast([]byte(i18n.T("Open another chat to split with")), false)
		return
	}
	next := at + 1
	if next >= len(tabs) {
		next = at - 1
	}
	a.OpenSplit(tabs[next])
}

// tabDrop follows a tab held and dragged down from the titlebar onto the
// conversation (chat: the chat pane's whole room last frame). It runs
// before the tabs' Update, while the tab is still held. prev is the chat
// open before the tab's press.
func (sh *Shell) tabDrop(a *state.Account, chat [4]float32, held, prev model.ChatRef) {
	sp := &sh.split
	e := &sh.env
	in := e.C().Input()
	over := held != 0 && shown(chat) && in.MouseY >= chat[1]+e.Pt(ztheme.Space6) &&
		in.MouseX >= chat[0] && in.MouseX < chat[2] && in.MouseY < chat[3]
	if held == 0 {
		if sp.held != 0 && sp.side < 0 && a.Open == sp.from && !in.Released[ui.ButtonLeft] {
			a.OpenChat(sp.held) // a drag that left the conversation: the tab is the one chosen
		}
		sp.held, sp.from, sp.side = 0, 0, -1
		return
	}
	if !over {
		sp.side = -1
		return
	}
	if _, ok := a.SplitChat(); ok {
		// a split: the tab takes the pane under the pointer
		sp.side = -1
		for i, r := range sp.rects {
			if in.MouseX >= r[0] && in.MouseX < r[2] && sp.chats[i] != held && !sh.inSplit(a, held) {
				sh.dropZone(r, i18n.T("Drop to show it here"))
				if in.Released[ui.ButtonLeft] {
					sh.replacePane(a, i, held)
				}
			}
		}
		return
	}
	if sp.held != held {
		// the chat shown beside it: the one open before the press, or,
		// for the open chat's own tab, the one before it
		from := prev
		if from == 0 || from == held {
			from = a.PrevOpen()
		}
		if from == 0 || from == held {
			return
		}
		sp.held, sp.from = held, from
		if a.Open != from {
			a.OpenChat(from) // the press opened the tab: the chat it goes beside shows
		}
	}
	sp.side = 1
	if in.MouseX < (chat[0]+chat[2])/2 {
		sp.side = 0
	}
	e.C().RequestFrame()
	if in.Released[ui.ButtonLeft] {
		a.SplitWith(held, sp.side == 0)
		sp.held, sp.from, sp.side = 0, 0, -1
	}
}

// inSplit reports whether ref is one of the split view's chats.
func (sh *Shell) inSplit(a *state.Account, ref model.ChatRef) bool {
	return ref == a.Open || ref == a.Split
}

// replacePane shows ref in pane i of the split, in use.
func (sh *Shell) replacePane(a *state.Account, i int, ref model.ChatRef) {
	other := sh.split.chats[1-i]
	if other != a.Open {
		a.UseSplit()
	}
	a.CloseSplit()
	a.OpenChat(other)
	a.SplitWith(ref, i == 0)
}
