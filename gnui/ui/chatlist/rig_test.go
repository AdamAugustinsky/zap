package chatlist

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

// rig is the main window offscreen with the chat list in its slot, over a
// fake core.
type rig struct {
	*widgettest.Rig
	s   *state.State
	sh  *shell.Shell
	f   *fake.Core
	l   *Screen
	tt  *testing.T
	ans *answering
}

func newRig(t *testing.T) *rig { return newRigScale(t, 1) }

func newRigScale(t *testing.T, scale float32) *rig { return newRigSized(t, scale, 0, 0) }

// newRigCached is newRigScale with the chat list already in the model
// when the window opens, as the core emits it from its cache; it returns
// before the first frames are settled.
func newRigCached(t *testing.T, scale float32, cached ...abi.Chat) *rig {
	return newRigSized(t, scale, 0, 0, cached...)
}

// newRigSized is newRigScale with a window of w×h points (0: the default).
func newRigSized(t *testing.T, scale, winW, winH float32, cached ...abi.Chat) *rig {
	t.Helper()
	f := fake.New(nil)
	s := state.New()
	s.Settings.WindowW, s.Settings.WindowH = winW, winH
	s.Settings.SidebarWidth = 480 // wide enough for every chip; the chips' scroll test narrows it
	l := New()
	w := &answering{Screen: l}
	sh, err := shell.New(s, &screen.Set{ChatList: w})
	if err != nil {
		t.Fatal(err)
	}
	s.AddAccount("1", nil, f)
	cfg := shell.Config(s)
	r := &rig{s: s, sh: sh, f: f, l: l, tt: t, ans: w}
	if len(cached) > 0 {
		f.Emit(ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: cached} }))
	} else {
		f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }))
	}
	r.Rig = widgettest.Open(t, cfg, int(cfg.Width*scale), int(cfg.Height*scale), scale, sh)
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	if len(cached) == 0 {
		r.Frames(3)
	}
	return r
}

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

func (r *rig) acct() *state.Account { return r.s.Accounts[0] }

// chats sends the chat list as one batch.
func (r *rig) chats(cs ...abi.Chat) {
	r.f.Emit(ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: cs} }))
	r.Frames(3)
}

// update sends one chat's new state.
func (r *rig) update(c abi.Chat) {
	r.f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) { e.ChatUpdated.Chat = c }))
	r.Frames(3)
}

func jid(n string) []byte { return []byte(n + "@s.whatsapp.net") }

func chat(n, name string, activity int64) abi.Chat {
	return abi.Chat{ID: jid(n), Name: []byte(name), LastActivity: activity}
}

func (r *rig) has(role ui.Role, name string) bool {
	_, ok := r.Find(role, name)
	return ok
}

// rows are the names of the chat rows on screen, top to bottom.
func (r *rig) rows() []string {
	var out []string
	t := r.Tree()
	for _, n := range t.Nodes {
		if name := string(t.Text(n.Name)); n.Role == ui.RoleListItem && name != "" {
			out = append(out, name)
		}
	}
	return out
}

// sentTags are the tags of the commands sent since the last call.
func (r *rig) sent() []abi.Command {
	cmds := r.f.Sent()
	return cmds
}

// lastCmd is the last command with the tag sent so far.
func (r *rig) lastCmd(tag abi.CommandTag) (abi.Command, bool) {
	cs := r.f.Sent()
	for i := len(cs) - 1; i >= 0; i-- {
		if cs[i].Tag == tag {
			return cs[i], true
		}
	}
	return abi.Command{}, false
}

// value is the accessible value of the first node with the role and name.
func (r *rig) value(role ui.Role, name string) string {
	n := r.Must(role, name)
	return string(r.Tree().Text(n.Value))
}

// answering is the chat list with the answer of a native menu: the platform
// delivers the outcome to the input of the frame (ui.Input.SetMenuResult),
// which the rig's steps cannot do, so the screen puts it there as its frame
// begins.
type answering struct {
	*Screen
	owner   ui.Key
	item    int
	outcome ui.MenuOutcome
}

func (w *answering) Build(e *screen.Env, r screen.Rect) {
	if w.outcome != ui.MenuPending {
		e.C().Input().SetMenuResult(w.owner, w.item, w.outcome)
		w.outcome = ui.MenuPending
	}
	w.Screen.Build(e, r)
}

// chipShown reports whether the chip, or the row's + for name "Manage
// labels", lies inside its scrolling row on screen.
func (r *rig) chipShown(row, name string) bool {
	chip, ok := r.Find(ui.RoleCheckbox, name)
	if !ok {
		chip, ok = r.Find(ui.RoleButton, name)
	}
	area, aok := r.Find(ui.RoleScrollArea, row)
	if !ok || !aok {
		r.tt.Fatalf("no chip %q or row %q", name, row)
	}
	return chip.Bounds[0] >= area.Bounds[0]-0.5 && chip.Bounds[2] <= area.Bounds[2]+0.5
}
