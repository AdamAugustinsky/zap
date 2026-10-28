package logic

import (
	"zapfast/abi"
	"zapfast/state"
)

func openIs(a *state.Account, chat []byte) bool { return a.IsOpen(chat) }

// reading reports whether the person is looking at the account's open chat:
// the account on screen, the window focused and unlocked, a chat view
// (zf:src/app.rs:2022, 2322).
func reading(s *state.State, a *state.Account) bool {
	return a.Active() && s.Focused && !s.Lock.Locked && (s.Page == state.PageChats || s.Page == state.PageCalls)
}

// chatUpdated marks the open chat read when a message arrives while it is
// being read, and takes a read chat's notifications away
// (zf:src/app.rs:4191-4212).
func (l *Logic) chatUpdated(a *state.Account, c *abi.Chat) {
	s := l.s
	if c.Unread == 0 {
		// Read here or on another device: its next message may notify at once.
		l.clearNotices(a, c.ID)
		return
	}
	if !reading(s, a) || !openIs(a, c.ID) || (c.Locked && a.Mode != state.ListLocked) {
		return
	}
	l.markRead(a, c.ID)
}

func (l *Logic) markRead(a *state.Account, chat []byte) {
	if ref := a.Model.Chats.Find(chat); ref != 0 {
		a.MarkRead(ref)
	}
	l.clearNotices(a, chat)
}

// focusFrame marks the open chat read when the window regains the focus
// (zf:src/app.rs:9136-9146), and tells each core whether the person is
// looking at it, so the phone keeps its notifications while they are not
// (zf:src/app.rs:8425-8436).
func (l *Logic) focusFrame(s *state.State) {
	looking := s.Focused && !s.Lock.Locked
	if looking && !l.focused {
		if a := s.Acct(); a != nil && (s.Page == state.PageChats || s.Page == state.PageCalls) {
			if c, ok := a.OpenChatView(); ok && c.Unread() > 0 {
				l.markRead(a, c.ID())
			}
		}
	}
	l.focused = looking
	for i, a := range s.Accounts {
		online := looking && i == s.Active
		if a.Conn == nil || (a.OnlineSent && a.Online == online) {
			continue
		}
		cmd := a.Cmd(abi.CommandTagSetOnline)
		cmd.SetOnline.Online = online
		if a.Send(cmd) == nil {
			a.Online, a.OnlineSent = online, true
		}
	}
}
