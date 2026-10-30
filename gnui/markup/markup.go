// Package markup is zf:src/markup.rs: WhatsApp message formatting.
//
// *bold*, _italic_, ~struck~, `mono`, ```mono```, fenced blocks, "> "
// quotes, "* " lists, web and mail addresses, phone numbers and named
// @mentions.
//
// Parse turns the raw message bytes into Spans appended to a caller's buffer:
// a Span is plain data (offsets, a style bitset, a link kind), so a transcript
// can keep them in arenas and parsing into a reused buffer allocates nothing.
// Spans are in reading order and cover the text that is shown, which is the
// raw text without its markers: a span's Start and End are the raw bytes it
// shows, or, for the three synthetic kinds (QuoteBar, Bullet, Mentioned), the
// raw bytes it replaces ("> ", the list marker, "@<id>"). Text renders the
// shown text; ToRich builds the framework's rich-text input from it.
//
// Upstream keeps a Span's text and link target as Strings. Here the target of
// a link is computed from the raw bytes by Target.
package markup

import (
	"unicode"
	"unicode/utf8"

	wm "zapfast/mentions"
)

// Style is a span's formatting.
type Style uint16

const (
	Bold Style = 1 << iota
	Italic
	Strike
	// Mono is code: inline, in ``` or in a fenced block.
	Mono
	// Quote marks every span of a quoted line.
	Quote
	// Mentioned is an @mention of a person or of everyone: shown as "@" and the
	// name of Mentions[Arg].
	Mentioned
	// QuoteBar is the bar before a quoted line: shown as "▎ ", it replaces the
	// raw "> ".
	QuoteBar
	// Bullet is a list marker: shown as "•  ", it replaces the raw marker.
	Bullet
)

// LinkKind says what a span links to.
type LinkKind uint8

const (
	NoLink LinkKind = iota
	// LinkWeb is a web address, with or without its scheme.
	LinkWeb
	// LinkMail is an email address, with or without "mailto:".
	LinkMail
	// LinkTel is a phone number.
	LinkTel
)

// Span is a range of the message.
type Span struct {
	// Start and End are raw byte offsets (see the package comment).
	Start, End uint32
	Style      Style
	Link       LinkKind
	// Arg is the index in the mentions of a Mentioned span.
	Arg uint32
}

// Mention is a WhatsApp mention id (the digits after "@" in the body, or
// "all") and the name shown for it.
type Mention struct {
	User, Name []byte
}

// Parse appends the spans of text to dst, with no mentions resolved.
func Parse(dst []Span, text []byte) []Span { return ParseMentions(dst, text, nil) }

// ParseMentions is Parse with the mentions of the message: "@<id>" for an id
// in mentions becomes a Mentioned span, and "@all" one when mentions holds the
// user "all".
func ParseMentions(dst []Span, text []byte, mentions []Mention) []Span {
	p := parser{dst: dst, text: text, mentions: mentions}
	p.parse()
	return p.dst
}

type parser struct {
	dst      []Span
	text     []byte
	mentions []Mention
}

func (p *parser) add(start, end int, st Style, link LinkKind) {
	if start < end || st&(QuoteBar|Bullet|Mentioned) != 0 {
		p.dst = append(p.dst, Span{Start: uint32(start), End: uint32(end), Style: st, Link: link})
	}
}

func (p *parser) parse() {
	text := p.text
	inBlock := false
	for ls, first := 0, true; ls <= len(text); {
		le := len(text)
		for i := ls; i < len(text); i++ {
			if text[i] == '\n' {
				le = i
				break
			}
		}
		if !first {
			p.add(ls-1, ls, 0, NoLink)
		}
		first = false
		line := text[ls:le]
		next := le + 1
		// A standalone ``` toggles a block. Inline ``` is handled below.
		if string(trim(line)) == "```" {
			inBlock = !inBlock
			ls = next
			continue
		}
		if inBlock {
			p.add(ls, le, Mono, NoLink)
			ls = next
			continue
		}
		var q Style
		cs := ls // start of the content
		if len(line) >= 2 && line[0] == '>' && line[1] == ' ' {
			cs += 2
		} else if len(line) >= 1 && line[0] == '>' {
			cs++
		}
		if cs > ls {
			q = Quote
			p.add(ls, cs, Quote|QuoteBar, NoLink)
		}
		content := text[cs:le]
		indent := len(content) - len(trimStart(content))
		body := content[indent:]
		for _, m := range [...]string{"* ", "- ", "• ", "◦ "} {
			if len(body) >= len(m) && string(body[:len(m)]) == m {
				p.add(cs, cs+indent, q, NoLink)
				p.add(cs+indent, cs+indent+len(m), q|Bullet, NoLink)
				cs += indent + len(m)
				break
			}
		}
		p.inline(cs, le, q)
		ls = next
	}
}

