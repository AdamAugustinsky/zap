package calls

import (
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

// history gives the account a call log (CallLog, in two batches).
func (w *world) history(msgs ...abi.Message) {
	w.t.Helper()
	half := len(msgs) / 2
	w.emit(ev(abi.EventTagCallLog, func(e *abi.Event) {
		e.CallLog = abi.EventCallLog{Batch: 40, Messages: msgs[:half]}
	}), ev(abi.EventTagCallLog, func(e *abi.Event) {
		e.CallLog = abi.EventCallLog{Batch: 40, Last: true, Messages: msgs[half:]}
	}))
}

// at is a time on the world's day (15:30 UTC) minus ago.
func (w *world) at(ago time.Duration) int64 { return w.now.Add(-ago).Unix() }

// CA-021, CA-026: the Calls page asks for the history as it opens, says it
// is loading, then lists the calls newest first, folding runs with a count;
// each row says how it went and when, and calls back the same way.
func TestTheCallHistoryListsAndCallsBack(t *testing.T) {
	w := newWorld(t)
	r := w.page()
	if w.count(abi.CommandTagLoadCallLog) != 1 {
		t.Errorf("opening the page sent %v", w.sent())
	}
	has(t, r.Rig, ui.RoleGroup, "Loading…")
	missed := callMsg(ada, "m1", false, w.at(time.Hour), abi.CallStatusMissed)
	missed2 := callMsg(ada, "m2", false, w.at(2*time.Hour), abi.CallStatusMissed)
	video := callMsg(grace, "v1", true, w.at(26*time.Hour), abi.CallStatusAnswered)
	video.Content.Call.Video, video.Content.Call.Duration = true, 754
	link := callMsg(LinkChat, "l1", true, w.at(10*24*time.Hour), abi.CallStatusAnswered)
	w.history(video, missed, missed2, link)
	r.Frames(2)
	shot(t, r.Rig, "calls")
	has(t, r.Rig, ui.RoleListItem, "Ada Lovelace (2), Missed voice call, 14:30")
	has(t, r.Rig, ui.RoleListItem, "Grace Hopper, Video call · 12 min 34 s, Yesterday")
	has(t, r.Rig, ui.RoleListItem, "Call link, Voice call, 10 Oct 2026")
	hasNot(t, r.Rig, ui.RoleButton, "Voice call Call link") // nobody to call back
	r.Play(widgettest.Click(ui.RoleButton, "Video call Grace Hopper"), widgettest.Idle(2))
	c, ok := w.last(abi.CommandTagStartCall)
	if !ok || string(c.StartCall.Chat) != grace || !c.StartCall.Video {
		t.Errorf("calling back sent %v", w.sent())
	}
	w.h.hangup()
	r.Play(widgettest.Click(ui.RoleButton, "Voice call Ada Lovelace"), widgettest.Idle(2))
	if c, _ := w.last(abi.CommandTagStartCall); string(c.StartCall.Chat) != ada || c.StartCall.Video {
		t.Errorf("calling Ada back sent %+v", c.StartCall)
	}
}

// CA-023: clicking a row opens that person's chat beside the list at the
// call and shows it picked; a call from the phone's log with nobody we chat
// with makes the chat; a call link's row opens nothing.
func TestARowOpensItsChat(t *testing.T) {
	w := newWorld(t)
	r := w.page()
	w.history(callMsg(grace, "g1", true, w.at(time.Hour), abi.CallStatusAnswered),
		callMsg(LinkChat, "l1", true, w.at(2*time.Hour), abi.CallStatusAnswered),
		callMsg(alan, "t1", false, w.at(3*time.Hour), abi.CallStatusAnswered))
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleListItem, "Grace Hopper, Voice call, 14:30"), widgettest.Idle(2))
	if ch, ok := w.a.OpenChatView(); !ok || string(ch.ID()) != grace {
		t.Fatalf("open chat %v", w.a.Open)
	}
	if c, ok := w.last(abi.CommandTagLoadChat); !ok || string(c.LoadChat.Chat) != grace {
		t.Errorf("sent %v", w.sent())
	}
	if j := &w.s.Panes.Jump; j.Chat != w.a.Open || j.Msg.Len() != 1 || string(j.Msg.At(0)) != "g1" {
		t.Errorf("the chat does not open at the call: %+v", j)
	}
	n := r.Must(ui.RoleListItem, "Grace Hopper, Voice call, 14:30")
	if n.State&ui.SemSelected == 0 {
		t.Error("the picked row is not selected")
	}
	r.Play(widgettest.Click(ui.RoleListItem, "Call link, Voice call, 13:30"), widgettest.Idle(2))
	if ch, _ := w.a.OpenChatView(); string(ch.ID()) != grace {
		t.Error("the call link's row opened something")
	}
	if w.s.Page != state.PageCalls {
		t.Error("the page changed")
	}
	r.Play(widgettest.Click(ui.RoleListItem, "Alan Turing, Voice call, 12:30"), widgettest.Idle(2))
	if c, ok := w.last(abi.CommandTagEnsureChat); !ok || string(c.EnsureChat.Chat) != alan {
		t.Errorf("a call with nobody we chat with sent %v", w.sent())
	}
}

