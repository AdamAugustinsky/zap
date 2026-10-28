package composer

import (
	"testing"

	"gonativeui/ui"

	"zapfast/abi"
)

// Editing a message that mentions everyone sends the edit with its @all
// token as it was. Whether the edit notifies again is the core's call, made
// from that token and the stored message (zf:src/backend/worker.rs
// prepare_text_edit, whose test text_edits_notify_everyone_only_when_adding_the_mention
// is upstream's); the UI must neither drop the token nor add a mention of
// its own (MM-009).
func TestEditingAnEveryoneMentionKeepsItsToken(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(group)
	m := textMsg("m1", me, true, r.now.Unix()-60, "meeting at 5 @all")
	m.Mentions = []abi.MentionRef{{User: []byte("all"), ID: []byte("mention-all")}}
	r.messages(group, m)
	r.s.Panes.Edit.Add([]byte("m1"))
	r.Frames(2)
	if r.text() != "meeting at 5 @all" {
		t.Fatalf("editing shows %q", r.text())
	}
	r.focus()
	r.key(ui.KeyEnd, 0)
	r.typeText(" sharp")
	r.key(ui.KeyEnter, 0)
	c, ok := r.last(abi.CommandTagEditText)
	if !ok || !eq(c.EditText.Text, "meeting at 5 @all sharp") {
		t.Fatalf("the edit sent %v: %+v", r.f.SentTags(), c.EditText)
	}
}
