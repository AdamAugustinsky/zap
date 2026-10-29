package shell

import (
	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The keyboard layer (Linear's): while nothing takes typing, single keys
// act on the open chat and the list. Nothing takes typing when no text field
// has the focus (the composer, a search field), no dialog, menu or picker is
// open, and the app is not locked; in the composer every key is the text's.
//
// A person typing and a person pressing a shortcut differ in rhythm: a
// shortcut is one key and a pause, typing a burst. So a letter waits
// burstNs before it acts: if another letter follows within that, the keys
// are typing, and with a chat open they go into its composer (type to
// compose), the letters already pressed included; otherwise the one key is
// its shortcut. A G waits chordNs for its second key (G then A: Archived).
//
// What a key changes in a chat (archive, mute, pin, unread) says so in a
// toast, and Z puts it back.
type keyboard struct {
	held    []byte // the letters waiting (their text, as typed)
	codes   []ui.KeyCode
	firstAt int64 // when the first of them went down, platform ns
	lastAt  int64 // and the last
	chord   bool  // a G waits for its second key
	chordAt int64
	undo    undoStep
}

const (
	// burstNs is how soon a second letter makes the first typing: under the
	// gap between a shortcut and the next one, over the gap between two
	// letters of a word (about 60–150 ms at speed).
	burstNs = 120_000_000
	// chordNs is how long a G waits for its second key.
	chordNs = 1_000_000_000
)

// undoStep is what Z puts back: the last change a key made to a chat.
type undoStep struct {
	acct string
	ref  model.ChatRef
	kind byte // 'e' archive, 'm' mute, 'p' pin, 'u' unread
	was  bool // the state before
	set  bool
}

// keys runs the layer once a frame in the main window.
func (sh *Shell) keys(e *screen.Env) {
	s, kb := sh.S, &sh.kb
	c := e.C()
	a := s.Acct()
	in := c.Input()
	if a == nil || s.Lock.Locked || c.TextFieldFocused() || c.ModalOpen() || s.Dialog.Open() || s.Panes.Picker != state.PickerNone || s.AccountMenu || s.Spotlight || s.Peek.Open() {
		kb.held, kb.codes, kb.chord = kb.held[:0], kb.codes[:0], false
		return
	}
	now := in.Time
	// / searches everything (Linear's)
	if len(kb.codes) == 0 && c.TakeKey('/', 0) {
		s.Spotlight = true
		s.Touch()
		return
	}
	// this frame's keys: letters and Space, unmodified (Shift is a capital)
	text := in.Text[:in.NText]
	ti := 0
	for i := 0; i < in.NKeys; i++ {
		k := in.Keys[i]
		if !k.Down || k.Repeat || k.Mods&^ui.ModShift != 0 {
			continue
		}
		// a Space inside a burst is the typing's; alone it is a row's (peek)
		space := k.Code == ui.KeySpace && len(kb.codes) > 0
		if !letter(k.Code) && !space {
			continue
		}
		at := k.Time
		if at == 0 {
			at = now
		}
		// the key's text: one rune of the frame's typing, in order
		r, n := nextRune(text[ti:])
		ti += n
		if kb.chord {
			kb.chord = false
			c.TakeKey(k.Code, k.Mods)
			sh.chordKey(e, a, k.Code)
			continue
		}
		if len(kb.codes) > 0 && at-kb.lastAt < burstNs {
			// a burst: typing
			kb.held = append(kb.held, r...)
			kb.codes = append(kb.codes, k.Code)
			kb.lastAt = at
			c.TakeKey(k.Code, k.Mods)
			continue
		}
		if len(kb.codes) > 0 {
			sh.resolve(e, a) // the one before was a shortcut
		}
		kb.held = append(kb.held[:0], r...)
		kb.codes = append(kb.codes[:0], k.Code)
		kb.firstAt, kb.lastAt = at, at
		c.TakeKey(k.Code, k.Mods)
	}
	if kb.chord && now-kb.chordAt > chordNs {
		kb.chord = false
	}
	if len(kb.codes) == 0 {
		return
	}
	switch {
	case len(kb.codes) > 1:
		// typing: into the open chat's composer, or with none open a search
		// over everything
		if a.Open != 0 && s.Page == state.PageChats {
			s.Req.Insert = append(s.Req.Insert, kb.held...)
			s.Req.FocusComposer = true
		} else {
			s.Req.SpotlightText = append(s.Req.SpotlightText, kb.held...)
			s.Spotlight = true
		}
		s.Touch()
		kb.held, kb.codes = kb.held[:0], kb.codes[:0]
	case now-kb.lastAt >= burstNs:
		sh.resolve(e, a)
	default:
		c.WakeIn(burstNs - (now - kb.lastAt)) // back when the wait is over
	}
}

// resolve runs the one held key as its shortcut.
func (sh *Shell) resolve(e *screen.Env, a *state.Account) {
	kb := &sh.kb
	code := kb.codes[0]
	kb.held, kb.codes = kb.held[:0], kb.codes[:0]
	sh.shortcut(e, a, code)
}

// shortcut is what a single key does.
func (sh *Shell) shortcut(e *screen.Env, a *state.Account, code ui.KeyCode) {
	s, kb := sh.S, &sh.kb
	switch code {
	case 'J':
		sh.stepChat(a, 1)
	case 'K':
		sh.stepChat(a, -1)
	case 'G':
		kb.chord, kb.chordAt = true, e.C().Input().Time
		return
	case 'E':
		sh.chatToggle(e, a, 'e')
	case 'M':
		sh.chatToggle(e, a, 'm')
	case 'P':
		sh.chatToggle(e, a, 'p')
	case 'U':
		sh.chatToggle(e, a, 'u')
	case 'R':
		sh.replyToLast(a)
	case 'Z':
		sh.undoLast(e)
	default:
		return
	}
	s.Touch()
}

// chordKey is the key after a G: where to go.
func (sh *Shell) chordKey(e *screen.Env, a *state.Account, code ui.KeyCode) {
	s := sh.S
	switch code {
	case 'I': // the inbox: the chats
		s.Page = state.PageChats
		a.SetMode(state.ListChats)
		s.Panes.ListHidden = false
	case 'A':
		s.Page = state.PageChats
		a.SetMode(state.ListArchived)
		s.Panes.ListHidden = false
	case 'S':
		s.Page = state.PageChats
		a.SetMode(state.ListStarred)
		s.Panes.ListHidden = false
	case 'C':
		s.Page = state.PageCalls
	default:
		return
	}
	s.Touch()
}

// chatToggle flips one of the open chat's states, says so, and keeps the way
// back for Z.
func (sh *Shell) chatToggle(e *screen.Env, a *state.Account, kind byte) {
	ch, ok := a.Model.Chats.Get(a.Open)
	if !ok || a.Open == 0 {
		return
	}
	var was bool
	switch kind {
	case 'e':
		was = ch.Archived()
	case 'm':
		was = ch.Muted(sh.S.Now().Unix())
	case 'p':
		was = ch.Pinned()
	case 'u':
		was = ch.Unread() > 0 || ch.MarkedUnread()
	}
	sh.setChat(a, ch, kind, !was)
	sh.kb.undo = undoStep{acct: a.ID, ref: a.Open, kind: kind, was: was, set: true}
	a.Toast(sh.toggled(ch, kind, !was), false)
}

// setChat puts a chat's state to on.
func (sh *Shell) setChat(a *state.Account, ch model.Chat, kind byte, on bool) {
	id := ch.ID()
	switch kind {
	case 'e':
		cmd := a.Cmd(abi.CommandTagSetArchived)
		cmd.SetArchived.Chat, cmd.SetArchived.Archived = id, on
		a.Send(cmd)
	case 'm':
		cmd := a.Cmd(abi.CommandTagSetMuted)
		cmd.SetMuted.Chat = id
		if on {
			cmd.SetMuted.Until, cmd.SetMuted.HasUntil = sh.S.Now().Unix()+8*3600, true
		}
		a.Send(cmd)
	case 'p':
		cmd := a.Cmd(abi.CommandTagSetPinned)
		cmd.SetPinned.Chat, cmd.SetPinned.Pinned = id, on
		a.Send(cmd)
	case 'u':
		if on {
			cmd := a.Cmd(abi.CommandTagMarkUnread)
			cmd.MarkUnread.Chat = id
			a.Send(cmd)
		} else {
			a.MarkRead(ch.Ref())
		}
	}
}

// toggled is the toast for a change: what happened, and Z.
func (sh *Shell) toggled(ch model.Chat, kind byte, on bool) []byte {
	var what string
	switch {
	case kind == 'e' && on:
		what = i18n.T("Archived")
	case kind == 'e':
		what = i18n.T("Unarchived")
	case kind == 'm' && on:
		what = i18n.T("Muted for 8 hours")
	case kind == 'm':
		what = i18n.T("Unmuted")
	case kind == 'p' && on:
		what = i18n.T("Pinned")
	case kind == 'p':
		what = i18n.T("Unpinned")
	case kind == 'u' && on:
		what = i18n.T("Marked as unread")
	default:
		what = i18n.T("Marked as read")
	}
	return []byte(i18n.Format(i18n.T("{what}: {chat}. Z to undo"), "{what}", what, "{chat}", model.S(chatName(sh.S, ch))))
}

// undoLast puts back the last change a key made.
func (sh *Shell) undoLast(e *screen.Env) {
	u := &sh.kb.undo
	if !u.set {
		return
	}
	u.set = false
	for _, a := range sh.S.Accounts {
		if a.ID != u.acct {
			continue
		}
		if ch, ok := a.Model.Chats.Get(u.ref); ok {
			sh.setChat(a, ch, u.kind, u.was)
			a.Toast(sh.toggled(ch, u.kind, u.was), false)
		}
	}
}

// replyToLast replies to the newest message from someone else in the open
// chat: the composer takes the keyboard with it quoted.
func (sh *Shell) replyToLast(a *state.Account) {
	s := sh.S
	v := a.Model.Conversation(nil)
	if ch, ok := a.Model.Chats.Get(a.Open); ok {
		v = a.Model.Conversation(ch.ID())
	}
	if v == nil {
		return
	}
	for i := v.Len() - 1; i >= 0; i-- {
		if m := v.At(i); !m.FromMe() {
			s.Panes.Edit.Reset()
			s.Panes.Reply.Reset()
			s.Panes.Reply.Add(m.ID())
			s.Req.FocusComposer = true
			return
		}
	}
}

// chatName is how a chat is named in a toast.
func chatName(s *state.State, ch model.Chat) []byte {
	if n := ch.Name(); len(n) > 0 {
		return n
	}
	if a := s.Acct(); a != nil {
		return a.Model.DisplayName(ch.ID())
	}
	return ch.ID()
}

// letter reports whether code is one the layer reads: a letter.
func letter(code ui.KeyCode) bool { return code >= 'A' && code <= 'Z' }

// nextRune is the first rune of b, as bytes, and its length; a key with no
// text (a dead key's first press) gives none.
func nextRune(b []byte) ([]byte, int) {
	if len(b) == 0 {
		return nil, 0
	}
	n := 1
	for n < len(b) && b[n]&0xC0 == 0x80 {
		n++
	}
	return b[:n], n
}
