package chatlist

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
)

func manyChats(n int) []abi.Chat {
	var cs []abi.Chat
	for i := 0; i < n; i++ {
		cs = append(cs, chat(itoa(i+1), "Chat "+itoa(i+1), int64(1000-i)))
	}
	return cs
}

func (r *rig) rowTop(name string) float32 {
	n, ok := r.Find(ui.RoleListItem, name)
	if !ok {
		return -1
	}
	return n.Bounds[1]
}

// A chat that gets a message moves to the top; when the list is at its top it
// shows there, and a reader further down keeps their place.
func TestListFollowsAMovedChatOnlyNearTheTop(t *testing.T) {
	r := newRig(t)
	r.chats(manyChats(40)...)
	first := r.rowTop("Chat 1")
	// at the top: a message in a lower chat puts it first and in view, not
	// above the list's top with the old first row still pinned there
	r.update(chat("3", "Chat 3", 5000))
	if y := r.rowTop("Chat 3"); y < first-1 || y > first+1 {
		t.Fatalf("the chat that got a message is at y=%v, the top row was at %v", y, first)
	}
	// a chat new to the list comes first and shows too
	r.update(chat("41", "Chat 41", 6000))
	if y := r.rowTop("Chat 41"); y < first-1 || y > first+1 {
		t.Fatalf("the new chat is at y=%v, the top row was at %v", y, first)
	}
	// scrolled well down: the chat that moves leaves the reader where they are
	r.Play(widgettest.Wheel(ui.RoleListItem, "Chat 3", -300, 2), widgettest.Idle(3))
	if r.has(ui.RoleListItem, "Chat 1") {
		t.Fatalf("the list did not scroll: rows %v", r.rows())
	}
	r.update(chat("40", "Chat 40", 9000))
	if r.has(ui.RoleListItem, "Chat 40") || r.has(ui.RoleListItem, "Chat 3") {
		t.Errorf("the list jumped to its top for a reader further down: rows %v", r.rows())
	}
}

// A chat opened from outside the list (⌘K, a notification, a link) leaves
// the list where it is; stepping to the next chat (⌥↓) walks the list, which
// follows.
func TestOnlyWalkingTheListScrollsIt(t *testing.T) {
	r := newRig(t)
	r.chats(manyChats(40)...)
	first := r.rowTop("Chat 1")
	a := r.acct()
	a.OpenChat(a.Model.Chats.Find(jid("35"))) // as ⌘K opens a chat
	r.Frames(3)
	if y := r.rowTop("Chat 1"); y != first {
		t.Fatalf("opening a chat far down moved the list: Chat 1 at y=%v, was %v", y, first)
	}
	r.Play(widgettest.Key(ui.KeyDown, ui.ModAlt), widgettest.Idle(3))
	if ch, _ := a.Model.Chats.Get(a.Open); string(ch.Name()) != "Chat 36" {
		t.Fatalf("⌥↓ opened %q, want Chat 36", ch.Name())
	}
	if y := r.rowTop("Chat 36"); y < first-1 {
		t.Fatalf("⌥↓ did not bring Chat 36 into view: y=%v, the list's top is %v", y, first)
	}
}

// Leaving a search puts the list back where the reader had it.
func TestLeavingASearchGoesBackToThePlace(t *testing.T) {
	r := newRig(t)
	r.chats(manyChats(40)...)
	r.Play(widgettest.Wheel(ui.RoleListItem, "Chat 3", -300, 2), widgettest.Idle(3))
	rows := r.rows()
	mark := rows[len(rows)/2]
	y := r.rowTop(mark)
	searchFor(r, "Chat 1")
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(3))
	if got := r.rowTop(mark); got != y {
		t.Fatalf("after the search %s is at y=%v, was %v (rows %v)", mark, got, y, r.rows())
	}
}
