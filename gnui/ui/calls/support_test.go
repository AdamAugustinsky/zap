package calls

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

const (
	ada   = "15550001111@s.whatsapp.net"
	grace = "15550002222@s.whatsapp.net"
	alan  = "15550003333@s.whatsapp.net"
	group = "120363000000000000@g.us"
	me    = "15559999999@s.whatsapp.net"
)

// world is a state with one account on a fake core, linked, with Ada, Grace
// and a group among its chats, and a clock the test moves.
type world struct {
	t   *testing.T
	s   *state.State
	f   *fake.Core
	a   *state.Account
	h   *hub
	now time.Time
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, s: state.New(), f: fake.New(nil), now: time.Date(2026, 10, 20, 15, 30, 0, 0, time.UTC)}
	w.s.Now = func() time.Time { return w.now }
	w.s.Shell = noShell{t}
	w.h = hubOf(w.s)
	w.h.tone = &fakeTone{}
	w.a = w.s.AddAccount("1", nil, w.f)
	w.emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagMe, func(e *abi.Event) {
			e.Me.ID, e.Me.Name, e.Me.HasName = []byte(me), []byte("Me Myself"), true
		}),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
				{ID: []byte(ada), Name: []byte("Ada Lovelace"), Kind: abi.ChatKindDirect, LastActivity: 30},
				{ID: []byte(grace), Name: []byte("Grace Hopper"), Kind: abi.ChatKindDirect, LastActivity: 20},
				{ID: []byte(group), Name: []byte("Engines"), Kind: abi.ChatKindGroup, LastActivity: 10},
			}}
		}),
		ev(abi.EventTagContacts, func(e *abi.Event) {
			e.Contacts = abi.EventContacts{Batch: 2, Last: true, Contacts: []abi.Contact{
				{ID: []byte(alan), FullName: []byte("Alan Turing"), HasFullName: true},
			}}
		}))
	return w
}

// noShell stands in for the system's opener: calls open no file or app, and
// a test never does.
type noShell struct{ t *testing.T }

func (n noShell) Open(path string) error {
	n.t.Errorf("opened %q", path)
	return nil
}

// fakeTone stands in for the ringback's player: no test opens a device.
type fakeTone struct{ starts, stops int }

func (f *fakeTone) Start() error { f.starts++; return nil }
func (f *fakeTone) Stop()        { f.stops++ }

// tone is the world's fake ringback.
func (w *world) tone() *fakeTone { return w.h.tone.(*fakeTone) }

// emit queues events and applies them.
func (w *world) emit(events ...*abi.Event) {
	w.t.Helper()
	if err := w.f.Emit(events...); err != nil {
		w.t.Fatal(err)
	}
	w.s.Pump(0)
}

// update applies one call update from the account's core.
func (w *world) update(set func(u *abi.CallUpdate)) {
	w.t.Helper()
	w.emit(ev(abi.EventTagCall, func(e *abi.Event) { set(&e.Call.Update) }))
}

// sent is the tags sent since the last call.
func (w *world) sent() []abi.CommandTag {
	tags := w.f.SentTags()
	return tags
}

// last is the last command sent with tag, and whether there is one.
func (w *world) last(tag abi.CommandTag) (abi.Command, bool) {
	cmds := w.f.Sent()
	for i := len(cmds) - 1; i >= 0; i-- {
		if cmds[i].Tag == tag {
			return cmds[i], true
		}
	}
	return abi.Command{}, false
}

// count is how many commands with tag were sent.
func (w *world) count(tag abi.CommandTag) int {
	n := 0
	for _, t := range w.f.SentTags() {
		if t == tag {
			n++
		}
	}
	return n
}

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

// incoming is a one-to-one call to us from Ada.
func incoming(gen uint64, phase abi.CallPhase) func(u *abi.CallUpdate) {
	return func(u *abi.CallUpdate) {
		u.Generation, u.Chat, u.Direction, u.Phase = gen, []byte(ada), abi.CallDirectionIncoming, phase
		if phase == abi.CallPhaseActive {
			u.ActiveForMS, u.HasActiveForMS = 0, true
		}
	}
}

// outgoing is the core's word about the call placed here.
func outgoing(gen uint64, phase abi.CallPhase) func(u *abi.CallUpdate) {
	return func(u *abi.CallUpdate) {
		u.Generation, u.Chat, u.Direction, u.Phase = gen, []byte(ada), abi.CallDirectionOutgoing, phase
	}
}

// status is the call's line under its name now.
func (w *world) status() string { return string(w.h.appendStatus(nil, w.now)) }

// name is the call's name.
func (w *world) name() string { return string(w.h.appendName(nil)) }

// winRig opens the call window alone, offscreen.
type winRig struct {
	*widgettest.Rig
	w   *world
	win *Window
}

func (w *world) window() *winRig { return w.windowWith(nil) }

// windowWith is window with th applied at the start of each frame.
func (w *world) windowWith(th screen.Theme) *winRig { return w.windowSized(th, 0, 0) }

// windowSized is windowWith in a window of wpx × hpx points (0: its own).
func (w *world) windowSized(th screen.Theme, wpx, hpx int) *winRig {
	w.t.Helper()
	win := NewWindow()
	cfg := win.Config(w.s)
	if wpx == 0 {
		wpx, hpx = int(cfg.Width), int(cfg.Height)
	}
	r := &winRig{Rig: widgettest.Open(w.t, cfg, wpx, hpx, 1, screen.NewWindow(w.s, state.WinCall, win, th, nil)), w: w, win: win}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Frames(3)
	return r
}

// pageRig is the main window with the Calls page, its content pane and the
// call bar in their slots, on the Calls page.
type pageRig struct {
	*widgettest.Rig
	w    *world
	page *Page
}

func (w *world) page() *pageRig { return w.pageSized(0, 0) }

// pageSized is page in a window of wpx × hpx points (0: the shell's own).
func (w *world) pageSized(wpx, hpx int) *pageRig {
	w.t.Helper()
	p := NewPage()
	sh, err := shell.New(w.s, &screen.Set{CallsPage: p, CallsContent: NewContent(), CallBar: NewBar()})
	if err != nil {
		w.t.Fatal(err)
	}
	sh.Traffic = true
	w.s.Page = state.PageCalls
	cfg := shell.Config(w.s)
	if wpx == 0 {
		wpx, hpx = int(cfg.Width), int(cfg.Height)
	}
	r := &pageRig{Rig: widgettest.Open(w.t, cfg, wpx, hpx, 1, sh), w: w, page: p}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Frames(3)
	return r
}

// shot writes the rig's last frame to $CALLS_SHOTS/name.png, when set: for
// looking at the screens, never compared.
func shot(t *testing.T, r *widgettest.Rig, name string) {
	dir := os.Getenv("CALLS_SHOTS")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, r.Image()); err != nil {
		t.Fatal(err)
	}
}

// has fails unless the last frame has a node of role and name.
func has(t *testing.T, r *widgettest.Rig, role ui.Role, name string) {
	t.Helper()
	if _, ok := r.Find(role, name); !ok {
		t.Errorf("no %s %q in\n%s", widgettest.RoleName(role), name, widgettest.Dump(r.Tree()))
	}
}

// hasNot fails if the last frame has a node of role and name.
func hasNot(t *testing.T, r *widgettest.Rig, role ui.Role, name string) {
	t.Helper()
	if _, ok := r.Find(role, name); ok {
		t.Errorf("a %s %q is shown", widgettest.RoleName(role), name)
	}
}
