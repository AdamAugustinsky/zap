package calls

import (
	"slices"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/state"
	"zapfast/ui/screen"
)

type dockLog struct{ rings, stops int }

func (d *dockLog) Ring() { d.rings++ }
func (d *dockLog) Stop() { d.stops++ }

// CA-003, CA-029: a call to us takes the screen (the call window is asked
// for), rings the Dock once, and once answered shows its length.
func TestARingingCallTakesTheScreenAndIsAnnouncedOnce(t *testing.T) {
	w := newWorld(t)
	d := &dockLog{}
	SetDock(w.s, d)
	w.update(incoming(1, abi.CallPhaseIncoming))
	if !w.h.liveCall() || !w.h.showing() {
		t.Fatal("the ringing call is not on screen")
	}
	if !w.s.Win.Want[state.WinCall] {
		t.Error("the call window was not asked for")
	}
	if got := w.status(); got != "Incoming voice call" {
		t.Errorf("status %q", got)
	}
	if w.name() != "Ada Lovelace" {
		t.Errorf("name %q", w.name())
	}
	w.update(incoming(1, abi.CallPhaseIncoming))
	if d.rings != 1 {
		t.Errorf("rang the Dock %d times", d.rings)
	}
	w.update(incoming(1, abi.CallPhaseActive))
	if d.stops != 1 {
		t.Errorf("the Dock was stopped %d times", d.stops)
	}
	w.now = w.now.Add(65 * time.Second)
	if got := w.status(); got != "1:05" {
		t.Errorf("an answered call reads %q after 65 s", got)
	}
	w.now = w.now.Add(time.Hour)
	if got := w.status(); got != "1:01:05" {
		t.Errorf("after an hour: %q", got)
	}
}

// CA-001, CA-010: a call shows "Connecting…" the moment its button is
// pressed, follows the core through Calling and Ringing, and when the other
// side hangs up says "Call ended" for 1.5 s, then goes.
func TestAPlacedCallFollowsItsPhasesAndSaysGoodbye(t *testing.T) {
	w := newWorld(t)
	w.h.place(w.a, []byte(ada), true)
	cmd, ok := w.last(abi.CommandTagStartCall)
	if !ok || string(cmd.StartCall.Chat) != ada || !cmd.StartCall.Video || cmd.StartCall.Generation == 0 || cmd.StartCall.Cancel == 0 {
		t.Fatalf("StartCall %+v (sent %v)", cmd.StartCall, w.sent())
	}
	gen := cmd.StartCall.Generation
	// The number comes from the core's counter, which numbers the calls it
	// is offered too.
	if next := w.f.CallGeneration(); next <= gen {
		t.Errorf("generation %d, the core's next %d", gen, next)
	}
	if w.status() != "Connecting…" || !w.s.Win.Want[state.WinCall] {
		t.Errorf("placing: %q, window asked %v", w.status(), w.s.Win.Want[state.WinCall])
	}
	if !w.h.liveHere(w.a, []byte(ada)) {
		t.Error("Ada's chat does not have the live call")
	}
	// A second press while it is placing places nothing.
	w.h.place(w.a, []byte(ada), false)
	if w.count(abi.CommandTagStartCall) != 1 || len(w.s.Toasts) != 1 || string(w.s.Toasts[0].Text) != "Hang up the current call first" {
		t.Errorf("a second call: %v, toasts %d", w.sent(), len(w.s.Toasts))
	}
	for _, step := range []struct {
		phase abi.CallPhase
		want  string
	}{{abi.CallPhaseDialing, "Calling…"}, {abi.CallPhaseRinging, "Ringing…"}, {abi.CallPhaseConnecting, "Connecting…"}} {
		w.update(outgoing(gen, step.phase))
		if got := w.status(); got != step.want {
			t.Errorf("%v reads %q, want %q", step.phase, got, step.want)
		}
	}
	w.update(func(u *abi.CallUpdate) {
		outgoing(gen, abi.CallPhaseActive)(u)
		u.ActiveForMS, u.HasActiveForMS = 3000, true
	})
	if got := w.status(); got != "0:03" {
		t.Errorf("active reads %q", got)
	}
	w.update(func(u *abi.CallUpdate) {
		outgoing(gen, abi.CallPhaseEnded)(u)
		u.Outcome, u.HasOutcome = abi.CallStatusAnswered, true
	})
	if w.status() != "Call ended" || !w.h.showing() || w.h.liveCall() {
		t.Errorf("ended: %q, showing %v, live %v", w.status(), w.h.showing(), w.h.liveCall())
	}
	w.now = w.now.Add(1400 * time.Millisecond)
	w.h.tick(w.now)
	if !w.h.call.on {
		t.Error("the farewell went before 1.5 s")
	}
	w.now = w.now.Add(200 * time.Millisecond)
	w.h.tick(w.now)
	if w.h.call.on {
		t.Error("the farewell stays past 1.5 s")
	}
	// A late word about the call that is over is not drawn again.
	w.update(outgoing(gen, abi.CallPhaseEnded))
	if w.h.call.on {
		t.Error("a late update brought the ended call back")
	}
}

