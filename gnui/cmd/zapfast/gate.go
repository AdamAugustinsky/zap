//go:build !gnui_release

package main

import (
	"fmt"
	"runtime"
	"slices"
	"strings"

	"gonativeui/ui"
)

// The allocation gate of the scripted session (D27, as decided for a
// session with user actions; docs/measurements/zapfast-macmini.md, "The
// allocation gate"). Every frame of the measured sessions is classified and
// its allocations counted, from the start of its script to the start of the
// next frame's:
//
//   - an action frame has an event, or follows a frame that had one. The
//     events are input (the pointer, a button, the wheel, a key, text, an
//     edit, focus), a step of the session that acts through code (typing
//     into the editor, asking the demo core for a message or typing, a
//     reaction arriving), and core events drained into the state. The frame
//     after one is included because what an action hands off (a goroutine,
//     the OS) may still be running in it;
//   - every other frame is steady.
//
// Steady frames must not allocate. The gate fails when the steady frames
// allocated in every session, or when a steady frame at the same point of
// the session allocated in two sessions or more: a runtime one-off (a new
// thread under load) lands once, somewhere. Allocations in action frames are
// reported frame by frame, with their events, and never fail the gate.

// The events of a frame, as bits.
const (
	evPointer uint16 = 1 << iota // moved, entered or left the window, a gesture
	evButton
	evWheel
	evKey    // keys, text, edits, commands, a paste, a menu choice
	evFocus  // focus or visibility
	evScript // a step of the session that acts through code
	evCore   // core events drained into the state
	evAfter  // (reports only) the frame after one with an event
)

var evNames = [...]string{"pointer", "button", "wheel", "key", "focus", "script", "core events", "after an event"}

func eventNames(k uint16) string {
	var names []string
	for i, n := range evNames {
		if k&(1<<i) != 0 {
			names = append(names, n)
		}
	}
	return strings.Join(names, ", ")
}

// maxGateFrames bounds the frames of a session the gate keeps per-frame
// counts for (made up front, so counting allocates nothing).
const maxGateFrames = 8192

type gateFrame struct {
	steadyMallocs, actionMallocs   uint64
	steadySessions, actionSessions uint16
	kinds                          uint16
}

// frameGate counts each frame's allocations by class. Its methods run on
// the main thread, in the session's frames, and allocate nothing.
type frameGate struct {
	read func() uint64 // the process's allocations so far

	on      bool   // inside a measured session
	inFrame bool   // a frame of the session is being counted
	last    uint64 // allocations when the current frame started
	cur     uint16 // the current frame's events
	prev    uint16 // the frame before's
	at      int    // the current frame's index in the session

	prevIn ui.Input
	haveIn bool

	session       int
	sessionSteady []uint64 // steady allocations per session
	frames        []gateFrame
	beyond        int // frames past maxGateFrames (counted, not kept per frame)

	steadyFrames, actionFrames   uint64
	steadyMallocs, actionMallocs uint64
}

func newFrameGate(sessions int) *frameGate {
	g := &frameGate{sessionSteady: make([]uint64, sessions), frames: make([]gateFrame, maxGateFrames)}
	var ms runtime.MemStats
	g.read = func() uint64 {
		runtime.ReadMemStats(&ms) // exact (it flushes every P's cache), unlike runtime/metrics
		return ms.Mallocs
	}
	return g
}

// sessionStart and sessionEnd bracket a measured session (app.BenchOptions'
// WindowStart and WindowEnd).
func (g *frameGate) sessionStart() {
	g.on, g.inFrame = true, false
}

func (g *frameGate) sessionEnd() {
	if g.inFrame {
		g.close(g.read())
	}
	g.on, g.inFrame = false, false
	g.session++
}

// frameStart begins a frame of the session (before its script); atStart
// says it is the session's first.
func (g *frameGate) frameStart(atStart bool) {
	if g.inFrame {
		g.close(g.read())
	} else if g.on {
		// WindowStart can precede benchmark setup (including a collection).
		// Count from the first script, not from that setup hook.
		g.last = g.read()
	}
	g.prev, g.cur = g.cur, 0
	g.inFrame = g.on
	if atStart {
		g.at = 0
	} else {
		g.at++
	}
}

// mark adds events to the current frame.
func (g *frameGate) mark(k uint16) { g.cur |= k }

