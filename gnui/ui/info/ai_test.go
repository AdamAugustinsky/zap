package info_test

import (
	"testing"

	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/agent"
)

// The panel's AI access: off until the person chooses, a reply level asks
// first until they say otherwise, and Off takes the grant away. A locked
// chat is not offered it.
func TestAIAccessOfAChat(t *testing.T) {
	r := newRig(t, chatSpec{id: ada, name: "Ada"}, chatSpec{id: grace, name: "Grace", mut: func(c *abi.Chat) { c.Locked = true }})
	r.open(ada, ada)
	if _, ok := r.a.AIGrant(b(ada)); ok || r.s.Settings.AIAccess {
		t.Fatal("a chat starts with AI access")
	}
	r.click(ui.RoleButton, "AI access")
	r.click(ui.RoleRadio, "Read and reply")
	g, ok := r.a.AIGrant(b(ada))
	if !ok || !g.Write || g.Auto || g.Wake || !r.s.Settings.AIAccess {
		t.Fatalf("Read and reply gave %+v (%v), AI access on: %v", g, ok, r.s.Settings.AIAccess)
	}
	r.click(ui.RoleSwitch, "Ask me before each reply")
	r.click(ui.RoleSwitch, "Start my agent when a message arrives")
	if g, _ := r.a.AIGrant(b(ada)); g != (agent.Grant{Write: true, Auto: true, Wake: true}) {
		t.Fatalf("the switches gave %+v", g)
	}
	r.click(ui.RoleRadio, "Read")
	if g, _ := r.a.AIGrant(b(ada)); g.Write || g.Auto || !g.Wake {
		t.Fatalf("Read gave %+v", g)
	}
	r.wantNot(ui.RoleSwitch, "Ask me before each reply") // nothing to ask about: it cannot reply
	r.click(ui.RoleRadio, "Off")
	if g, ok := r.a.AIGrant(b(ada)); ok {
		t.Fatalf("Off left %+v", g)
	}

	r.open(grace, grace)
	r.wantNot(ui.RoleButton, "AI access")
}
