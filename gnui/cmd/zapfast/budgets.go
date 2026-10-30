//go:build !gnui_release

package main

// The app budgets (D51, docs/GNUI-MIGRATION-PLAN.md "App budgets"), measured
// on the built binary:
//
//	zapfast -demo -exit-first   print the startup report as JSON and quit once the first frame is on glass
//	zapfast -demo -startup      the same, and quit once the chat list is on screen (exec → chats shown)
//	zapfast -demo -idle 10s     open the window, let it settle (-settle, 8 s, and 7 s past the last frame,
//	                            after the framework's drawable trim), count the frames built, the wakeups
//	                            and the CPU over 10 s, print them as JSON and quit; -idle-closed closes
//	                            the main window first (resident mode, the status item only)
//	zapfast -demo -bench        the scripted session (bench.go) in a real window: allocations, CPU per
//	                            frame and pacing as JSON; -gate exits with status 3 unless 0 allocations,
//	                            scrolling CPU within -max-cpu-us and paced frames
//
// Measurement modes are development-only (!gnui_release).
//
// The startup report adds to the framework's (app.StartupReport, which
// app.Main does not return) what happens before app.Main and what the first
// frame showed: the gate is a first frame on glass by 150 ms whose chat list
// has its rows and no skeleton row.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget"

	"zapfast/core"
	"zapfast/i18n"
	"zapfast/internal/procinfo"
	"zapfast/state"
	"zapfast/sys"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

// budgetOptions are the flags of the measurement modes.
type budgetOptions struct {
	startup, exitFirst bool

	idle       time.Duration
	idleClosed bool
	settle     time.Duration

	bench, gate, allowUnpaced, allocSites bool
	gateTestAlloc, memDetail              bool
	maxCPU                                float64
	messages                              int
	session, scrollers                    string
	timeout                               time.Duration
	cpuProfile, memProfile                string
}

func (b *budgetOptions) register(fs *flag.FlagSet) {
	fs.BoolVar(&b.startup, "startup", false, "print the startup report as JSON and quit once the chat list is on screen")
	fs.BoolVar(&b.exitFirst, "exit-first", false, "print the startup report as JSON and quit once the first frame is on glass")
	fs.DurationVar(&b.idle, "idle", 0, "let the window settle into steady idle (see -settle), then print the frames built, wakeups and CPU over this long as JSON, and quit")
	fs.BoolVar(&b.idleClosed, "idle-closed", false, "with -idle: close the main window first (resident mode)")
	fs.DurationVar(&b.settle, "settle", 8*time.Second, "with -idle: how long to let the app settle before measuring; the window also starts no sooner than 7 s after the last frame, past the framework's drawable trim (5 s after it, a few wakeups once), so it measures steady idle. Read the idle footprint 60 s or more after launch (-settle 50s): macOS charges the trimmed drawables to the app until about 30 s after launch")
	fs.BoolVar(&b.bench, "bench", false, "the scripted demo session in a real window; print allocations, CPU and pacing as JSON")
	fs.BoolVar(&b.gate, "gate", false, "with -bench: exit with status 3 unless the steady frames allocate nothing, CPU is within -max-cpu-us and frames are paced")
	fs.BoolVar(&b.memDetail, "memdetail", false, "with -bench: print footprint(1)'s categories of the process after the session")
	fs.BoolVar(&b.gateTestAlloc, "gate-test-alloc", false, "with -bench: allocate once a session in a steady frame (a test of the gate, which must fail)")
	fs.BoolVar(&b.allowUnpaced, "allow-unpaced", false, "with -gate: accept unpaced frames (no frame events: a sleeping display)")
	fs.Float64Var(&b.maxCPU, "max-cpu-us", 4000, "with -gate: the scrolling frames' CPU budget in µs per frame (0: not gated)")
	fs.IntVar(&b.messages, "messages", 10_000, "with -bench: the long transcript's messages")
	fs.StringVar(&b.scrollers, "scrollers", "system", "with -bench: the scroll bars' style: system, overlay or legacy (always shown)")
	fs.StringVar(&b.session, "session", "full", "with -bench: full (the whole scripted session) or scroll (the long transcript's scrolling alone)")
	fs.DurationVar(&b.timeout, "timeout", 240*time.Second, "with -bench: give up after this long")
	fs.BoolVar(&b.allocSites, "allocsites", false, "with -bench: profile every allocation and print the stacks of those in the measured frames")
	fs.StringVar(&b.memProfile, "memprofile", "", "with -bench: write a heap profile (sampled every 4 KB) after the session to this file")
	fs.StringVar(&b.cpuProfile, "cpuprofile", "", "write a CPU profile of the run (with -idle: of the measured window) to this file")
}