// input adds the frame's input events: what changed since the last frame's
// input, the OS's and the script's alike.
func (g *frameGate) input(in *ui.Input) {
	if g.haveIn {
		g.cur |= inputKinds(&g.prevIn, in)
	}
	g.prevIn, g.haveIn = *in, true
}

var zeroInput ui.Input

func inputKinds(p, c *ui.Input) uint16 {
	var k uint16
	if c.MouseX != p.MouseX || c.MouseY != p.MouseY || c.Inside != p.Inside || c.NGestures > 0 {
		k |= evPointer
	}
	if c.Down != p.Down || c.Pressed != zeroInput.Pressed || c.Released != zeroInput.Released || c.Cancelled != zeroInput.Cancelled {
		k |= evButton
	}
	if c.NScrolls > 0 || c.ScrollX != 0 || c.ScrollY != 0 {
		k |= evWheel
	}
	if c.NKeys > 0 || c.NText > 0 || c.CommitLen > 0 || c.Mods != p.Mods || c.NEdit > 0 || c.NCommands > 0 || c.NActions > 0 ||
		c.HasPaste || c.HasReplace || c.HasMenuChoice || c.HasColorPick {
		k |= evKey
	}
	if c.Focused != p.Focused || c.FocusGained || c.Hidden != p.Hidden {
		k |= evFocus
	}
	return k
}

// close ends the current frame, with the process's allocations at m.
func (g *frameGate) close(m uint64) {
	d := m - g.last
	g.last = m
	kinds := g.cur
	if kinds == 0 && g.prev != 0 {
		kinds = evAfter
	}
	var f *gateFrame
	if g.at < len(g.frames) {
		f = &g.frames[g.at]
	} else {
		g.beyond++
	}
	if kinds == 0 {
		g.steadyFrames++
		g.steadyMallocs += d
		if g.session < len(g.sessionSteady) {
			g.sessionSteady[g.session] += d
		}
		if f != nil && d > 0 {
			f.steadyMallocs += d
			f.steadySessions++
		}
		return
	}
	g.actionFrames++
	g.actionMallocs += d
	if f != nil && d > 0 {
		f.actionMallocs += d
		f.actionSessions++
		f.kinds |= kinds
	}
}

// gateReport is the gate's part of the -bench JSON.
type gateReport struct {
	Sessions                     int
	SteadyFrames, ActionFrames   uint64
	SteadyMallocs, ActionMallocs uint64
	SteadyMallocsBySession       []uint64
	SteadyAllocFrames            []gateFrameReport `json:",omitempty"`
	ActionAllocFrames            []gateFrameReport `json:",omitempty"`
}

// gateFrameReport is one frame of the session that allocated: where it is
// in the session, its events, and its allocations over the sessions.
type gateFrameReport struct {
	Frame    int
	Events   string `json:",omitempty"`
	Mallocs  uint64
	Sessions int
}

func (g *frameGate) report() *gateReport {
	r := &gateReport{Sessions: min(g.session, len(g.sessionSteady)),
		SteadyFrames: g.steadyFrames, ActionFrames: g.actionFrames,
		SteadyMallocs: g.steadyMallocs, ActionMallocs: g.actionMallocs}
	r.SteadyMallocsBySession = g.sessionSteady[:r.Sessions]
	for i := range g.frames {
		f := &g.frames[i]
		if f.steadyMallocs > 0 {
			r.SteadyAllocFrames = append(r.SteadyAllocFrames, gateFrameReport{Frame: i, Mallocs: f.steadyMallocs, Sessions: int(f.steadySessions)})
		}
		if f.actionMallocs > 0 {
			r.ActionAllocFrames = append(r.ActionAllocFrames, gateFrameReport{Frame: i, Events: eventNames(f.kinds), Mallocs: f.actionMallocs, Sessions: int(f.actionSessions)})
		}
	}
	return r
}

// failures are the gate's verdict on the steady frames.
func (r *gateReport) failures() []string {
	var f []string
	if len(r.SteadyMallocsBySession) > 0 && slices.Min(r.SteadyMallocsBySession) > 0 {
		f = append(f, fmt.Sprintf("the steady frames allocated in every one of %d sessions (fewest %d mallocs)",
			len(r.SteadyMallocsBySession), slices.Min(r.SteadyMallocsBySession)))
	}
	for _, s := range r.SteadyAllocFrames {
		if s.Sessions >= 2 {
			f = append(f, fmt.Sprintf("steady frame %d of the session allocated in %d sessions (%d mallocs)", s.Frame, s.Sessions, s.Mallocs))
		}
	}
	return f
}
