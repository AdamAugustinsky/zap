package mentions

import (
	"testing"

	"zapfast/abi"
	"zapfast/i18n"
)

func TestTheWireTokenIsAWholeWord(t *testing.T) {
	for text, want := range map[string]bool{
		"Hello @all": true, "@all": true, "(@all)": true, "see @all.": true,
		"@alligator": false, "mail@all.com": false, "me@all.com": false, "@all_": false,
		"@all1": false, ".@all": false, "@ALL": false, "@All": false,
	} {
		if got := HasEveryoneToken([]byte(text)); got != want {
			t.Errorf("HasEveryoneToken(%q) = %v", text, got)
		}
	}
	if end, ok := EveryoneTokenEnd([]byte("hi @all!"), 3); !ok || end != 7 {
		t.Errorf("EveryoneTokenEnd = %d, %v", end, ok)
	}
	if _, ok := EveryoneTokenEnd([]byte("hi @all!"), 99); ok {
		t.Error("out of range")
	}
}

func TestALocalizedAliasBecomesTheWireToken(t *testing.T) {
	rewrite := func(text, alias string) (string, bool) {
		out, has := RewriteAlias(nil, []byte(text), alias)
		return string(out), has
	}
	if got, has := rewrite("oi @todos e @Todos, nao mail@todos.com nem @todosX", "todos"); !has ||
		got != "oi @all e @all, nao mail@todos.com nem @todosX" {
		t.Errorf("%q %v", got, has)
	}
	if got, has := rewrite("@ALL stays, and so does @todos", "all"); has || got != "@ALL stays, and so does @todos" {
		t.Errorf("%q %v", got, has)
	}
	if got, has := rewrite("ping @all", "todos"); !has || got != "ping @all" {
		t.Errorf("%q %v", got, has)
	}
	if got, has := rewrite("oi @todos", "to dos"); has || got != "oi @todos" {
		t.Errorf("a spaced alias must be ignored: %q %v", got, has)
	}
	// A non-ASCII alias, and appending after existing bytes.
	out, has := RewriteAlias([]byte("> "), []byte("嗨 @所有人 好"), "所有人")
	if !has || string(out) != "> 嗨 @all 好" {
		t.Errorf("%q %v", out, has)
	}
}

func TestThePickerQueryMatchesEitherWord(t *testing.T) {
	for q, want := range map[string]bool{
		"": true, "a": true, "all": true, "t": true, "Todos": true, "@to": true, "tom": false, "todosa": false,
	} {
		if got := EveryoneQueryMatches(q, "todos"); got != want {
			t.Errorf("%q = %v", q, got)
		}
	}
	if !EveryoneQueryMatches("所", "所有人") || EveryoneQueryMatches("人", "所有人") {
		t.Error("CJK alias")
	}
}

func TestAliasesComeFromTheCatalog(t *testing.T) {
	for l, want := range map[i18n.Locale]string{
		i18n.English: "all", i18n.PortugueseBrazil: "todos", i18n.German: "alle", i18n.Spanish: "todos",
		i18n.Italian: "tutti", i18n.French: "tous", i18n.Russian: "все", i18n.ChineseSimplified: "所有人",
		i18n.ChineseTraditional: "所有人", i18n.Turkish: "herkes", i18n.Indonesian: "semua",
	} {
		if got := EveryoneAlias(l); got != want {
			t.Errorf("%v: %q, want %q", l, got, want)
		}
	}
}

func TestTheFlagKeepsOtherBits(t *testing.T) {
	if WithFlag(0) != 1 || WithFlag(2) != 3 {
		t.Error("WithFlag")
	}
	if WithoutFlag(1) != 0 || WithoutFlag(3) != 2 || WithoutFlag(0) != 0 {
		t.Error("WithoutFlag")
	}
	if !Flagged(1) || Flagged(2) || Flagged(0) {
		t.Error("Flagged")
	}
}

func TestStoredMentions(t *testing.T) {
	all := EveryoneRef()
	if !IsEveryone(&all) || string(all.User) != "all" || string(all.ID) != "mention-all" {
		t.Errorf("%+v", all)
	}
	var ms []abi.MentionRef
	ms = PushEveryone(PushEveryone(ms))
	if len(ms) != 1 {
		t.Errorf("everyone pushed %d times", len(ms))
	}
	ids := PushEveryoneID(PushEveryoneID([][]byte{[]byte("1@s.whatsapp.net")}))
	if len(ids) != 2 || string(ids[1]) != AllID {
		t.Errorf("%q", ids)
	}
	me := abi.MentionRef{User: []byte("5511999"), ID: []byte("5511999@s.whatsapp.net")}
	other := abi.MentionRef{User: []byte("123"), ID: []byte("123@lid")}
	ours := [][]byte{[]byte("5511999@s.whatsapp.net"), []byte("77@lid")}
	if !NamesUs(&me, ours) || NamesUs(&other, ours) || !NamesUs(&all, nil) {
		t.Error("NamesUs by id or everyone")
	}
	byUser := abi.MentionRef{User: []byte("77"), ID: []byte("elsewhere")}
	if !NamesUs(&byUser, ours) {
		t.Error("NamesUs by the user part of one of our ids")
	}
}

func TestNothingAllocates(t *testing.T) {
	text := []byte("heading over @all tonight, @todos")
	buf := make([]byte, 0, 128)
	if n := testing.AllocsPerRun(100, func() {
		_ = HasEveryoneToken(text)
		_, _ = RewriteAlias(buf[:0], text, "todos")
		_ = EveryoneQueryMatches("@to", "todos")
	}); n != 0 {
		t.Errorf("%v allocations", n)
	}
}
