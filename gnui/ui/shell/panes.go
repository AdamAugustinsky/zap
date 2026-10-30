package shell

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
	"gonativeui/widget/workspace"

	"zapfast/format"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// The main window is Rex's (gonativeui D17): the field shows round the panes,
// Inset from the window's edges and Gutter between them, under the titlebar
// band. Each pane is a glass surface with its title row at its top: opaque
// while it is the one in use, translucent over the field otherwise. Where
// the panes lie and which is in use is a workspace layout, list | (chat |
// side); the rail and the titlebar band are not panes.

// accountChipPt is the width of the titlebar's account block: our picture,
// name and number, as Rex's host block.
const accountChipPt = 200

// trafficPt is the band's room for the window's buttons, before the account
// block.
const trafficPt = 80

// The main window's panes. Their slots in the layout (Slot) are also their
// surfaces' cache slots (theme.Surface.Slot).
const (
	paneList workspace.PaneID = 1 + iota // the chat list, or the Calls page's
	paneChat                             // the conversation, or what the page shows
	paneSide                             // the side pane (info, search, starred…)
	// the split view's other panes follow (splitID): not in the layout,
	// they share the chat pane's room
	numPanes
)

var paneFocusKey = ui.HashString(0, "zapfast/pane focus")

// newPanes is the main window's layout: the list, at its width from the
// settings, before the conversation, and the side pane, closed, after it.
// It returns the layout and the list's divider.
func newPanes() (workspace.Layout, uint32) {
	// The side pane covering a narrow window covers the chat without taking
	// its place: the chat stays in use and the covering pane translucent, as
	// before the workspace.
	l := workspace.Layout{Key: ui.HashString(0, "zapfast/pane"), SoloKeepsFocus: true}
	l.Reset(paneList)
	div := l.Split(paneList, paneChat, workspace.Right, 0.7)
	l.Split(paneChat, paneSide, workspace.Right, 0.3)
	l.SetLimits(paneList, workspace.Limits{Min: [2]float32{minListPt}, Max: [2]float32{maxListPt}})
	l.SetHidden(paneSide, true)
	return l, div
}

// syncPanes tells the layout this frame's state: the list's width and
// whether it is hidden, the side pane and its width, the open chat, and
// which pane a narrow window shows alone. Every call is idempotent.
func (sh *Shell) syncPanes(ref model.ChatRef, open, narrow bool) {
	s, e, l := sh.S, &sh.env, &sh.panes
	l.SetFixed(paneList, s.Settings.SidebarWidth)
	l.SetHidden(paneList, s.Panes.ListHidden)
	side := s.Panes.Pane != state.PaneNone && open
	l.SetHidden(paneSide, !side)
	// The side pane shows only with room for its least beside the
	// conversation's 320 points (it yields); without one the conversation
	// takes what the list leaves.
	chat := workspace.Limits{}
	if side {
		chat.Min[0] = 320
		lim := workspace.Limits{Min: [2]float32{sideMinPt}, Max: [2]float32{sideMaxPt}, Yield: true}
		if s.Panes.Pane == state.PaneSearch {
			lim.Min[0], lim.Max[0] = searchMinPt, searchMaxPt // zf:src/ui/pane.rs:22-23
		}
		l.SetLimits(paneSide, lim)
		w := s.Settings.InfoPaneWidth
		if slot := sh.sideSlot(s.Panes.Pane); slot != nil {
			w = slot.Width(e) / e.K.Scale()
		}
		// A width asked below the least (an old or hand-edited file) opens
		// at the least, as upstream clamps it (zf:src/ui/pane.rs:108-109),
		// rather than never; the room left for the chat still decides.
		l.SetFixed(paneSide, max(w, lim.Min[0]))
	}
	l.SetLimits(paneChat, chat)
	// A chat that opens is the pane in use, whichever pane a narrow window
	// showed alone (the focus cannot move to a pane the solo one leaves out,
	// so the solo pane is let go first and set again below, keeping the
	// focus); with none open the conversation (but for the Calls page's
	// content) is empty and passes the focus by.
	if open && ref != sh.focusChat {
		l.SetSolo(0)
		l.SetEmpty(paneChat, false)
		l.SetFocus(paneChat)
	}
	sh.focusChat = ref
	l.SetEmpty(paneChat, !open && (s.Page != state.PageCalls || sh.Set.CallsContent == nil))
	solo := workspace.PaneID(0)
	switch {
	case narrow && side:
		solo = paneSide // covers the window (Panes.Covering)
	case narrow && (open || s.Panes.ListHidden):
		solo = paneChat
	case narrow:
		solo = paneList
	}
	if l.Solo() != solo {
		l.SetSolo(solo)
	}
}

