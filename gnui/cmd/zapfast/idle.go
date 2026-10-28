//go:build !gnui_release

package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"gonativeui/app"

	"zapfast/internal/procinfo"
	"zapfast/state"
	"zapfast/sys"
	"zapfast/ui/shell"
)

// -idle: the window open (or closed, with only the status item: resident
// mode), the demo core quiet, nothing touched. The budget is 0 presents and
// at most 1 wakeup a second for the whole process (D51). The counters are
// read on a goroutine that sleeps through the window, so the measurement
// adds one wakeup to it.
//
// The budget is for steady idle, so the window starts no sooner than
// quietBeforeIdle after the last frame: 5 s after an idle window's last
// frame the framework gives back its spare drawables (platform trimAfterNs),
// which costs a few wakeups once.
const quietBeforeIdle = 7 * time.Second

// idleReport is the JSON of -idle.
type idleReport struct {
	Machine    string
	Demo       bool
	Window     string // open, or closed (resident mode)
	StatusItem bool
	SettleS    float64
	QuietS     float64 // since the last frame, when the window began
	Seconds    float64

	Builds             int64   // frames built over the window (every present is a build)
	IdleWakeups        uint64  // package-idle wakeups (Activity Monitor's "Idle Wake Ups")
	Wakeups            uint64  // every interrupt wakeup of the process's threads
	IdleWakeupsPerSec  float64 // the budget: ≤ 1
	WakeupsPerSec      float64
	CPUMs              float64
	Mallocs            uint64 // Go heap allocations over the window
	GCs                uint32 // Go collections over the window
	ScavengedMB        float64
	Goroutines         int
	FootprintMB        float64
	ResidentMB         float64
	ChatsLoaded        bool
	MeasurementWakeups int // the measuring goroutine's own

	Pass     bool
	Failures []string `json:",omitempty"`
}

const (
	postIdleClose = postBudget + 10
	postIdleDone  = postBudget + 11
)

type idleRun struct {
	*shell.App
	o      *budgetOptions
	start  time.Time
	builds atomic.Int64
	last   atomic.Int64 // when the last frame was built, in ns since start
	rep    idleReport
}

func runIdle(o *options, s *state.State, sh *shell.Shell, h *shell.App) (int, error) {
	r := &idleRun{App: h, o: &o.budget, start: time.Now()}
	r.rep = idleReport{Machine: machine(), Demo: o.demo, Window: "open", StatusItem: true, SettleS: o.budget.settle.Seconds(), MeasurementWakeups: 1}
	if o.budget.idleClosed {
		r.rep.Window = "closed (resident mode)"
	}
	sh.Set.Theme = &buildHook{Theme: sh.Set.Theme, fn: func() {
		r.builds.Add(1)
		r.last.Store(int64(time.Since(r.start)))
	}}
	if err := app.Main(sys.Config(AppID, s.Settings.KeepRunning), r); err != nil {
		return 1, err
	}
	if a := s.Acct(); a != nil {
		r.rep.ChatsLoaded = a.Model.Chats.Len() > 0
	}
	rep := &r.rep
	if rep.Builds != 0 {
		rep.Failures = append(rep.Failures, fmt.Sprintf("%d frames built while idle, want 0", rep.Builds))
	}
	if rep.IdleWakeupsPerSec > 1 {
		rep.Failures = append(rep.Failures, fmt.Sprintf("%.2f idle wakeups/s, budget 1", rep.IdleWakeupsPerSec))
	}
	if !rep.ChatsLoaded {
		rep.Failures = append(rep.Failures, "the chat list never loaded")
	}
	rep.Pass = len(rep.Failures) == 0
	printJSON(rep)
	if !rep.Pass {
		fmt.Fprintln(os.Stderr, "zapfast: idle over budget:", strings.Join(rep.Failures, "; "))
		return 3, nil
	}
	return 0, nil
}

func (r *idleRun) Start(a *app.App) error {
	if err := r.App.Start(a); err != nil {
		return err
	}
	go r.measure()
	return nil
}

// measure runs on its own goroutine: settle, close the window if asked,
// settle again, then count over the window.
func (r *idleRun) measure() {
	post := func(id uint64) {
		for !app.Post(app.Event{ID: id}, nil) {
			time.Sleep(time.Millisecond)
		}
	}
	time.Sleep(r.o.settle)
	if r.o.idleClosed {
		post(postIdleClose)
		time.Sleep(r.o.settle)
	}
	r.rep.QuietS = r.waitQuiet().Seconds()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	u0, _ := procinfo.Read()
	b0 := r.builds.Load()
	t0 := time.Now()
	stop := startCPUProfile(r.o.cpuProfile)
	time.Sleep(r.o.idle)
	stop()
	u1, _ := procinfo.Read()
	b1 := r.builds.Load()
	secs := time.Since(t0).Seconds()
	runtime.ReadMemStats(&m1)
	rep := &r.rep
	rep.Mallocs, rep.GCs = m1.Mallocs-m0.Mallocs, m1.NumGC-m0.NumGC
	rep.ScavengedMB = (float64(m1.HeapReleased) - float64(m0.HeapReleased)) / (1 << 20)
	rep.Goroutines = runtime.NumGoroutine()
	rep.Seconds = secs
	rep.Builds = b1 - b0
	rep.IdleWakeups, rep.Wakeups = u1.IdleWakeups-u0.IdleWakeups, u1.Wakeups-u0.Wakeups
	rep.IdleWakeupsPerSec, rep.WakeupsPerSec = float64(rep.IdleWakeups)/secs, float64(rep.Wakeups)/secs
	rep.CPUMs = float64(u1.CPUNs-u0.CPUNs) / 1e6
	rep.FootprintMB, rep.ResidentMB = float64(u1.Footprint)/(1<<20), float64(u1.Resident)/(1<<20)
	post(postIdleDone)
}

// waitQuiet waits until quietBeforeIdle has passed since the last frame, for
// 30 s at most (frames that never stop fail the budget anyway), and returns
// how long ago the last frame was.
func (r *idleRun) waitQuiet() time.Duration {
	for give := time.Now().Add(30 * time.Second); ; {
		quiet := time.Since(r.start) - time.Duration(r.last.Load())
		if quiet >= quietBeforeIdle || !time.Now().Before(give) {
			return quiet
		}
		time.Sleep(quietBeforeIdle - quiet)
	}
}

func (r *idleRun) Event(a *app.App, e *app.Event) {
	if e.Kind == app.EvPost {
		switch e.ID {
		case postIdleClose:
			if w := a.Window(); w != nil {
				w.Close()
			}
			return
		case postIdleDone:
			a.Quit()
			return
		}
	}
	r.App.Event(a, e)
}
