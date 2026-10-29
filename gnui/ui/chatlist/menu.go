package chatlist

import (
	"bytes"
	"path/filepath"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The list's menus (zf:src/ui/chats.rs context_menu, labels::chat_menu,
// sound_menu) are described once as ui.MenuItems: a native menu where the app
// has them (rexui.OnContextMenu builds the items on the click), else the
// drawn popup below, which draws the same items.

// menuKind is what the open context menu is about.
type menuKind uint8

const (
	menuChat menuKind = iota + 1
	menuChannels
	menuStar
	menuFilters // the filters folded into the More chip
	menuFolder  // a folder's row (star: the folder's index)
)

// The menu items' ids. Sound and label items count up from their base.
const (
	miRead = iota + 1
	miUnread
	miPin
	miFavorite
	miArchive
	miLeave
	miUnmute
	miMute8h
	miMuteWeek
	miMuteForever
	miLock
	miCopy
	miInfo
	miDelete
	miSoundFile
	miCustom
	miMuteAll
	miUnstar
	miManageLabels
	miNoFolder
	miNewFolder
	miFolderRename
	miFolderSpace
	miFolderDelete
	miSplit             // open it beside the open chat (the split view)
	miFloat             // keep it floating over other apps
	miSound      = 100  // + the choice
	miFilter     = 500  // + the filter
	miLabel      = 1000 // + the label's index
	miFolder     = 2000 // + the folder's index
	miFolderIcon = 3000 // + the symbol's index in state.FolderIcons
)

// ctxMenu is the drawn fallback: the open menu, at the pointer, with the
// submenu that is open (index+1 in items; 0 none).
type ctxMenu struct {
	kind  menuKind
	ref   model.ChatRef
	star  int
	at    [2]float32
	sub   int
	subAt [4]float32
	rules int
	items []ui.MenuItem
}

func (m *ctxMenu) close() { *m = ctxMenu{items: m.items[:0]} }

func (m *ctxMenu) show(ref model.ChatRef, x, y float32) {
	*m = ctxMenu{kind: menuChat, ref: ref, at: [2]float32{x, y}, items: m.items[:0]}
}

func (m *ctxMenu) showStar(i int, x, y float32) {
	*m = ctxMenu{kind: menuStar, star: i, at: [2]float32{x, y}, items: m.items[:0]}
}

func (m *ctxMenu) showFolder(i int, x, y float32) {
	*m = ctxMenu{kind: menuFolder, star: i, at: [2]float32{x, y}, items: m.items[:0]}
}

func (m *ctxMenu) showChannels(x, y float32) {
	*m = ctxMenu{kind: menuChannels, at: [2]float32{x, y}, items: m.items[:0]}
}

// menuW and menuRowH are the drawn menu's metrics, in points.
const (
	menuW    = 224
	menuRowH = 26
)

var menuKey = ui.HashString(0, "chatlist:menu")

// items describes the menu of kind (the chat ref, or the starred row i).
func (s *Screen) menuItems(items *[]ui.MenuItem, e *screen.Env, a *state.Account, kind menuKind, ref model.ChatRef, star int) {
	switch kind {
	case menuChat:
		s.chatMenu(items, e, a, ref)
	case menuChannels:
		label := i18n.T("Mute all channels")
		if s.allChannelsMuted(e, a) {
			label = i18n.T("Unmute all channels")
		}
		*items = append(*items, ui.MenuItem{ID: miMuteAll, Title: label, Symbol: "bell.slash"})
	case menuStar:
		*items = append(*items, ui.MenuItem{ID: miUnstar, Title: i18n.T("Unstar"), Symbol: "star.slash"})
	case menuFolder:
		s.folderMenu(items, a, star)
	case menuFilters:
		names := filterNames()
		for f := s.fold; f <= state.FilterChannels; f++ {
			it := ui.MenuItem{ID: miFilter + int(f), Title: names[f]}
			if a.Mode == state.ListChats && a.Filter == f {
				it.Mark = ui.MarkOn
			}
			*items = append(*items, it)
		}
	}
}

// act does what menu item id chose.
func (s *Screen) menuAct(e *screen.Env, a *state.Account, kind menuKind, ref model.ChatRef, star, id int) {
	switch kind {
	case menuChat:
		s.chatAct(e, a, ref, id)
	case menuFolder:
		s.folderAct(e, a, star, id)
	case menuChannels:
		if id == miMuteAll {
			s.muteAllChannels(e, a, !s.allChannelsMuted(e, a))
		}
	case menuFilters:
		if f := state.Filter(id - miFilter); id >= miFilter && f <= state.FilterChannels {
			if a.Mode == state.ListChats && a.Filter == f {
				f = state.FilterAll // as a second click on a chip
			}
			s.setFilter(e, a, f)
		}
	case menuStar:
		mir := s.mirror(a)
		if id == miUnstar && star < len(mir.starred) {
			x := &mir.starred[star]
			cmd := a.Cmd(abi.CommandTagSetStar)
			cmd.SetStar.Chat, cmd.SetStar.Message, cmd.SetStar.Starred = []byte(x.chat), []byte(x.id), false
			a.Send(cmd)
		}
	}
}

// native pops the row's native menu on a right-click and acts on the choice
// that comes back; false when the app has no native menus (the drawn menu
// takes the click).
func (s *Screen) native(e *screen.Env, id ui.ID, kind menuKind, ref model.ChatRef, star int) bool {
	k := e.K
	if !k.Context().NativeMenus() {
		return false
	}
	a := e.Acct()
	if item, ok := rexui.OnContextMenu(k.Kit, id, func(m *[]ui.MenuItem) { s.menuItems(m, e, a, kind, ref, star) }); ok {
		s.menuAct(e, a, kind, ref, star, item)
		e.S.Touch()
	}
	return true
}

// build draws the open menu, if any.
func (m *ctxMenu) build(s *Screen, e *screen.Env, a *state.Account) {
	if m.kind == 0 {
		return
	}
	if len(m.items) == 0 {
		s.menuItems(&m.items, e, a, m.kind, m.ref, m.star)
		if len(m.items) == 0 {
			m.close()
			return
		}
	}
	k := e.K
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	m.rules = 0
	pad := theme.Snap(5 * sc)
	c.NextBG(r.Colors[theme.Raised])
	c.NextBorder(r.Colors[theme.Separator], r.Lines.Hairline)
	c.NextRadius(theme.Snap(10 * sc))
	c.NextShadow(ui.Shadow{OffY: 4 * sc, Sigma: 12 * sc, Color: theme.WithAlpha(r.Colors[theme.Shadow], 0.35)})
	c.NextPadding(pad, pad, pad, pad)
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Px(theme.Snap(menuW*sc)+2*pad), ui.Children(1))
	c.NextRole(ui.RoleMenu)
	c.NextSemName(i18n.T("Chat menu"))
	at := [4]float32{m.at[0], m.at[1], m.at[0], m.at[1]}
	os := c.BeginOverlay(menuKey, ui.OverlayPopup, at, ui.PlaceBelow)
	if os.Dismissed {
		m.close()
		c.EndOverlay()
		return
	}
	var nv base.PopupNav
	id, ok := m.draw(e, &nv, m.items)
	nv.End(c)
	c.EndOverlay()
	if ok {
		kind, ref, star := m.kind, m.ref, m.star
		m.close()
		s.menuAct(e, a, kind, ref, star, id)
		e.S.Touch()
	}
}