// keepFocus is Zap's fallback when the pane in use stops showing: the open
// chat, else the list, rather than the pane used before it (so a side pane
// in use that closes gives the focus to the chat, even when the list was
// used in between).
func (sh *Shell) keepFocus(was workspace.PaneID) {
	l := &sh.panes
	if l.Focus() == was || l.Shown(was) {
		return
	}
	if !l.SetFocus(paneChat) {
		l.SetFocus(paneList)
	}
}

// headerH is a pane's title row, pixels.
func (sh *Shell) headerH() float32 {
	k := sh.env.K
	return theme.Snap(k.Theme().Surface(theme.SurfacePane, k.Inactive()).Header)
}

// body is the part of pane rect r under its title row.
func (sh *Shell) body(r [4]float32) screen.Rect {
	hh := sh.headerH()
	return screen.Rect{X: r[0], Y: r[1] + hh, W: r[2] - r[0], H: max(r[3]-r[1]-hh, 0)}
}

// paneRects are where the panes are drawn this frame, by PaneID; a pane
// with no room has none (shown).
type paneRects [numPanes][4]float32

func shown(r [4]float32) bool { return r[2] > r[0] && r[3] > r[1] }

// splitFocus follows the split view (split): the open chat's pane is the
// one in use in the chat pane's place, and a press in a pane whose chat is
// not in use puts it in use, before anything builds, so that the press
// acts on its own chat.
func (sh *Shell) splitFocus(a *state.Account, split bool) {
	sh.liveSplit = 0
	if !split {
		return
	}
	sp := &sh.split
	live := paneChat
	for i, r := range sp.chats {
		if r == a.Open {
			live = splitID(i)
		}
	}
	if in := sh.env.C().Input(); in.Pressed[ui.ButtonLeft] {
		for i, r := range sp.rects {
			if shown(r) && in.MouseX >= r[0] && in.MouseX < r[2] && in.MouseY >= r[1] && in.MouseY < r[3] && sp.chats[i] != 0 {
				live = splitID(i)
				if sp.chats[i] != a.Open {
					a.UsePane(sp.chats[i])
					sh.focusChat = a.Open
				}
			}
		}
	}
	if live != paneChat {
		sh.liveSplit = live
	}
}

// surface builds pane p's glass at r, its focus animating toward whether it
// is the pane in use. The pane's content builds after it, over it, in window
// coordinates (the slots' Rects).
func (sh *Shell) surface(p workspace.PaneID, r [4]float32) {
	k := sh.env.K
	c, th := k.Context(), k.Theme()
	target := float32(0)
	use := sh.panes.Focus()
	if use == paneChat && sh.liveSplit != 0 {
		use = sh.liveSplit // the split's pane in use stands for the chat pane
	}
	if p == use && !k.Inactive() {
		target = 1
	}
	slot := sh.panes.Slot(p)
	if slot < 0 {
		slot = workspace.MaxPanes + int(p) // the split's, after the layout's
	}
	s := theme.Surface{
		Key: sh.panes.PaneKey(p), Slot: slot, Rect: r,
		Focus: c.AnimateValue(ui.KeyN(paneFocusKey, uint64(p)), target, th.Motion.Dim), Opacity: 1, Top: true, // DurShort, at once under Reduce Motion
	}
	th.Chrome.SurfaceShadow(c, th, s)
	kg := th.Chrome.BeginSurface(c, th, s)
	th.Chrome.EndSurface(c, th, s, kg)
}

// divider is the grab area of the list's divider, whose drag (Update)
// resizes the list within 260–520 points; the width is saved
// (Settings.SidebarWidth, zf:src/ui/chats.rs:18-36).
func (sh *Shell) divider() {
	e, l := &sh.env, &sh.panes
	c := e.C()
	for i := 0; i < l.NumDividers(); i++ {
		d := l.Divider(i)
		if d.ID != sh.listDiv {
			continue
		}
		x, w := (d.Gutter[0]+d.Gutter[2])/2, e.Pt(6)
		theme.At(c, x-w/2, d.Gutter[1], w, d.Gutter[3]-d.Gutter[1])
		c.NextCursor(ui.CursorResizeEW)
		c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask())
		c.NextRole(ui.RoleSplitter)
		c.NextSemName(i18n.T("Resize"))
		c.BoxKeyed(ui.Clickable|base.Floating, d.Key, nil)
	}
}

// header opens pane r's title row on tb, which lives on the shell: the slots
// take a pointer to it through an interface, which a local would escape by.
func (sh *Shell) header(tb *rexui.Toolbar, label, title string, r [4]float32) {
	*tb = rexui.BeginToolbar(sh.env.K.Kit, label, rexui.ToolbarOpts{
		Style: ToolbarStyle, Title: title, Header: [4]float32{r[0], r[1], r[2], r[1] + sh.headerH()},
	})
}

