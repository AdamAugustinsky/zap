package calls

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

// CA-001: the ringback is upstream's ETSI tone, 425 Hz for one second in
// every five, in a clip long enough to outlast a call that rings.
func TestTheRingbackIsOneSecondOf425HzInFive(t *testing.T) {
	s := ringbackSamples()
	if len(s) != 5*ringbackRate {
		t.Fatalf("%d samples", len(s))
	}
	last := 0
	for i, v := range s {
		if math.Abs(float64(v)) > 1e-4 {
			last = i
		}
		if math.Abs(float64(v)) > ringbackLevel+1e-3 {
			t.Fatalf("sample %d is %v, louder than the line tone", i, v)
		}
	}
	if last >= ringbackRate {
		t.Errorf("sounding until sample %d, past the first second", last)
	}
	mid, crossings := s[ringbackRate/4:ringbackRate*3/4], 0
	for i := 1; i < len(mid); i++ {
		if mid[i-1] <= 0 && mid[i] > 0 {
			crossings++
		}
	}
	if hz := float64(crossings) / 0.5; math.Abs(hz-425) > 10 {
		t.Errorf("%.0f Hz", hz)
	}
	if pcm := ringbackPCM(); len(pcm) != 2*ringbackCycles*len(s) {
		t.Errorf("clip of %d bytes", len(pcm))
	}
}

// CA-001: the ringback plays while the call we placed rings at the other
// end, and stops when it is answered, ends or is hung up; a call to us and
// the demo never play it.
func TestTheRingbackPlaysOnlyWhileOurCallRings(t *testing.T) {
	w := newWorld(t)
	w.h.place(w.a, []byte(ada), false)
	gen := w.h.call.u.Generation
	for _, p := range []abi.CallPhase{abi.CallPhaseDialing, abi.CallPhaseConnecting} {
		w.update(outgoing(gen, p))
	}
	if tn := w.tone(); tn.starts != 0 {
		t.Fatalf("ringback before it rings: %d starts", tn.starts)
	}
	w.update(outgoing(gen, abi.CallPhaseRinging))
	w.update(outgoing(gen, abi.CallPhaseRinging))
	if tn := w.tone(); tn.starts != 1 || !w.h.toneOn {
		t.Fatalf("ringing: %d starts, on %v", tn.starts, w.h.toneOn)
	}
	w.update(outgoing(gen, abi.CallPhaseActive))
	if tn := w.tone(); tn.stops != 1 || w.h.toneOn {
		t.Errorf("answered: %d stops, on %v", tn.stops, w.h.toneOn)
	}

	// Hanging up while it rings.
	w = newWorld(t)
	w.h.place(w.a, []byte(ada), false)
	w.update(outgoing(w.h.call.u.Generation, abi.CallPhaseRinging))
	w.h.hangup()
	if tn := w.tone(); tn.starts != 1 || tn.stops != 1 {
		t.Errorf("hung up: %d starts, %d stops", tn.starts, tn.stops)
	}

	// A call to us rings with the ringtone (package sys), not this.
	w = newWorld(t)
	w.update(incoming(w.f.CallGeneration(), abi.CallPhaseIncoming))
	if w.tone().starts != 0 {
		t.Error("the ringback played for a call to us")
	}

	// The demo makes no sound.
	w = newWorld(t)
	w.s.Demo = true
	w.h.place(w.a, []byte(ada), false)
	w.update(outgoing(w.h.call.u.Generation, abi.CallPhaseRinging))
	if w.tone().starts != 0 {
		t.Error("the demo played the ringback")
	}
}

// CA-004: a call that rings while ZapFast is not in front (its main window
// closed or hidden) opens its own window with no main window behind it
// (OpenWindowAlone); one that rings with the window in front opens as any
// window does, and a call we place never comes alone.
func TestACallThatRingsWhileTheAppIsNotInFrontOpensAloneNotTheMainWindow(t *testing.T) {
	w := newWorld(t)
	w.update(incoming(w.f.CallGeneration(), abi.CallPhaseIncoming))
	if !w.s.Win.Alone[state.WinCall] || !w.s.Win.Want[state.WinCall] || w.s.Req.RaiseMain {
		t.Errorf("not in front: alone %v, window asked %v, main raised %v", w.s.Win.Alone[state.WinCall], w.s.Win.Want[state.WinCall], w.s.Req.RaiseMain)
	}

	w = newWorld(t)
	w.s.Focused = true
	w.update(incoming(w.f.CallGeneration(), abi.CallPhaseIncoming))
	if w.s.Win.Alone[state.WinCall] || !w.s.Win.Want[state.WinCall] || w.s.Req.RaiseMain {
		t.Errorf("in front: alone %v, window asked %v", w.s.Win.Alone[state.WinCall], w.s.Win.Want[state.WinCall])
	}

	w = newWorld(t)
	w.h.place(w.a, []byte(ada), false)
	if w.s.Win.Alone[state.WinCall] || w.s.Req.RaiseMain {
		t.Error("placing a call came alone")
	}
}

