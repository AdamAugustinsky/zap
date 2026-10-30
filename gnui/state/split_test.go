package state_test

import (
	"slices"
	"testing"

	"zapfast/abi"
	"zapfast/model"
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

// The split puts chats beside the open one, each in use as it comes and
// read; the pane in use is the open chat, and putting another in use trades
// their per-chat state (a reply set in one stays its own) while the panes
// keep their places. How many fit is the window's (MaxPanes); a chat is in
// one pane at a time.
func TestTheSplitKeepsEachPanesOwnState(t *testing.T) {
	r, a := twoChats(t)
	const kath = "5@s.whatsapp.net"
	r.cores[0].Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(kath), Name: []byte("Katherine"), LastActivity: 3}
	}))
	r.pump()
	adaRef, graceRef, kathRef := a.Model.Chats.Find([]byte(ada)), a.Model.Chats.Find([]byte(grace)), a.Model.Chats.Find([]byte(kath))
	if !a.OpenSplit(graceRef) || a.Open != graceRef || a.SplitOn() {
		t.Fatalf("with nothing open the split opens the chat: open %v panes %v", a.Open, a.Panes())
	}
	a.OpenChat(adaRef)
	if a.OpenSplit(adaRef) {
		t.Error("the open chat went beside itself")
	}
	if !a.OpenSplit(graceRef) || a.Open != graceRef || !slices.Equal(a.Panes(), []model.ChatRef{adaRef, graceRef}) {
		t.Fatalf("beside: open %v panes %v", a.Open, a.Panes())
	}

	// each pane's reply is its own
	r.s.Panes.Reply.Add([]byte("g1"))
	a.UsePane(adaRef)
	if a.Open != adaRef || r.s.Panes.Reply.Len() != 0 {
		t.Fatalf("Ada in use: open %v, reply %d", a.Open, r.s.Panes.Reply.Len())
	}
	if !slices.Equal(a.Panes(), []model.ChatRef{adaRef, graceRef}) {
		t.Errorf("the panes moved when one was put in use: %v", a.Panes())
	}
	a.SideBegin(graceRef)
	if a.Open != graceRef || !r.s.Side || string(r.s.Panes.Reply.At(0)) != "g1" {
		t.Errorf("the other pane building: open %v side %v", a.Open, r.s.Side)
	}
	a.SideEnd(graceRef)
	if a.Open != adaRef || r.s.Side {
		t.Error("SideEnd did not trade back")
	}
	a.OpenChat(graceRef) // opening a pane's chat puts it in use
	if a.Open != graceRef || string(r.s.Panes.Reply.At(0)) != "g1" {
		t.Fatalf("opened from its tab: open %v", a.Open)
	}
	if a.PrevOpen() != adaRef {
		t.Errorf("the chat before: %v", a.PrevOpen())
	}
	a.MovePane(1, 0)
	if !slices.Equal(a.Panes(), []model.ChatRef{graceRef, adaRef}) {
		t.Errorf("moved: %v", a.Panes())
	}

	// a third pane where there is room, none where there is not
	r.s.MaxPanes = 2
	if a.OpenSplit(kathRef) {
		t.Error("a third pane in a window with room for two")
	}
	r.s.MaxPanes = 3
	if !a.OpenSplit(kathRef) || len(a.Panes()) != 3 || a.Open != kathRef {
		t.Fatalf("a third pane: open %v panes %v", a.Open, a.Panes())
	}
	r.s.SetShare(3, 0, 0.6)
	if sh := r.s.Shares(3); sh[0] < 0.59 || sh[0] > 0.61 {
		t.Errorf("shares %v, want the first where it was left", sh)
	}
	a.OpenChat(0) // the chat in use closed: its pane goes, the others stay
	if a.Open == kathRef || len(a.Panes()) != 2 {
		t.Fatalf("closed: open %v panes %v", a.Open, a.Panes())
	}
	a.Expand(adaRef) // Ada fills the window
	if a.Open != adaRef || a.SplitOn() {
		t.Fatalf("expanded: open %v panes %v", a.Open, a.Panes())
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

// A space's colour is kept with its folder, or with its account.
func TestASpacesColourIsKept(t *testing.T) {
	r, a := twoChats(t)
	f := a.FolderByID(a.NewFolder("Work", ""))
	sp := state.Space{Acct: a.Index, Folder: a.Settings.Folders[f].ID}
	a.SetTint(sp, "teal")
	a.SetTint(state.Space{Acct: a.Index}, "orange")
	if a.TintOf(sp) != "teal" || a.TintOf(state.Space{Acct: a.Index}) != "orange" || r.s.SpaceTint() != "orange" {
		t.Fatalf("tints: folder %q, account %q, on screen %q", a.TintOf(sp), a.TintOf(state.Space{Acct: a.Index}), r.s.SpaceTint())
	}
	// a new colour keeps the mix and the strength chosen
	a.SetTheme(sp, state.SpaceTheme{Tint: "teal", Mix: "analogous", Level: 0.9})
	a.SetTint(sp, "pink")
	if got := a.ThemeOf(sp); got != (state.SpaceTheme{Tint: "pink", Mix: "analogous", Level: 0.9}) {
		t.Fatalf("after a new colour the theme is %+v", got)
	}
}
