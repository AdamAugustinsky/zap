//go:build !gnui_release

package main

import (
	"testing"

	"gonativeui/ui"
)

// gateRun drives a frameGate through sessions of n frames with a counter
// for the allocations: frame(session, i, in) sets the frame's input and
// returns the allocations it makes and whether it acts through code.
func gateRun(t *testing.T, sessions, n int, frame func(s, i int, in *ui.Input) (allocs uint64, acts uint16)) *gateReport {
	t.Helper()
	g := newFrameGate(sessions)
	var count uint64
	g.read = func() uint64 { return count }
	var in ui.Input
	in.Inside, in.Focused = true, true
	for s := 0; s < sessions; s++ {
		g.sessionStart()
		for i := 0; i < n; i++ {
			g.frameStart(i == 0)
			in.ClearFrame()
			allocs, acts := frame(s, i, &in)
			g.input(&in)
			g.mark(acts)
			count += allocs
		}
		g.sessionEnd()
	}
	return g.report()
}

// The session's link click: the pointer comes to rest on the link, then the
// button goes down and up, and the click hands the address to the OS (7
// allocations in the frame of the release, 2 more in the next); then the
// pointer leaves.
func linkClick(i int, in *ui.Input) uint64 {
	switch i {
	case 10:
		in.MouseX, in.MouseY = 200, 300
	case 20:
		in.SetButton(ui.ButtonLeft, true, 1)
	case 21:
		in.SetButton(ui.ButtonLeft, false, 0)
		return 7
	case 22:
		return 2
	case 30:
		in.MouseX, in.MouseY = 0, 0
	}
	return 0
}

func TestTheGatePassesAllocationsInActionFrames(t *testing.T) {
	r := gateRun(t, 4, 60, func(s, i int, in *ui.Input) (uint64, uint16) {
		n := linkClick(i, in)
		if i == 40 { // a message from the demo core arrives, and is decoded
			return 3, evCore
		}
		if i == 50 { // the session types a character into the editor
			return 1, evScript
		}
		return n, 0
	})
	if f := r.failures(); len(f) != 0 {
		t.Fatalf("the gate failed: %v", f)
	}
	if r.SteadyMallocs != 0 || r.ActionMallocs != 4*(7+2+3+1) {
		t.Errorf("steady %d, action %d mallocs", r.SteadyMallocs, r.ActionMallocs)
	}
	var click *gateFrameReport
	for i := range r.ActionAllocFrames {
		if r.ActionAllocFrames[i].Frame == 21 {
			click = &r.ActionAllocFrames[i]
		}
	}
	if click == nil || click.Mallocs != 28 || click.Sessions != 4 || click.Events != "button" {
		t.Errorf("the click frame's report: %+v", r.ActionAllocFrames)
	}
	// Frames 10, 20, 21, 30, 40 and 50 have events: they and the frame
	// after each are action frames.
	if r.ActionFrames != 4*11 || r.SteadyFrames != 4*(60-11) {
		t.Errorf("%d action and %d steady frames", r.ActionFrames, r.SteadyFrames)
	}
}

func TestTheGateFailsAnAllocationInASteadyFrame(t *testing.T) {
	r := gateRun(t, 4, 60, func(s, i int, in *ui.Input) (uint64, uint16) {
		n := linkClick(i, in)
		if i == 3 { // what -gate-test-alloc does
			n++
		}
		return n, 0
	})
	f := r.failures()
	if len(f) != 2 || len(r.SteadyAllocFrames) != 1 || r.SteadyAllocFrames[0].Frame != 3 {
		t.Fatalf("failures %q, steady frames that allocated %+v", f, r.SteadyAllocFrames)
	}
	// Two frames after the click is steady again.
	r = gateRun(t, 4, 60, func(s, i int, in *ui.Input) (uint64, uint16) {
		n := linkClick(i, in)
		if i == 23 {
			n++
		}
		return n, 0
	})
	if len(r.failures()) == 0 {
		t.Error("an allocation two frames after the click passed")
	}
}

func TestTheGateForgivesARuntimeOneOff(t *testing.T) {
	oneOff := func(s, i int, in *ui.Input) (uint64, uint16) {
		if s == 1 && i == 33 {
			return 1, 0 // a new thread under load, once
		}
		return linkClick(i, in), 0
	}
	if f := gateRun(t, 4, 60, oneOff).failures(); len(f) != 0 {
		t.Errorf("a one-off failed the gate: %v", f)
	}
	again := func(s, i int, in *ui.Input) (uint64, uint16) {
		if (s == 1 || s == 3) && i == 33 {
			return 1, 0 // the same point of two sessions: not a one-off
		}
		return linkClick(i, in), 0
	}
	if f := gateRun(t, 4, 60, again).failures(); len(f) != 1 {
		t.Errorf("a steady frame that allocated in two sessions: %v", f)
	}
}

func TestInputKinds(t *testing.T) {
	var a, b ui.Input
	if k := inputKinds(&a, &b); k != 0 {
		t.Errorf("no change: %s", eventNames(k))
	}
	b.MouseX = 4
	b.SetButton(ui.ButtonLeft, true, 1)
	b.NText = 1
	if k := inputKinds(&a, &b); k != evPointer|evButton|evKey {
		t.Errorf("a move, a press and text: %s", eventNames(k))
	}
}

func TestTheGateStartsAtTheFirstScriptFrame(t *testing.T) {
	for _, tc := range []struct {
		name   string
		allocs uint64
		acts   uint16
	}{
		{"setup only", 0, 0},
		{"first steady frame", 1, 0},
		{"first action frame", 1, evScript},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newFrameGate(1)
			var mallocs uint64
			g.read = func() uint64 { return mallocs }
			g.sessionStart()
			mallocs += 7 // benchmark setup after WindowStart, before Script
			g.frameStart(true)
			g.mark(tc.acts)
			mallocs += tc.allocs // a real allocation in the first script frame
			g.sessionEnd()
			r := g.report()
			steady, action := tc.allocs, uint64(0)
			if tc.acts != 0 {
				steady, action = 0, tc.allocs
			}
			if r.SteadyMallocs != steady || r.ActionMallocs != action {
				t.Fatalf("steady/action = %d/%d, want %d/%d", r.SteadyMallocs, r.ActionMallocs, steady, action)
			}
			if failed := len(r.failures()) != 0; failed != (steady != 0) {
				t.Fatalf("gate failed=%v, want %v", failed, steady != 0)
			}
			if steady != 0 && (len(r.SteadyAllocFrames) != 1 || r.SteadyAllocFrames[0].Frame != 0 || r.SteadyAllocFrames[0].Mallocs != 1) {
				t.Fatalf("first frame lost: %+v", r.SteadyAllocFrames)
			}
		})
	}
}
