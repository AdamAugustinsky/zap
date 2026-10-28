package composer

import "strings"

func emojiGlyph(i int) string { return emojiTable()[i].emoji }
func emojiCount() int         { return len(emojiTable()) }

// emojiSweep is the queries tried for table row i: each shortcode whole,
// cut to a prefix and to its middle, and the name's first word.
func emojiSweep(i int) []string {
	r := &emojiTable()[i]
	var qs []string
	for _, code := range strings.Fields(r.codes) {
		qs = append(qs, code, code[:(len(code)+1)/2], code[len(code)/3:])
	}
	if w, _, _ := strings.Cut(r.name, " "); w != "" {
		qs = append(qs, w)
	}
	qs = append(qs, r.name)
	return qs
}