// A locked app says nothing of who calls: the call window waits for the lock
// to open (and goes when the lock locks), then shows the call.
func TestTheCallWindowWaitsForTheAppLock(t *testing.T) {
	w := newWorld(t)
	w.s.Lock.Locked = true
	w.update(incoming(w.f.CallGeneration(), abi.CallPhaseIncoming))
	if w.s.Win.Want[state.WinCall] || w.h.showing() || !w.h.liveCall() {
		t.Fatalf("locked: window asked %v, showing %v, live %v", w.s.Win.Want[state.WinCall], w.h.showing(), w.h.liveCall())
	}
	w.s.Lock.Locked = false
	w.h.tick(w.now)
	if !w.s.Win.Want[state.WinCall] || !w.h.showing() {
		t.Errorf("unlocked: window asked %v", w.s.Win.Want[state.WinCall])
	}

	// Locking again with the window open closes it, and is not the reader
	// closing it.
	w.s.Win.Want[state.WinCall], w.s.Win.Open[state.WinCall], w.h.winSeen = false, true, true
	w.s.Lock.Locked = true
	w.s.Locked()
	if !w.s.Win.Shut[state.WinCall] || w.h.winSeen {
		t.Errorf("locking: window shut %v, seen %v", w.s.Win.Shut[state.WinCall], w.h.winSeen)
	}
	w.s.Win.Open[state.WinCall] = false
	w.h.tick(w.now)
	if w.h.call.hidden {
		t.Error("the lock closing the window put the call aside")
	}
	w.s.Lock.Locked = false
	w.h.tick(w.now)
	if !w.s.Win.Want[state.WinCall] {
		t.Error("the call window did not come back with the lock open")
	}
}

// CA-004: a call missed while resident and app-locked never asks for a
// caller window, including after unlocking past the farewell.
func TestACallMissedWhileLockedDoesNotOpenAfterUnlock(t *testing.T) {
	w := newWorld(t)
	w.s.Lock.Locked = true
	w.update(incoming(1, abi.CallPhaseIncoming))
	if w.s.Win.Want[state.WinCall] || w.s.Win.Alone[state.WinCall] || w.s.Req.RaiseMain {
		t.Fatal("the locked resident app asked for a window")
	}
	w.update(func(u *abi.CallUpdate) {
		incoming(1, abi.CallPhaseEnded)(u)
		u.HasOutcome, u.Outcome = true, abi.CallStatusMissed
	})
	if w.s.Win.Want[state.WinCall] || w.h.showing() {
		t.Fatal("a missed call revealed its caller behind the app lock")
	}
	w.now = w.now.Add(farewell)
	w.s.Lock.Locked = false
	w.h.tick(w.now)
	if w.s.Win.Want[state.WinCall] || w.h.call.on || w.s.Req.RaiseMain {
		t.Fatal("unlocking resurrected the missed call")
	}
}

// writePicture writes a small PNG and returns its path.
func writePicture(t *testing.T, name string) string {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			im.Set(x, y, color.RGBA{R: 40, G: 150 + uint8(10*y), B: 130, A: 255}) // a teal, so a capture shows it
		}
	}
	im.Set(1, 1, color.RGBA{R: 200, A: 255})
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, im); err != nil {
		t.Fatal(err)
	}
	return path
}