// draw draws items and reports the id of the one chosen.
func (m *ctxMenu) draw(e *screen.Env, nv *base.PopupNav, items []ui.MenuItem) (id int, ok bool) {
	c := e.C()
	for i := range items {
		it := &items[i]
		switch {
		case it.Separator:
			m.rule(e)
		case len(it.Sub) > 0:
			sig := m.row(e, nv, it.Title, false, true, it.Disabled)
			if sig.Hovering && c.Input().MouseX != 0 && m.sub != i+1 {
				m.sub = i + 1
			}
			if sig.Clicked {
				m.sub = i + 1
			}
			if m.sub == i+1 {
				m.subAt = c.Rect(sig.ID)
				if sid, sok := m.subPanel(e, it.Title, it.Sub); sok {
					id, ok = sid, true
				}
			}
		default:
			if m.row(e, nv, it.Title, it.Mark == ui.MarkOn, false, it.Disabled).Clicked {
				id, ok = it.ID, true
			}
		}
	}
	return id, ok
}

// row builds one menu row and reports a choice. A submenu row opens its
// submenu on hover or click.
func (m *ctxMenu) row(e *screen.Env, nv *base.PopupNav, label string, checked, sub, disabled bool) base.Signal {
	k := e.K
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	p := k.PressState(label, base.HoverOn)
	h := theme.Snap(menuRowH * sc)
	hot := !disabled && (p.Hot > 0.5 || (p.Ring > 0.5))
	fg := r.Colors[theme.Label]
	var fill uint32
	flags := ui.Flags(0)
	switch {
	case disabled:
		fg = r.Colors[theme.TextDisabled]
	case hot:
		fill, fg = r.Colors[theme.Accent], r.Colors[theme.OnAccent]
		flags |= ui.DrawBackground
	}
	c.NextBG(fill)
	c.NextRadius(theme.Snap(6 * sc))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextPadding(theme.Snap(6*sc), 0, theme.Snap(6*sc), 0)
	c.NextGap(theme.Snap(4 * sc))
	k.BeginPress(&p, ui.RoleMenuItem, ui.Pct(1), ui.Px(h), flags, nil)
	box := theme.Snap(14 * sc)
	c.NextSize(ui.Px(box), ui.Px(h))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.Push(c.BoxKeyed(0, ui.KeyN(p.Key, 50), nil))
	if checked {
		k.Icon(icon.Check, fg)
	}
	c.Pop()
	st := &r.Styles[theme.StyleBody]
	c.NextSize(ui.Pct(1), ui.Px(st.Line))
	c.NextFont(st.Font)
	c.NextTextColor(fg)
	c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX, ui.KeyN(p.Key, 51), base.StrBytes(p.Name))
	if sub {
		k.Icon(icon.ChevronRight, fg)
	}
	sig := k.EndPress(&p)
	if disabled {
		sig.Clicked = false
	} else {
		nv.Arrive(c, p.Key)
		nv.At(c, p.Key, sig.Focused)
	}
	return sig
}

