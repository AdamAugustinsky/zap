package shell

import (
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
	"gonativeui/widget/workspace"

	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// The split view (state/split.go), as Zen's split tabs: several chats side
// by side, as many as the window has room for at minSplitPt each (a wider
// screen holds more, up to one per pane's screens: Set.MorePanes). Every
// pane is the whole conversation and composer, the one in use the open
// chat; a press in another puts it in use. The panes slide to where they
// go rather than jump:
//
//   - A tab dragged down onto the conversation makes room for itself: the
//     panes shown move aside, and a pane waits for the tab between them,
//     where the pointer is; the drop puts it there, in use. With no split,
//     the chat shown is the one open before the tab's press, or, for the
//     open chat's own tab, the chat open before it. A split with no room
//     left lets the tab take the pane it is dropped on.
//   - The dividers between the panes resize them freely; under the pointer
//     each shows its bar, and a double click evens the two beside it.
//   - Each pane has a dock at its top, under the pointer: a grip that drags
//     the pane along the others (they slide aside to where it would go),
//     "Take out of the split", and Expand, which gives the window to it.
//   - ⌃⌥V adds the next tab to the split (or closes it when there is no room
//     or no tab left), ⌃⌥U closes the split.

// minSplitPt is the room each pane needs for the split to show, and
// minPanePt the narrowest a divider makes one.
const (
	minSplitPt = 300
	minPanePt  = 240
)

// splitUI is the split view's state between frames.
type splitUI struct {
	held  model.ChatRef // the tab held over the conversation, 0 for none
	from  model.ChatRef // the chat shown beside it while it is held (no split)
	ins   int           // where it would go among the panes; -1 for nowhere
	grip  model.ChatRef // the pane whose grip is held, 0 for none
	to    int           // where it would go
	drag  float32       // the share a divider's drag began at
	dragI int           // the divider being dragged, -1 for none
	icons [3]icon.ID    // the dock's grip, take out and Expand

	// the frame's panes, left to right: their chats (0: the pane waiting
	// for a held tab), where they are drawn, where they go, and the slot of
	// screens each chat builds in
	chats  []model.ChatRef
	rects  [][4]float32
	goal   [][4]float32
	slotOf map[model.ChatRef]int
	base   [][4]float32 // the panes' rects with nothing held, for the drops
	max    int          // how many panes the window has room for
	even   []float32    // shares, reused
	moved  []float32
}

func (sp *splitUI) init(e *screen.Env) {
	sp.icons[0] = e.K.AddIcon(ztheme.SmallIcon("line.3.horizontal", '≡'))
	sp.icons[1] = e.K.AddIcon(ztheme.SmallIcon("minus", '–'))
	sp.icons[2] = e.K.AddIcon(ztheme.SmallIcon("arrow.up.left.and.arrow.down.right", '⤢'))
	sp.ins, sp.to, sp.dragI = -1, -1, -1
	sp.slotOf = map[model.ChatRef]int{}
}

// splitID is pane i's surface: the chat pane's for the first.
func splitID(i int) workspace.PaneID {
	if i == 0 {
		return paneChat
	}
	return numPanes + workspace.PaneID(i)
}

// splitLayout lays out the split view in the chat pane of panes: its
// chats, or, while a tab or a pane's grip is held over it, where they go
// then. It reports whether there is more than one pane to build; the
// first's rect becomes the chat pane's.
func (sh *Shell) splitLayout(a *state.Account, panes *paneRects, gut float32) bool {
	e := &sh.env
	c := e.C()
	r := e.K.Theme()
	sp := &sh.split
	cp := panes[paneChat]
	if !shown(cp) {
		return false
	}
	room := cp[2] - cp[0]
	sp.max = min(int((room+gut)/(e.Pt(minSplitPt)+gut)), 1+len(sh.Set.MorePanes))
	a.SetMaxPanes(max(sp.max, 2))
	// the chats shown, left to right
	sp.chats = sp.chats[:0]
	if a.SplitOn() {
		sp.chats = append(sp.chats, a.Panes()...)
	} else {
		sp.chats = append(sp.chats, a.Open)
	}
	if len(sp.chats) > sp.max {
		// more than fit: the ones round the pane in use
		at := 0
		for i, ref := range sp.chats {
			if ref == a.Open {
				at = i
			}
		}
		first := min(max(at-sp.max/2, 0), len(sp.chats)-sp.max)
		sp.chats = append(sp.chats[:0], sp.chats[first:first+max(sp.max, 1)]...)
	}
	n := len(sp.chats)
	shares := sh.S.Shares(max(len(a.Panes()), 1))
	if len(shares) != n {
		shares = evenShares(sp.even[:0], n)
	}
	sp.base = layoutPanes(sp.base[:0], cp, gut, shares)
	// a held tab waits between them; a held grip moves its pane along them
	switch {
	case sp.held != 0 && sp.ins >= 0 && n < sp.max:
		at := min(sp.ins, n)
		sp.chats = append(sp.chats, 0)
		copy(sp.chats[at+1:], sp.chats[at:])
		sp.chats[at] = 0
		n++
		shares = evenShares(sp.even[:0], n)
	case sp.grip != 0 && sp.to >= 0 && a.SplitOn():
		from := -1
		for i, ref := range sp.chats {
			if ref == sp.grip {
				from = i
			}
		}
		if from >= 0 && sp.to < n && from != sp.to {
			ref := sp.chats[from]
			moved := append(sp.moved[:0], shares...)
			v := moved[from]
			if from < sp.to {
				copy(sp.chats[from:sp.to], sp.chats[from+1:sp.to+1])
				copy(moved[from:sp.to], moved[from+1:sp.to+1])
			} else {
				copy(sp.chats[sp.to+1:from+1], sp.chats[sp.to:from])
				copy(moved[sp.to+1:from+1], moved[sp.to:from])
			}
			sp.chats[sp.to], moved[sp.to] = ref, v
			shares = moved
		}
	}
	if n < 2 {
		return false
	}
	sp.goal = layoutPanes(sp.goal[:0], cp, gut, shares)
	// each pane eases to where it goes (at once under Reduce Motion): the
	// others slide aside for a pane that comes or moves
	sp.rects = append(sp.rects[:0], sp.goal...)
	moving := false
	for i, ref := range sp.chats {
		g := sp.goal[i]
		key := ui.KeyN(ui.HashString(0, "zapfast/split pane"), uint64(ref))
		if ref == 0 {
			// the waiting pane grows from nothing where it goes
			t := c.AnimateValueFrom(ui.HashString(0, "zapfast/split waiting"), 0, 1, ztheme.Enter(r, ztheme.T.DurShort))
			mid := (g[0] + g[2]) / 2
			sp.rects[i][0], sp.rects[i][2] = mid-(mid-g[0])*t, mid+(g[2]-mid)*t
			moving = moving || t < 1
			continue
		}
		if sh.Slides {
			x0 := c.AnimateValue(ui.KeyN(key, 1), g[0], ztheme.Toward(r, ztheme.T.DurShort, true))
			x1 := c.AnimateValue(ui.KeyN(key, 2), g[2], ztheme.Toward(r, ztheme.T.DurShort, true))
			sp.rects[i][0], sp.rects[i][2] = theme.Snap(x0), theme.Snap(x1)
			moving = moving || x0 != g[0] || x1 != g[2]
		}
	}
	if moving {
		c.RequestFrame()
	}
	panes[paneChat] = sp.rects[0]
	return true
}

// layoutPanes appends to dst the panes' rects in room cp, gut apart, each
// its share of the width (none under minPanePt where they fit).
func layoutPanes(dst [][4]float32, cp [4]float32, gut float32, shares []float32) [][4]float32 {
	n := len(shares)
	w := cp[2] - cp[0] - gut*float32(n-1)
	x := cp[0]
	for i, s := range shares {
		pw := theme.Snap(w * s)
		if i == n-1 {
			pw = cp[2] - x
		}
		dst = append(dst, [4]float32{x, cp[1], x + pw, cp[3]})
		x += pw + gut
	}
	return dst
}

func evenShares(dst []float32, n int) []float32 {
	for range n {
		dst = append(dst, 1/float32(n))
	}
	return dst
}

// slot is the screens chat ref builds in: the same while it stays in the
// split, so that its transcript and composer keep their state as the panes
// move; the first pane's screens go to the pane in use when they are free.
func (sh *Shell) slot(ref model.ChatRef) int {
	sp := &sh.split
	if i, ok := sp.slotOf[ref]; ok {
		return i
	}
	used := [8]bool{}
	for r, i := range sp.slotOf {
		keep := false
		for _, c := range sp.chats {
			keep = keep || c == r
		}
		if !keep {
			delete(sp.slotOf, r)
			continue
		}
		if i < len(used) {
			used[i] = true
		}
	}
	for i := range 1 + len(sh.Set.MorePanes) {
		if !used[i] {
			sp.slotOf[ref] = i
			return i
		}
	}
	return 0
}

// splitPanes builds the split view's panes where splitLayout put them, each
// with its chat's state (the ones not in use traded in while they build),
// the waiting pane, the dividers between them and their docks.
func (sh *Shell) splitPanes(a *state.Account, gut, tbH, bottom float32) {
	sp := &sh.split
	// each pane names its chat: the tabs choose the group only
	tabbed := sh.env.Tabbed()
	sh.env.SetTabbed(false)
	defer sh.env.SetTabbed(tabbed)
	for i, ref := range sp.chats {
		r := sp.rects[i]
		if !shown(r) {
			continue
		}
		if i > 0 {
			sh.surface(splitID(i), r)
		}
		if ref == 0 {
			sh.waiting(a, r)
			continue
		}
		live := ref == a.Open
		if !live {
			a.SideBegin(ref)
		}
		sh.chatIn(a, r, true, sh.slot(ref))
		if !live {
			a.SideEnd(ref)
		}
	}
	if sp.held != 0 || sp.grip != 0 {
		return // what is held decides; no divider nor dock meanwhile
	}
	n := len(sp.chats)
	for i := 0; i+1 < n; i++ {
		sh.splitHandle(i, n, (sp.rects[i][2]+sp.rects[i+1][0])/2, tbH, bottom-tbH)
	}
	for i := range n {
		sh.dock(a, i)
	}
}

// waiting builds the pane that waits for a held tab at r: the chat it
// would show.
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
	c.Push(c.Box(base.Floating|ui.DrawBackground|ui.DrawBorder|ui.Clip, "zapfast/split waiting pane"))
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

// splitHandle is divider i of n, between panes i and i+1, at x: a drag
// moves it (where the pointer goes, from where it began), a double click
// evens the two panes, and under the pointer (or while dragged) it shows
// its bar.
func (sh *Shell) splitHandle(i, n int, x, y, h float32) {
	s, e := sh.S, &sh.env
	c, th := e.C(), e.K.Theme()
	sp := &sh.split
	w := e.Pt(8)
	theme.At(c, x-w/2, y, w, h)
	c.NextCursor(ui.CursorResizeEW)
	c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask())
	c.NextRole(ui.RoleSplitter)
	c.NextSemName(i18n.T("Resize the split"))
	key := ui.KeyN(ui.HashString(0, "zapfast/split-edge"), uint64(i))
	sig := c.Signal(c.BoxKeyed(ui.Clickable|ui.Hoverable|base.Floating, key, nil))
	l, r := sp.base[i], sp.base[i+1]
	pair := r[2] - l[0] - (r[0] - l[2])
	shares := s.Shares(n)
	switch {
	case sig.DoubleClicked:
		s.SetShare(n, i, (shares[i]+shares[i+1])/2)
		s.Touch()
	case sig.Dragging && pair > 0:
		if sp.dragI != i {
			sp.dragI, sp.drag = i, shares[i]
		}
		// DragDelta runs from the press: the share the drag began at, and
		// as far again as the pointer has gone; it stays where it stops
		total := shares[i] + shares[i+1]
		v := sp.drag + sig.DragDelta[0]/pair*total
		least := e.Pt(minPanePt) / pair * total
		v = min(max(v, least), total-least)
		if v != shares[i] {
			s.SetShare(n, i, v)
			s.Touch()
		}
	case !sig.Dragging && sp.dragI == i:
		sp.dragI = -1
	}
	on := float32(0)
	if sig.Hovering || sig.Dragging {
		on = 1
	}
	t := c.AnimateValue(ui.KeyN(key, 1), on, th.Motion.Dim)
	if t < 0.01 {
		return
	}
	bw, bh := e.Pt(4), e.Pt(40)
	theme.At(c, x-bw/2, y+(h-bh)/2, bw, bh)
	c.NextBG(theme.WithAlpha(th.Colors[theme.Label], 0.45*t))
	c.NextRadius(bw / 2)
	c.BoxKeyed(base.Floating|ui.DrawBackground, ui.KeyN(key, 2), nil)
}

