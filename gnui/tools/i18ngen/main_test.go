package main

import (
	"bytes"
	"compress/flate"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const polish = `# Three plural forms.
msgid ""
msgstr ""
"Language: pl\n"
"Content-Type: text/plain; charset=UTF-8\n"
"Plural-Forms: nplurals=3; plural=(n%10==1 && n%100!=11 ? 0 : n%10>=2 && "
"n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2);\n"

msgid "Home"
msgstr "Start"

msgid "Search"
msgstr ""

#, fuzzy
msgid "Library"
msgstr "Unreviewed translation"

#, rust-format
msgid "{} track"
msgid_plural "{} tracks"
msgstr[0] "incomplete one"
msgstr[1] ""
msgstr[2] "incomplete many"

#. translator note
#: src/x.rs:1
msgctxt "verb"
msgid "Mute"
msgstr "Wycisz "
"dwa\tpo"
"\"linii\""

msgid "{} song"
msgid_plural "{} songs"
msgstr[0] "{} utwór"
msgstr[1] "{} utwory"
msgstr[2] "{} utworów"

#~ msgid "Gone"
#~ msgstr "Zniknął"
`

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUnfinishedMessagesAreLeftOutAndStringsAreJoined(t *testing.T) {
	c, err := parsePO(polish)
	if err != nil {
		t.Fatal(err)
	}
	if c.nplurals != 3 {
		t.Errorf("nplurals %d", c.nplurals)
	}
	want := map[string][]string{
		"|Home":     {"Start"},
		"verb|Mute": {"Wycisz dwa\tpo\"linii\""},
		"|{} song":  {"{} utwór", "{} utwory", "{} utworów"},
	}
	if len(c.msgs) != len(want) {
		t.Fatalf("got %d messages: %+v", len(c.msgs), c.msgs)
	}
	for _, m := range c.msgs {
		w, ok := want[m.ctx+"|"+m.id]
		if !ok || strings.Join(w, "\x00") != strings.Join(m.forms, "\x00") {
			t.Errorf("unexpected %+v", m)
		}
	}
}

func TestGenerateIsDeterministicAndRejectsUnknownPluralRules(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pl.po", polish)
	write(t, dir, "app.pot", "not a catalog")
	a, err := Generate(dir, "i18n")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Generate(dir, "i18n")
	if !bytes.Equal(a, b) || !bytes.Contains(a, []byte(`"pl": {packed: packedPl, plural: pluralPl}`)) {
		t.Errorf("not deterministic or no index:\n%s", a)
	}
	table := tableText(t, a, "packedPl")
	if !bytes.Contains(table, []byte("Wycisz dwa")) {
		t.Errorf("the table lacks a message: %q", table)
	}
	if bytes.Contains(table, []byte("Unreviewed")) || bytes.Contains(table, []byte("Zniknął")) {
		t.Error("an unfinished or obsolete message was kept")
	}
	write(t, dir, "xx.po", strings.Replace(polish, "nplurals=3", "nplurals=4", 1))
	if _, err := Generate(dir, "i18n"); err == nil || !strings.Contains(err.Error(), "xx.po") {
		t.Errorf("a plural count that disagrees with the rule: %v", err)
	}
	write(t, dir, "xx.po", strings.Replace(polish, "nplurals=3; plural=", "nplurals=2; plural=n+", 1))
	if _, err := Generate(dir, "i18n"); err == nil || !strings.Contains(err.Error(), "no Go code") {
		t.Errorf("an unknown rule: %v", err)
	}
	write(t, dir, "xx.po", strings.Replace(polish, "msgid \"Home\"", "msgid \"Home\"\nmsgstr \"a\"\n\nmsgid \"Home\"", 1))
	if _, err := Generate(dir, "i18n"); err == nil {
		t.Error("a duplicate message")
	}
}

// The committed tables are what the .po files generate; this is the test
// behind `go run ./tools/i18ngen -check`.
func TestCheckModeFailsOnAStaleFile(t *testing.T) {
	const po, gen = "../../../assets/i18n", "../../i18n/tables_gen.go"
	var out, errb bytes.Buffer
	if code := run([]string{"-po", po, "-out", gen, "-check"}, &out, &errb); code != 0 {
		t.Fatalf("the committed tables are stale (run go generate ./i18n): %s", errb.String())
	}

	dir := t.TempDir()
	write(t, dir, "pl.po", polish)
	tmp := filepath.Join(dir, "out.go")
	if code := run([]string{"-po", dir, "-out", tmp}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	if code := run([]string{"-po", dir, "-out", tmp, "-check"}, &out, &errb); code != 0 {
		t.Fatalf("a fresh file reported stale: %s", errb.String())
	}
	write(t, dir, "pl.po", strings.Replace(polish, `"Start"`, `"Początek"`, 1))
	errb.Reset()
	if code := run([]string{"-po", dir, "-out", tmp, "-check"}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "stale") {
		t.Errorf("a changed .po was not caught: %d %s", code, errb.String())
	}
	if code := run([]string{"-po", dir, "-out", filepath.Join(dir, "missing.go"), "-check"}, &out, &errb); code != 1 {
		t.Error("a missing file passed -check")
	}
}

// tableText is the inflated text of the packed table constant name in the
// generated source src.
func tableText(t *testing.T, src []byte, name string) []byte {
	t.Helper()
	_, rest, ok := bytes.Cut(src, []byte("const "+name+" = "))
	if !ok {
		t.Fatalf("no %s in:\n%s", name, src)
	}
	rest, _, _ = bytes.Cut(rest, []byte("\n\n"))
	var packed strings.Builder
	for lit := string(rest); lit != ""; {
		q, err := strconv.QuotedPrefix(lit)
		if err != nil {
			t.Fatalf("%s: %v at %q", name, err, lit[:min(len(lit), 20)])
		}
		u, _ := strconv.Unquote(q)
		packed.WriteString(u)
		lit = strings.TrimLeft(strings.TrimPrefix(strings.TrimLeft(lit[len(q):], " \n\t"), "+"), " \n\t")
	}
	raw, err := io.ReadAll(flate.NewReader(strings.NewReader(packed.String())))
	if err != nil {
		t.Fatalf("%s does not inflate: %v", name, err)
	}
	return raw
}
