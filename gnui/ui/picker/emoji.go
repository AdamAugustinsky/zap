package picker

import (
	"strings"

	"zapfast/emoji"
	"zapfast/i18n"
)

// The emoji table is package emoji's (the composer reads it too). Upstream
// paints the group names untranslated.

// The groups, in table order (emojis::Group). Upstream paints their names
// untranslated; here they are Unicode's group names, translated.
const numGroups = 9

// groupName is group g's name in the interface language.
func groupName(g int) string {
	switch g {
	case 0:
		return i18n.T("Smileys & Emotion")
	case 1:
		return i18n.T("People & Body")
	case 2:
		return i18n.T("Animals & Nature")
	case 3:
		return i18n.T("Food & Drink")
	case 4:
		return i18n.T("Travel & Places")
	case 5:
		return i18n.T("Activities")
	case 6:
		return i18n.T("Objects")
	case 7:
		return i18n.T("Symbols")
	}
	return i18n.T("Flags")
}

// groupGlyphs are the category tabs' glyphs, after the recent one
// (zf:src/ui/picker.rs:259).
var groupGlyphs = [numGroups]string{"😀", "👋", "🐻", "🍔", "🚗", "⚽", "💡", "🔣", "🏁"}

const recentGlyph = "🕒"

// emojis is the shared table.
func emojis() *emoji.Table { return emoji.Get() }

// gridRow is a heading or a row of emoji of the grid.
type gridRow struct {
	label string // the heading, "" for a row of emoji
	cat   int8   // the category it belongs to: 0 recent, 1… the groups
	from  int32  // the first emoji, in flat
	n     int32
}

// grid is what the emoji tab shows: rows over a flat list of table indexes.
type grid struct {
	flat   []int32
	rows   []gridRow
	cols   int
	count  int // emoji in flat
	query  string
	recent []int32
	label  string
	gen    int // changes when the rows are rebuilt
}

const (
	maxRecent = 36
)

// build makes the rows for a query, the recent emoji and a column count
// (zf:src/ui/picker.rs:185 rows_for). The same inputs make the same rows.
func (g *grid) build(query string, recent []int32, cols int, recentLabel string) {
	t := emojis()
	g.flat, g.rows = g.flat[:0], g.rows[:0]
	g.cols, g.query, g.recent, g.label = cols, query, recent, recentLabel
	g.gen++
	// rowsFrom cuts flat[start:] into rows of cols emoji
	rowsFrom := func(cat int8, start int) {
		for off := start; off < len(g.flat); off += cols {
			g.rows = append(g.rows, gridRow{cat: cat, from: int32(off), n: int32(min(cols, len(g.flat)-off))})
		}
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q != "" {
		for i := range t.All {
			if t.Matches(i, q) {
				g.flat = append(g.flat, int32(i))
			}
		}
		if len(g.flat) == 0 {
			g.rows = append(g.rows, gridRow{label: i18n.T("Nothing matches"), cat: -1})
		}
		rowsFrom(-1, 0)
		g.count = len(g.flat)
		return
	}
	if len(recent) > 0 {
		g.rows = append(g.rows, gridRow{label: recentLabel, cat: 0})
		g.flat = append(g.flat, recent...)
		rowsFrom(0, 0)
	}
	for grp := 0; grp < numGroups; grp++ {
		g.rows = append(g.rows, gridRow{label: groupName(grp), cat: int8(grp + 1)})
		start := len(g.flat)
		for i := range t.All {
			if int(t.All[i].Group) == grp {
				g.flat = append(g.flat, int32(i))
			}
		}
		rowsFrom(int8(grp+1), start)
	}
	g.count = len(g.flat)
}

// rowOf is the row that shows emoji sel of flat, -1 for none.
func (g *grid) rowOf(sel int) int {
	for i := range g.rows {
		r := &g.rows[i]
		if r.label == "" && sel >= int(r.from) && sel < int(r.from+r.n) {
			return i
		}
	}
	return -1
}

// headerRow is the row of the heading of category cat; a category that has
// none (no recent emoji) falls back to the first group's
// (zf:src/ui/picker.rs:310 resolve_jump).
func (g *grid) headerRow(cat int) int {
	for i := range g.rows {
		if g.rows[i].label != "" && int(g.rows[i].cat) == cat {
			return i
		}
	}
	if cat == 0 {
		return g.headerRow(1)
	}
	return -1
}

// moveSelection is zf:src/ui/picker.rs:343 move_emoji_selection.
func moveSelection(selected, count, cols int, key moveKey) int {
	if count == 0 {
		return 0
	}
	selected = min(selected, count-1)
	switch key {
	case moveLeft:
		return max(selected-1, 0)
	case moveRight:
		return min(selected+1, count-1)
	case moveUp:
		if selected >= cols {
			return selected - cols
		}
	case moveDown:
		if selected+cols < count {
			return selected + cols
		}
	}
	return selected
}

type moveKey uint8

const (
	moveNone moveKey = iota
	moveLeft
	moveRight
	moveUp
	moveDown
)

// recentIndexes are the table rows of the recent emoji: those the table
// knows, in order (zf:src/ui/picker.rs:158 usable_recent).
func recentIndexes(dst []int32, recent [][]byte) []int32 {
	t := emojis()
	dst = dst[:0]
	for _, r := range recent {
		if i := t.GetBytes(r); i >= 0 {
			dst = append(dst, i)
		}
	}
	return dst
}
