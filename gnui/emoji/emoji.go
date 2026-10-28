// Package emoji is the emoji table the screens share: the picker's grid and
// search, and the composer's :shortcode: completion.
//
// Upstream reads it from the Rust `emojis` crate (0.9, MIT or Apache-2.0;
// zf:src/ui/picker.rs via emojis::iter), in Unicode CLDR order with every
// skin-tone variant a row of its own. The core's ABI has no command for the
// table, so it is embedded: emoji.tsv has a line per emoji (glyph, group
// 0…8, name, shortcodes comma separated), and the binary carries it
// deflated as emoji.tsv.z (36 KB for 190 KB). TestTableIsPacked keeps the
// two files the same (go test -run TestTableIsPacked -update rewrites
// emoji.tsv.z from emoji.tsv).
//
// The table is inflated and parsed once, on first use; Warm starts that on
// a goroutine, so a screen's Init can have it ready before the first frame
// that needs it. After that, lookups allocate nothing.
package emoji

import (
	"compress/flate"
	_ "embed"
	"io"
	"strings"
	"sync"
)

//go:embed emoji.tsv.z
var packed string

// Entry is one emoji.
type Entry struct {
	Glyph string
	Name  string // Unicode's name, as the crate gives it
	LName string // Name, lower case
	Codes string // GitHub shortcodes, lower case, comma separated; "" for none
	Group uint8  // 0…8: Smileys & Emotion … Flags
	Skin  bool   // a skin-tone variant: the name says "skin tone"
}

// Table is the emoji, in the crate's order.
type Table struct {
	All     []Entry
	byGlyph map[string]int32
}

var (
	once sync.Once
	tbl  Table
)

// Get is the table, inflated and parsed on first use.
func Get() *Table {
	once.Do(load)
	return &tbl
}

// Warm starts loading the table on a goroutine.
func Warm() { go Get() }

func load() {
	r := flate.NewReader(strings.NewReader(packed))
	b, err := io.ReadAll(r)
	if err != nil {
		panic("emoji: emoji.tsv.z does not inflate: " + err.Error())
	}
	src := string(b)
	t := &tbl
	t.All = make([]Entry, 0, 4000)
	t.byGlyph = make(map[string]int32, 4600)
	for len(src) > 0 {
		var line string
		line, src, _ = strings.Cut(src, "\n")
		glyph, rest, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		g, rest, _ := strings.Cut(rest, "\t")
		name, codes, _ := strings.Cut(rest, "\t")
		t.All = append(t.All, Entry{
			Glyph: glyph, Name: name, LName: strings.ToLower(name), Codes: strings.ToLower(codes),
			Group: uint8(g[0] - '0'), Skin: strings.Contains(name, "skin tone"),
		})
	}
	for i := range t.All {
		t.byGlyph[t.All[i].Glyph] = int32(i)
	}
	// emojis::get takes an emoji with or without its presentation selector
	for i := range t.All {
		if p := Plain(t.All[i].Glyph); p != t.All[i].Glyph {
			if _, ok := t.byGlyph[p]; !ok {
				t.byGlyph[p] = int32(i)
			}
		}
	}
}

// Get is emojis::get: the index of the emoji s, or -1.
func (t *Table) Get(s string) int32 {
	if i, ok := t.byGlyph[s]; ok {
		return i
	}
	return -1
}

// GetBytes is Get for text held as bytes, without copying it.
func (t *Table) GetBytes(b []byte) int32 {
	if i, ok := t.byGlyph[string(b)]; ok {
		return i
	}
	return -1
}

// Matches reports whether emoji i is named by the lower-case word q: its
// name or one of its shortcodes contains it.
func (t *Table) Matches(i int, q string) bool {
	e := &t.All[i]
	return strings.Contains(e.LName, q) || strings.Contains(e.Codes, q)
}

// Plain drops the presentation selector, so an emoji compares equal with or
// without it (zf:src/sticker_search.rs:13).
func Plain(s string) string {
	if !strings.Contains(s, "\uFE0F") {
		return s
	}
	return strings.ReplaceAll(s, "\uFE0F", "")
}
