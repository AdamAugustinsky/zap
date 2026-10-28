package info

import (
	"bytes"
	"time"

	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// Layout, in points (zf:src/ui/info.rs).
const (
	margin    = 14  // the panel's side margin
	headerPt  = 52  // the header band
	memberPt  = 30  // a member row
	docPt     = 60  // a document or link row
	gapPt     = 4   // between the cells of the picture grid and strip
	maxName   = 100 // WhatsApp refuses longer group names (GROUP_NAME_LIMIT)
	minWidth  = 240 // the narrowest the panel is dragged
	maxWidth  = 640
	jidMax    = 128
	savingMax = 8
)

// icons are the panel's symbols.
type icons struct {
	closeX, back, chevronRight, check, pencil         icon.ID
	message, person, copy, bell, bellOff, pin, pinOff icon.ID
	archive, unarchive, photo, video, play, doc, link icon.ID
	refresh                                           icon.ID
}

// Screen is the info pane.
type Screen struct {
	env *screen.Env
	ic  icons

	// the panel's chat
	jid   [jidMax]byte
	jidN  int
	acct  int
	owner model.ChatRef // the open chat the panel was opened over
	valid bool
	v     view
	sh    shared

	// pending asks
	openJID  [jidMax]byte // a chat to open once the core has made it
	openN    int
	openAcct int
	saving   [savingMax][jidMax]byte // groups whose name or photo is being saved
	savingN  [savingMax]uint8

	// widget state
	scroll   widget.ScrollState // the overview
	gridSc   widget.ScrollState // the picture grid
	docSc    widget.ScrollState
	linkSc   widget.ScrollState
	memberSc widget.ScrollState
	descSc   widget.ScrollState
	tab      int
	sel      ui.SelectionScope
	contact  contactEdit
	rename   renameEdit
	resizing bool
	width    float32
	viewGen  uint32 // state.Gen the media tab was last changed at
	selText  [3][]byte
	options  [3]string
	hdr      header // what the header says this frame
	inBand   bool   // Toolbar built the header in the toolbar band (this frame, after Build)
	bandNow  bool   // so it did last frame: the pane leaves its header out

	optionGen uint32
	count     [3][8]byte
	fmtBuf    []byte
	pressName []byte
	thumbs    thumbCache
	find      find // the media view's search
}

var _ screen.SidePane = (*Screen)(nil)
var _ state.Listener = (*Screen)(nil)
var _ ui.SelectionText = (*Screen)(nil)

// New returns the pane.
func New() *Screen { return &Screen{} }

// Init readies the pane for a window: its symbols, and the events it keeps.
func (s *Screen) Init(e *screen.Env) error {
	s.env = e
	k := e.K
	ic := &s.ic
	for _, it := range [...]struct {
		dst *icon.ID
		sym string
		fb  rune
		pt  float32
	}{
		{&ic.closeX, "xmark", '✕', 13}, {&ic.back, "chevron.left", '‹', 13}, {&ic.chevronRight, "chevron.right", '›', 12},
		{&ic.check, "checkmark", '✓', 13}, {&ic.pencil, "pencil", '✎', 12},
		{&ic.message, "message", '✉', 13}, {&ic.person, "person.badge.plus", '☺', 13}, {&ic.copy, "doc.on.doc", '⎘', 13},
		{&ic.bell, "bell", '♪', 13}, {&ic.bellOff, "bell.slash", '♪', 13}, {&ic.pin, "pin", '⚲', 13}, {&ic.pinOff, "pin.slash", '⚲', 13},
		{&ic.archive, "archivebox", '▤', 13}, {&ic.unarchive, "tray.and.arrow.up", '▤', 13},
		{&ic.photo, "photo", '▣', 22}, {&ic.video, "video", '▶', 22}, {&ic.play, "play.fill", '▶', 14},
		{&ic.doc, "doc.text", '▤', 20}, {&ic.link, "link", '↗', 20}, {&ic.refresh, "arrow.clockwise", '↻', 13},
	} {
		*it.dst = k.AddIcon(icon.Spec{Symbol: it.sym, Fallback: it.fb, Pt: it.pt})
	}
	e.S.Listen(s, abi.EventTagChatShared, abi.EventTagChatSharedChanged, abi.EventTagGroupSaving, abi.EventTagChatUpdated, abi.EventTagChats)
	s.contact.first.Max, s.contact.last.Max = 256, 256
	s.contact.first.Reserve(256)
	s.contact.last.Reserve(256)
	s.rename.name.Max = maxName * 4
	s.rename.name.Reserve(maxName * 4)
	return nil
}

// Width is the pane's width.
func (s *Screen) Width(e *screen.Env) float32 { return e.Pt(e.S.Settings.InfoPaneWidth) }

// Event handles the answers the panel waits for.
func (s *Screen) Event(a *state.Account, ev *abi.Event) {
	switch ev.Tag {
	case abi.EventTagChatShared:
		if !a.Active() || a.Index != s.acct {
			return
		}
		e := &ev.ChatShared
		if e.Request != s.sh.request || !s.sh.pending {
			return // an earlier request's, or a chat the panel no longer shows
		}
		if e.HasError {
			s.sh.pending, s.sh.failed = false, true
			s.sh.stale = false
			return
		}
		if !s.sh.forChat(e.Chat) {
			return
		}
		s.sh.take(a.Conn, e)
		if e.Last {
			s.sh.pending, s.sh.failed, s.sh.loaded = false, false, true
			s.sh.finish(s.now())
			s.env.S.Touch()
		}
	case abi.EventTagChatSharedChanged:
		if !a.Active() || a.Index != s.acct || !s.sh.forChat(ev.ChatSharedChanged.Chat) {
			return
		}
		// what the panel lists changed: ask again, one request at a time
		if s.sh.pending {
			s.sh.stale = true
		} else if s.valid && s.allowed(a) {
			s.request(a)
		} else {
			s.sh.stale = true
		}
	case abi.EventTagGroupSaving:
		g := &ev.GroupSaving
		s.setSaving(g.Chat, g.Saving)
		s.env.S.Touch()
	case abi.EventTagChatUpdated:
		s.openWhenMade(a, ev.ChatUpdated.Chat.ID)
	case abi.EventTagChats:
		if s.openN > 0 {
			for i := range ev.Chats.Chats {
				s.openWhenMade(a, ev.Chats.Chats[i].ID)
			}
		}
	}
}

func (s *Screen) now() time.Time { return s.env.S.Now() }

// openWhenMade opens the chat "Message" asked for once the core has it.
func (s *Screen) openWhenMade(a *state.Account, id []byte) {
	if s.openN == 0 || a.Index != s.openAcct || !bytes.Equal(id, s.openJID[:s.openN]) {
		return
	}
	if ref := a.Model.Chats.Find(id); ref != 0 {
		s.openN = 0
		if a.Active() {
			a.OpenChat(ref)
		}
	}
}

func (s *Screen) setSaving(chat []byte, on bool) {
	free := -1
	for i := range s.savingN {
		n := int(s.savingN[i])
		if n > 0 && bytes.Equal(s.saving[i][:n], chat) {
			if !on {
				s.savingN[i] = 0
			}
			return
		}
		if n == 0 && free < 0 {
			free = i
		}
	}
	if on && free >= 0 && len(chat) <= jidMax {
		s.savingN[free] = uint8(copy(s.saving[free][:], chat))
	}
}

func (s *Screen) isSaving(chat []byte) bool {
	for i := range s.savingN {
		if n := int(s.savingN[i]); n > 0 && bytes.Equal(s.saving[i][:n], chat) {
			return true
		}
	}
	return false
}

// allowed reports whether the panel may list its chat's media: a locked
// chat's stays in the locked folder (zf:src/app.rs info_media_allowed).
func (s *Screen) allowed(a *state.Account) bool {
	return !s.v.locked || a.Mode == state.ListLocked
}

// request asks the core for the chat's listing.
func (s *Screen) request(a *state.Account) {
	if s.jidN == 0 || a.Conn == nil {
		return
	}
	s.sh.request++
	s.sh.pending, s.sh.failed, s.sh.stale, s.sh.asked = true, false, false, true
	cmd := a.Cmd(abi.CommandTagListChatShared)
	cmd.ListChatShared.Chat = s.jid[:s.jidN]
	cmd.ListChatShared.Request = s.sh.request
	a.Send(cmd)
}

// shows reports whether jid may be shown over the open chat: the chat
// itself or one of its members.
func shows(a *state.Account, jid []byte) bool {
	ch, ok := a.OpenChatView()
	if !ok {
		return false
	}
	if bytes.Equal(ch.ID(), jid) {
		return true
	}
	if !ch.Group() {
		return false
	}
	var c abi.Chat
	if ch.Decode(&c) != nil {
		return false
	}
	return listsAny(c.Participants, jid)
}

// sync follows the state: a new chat resets the panel and asks for its
// listing; a chat that is not the open one's closes it. It reports whether
// the panel shows.
func (s *Screen) sync(e *screen.Env, a *state.Account) bool {
	p := &e.S.Panes
	if p.Info.Len() == 0 {
		return false
	}
	jid := p.Info.At(0)
	fresh := !s.valid || a.Index != s.acct || !bytes.Equal(jid, s.jid[:s.jidN]) || a.Open != s.owner
	if fresh {
		if len(jid) > jidMax || !shows(a, jid) {
			p.SetInfo(nil)
			e.S.Touch()
			return false
		}
		s.sh.reset(a.Conn, jid)
		s.jidN = copy(s.jid[:], jid)
		s.acct, s.owner, s.valid = a.Index, a.Open, true
		s.contact.on, s.rename.on = false, false
		s.tab = 0
		s.find.reset()
		s.scroll, s.gridSc, s.docSc, s.linkSc, s.memberSc, s.descSc = widget.ScrollState{}, widget.ScrollState{}, widget.ScrollState{}, widget.ScrollState{}, widget.ScrollState{}, widget.ScrollState{}
		s.sel.Clear()
		s.v.ok = false
	}
	// the strings, when the model moved
	st := a.Model
	var gen uint32
	if s.v.hasChat || st.Chats.Find(jid) != 0 {
		if ref := st.Chats.Find(jid); ref != 0 {
			gen = st.Chats.At(ref).Gen()
		}
	}
	_, nowOK := presenceOf(st, jid)
	if !s.v.ok || fresh || gen != s.v.chatGen || s.v.hasChat != (st.Chats.Find(jid) != 0) ||
		st.Contacts.Gen() != s.v.contactGen || st.Me.Gen() != s.v.meGen || nowOK != s.v.presence.ok {
		s.v.load(a, jid, s.now())
	}
	s.updatePresence(st, jid)
	if fresh {
		a.FetchAvatar(jid, true)
	}
	if s.allowed(a) {
		if !s.sh.asked || (s.sh.stale && !s.sh.pending) {
			s.request(a)
		}
	}
	return true
}

type presence struct {
	online bool
	seen   int64
	ok     bool
}

func presenceOf(st *model.Store, jid []byte) (presence, bool) {
	on, seen, ok := st.Presence(jid)
	return presence{on, seen, ok}, ok
}

func (s *Screen) updatePresence(st *model.Store, jid []byte) {
	p, _ := presenceOf(st, jid)
	if p.online == s.v.presence.online && p.seen == s.v.presence.seen && p.ok == s.v.presence.ok {
		return
	}
	s.v.presence.online, s.v.presence.seen, s.v.presence.ok = p.online, p.seen, p.ok
	s.v.status = presenceLine(p.online, p.seen, p.ok, s.now())
}

// AppendSelected implements ui.SelectionText for the panel's selectable
// texts: 0 the name, 1 the number, 2 the description.
func (s *Screen) AppendSelected(dst []byte, id uint64, fromPart uint16, fromOff uint32, toPart uint16, toOff uint32) []byte {
	if id >= uint64(len(s.selText)) {
		return dst
	}
	return append(dst, ui.SelectedBytes(s.selText[id], 0, fromPart, fromOff, toPart, toOff)...)
}

// closePanel closes the panel and gives the keyboard back to the composer.
func (s *Screen) closePanel(e *screen.Env) {
	e.S.Panes.SetInfo(nil)
	e.S.Req.FocusComposer = true
	e.S.Touch()
}

// Build builds the pane at r.
func (s *Screen) Build(e *screen.Env, r screen.Rect) {
	a := e.Acct()
	if a == nil || !s.sync(e, a) {
		return
	}
	if !a.Model.Linked() {
		return
	}
	s.hdr = header{}
	s.bandNow, s.inBand = s.inBand, false // Toolbar says again, after this, if the shell still hosts it
	e.Begin(r, "Info")
	k := e.K
	k.NextGap(0)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "info", ui.Px(r.W), ui.Px(r.H), 0)
	media := e.S.Panes.InfoView != state.InfoOverview && s.mediaAllowed(a)
	if e.S.Panes.InfoView != state.InfoOverview && !media {
		e.S.Panes.InfoView = state.InfoOverview // a locked chat's media is never drawn
	}
	if media {
		s.mediaView(e, a, r)
	} else {
		s.overview(e, a, r)
	}
	k.EndContainer()
	s.resizeHandle(e, r)
	e.End()
	s.keys(e)
}

