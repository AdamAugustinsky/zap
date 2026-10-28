package screen

import (
	"runtime"
	"testing"
	"weak"

	"gonativeui/app"
	"gonativeui/menu"
	"gonativeui/widget/widgettest"

	"zapfast/state"
)

type menuPage struct{}

func (*menuPage) Init(*Env) error                      { return nil }
func (*menuPage) Config(*state.State) app.WindowConfig { return app.WindowConfig{} }
func (*menuPage) Build(*Env)                           {}

type fixedChrome struct {
	model *menu.Model
	reads int
}

func (c *fixedChrome) Menu() *menu.Model { c.reads++; return c.model }
func (*fixedChrome) Commands(*Env)       {}

type refreshingChrome struct {
	fixedChrome
	revision, refreshes int
	own                 *menu.Model
}

func (c *refreshingChrome) RefreshMenus() int               { c.refreshes++; return c.revision }
func (c *refreshingChrome) MenuOf(state.Window) *menu.Model { return c.own }

func TestWindowMenusFollowRevisions(t *testing.T) {
	chrome := &refreshingChrome{revision: 1}
	w := NewWindow(state.New(), state.WinViewer, &menuPage{}, nil, chrome)
	r := widgettest.Open(t, app.Config{}, 100, 100, 1, w)
	t.Cleanup(w.Closed)
	r.Frames(4)
	reads, refreshes := chrome.reads, chrome.refreshes
	r.Frames(4)
	if chrome.reads != reads || chrome.refreshes <= refreshes {
		t.Fatal("steady menus were rebuilt or stopped checking for revisions")
	}
	chrome.revision++
	r.Frame()
	if chrome.reads != reads+1 || w.menus != chrome.revision {
		t.Fatal("new menu revision did not reach the window")
	}
	r.Frame()
	if chrome.reads != reads+1 {
		t.Fatal("same revision was installed again")
	}
}

// keepPage keeps its Env, as a screen with background callbacks does.
type keepPage struct{ e *Env }

func (p *keepPage) Init(e *Env) error                  { p.e = e; return nil }
func (*keepPage) Config(*state.State) app.WindowConfig { return app.WindowConfig{} }
func (*keepPage) Build(*Env)                           {}

// A screen that outlives its window keeps the state through its Env, not
// the closed window's kit.
func TestAClosedWindowsEnvKeepsTheStateNotTheKit(t *testing.T) {
	st, p := state.New(), &keepPage{}
	w := NewWindow(st, state.WinViewer, p, nil, nil)
	r := widgettest.Open(t, app.Config{}, 64, 64, 1, w)
	r.Frames(2)
	kit := weak.Make(p.e.K)
	w.Closed()
	runtime.GC()
	runtime.GC()
	if kit.Value() != nil {
		t.Error("the closed window's kit is still reachable from its screen")
	}
	if p.e.S != st {
		t.Error("the screen lost the state")
	}
	runtime.KeepAlive(r)
}
