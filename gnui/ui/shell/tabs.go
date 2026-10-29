package shell

import (
	"strconv"
	"strings"

	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
)

// The titlebar's tabs are the chats opened lately (state.ChatTabs), as Rex's
// sessions: Rex's capsule over the panes from the conversation's edge, each
// tab the chat's picture, with a dot while it has unread messages, and its
// name. A press opens the chat, the close button forgets it, a drag reorders.

// Lengths in points: the picture in a tab's lead, and where it sits.
const (
	tabPicPt  = 18
	tabPicX   = 8
	tabLeadPt = tabPicX + tabPicPt + 7
)

// chatTabs is the bar's state on the shell: the frame's tabs, built in place,
// and each slot's picture.
type chatTabs struct {
	bar    rexui.TabBar
	tabs   [state.MaxChatTabs]rexui.Tab
	pics   [state.MaxChatTabs]tabPic
	unread [state.MaxChatTabs]tabCount
}

// tabCount is a tab's unread count as its detail shows it, kept so that the
// text is made once per count, not each frame.
type tabCount struct {
	n int
	s string
}

func (t *tabCount) of(n int) string {
	if n != t.n || t.s == "" {
		t.n, t.s = n, strconv.Itoa(n)
	}
	return t.s
}

// The tabs come up from ztheme.T.MinTabs open chats (2: one tab would only
// repeat the open chat's header and its row).

// tabsUp reports whether the tabs show between x0 and x1 this frame.
func (sh *Shell) tabsUp(a *state.Account, x0, x1 float32) bool {
	return len(a.ChatTabs()) >= int(ztheme.T.MinTabs) && x1-x0 >= sh.env.Pt(80)
}

// tabDetail is what a tab says after its name about a chat that is not on
// screen, the most pressing first: someone typing in it, a draft left in
// it, its unread messages. The chosen tab says nothing: its chat is there.
func (sh *Shell) tabDetail(a *state.Account, ch model.Chat, i int, chosen bool) string {
	if chosen {
		return ""
	}
	switch {
	case typingIn(a, ch.Ref()):
		return i18n.T("typing…")
	case len(a.Model.Draft(ch.ID())) > 0:
		return strings.TrimSuffix(strings.TrimSpace(i18n.T("Draft:")), ":")
	case ch.Unread() > 0 && !ch.Muted(sh.S.Now().Unix()):
		return sh.tabs.unread[i].of(ch.Unread())
	}
	return ""
}

func typingIn(a *state.Account, ref model.ChatRef) bool {
	_, n := a.Model.Typing(ref)
	return n > 0
}

// tabPic is the picture of the chat in a tab slot, read again when the chat
// or the avatars change.
type tabPic struct {
	ref    model.ChatRef
	serial uint64
	gen    uint32
	path   string
	key    img.Key
}

// handle is the picture of chat ch, px pixels square, from the image cache.
func (p *tabPic) handle(a *state.Account, cache *img.Cache, ch model.Chat, px int32) img.Handle {
	g := a.Model.Avatars.Gen()
	if p.ref != ch.Ref() || p.serial != a.Serial || p.gen != g {
		p.ref, p.serial, p.gen = ch.Ref(), a.Serial, g
		path, id, ok := a.Model.Avatars.Picture(ch.ID(), false)
		if !ok {
			path = nil
		}
		if string(path) != p.path {
			p.path = string(path)
		}
		p.key = img.Key{Hash: id}
	}
	if p.path == "" || cache == nil {
		return 0
	}
	p.key.W, p.key.H, p.key.Flags = px, px, img.Cover
	h, _ := cache.Request(p.key, img.File(p.path))
	return h
}

// chatTabs builds the tabs between x0 and x1 in the titlebar band, tbH high,
// and does what they ask.
func (sh *Shell) chatTabs(a *state.Account, x0, x1, tbH float32) {
	refs := a.ChatTabs()
	if !sh.tabsUp(a, x0, x1) {
		return
	}
	s, e, k := sh.S, &sh.env, sh.env.K
	c, r := k.Context(), k.Theme()
	t := &sh.tabs
	sel := -1
	for i, ref := range refs {
		ch, _ := a.Model.Chats.Get(ref) // ChatTabs keeps only chats that are there
		title := ch.Name()
		if len(title) == 0 {
			title = a.Model.DisplayName(ch.ID())
		}
		chosen := ref == a.Open && s.Page == state.PageChats
		t.tabs[i] = rexui.Tab{ID: uint64(ref), Title: model.S(title), Detail: sh.tabDetail(a, ch, i, chosen), Lead: tabLeadPt}
		if chosen {
			sel = i
		}
	}
	tabs := t.tabs[:len(refs)]
	ev := t.bar.Update(k.Kit, tabs, sel)
	rc := &rexui.TabsRecipes[rexui.TabsRex]
	capH := rc.Height*r.Scale + 2*rc.Inset[1]*r.Scale
	b := t.bar.Begin(k.Kit, i18n.T("Open chats")+"###chat tabs", tabs, sel, rexui.TabsOpts{X0: x0, X1: x1, Y: theme.Snap((tbH - capH) / 2)})
	pic := e.Pt(tabPicPt)
	for j := 0; j < b.N(); j++ {
		cell := b.BeginTab(j)
		ref := refs[cell.Index]
		ch, _ := a.Model.Chats.Get(ref)
		if cell.Hot > 0 {
			a.Prefetch(ref) // the chat is drawn by the time the press lands
		}
		h := cell.Rect[3] - cell.Rect[1]
		theme.At(c, theme.Snap(cell.X+e.Pt(tabPicX))-cell.Rect[0], theme.Snap((h-pic)/2), pic, pic)
		c.Push(c.Box(base.Floating, "picture"))
		rexui.Avatar(k.Kit, tabs[cell.Index].Title, rexui.AvatarOpts{Size: tabPicPt, Image: t.pics[cell.Index].handle(a, sh.imgs, ch, int32(pic)), Backdrop: rexui.Of(theme.Field)})
		if ch.Unread() > 0 || ch.MarkedUnread() {
			sh.unreadDot(pic, ch.Muted(s.Now().Unix()))
		}
		c.Pop()
		b.EndTab(&cell)
	}
	b.End()

	switch {
	case ev.Close >= 0:
		a.CloseChatTab(ev.Close)
	case ev.MoveFrom >= 0:
		a.MoveChatTab(ev.MoveFrom, ev.MoveTo)
	case ev.Select >= 0:
		ref := refs[ev.Select]
		if s.Page != state.PageChats {
			s.Page = state.PageChats
		}
		sh.keepUnread(ref)
		a.OpenChat(ref)
	}
}

// unreadDot marks a picture size pixels wide whose chat has unread
// messages, in the accent (grey when the chat is muted), ringed in the
// field's colour as the account block's.
func (sh *Shell) unreadDot(size float32, muted bool) {
	e := &sh.env
	c, r := e.C(), e.K.Theme()
	ring, dot := e.Pt(9), e.Pt(6.5)
	col := r.Colors[theme.Accent]
	if muted {
		col = r.Colors[theme.Label3]
	}
	theme.At(c, size-ring+e.Pt(2), -e.Pt(2), ring, ring)
	c.NextBG(r.Colors[theme.Field])
	c.NextRadius(ring / 2)
	c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask())
	c.Push(c.Box(base.Floating|ui.DrawBackground, "unread"))
	c.NextCenter()
	c.NextSize(ui.Px(dot), ui.Px(dot))
	c.NextBG(col)
	c.NextRadius(dot / 2)
	c.Box(base.Floating|ui.DrawBackground, "dot")
	c.Pop()
}
