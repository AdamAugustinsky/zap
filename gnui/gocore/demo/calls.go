package demo

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"zapfast/abi"
)

// Calls, one at a time, as the worker allows: a call placed here rings and
// is answered; one rung in waits for AnswerCall or DeclineCall, and is
// missed after MissedAfter. While a video call is up, each stream that has
// a picture gets a moving test pattern at about 15 frames a second. A
// finished call is filed in its chat and the call log.

// liveCall is the call the demo has up.
type liveCall struct {
	update  abi.CallUpdate
	started time.Time // when the media came up; zero before
	cancel  *cancelFlag
	// caller is who rang, for a call rung in.
	caller string
	// began is when it began, in Unix seconds, for its entry.
	began int64
	frame uint64
	// file is whether its end is filed: not for a call never placed.
	file bool
}

type step uint8

const (
	stepOffer   step = iota // the offer goes out, unless the call was cancelled first
	stepRing                // the peer's phone rings
	stepAnswer              // the peer answers
	stepConnect             // the media is up
	stepMissed              // nobody answered the call rung in
	stepFrame               // the next video frames
)

// live is upstream's CallPhase::is_live.
func live(p abi.CallPhase) bool { return p != abi.CallPhaseEnded && p != abi.CallPhaseFailed }

// placingUpdate is upstream's CallUpdate::placing: ours, not yet offered.
func placingUpdate(generation uint64, chat string, video bool) abi.CallUpdate {
	return abi.CallUpdate{Generation: generation, Chat: bs(chat), Direction: abi.CallDirectionOutgoing, Video: video, Phase: abi.CallPhasePlacing}
}

// notPlaced is the call failed before its offer went out.
func notPlaced(u abi.CallUpdate) abi.CallUpdate {
	u.Phase = abi.CallPhaseFailed
	u.Outcome, u.HasOutcome = abi.CallStatusFailed, true
	return u
}

func participants(ids []string, joined func(string) bool) []abi.Participant {
	ps := make([]abi.Participant, 0, len(ids))
	for _, id := range ids {
		ps = append(ps, abi.Participant{ID: bs(id), Joined: joined(id)})
	}
	return ps
}

// emitUpdate is a Call event, after the frames follow it.
func (s *state) emitUpdate(u abi.CallUpdate, started time.Time) {
	s.e.frames.onUpdate(&u)
	if !started.IsZero() {
		u.ActiveForMS, u.HasActiveForMS = uint64(time.Since(started).Milliseconds()), true
	}
	ev := abi.Event{Tag: abi.EventTagCall}
	ev.Call.Update = u
	s.out.Emit(&ev)
}

// emitCall publishes the call's state; a call that has ended is filed and
// let go.
func (s *state) emitCall() {
	c := s.call
	if c == nil {
		return
	}
	s.emitUpdate(c.update, c.started)
	if !live(c.update.Phase) {
		s.call = nil
		s.fileCall(c)
		s.e.flags.release(c.cancel)
	}
}

// changeCall changes the live call and publishes it.
func (s *state) changeCall(change func(*abi.CallUpdate)) {
	if s.call != nil && live(s.call.update.Phase) {
		change(&s.call.update)
		s.emitCall()
	}
}

func (s *state) emitDevices() {
	d := func(id, label string) abi.Device { return abi.Device{ID: bs(id), Label: bs(label)} }
	ev := abi.Event{Tag: abi.EventTagCallDevices}
	ev.CallDevices.Devices = abi.DeviceList{
		Microphones: []abi.Device{d("BuiltInMicrophoneDevice", "MacBook Pro Microphone"), d("demo-headset-mic", "Demo Headset")},
		Speakers:    []abi.Device{d("BuiltInSpeakerDevice", "MacBook Pro Speakers"), d("desk-speakers", "Desk Speakers"), d("demo-headset", "Demo Headset")},
		Cameras:     []abi.Device{d("0", "FaceTime HD Camera"), d("1", "Desk Camera")},
	}
	s.out.Emit(&ev)
}

