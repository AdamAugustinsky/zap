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
	// at the top: a message in a lower chat puts it first and in view
	r.update(chat("3", "Chat 3", 5000))
	if y := r.rowTop("Chat 3"); y < 0 || y > first+1 {
		t.Fatalf("the chat that got a message is at y=%v, the top row was at %v", y, first)
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