func trim(b []byte) []byte { return trimEnd(trimStart(b)) }

func trimStart(b []byte) []byte {
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if !unicode.IsSpace(r) {
			break
		}
		b = b[n:]
	}
	return b
}

func trimEnd(b []byte) []byte {
	for len(b) > 0 {
		r, n := utf8.DecodeLastRune(b)
		if !unicode.IsSpace(r) {
			break
		}
		b = b[:len(b)-n]
	}
	return b
}

// isAlnum is Rust's char::is_alphanumeric.
func isAlnum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Other_Alphabetic, r)
}

// inline splits text[start:end], one line, at its markers. quote is added to
// every span.
func (p *parser) inline(start, end int, quote Style) {
	line := p.text[:end]
	var flags Style // Bold, Italic, Strike
	runStart := -1
	flush := func(upto int) {
		if runStart >= 0 && runStart < upto {
			p.plain(runStart, upto, flags|quote)
		}
		runStart = -1
	}
	triple := func(at int) bool {
		return at+2 < end && line[at] == '`' && line[at+1] == '`' && line[at+2] == '`'
	}
	i := start
	for i < end {
		c := line[i]
		// WhatsApp inline monospace: ```text```.
		if triple(i) {
			close := -1
			for at := i + 3; at < end; at++ {
				if triple(at) {
					close = at
					break
				}
			}
			if close > i+3 {
				flush(i)
				p.add(i+3, close, Mono|quote, NoLink)
				i = close + 3
				continue
			}
		}
		if c == '`' {
			close := -1
			for at := i + 1; at < end; at++ {
				if line[at] == '`' {
					close = at
					break
				}
			}
			if close > i+1 {
				flush(i)
				p.add(i+1, close, Mono|quote, NoLink)
				i = close + 1
				continue
			}
		}
		if c == '*' || c == '_' || c == '~' {
			bit := Bold
			switch c {
			case '_':
				bit = Italic
			case '~':
				bit = Strike
			}
			if flags&bit != 0 {
				if isCloser(line[:end], start, i) {
					flush(i)
					flags &^= bit
					i++
					continue
				}
			} else if isOpener(line[:end], start, i) && hasCloser(line[:end], start, i+1, c) {
				flush(i)
				flags |= bit
				i++
				continue
			}
		}
		if runStart < 0 {
			runStart = i
		}
		i++
	}
	flush(end)
}

func isOpener(line []byte, start, i int) bool {
	beforeOK := i == start
	if !beforeOK {
		prev, _ := utf8.DecodeLastRune(line[start:i])
		beforeOK = !isAlnum(prev)
	}
	if i+1 >= len(line) {
		return false
	}
	next, _ := utf8.DecodeRune(line[i+1:])
	return beforeOK && !unicode.IsSpace(next) && next != rune(line[i])
}

func isCloser(line []byte, start, i int) bool {
	if i == start {
		return false
	}
	prev, _ := utf8.DecodeLastRune(line[start:i])
	if unicode.IsSpace(prev) || prev == rune(line[i]) {
		return false
	}
	if i+1 >= len(line) {
		return true
	}
	next, _ := utf8.DecodeRune(line[i+1:])
	return !isAlnum(next)
}

func hasCloser(line []byte, start, from int, marker byte) bool {
	for j := from; j < len(line); j++ {
		if line[j] == marker && isCloser(line, start, j) {
			return true
		}
	}
	return false
}

