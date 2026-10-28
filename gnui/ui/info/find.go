package info

import (
	"bytes"
	"strings"

	"gonativeui/ui"

	"zapfast/format"
)

// find is the media view's search: the rows of the open tab whose words match
// the query (a picture's or video's caption and kind, a document's name and
// caption, a link's title and address), ignoring case and accents. The match
// is worked out again only when the query, the tab or the listing changes,
// never per frame.
type find struct {
	buf  ui.TextBuffer
	key  []rune
	on   bool // the query is not empty: the rows are hits
	hits []int32

	done bool // hits are for rev, gen and tab
	rev  uint32
	gen  uint32
	tab  int
}

// reset empties the query (a new chat).
func (f *find) reset() {
	f.buf.Reset()
	f.done = false
}

// update matches the query against tab's rows of sh.
func (f *find) update(sh *shared, tab int) {
	if f.done && f.rev == f.buf.Rev() && f.gen == sh.gen && f.tab == tab {
		return
	}
	f.done, f.rev, f.gen, f.tab = true, f.buf.Rev(), sh.gen, tab
	f.key = format.FoldKey(strings.TrimSpace(f.buf.String()))
	f.on = len(f.key) > 0
	f.hits = f.hits[:0]
	if !f.on {
		return
	}
	match := func(b []byte) bool { return format.ContainsFold(b, f.key) }
	switch tab {
	case 0:
		for i := range sh.media {
			m := &sh.media[i]
			if match(sh.bytes(m.caption)) || match(shown(sh.bytes(m.label))) {
				f.hits = append(f.hits, int32(i))
			}
		}
	case 1:
		for i := range sh.docs {
			d := &sh.docs[i]
			if match(sh.bytes(d.name)) || match(sh.bytes(d.caption)) {
				f.hits = append(f.hits, int32(i))
			}
		}
	default:
		for i := range sh.links {
			l := &sh.links[i]
			if match(sh.bytes(l.title)) || match(sh.bytes(l.url)) {
				f.hits = append(f.hits, int32(i))
			}
		}
	}
}

// rows is how many rows the tab shows of its total.
func (f *find) rows(total int) int {
	if !f.on {
		return total
	}
	return len(f.hits)
}

// at is the listing's index of row i.
func (f *find) at(i int) int {
	if !f.on {
		return i
	}
	return int(f.hits[i])
}

// shown is a box label's text without its "###id".
func shown(label []byte) []byte {
	if i := bytes.Index(label, []byte("###")); i >= 0 {
		return label[:i]
	}
	return label
}
