package chatlist

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// compactCell is one avatar's height in the rail, in points
// (zf:src/ui/chats.rs:1735).
const compactCell = 52

// Compact builds the chats' avatars in the rail at r while the list is
// hidden (zf:src/ui/chats.rs compact_list): the chats the full list would
// show now, under the same chip, search and folder.
func (s *Screen) Compact(e *screen.Env, r screen.Rect) {
	a := e.Acct()
	e.Begin(r, "chatlist-compact")
	defer e.End()
	if a == nil {
		return
	}
	k := e.K
	c := k.Context()
	s.takeRequests(e, a)
	s.sync(e, a)
	e.S.Visible = s.refs
	s.px = int32(avatarPt * k.Scale())
	if s.cver != s.version {
		s.cver = s.version
		s.cids = s.cids[:0]
		for _, ref := range s.refs {
			s.cids = append(s.cids, itemID(kChat, uint32(ref)))
		}
		s.cst.Reset(s.cids)
	}
	if len(s.refs) == 0 {
		return
	}
	v := base.BeginVList(c, "compact", &s.cst, base.VListOpts{Estimate: compactCell * k.Scale(), Line: 40})
	for i := v.First; i < v.Last; i++ {
		id := v.Item(i)
		s.compactCell(e, a, model.ChatRef(itemN(id)))
	}
	base.EndVList(&v)
	if a.Open != 0 && a.Open != s.cOpen {
		s.cOpen = a.Open
		if _, ok := s.cst.IndexOf(itemID(kChat, uint32(a.Open))); ok {
			s.cst.ScrollToID(itemID(kChat, uint32(a.Open)), base.AlignNearest)
		}
	}
}

// compactCell is one avatar: a click opens the chat, hovering names it, an
// unread chat carries its badge.
func (s *Screen) compactCell(e *screen.Env, a *state.Account, ref model.ChatRef) {
	ch, ok := a.Model.Chats.Get(ref)
	if !ok {
		return
	}
	k := e.K
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	in := s.infoOf(ref)
	s.refresh(e, in, ch)
	s.avatarHandle(e, in, s.px)
	in.muted = ch.Muted(e.S.Now().Unix())
	selected := ref == a.Open
	p := k.PressState("cell", base.HoverOn)
	h := theme.Snap(compactCell * sc)
	var fill uint32
	var fl ui.Flags
	if p.Hot > 0.01 {
		fill = theme.WithAlpha(r.Colors[theme.Fill2], p.Hot)
		fl = ui.DrawBackground
	}
	c.NextBG(fill)
	c.NextRadius(theme.Snap(8 * sc))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	var st ui.SemState
	if selected {
		st = ui.SemSelected
	}
	c.NextSemState(st)
	c.NextSemValueBytes(in.announce)
	p.Name = in.title
	k.BeginPress(&p, ui.RoleListItem, ui.Pct(1), ui.Px(h), fl, nil)
	side := theme.Snap(avatarPt * sc)
	c.NextSize(ui.Px(side), ui.Px(side))
	c.Push(c.BoxKeyed(0, ui.KeyN(p.Key, 60), nil))
	rexui.Avatar(k.Kit, in.title, rexui.AvatarOpts{Size: avatarPt, Image: in.handle, Backdrop: rexui.Of(theme.Field)})
	if in.unread > 0 || in.marked {
		bo := rexui.BadgeOpts{Color: rexui.Of(theme.Accent)}
		if in.muted {
			bo.Color = rexui.Of(theme.Gray)
		}
		theme.At(c, side-theme.Snap(14*sc), -theme.Snap(2*sc), theme.Snap(20*sc), theme.Snap(16*sc))
		c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask())
		c.Push(c.BoxKeyed(base.Floating, ui.KeyN(p.Key, 61), nil))
		if in.unread > 0 {
			rexui.BadgeCountN(k.Kit, in.unread, bo)
		} else {
			bo.Variant, bo.Name = rexui.BadgeStatus, "Unread"
			rexui.Badge(k.Kit, "", bo)
		}
		c.Pop()
	}
	c.Pop()
	if selected {
		// a short accent bar stands in for the selected row's block
		theme.At(c, theme.Snap(sc), (h-theme.Snap(22*sc))/2, theme.Snap(3*sc), theme.Snap(22*sc))
		c.NextBG(r.Colors[theme.Accent])
		c.NextRadius(theme.Snap(1.5 * sc))
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(p.Key, 62), nil)
	}
	k.FocusRing(p.ID, p.Visual, theme.Snap(8*sc))
	sig := k.EndPress(&p)
	rexui.Tooltip(k.Kit, sig.ID, in.title)
	if sig.Clicked {
		a.OpenChat(ref)
	}
}
