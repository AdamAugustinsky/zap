package core

import (
	"testing"
	"time"

	"zapfast/abi"
)

// sentMessages is a Handler keeping copies of the messages of ours the demo
// files (ids DEMO…), in order.
type sentMessages struct {
	*recorder
	sent []abi.Message
}

func (h *sentMessages) Event(e *abi.Event) {
	if e.Tag == abi.EventTagMessages {
		for i := range e.Messages.Messages {
			m := &e.Messages.Messages[i]
			if m.FromMe && len(m.ID) > 5 && string(m.ID[:5]) == "DEMO0" {
				h.sent = append(h.sent, abi.Message{
					ID:        []byte(string(m.ID)),
					Quoted:    copyQuoted(m.Quoted),
					HasQuoted: m.HasQuoted,
					Mentions:  copyMentions(m.Mentions),
				})
			}
		}
	}
	h.recorder.Event(e)
}

func copyQuoted(q abi.Quoted) abi.Quoted {
	return abi.Quoted{
		Chat: []byte(string(q.Chat)), HasChat: q.HasChat, ID: []byte(string(q.ID)),
		Sender: []byte(string(q.Sender)), Summary: []byte(string(q.Summary)), Mentions: copyMentions(q.Mentions),
	}
}

func copyMentions(ms []abi.MentionRef) []abi.MentionRef {
	out := make([]abi.MentionRef, len(ms))
	for i, m := range ms {
		out[i] = abi.MentionRef{User: []byte(string(m.User)), ID: []byte(string(m.ID))}
	}
	return out
}

// drainUntil drains into h until done holds.
func (s *session) drainUntil(h Handler, what string, done func() bool) {
	s.t.Helper()
	s.h.match, s.h.matched = func(*abi.Event) bool { return done() }, false
	deadline := time.After(10 * time.Second)
	for !s.h.matched {
		if _, err := s.c.Drain(h, 0); err != nil {
			s.t.Fatal(err)
		}
		if s.h.matched {
			return
		}
		select {
		case <-s.w.ch:
		case <-deadline:
			s.t.Fatalf("no %s within 10 s", what)
		}
	}
}

func TestDemoReplyEchoesItsQuoteAndMentions(t *testing.T) {
	s := openDemo(t)
	h := &sentMessages{recorder: s.h}
	var cmd abi.Command
	cmd.Tag = abi.CommandTagSendText
	cmd.SendText = abi.CommandSendText{
		Chat:       []byte(ada),
		Text:       []byte("@393331234567 here it is"),
		Quoting:    abi.ReplyTarget{ID: []byte("Ada-Lovelace-2")},
		HasQuoting: true,
		Mentions:   [][]byte{[]byte(ada)},
	}
	s.send(&cmd)
	s.drainUntil(h, "the reply", func() bool { return len(h.sent) > 0 })
	m := h.sent[0]
	if !m.HasQuoted || string(m.Quoted.ID) != "Ada-Lovelace-2" || string(m.Quoted.Sender) != ada ||
		string(m.Quoted.Summary) != "That is wonderful news. Send me the branch when you can." || m.Quoted.HasChat {
		t.Fatalf("the echoed reply's quote: %+v (has %v)", m.Quoted, m.HasQuoted)
	}
	if len(m.Mentions) != 1 || string(m.Mentions[0].ID) != ada || string(m.Mentions[0].User) != "393331234567" {
		t.Fatalf("mentions %+v", m.Mentions)
	}
}

// The commands the screens send that the demo core used to refuse.
func TestDemoAnswersGifsStickersAndNewChats(t *testing.T) {
	s := openDemo(t)
	for _, c := range []struct {
		cmd  abi.Command
		want abi.EventTag
	}{
		{abi.Command{Tag: abi.CommandTagSearchGifs, SearchGifs: abi.CommandSearchGifs{Query: []byte("party"), Key: []byte("k")}}, abi.EventTagGifs},
		{abi.Command{Tag: abi.CommandTagRecentStickers}, abi.EventTagStickers},
		{abi.Command{Tag: abi.CommandTagEnsureChat, EnsureChat: abi.CommandEnsureChat{Chat: []byte("4930123456@s.whatsapp.net"), Name: []byte("Dorothy Vaughan")}}, abi.EventTagChatUpdated},
		{abi.Command{Tag: abi.CommandTagLoadChatMedia, LoadChatMedia: abi.CommandLoadChatMedia{Chat: []byte(ada)}}, abi.EventTagChatMedia},
		{abi.Command{Tag: abi.CommandTagViewStickerPack, ViewStickerPack: abi.CommandViewStickerPack{Chat: []byte("120363012345678901@g.us"), Message: []byte("DEMOPACK0")}}, abi.EventTagStickerPackPreview},
	} {
		s.send(&c.cmd)
		s.until(c.want.String(), tag(c.want))
	}
	played := abi.Command{Tag: abi.CommandTagMarkPlayed, MarkPlayed: abi.CommandMarkPlayed{
		Chat: []byte(ada), Message: []byte("DEMOVOICE0"), Sender: []byte(ada), Receipts: true,
	}}
	if err := s.c.Send(&played); err != nil {
		t.Fatalf("MarkPlayed: %v", err)
	}
	gifs := abi.Command{Tag: abi.CommandTagSearchGifs, SearchGifs: abi.CommandSearchGifs{Key: []byte("zf-demo:bad-key")}}
	s.send(&gifs)
	s.until("the bad key", func(e *abi.Event) bool {
		return e.Tag == abi.EventTagGifs && e.Gifs.HasError && e.Gifs.Error.BadKey
	})
}
