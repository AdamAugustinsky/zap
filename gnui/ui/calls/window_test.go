package calls

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/core/fake"
)

// CA-003: a ringing call shows the caller and "Incoming video call" with
// Accept and Decline; Accept answers, Decline refuses and takes the window
// down.
func TestTheRingingWindowAnswersAndDeclines(t *testing.T) {
	w := newWorld(t)
	w.update(func(u *abi.CallUpdate) { incoming(1, abi.CallPhaseIncoming)(u); u.Video = true })
	r := w.window()
	shot(t, r.Rig, "ringing")
	has(t, r.Rig, ui.RoleButton, "Accept")
	has(t, r.Rig, ui.RoleButton, "Decline")
	has(t, r.Rig, ui.RoleLabel, "Incoming video call")
	has(t, r.Rig, ui.RoleLabel, "Ada Lovelace")
	hasNot(t, r.Rig, ui.RoleButton, "Hide the call screen")
	r.Play(widgettest.Click(ui.RoleButton, "Accept"), widgettest.Idle(1))
	if w.count(abi.CommandTagAnswerCall) != 1 {
		t.Errorf("Accept sent %v", w.sent())
	}
	r.Play(widgettest.Click(ui.RoleButton, "Decline"), widgettest.Idle(2))
	if w.count(abi.CommandTagDeclineCall) != 1 || w.h.call.on {
		t.Errorf("Decline sent %v, call on %v", w.sent(), w.h.call.on)
	}
}

// CA-016: a group call rings under the group's name, with who calls and the
// faces already on it.
func TestAGroupCallRingsWithItsFaces(t *testing.T) {
	w := newWorld(t)
	w.update(func(u *abi.CallUpdate) {
		u.Generation, u.Chat, u.Direction, u.Phase, u.HasGroup = 1, []byte(group), abi.CallDirectionIncoming, abi.CallPhaseIncoming, true
		u.Group.Caller, u.Group.HasCaller = []byte(grace), true
		u.Group.Participants = []abi.Participant{{ID: []byte(grace), Joined: true}, {ID: []byte(alan), Joined: true}, {ID: []byte(ada)}}
	})
	r := w.window()
	shot(t, r.Rig, "ringing-group")
	has(t, r.Rig, ui.RoleLabel, "Engines")
	has(t, r.Rig, ui.RoleLabel, "Group voice call from Grace Hopper")
	has(t, r.Rig, ui.RoleGroup, "Grace Hopper, Alan Turing") // Ada is asked, not on it yet
}

// CA-006, CA-012: a connected call shows its length and Mute, the camera,
// Devices and Hang up; each is a core command, and the buttons follow the
// call's state.
func TestTheLiveWindowsControls(t *testing.T) {
	w := newWorld(t)
	w.update(func(u *abi.CallUpdate) {
		incoming(1, abi.CallPhaseActive)(u)
		u.ActiveForMS = 61_000
	})
	r := w.window()
	shot(t, r.Rig, "active")
	has(t, r.Rig, ui.RoleLabel, "1:01")
	has(t, r.Rig, ui.RoleButton, "Hide the call screen")
	r.Play(widgettest.Click(ui.RoleButton, "Mute"), widgettest.Idle(1))
	if c, ok := w.last(abi.CommandTagSetCallMuted); !ok || !c.SetCallMuted.Muted {
		t.Errorf("Mute sent %v", w.sent())
	}
	w.update(func(u *abi.CallUpdate) { incoming(1, abi.CallPhaseActive)(u); u.Muted = true })
	r.Frames(2)
	has(t, r.Rig, ui.RoleButton, "Unmute")
	// A voice call's camera button asks to switch to video.
	r.Play(widgettest.Click(ui.RoleButton, "Start video"), widgettest.Idle(1))
	if c, ok := w.last(abi.CommandTagSetCallCamera); !ok || !c.SetCallCamera.On {
		t.Errorf("Start video sent %v", w.sent())
	}
	r.Play(widgettest.Click(ui.RoleButton, "Hang up"), widgettest.Idle(2))
	if w.count(abi.CommandTagHangupCall) != 1 || w.h.call.on {
		t.Errorf("Hang up sent %v, on %v", w.sent(), w.h.call.on)
	}
}

