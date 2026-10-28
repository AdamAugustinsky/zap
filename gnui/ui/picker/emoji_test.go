package picker

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

// PK-001: a click outside the picker closes it, and the composer gets the
// keyboard back.
func TestAClickOutsideClosesThePicker(t *testing.T) {
	r := newRig(t)
	r.open(state.PickerEmoji)
	r.Click(ui.RoleButton, "Chats") // a press elsewhere
	r.Frames(2)
	if r.s.Panes.Picker != state.PickerNone {
		t.Fatalf("still open: %v", r.s.Panes.Picker)
	}
	if !r.s.Req.FocusComposer {
		t.Error("the composer was not asked to take the keyboard")
	}
	if _, ok := r.Find(ui.RoleDialog, "Emoji picker"); ok {
		t.Error("the picker is still built")
	}
}

// Escape closes it, from the search field, where the keyboard starts.
func TestEscapeClosesThePicker(t *testing.T) {
	r := newRig(t)
	r.open(state.PickerEmoji)
	r.Key(ui.KeyEscape, 0)
	r.Frames(2)
	if r.s.Panes.Picker != state.PickerNone {
		t.Fatal("Escape left it open")
	}
}

// PK-001: the tabs switch what the picker shows, and the composer's own
// toggle (state.Panes.Picker) does as well.
func TestTheTabsSwitchTheBody(t *testing.T) {
	r := newRig(t)
	r.open(state.PickerEmoji)
	r.Click(ui.RoleTab, "Stickers")
	r.Frames(2)
	if r.s.Panes.Picker != state.PickerStickers {
		t.Fatalf("picker %v", r.s.Panes.Picker)
	}
	if _, ok := r.Find(ui.RoleSearchField, "Search by emoji, word, or pack"); !ok {
		t.Error("no sticker search")
	}
	if len(r.sent(abi.CommandTagRecentStickers)) != 1 {
		t.Errorf("opening the stickers asked for them %d times", len(r.sent(abi.CommandTagRecentStickers)))
	}
	r.s.Panes.Picker = state.PickerEmoji
	r.s.Touch()
	r.Frames(3)
	if _, ok := r.Find(ui.RoleSearchField, "Search emoji"); !ok {
		t.Error("the emoji tab did not come back")
	}
	if tab, _ := r.Find(ui.RoleTab, "Emoji"); tab.State&ui.SemSelected == 0 {
		t.Error("the strip does not show Emoji chosen")
	}
}

