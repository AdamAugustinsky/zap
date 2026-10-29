package chatlist

import (
	"bytes"
	"slices"
	"time"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/markup"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// kind tells the rows of the list apart; an item id is the kind in its top
// byte and the chat's ref (or an index) below.
type kind uint8

const (
	kChat kind = iota + 1
	kHeader
	kHit
	kContact
	kSelf
	kStar
	kDropped // the foot of the chats: some could not be shown (n: how many)
	kFolder  // a folder's row (n: its index in the account's Folders)
	kRule    // the line under the folders, over the chats in none
)

func itemID(k kind, n uint32) uint64 { return uint64(k)<<56 | uint64(n) }
func itemKind(id uint64) kind        { return kind(id >> 56) }
func itemN(id uint64) uint32         { return uint32(id) }

// Screen is the chat list.
type Screen struct {
	fdr folderUI // the folders' rows (folders.go)
	// folderOf is each of scratch's chats' folder (folders), -1 for none.
	folderOf []int8
	ic       icons

	// the row the pointer rests on, since when (dwell)
	hoverRef  model.ChatRef
	hoverAcct uint64
	hoverFrom int64
	dwellKey  ui.Key

	hour12      bool   // the clock the stamps were made for
	clockSerial uint32 // counts changes of it

	st   base.VListState
	sbar base.ScrollState

	chipScroll, labelScroll    base.ScrollState // the chip rows' sideways scroll
	rowInner, rowBox, revealed ui.ID            // the row being built, and the chip last brought into view
	fold                       state.Filter     // the first filter folded into the More chip; past FilterChannels when none is
	ids                        []uint64         // the list's items now
	next                       []uint64
	refs                       []model.ChatRef // the chats listed, top to bottom
	info                       []rowInfo       // by ref-1
	vkey                       viewKey
	vok                        bool
	acct                       *state.Account
	acctSerial                 uint64 // acct's Serial: the account the rows' cache (info) is of
	open                       model.ChatRef
	dirty                      bool

	// search
	search    ui.TextBuffer
	searchRev uint32
	query     string
	needle    []rune
	needleB   []byte
	selRef    model.ChatRef // the result the arrows reached
	focus     bool
	searchKey ui.Key
	cmdr      searchCmd
	mirrors   []*mirror // by account, Serial

	// what the chips show
	counts      [6]int
	labelCounts []int
	kept        []model.ChatRef // chats the Unread chip keeps listed after they were read
	scratch     []model.ChatRef

	hitsN, contactsN int
	contacts         []int // indexes into Contacts of the search's people
	contactKeys      []string
	offerSelf        bool

	menu ctxMenu
	lm   labelManager

	sentQuery string
	px        int32 // avatar decode size

	dec                  abi.Chat      // decode scratch
	spans                []markup.Span // appendPlain's scratch
	pendingFocusComposer bool
	inStarred            bool
	waitFor              []byte // a chat being made, to open when it arrives
	version              uint32 // moves whenever the rows are listed again
	qgen                 uint32

	// the rail's avatars
	cst   base.VListState
	cids  []uint64
	cver  uint32
	cOpen model.ChatRef
}

var _ screen.ListPane = (*Screen)(nil)

// New returns the chat list.
func New() *Screen { return &Screen{} }

// Init readies the list for a window: its icons, the avatar cache, and the
// listeners for the events the model does not keep.
func (s *Screen) Init(e *screen.Env) error {
	s.ic.add(e.K.Kit, theme.MacWindow.Capsule.Glyph+2)
	s.fdr.add(e.K.Kit)
	s.hour12 = format.Hour12()
	s.search.Reserve(256)
	s.cmdr.s = s
	s.dwellKey = ui.HashString(0, "chatlist dwell")
	e.S.OnAccountGone(s, s.accountGone)
	return nil
}

// viewKey is everything the listed rows are computed from.
type viewKey struct {
	acct                   uint64 // Account.Serial
	state                  uint64
	chats, order, contacts uint32
	mir                    uint64
	query                  uint32
	open                   model.ChatRef
	dropped                uint64
}

// mirror is account a's mirror, by its Serial: an index moves when an
// earlier account goes, a Serial never names another account.
func (s *Screen) mirror(a *state.Account) *mirror {
	for _, m := range s.mirrors {
		if m.serial == a.Serial {
			return m
		}
	}
	m := &mirror{serial: a.Serial}
	s.mirrors = append(s.mirrors, m)
	return m
}

// accountGone drops the mirror of an account that is gone, and what it held.
func (s *Screen) accountGone(_ *state.State, serial uint64) {
	s.mirrors = slices.DeleteFunc(s.mirrors, func(m *mirror) bool { return m.serial == serial })
}

// Build builds the list at r: the search field and the chips over the rows
// (or the search's results, the starred messages), and the menus.
func (s *Screen) Build(e *screen.Env, r screen.Rect) {
	a := e.Acct()
	e.Begin(r, "chatlist")
	defer e.End()
	if a == nil {
		return
	}
	k := e.K
	if h := format.Hour12(); h != s.hour12 {
		s.hour12 = h
		s.clockSerial++
	}
	s.takeRequests(e, a)
	s.sync(e, a)
	e.S.Visible = s.refs
	s.px = int32(avatarPt * k.Scale())

	k.BeginContainer(ui.AxisY, ui.CrossStart, "col", ui.Px(r.W), ui.Px(r.H), 0)
	s.header(e, a)
	k.NextHeight(widget.Fill)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "body", ui.Pct(1), widget.Fill, 0)
	s.body(e, a)
	k.EndContainer()
	k.EndContainer()
	s.menu.build(s, e, a)
	s.lm.build(s, e, a)
}