// CA-011, CA-012, CA-013: on a video call our camera sits in the corner and
// its button turns it off and on; their request and their camera being off
// are said.
func TestAVideoCallsCameraAndItsWords(t *testing.T) {
	w := newWorld(t)
	w.update(func(u *abi.CallUpdate) {
		incoming(1, abi.CallPhaseActive)(u)
		u.Video, u.CameraOn = true, true
	})
	r := w.window()
	shot(t, r.Rig, "video")
	has(t, r.Rig, ui.RoleGroup, "You")
	has(t, r.Rig, ui.RoleLabel, "Their camera is off")
	r.Play(widgettest.Click(ui.RoleButton, "Turn the camera off"), widgettest.Idle(1))
	if c, ok := w.last(abi.CommandTagSetCallCamera); !ok || c.SetCallCamera.On {
		t.Errorf("camera off sent %v", w.sent())
	}
	w.update(func(u *abi.CallUpdate) {
		incoming(1, abi.CallPhaseActive)(u)
		u.Video, u.RemoteVideo = true, true
	})
	r.Frames(2)
	hasNot(t, r.Rig, ui.RoleGroup, "You")
	hasNot(t, r.Rig, ui.RoleLabel, "Their camera is off")
	r.Play(widgettest.Click(ui.RoleButton, "Turn the camera on"), widgettest.Idle(1))
	if c, ok := w.last(abi.CommandTagSetCallCamera); !ok || !c.SetCallCamera.On {
		t.Errorf("camera on sent %v", w.sent())
	}
	w.update(func(u *abi.CallUpdate) { incoming(1, abi.CallPhaseActive)(u); u.VideoRequested = true })
	r.Frames(2)
	has(t, r.Rig, ui.RoleLabel, "Wants to switch to video: turn on your camera to answer")
}

// CA-014, CA-015: Devices opens the pickers, which move the call; a device
// that went away and audio that does not arrive are said.
func TestTheDevicePickersAndTheNotes(t *testing.T) {
	w := newWorld(t)
	w.update(func(u *abi.CallUpdate) {
		incoming(1, abi.CallPhaseActive)(u)
		u.LostDevices = []abi.LostDevice{{Kind: abi.DeviceKindMicrophone, Name: []byte("Old Mic")}}
		u.PeerAudio, u.HasPeerAudio = abi.PeerAudio{Tag: abi.PeerAudioTagStalled}, true
	})
	w.emit(ev(abi.EventTagCallDevices, func(e *abi.Event) {
		e.CallDevices.Devices = abi.DeviceList{
			Microphones: []abi.Device{{ID: []byte("mic-1"), Label: []byte("Studio Mic")}},
			Speakers:    []abi.Device{{ID: []byte("spk-1"), Label: []byte("Desk Speakers")}},
		}
	}))
	r := w.window()
	has(t, r.Rig, ui.RoleLabel, "Microphone “Old Mic” is not available; using the system default")
	has(t, r.Rig, ui.RoleLabel, "No audio is arriving from this call")
	hasNot(t, r.Rig, ui.RoleComboBox, "Microphone")
	r.Play(widgettest.Click(ui.RoleButton, "Devices"), widgettest.Idle(2))
	shot(t, r.Rig, "devices")
	if w.count(abi.CommandTagRefreshCallDevices) != 1 {
		t.Errorf("Devices sent %v", w.sent())
	}
	has(t, r.Rig, ui.RoleComboBox, "Microphone")
	has(t, r.Rig, ui.RoleComboBox, "Speaker")
	hasNot(t, r.Rig, ui.RoleComboBox, "Camera") // a voice call
	r.Play(widgettest.Click(ui.RoleComboBox, "Microphone"), widgettest.Idle(2), widgettest.Click(ui.RoleListItem, "Studio Mic"), widgettest.Idle(2))
	if c, ok := w.last(abi.CommandTagSetCallMicrophone); !ok || string(c.SetCallMicrophone.Device) != "mic-1" {
		t.Errorf("picking a microphone sent %v", w.sent())
	}
}

// CA-007, CA-008, CA-009: Hide the call screen and Escape put the call aside
// (the window goes, the call runs); Escape leaves full screen first.
func TestHidingAndFullScreenLeaveTheCallAlone(t *testing.T) {
	w := newWorld(t)
	w.update(incoming(1, abi.CallPhaseActive))
	r := w.window()
	r.Play(widgettest.Click(ui.RoleButton, "Full screen"), widgettest.Idle(2))
	if !r.win.full {
		t.Fatal("not in full screen")
	}
	has(t, r.Rig, ui.RoleButton, "Exit full screen")
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	if r.win.full || !w.h.showing() {
		t.Errorf("Escape: full %v, showing %v", r.win.full, w.h.showing())
	}
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	if w.h.showing() || !w.h.liveCall() || w.count(abi.CommandTagHangupCall) != 0 {
		t.Errorf("the second Escape: showing %v, live %v", w.h.showing(), w.h.liveCall())
	}
	w.h.returnTo()
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleButton, "Hide the call screen"), widgettest.Idle(2))
	if w.h.showing() || !w.h.liveCall() {
		t.Error("Hide the call screen ended the call or kept the window")
	}
}

// CA-010: the farewell says how the call ended.
func TestTheWindowSaysHowTheCallEnded(t *testing.T) {
	w := newWorld(t)
	w.update(incoming(1, abi.CallPhaseActive))
	r := w.window()
	w.update(func(u *abi.CallUpdate) {
		incoming(1, abi.CallPhaseEnded)(u)
		u.Outcome, u.HasOutcome = abi.CallStatusConnectionLost, true
	})
	r.Frames(2)
	shot(t, r.Rig, "ended")
	has(t, r.Rig, ui.RoleLabel, "Connection lost")
	hasNot(t, r.Rig, ui.RoleButton, "Hide the call screen")
}