// CA-010: each way a call can end reads as upstream's.
func TestEveryOutcomeSaysHowTheCallEnded(t *testing.T) {
	for _, c := range []struct {
		o     abi.CallStatus
		phase abi.CallPhase
		want  string
	}{
		{abi.CallStatusAnswered, abi.CallPhaseEnded, "Call ended"},
		{abi.CallStatusMissed, abi.CallPhaseEnded, "Missed call"},
		{abi.CallStatusDeclined, abi.CallPhaseFailed, "Declined"},
		{abi.CallStatusBusy, abi.CallPhaseFailed, "Busy"},
		{abi.CallStatusFailed, abi.CallPhaseFailed, "Could not connect"},
		{abi.CallStatusNoAnswer, abi.CallPhaseFailed, "No answer"},
		{abi.CallStatusConnectionLost, abi.CallPhaseEnded, "Connection lost"},
		{abi.CallStatusConnectionLost, abi.CallPhaseFailed, "Could not connect"},
		{abi.CallStatusAnsweredElsewhere, abi.CallPhaseEnded, "Answered on another device"},
		{abi.CallStatusDeclinedElsewhere, abi.CallPhaseEnded, "Declined on another device"},
	} {
		u := abi.CallUpdate{Phase: c.phase, Outcome: c.o, HasOutcome: true}
		if got := string(appendStatus(nil, &u, 0, false, nil)); got != c.want {
			t.Errorf("%v/%v: %q, want %q", c.o, c.phase, got, c.want)
		}
	}
	u := abi.CallUpdate{Phase: abi.CallPhaseEnded}
	if got := string(appendStatus(nil, &u, 0, false, nil)); got != "Call ended" {
		t.Errorf("no outcome: %q", got)
	}
}

// CA-002: hanging up while connecting cancels the call before it reaches
// them; the screen goes at once and a new call can be placed.
func TestHangingUpWhileConnectingNeverRingsThePeer(t *testing.T) {
	w := newWorld(t)
	w.h.place(w.a, []byte(ada), false)
	start, _ := w.last(abi.CommandTagStartCall)
	w.h.hangup()
	cancel, ok := w.last(abi.CommandTagCancelCall)
	if !ok || cancel.CancelCall.Token != start.StartCall.Cancel {
		t.Errorf("the placing call was not cancelled: %v", w.sent())
	}
	if w.count(abi.CommandTagHangupCall) != 1 {
		t.Error("no HangupCall for an offer that already went")
	}
	if w.h.call.on || w.h.liveCall() {
		t.Error("the screen stays after hanging up")
	}
	w.update(outgoing(start.StartCall.Generation, abi.CallPhaseDialing))
	if w.h.call.on {
		t.Error("the core's last word about the call was drawn")
	}
	w.h.place(w.a, []byte(ada), false)
	if next, _ := w.last(abi.CommandTagStartCall); next.StartCall.Generation == start.StartCall.Generation || w.h.call.u.Phase != abi.CallPhasePlacing {
		t.Errorf("no new call: gen %d, phase %v", next.StartCall.Generation, w.h.call.u.Phase)
	}
}