// takeRequests acts on the shell's one-shot asks and on a change of the
// open chat.
func (s *Screen) takeRequests(e *screen.Env, a *state.Account) {
	if e.S.Req.FocusSearch {
		e.S.Req.FocusSearch = false
		s.focus = true
	}
	if e.S.Req.ClearSearch {
		e.S.Req.ClearSearch = false
		s.search.Reset() // the query follows the field's revision
	}
	if a != s.acct || a.Serial != s.acctSerial {
		s.acct, s.acctSerial = a, a.Serial
		s.forgetRows()
		s.vok = false
		s.search.Reset()
		s.searchRev = s.search.Rev()
		s.query, s.needle, s.needleB, s.selRef = "", nil, nil, 0
		s.kept = s.kept[:0]
		s.menu.close()
		s.open = 0
	}
}

// KeepUnread retains a listed unread chat before a click or keyboard open
// reads it. Explicit jumps bypass it; searches and folders do not retain.
func (s *Screen) KeepUnread(e *screen.Env, ref model.ChatRef) {
	a := e.Acct()
	if a == nil {
		return
	}
	s.takeRequests(e, a)
	if a.Mode == state.ListChats && a.Filter == state.FilterUnread && s.query == "" {
		if ch, ok := a.Model.Chats.Get(ref); ok && (ch.Unread() > 0 || ch.MarkedUnread()) && !slices.Contains(s.kept, ref) {
			s.kept = append(s.kept, ref)
		}
	}
}

// sync recomputes the listed rows when anything they depend on moved.
func (s *Screen) sync(e *screen.Env, a *state.Account) {
	m := a.Model
	if rev := s.search.Rev(); rev != s.searchRev {
		s.searchRev = rev
		s.setQuery(e, a, string(bytes.TrimSpace(s.search.Bytes())))
	}
	mir := s.mirror(a)
	s.refreshMirror(a, mir)
	key := viewKey{acct: a.Serial, state: e.S.Gen(), chats: m.Chats.Gen(), order: m.Chats.OrderGen(), contacts: m.Contacts.Gen(),
		mir: mir.gen, query: uint32(len(s.query)) + s.queryGen(), open: a.Open, dropped: droppedChats(m)}
	if s.vok && key == s.vkey {
		return
	}
	prev := s.vkey
	s.vkey, s.vok = key, true
	s.rebuild(e, a, mir)
	s.version++
	s.enter(e, a, mir)
	if len(s.waitFor) > 0 {
		if ref := m.Chats.Find(s.waitFor); ref != 0 {
			s.waitFor = s.waitFor[:0]
			a.OpenChat(ref)
		}
	}
	if a.Open != 0 && a.Open != prev.open {
		s.reveal(a.Open)
	}
}

