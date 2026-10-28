package composer

import (
	"bytes"
	"strconv"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/mentions"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// sendBufs are the send's scratch: the body as it goes out, and the JIDs it
// mentions.
type sendBufs struct {
	body, tmp []byte
	ids       [][]byte
	paths     [][]byte
}

// send sends what the composer holds: the staged attachments with the text
// as their caption, an edit of a message, or a text, quoting the reply
// (zf:src/ui/conversation.rs:1835-1859, zf:src/app.rs:5371).
func (s *Screen) send(e *screen.Env, a *state.Account, ch model.Chat) {
	if len(s.edit) == 0 && e.S.QuoteHidden(a) {
		// A private reply quoting a chat the reader may not see now sends
		// nothing: the text, the attachments and the reply stay
		// (zf:src/app.rs:6007; MM-007).
		s.paste.sendQueued = false
		quoteUnavailable(e.S)
		return
	}
	text := bytes.TrimSpace(s.st.Text())
	if len(s.state.Staged) > 0 {
		for i := range s.state.Staged {
			st := &s.state.Staged[i]
			if st.Failed {
				if a.Conn == nil {
					e.S.Toast([]byte(state.ErrNoCore.Error()), true)
					return
				}
				st.Failed = false
				s.readPNG(e, st.ID, s.paste.failed[st.ID].data)
			}
		}
		if !s.picturesReady() {
			s.paste.sendQueued = true // the send goes as soon as the pictures are read
			return
		}
		s.sendStaged(e, a, ch, text)
		return
	}
	if len(text) == 0 {
		return
	}
	body, ids := s.encodeMentions(ch, text)
	s.stopComposing(a)
	p := &e.S.Panes
	if len(s.edit) > 0 {
		cmd := a.Cmd(abi.CommandTagEditText)
		cmd.EditText.Chat, cmd.EditText.ID, cmd.EditText.Text, cmd.EditText.Mentions = s.chat, s.edit, body, ids
		if err := a.Send(cmd); err != nil {
			e.S.Toast([]byte(err.Error()), true)
			return
		}
		s.edit = s.edit[:0]
		p.Edit.Reset()
	} else {
		cmd := a.Cmd(abi.CommandTagSendText)
		cmd.SendText.Chat, cmd.SendText.Text, cmd.SendText.Mentions = s.chat, body, ids
		cmd.SendText.Quoting, cmd.SendText.HasQuoting = quoting(p)
		if err := a.Send(cmd); err != nil {
			e.S.Toast([]byte(err.Error()), true)
			return
		}
		s.flush(a, s.chat, nil) // only an accepted send clears the draft
		p.Reply.Reset()
		p.ReplyChat.Reset()
		s.reply, s.replyChat = s.reply[:0], s.replyChat[:0]
	}
	s.clearText()
	s.st.Focus(e.K.Kit)
	e.S.Touch()
}

// quoteUnavailable says why a send quoting a chat the reader may not see
// went nowhere (Refusal::QuoteUnavailable's words).
func quoteUnavailable(st *state.State) {
	st.Toast([]byte(i18n.T("Not sent: the message you’re replying to isn’t available on this computer. Cancel the reply to send without a quote.")), true)
}

// quoting is the reply a send quotes: the message, and its chat when it is
// another (a private reply, zf:src/app.rs:6661).
func quoting(p *state.Panes) (abi.ReplyTarget, bool) {
	if p.Reply.Len() == 0 {
		return abi.ReplyTarget{}, false
	}
	q := abi.ReplyTarget{ID: p.Reply.At(0)}
	if p.ReplyChat.Len() > 0 {
		q.Chat, q.HasChat = p.ReplyChat.At(0), true
	}
	return q, true
}

// sendStaged sends the staged attachments: the caption, its mentions and
// the reply go with the first; files go in runs of one kind, in the order
// they were staged (zf:src/app.rs:5510).
func (s *Screen) sendStaged(e *screen.Env, a *state.Account, ch model.Chat, caption []byte) {
	s.paste.sendQueued = false
	// A picture still being read (or that could not be) stops the send
	// before anything goes.
	for i := range s.state.Staged {
		if st := &s.state.Staged[i]; st.Kind == state.StagedPicture && !st.Ready {
			e.S.Toast([]byte(st.Name), true)
			return
		}
	}
	body, ids := s.encodeMentions(ch, caption)
	p := &e.S.Panes
	quote, quoted := quoting(p)
	first := true
	files := 0
	fill := func(capt *[]byte, has *bool, ms *[][]byte, q *abi.ReplyTarget, hq *bool) {
		if !first {
			return
		}
		if len(body) > 0 {
			*capt, *has = body, true
		}
		*ms = ids
		*q, *hq = quote, quoted
	}
	accepted := func() {
		if first {
			first = false
			s.flush(a, s.chat, nil)
			p.Reply.Reset()
			p.ReplyChat.Reset()
			s.reply, s.replyChat = s.reply[:0], s.replyChat[:0]
			s.clearText()
		}
		e.S.Touch()
	}
	// The head goes, a picture alone or a run of files of one kind, until
	// none is left. Accepted sends cannot be recalled: only their inputs
	// leave the staged list, so a later failure can be retried without
	// duplicates.
	staged := &s.state.Staged
	for len(*staged) > 0 {
		st, n := &(*staged)[0], 1
		var cmd *abi.Command
		if st.Kind == state.StagedPicture {
			cmd = a.Cmd(abi.CommandTagSendImage)
			im := &cmd.SendImage
			im.Chat, im.Width, im.Height, im.RGBA = s.chat, st.W, st.H, st.Blob
			fill(&im.Caption, &im.HasCaption, &im.Mentions, &im.Quoting, &im.HasQuoting)
		} else {
			// CommandSendFiles.Paths has an ABI bound of 64.
			s.out.paths = s.out.paths[:0]
			for n = 0; n < min(len(*staged), 64) && (*staged)[n].Kind == st.Kind; n++ {
				s.out.paths = append(s.out.paths, (*staged)[n].Path)
			}
			cmd = a.Cmd(abi.CommandTagSendFiles)
			f := &cmd.SendFiles
			f.Chat, f.Paths, f.Documents = s.chat, s.out.paths, st.Kind == state.StagedDocument
			fill(&f.Caption, &f.HasCaption, &f.Mentions, &f.Quoting, &f.HasQuoting)
		}
		if err := a.Send(cmd); err != nil {
			e.S.Toast([]byte(err.Error()), true)
			return // a picture keeps its blob
		}
		if st.Kind != state.StagedPicture {
			files += n
		}
		*staged = append((*staged)[:0], (*staged)[n:]...)
		accepted()
	}
	s.stopComposing(a)
	p.Reply.Reset()
	p.ReplyChat.Reset()
	s.reply, s.replyChat = s.reply[:0], s.replyChat[:0]
	s.clearText()
	if files > 0 {
		n := strconv.Itoa(files)
		e.S.Toast([]byte(i18n.Format(i18n.N("Sending {} file…", "Sending {} files…", files), "{}", n)), false)
	}
	s.st.Focus(e.K.Kit)
	e.S.Touch()
}

// encodeMentions is the text as it goes out: each member mentioned by name
// becomes WhatsApp's "@user" token, wire tokens of members already in the
// text count too, and the localized word for everyone becomes "@all"
// (zf:src/app.rs:5424). It returns the body and the mentioned JIDs, valid
// until the next send.
func (s *Screen) encodeMentions(ch model.Chat, text []byte) (body []byte, ids [][]byte) {
	b := &s.out
	b.body = append(b.body[:0], text...)
	b.ids = b.ids[:0]
	parts := s.row.Participants
	if !s.rowOK {
		parts = nil
	}
	for _, m := range s.mentions {
		if !hasID(parts, m.id) {
			continue
		}
		user, _, _ := bytes.Cut(m.id, []byte("@"))
		if len(user) == 0 {
			continue
		}
		b.tmp = append(append(b.tmp[:0], '@'), m.name...)
		at := findNamedMention(b.body, b.tmp)
		if at < 0 {
			continue
		}
		end := at + len(b.tmp)
		b.tmp = append(b.tmp[:0], b.body[:at]...)
		b.tmp = append(append(b.tmp, '@'), user...)
		b.tmp = append(b.tmp, b.body[end:]...)
		b.body, b.tmp = b.tmp, b.body
		if !hasID(b.ids, m.id) {
			b.ids = append(b.ids, m.id)
		}
	}
	for _, id := range parts {
		user, _, _ := bytes.Cut(id, []byte("@"))
		if len(user) > 0 && containsMentionToken(b.body, user) && !hasID(b.ids, id) {
			b.ids = append(b.ids, id)
		}
	}
	if s.canMentionEveryone(ch) {
		var dst []byte
		dst, _ = mentions.RewriteAlias(b.tmp[:0], b.body, mentions.EveryoneAlias(i18n.Current()))
		b.body, b.tmp = dst, b.body
	}
	return b.body, b.ids
}

func hasID(ids [][]byte, id []byte) bool {
	for _, x := range ids {
		if bytes.Equal(x, id) {
			return true
		}
	}
	return false
}

// containsMentionToken reports whether text holds "@user" with no digit
// after it (zf:src/app.rs:9463).
func containsMentionToken(text, user []byte) bool {
	for off := 0; off < len(text); {
		i := bytes.IndexByte(text[off:], '@')
		if i < 0 {
			return false
		}
		at := off + i + 1
		if bytes.HasPrefix(text[at:], user) {
			end := at + len(user)
			if end == len(text) || text[end] < '0' || text[end] > '9' {
				return true
			}
		}
		off = at
	}
	return false
}
