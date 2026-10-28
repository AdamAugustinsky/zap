package composer

import (
	"testing"

	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/core/fake"
)

// Switching accounts parks the open chat's text, mentions, staged files and
// the reply they carry, empties the composer for the other account, and
// puts them back on return (AC-007, zf:src/app.rs:1559-1606).
func TestSwitchingAccountsParksTheComposer(t *testing.T) {
	r := newRig(t, nil, nil)
	f2 := fake.New(nil)
	f2.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: 1}}}
		}))
	b := r.s.AddAccount("2", nil, f2)
	r.Frames(2)
	r.open(group)
	r.messages(group, textMsg("m1", ada, false, 100, "Shall we compute?"))
	r.focus()
	r.typeKeys("hi ", "@", "gr")
	r.key(ui.KeyEnter, 0)
	r.typeText("look")
	r.s.Panes.Reply.Add([]byte("m1"))
	r.dropAt(ui.DropFiles, []byte("/tmp/a.txt\x00"), 1)
	if r.text() != "hi @Grace Hopper look" || len(r.chips()) != 1 {
		t.Fatalf("set-up: %q, %d chips", r.text(), len(r.chips()))
	}

	b.OpenChat(b.Model.Chats.Find([]byte(ada)))
	r.s.SwitchAccount(1)
	r.Frames(4)
	if r.text() != "" || len(r.chips()) != 0 || r.s.Panes.Reply.Len() != 0 {
		t.Errorf("the other account's composer holds %q, %d chips, %d replies", r.text(), len(r.chips()), r.s.Panes.Reply.Len())
	}

	r.s.SwitchAccount(0)
	r.Frames(4)
	if r.text() != "hi @Grace Hopper look" || len(r.chips()) != 1 || r.s.Panes.Reply.Len() != 1 || string(r.s.Panes.Reply.At(0)) != "m1" {
		t.Fatalf("back again: %q, %d chips, %d replies", r.text(), len(r.chips()), r.s.Panes.Reply.Len())
	}
	r.key(ui.KeyEnter, 0)
	c, ok := r.last(abi.CommandTagSendFiles)
	if !ok || !c.SendFiles.HasQuoting || !eq(c.SendFiles.Quoting.ID, "m1") || !eq(c.SendFiles.Caption, "hi @2 look") ||
		len(c.SendFiles.Mentions) != 1 || !eq(c.SendFiles.Mentions[0], grace) {
		t.Fatalf("sent %v: %+v", r.f.SentTags(), c.SendFiles)
	}
}
