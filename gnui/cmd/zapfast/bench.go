//go:build !gnui_release

package main

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gonativeui/app"
	"gonativeui/img"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget/script"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/internal/procinfo"
	"zapfast/state"
	"zapfast/ui/shell"
)

// -bench: the scripted demo session (D51's frames budget) in a real window,
// through app.RunBench and the allocation gate of gate.go (D27: steady
// frames allocate nothing; action frames may). Before the
// session the open chat, Ada Lovelace's, is given a long history (10 000
// messages by default: text of mixed lengths, links, reactions, and a photo
// every tenth message, from files written once to the temporary folder).
// The session, looped:
//
//   - switch chats: Grace Hopper, Katherine Johnson, back to Ada;
//   - scroll the long transcript up and back to the newest message;
//   - hover over the transcript, and over and onto a link;
//   - select text by dragging, and copy it;
//   - type in the composer (its editor's text changes as keys change it:
//     the Go side of typing), then clear it;
//   - receive: an incoming message from the demo core, typing on and off,
//     a reaction added to a message and taken away.
//
// It ends where it began. -session scroll runs the scrolling alone, for the
// scrolling budget (< 4 ms CPU a frame).

const (
	ada   = "393331234567@s.whatsapp.net"
	grace = "441632960123@s.whatsapp.net"
	me    = "15550001111@s.whatsapp.net"

	benchLink = "https://example.com/zapfast"
	benchPics = 24
)

// benchReport is the JSON of -bench.
type benchReport struct {
	Machine   string
	Session   string
	Scrollers string // -scrollers
	Messages  int
	app.BenchReport
	SessionCycles int
	LinksOpened   int64
	FootprintMB   float64 // phys_footprint after the session, two collections and the scavenger's work
	ResidentMB    float64
	// FootprintAt2sMB is phys_footprint 2 s after the collections (the old
	// reading, which raced the scavenger); ScavengeWaitS is when FootprintMB
	// was read.
	FootprintAt2sMB, ScavengeWaitS float64
	// The Go heap then: in use, free but kept from the OS, and stacks.
	GoHeapInuseMB, GoHeapRetainedMB, GoStacksMB float64
	// Images is the main window's image cache after the session;
	// LegacyScrollers the scroll bars' style the window drew then.
	Images          img.Stats
	LegacyScrollers bool

	Gate         *gateReport `json:",omitempty"` // the full session's frames, steady and action (gate.go)
	GateFailures []string    `json:",omitempty"`
}

type bench struct {
	*shell.Shell
	s *state.State
	o *budgetOptions

	p       *script.Player
	session string
	phase   int
	waited  int
	err     error

	pics    []string
	editor  interface{ Editor() *rexui.ComposerState }
	typed   int
	cmd     abi.Command
	ev      abi.Event
	reacted []byte
	text    int
	link    int // the frame of the link's hover and click, 0 outside it
	sem     ui.SemTree
	opened  atomic.Int64
	g       *frameGate
}

var (
	_ app.Scripter     = (*bench)(nil)
	_ app.CycleAligned = (*bench)(nil)
	_ app.Initer       = (*bench)(nil)
)

// openRecorder is the bench's state.Shell: a click on a link counts, and
// opens nothing.
type openRecorder struct{ n *atomic.Int64 }

func (r openRecorder) Open(string) error { r.n.Add(1); return nil }

