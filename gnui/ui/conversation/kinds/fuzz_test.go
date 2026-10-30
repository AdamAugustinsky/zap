package kinds

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzParseVcard checks the reading of a shared contact's card: a card is
// usable only with a telephone number of at least seven digits; its account
// is that many digits or none; the name falls back to the message's; the
// line endings (LF or CRLF) change nothing; and nothing it returns outgrows
// the card.
func FuzzParseVcard(f *testing.F) {
	for _, s := range []string{
		"BEGIN:VCARD\r\nVERSION:3.0\r\nN:Lovelace;Ada Augusta;;;\r\nFN:Ada Augusta Lovelace\r\nTEL;type=CELL;waid=5511999990001:+55 11 99999-0001\r\nEND:VCARD",
		"BEGIN:VCARD\r\nFN:Bob\r\nTEL:011 99999-0002\r\nEND:VCARD",
		"BEGIN:VCARD\nVERSION:3.0\nFN:Dorothy Vaughan\nTEL;TYPE=CELL;waid=15550003333:+1 555-000-3333\nEND:VCARD",
		"BEGIN:VCARD\nEND:VCARD", "",
		"BEGIN:VCARD\nFN:A\nTEL:+1 555 000 1111\nTEL;PREF=2:+1 555 000 2222\nitem1.TEL;TYPE=pref,cell:+1 555 000 3333\nEND:VCARD",
		"BEGIN:VCARD\nN:Doe\\;Jr;Jane\\nMary;Q\nTEL;PREF=\"1\":555\n 0001234\nEND:VCARD\nBEGIN:VCARD\nFN:Second\nEND:VCARD",
		"FN:no begin\nTEL:+44 20 7946 0958", "TEL;WAID=\"123\":+49 176 3114 1665", "TEL;PREF=999999999999999999999:5550001",
		"BEGIN:VCARD\r\n\tFN:fold\r\nTEL:\xff5550001234\r\nEND:VCARD", "N:\\", ":", ";;;:", "TEL;=:1234567",
	} {
		f.Add(s, "Fallback")
	}
	f.Fuzz(func(t *testing.T, vcard, fallback string) {
		c := parseVcard(vcard, fallback)
		if !c.ok {
			if c != (sharedContact{name: fallback}) {
				t.Fatalf("%q: no card, but %+v", vcard, c)
			}
		} else {
			if n := len(digitsOf(c.number)); n < 7 {
				t.Fatalf("%q: number %q has %d digits", vcard, c.number, n)
			}
			if c.number != strings.TrimSpace(c.number) {
				t.Fatalf("%q: number %q is not trimmed", vcard, c.number)
			}
			if c.name == "" && fallback != "" {
				t.Fatalf("%q: no name, and the fallback %q was not used", vcard, fallback)
			}
			if c.account != "" && (len(c.account) < 7 || digitsOf(c.account) != c.account) {
				t.Fatalf("%q: account %q", vcard, c.account)
			}
			if c.account != "" && !strings.Contains(digitsOf(vcard), c.account) && !strings.Contains(vcard, "\n ") && !strings.Contains(vcard, "\n\t") {
				t.Fatalf("%q: account %q is not in the card", vcard, c.account)
			}
			// Each field is from one property's value; a folded property
			// joins lines, a first name drops escapes, an invalid byte
			// becomes U+FFFD (three bytes).
			if len(c.number) > len(vcard) || len(c.account) > len(vcard) || len(c.firstName) > 3*len(vcard)+1 ||
				(c.name != fallback && len(c.name) > len(vcard)) {
				t.Fatalf("%q: %+v outgrows the card", vcard, c)
			}
		}
		crlf := parseVcard(strings.ReplaceAll(vcard, "\n", "\r\n"), fallback)
		if crlf != c {
			t.Fatalf("%q: %+v, with CRLF %+v", vcard, c, crlf)
		}
	})
}

// FuzzVcardFirstName checks the N value's given and additional names: for
// a value with no escapes, the second and third ";" fields, trimmed, the
// empty ones left out, joined by a space.
func FuzzVcardFirstName(f *testing.F) {
	for _, s := range []string{"Lovelace;Ada Augusta;;;", "Doe\\;Jr;Jane\\nMary;Q", ";;", "", "a;b;c;d;e", " ; x ; y ", "\\", "a;\xff"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, value string) {
		got := vcardFirstName(value)
		if got != strings.TrimSpace(got) || strings.Contains(got, "  ") && !strings.Contains(value, "  ") && !strings.Contains(value, "\\") {
			t.Fatalf("%q: %q", value, got)
		}
		if utf8.ValidString(value) && !utf8.ValidString(got) {
			t.Fatalf("%q: %q is not UTF-8", value, got)
		}
		if len(got) > 3*len(value)+1 {
			t.Fatalf("%q: %q outgrows it", value, got)
		}
		if strings.Contains(value, "\\") || !utf8.ValidString(value) {
			return
		}
		parts := strings.Split(value, ";")
		var want []string
		for _, i := range []int{1, 2} {
			if i < len(parts) {
				if p := strings.TrimSpace(parts[i]); p != "" {
					want = append(want, p)
				}
			}
		}
		if w := strings.Join(want, " "); got != w {
			t.Fatalf("%q: %q, want %q", value, got, w)
		}
	})
}

// FuzzVcardTelPreference checks a TEL property's PREF rank: when there is
// one it is 1 to 100, and it is the smallest the parameters give.
func FuzzVcardTelPreference(f *testing.F) {
	for _, s := range []string{"TEL;type=CELL;waid=5511999990001", "TEL;PREF=2", "TEL;TYPE=pref,cell", "TEL;pref", "TEL;PREF=\"1\";PREF=50", "TEL;PREF=0", "TEL;PREF=101", "TEL;PREF=", "TEL"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, property string) {
		rank, ok := vcardTelPreference(property)
		if !ok {
			if rank != 0 {
				t.Fatalf("%q: rank %d without a preference", property, rank)
			}
			return
		}
		if rank < 1 || rank > 100 {
			t.Fatalf("%q: rank %d", property, rank)
		}
		// Adding a better preference wins; adding a worse one changes nothing.
		if r, ok := vcardTelPreference(property + ";PREF=1"); !ok || r != 1 {
			t.Fatalf("%q;PREF=1: rank %d, %v", property, r, ok)
		}
		if r, ok := vcardTelPreference(property + ";PREF=100"); !ok || r != rank {
			t.Fatalf("%q;PREF=100: rank %d, %v; was %d", property, r, ok, rank)
		}
	})
}