// Declining sends DeclineCall and takes the call down at once.
func TestDecliningTakesTheCallDownAtOnce(t *testing.T) {
	w := newWorld(t)
	w.update(incoming(7, abi.CallPhaseIncoming))
	w.h.decline()
	if w.count(abi.CommandTagDeclineCall) != 1 || w.h.call.on {
		t.Errorf("sent %v, on %v", w.sent(), w.h.call.on)
	}
	w.update(func(u *abi.CallUpdate) {
		incoming(7, abi.CallPhaseEnded)(u)
		u.Outcome, u.HasOutcome = abi.CallStatusDeclined, true
	})
	if w.h.call.on {
		t.Error("the declined call's end was drawn")
	}
}

// A call that ended before anything was drawn for it is not drawn; the peer
// declining a placed call says so briefly.
func TestOnlyCallsThatWereShownSayGoodbye(t *testing.T) {
	w := newWorld(t)
	w.update(func(u *abi.CallUpdate) {
		incoming(3, abi.CallPhaseFailed)(u)
		u.Outcome, u.HasOutcome = abi.CallStatusFailed, true
	})
	if w.h.call.on {
		t.Error("an unseen call's end was drawn")
	}
	w.h.place(w.a, []byte(ada), false)
	gen := w.h.call.u.Generation
	w.update(outgoing(gen, abi.CallPhaseRinging))
	w.update(func(u *abi.CallUpdate) {
		outgoing(gen, abi.CallPhaseFailed)(u)
		u.Outcome, u.HasOutcome = abi.CallStatusDeclined, true
	})
	if w.status() != "Declined" || w.h.call.until.IsZero() {
		t.Errorf("declined: %q, farewell %v", w.status(), w.h.call.until)
	}
}

// A call the core refuses to place, or never answers for, says "Could not
// connect" and goes; the late one is not placed.
func TestACallThatCannotBePlacedSaysSo(t *testing.T) {
	w := newWorld(t)
	w.f.Refuse(abi.CommandTagStartCall, -3, "no calls in the demo")
	w.h.place(w.a, []byte(ada), false)
	if w.h.call.u.Phase != abi.CallPhaseFailed || w.status() != "Could not connect" || w.h.liveCall() {
		t.Errorf("refused: phase %v, %q", w.h.call.u.Phase, w.status())
	}

	w = newWorld(t)
	w.h.place(w.a, []byte(ada), false)
	w.now = w.now.Add(placingTimeout)
	w.h.tick(w.now)
	if w.h.call.u.Phase != abi.CallPhaseFailed || w.count(abi.CommandTagCancelCall) != 1 {
		t.Errorf("timed out: phase %v, sent %v", w.h.call.u.Phase, w.sent())
	}
}

// A call to us takes the place of one still being placed; the core's
// refusal of the placing one is not drawn over it.
func TestACallToUsTakesThePlaceOfOneBeingPlaced(t *testing.T) {
	w := newWorld(t)
	w.h.place(w.a, []byte(ada), false)
	gen := w.h.call.u.Generation
	w.update(incoming(w.f.CallGeneration(), abi.CallPhaseIncoming))
	if w.h.call.u.Phase != abi.CallPhaseIncoming {
		t.Fatalf("phase %v", w.h.call.u.Phase)
	}
	w.update(func(u *abi.CallUpdate) {
		outgoing(gen, abi.CallPhaseFailed)(u)
		u.Outcome, u.HasOutcome = abi.CallStatusBusy, true
	})
	if w.h.call.u.Phase != abi.CallPhaseIncoming {
		t.Errorf("the refused placing call replaced the ringing one: %v", w.h.call.u.Phase)
	}
}