// plain splits a plain (not mono) span around web addresses, phone numbers
// and mentions.
func (p *parser) plain(s, e int, st Style) {
	text := p.text[s:e]
	keep := st & (Bold | Italic | Strike | Quote)
	plainStart := 0
	push := func(upto int) { p.add(s+plainStart, s+upto, st, NoLink) }
	i := 0
	for i < len(text) {
		if !utf8.RuneStart(text[i]) {
			i++
			continue
		}
		atBoundary := i == 0
		if !atBoundary {
			prev, _ := utf8.DecodeLastRune(text[:i])
			atBoundary = !isAlnum(prev)
		}
		if atBoundary {
			if b := text[i]; b >= '0' && b <= '9' || b == '+' || b == '(' {
				if end, digits, valid := phoneAt(text, i); end > 0 {
					if valid {
						push(i)
						p.add(s+i, s+end, keep, LinkTel)
						plainStart, i = end, end
						continue
					}
					if digits == 0 && end > i {
						i = end
						continue
					}
					// Don't retry a suffix of an oversized number as a new link.
					// Let the email parser inspect the whole token when it ends in @.
					if digits > 15 && !(end < len(text) && text[end] == '@') {
						i = end
						continue
					}
				}
			}
			if text[i] == '@' {
				if end, idx, ok := mentionAt(text, i, p.mentions); ok {
					push(i)
					p.dst = append(p.dst, Span{Start: uint32(s + i), End: uint32(s + end), Style: (st & Quote) | Mentioned, Arg: uint32(idx)})
					plainStart, i = end, end
					continue
				}
			}
			// Links start with a letter or digit. Skip punctuation runs.
			if b := text[i]; b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' {
				if end, kind, ok := linkAt(text, i); ok {
					push(i)
					p.add(s+i, s+end, keep, kind)
					plainStart, i = end, end
					continue
				}
			}
		}
		i++
	}
	push(len(text))
}

// phoneAt finds a phone candidate with optional spaces, hyphens and
// parentheses. Dots are excluded so formatted tax IDs such as
// XXX.XXX.XXX-XX stay plain. It returns end 0 for no candidate; digits counts
// the digits (0 for an ISO date, which is skipped as one token) and valid
// tells the caller to link it.
func phoneAt(text []byte, at int) (end, digits int, valid bool) {
	// Reject an ISO date before scanning through the space after it; the next
	// token may be a time or a separate phone number.
	dateAt := at
	if text[at] == '(' {
		dateAt++
	}
	if dateAt+10 <= len(text) {
		d := text[dateAt : dateAt+10]
		if d[4] == '-' && d[7] == '-' && allDigits(d[:4]) && allDigits(d[5:7]) && allDigits(d[8:]) {
			return dateAt + 10, 0, false
		}
	}

	end = at
scan:
	for off := at; off < len(text); off++ {
		switch c := text[off]; {
		case c >= '0' && c <= '9':
			digits++
		case c == '+':
			if off != at {
				break scan
			}
		case c == ' ' || c == '-' || c == '(' || c == ')':
		default:
			break scan
		}
		end = off + 1
	}
	for end > at && (text[end-1] == ' ' || text[end-1] == '-' || text[end-1] == '(') {
		end--
	}
	if digits == 0 {
		return 0, 0, false
	}
	valid = digits >= 7 && digits <= 15
	if valid && end < len(text) {
		f := text[end]
		valid = !(f >= '0' && f <= '9' || f >= 'a' && f <= 'z' || f >= 'A' && f <= 'Z') && f != '@'
	}
	return end, digits, valid
}

