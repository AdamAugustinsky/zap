package composer

import (
	"strings"
	"sync"

	"zapfast/emoji"
)

// The completion's emoji are the shared table's (zapfast/emoji) without the
// skin-tone variants: each emoji once, in its default tone, in the crate's
// order, as emojis::iter yields them for the completion.

// emojiRec is one emoji of the :shortcode: completion: the emoji, its
// Unicode name, its GitHub shortcodes (space-separated, the first is the
// one shown) and its row's title ("😀  :grinning:").
type emojiRec struct {
	emoji, name, codes, row string
}

// score ranks the emoji for a lowercased query, lower first; ok is false
// when it does not match (zf:src/ui/conversation.rs:972).
func (r *emojiRec) score(q string) (int, bool) {
	codes := r.codes
	anyCode := func(f func(code string) bool) bool {
		for rest := codes; rest != ""; {
			code, more, _ := strings.Cut(rest, " ")
			if f(code) {
				return true
			}
			rest = more
		}
		return false
	}
	switch {
	case anyCode(func(c string) bool { return c == q }):
		return 0, true
	case anyCode(func(c string) bool { return strings.HasPrefix(c, q) }):
		return 1, true
	case r.name == q:
		return 2, true
	case strings.HasPrefix(r.name, q) || wordPrefix(r.name, q):
		return 3, true
	case anyCode(func(c string) bool { return strings.Contains(c, q) }):
		return 4, true
	case strings.Contains(r.name, q):
		return 5, true
	}
	return 0, false
}

// wordPrefix reports whether a word of name (split at spaces, - and _)
// starts with q.
func wordPrefix(name, q string) bool {
	for i := 0; i <= len(name); i++ {
		if i == 0 || name[i-1] == ' ' || name[i-1] == '-' || name[i-1] == '_' {
			if strings.HasPrefix(name[i:], q) {
				return true
			}
		}
	}
	return false
}

var (
	emojiOnce sync.Once
	emojiRecs []emojiRec
)

// emojiTable is the completion's emoji, built from the shared table on first
// use (warmEmoji starts that early).
func emojiTable() []emojiRec {
	emojiOnce.Do(func() {
		t := emoji.Get()
		recs := make([]emojiRec, 0, len(t.All))
		for i := range t.All {
			e := &t.All[i]
			if e.Skin {
				continue
			}
			code := strings.NewReplacer(" ", "_", "-", "_").Replace(e.Name)
			codes := ""
			if e.Codes != "" {
				codes = strings.ReplaceAll(e.Codes, ",", " ")
				code, _, _ = strings.Cut(e.Codes, ",")
			}
			recs = append(recs, emojiRec{emoji: e.Glyph, name: e.Name, codes: codes, row: e.Glyph + "  :" + code + ":"})
		}
		emojiRecs = recs
	})
	return emojiRecs
}

// warmEmoji builds the table on a goroutine, so the first ":" does not wait
// for it.
func warmEmoji() { go emojiTable() }

// emojiIndex is the table's row of emoji em, -1 for none.
func emojiIndex(em []byte) int {
	for i, r := range emojiTable() {
		if r.emoji == string(em) {
			return i
		}
	}
	return -1
}