func (b *budgetOptions) on() bool { return b.startup || b.exitFirst || b.idle > 0 || b.bench }

// mainNs is platform.Now as package main's init runs, just before main;
// execNs is the process's start on the same clock (-1 unknown).
var mainNs, execNs int64

func init() {
	mainNs = platform.Now()
	execNs = -1
	if age := procinfo.AgeNs(); age >= 0 {
		execNs = mainNs - age
	}
}

// runBudgets runs the measurement mode o asks for, in place of app.Main, and
// returns the exit status.
func runBudgets(o *options, s *state.State, sh *shell.Shell, h *shell.App) int {
	b := &o.budget
	if b.idle == 0 { // -idle profiles its window alone
		defer startCPUProfile(b.cpuProfile)()
	}
	var err error
	code := 0
	switch {
	case b.bench:
		if !o.demo {
			err = fmt.Errorf("-bench runs on the demo core: add -demo")
			break
		}
		code, err = runBench(b, s, sh)
	case b.idle > 0:
		code, err = runIdle(o, s, sh, h)
	default:
		code, err = runStartup(o, s, sh, h)
	}
	if err != nil { // first: the reason is out whatever the closing does
		fmt.Fprintln(os.Stderr, "zapfast:", err)
		code = 1
	}
	closeCores(s)
	s.CloseSettings()
	return code
}

// buildHook runs fn at the start of every window's Build (every frame the
// app builds), then the theme: it wraps the Theme slot, which the shell and
// each window apply first thing in Build.
type buildHook struct {
	screen.Theme
	fn func()
}

func (b *buildHook) Apply(k *widget.Kit, s *state.State) {
	b.fn()
	if b.Theme != nil {
		b.Theme.Apply(k, s)
	}
}

// initHook runs fn when the shell's Init starts (it initialises the
// reducers before the screens).
type initHook struct {
	screen.Reducer
	fn func()
}

func (r *initHook) Init(s *state.State) {
	r.fn()
	if r.Reducer != nil {
		r.Reducer.Init(s)
	}
}

func (r *initHook) Frame(s *state.State) {
	if r.Reducer != nil {
		r.Reducer.Frame(s)
	}
}

// postBudget is the app post of the measurement modes.
const postBudget = 0x7a66_4255_0001

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func msSince(t0, t1 int64) float64 {
	if t0 <= 0 || t1 <= 0 {
		return -1
	}
	return float64(t1-t0) / 1e6
}

// ---- startup ----

// startupReport is the JSON of -startup and -exit-first. Times are
// milliseconds; -1 is unknown. The stages add up to ExecToOnGlassMs.
type startupReport struct {
	Machine string
	Demo    bool

	ExecToMainMs      float64 // process start → main (the runtime and every package's init)
	MainToAppMainMs   float64 // main → app.Main: flags, sys.Launch, the settings, shell.New, the cores' open begun
	AppMainToStartMs  float64 // app.Main → Handler.Start (the application object)
	StartToOpenMs     float64 // Start → the window starts opening (sys.Start: notifications, status item, login, instance)
	OpenMs            float64 // platform.Open (NSApplication, NSWindow, the view)
	OpenToInitMs      float64 // → the shell's Init (surface, renderer, text system, ui context)
	InitMs            float64 // the shell's Init: the kit, icons, every screen's Init
	FirstBuildToEndMs float64 // the first frame: two builds, the encode and the commit
	CommitToGlassMs   float64 // committed → on the display

	ExecToFirstPresentMs float64
	ExecToOnGlassMs      float64 // the budget: ≤ 150 ms warm
	BudgetMs             float64

	OpenStages map[string]float64 // platform.Open's stages, ms from exec

	// The account's core, opened on a goroutine: for a real account,
	// loading SQLCipher and any migration, then gocore.Open; the demo has
	// no load.
	CoreLibrary      string
	CoreLoadMs       float64
	CoreOpenMs       float64 // gocore.Open
	ExecToCoreOpenMs float64

	// What the first frame showed: the gate needs chat rows and no
	// skeleton row (the content-first rule).
	FirstFrameChatRows int
	FirstFrameSkeleton bool
	FirstFrameComplete bool
	ExecToChatsShownMs float64 // -startup: the first frame built with the chat list's rows (-1: not in this run)

	Pass     bool
	Failures []string `json:",omitempty"`
}

