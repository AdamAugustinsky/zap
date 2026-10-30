package markup

import (
	"bytes"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"gonativeui/ui"
)

// Seeds: the texts of the package's tests, and the edges of every rule.
var fuzzSeeds = []string{
	"", "a *bold* and _it_ and ~no~ and `code`", "2 * 3 = 6", "snake_case_name", "*unclosed", "* Guestlist",
	"> quoted\n- one\n```\nlet x = 1;\n```", "see https://a.b/c?d=1.", "go to spotifast.rocks!",
	"mail hello@section8berlin.com or dm", "(www.rust-lang.org)", "version 0.3.0 of main.rs and e.g. this",
	"abchttp://x", "https://example.com/testing(testing)", "read https://en.wikipedia.org/wiki/Foo_(bar).",
	"[https://example.com/a[1]]", "example.com/a(b)c, then",
	"CPF 000.000.000-00; call 000 0000-0000 or +00 (00) 00000-0000.", "Call +1 (212) 555-1212",
	"Date (2026-10-01)", "Date 2026-10-01 1234567", "Reference 12345678-90123456", "1234567890123456@example.com",
	"20:00 @174057861464188 (no pronouns)", "hi @123", "heading over @all tonight", "mail@all.com and @alligator",
	"say ```hello``` now", "```a```\nb", "```\ncode\n```\nafter", "> *hi* @174057861464188 www.a.com\n- ok",
	"  - *a.com*", "😂🙏👍", "👍🏽", "\xff\xfe*\xe2\x82*", "* x*", ">\n>\n> ", "• ◦ ", " ``` ",
	"_*~`x`~*_", "HTTP://X.COM/y MAILTO:a@b.com", "+", "(", "@", "@@all", "x\ny\n", "\n\n",
}

var fuzzMentions = []Mention{
	{User: []byte("174057861464188"), Name: []byte("Ana")},
	{User: []byte("all"), Name: []byte("all")},
	{User: []byte("12345"), Name: []byte("\xffodd\nname")},
}

// FuzzParse checks what the package comment promises of the spans of any
// bytes: in order, inside the text, never overlapping, on rune boundaries
// when the text is UTF-8, every byte not shown a marker, and each kind of
// span the raw bytes it says it is.
func FuzzParse(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s), true)
	}
	f.Fuzz(func(t *testing.T, text []byte, withMentions bool) {
		var mentions []Mention
		if withMentions {
			mentions = fuzzMentions
		}
		// Appending keeps what dst held.
		pre := []Span{{Start: 7, End: 9, Style: Bold}}
		spans := ParseMentions(pre, text, mentions)
		if spans[0] != pre[0] {
			t.Fatalf("%q: the caller's span was overwritten", text)
		}
		spans = spans[1:]
		if again := ParseMentions(nil, text, mentions); !equalSpans(again, spans) {
			t.Fatalf("%q: parsing is not deterministic", text)
		}
		// Every synthetic span replaces at least one byte and every other is
		// non-empty, so there are no more spans than bytes.
		if len(spans) > len(text) {
			t.Fatalf("%q: %d spans for %d bytes", text, len(spans), len(text))
		}
		valid := utf8.Valid(text)
		prev := 0
		for i, s := range spans {
			start, end := int(s.Start), int(s.End)
			if start < prev || end < start || end > len(text) {
				t.Fatalf("%q: span %d %+v out of order or range in %+v", text, i, s, spans)
			}
			if start == end {
				t.Fatalf("%q: empty span %d %+v", text, i, s)
			}
			if valid && (!runeEdge(text, start) || !runeEdge(text, end)) {
				t.Fatalf("%q: span %d %+v splits a rune", text, i, s)
			}
			// The bytes between two spans are hidden: markers, or the
			// white space around a ``` fence line.
			if gap := text[prev:start]; !hiddenOnly(gap) {
				t.Fatalf("%q: bytes %q at %d are neither shown nor a marker (%+v)", text, gap, prev, spans)
			}
			raw := text[start:end]
			switch {
			case s.Style&Mentioned != 0:
				if s.Link != NoLink || int(s.Arg) >= len(mentions) || raw[0] != '@' ||
					!bytes.Equal(raw[1:], mentions[s.Arg].User) {
					t.Fatalf("%q: mention span %+v over %q", text, s, raw)
				}
			case s.Style&QuoteBar != 0:
				if s.Style&Quote == 0 || (string(raw) != ">" && string(raw) != "> ") || (start > 0 && text[start-1] != '\n') {
					t.Fatalf("%q: quote bar %+v over %q", text, s, raw)
				}
			case s.Style&Bullet != 0:
				if m := string(raw); m != "* " && m != "- " && m != "• " && m != "◦ " {
					t.Fatalf("%q: bullet %+v over %q", text, s, raw)
				}
			case s.Link != NoLink:
				if s.Style&Mono != 0 {
					t.Fatalf("%q: a link in code: %+v", text, s)
				}
				checkTarget(t, text, s)
			}
			prev = end
		}
		if gap := text[prev:]; !hiddenOnly(gap) {
			t.Fatalf("%q: trailing bytes %q are neither shown nor a marker", text, gap)
		}
		// A span that is not a link has no target.
		for _, s := range spans {
			if s.Link == NoLink && len(Target(nil, text, s)) != 0 {
				t.Fatalf("%q: %+v is no link but has a target", text, s)
			}
		}
	})
}

