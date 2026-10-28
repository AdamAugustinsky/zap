package kinds_test

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
	"zapfast/ui/conversation/kinds"
)

func poll(id string, selectable uint64, selected, counts []uint64, voters uint64, canVote, complete bool) abi.Message {
	m := msg(id, 10, false, abi.Content{Tag: abi.ContentTagPoll})
	m.Content.Poll = abi.ContentPoll{Question: []byte("Lunch?"), Options: [][]byte{[]byte("Pizza"), []byte("Pasta"), []byte("Salad")},
		State: abi.PollState{Selectable: selectable, Selected: selected, Counts: counts, Voters: voters, CanVote: canVote, HistoryComplete: complete}}
	return m
}

func TestPollShowsEveryAnswerWithItsCountAndYourChoice(t *testing.T) {
	r := newRig(t, poll("p", 1, []uint64{1}, []uint64{2, 3, 0}, 5, true, true))
	for _, name := range []string{"Select one answer", "5 voters", "2", "3", "0"} {
		if !hasText(r, name) {
			t.Errorf("no %q", name)
		}
	}
	radios := r.names(ui.RoleRadio)
	if len(radios) != 3 || radios[0] != "Pizza" || radios[1] != "Pasta" {
		t.Fatalf("answers %v", radios)
	}
	if n, ok := r.Find(ui.RoleRadio, "Pasta"); !ok || n.State&ui.SemChecked == 0 {
		t.Error("your choice is not checked")
	}
	if n, _ := r.Find(ui.RoleRadio, "Pizza"); n.State&ui.SemChecked != 0 {
		t.Error("an answer you did not choose is checked")
	}
	if got := r.names(ui.RoleButton); len(got) != 1 || got[0] != "Show votes" {
		t.Errorf("buttons %v", got)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Show votes"), widgettest.Idle(2))
	if r.s.Dialog.Kind != state.DialogPollResults || r.s.Dialog.IDs.Len() != 1 || string(r.s.Dialog.IDs.At(0)) != "p" {
		t.Errorf("dialog %+v", r.s.Dialog)
	}
}

// A click votes, a second click takes the vote back; a second poll vote
// waits for the answer to the first.
func TestClickingAnAnswerVotesAndClickingAgainTakesItBack(t *testing.T) {
	r := newRig(t, poll("p", 1, nil, []uint64{0, 0, 0}, 0, true, true))
	r.Play(widgettest.Click(ui.RoleRadio, "Pizza"), widgettest.Idle(2))
	got := commands(r, abi.CommandTagVotePoll)
	if len(got) != 1 || string(got[0].VotePoll.Message) != "p" || len(got[0].VotePoll.Choices) != 1 || got[0].VotePoll.Choices[0] != 0 {
		t.Fatalf("votes %+v", got)
	}
	if !hasText(r, "Sending vote…") {
		t.Error("no Sending vote… while it is on its way")
	}
	r.Play(widgettest.Click(ui.RoleRadio, "Pasta"), widgettest.Idle(2))
	if n := len(commands(r, abi.CommandTagVotePoll)); n != 1 {
		t.Errorf("%d votes while one is in flight", n)
	}
	// the core answers: the vote is in, the message shows it
	r.f.Emit(ev(abi.EventTagPollVoted, func(e *abi.Event) { e.PollVoted.Chat, e.PollVoted.Message = []byte(chatID), []byte("p") }))
	r.update(poll("p", 1, []uint64{0}, []uint64{1, 0, 0}, 1, true, true))
	if hasText(r, "Sending vote…") {
		t.Error("still sending after the answer")
	}
	r.Play(widgettest.Click(ui.RoleRadio, "Pizza"), widgettest.Idle(2))
	got = commands(r, abi.CommandTagVotePoll)
	if len(got) != 2 || len(got[1].VotePoll.Choices) != 0 {
		t.Fatalf("taking the vote back: %+v", got)
	}
}

func TestMultipleAnswersAddUpToTheLimit(t *testing.T) {
	r := newRig(t, poll("p", 2, []uint64{0, 2}, []uint64{1, 0, 1}, 1, true, true))
	if got := r.names(ui.RoleCheckbox); len(got) != 3 {
		t.Fatalf("answers %v", got)
	}
	if !hasText(r, "Select answers") && !hasText(r, "Select one answer") {
		t.Error("no selection hint")
	}
	r.Play(widgettest.Click(ui.RoleCheckbox, "Pasta"), widgettest.Idle(2))
	if n := len(commands(r, abi.CommandTagVotePoll)); n != 0 {
		t.Error("a third answer was sent past a limit of two")
	}
	r.Play(widgettest.Click(ui.RoleCheckbox, "Pizza"), widgettest.Idle(2))
	got := commands(r, abi.CommandTagVotePoll)
	if len(got) != 1 || len(got[0].VotePoll.Choices) != 1 || got[0].VotePoll.Choices[0] != 2 {
		t.Errorf("votes %+v", got)
	}
}

func TestVotingIsBlockedWithAReason(t *testing.T) {
	r := newRig(t, poll("nokey", 1, nil, []uint64{0, 0, 0}, 0, false, true))
	if !hasText(r, "Voting key unavailable · use your phone") {
		t.Error("no key reason")
	}
	r.Play(widgettest.Click(ui.RoleRadio, "Pizza"), widgettest.Idle(2))
	if n := len(commands(r, abi.CommandTagVotePoll)); n != 0 {
		t.Error("voted without a key")
	}
	// offline
	r.f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagDisconnected }))
	r.update(poll("off", 1, nil, []uint64{0, 0, 0}, 0, true, true))
	r.Frames(2)
	if !hasText(r, "Reconnect to vote") {
		t.Error("no reconnect reason")
	}
}

func TestPollSaysWhenEarlierVotesAreMissingAndAsksForThem(t *testing.T) {
	m := poll("p", 1, nil, []uint64{0, 0, 0}, 0, true, false)
	m.Content.Poll.State.RefreshNeeded = true
	r := newRig(t, m)
	if !hasText(r, "Earlier votes have not been loaded yet") {
		t.Error("no history notice")
	}
	r.Frames(4)
	got := commands(r, abi.CommandTagRefreshPoll)
	if len(got) != 1 || string(got[0].RefreshPoll.Message) != "p" {
		t.Errorf("refresh commands %+v", got)
	}
	m.Content.Poll.State.Refreshing = true
	m.Content.Poll.State.RefreshNeeded = false
	r.update(m)
	if !hasText(r, "Loading earlier votes from your phone…") {
		t.Error("no loading notice")
	}
	m.Content.Poll.State.Refreshing, m.Content.Poll.State.RefreshFailed = false, true
	r.update(m)
	if !hasText(r, "Waiting for your phone · earlier votes may be missing") {
		t.Error("no failure notice")
	}
}

func TestPollWithNoVotersHasNothingToShow(t *testing.T) {
	r := newRig(t, poll("p", 1, nil, []uint64{0, 0, 0}, 0, true, true))
	if !hasText(r, "0 voters") {
		t.Error("no voter count")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Show votes"), widgettest.Idle(2))
	if r.s.Dialog.Open() {
		t.Error("Show votes opened a dialog for a poll with no votes")
	}
	if !kinds.Has(r.view("p")) || kinds.Caption(r.view("p")) != kinds.Above {
		t.Error("a poll's question goes above its block")
	}
}