// place places a call, or joins a call link, under the UI's generation.
func (s *state) place(chat string, video bool, generation uint64, cancel *cancelFlag, link bool) {
	if s.call != nil {
		// One call at a time: this one is refused as the worker refuses
		// it, and the call that is up goes on.
		s.emitUpdate(notPlaced(placingUpdate(generation, chat, video)), time.Time{})
		s.e.flags.release(cancel)
		return
	}
	u := placingUpdate(generation, chat, video)
	u.CameraOn = video
	nobody := func(string) bool { return false }
	switch {
	case link:
		u.Group, u.HasGroup = abi.GroupCall{Participants: participants(linkMembers, nobody), Link: true}, true
	case strings.HasSuffix(chat, "@g.us"):
		u.Group, u.HasGroup = abi.GroupCall{Participants: participants(membersOf(chat), nobody)}, true
	}
	s.call = &liveCall{update: u, cancel: cancel, began: now(), file: true}
	s.emitDevices()
	s.emitCall()
	s.later(placing, due{kind: dueCall, generation: generation, step: stepOffer})
}

// ring rings a call from caller in chat, numbered as the worker numbers
// the calls it is offered. Busy with another call, it is missed.
func (s *state) ring(chat, caller string, name []byte, hasName, video bool) {
	if s.chat(chat) == nil {
		n := caller
		if hasName {
			n = string(name)
		}
		s.chats = append(s.chats, newChat(chat, n))
	}
	group := strings.HasSuffix(chat, "@g.us")
	generation := nextGeneration()
	if s.call != nil {
		m := callEntry(fmt.Sprintf("%sdemo-busy-%d", callEntryPrefix, generation), chat, caller, false, now(), video, abi.CallStatusMissed, 0, group)
		s.callLog = append([]*message{m}, s.callLog...)
		s.file(m, false)
		return
	}
	u := placingUpdate(generation, chat, video)
	u.Direction = abi.CallDirectionIncoming
	u.Phase = abi.CallPhaseIncoming
	if group {
		ids := slices.Clone(membersOf(chat))
		if !slices.Contains(ids, caller) {
			ids = append([]string{caller}, ids...)
		}
		u.Group, u.HasGroup = abi.GroupCall{Participants: participants(ids, func(id string) bool { return id == caller })}, true
		u.Group.Caller, u.Group.HasCaller = opt(caller)
	}
	s.call = &liveCall{update: u, caller: caller, began: now(), file: true}
	s.emitCall()
	s.later(MissedAfter, due{kind: dueCall, generation: generation, step: stepMissed})
}

func (s *state) answerCall() {
	c := s.call
	if c == nil || c.update.Phase != abi.CallPhaseIncoming {
		return
	}
	c.update.Phase = abi.CallPhaseAccepted
	c.update.CameraOn = c.update.Video
	s.emitDevices()
	s.emitCall()
	s.later(connecting, due{kind: dueCall, generation: c.update.Generation, step: stepConnect})
}

func (s *state) hangup() {
	c := s.call
	if c == nil || !live(c.update.Phase) {
		return
	}
	u := &c.update
	switch {
	case !c.started.IsZero():
		u.Outcome = abi.CallStatusAnswered
	case u.Direction == abi.CallDirectionIncoming:
		u.Outcome = abi.CallStatusMissed
	default:
		u.Outcome = abi.CallStatusNoAnswer
	}
	u.HasOutcome = true
	u.Phase = abi.CallPhaseEnded
	s.emitCall()
}

