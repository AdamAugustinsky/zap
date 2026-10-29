package state_test

import (
	"testing"

	"zapfast/abi"
	"zapfast/state"
)

func twoChats(t *testing.T) (*rig, *state.Account) {
	r := newRig(t, 1)
	r.cores[0].Emit(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 2, Last: true, Chats: []abi.Chat{
			{ID: []byte(ada), Name: []byte("Ada"), LastActivity: 5},
			{ID: []byte(grace), Name: []byte("Grace"), LastActivity: 4, Unread: 2},
		}}
	}))
	r.pump()
	return r, r.s.Accounts[0]
}

// The split puts a second chat beside the open one, in use and read as
// it comes in. The pane in use is the open chat: putting the other in use
// trades their per-chat state (a reply set in one stays its own), and the
// panes keep their places until they are moved. A chat is in one pane at a
// time.
func TestTheSplitKeepsEachPanesOwnState(t *testing.T) {
	r, a := twoChats(t)
	adaRef, graceRef := a.Model.Chats.Find([]byte(ada)), a.Model.Chats.Find([]byte(grace))
	if !a.OpenSplit(graceRef) || a.Open != graceRef || a.Split != 0 {
		t.Fatalf("with nothing open the split opens the chat: open %v split %v", a.Open, a.Split)
	}
	a.OpenChat(adaRef)
	if a.OpenSplit(adaRef) {
		t.Error("the open chat went beside itself")
	}
	reads := r.sent(0, abi.CommandTagMarkRead)
	if !a.OpenSplit(graceRef) || a.Open != graceRef || a.Split != adaRef {
		t.Fatalf("beside: open %v split %v", a.Open, a.Split)
	}
	if first, second := a.Panes(); first != adaRef || second != graceRef {
		t.Fatalf("places: %v %v", first, second)
	}
	if r.sent(0, abi.CommandTagMarkRead) < reads {
		t.Error("reads went missing")
	}

	// each pane's reply is its own
	r.s.Panes.Reply.Add([]byte("g1"))
	a.UseSplit()
	if a.Open != adaRef || r.s.Panes.Reply.Len() != 0 {
		t.Fatalf("Ada in use: open %v, reply %d", a.Open, r.s.Panes.Reply.Len())
	}
	if first, second := a.Panes(); first != adaRef || second != graceRef {
		t.Errorf("the panes moved when one was put in use: %v %v", first, second)
	}
	a.SideBegin()
	if a.Open != graceRef || !r.s.Side || string(r.s.Panes.Reply.At(0)) != "g1" {
		t.Errorf("the other pane building: open %v side %v", a.Open, r.s.Side)
	}
	a.SideEnd()
	if a.Open != adaRef || r.s.Side {
		t.Error("SideEnd did not trade back")
	}
	a.OpenChat(graceRef) // opening the other pane's chat puts it in use
	if a.Open != graceRef || a.Split != adaRef || string(r.s.Panes.Reply.At(0)) != "g1" {
		t.Fatalf("opened from its tab: open %v split %v", a.Open, a.Split)
	}
	a.SwapSplit()
	if first, _ := a.Panes(); first != graceRef {
		t.Error("the panes did not trade places")
	}
	if a.PrevOpen() != adaRef {
		t.Errorf("the chat before: %v", a.PrevOpen())
	}

	a.Expand(adaRef) // Ada fills the window
	if a.Open != adaRef || a.Split != 0 {
		t.Fatalf("expanded: open %v split %v", a.Open, a.Split)
	}
	a.OpenSplit(graceRef)
	a.OpenChat(0) // the chat in use closed: the other has the window
	if a.Open != adaRef || a.Split != 0 {
		t.Fatalf("closed: open %v split %v", a.Open, a.Split)
	}
	if r.s.SplitShare = 0.9; r.s.Share() != 0.75 {
		t.Errorf("share %v, want it kept to 0.75", r.s.Share())
	}
}

// A floating chat opens its window on its own, shrinks to its icon and
// closes; the app locked refuses it.
func TestTheFloatingChat(t *testing.T) {
	r, a := twoChats(t)
	s := r.s
	ref := a.Model.Chats.Find([]byte(grace))
	if !s.FloatChat(a, ref) || !s.Pip.Open() || !s.Win.Want[state.WinPip] || !s.Win.Alone[state.WinPip] {
		t.Fatalf("pip %+v want %v alone %v", s.Pip, s.Win.Want[state.WinPip], s.Win.Alone[state.WinPip])
	}
	s.Pip.Focused = true
	s.SetPipMini(true)
	if !s.Pip.Mini || s.Pip.Focused {
		t.Errorf("shrunk: %+v", s.Pip)
	}
	s.ClosePip()
	if s.Pip.Open() || s.Win.Want[state.WinPip] {
		t.Errorf("closed: %+v", s.Pip)
	}
	s.Lock.Locked = true
	if s.FloatChat(a, ref) {
		t.Error("a chat floated behind the lock")
	}
}
