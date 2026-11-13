package settings

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/agent"
	"zapfast/core/fake"
	"zapfast/state"
)

// The AI access section: off, it offers only its switch; on, the ways to
// connect an agent, the agent to start and every chat that has access, each
// with the button that takes it back.
func TestAIAccessSection(t *testing.T) {
	const ada = "393331234567@s.whatsapp.net"
	r := newRig(t, 900, 900, func(s *state.State, f *fake.Core, scr *Screen) {
		s.Accounts[0].Settings.AI = map[string]agent.Grant{ada: {Write: true, Wake: true}}
	})
	find := func() {
		r.Play(widgettest.Click(ui.RoleSearchField, "Search settings"), widgettest.Key('A', ui.PrimaryMod()), widgettest.Type("AI access"), widgettest.Idle(2))
	}
	find()
	if !r.text("Let AI agents use chosen chats") || r.text("Connect an agent") {
		t.Fatalf("off: the switch alone was expected")
	}
	// The chats that have a grant show while it is off too, so they can be
	// looked over before it is turned on.
	if !r.text("Chats with AI access") || !r.text("Reply with approval · starts your agent") {
		t.Fatalf("the chat with a grant is not listed")
	}
	r.click(ui.RoleSwitch, "Let AI agents use chosen chats")
	if !r.s.Settings.AIAccess {
		t.Fatal("the switch did not turn AI access on")
	}
	r.Frames(2)
	for _, want := range []string{"Connect an agent", "Claude Code", "Codex", "opencode", "Agent to start", "Replies per hour", "Recent activity"} {
		if !r.text(want) {
			t.Errorf("on: no %q", want)
		}
	}
	if r.text("Custom command") {
		t.Error("the custom command shows with no custom agent chosen")
	}
	r.choose("Agent to start", "Claude Code")
	if r.s.Settings.AIRunner.Kind != agent.RunnerClaude {
		t.Fatalf("runner = %+v", r.s.Settings.AIRunner)
	}
	r.click(ui.RoleButton, "Remove")
	if len(r.acct().Settings.AI) != 0 {
		t.Fatalf("the grant stayed: %+v", r.acct().Settings.AI)
	}
	r.Frames(2)
	if !r.text("No chat has AI access. Right-click a chat and choose AI access.") {
		t.Errorf("no empty state")
	}
}
