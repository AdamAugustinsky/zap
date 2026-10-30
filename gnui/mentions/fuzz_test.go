package mentions

import (
	"bytes"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

var fuzzTexts = []string{
	"", "Hello @all", "@all", "(@all)", "see @all.", "@alligator", "mail@all.com", "me@all.com", "@all_", "@all1",
	".@all", "@ALL", "@All", "hi @all!", "oi @todos e @Todos, nao mail@todos.com nem @todosX",
	"@ALL stays, and so does @todos", "ping @all", "嗨 @所有人 好", "@所有人@所有人", "@todos@todos", "@\xff",
	"\xff@all", "@all\xff", "@allé", "@alĺ", "@@all", "@", "@tOdOs", "@ＴＯＤＯＳ",
}

var fuzzAliases = []string{"todos", "all", "所有人", "alle", "все", "herkes", "", "to dos", " todos ", "ALL", "x", "K", "K"}

// FuzzEveryoneToken checks the wire token: EveryoneTokenEnd finds exactly
// "@all" at its "@", and HasEveryoneToken is whether any "@" starts one.
func FuzzEveryoneToken(f *testing.F) {
	for _, s := range fuzzTexts {
		f.Add([]byte(s), 0)
	}
	f.Fuzz(func(t *testing.T, text []byte, at int) {
		if end, ok := EveryoneTokenEnd(text, at); ok {
			if at < 0 || end != at+4 || end > len(text) || string(text[at:end]) != "@all" {
				t.Fatalf("%q at %d: token ends at %d", text, at, end)
			}
			if !HasEveryoneToken(text) {
				t.Fatalf("%q: a token at %d, but HasEveryoneToken is false", text, at)
			}
		}
		any := false
		for i := range text {
			if _, ok := EveryoneTokenEnd(text, i); ok {
				any = true
			}
		}
		if any != HasEveryoneToken(text) {
			t.Fatalf("%q: HasEveryoneToken %v, a token at some @: %v", text, !any, any)
		}
		if !bytes.Contains(text, []byte("@all")) && HasEveryoneToken(text) {
			t.Fatalf("%q: a token without @all", text)
		}
		// The token is a whole word: a letter or digit glued on either side
		// unmakes it.
		if HasEveryoneToken(text) {
			glued := bytes.ReplaceAll(text, []byte("@all"), []byte("x@allx"))
			if HasEveryoneToken(glued) {
				t.Fatalf("%q: %q still has the token", text, glued)
			}
		}
	})
}

// FuzzRewriteAlias checks the composer's rewrite: dst kept, no change for an
// alias it must ignore, the flag it returns is HasEveryoneToken of what it
// wrote, a rewrite always leaves a real token, a second pass changes
// nothing, and the output stays within twice the input.
func FuzzRewriteAlias(f *testing.F) {
	for _, s := range fuzzTexts {
		for _, a := range fuzzAliases {
			f.Add([]byte(s), a)
		}
	}
	f.Fuzz(func(t *testing.T, text []byte, alias string) {
		out, has := RewriteAlias([]byte("> "), text, alias)
		if string(out[:2]) != "> " {
			t.Fatalf("%q %q: dst overwritten", text, alias)
		}
		out = out[2:]
		if has != HasEveryoneToken(out) {
			t.Fatalf("%q %q: reported %v for %q", text, alias, has, out)
		}
		trimmed := strings.TrimSpace(alias)
		if trimmed == "" || strings.EqualFold(trimmed, AllUser) || strings.IndexFunc(trimmed, unicode.IsSpace) >= 0 {
			if !bytes.Equal(out, text) {
				t.Fatalf("%q %q: an ignored alias rewrote it to %q", text, alias, out)
			}
			return
		}
		// "@" and a non-empty alias become "@all": at most twice as long.
		if len(out) > 2*len(text) {
			t.Fatalf("%q %q: %d bytes from %d", text, alias, len(out), len(text))
		}
		if !bytes.Equal(out, text) && !has {
			t.Fatalf("%q %q: rewritten to %q without a real token", text, alias, out)
		}
		if HasEveryoneToken(text) && !has {
			t.Fatalf("%q %q: the token there was lost in %q", text, alias, out)
		}
		// An alias is one word (EveryoneAlias): for one, a second pass finds
		// nothing left, and every "@" survives the rewrite.
		if strings.IndexFunc(trimmed, func(r rune) bool { return !isAlnum(r) }) >= 0 {
			return
		}
		if again, _ := RewriteAlias(nil, out, alias); !bytes.Equal(again, out) {
			t.Fatalf("%q %q: a second pass rewrote %q to %q", text, alias, out, again)
		}
		if bytes.Count(out, []byte("@")) != bytes.Count(text, []byte("@")) {
			t.Fatalf("%q %q: the @ count changed in %q", text, alias, out)
		}
		if utf8.Valid(text) && !utf8.Valid(out) {
			t.Fatalf("%q %q: %q is not UTF-8", text, alias, out)
		}
	})
}

// FuzzEveryoneQueryMatches checks the picker: an empty query and every
// prefix of "all" or of the alias offer everyone, with or without "@" and
// in any case.
func FuzzEveryoneQueryMatches(f *testing.F) {
	for _, a := range fuzzAliases {
		f.Add("", a, 0)
		f.Add("@to", a, 2)
	}
	f.Add("tom", "todos", 1)
	f.Add("所", "所有人", 1)
	f.Fuzz(func(t *testing.T, query, alias string, cut int) {
		_ = EveryoneQueryMatches(query, alias)
		if !EveryoneQueryMatches("", alias) || !EveryoneQueryMatches("  @", alias) {
			t.Fatalf("an empty query does not match %q", alias)
		}
		for i := 0; i <= len(AllUser); i++ {
			if !EveryoneQueryMatches("@"+strings.ToUpper(AllUser[:i]), alias) {
				t.Fatalf("%q does not match with alias %q", AllUser[:i], alias)
			}
		}
		// A prefix of an alias as EveryoneAlias gives it: one word, no "@".
		if !utf8.ValidString(alias) || alias == "" || strings.IndexFunc(alias, unicode.IsSpace) >= 0 || strings.HasPrefix(alias, "@") {
			return
		}
		if cut < 0 {
			cut = -cut
		}
		cut %= len(alias) + 1
		for cut > 0 && !utf8.RuneStart(alias[cut%len(alias)]) && cut < len(alias) {
			cut--
		}
		prefix := alias[:cut]
		if !EveryoneQueryMatches(prefix, alias) || !EveryoneQueryMatches("@"+prefix+" ", alias) {
			t.Fatalf("the prefix %q of %q does not match", prefix, alias)
		}
	})
}
