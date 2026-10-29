package chatlist

import (
	"time"

	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// body builds what the list shows under the header: skeleton rows while the
// archive loads, an empty state, or the rows.
func (s *Screen) body(e *screen.Env, a *state.Account) {
	m := a.Model
	k := e.K
	c := k.Context()
	if len(s.ids) == 0 {
		if a.Mode != state.ListStarred && a.Mode != state.ListLocked && !m.Chats.Loaded() && m.Chats.Len() == 0 {
			s.skeleton(e)
			return
		}
		s.empty(e, a)
		return
	}
	// With legacy scrollers (a mouse, or "Always" in System Settings) the
	// rows end before the bar rather than under it.
	v := base.BeginVList(c, "chats", &s.st, base.VListOpts{Estimate: rowH * k.Scale(), Line: 40, BarRoom: k.VListBarRoom("chats", &s.sbar)})
	typing := false
	for i := v.First; i < v.Last; i++ {
		id := v.Item(i)
		typing = s.item(e, a, id) || typing
	}
	base.EndVList(&v)
	c.Push(v.Outer)
	sv := base.ScrollView{Outer: v.Outer, Inner: v.Body, State: &s.sbar, Axes: base.ScrollVertical}
	rexui.ScrollBars(k.Kit, &sv)
	c.Pop()
	if typing {
		c.WakeIn(int64(time.Second)) // a "typing…" lapses without an event
	}
	s.takeFocusComposer(e)
}

// skeleton is the rows' placeholders while the archive is still loading.
func (s *Screen) skeleton(e *screen.Env) {
	k := e.K
	c := k.Context()
	sc := k.Scale()
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(i18n.T("Loading your chats"))
	c.Push(c.BoxKeyed(ui.Clip, ui.HashString(c.ParentKey(), "skeleton"), nil))
	n := 12
	var name string
	for i := 0; i < n; i++ {
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextGap(theme.Snap(10 * sc))
		c.NextPadding(theme.Snap(16*sc), 0, theme.Snap(16*sc), 0)
		c.NextSize(ui.Pct(1), ui.Px(theme.Snap(rowH*sc)))
		c.Push(c.BoxKeyed(0, ui.KeyN(c.ParentKey(), uint64(i)), nil))
		if i == 0 {
			name = i18n.T("Loading your chats")
		} else {
			name = ""
		}
		rexui.Skeleton(k.Kit, "avatar", rexui.SkeletonOpts{Shape: rexui.SkeletonAvatar, Name: name})
		c.NextAxis(ui.AxisY)
		c.NextGap(theme.Snap(8 * sc))
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.Push(c.BoxKeyed(0, ui.KeyN(c.ParentKey(), 100), nil))
		k.NextWidth(ui.Pct(0.55))
		rexui.Skeleton(k.Kit, "title", rexui.SkeletonOpts{})
		k.NextWidth(ui.Pct(0.85))
		rexui.Skeleton(k.Kit, "preview", rexui.SkeletonOpts{})
		c.Pop()
		c.Pop()
	}
	c.Pop()
}

// empty is the empty state of the list (zf:src/ui/chats.rs:915).
func (s *Screen) empty(e *screen.Env, a *state.Account) {
	k := e.K
	var title, msg string
	ic := s.ic.eChat
	switch {
	case a.Mode == state.ListStarred:
		title, msg, ic = i18n.T("No starred messages"), i18n.T("Right-click a message and choose Star to keep it here."), s.ic.eStar
	case a.Mode == state.ListLocked:
		title, msg, ic = i18n.T("No locked chats"), i18n.T("Lock a chat from its context menu to hide it here."), s.ic.eLock
	case s.searching(a):
		title, msg, ic = i18n.T("No results"), i18n.T("Try another name, number, or message text."), s.ic.eSearch
	case a.Mode == state.ListArchived:
		title, msg, ic = i18n.T("Nothing archived"), i18n.T("Archived chats appear here."), s.ic.eArchive
	case a.Filter != state.FilterAll && a.Filter != state.FilterLabel:
		switch a.Filter {
		case state.FilterUnread:
			title = i18n.T("No unread chats")
		case state.FilterPrivate:
			title = i18n.T("No private chats")
		case state.FilterChannels:
			title = i18n.T("No channels")
		case state.FilterFavorites:
			title = i18n.T("No favorites yet")
		default:
			title = i18n.T("No groups")
		}
		msg = i18n.T("Choose All to see every chat.")
	default:
		if on, _, _ := a.Model.Syncing(); on {
			title, msg = i18n.T("Loading your chats"), i18n.T("Receiving history from your phone.")
		} else {
			title, msg = i18n.T("No chats yet"), i18n.T("Use New chat to message a contact or yourself.")
		}
	}
	rexui.EmptyState(k.Kit, title, rexui.EmptyStateOpts{Icon: ic, HasIcon: true, Message: msg})
}

// item builds one item of the list; it reports whether a row shows typing.
func (s *Screen) item(e *screen.Env, a *state.Account, id uint64) (typing bool) {
	switch itemKind(id) {
	case kChat:
		return s.chat(e, a, model.ChatRef(itemN(id)))
	case kHeader:
		s.section(e, int(itemN(id)))
	case kHit:
		s.hit(e, a, int(itemN(id)))
	case kContact:
		s.contact(e, a, int(itemN(id)))
	case kSelf:
		s.self(e, a)
	case kStar:
		s.star(e, a, int(itemN(id)))
	case kDropped:
		s.dropped(e)
	case kFolder:
		s.folderRow(e, a, int(itemN(id)))
	case kRule:
		s.rule(e)
	}
	return false
}

// infoOf is the cached text of chat ref's row.
func (s *Screen) infoOf(ref model.ChatRef) *rowInfo {
	for len(s.info) < int(ref) {
		s.info = append(s.info, rowInfo{})
	}
	return &s.info[ref-1]
}

// forgetRows drops every row's cached text and picture: they were made from
// another account's model, whose generations may equal this one's. The
// buffers stay for reuse.
func (s *Screen) forgetRows() {
	for i := range s.info {
		in := &s.info[i]
		*in = rowInfo{when: in.when[:0], preview: in.preview[:0], announce: in.announce[:0], value: in.value[:0],
			draftSrc: in.draftSrc[:0], prefix: in.prefix[:0], previewFull: in.previewFull[:0]}
	}
}

// chat builds the row of chat ref.
func (s *Screen) chat(e *screen.Env, a *state.Account, ref model.ChatRef) (typing bool) {
	ch, ok := a.Model.Chats.Get(ref)
	if !ok {
		return false
	}
	in := s.infoOf(ref)
	s.refresh(e, in, ch)
	s.avatarHandle(e, in, s.px)
	in.muted = ch.Muted(e.S.Now().Unix())
	sender, n := a.Model.Typing(ref)
	typing = n > 0
	var who string
	if typing {
		who = s.typingText(a, in, ch, sender)
	}
	selected := ref == a.Open
	if s.query != "" && s.selRef != 0 {
		selected = ref == s.selRef
	}
	sig := s.chatRow(e, in, selected, typing, who)
	// A row opens its chat when the button goes down, as a macOS list
	// selects, not a click's length later; the keyboard's activation is
	// its Clicked.
	us := e.C().Signal(sig.ID)
	if sig.Clicked || us.Pressed && e.C().Input().Mods&ui.ModCtrl == 0 {
		s.KeepUnread(e, ref)
		a.OpenChat(ref)
	} else if sig.Hovering {
		s.dwell(e, a, ref)
	}
	if !s.native(e, sig.ID, menuChat, ref, 0) && sig.RightClicked {
		inp := e.C().Input()
		s.menu.show(ref, inp.MouseX, inp.MouseY)
	}
	return typing
}

// dwellNs is how long the pointer rests on a row before its chat's messages
// are loaded ahead of a click: long enough that sweeping over the list loads
// nothing.
const dwellNs = 90_000_000

// dwell follows the pointer resting on chat ref's row, and prefetches its
// conversation once it has rested dwellNs.
func (s *Screen) dwell(e *screen.Env, a *state.Account, ref model.ChatRef) {
	c := e.C()
	now := c.Input().Time
	if s.hoverRef != ref || s.hoverAcct != a.Serial {
		s.hoverRef, s.hoverAcct, s.hoverFrom = ref, a.Serial, now
	}
	if c.Deadline(ui.KeyN(s.dwellKey, 0), s.hoverFrom+dwellNs) || now-s.hoverFrom >= dwellNs {
		a.Prefetch(ref)
	}
}

// typingText is "typing…" or "Name is typing…" in a group.
func (s *Screen) typingText(a *state.Account, in *rowInfo, ch model.Chat, sender []byte) string {
	group := ch.Group()
	if in.tvalue != "" && in.tGroup == group && (!group || in.typingFor == string(sender)) {
		return in.typingWho
	}
	in.tGroup = group
	if group {
		in.typingFor = string(sender)
		name := string(a.Model.DisplayName(sender))
		for len(name) > 0 && name[0] == '~' {
			name = name[1:]
		}
		in.typingWho = i18n.Format(i18n.T("{} is typing…"), "{}", name)
	} else {
		in.typingFor, in.typingWho = "", i18n.T("typing…")
	}
	in.tvalue = joinValue(string(in.announce), in.typingWho)
	return in.typingWho
}

// section is a header of the search's results.
func (s *Screen) section(e *screen.Env, which int) {
	k := e.K
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	var t string
	switch which {
	case 0:
		t = i18n.T("Chats")
	case 1:
		t = i18n.T("Messages")
	default:
		t = i18n.T("Contacts")
	}
	st := &r.Styles[theme.StyleSidebarHeader]
	c.NextPadding(theme.Snap(16*sc), theme.Snap(10*sc), theme.Snap(16*sc), theme.Snap(4*sc))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextRole(ui.RoleLabel)
	c.NextSemName(t)
	c.Push(c.BoxKeyed(0, ui.HashString(c.ParentKey(), "section"), nil))
	c.NextSize(ui.Pct(1), ui.Px(st.Line))
	c.NextFont(st.Font)
	c.NextTextColor(r.Colors[theme.Accent])
	c.BoxKeyed(ui.DrawText, ui.HashString(c.ParentKey(), "t"), base.StrBytes(t))
	c.Pop()
}

// hit is a message the search found: its chat, who said it, and the line.
func (s *Screen) hit(e *screen.Env, a *state.Account, i int) {
	mir := s.mirror(a)
	if i >= len(mir.hits) {
		return
	}
	h := &mir.hits[i]
	m := a.Model
	ref := m.Chats.Find([]byte(h.chat))
	title := string(m.DisplayName([]byte(h.chat)))
	var handle = s.chatHandle(e, ref)
	if ref != 0 {
		title = s.title(a, m.Chats.At(ref))
	}
	prev := h.text
	switch {
	case h.fromMe:
		prev = i18n.T("You") + ": " + prev
	case bytesHasSuffix(h.chat, "@g.us"):
		prev = s.shortName(a, []byte(h.sender)) + ": " + prev
	}
	if rexui.ChatListRow(e.K.Kit, "row", title, prev, stamp(e.S.Now(), h.ts), rexui.ChatRowOpts{Image: handle}).Clicked {
		s.openMessage(a, h.chat, h.id)
	}
}

func bytesHasSuffix(s, suf string) bool { return len(s) >= len(suf) && s[len(s)-len(suf):] == suf }

// chatHandle is the avatar of an existing chat for another list's row.
func (s *Screen) chatHandle(e *screen.Env, ref model.ChatRef) (h img.Handle) {
	if ref == 0 {
		return 0
	}
	in := s.infoOf(ref)
	if !in.valid {
		return 0
	}
	s.avatarHandle(e, in, s.px)
	return in.handle
}

// contact is a person without a chat: clicking starts one.
func (s *Screen) contact(e *screen.Env, a *state.Account, i int) {
	if i >= len(s.contacts) {
		return
	}
	ct := a.Model.Contacts.At(s.contacts[i])
	name := string(ct.DisplayName())
	if name == "" {
		name = format.Phone(model.S(model.Phone(ct.ID())))
	}
	detail := format.Phone(model.S(model.Phone(ct.ID())))
	if rexui.ChatListRow(e.K.Kit, "row", name, detail, "", rexui.ChatRowOpts{}).Clicked {
		s.startChat(e, a, ct.ID(), name)
	}
}

// self offers the chat with ourselves.
func (s *Screen) self(e *screen.Env, a *state.Account) {
	if rexui.ChatListRow(e.K.Kit, "row", s.selfTitle(a), i18n.T("Message yourself"), "", rexui.ChatRowOpts{}).Clicked {
		s.startChat(e, a, a.Model.Me.ID(), string(a.Model.Me.Name()))
	}
}

// startChat opens the chat with jid, making it first when there is none
// (zf:src/app.rs:6132).
func (s *Screen) startChat(e *screen.Env, a *state.Account, jid []byte, name string) {
	if ref := a.Model.Chats.Find(jid); ref != 0 {
		a.OpenChat(ref)
	} else {
		cmd := a.Cmd(abi.CommandTagEnsureChat)
		cmd.EnsureChat.Chat, cmd.EnsureChat.Name = jid, []byte(name)
		a.Send(cmd)
		s.waitFor = append(s.waitFor[:0], jid...)
	}
	s.search.Reset()
}

// openMessage jumps to a message: the state opens its chat, pages toward it
// and has the conversation scroll to it and flash it.
func (s *Screen) openMessage(a *state.Account, chat, id string) {
	a.OpenMessage([]byte(chat), []byte(id))
}

// dropped is the placeholder for chats too large to cross the ABI (they
// are not in the model, so there is nothing to name): a line under the
// chats, not a row that does anything (SH-020).
func (s *Screen) dropped(e *screen.Env) {
	k := e.K
	c := k.Context()
	sc := k.Scale()
	t := i18n.T("Some chats could not be shown")
	c.NextPadding(theme.Snap(16*sc), theme.Snap(12*sc), theme.Snap(16*sc), theme.Snap(12*sc))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextRole(ui.RoleLabel)
	c.NextSemName(t)
	c.Push(c.BoxKeyed(0, ui.HashString(c.ParentKey(), "dropped"), nil))
	rexui.Label(k.Kit, t, rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
	c.Pop()
}

// star is a starred message: its chat, when it was starred, and its line.
func (s *Screen) star(e *screen.Env, a *state.Account, i int) {
	mir := s.mirror(a)
	if i >= len(mir.starred) {
		return
	}
	x := &mir.starred[i]
	m := a.Model
	ref := m.Chats.Find([]byte(x.chat))
	title := string(m.DisplayName([]byte(x.chat)))
	if ref != 0 {
		title = s.title(a, m.Chats.At(ref))
	}
	body := x.text
	switch {
	case x.fromMe:
		body = i18n.T("You") + ": " + body
	case bytesHasSuffix(x.chat, "@g.us"):
		body = s.shortName(a, []byte(x.sender)) + ": " + body
	}
	h := s.chatHandle(e, ref)
	sig := rexui.ChatListRow(e.K.Kit, "row", title, body, stamp(e.S.Now(), x.at), rexui.ChatRowOpts{Image: h})
	if sig.Clicked {
		s.openMessage(a, x.chat, x.id)
	}
	// the message's own menu belongs to the conversation; Unstar is here
	if !s.native(e, sig.ID, menuStar, 0, i) && sig.RightClicked {
		inp := e.C().Input()
		s.menu.showStar(i, inp.MouseX, inp.MouseY)
	}
}

var _ icon.ID
