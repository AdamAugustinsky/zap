package state

import "zapfast/model"

// Peek is a chat looked at without being read, as Arc's peek and Zen's
// glance look at a page: its messages in a panel over the window, and a line
// to answer in. Nothing a peek does marks the chat read or sends a read
// receipt: it loads the chat as hovering a row does (Account.Prefetch), and
// its reply is a plain SendText, which the core does not count as reading.
// Opening the chat from it is opening it.
type Peek struct {
	Acct string        // the chat's account (Account.ID); "" when none is open
	Ref  model.ChatRef // the chat
	// From is the window rectangle the peek came from (the row, the tab),
	// pixels: the panel grows from there.
	From [4]float32
}

// Open reports whether a peek is open.
func (p *Peek) Open() bool { return p.Acct != "" && p.Ref != 0 }

// PeekChat opens a peek at chat ref of account a, from the rectangle from.
func (s *State) PeekChat(a *Account, ref model.ChatRef, from [4]float32) {
	if a == nil || ref == 0 {
		return
	}
	if c, ok := a.Model.Chats.Get(ref); !ok || (c.Locked() && a.Mode != ListLocked) {
		return // a locked chat is not looked at outside its folder
	}
	a.Prefetch(ref)
	s.Peek = Peek{Acct: a.ID, Ref: ref, From: from}
	s.Touch()
}

// ClosePeek closes the peek.
func (s *State) ClosePeek() {
	s.Peek = Peek{}
	s.Touch()
}

// FloatPeekChat opens chat ref of account a in the floating peek (WinPeek),
// a panel over the desktop that comes forward without the main window. It
// reports false, and opens nothing, where a peek would not do: the app is
// locked, the chat is locked, or the main window already shows the chat
// (opening it there reads it as the window does).
func (s *State) FloatPeekChat(a *Account, ref model.ChatRef) bool {
	if a == nil || ref == 0 || s.Lock.Locked {
		return false
	}
	c, ok := a.Model.Chats.Get(ref)
	if !ok || c.Locked() {
		return false
	}
	if a.Index == s.Active && a.Open == ref {
		return false
	}
	a.Prefetch(ref)
	s.FloatPeek = Peek{Acct: a.ID, Ref: ref}
	s.OpenWindowAlone(WinPeek)
	return true
}

// CloseFloatPeek closes the floating peek and its panel.
func (s *State) CloseFloatPeek() {
	if !s.FloatPeek.Open() && !s.Win.Open[WinPeek] && !s.Win.Want[WinPeek] {
		return
	}
	s.FloatPeek = Peek{}
	s.CloseWindow(WinPeek)
}

// PeekTarget is the chat a glance from outside the window looks at: the
// newest unread one that counts (not archived, locked or muted), the account
// on screen first; with none, the newest chat of the account on screen.
func (s *State) PeekTarget() (*Account, model.ChatRef) {
	now := s.Now().Unix()
	if a := s.Acct(); a != nil {
		if ref := newestUnread(a, now); ref != 0 {
			return a, ref
		}
	}
	for _, a := range s.Accounts {
		if a.Index == s.Active {
			continue
		}
		if ref := newestUnread(a, now); ref != 0 {
			return a, ref
		}
	}
	if a := s.Acct(); a != nil {
		for _, ref := range a.Model.Chats.Order() {
			if c := a.Model.Chats.At(ref); !c.Archived() && !c.Locked() {
				return a, ref
			}
		}
	}
	return nil, 0
}

// UnreadToPeek appends to dst the unread chats that count, newest first, at
// most n of them, across the accounts (the account on screen first).
func (s *State) UnreadToPeek(dst []PeekRef, n int) []PeekRef {
	dst = dst[:0]
	now := s.Now().Unix()
	add := func(a *Account) {
		for _, ref := range a.Model.Chats.Order() {
			if len(dst) >= n {
				return
			}
			if c := a.Model.Chats.At(ref); counts(c, now) {
				dst = append(dst, PeekRef{Acct: a, Ref: ref})
			}
		}
	}
	if a := s.Acct(); a != nil {
		add(a)
	}
	for _, a := range s.Accounts {
		if a.Index != s.Active {
			add(a)
		}
	}
	return dst
}

// PeekRef is a chat of an account.
type PeekRef struct {
	Acct *Account
	Ref  model.ChatRef
}

func newestUnread(a *Account, now int64) model.ChatRef {
	for _, ref := range a.Model.Chats.Order() {
		if counts(a.Model.Chats.At(ref), now) {
			return ref
		}
	}
	return 0
}

// counts is whether c is an unread chat the Dock's badge counts.
func counts(c model.Chat, now int64) bool {
	return !c.Archived() && !c.Locked() && !c.Muted(now) && (c.Unread() > 0 || c.MarkedUnread())
}