type startupRun struct {
	*shell.App
	o       *options
	sh      *shell.Shell
	pending *core.Pending
	rep     startupReport

	tAppMain, tStart, tStarted, tInit, tFirstBuild int64
	builds                                         int
	tChats                                         int64
	glassed                                        bool
	sem                                            ui.SemTree
	surfDone                                       atomic.Bool
}

func runStartup(o *options, s *state.State, sh *shell.Shell, h *shell.App) (int, error) {
	r := &startupRun{App: h, o: o, sh: sh}
	r.rep.Machine, r.rep.Demo, r.rep.BudgetMs = machine(), o.demo, 150
	sh.Set.Theme = &buildHook{Theme: sh.Set.Theme, fn: r.build}
	sh.Set.Logic = &initHook{Reducer: sh.Set.Logic, fn: func() { r.tInit = platform.Now() }}
	if a := s.Acct(); a != nil {
		r.pending = a.Pending // the shell takes it from the account once it is open
	}
	r.tAppMain = platform.Now()
	err := app.Main(sys.Config(AppID, s.Settings.KeepRunning), r)
	if err != nil {
		return 1, err
	}
	r.finish()
	printJSON(r.rep)
	if !r.rep.Pass {
		fmt.Fprintln(os.Stderr, "zapfast: startup over budget:", strings.Join(r.rep.Failures, "; "))
		return 3, nil
	}
	return 0, nil
}

// build runs first thing in every Build.
func (r *startupRun) build() {
	r.builds++
	now := platform.Now()
	if r.builds == 1 {
		r.tFirstBuild = now
	}
	// The model at the start of a build is what the build before it
	// applied and showed: rows then means that frame had them.
	if r.tChats == 0 && r.builds > 2 {
		if a := r.S.Acct(); a != nil && a.Model.Chats.Len() > 0 {
			r.tChats = now
		}
	}
}

func (r *startupRun) Start(a *app.App) error {
	r.tStart = platform.Now()
	err := r.App.Start(a)
	r.tStarted = platform.Now()
	if err != nil {
		return err
	}
	// The first frame was built and committed in OpenWindow: its semantic
	// tree is the shell's context's now.
	if c := r.sh.Context(); c != nil {
		c.Semantics(&r.sem)
		skeleton := i18n.T("Loading your chats")
		for i := range r.sem.Nodes {
			n := &r.sem.Nodes[i]
			switch {
			case n.Role == ui.RoleListItem && len(r.sem.Text(n.Name)) > 0:
				r.rep.FirstFrameChatRows++
			case n.Role == ui.RoleGroup && string(r.sem.Text(n.Name)) == skeleton:
				r.rep.FirstFrameSkeleton = true
			}
		}
	}
	if r.rep.FirstFrameChatRows > 0 && !r.rep.FirstFrameSkeleton {
		r.tChats = r.tFirstBuild
	}
	// Wait off the main thread for the present feedback, then post.
	var surf *atomic.Int64
	if w := a.Window(); w != nil && w.App() != nil && w.App().Surf != nil {
		surf = &w.App().Surf.Trace.OnGlass
	}
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for surf != nil && surf.Load() == 0 && time.Now().Before(deadline) {
			time.Sleep(250 * time.Microsecond)
		}
		for !app.Post(app.Event{ID: postBudget}, nil) {
			time.Sleep(time.Millisecond)
		}
	}()
	return nil
}

