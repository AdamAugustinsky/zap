package conversation

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/logic"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

var _ screen.SelectionBar = (*Screen)(nil)

// SelectionBarHeight is the selection bar's height: it takes the
// composer's place while Panes.Selecting.
func (s *Screen) SelectionBarHeight(e *screen.Env, w float32) float32 { return e.Pt(selBarPt) }

// BuildSelectionBar builds selection mode's bar at r, the composer's place
// (zf:src/ui/conversation.rs selection_bar).
func (s *Screen) BuildSelectionBar(e *screen.Env, r screen.Rect) {
	a := e.Acct()
	if a == nil {
		return
	}
	ch, ok := a.OpenChatView()
	if !ok {
		return
	}
	if s.h == nil {
		s.Init(e)
	}
	e.Begin(r, "selection")
	s.selectionBar(e, a, ch, a.Model.Conversation(ch.ID()), r.W, r.H)
	e.End()
}

// selectionBar is selection mode's bar: Cancel, the count, Copy, Star,
// Delete… and Forward…, in the composer's capsule (rexui.ComposerLook), as
// the recording bar is.
func (s *Screen) selectionBar(e *screen.Env, a *state.Account, ch model.Chat, v *model.Conversation, w, h float32) {
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	look := &rexui.ComposerLook
	pn := &e.S.Panes
	n := pn.Selection.Len()
	inX, inY := e.Pt(10), e.Pt(6)
	bh := max(h-2*inY, 0)
	theme.At(c, inX, inY, max(w-2*inX, 0), bh)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(10))
	c.NextPadding(e.Pt(6), 0, e.Pt(6), 0)
	c.NextBG(look.Fill.RGBA(r))
	c.NextBorder(look.Border.RGBA(r), e.Pt(look.BorderW))
	c.NextRadius(min(e.Pt(look.Radius), bh/2))
	c.NextRole(ui.RoleToolbar)
	c.NextSemName(i18n.T("Select messages"))
	c.Push(c.Box(base.Floating|ui.DrawBackground|ui.DrawBorder, "selection bar"))
	ic := &s.icons
	if rexui.IconButton(k, ic.close, i18n.T("Cancel selection"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		logic.CancelSelection(e.S)
	}
	count := i18n.T("1 selected")
	if n != 1 {
		count = fillInt(c.Fmt(), i18n.T("{} selected"), "{}", n).String()
	}
	rexui.Label(k, count, rexui.LabelOpts{Style: rexui.TextHeadline})
	c.NextSize(ui.Pct(1), ui.Px(1))
	c.Box(0, "flex")
	k.BeginDisabled(n == 0)
	if rexui.IconButton(k, ic.copy, i18n.T("Copy"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		s.copySelected(e, a, ch, v)
	}
	if rexui.IconButton(k, ic.star, i18n.T("Star"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		s.starSelected(a, ch, v, &pn.Selection)
	}
	del := rexui.Button(k, i18n.T("Delete…"), rexui.ButtonOpts{Variant: rexui.ButtonDestructive})
	if del.Clicked {
		s.delMenu, s.delAt = !s.delMenu, [4]float32{}
	}
	if rexui.Button(k, i18n.T("Forward…"), rexui.ButtonOpts{Variant: rexui.ButtonPrimary}).Clicked {
		logic.OpenForward(e.S, a, ch.Ref(), &pn.Selection)
	}
	k.EndDisabled()
	c.Pop()
	if n == 0 {
		s.delMenu = false
	}
	if s.delMenu {
		at, edge := c.RectOf(c.KeyOf(del.ID)), rexui.PopoverAbove
		if s.delAt != ([4]float32{}) {
			at, edge = s.delAt, rexui.PopoverBelow // opened on a selected message
		}
		p := rexui.BeginPopover(k, "delete menu", at, rexui.PopoverOpts{Width: 200, Edge: edge, Label: i18n.T("Delete…")})
		done := false
		if s.canRevoke(v, &pn.Selection) && item(k, ic.trash, i18n.T("Delete for everyone")) {
			d := state.Dialog{Kind: state.DialogConfirmDeleteSelection, Chat: ch.Ref(), ForEveryone: true}
			copyIDs(&d.IDs, &pn.Selection)
			e.S.OpenDialog(d)
			done = true
		}
		if item(k, ic.trash, i18n.T("Delete for me")) {
			d := state.Dialog{Kind: state.DialogConfirmDeleteSelection, Chat: ch.Ref()}
			copyIDs(&d.IDs, &pn.Selection)
			e.S.OpenDialog(d)
			done = true
		}
		rexui.EndPopover(k, &p)
		if done || p.Dismissed {
			s.delMenu, s.delAt = false, [4]float32{}
		}
	}
}

func copyIDs(dst, src *model.IDList) {
	dst.Reset()
	for i := 0; i < src.Len(); i++ {
		dst.Add(src.At(i))
	}
}

// canRevoke reports whether every selected message can be deleted for
// everyone: ours, not deleted, within the window.
func (s *Screen) canRevoke(v *model.Conversation, sel *model.IDList) bool {
	if v == nil || sel.Len() == 0 {
		return false
	}
	for i := 0; i < sel.Len(); i++ {
		j := v.Find(sel.At(i))
		if j < 0 {
			return false
		}
		m := v.At(j)
		if !m.FromMe() || m.Kind() == abi.ContentTagRevoked || s.unix-m.Time() > revokeWin {
			return false
		}
	}
	return true
}

// starSelected stars the selected messages, or unstars them when all are
// starred.
func (s *Screen) starSelected(a *state.Account, ch model.Chat, v *model.Conversation, sel *model.IDList) {
	all := true
	for i := 0; i < sel.Len(); i++ {
		if !a.Model.Starred(ch.ID(), sel.At(i)) {
			all = false
			break
		}
	}
	for i := 0; i < sel.Len(); i++ {
		cmd := a.Cmd(abi.CommandTagSetStar)
		cmd.SetStar.Chat, cmd.SetStar.Message, cmd.SetStar.Starred = ch.ID(), sel.At(i), !all
		a.Send(cmd)
	}
}

// livePins fills s.pinIdx with the open chat's pins (the model's ChatPin
// indexes) that are still pinned and have not run out, newest first, and
// s.pinSoon with when the first of them runs out (0: none); it returns how
// many there are.
func (s *Screen) livePins(a *state.Account, chat []byte) int {
	s.pinN, s.pinSoon = 0, 0
	n := a.Model.ChatPins(chat)
	for i := 0; i < n && s.pinN < len(s.pinIdx); i++ {
		p := &s.pin
		if a.Model.ChatPin(chat, i, p) != nil || (p.ExpiresAt != 0 && p.ExpiresAt <= s.unix) || !a.Model.PinnedMessage(chat, p.ID) {
			continue
		}
		if p.ExpiresAt != 0 && (s.pinSoon == 0 || p.ExpiresAt < s.pinSoon) {
			s.pinSoon = p.ExpiresAt
		}
		j := s.pinN
		for ; j > 0 && s.pinAt[j-1] < p.PinnedAt; j-- {
			s.pinIdx[j], s.pinAt[j] = s.pinIdx[j-1], s.pinAt[j-1]
		}
		s.pinIdx[j], s.pinAt[j] = i, p.PinnedAt
		s.pinN++
	}
	return s.pinN
}

// pinBar is the pinned messages' bar over the transcript
// (zf:src/ui/conversation.rs pin_banner): the shown pin's text; a click
// jumps to it and shows the next. livePins has run this frame.
func (s *Screen) pinBar(e *screen.Env, a *state.Account, ch model.Chat, w float32) {
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	n := s.pinN
	if s.pinShown >= n {
		s.pinShown = 0
	}
	it := &s.pin
	if n == 0 || a.Model.ChatPin(ch.ID(), s.pinIdx[s.pinShown], it) != nil {
		return
	}
	h := e.Pt(pinBarPt)
	f := c.Fmt()
	fillPin(f, i18n.T("Pinned message {index} of {count}: {text}"), s.pinShown+1, n, it.Text)
	theme.At(c, 0, 0, w, h)
	c.NextSize(ui.Px(w), ui.Px(h))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(10))
	c.NextPadding(e.Pt(12), 0, e.Pt(12), 0)
	c.NextBG(r.Colors[theme.Pane])
	c.NextBorder(r.Colors[theme.Separator], r.Lines.Hairline)
	c.NextBorderSides(ui.SideBottom)
	c.NextRole(ui.RoleButton)
	c.NextSemName(f.String())
	c.NextCursor(ui.CursorPointer)
	bar := c.Box(base.Floating|ui.Clickable|ui.DrawBackground|ui.DrawBorder|ui.DrawHotEffects, "pins")
	c.Push(bar)
	// the segments, one per pin, the shown one in the accent
	c.NextSize(ui.Px(e.Pt(3)), ui.Px(h-e.Pt(16)))
	c.NextAxis(ui.AxisY)
	c.NextGap(e.Pt(2))
	c.Push(c.Box(0, "segments"))
	for i := 0; i < n; i++ {
		col := r.Colors[theme.Label3]
		if n-1-i == s.pinShown {
			col = r.Colors[theme.Accent]
		}
		c.NextSize(ui.Pct(1), ui.Pct(1/float32(n)))
		c.NextBG(col)
		c.NextRadius(e.Pt(1.5))
		c.BoxKeyed(ui.DrawBackground, ui.KeyN(c.ParentKey(), uint64(i)), nil)
	}
	c.Pop()
	k.Icon(s.icons.pin, r.Colors[theme.Accent])
	rexui.LabelBytes(k, it.Text, rexui.LabelOpts{})
	c.Pop()
	if c.Signal(bar).Clicked {
		a.OpenMessage(ch.ID(), it.ID)
		s.pinShown = (s.pinShown + 1) % n
	}
	// a pin that runs out takes the bar's next frame
	if s.pinSoon > 0 {
		c.WakeIn((s.pinSoon - s.unix + 1) * 1e9)
	}
}

func fillPin(f *ui.Fmt, tmpl string, index, count int, text []byte) {
	for len(tmpl) > 0 {
		i := indexOf(tmpl, '{')
		if i < 0 {
			f.Str(tmpl)
			return
		}
		f.Str(tmpl[:i])
		tmpl = tmpl[i:]
		switch {
		case hasPrefix(tmpl, "{index}"):
			f.Int(index)
			tmpl = tmpl[len("{index}"):]
		case hasPrefix(tmpl, "{count}"):
			f.Int(count)
			tmpl = tmpl[len("{count}"):]
		case hasPrefix(tmpl, "{text}"):
			f.Str(model.S(text))
			tmpl = tmpl[len("{text}"):]
		default:
			f.Byte('{')
			tmpl = tmpl[1:]
		}
	}
}