// rule is a hairline between groups of rows.
func (m *ctxMenu) rule(e *screen.Env) {
	k := e.K
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	c.NextPadding(theme.Snap(6*sc), theme.Snap(3*sc), theme.Snap(6*sc), theme.Snap(3*sc))
	c.NextSize(ui.Pct(1), ui.Children(1))
	m.rules++
	c.Push(c.BoxKeyed(0, ui.HashString(c.ParentKey(), "rule"+itoa(m.rules)), nil))
	c.NextSize(ui.Pct(1), ui.Px(r.Lines.Hairline))
	c.NextBG(r.Colors[theme.Separator])
	c.BoxKeyed(ui.DrawBackground, ui.HashString(c.ParentKey(), "line"), nil)
	c.Pop()
}

// subPanel draws a submenu's panel at the right of its row.
func (m *ctxMenu) subPanel(e *screen.Env, name string, items []ui.MenuItem) (int, bool) {
	k := e.K
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	pad := theme.Snap(5 * sc)
	c.NextBG(r.Colors[theme.Raised])
	c.NextBorder(r.Colors[theme.Separator], r.Lines.Hairline)
	c.NextRadius(theme.Snap(10 * sc))
	c.NextShadow(ui.Shadow{OffY: 4 * sc, Sigma: 12 * sc, Color: theme.WithAlpha(r.Colors[theme.Shadow], 0.35)})
	c.NextPadding(pad, pad, pad, pad)
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Px(theme.Snap(menuW*sc)+2*pad), ui.Children(1))
	c.NextRole(ui.RoleMenu)
	c.NextSemName(name)
	os := c.BeginOverlay(ui.KeyN(menuKey, uint64(m.sub)), ui.OverlayPopup, m.subAt, ui.PlaceRight)
	if os.Dismissed {
		m.sub = 0
		c.EndOverlay()
		return 0, false
	}
	var nv base.PopupNav
	var sub ctxMenu
	id, ok := sub.draw(e, &nv, items)
	nv.End(c)
	c.EndOverlay()
	return id, ok
}