func (r *startupRun) Event(a *app.App, e *app.Event) {
	if e.Kind == app.EvPost && e.ID == postBudget {
		r.glassed = true
		r.fill(a)
		if r.o.budget.exitFirst || r.tChats != 0 {
			a.Quit()
			return
		}
		// -startup: wait for the chat list, at most 5 s.
		go func() {
			for i := 0; i < 1000; i++ {
				time.Sleep(5 * time.Millisecond)
				for !app.Post(app.Event{ID: postBudget + 1}, nil) {
					time.Sleep(time.Millisecond)
				}
			}
		}()
		return
	}
	if e.Kind == app.EvPost && e.ID == postBudget+1 {
		if r.tChats != 0 || platform.Now()-r.tStarted > int64(5*time.Second) {
			a.Quit()
		} else if w := a.Window(); w != nil {
			w.Wake()
		}
		return
	}
	r.App.Event(a, e)
}

// fill reads the first window's timeline once it is on glass.
func (r *startupRun) fill(a *app.App) {
	rep := &r.rep
	rep.ExecToMainMs = msSince(execNs, mainNs)
	rep.AppMainToStartMs = msSince(r.tAppMain, r.tStart)
	stages := platform.OpenTimeline()
	tOpen0, tOpen1 := int64(0), int64(0)
	if len(stages) > 0 {
		tOpen0, tOpen1 = stages[0].T, stages[len(stages)-1].T
		rep.OpenStages = map[string]float64{}
		for _, st := range stages {
			rep.OpenStages[st.Name] = msSince(execNs, st.T)
		}
	}
	rep.StartToOpenMs = msSince(r.tStart, tOpen0)
	rep.OpenMs = msSince(tOpen0, tOpen1)
	rep.OpenToInitMs = msSince(tOpen1, r.tInit)
	rep.InitMs = msSince(r.tInit, r.tFirstBuild)
	rep.ExecToFirstPresentMs, rep.ExecToOnGlassMs = -1, -1
	if w := a.Window(); w != nil && w.App() != nil && w.App().Surf != nil {
		tr := &w.App().Surf.Trace
		rep.FirstBuildToEndMs = msSince(r.tFirstBuild, tr.Done)
		rep.ExecToFirstPresentMs = msSince(execNs, tr.Committed)
		if g := tr.OnGlass.Load(); g != 0 {
			rep.CommitToGlassMs = msSince(tr.Done, g)
			rep.ExecToOnGlassMs = msSince(execNs, g)
		}
	}
}

func (r *startupRun) finish() {
	rep := &r.rep
	rep.MainToAppMainMs = msSince(mainNs, r.tAppMain)
	rep.ExecToChatsShownMs = msSince(execNs, r.tChats)
	rep.FirstFrameComplete = rep.FirstFrameChatRows > 0 && !rep.FirstFrameSkeleton
	rep.CoreLoadMs, rep.CoreOpenMs, rep.ExecToCoreOpenMs = -1, -1, -1
	if p := r.pending; p != nil && p.Done() {
		t := p.Timing
		rep.CoreLibrary = t.Library
		rep.CoreLoadMs = float64(t.Loaded.Sub(t.Began).Microseconds()) / 1000
		rep.CoreOpenMs = float64(t.Opened.Sub(t.Loaded).Microseconds()) / 1000
		if exec, ok := procinfo.ExecTime(); ok {
			rep.ExecToCoreOpenMs = float64(t.Opened.Sub(exec).Microseconds()) / 1000
		}
	}
	if !r.glassed {
		rep.Failures = append(rep.Failures, "the first frame never reached the display")
	}
	if rep.ExecToOnGlassMs < 0 || rep.ExecToOnGlassMs > rep.BudgetMs {
		rep.Failures = append(rep.Failures, fmt.Sprintf("exec → first frame on glass %.1f ms, budget %.0f", rep.ExecToOnGlassMs, rep.BudgetMs))
	}
	if !rep.FirstFrameComplete {
		rep.Failures = append(rep.Failures, fmt.Sprintf("the first frame is not complete: %d chat rows, skeleton %v", rep.FirstFrameChatRows, rep.FirstFrameSkeleton))
	}
	rep.Pass = len(rep.Failures) == 0
}
