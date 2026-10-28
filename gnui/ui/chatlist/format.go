package chatlist

import (
	"bytes"
	"time"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/markup"
	"zapfast/model"
)

// Text helpers of the list: row stamps (zf:src/util.rs chat_stamp), the
// search fold (search_key) and message summaries (Content::summary). They
// run when a chat or a result changes, never per frame.

// stamp is the chat row's time: the clock today, "Yesterday", the weekday
// this week, or the date (format.AppendChatStamp).
func stamp(now time.Time, ts int64) string { return string(appendStamp(nil, now, ts)) }

// appendStamp appends stamp(now, ts) to dst.
func appendStamp(dst []byte, now time.Time, ts int64) []byte {
	if ts <= 0 {
		return dst
	}
	return format.AppendChatStamp(dst, i18n.Current(), time.Unix(ts, 0), now)
}

// dayNumber counts local calendar days since the Unix epoch.
func dayNumber(t time.Time) int {
	y, m, d := t.Date()
	return int(time.Date(y, m, d, 12, 0, 0, 0, time.UTC).Unix() / 86400)
}

// containsDigits reports whether text contains digits (a phone search).
func containsDigits(text, digits []byte) bool {
	return len(digits) > 0 && bytes.Contains(text, digits)
}

// digitsOf are the digits of the query when it is all digits, spaces, plus
// signs and dashes (a phone number typed as people write it), else nil.
func digitsOf(q string) []byte {
	var d []byte
	for i := 0; i < len(q); i++ {
		switch b := q[i]; {
		case b >= '0' && b <= '9':
			d = append(d, b)
		case b == ' ' || b == '+' || b == '-' || b == '(' || b == ')':
		default:
			return nil
		}
	}
	return d
}

// appendPlain appends a message line with its markup removed and @numbers
// named (zf:src/app.rs resolve_mention_tokens, markup::plain) to dst.
func (s *Screen) appendPlain(dst []byte, m *model.Store, text []byte) []byte {
	if len(text) == 0 {
		return dst
	}
	if bytes.IndexByte(text, '@') >= 0 {
		text = resolveMentionTokens(m, text)
	}
	s.spans = markup.ParseMentions(s.spans[:0], text, nil)
	return markup.AppendText(dst, text, s.spans, nil)
}

// resolveMentionTokens replaces "@number" of a known person with "@name".
func resolveMentionTokens(m *model.Store, text []byte) []byte {
	var out []byte
	last := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '@' {
			continue
		}
		j := i + 1
		for j < len(text) && text[j] >= '0' && text[j] <= '9' {
			j++
		}
		if j-i < 6 {
			continue
		}
		jid := append(append(make([]byte, 0, j-i+14), text[i+1:j]...), "@s.whatsapp.net"...)
		ci := m.Contacts.Find(jid)
		if ci < 0 {
			continue
		}
		name := m.Contacts.At(ci).DisplayName()
		if len(name) == 0 {
			continue
		}
		out = append(out, text[last:i+1]...)
		out = append(out, name...)
		last = j
		i = j - 1
	}
	if out == nil {
		return text
	}
	return append(out, text[last:]...)
}

// summarize is Content::summary (zf:src/model.rs:1182): the one line a
// message is listed by.
func summarize(msg *abi.Message) string {
	c := &msg.Content
	caption := func(label string, cap []byte, has bool) string {
		if has && len(bytes.TrimSpace(cap)) > 0 {
			return label + ": " + firstLine(cap)
		}
		return label
	}
	switch c.Tag {
	case abi.ContentTagText:
		return firstLine(c.Text.Text)
	case abi.ContentTagInteractive:
		return firstLine(c.Interactive.Text)
	case abi.ContentTagImage:
		return caption(i18n.T("Photo"), c.Image.Caption, c.Image.HasCaption)
	case abi.ContentTagVideo:
		label := i18n.T("Video")
		if c.Video.Gif {
			label = i18n.T("GIF")
		}
		return caption(label, c.Video.Caption, c.Video.HasCaption)
	case abi.ContentTagAudio:
		label := i18n.T("Audio")
		if c.Audio.VoiceNote {
			label = i18n.T("Voice message")
		}
		if c.Audio.HasSeconds {
			s := c.Audio.Seconds
			return label + " (" + string(format.AppendDuration(nil, s)) + ")"
		}
		return label
	case abi.ContentTagDocument:
		return i18n.T("Document") + ": " + string(c.Document.FileName)
	case abi.ContentTagSticker:
		return i18n.T("Sticker")
	case abi.ContentTagStickerPack:
		return i18n.T("Sticker pack") + ": " + string(c.StickerPack.Name)
	case abi.ContentTagLocation:
		if c.Location.HasName {
			return i18n.T("Location") + ": " + string(c.Location.Name)
		}
		return i18n.T("Location")
	case abi.ContentTagLiveLocation:
		if c.LiveLocation.Ended {
			return i18n.T("Live location ended")
		}
		return i18n.T("Live location")
	case abi.ContentTagContact:
		return i18n.T("Contact") + ": " + string(c.Contact.DisplayName)
	case abi.ContentTagPoll:
		return i18n.T("Poll") + ": " + string(c.Poll.Question)
	case abi.ContentTagRevoked:
		return i18n.T("This message was deleted")
	}
	return i18n.T("Message on your phone")
}

func firstLine(b []byte) string {
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		b = b[:i]
	}
	return string(b)
}