// dock is pane i's dock at the top of its pane, under the pointer: the
// grip that drags it along the other panes, "Take out of the split" and
// Expand.
func (sh *Shell) dock(a *state.Account, i int) {
	sp := &sh.split
	e := &sh.env
	k := e.K
	c, th := k.Context(), k.Theme()
	in := c.Input()
	r, ref := sp.rects[i], sp.chats[i]
	if ref == 0 {
		return
	}
	over := in.MouseX >= r[0] && in.MouseX < r[2] && in.MouseY >= r[1] && in.MouseY < r[3]
	key := ui.KeyN(ui.HashString(0, "zapfast/split dock"), uint64(ref))
	on := float32(0)
	if over {
		on = 1
	}
	t := c.AnimateValue(ui.KeyN(key, 1), on, th.Motion.Dim)
	if t < 0.01 {
		return
	}
	hit := e.Pt(ztheme.T.HitTool)
	pad := e.Pt(ztheme.Space1)
	w, h := 3*hit+2*pad, hit+2*pad
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
	c.Push(c.BoxKeyed(base.Floating|ui.DrawBackground|ui.DrawBorder, key, nil))
	// the grip: held, the pane goes along the others to where it is dropped
	ps := k.PressState(c.Fmt().Str("grip ").Int(int(ref)).String(), base.HoverOn)
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
	if rexui.IconButton(k.Kit, sp.icons[1], i18n.T("Take out of the split"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		a.ClosePane(ref)
	}
	k.NextSize(hitTool, hitTool)
	if rexui.IconButton(k.Kit, sp.icons[2], i18n.T("Fill the window with this chat"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		a.Expand(ref)
	}
	c.Pop()
	if c.Signal(sig.ID).Dragging {
		sp.grip, sp.to = ref, i
	}
}

// followGrip follows a pane's grip held from the frame before: where the
// pane would go among the others (by the pointer), and on the release its
// move there. It runs before the layout.
func (sh *Shell) followGrip(a *state.Account) {
	sp := &sh.split
	if sp.grip == 0 {
		return
	}
	c := sh.env.C()
	in := c.Input()
	from := -1
	for i, ref := range a.Panes() {
		if ref == sp.grip {
			from = i
		}
	}
	if from < 0 {
		sp.grip, sp.to = 0, -1
		return
	}
	sp.to = from
	for i, r := range sp.base {
		if in.MouseX >= r[0] && in.MouseX < r[2] {
			sp.to = i
		}
	}
	c.RequestFrame()
	if !in.Down[ui.ButtonLeft] {
		a.MovePane(from, sp.to)
		sp.grip, sp.to = 0, -1
	}
}

// splitWithTab is ⌃⌥V: the next tab not in the split goes beside the open
// chat while there is room; with none, or no room, the split closes.
func (sh *Shell) splitWithTab(a *state.Account) {
	tabs := a.ChatTabs()
	at := -1
	for i, ref := range tabs {
		if ref == a.Open {
			at = i
		}
	}
	room := !a.SplitOn() || len(a.Panes()) < a.MaxPanes()
	for d := 1; at >= 0 && room && d < len(tabs); d++ {
		ref := tabs[(at+d)%len(tabs)]
		if !a.InSplit(ref) && ref != a.Open {
			a.OpenSplit(ref)
			return
		}
	}
	if a.SplitOn() {
		a.CloseSplit()
		return
	}
	sh.S.Toast([]byte(i18n.T("Open another chat to split with")), false)
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
		if sp.held != 0 && sp.ins < 0 && a.Open == sp.from && sp.from != 0 && !in.Released[ui.ButtonLeft] {
			a.OpenChat(sp.held) // a drag that left the conversation: the tab is the one chosen
		}
		sp.held, sp.from, sp.ins = 0, 0, -1
		return
	}
	if a.InSplit(held) {
		return // a pane's own tab: its grip moves it
	}
	if !over {
		sp.ins = -1
		return
	}
	if !a.SplitOn() && sp.held != held {
		// the chat shown beside it: the one open before the press, or,
		// for the open chat's own tab, the one before it
		from := prev
		if from == 0 || from == held {
			from = a.PrevOpen()
		}
		if from == 0 || from == held {
			return
		}
		sp.from = from
		if a.Open != from {
			a.OpenChat(from) // the press opened the tab: the chat it goes beside shows
		}
	}
	sp.held = held
	e.C().RequestFrame()
	// where it goes: between the panes the pointer is between
	sp.ins = 0
	for _, r := range sp.base {
		if in.MouseX >= (r[0]+r[2])/2 {
			sp.ins++
		}
	}
	full := a.SplitOn() && len(a.Panes()) >= a.MaxPanes()
	if full {
		// no room: the tab takes the pane under the pointer
		target := -1
		for i, r := range sp.base {
			if in.MouseX >= r[0] && in.MouseX < r[2] {
				target = i
				sh.dropZone(r, i18n.T("Drop to show it here"))
			}
		}
		sp.ins = -1
		if in.Released[ui.ButtonLeft] && target >= 0 {
			sh.replacePane(a, target, held)
			sp.held, sp.from = 0, 0
		}
		return
	}
	if in.Released[ui.ButtonLeft] {
		a.InsertPane(held, sp.ins)
		sp.held, sp.from, sp.ins = 0, 0, -1
	}
}

// dropZone marks pane r as where a drop would land.
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

// replacePane shows ref in pane i of a full split, in use.
func (sh *Shell) replacePane(a *state.Account, i int, ref model.ChatRef) {
	p := a.Panes()
	if i < 0 || i >= len(p) {
		return
	}
	gone := p[i]
	a.UsePane(gone)
	a.ClosePane(gone)
	a.InsertPane(ref, i)
}