// droppedChats is how many chats the core could not send, as a chat or as
// an update of one (EventDropped).
func droppedChats(m *model.Store) uint64 {
	return m.DroppedOf(abi.EventTagChats) + m.DroppedOf(abi.EventTagChatUpdated)
}

// reveal scrolls the list to chat ref when it is listed.
func (s *Screen) reveal(ref model.ChatRef) {
	if _, ok := s.st.IndexOf(itemID(kChat, uint32(ref))); ok {
		s.st.ScrollToID(itemID(kChat, uint32(ref)), base.AlignNearest)
	}
}

func (s *Screen) queryGen() uint32 { return s.qgen }

// setQuery makes q the search, and asks the core for message matches.
func (s *Screen) setQuery(e *screen.Env, a *state.Account, q string) {
	s.qgen++
	s.query = q
	s.selRef = 0
	s.needle = format.FoldKey(q)
	s.needleB = s.needleB[:0]
	for _, r := range s.needle {
		s.needleB = append(s.needleB, string(r)...)
	}
	a.Model.Answer(abi.EventTagSearchHits).Clear()
	if q != "" && a.Mode != state.ListLocked && q != s.sentQuery {
		cmd := a.Cmd(abi.CommandTagSearchMessages)
		cmd.SearchMessages.Query = []byte(q)
		a.Send(cmd)
	}
	s.sentQuery = q
}

// searching reports whether the list shows search results.
func (s *Screen) searching(a *state.Account) bool {
	return s.query != "" && a.Mode != state.ListLocked && a.Mode != state.ListStarred
}

// filterOn reports whether the chips filter the list: not while searching,
// nor in the archive or the locked folder.
func (s *Screen) filterOn(a *state.Account) bool {
	return a.Mode == state.ListChats && s.query == ""
}

// matches reports whether chat ch is one of the chips' (zf:src/model.rs:222).
func chipMatches(f state.Filter, ch model.Chat) bool {
	channel := isChannel(ch)
	switch f {
	case state.FilterAll:
		return !channel
	case state.FilterUnread:
		return (ch.Unread() > 0 || ch.MarkedUnread()) && !channel
	case state.FilterPrivate:
		return ch.Kind() == abi.ChatKindDirect && !channel
	case state.FilterFavorites:
		return ch.Favorite() && !channel
	case state.FilterGroups:
		return ch.Group()
	case state.FilterChannels:
		return channel
	}
	return false
}

func isChannel(ch model.Chat) bool { return bytes.HasSuffix(ch.ID(), []byte("@newsletter")) }

// rebuild lists the rows and updates the virtual list.
func (s *Screen) rebuild(e *screen.Env, a *state.Account, mir *mirror) {
	m := a.Model
	s.next = s.next[:0]
	s.refs = s.refs[:0]
	s.hitsN, s.contactsN, s.offerSelf = 0, 0, false
	s.counts = [6]int{}
	if cap(s.labelCounts) < len(mir.labels) {
		s.labelCounts = make([]int, len(mir.labels))
	}
	s.labelCounts = s.labelCounts[:len(mir.labels)]
	clear(s.labelCounts)
	for _, ref := range m.Chats.Order() {
		ch := m.Chats.At(ref)
		if ch.Archived() || ch.Locked() {
			continue
		}
		if ch.Unread() > 0 || ch.MarkedUnread() {
			for f := state.FilterUnread; f <= state.FilterChannels; f++ {
				if chipMatches(f, ch) {
					s.counts[f]++
				}
			}
		}
		if ch.Unread() > 0 {
			for i := 0; i < ch.Labels(); i++ {
				if j := mir.labelIndex(ch.Label(i)); j >= 0 {
					s.labelCounts[j]++
				}
			}
		}
	}
	mode := a.Mode
	switch {
	case mode == state.ListStarred:
		for i := range mir.starred {
			if ref := m.Chats.Find([]byte(mir.starred[i].chat)); ref != 0 && m.Chats.At(ref).Locked() {
				continue
			}
			s.next = append(s.next, itemID(kStar, uint32(i)))
		}
	default:
		s.collect(e, a, mir)
	}
	s.apply()
}

