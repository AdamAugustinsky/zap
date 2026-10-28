package picker

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"zapfast/emoji"
)

// Sticker search (zf:src/sticker_search.rs): by emoji, by words that name an
// emoji, and by pack name.

// pack is a sticker pack as the core listed it.
type pack struct {
	name, dir string
	local     bool
	stickers  []string
}

// library is where a search looks: the picker's lists and each sticker's
// emojis.
type library struct {
	recent, favorites, received []string
	packs                       []pack
	emojis                      map[string][]string
}

// named is the emoji a search term names: the emoji itself, or every emoji
// whose name or shortcode contains the word (zf:src/sticker_search.rs:18).
func named(word string) map[string]struct{} {
	found := map[string]struct{}{}
	t := emojis()
	if i := t.Get(word); i >= 0 {
		found[emoji.Plain(t.All[i].Glyph)] = struct{}{}
		return found
	}
	word = strings.ToLower(word)
	if utf8.RuneCountInString(word) < 2 {
		return found
	}
	for i := range t.All {
		if t.Matches(i, word) {
			found[emoji.Plain(t.All[i].Glyph)] = struct{}{}
		}
	}
	return found
}

// clusters splits s into the units a person sees as one character: enough of
// extended grapheme clusters for emoji (variation selectors, skin tones,
// joiners, keycaps, regional-indicator pairs and tag sequences).
func clusters(s string) []string {
	var out []string
	rs := []rune(s)
	for i := 0; i < len(rs); {
		j := i + 1
		if isRegional(rs[i]) && j < len(rs) && isRegional(rs[j]) {
			j++
		}
		for j < len(rs) {
			r := rs[j]
			switch {
			case r == 0xFE0F || r == 0xFE0E || r == 0x20E3 || (r >= 0x1F3FB && r <= 0x1F3FF) ||
				(r >= 0xE0020 && r <= 0xE007F) || unicode.Is(unicode.Mn, r):
				j++
			case r == 0x200D && j+1 < len(rs):
				j += 2
			default:
				goto done
			}
		}
	done:
		out = append(out, string(rs[i:j]))
		i = j
	}
	return out
}

func isRegional(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

func allNonAlnum(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return false
		}
	}
	return true
}

// search is the stickers matching query: favorites and recent ones first,
// received ones last, each once (zf:src/sticker_search.rs:56).
func (l *library) search(query string) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	t := emojis()
	var terms []map[string]struct{}
	for _, w := range strings.Fields(query) {
		if t.Get(w) >= 0 || allNonAlnum(w) {
			for _, g := range clusters(w) {
				terms = append(terms, named(g))
			}
		} else {
			terms = append(terms, named(w))
		}
	}
	tagged := func(path string) bool {
		tags := l.emojis[path]
		if len(tags) == 0 || len(terms) == 0 {
			return false
		}
		have := make(map[string]struct{}, len(tags))
		for _, e := range tags {
			have[emoji.Plain(e)] = struct{}{}
		}
		for _, term := range terms {
			ok := false
			for e := range term {
				if _, in := have[e]; in {
					ok = true
					break
				}
			}
			if !ok {
				return false
			}
		}
		return true
	}
	needle := strings.ToLower(query)
	seen := map[string]struct{}{}
	var found []string
	visit := func(path string) {
		if _, dup := seen[path]; !dup && tagged(path) {
			seen[path] = struct{}{}
			found = append(found, path)
		}
	}
	for _, p := range l.favorites {
		visit(p)
	}
	for _, p := range l.recent {
		visit(p)
	}
	for i := range l.packs {
		for _, p := range l.packs[i].stickers {
			visit(p)
		}
	}
	for _, p := range l.received {
		visit(p)
	}
	for i := range l.packs {
		if strings.Contains(strings.ToLower(l.packs[i].name), needle) {
			for _, p := range l.packs[i].stickers {
				if _, dup := seen[p]; !dup {
					seen[p] = struct{}{}
					found = append(found, p)
				}
			}
		}
	}
	return found
}