// CA-003, CA-016: the call shows the person's picture, asked for from the
// core once; the initials stay until it is in, and a locked chat's call
// shows no picture.
func TestTheCallShowsThePersonsPicture(t *testing.T) {
	w := newWorld(t)
	w.update(incoming(w.f.CallGeneration(), abi.CallPhaseIncoming))
	r := w.window()
	r.Frames(3)
	if n := countAvatarAsks(w, ada, true); n != 1 {
		t.Fatalf("the large picture was asked for %d times", n)
	}
	if pc := r.win.pics.large[ada]; pc == nil || pc.path != "" {
		t.Fatalf("before the core answers: %+v", pc)
	}
	path := writePicture(t, "ada.png")
	w.emit(ev(abi.EventTagAvatar, func(e *abi.Event) {
		e.Avatar.ID, e.Avatar.Full, e.Avatar.Path, e.Avatar.HasPath = []byte(ada), true, []byte(path), true
	}))
	r.Frames(3)
	if pc := r.win.pics.large[ada]; pc == nil || pc.path != path {
		t.Errorf("after the core answers: %+v", pc)
	}
	if n := countAvatarAsks(w, ada, true); n != 1 {
		t.Errorf("asked %d times in all", n)
	}
}

func countAvatarAsks(w *world, jid string, full bool) int {
	n := 0
	for _, c := range w.f.Sent() {
		if c.Tag == abi.CommandTagFetchAvatar && string(c.FetchAvatar.ID) == jid && c.FetchAvatar.Full == full {
			n++
		}
	}
	return n
}

// CA-027: Escape leaves Calls for Chats, from the list as from the new-call
// list's own Escape first.
func TestEscapeLeavesTheCallsTab(t *testing.T) {
	w := newWorld(t)
	r := w.page()
	w.history()
	r.Frames(2)
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	if w.s.Page != state.PageChats {
		t.Errorf("page %v after Escape", w.s.Page)
	}
}

// CA-028: in a narrow window the call list takes the window beside the rail,
// and a picked call's chat takes it over both; closing the chat brings the
// list back.
func TestInANarrowWindowAPickedCallsChatTakesTheWindow(t *testing.T) {
	w := newWorld(t)
	r := w.pageSized(600, 700)
	w.history(callMsg(grace, "g1", true, w.at(time.Hour), abi.CallStatusAnswered))
	r.Frames(2)
	has(t, r.Rig, ui.RoleList, "Call history")
	r.Play(widgettest.Click(ui.RoleListItem, "Grace Hopper, Voice call, 14:30"), widgettest.Idle(2))
	if ch, ok := w.a.OpenChatView(); !ok || string(ch.ID()) != grace {
		t.Fatalf("open chat %v", w.a.Open)
	}
	hasNot(t, r.Rig, ui.RoleList, "Call history")
	has(t, r.Rig, ui.RoleLabel, "Conversation")
	w.a.Open = 0
	w.s.Touch()
	r.Frames(2)
	has(t, r.Rig, ui.RoleList, "Call history")
}

// LK-008: a locked chat's call waits in the bar as "Locked chat".
func TestALockedChatsCallWaitsInTheBarAsLockedChat(t *testing.T) {
	w := newWorld(t)
	w.emit(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 9, Last: true, Chats: []abi.Chat{
			{ID: []byte(ada), Name: []byte("Ada Lovelace"), Kind: abi.ChatKindDirect, Locked: true},
		}}
	}))
	r := w.page()
	w.history()
	w.update(incoming(w.f.CallGeneration(), abi.CallPhaseActive))
	r.Frames(2)
	has(t, r.Rig, ui.RoleGroup, "Locked chat")
	hasNot(t, r.Rig, ui.RoleGroup, "Ada Lovelace")
	r.Play(widgettest.Click(ui.RoleButton, "Return to the call"), widgettest.Idle(2))
	if w.s.Dialog.Kind != state.DialogUnlockLockedChats || w.h.showing() {
		t.Errorf("Return to the call: dialog %v, showing %v", w.s.Dialog.Kind, w.h.showing())
	}
}

