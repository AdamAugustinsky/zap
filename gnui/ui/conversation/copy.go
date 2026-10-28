package conversation

import (
	"strconv"

	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/markup"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// Copying, as WhatsApp's transcript has it (zf:src/transcript.rs,
// zf:src/ui/conversation.rs message_copy_row): one message's selected text
// alone; across messages, a line per message,
//
//	[22:41, 8/18/2026] Ada: (replying to Bo: "…") [photo] text (❤️ You)
//
// with the non-text messages in between as their marker.

// marker appends m's transcript marker ("[photo]", "[voice message, 0:12]",
// "[document: notes.pdf]" …) to dst; it appends nothing for a text.
func (s *Screen) marker(dst []byte, m model.Message) []byte {
	md, _ := m.Media()
	switch m.Kind() {
	case abi.ContentTagImage:
		return append(dst, "[photo]"...)
	case abi.ContentTagVideo:
		if md.Gif {
			return append(dst, "[GIF]"...)
		}
		return append(dst, "[video]"...)
	case abi.ContentTagAudio:
		if !md.Voice {
			return append(dst, "[audio]"...)
		}
		if md.HasSeconds {
			dst = append(dst, "[voice message, "...)
			return append(duration(dst, md.Seconds), ']')
		}
		return append(dst, "[voice message]"...)
	case abi.ContentTagDocument:
		dst = append(dst, "[document: "...)
		return append(append(dst, md.FileName...), ']')
	case abi.ContentTagSticker:
		return append(dst, "[sticker]"...)
	case abi.ContentTagLocation:
		return append(dst, "[location]"...)
	case abi.ContentTagLiveLocation:
		if m.Decode(&s.dec) == nil && s.dec.Content.LiveLocation.Ended {
			return append(dst, "[live location ended]"...)
		}
		return append(dst, "[live location]"...)
	case abi.ContentTagContact:
		dst = append(dst, "[contact: "...)
		return append(append(dst, m.Text()...), ']')
	case abi.ContentTagStickerPack:
		dst = append(dst, "[sticker pack: "...)
		if m.Decode(&s.dec) == nil {
			dst = append(dst, s.dec.Content.StickerPack.Name...)
		}
		return append(dst, ']')
	case abi.ContentTagPoll:
		dst = append(dst, "[poll: "...)
		return append(append(dst, m.Text()...), ']')
	}
	return dst
}

// summary appends m's one-line summary (upstream's Content::summary, in
// English as upstream's are): what a bubble shows for content no block
// draws, and selection mode's names.
func (s *Screen) summary(f *ui.Fmt, m model.Message) *ui.Fmt { return s.summaryOf(f, m, true) }

// kindLine is the summary without the caption: the line a bubble shows for
// content no block draws, over its caption.
func (s *Screen) kindLine(f *ui.Fmt, m model.Message) *ui.Fmt { return s.summaryOf(f, m, false) }

func (s *Screen) summaryOf(f *ui.Fmt, m model.Message, withCaption bool) *ui.Fmt {
	md, _ := m.Media()
	caption := func(label string) *ui.Fmt {
		f.Str(label)
		if t := firstLine(m.Text()); withCaption && len(t) > 0 {
			f.Str(": ").Str(model.S(t))
		}
		return f
	}
	switch m.Kind() {
	case abi.ContentTagText, abi.ContentTagInteractive:
		return f.Str(model.S(firstLine(m.Text())))
	case abi.ContentTagImage:
		return caption(i18n.T("Photo"))
	case abi.ContentTagVideo:
		switch {
		case md.Gif:
			return caption(i18n.T("GIF"))
		case md.VideoNote:
			return caption(i18n.T("Video message"))
		}
		return caption(i18n.T("Video"))
	case abi.ContentTagAudio:
		label := i18n.T("Audio")
		if md.Voice {
			label = i18n.T("Voice message")
		}
		f.Str(label)
		if md.HasSeconds {
			var b [16]byte
			f.Str(" (").Str(model.S(duration(b[:0], md.Seconds))).Byte(')')
		}
		return f
	case abi.ContentTagDocument:
		return fill(f, i18n.T("Document: {name}"), "{name}", model.S(md.FileName))
	case abi.ContentTagSticker:
		return f.Str(i18n.T("Sticker"))
	case abi.ContentTagStickerPack:
		name := ""
		if m.Decode(&s.dec) == nil {
			name = model.S(s.dec.Content.StickerPack.Name)
		}
		return fill(f, i18n.T("Sticker pack: {name}"), "{name}", name)
	case abi.ContentTagLocation:
		if t := m.Text(); len(t) > 0 {
			return fill(f, i18n.T("Location: {name}"), "{name}", model.S(t))
		}
		return f.Str(i18n.T("Location"))
	case abi.ContentTagLiveLocation:
		if m.Decode(&s.dec) == nil && s.dec.Content.LiveLocation.Ended {
			return f.Str(i18n.T("Live location ended"))
		}
		return f.Str(i18n.T("Live location"))
	case abi.ContentTagContact:
		return fill(f, i18n.T("Contact: {name}"), "{name}", model.S(m.Text()))
	case abi.ContentTagPoll:
		return fill(f, i18n.T("Poll: {question}"), "{question}", model.S(m.Text()))
	case abi.ContentTagRevoked:
		return f.Str(i18n.T("This message was deleted"))
	case abi.ContentTagUnsupported:
		return fill(f, i18n.T("Unsupported message ({what})"), "{what}", model.S(m.Text()))
	case abi.ContentTagPhoneOnly:
		if m.Decode(&s.dec) != nil {
			return f.Str(i18n.T("Message on your phone"))
		}
		po := &s.dec.Content.PhoneOnly
		switch {
		case po.LiveLocation:
			return f.Str(i18n.T("Live location"))
		case po.HasOnce:
			switch po.Once {
			case abi.OnceMediaPhoto:
				return f.Str(i18n.T("View once photo"))
			case abi.OnceMediaVideo:
				return f.Str(i18n.T("View once video"))
			case abi.OnceMediaVoice:
				return f.Str(i18n.T("View once voice message"))
			}
			return f.Str(i18n.T("View once audio"))
		case po.ViewOnce:
			return f.Str(i18n.T("View once message"))
		}
		return f.Str(i18n.T("Message on your phone"))
	case abi.ContentTagCall:
		if m.Decode(&s.dec) != nil {
			return f.Str(i18n.T("Voice call"))
		}
		cl := &s.dec.Content.Call
		switch {
		case cl.Status == abi.CallStatusMissed && cl.Video:
			f.Str(i18n.T("Missed video call"))
		case cl.Status == abi.CallStatusMissed:
			f.Str(i18n.T("Missed voice call"))
		case cl.Video:
			f.Str(i18n.T("Video call"))
		default:
			f.Str(i18n.T("Voice call"))
		}
		if cl.Status == abi.CallStatusAnswered && cl.Duration > 0 {
			var b [16]byte
			f.Str(" (").Str(model.S(duration(b[:0], uint32(cl.Duration)))).Byte(')')
		}
		return f
	}
	return f
}

func firstLine(b []byte) []byte {
	for i, c := range b {
		if c == '\n' {
			return b[:i]
		}
	}
	return b
}

// copyRow appends message m's transcript line to dst with body as its text,
// and reports where the body is in it.
func (s *Screen) copyRow(dst []byte, a *state.Account, ch model.Chat, m model.Message, body []byte) ([]byte, int, int) {
	dst = append(dst, '[')
	dst = copyStamp(dst, m.Time())
	dst = append(dst, "] "...)
	dst = append(dst, s.senderStr(a, m)...)
	dst = append(dst, ": "...)
	if _, qs, qsum, ok := m.Quote(); ok {
		name := qs
		if isJID(name) {
			name = a.Model.DisplayName(name)
		}
		dst = append(dst, "(replying to "...)
		dst = append(dst, name...)
		dst = append(dst, ": \""...)
		plain := markup.Plain(qsum, nil)
		n := 0
		for i := range plain {
			if n == 48 {
				dst = append(dst, plain[:i]...)
				dst = append(dst, "…"...)
				plain = ""
				break
			}
			n++
		}
		dst = append(dst, plain...)
		dst = append(dst, "\") "...)
	}
	mk := len(dst)
	dst = s.marker(dst, m)
	if len(dst) > mk && len(body) > 0 {
		dst = append(dst, ' ')
	}
	b0 := len(dst)
	dst = append(dst, body...)
	b1 := len(dst)
	if m.Reactions() > 0 && m.Decode(&s.dec) == nil && len(s.dec.Reactions) > 0 {
		dst = append(dst, " ("...)
		for i := range s.dec.Reactions {
			re := &s.dec.Reactions[i]
			if i > 0 {
				dst = append(dst, ", "...)
			}
			dst = append(dst, re.Emoji...)
			dst = append(dst, ' ')
			if re.FromMe {
				dst = append(dst, i18n.T("You")...)
			} else {
				dst = append(dst, a.Model.DisplayName(re.Sender)...)
			}
		}
		dst = append(dst, ')')
	}
	return dst, b0, b1
}

// copyText puts the transcript's text selection on the clipboard (the
// selection scope's Copy: ⌘C, Edit ▸ Copy).
func (s *Screen) copyText(e *screen.Env, a *state.Account, ch model.Chat, v *model.Conversation) {
	from, to, ok := s.sc.Selected()
	if !ok || v == nil {
		return
	}
	i0, i1 := s.t.index(from.Item), s.t.index(to.Item)
	if i0 < 0 || i1 < 0 || i1 < i0 {
		return
	}
	out := s.copied[:0]
	lines, b0, b1 := 0, 0, 0
	for i := i0; i <= i1; i++ {
		m := v.At(i)
		fp, fo, tp, toff := uint16(0), uint32(0), ui.PartEnd, ui.OffEnd
		if i == i0 {
			fp, fo = from.Part, from.Off
		}
		if i == i1 {
			tp, toff = to.Part, to.Off
		}
		var body []byte
		if hasText(m) {
			s.shown(a, m)
			body = ui.SelectedBytes(s.body, 0, fp, fo, tp, toff)
		}
		if len(body) == 0 && len(s.marker(s.buf[:0], m)) == 0 {
			continue
		}
		if lines > 0 {
			out = append(out, '\n')
		}
		var x0, x1 int
		out, x0, x1 = s.copyRow(out, a, ch, m, body)
		b0, b1 = x0, x1
		lines++
	}
	if lines == 1 {
		out = append(out[:0], out[b0:b1]...)
	}
	s.copied = out
	if lines > 0 {
		setClipboard(e, out)
	}
}

// copySelected copies selection mode's messages whole, a line each, in the
// chat's order (zf:src/ui/conversation.rs selected_text).
func (s *Screen) copySelected(e *screen.Env, a *state.Account, ch model.Chat, v *model.Conversation) {
	sel := &e.S.Panes.Selection
	if v == nil || sel.Len() == 0 {
		return
	}
	out := s.copied[:0]
	lines := 0
	for i := 0; i < v.Len(); i++ {
		m := v.At(i)
		if !sel.Has(m.ID()) {
			continue
		}
		var body []byte
		if hasText(m) {
			s.shown(a, m)
			body = append(s.link[:0], s.body...)
			s.link = body
		}
		if len(body) == 0 && len(s.marker(s.buf[:0], m)) == 0 {
			body = append(s.link[:0], s.summary(e.C().Fmt(), m).Bytes()...)
			s.link = body
		}
		if lines > 0 {
			out = append(out, '\n')
		}
		out, _, _ = s.copyRow(out, a, ch, m, body)
		lines++
	}
	s.copied = out
	if lines > 0 {
		setClipboard(e, out)
	}
}

// copyMessageText is the menu's Copy text: the text without its markup,
// the mentions named; a location's coordinates; a contact's card.
func (s *Screen) copyMessageText(e *screen.Env, a *state.Account, m model.Message) {
	switch m.Kind() {
	case abi.ContentTagLocation, abi.ContentTagLiveLocation:
		if m.Decode(&s.dec) != nil {
			return
		}
		lat, lon := s.dec.Content.Location.Latitude, s.dec.Content.Location.Longitude
		if m.Kind() == abi.ContentTagLiveLocation {
			lat, lon = s.dec.Content.LiveLocation.Latitude, s.dec.Content.LiveLocation.Longitude
		}
		b := strconv.AppendFloat(s.copied[:0], lat, 'f', -1, 64)
		b = append(b, ',')
		s.copied = strconv.AppendFloat(b, lon, 'f', -1, 64)
	case abi.ContentTagContact:
		if m.Decode(&s.dec) != nil {
			return
		}
		s.copied = append(s.copied[:0], s.dec.Content.Contact.Vcard...)
	default:
		s.shown(a, m)
		s.copied = append(s.copied[:0], s.body...)
	}
	setClipboard(e, s.copied)
}

// hasCopyText reports whether the menu offers Copy text for m.
func hasCopyText(m model.Message) bool {
	switch m.Kind() {
	case abi.ContentTagLocation, abi.ContentTagLiveLocation, abi.ContentTagContact:
		return true
	case abi.ContentTagText, abi.ContentTagInteractive:
		return true
	case abi.ContentTagImage, abi.ContentTagVideo, abi.ContentTagDocument:
		return len(m.Text()) > 0
	}
	return false
}

func setClipboard(e *screen.Env, b []byte) {
	if c := e.C(); c.ClipboardSet != nil {
		c.ClipboardSet(b)
	}
}
