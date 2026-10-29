//go:build !gnui_release

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/script"

	"zapfast/abi"
	"zapfast/state"
	"zapfast/ui/shell"
)

// -demo-tour: upstream's launch tour (DEMO.md "Recording a demo",
// zf:src/demo/tour.rs script), or with -demo-tour-script whats-new its
// what's-new tour (whatsnew.go), played through the real pointer and keyboard
// handlers by a scripted player over the live window: Space starts it (or
// -demo-tour-delay), it makes no sound, holds its last frame, and Space
// plays it again. -demo-tour-frames DIR renders it offscreen instead, one
// PNG a frame at -demo-fps on a virtual clock. The steps are upstream's as
// far as the Go screens and the demo core's data go; the theme changes
// through the state (upstream: the Settings window).

// tourOptions are the tour's flags.
type tourOptions struct {
	on     bool
	script string
	delay  int    // ms before an automatic start; 0: Space starts it
	frames string // offscreen frames to this folder
	fps    int
	size   string // WxH points
	events string // the pointer and shortcut trace, JSON, to this file
	hover  string // X,Y points: a fake pointer held there
}

func (t *tourOptions) register(fs *flag.FlagSet) {
	fs.BoolVar(&t.on, "demo-tour", false, "play upstream's launch tour over the demo (Space starts and replays it)")
	fs.StringVar(&t.script, "demo-tour-script", "launch", "the tour: launch or whats-new")
	fs.IntVar(&t.delay, "demo-tour-delay", 0, "with -demo-tour: start by itself after this many milliseconds")
	fs.StringVar(&t.frames, "demo-tour-frames", "", "with -demo-tour: render the tour offscreen into this folder, a PNG a frame, and quit")
	fs.IntVar(&t.fps, "demo-fps", 30, "with -demo-tour-frames: frames a second of the virtual clock")
	fs.StringVar(&t.size, "demo-size", "", "the demo window's size in points, WxH (as 1280x800)")
	fs.StringVar(&t.hover, "demo-hover", "", "hold a fake pointer at X,Y points over the demo window (the live demo or -demo-shot), to show what hovering shows")
	fs.StringVar(&t.events, "demo-tour-events", "", "with -demo-tour: write the pointer, its clicks and the shortcut labels (not typed text) to this JSON file, for scripts/render-demo.py")
}

// windowSize parses -demo-size; 0, 0 for none.
func (t *tourOptions) windowSize() (w, h float32, err error) {
	if t.size == "" {
		return 0, 0, nil
	}
	ws, hs, ok := strings.Cut(t.size, "x")
	wi, err1 := strconv.Atoi(ws)
	hi, err2 := strconv.Atoi(hs)
	if !ok || err1 != nil || err2 != nil || wi < 400 || hi < 300 {
		return 0, 0, fmt.Errorf("-demo-size %q: want WxH in points, at least 400x300", t.size)
	}
	return float32(wi), float32(hi), nil
}

// hoverAt parses -demo-hover; on is false for none.
func (t *tourOptions) hoverAt() (x, y float32, on bool, err error) {
	if t.hover == "" {
		return 0, 0, false, nil
	}
	xs, ys, ok := strings.Cut(t.hover, ",")
	xf, err1 := strconv.ParseFloat(strings.TrimSpace(xs), 32)
	yf, err2 := strconv.ParseFloat(strings.TrimSpace(ys), 32)
	if !ok || err1 != nil || err2 != nil || xf < 0 || yf < 0 {
		return 0, 0, false, fmt.Errorf("-demo-hover %q: want X,Y in points", t.hover)
	}
	return float32(xf), float32(yf), true, nil
}

// hoverer is the window's Builder with a fake pointer held at x, y points
// (-demo-hover, zf:DEMO.md): hover states show in a live demo and in a
// -demo-shot alike.
type hoverer struct {
	*shell.Shell
	x, y float32
}

var _ app.Scripter = (*hoverer)(nil)

func (h *hoverer) Script(_ int, in *ui.Input) bool {
	sc := h.Shell.Context().Scale()
	in.MouseX, in.MouseY, in.Inside = h.x*sc, h.y*sc, true
	return true
}

