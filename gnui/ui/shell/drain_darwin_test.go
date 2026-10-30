//go:build darwin

package shell

import (
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The tests here run the shell's app over a fake core and check that the
// cores' events are handled whatever the main window does. A core wakes the
// app once and then waits to be drained (gocore.Conn's wake), so a drain that
// is lost leaves a resident ZapFast deaf: no message, no call. The fake wakes
// at each event, so a test checks the event that came with the lost wake.

// heard counts the messages the state hands out, and how the main window was
// when one came.
type heard struct {
	h          *App
	n          atomic.Int32
	mainClosed atomic.Bool
	hidden     atomic.Bool // in a frame of the main window while hidden
}

func (l *heard) Event(*state.Account, *abi.Event) {
	if w := l.h.main.Load(); w == nil || !w.Open() {
		l.mainClosed.Store(true)
	} else if c := l.h.Shell.env.K.Context(); c != nil && c.Input().Hidden {
		l.hidden.Store(true)
	}
	l.n.Add(1)
}

// postEndTest ends a test's app.
const postEndTest = 0x7a66_5348_0001

// quitter is the shell's app handler, ended by postEndTest.
type quitter struct{ *App }

func (q quitter) Event(a *app.App, e *app.Event) {
	if e.Kind == app.EvPost && e.ID == postEndTest {
		a.Quit()
		return
	}
	q.App.Event(a, e)
}

const adaChat = "1@s.whatsapp.net"

func message(id string) *abi.Event {
	m := abi.Message{ID: []byte(id), Chat: []byte(adaChat), Sender: []byte(adaChat), Timestamp: 1_800_000_000}
	m.Content.Tag = abi.ContentTagText
	m.Content.Text.Text = []byte("hello")
	return &abi.Event{Tag: abi.EventTagIncoming, Incoming: abi.EventIncoming{Chat: []byte(adaChat), Message: m}}
}

// emitLater emits ev from its own goroutine, as the core's, a second after
// the fake is stored (the window idle, or hidden, by then).
func emitLater(ev *abi.Event) *atomic.Pointer[fake.Core] {
	var core atomic.Pointer[fake.Core]
	go func() {
		for deadline := time.Now().Add(10 * time.Second); core.Load() == nil; {
			if time.Now().After(deadline) {
				return // the step never ran: the test says so
			}
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(time.Second)
		core.Load().Emit(ev)
	}()
	return &core
}

// residentRun runs the shell's app, keeping running with no window, over a
// fake core with one chat whose wake is wake(app). step runs once in a frame
// of the main window, after the pump, once the window is the app's and a few
// frames on (the first ones run inside OpenWindow). It returns once a message
// was heard, or after 5 s; ok is whether step ran.
func residentRun(t *testing.T, wake func(*App), step func(e *screen.Env, f *fake.Core)) (l *heard, f *fake.Core, ok bool) {
	t.Helper()
	var h atomic.Pointer[App]
	f = fake.New(func() {
		if h := h.Load(); h != nil {
			wake(h)
		}
	})
	f.Emit(&abi.Event{Tag: abi.EventTagLink, Link: abi.EventLink{Status: abi.LinkStatus{Tag: abi.LinkStatusTagConnected}}},
		&abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
			{ID: []byte(adaChat), Name: []byte("Ada Lovelace"), LastActivity: 20},
		}}})
	s := state.New()
	var a *state.Account
	var stepped atomic.Bool
	frames := 0
	tick := screen.TickFunc(func(e *screen.Env) {
		if stepped.Load() {
			return
		}
		if w := h.Load().main.Load(); !a.Model.Chats.Loaded() || w == nil || !w.Open() || frames < 5 {
			frames++
			e.A.Window().Wake() // the next frame
			return
		}
		step(e, f)
		stepped.Store(true)
	})
	sh, err := New(s, &screen.Set{Tickers: []screen.Ticker{tick}})
	if err != nil {
		t.Fatal(err)
	}
	a = s.AddAccount("1", nil, f)
	ap := NewApp(sh)
	s.Wake = ap.Wake
	l = &heard{h: ap}
	s.Listen(l, abi.EventTagIncoming)
	h.Store(ap)

	end := make(chan struct{})
	go func() {
		defer close(end)
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && l.n.Load() == 0; {
			time.Sleep(20 * time.Millisecond)
		}
		for !app.Post(app.Event{ID: postEndTest}, nil) { // a test may fill the queue
			time.Sleep(20 * time.Millisecond)
		}
	}()
	var runErr error
	widgettest.OnMain(func() {
		runErr = app.Main(app.AppConfig{ID: "ai.a3lab.zap.shell-live", KeepRunning: true}, quitter{ap})
	})
	<-end
	if runErr != nil {
		t.Skip("no window to open:", runErr)
	}
	return l, f, stepped.Load()
}

// A message that comes in the frame the main window closes in (⌘W, keep
// running on) is handled all the same, with the window closed: its wake went
// to the main window, which draws no frame again. It opens a window, so it
// runs only with ZAPFAST_LIVE=1.
func TestLiveAMessageAsTheMainWindowClosesIsStillHandled(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a window: set ZAPFAST_LIVE=1")
	}
	l, f, ok := residentRun(t, (*App).Wake, func(e *screen.Env, f *fake.Core) {
		f.Emit(message("m1")) // after this frame's pump: it waits for a drain
		e.A.Window().Close()
	})
	if !ok {
		t.Fatal("the main window never got to close")
	}
	if l.n.Load() == 0 {
		t.Fatalf("the message that came as the main window closed was never handled (%d events wait)", f.Pending())
	}
	if !l.mainClosed.Load() {
		t.Error("it was handled before the main window closed")
	}
}