// CA-022, CA-026: the chips pick All or Missed (remembered per number), the
// search looks by name or number within the chip, and the empty states say
// why.
func TestTheChipsAndTheSearch(t *testing.T) {
	w := newWorld(t)
	r := w.page()
	w.history(callMsg(grace, "g1", true, w.at(time.Hour), abi.CallStatusAnswered),
		callMsg(ada, "a1", true, w.at(2*time.Hour), abi.CallStatusNoAnswer))
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleCheckbox, "Missed"), widgettest.Idle(2))
	if string(w.a.Settings.CallFilter) != "missed" {
		t.Errorf("the chip was not remembered: %q", w.a.Settings.CallFilter)
	}
	has(t, r.Rig, ui.RoleGroup, "No missed calls")
	r.Play(widgettest.Click(ui.RoleCheckbox, "All"), widgettest.Idle(2))
	has(t, r.Rig, ui.RoleListItem, "Ada Lovelace, Voice call · No answer, 13:30")
	r.Play(widgettest.Click(ui.RoleSearchField, "Search name or number"), widgettest.Type("grace"), widgettest.Idle(3))
	has(t, r.Rig, ui.RoleListItem, "Grace Hopper, Voice call, 14:30")
	hasNot(t, r.Rig, ui.RoleListItem, "Ada Lovelace, Voice call · No answer, 13:30")
	r.Play(widgettest.Type("x"), widgettest.Idle(3))
	has(t, r.Rig, ui.RoleLabel, "No matching calls")

	w = newWorld(t)
	w.s.Accounts[0].Settings.CallFilter = []byte("missed")
	r = w.page()
	w.history()
	r.Frames(2)
	has(t, r.Rig, ui.RoleGroup, "No calls yet")
}

// CA-024: New call lists the most-called people, then every contact, with
// voice and video buttons; a call from it closes it; the search finds
// nobody with "No matching contacts"; Escape goes back.
func TestTheNewCallList(t *testing.T) {
	w := newWorld(t)
	r := w.page()
	w.history(callMsg(grace, "g1", true, w.at(time.Hour), abi.CallStatusAnswered),
		callMsg(grace, "g2", false, w.at(2*time.Hour), abi.CallStatusAnswered),
		callMsg(ada, "a1", true, w.at(3*time.Hour), abi.CallStatusAnswered))
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleButton, "New call"), widgettest.Idle(2))
	shot(t, r.Rig, "new-call")
	has(t, r.Rig, ui.RoleLabel, "Frequently contacted")
	has(t, r.Rig, ui.RoleLabel, "All contacts")
	for _, n := range []string{"Grace Hopper", "Ada Lovelace", "Alan Turing"} {
		has(t, r.Rig, ui.RoleListItem, n)
	}
	hasNot(t, r.Rig, ui.RoleListItem, "Engines") // groups are not called from here
	r.Play(widgettest.Click(ui.RoleListItem, "Alan Turing"), widgettest.Idle(2))
	if w.count(abi.CommandTagStartCall) != 0 {
		t.Error("a row alone started a call")
	}
	r.Play(widgettest.Click(ui.RoleSearchField, "Search name or number"), widgettest.Type("zzz"), widgettest.Idle(3))
	has(t, r.Rig, ui.RoleLabel, "No matching contacts")
	// The first Escape clears the search (as a search field does), the next
	// leaves the list.
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2), widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	has(t, r.Rig, ui.RoleButton, "New call")
	if w.s.Page != state.PageCalls {
		t.Error("Escape left the page from the new-call list")
	}
	r.Play(widgettest.Click(ui.RoleButton, "New call"), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "Video call Alan Turing"), widgettest.Idle(2))
	if c, ok := w.last(abi.CommandTagStartCall); !ok || string(c.StartCall.Chat) != alan || !c.StartCall.Video {
		t.Errorf("sent %v", w.sent())
	}
	has(t, r.Rig, ui.RoleButton, "New call") // back on the history
}

// CA-007: a call put aside waits in a bar along the foot of the window, on
// every page, with the way back and Hang up; a call that ends meanwhile
// comes back to say how.
func TestTheBarOfACallPutAside(t *testing.T) {
	w := newWorld(t)
	r := w.page()
	w.history()
	w.update(incoming(1, abi.CallPhaseActive))
	w.h.leave()
	r.Frames(2)
	shot(t, r.Rig, "call-bar")
	has(t, r.Rig, ui.RoleGroup, "Ada Lovelace")
	r.Play(widgettest.Click(ui.RoleButton, "Return to the call"), widgettest.Idle(2))
	if !w.h.showing() {
		t.Error("Return to the call did not bring the call back")
	}
	hasNot(t, r.Rig, ui.RoleButton, "Return to the call")

	w.h.leave()
	w.s.Page = state.PageChats
	w.s.Touch()
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleButton, "Hang up"), widgettest.Idle(2))
	if w.count(abi.CommandTagHangupCall) != 1 || w.h.call.on {
		t.Errorf("Hang up sent %v", w.sent())
	}
	hasNot(t, r.Rig, ui.RoleGroup, "Ada Lovelace")

	w.update(incoming(2, abi.CallPhaseActive))
	w.h.leave()
	r.Frames(2)
	w.update(func(u *abi.CallUpdate) {
		incoming(2, abi.CallPhaseEnded)(u)
		u.Outcome, u.HasOutcome = abi.CallStatusAnswered, true
	})
	r.Frames(2)
	if !w.h.showing() {
		t.Error("the call that ended put aside does not say how")
	}
	hasNot(t, r.Rig, ui.RoleButton, "Return to the call")
}

