package format

import (
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"zapfast/i18n"
)

var foldSeeds = []string{
	"", "estevao", "Estevão", "Töne", "TONE", "contact", "ÀÉÎÕÜ", "ǅ", "ΣΑΣ", "İstanbul", "ı", "ß", "ﬃ", "KK",
	"Å", "é", "́", "Ⱥ", "\xff", "a\xe2\x82", "😀", "日本語", " ", "ǰ",
}

// FuzzFold checks the search fold: idempotent, the same whether made by
// Fold, AppendFold or FoldKey, lower case, no larger than three times the
// input, and ContainsFold agreeing with a substring search of the folded
// text.
func FuzzFold(f *testing.F) {
	for _, s := range foldSeeds {
		f.Add(s, "e")
	}
	f.Add("Estevão Töne", "estevao")
	f.Add("Töne", "tone")
	f.Add("é́x", "ex")
	f.Fuzz(func(t *testing.T, s, q string) {
		folded := Fold(s)
		if Fold(folded) != folded {
			t.Fatalf("%q: Fold not idempotent: %q then %q", s, folded, Fold(folded))
		}
		if got := string(AppendFold([]byte("pre"), s)); got != "pre"+folded {
			t.Fatalf("%q: AppendFold %q, Fold %q", s, got, folded)
		}
		if got := string(FoldKey(s)); got != folded {
			t.Fatalf("%q: FoldKey %q, Fold %q", s, got, folded)
		}
		if !utf8.ValidString(folded) {
			t.Fatalf("%q: folded %q is not UTF-8", s, folded)
		}
		if len(folded) > 3*len(s) {
			t.Fatalf("%q: folded to %d bytes from %d", s, len(folded), len(s))
		}
		for _, r := range folded {
			if unicode.Is(unicode.Mn, r) {
				t.Fatalf("%q: folded %q keeps the mark %U", s, folded, r)
			}
			if r < utf8.RuneSelf && r >= 'A' && r <= 'Z' {
				t.Fatalf("%q: folded %q keeps an upper-case letter", s, folded)
			}
		}
		if s != "" && !ContainsFold([]byte(s), FoldKey(s)) {
			t.Fatalf("%q does not contain its own key", s)
		}
		if !ContainsFold([]byte(s), nil) {
			t.Fatalf("%q does not contain the empty key", s)
		}
		key := FoldKey(q)
		if got, want := ContainsFold([]byte(s), key), strings.Contains(folded, string(key)); got != want {
			t.Fatalf("ContainsFold(%q, key of %q) = %v; the folded text %q contains %q: %v", s, q, got, folded, string(key), want)
		}
		// Every piece of a valid text is found in it, in any case.
		if utf8.ValidString(s) && len(q) > 0 {
			i, j := int(q[0])%(len(s)+1), int(q[len(q)-1])%(len(s)+1)
			if i > j {
				i, j = j, i
			}
			for i > 0 && i < len(s) && !utf8.RuneStart(s[i]) {
				i--
			}
			for j < len(s) && !utf8.RuneStart(s[j]) {
				j++
			}
			sub := s[i:j]
			if !ContainsFold([]byte(s), FoldKey(strings.ToUpper(sub))) && strings.ToLower(strings.ToUpper(sub)) == strings.ToLower(sub) {
				t.Fatalf("%q does not contain its piece %q upper-cased", s, sub)
			}
		}
	})
}

// FuzzPhone checks AppendPhone: no digits append nothing; otherwise a "+",
// then exactly the input's digits in order, grouped by single spaces,
// "(", ")" and "-"; dst kept; the same for its own output; bounded.
func FuzzPhone(f *testing.F) {
	for _, s := range []string{
		"393331234567", "15551234567", "79161234567", "584241234567", "2348031234567", "97150123456",
		"5511999999999", "551140028922", "+55 (11) 99999-9999", "", "+1 (555) 000-0000", "7", "5", "55", "5511",
		"abc", "1234567890123456789012345678901234567", "٣٤٥", "55\xff11999999999", "+",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		var digits []byte
		for i := 0; i < len(in); i++ {
			if c := in[i]; c >= '0' && c <= '9' {
				digits = append(digits, c)
			}
		}
		out := AppendPhone([]byte("tel: "), in)
		if string(out[:5]) != "tel: " {
			t.Fatalf("%q: dst overwritten", in)
		}
		got := string(out[5:])
		if len(digits) == 0 {
			if got != "" {
				t.Fatalf("%q: no digits, but %q", in, got)
			}
			return
		}
		if got[0] != '+' {
			t.Fatalf("%q: %q has no plus", in, got)
		}
		var kept []byte
		for i := 1; i < len(got); i++ {
			switch c := got[i]; {
			case c >= '0' && c <= '9':
				kept = append(kept, c)
			case c == ' ' || c == '(' || c == ')' || c == '-':
				if got[i-1] == ' ' && c == ' ' {
					t.Fatalf("%q: %q has two spaces", in, got)
				}
			default:
				t.Fatalf("%q: %q holds %q", in, got, c)
			}
		}
		if string(kept) != string(digits) {
			t.Fatalf("%q: %q keeps the digits %q, not %q", in, got, kept, digits)
		}
		if strings.HasSuffix(got, " ") || strings.HasSuffix(got, "-") {
			t.Fatalf("%q: %q ends in a separator", in, got)
		}
		if again := Phone(got); again != got {
			t.Fatalf("%q: %q formats again as %q", in, got, again)
		}
		if len(got) > 2*len(digits)+4 {
			t.Fatalf("%q: %d bytes for %d digits", in, len(got), len(digits))
		}
	})
}

