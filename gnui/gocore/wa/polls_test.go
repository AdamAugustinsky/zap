package wa

import (
	"slices"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
)

func pollState(t *testing.T, evs []abi.Event) abi.PollState {
	t.Helper()
	m := one(t, evs, abi.EventTagMessageUpdated).MessageUpdated.Message
	if m.Content.Tag != abi.ContentTagPoll {
		t.Fatalf("not a poll: %+v", m.Content)
	}
	return m.Content.Poll.State
}

func TestPolls(t *testing.T) {
	h := linked(t)
	draft := abi.PollDraft{Question: []byte("Lunch?"), Options: [][]byte{[]byte("Yes"), []byte("No")}}
	h.send(abi.Command{Tag: abi.CommandTagCreatePoll, CreatePoll: abi.CommandCreatePoll{Chat: chatID(peer), Draft: draft}})
	evs := h.take()
	if p := one(t, evs, abi.EventTagPollCreated).PollCreated; p.HasError || string(p.Chat) != peer.String() {
		t.Fatalf("created: %+v", p)
	}
	sent := h.f.lastSent()
	if c := sent.msg.GetPollCreationMessage(); c.GetName() != "Lunch?" || len(c.GetOptions()) != 2 || c.GetSelectableOptionsCount() != 1 || len(sent.msg.GetMessageContextInfo().GetMessageSecret()) != 32 {
		t.Fatalf("sent: %v", sent.msg)
	}
	poll := sent.id
	if ms := messagesOf(evs); len(ms) != 1 || string(ms[0].ID) != poll || ms[0].Status != abi.DeliverySent {
		t.Fatalf("filed: %+v", ms)
	}

	h.send(abi.Command{Tag: abi.CommandTagCreatePoll, CreatePoll: abi.CommandCreatePoll{Chat: chatID(peer), Draft: abi.PollDraft{Question: []byte("Q"), Options: [][]byte{[]byte("only")}}}})
	if p := one(t, h.take(), abi.EventTagPollCreated).PollCreated; !p.HasError || string(p.Error) != "Add 2–12 answers, each with 1–100 characters." {
		t.Fatalf("invalid: %+v", p)
	}

	h.send(abi.Command{Tag: abi.CommandTagVotePoll, VotePoll: abi.CommandVotePoll{Chat: chatID(peer), Message: []byte(poll), Choices: []uint64{0}}})
	evs = h.take()
	if v := one(t, evs, abi.EventTagPollVoted).PollVoted; v.HasError || string(v.Message) != poll {
		t.Fatalf("voted: %+v", v)
	}
	if s := pollState(t, evs); !slices.Equal(s.Counts, []uint64{1, 0}) || !slices.Equal(s.Selected, []uint64{0}) || h.f.called("BuildPollVote") != 1 {
		t.Fatalf("counted: %+v", s)
	}

	// A vote that arrives: decrypted, then counted.
	h.f.more().vote = &waE2E.PollVoteMessage{SelectedOptions: whatsmeow.HashPollOptions([]string{"No"})}
	vote := &waE2E.Message{PollUpdateMessage: &waE2E.PollUpdateMessage{PollCreationMessageKey: &waCommon.MessageKey{ID: proto.String(poll), FromMe: proto.Bool(true)}, Vote: &waE2E.PollEncValue{}}}
	h.inject(message(peer, peer, "V1", t0, vote))
	if s := pollState(t, h.take()); !slices.Equal(s.Counts, []uint64{1, 1}) {
		t.Fatalf("their vote: %+v", s)
	}

	h.send(abi.Command{Tag: abi.CommandTagRefreshPoll, RefreshPoll: abi.CommandRefreshPoll{Chat: chatID(peer), Message: []byte(poll)}})
	pollState(t, h.take())

	// A choice that is not one.
	h.send(abi.Command{Tag: abi.CommandTagVotePoll, VotePoll: abi.CommandVotePoll{Chat: chatID(peer), Message: []byte(poll), Choices: []uint64{5}}})
	if v := one(t, h.take(), abi.EventTagPollVoted).PollVoted; !v.HasError {
		t.Fatalf("bad choice: %+v", v)
	}
}

func TestReplyInteractive(t *testing.T) {
	h := linked(t)
	buttons := &waE2E.Message{ButtonsMessage: &waE2E.ButtonsMessage{ContentText: proto.String("Pick"), Buttons: []*waE2E.ButtonsMessage_Button{
		{ButtonID: proto.String("b-yes"), Type: waE2E.ButtonsMessage_Button_RESPONSE.Enum(), ButtonText: &waE2E.ButtonsMessage_Button_ButtonText{DisplayText: proto.String("Yes")}},
	}}}
	h.receive(peer, peer, "B1", t0, buttons)

	h.send(abi.Command{Tag: abi.CommandTagReplyInteractive, ReplyInteractive: abi.CommandReplyInteractive{Chat: chatID(peer), Message: []byte("B1")}})
	evs := h.take()
	states := only(evs, abi.EventTagInteractiveReplyState)
	if len(states) != 2 || !states[0].InteractiveReplyState.Pending || states[1].InteractiveReplyState.Pending {
		t.Fatalf("states: %s", tags(evs))
	}
	r := h.f.lastSent().msg.GetButtonsResponseMessage()
	if r.GetSelectedButtonID() != "b-yes" || r.GetSelectedDisplayText() != "Yes" || r.GetContextInfo().GetStanzaID() != "B1" {
		t.Fatalf("reply: %v", r)
	}

	h.send(abi.Command{Tag: abi.CommandTagReplyInteractive, ReplyInteractive: abi.CommandReplyInteractive{Chat: chatID(peer), Message: []byte("M1")}})
	if evs := h.take(); lastError(t, evs) != "This message cannot be answered here" {
		t.Fatalf("not interactive: %s", tags(evs))
	}
}
