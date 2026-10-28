// Package i18n holds ZapFast's interface strings and their translations,
// ported from zf:src/i18n.rs and generated from zf:assets/i18n/*.po.
//
// Screens call T, P and N with upstream's English source strings, exactly as
// upstream calls gettext, pgettext and ngettext. A string with no
// translation in the current language comes back unchanged. Upstream fills
// arguments in with a plain replace of "{}" or "{name}" after the lookup;
// Format does the same.
//
// The tables are generated (go generate ./i18n), one deflated string
// constant per language, and a language's table is unpacked into a sorted
// array the first time it is looked up in (about 35 KB of text and 40 KB of
// entries; under a millisecond): the binary carries a fifth of the text, and
// only the language in use is in memory. A lookup is then a binary search
// that allocates nothing.
package i18n

//go:generate go run ../tools/i18ngen -po ../../assets/i18n -out tables_gen.go

import (
	"compress/flate"
	"encoding/binary"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"gonativeui/app/locale"
)

// entry is one translated message; forms holds one text for a message with
// no plural, otherwise one per plural form of the language.
type entry struct {
	ctx, id string
	forms   []string
}

// catalog is one language's compiled table.
type catalog struct {
	packed string // the table as tools/i18ngen packs it (unpack)
	plural func(n uint64) int

	once    sync.Once
	entries []entry // sorted by (ctx, id), once unpacked
}

// unpack inflates the packed table into entries whose strings all view one
// copy of its text: a uvarint count of entries, then each entry's context,
// msgid and forms (a count, then each), every string a uvarint length and
// its bytes (tools/i18ngen pack).
func (c *catalog) unpack() {
	raw, err := io.ReadAll(flate.NewReader(strings.NewReader(c.packed)))
	if err != nil {
		panic("i18n: a generated table does not inflate: " + err.Error())
	}
	text := string(raw)
	at := 0
	num := func() int {
		v, n := binary.Uvarint(raw[at:])
		if n <= 0 {
			panic("i18n: a generated table is cut short")
		}
		at += n
		return int(v)
	}
	str := func() string {
		n := num()
		s := text[at : at+n]
		at += n
		return s
	}
	entries := make([]entry, num())
	var forms []string
	for i := range entries {
		e := &entries[i]
		e.ctx, e.id = str(), str()
		start := len(forms)
		for k := num(); k > 0; k-- {
			forms = append(forms, str())
		}
		e.forms = forms[start:len(forms):len(forms)]
	}
	c.entries = entries
}

func (c *catalog) find(ctx, id string) []string {
	c.once.Do(c.unpack)
	lo, hi := 0, len(c.entries)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		e := &c.entries[mid]
		cmp := strings.Compare(e.ctx, ctx)
		if cmp == 0 {
			cmp = strings.Compare(e.id, id)
		}
		switch {
		case cmp == 0:
			return e.forms
		case cmp < 0:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return nil
}

var (
	current    atomic.Pointer[catalog] // nil: English
	currentLoc atomic.Uint32
)

// SetLocale makes l the language T, P and N translate into. It may be called
// from any goroutine.
func SetLocale(l Locale) {
	if l >= numLocales {
		l = English
	}
	currentLoc.Store(uint32(l))
	current.Store(catalogOf(l))
}

// Current is the language set with SetLocale (English until then).
func Current() Locale { return Locale(currentLoc.Load()) }

// T translates msgid (upstream's gettext).
func T(msgid string) string { return lookup(current.Load(), "", msgid) }

// P translates msgid within the context ctx (upstream's pgettext).
func P(ctx, msgid string) string { return lookup(current.Load(), ctx, msgid) }

// N translates the singular or plural form for n (upstream's ngettext).
func N(singular, plural string, n int) string { return ngettext(current.Load(), singular, plural, n) }

// TIn, PIn and NIn are T, P and N for an explicit language, for code that
// does not follow the current one (a notification in the account's language,
// tests).
func TIn(l Locale, msgid string) string { return lookup(catalogOf(l), "", msgid) }

// PIn is P for the language l.
func PIn(l Locale, ctx, msgid string) string { return lookup(catalogOf(l), ctx, msgid) }

// NIn is N for the language l.
func NIn(l Locale, singular, plural string, n int) string {
	return ngettext(catalogOf(l), singular, plural, n)
}

func catalogOf(l Locale) *catalog {
	return catalogs[l.Tag()]
}

func lookup(c *catalog, ctx, msgid string) string {
	if c == nil {
		return msgid
	}
	if f := c.find(ctx, msgid); len(f) > 0 {
		return f[0]
	}
	return msgid
}

func ngettext(c *catalog, singular, plural string, n int) string {
	if n < 0 {
		n = -n
	}
	if c != nil {
		if f := c.find("", singular); len(f) > 0 {
			i := c.plural(uint64(n))
			if i >= len(f) {
				i = len(f) - 1
			}
			return f[i]
		}
	}
	if n == 1 {
		return singular
	}
	return plural
}

// Format fills arguments into a translated string the way upstream does with
// str::replace: pairs of a placeholder, written as it appears in the string,
// and its value.
//
//	i18n.Format(i18n.N("{} member", "{} members", n), "{}", strconv.Itoa(n))
//	i18n.Format(i18n.T("Select message from {sender}"), "{sender}", name)
//
// Each placeholder is replaced everywhere it occurs, in order, so a value
// that itself contains a later placeholder is replaced too, as chained
// replace calls would.
func Format(s string, pairs ...string) string {
	for i := 0; i+1 < len(pairs); i += 2 {
		if strings.Contains(s, pairs[i]) {
			s = strings.ReplaceAll(s, pairs[i], pairs[i+1])
		}
	}
	return s
}

// detectLanguages is where Detect reads the system's preferred languages;
// tests replace it.
var detectLanguages = func() []string { return locale.Current().Languages }

// SetSystemLanguages replaces where Detect reads the system's preferred
// languages and returns what restores it: tests only (another package's,
// such as the app's start), so that the machine's languages decide nothing.
func SetSystemLanguages(f func() []string) (restore func()) {
	old := detectLanguages
	detectLanguages = f
	return func() { detectLanguages = old }
}

// Detect is the first supported language the system prefers, English if none.
// It asks the OS the first time, so call it at startup, not in a frame.
func Detect() Locale {
	for _, tag := range detectLanguages() {
		if l, ok := FromSystem(tag); ok {
			return l
		}
	}
	return English
}

// Resolve turns a stored preference into a language: the tag of an explicit
// choice ("pt-BR") wins, otherwise (empty, or a tag no catalog has) Detect.
func Resolve(choice string) Locale {
	if l, ok := ParseTag(choice); ok {
		return l
	}
	return Detect()
}