// FuzzDurations checks the clock and the words of a length read back as
// the seconds they were made from.
func FuzzDurations(f *testing.F) {
	for _, s := range []uint64{0, 5, 45, 59, 60, 65, 75, 720, 754, 3599, 3600, 3725, 86399, 86400, 1<<32 - 1, 1 << 40, 1<<63 - 1, 1<<64 - 1} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, secs uint64) {
		// "m:ss" for media.
		d := uint32(secs)
		got := string(AppendDuration([]byte("x"), d))[1:]
		m, ss, ok := strings.Cut(got, ":")
		mv, err1 := strconv.ParseUint(m, 10, 64)
		sv, err2 := strconv.ParseUint(ss, 10, 64)
		if !ok || err1 != nil || err2 != nil || len(ss) != 2 || sv > 59 || mv*60+sv != uint64(d) {
			t.Fatalf("AppendDuration(%d) = %q", d, got)
		}
		// "m:ss" or "h:mm:ss" for a call; the hours of a uint64 fit an int on
		// 64-bit platforms only.
		if strconv.IntSize == 64 && secs <= 1<<62 {
			got = string(AppendCallDuration(nil, secs))
			parts := strings.Split(got, ":")
			var total uint64
			for i, p := range parts {
				v, err := strconv.ParseUint(p, 10, 64)
				if err != nil || (i > 0 && (len(p) != 2 || v > 59)) || (len(parts) == 3 && i == 0 && v == 0) {
					t.Fatalf("AppendCallDuration(%d) = %q", secs, got)
				}
				total = total*60 + v
			}
			if len(parts) < 2 || len(parts) > 3 || (len(parts) == 3) != (secs >= 3600) || total != secs {
				t.Fatalf("AppendCallDuration(%d) = %q (%d)", secs, got, total)
			}
			// "1 h 2 min" in English: the parts that are not zero, seconds
			// only under an hour.
			words := string(AppendCallLength(nil, i18n.English, secs))
			f := strings.Fields(words)
			var h, mn, s uint64
			if len(f) == 0 || len(f)%2 != 0 || len(f) > 4 {
				t.Fatalf("AppendCallLength(%d) = %q", secs, words)
			}
			for i := 0; i < len(f); i += 2 {
				v, err := strconv.ParseUint(f[i], 10, 64)
				if err != nil {
					t.Fatalf("AppendCallLength(%d) = %q", secs, words)
				}
				switch f[i+1] {
				case "h":
					h = v
				case "min":
					mn = v
				case "s":
					s = v
				default:
					t.Fatalf("AppendCallLength(%d) = %q", secs, words)
				}
			}
			want := secs
			if secs >= 3600 {
				want -= secs % 60
			}
			if h*3600+mn*60+s != want {
				t.Fatalf("AppendCallLength(%d) = %q", secs, words)
			}
			for _, l := range i18n.All {
				if w := AppendCallLength(nil, l, secs); len(w) == 0 || len(w) > 64 || strings.Contains(string(w), "{") || !utf8.Valid(w) {
					t.Fatalf("AppendCallLength(%v, %d) = %q", l, secs, w)
				}
			}
		}
		// A time of day reads back.
		hour, minute := int(secs%24), int(secs/24%60)
		got = string(AppendTimeOfDay(nil, hour, minute, false))
		if want := strconv.Itoa(hour/10) + strconv.Itoa(hour%10) + ":" + strconv.Itoa(minute/10) + strconv.Itoa(minute%10); got != want {
			t.Fatalf("AppendTimeOfDay(%d, %d) = %q", hour, minute, got)
		}
		got = string(AppendTimeOfDay(nil, hour, minute, true))
		clock, mer, _ := strings.Cut(got, " ")
		hh, mm, _ := strings.Cut(clock, ":")
		hv, err := strconv.Atoi(hh)
		if err != nil || hv < 1 || hv > 12 || len(mm) != 2 || (mer != "AM" && mer != "PM") {
			t.Fatalf("AppendTimeOfDay(%d, %d, 12) = %q", hour, minute, got)
		}
		if back := hv%12 + map[string]int{"AM": 0, "PM": 12}[mer]; back != hour || mm != got[len(clock)-2:len(clock)] {
			t.Fatalf("AppendTimeOfDay(%d, %d, 12) = %q", hour, minute, got)
		}
	})
}