// PK-002: the emoji tab opens on its groups, with category tabs that jump to
// them, and a search that finds by name and shortcode.
func TestEmojiGroupsSearchAndCategoryTabs(t *testing.T) {
	r := newRig(t)
	r.open(state.PickerEmoji)
	if _, ok := r.Find(ui.RoleButton, "😀"); !ok {
		t.Fatal("the first emoji is not shown")
	}
	if _, ok := r.Find(ui.RoleButton, "🏁"); ok {
		t.Error("the flags are in view at the top")
	}
	for _, name := range []string{"Smileys & Emotion", "People & Body", "Animals & Nature", "Food & Drink", "Travel & Places", "Activities", "Objects", "Symbols", "Flags"} {
		if _, ok := r.Find(ui.RoleTab, name); !ok {
			t.Errorf("no %q category tab", name)
		}
	}
	if _, ok := r.Find(ui.RoleTab, "Recent"); ok {
		t.Error("a Recent tab with no recent emoji")
	}
	r.Click(ui.RoleTab, "Flags")
	r.Frames(4)
	if _, ok := r.Find(ui.RoleButton, "😀"); ok {
		t.Error("the smileys are still in view after jumping to the flags")
	}
	if tab, _ := r.Find(ui.RoleTab, "Flags"); tab.State&ui.SemSelected == 0 {
		t.Error("the flags tab is not the current one")
	}
	r.Click(ui.RoleSearchField, "Search emoji")
	r.Type("crab")
	r.Frames(2)
	if _, ok := r.Find(ui.RoleButton, "🦀"); !ok {
		t.Fatal("crab did not find 🦀")
	}
	if _, ok := r.Find(ui.RoleButton, "😀"); ok {
		t.Error("the search left the smileys in")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Clear"), widgettest.Idle(2))
	r.Type("zzzzzz")
	r.Frames(2)
	if _, ok := r.Find(ui.RoleButton, "🦀"); ok {
		t.Error("a search that matches nothing shows emoji")
	}
}

// PK-003: an emoji goes to the composer and is remembered as recent; the
// picker stays open for the next one. Recent emoji lead the grid, in the
// order the picker opened with.
func TestChoosingAnEmojiInsertsItAndRecordsIt(t *testing.T) {
	r := newRig(t)
	r.s.Settings.RecentEmoji = [][]byte{[]byte("🎉"), []byte("not an emoji")}
	r.open(state.PickerEmoji)
	if _, ok := r.Find(ui.RoleTab, "Recent"); !ok {
		t.Error("no Recent tab with a recent emoji")
	}
	if _, ok := r.Find(ui.RoleButton, "🎉"); !ok {
		t.Error("the recent emoji is not first in the grid")
	}
	r.Type("crab")
	r.Click(ui.RoleButton, "🦀")
	r.Frames(2)
	text, ok := r.insert()
	if !ok || string(text) != "🦀" {
		t.Fatalf("the composer was offered %q", text)
	}
	if _, again := r.insert(); again {
		t.Error("an emoji is offered twice")
	}
	rec := r.s.Settings.RecentEmoji
	if len(rec) != 3 || string(rec[0]) != "🦀" || string(rec[1]) != "🎉" {
		t.Errorf("recent emoji %q", rec)
	}
	if r.s.Panes.Picker != state.PickerEmoji {
		t.Error("choosing an emoji closed the picker")
	}
	if !r.s.Req.FocusComposer {
		t.Error("the composer was not asked for the keyboard")
	}
}

// The arrows move through the grid from the search field and Enter chooses,
// as upstream's keys do.
func TestArrowsAndEnterChooseFromTheSearchField(t *testing.T) {
	r := newRig(t)
	r.open(state.PickerEmoji)
	r.Play(widgettest.Key(ui.KeyRight, 0), widgettest.Key(ui.KeyRight, 0), widgettest.Key(ui.KeyDown, 0), widgettest.Idle(2))
	cols := r.pk.em.g.cols
	if r.pk.em.selected != 2+cols {
		t.Fatalf("selected %d, want %d", r.pk.em.selected, 2+cols)
	}
	r.Play(widgettest.Key(ui.KeyUp, 0), widgettest.Key(ui.KeyLeft, 0), widgettest.Idle(2))
	if r.pk.em.selected != 1 {
		t.Fatalf("selected %d after Up and Left", r.pk.em.selected)
	}
	want := emojis().All[r.pk.em.g.flat[1]].Glyph
	r.Play(widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(2))
	if text, _ := r.insert(); string(text) != want {
		t.Errorf("Enter chose %q, want %q", text, want)
	}
	// moving past the first visible rows brings the selection into view
	for i := 0; i < 12; i++ {
		r.Play(widgettest.Key(ui.KeyDown, 0))
	}
	r.Frames(3)
	want = emojis().All[r.pk.em.g.flat[r.pk.em.selected]].Glyph
	if _, ok := r.Find(ui.RoleButton, want); !ok {
		t.Errorf("the selected %q is not in view", want)
	}
}

// PK-004: as a reaction picker it reacts to the message and closes; choosing
// the emoji already reacted with takes the reaction back.
func TestReactionPicker(t *testing.T) {
	r := newRig(t)
	r.s.OpenReaction(r.a.Open, []byte(msgID), anchorRect())
	r.Frames(4)
	if _, ok := r.Find(ui.RoleDialog, "React to message"); !ok {
		t.Fatalf("no reaction picker:\n%s", widgettest.Dump(r.Tree()))
	}
	if _, ok := r.Find(ui.RoleButton, "Close reactions"); !ok {
		t.Error("no close button")
	}
	if _, ok := r.Find(ui.RoleTab, "Stickers"); ok {
		t.Error("the reaction picker has the tab strip")
	}
	r.Type("crab")
	r.Click(ui.RoleButton, "🦀")
	r.Frames(3)
	reacts := r.sent(abi.CommandTagReact)
	if len(reacts) != 1 || string(reacts[0].React.Emoji) != "🦀" || string(reacts[0].React.Message) != msgID || string(reacts[0].React.Chat) != chatID {
		t.Fatalf("reactions sent: %+v", reacts)
	}
	if _, ok := r.Find(ui.RoleDialog, "React to message"); ok {
		t.Error("the picker stayed open after reacting")
	}
	if r.s.Req.FocusComposer {
		t.Error("a reaction took the composer's keyboard")
	}
	if text, ok := r.insert(); ok {
		t.Errorf("a reaction inserted %q in the composer", text)
	}
	// the emoji we reacted with now leads the picker as a frequent one
	r.s.OpenReaction(r.a.Open, []byte(msgID), anchorRect())
	r.Frames(4)
	if _, ok := r.Find(ui.RoleTab, "Frequently Used"); !ok {
		t.Error("no Frequently Used tab after a reaction")
	}
}

func TestReactingAgainWithTheSameEmojiTakesItBack(t *testing.T) {
	r := newRig(t)
	r.emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		var m abi.Message
		m.ID, m.Chat, m.Sender = []byte(msgID), []byte(chatID), []byte(chatID)
		m.Content.Tag = abi.ContentTagText
		m.Content.Text.Text = []byte("Hello there")
		m.Timestamp = 10
		m.Reactions = []abi.Reaction{{FromMe: true, Emoji: []byte("🦀")}}
		e.Messages = abi.EventMessages{Chat: []byte(chatID), Batch: 2, Last: true, Messages: []abi.Message{m}}
	}))
	r.s.OpenReaction(r.a.Open, []byte(msgID), anchorRect())
	r.Frames(4)
	r.Type("crab")
	r.Click(ui.RoleButton, "🦀")
	r.Frames(2)
	reacts := r.sent(abi.CommandTagReact)
	if len(reacts) != 1 || len(reacts[0].React.Emoji) != 0 {
		t.Fatalf("reactions sent: %+v", reacts)
	}
}

