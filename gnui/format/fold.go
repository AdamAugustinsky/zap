package format

import (
	"unicode"
	"unicode/utf8"
)

// The search fold (zf:src/util.rs search_key): lower case, with the accents
// of the Latin letters dropped, so "estevao" finds "Estevão" and "tone"
// finds "Töne". Combining marks are skipped too, as the NFD-without-marks
// fold upstream's search does.

// FoldRune is r lower-cased, without the accent of an accented Latin letter.
func FoldRune(r rune) rune {
	if r < utf8.RuneSelf {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}
	r = unicode.ToLower(r)
	if r >= 0x00C0 && r < 0x0250 {
		if b, ok := latin[r]; ok {
			return b
		}
	}
	return r
}

// latin maps the accented Latin letters of Latin-1 Supplement and Latin
// Extended-A/B to their base letters.
var latin = func() map[rune]rune {
	m := map[rune]rune{}
	for _, g := range [...]string{
		"aàáâãäåāăąǎ", "cçćĉċč", "dďđ", "eèéêëēĕėęě", "gĝğġģ", "hĥħ", "iìíîïĩīĭįıǐ", "jĵ", "kķ",
		"lĺļľŀł", "nñńņňŉ", "oòóôõöøōŏőǒ", "rŕŗř", "sśŝşš", "tţťŧ", "uùúûüũūŭůűųǔ", "wŵ", "yýÿŷ", "zźżž",
	} {
		rs := []rune(g)
		for _, r := range rs[1:] {
			m[r] = rs[0]
		}
	}
	return m
}()

// AppendFold appends s folded to dst.
func AppendFold(dst []byte, s string) []byte {
	for i := 0; i < len(s); {
		if b := s[i]; b < utf8.RuneSelf {
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			dst = append(dst, b)
			i++
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		if !unicode.Is(unicode.Mn, r) {
			dst = utf8.AppendRune(dst, FoldRune(r))
		}
	}
	return dst
}

// Fold is s folded, without allocating for ASCII in lower case already.
func Fold(s string) string {
	for i := 0; i < len(s); i++ {
		if b := s[i]; b >= utf8.RuneSelf || 'A' <= b && b <= 'Z' {
			return string(AppendFold(make([]byte, 0, len(s)), s))
		}
	}
	return s
}

// FoldKey is the search key of a query: its runes folded (an allocation, made
// when the query changes).
func FoldKey(s string) []rune {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if !unicode.Is(unicode.Mn, r) {
			out = append(out, FoldRune(r))
		}
	}
	return out
}

// ContainsFold reports whether text, folded, contains needle (a FoldKey),
// without allocating. An empty needle is in every text.
func ContainsFold(text []byte, needle []rune) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i < len(text); {
		if hasFoldPrefix(text[i:], needle) {
			return true
		}
		_, n := utf8.DecodeRune(text[i:])
		i += n
	}
	return false
}

func hasFoldPrefix(text []byte, needle []rune) bool {
	for _, want := range needle {
		for {
			if len(text) == 0 {
				return false
			}
			r, n := utf8.DecodeRune(text)
			text = text[n:]
			if unicode.Is(unicode.Mn, r) {
				continue
			}
			if FoldRune(r) != want {
				return false
			}
			break
		}
	}
	return true
}
