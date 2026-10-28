package markup

import (
	ftext "gonativeui/text"
	"gonativeui/ui"
)

// Fonts are the fonts a message is drawn in. Mono is upstream's monospace at
// 0.95 of the text size.
type Fonts struct {
	Regular, Bold, Italic, BoldItalic, Mono ftext.FontKey
}

// Colors are 0xRRGGBBAA. Plain text takes the box's own colour.
type Colors struct {
	// Secondary draws the bar of a quote.
	Secondary uint32
	// Link draws and underlines web addresses, mail addresses and phone numbers.
	Link uint32
	// Mention draws @mentions.
	Mention uint32
}

// The ui.RichStyles indexes ToRich uses: 0 is plain text; 1 to 7 the
// combinations of bold, italic and strike (bit 1, 2, 4); 8 monospace; 9 to 15
// are free; 16 to 23 are links in those combinations; 24 mentions; 25 the bar
// of a quote.
const (
	styleMono    = 8
	styleLink    = 16
	styleMention = 24
	styleBar     = 25
)

// RichStyleIndex is the ui.RichStyles index of a span.
func RichStyleIndex(s Span) uint16 {
	var b uint16
	if s.Style&Bold != 0 {
		b |= 1
	}
	if s.Style&Italic != 0 {
		b |= 2
	}
	if s.Style&Strike != 0 {
		b |= 4
	}
	switch {
	case s.Style&Mentioned != 0:
		return styleMention
	case s.Style&QuoteBar != 0:
		return styleBar
	case s.Link != NoLink:
		return styleLink + b
	case s.Style&Mono != 0:
		return styleMono
	}
	return b
}

// RichStyles builds the table RichStyleIndex indexes. Keep it with the theme
// and pass it to ui.Context.RichText for every message.
func RichStyles(f Fonts, c Colors) ui.RichStyles {
	var t ui.RichStyles
	font := func(b uint16) ftext.FontKey {
		switch {
		case b&1 != 0 && b&2 != 0:
			return f.BoldItalic
		case b&1 != 0:
			return f.Bold
		case b&2 != 0:
			return f.Italic
		}
		return f.Regular
	}
	for b := uint16(0); b < 8; b++ {
		var deco ui.Deco
		if b&4 != 0 {
			deco = ui.DecoStrike
		}
		t.Set(int(b), ui.RichStyle{Font: font(b), Deco: deco})
		t.Set(int(styleLink+b), ui.RichStyle{Font: font(b), Color: c.Link, Deco: deco | ui.DecoUnderline})
	}
	t.Set(styleMono, ui.RichStyle{Font: f.Mono})
	t.Set(styleMention, ui.RichStyle{Font: f.Bold, Color: c.Mention})
	t.Set(styleBar, ui.RichStyle{Font: f.Regular, Color: c.Secondary})
	return t
}

// ToRich appends what ui.Context.RichText takes to body and rich: the shown
// text of the spans (AppendText) and its styled ranges. Link and mention
// ranges carry ui.SpanLink and ui.SpanMention, and a Tag of the index of
// their Span in spans plus one, which RichSignal.Clicked reports back; Target
// gives the address. Plain ranges are left out, as style 0 is the default.
// Both outputs are appended to, so a caller reuses buffers across messages.
func ToRich(body []byte, rich []ui.Span, text []byte, spans []Span, mentions []Mention) ([]byte, []ui.Span) {
	for i := range spans {
		s := &spans[i]
		off := len(body)
		body = appendShown(body, text, s, mentions)
		idx := RichStyleIndex(*s)
		var flags uint16
		var tag uint32
		switch {
		case s.Style&Mentioned != 0:
			flags, tag = ui.SpanMention, uint32(i)+1
		case s.Link != NoLink:
			flags, tag = ui.SpanLink, uint32(i)+1
		}
		if (idx == 0 && flags == 0) || len(body) == off {
			continue
		}
		rich = append(rich, ui.Span{Off: uint32(off), Len: uint32(len(body) - off), Style: idx, Flags: flags, Tag: tag})
	}
	return body, rich
}
