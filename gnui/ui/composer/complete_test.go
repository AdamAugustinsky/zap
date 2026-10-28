package composer

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

// typeKeys types each piece as a keystroke of its own (a trigger opens
// only when it is typed).
func (r *rig) typeKeys(pieces ...string) {
	r.t.Helper()
	for _, p := range pieces {
		r.typeText(p)
	}
}

// listShown reports whether the suggestions list is on screen.
func (r *rig) listShown() bool { return r.has(ui.RoleList, "Suggestions") }

// CP-006, KB-033: "@" in a group offers its members above the composer;
// Down, Up and Enter work the list, not the text; accepting puts the name
// in as one undo step, and the send turns it into WhatsApp's mention.
func TestMentionSuggestions(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(group)
	r.focus()
	r.typeKeys("hi ", "@")
	if !r.listShown() || !r.has(ui.RoleListItem, "Ada Lovelace") || !r.has(ui.RoleListItem, "Grace Hopper") || !r.has(ui.RoleListItem, "all") {
		t.Fatalf("no members after @:\n%s", widgettest.Dump(r.Tree()))
	}
	r.typeKeys("gr")
	if r.has(ui.RoleListItem, "Ada Lovelace") || !r.has(ui.RoleListItem, "Grace Hopper") {
		t.Fatal("the list did not narrow to Grace")
	}
	// The list sits above the composer and never over its field.
	list, _ := r.Find(ui.RoleList, "Suggestions")
	field, _ := r.Find(ui.RoleGroup, "Message")
	if list.Bounds[3] > field.Bounds[1] {
		t.Errorf("the list %v overlaps the field %v", list.Bounds, field.Bounds)
	}
	r.key(ui.KeyDown, 0)
	r.key(ui.KeyUp, 0)
	if r.text() != "hi @gr" {
		t.Fatalf("the list's keys reached the text: %q", r.text())
	}
	r.key(ui.KeyEnter, 0)
	if r.text() != "hi @Grace Hopper " || r.listShown() {
		t.Fatalf("accepting gave %q", r.text())
	}
	if _, ok := r.last(abi.CommandTagSendText); ok {
		t.Fatal("the Enter that accepted also sent")
	}
	// One undo takes the accept back.
	r.Play(widgettest.Edit(ui.EditUndo), widgettest.Idle(2))
	if r.text() != "hi @gr" {
		t.Fatalf("undo gave %q", r.text())
	}
	r.Play(widgettest.Edit(ui.EditRedo), widgettest.Idle(2))
	if r.text() != "hi @Grace Hopper " {
		t.Fatalf("redo gave %q", r.text())
	}
	r.typeText("look")
	r.key(ui.KeyEnter, 0)
	c, ok := r.last(abi.CommandTagSendText)
	if !ok || !eq(c.SendText.Text, "hi @2 look") || len(c.SendText.Mentions) != 1 || !eq(c.SendText.Mentions[0], grace) {
		t.Fatalf("sent %q mentioning %q", c.SendText.Text, c.SendText.Mentions)
	}
}

// CP-007: "all" heads the list in a small group and goes in as @all.
func TestMentionEveryone(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(group)
	r.focus()
	r.typeKeys("@", "a")
	r.key(ui.KeyEnter, 0)
	if r.text() != "@all " {
		t.Fatalf("all went in as %q", r.text())
	}
	r.key(ui.KeyEnter, 0)
	if c, ok := r.last(abi.CommandTagSendText); !ok || !eq(c.SendText.Text, "@all") {
		t.Fatalf("sent %q", c.SendText.Text)
	}
}

// Escape closes the list and leaves the text; a direct chat offers no
// members; an @ inside an address stays text.
func TestMentionListCloses(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(group)
	r.focus()
	r.typeKeys("@")
	if !r.listShown() {
		t.Fatal("no list")
	}
	r.key(ui.KeyEscape, 0)
	if r.listShown() || r.text() != "@" {
		t.Fatalf("Escape: list %v, text %q", r.listShown(), r.text())
	}
	if r.a.Open == 0 {
		t.Fatal("the Escape that closed the list closed the chat")
	}
	r.typeKeys(" mail me at a", "@", "b")
	if r.listShown() {
		t.Error("an address opened the list")
	}
	r.open(ada)
	r.focus()
	r.typeKeys("@")
	if r.listShown() {
		t.Error("a direct chat offers members")
	}
}