// callStep is one step of call generation, when it is still the call.
func (s *state) callStep(generation uint64, st step) {
	c := s.call
	if c == nil || c.update.Generation != generation {
		return
	}
	if st == stepFrame {
		s.frameTick(generation)
		return
	}
	if st == stepOffer && c.cancel != nil && c.cancel.cancelled.Load() {
		// Cancelled before the offer went out: never placed.
		c.update = notPlaced(c.update)
		c.file = false
		s.emitCall()
		return
	}
	u := &c.update
	var after time.Duration
	var next step
	hasNext := true
	switch {
	case st == stepOffer:
		if u.HasGroup && u.Group.Link {
			u.Phase, after, next = abi.CallPhaseConnecting, connecting, stepConnect
		} else {
			u.Phase, after, next = abi.CallPhaseDialing, dialing, stepRing
		}
	case st == stepRing && u.Phase == abi.CallPhaseDialing:
		u.Phase, after, next = abi.CallPhaseRinging, ringing, stepAnswer
	case st == stepAnswer && u.Phase == abi.CallPhaseRinging:
		u.Phase, after, next = abi.CallPhaseConnecting, connecting, stepConnect
	case st == stepConnect && (u.Phase == abi.CallPhaseConnecting || u.Phase == abi.CallPhaseAccepted):
		u.Phase = abi.CallPhaseActive
		c.started = time.Now()
		for i := range u.Group.Participants {
			u.Group.Participants[i].Joined = true
		}
		after, next = frameEvery, stepFrame
	case st == stepMissed && u.Phase == abi.CallPhaseIncoming:
		u.Phase = abi.CallPhaseEnded
		u.Outcome, u.HasOutcome = abi.CallStatusMissed, true
		hasNext = false
	default:
		return
	}
	s.emitCall()
	if hasNext {
		s.later(after, due{kind: dueCall, generation: generation, step: next})
	}
}

// frameTick is the next frame of every stream that has a picture: ours
// while our camera is on, the peer's on a video call, and on a group's
// video call the first two members'. A picture's first frame turns its
// flag on, as the worker's does.
func (s *state) frameTick(generation uint64) {
	c := s.call
	if c == nil || c.update.Phase != abi.CallPhaseActive {
		return
	}
	c.frame++
	n := c.frame
	u := &c.update
	var local, remote *i420
	if u.CameraOn {
		local = testPattern(320, 180, 0, n)
	}
	first := false
	type member struct {
		id  string
		pic *i420
	}
	var members []member
	switch {
	case !u.HasGroup && u.Video:
		remote = testPattern(640, 360, 1, n)
		first = first || !u.RemoteVideo
		u.RemoteVideo = true
	case u.HasGroup && u.Video:
		k := 0
		for i := range u.Group.Participants {
			p := &u.Group.Participants[i]
			if !p.Joined || k == 2 {
				continue
			}
			first = first || !p.Video
			p.Video = true
			members = append(members, member{string(p.ID), testPattern(320, 180, 2+k, n)})
			k++
		}
	}
	if local != nil || remote != nil {
		s.e.frames.put(local, remote, "")
	}
	for _, m := range members {
		s.e.frames.put(nil, m.pic, m.id)
	}
	if first {
		s.emitCall()
	}
	s.later(frameEvery, due{kind: dueCall, generation: generation, step: stepFrame})
}

// fileCall files a finished call in its chat and the call log, as the
// worker does.
func (s *state) fileCall(c *liveCall) {
	u := &c.update
	if !u.HasOutcome || !c.file {
		return
	}
	link := u.HasGroup && u.Group.Link
	chat := string(u.Chat)
	if link {
		chat = callLinkChat
	}
	fromMe := u.Direction == abi.CallDirectionOutgoing
	sender := Me
	if !fromMe {
		sender = c.caller
		if sender == "" {
			sender = chat
		}
	}
	var duration uint64
	if !c.started.IsZero() {
		duration = uint64(time.Since(c.started) / time.Second)
	}
	m := callEntry(fmt.Sprintf("%sdemo-%d", callEntryPrefix, u.Generation), chat, sender, fromMe, c.began, u.Video, u.Outcome, duration, u.HasGroup)
	s.callLog = append([]*message{m}, s.callLog...)
	if link {
		s.emitCallLog()
	} else {
		s.file(m, false)
	}
}
