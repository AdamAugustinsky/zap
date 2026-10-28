package state

import (
	"bytes"
	"time"

	"zapfast/model"
)

// OnUnlinked adds f to what runs when an account's number is unlinked (the
// LoggedOut link status), after the model forgot the session and before
// the account's open chat closes: a screen drops what it keeps of the
// account's chats (the composer its text, attachments and asides, without
// storing them; Settings its privacy and storage answers). It runs for an
// account that is not on screen too. Register in Init with the screen as
// owner.
func (s *State) OnUnlinked(owner any, f func(s *State, a *Account)) {
	s.hooks.unlinked = add(s.hooks.unlinked, owner, f)
}

// loggedOut ends the session of the account whose number was unlinked
// (zf:src/app.rs:4128-4174). The model has already forgotten the chats,
// conversations, contacts, avatars, drafts, stars, pins, privacy and
// answers (model.Store's endSession). The state drops what pointed into
// them: the lookups, the open chat (its text is not stored: unsent text
// belongs to the number that left), the starred list, the played receipts,
// and, while the account is on screen, the window's composer requests,
// attachments, quote, selection, side panes, history, dialog, voice run
// and viewer. The account gets a new Serial, so every cache keyed by it
// (thumbnails, pictures, names, a call's frames) misses from now on.
// Notifications are package sys's (it withdraws the account's on the same
// event); the app lock is ui/lock's.
func (s *State) loggedOut(a *Account, live bool) {
	if live {
		// A refused send's text and a link's are the old number's: they must
		// not reach the archive through the chat's close.
		s.Req.Restore.Chat, s.Req.Restore.Text = s.Req.Restore.Chat[:0], s.Req.Restore.Text[:0]
		s.Req.Prefill.Chat, s.Req.Prefill.Text = s.Req.Prefill.Chat[:0], s.Req.Prefill.Text[:0]
		s.Req.Insert = s.Req.Insert[:0]
	}
	for _, h := range s.hooks.unlinked {
		h.f(s, a)
	}
	s.chatGoing(a)
	if live {
		// The composer's hook has given back the staged pictures.
		s.Staged = s.Staged[:0]
		s.clearAccountUI()
		s.Forwarding = Forward{}
		s.Voice = Voice{}
		if s.Win.Want[WinViewer] || s.Win.Open[WinViewer] {
			s.CloseWindow(WinViewer)
		}
		a.setOpen(0)
	} else {
		// A hidden account's chat was parked, and its hooks ran, as it left
		// the screen.
		a.Open = 0
		a.pinConversations()
	}
	a.openID = a.openID[:0]
	a.Mode = ListChats
	a.Played.Reset()
	a.starting, a.startBy = a.starting[:0], time.Time{}
	a.unreadRef, a.unreadAt = 0, 0
	a.prefetchSent, a.prefetchChat = false, 0
	a.OnlineSent = false
	// The chat open at quit was the old number's.
	a.restored = true
	if len(a.Settings.LastChat) > 0 {
		a.Settings.LastChat = a.Settings.LastChat[:0]
		a.SettingsChanged()
	}
	old := a.Serial
	a.Serial = accountSerial.Add(1)
	s.accountGone(old)
	// Upstream's words, untranslated as there (not in its catalog).
	a.Toast(unlinkedToast, true)
	s.gen++
}

var unlinkedToast = []byte("This device was unlinked from your phone")

// dropChatUI closes what is on screen of the chats hidden reports on, a
// chat known by its ref or, in the info panel, by its id: the info panel,
// the viewer and a dialog. A removed chat's go (chatRemoved), and a locked
// chat's outside the open folder (hideLocked).
func (s *State) dropChatUI(hidden func(ref model.ChatRef, id []byte) bool) {
	if p := &s.Panes; p.Info.Len() > 0 && hidden(0, p.Info.At(0)) {
		p.SetInfo(nil)
	}
	if w := &s.Win; w.ViewerChat != 0 && hidden(w.ViewerChat, nil) {
		s.CloseWindow(WinViewer)
		w.ViewerChat = 0
		w.ViewerMsg.Reset()
		w.ViewerPlay, w.ViewerAt = false, 0
	}
	if d := &s.Dialog; d.Open() && d.Chat != 0 && hidden(d.Chat, nil) {
		s.CloseDialog()
	}
}

// chatRemoved drops what the state kept of a chat the core removed, as
// upstream's forget_chat and leave_chat (zf:src/app.rs:2235-2316); the model
// has already forgotten the chat itself (model.Store's forgetChat). Its
// notifications go; its open chat closes without its text being stored (the
// composer listens to ChatRemoved and drops it first); the last chat, the
// unread divider and the prefetch stop naming it; and, while the account is
// on screen, the history forgets it and the info panel, the viewer, a
// dialog, the forward recipients, the jump, the voice run and the composer
// requests that concern it go.
func (s *State) chatRemoved(a *Account, chat []byte, live bool) {
	ref := a.removed
	a.removed = 0
	s.NoticeClears = append(s.NoticeClears, NoticeClear{Account: a.ID, Chat: append([]byte(nil), chat...)})
	open := a.Open != 0 && (a.Open == ref || bytes.Equal(a.openID, chat))
	if live {
		s.dropChatUI(func(r model.ChatRef, id []byte) bool {
			return r != 0 && r == ref || r == 0 && bytes.Equal(id, chat)
		})
		p := &s.Panes
		s.Forwarding.Recipients.Remove(chat)
		if p.Jump.Chat == ref {
			p.Jump.Chat = 0
			p.Jump.Msg.Reset()
		}
		if s.Voice.Chat != 0 && s.Voice.Chat == ref {
			s.Voice = Voice{}
		}
		for _, r := range [...]*Prefill{&s.Req.Restore, &s.Req.Prefill} {
			if bytes.Equal(r.Chat, chat) {
				r.Chat, r.Text, r.Acct = r.Chat[:0], r.Text[:0], 0
			}
		}
		if ref != 0 {
			s.history.forget(ref)
		}
	}
	if open {
		s.chatGoing(a)
		if live {
			p := &s.Panes
			p.Reply.Reset()
			p.ReplyChat.Reset()
			p.Edit.Reset()
			p.Picker = PickerNone
			a.openChat(0, false) // the open chat's selection and side panes go too
		} else {
			a.Open = 0
			a.pinConversations()
		}
		a.openID = a.openID[:0]
	}
	if a.unreadRef == ref {
		a.unreadRef, a.unreadAt = 0, 0
	}
	if a.prefetchChat == ref {
		a.prefetchSent = false
	}
	if bytes.Equal(a.Settings.LastChat, chat) {
		a.Settings.LastChat = a.Settings.LastChat[:0]
		a.SettingsChanged()
	}
	s.gen++
}
