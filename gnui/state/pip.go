package state

import "zapfast/model"

// Pip is a chat kept floating over other apps, as a video's picture in
// picture (WinPip, ui/chatview.Pip): its transcript and a composer in a
// small window that stays over everything, on every Space, while the
// person works elsewhere. Minimized, it is a small floating Zap icon with
// the chat's unread count. It is read while its window has the focus (the
// person is in it), never only because it shows (logic).
type Pip struct {
	Acct string        // the chat's account (Account.ID); "" for none
	Ref  model.ChatRef // the chat
	Mini bool          // shrunk to the floating icon
	// Focused is whether its window has the keyboard: the chat is being
	// read (the window sets it every frame).
	Focused bool
}

// Open reports whether a chat floats.
func (p *Pip) Open() bool { return p.Acct != "" && p.Ref != 0 }

// FloatChat keeps chat ref of account a floating over other apps, in place
// of the chat that floated. The app or the chat locked refuses it.
func (s *State) FloatChat(a *Account, ref model.ChatRef) bool {
	if a == nil || s.Lock.Locked {
		return false
	}
	c, ok := a.Model.Chats.Get(ref)
	if !ok || c.Locked() {
		return false
	}
	s.Pip = Pip{Acct: a.ID, Ref: ref}
	a.loadSide(c)
	a.pinConversations()
	s.OpenWindowAlone(WinPip)
	s.Touch()
	return true
}

// ClosePip ends the floating chat and closes its window.
func (s *State) ClosePip() {
	if !s.Pip.Open() && !s.Win.Open[WinPip] && !s.Win.Want[WinPip] {
		return
	}
	acct := s.AccountByID(s.Pip.Acct)
	s.Pip = Pip{}
	if acct != nil {
		acct.pinConversations()
	}
	s.CloseWindow(WinPip)
}

// SetPipMini shrinks the floating chat to its icon (on) or brings it back.
func (s *State) SetPipMini(on bool) {
	if s.Pip.Mini == on {
		return
	}
	s.Pip.Mini = on
	if on {
		s.Pip.Focused = false
	}
	s.Touch()
}
