package composer

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"zapfast/state"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/emoji_candidates.golden")

// emojiAnswer is what the completion offers for a query, as text: one line
// per candidate, "emoji | title | detail".
func emojiAnswer(st *state.State, query string) string {
	var c completion
	c.query = []byte(strings.ToLower(query))
	c.emojiCandidates(st)
	var b strings.Builder
	fmt.Fprintf(&b, "%q\n", query)
	for i := range c.items {
		fmt.Fprintf(&b, "  %s | %s | %s\n", emojiGlyph(c.emoji[i]), c.items[i].Title, c.items[i].Detail)
	}
	return b.String()
}

// The :shortcode: completion keeps offering the same emoji, in the same
// order, with the same titles, for the same input (the table moved to the
// shared package zapfast/emoji; the golden was captured before it did).
// testdata/emoji_candidates.golden holds the answers for hand-picked inputs
// and the SHA-256 of the answers to a sweep of the whole table (every
// shortcode, a prefix, a middle, every name's first word).
func TestEmojiCandidatesAreUnchanged(t *testing.T) {
	var b strings.Builder
	empty := state.New()
	b.WriteString("# empty query, no recent emoji\n")
	b.WriteString(emojiAnswer(empty, ""))
	withRecent := state.New()
	for _, r := range []string{"🦀", "👍🏽", "not an emoji", "👍", "🦀", "🇧🇷", "❤️", "😂", "🎉", "🔥"} {
		withRecent.Settings.RecentEmoji = append(withRecent.Settings.RecentEmoji, []byte(r))
	}
	b.WriteString("# empty query, recent emoji (a skin-tone variant, a repeat, text, one without its selector)\n")
	b.WriteString(emojiAnswer(withRecent, ""))
	b.WriteString("# queries\n")
	for _, q := range []string{
		"smile", "heart", "zzzzqq", "+1", "-1", "100", "thumbsup", "thumbs", "joy", "laugh", "cry", "sob", "fire", "tada",
		"crab", "brazil", "flag", "flag: brazil", "japan", "aland", "åland", "côte", "são", "cote d", "x", "e", "a", "  ", "u+", ":",
		"grinning face", "grinning-face", "face_with", "face with", "red heart", "heart_eyes", "kiss", "wave", "hand", "clap",
		"pray", "muscle", "ok_hand", "ok", "v", "eyes", "pizza", "coffee", "beer", "car", "airplane", "rocket", "moon", "sun",
		"star", "sparkles", "zap", "rainbow", "cat", "dog", "unicorn", "poop", "ghost", "skull", "robot", "alien", "santa",
		"man", "woman", "family", "couple", "kiss_mn", "people holding hands", "light skin", "tone", "skin tone", "keycap",
		"1", "10", "!", "?", "-", "_", "’", "“", "ß", "İ", "ǅ", "Σ",
	} {
		b.WriteString(emojiAnswer(empty, q))
	}
	got := b.String()

	// a sweep of the whole table, kept as a digest
	h := sha256.New()
	n := 0
	for i := 0; i < emojiCount(); i++ {
		for _, q := range emojiSweep(i) {
			h.Write([]byte(emojiAnswer(empty, q)))
			n++
		}
	}
	got += fmt.Sprintf("# sweep: %d queries, sha256 %s\n", n, hex.EncodeToString(h.Sum(nil)))

	const path = "testdata/emoji_candidates.golden"
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gl) && i < len(wl); i++ {
			if gl[i] != wl[i] {
				t.Fatalf("the completion's answers changed at line %d:\n got  %q\n want %q", i+1, gl[i], wl[i])
			}
		}
		t.Fatalf("the completion's answers changed (%d lines, want %d)", len(gl), len(wl))
	}
}