// CA-019: one call at a time: another number's offer while a call is up is
// declined there; the call's own commands reach the number it is on.
func TestOneCallAtATimeAcrossNumbers(t *testing.T) {
	w := newWorld(t)
	w.update(incoming(1, abi.CallPhaseActive))
	f2 := newWorld(t).f // a second core
	b := w.s.AddAccount("2", nil, f2)
	if err := f2.Emit(ev(abi.EventTagCall, func(e *abi.Event) { incoming(2, abi.CallPhaseIncoming)(&e.Call.Update) })); err != nil {
		t.Fatal(err)
	}
	w.s.Pump(0)
	if !slices.Contains(f2.SentTags(), abi.CommandTagDeclineCall) {
		t.Errorf("the second number's offer was not declined: %v", f2.SentTags())
	}
	if w.h.call.serial != w.a.Serial || w.h.call.u.Generation != 1 {
		t.Error("the second number's offer replaced the call")
	}
	_ = b
	// Now the second number has the call and the first is on screen.
	w = newWorld(t)
	f2 = newWorld(t).f
	b = w.s.AddAccount("2", nil, f2)
	f2.Emit(ev(abi.EventTagCall, func(e *abi.Event) { incoming(5, abi.CallPhaseIncoming)(&e.Call.Update) }))
	w.s.Pump(0)
	w.h.answer()
	w.h.setMuted(true)
	if slices.Contains(w.f.SentTags(), abi.CommandTagAnswerCall) || !slices.Contains(f2.SentTags(), abi.CommandTagAnswerCall) || !slices.Contains(f2.SentTags(), abi.CommandTagSetCallMuted) {
		t.Errorf("first %v, second %v", w.f.SentTags(), f2.SentTags())
	}
	if w.h.liveHere(w.a, []byte(ada)) {
		t.Error("the account on screen has no call")
	}
}

// LK-008: a locked chat's call waits in the strip as "Locked chat" until its
// folder is opened; returning asks for the code.
func TestALockedChatsCallSaysNothingUntilItsFolderOpens(t *testing.T) {
	w := newWorld(t)
	w.emit(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 9, Last: true, Chats: []abi.Chat{
			{ID: []byte(ada), Name: []byte("Ada Lovelace"), Kind: abi.ChatKindDirect, Locked: true},
		}}
	}))
	d := &dockLog{}
	SetDock(w.s, d)
	w.update(incoming(1, abi.CallPhaseIncoming))
	if w.h.showing() || w.s.Win.Want[state.WinCall] {
		t.Error("the locked chat's call took the screen")
	}
	if w.name() != "Locked chat" || d.rings != 0 {
		t.Errorf("name %q, rings %d", w.name(), d.rings)
	}
	w.h.returnTo()
	if w.h.showing() || w.s.Dialog.Kind != state.DialogUnlockLockedChats {
		t.Errorf("returning: showing %v, dialog %v", w.h.showing(), w.s.Dialog.Kind)
	}
	w.a.Mode = state.ListLocked // the folder is open
	if w.name() != "Ada Lovelace" {
		t.Errorf("with the folder open: %q", w.name())
	}
	w.h.returnTo()
	if !w.h.showing() {
		t.Error("the call did not come back with the folder open")
	}
	w.a.Mode = state.ListChats
	w.h.tick(w.now)
	if w.h.showing() {
		t.Error("closing the folder left the call on screen")
	}
}

// CA-018, MK-007: a call link (sys hands it to JoinLink) joins with
// "Connecting…" under "Call link"; a second while a call is up is not joined.
func TestACallLinkJoinsHere(t *testing.T) {
	w := newWorld(t)
	JoinLink(w.s, w.a, []byte("AbCdEf1234567890"), true)
	cmd, ok := w.last(abi.CommandTagJoinCallLink)
	if !ok || string(cmd.JoinCallLink.Token) != "AbCdEf1234567890" || !cmd.JoinCallLink.Video {
		t.Fatalf("sent %v", w.sent())
	}
	if w.name() != "Call link" || w.status() != "Connecting…" || !w.h.link() {
		t.Errorf("%q %q", w.name(), w.status())
	}
	JoinLink(w.s, w.a, []byte("other"), false)
	if w.count(abi.CommandTagJoinCallLink) != 1 {
		t.Error("a second link was joined during the call")
	}
}