// collect lists the chats the chips and the search show, then the search's
// messages and people.
func (s *Screen) collect(e *screen.Env, a *state.Account, mir *mirror) {
	m := a.Model
	locked := a.Mode == state.ListLocked
	archived := a.Mode == state.ListArchived
	searching := s.searching(a)
	filtering := s.filterOn(a)
	label := []byte(nil)
	if a.Filter == state.FilterLabel && a.Label.Len() > 0 {
		label = a.Label.At(0)
	}
	favOrder := filtering && label == nil && a.Filter == state.FilterFavorites
	s.scratch = s.scratch[:0]
	for _, ref := range m.Chats.Order() {
		ch := m.Chats.At(ref)
		if ch.Locked() != locked {
			continue
		}
		if !locked && ch.Archived() != archived && !searching {
			continue
		}
		if filtering {
			if label != nil {
				if !ch.HasLabel(label) {
					continue
				}
			} else if !chipMatches(a.Filter, ch) && !(a.Filter == state.FilterUnread && slices.Contains(s.kept, ref)) {
				continue
			}
		}
		if s.query != "" && !s.matchChat(e, a, ch) {
			continue
		}
		if !searching && !a.InSpace(ch.ID()) {
			continue // a folder's space shows its chats alone
		}
		s.scratch = append(s.scratch, ref)
	}
	if favOrder {
		slices.SortStableFunc(s.scratch, func(x, y model.ChatRef) int {
			cx, cy := m.Chats.At(x), m.Chats.At(y)
			if cx.Pinned() != cy.Pinned() {
				if cx.Pinned() {
					return -1
				}
				return 1
			}
			if cx.Pinned() {
				return 0
			}
			return cx.FavoritePos() - cy.FavoritePos()
		})
	}
	if searching {
		if len(s.scratch) > 0 {
			s.next = append(s.next, itemID(kHeader, 0))
		}
	}
	if s.grouped(a, searching, filtering) {
		s.folders(a)
	} else {
		for _, ref := range s.scratch {
			s.next = append(s.next, itemID(kChat, uint32(ref)))
		}
		s.refs = append(s.refs, s.scratch...)
	}
	if !searching {
		// Chats the core could not send are not in any list: say so, once,
		// under the chats (SH-020).
		if n := droppedChats(m); n > 0 && a.Mode == state.ListChats && a.Filter == state.FilterAll {
			s.next = append(s.next, itemID(kDropped, uint32(min(n, 1<<31))))
		}
		return
	}
	if len(mir.hits) > 0 {
		first := true
		for i := range mir.hits {
			if ref := m.Chats.Find([]byte(mir.hits[i].chat)); ref != 0 && m.Chats.At(ref).Locked() {
				continue
			}
			if first {
				s.next = append(s.next, itemID(kHeader, 1))
				first = false
			}
			s.next = append(s.next, itemID(kHit, uint32(i)))
			s.hitsN++
		}
	}
	s.people(a)
	if s.offerSelf || len(s.contacts) > 0 {
		s.next = append(s.next, itemID(kHeader, 2))
		if s.offerSelf {
			s.next = append(s.next, itemID(kSelf, 0))
		}
		for i := range s.contacts {
			s.next = append(s.next, itemID(kContact, uint32(i)))
		}
	}
}

// grouped reports whether the list shows its folders: the chats of the
// whole account under the All chip, not searched.
func (s *Screen) grouped(a *state.Account, searching, filtering bool) bool {
	return len(a.Settings.Folders) > 0 && a.SpaceFolder == "" && a.Mode == state.ListChats && !searching && (!filtering || a.Filter == state.FilterAll)
}