// chatMenu describes the chat's menu, in upstream's order.
func (s *Screen) chatMenu(items *[]ui.MenuItem, e *screen.Env, a *state.Account, ref model.ChatRef) {
	ch, ok := a.Model.Chats.Get(ref)
	if !ok {
		return
	}
	id := ch.ID()
	now := e.S.Now().Unix()
	add := func(it ui.MenuItem) { *items = append(*items, it) }
	// Zap's ways to look at it beside what is open, first, as a browser's
	// "Open in a new tab"
	if a.Open != 0 && a.Open != ref && a.Split != ref && !ch.Locked() {
		add(ui.MenuItem{ID: miSplit, Title: i18n.T("Open beside the open chat"), Symbol: "rectangle.split.2x1"})
	}
	if !ch.Locked() {
		add(ui.MenuItem{ID: miFloat, Title: i18n.T("Float over other apps"), Symbol: "pip"})
		add(ui.MenuSeparator)
	}
	if ch.Unread() > 0 || ch.MarkedUnread() {
		add(ui.MenuItem{ID: miRead, Title: i18n.T("Mark as read"), Symbol: "checkmark.circle"})
	} else {
		add(ui.MenuItem{ID: miUnread, Title: i18n.T("Mark as unread"), Symbol: "bubble.left"})
	}
	if ch.Pinned() {
		add(ui.MenuItem{ID: miPin, Title: i18n.T("Unpin"), Symbol: "pin.slash"})
	} else {
		add(ui.MenuItem{ID: miPin, Title: i18n.T("Pin to top"), Symbol: "pin"})
	}
	if !isChannel(ch) { // channels cannot be favorites
		if ch.Favorite() {
			add(ui.MenuItem{ID: miFavorite, Title: i18n.T("Remove from favorites"), Symbol: "heart.slash"})
		} else {
			add(ui.MenuItem{ID: miFavorite, Title: i18n.T("Add to favorites"), Symbol: "heart"})
		}
	}
	if ch.Archived() {
		add(ui.MenuItem{ID: miArchive, Title: i18n.T("Unarchive"), Symbol: "archivebox"})
	} else {
		add(ui.MenuItem{ID: miArchive, Title: i18n.T("Archive"), Symbol: "archivebox"})
	}
	if s.canLeave(a, ch) {
		leave := i18n.T("Leave group")
		if isChannel(ch) {
			leave = i18n.T("Leave channel")
		}
		add(ui.MenuItem{ID: miLeave, Title: leave, Symbol: "rectangle.portrait.and.arrow.right"})
	}
	if ch.Muted(now) {
		add(ui.MenuItem{ID: miUnmute, Title: i18n.T("Unmute"), Symbol: "bell"})
	} else {
		add(ui.MenuItem{ID: miMute8h, Title: i18n.T("Mute for 8 hours"), Symbol: "bell.slash"})
		add(ui.MenuItem{ID: miMuteWeek, Title: i18n.T("Mute for a week"), Symbol: "bell.slash"})
		add(ui.MenuItem{ID: miMuteForever, Title: i18n.T("Mute indefinitely"), Symbol: "bell.slash"})
	}
	add(ui.MenuItem{Title: i18n.T("Notification sound"), Symbol: "speaker.wave.2", Sub: s.soundItems(ch)})
	add(ui.MenuItem{Title: i18n.T("Folder"), Symbol: "folder", Sub: s.folderItems(a, ch)})
	add(ui.MenuItem{Title: i18n.T("Labels"), Symbol: "tag", Sub: s.labelItems(a, ch)})
	if ch.Locked() {
		add(ui.MenuItem{ID: miLock, Title: i18n.T("Unlock chat"), Symbol: "lock.open"})
	} else {
		add(ui.MenuItem{ID: miLock, Title: i18n.T("Lock chat"), Symbol: "lock"})
	}
	add(ui.MenuSeparator)
	if model.Phone(id) != nil {
		add(ui.MenuItem{ID: miCopy, Title: i18n.T("Copy number"), Symbol: "doc.on.doc"})
	}
	add(ui.MenuItem{ID: miInfo, Title: i18n.T("Info"), Symbol: "info.circle"})
	// deleting reaches the phone, so it waits for a connection
	add(ui.MenuItem{ID: miDelete, Title: i18n.T("Delete chat"), Symbol: "trash", Destructive: true, Disabled: !a.Model.Link.Connected()})
}

