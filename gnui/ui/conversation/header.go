package conversation

import (
	"bytes"
	"slices"
	"strconv"

	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/calls"
	"zapfast/ui/screen"
)

// Toolbar builds the conversation's header in the toolbar
// (zf:src/ui/conversation.rs header): the avatar, the name and the
// subtitle as one button that opens the info panel, then the call buttons,
// Search messages and More.
func (s *Screen) Toolbar(e *screen.Env, tb *rexui.Toolbar) {
	a := e.Acct()
	if a == nil {
		tb.Title()
		return
	}
	ch, ok := a.OpenChatView()
	if !ok {
		tb.Title()
		return
	}
	if s.h == nil {
		s.Init(e)
	}
	s.now = e.S.Now()
	c, k := e.C(), e.K.Kit

	// A narrow window shows the list or the chat, not both (the shell gives
	// the chat the whole width): this is the way back to the list.
	if s.narrow {
		back := i18n.T("Back to chats")
		if e.S.Page == state.PageCalls {
			back = i18n.T("Back to the calls")
		}
		if tb.Item(s.icons.back, back).Clicked {
			a.OpenChat(0)
			e.S.Touch()
		}
	}
	tb.BeginCustom("chat-header", ui.Pct(1))
	name := ch.Name()
	if len(name) == 0 {
		name = a.Model.DisplayName(ch.ID())
	}
	h := tb.Height()
	// The name and subtitle give way to the toolbar's other items (and an
	// open side pane's header), fading at the end, rather than run over them.
	c.NextSize(ui.Children(0), ui.Px(h))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(8))
	c.NextPadding(e.Pt(8), 0, e.Pt(8), 0)
	c.NextRole(ui.RoleButton)
	c.NextSemNameBytes(name)
	c.NextCursor(ui.CursorPointer)
	hdr := c.Box(ui.Clickable, "chat-header-info")
	c.Push(hdr)
	rexui.Avatar(k, model.S(name), rexui.AvatarOpts{Size: 28, Presence: s.presenceDot(a, ch), Image: s.avatar(e, a, ch)})
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Children(0), ui.Children(1))
	c.Push(c.Box(0, "titles"))
	k.NextWidth(shrinkingLabel)
	rexui.LabelBytes(k, name, rexui.LabelOpts{Style: rexui.TextHeadline})
	if sub, accent := s.subtitle(e, a, ch); len(sub) > 0 {
		col := rexui.Of(theme.Label2)
		if accent {
			col = rexui.Of(theme.Accent)
		}
		k.NextWidth(shrinkingLabel)
		rexui.LabelBytes(k, sub, rexui.LabelOpts{Style: rexui.TextCaption, Color: col})
	}
	c.Pop()
	c.Pop()
	if c.Signal(hdr).Clicked {
		e.S.Panes.SetInfo(ch.ID())
		e.S.Touch()
	}
	tb.EndCustom()

	ic := &s.icons
	if canCall(a, ch) {
		// While this chat has the call, the button ends it
		// (zf:src/ui/conversation.rs header).
		if calls.LiveHere(e.S, a, ch.ID()) {
			if tb.Item(ic.hangup, i18n.T("Hang up")).Clicked {
				a.Send(a.Cmd(abi.CommandTagHangupCall))
			}
		} else {
			if tb.Item(ic.call, i18n.T("Voice call")).Clicked {
				calls.Place(e.S, a, ch.ID(), false)
			}
			if tb.Item(ic.video, i18n.T("Video call")).Clicked {
				calls.Place(e.S, a, ch.ID(), true)
			}
		}
	}
	search := tb.Item(ic.search, i18n.T("Search messages"))
	e.TabStop(screen.StopChatSearch, search.ID)
	if search.Clicked {
		if e.S.Panes.Pane == state.PaneSearch {
			s.h.search.close(e.S)
		} else {
			s.openSearch(e)
		}
	}
	more := tb.Item(ic.more, i18n.T("More"))
	if more.Clicked {
		s.more = !s.more
		s.menu.open = false
	}
	if s.more {
		s.moreMenu(e, a, ch, c.RectOf(c.KeyOf(more.ID)))
	}
}