// folders lays the chats out as Arc's sidebar does: the pinned chats in no
// folder first, then each folder's row and, open, its chats, then the rest;
// refs, which the rail and the keys walk, are the rows' chats in order.
func (s *Screen) folders(a *state.Account) {
	fs := a.Settings.Folders
	in := s.folderOf[:0]
	for _, ref := range s.scratch {
		in = append(in, int8(a.FolderOf(a.Model.Chats.At(ref).ID())))
	}
	s.folderOf = in
	for i, ref := range s.scratch {
		if in[i] < 0 && a.Model.Chats.At(ref).Pinned() {
			s.next = append(s.next, itemID(kChat, uint32(ref)))
			s.refs = append(s.refs, ref)
		}
	}
	for fi := range fs {
		s.next = append(s.next, itemID(kFolder, uint32(fi)))
		if fs[fi].Collapsed {
			continue
		}
		for i, ref := range s.scratch {
			if int(in[i]) == fi {
				s.next = append(s.next, itemID(kChat, uint32(ref)))
				s.refs = append(s.refs, ref)
			}
		}
	}
	rule := false
	for i, ref := range s.scratch {
		if in[i] < 0 && !a.Model.Chats.At(ref).Pinned() {
			if !rule {
				s.next, rule = append(s.next, itemID(kRule, 0)), true
			}
			s.next = append(s.next, itemID(kChat, uint32(ref)))
			s.refs = append(s.refs, ref)
		}
	}
}

// matchChat is upstream's search match: the title, the phone number, or the
// latest message (zf:src/app.rs:2827).
func (s *Screen) matchChat(e *screen.Env, a *state.Account, ch model.Chat) bool {
	if format.ContainsFold(s.titleBytes(a, ch), s.needle) {
		return true
	}
	if ph := model.Phone(ch.ID()); ph != nil && len(s.needleB) > 0 && bytes.Contains(ph, s.needleB) {
		return true
	}
	return ch.HasLast() && format.ContainsFold(ch.Preview(), s.needle)
}

// people lists the contacts without a chat that match the search, and
// whether the chat with ourselves is offered (zf:src/app.rs:2871).
func (s *Screen) people(a *state.Account) {
	m := a.Model
	s.contacts, s.contactKeys = s.contacts[:0], s.contactKeys[:0]
	me := m.Me.ID()
	for i := 0; i < m.Contacts.Len(); i++ {
		ct := m.Contacts.At(i)
		id := ct.ID()
		ph := model.Phone(id)
		if ph == nil || bytes.Equal(id, me) || m.Chats.Find(id) != 0 {
			continue
		}
		if !format.ContainsFold(ct.DisplayName(), s.needle) && !(len(s.needleB) > 0 && bytes.Contains(ph, s.needleB)) {
			continue
		}
		s.contacts = append(s.contacts, i)
	}
	slices.SortFunc(s.contacts, func(x, y int) int {
		return bytes.Compare(bytes.ToLower(m.Contacts.At(x).DisplayName()), bytes.ToLower(m.Contacts.At(y).DisplayName()))
	})
	if len(s.contacts) > 15 {
		s.contacts = s.contacts[:15]
	}
	s.offerSelf = false
	if len(me) > 0 {
		if ref := m.Chats.Find(me); ref == 0 || !m.Chats.At(ref).Locked() {
			listed := false
			for _, r := range s.scratch {
				if bytes.Equal(m.Chats.At(r).ID(), me) {
					listed = true
				}
			}
			s.offerSelf = !listed && s.offersSelf(a, me)
		}
	}
}

// offersSelf is app.offers_self: the search names us.
func (s *Screen) offersSelf(a *state.Account, me []byte) bool {
	hay := [...]string{s.selfTitle(a), i18n.T("Message yourself"), i18n.T("You"), "Message yourself"}
	for _, h := range hay {
		if format.ContainsFold([]byte(h), s.needle) {
			return true
		}
	}
	ph := model.Phone(me)
	return ph != nil && len(s.needleB) > 0 && bytes.Contains(ph, s.needleB)
}

