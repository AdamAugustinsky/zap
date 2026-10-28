package sys

import (
	"math"
	"testing"

	"gonativeui/app"
	"gonativeui/platform"

	"zapfast/abi"
	"zapfast/state"
)

func (r *rig) ring(chat string, gen uint64) {
	r.t.Helper()
	u := &abi.CallUpdate{Generation: gen, Chat: []byte(chat), Direction: abi.CallDirectionIncoming, Phase: abi.CallPhaseIncoming}
	r.pump(ev(abi.EventTagCall, func(e *abi.Event) { e.Call.Update = *u }))
}

func (r *rig) stopRinging(chat string, gen uint64) {
	r.t.Helper()
	u := &abi.CallUpdate{Generation: gen, Chat: []byte(chat), Direction: abi.CallDirectionIncoming, Phase: abi.CallPhaseActive}
	r.pump(ev(abi.EventTagCall, func(e *abi.Event) { e.Call.Update = *u }))
}

// CA-005: the ringtone plays while a call to us rings, and stops when it is
// answered, declined or given up.
func TestTheRingtonePlaysWhileACallRingsAndStopsWhenItDoes(t *testing.T) {
	r := newRig(t)
	r.ring(ada, 7)
	if !r.tone.on || r.tone.starts != 1 {
		t.Fatalf("on %v starts %d", r.tone.on, r.tone.starts)
	}
	r.ring(ada, 7) // the same ring, updated
	if r.tone.starts != 1 {
		t.Error("the ringtone was started again")
	}
	// A cycle ends: the next is played.
	if !r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvService, Source: platform.SourceMedia}) || r.tone.replays != 1 {
		t.Error("the end of a cycle was not taken")
	}
	r.stopRinging(ada, 7)
	if r.tone.on || r.tone.stops != 1 {
		t.Errorf("on %v stops %d", r.tone.on, r.tone.stops)
	}
	r.ring(ada, 8)
	if !r.tone.on {
		t.Error("the next call is silent")
	}
	r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvWillTerminate})
	if r.tone.on {
		t.Error("the app ended with the ringtone on")
	}
}

// The ringtone follows the notification sound: None, notifications off and
// quiet chats ring silently.
func TestTheRingtoneFollowsTheNotificationSound(t *testing.T) {
	for name, set := range map[string]func(r *rig){
		"the message sound is none": func(r *rig) { r.s.Settings.MessageSound = state.SoundChoice{Sound: state.SoundNone} },
		"notifications are off":     func(r *rig) { r.a.Settings.Notifications = false },
		"the chat's own sound is none": func(r *rig) {
			own := abi.NotificationSound{Tag: abi.NotificationSoundTagNone}
			r.pump(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
				e.ChatUpdated.Chat = abi.Chat{ID: []byte(ada), Name: []byte("Ada Lovelace"), NotificationSound: own, HasNotificationSound: true}
			}))
		},
		"the chat is archived": func(r *rig) {
			r.pump(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
				e.ChatUpdated.Chat = abi.Chat{ID: []byte(ada), Name: []byte("Ada"), Archived: true}
			}))
		},
		"the chat is locked": func(r *rig) {
			r.pump(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
				e.ChatUpdated.Chat = abi.Chat{ID: []byte(ada), Name: []byte("Ada"), Locked: true}
			}))
		},
	} {
		r := newRig(t)
		set(r)
		r.ring(ada, 7)
		if r.tone.on {
			t.Errorf("%s: it rang", name)
		}
	}
}

func TestACustomSoundStillRingsTheRingtoneAndAStrangerRingsWithTheOrdinarySound(t *testing.T) {
	r := newRig(t)
	r.s.Settings.MessageSound = state.SoundChoice{Sound: state.SoundCustom, Path: []byte("/s/ding.wav")}
	r.ring("5@s.whatsapp.net", 3) // no chat row
	if !r.tone.on {
		t.Error("a stranger's call is silent")
	}
}

// Without a notification center the call still rings.
func TestACallRingsWithoutANotificationCenter(t *testing.T) {
	r := newRig(t, func(r *rig) { r.c.available = false })
	r.ring(ada, 7)
	if !r.tone.on || len(r.c.posts) != 0 {
		t.Errorf("on %v posts %d", r.tone.on, len(r.c.posts))
	}
}

func TestARingtoneThatCannotStartDoesNotBreakTheCall(t *testing.T) {
	r := newRig(t)
	r.tone.err = errTest
	r.ring(ada, 7)
	r.stopRinging(ada, 7)
	if r.tone.on || r.tone.stops != 0 {
		t.Errorf("on %v stops %d", r.tone.on, r.tone.stops)
	}
	if len(r.c.posts) != 1 {
		t.Error("the notification went with it")
	}
}

// The tone is the upstream one: two bell notes twice, then a rest, in three
// seconds, quiet and without clicks.
func TestTheRingtoneCycleIsTwoBellNotesTwiceAndARest(t *testing.T) {
	s := ringtoneCycle()
	if len(s) != 3*toneRate {
		t.Fatalf("%d samples", len(s))
	}
	loud := func(from, to float64) float64 {
		var m float64
		for _, v := range s[int(from*toneRate):int(to*toneRate)] {
			m = math.Max(m, math.Abs(float64(v)))
		}
		return m
	}
	if loud(0.01, 0.3) < 0.1 || loud(1.3, 1.5) < 0.05 {
		t.Error("the notes are silent")
	}
	if loud(2.0, 3.0) > 1e-4 {
		t.Errorf("the rest sounds: %v", loud(2.0, 3.0))
	}
	if loud(0, 3) > 0.5 {
		t.Error("too loud")
	}
	if s[0] != 0 || math.Abs(float64(s[len(s)-1])) > 1e-6 {
		t.Error("the cycle clicks at its ends")
	}
	if got := len(ringtonePCM()); got != 2*len(s) {
		t.Errorf("%d bytes of PCM", got)
	}
}