// shrinkingLabel is a header label's width: its text's, giving way entirely
// when the header is narrower (the label fades at the end).
var shrinkingLabel = ui.TextSize(0).Strict(0)

// presenceDot is the avatar's dot: online, from the open chat's presence.
func (s *Screen) presenceDot(a *state.Account, ch model.Chat) rexui.AvatarPresence {
	if on, _, ok := a.Model.Presence(ch.ID()); ok && on && !ch.Group() {
		return rexui.PresenceOnline
	}
	return rexui.PresenceNone
}

// subtitle is the line under the name (zf:src/ui/conversation.rs
// subtitle): who is typing, a group's members, online or last seen, or the
// number of a contact we have not saved. accent: the accent colour.
func (s *Screen) subtitle(e *screen.Env, a *state.Account, ch model.Chat) (b []byte, accent bool) {
	c := e.C()
	if s.typers = a.Model.Typers(ch.Ref(), s.typers[:0]); len(s.typers) > 0 {
		return s.typingLine(c.Fmt(), a, ch).Bytes(), true
	}
	if ch.Group() {
		if names := s.members(a, ch); len(names) > 0 {
			return names, false
		}
		return c.Fmt().Str(i18n.T("Group")).Bytes(), false
	}
	if on, seen, ok := a.Model.Presence(ch.ID()); ok {
		if on {
			return c.Fmt().Str(i18n.T("online")).Bytes(), true
		}
		if seen > 0 {
			return lastSeen(c.Fmt(), seen, s.now).Bytes(), false
		}
	}
	if ph := model.Phone(ch.ID()); len(ph) > 0 && !saved(a, ch.ID()) {
		fmtBuf = format.AppendPhone(fmtBuf[:0], model.S(ph))
		return c.Fmt().Str(model.S(fmtBuf)).Bytes(), false
	}
	return nil, false
}

// members is a group's members as its subtitle lists them
// (zf:src/app.rs participant_names): first names sorted, a repeated one
// counted ("Ana x2"), then the numbers, then You. It is worked out again
// only when the chat or the contacts change.
func (s *Screen) members(a *state.Account, ch model.Chat) []byte {
	mb := &s.mem
	g, cg := ch.Gen(), a.Model.Contacts.Gen()
	if mb.ok && mb.acct == a.Serial && mb.ref == ch.Ref() && mb.gen == g && mb.cgen == cg {
		return mb.out
	}
	*mb = memberNames{ok: true, acct: a.Serial, ref: ch.Ref(), gen: g, cgen: cg, dec: mb.dec, out: mb.out[:0], b: mb.b[:0], names: mb.names[:0], nums: mb.nums[:0]}
	if ch.Decode(&mb.dec) != nil {
		return nil
	}
	me, you := a.Model.Me.ID(), false
	for i, id := range mb.dec.Participants {
		if len(me) > 0 && bytes.Equal(id, me) {
			you = true
			continue
		}
		dup := false
		for _, prev := range mb.dec.Participants[:i] {
			dup = dup || bytes.Equal(prev, id)
		}
		if dup {
			continue
		}
		name := a.Model.DisplayName(id)
		if len(name) == 0 || name[0] == '+' || string(name) == "Unknown" {
			mb.nums = append(mb.nums, mb.put(name))
			continue
		}
		if ct := &a.Model.Contacts; ct.Find(id) >= 0 && len(ct.At(ct.Find(id)).FirstName()) > 0 {
			name = ct.At(ct.Find(id)).FirstName()
		} else {
			name = firstName(bytes.TrimLeft(name, "~"))
		}
		mb.names = append(mb.names, memberName{mb.put(name), mb.put(bytes.ToLower(name))})
	}
	at := func(r [2]int) []byte { return mb.b[r[0]:r[1]] }
	slices.SortFunc(mb.names, func(x, y memberName) int { return bytes.Compare(at(x.folded), at(y.folded)) })
	slices.SortFunc(mb.nums, func(x, y [2]int) int { return bytes.Compare(at(x), at(y)) })
	sep := func() {
		if len(mb.out) > 0 {
			mb.out = append(mb.out, ", "...)
		}
	}
	for i := 0; i < len(mb.names); {
		j := i + 1
		for j < len(mb.names) && bytes.EqualFold(at(mb.names[j].original), at(mb.names[i].original)) {
			j++
		}
		sep()
		mb.out = append(mb.out, at(mb.names[i].original)...)
		if j-i > 1 {
			mb.out = append(append(mb.out, " x"...), strconv.Itoa(j-i)...)
		}
		i = j
	}
	for _, n := range mb.nums {
		sep()
		mb.out = append(mb.out, at(n)...)
	}
	if you {
		sep()
		mb.out = append(mb.out, i18n.T("You")...)
	}
	return mb.out
}

