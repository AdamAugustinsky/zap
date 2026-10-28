package calls

import (
	"testing"
	"time"

	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/core/fake"
)

// steady fails unless a steady frame of r allocates nothing.
func steady(t *testing.T, what string, r *widgettest.Rig) {
	t.Helper()
	r.Play(widgettest.Settle())
	r.Frames(4)
	if n := testing.AllocsPerRun(20, func() { r.Frame() }); n != 0 {
		t.Errorf("%s: %v allocations per frame", what, n)
	}
}

// pictures gives everyone in the world a profile picture, small and large.
func (w *world) pictures(path string) {
	w.t.Helper()
	for _, jid := range []string{ada, grace, alan, group, me} {
		for _, full := range []bool{false, true} {
			w.emit(ev(abi.EventTagAvatar, func(e *abi.Event) {
				e.Avatar.ID, e.Avatar.Full, e.Avatar.Path, e.Avatar.HasPath = []byte(jid), full, []byte(path), true
			}))
		}
	}
}

// A steady frame allocates nothing: the call window ringing a group call,
// live with video frames coming in (taken and given back each frame), and a
// group call's tiles; the main window on the Calls page with the history,
// the Start call pane and the bar of a call put aside, and with the new-call
// list.
func TestSteadyFramesAllocateNothing(t *testing.T) {
	pic := writePicture(t, "pic.png")
	w := newWorld(t)
	w.pictures(pic)
	w.update(func(u *abi.CallUpdate) {
		u.Generation, u.Chat, u.Direction, u.Phase, u.HasGroup = 1, []byte(group), abi.CallDirectionIncoming, abi.CallPhaseIncoming, true
		u.Group.Caller, u.Group.HasCaller = []byte(grace), true
		u.Group.Participants = []abi.Participant{{ID: []byte(grace), Joined: true}, {ID: []byte(alan), Joined: true}, {ID: []byte(ada)}}
	})
	steady(t, "ringing", w.window().Rig)

	w = newWorld(t)
	w.pictures(pic)
	w.f.SetFrame(core.StreamLocal, fake.TestPattern(32, 18, 1))
	w.f.SetFrame(core.StreamRemote, fake.TestPattern(64, 36, 1))
	w.update(func(u *abi.CallUpdate) {
		incoming(1, abi.CallPhaseActive)(u)
		u.Video, u.CameraOn, u.RemoteVideo, u.ActiveForMS = true, true, true, 61_000
	})
	steady(t, "live video", w.window().Rig)

	w = newWorld(t)
	w.pictures(pic)
	w.update(func(u *abi.CallUpdate) {
		u.Generation, u.Chat, u.Direction, u.Phase, u.HasGroup, u.Video, u.CameraOn = 1, []byte(group), abi.CallDirectionOutgoing, abi.CallPhaseActive, true, true, true
		u.Group.Participants = []abi.Participant{{ID: []byte(grace), Joined: true, Video: true}, {ID: []byte(alan), Joined: true}, {ID: []byte(ada)}}
	})
	steady(t, "group tiles", w.window().Rig)

	w = newWorld(t)
	w.pictures(pic)
	r := w.page()
	w.history(callMsg(grace, "g1", true, w.at(time.Hour), abi.CallStatusAnswered),
		callMsg(ada, "a1", false, w.at(26*time.Hour), abi.CallStatusMissed),
		callMsg(ada, "a2", false, w.at(27*time.Hour), abi.CallStatusMissed),
		callMsg(LinkChat, "l1", true, w.at(10*24*time.Hour), abi.CallStatusAnswered))
	w.update(incoming(1, abi.CallPhaseActive))
	w.h.leave()
	steady(t, "history, Start call, the call bar", r.Rig)
	w.h.acct(w.a).newCall = true
	w.s.Touch()
	steady(t, "new-call list", r.Rig)
}
