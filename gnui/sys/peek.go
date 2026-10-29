package sys

import (
	"gonativeui/app"
	"gonativeui/app/hotkey"
	"gonativeui/menu"
	"gonativeui/ui"

	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
)

// The floating peek from outside the window (ui/peek.Window, WinPeek): a
// clicked message notification, the Dock's menu and the global shortcut
// open a chat in a panel over the desktop, unread, without the main window.
// Where a peek would not do (the app or the chat is locked, the main window
// shows the chat already) they open the chat as before. The app going to
// the background closes the panel.

// The Dock menu's commands: one per unread chat, and the newest.
const (
	cmdDockPeekNewest menu.ID = 1101 + iota
	cmdDockPeek               // the first unread chat; the rest follow
)

// dockPeekMax is how many unread chats the Dock's menu lists.
const dockPeekMax = 5

// peekKey is the global shortcut's ID, and peekChord the shortcut: ⌃⌥Z
// ("Zap"), which no system shortcut holds.
const peekKey hotkey.ID = 1

var peekChord = hotkey.Shortcut{Key: 'Z', Mods: ui.ModCtrl | ui.ModAlt}

type peekState struct {
	// key says what the Dock menu was made from: the unread chats, and
	// whether it may name them. The menu is made again when it changes.
	key   []peekItem
	named bool
	made  bool
	refs  []state.PeekRef // the chats of the menu's commands, in order
	scan  []state.PeekRef
	// hotkey registers the global shortcut; nil: none (tests).
	hotkey func(hotkey.ID, hotkey.Shortcut) error
	// setMenu makes m the Dock's menu; nil: the app's.
	setMenu func(m *menu.Model)
}

type peekItem struct {
	acct *state.Account
	ref  model.ChatRef
	gen  uint32
}

// startPeek registers the global shortcut. Another app holding it leaves
// Zap without one (macOS does not say so).
func (y *System) startPeek() {
	reg := y.p.hotkey
	if reg == nil && y.app != nil {
		reg = hotkey.Register
	}
	if reg == nil {
		return
	}
	if err := reg(peekKey, peekChord); err != nil {
		logWarn("the peek shortcut", "err", err)
	}
}

// peekEvent handles the shortcut's press: it opens the newest unread chat
// in the panel, or closes the panel when it is up.
func (y *System) peekEvent(e *app.Event) bool {
	id, ok := hotkey.Pressed(e)
	if !ok || id != peekKey {
		return false
	}
	s := y.s
	if s == nil {
		return true
	}
	if s.FloatPeek.Open() {
		s.CloseFloatPeek()
		return true
	}
	a, ref := s.PeekTarget()
	y.peekAt(a, ref, nil)
	return true
}

// peekAt opens chat ref of a in the floating peek, or, where a peek would
// not do, the chat at msg in the main window. The app locked shows the lock
// screen.
func (y *System) peekAt(a *state.Account, ref model.ChatRef, msg []byte) {
	s := y.s
	if s.Lock.Locked {
		y.showMain()
		return
	}
	if a == nil || ref == 0 {
		y.showMain()
		return
	}
	if s.FloatPeekChat(a, ref) {
		return
	}
	if ch, ok := a.Model.Chats.Get(ref); ok {
		s.OpenMessageIn(a.ID, ch.ID(), msg)
	}
	y.showMain()
}

// syncDockMenu keeps the Dock's menu on the unread chats: "Peek at" each of
// the newest ones, or, while notifications may not name a chat (their
// content hidden, the app locked), one item that peeks at the newest.
func (y *System) syncDockMenu(s *state.State) {
	set := y.p.setMenu
	if set == nil && y.app != nil {
		set = func(m *menu.Model) { y.app.SetDockMenu(m, 0) }
	}
	if set == nil {
		return
	}
	named := s.Settings.NotificationText && !s.Lock.Locked
	y.p.scan = s.UnreadToPeek(y.p.scan, dockPeekMax)
	if y.p.made && named == y.p.named && sameItems(y.p.key, y.p.scan) {
		return
	}
	y.p.made, y.p.named = true, named
	y.p.key = y.p.key[:0]
	for _, r := range y.p.scan {
		y.p.key = append(y.p.key, peekItem{acct: r.Acct, ref: r.Ref, gen: r.Acct.Model.Chats.Gen()})
	}
	y.p.refs = append(y.p.refs[:0], y.p.scan...)
	m, err := dockMenu(s, y.p.refs, named)
	if err != nil {
		logWarn("the Dock menu", "err", err)
		return
	}
	set(m)
}

// sameItems reports whether the chats are the ones the menu was made from,
// and none of them has changed since (a new name).
func sameItems(key []peekItem, refs []state.PeekRef) bool {
	if len(key) != len(refs) {
		return false
	}
	for i, r := range refs {
		if key[i].acct != r.Acct || key[i].ref != r.Ref || key[i].gen != r.Acct.Model.Chats.Gen() {
			return false
		}
	}
	return true
}

func dockMenu(s *state.State, refs []state.PeekRef, named bool) (*menu.Model, error) {
	item := func(id menu.ID) menu.Item { return menu.Item{Kind: menu.ItemCommand, Command: id} }
	var cmds []menu.Command
	var items []menu.Item
	if named {
		for i, r := range refs {
			name := "?"
			if ch, ok := r.Acct.Model.Chats.Get(r.Ref); ok {
				name = model.S(chatName(r.Acct, ch))
			}
			id := cmdDockPeek + menu.ID(i)
			cmds = append(cmds, menu.Command{ID: id, Title: i18n.Format(i18n.T("Peek at {chat}"), "{chat}", name), Symbol: "eye"})
			items = append(items, item(id))
		}
	}
	if len(items) == 0 {
		title := i18n.T("Peek at the newest chat")
		if len(refs) > 0 {
			title = i18n.T("Peek at the newest unread chat")
		}
		cmds = append(cmds, menu.Command{ID: cmdDockPeekNewest, Title: title, Symbol: "eye"})
		items = append(items, item(cmdDockPeekNewest))
	}
	return menu.NewModel(cmds, []menu.Menu{{Title: "Zap", Items: items}}, nil)
}

// chatName is a chat's name as the list writes it.
func chatName(a *state.Account, ch model.Chat) []byte {
	if n := ch.Name(); len(n) > 0 {
		return n
	}
	return a.Model.DisplayName(ch.ID())
}

// peekCommand does what the Dock's menu chose.
func (y *System) peekCommand(e *app.Event) bool {
	id := menu.ID(e.ID)
	s := y.s
	switch {
	case s == nil:
		return false
	case id == cmdDockPeekNewest:
		a, ref := s.PeekTarget()
		y.peekAt(a, ref, nil)
		return true
	case id >= cmdDockPeek && id < cmdDockPeek+dockPeekMax:
		i := int(id - cmdDockPeek)
		if i >= len(y.p.refs) {
			return true
		}
		r := y.p.refs[i]
		y.peekAt(r.Acct, r.Ref, nil)
		return true
	}
	return false
}