// runHover runs the live demo with the pointer held (-demo-hover), in the
// tour's loop (the one with a Scripter: no status item, which suits a demo).
func runHover(s *state.State, h *hoverer) error {
	cfg := shell.Config(s)
	cfg.Title = "Zap Demo"
	_, err := app.RunBench(cfg, h, app.BenchOptions{Warm: 1 << 30, Timeout: 24 * time.Hour})
	return err
}

const (
	tourWaiting = iota
	tourPlaying
	tourHolding
)

// tour is the Builder of a tour's window: the shell, and the player before
// each frame.
type tour struct {
	*shell.Shell
	s      *state.State
	p      *script.Player
	script string // launch or whats-new
	phase  int
	delay  int64 // ns after the first frame: the automatic start, 0 for Space
	start  int64 // the automatic start on the input's clock (Input.Time)
	once   bool  // offscreen: end after one play
	frames int   // offscreen: frames played
	// The opening shot is set up (prepare) while the tour waits, and the
	// play starts only once it has settled: its first frame is the shot,
	// never the state before it.
	prepared bool
	settle   int  // frames the prepared shot has been steady
	asked    bool // Space went down while the shot settled
	// The trace (-demo-tour-events): where it goes, the play's start and
	// the frame's time on the player's clock, and what it has.
	trace       string
	synthetic   bool  // offscreen: the player's clock is the frame count
	t0, clock   int64 // the play's first frame and this one, on the player's clock
	trail       []tourEvent
	px, py      float32
	traceFailed bool
}

// tourEvent is one entry of the trace, as upstream's (zf:src/demo/tour.rs
// Trace): the pointer, a click, or a shortcut's label, at seconds into
// the play, in points.
type tourEvent struct {
	At     float64
	Kind   string
	X, Y   float32
	Button string
	Label  string
}

// MarshalJSON writes an event with its kind's fields only: a pointer and a
// click their place (at the window's corner too), a click its button, keys
// their label.
func (e tourEvent) MarshalJSON() ([]byte, error) {
	switch e.Kind {
	case "keys":
		return json.Marshal(struct {
			At    float64 `json:"at"`
			Kind  string  `json:"kind"`
			Label string  `json:"label"`
		}{e.At, e.Kind, e.Label})
	case "click":
		return json.Marshal(struct {
			At     float64 `json:"at"`
			Kind   string  `json:"kind"`
			X      float32 `json:"x"`
			Y      float32 `json:"y"`
			Button string  `json:"button"`
		}{e.At, e.Kind, e.X, e.Y, e.Button})
	}
	return json.Marshal(struct {
		At   float64 `json:"at"`
		Kind string  `json:"kind"`
		X    float32 `json:"x"`
		Y    float32 `json:"y"`
	}{e.At, e.Kind, e.X, e.Y})
}

// settleFrames is how long the opening shot stays as it is before the play
// starts (its pictures decoded, the transcript at its end).
const settleFrames = 24

var (
	_ app.Scripter = (*tour)(nil)
	_ app.Builder  = (*tour)(nil)
)

func newTour(s *state.State, sh *shell.Shell, synthetic bool) *tour {
	t := &tour{Shell: sh, s: s, synthetic: synthetic}
	t.p = script.NewPlayer(t, t.steps(), script.Options{Synthetic: synthetic})
	return t
}

// ready reports whether the sample chats are in.
func (t *tour) ready() bool {
	a := t.s.Acct()
	return a != nil && a.Model.Linked() && a.Model.Chats.Loaded() && a.Model.Chats.Find([]byte(demoAda)) != 0
}

// shot reports whether the opening shot is on screen as it will play:
// connected (no banner), the open chat's messages in.
func (t *tour) shot() bool {
	a := t.s.Acct()
	if a == nil || a.Model.Link.Status() != abi.LinkStatusTagConnected {
		return false
	}
	c := a.Conversation()
	return c != nil && c.Len() > 0
}