// memberNames is the header's list of the open group's members.
type memberNames struct {
	ok        bool
	acct      uint64
	ref       model.ChatRef
	gen, cgen uint32
	dec       abi.Chat
	b, out    []byte
	names     []memberName
	nums      [][2]int
}

// Both ranges live in memberNames.b; only original is shown to the reader.
type memberName struct {
	original, folded [2]int
}

func (mb *memberNames) put(name []byte) [2]int {
	at := len(mb.b)
	mb.b = append(mb.b, name...)
	return [2]int{at, len(mb.b)}
}

func firstName(b []byte) []byte {
	for i, c := range b {
		if c == ' ' {
			return b[:i]
		}
	}
	return b
}

// saved reports whether jid is in the address book.
func saved(a *state.Account, jid []byte) bool {
	ct := &a.Model.Contacts
	i := ct.Find(jid)
	return i >= 0 && len(ct.At(i).FullName()) > 0
}

// avatar is the chat's picture in the window's image cache (0 until it is
// known): asked for once per chat, then followed as its file changes.
func (s *Screen) avatar(e *screen.Env, a *state.Account, ch model.Chat) img.Handle {
	av := &s.av
	g := a.Model.Avatars.Gen()
	if !av.known || av.acct != a.Serial || av.ref != ch.Ref() {
		*av = avatarState{known: true, acct: a.Serial, ref: ch.Ref(), gen: g - 1}
		a.FetchAvatar(ch.ID(), false)
	}
	if av.gen != g {
		av.gen = g
		path, id, ok := a.Model.Avatars.Picture(ch.ID(), false)
		switch {
		case !ok:
			av.path = ""
		default:
			if string(path) != av.path {
				av.path = s.avPaths.of(path)
			}
			// the picture's identity: a new one reuses the last one's file
			av.key = img.Key{Hash: id}
		}
	}
	cache := e.K.Images()
	if av.path == "" || cache == nil {
		return 0
	}
	px := int32(e.Pt(28) + 0.5)
	av.key.W, av.key.H, av.key.Flags = px, px, img.Cover
	h, _ := cache.Request(av.key, img.File(av.path))
	return h
}

// avatarState is the header's picture of the open chat.
type avatarState struct {
	known bool
	acct  uint64
	ref   model.ChatRef
	gen   uint32
	path  string
	key   img.Key
}

// avatarPaths are the last pictures' paths, as strings the image cache may
// keep: a chat opened again finds its path here, so switching between
// chats makes no new string.
type avatarPaths struct {
	p    [8]string
	next int
}

func (r *avatarPaths) of(path []byte) string {
	for _, p := range r.p {
		if p == string(path) {
			return p
		}
	}
	s := string(path)
	r.p[r.next%len(r.p)] = s
	r.next++
	return s
}

// canCall reports whether the chat has call buttons: a person or a group,
// not a channel, a broadcast list or the chat with ourselves.
func canCall(a *state.Account, ch model.Chat) bool {
	switch ch.Kind() {
	case abi.ChatKindGroup:
		return true
	case abi.ChatKindDirect:
	default:
		return false
	}
	me := a.Model.Me.ID()
	return len(me) == 0 || !sameUser(me, ch.ID())
}

// sameUser compares two JIDs by their user part.
func sameUser(a, b []byte) bool {
	return sameID(userOf(a), userOf(b))
}

func userOf(j []byte) []byte {
	for i, c := range j {
		if c == '@' || c == ':' {
			return j[:i]
		}
	}
	return j
}