// apply moves the virtual list to s.next, with Move and Splice where the
// change is one chat moving or arriving, and Reset otherwise.
func (s *Screen) apply() {
	old, nw := s.ids, s.next
	defer func() { s.ids = append(s.ids[:0], nw...) }()
	if len(old) == 0 || len(nw) == 0 {
		s.st.Reset(nw)
		return
	}
	i := 0
	for i < len(old) && i < len(nw) && old[i] == nw[i] {
		i++
	}
	if i == len(old) && i == len(nw) {
		return
	}
	jo, jn := len(old), len(nw)
	for jo > i && jn > i && old[jo-1] == nw[jn-1] {
		jo--
		jn--
	}
	ro, rn := old[i:jo], nw[i:jn]
	switch {
	case len(ro) == len(rn) && len(ro) > 1 && ro[0] == rn[len(rn)-1] && slices.Equal(ro[1:], rn[:len(rn)-1]):
		s.st.Move(i, 1, jn-1) // one chat moved down
	case len(ro) == len(rn) && len(ro) > 1 && ro[len(ro)-1] == rn[0] && slices.Equal(ro[:len(ro)-1], rn[1:]):
		s.st.Move(jo-1, 1, i) // one chat moved up
	case len(ro) == 0:
		s.st.Splice(i, 0, rn)
	case len(rn) == 0:
		s.st.Splice(i, len(ro), nil)
	default:
		s.st.Reset(nw)
	}
}

// header builds the search field and the chips.
func (s *Screen) header(e *screen.Env, a *state.Account) {
	k := e.K
	c := k.Context()
	sc := k.Scale()
	pad := theme.Snap(10 * sc)
	c.NextAxis(ui.AxisY)
	c.NextPadding(pad, theme.Snap(4*sc), pad, theme.Snap(6*sc))
	c.NextGap(theme.Snap(8 * sc))
	c.NextSize(ui.Pct(1), ui.Children(1))
	box := c.BoxKeyed(0, ui.HashString(c.ParentKey(), "header"), nil)
	c.Push(box)
	s.searchField(e, a)
	// Search lists every match, so the chips hide there (but not in the
	// locked folder, where the field is a title search); the starred list
	// replaces the chat list and its filters. In the archive and the locked
	// folder they read as unchosen and do not filter.
	if a.Mode != state.ListStarred && (a.Mode == state.ListLocked || s.query == "") {
		s.chips(e, a)
	}
	c.Pop()
}

// searchField builds the search field. The arrows walk the matching chats
// while it keeps the focus; Enter opens the one reached; Escape clears it
// and gives the keyboard to the open chat's composer.
func (s *Screen) searchField(e *screen.Env, a *state.Account) {
	k := e.K
	c := k.Context()
	if a.Mode == state.ListStarred {
		return
	}
	searchLabel := i18n.T("Search") + "###chat-search"
	key := base.FieldKey(c.ParentKey(), searchLabel)
	s.searchKey = key
	if s.focus {
		s.focus = false
		c.SetFocus(key)
		c.SetFocusVisible(true)
	}
	if c.FocusKey() == key {
		c.NextFieldCommander(&s.cmdr, "")
	}
	fs := rexui.SearchField(k.Kit, searchLabel, &s.search, i18n.T("Search"), rexui.FieldOpts{Size: theme.SizeLarge})
	e.TabStopKey(screen.StopSearch, fs.Key)
}

// searchCmd takes the arrows, Enter and Escape of the search field.
type searchCmd struct{ s *Screen }

// FieldCommand implements ui.FieldCommander.
func (sc *searchCmd) FieldCommand(cmd ui.FieldCommand, _ rune) bool {
	s := sc.s
	a := s.acct
	if a == nil {
		return false
	}
	switch cmd {
	case ui.FieldMoveDown, ui.FieldMoveUp:
		if s.query == "" || len(s.refs) == 0 {
			return false
		}
		i := slices.Index(s.refs, s.selRef)
		if cmd == ui.FieldMoveDown {
			i = min(i+1, len(s.refs)-1)
		} else if i > 0 {
			i--
		}
		i = max(i, 0)
		s.selRef = s.refs[i]
		s.reveal(s.selRef)
		return true
	case ui.FieldInsertNewline:
		if s.query == "" || len(s.refs) == 0 {
			return false
		}
		ref := s.selRef
		if ref == 0 || !slices.Contains(s.refs, ref) {
			ref = s.refs[0]
		}
		s.search.Reset()
		a.OpenChat(ref)
		s.pendingFocusComposer = true
		return true
	case ui.FieldCancel:
		if s.query == "" && s.search.Len() == 0 {
			return false
		}
		s.search.Reset()
		if a.Open != 0 {
			s.pendingFocusComposer = true
		}
		return true
	}
	return false
}

