package i18n

import (
	"strings"
	"testing"
)

var tagSeeds = []string{
	"pt-BR", "pt_PT", "pt_BR.UTF-8", "de-DE", "es", "zh-Hans", "zh", "zh_CN.GB2312", "zh-Hans-TW", "zh-Hant-CN",
	"zh-TW", "zh-HK", "zh-MO", "zh-CHT", "zh_TW.UTF-8", "tr_TR.UTF-8", "id-ID", "en-US", "ja-JP", "", "-", ".UTF-8",
	"C", "POSIX", "nb-NO", "xx", "en", "zh-Hant", "de_DE@euro", "ZH-hant", "zh--hant", "_zh", "zh-Latn-Hant",
	"\xff", "ıd", "İD",
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// FuzzParseTag checks the stored tag: ParseTag accepts exactly the tags
// Tag gives and inverts it, and UnmarshalText reads what MarshalText wrote.
func FuzzParseTag(f *testing.F) {
	for _, s := range tagSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, tag string) {
		l, ok := ParseTag(tag)
		if ok != (l.Tag() == tag) {
			t.Fatalf("ParseTag(%q) = %v, %v; its tag is %q", tag, l, ok, l.Tag())
		}
		if !ok && l != English {
			t.Fatalf("ParseTag(%q) = %v without a match", tag, l)
		}
		var u Locale = Russian
		if err := u.UnmarshalText([]byte(tag)); err != nil || u != l {
			t.Fatalf("UnmarshalText(%q) = %v, %v", tag, u, err)
		}
		if b, _ := l.MarshalText(); string(b) != l.Tag() {
			t.Fatalf("MarshalText(%v) = %q", l, b)
		}
	})
}

// FuzzFromSystem checks the system tag's reading: a locale found is found
// again by its own tag; letter case, "-" against "_", and a POSIX encoding
// or modifier change nothing; an explicit Chinese script decides; and
// Resolve takes an explicit choice before the system's.
func FuzzFromSystem(f *testing.F) {
	for _, s := range tagSeeds {
		f.Add(s, "de")
	}
	f.Fuzz(func(t *testing.T, id, choice string) {
		l, ok := FromSystem(id)
		if !ok && l != English {
			t.Fatalf("FromSystem(%q) = %v without a match", id, l)
		}
		if l >= numLocales {
			t.Fatalf("FromSystem(%q) = %d", id, l)
		}
		if ok {
			if back, ok2 := FromSystem(l.Tag()); !ok2 || back != l {
				t.Fatalf("FromSystem(%q) = %v, but its tag %q reads as %v, %v", id, l, l.Tag(), back, ok2)
			}
		}
		if isASCII(id) {
			for _, v := range []string{strings.ToUpper(id), strings.ToLower(id), strings.ReplaceAll(id, "-", "_"), strings.ReplaceAll(id, "_", "-")} {
				if l2, ok2 := FromSystem(v); l2 != l || ok2 != ok {
					t.Fatalf("FromSystem(%q) = %v, %v but FromSystem(%q) = %v, %v", id, l, ok, v, l2, ok2)
				}
			}
			if !strings.ContainsAny(id, ".@") {
				for _, suffix := range []string{".UTF-8", "@euro", ".ISO8859-1@euro"} {
					if l2, ok2 := FromSystem(id + suffix); l2 != l || ok2 != ok {
						t.Fatalf("FromSystem(%q) = %v, %v but with %q %v, %v", id, l, ok, suffix, l2, ok2)
					}
				}
				// zh with an explicit script before anything else in the tag.
				if tag, ok := FromSystem("zh-Hant-" + id); !ok || tag != ChineseTraditional {
					t.Fatalf("zh-Hant-%s reads as %v", id, tag)
				}
				if tag, ok := FromSystem("zh_hans_" + id); !ok || tag != ChineseSimplified {
					t.Fatalf("zh_hans_%s reads as %v", id, tag)
				}
			}
		}
		restore := SetSystemLanguages(func() []string { return []string{id} })
		defer restore()
		detected := Detect()
		if detected != l {
			t.Fatalf("Detect with the system on %q = %v, FromSystem %v", id, detected, l)
		}
		want, explicit := ParseTag(choice)
		if !explicit {
			want = detected
		}
		if got := Resolve(choice); got != want {
			t.Fatalf("Resolve(%q) with the system on %q = %v, want %v", choice, id, got, want)
		}
	})
}