func runBench(o *budgetOptions, s *state.State, sh *shell.Shell) (int, error) {
	session := o.session
	if o.scrollers != "system" && o.scrollers != "overlay" && o.scrollers != "legacy" {
		return 2, fmt.Errorf("-scrollers %q: want system, overlay or legacy", o.scrollers)
	}
	if o.memProfile != "" {
		runtime.MemProfileRate = 4096 // the heap after the session, in detail (writeHeapProfile)
	}
	b := &bench{Shell: sh, s: s, o: o, session: session}
	opt := app.BenchOptions{Warm: 1200, PostGCWarm: 600, Windows: 12, Timeout: o.timeout}
	b.g = newFrameGate(opt.Windows)
	b.editor, _ = sh.Set.Composer.(interface{ Editor() *rexui.ComposerState })
	s.Shell = openRecorder{&b.opened}
	var err error
	if b.pics, err = benchPictures(); err != nil {
		return 1, err
	}
	steps := b.full()
	if session == "scroll" {
		steps = b.scroll()
	}
	b.p = script.NewPlayer(b, steps, script.Options{Loop: true})
	var as allocSites
	// The full session counts every frame's allocations (gate.go); the
	// scrolling one does not, so its CPU is the frames' alone.
	perFrame := session != "scroll"
	opt.WindowStart = func() {
		if o.allocSites {
			as.start()
		}
		if perFrame {
			b.g.sessionStart()
		}
	}
	opt.WindowEnd = func() {
		if perFrame {
			b.g.sessionEnd()
		}
		if o.allocSites {
			as.end()
		}
	}
	br, err := app.RunBench(shell.Config(s), b, opt)
	if b.err != nil {
		return 1, b.err
	}
	if perr := b.p.Err(); perr != nil {
		return 1, perr
	}
	if err != nil {
		return 1, err
	}
	rep := benchReport{Machine: machine(), Session: session, Scrollers: o.scrollers, Messages: o.messages, BenchReport: br,
		SessionCycles: b.p.Cycles(), LinksOpened: b.opened.Load(), Images: b.ImageStats()}
	if c := b.Context(); c != nil {
		v, _ := c.SystemValues()
		rep.LegacyScrollers = v.LegacyScrollers
	}
	if perFrame {
		rep.Gate = b.g.report()
		printGate(&rep)
	}
	// Two collections with the same live heap: the runtime's background
	// scavenger then aims to keep 10 % of the heap in use as free pages,
	// and returns the rest to the OS at its own pace (about 1 % of a CPU,
	// slower on a loaded machine). Until it has, those pages count in the
	// footprint: read at a fixed delay, the footprint was 141 or 157 MB from
	// run to run. It is read once the scavenger has reached its goal, as it
	// would in an app left alone (at most 20 s).
	runtime.GC()
	runtime.GC()
	time.Sleep(2 * time.Second)
	if u, ok := procinfo.Read(); ok {
		rep.FootprintAt2sMB = float64(u.Footprint) / (1 << 20)
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t0 := time.Now()
	for ms.HeapIdle-ms.HeapReleased > ms.HeapInuse/10+1<<20 && time.Since(t0) < 20*time.Second {
		time.Sleep(250 * time.Millisecond)
		runtime.ReadMemStats(&ms)
	}
	rep.ScavengeWaitS = 2 + time.Since(t0).Seconds()
	if u, ok := procinfo.Read(); ok {
		rep.FootprintMB, rep.ResidentMB = float64(u.Footprint)/(1<<20), float64(u.Resident)/(1<<20)
	}
	rep.GoHeapInuseMB = float64(ms.HeapInuse) / (1 << 20)
	rep.GoHeapRetainedMB = float64(ms.HeapIdle-ms.HeapReleased) / (1 << 20)
	rep.GoStacksMB = float64(ms.StackSys) / (1 << 20)
	if o.memProfile != "" {
		writeHeapProfile(o.memProfile)
	}
	if o.memDetail {
		// footprint(1) on this process: the footprint by category.
		pid := strconv.Itoa(os.Getpid())
		out, err := exec.Command("/usr/bin/footprint", "-p", pid, "-f", "bytes").CombinedOutput()
		fmt.Fprintf(os.Stderr, "zapfast: footprint (%v):\n%s", err, out)
		// The named IOSurfaces (the layer's drawables and the like), with
		// their size and maker.
		out, err = exec.Command("/usr/bin/footprint", "-v", "-p", pid, "-f", "bytes").CombinedOutput()
		fmt.Fprintf(os.Stderr, "zapfast: footprint -v surfaces (%v):\n", err)
		for _, l := range strings.Split(string(out), "\n") {
			if strings.Contains(l, "IOSurface SID") {
				fmt.Fprintln(os.Stderr, "  surface", strings.TrimSpace(l))
			}
		}
		// The malloc heap by kind of block, largest first; started with
		// MallocStackLogging=1, the process has heap(1) name the function
		// that made each kind of untyped block.
		out, err = exec.Command("/usr/bin/heap", "-s", "-q", pid).CombinedOutput()
		fmt.Fprintf(os.Stderr, "zapfast: heap(1) (%v):\n", err)
		for i, l := range strings.Split(string(out), "\n") {
			if i < 70 {
				fmt.Fprintln(os.Stderr, "  heap", l)
			}
		}
		// The malloc zones' use and fragmentation.
		out, err = exec.Command("/usr/bin/vmmap", "--summary", pid).CombinedOutput()
		if _, zones, ok := strings.Cut(string(out), "MALLOC ZONE        "); ok {
			fmt.Fprintf(os.Stderr, "zapfast: vmmap malloc zones (%v):\nMALLOC ZONE%s\n", err, zones)
		}
	}
	if o.gate {
		rep.GateFailures = benchGate(&rep, o)
	}
	printJSON(rep)
	if len(rep.GateFailures) > 0 {
		fmt.Fprintln(os.Stderr, "zapfast: gate failed:", strings.Join(rep.GateFailures, "; "))
		return 3, nil
	}
	return 0, nil
}

// printGate summarises the gate's frames on stderr: the steady frames that
// allocated, the action frames that did (allowed), and the run's allocation
// sites (all of them action frames' or the runtime's when the steady frames
// are clean).
func printGate(r *benchReport) {
	g := r.Gate
	fmt.Fprintf(os.Stderr, "zapfast: %d sessions: %d steady frames, %d mallocs; %d action frames, %d mallocs (allowed)\n",
		g.Sessions, g.SteadyFrames, g.SteadyMallocs, g.ActionFrames, g.ActionMallocs)
	for _, f := range g.SteadyAllocFrames {
		fmt.Fprintf(os.Stderr, "  steady frame %d: %d mallocs in %d sessions\n", f.Frame, f.Mallocs, f.Sessions)
	}
	for _, f := range g.ActionAllocFrames {
		fmt.Fprintf(os.Stderr, "  action frame %d (%s): %d mallocs in %d sessions\n", f.Frame, f.Events, f.Mallocs, f.Sessions)
	}
	for i, s := range r.AllocSites {
		if i == 10 {
			fmt.Fprintf(os.Stderr, "  … %d more sites (-allocsites prints the stacks)\n", len(r.AllocSites)-i)
			break
		}
		fmt.Fprintf(os.Stderr, "  site %s (%s): %d objects, %d bytes\n", s.Func, s.Class, s.Objects, s.Bytes)
	}
}

func benchGate(r *benchReport, o *budgetOptions) []string {
	var f []string
	if r.Frames < 300 {
		f = append(f, fmt.Sprintf("%d measured frames in the session, want at least 300", r.Frames))
	}
	if r.Gate != nil {
		f = append(f, r.Gate.failures()...)
	}
	if r.Session == "scroll" && o.maxCPU > 0 && r.MainThreadCPUus > o.maxCPU {
		f = append(f, fmt.Sprintf("main-thread CPU %.0f µs/frame scrolling, budget %.0f", r.MainThreadCPUus, o.maxCPU))
	}
	if !r.Paced && !o.allowUnpaced {
		f = append(f, fmt.Sprintf("%d frames were not paced (no frame events); -allow-unpaced accepts a sleeping display", r.UnpacedFrames))
	}
	if r.FootprintMB > 150 {
		f = append(f, fmt.Sprintf("phys_footprint %.0f MB after the session, budget 150", r.FootprintMB))
	}
	return f
}

// Init starts the services as app.Main's Start would (the status item,
// notifications, the Dock), then the shell.
func (b *bench) Init(a *app.App) error {
	if y := b.Set.System; y != nil {
		if err := y.Start(a, b.s); err != nil {
			return err
		}
	}
	return b.Shell.Init(a)
}

// AtCycleStart implements app.CycleAligned: the next frame begins the
// session (never during the setup).
func (b *bench) AtCycleStart() bool { return b.phase == phaseReady && b.p.AtStart() }

const (
	phaseChats = iota
	phaseOpen
	phaseHistory
	phaseSettle
	phaseReady
)

// Script implements app.Scripter: the setup, then the session.
func (b *bench) Script(frame int, in *ui.Input) bool {
	if b.err != nil {
		return false
	}
	b.forceScrollers()
	if b.phase == phaseReady {
		g := b.g
		g.frameStart(b.link == 0 && b.p.AtStart())
		if b.o.gateTestAlloc && g.on && g.at == gateTestFrame && g.prev == 0 {
			gateTestSink = new([4]uint64)
		}
		ok := true
		if b.link > 0 {
			b.linkFrame(in)
			ok = b.err == nil
		} else {
			ok = b.p.Script(frame, in)
		}
		g.input(in)
		return ok
	}
	in.Inside, in.Focused = true, true
	b.waited++
	if b.waited > 3000 {
		b.err = fmt.Errorf("bench: the setup is stuck in phase %d", b.phase)
		return false
	}
	a := b.s.Acct()
	switch b.phase {
	case phaseChats:
		if a != nil && a.Model.Linked() && a.Model.Chats.Loaded() && a.Model.Chats.Find([]byte(ada)) != 0 {
			b.phase, b.waited = phaseOpen, 0
		}
	case phaseOpen:
		// The demo core refuses LoadPins, which every chat opened sends (a
		// real core answers it); the bench drops it rather than measure a
		// refusal's log line at every switch.
		a.Conn = benchConn{a.Conn, b.g}
		a.OpenChat(a.Model.Chats.Find([]byte(ada)))
		b.phase, b.waited = phaseHistory, 0
	case phaseHistory:
		if v := a.Conversation(); v != nil && v.Len() > 0 {
			b.history(a)
			b.phase, b.waited = phaseSettle, 0
		}
	case phaseSettle:
		if b.waited > 60 {
			if v := a.Conversation(); v == nil || v.Len() < b.o.messages {
				b.err = errors.New("bench: the long history did not load")
				return false
			}
			b.phase = phaseReady
		}
	}
	return true
}

// forceScrollers makes the main window see the scroll bars' style that
// -scrollers asks for, with the system's other values as they are, until the
// system's appearance changes and the app reads it again.
func (b *bench) forceScrollers() {
	if b.o.scrollers == "system" {
		return
	}
	c := b.Context()
	if c == nil {
		return
	}
	legacy := b.o.scrollers == "legacy"
	if v, _ := c.SystemValues(); v.Known && v.LegacyScrollers == legacy {
		return
	}
	var v platform.SystemValues
	platform.ReadSystemValues(&v)
	v.LegacyScrollers = legacy
	c.SetSystemValues(platform.AppearanceSerial(), &v)
}

// history gives Ada's chat its long history, older than the demo's lines,
// in pages of 250 as the core sends them.
func (b *bench) history(a *state.Account) {
	words := [...]string{"the", "engine", "room", "is", "warm", "tonight", "and", "we", "keep", "going", "👍", "fine", "see", "you", "at", "nine", "*bold*", "_soft_"}
	n := b.o.messages
	start := time.Now().Add(-48*time.Hour).Unix() - int64(n)*240
	const page = 250
	msgs := make([]abi.Message, 0, page)
	var body strings.Builder
	for at := 0; at < n; at += page {
		msgs = msgs[:0]
		for i := at; i < min(at+page, n); i++ {
			var m abi.Message
			m.ID = []byte("bench-" + strconv.Itoa(i))
			m.Chat = []byte(ada)
			m.Timestamp = start + int64(i)*240
			m.FromMe = i%3 == 0
			m.Sender = []byte(ada)
			if m.FromMe {
				m.Sender, m.Status = []byte(me), abi.DeliveryRead
			}
			if i%10 == 5 {
				m.Content.Tag = abi.ContentTagImage
				md := &m.Content.Image.Media
				md.Mime, md.Path, md.HasPath = []byte("image/jpeg"), []byte(b.pics[(i/10)%len(b.pics)]), true
				md.Width, md.HasWidth, md.Height, md.HasHeight = 800, true, 600, true
				md.Size = 60_000
			} else {
				body.Reset()
				for w := 0; w < 3+i*7%23; w++ {
					if w > 0 {
						body.WriteByte(' ')
					}
					body.WriteString(words[(i*31+w*7)%len(words)])
				}
				if i%29 == 0 || i >= n-3 {
					body.WriteString(" " + benchLink)
				}
				m.Content.Tag = abi.ContentTagText
				m.Content.Text.Text = []byte(body.String())
			}
			if i%17 == 0 {
				m.Reactions = []abi.Reaction{{Sender: []byte(ada), Emoji: []byte("❤️")}}
			}
			msgs = append(msgs, m)
		}
		var e abi.Event
		e.Tag = abi.EventTagMessages
		e.Messages = abi.EventMessages{Chat: []byte(ada), Batch: 7001, Last: at+page >= n, Older: true, Complete: true, Messages: msgs}
		a.Event(&e)
	}
}

// The session's steps.
var (
	idle  = script.Idle
	pause = script.Pause
	click = script.Click
	key   = script.Key
	do    = script.Do
)

// scroll is the scrolling session: up through the long transcript (about
// 300 messages) and back down to the newest.
func (b *bench) scroll() []script.Step {
	return []script.Step{
		idle(4),
		script.Wheel(ui.RoleList, "Messages", 60, 300),
		idle(4),
		click(ui.RoleButton, "Newest message"), script.Settle(), idle(10),
	}
}

// full is the whole session.
func (b *bench) full() []script.Step {
	prim := ui.PrimaryMod()
	steps := []script.Step{
		idle(6),
		// switch chats and back
		click(ui.RoleListItem, "Grace Hopper"), idle(20),
		click(ui.RoleListItem, "Katherine Johnson"), idle(20),
		click(ui.RoleListItem, "Ada Lovelace"), script.Settle(), idle(20),
	}
	// scroll the long transcript and come back
	steps = append(steps, b.scroll()...)
	steps = append(steps,
		// hover over the transcript, then over the newest link, and click it
		script.Sweep(ui.RoleList, "Messages", 40), idle(2),
		do(func() { b.link = 1 }), idle(4),
		// select by dragging across a message, and copy
		script.Drag(ui.RoleList, "Messages", -12, 20), idle(4),
		key('C', prim), idle(6),
	)
	// type in the composer, then clear it
	for _, ch := range "on my way 👍" {
		steps = append(steps, b.act(func() { b.typeRune(ch) }), idle(2))
	}
	steps = append(steps, idle(20), b.act(b.clearComposer), idle(10))
	// receive: a message, typing, a reaction
	steps = append(steps,
		b.act(b.incoming), pause(300), script.Settle(), idle(10),
		b.act(func() { b.typing(true) }), pause(200), idle(4),
		b.act(func() { b.typing(false) }), pause(200), idle(4),
		b.act(func() { b.reaction(true) }), idle(10),
		b.act(func() { b.reaction(false) }), idle(10),
	)
	return steps
}

// benchConn is the account's core without LoadPins (see phaseOpen), which
// tells the gate when core events are drained into the state.
type benchConn struct {
	core.Conn
	g *frameGate
}

func (c benchConn) Send(cmd *abi.Command) error {
	if cmd.Tag == abi.CommandTagLoadPins {
		return nil
	}
	return c.Conn.Send(cmd)
}

func (c benchConn) Drain(h core.Handler, budget time.Duration) (int, error) {
	n, err := c.Conn.Drain(h, budget)
	if n > 0 {
		c.g.mark(evCore)
	}
	return n, err
}

// gateTestFrame is the frame of the session -gate-test-alloc allocates in
// (in the opening idle, a steady frame), and gateTestSink keeps what it
// allocates.
const gateTestFrame = 3

var gateTestSink *[4]uint64

// act is a step that acts through code: an event for the gate.
func (b *bench) act(f func()) script.Step {
	return do(func() {
		b.g.mark(evScript)
		f()
	})
}

// linkFrame is a frame of the link step: the pointer goes to the newest
// link in view, rests on it, and clicks it. (A script finds a node by its
// order in the tree, and the first link built may be in a row above the
// view.)
func (b *bench) linkFrame(in *ui.Input) {
	in.Inside, in.Focused = true, true
	switch {
	case b.link == 1:
		x, y, ok := b.newestLink()
		if !ok {
			var names []string
			for i := range b.sem.Nodes {
				if n := &b.sem.Nodes[i]; n.Role == ui.RoleLink {
					names = append(names, fmt.Sprintf("%q@%v", b.sem.Text(n.Name), n.Bounds))
				}
			}
			b.err = fmt.Errorf("bench: no link in view (links: %v)", names)
			return
		}
		in.MouseX, in.MouseY = x, y
	case b.link == 12:
		in.SetButton(ui.ButtonLeft, true, 1)
	case b.link == 13:
		in.SetButton(ui.ButtonLeft, false, 0)
	case b.link >= 24:
		b.link = 0
		return
	}
	b.link++
}

// newestLink is the centre of the lowest link in the transcript's view.
func (b *bench) newestLink() (x, y float32, ok bool) {
	c := b.Context()
	c.Semantics(&b.sem)
	t := &b.sem
	var list [4]float32
	for i := range t.Nodes {
		if n := &t.Nodes[i]; n.Role == ui.RoleList && string(t.Text(n.Name)) == "Messages" {
			list, ok = n.Bounds, true
			break
		}
	}
	if !ok {
		return 0, 0, false
	}
	ok = false
	for i := range t.Nodes {
		n := &t.Nodes[i]
		if n.Role != ui.RoleLink || string(t.Text(n.Name)) != benchLink {
			continue
		}
		cx, cy := (n.Bounds[0]+n.Bounds[2])/2, (n.Bounds[1]+n.Bounds[3])/2
		if cy <= list[1] || cy >= list[3] { // (a link is a span of its text's box: HitReaches knows the box, not the span)
			continue
		}
		if !ok || cy > y {
			x, y, ok = cx, cy, true
		}
	}
	return x, y, ok
}

// typeRune types r at the end of the composer's text.
func (b *bench) typeRune(r rune) {
	if b.editor == nil {
		b.err = errors.New("bench: the composer has no editor")
		return
	}
	st := b.editor.Editor()
	if st == nil {
		b.err = errors.New("bench: the composer has no editor")
		return
	}
	var buf [4]byte
	n := len(st.Text())
	w := encodeRune(buf[:], r)
	if err := st.Replace(platform.TextRange{Start: n, End: n}, buf[:w], platform.TextRange{Start: n + w, End: n + w}); err != nil {
		b.err = fmt.Errorf("bench: typing: %w", err)
	}
}

func (b *bench) clearComposer() {
	st := b.editor.Editor()
	if n := len(st.Text()); n > 0 {
		if err := st.Replace(platform.TextRange{Start: 0, End: n}, nil, platform.TextRange{}); err != nil {
			b.err = fmt.Errorf("bench: clearing the composer: %w", err)
		}
	}
}

func encodeRune(p []byte, r rune) int {
	switch {
	case r < 0x80:
		p[0] = byte(r)
		return 1
	case r < 0x800:
		p[0], p[1] = 0xC0|byte(r>>6), 0x80|byte(r)&0x3F
		return 2
	case r < 0x10000:
		p[0], p[1], p[2] = 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F
		return 3
	}
	p[0], p[1], p[2], p[3] = 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F, 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F
	return 4
}

var incomingTexts = [...]string{"are you coming tonight?", "the train is late again", "see " + benchLink, "ok 👍"}

// incoming asks the demo core for a message from Ada in her chat.
func (b *bench) incoming() {
	a := b.s.Acct()
	c := &b.cmd
	*c = abi.Command{Tag: abi.CommandTagDemoIncoming}
	c.DemoIncoming.Chat, c.DemoIncoming.Sender = adaB, adaB
	c.DemoIncoming.Text = incomingB[b.text%len(incomingB)]
	b.text++
	if err := a.Send(c); err != nil {
		b.err = err
	}
}

// typing asks the demo core for Ada's typing to start or stop.
func (b *bench) typing(on bool) {
	a := b.s.Acct()
	c := &b.cmd
	*c = abi.Command{Tag: abi.CommandTagDemoTyping}
	c.DemoTyping.Chat, c.DemoTyping.Sender, c.DemoTyping.Composing = adaB, adaB, on
	if err := a.Send(c); err != nil {
		b.err = err
	}
}

// reaction adds Ada's 😂 to the newest of the long history's messages, or
// takes it away: the event a reaction from another person arrives as.
func (b *bench) reaction(on bool) {
	a := b.s.Acct()
	v := a.Conversation()
	if v == nil {
		b.err = errors.New("bench: no conversation for the reaction")
		return
	}
	if b.reacted == nil {
		b.reacted = benchID(b.o.messages - 1)
	}
	i := v.Find(b.reacted)
	if i < 0 {
		b.err = errors.New("bench: the message to react to is gone")
		return
	}
	e := &b.ev
	e.Tag = abi.EventTagMessageUpdated
	if err := v.Decode(i, &e.MessageUpdated.Message); err != nil {
		b.err = err
		return
	}
	m := &e.MessageUpdated.Message
	m.Reactions = m.Reactions[:0]
	if on {
		m.Reactions = append(m.Reactions, abi.Reaction{Sender: adaB, Emoji: laughB})
	}
	a.Event(e)
}

// The session's bytes, made once: the commands and events copy what they
// keep, and the session allocates nothing of its own.
var (
	adaB      = []byte(ada)
	laughB    = []byte("😂")
	incomingB = func() (out [len(incomingTexts)][]byte) {
		for i, t := range incomingTexts {
			out[i] = []byte(t)
		}
		return out
	}()
)

func benchID(i int) []byte { return []byte("bench-" + strconv.Itoa(i)) }

// benchPictures writes the session's photos once, to the temporary folder:
// 800 × 600 JPEGs of different colours.
func benchPictures() ([]string, error) {
	dir := filepath.Join(os.TempDir(), "zapfast-bench-pictures")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var out []string
	for i := 0; i < benchPics; i++ {
		p := filepath.Join(dir, fmt.Sprintf("photo-%02d.jpg", i))
		out = append(out, p)
		if _, err := os.Stat(p); err == nil {
			continue
		}
		img := image.NewRGBA(image.Rect(0, 0, 800, 600))
		for y := 0; y < 600; y++ {
			for x := 0; x < 800; x++ {
				img.Set(x, y, color.RGBA{uint8(x*255/800 + i*37), uint8(y*255/600 + i*53), uint8((x + y + i*91) % 256), 255})
			}
		}
		f, err := os.Create(p)
		if err != nil {
			return nil, err
		}
		err = jpeg.Encode(f, img, &jpeg.Options{Quality: 80})
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
