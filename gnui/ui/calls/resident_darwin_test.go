//go:build darwin

package calls

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

const residentStep = 0xCA004

func TestMain(m *testing.M) { widgettest.RunMain(m) }

// residentCall observes real window creation/closure, not just Win.Want.
// All fields except the wake pointer are confined to the app thread.
type residentCall struct {
	*shell.App
	f                             *fake.Core
	h                             *hub
	mainID                        uint32
	frames, stage, opened, closed int
	missed                        bool
	err                           string
}

type residentWindow struct {
	*Window
	r *residentCall
}

func (w *residentWindow) Build(e *screen.Env) {
	w.Window.Build(e)
	r := w.r
	if !r.h.showing() {
		return
	}
	if r.stage == 1 || r.stage == 3 {
		r.opened++
		r.stage++
		app.Post(app.Event{ID: residentStep}, nil)
	}
	if r.h.call.u.HasOutcome && r.h.call.u.Outcome == abi.CallStatusMissed {
		r.missed = true
	}
}

func (r *residentCall) ring(gen uint64) {
	if err := r.f.Emit(ev(abi.EventTagCall, func(e *abi.Event) {
		incoming(gen, abi.CallPhaseIncoming)(&e.Call.Update)
	})); err != nil {
		r.err = err.Error()
	}
}

func (r *residentCall) Event(a *app.App, e *app.Event) {
	if e.Kind == app.EvPost && e.ID == residentStep {
		switch r.stage {
		case 2:
			r.h.decline()
			r.Shell.WakeWindows()
		case 4:
			r.f.Emit(ev(abi.EventTagCall, func(e *abi.Event) {
				incoming(2, abi.CallPhaseEnded)(&e.Call.Update)
				e.Call.Update.HasOutcome, e.Call.Update.Outcome = true, abi.CallStatusMissed
			}))
		}
		return
	}
	if e.Kind == app.EvPost && e.ID == residentStep+1 {
		a.Quit()
		return
	}
	r.App.Event(a, e)
	if e.Kind != app.EvWindowClosed {
		return
	}
	if e.Window == r.mainID {
		r.stage = 1
		r.ring(1) // the main window has closed; no window is open
	} else {
		r.closed++
		if r.stage == 2 {
			r.stage = 3
			r.ring(2) // resident again after Decline
		} else if r.stage == 4 {
			r.stage = 5
			a.Quit()
		}
	}
}

// CA-004: the fake core rings with no window open. Only the call window
// opens, Decline closes it, and a second missed call closes after farewell.
func TestLiveIncomingCallsOpenAndCloseWithoutTheMainWindow(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens windows: set ZAPFAST_LIVE=1")
	}
	s := state.New()
	s.Shell = noShell{t}
	r := &residentCall{h: hubOf(s)}
	r.h.tone = &fakeTone{}
	var wake atomic.Pointer[shell.App]
	r.f = fake.New(func() {
		if a := wake.Load(); a != nil {
			a.Wake()
		}
	})
	s.AddAccount("1", nil, r.f)
	tick := screen.TickFunc(func(e *screen.Env) {
		if r.stage != 0 {
			r.err = "the main window reopened for the call"
			return
		}
		r.frames++
		if r.frames >= 5 {
			r.mainID = e.A.Window().ID()
			e.A.Window().Close()
		} else {
			e.A.Window().Wake()
		}
	})
	sh, err := shell.New(s, &screen.Set{Call: &residentWindow{NewWindow(), r}, Tickers: []screen.Ticker{tick}})
	if err != nil {
		t.Fatal(err)
	}
	r.App = shell.NewApp(sh)
	s.Wake = r.App.Wake
	wake.Store(r.App)
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			app.Post(app.Event{ID: residentStep + 1}, nil)
		}
	}()
	widgettest.OnMain(func() {
		err = app.Main(app.AppConfig{ID: "dev.zapfast.calls-resident-test", KeepRunning: true}, r)
	})
	close(done)
	<-stopped
	if err != nil {
		t.Fatal(err)
	}
	if r.err != "" || r.stage != 5 || r.opened != 2 || r.closed != 2 || !r.missed {
		t.Fatalf("resident lifecycle: stage=%d opened=%d closed=%d missed=%v error=%s", r.stage, r.opened, r.closed, r.missed, r.err)
	}
	if !containsTag(r.f.SentTags(), abi.CommandTagDeclineCall) {
		t.Fatal("Decline did not reach the fake core")
	}
}