// Script implements app.Scripter: wait for the samples, set the opening
// shot and let it settle, wait for Space (or the delay), play, hold the
// last frame until Space plays it again.
func (t *tour) Script(frame int, in *ui.Input) bool {
	switch t.phase {
	case tourWaiting:
		if t.delay > 0 && t.start == 0 {
			t.start = in.Time + t.delay
		}
		t.asked = space(in) || t.asked
		if !t.ready() {
			return true
		}
		if !t.prepared {
			t.prepare()
			t.prepared, t.settle = true, 0
			return true
		}
		if !t.shot() {
			t.settle = 0
			return true
		}
		if t.settle < settleFrames {
			t.settle++
			return true
		}
		if t.once || t.asked || (t.start > 0 && in.Time >= t.start) {
			t.phase, t.asked = tourPlaying, false
			t.p.Load(t.steps()...)
			// The trace counts from this frame, the first recorded one.
			t.t0, t.clock, t.trail = t.now(frame, in), t.now(frame, in), t.trail[:0]
		}
		return true
	case tourPlaying:
		t.frames++
		t.clock = t.now(frame, in)
		more := t.p.Script(frame, in)
		t.observe(in)
		if !more {
			err := t.p.Err()
			if err != nil {
				fmt.Fprintln(os.Stderr, "zapfast: the tour:", err)
			}
			t.saveTrace(err == nil)
			t.phase = tourHolding
			return !t.once
		}
		return true
	default:
		if space(in) { // again, from the opening shot
			t.phase, t.prepared, t.asked, t.frames = tourWaiting, false, true, 0
		}
		return true
	}
}

// now is the frame's time on the player's clock: in frames mode the
// player's own (a 60th of a second a frame, which the recorded frames
// sample), live the input's.
func (t *tour) now(frame int, in *ui.Input) int64 {
	if t.synthetic {
		return script.Clock(frame)
	}
	return in.Time
}

// observe adds the frame's pointer and clicks to the trace.
func (t *tour) observe(in *ui.Input) {
	if t.trace == "" {
		return
	}
	sc := t.Shell.Context().Scale()
	x, y := in.MouseX/sc, in.MouseY/sc
	at := float64(t.clock-t.t0) / 1e9
	if x != t.px || y != t.py || len(t.trail) == 0 {
		t.px, t.py = x, y
		t.trail = append(t.trail, tourEvent{At: at, Kind: "pointer", X: x, Y: y})
	}
	for b, name := range [...]string{ui.ButtonLeft: "left", ui.ButtonRight: "right"} {
		if in.Pressed[b] {
			t.trail = append(t.trail, tourEvent{At: at, Kind: "click", X: x, Y: y, Button: name})
		}
	}
}

// label puts a shortcut's caption in the trace as its key goes down, as
// upstream's Key cues carry one ("⌘ + ⇧ + F · Search chats").
func (t *tour) label(text string, step script.Step) []script.Step {
	return []script.Step{script.Do(func() {
		if t.trace != "" {
			t.trail = append(t.trail, tourEvent{At: float64(t.clock-t.t0) / 1e9, Kind: "keys", Label: text})
		}
	}), step}
}

// saveTrace writes the trace (-demo-tour-events) once the play ends.
func (t *tour) saveTrace(complete bool) {
	if t.trace == "" {
		return
	}
	c := t.Shell.Context()
	w, h := c.Size()
	data, err := json.Marshal(struct {
		Width    float32     `json:"width"`
		Height   float32     `json:"height"`
		Duration float64     `json:"duration"`
		Complete bool        `json:"complete"`
		Events   []tourEvent `json:"events"`
	}{float32(w) / c.Scale(), float32(h) / c.Scale(), float64(t.clock-t.t0) / 1e9, complete, t.trail})
	if err == nil {
		err = os.WriteFile(t.trace, data, 0o644)
	}
	if err != nil {
		t.traceFailed = true
		fmt.Fprintln(os.Stderr, "zapfast: the tour's trace:", err)
	}
}

// space reports whether Space went down this frame, and takes it.
func space(in *ui.Input) bool {
	for i := 0; i < in.NKeys; i++ {
		if e := &in.Keys[i]; e.Down && !e.Repeat && e.Code == ui.KeySpace && e.Mods == 0 {
			e.Code = 0
			return true
		}
	}
	return false
}

