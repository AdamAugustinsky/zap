//go:build darwin

package shell

import (
	"os"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/chatlist"
	"zapfast/ui/screen"
)

type findReopen struct {
	*App
	closing, reopened, focused bool
}

func (h *findReopen) Event(a *app.App, e *app.Event) {
	if e.Kind == app.EvPost && e.ID == postEndTest {
		a.Quit()
		return
	}
	w := h.main.Load()
	closed := h.closing && !h.reopened && e.Kind == app.EvWindowClosed && w != nil && e.Window == w.ID()
	h.App.Event(a, e)
	if closed {
		h.reopened = true
		h.App.Event(a, &app.Event{Kind: app.EvCommand, ID: uint64(CmdFind)})
	}
}

// MB-029, upstream macos::drain(hidden): show the window before FocusSearch.
// Close the real main window, deliver Find to the resident app, then check
// that the recreated list consumes the request and assigns UI focus to its
// sole field (Search). Native application activation remains a hand check.
func TestLiveFindReopensTheMainWindowAndRequestsSearchFocus(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a real window: set ZAPFAST_LIVE=1")
	}
	s := state.New()
	f := fake.New(nil)
	f.Emit(&abi.Event{Tag: abi.EventTagLink, Link: abi.EventLink{Status: abi.LinkStatus{Tag: abi.LinkStatusTagConnected}}},
		&abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: 1, Last: true}})
	s.AddAccount("1", nil, f)
	var h *findReopen
	frames := 0
	set := &screen.Set{ChatList: chatlist.New()}
	set.Tickers = []screen.Ticker{screen.TickFunc(func(e *screen.Env) {
		frames++
		if !h.closing && frames > 5 {
			h.closing = true
			e.A.Window().Close()
		} else if h.reopened && e.C().FocusKey() != 0 && !s.Req.FocusSearch {
			h.focused = true
			h.a.Quit()
		} else {
			e.A.Window().Wake()
		}
	})}
	sh, err := New(s, set)
	if err != nil {
		t.Fatal(err)
	}
	h = &findReopen{App: NewApp(sh)}
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
			return
		case <-time.After(8 * time.Second):
			app.Post(app.Event{ID: postEndTest}, nil)
		}
	}()
	widgettest.OnMain(func() {
		err = app.Main(app.AppConfig{ID: "ai.a3lab.zap.shell-find-live", KeepRunning: true}, h)
	})
	close(done)
	if err != nil {
		t.Fatal(err)
	}
	if !h.reopened || !h.focused {
		t.Fatalf("Find after close: reopened=%v search focused=%v", h.reopened, h.focused)
	}
}