// titlebar builds the band over the panes: the window's buttons, then the
// account block (our picture, name and number, which opens the switcher).
func (sh *Shell) titlebar(a *state.Account) {
	s, e := sh.S, &sh.env
	sh.tb = rexui.BeginToolbar(e.K.Kit, "Toolbar", rexui.ToolbarOpts{Style: ToolbarStyle, Traffic: sh.Traffic})
	tb := &sh.tb
	tb.BeginCustom(i18n.T("Accounts")+"###account", ui.Px(e.Pt(accountChipPt)))
	key := sh.accountChip(a, tb.Height())
	tb.EndCustom()
	// The list's toggle (⌘B) keeps one place, beside the account, whether
	// the list is shown or hidden, so it is where the list went: a narrow
	// window has no list to hide (its rail and Back lead to the list).
	if sh.sidebarW() > 0 {
		tip := i18n.T("Hide the chat list (⌘B)")
		if s.Panes.ListHidden {
			tip = i18n.T("Show the chat list (⌘B)")
		}
		it := tb.Item(sh.ic.sidebar, tip)
		e.TabStop(screen.StopSidebar, it.ID)
		if it.Clicked {
			s.Panes.ListHidden = !s.Panes.ListHidden
			s.Touch()
		}
	}
	tb.Flexible()
	tb.End()
	if s.AccountMenu {
		sh.accounts(sh.anchorOf(key, screen.Rect{X: e.Pt(trafficPt), Y: e.Pt(9), W: e.Pt(accountChipPt), H: e.Pt(28)}))
	}
}

// sidebarW is the room the list's toggle takes in the titlebar after the
// account block, pixels: none in a narrow window or on a page without the
// chat list (Calls).
func (sh *Shell) sidebarW() float32 {
	s, e := sh.S, &sh.env
	if e.Narrow() || s.Page == state.PageCalls {
		return 0
	}
	return e.Pt(sidebarItemPt)
}

// sidebarItemPt is the toggle's toolbar item and the space after it.
const sidebarItemPt = 28 + 8

// accountChip is the titlebar's account block, h high: one button, our
// picture with the dot of unread chats elsewhere, our name over our number.
func (sh *Shell) accountChip(a *state.Account, h float32) ui.Key {
	s, e, k := sh.S, &sh.env, sh.env.K
	c := k.Context()
	p := k.PressState("Accounts", base.HoverOn)
	p.Name = i18n.T("Switch account") // zf:src/ui/accounts.rs:33
	k.BeginPress(&p, ui.RoleButton, ui.Pct(1), ui.Px(h), 0, nil)
	name := a.Model.Me.Name()
	phone := model.Phone(a.Model.Me.ID())
	if len(name) == 0 {
		name = phone
	}
	size := e.Pt(26)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(8))
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Push(c.Box(0, "chip"))
	c.NextSize(ui.Px(size), ui.Px(size))
	c.Push(c.Box(0, "picture"))
	rexui.Avatar(k.Kit, model.S(name), rexui.AvatarOpts{Size: 26, Image: sh.myPicture(a, int32(size)), Backdrop: rexui.Of(theme.Field)})
	sh.elsewhereDot(size)
	c.Pop()
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Pct(1), ui.Children(0))
	c.Push(c.Box(0, "names"))
	k.NextWidth(ui.Pct(1))
	rexui.LabelBytes(k.Kit, name, rexui.LabelOpts{Style: rexui.TextHeadline})
	if len(phone) > 0 && len(a.Model.Me.Name()) > 0 {
		k.NextWidth(ui.Pct(1))
		sh.phone = format.AppendPhone(sh.phone[:0], model.S(phone))
		// Rex's host block sets the number in its metadata's face
		rexui.LabelBytes(k.Kit, sh.phone, rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2), MetaPt: ztheme.T.MetaPt})
	}
	c.Pop()
	c.Pop()
	sig := k.EndPress(&p)
	e.TabStop(screen.StopProfile, sig.ID)
	if sig.Clicked {
		s.AccountMenu = !s.AccountMenu
		s.Touch()
	}
	return p.Key
}

// elsewhereDot marks a picture size pixels wide when another account has
// unread chats, ringed in the field's colour so it reads on any picture.
func (sh *Shell) elsewhereDot(size float32) {
	if sh.unreadElsewhere() == 0 {
		return
	}
	e := &sh.env
	c, r := e.C(), e.K.Theme()
	ring, dot := e.Pt(12), e.Pt(9)
	theme.At(c, size-e.Pt(3)-ring/2, e.Pt(3)-ring/2, ring, ring)
	c.NextBG(r.Colors[theme.Field])
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