// FuzzFormat checks Format against chained strings.ReplaceAll, as its
// comment promises: nothing to fill leaves s as it is, and a value is
// searched by the later pairs only.
func FuzzFormat(f *testing.F) {
	f.Add("{} member", "{}", "3", "{x}", "y")
	f.Add("Match {index} of {count}: {text}", "{count}", "9", "{text}", "hi {count}")
	f.Add("{a}{b}", "{a}", "{b}", "{b}", "x")
	f.Add("no placeholders", "{}", "1", "", "")
	f.Add("ab", "", "-", "a", "b")
	f.Add("\xff{}\xfe", "{}", "\xff", "\xff", "{}")
	f.Fuzz(func(t *testing.T, s, p1, v1, p2, v2 string) {
		if got := Format(s); got != s {
			t.Fatalf("Format(%q) = %q", s, got)
		}
		if got := Format(s, p1); got != s {
			t.Fatalf("Format(%q, %q) with no value = %q", s, p1, got)
		}
		// An empty placeholder is in every string, as with str::replace.
		if len(s)+(len(s)+1)*(len(v1)+len(v2)) > 1<<16 {
			return
		}
		want := strings.ReplaceAll(s, p1, v1)
		if got := Format(s, p1, v1); got != want {
			t.Fatalf("Format(%q, %q, %q) = %q, want %q", s, p1, v1, got, want)
		}
		want = strings.ReplaceAll(want, p2, v2)
		if got := Format(s, p1, v1, p2, v2); got != want {
			t.Fatalf("Format(%q, %q, %q, %q, %q) = %q, want %q", s, p1, v1, p2, v2, got, want)
		}
		if got := Format(s, p1, v1, p2, v2, "dangling"); got != want {
			t.Fatalf("an odd placeholder changed the result to %q", got)
		}
		if !strings.Contains(s, p1) && !strings.Contains(s, p2) {
			if got := Format(s, p1, v1, p2, v2); got != s {
				t.Fatalf("Format(%q) without its placeholders = %q", s, got)
			}
		}
	})
}

// FuzzPlural checks plural selection: every catalog's rule picks a form the
// catalog has (ngettext's clamp would hide one that does not), a message no
// catalog has follows the English rule, and a translated one gives one of
// its forms.
func FuzzPlural(f *testing.F) {
	for _, n := range []int64{0, 1, 2, 3, 5, 11, 12, 21, 22, 101, 111, -1, -21, 1 << 40, -1 << 63, 1<<63 - 1} {
		f.Add(n, "{} aardvark", "{} aardvarks")
	}
	f.Add(int64(21), "{} member", "{} members")
	f.Add(int64(1), "", "")
	f.Fuzz(func(t *testing.T, n64 int64, singular, plural string) {
		n := int(n64)
		abs := uint64(n64)
		if n64 < 0 {
			abs = -abs
		}
		for tag, c := range catalogs {
			c.once.Do(c.unpack)
			nforms := 1
			for _, e := range c.entries {
				nforms = max(nforms, len(e.forms))
			}
			if i := c.plural(abs); i < 0 || i >= nforms {
				t.Fatalf("%s: plural(%d) = %d of %d forms", tag, abs, i, nforms)
			}
			if i := c.plural(uint64(n64)); i < 0 || i >= nforms {
				t.Fatalf("%s: plural(%d) = %d of %d forms", tag, uint64(n64), i, nforms)
			}
		}
		for _, l := range All {
			got := NIn(l, singular, plural, n)
			forms := []string{singular, plural}
			if c := catalogOf(l); c != nil {
				if f := c.find("", singular); len(f) > 0 {
					forms = f
				}
			}
			if forms[0] == singular && len(forms) == 2 && forms[1] == plural {
				// No translation: the English rule, by the magnitude of n.
				want := plural
				if n == 1 || n == -1 {
					want = singular
				}
				if got != want {
					t.Fatalf("%v: NIn(%q, %q, %d) = %q, want %q", l, singular, plural, n, got, want)
				}
				continue
			}
			found := false
			for _, f := range forms {
				found = found || f == got
			}
			if !found {
				t.Fatalf("%v: NIn(%q, %d) = %q, none of %q", l, singular, n, got, forms)
			}
		}
	})
}
