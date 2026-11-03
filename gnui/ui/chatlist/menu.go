package chatlist

import (
	"bytes"
	"path/filepath"

	"gonativeui/rexui"
	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The list's menus (zf:src/ui/chats.rs context_menu, labels::chat_menu,
// sound_menu) are described once as ui.MenuItems: a native menu where the app
// has them, else rexui's drawn menu with the same items. Symbol is the native
// menu's icon, Icon (Lucide) the drawn menu's.

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
	miFolderTint = 4000 // + 1 + the colour's index in state.SpaceTints (0: none)
)

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
		*items = append(*items, ui.MenuItem{ID: miMuteAll, Title: label, Symbol: "bell.slash", Icon: "bell-off"})
	case menuStar:
		*items = append(*items, ui.MenuItem{ID: miUnstar, Title: i18n.T("Unstar"), Symbol: "star.slash", Icon: "star-off"})
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

// ctxMenu runs the context menu of the component id: native where the app
// has native menus, drawn with the same items elsewhere (rexui.ContextMenu).
// Call it in every frame that builds the component.
func (s *Screen) ctxMenu(e *screen.Env, id ui.ID, kind menuKind, ref model.ChatRef, star int) {
	a := e.Acct()
	if item, ok := rexui.ContextMenu(e.K.Kit, id, func(m *[]ui.MenuItem) { s.menuItems(m, e, a, kind, ref, star) }); ok {
		s.menuAct(e, a, kind, ref, star, item)
		e.S.Touch()
	}
}

// chatMenu describes the chat's menu in WhatsApp's groups: where to look at
// it, what it is in the list (read, pinned, favourite, archived), how it
// notifies and where it is filed (mute, sound, folder, labels, lock), what
// it is (number, info), and leaving or deleting it, set apart in red.
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
	if a.Open != 0 && a.Open != ref && !a.InSplit(ref) && !ch.Locked() {
		add(ui.MenuItem{ID: miSplit, Title: i18n.T("Open beside the open chat"), Symbol: "rectangle.split.2x1", Icon: "columns-2"})
	}
	if !ch.Locked() {
		add(ui.MenuItem{ID: miFloat, Title: i18n.T("Float over other apps"), Symbol: "pip", Icon: "picture-in-picture-2"})
		add(ui.MenuSeparator)
	}
	if ch.Unread() > 0 || ch.MarkedUnread() {
		add(ui.MenuItem{ID: miRead, Title: i18n.T("Mark as read"), Symbol: "checkmark.circle", Icon: "circle-check"})
	} else {
		add(ui.MenuItem{ID: miUnread, Title: i18n.T("Mark as unread"), Symbol: "bubble.left", Icon: "message-circle"})
	}
	if ch.Pinned() {
		add(ui.MenuItem{ID: miPin, Title: i18n.T("Unpin"), Symbol: "pin.slash", Icon: "pin-off"})
	} else {
		add(ui.MenuItem{ID: miPin, Title: i18n.T("Pin to top"), Symbol: "pin", Icon: "pin"})
	}
	if !isChannel(ch) { // channels cannot be favorites
		if ch.Favorite() {
			add(ui.MenuItem{ID: miFavorite, Title: i18n.T("Remove from favorites"), Symbol: "heart.slash", Icon: "heart-off"})
		} else {
			add(ui.MenuItem{ID: miFavorite, Title: i18n.T("Add to favorites"), Symbol: "heart", Icon: "heart"})
		}
	}
	if ch.Archived() {
		add(ui.MenuItem{ID: miArchive, Title: i18n.T("Unarchive"), Symbol: "archivebox", Icon: "archive-restore"})
	} else {
		add(ui.MenuItem{ID: miArchive, Title: i18n.T("Archive"), Symbol: "archivebox", Icon: "archive"})
	}
	add(ui.MenuSeparator)
	if ch.Muted(now) {
		add(ui.MenuItem{ID: miUnmute, Title: i18n.T("Unmute"), Symbol: "bell", Icon: "bell"})
	} else {
		add(ui.MenuItem{Title: i18n.T("Mute notifications"), Symbol: "bell.slash", Icon: "bell-off", Sub: []ui.MenuItem{
			{ID: miMute8h, Title: i18n.T("Mute for 8 hours")},
			{ID: miMuteWeek, Title: i18n.T("Mute for a week")},
			{ID: miMuteForever, Title: i18n.T("Mute indefinitely")},
		}})
	}
	add(ui.MenuItem{Title: i18n.T("Notification sound"), Symbol: "speaker.wave.2", Icon: "volume-2", Sub: s.soundItems(ch)})
	add(ui.MenuItem{Title: i18n.T("Folder"), Symbol: "folder", Icon: "folder", Sub: s.folderItems(a, ch)})
	add(ui.MenuItem{Title: i18n.T("Labels"), Symbol: "tag", Icon: "tag", Sub: s.labelItems(a, ch)})
	if ch.Locked() {
		add(ui.MenuItem{ID: miLock, Title: i18n.T("Unlock chat"), Symbol: "lock.open", Icon: "lock-open"})
	} else {
		add(ui.MenuItem{ID: miLock, Title: i18n.T("Lock chat"), Symbol: "lock", Icon: "lock"})
	}
	add(ui.MenuSeparator)
	if model.Phone(id) != nil {
		add(ui.MenuItem{ID: miCopy, Title: i18n.T("Copy number"), Symbol: "doc.on.doc", Icon: "copy"})
	}
	add(ui.MenuItem{ID: miInfo, Title: i18n.T("Info"), Symbol: "info.circle", Icon: "info"})
	add(ui.MenuSeparator)
	if s.canLeave(a, ch) {
		leave := i18n.T("Leave group")
		if isChannel(ch) {
			leave = i18n.T("Leave channel")
		}
		add(ui.MenuItem{ID: miLeave, Title: leave, Symbol: "rectangle.portrait.and.arrow.right", Icon: "log-out", Destructive: true})
	}
	// deleting reaches the phone, so it waits for a connection
	add(ui.MenuItem{ID: miDelete, Title: i18n.T("Delete chat"), Symbol: "trash", Icon: "trash-2", Destructive: true, Disabled: !a.Model.Link.Connected()})
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