func TestTheReactionPickerClosesWithItsMessage(t *testing.T) {
	r := newRig(t)
	r.s.OpenReaction(r.a.Open, []byte("nothing"), anchorRect())
	r.Frames(3)
	if _, ok := r.Find(ui.RoleDialog, "React to message"); ok {
		t.Error("a reaction picker for a message that is not there")
	}
}

func TestSearchAndMovement(t *testing.T) {
	var g grid
	g.build("crab", nil, 8, "Recent")
	found := map[string]bool{}
	for _, i := range g.flat {
		found[emojis().All[i].Glyph] = true
	}
	if !found["🦀"] {
		t.Error("crab does not find 🦀")
	}
	g.build("zzzzz", nil, 8, "Recent")
	if len(g.rows) != 1 || g.rows[0].label != "Nothing matches" {
		t.Errorf("rows %+v", g.rows)
	}
	g.build("", recentIndexes(nil, [][]byte{[]byte("👍")}), 8, "Frequently Used")
	if g.rows[0].label != "Frequently Used" || g.rows[2].label == "" && g.rows[1].label != "" {
		t.Errorf("first rows %+v", g.rows[:3])
	}
	for _, label := range []string{"Smileys & Emotion", "Flags"} {
		found := false
		for _, row := range g.rows {
			found = found || row.label == label
		}
		if !found {
			t.Errorf("no %q heading", label)
		}
	}
	g.build("", nil, 8, "Recent")
	if g.rows[0].label != "Smileys & Emotion" {
		t.Errorf("empty recent leaves a %q heading first", g.rows[0].label)
	}
	if row := g.headerRow(0); row != 0 {
		t.Errorf("a jump to the missing recent row went to %d, want the first group's heading", row)
	}
	if moveSelection(0, 25, 10, moveRight) != 1 || moveSelection(1, 25, 10, moveDown) != 11 ||
		moveSelection(11, 25, 10, moveLeft) != 10 || moveSelection(10, 25, 10, moveUp) != 0 ||
		moveSelection(20, 25, 10, moveDown) != 20 || moveSelection(24, 25, 10, moveRight) != 24 {
		t.Error("the arrows move wrongly")
	}
}

// The reaction is counted for the quick reactions, and the picker's close
// button gives up the target.
func TestReactionCountsAndCloseEndsTheTarget(t *testing.T) {
	r := newRig(t)
	r.s.OpenReaction(r.a.Open, []byte(msgID), anchorRect())
	r.Frames(4)
	r.Type("crab")
	r.Click(ui.RoleButton, "🦀")
	r.Frames(3)
	if re := r.s.Settings.ReactionEmoji; len(re) != 1 || string(re[0].Emoji) != "🦀" || re[0].Count != 1 {
		t.Errorf("reaction emoji %+v", re)
	}
	if r.s.Panes.ReactTarget.Open() {
		t.Error("the target outlived the reaction")
	}
	r.s.OpenReaction(r.a.Open, []byte(msgID), anchorRect())
	r.Frames(4)
	r.Click(ui.RoleButton, "Close reactions")
	r.Frames(3)
	if r.s.Panes.ReactTarget.Open() {
		t.Error("closing the reaction picker left its target")
	}
	if _, ok := r.Find(ui.RoleDialog, "React to message"); ok {
		t.Error("the reaction picker is still open")
	}
	if len(r.sent(abi.CommandTagReact)) != 1 {
		t.Error("closing sent a reaction")
	}
}

// The picker opens above the composer's emoji button, wherever the composer
// puts it.
func TestOpensAboveTheComposerButton(t *testing.T) {
	r := newRig(t)
	r.s.Panes.PickerAnchor = state.Rect{X: 600, Y: 560, W: 30, H: 30}
	r.open(state.PickerEmoji)
	d, ok := r.Find(ui.RoleDialog, "Emoji picker")
	if !ok {
		t.Fatal("no picker")
	}
	if d.Bounds[3] > 560+1 {
		t.Errorf("the picker reaches %v, below the button at 560", d.Bounds[3])
	}
}
