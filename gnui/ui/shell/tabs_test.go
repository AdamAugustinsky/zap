package shell

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/model"
	"zapfast/state"
)

// The titlebar keeps a tab for each chat opened, in order; a tab's press
// opens its chat, and closing the open chat's tab opens its neighbour.
func TestTheTitlebarTabsFollowTheOpenedChats(t *testing.T) {
	s := state.New()
	f := fake.New(nil)
	link := &abi.Event{Tag: abi.EventTagLink}
	link.Link.Status.Tag = abi.LinkStatusTagConnected
	f.Emit(link,
		&abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
			{ID: []byte("1@s.whatsapp.net"), Name: []byte("Ada Lovelace"), LastActivity: 5},
			{ID: []byte("2@s.whatsapp.net"), Name: []byte("Grace Hopper"), LastActivity: 7},
			{ID: []byte("3@s.whatsapp.net"), Name: []byte("Katherine Johnson"), LastActivity: 3},
		}}})
	sh, err := New(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := s.AddAccount("1", nil, f)
	r := widgettest.Open(t, Config(s), 1100, 720, 1, sh)
	r.Frames(3)
	ada, grace, kj := a.Model.Chats.Find([]byte("1@s.whatsapp.net")), a.Model.Chats.Find([]byte("2@s.whatsapp.net")), a.Model.Chats.Find([]byte("3@s.whatsapp.net"))
	for _, ref := range []model.ChatRef{ada, grace, kj} {
		a.OpenChat(ref)
		s.Touch()
		r.Frames(2)
	}
	if got := a.ChatTabs(); len(got) != 3 || got[0] != ada || got[1] != grace || got[2] != kj {
		t.Fatalf("tabs %v, want Ada, Grace, Katherine", got)
	}
	if _, ok := r.Find(ui.RoleTab, "Grace Hopper"); !ok {
		t.Fatalf("no tab for Grace Hopper:\n%s", widgettest.Dump(r.Tree()))
	}

	r.Click(ui.RoleTab, "Ada Lovelace")
	r.Frames(2)
	if a.Open != ada {
		t.Fatalf("the tab's press opened %v, want Ada (%v)", a.Open, ada)
	}

	// closing the open chat's tab opens the one after it
	a.CloseChatTab(0)
	s.Touch()
	r.Frames(2)
	if got := a.ChatTabs(); len(got) != 2 || a.Open != grace {
		t.Errorf("after closing Ada's tab: tabs %v, open %v, want Grace (%v) open", got, a.Open, grace)
	}
	if _, ok := r.Find(ui.RoleTab, "Ada Lovelace"); ok {
		t.Error("Ada's tab is still in the titlebar")
	}
}