// A drain posted while the main window is open is not lost: Wake posts while
// the main window is not the app's yet (its first frames run inside
// OpenWindow), and the post may come once it is, to an idle window. The shell
// leaves it to the main window, which the framework gives a frame after any
// app event. Here the fake's wake always posts, as Wake does then, and the
// message comes from the core's goroutine. It opens a window, so it runs only
// with ZAPFAST_LIVE=1.
func TestLiveADrainPostedWhileTheMainWindowIsOpenIsNotDropped(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a window: set ZAPFAST_LIVE=1")
	}
	core := emitLater(message("m1"))
	l, f, ok := residentRun(t, (*App).postDrain, func(e *screen.Env, f *fake.Core) {
		core.Store(f)
	})
	if !ok {
		t.Fatal("the main window never got to the message")
	}
	if l.n.Load() == 0 {
		t.Fatalf("the message whose drain was posted was never handled (%d events wait)", f.Pending())
	}
	if l.mainClosed.Load() {
		t.Error("the main window closed")
	}
}

// The main window hidden (the status item's Hide) or in the Dock still drains
// the cores: the framework draws a hidden window's frame for a Wake. A message
// from the core's goroutine then is handled in a frame of the hidden window.
// It opens a window, so it runs only with ZAPFAST_LIVE=1.
func TestLiveAMessageWhileTheMainWindowIsHiddenIsHandled(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a window: set ZAPFAST_LIVE=1")
	}
	for _, tc := range []struct {
		name string
		hide func(*app.Win)
	}{
		{"hidden", (*app.Win).Hide},
		{"in the Dock", (*app.Win).Miniaturize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core := emitLater(message("m2"))
			l, f, ok := residentRun(t, (*App).Wake, func(e *screen.Env, f *fake.Core) {
				tc.hide(e.A.Window())
				core.Store(f)
			})
			if !ok {
				t.Fatal("the main window never got to hide")
			}
			if l.n.Load() == 0 {
				t.Fatalf("the message that came while the main window was %s was never handled (%d events wait)", tc.name, f.Pending())
			}
			if !l.hidden.Load() {
				t.Errorf("it was not handled in a frame of the %s window (closed: %v)", tc.name, l.mainClosed.Load())
			}
		})
	}
}

// postFiller is an app post nobody handles, to fill the app's post queue.
const postFiller = 0x7a66_5348_0002

// A drain that the app's full post queue refuses is not lost. The main
// window closes with a message's wake in it, and the drain the close posts
// finds platform.MaxPosted posts waiting: Post refuses it, and the
// platform.Wake in its place drains nothing. The shell owes the cores that
// drain, and the next app event (one of the waiting posts) pays it. It
// opens a window, so it runs only with ZAPFAST_LIVE=1.
func TestLiveADrainTheFullPostQueueRefusedIsStillHandled(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a window: set ZAPFAST_LIVE=1")
	}
	var full atomic.Bool
	l, f, ok := residentRun(t, (*App).Wake, func(e *screen.Env, f *fake.Core) {
		w := e.A.Window()
		e.A.AtSafePoint(func() {
			w.Close() // at the end of this safe point
			for range platform.MaxPosted + 1 {
				if !app.Post(app.Event{ID: postFiller}, nil) {
					full.Store(true)
					break
				}
			}
			f.Emit(message("m1")) // its wake goes to the main window, closing
		})
	})
	if !ok || !full.Load() {
		t.Fatalf("the main window never closed with the post queue full (closed: %v, full: %v)", ok, full.Load())
	}
	if l.n.Load() == 0 {
		t.Fatalf("the message whose drain the full post queue refused was never handled (%d events wait)", f.Pending())
	}
	if !l.mainClosed.Load() {
		t.Error("it was handled before the main window closed")
	}
}

// The cores wake the app from their own goroutines while the main thread
// opens and closes the main window: Wake must not read the window's state
// (app.Win.Open is the main thread's) to choose between its frame and a
// post. Here a core's goroutine wakes the app over and over while the main
// window closes; the race detector (go test -race) reports any read Wake
// makes of what the close writes. It opens a window, so it runs only with
// ZAPFAST_LIVE=1.
func TestLiveWakesFromACoresGoroutineAsTheMainWindowCloses(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a window: set ZAPFAST_LIVE=1")
	}
	stop := make(chan struct{})
	defer close(stop)
	l, f, ok := residentRun(t, (*App).Wake, func(e *screen.Env, f *fake.Core) {
		go func() {
			for i := range 20000 {
				select {
				case <-stop:
					return
				default:
				}
				f.Emit(message("w" + strconv.Itoa(i))) // the fake wakes at each
				runtime.Gosched()
			}
		}()
		e.A.Window().Close()
	})
	if !ok {
		t.Fatal("the main window never got to close")
	}
	if l.n.Load() == 0 {
		t.Fatalf("no message was handled (%d events wait)", f.Pending())
	}
}