// CA-017: on a group call everyone has a tile: those on it, you (with the
// red microphone while muted), then the invited, dimmed.
func TestAGroupCallHasATileForEveryone(t *testing.T) {
	w := newWorld(t)
	w.update(func(u *abi.CallUpdate) {
		u.Generation, u.Chat, u.Direction, u.Phase, u.HasGroup, u.Muted = 1, []byte(group), abi.CallDirectionOutgoing, abi.CallPhaseActive, true, true
		u.ActiveForMS, u.HasActiveForMS = 5000, true
		u.Group.Participants = []abi.Participant{{ID: []byte(grace), Joined: true}, {ID: []byte(alan), Joined: true}, {ID: []byte(ada)}}
	})
	r := w.window()
	shot(t, r.Rig, "group")
	for _, n := range []string{"Grace Hopper", "Alan Turing", "You", "Ada Lovelace"} {
		has(t, r.Rig, ui.RoleGroup, n)
	}
	has(t, r.Rig, ui.RoleLabel, "Invited")
	has(t, r.Rig, ui.RoleImage, "Unmute") // our muted microphone
	has(t, r.Rig, ui.RoleLabel, "Engines")
	has(t, r.Rig, ui.RoleLabel, "0:05")
	// A group's voice call offers no camera.
	hasNot(t, r.Rig, ui.RoleButton, "Start video")
	// Tiles follow leaves: Alan leaves.
	w.update(func(u *abi.CallUpdate) {
		u.Generation, u.Chat, u.Direction, u.Phase, u.HasGroup = 1, []byte(group), abi.CallDirectionOutgoing, abi.CallPhaseActive, true
		u.Group.Participants = []abi.Participant{{ID: []byte(grace), Joined: true}}
	})
	r.Frames(2)
	hasNot(t, r.Rig, ui.RoleGroup, "Alan Turing")
	// Past twelve, the last tile counts the rest.
	w.update(func(u *abi.CallUpdate) {
		u.Generation, u.Chat, u.Direction, u.Phase, u.HasGroup = 1, []byte(group), abi.CallDirectionOutgoing, abi.CallPhaseActive, true
		for i := 0; i < 14; i++ {
			u.Group.Participants = append(u.Group.Participants, abi.Participant{ID: append([]byte("1555000010"), byte('a'+i)), Joined: true})
		}
	})
	r.Frames(2)
	has(t, r.Rig, ui.RoleGroup, "+4")
}

// CA-011: during a live call the window takes the newest frame of each
// stream it shows from the call's core (ours, theirs, each member's with a
// camera on) and gives it back in the same frame; a stream whose camera is
// off is not asked for.
func TestTheWindowTakesTheCallsFramesAndGivesThemBack(t *testing.T) {
	w := newWorld(t)
	w.f.SetFrame(core.StreamLocal, fake.TestPattern(32, 18, 1))
	w.f.SetFrame(core.StreamRemote, fake.TestPattern(64, 36, 1))
	w.update(func(u *abi.CallUpdate) {
		incoming(1, abi.CallPhaseActive)(u)
		u.Video, u.CameraOn, u.RemoteVideo = true, true, true
	})
	r := w.window()
	for _, s := range []uint32{core.StreamLocal, core.StreamRemote} {
		if w.f.Acquired(s) == 0 || w.f.Held(s) {
			t.Errorf("stream %d: taken %d times, held %v", s, w.f.Acquired(s), w.f.Held(s))
		}
	}
	n := w.f.Acquired(core.StreamRemote)
	w.update(func(u *abi.CallUpdate) {
		incoming(1, abi.CallPhaseActive)(u)
		u.Video, u.CameraOn = true, true
	})
	r.Frames(2)
	if w.f.Acquired(core.StreamRemote) != n {
		t.Error("their stream was asked for with their camera off")
	}

	w = newWorld(t)
	w.f.SetFrame(core.StreamMember+1, fake.TestPattern(32, 18, 1))
	w.update(func(u *abi.CallUpdate) {
		u.Generation, u.Chat, u.Direction, u.Phase, u.HasGroup = 1, []byte(group), abi.CallDirectionOutgoing, abi.CallPhaseActive, true
		u.Group.Participants = []abi.Participant{{ID: []byte(grace), Joined: true}, {ID: []byte(alan), Joined: true, Video: true}}
	})
	w.window()
	if w.f.Acquired(core.StreamMember+1) == 0 || w.f.Held(core.StreamMember+1) || w.f.Acquired(core.StreamMember) != 0 {
		t.Errorf("members' streams: Alan's taken %d times (held %v), Grace's %d", w.f.Acquired(core.StreamMember+1),
			w.f.Held(core.StreamMember+1), w.f.Acquired(core.StreamMember))
	}
}
