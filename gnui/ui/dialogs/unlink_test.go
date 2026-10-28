package dialogs

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

func contactsEvent(cs ...abi.Contact) *abi.Event {
	return ev(abi.EventTagContacts, func(e *abi.Event) { e.Contacts.Contacts = cs })
}

// LG-007, DL-005: a number linked after another was unlinked from the
// account finds none of the first one's contacts in New chat: the unlink
// forgets them (zf:src/app.rs:4141 contacts.clear()), so the second
// number's Contacts upsert into an empty list.
func TestNewChatAfterARelinkShowsOnlyTheNewNumbersContacts(t *testing.T) {
	r := newRig(t, append(baseEvents(), contactsEvent(
		abi.Contact{ID: []byte("3@s.whatsapp.net"), FullName: []byte("Charles Babbage"), HasFullName: true},
		abi.Contact{ID: []byte("1@s.whatsapp.net"), FullName: []byte("Ada Lovelace"), HasFullName: true},
	))...)
	r.open(state.Dialog{Kind: state.DialogNewChat})
	if !r.has(ui.RoleListItem, "Charles Babbage") {
		t.Fatalf("set-up: the first number's contact is not offered\n%s", widgettest.Dump(r.Tree()))
	}
	r.s.CloseDialog()
	r.Frames(2)

	r.f.Emit(
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagLoggedOut }),
		linkedEvent(),
		chatsEvent(chat("1@s.whatsapp.net", "Ada", 30)),
		contactsEvent(abi.Contact{ID: []byte("9@s.whatsapp.net"), FullName: []byte("Hedy Lamarr"), HasFullName: true}),
	)
	r.Frames(3)
	r.open(state.Dialog{Kind: state.DialogNewChat})
	for _, old := range []string{"Charles Babbage", "Ada Lovelace", "Grace Hopper", "Analytical Engine"} {
		if r.has(ui.RoleListItem, old) {
			t.Errorf("New chat offers the first number's %q\n%s", old, widgettest.Dump(r.Tree()))
		}
	}
	if !r.has(ui.RoleListItem, "Hedy Lamarr") {
		t.Errorf("New chat does not offer the new number's contact\n%s", widgettest.Dump(r.Tree()))
	}
}