// prepare is the opening shot (zf:src/demo/tour.rs common_setup): dark, the
// chats page, nothing open over it, Ada's chat open.
func (t *tour) prepare() {
	s := t.s
	a := s.Acct()
	s.Settings.Theme = state.ThemeDark
	s.Page, s.Lock.Locked, s.AccountMenu = state.PageChats, false, false
	s.CloseDialog()
	s.Panes = state.Panes{}
	a.Mode = state.ListChats
	a.OpenChat(0)
	a.OpenChat(a.Model.Chats.Find([]byte(demoAda)))
	if len(s.Settings.GiphyKey) == 0 {
		s.Settings.GiphyKey = []byte("zf-demo-key") // the demo core's GIFs
	}
	s.Touch()
}

// typing types text a character every ms milliseconds, as upstream's
// cues do.
func typing(text string, ms int64) []script.Step {
	var out []script.Step
	for _, r := range text {
		out = append(out, script.Type(string(r)), script.Pause(ms))
	}
	return out
}

// steps are the script's.
func (t *tour) steps() []script.Step {
	if t.script == "whats-new" {
		return t.whatsNew()
	}
	return t.launch()
}

// launch is the launch tour (zf:src/demo/tour.rs script, 41 s) on the
// demo core's samples: the chat search, stepping through chats, a reply to
// Ada's voice message with an emoji completed, a GIF found and a sticker
// sent from the picker, a mention completed in Rust Berlin, the list
// folded, the group's information, the shortcut list and the theme.
func (t *tour) launch() []script.Step {
	prim, alt := ui.PrimaryMod(), ui.ModAlt
	click, key, pause := script.Click, script.Key, script.Pause
	light := func(on bool) func() {
		return func() {
			t.s.Settings.Theme = state.ThemeDark
			if on {
				t.s.Settings.Theme = state.ThemeLight
			}
			t.s.Touch()
		}
	}
	var st []script.Step
	add := func(steps ...script.Step) { st = append(st, steps...) }
	add(script.Idle(20)) // the opening shot (prepare, before the play)
	// search the chats, open one, step through them
	// (⌘K is Zap's search over everything; the list's is ⌘⇧F)
	add(t.label("⌘ + ⇧ + F · Search chats", key('F', prim|ui.ModShift))...)
	add(pause(200))
	add(typing("Rust", 25)...)
	// Enter opens the result under the pointer and leaves the search (the
	// Go list keeps a clicked result's search, where upstream's Escape
	// returns to the chat)
	add(pause(300), script.Move(ui.RoleListItem, "Rust Berlin"), pause(350))
	add(t.label("Enter · Open the chat", key(ui.KeyEnter, 0))...)
	add(pause(950))
	add(t.label("⌥ + ↑ · Previous chat", key(ui.KeyUp, alt))...)
	add(pause(500))
	add(t.label("⌥ + ↓ · Next chat", key(ui.KeyDown, alt))...)
	add(pause(500))
	add(t.label("⌥ + ↑ · Previous chat", key(ui.KeyUp, alt))...)
	add(pause(1700))
	add(t.label("⌘ + End · Latest messages", key(ui.KeyEnd, prim))...)
	add(pause(600))
	// reply to Ada's voice message from its menu, an emoji completed
	add(script.RClick(ui.RoleSlider, "Voice message"), pause(450))
	add(click(ui.RoleButton, "Reply"), pause(400))
	add(typing("See you tonight! :smile", 25)...)
	add(pause(400))
	add(t.label("Enter · Complete emoji", key(ui.KeyEnter, 0))...)
	add(pause(600))
	add(t.label("Enter · Send reply", key(ui.KeyEnter, 0))...)
	add(pause(700))
	// a GIF search
	add(click(ui.RoleButton, "Emoji, GIFs, and stickers"), pause(700))
	add(click(ui.RoleTab, "GIF"), pause(600))
	add(click(ui.RoleSearchField, "Search GIFs via GIPHY"), pause(200))
	add(typing("party", 25)...)
	add(pause(400))
	add(t.label("Enter · Search GIFs", key(ui.KeyEnter, 0))...)
	add(pause(700))
	add(script.Move(ui.RoleButton, "demo-gif-fireworks"), pause(500))
	add(t.label("Esc · Close GIF search", key(ui.KeyEscape, 0))...)
	add(pause(2100))
	// a sticker sent
	add(click(ui.RoleButton, "Emoji, GIFs, and stickers"), pause(700))
	add(click(ui.RoleTab, "Stickers"), pause(600))
	add(script.Move(ui.RoleButton, "Sticker"), pause(500))
	add(click(ui.RoleButton, "Sticker"), pause(1700))
	// the next chat, a mention completed
	add(t.label("⌥ + ↓ · Next chat", key(ui.KeyDown, alt))...)
	add(pause(600))
	add(typing("@gr", 60)...)
	add(pause(500))
	add(t.label("Enter · Complete mention", key(ui.KeyEnter, 0))...)
	add(pause(300))
	add(typing(" see you in the front row!", 25)...)
	add(pause(500))
	add(t.label("Enter · Send message", key(ui.KeyEnter, 0))...)
	add(pause(1000))
	// fold the list and bring it back
	add(t.label("⌘ + B · Hide chat list", key('B', prim))...)
	add(pause(1000))
	add(t.label("⌘ + B · Show chat list", key('B', prim))...)
	add(pause(1000))
	// the group's information
	add(click(ui.RoleButton, "Rust Berlin"), pause(1500))
	add(t.label("Esc · Close group info", key(ui.KeyEscape, 0))...)
	add(pause(600))
	// the shortcut list
	add(t.label("⌘ + / · Keyboard shortcuts", key('/', prim))...)
	add(pause(3200))
	add(t.label("Esc · Close shortcuts", key(ui.KeyEscape, 0))...)
	add(pause(700))
	// the light theme and back (upstream: through Settings)
	add(script.Do(light(true)), pause(1500))
	add(script.Do(light(false)), pause(1500))
	add(t.label("⌥ + ↑ · Previous chat", key(ui.KeyUp, alt))...)
	add(pause(2500))
	return st
}