// FuzzPlain checks the shown text against the raw one: without markers it
// is the text itself, otherwise no longer than the synthetic spans can make
// it, valid UTF-8 for valid input, and what ToRich and NameMentions build.
func FuzzPlain(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s), true)
	}
	f.Fuzz(func(t *testing.T, text []byte, withMentions bool) {
		var mentions []Mention
		maxName := 0
		if withMentions {
			mentions = fuzzMentions
			for _, m := range mentions {
				maxName = max(maxName, len(m.Name))
			}
		}
		spans := ParseMentions(nil, text, mentions)
		shown := AppendText([]byte("pre"), text, spans, mentions)
		if string(shown[:3]) != "pre" {
			t.Fatalf("%q: AppendText overwrote dst", text)
		}
		shown = shown[3:]
		if got := Plain(text, mentions); got != string(shown) {
			t.Fatalf("%q: Plain %q, AppendText %q", text, got, shown)
		}
		// A quote bar shows 4 bytes for 1, a bullet 5 for 2, a mention its
		// name for at least 5 bytes: the shown text stays a small multiple.
		if bound := 4*len(text) + maxName*(len(text)/5+1); len(shown) > bound {
			t.Fatalf("%q: %d shown bytes for %d raw", text, len(shown), len(text))
		}
		if utf8.Valid(text) && !utf8.Valid(shown) {
			// Mention names are data; only the raw text's runes count here.
			ok := false
			for _, m := range mentions {
				ok = ok || !utf8.Valid(m.Name)
			}
			if !ok {
				t.Fatalf("%q: shown text %q is not UTF-8", text, shown)
			}
		}
		if !hasMarkup(text) && !withMentions {
			if string(shown) != string(text) {
				t.Fatalf("%q has no markup but shows as %q", text, shown)
			}
		}
		if !withMentions {
			if got := NameMentions(text, nil); got != string(text) {
				t.Fatalf("%q: NameMentions without mentions changed it to %q", text, got)
			}
		} else if got := NameMentions(text, mentions); got != string(shown) {
			t.Fatalf("%q: NameMentions %q, Plain %q", text, got, shown)
		}

		body, rich := ToRich([]byte("x"), []ui.Span{{Off: 0, Len: 1}}, text, spans, mentions)
		if string(body[1:]) != string(shown) {
			t.Fatalf("%q: ToRich body %q, AppendText %q", text, body[1:], shown)
		}
		end := uint32(1)
		for _, r := range rich[1:] {
			if r.Off < end || r.Len == 0 || int(r.Off+r.Len) > len(body) {
				t.Fatalf("%q: rich span %+v out of order or range in %+v", text, r, rich)
			}
			end = r.Off + r.Len
			if r.Flags != 0 {
				if r.Tag == 0 || int(r.Tag) > len(spans) {
					t.Fatalf("%q: rich span %+v tags no span", text, r)
				}
				s := spans[r.Tag-1]
				if (r.Flags == ui.SpanLink) != (s.Link != NoLink) || (r.Flags == ui.SpanMention) != (s.Style&Mentioned != 0) {
					t.Fatalf("%q: rich span %+v for %+v", text, r, s)
				}
				if string(body[r.Off:r.Off+r.Len]) != string(appendShown(nil, text, &s, mentions)) {
					t.Fatalf("%q: rich span %+v is not its span's text", text, r)
				}
			}
		}
	})
}