// chatAct does what the chat's menu item id chose.
func (s *Screen) chatAct(e *screen.Env, a *state.Account, ref model.ChatRef, item int) {
	ch, ok := a.Model.Chats.Get(ref)
	if !ok {
		return
	}
	id := ch.ID()
	now := e.S.Now().Unix()
	mute := func(until int64, has bool) {
		cmd := a.Cmd(abi.CommandTagSetMuted)
		cmd.SetMuted.Chat, cmd.SetMuted.Until, cmd.SetMuted.HasUntil = id, until, has
		a.Send(cmd)
	}
	if s.chatFolderAct(a, ch, item) {
		return
	}
	switch {
	case item == miSplit:
		a.OpenSplit(ref)
	case item == miFloat:
		e.S.FloatChat(a, ref)
	case item == miRead:
		a.MarkRead(ref)
	case item == miUnread:
		cmd := a.Cmd(abi.CommandTagMarkUnread)
		cmd.MarkUnread.Chat = id
		a.Send(cmd)
	case item == miPin:
		s.setPinned(e, a, ch, !ch.Pinned())
	case item == miFavorite:
		cmd := a.Cmd(abi.CommandTagSetFavorite)
		cmd.SetFavorite.Chat, cmd.SetFavorite.Favorite = id, !ch.Favorite()
		a.Send(cmd)
	case item == miArchive:
		archive := !ch.Archived()
		cmd := a.Cmd(abi.CommandTagSetArchived)
		cmd.SetArchived.Chat, cmd.SetArchived.Archived = id, archive
		a.Send(cmd)
		if archive && a.Open == ref {
			a.OpenChat(0) // archiving closes the chat
		}
	case item == miLeave:
		e.S.OpenDialog(state.Dialog{Kind: state.DialogConfirmLeaveGroup, Chat: ref})
	case item == miUnmute:
		mute(0, false)
	case item == miMute8h:
		mute(now+8*3600, true)
	case item == miMuteWeek:
		mute(now+7*86400, true)
	case item == miMuteForever:
		mute(0, true)
	case item == miLock:
		if ch.Locked() {
			cmd := a.Cmd(abi.CommandTagSetLocked)
			cmd.SetLocked.Chat = id
			a.Send(cmd)
		} else {
			e.S.OpenDialog(state.Dialog{Kind: state.DialogConfirmLockChat, Chat: ref})
		}
	case item == miCopy:
		if ph := model.Phone(id); ph != nil {
			if set := e.C().ClipboardSet; set != nil {
				set(append([]byte{'+'}, ph...))
			}
		}
	case item == miInfo:
		a.OpenChat(ref) // the panel sits beside the conversation, so the chat opens first
		e.S.Panes.SetInfo(id)
	case item == miDelete:
		e.S.OpenDialog(state.Dialog{Kind: state.DialogConfirmDeleteChat, Chat: ref})
	case item == miSoundFile:
		cmd := a.Cmd(abi.CommandTagPickChatSound)
		cmd.PickChatSound.Chat = id
		a.Send(cmd)
	case item >= miSound && item < miSound+len(soundChoices):
		o := soundChoices[item-miSound]
		cmd := a.Cmd(abi.CommandTagSetChatSound)
		cmd.SetChatSound.Chat = id
		if o.set {
			cmd.SetChatSound.Sound.Tag, cmd.SetChatSound.HasSound = o.tag, true
		}
		a.Send(cmd)
	case item == miManageLabels:
		e.S.OpenDialog(state.Dialog{Kind: state.DialogLabels})
	case item >= miLabel:
		mir := s.mirror(a)
		i := item - miLabel
		if i >= len(mir.labels) {
			return
		}
		lid := mir.labels[i].id
		worn := ch.HasLabel([]byte(lid))
		var next [][]byte
		for j := 0; j < ch.Labels(); j++ {
			if l := ch.Label(j); !worn || string(l) != lid {
				next = append(next, append([]byte(nil), l...))
			}
		}
		if !worn {
			next = append(next, []byte(lid))
		}
		cmd := a.Cmd(abi.CommandTagSetChatLabels)
		cmd.SetChatLabels.Chat, cmd.SetChatLabels.Labels = id, next
		a.Send(cmd)
	}
}