// runTour plays the tour over a live window, or renders its frames
// offscreen with -demo-tour-frames.
func runTour(o *tourOptions, s *state.State, sh *shell.Shell) error {
	if o.script != "launch" && o.script != "whats-new" {
		return fmt.Errorf("-demo-tour-script %q: launch or whats-new", o.script)
	}
	if o.frames != "" {
		return tourFrames(o, s, sh)
	}
	t := newTour(s, sh, false)
	t.script, t.trace = o.script, o.events
	if o.delay > 0 {
		t.delay = int64(o.delay) * int64(time.Millisecond)
	}
	cfg := shell.Config(s)
	cfg.Title = "Zap Demo"
	// The bench's loop is the live loop with a Scripter: it ends when the
	// window closes; nothing is measured (the warm-up never ends).
	_, err := app.RunBench(cfg, t, app.BenchOptions{Warm: 1 << 30, Timeout: 24 * time.Hour})
	return err
}

// tourFrames renders the tour offscreen, a frame every 1/fps s of a
// virtual clock, into o.frames as 00000.png, 00001.png, …
func tourFrames(o *tourOptions, s *state.State, sh *shell.Shell) error {
	if o.fps <= 0 || o.fps > 120 {
		return fmt.Errorf("-demo-fps %d: want 1 to 120", o.fps)
	}
	if err := os.MkdirAll(o.frames, 0o755); err != nil {
		return err
	}
	sh.Traffic = true
	t := newTour(s, sh, true)
	t.script, t.once, t.trace = o.script, true, o.events
	cfg := shell.Config(s)
	off, err := app.NewOffscreen(cfg, int(cfg.Width*2), int(cfg.Height*2), 2, t)
	if err != nil {
		return err
	}
	defer off.Close()
	off.UI.SetSystemAppearance(ui.Appearance{Dark: true, Known: true})
	// The player's clock is 60 frames a second; one in 60/fps is kept.
	every := max(60/o.fps, 1)
	n := 0
	for i := 0; i < 60*120; i++ {
		img, more := off.Frame()
		if t.phase == tourWaiting {
			time.Sleep(5 * time.Millisecond) // the demo core's chats arrive
			continue
		}
		if t.frames%every == 0 {
			f, err := os.Create(filepath.Join(o.frames, fmt.Sprintf("%05d.png", n)))
			if err != nil {
				return err
			}
			err = png.Encode(f, img)
			f.Close()
			if err != nil {
				return err
			}
			n++
		}
		if !more || t.phase == tourHolding {
			break
		}
	}
	fmt.Fprintf(os.Stderr, "zapfast: %d tour frames in %s\n", n, o.frames)
	return nil
}
