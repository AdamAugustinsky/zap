package composer

import (
	"context"
	"strings"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/agent"
	"zapfast/state"
)

// aiRig is the rig with the agents' door open and Ada's chat given to an
// agent that asks first; propose is what such an agent sends.
func aiRig(t *testing.T) (r *rig, propose func(text string) agent.Reply) {
	t.Helper()
	r = newRig(t, nil, func(s *state.State) { s.Agent = agent.New(agent.Options{}) })
	t.Cleanup(r.s.Agent.Close)
	r.a.SetAIGrant([]byte(ada), agent.Grant{Write: true})
	r.open(ada)
	return r, func(text string) agent.Reply {
		t.Helper()
		rep := r.s.Agent.Do(context.Background(), agent.Request{Op: "send", Chat: agent.Handle("1", ada), Text: text})
		r.Frames(3)
		r.now = r.now.Add(time.Second) // the reply has been in the strip a moment
		return rep
	}
}

// A reply from an agent that asks first is not sent: it waits above the
// field, Send sends it as the person's own message, and Discard drops it.
func TestAnAgentsReplyWaitsForItsAnswer(t *testing.T) {
	r, propose := aiRig(t)
	if rep := propose("The branch is pushed."); rep.Status != "waiting_for_approval" {
		t.Fatalf("the agent was told %+v", rep)
	}
	if n := len(r.sent(abi.CommandTagSendText)); n != 0 || !r.hasText("The branch is pushed.") {
		t.Fatalf("%d sent before the answer; strip shown: %v", n, r.hasText("The branch is pushed."))
	}
	r.Play(widgettest.Click(ui.RoleButton, "Send"), widgettest.Idle(3))
	c, ok := r.last(abi.CommandTagSendText)
	if !ok || !eq(c.SendText.Chat, ada) || !eq(c.SendText.Text, "The branch is pushed.") {
		t.Fatalf("Send sent %+v (%v)", c.SendText, ok)
	}
	if r.hasText("Your agent wrote this reply") {
		t.Error("the strip stayed after Send")
	}

	propose("Second thoughts.")
	r.Play(widgettest.Click(ui.RoleButton, "Discard"), widgettest.Idle(3))
	if n := len(r.sent(abi.CommandTagSendText)); n != 1 || r.hasText("Second thoughts.") {
		t.Fatalf("after Discard: %d sent, strip shown: %v", n, r.hasText("Second thoughts."))
	}
}

// Edit moves the agent's words into the field and sends nothing: what goes
// out from there is what the person sends.
func TestEditTakesAnAgentsReplyIntoTheField(t *testing.T) {
	r, propose := aiRig(t)
	propose("The branch is pushed.")
	r.Play(widgettest.Click(ui.RoleButton, "Edit"), widgettest.Idle(3))
	if got := r.text(); got != "The branch is pushed." {
		t.Fatalf("the field holds %q", got)
	}
	if n := len(r.sent(abi.CommandTagSendText)); n != 0 || r.hasText("Your agent wrote this reply") {
		t.Fatalf("after Edit: %d sent, strip shown: %v", n, r.hasText("Your agent wrote this reply"))
	}
}

// A reply longer than the strip shows cannot be sent unread: only Edit,
// which puts all of it in the field, and Discard are offered.
func TestALongReplyIsNotSentUnread(t *testing.T) {
	r, propose := aiRig(t)
	long := strings.Repeat("All work and no play. ", 60)
	propose(long)
	if r.has(ui.RoleButton, "Send") {
		t.Fatal("Send is offered for a reply shown in part")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Edit"), widgettest.Idle(3))
	if got := r.text(); got != strings.TrimSpace(long) {
		t.Fatalf("the field holds %d bytes of %d", len(got), len(long))
	}
}

// The click that sends one reply does not also answer the next: it takes
// the first one's place in the strip, unread, under the same button.
func TestASecondClickDoesNotSendTheNextReply(t *testing.T) {
	r, propose := aiRig(t)
	propose("First.")
	propose("Second.")
	r.Play(widgettest.Click(ui.RoleButton, "Send"), widgettest.Idle(2), widgettest.Click(ui.RoleButton, "Send"), widgettest.Idle(3))
	if got := r.sent(abi.CommandTagSendText); len(got) != 1 || !eq(got[0].SendText.Text, "First.") {
		t.Fatalf("%d replies sent by a double-click", len(got))
	}
	if !r.hasText("Second.") {
		t.Fatal("the second reply is not waiting")
	}
	r.now = r.now.Add(time.Second)
	r.Play(widgettest.Click(ui.RoleButton, "Send"), widgettest.Idle(3))
	if got := r.sent(abi.CommandTagSendText); len(got) != 2 || !eq(got[1].SendText.Text, "Second.") {
		t.Fatalf("the second reply, once seen: %d sent", len(got))
	}
}