// CP-008: ":" and letters offer emoji that narrow as you type; accepting
// replaces the shortcode; colons in times and links stay text.
func TestEmojiSuggestions(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.focus()
	r.typeKeys("ok ", ":")
	if !r.listShown() {
		t.Fatal("no emoji after :")
	}
	r.typeKeys("thumbsu")
	first := emojiTable()[r.scr.comp.emoji[0]]
	if first.emoji != "👍" || !r.has(ui.RoleListItem, first.row) {
		t.Fatalf("the first suggestion is %q", first.row)
	}
	r.key(ui.KeyEnter, 0)
	if r.text() != "ok 👍" {
		t.Fatalf("accepting gave %q", r.text())
	}
	if len(r.s.Settings.RecentEmoji) == 0 || string(r.s.Settings.RecentEmoji[0]) != "👍" {
		t.Error("the emoji is not remembered as recent")
	}
	// The recent emoji head the list of an empty query.
	r.typeKeys(" ", ":")
	if e := emojiTable()[r.scr.comp.emoji[0]]; e.emoji != "👍" {
		t.Errorf("the empty query starts with %q", e.emoji)
	}
	r.key(ui.KeyEscape, 0)
	for _, keys := range [][]string{{" at 10", ":", "30"}, {" http", ":", "//x"}} {
		r.typeKeys(keys...)
		if r.listShown() {
			t.Errorf("%q opened the list", keys)
		}
	}
	// A space ends the query without changing the text.
	r.typeKeys(" ", ":", "smi")
	if !r.listShown() {
		t.Fatal("no list for :smi")
	}
	r.typeKeys(" ")
	if r.listShown() {
		t.Error("a space did not end the query")
	}
}

// The emoji ranking: an exact shortcode first, then a shortcode's prefix,
// then a name; nothing for what matches nothing.
func TestEmojiRanking(t *testing.T) {
	var c completion
	r := state.New()
	c.query = []byte("smile")
	c.emojiCandidates(r)
	if len(c.emoji) == 0 || emojiTable()[c.emoji[0]].emoji != "😄" {
		t.Fatalf("smile: %v", c.emoji)
	}
	c.query = []byte("zzzzqq")
	c.emojiCandidates(r)
	if len(c.emoji) != 0 {
		t.Errorf("zzzzqq: %d", len(c.emoji))
	}
	c.query = []byte("heart")
	c.emojiCandidates(r)
	if len(c.emoji) != emojiLimit {
		t.Errorf("heart: %d", len(c.emoji))
	}
}

// CP-009: an input method's composition closes the list, and the Enter that
// commits it does not send.
func TestCompositionClosesTheList(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(group)
	r.focus()
	r.typeKeys("@")
	if !r.listShown() {
		t.Fatal("no list")
	}
	r.before(func(c *ui.Context) { c.Input().SetPreedit([]byte("か"), 3, 0) })
	if r.listShown() {
		t.Error("the list stays while an input method composes")
	}
	r.before(func(c *ui.Context) {
		in := c.Input()
		in.SetPreedit([]byte("か"), 3, 0)
		in.PushKey(ui.KeyEvent{Code: ui.KeyEnter, Down: true})
	})
	r.Frames(2)
	if _, ok := r.last(abi.CommandTagSendText); ok {
		t.Error("the Enter of a composition sent")
	}
}

// SH-017: Starred comes before the suggestions in Escape's order
// (zf:src/ui/keys.rs:164): the first Escape leaves Starred and keeps the
// list; the next closes the list.
func TestEscapeLeavesStarredBeforeTheSuggestions(t *testing.T) {
	r := newRig(t, nil, nil)
	r.a.Mode = state.ListStarred
	r.open(group)
	r.focus()
	r.typeKeys("@")
	if !r.listShown() {
		t.Fatal("no list")
	}
	r.key(ui.KeyEscape, 0)
	if r.a.Mode != state.ListChats || !r.listShown() {
		t.Fatalf("first Escape: mode %v, list %v", r.a.Mode, r.listShown())
	}
	r.key(ui.KeyEscape, 0)
	if r.listShown() || r.a.Open == 0 {
		t.Errorf("second Escape: list %v, open %v", r.listShown(), r.a.Open)
	}
}