// CA-007, CA-008: putting the call aside keeps it running; returning brings
// the window back; closing the window by its own button puts it aside.
func TestPuttingTheCallAsideKeepsItRunning(t *testing.T) {
	w := newWorld(t)
	w.update(incoming(1, abi.CallPhaseActive))
	w.s.Win.Want[state.WinCall] = false
	w.h.leave()
	if w.h.showing() || !w.h.liveCall() || w.count(abi.CommandTagHangupCall) != 0 {
		t.Errorf("aside: showing %v, live %v, sent %v", w.h.showing(), w.h.liveCall(), w.sent())
	}
	w.h.returnTo()
	if !w.h.showing() || !w.s.Win.Want[state.WinCall] {
		t.Error("returning did not bring the window back")
	}
	w.s.Win.Want[state.WinCall], w.s.Win.Open[state.WinCall] = false, false
	w.h.winSeen = true // the window was open, and closed
	w.h.tick(w.now)
	if w.h.showing() || !w.h.liveCall() {
		t.Error("closing the window did not put the call aside, or ended it")
	}
	// A call that ends while aside says how, on screen.
	w.update(func(u *abi.CallUpdate) {
		incoming(1, abi.CallPhaseEnded)(u)
		u.Outcome, u.HasOutcome = abi.CallStatusAnswered, true
	})
	if !w.h.showing() {
		t.Error("the ended call stayed behind the strip")
	}
}

// CA-006, CA-012, CA-014: mute, the camera and the devices are core
// commands to the call's account.
func TestTheCallsControlsAreCoreCommands(t *testing.T) {
	w := newWorld(t)
	w.update(incoming(1, abi.CallPhaseActive))
	w.h.setMuted(true)
	w.h.setCamera(true)
	w.h.setDevice(abi.DeviceKindMicrophone, []byte("usb"))
	w.h.setDevice(abi.DeviceKindSpeaker, nil)
	w.h.setDevice(abi.DeviceKindCamera, []byte("cam1"))
	w.h.refreshDevices()
	if c, _ := w.last(abi.CommandTagSetCallMuted); !c.SetCallMuted.Muted {
		t.Error("not muted")
	}
	if c, _ := w.last(abi.CommandTagSetCallCamera); !c.SetCallCamera.On {
		t.Error("camera not on")
	}
	if c, _ := w.last(abi.CommandTagSetCallMicrophone); !c.SetCallMicrophone.HasDevice || string(c.SetCallMicrophone.Device) != "usb" {
		t.Errorf("microphone %+v", c.SetCallMicrophone)
	}
	if c, ok := w.last(abi.CommandTagSetCallSpeaker); !ok || c.SetCallSpeaker.HasDevice {
		t.Errorf("speaker %+v", c.SetCallSpeaker)
	}
	if c, _ := w.last(abi.CommandTagSetCallCameraDevice); string(c.SetCallCameraDevice.Device) != "cam1" {
		t.Errorf("camera %+v", c.SetCallCameraDevice)
	}
	if w.count(abi.CommandTagRefreshCallDevices) != 1 {
		t.Error("no RefreshCallDevices")
	}
}

// CA-016: a group call rings under the group's name with who is calling.
func TestAGroupCallRingsUnderTheGroupsName(t *testing.T) {
	w := newWorld(t)
	w.update(func(u *abi.CallUpdate) {
		u.Generation, u.Chat, u.Direction, u.Phase, u.Video, u.HasGroup = 1, []byte(group), abi.CallDirectionIncoming, abi.CallPhaseIncoming, true, true
		u.Group.Caller, u.Group.HasCaller = []byte(grace), true
	})
	if w.name() != "Engines" || w.status() != "Group video call from Grace Hopper" {
		t.Errorf("%q: %q", w.name(), w.status())
	}
	u := abi.CallUpdate{Phase: abi.CallPhaseIncoming, HasGroup: true}
	if got := string(appendIncoming(nil, &u, nil)); got != "Incoming group voice call" {
		t.Errorf("no caller: %q", got)
	}
}

// The words of a finished call's length and of the duration.
func TestLengthsAndDurations(t *testing.T) {
	for secs, want := range map[uint64]string{45: "45 s", 754: "12 min 34 s", 720: "12 min", 3725: "1 h 2 min", 3600: "1 h"} {
		if got := string(appendLength(nil, secs)); got != want {
			t.Errorf("%d: %q, want %q", secs, got, want)
		}
	}
	for secs, want := range map[int64]string{0: "0:00", 65: "1:05", 3723: "1:02:03"} {
		if got := string(appendDuration(nil, secs)); got != want {
			t.Errorf("%d: %q, want %q", secs, got, want)
		}
	}
	if got := string(appendKey(nil, []byte("Ádá LÖVELACE"))); got != "ada lovelace" {
		t.Errorf("key %q", got)
	}
}

