package emoji

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzPlain checks the presentation selector's removal: Plain is the text
// with every U+FE0F gone and nothing else changed, and the table finds an
// emoji with or without it.
func FuzzPlain(f *testing.F) {
	for _, s := range []string{
		"", "🦀", "❤️", "❤", "👋🏽", "🇧🇷", "1️⃣", "👨‍👩‍👧", "️", "️️", "a️b", "not an emoji",
		"\xef\xb8", "\xef\xb8\xef\xb8\x8f\x8f", "\xff️",
	} {
		f.Add(s)
	}
	tb := Get()
	f.Fuzz(func(t *testing.T, s string) {
		p := Plain(s)
		if len(p) > len(s) {
			t.Fatalf("%q: Plain grew it to %q", s, p)
		}
		if !strings.Contains(s, "️") && p != s {
			t.Fatalf("%q has no selector, but Plain is %q", s, p)
		}
		// Removing whole runes from valid UTF-8 leaves the other runes, in
		// order. (In invalid UTF-8 the bytes around a removed selector can
		// form a new one; no emoji is invalid UTF-8, so no lookup misses.)
		if utf8.ValidString(s) {
			if strings.Contains(p, "️") {
				t.Fatalf("%q: Plain %q keeps a selector", s, p)
			}
			if Plain(p) != p {
				t.Fatalf("%q: Plain is not idempotent", s)
			}
			var want []rune
			for _, r := range s {
				if r != 0xFE0F {
					want = append(want, r)
				}
			}
			if string(want) != p {
				t.Fatalf("%q: Plain %q, want %q", s, p, string(want))
			}
		}
		i := tb.Get(s)
		if j := tb.GetBytes([]byte(s)); i != j {
			t.Fatalf("%q: Get %d, GetBytes %d", s, i, j)
		}
		if i < -1 || int(i) >= len(tb.All) {
			t.Fatalf("%q: index %d", s, i)
		}
		if i >= 0 {
			if tb.Get(p) < 0 {
				t.Fatalf("%q is found, but not without its selector (%q)", s, p)
			}
			if Plain(tb.All[i].Glyph) != p {
				t.Fatalf("%q found %q", s, tb.All[i].Glyph)
			}
			if !tb.Matches(int(i), "") || !tb.Matches(int(i), tb.All[i].LName) {
				t.Fatalf("%q: does not match its own name", s)
			}
		}
	})
}
