package kinds

import (
	"testing"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/calls"
)

// A call link in a message joins through the call hub, as one opened from
// the system does: each join is numbered, so the second link's call after
// the first ended is drawn and controlled, not ignored as the first's late
// word.
func TestMessageCallLinksJoinThroughTheHub(t *testing.T) {
	s := state.New()
	f := fake.New(nil)
	a := s.AddAccount("1", nil, f)
	f.Emit(&abi.Event{Tag: abi.EventTagLink, Link: abi.EventLink{Status: abi.LinkStatus{Tag: abi.LinkStatusTagConnected}}})
	s.Pump(0)
	join := func(link string) abi.Command {
		t.Helper()
		before := len(f.Sent())
		OpenLink(s, a, link)
		sent := f.Sent()[before:]
		if len(sent) != 1 || sent[0].Tag != abi.CommandTagJoinCallLink {
			t.Fatalf("%s sent %v", link, f.SentTags()[before:])
		}
		c := sent[0]
		if c.JoinCallLink.Generation == 0 || c.JoinCallLink.Cancel == 0 {
			t.Fatalf("%s joined unnumbered: generation %d, cancel %d", link, c.JoinCallLink.Generation, c.JoinCallLink.Cancel)
		}
		if !calls.LiveHere(s, a, nil) {
			t.Fatalf("%s: the hub shows no call", link)
		}
		return c
	}
	phase := func(gen uint64, p abi.CallPhase) {
		t.Helper()
		f.Emit(&abi.Event{Tag: abi.EventTagCall, Call: abi.EventCall{Update: abi.CallUpdate{
			Generation: gen, Direction: abi.CallDirectionOutgoing, Phase: p, HasGroup: true, Group: abi.GroupCall{Link: true}}}})
		s.Pump(0)
	}
	first := join("https://call.whatsapp.com/voice/AbCdEfGh1234")
	phase(first.JoinCallLink.Generation, abi.CallPhaseActive)
	phase(first.JoinCallLink.Generation, abi.CallPhaseEnded)
	if calls.LiveHere(s, a, nil) {
		t.Fatal("the first call did not end")
	}
	second := join("https://call.whatsapp.com/video/ZyXwVuTs9876")
	if second.JoinCallLink.Generation == first.JoinCallLink.Generation || !second.JoinCallLink.Video {
		t.Fatalf("second join: %+v", second.JoinCallLink)
	}
	phase(second.JoinCallLink.Generation, abi.CallPhaseActive)
	if !calls.LiveHere(s, a, nil) {
		t.Error("the second call's updates were ignored")
	}
}
