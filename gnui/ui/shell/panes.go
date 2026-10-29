package shell

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

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
// while it is the one in use, translucent over the field otherwise.

// paneHeaderPt is a pane's title row, which holds the pane's toolbar items.
const paneHeaderPt = 40

// accountChipPt is the width of the titlebar's account block: our picture,
// name and number, as Rex's host block.
const accountChipPt = 200

// trafficPt is the band's room for the window's buttons, before the account
// block.
const trafficPt = 80

// paneID names the main window's panes, which are also their surfaces' cache
// slots (theme.Surface.Slot).
type paneID int8

const (
	paneList  paneID = iota // the chat list, or the Calls page's
	paneChat                // the conversation, or what the page shows
	paneSide                // the side pane (info, search, starred…)
	paneSplit               // the chat beside the open one (split.go)
	numPanes
)

var (
	paneKey      = ui.HashString(0, "zapfast/pane")
	paneFocusKey = ui.HashString(0, "zapfast/pane focus")
)

// paneRects is where each pane lies this frame, window pixels (x0, y0, x1,
// y1); a pane not shown is zero.
type paneRects [numPanes][4]float32

func shown(r [4]float32) bool { return r[2] > r[0] && r[3] > r[1] }

// headerH is a pane's title row, pixels.
func (sh *Shell) headerH() float32 { return theme.Snap(sh.env.Pt(paneHeaderPt)) }

// body is the part of pane rect r under its title row.
func (sh *Shell) body(r [4]float32) screen.Rect {
	hh := sh.headerH()
	return screen.Rect{X: r[0], Y: r[1] + hh, W: r[2] - r[0], H: max(r[3]-r[1]-hh, 0)}
}

// noteFocus follows which pane is in use: the one pressed in, and the
// conversation when a chat opens (Rex focuses a pane on a click). In the
// split view (split) a press in the pane of the chat not in use puts it in
// use, before anything builds, so that the press acts on its own chat.
func (sh *Shell) noteFocus(a *state.Account, panes *paneRects, split bool) {
	c := sh.env.C()
	ref, open := model.ChatRef(0), false
	if ch, ok := a.OpenChatView(); ok {
		ref, open = ch.Ref(), true
	}
	// the open chat's pane: the second, in a split where it is the second
	live := paneChat
	if first, _ := a.Panes(); split && first != a.Open {
		live = paneSplit
	}
	if open && ref != sh.focusChat {
		sh.focus = live
	}
	sh.focusChat = ref
	if in := c.Input(); in.Pressed[ui.ButtonLeft] {
		for p := range panes {
			r := panes[p]
			if shown(r) && in.MouseX >= r[0] && in.MouseX < r[2] && in.MouseY >= r[1] && in.MouseY < r[3] {
				sh.focus = paneID(p)
			}
		}
		if split && (sh.focus == paneChat || sh.focus == paneSplit) && sh.focus != live {
			a.UseSplit()
			sh.focusChat = a.Open
		}
	}
	if !shown(panes[sh.focus]) {
		sh.focus = paneList
		if shown(panes[paneChat]) && open {
			sh.focus = paneChat
		}
	}
}

// surface builds pane p's glass at r, its focus animating toward whether it
// is the pane in use. The pane's content builds after it, over it, in window
// coordinates (the slots' Rects).
func (sh *Shell) surface(p paneID, r [4]float32) {
	k := sh.env.K
	c, th := k.Context(), k.Theme()
	target := float32(0)
	if p == sh.focus && !k.Inactive() {
		target = 1
	}
	s := theme.Surface{
		Key: ui.KeyN(paneKey, uint64(p)), Slot: int(p), Rect: r,
		Focus: c.AnimateValue(ui.KeyN(paneFocusKey, uint64(p)), target, th.Motion.Dim), Opacity: 1, Top: true, // DurShort, at once under Reduce Motion
	}
	th.Chrome.SurfaceShadow(c, th, s)
	kg := th.Chrome.BeginSurface(c, th, s)
	th.Chrome.EndSurface(c, th, s, kg)
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
