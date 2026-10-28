package info_test

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"
	"zapfast/abi"
)

// Steady frames allocate nothing: the overview of a group with members and a
// long description, the lists of media, documents and links, and a contact.
func TestSteadyFramesAllocateNothing(t *testing.T) {
	r := newRig(t, teamChat(func(c *abi.Chat) {
		c.GroupDescription = b("Weekly sync notes and general chatter. Weekly sync notes and general chatter. Weekly sync notes and general chatter. Weekly sync notes and general chatter.")
	}), chatSpec{id: ada, name: "Ada Lovelace"})
	check := func(what string) {
		t.Helper()
		r.Frames(10)
		if n := testing.AllocsPerRun(30, r.Frame); n != 0 {
			t.Errorf("%s: %v allocations per frame", what, n)
		}
	}
	r.open(team, team)
	r.answer(answerOpts{},
		photo("p1", team, epoch), video("v1", team, epoch+60, 75),
		doc("d1", team, "report.pdf", epoch+10, 1_500_000, 12),
		link("l1", "https://example.com/post", "A post", epoch+30),
	)
	check("group overview")
	r.click(ui.RoleButton, "Media, links and docs")
	check("media")
	r.click(ui.RoleTab, "Docs 1")
	check("documents")
	r.click(ui.RoleTab, "Links 1")
	check("links")
	r.click(ui.RoleButton, "Back")
	r.click(ui.RoleButton, "Edit group name")
	check("group name editor")
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	r.open(ada, ada)
	r.f.Emit(ev(abi.EventTagPresence, func(e *abi.Event) {
		e.Presence = abi.EventPresence{ID: b(ada), LastSeen: r.s.Now().Unix() - 7200, HasLastSeen: true}
	}))
	check("contact")
	r.click(ui.RoleButton, "Rename")
	check("contact editor")
}