// FuzzLinks checks Links: each address once, web and phone only, each one
// a span's target.
func FuzzLinks(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, text []byte) {
		links := Links(text)
		targets := map[string]bool{}
		for _, s := range Parse(nil, text) {
			if s.Link != NoLink {
				targets[string(Target(nil, text, s))] = true
			}
		}
		seen := map[string]bool{}
		for _, l := range links {
			if seen[l] {
				t.Fatalf("%q: %q listed twice in %q", text, l, links)
			}
			seen[l] = true
			if !targets[l] {
				t.Fatalf("%q: %q is no span's target", text, l)
			}
			if strings.HasPrefix(strings.ToLower(l), "mailto:") {
				t.Fatalf("%q: mail address %q listed", text, l)
			}
		}
		if len(strings.Join(links, "")) > 2*len(text)+8*len(links) {
			t.Fatalf("%q: links %q outgrow the text", text, links)
		}
	})
}

// FuzzBig checks the emoji-only rule against changes that must not make a
// big message of a small one or the reverse.
func FuzzBig(f *testing.F) {
	for _, s := range []string{"😂", "😂 🙏", "😂🙏👍", "😂🙏👍🔥", "ok 😂", "", "👍🏽", "1️⃣", "🇧🇷", "👨‍👩‍👧", "️", "‍", "\xff"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, text []byte) {
		if !Big(text) {
			return
		}
		if Big(append(append([]byte{}, text...), 'x')) {
			t.Fatalf("%q + \"x\" is still big", text)
		}
		if !Big(append(append([]byte{}, text...), ' ')) {
			t.Fatalf("%q + \" \" is not big", text)
		}
		for _, r := range string(text) {
			if r < utf8.RuneSelf && (unicode.IsLetter(r)) {
				t.Fatalf("%q: big with the letter %q", text, r)
			}
		}
	})
}

func equalSpans(a, b []Span) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func runeEdge(text []byte, off int) bool { return off == len(text) || utf8.RuneStart(text[off]) }

// hiddenOnly reports whether b is only bytes Parse may leave out: the
// inline markers, and the white space of a ``` fence line.
func hiddenOnly(b []byte) bool {
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		switch {
		case r == '*' || r == '_' || r == '~' || r == '`':
		case r != utf8.RuneError && unicode.IsSpace(r):
		default:
			return false
		}
		b = b[n:]
	}
	return true
}

// hasMarkup reports whether text has anything Parse may hide or replace:
// an inline marker, a quote, a list marker or a fence.
func hasMarkup(text []byte) bool {
	if bytes.ContainsAny(text, "*_~`>") {
		return true
	}
	for _, line := range bytes.Split(text, []byte("\n")) {
		body := bytes.TrimLeftFunc(line, unicode.IsSpace)
		for _, m := range []string{"- ", "• ", "◦ "} {
			if bytes.HasPrefix(body, []byte(m)) {
				return true
			}
		}
	}
	return false
}

func checkTarget(t *testing.T, text []byte, s Span) {
	t.Helper()
	target := string(Target(nil, text, s))
	lower := strings.ToLower(target)
	raw := text[s.Start:s.End]
	switch s.Link {
	case LinkWeb:
		if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
			t.Fatalf("%q: web target %q", text, target)
		}
		if !strings.HasSuffix(target, string(raw)) {
			t.Fatalf("%q: web target %q does not end in %q", text, target, raw)
		}
	case LinkMail:
		if !strings.HasPrefix(lower, "mailto:") || !strings.Contains(target, "@") {
			t.Fatalf("%q: mail target %q", text, target)
		}
	case LinkTel:
		digits := strings.TrimPrefix(strings.TrimPrefix(target, "tel:"), "+")
		if !strings.HasPrefix(target, "tel:") || len(digits) < 7 || len(digits) > 15 || strings.Trim(digits, "0123456789") != "" {
			t.Fatalf("%q: phone target %q from %q", text, target, raw)
		}
		if strings.ContainsAny(string(raw), ".\n") {
			t.Fatalf("%q: phone %q spans a dot or a line", text, raw)
		}
	default:
		t.Fatalf("%q: link kind %d", text, s.Link)
	}
	if bytes.ContainsAny(raw, " \n\t") && s.Link != LinkTel {
		t.Fatalf("%q: address %q holds white space", text, raw)
	}
}
