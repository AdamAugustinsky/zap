package calls

import (
	"slices"
	"testing"
	"time"

	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// The call and the call histories belong to their account by its Serial:
// with accounts one, two and three, two on a call, the real removal of one
// moves two to index 0 and three to index 1. The call's commands still reach
// two's core, never three's; three's history is its own, not two's.
func TestRemovingAnEarlierAccountKeepsTheCallWithItsAccount(t *testing.T) {
	w := newWorld(t)
	f2, f3 := fake.New(nil), fake.New(nil)
	two := w.s.AddAccount("2", nil, f2)
	three := w.s.AddAccount("3", nil, f3)
	if err := f2.Emit(ev(abi.EventTagCall, func(e *abi.Event) { incoming(5, abi.CallPhaseActive)(&e.Call.Update) }),
		ev(abi.EventTagCallLog, func(e *abi.Event) {
			e.CallLog = abi.EventCallLog{Batch: 3, Last: true, Messages: []abi.Message{callMsg(grace, "two-call", true, w.at(time.Hour), abi.CallStatusAnswered)}}
		})); err != nil {
		t.Fatal(err)
	}
	w.s.Pump(0)
	if !w.h.liveCall() || len(w.h.acct(two).log.e) != 1 {
		t.Fatalf("account two has no call or history: live %v", w.h.liveCall())
	}
	w.s.RemoveAccount(w.a.Index)
	w.emit(&abi.Event{Tag: abi.EventTagAccountRemoved})
	if len(w.s.Accounts) != 2 || two.Index != 0 || three.Index != 1 {
		t.Fatalf("account one was not removed: %d accounts", len(w.s.Accounts))
	}
	w.h.tick(w.now)
	if !w.h.liveCall() || w.h.account() != two {
		t.Fatalf("the call left its account: live %v", w.h.liveCall())
	}
	w.h.setMuted(true)
	w.h.hangup()
	if tags := f3.SentTags(); slices.Contains(tags, abi.CommandTagHangupCall) || slices.Contains(tags, abi.CommandTagSetCallMuted) {
		t.Errorf("account three's core got the call's commands: %v", tags)
	}
	if tags := f2.SentTags(); !slices.Contains(tags, abi.CommandTagHangupCall) || !slices.Contains(tags, abi.CommandTagSetCallMuted) {
		t.Errorf("account two's core did not get the call's commands: %v", tags)
	}
	if n := len(w.h.acct(three).log.e); n != 0 {
		t.Errorf("account three shows %d calls of another account", n)
	}
	for _, ac := range w.h.accts {
		if ac.serial != two.Serial && ac.serial != three.Serial {
			t.Errorf("a removed account's calls are kept (serial %d)", ac.serial)
		}
	}
}

// The call of an account that goes, goes with it (zf:src/app.rs:1769).
func TestRemovingTheCallsAccountEndsItsCall(t *testing.T) {
	w := newWorld(t)
	f2 := fake.New(nil)
	two := w.s.AddAccount("2", nil, f2)
	if err := f2.Emit(ev(abi.EventTagCall, func(e *abi.Event) { incoming(5, abi.CallPhaseActive)(&e.Call.Update) })); err != nil {
		t.Fatal(err)
	}
	w.s.Pump(0)
	w.s.RemoveAccount(two.Index)
	f2.Emit(&abi.Event{Tag: abi.EventTagAccountRemoved})
	w.s.Pump(0)
	w.h.tick(w.now)
	if w.h.call.on {
		t.Error("the removed account's call stayed on screen")
	}
}

// Locked → Calls → an ordinary chat: opening it closes the locked folder,
// and the history drops the locked chat's calls at once, names, times and
// call-back buttons.
func TestTheCallHistoryHidesLockedCallsOnceTheFolderCloses(t *testing.T) {
	w := newWorld(t)
	w.emit(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 9, Last: true, Chats: []abi.Chat{
			{ID: []byte(ada), Name: []byte("Ada Lovelace"), Kind: abi.ChatKindDirect, Locked: true, LastActivity: 30},
			{ID: []byte(grace), Name: []byte("Grace Hopper"), Kind: abi.ChatKindDirect, LastActivity: 20},
		}}
	}))
	w.a.Mode = state.ListLocked // the locked folder is open
	r := w.page()
	w.history(callMsg(ada, "a1", false, w.at(time.Hour), abi.CallStatusAnswered),
		callMsg(grace, "g1", true, w.at(2*time.Hour), abi.CallStatusAnswered))
	r.Frames(2)
	has(t, r.Rig, ui.RoleListItem, "Ada Lovelace, Voice call, 14:30")
	w.a.OpenChat(w.a.Model.Chats.Find([]byte(grace)))
	if w.a.Mode != state.ListChats || w.s.Page != state.PageCalls {
		t.Fatalf("mode %v, page %v", w.a.Mode, w.s.Page)
	}
	r.Frames(2)
	hasNot(t, r.Rig, ui.RoleListItem, "Ada Lovelace, Voice call, 14:30")
	hasNot(t, r.Rig, ui.RoleButton, "Voice call Ada Lovelace")
	has(t, r.Rig, ui.RoleListItem, "Grace Hopper, Voice call, 13:30")
}