// setPinned pins or unpins the chat; past the limit a toast says why
// (zf:src/app.rs:7515).
func (s *Screen) setPinned(e *screen.Env, a *state.Account, ch model.Chat, pin bool) {
	if pin {
		n := 0
		for _, ref := range a.Model.Chats.Order() {
			if a.Model.Chats.At(ref).Pinned() {
				n++
			}
		}
		if limit := a.Model.PinLimit(); n >= limit {
			e.S.Toast([]byte(i18n.Format(i18n.T("You can only pin {} chats"), "{}", itoa(limit))), false)
			return
		}
	}
	cmd := a.Cmd(abi.CommandTagSetPinned)
	cmd.SetPinned.Chat, cmd.SetPinned.Pinned = ch.ID(), pin
	a.Send(cmd)
}

// canLeave is Chat::can_leave: a group or channel we are still in
// (zf:src/model.rs:382).
func (s *Screen) canLeave(a *state.Account, ch model.Chat) bool {
	if ch.Left() {
		return false
	}
	if isChannel(ch) {
		return !ch.ReadOnly()
	}
	if !ch.Group() {
		return false
	}
	me := a.Model.Me.ID()
	if len(me) == 0 || ch.Decode(&s.dec) != nil || len(s.dec.Participants) == 0 {
		return true
	}
	lid := a.Model.Me.LID()
	for _, p := range s.dec.Participants {
		if bytes.Equal(p, me) || (len(lid) > 0 && bytes.Equal(p, lid)) {
			return true
		}
	}
	return false
}

type soundChoice struct {
	label func() string
	tag   abi.NotificationSoundTag
	set   bool
}

var soundChoices = [...]soundChoice{
	{func() string { return i18n.T("Default") }, 0, false},
	{func() string { return i18n.T("Pidgin") }, abi.NotificationSoundTagReceive, true},
	{func() string { return i18n.T("Pidgin alert") }, abi.NotificationSoundTagAlert, true},
	{func() string { return i18n.T("System sound") }, abi.NotificationSoundTagSystem, true},
	{func() string { return i18n.T("No sound") }, abi.NotificationSoundTagNone, true},
}

// soundItems are the chat's own notification sound choices.
func (s *Screen) soundItems(ch model.Chat) []ui.MenuItem {
	var cur abi.NotificationSound
	has := false
	if ch.Decode(&s.dec) == nil {
		cur, has = s.dec.NotificationSound, s.dec.HasNotificationSound
	}
	var out []ui.MenuItem
	for i, o := range soundChoices {
		mark := ui.MarkNone
		if (!o.set && !has) || (o.set && has && cur.Tag == o.tag) {
			mark = ui.MarkOn
		}
		out = append(out, ui.MenuItem{ID: miSound + i, Title: o.label(), Mark: mark})
	}
	if has && cur.Tag == abi.NotificationSoundTagCustom {
		name := filepath.Base(string(cur.Custom.Path))
		if name == "" || name == "." {
			name = i18n.T("Custom")
		}
		out = append(out, ui.MenuItem{ID: miCustom, Title: name, Mark: ui.MarkOn})
	}
	return append(out, ui.MenuItem{ID: miSoundFile, Title: i18n.T("Choose a file…")})
}

// labelItems are the labels with a check on those the chat wears, and
// "Manage labels…".
func (s *Screen) labelItems(a *state.Account, ch model.Chat) []ui.MenuItem {
	mir := s.mirror(a)
	var out []ui.MenuItem
	for i := range mir.labels {
		mark := ui.MarkNone
		if ch.HasLabel([]byte(mir.labels[i].id)) {
			mark = ui.MarkOn
		}
		out = append(out, ui.MenuItem{ID: miLabel + i, Title: mir.labels[i].name, Mark: mark})
	}
	if len(out) > 0 {
		out = append(out, ui.MenuSeparator)
	}
	return append(out, ui.MenuItem{ID: miManageLabels, Title: i18n.T("Manage labels…")})
}