// Toolbar builds the list's toolbar items: Back and the title of the folder
// it shows and New chat (the list's toggle is the titlebar's).
func (s *Screen) Toolbar(e *screen.Env, tb *rexui.Toolbar) {
	a := e.Acct()
	k := e.K
	c := k.Context()
	title := i18n.T("Chats")
	if a != nil && a.SpaceFolder != "" {
		if i := a.FolderByID(a.SpaceFolder); i >= 0 {
			title = a.Settings.Folders[i].Name // a folder's space is named for it
		}
	}
	back := false
	calls := e.S.Page == state.PageCalls
	switch {
	case calls:
		title = i18n.T("Calls")
	case a != nil && a.Mode == state.ListStarred:
		title, back = i18n.T("Starred"), true
	case a != nil && a.Mode == state.ListArchived:
		title, back = i18n.T("Archived"), true
	case a != nil && a.Mode == state.ListLocked:
		title, back = i18n.T("Locked chats"), true
	}
	if back {
		it := tb.Item(s.ic.back, i18n.T("Back to chats"))
		e.TabStop(screen.StopBack, it.ID)
		if it.Clicked {
			s.leave(e, a)
		}
	}
	tb.BeginCustom("list title", ui.Pct(1))
	r := k.Theme()
	st := &r.Styles[theme.StyleHeadline]
	// First in the band, the title keeps a little more room from the
	// window's buttons than the band's item gap.
	inset := float32(0)
	if !back {
		inset = theme.Snap(e.Pt(6))
	}
	theme.At(c, 0, theme.Snap((tb.Height()-st.Line)/2), 0, st.Line)
	c.NextSize(ui.Pct(1), ui.Px(st.Line))
	c.NextPadding(inset, 0, 0, 0)
	c.NextFont(st.Font)
	c.NextTextColor(rexui.ToolbarLook.Title.RGBA(r))
	c.NextRole(ui.RoleLabel)
	c.NextSemName(title)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|base.Floating, ui.HashString(c.ParentKey(), "title"), base.StrBytes(title))
	tb.EndCustom()
	if calls || a == nil || a.Mode == state.ListStarred {
		return
	}
	nc := tb.Item(s.ic.newChat, i18n.T("New chat (⌘N)"))
	e.TabStop(screen.StopNewChat, nc.ID)
	if nc.Clicked {
		e.S.OpenDialog(state.Dialog{Kind: state.DialogNewChat})
	}
	// the list's toggle is the titlebar's (shell), beside the account
}

// leave goes back to the chats from Starred, Archived or Locked.
func (s *Screen) leave(e *screen.Env, a *state.Account) {
	if a == nil {
		return
	}
	a.SetMode(state.ListChats)
	e.S.Touch()
}

// pendingFocusComposer hands the keyboard to the composer after a frame.
func (s *Screen) takeFocusComposer(e *screen.Env) {
	if s.pendingFocusComposer {
		s.pendingFocusComposer = false
		e.S.Req.FocusComposer = true
		e.S.Touch()
	}
}

// wakeIn is how often a typing row is looked at again.
const wakeIn = time.Second

// enter asks for what a folder needs when it opens: the starred messages
// (zf:src/app.rs ToggleStarred sends LoadStarred).
func (s *Screen) enter(e *screen.Env, a *state.Account, mir *mirror) {
	if a.Mode == state.ListStarred && !s.inStarred {
		cmd := a.Cmd(abi.CommandTagLoadStarred)
		a.Send(cmd)
	}
	s.inStarred = a.Mode == state.ListStarred
}