// CA-016: the faces on a ringing group call are pictures, overlapping, with
// "+N" for those past the first five; the stack is named by who is on it.
func TestTheFacesOnARingingGroupCallCountTheRest(t *testing.T) {
	w := newWorld(t)
	names := []string{"Ada Lovelace", "Grace Hopper", "Alan Turing", "Mary Jackson", "Dorothy Vaughan", "Hedy Lamarr", "Radia Perlman"}
	jids := make([]abi.Participant, len(names))
	chats := make([]abi.Chat, len(names))
	for i, n := range names {
		id := []byte(n[:4] + "@s.whatsapp.net")
		jids[i] = abi.Participant{ID: id, Joined: true}
		chats[i] = abi.Chat{ID: id, Name: []byte(n), Kind: abi.ChatKindDirect}
	}
	w.emit(ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 9, Last: true, Chats: chats} }))
	w.update(func(u *abi.CallUpdate) {
		u.Generation, u.Chat, u.Direction, u.Phase, u.HasGroup = w.f.CallGeneration(), []byte(group), abi.CallDirectionIncoming, abi.CallPhaseIncoming, true
		u.Group.Participants = jids
	})
	pic := writePicture(t, "face.png")
	for _, j := range jids {
		w.emit(ev(abi.EventTagAvatar, func(e *abi.Event) {
			e.Avatar.ID, e.Avatar.Path, e.Avatar.HasPath = j.ID, []byte(pic), true
		}))
	}
	r := w.window()
	r.Play(widgettest.Settle())
	r.Frames(3)
	shot(t, r.Rig, "ringing-faces")
	stack := r.Must(ui.RoleGroup, "Ada Lovelace, Grace Hopper, Alan Turing, Mary Jackson, Dorothy Vaughan, Hedy Lamarr, Radia Perlman")
	// Five pictures and a "+2" disc, 36 pt each, 0.7 of a face apart.
	if sw, sh := stack.Bounds[2]-stack.Bounds[0], stack.Bounds[3]-stack.Bounds[1]; sw < 150 || sw > 175 || sh != 36 {
		t.Errorf("the stack is %v × %v, want 6 faces of 36", sw, sh)
	}
	faces := 0
	for _, n := range r.Tree().Nodes {
		if n.Role == ui.RoleImage && n.Bounds[0] >= stack.Bounds[0] && n.Bounds[2] <= stack.Bounds[2] && n.Bounds[1] >= stack.Bounds[1] && n.Bounds[3] <= stack.Bounds[3] {
			faces++
		}
	}
	if faces != 5 {
		t.Errorf("%d pictures in the stack, want 5", faces)
	}
}

// CA-001, by Adam's decision (2026-10-21): the ringback follows the
// notification sound. With the account's notifications off, or the sound
// they make set to none, the call is placed silently: the phases go on, the
// tone is never started. A chat with a sound of its own over a message
// sound of none still rings back.
func TestTheRingbackFollowsTheNotificationSound(t *testing.T) {
	ring := func(w *world) *fakeTone {
		w.h.place(w.a, []byte(ada), false)
		gen := w.h.call.u.Generation
		w.update(outgoing(gen, abi.CallPhaseRinging))
		if w.h.call.u.Phase != abi.CallPhaseRinging {
			t.Fatalf("the call is in phase %v", w.h.call.u.Phase)
		}
		w.update(outgoing(gen, abi.CallPhaseActive))
		w.h.hangup()
		return w.tone()
	}
	cases := []struct {
		name  string
		setup func(w *world)
		plays bool
	}{
		{"sounds on", func(w *world) {}, true},
		{"notifications off", func(w *world) { w.a.Settings.Notifications = false }, false},
		{"message sound none", func(w *world) { w.s.Settings.MessageSound = state.SoundChoice{Sound: state.SoundNone} }, false},
		{"group sounds off do not matter", func(w *world) { w.s.Settings.GroupSounds = false }, true},
		{"the chat's own sound over none", func(w *world) {
			w.s.Settings.MessageSound = state.SoundChoice{Sound: state.SoundNone}
			w.emit(ev(abi.EventTagChats, func(e *abi.Event) {
				e.Chats = abi.EventChats{Batch: 3, Last: true, Chats: []abi.Chat{{
					ID: []byte(ada), Name: []byte("Ada Lovelace"), Kind: abi.ChatKindDirect, LastActivity: 30,
					HasNotificationSound: true, NotificationSound: abi.NotificationSound{Tag: abi.NotificationSoundTagAlert},
				}}}
			}))
		}, true},
		{"the chat's own none", func(w *world) {
			w.emit(ev(abi.EventTagChats, func(e *abi.Event) {
				e.Chats = abi.EventChats{Batch: 3, Last: true, Chats: []abi.Chat{{
					ID: []byte(ada), Name: []byte("Ada Lovelace"), Kind: abi.ChatKindDirect, LastActivity: 30,
					HasNotificationSound: true, NotificationSound: abi.NotificationSound{Tag: abi.NotificationSoundTagNone},
				}}}
			}))
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			tc.setup(w)
			tn := ring(w)
			if tc.plays && (tn.starts != 1 || tn.stops != 1) {
				t.Errorf("%d starts, %d stops: want the ringback to play and stop", tn.starts, tn.stops)
			}
			if !tc.plays && tn.starts != 0 {
				t.Errorf("the ringback started %d times", tn.starts)
			}
		})
	}
}