func allDigits(b []byte) bool {
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// mentionAt finds the mention at the "@" at at: "@all" when the message
// mentions everyone, otherwise "@" and at least five digits naming one of
// mentions.
func mentionAt(text []byte, at int, mentions []Mention) (end, idx int, ok bool) {
	if e, found := wm.EveryoneTokenEnd(text, at); found {
		for i := range mentions {
			if string(mentions[i].User) == wm.AllUser {
				return e, i, true
			}
		}
	}
	n := 0
	for at+1+n < len(text) && text[at+1+n] >= '0' && text[at+1+n] <= '9' {
		n++
	}
	if n < 5 {
		return 0, 0, false
	}
	digits := text[at+1 : at+1+n]
	for i := range mentions {
		if string(mentions[i].User) == string(digits) {
			return at + 1 + n, i, true
		}
	}
	return 0, 0, false
}

// linkAt parses a web or email address at at and returns its end and kind.
func linkAt(text []byte, at int) (end int, kind LinkKind, ok bool) {
	rest := text[at:]
	// Brackets opened inside the address belong to it, as in
	// https://en.wikipedia.org/wiki/Rust_(programming_language); a closing one
	// without its opener closes the text around it: "(see example.com)".
	parens, squares := 0, 0
	tokenEnd := len(rest)
scan:
	for j := 0; j < len(rest); {
		c, w := utf8.DecodeRune(rest[j:])
		switch c {
		case '(':
			parens++
		case ')':
			if parens == 0 {
				tokenEnd = j
				break scan
			}
			parens--
		case '[':
			squares++
		case ']':
			if squares == 0 {
				tokenEnd = j
				break scan
			}
			squares--
		case '<', '>', '"', '\'', '{', '}':
			tokenEnd = j
			break scan
		default:
			if unicode.IsSpace(c) {
				tokenEnd = j
				break scan
			}
		}
		j += w
	}
	token := rest[:tokenEnd]
	for len(token) > 0 {
		switch token[len(token)-1] {
		case '.', ',', ';', ':', '!', '?', '*', '_', '~':
			token = token[:len(token)-1]
			continue
		}
		break
	}
	if len(token) < 4 {
		return 0, 0, false
	}
	end = at + len(token)
	switch {
	case hasPrefixFold(token, "http://") || hasPrefixFold(token, "https://"):
		if len(token) > 8 {
			return end, LinkWeb, true
		}
		return 0, 0, false
	case hasPrefixFold(token, "www."):
		return end, LinkWeb, true
	case hasPrefixFold(token, "mailto:"):
		// The scheme alone, or with no address after it, is no link.
		addr := token[len("mailto:"):]
		if q := indexByte(addr, '?'); q >= 0 {
			addr = addr[:q]
		}
		if k := indexByte(addr, '@'); k > 0 && isDomain(addr[k+1:]) {
			return end, LinkMail, true
		}
		return 0, 0, false
	}
	if k := indexByte(token, '@'); k >= 0 {
		user, host := token[:k], token[k+1:]
		if len(user) > 0 && indexByte(user, '/') < 0 && isDomain(host) {
			return end, LinkMail, true
		}
	}
	host := token
	if k := indexByte(token, '/'); k >= 0 {
		host = token[:k]
	}
	if isDomain(host) {
		return end, LinkWeb, true
	}
	return 0, 0, false
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func hasPrefixFold(b []byte, prefix string) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		c := b[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != prefix[i] {
			return false
		}
	}
	return true
}

// isDomain reports whether a token is a hostname without a scheme.
func isDomain(host []byte) bool {
	if k := indexByte(host, ':'); k >= 0 {
		host = host[:k]
	}
	labels := 0
	var last []byte
	for start := 0; start <= len(host); {
		end := start
		for end < len(host) && host[end] != '.' {
			end++
		}
		label := host[start:end]
		if len(label) == 0 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
				return false
			}
		}
		labels++
		last = label
		start = end + 1
	}
	if labels < 2 {
		return false
	}
	var tld [16]byte
	for _, c := range last {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	switch {
	case len(last) <= 1:
		return false
	case len(last) > len(tld):
		return false
	}
	for i, c := range last {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		tld[i] = c
	}
	t := string(tld[:len(last)])
	if len(last) == 2 {
		switch t {
		case "js", "rs", "py", "ts", "md", "sh", "go", "rb", "cs", "cc", "hs", "ml", "so":
			return false
		}
		return true
	}
	_, known := knownTLDs[t]
	return known
}

var knownTLDs = func() map[string]struct{} {
	m := make(map[string]struct{}, len(knownTLDList))
	for _, t := range knownTLDList {
		m[t] = struct{}{}
	}
	return m
}()

var knownTLDList = [...]string{
	"com", "net", "org", "edu", "gov", "mil", "int", "info", "biz", "name", "pro", "app", "dev", "io",
	"ai", "xyz", "site", "online", "tech", "cloud", "page", "shop", "store", "blog", "news", "berlin",
	"rocks", "world", "live", "life", "media", "email", "digital", "design", "studio", "agency", "club",
	"social", "space", "wiki", "zone", "today", "art", "fun", "one", "top", "link", "lol", "eu", "asia",
	"travel", "museum", "coop", "jobs", "mobi", "tel", "aero", "photos", "pics", "video", "music",
	"games", "team", "run", "ninja", "guru", "expert", "chat", "codes", "cool", "events", "health",
	"house", "land", "law", "money", "network", "party", "pub", "rest", "school", "science", "software",
	"solutions", "systems", "tools", "toys", "works", "academy", "bar", "beer", "bio", "cafe", "camp",
	"care", "center", "city", "coffee", "company", "fit", "fitness", "gallery", "group", "help", "host",
	"kitchen", "love", "market", "pizza", "press", "recipes", "review", "sale", "style", "tips",
	"vision", "watch", "wine", "yoga",
}
