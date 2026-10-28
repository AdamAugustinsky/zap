package markup

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"gonativeui/ui"
)

// view is a span as upstream's tests see it: its shown text, its flags and
// its link target.
type view struct {
	Text                       string
	Bold, Italic, Strike, Mono bool
	Link                       string
	Mention, Quote             bool
}

func views(text string, mentions []Mention) []view {
	b := []byte(text)
	var out []view
	for _, s := range ParseMentions(nil, b, mentions) {
		out = append(out, view{
			Text:    string(appendShown(nil, b, &s, mentions)),
			Bold:    s.Style&Bold != 0,
			Italic:  s.Style&Italic != 0,
			Strike:  s.Style&Strike != 0,
			Mono:    s.Style&Mono != 0,
			Link:    string(Target(nil, b, s)),
			Mention: s.Style&Mentioned != 0,
			Quote:   s.Style&Quote != 0,
		})
	}
	return out
}

func plain(text string, mentions []Mention) string { return Plain([]byte(text), mentions) }

func linksOf(text string) []string {
	var out []string
	for _, v := range views(text, nil) {
		if v.Link != "" {
			out = append(out, v.Link)
		}
	}
	return out
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

func TestInlineMarkersPairUp(t *testing.T) {
	type k struct {
		text                       string
		bold, italic, strike, mono bool
	}
	var got []k
	for _, v := range views("a *bold* and _it_ and ~no~ and `code`", nil) {
		got = append(got, k{v.Text, v.Bold, v.Italic, v.Strike, v.Mono})
	}
	eq(t, got, []k{
		{"a ", false, false, false, false},
		{"bold", true, false, false, false},
		{" and ", false, false, false, false},
		{"it", false, true, false, false},
		{" and ", false, false, false, false},
		{"no", false, false, true, false},
		{" and ", false, false, false, false},
		{"code", false, false, false, true},
	})
}

func TestStrayMarkersStayLiteral(t *testing.T) {
	eq(t, plain("2 * 3 = 6", nil), "2 * 3 = 6")
	eq(t, plain("snake_case_name", nil), "snake_case_name")
	eq(t, plain("*unclosed", nil), "*unclosed")
	eq(t, plain("* Guestlist", nil), "•  Guestlist")
}

func TestBlocksAndLists(t *testing.T) {
	spans := views("> quoted\n- one\n```\nlet x = 1;\n```", nil)
	eq(t, spans[0].Text, "▎ ")
	if !spans[0].Quote {
		t.Error("the bar is not a quote span")
	}
	eq(t, spans[1].Text, "quoted")
	var bullet, code bool
	for _, s := range spans {
		bullet = bullet || s.Text == "•  "
		code = code || (s.Mono && s.Text == "let x = 1;")
	}
	if !bullet || !code {
		t.Errorf("bullet %v code %v in %+v", bullet, code, spans)
	}
}

func TestAddressesAreFoundWithAndWithoutAScheme(t *testing.T) {
	eq(t, linksOf("see https://a.b/c?d=1."), []string{"https://a.b/c?d=1"})
	eq(t, linksOf("go to spotifast.rocks!"), []string{"https://spotifast.rocks"})
	eq(t, linksOf("mail hello@section8berlin.com or dm"), []string{"mailto:hello@section8berlin.com"})
	eq(t, linksOf("(www.rust-lang.org)"), []string{"https://www.rust-lang.org"})
	eq(t, linksOf("version 0.3.0 of main.rs and e.g. this"), []string(nil))
	eq(t, linksOf("abchttp://x"), []string(nil))
}

func TestAddressesKeepTheBracketsTheyOpen(t *testing.T) {
	eq(t, linksOf("https://example.com/testing(testing)"), []string{"https://example.com/testing(testing)"})
	eq(t, linksOf("read https://en.wikipedia.org/wiki/Foo_(bar)."), []string{"https://en.wikipedia.org/wiki/Foo_(bar)"})
	eq(t, linksOf("(see https://en.wikipedia.org/wiki/Foo_(bar))"), []string{"https://en.wikipedia.org/wiki/Foo_(bar)"})
	eq(t, linksOf("(see https://example.com)"), []string{"https://example.com"})
	eq(t, linksOf("[https://example.com/a[1]]"), []string{"https://example.com/a[1]"})
	eq(t, linksOf("example.com/a(b)c, then"), []string{"https://example.com/a(b)c"})
	spans := views("(see https://example.com)", nil)
	eq(t, spans[len(spans)-1].Text, ")")
}

func TestPhoneNumbersBecomeLinksWithoutLinkingTaxIDs(t *testing.T) {
	eq(t, linksOf("CPF 000.000.000-00; call 000 0000-0000 or +00 (00) 00000-0000."),
		[]string{"tel:00000000000", "tel:+0000000000000"})
	eq(t, linksOf("Call (212) 555-1212"), []string{"tel:2125551212"})
	shown := func(text string) string {
		for _, v := range views(text, nil) {
			if v.Link != "" {
				return v.Text
			}
		}
		return ""
	}
	eq(t, shown("Call (212) 555-1212"), "(212) 555-1212")
	eq(t, linksOf("Call +1 (212) 555-1212"), []string{"tel:+12125551212"})
	eq(t, shown("Call +1 (212) 555-1212"), "+1 (212) 555-1212")
	eq(t, linksOf("Date 2026-10-01"), []string(nil))
	eq(t, linksOf("Date (2026-10-01)"), []string(nil))
	eq(t, linksOf("Date 2026-10-01 12:30"), []string(nil))
	eq(t, linksOf("Date 2026-10-01 1234567"), []string{"tel:1234567"})
	eq(t, linksOf("Reference 12345678-90123456"), []string(nil))
	eq(t, linksOf("1234567@example.com"), []string{"mailto:1234567@example.com"})
	eq(t, linksOf("1234567890123456@example.com"), []string{"mailto:1234567890123456@example.com"})
	eq(t, linksOf("id 000000; long id 0000000000000000"), []string(nil))
	eq(t, linksOf("abc00000000000"), []string(nil))
}

func TestMentionsBecomeNames(t *testing.T) {
	mentions := []Mention{{User: []byte("174057861464188"), Name: []byte("+49 176 31141665")}}
	var found *view
	for _, v := range views("20:00 @174057861464188 (no pronouns)", mentions) {
		if v.Mention {
			v := v
			found = &v
		}
	}
	if found == nil {
		t.Fatal("no mention")
	}
	eq(t, found.Text, "@+49 176 31141665")
	eq(t, plain("hi @174057861464188", mentions), "hi @+49 176 31141665")
	eq(t, plain("hi @123", mentions), "hi @123")
}

func TestEveryoneIsHighlightedOnlyWithItsMention(t *testing.T) {
	everyone := []Mention{{User: []byte("all"), Name: []byte("all")}}
	var found *view
	for _, v := range views("heading over @all tonight", everyone) {
		if v.Mention {
			v := v
			found = &v
		}
	}
	if found == nil {
		t.Fatal("no mention")
	}
	eq(t, found.Text, "@all")
	for _, v := range views("heading over @all tonight", nil) {
		if v.Mention {
			t.Error("@all is a mention without its mention row")
		}
	}
	for _, v := range views("mail@all.com and @alligator", everyone) {
		if v.Mention {
			t.Errorf("%q is not a mention", v.Text)
		}
	}
	eq(t, plain("ping @all", everyone), "ping @all")
}

func TestTripleBackticksMarkMonospaceOnALine(t *testing.T) {
	type k struct {
		text string
		mono bool
	}
	mono := func(text string) []k {
		var out []k
		for _, v := range views(text, nil) {
			out = append(out, k{v.Text, v.Mono})
		}
		return out
	}
	eq(t, mono("say ```hello``` now"), []k{{"say ", false}, {"hello", true}, {" now", false}})
	eq(t, mono("```hello```"), []k{{"hello", true}})
	// Preserve all text after an unmatched marker.
	eq(t, mono("```a```\nb"), []k{{"a", true}, {"\n", false}, {"b", false}})
}

func TestAFenceLineStillOpensABlock(t *testing.T) {
	var code, after bool
	for _, v := range views("```\ncode\n```\nafter", nil) {
		code = code || (v.Text == "code" && v.Mono)
		after = after || (v.Text == "after" && !v.Mono)
	}
	if !code || !after {
		t.Errorf("code %v after %v", code, after)
	}
}

func TestWebLinksAreListedOnceWithoutMailAddresses(t *testing.T) {
	eq(t, Links([]byte("see https://a.b/c, then spotifast.rocks and https://a.b/c again")),
		[]string{"https://a.b/c", "https://spotifast.rocks"})
	eq(t, len(Links([]byte("mail hello@section8berlin.com or nothing"))), 0)
	eq(t, len(Links([]byte("version 0.3.0 of main.rs"))), 0)
}

// The behaviours the Go representation adds: raw offsets, styles around links
// and quotes, and the synthetic spans.
func TestSpansCoverTheRawTextByOffset(t *testing.T) {
	text := []byte("> *hi* https://a.com/x")
	spans := Parse(nil, text)
	eq(t, spans[0], Span{Start: 0, End: 2, Style: Quote | QuoteBar})
	eq(t, spans[1], Span{Start: 3, End: 5, Style: Bold | Quote})
	eq(t, spans[2], Span{Start: 6, End: 7, Style: Quote})
	last := spans[len(spans)-1]
	eq(t, last, Span{Start: 7, End: 22, Style: Quote, Link: LinkWeb})
	eq(t, string(Target(nil, text, last)), "https://a.com/x")

	// A bold link keeps its bold, a bullet keeps the indent.
	spans = Parse(nil, []byte("  - *a.com*"))
	eq(t, spans[0], Span{Start: 0, End: 2})
	eq(t, spans[1], Span{Start: 2, End: 4, Style: Bullet})
	eq(t, spans[2], Span{Start: 5, End: 10, Style: Bold, Link: LinkWeb})
	eq(t, plain("  - *a.com*", nil), "  •  a.com")

	eq(t, string(Target(nil, []byte("HTTP://X.COM/y"), Span{Start: 0, End: 14, Link: LinkWeb})), "HTTP://X.COM/y")
	eq(t, string(Target(nil, []byte("MAILTO:a@b.com"), Span{Start: 0, End: 14, Link: LinkMail})), "MAILTO:a@b.com")
	eq(t, string(Target(nil, []byte("text"), Span{Start: 0, End: 4})), "")
}

func TestBigIsOneToThreeEmojiAlone(t *testing.T) {
	for text, want := range map[string]bool{
		"😂": true, "😂 🙏": true, "😂🙏👍": true, "😂🙏👍🔥": false, "ok 😂": false, "": false, "👍🏽": true,
	} {
		if got := Big([]byte(text)); got != want {
			t.Errorf("Big(%q) = %v", text, got)
		}
	}
}

func TestToRichMapsSpansToTheFrameworkInput(t *testing.T) {
	text := []byte("> *hi* @174057861464188 www.a.com\n- ok")
	mentions := []Mention{{User: []byte("174057861464188"), Name: []byte("Ana")}}
	spans := ParseMentions(nil, text, mentions)
	body, rich := ToRich(nil, nil, text, spans, mentions)
	if string(body) != "▎ hi @Ana www.a.com\n•  ok" {
		t.Fatalf("body %q", body)
	}
	var sawBar, sawBold, sawMention, sawLink bool
	prevEnd := uint32(0)
	for _, s := range rich {
		if s.Off < prevEnd || int(s.Off+s.Len) > len(body) {
			t.Fatalf("spans not sorted or out of range: %+v", rich)
		}
		prevEnd = s.Off + s.Len
		got := string(body[s.Off : s.Off+s.Len])
		switch {
		case s.Style == styleBar:
			sawBar = got == "▎ "
		case s.Style == 1:
			sawBold = got == "hi"
		case s.Flags == ui.SpanMention:
			sawMention = got == "@Ana" && s.Style == styleMention
		case s.Flags == ui.SpanLink:
			sawLink = got == "www.a.com" && s.Style == styleLink
			if s.Tag == 0 || string(Target(nil, text, spans[s.Tag-1])) != "https://www.a.com" {
				t.Errorf("link tag %d", s.Tag)
			}
		}
	}
	if !sawBar || !sawBold || !sawMention || !sawLink {
		t.Errorf("bar %v bold %v mention %v link %v: %+v", sawBar, sawBold, sawMention, sawLink, rich)
	}
	st := RichStyles(Fonts{Regular: 1, Bold: 2, Italic: 3, BoldItalic: 4, Mono: 5}, Colors{Link: 0xff, Mention: 0xee, Secondary: 0xdd})
	if st.N != styleBar+1 || st.Styles[styleLink+1].Font != 2 || st.Styles[styleLink+4].Deco&ui.DecoStrike == 0 ||
		st.Styles[styleLink].Deco&ui.DecoUnderline == 0 || st.Styles[3].Font != 4 || st.Styles[styleMono].Font != 5 {
		t.Errorf("styles %+v", st)
	}
}

func TestParsingIntoAReusedBufferAllocatesNothing(t *testing.T) {
	text := []byte("> *bold* _it_ ~x~ `code` ```mono``` https://a.com/x?y=(1) hello@section8berlin.com " +
		"+1 (212) 555-1212 @174057861464188 @all\n- item\n```\nblock\n```\n2 * 3 and snake_case")
	mentions := []Mention{{User: []byte("174057861464188"), Name: []byte("Ana")}, {User: []byte("all"), Name: []byte("all")}}
	buf := make([]Span, 0, 256)
	if n := len(ParseMentions(buf[:0], text, mentions)); n < 20 {
		t.Fatalf("only %d spans", n)
	}
	if n := testing.AllocsPerRun(100, func() { buf = ParseMentions(buf[:0], text, mentions) }); n != 0 {
		t.Errorf("%v allocations per parse", n)
	}
	body := make([]byte, 0, 512)
	rich := make([]ui.Span, 0, 256)
	if n := testing.AllocsPerRun(100, func() { body, rich = ToRich(body[:0], rich[:0], text, buf, mentions) }); n != 0 {
		t.Errorf("%v allocations per ToRich", n)
	}
	tgt := make([]byte, 0, 128)
	if n := testing.AllocsPerRun(100, func() {
		for _, s := range buf {
			tgt = Target(tgt[:0], text, s)
		}
	}); n != 0 {
		t.Errorf("%v allocations per Target", n)
	}
}

// Whatever the bytes (invalid UTF-8, unmatched markers, odd Unicode), spans
// stay inside the text, in order, and never overlap.
func TestSpansAreOrderedAndInRangeOnAnyInput(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []string{"*", "_", "~", "`", "```", "\n", "> ", ">", "- ", "* ", "• ", " ", "a", "Z", "1", "5551234567",
		"+", "(", ")", "@", "@all", "@174057861464188", "http://", "https://", "www.", ".com", "x.org/y", "a@b.com", "é", "世界",
		"😀", "\xff", "\xe2\x82", "-", ".", ",", "[", "]", "\u00a0", "2026-10-01"}
	mentions := []Mention{{User: []byte("174057861464188"), Name: []byte("N")}, {User: []byte("all"), Name: []byte("all")}}
	for n := 0; n < 3000; n++ {
		var b strings.Builder
		for i := rng.Intn(24); i >= 0; i-- {
			b.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		text := []byte(b.String())
		spans := ParseMentions(nil, text, mentions)
		prev := uint32(0)
		for _, s := range spans {
			if s.Start < prev || s.End < s.Start || int(s.End) > len(text) {
				t.Fatalf("%q: bad span %+v in %+v", text, s, spans)
			}
			prev = s.End
		}
		if utf8.Valid(text) {
			if !utf8.Valid(AppendText(nil, text, spans, mentions)) {
				t.Fatalf("%q: shown text is not UTF-8", text)
			}
		}
		_ = Links(text)
		_, _ = ToRich(nil, nil, text, spans, mentions)
	}
}

// A Meta AI reply reaches the window as the text the worker makes of it (a
// code block in fences, a table as lines of "a | b"), and upstream draws it
// as plain markup: the code in monospace, every table line as it is, with no
// grid (MK-031, zf:src/demo.rs meta_ai_sample).
func TestAMetaAIReplyWithCodeAndATable(t *testing.T) {
	const reply = "Collect its characters in reverse order:\n\n```\nlet reversed: String = text.chars().rev().collect();\n```\n\nMethod | Handles\nchars().rev() | Unicode scalar values\ngraphemes(true).rev() | Combined emoji and accents"
	var code, rest strings.Builder
	for _, s := range views(reply, nil) {
		if s.Mono {
			code.WriteString(s.Text)
		} else {
			rest.WriteString(s.Text)
		}
		if s.Link != "" {
			t.Errorf("%q became a link to %q", s.Text, s.Link)
		}
	}
	eq(t, code.String(), "let reversed: String = text.chars().rev().collect();")
	for _, line := range []string{"Method | Handles", "chars().rev() | Unicode scalar values", "graphemes(true).rev() | Combined emoji and accents"} {
		if !strings.Contains(rest.String(), line) {
			t.Errorf("the table line %q is not in the text: %q", line, rest.String())
		}
	}
}
