package settings

import (
	"sync"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/app/selfupdate"
	"gonativeui/text"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
)

type rig struct {
	*widgettest.Rig
	t   *testing.T
	s   *state.State
	f   *fake.Core
	scr *Screen
	win *screen.Window
}

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

// newRig opens the settings window offscreen over an account on the fake core.
func newRig(t *testing.T, w, h int, setup func(s *state.State, f *fake.Core, scr *Screen)) *rig {
	t.Helper()
	return newRigOn(t, w, h, nil, setup)
}

// newRigOn is newRig with before run on the state ahead of its account
// (loading the settings files has to come first).
func newRigOn(t *testing.T, w, h int, before func(s *state.State), setup func(s *state.State, f *fake.Core, scr *Screen)) *rig {
	t.Helper()
	t.Setenv("ZAPFAST_UPDATE_FEED", "")
	f := fake.New(nil)
	s := state.New()
	s.Shell = &fakeShell{} // nothing opens a real link or folder
	if before != nil {
		before(s)
	}
	s.AddAccount("1", nil, f)
	scr := New()
	scr.up.SetSystem(&fakeSelf{}) // never the real app/selfupdate
	scr.files.envDone = true
	if setup != nil {
		setup(s, f, scr)
	}
	cfg := scr.Config(s)
	win := screen.NewWindow(s, state.WinSettings, scr, nil, nil)
	b := &reserved{win}
	r := &rig{Rig: widgettest.Open(t, cfg, w, h, 1, b), t: t, s: s, f: f, scr: scr, win: win}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.pump()
	r.Frames(4)
	return r
}

// fakeShell records what the window opened.
type fakeShell struct {
	mu     sync.Mutex
	opened []string
}

func (f *fakeShell) Open(path string) error {
	f.mu.Lock()
	f.opened = append(f.opened, path)
	f.mu.Unlock()
	return nil
}

func (f *fakeShell) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.opened...)
}

// fakeSelf is app/selfupdate as the updater sees it: what it was asked, what
// it answers.
type fakeSelf struct {
	mu            sync.Mutex
	supported     error
	prepare       error
	install       error
	result        selfupdate.Result
	stagedVersion string // the version the staged bundle has: 0.21.0 when empty
	calls         []string
	started       int
}

func (f *fakeSelf) note(s string) { f.mu.Lock(); f.calls = append(f.calls, s); f.mu.Unlock() }
func (f *fakeSelf) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeSelf) Supported() error { f.note("supported"); return f.supported }
func (f *fakeSelf) Prepare(zip string) (StagedUpdate, error) {
	f.note("prepare")
	if f.prepare != nil {
		return nil, f.prepare
	}
	return &fakeStaged{f}, nil
}
func (f *fakeSelf) Started()                      { f.mu.Lock(); f.started++; f.mu.Unlock() }
func (f *fakeSelf) LastResult() selfupdate.Result { return f.result }

type fakeStaged struct{ f *fakeSelf }

func (s *fakeStaged) Version() string {
	if s.f.stagedVersion != "" {
		return s.f.stagedVersion
	}
	return "0.21.0"
}

func (s *fakeStaged) InstallAndRelaunch(args []string) error {
	s.f.note("install")
	return s.f.install
}
func (s *fakeStaged) Discard() error { s.f.note("discard"); return nil }

// reserved is the window's builder after the main window has sized the shared
// text system, as it does in the app (ui/shell.Init).
type reserved struct{ app.Builder }

func (b *reserved) Init(a *app.App) error {
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	return b.Builder.(app.Initer).Init(a)
}

// pump applies what the fake core has to say.
func (r *rig) pump() {
	r.s.Pump(0)
}

func (r *rig) emit(evs ...*abi.Event) {
	r.t.Helper()
	if err := r.f.Emit(evs...); err != nil {
		r.t.Fatal(err)
	}
	r.pump()
	r.Frames(2)
}

func connect(r *rig) {
	r.emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagMe, func(e *abi.Event) {
			e.Me.ID, e.Me.Name, e.Me.HasName = []byte("5511999990000:3@s.whatsapp.net"), []byte("Ada Lovelace"), true
			e.Me.About, e.Me.HasAbout = []byte("Analytical"), true
		}))
}

func (r *rig) has(role ui.Role, name string) bool {
	_, ok := r.Find(role, name)
	return ok
}

func (r *rig) sent(tag abi.CommandTag) []abi.Command {
	var out []abi.Command
	for _, c := range r.f.Sent() {
		if c.Tag == tag {
			out = append(out, c)
		}
	}
	return out
}

// choose opens the pop-up named name and picks the option.
func (r *rig) choose(name, option string) {
	r.t.Helper()
	r.Play(widgettest.Click(ui.RoleComboBox, name), widgettest.Idle(2), widgettest.Click(ui.RoleListItem, option), widgettest.Idle(2))
}

// click presses the control and lets the frame settle.
func (r *rig) click(role ui.Role, name string) {
	r.t.Helper()
	r.Play(widgettest.Click(role, name), widgettest.Idle(2))
}

// typeIn types text into the field.
func (r *rig) typeIn(field, text string) {
	r.t.Helper()
	r.Play(widgettest.Click(ui.RoleTextField, field), widgettest.Type(text), widgettest.Idle(2))
}

// value is what the text field named name shows and announces.
func (r *rig) value(name string) string {
	r.t.Helper()
	n := r.Must(ui.RoleTextField, name)
	return string(r.Tree().Text(n.Value))
}

// wait runs frames until cond holds, or fails the test.
func (r *rig) wait(what string, cond func() bool) {
	r.t.Helper()
	for i := 0; i < 400; i++ {
		r.pump()
		r.Frames(1)
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatalf("timed out waiting for %s", what)
}

func (r *rig) toast() string {
	if n := len(r.s.Toasts); n > 0 {
		return string(r.s.Toasts[n-1].Text)
	}
	return ""
}

// text reports whether a label with this text is on screen.
func (r *rig) text(s string) bool { return r.has(ui.RoleLabel, s) }

func (r *rig) acct() *state.Account { return r.s.Accounts[0] }
