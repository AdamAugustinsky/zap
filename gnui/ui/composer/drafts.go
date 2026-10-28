package composer

import (
	"bytes"
	"time"

	"zapfast/abi"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// mention is a member the composer's text mentions by name ("@Ada"), which
// a send turns into the wire token ("@15551234567") (zf:src/app.rs:959).
type mention struct{ id, name []byte }

// A chat's unsent text is the model's draft (Store.Draft): the composer
// keeps it there as the text changes, and in the archive (SaveDraft) when
// the chat closes, after a pause, at the lock and on a send, so that it
// survives a restart. What else a chat's composer holds while another chat
// is open waits in its aside (zf:src/app.rs:1552-1600, :5162-5220).

// aside is a chat's mentions, its staged attachments and the reply they
// carry, while another chat is open.
type aside struct {
	mentions         []mention
	staged           []state.Staged
	reply, replyChat []byte
}

// asides are the chats' asides, by account and chat.
type asides struct {
	m map[*state.Account]map[string]*aside
}

func (d *asides) init() {
	// Parked drafts belong to the accounts, not to the recreated window.
	if d.m == nil {
		d.m = map[*state.Account]map[string]*aside{}
	}
}

// get is the chat's aside; with make, one is made when there is none.
func (d *asides) get(a *state.Account, chat []byte, make bool) *aside {
	m := d.m[a]
	if m == nil {
		if !make {
			return nil
		}
		m = map[string]*aside{}
		d.m[a] = m
	}
	as := m[string(chat)]
	if as == nil && make {
		as = &aside{}
		m[string(chat)] = as
	}
	return as
}

// staged is attachment id waiting in a chat's aside, in any account; nil
// for none.
func (d *asides) staged(id uint64) *state.Staged {
	for _, m := range d.m {
		for _, as := range m {
			for i := range as.staged {
				if as.staged[i].ID == id {
					return &as.staged[i]
				}
			}
		}
	}
	return nil
}

// unlinked forgets what the composer keeps of account a, whose number was
// unlinked (zf:src/app.rs:4160-4172: drafts, draft_mentions,
// attachment_drafts, and the composer, reply and pending attachments): every
// chat's aside, its staged pictures given back to the core, and, when it
// holds one of a's chats, its text, mentions, quote, edit, recording, link
// text and attachments. Nothing is stored: the text belonged to the number
// that left.
func (s *Screen) unlinked(a *state.Account) {
	s.voice.forget(s, a, nil)
	if m := s.asides.m[a]; m != nil {
		for _, as := range m {
			s.freeAside(a, as)
		}
		delete(s.asides.m, a)
	}
	if s.acct != a {
		return
	}
	s.dropHeld(a)
	s.acct, s.ref, s.rowOK, s.rowRef = nil, 0, false, 0
}

// chatRemoved forgets what the composer keeps of account a's chat that the
// core removed (zf:src/app.rs:2244-2246, :2299-2315: drafts,
// draft_mentions, attachment_drafts, and the composer when it is the open
// chat), before the state closes it, so nothing of it is stored.
func (s *Screen) chatRemoved(a *state.Account, chat []byte) {
	s.voice.forget(s, a, chat)
	if m := s.asides.m[a]; m != nil {
		if as := m[string(chat)]; as != nil {
			s.freeAside(a, as)
			delete(m, string(chat))
		}
	}
	if s.acct == a && len(s.chat) > 0 && bytes.Equal(s.chat, chat) {
		s.dropHeld(a)
		s.ref, s.rowOK, s.rowRef = 0, false, 0
	}
}

// freeAside gives an aside's staged pictures back to the core, and forgets
// their failures.
func (s *Screen) freeAside(a *state.Account, as *aside) {
	for i := range as.staged {
		delete(s.paste.failed, as.staged[i].ID)
		freeStaged(a, &as.staged[i])
	}
}

// dropHeld empties the composer of the chat it holds, storing nothing.
func (s *Screen) dropHeld(a *state.Account) {
	s.pendingPicks = s.pendingPicks[:0]
	if len(s.chat) > 0 {
		s.voice.discard(s)
		s.clearStaged(a)
		s.composing, s.lastKey = false, time.Time{}
		s.timer.Stop()
		s.edit, s.reply, s.replyChat = s.edit[:0], s.reply[:0], s.replyChat[:0]
		s.replyWho, s.replyText = "", s.replyText[:0]
		s.prefill.text, s.dirty = s.prefill.text[:0], false
		s.paste.sendQueued, s.tools = false, false
		s.clearText()
		s.chat = s.chat[:0]
	}
}

// follow parks the chat the composer held and restores the open chat's,
// when they differ.
func (s *Screen) follow(e *screen.Env, a *state.Account, ch model.Chat) {
	if s.acct == a && s.ref == ch.Ref() && bytes.Equal(s.chat, ch.ID()) {
		return
	}
	if s.acct != nil && len(s.chat) > 0 {
		s.park(e)
	}
	s.acct, s.ref = a, ch.Ref()
	s.chat = append(s.chat[:0], ch.ID()...)
	s.restore(e, a)
}

// park stores the composer's text as its chat's draft and puts the rest in
// the chat's aside. An unfinished edit is dropped, not kept, and a link's
// text the reader never changed is not theirs: their draft stays as it was
// (zf:src/app.rs:5186). A reply or an edit set for the chat now open (a
// private reply opens the chat it goes to) is left to it.
func (s *Screen) park(e *screen.Env) {
	a := s.acct
	s.stopComposing(a)
	// A recording belongs to the chat it was started in.
	s.voice.discard(s)
	as := s.asides.get(a, s.chat, true)
	text := s.st.Text()
	as.mentions = as.mentions[:0]
	switch {
	case s.untouchedPrefill(text):
	case len(s.edit) > 0 || !hasContent(text):
		s.flush(a, s.chat, nil)
	default:
		as.mentions = append(as.mentions, s.mentions...)
		s.flush(a, s.chat, text)
	}
	as.staged, e.S.Staged = e.S.Staged, nil
	p := &e.S.Panes
	own := p.Reply.Len() > 0 && bytes.Equal(p.Reply.At(0), s.reply)
	as.reply, as.replyChat = as.reply[:0], as.replyChat[:0]
	if own && len(as.staged) > 0 {
		as.reply = append(as.reply, s.reply...)
		as.replyChat = append(as.replyChat, s.replyChat...)
	}
	if own {
		p.Reply.Reset()
		p.ReplyChat.Reset()
	}
	if p.Edit.Len() > 0 && bytes.Equal(p.Edit.At(0), s.edit) {
		p.Edit.Reset()
	}
	s.mentions = s.mentions[:0]
	s.edit, s.reply, s.replyChat = s.edit[:0], s.reply[:0], s.replyChat[:0]
	s.prefill.text, s.dirty = s.prefill.text[:0], false
	s.paste.sendQueued, s.tools = false, false
	s.comp.close(s)
	_ = s.st.SetText(nil)
	s.seen()
}

// restore puts the open chat's draft in the composer, with its aside.
func (s *Screen) restore(e *screen.Env, a *state.Account) {
	_ = s.st.SetText(a.Model.Draft(s.chat))
	s.mentions = s.mentions[:0]
	if as := s.asides.get(a, s.chat, false); as != nil {
		s.mentions = append(s.mentions, as.mentions...)
		e.S.Staged, as.staged = as.staged, nil
		p := &e.S.Panes
		if len(as.reply) > 0 && p.Reply.Len() == 0 {
			p.Reply.Add(as.reply)
			p.ReplyChat.Reset()
			if len(as.replyChat) > 0 {
				p.ReplyChat.Add(as.replyChat)
			}
		}
		as.mentions, as.reply, as.replyChat = as.mentions[:0], as.reply[:0], as.replyChat[:0]
	}
	s.seen()
	s.styleMentions(e)
}

// keep is a change of the text: the model's draft follows it (not an edit's
// text, nor a link's the reader has not changed), and the archive after a
// pause.
func (s *Screen) keep(e *screen.Env, a *state.Account) {
	text := s.st.Text()
	if len(s.edit) > 0 || s.untouchedPrefill(text) {
		return
	}
	s.prefill.text = s.prefill.text[:0]
	if hasContent(text) {
		a.Model.SetDraft(s.chat, text)
	} else if len(a.Model.Draft(s.chat)) > 0 {
		a.Model.SetDraft(s.chat, nil)
	}
	s.dirty, s.lastEdit = true, e.S.Now()
	s.timer.Reset(composingTimeout + 50*time.Millisecond)
}

// flush stores a chat's draft in the model and the archive, where a restart
// finds it (zf:src/app.rs:4793). The composer keeps the model's draft as it
// types, so it sends SaveDraft itself rather than through
// Account.SaveDraft, which sends nothing for the text the model holds.
func (s *Screen) flush(a *state.Account, chat, text []byte) {
	if a == nil || len(chat) == 0 {
		return
	}
	if !hasContent(text) {
		text = nil
	}
	if !bytes.Equal(a.Model.Draft(chat), text) {
		a.Model.SetDraft(chat, text)
	}
	cmd := a.Cmd(abi.CommandTagSaveDraft)
	cmd.SaveDraft.Chat, cmd.SaveDraft.Text = chat, text
	a.Send(cmd)
	if bytes.Equal(chat, s.chat) {
		s.dirty = false
	}
}

// flushOpen stores the open chat's text: on a pause in typing and at the
// lock (zf:src/app.rs:9407 flush_open_draft).
func (s *Screen) flushOpen() {
	text := s.st.Text()
	if s.acct == nil || len(s.chat) == 0 || len(s.edit) > 0 || s.untouchedPrefill(text) {
		return
	}
	s.flush(s.acct, s.chat, text)
}

// untouchedPrefill: the composer holds the text a link brought, which the
// reader has not changed (zf:src/app.rs:1553).
func (s *Screen) untouchedPrefill(text []byte) bool {
	return len(s.prefill.text) > 0 && bytes.Equal(text, s.prefill.text)
}

// prefillWait is how long a link's text waits for its chat to open: the
// core makes a chat it does not have within StartChat's 10 seconds.
const prefillWait = 11 * time.Second

// takePrefill puts a link's text in the open chat's composer, only where
// nothing is written: no draft, no edit, no reply, no recording
// (zf:src/app.rs:6149). Text for a chat that exists and did not open (a
// locked chat outside its folder, or the reader went elsewhere) is dropped;
// for one the core is still making, it waits.
func (s *Screen) takePrefill(e *screen.Env, a *state.Account, ch model.Chat) {
	p := &e.S.Req.Prefill
	if len(p.Chat) == 0 {
		s.prefill.since = time.Time{}
		return
	}
	if !bytes.Equal(p.Chat, ch.ID()) {
		now := e.S.Now()
		if s.prefill.since.IsZero() {
			s.prefill.since = now
		}
		if a.Model.Chats.Find(p.Chat) == 0 && now.Sub(s.prefill.since) < prefillWait {
			return
		}
	} else if len(s.edit) == 0 && !hasContent(s.st.Text()) && !s.voice.recording() &&
		e.S.Panes.Reply.Len() == 0 && len(a.Model.Draft(p.Chat)) == 0 && hasContent(p.Text) {
		if s.st.SetText(p.Text) == nil {
			s.prefill.text = append(s.prefill.text[:0], p.Text...)
			s.mentions = s.mentions[:0]
			s.comp.close(s)
			s.seen()
			s.styleMentions(e)
			s.st.Focus(e.K.Kit)
		}
	}
	p.Chat, p.Text = p.Chat[:0], p.Text[:0]
	s.prefill.since = time.Time{}
	e.S.Touch()
}

// syncEdit follows state.Panes.Edit: the message's text goes into the
// composer, under "Editing message" (zf:src/app.rs:6913).
func (s *Screen) syncEdit(e *screen.Env, a *state.Account) {
	p := &e.S.Panes
	if p.Edit.Len() == 0 {
		if len(s.edit) > 0 { // the edit ended elsewhere
			s.edit = s.edit[:0]
			s.clearText()
		}
		return
	}
	id := p.Edit.At(0)
	if bytes.Equal(id, s.edit) {
		return
	}
	conv := a.Conversation()
	i := -1
	if conv != nil {
		i = conv.Find(id)
	}
	if i < 0 || conv.At(i).Kind() != abi.ContentTagText || !conv.At(i).FromMe() {
		p.Edit.Reset()
		e.S.Touch()
		return
	}
	m := conv.At(i)
	s.edit = append(s.edit[:0], id...)
	s.tools = false
	p.Reply.Reset()
	p.ReplyChat.Reset()
	s.reply, s.replyChat = s.reply[:0], s.replyChat[:0]
	// The message's text takes the composer's place: what was written there
	// is not kept (zf:src/app.rs:6913).
	if len(a.Model.Draft(s.chat)) > 0 {
		a.Model.SetDraft(s.chat, nil)
	}
	s.prefill.text = s.prefill.text[:0]
	_ = s.st.SetText(m.Text())
	s.mentions = s.mentions[:0]
	s.comp.close(s)
	s.seen()
	s.styleMentions(e)
	s.st.Focus(e.K.Kit)
	e.S.Touch()
}

// cancelEdit stops editing and empties the composer (zf:src/app.rs:6946).
func (s *Screen) cancelEdit(e *screen.Env) {
	e.S.Panes.Edit.Reset()
	if len(s.edit) > 0 {
		s.edit = s.edit[:0]
		s.clearText()
	}
	e.S.Touch()
}

// clearText empties the composer and forgets its mentions.
func (s *Screen) clearText() {
	_ = s.st.SetText(nil)
	s.mentions = s.mentions[:0]
	s.comp.close(s)
	s.seen()
	s.spans = s.spans[:0]
	_ = s.st.SetSpans(s.spans)
}

// editPrevious edits our most recent message that can still be edited: a
// text sent within 15 minutes (zf:src/app.rs:5298).
func (s *Screen) editPrevious(e *screen.Env, a *state.Account) {
	conv := a.Conversation()
	if conv == nil {
		return
	}
	now := e.S.Now().Unix()
	for i := conv.Len() - 1; i >= 0; i-- {
		m := conv.At(i)
		if m.FromMe() && m.Kind() == abi.ContentTagText && now-m.Time() <= editWindow {
			e.S.Panes.Edit.Reset()
			e.S.Panes.Edit.Add(m.ID())
			e.S.Touch()
			e.C().RequestFrame()
			return
		}
	}
}
