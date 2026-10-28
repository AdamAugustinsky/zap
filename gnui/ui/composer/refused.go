package composer

import (
	"bytes"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

// A send the core refused comes back where it was written
// (zf:src/app.rs:4630-4790 send_refused): its text (package state puts a
// refused text or location in Requests.Restore; a caption is the
// composer's), its attachments, its voice message, and the reply it quoted.
// Package state toasts why.

// refused takes back what a refused send held, for chat in account a.
func (s *Screen) refused(a *state.Account, r *abi.EventSendRefused) {
	// A private reply's recovery needs its chat to still be there.
	if r.HasQuoting && r.Quoting.HasChat && a.Model.Chats.Find(r.Chat) == 0 {
		s.freeUnsent(a, &r.Unsent)
		return
	}
	held := s.holds(a, r.Chat)
	if r.HasQuoting {
		s.rearmQuote(a, r.Chat, &r.Quoting)
	}
	u := &r.Unsent
	switch u.Tag {
	case abi.UnsentTagVoice:
		s.voice.refused(s, a, r.Chat, u.Voice.Samples, nil)
	case abi.UnsentTagFiles:
		kind := state.StagedFile
		if u.Files.Documents {
			kind = state.StagedDocument
		}
		items := make([]state.Staged, 0, len(u.Files.Paths))
		for _, p := range u.Files.Paths {
			items = append(items, s.newStaged(kind, p))
		}
		s.restoreStaged(a, r, items, held)
		s.restoreText(a, r.Chat, u.Files.Caption, held)
	case abi.UnsentTagImage:
		s.nextID++
		pic := state.Staged{ID: s.nextID, Kind: state.StagedPicture, Name: i18n.T("Photo"),
			W: u.Image.Width, H: u.Image.Height, Blob: u.Image.RGBA, Ready: true}
		if !s.restoreStaged(a, r, []state.Staged{pic}, held) && a.Conn != nil {
			a.Conn.FreeBlob(u.Image.RGBA.ID)
		}
		s.restoreText(a, r.Chat, u.Image.Caption, held)
	}
}

// freeUnsent gives back the core blobs of a refusal nobody takes.
func (s *Screen) freeUnsent(a *state.Account, u *abi.Unsent) {
	if a.Conn == nil {
		return
	}
	switch u.Tag {
	case abi.UnsentTagVoice:
		a.Conn.FreeBlob(u.Voice.Samples.ID)
	case abi.UnsentTagImage:
		a.Conn.FreeBlob(u.Image.RGBA.ID)
	}
}

// holds reports whether the composer holds chat of a, open on screen.
func (s *Screen) holds(a *state.Account, chat []byte) bool {
	return a.Active() && a == s.acct && len(s.chat) > 0 && bytes.Equal(chat, s.chat)
}

// openHere reports whether chat is a's open chat on screen (the composer
// may not hold it yet: it follows at the next frame).
func openHere(a *state.Account, chat []byte) bool {
	if !a.Active() {
		return false
	}
	c, ok := a.Model.Chats.Get(a.Open)
	return ok && bytes.Equal(c.ID(), chat)
}

// rearmQuote shows the refused send's reply again, unless another reply or
// an edit has taken its place since; a private reply's quote for a chat not
// on screen waits in that chat's aside.
func (s *Screen) rearmQuote(a *state.Account, chat []byte, q *abi.ReplyTarget) {
	p := &s.state.Panes
	switch {
	case openHere(a, chat):
		if p.Reply.Len() == 0 && p.Edit.Len() == 0 {
			p.Reply.Add(q.ID)
			p.ReplyChat.Reset()
			if q.HasChat {
				p.ReplyChat.Add(q.Chat)
			}
			s.state.Req.FocusComposer = true
			s.state.Touch()
		}
	case q.HasChat:
		as := s.asides.get(a, chat, true)
		if len(as.reply) == 0 {
			as.reply = append(as.reply[:0], q.ID...)
			as.replyChat = append(as.replyChat[:0], q.Chat...)
		}
	}
}

// restoreStaged gives a refused send's attachments back: to the composer
// when it holds the chat, else to the chat's aside, with the reply they
// carried; false when the chat is gone.
func (s *Screen) restoreStaged(a *state.Account, r *abi.EventSendRefused, items []state.Staged, held bool) bool {
	if held {
		s.state.Staged = append(s.state.Staged, items...)
		s.st.Focus(s.env.K.Kit)
		s.state.Touch()
		return true
	}
	if a.Model.Chats.Find(r.Chat) == 0 {
		return false
	}
	as := s.asides.get(a, r.Chat, true)
	as.staged = append(as.staged, items...)
	if r.HasQuoting && len(as.reply) == 0 && !openHere(a, r.Chat) {
		as.reply = append(as.reply[:0], r.Quoting.ID...)
		as.replyChat = append(as.replyChat[:0], r.Quoting.Chat...)
	}
	return true
}

// restoreText puts refused text back (upstream's restore_text): into the
// composer holding its chat when it is empty and not editing, else into the
// chat's draft when that is empty. Text typed since is kept.
func (s *Screen) restoreText(a *state.Account, chat, text []byte, held bool) {
	if !hasContent(text) {
		return
	}
	if held {
		if len(s.edit) == 0 && !hasContent(s.st.Text()) && s.st.SetText(text) == nil {
			s.prefill.text = s.prefill.text[:0]
			s.mentions = s.mentions[:0]
			s.comp.close(s)
			s.seen()
			s.styleMentions(s.env)
			s.st.Focus(s.env.K.Kit)
			s.flush(a, chat, text)
			s.state.Touch()
		}
		return
	}
	if !hasContent(a.Model.Draft(chat)) {
		s.flush(a, chat, text)
	}
}

// takeRestore takes a refused text package state asks the open chat's
// composer to restore (Requests.Restore), or drops it.
func (s *Screen) takeRestore(e *screen.Env, a *state.Account) {
	r := &e.S.Req.Restore
	if len(r.Chat) == 0 {
		return
	}
	if r.Acct == a.Serial && a == s.acct && bytes.Equal(r.Chat, s.chat) {
		s.restoreText(a, r.Chat, r.Text, true)
	}
	r.Chat, r.Text = r.Chat[:0], r.Text[:0]
}