func (s *Screen) mediaAllowed(a *state.Account) bool { return s.allowed(a) }

// keys: Escape goes from the media list back to the overview; the overview's
// Escape falls to the shell, which closes the panel.
func (s *Screen) keys(e *screen.Env) {
	c := e.C()
	if e.S.Panes.InfoView == state.InfoOverview || c.ModalOpen() || c.TextFieldFocused() {
		return
	}
	if c.TakeKey(ui.KeyEscape, 0) {
		e.S.Panes.InfoView = state.InfoOverview
		e.S.Touch()
	}
}

// resizeHandle is the panel's left edge: dragging it resizes the panel
// (zf:src/ui/pane.rs), within the room the window leaves.
func (s *Screen) resizeHandle(e *screen.Env, r screen.Rect) {
	c := e.C()
	w := e.Pt(6)
	theme.At(c, 0, 0, w, r.H)
	c.NextCursor(ui.CursorResizeEW)
	c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask())
	c.NextRole(ui.RoleSplitter)
	c.NextSemName(i18n.T("Resize"))
	id := c.Box(ui.Clickable|base.Floating, "Resize")
	sig := c.Signal(id)
	if sig.Dragging && sig.DragDelta[0] != 0 {
		if s.width == 0 {
			s.width = e.S.Settings.InfoPaneWidth
		}
		s.width -= sig.DragDelta[0] / e.K.Scale()
		e.S.Settings.InfoPaneWidth = min(max(s.width, minWidth), maxWidth)
		e.S.Touch()
	} else if !sig.Dragging {
		s.width = 0
	}
}

var _ = i18n.T

var _ screen.ToolbarPart = (*Screen)(nil)
