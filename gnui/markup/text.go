package markup

import (
	ftext "gonativeui/text"
)

// Shown texts of the synthetic spans.
const (
	quoteBar = "▎ "
	bullet   = "•  "
)

// AppendText appends the text the spans show to dst: the raw text without
// its markers, a quote bar for each QuoteBar, "•  " for each Bullet, and
// "@name" for each Mention (upstream's plain).
func AppendText(dst, text []byte, spans []Span, mentions []Mention) []byte {
	for i := range spans {
		dst = appendShown(dst, text, &spans[i], mentions)
	}
	return dst
}

func appendShown(dst, text []byte, s *Span, mentions []Mention) []byte {
	switch {
	case s.Style&Mentioned != 0:
		dst = append(dst, '@')
		if int(s.Arg) < len(mentions) {
			dst = append(dst, mentions[s.Arg].Name...)
		}
		return dst
	case s.Style&QuoteBar != 0:
		return append(dst, quoteBar...)
	case s.Style&Bullet != 0:
		return append(dst, bullet...)
	}
	return append(dst, text[s.Start:s.End]...)
}

// Plain is the text of a message with its mentions resolved and its markup
// removed, as previews show it (upstream's plain). It allocates.
func Plain(text []byte, mentions []Mention) string {
	spans := ParseMentions(nil, text, mentions)
	return string(AppendText(nil, text, spans, mentions))
}

// NameMentions is upstream's name_mentions: the text as it is when mentions
// is empty, otherwise Plain (which, as upstream's does, also removes the
// other markup). It allocates.
func NameMentions(text []byte, mentions []Mention) string {
	if len(mentions) == 0 {
		return string(text)
	}
	return Plain(text, mentions)
}

// Target appends the address a link span opens to dst: the web address with
// its scheme ("https://" added to "www.x.com"), "mailto:" and the address,
// "tel:" and the digits of a phone number with its leading "+". It appends
// nothing for a span that is not a link.
func Target(dst, text []byte, s Span) []byte {
	raw := text[s.Start:s.End]
	switch s.Link {
	case LinkWeb:
		if !hasPrefixFold(raw, "http://") && !hasPrefixFold(raw, "https://") {
			dst = append(dst, "https://"...)
		}
		return append(dst, raw...)
	case LinkMail:
		if !hasPrefixFold(raw, "mailto:") {
			dst = append(dst, "mailto:"...)
		}
		return append(dst, raw...)
	case LinkTel:
		dst = append(dst, "tel:"...)
		if len(raw) > 0 && raw[0] == '+' {
			dst = append(dst, '+')
		}
		for _, c := range raw {
			if c >= '0' && c <= '9' {
				dst = append(dst, c)
			}
		}
	}
	return dst
}

// Links lists the addresses in text, each once, in reading order, found the
// way a bubble underlines them (upstream's links). Mail addresses are left
// out: they do not open in a browser. Phone numbers are listed as "tel:...",
// as upstream does. It allocates.
func Links(text []byte) []string {
	var found []string
	var buf []byte
	for _, s := range Parse(nil, text) {
		if s.Link == NoLink || s.Link == LinkMail {
			continue
		}
		buf = Target(buf[:0], text, s)
		dup := false
		for _, f := range found {
			if f == string(buf) {
				dup = true
				break
			}
		}
		if !dup {
			found = append(found, string(buf))
		}
	}
	return found
}

// Big reports whether a message is emoji and nothing else, one to three of
// them, and so is drawn large (upstream's only_emoji(text) <= 3). The
// framework's text.EmojiOnly does the counting; ui.RichOpts.EmojiLarge does
// the same itself.
func Big(text []byte) bool {
	n := ftext.EmojiOnly(text)
	return n >= 1 && n <= ftext.MaxJumboEmoji
}
