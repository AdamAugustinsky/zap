package demo

import (
	"zapfast/abi"
	"zapfast/gocore/out"
)

// The demo's records go through the conversions zfcore's dto.rs makes:
// display text cut to its bound, lists past theirs cut to their first
// entries, thumbnails as event blobs, and the lists chunked.

// chatRecord is dto.rs's chat.
func chatRecord(c *abi.Chat) abi.Chat {
	r := *c
	r.Name = bs(out.Clip(string(c.Name), 1024))
	if c.HasLast {
		r.Last.Summary = bs(out.Clip(string(c.Last.Summary), 1024))
		r.Last.Full = bs(out.Clip(string(c.Last.Full), 196_608))
		if c.Last.HasSenderName {
			r.Last.SenderName = bs(out.Clip(string(c.Last.SenderName), 1024))
		}
	}
	// A group larger than the bound shows its first members only.
	r.Participants = c.Participants[:min(len(c.Participants), 1024)]
	if c.HasGroupDescription {
		r.GroupDescription = bs(out.Clip(string(c.GroupDescription), 8192))
	}
	r.Labels = c.Labels[:min(len(c.Labels), 64)]
	return r
}

// messageRecord is dto.rs's message; its thumbnail becomes an event blob
// of r.
func messageRecord(r *out.Record, m *message) abi.Message {
	x := m.Message
	if x.HasSenderName {
		x.SenderName = bs(out.Clip(string(x.SenderName), 1024))
	}
	if x.HasQuoted {
		q := &x.Quoted
		if q.HasSenderName {
			q.SenderName = bs(out.Clip(string(q.SenderName), 1024))
		}
		q.Summary = bs(out.Clip(string(q.Summary), 1024))
		q.Mentions = q.Mentions[:min(len(q.Mentions), mentionsMax)]
	}
	x.Reactions = x.Reactions[:min(len(x.Reactions), 256)]
	x.Mentions = x.Mentions[:min(len(x.Mentions), mentionsMax)]
	if c := &x.Content; c.Tag == abi.ContentTagText && c.Text.HasPreview {
		// A preview is decoration: one whose address cannot be carried is
		// left off, and its text is cut.
		p := c.Text.Preview
		if len(p.URL) > 4096 {
			c.Text.HasPreview = false
			c.Text.Preview = abi.LinkPreview{}
		} else {
			if p.HasTitle {
				p.Title = bs(out.Clip(string(p.Title), 1024))
			}
			if p.HasDescription {
				p.Description = bs(out.Clip(string(p.Description), 4096))
			}
			c.Text.Preview = p
		}
	}
	x.Thumbnail, x.HasThumbnail = abi.BlobRef{}, false
	if m.thumb != nil {
		x.Thumbnail, x.HasThumbnail = r.Blob(m.thumb), true
	}
	return x
}

// mentionsMax is the most mentions a message or quote carries.
const mentionsMax = 256

func (s *state) emitChats() {
	out.Chunks(s.out, s.chats, func(_ *out.Record, c *abi.Chat) abi.Chat { return chatRecord(c) },
		func(batch uint32, last bool, chats []abi.Chat) *abi.Event {
			return &abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: batch, Last: last, Chats: chats}}
		})
}

// emitMessages is a Messages batch of chat; complete only on its last
// record.
func (s *state) emitMessages(chat string, ms []*message, older, complete bool) {
	out.Chunks(s.out, ms, messageRecord, func(batch uint32, last bool, chunk []abi.Message) *abi.Event {
		return &abi.Event{Tag: abi.EventTagMessages, Messages: abi.EventMessages{
			Chat: bs(chat), Batch: batch, Last: last, Older: older, Complete: complete && last, Messages: chunk,
		}}
	})
}

func (s *state) emitContacts() {
	out.Chunks(s.out, s.contacts, func(_ *out.Record, c abi.Contact) abi.Contact {
		for _, n := range []*[]byte{&c.FullName, &c.FirstName, &c.PushName} {
			*n = bs(out.Clip(string(*n), 1024))
		}
		return c
	}, func(batch uint32, last bool, contacts []abi.Contact) *abi.Event {
		return &abi.Event{Tag: abi.EventTagContacts, Contacts: abi.EventContacts{Batch: batch, Last: last, Contacts: contacts}}
	})
}

func (s *state) emitCallLog() {
	out.Chunks(s.out, s.callLog, messageRecord, func(batch uint32, last bool, chunk []abi.Message) *abi.Event {
		return &abi.Event{Tag: abi.EventTagCallLog, CallLog: abi.EventCallLog{Batch: batch, Last: last, Messages: chunk}}
	})
}

func (s *state) emitInfo(text string) {
	ev := abi.Event{Tag: abi.EventTagInfo}
	ev.Info.Text = bs(out.Toast(text))
	s.out.Emit(&ev)
}