// The group grid fills the room without overlapping, two people side by
// side, a short last row centred (zf:src/ui/call.rs:1301).
func TestTilesFillTheRoomWithoutOverlapping(t *testing.T) {
	area := rect(0, 0, 1200, 600)
	for n := 1; n <= maxTiles; n++ {
		tiles := tileGrid(nil, n, area, tileGap)
		if len(tiles) != n {
			t.Fatalf("%d tiles: %d", n, len(tiles))
		}
		for i, a := range tiles {
			if a.X < -0.5 || a.Y < -0.5 || a.X+a.W > 1200.5 || a.Y+a.H > 600.5 {
				t.Errorf("%d tiles: %+v leaves the room", n, a)
			}
			if d := a.W/a.H - tileAspect; d > 0.01 || d < -0.01 {
				t.Errorf("%d tiles: aspect %v", n, a.W/a.H)
			}
			for _, b := range tiles[i+1:] {
				if a.X+a.W-0.5 > b.X && b.X+b.W-0.5 > a.X && a.Y+a.H-0.5 > b.Y && b.Y+b.H-0.5 > a.Y {
					t.Errorf("%d tiles overlap", n)
				}
			}
		}
	}
	two := tileGrid(nil, 2, area, tileGap)
	if two[0].Y != two[1].Y {
		t.Error("two tiles are not side by side")
	}
	five := tileGrid(nil, 5, area, tileGap)
	if mid := (five[3].X + five[4].X + five[4].W) / 2; mid < 599.5 || mid > 600.5 {
		t.Errorf("the short row is not centred: %v", mid)
	}
}

// Alike calls in a row fold into one history row; the new-call list offers
// the people called most (zf:src/ui/calls.rs:724, :740).
func TestHistoryRowsAndFrequentPeople(t *testing.T) {
	var l callLog
	l.add(&abi.EventCallLog{Batch: 1, Last: true, Messages: []abi.Message{
		callMsg(ada, "1", false, 60, abi.CallStatusMissed),
		callMsg(ada, "2", false, 50, abi.CallStatusMissed),
		callMsg(ada, "3", false, 40, abi.CallStatusAnswered),
		callMsg(grace, "4", true, 30, abi.CallStatusAnswered),
		callMsg(ada, "5", false, 20, abi.CallStatusAnswered),
		callMsg(group, "6", false, 15, abi.CallStatusMissed),
		callMsg(me, "7", true, 12, abi.CallStatusAnswered),
		callMsg(LinkChat, "8", true, 10, abi.CallStatusAnswered),
	}})
	rows := fold(nil, &l, nil)
	var got [][]string
	for _, r := range rows {
		var ids []string
		for i := r.first; i < r.first+r.n; i++ {
			ids = append(ids, string(l.id(i)))
		}
		got = append(got, ids)
	}
	want := [][]string{{"1", "2"}, {"3"}, {"4"}, {"5"}, {"6"}, {"7"}, {"8"}}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("rows %v", got)
	}
	var names []string
	for _, i := range frequent(nil, &l, []byte(me), nil) {
		names = append(names, string(l.chat(i)))
	}
	if !slices.Equal(names, []string{ada, grace}) {
		t.Errorf("frequent %v", names)
	}
	e := entry{status: abi.CallStatusAnswered, duration: 221}
	if got := string(appendSummary(nil, &e)); got != "Voice call · 3 min 41 s" {
		t.Errorf("summary %q", got)
	}
	e = entry{status: abi.CallStatusMissed, video: true}
	if got := string(appendSummary(nil, &e)); got != "Missed video call" {
		t.Errorf("summary %q", got)
	}
	e = entry{status: abi.CallStatusNoAnswer, fromMe: true}
	if got := string(appendSummary(nil, &e)); got != "Voice call · No answer" {
		t.Errorf("summary %q", got)
	}
}

func callMsg(chat, id string, fromMe bool, ts int64, st abi.CallStatus) abi.Message {
	m := abi.Message{ID: []byte(id), Chat: []byte(chat), Sender: []byte(chat), FromMe: fromMe, Timestamp: ts}
	m.Content.Tag = abi.ContentTagCall
	m.Content.Call.Status = st
	return m
}

func rect(x, y, w, h float32) screen.Rect { return screen.Rect{X: x, Y: y, W: w, H: h} }