// CA-023: with no chat open the Calls page's content offers Start call,
// which opens the new-call list.
func TestStartCallOpensTheNewCallList(t *testing.T) {
	w := newWorld(t)
	r := w.page()
	w.history()
	r.Frames(2)
	shot(t, r.Rig, "start-call")
	has(t, r.Rig, ui.RoleLabel, "Call one person by voice or video, or choose a call in the list to see it.")
	r.Play(widgettest.Click(ui.RoleButton, "Start call"), widgettest.Idle(2))
	has(t, r.Rig, ui.RoleButton, "Back to the calls")
	has(t, r.Rig, ui.RoleListItem, "Alan Turing")
}

// CA-027: switching numbers keeps the Calls tab, on the other number's
// calls; reopening the page asks again.
func TestSwitchingNumbersKeepsTheCallsTab(t *testing.T) {
	w := newWorld(t)
	r := w.page()
	f2 := newWorld(t).f
	w.s.AddAccount("2", nil, f2)
	f2.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }))
	w.s.Pump(0)
	r.Frames(1)
	w.s.SwitchAccount(1)
	r.Frames(2)
	if w.s.Page != state.PageCalls || !containsTag(f2.SentTags(), abi.CommandTagLoadCallLog) {
		t.Errorf("page %v, second number sent %v", w.s.Page, f2.SentTags())
	}
	w.s.SwitchAccount(0)
	r.Frames(1)
	w.s.Page = state.PageChats
	r.Frames(2)
	w.s.Page = state.PageCalls
	r.Frames(2)
	if n := w.count(abi.CommandTagLoadCallLog); n < 3 {
		t.Errorf("reopening the page asked %d times in all", n)
	}
}

// A call filed while the Calls page is open joins its list.
func TestACallFiledWhileOpenJoinsTheList(t *testing.T) {
	w := newWorld(t)
	r := w.page()
	w.history()
	r.Frames(1)
	has(t, r.Rig, ui.RoleGroup, "No calls yet")
	m := callMsg(ada, "x", false, w.at(0), abi.CallStatusMissed)
	if err := w.f.Reply(abi.CommandTagLoadCallLog, ev(abi.EventTagCallLog, func(e *abi.Event) {
		e.CallLog = abi.EventCallLog{Batch: 41, Last: true, Messages: []abi.Message{m}}
	})); err != nil {
		t.Fatal(err)
	}
	before := w.count(abi.CommandTagLoadCallLog)
	w.emit(ev(abi.EventTagIncoming, func(e *abi.Event) {
		e.Incoming.Chat = []byte(ada)
		e.Incoming.Message = m
	}))
	r.Frames(2)
	if w.count(abi.CommandTagLoadCallLog) != before+1 {
		t.Errorf("sent %v", w.sent())
	}
	has(t, r.Rig, ui.RoleListItem, "Ada Lovelace, Missed voice call, 15:30")
	hasNot(t, r.Rig, ui.RoleGroup, "No calls yet")
}

// CA-019: a call on a number not on screen keeps its own names; the Calls
// page cannot place another call over it.
func TestTheCallsPageCannotReplaceAnotherNumbersCall(t *testing.T) {
	w := newWorld(t)
	f2 := newWorld(t).f
	b := w.s.AddAccount("2", nil, f2)
	if err := f2.Emit(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 10, Last: true, Chats: []abi.Chat{
			{ID: []byte(ada), Name: []byte("Ada on work number"), Kind: abi.ChatKindDirect},
		}}
	}), ev(abi.EventTagCall, func(e *abi.Event) { incoming(50, abi.CallPhaseIncoming)(&e.Call.Update) })); err != nil {
		t.Fatal(err)
	}
	w.s.Pump(0)
	win := w.window()
	has(t, win.Rig, ui.RoleLabel, "Ada on work number")
	hasNot(t, win.Rig, ui.RoleLabel, "Ada Lovelace")
	r := w.page()
	w.history(callMsg(grace, "g1", true, w.at(time.Hour), abi.CallStatusAnswered))
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleButton, "Voice call Grace Hopper"), widgettest.Idle(2))
	if w.count(abi.CommandTagStartCall) != 0 || containsTag(f2.SentTags(), abi.CommandTagStartCall) {
		t.Fatal("the Calls page placed a second call")
	}
	has(t, r.Rig, ui.RoleGroup, "Error: Hang up the current call first")
	if w.h.call.u.Generation != 50 || w.h.call.serial != b.Serial || w.s.Active != 0 {
		t.Fatal("the call or the account on screen changed")
	}
}

func containsTag(tags []abi.CommandTag, tag abi.CommandTag) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}
