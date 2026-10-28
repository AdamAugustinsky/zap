package msginfo_test

import (
	"strings"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/core"
	"zapfast/state"
	"zapfast/ui/msginfo"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

// The pane against the real demo core (the library: $ZAPFAST_CORE or a cargo
// build; skipped without one, and with -short): the demo answers
// WatchReceipts for Rust Berlin's own messages, with Rust-Berlin-1 partial
// (Ada read it, Grace has it, Katherine was never expected) and Family-1
// with none recorded.
const (
	demoBerlin = "120363012345678901@g.us"
	demoFamily = "120363098765432109@g.us"
	demoAda    = "393331234567@s.whatsapp.net"
)

func newDemoRig(t *testing.T) *rig {
	t.Helper()
	if testing.Short() {
		t.Skip("the demo core takes seconds")
	}
	if _, err := core.FindLibrary(); err != nil {
		t.Skipf("no core library: %v", err)
	}
	c, err := core.Open(core.Config{Demo: true, Wake: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(5 * time.Second) })
	s := state.New()
	s.Demo = true
	a := s.AddAccount("1", nil, c)
	sh, err := shell.New(s, &screen.Set{MessageInfo: msginfo.New()})
	if err != nil {
		t.Fatal(err)
	}
	cfg := shell.Config(s)
	r := &rig{Rig: widgettest.Open(t, cfg, 1100, 720, 1, sh), t: t, s: s, a: a}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.waitFor("the chats", func() bool { return a.Model.Chats.Loaded() })
	return r
}

func (r *rig) waitFor(what string, ok func() bool) {
	r.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		r.Frames(1)
		if ok() {
			r.Frames(3)
			return
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("no %s within 10 s; labels %q", what, r.labels())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// openDemo opens the chat, waits for its history, then the pane on msg.
func (r *rig) openDemo(chat, msg string) {
	r.t.Helper()
	ref := r.a.Model.Chats.Find([]byte(chat))
	if ref == 0 {
		r.t.Fatalf("the demo has no chat %s", chat)
	}
	r.a.OpenChat(ref)
	r.waitFor("the history of "+chat, func() bool {
		v := r.a.Model.Conversation([]byte(chat))
		return v != nil && v.Find([]byte(msg)) >= 0
	})
	r.s.Panes.MessageInfo.Reset()
	r.s.Panes.MessageInfo.Add([]byte(msg))
	r.s.Panes.Pane = state.PaneMessageInfo
	r.s.Touch()
}

func (r *rig) waitLabel(label string) {
	r.t.Helper()
	r.waitFor(label, func() bool { return r.has(ui.RoleLabel, label) })
}

// MI-003: Rust-Berlin-1: Ada read it, Grace has it, and Katherine was never
// expected, so she is in no section and nothing remains. The audience is known
// (two were expected), so the "Some receipts … were not recorded" note, which
// is for messages whose audience was not saved, does not show; the demo has
// no such message (the fake core has).
func TestDemoPartialAudience(t *testing.T) {
	r := newDemoRig(t)
	r.openDemo(demoBerlin, "Rust-Berlin-1")
	r.waitLabel("Read by")
	r.want(ui.RoleLabel, "Ada Lovelace")
	r.want(ui.RoleLabel, "Delivered to")
	r.want(ui.RoleLabel, "Grace Hopper")
	r.wantNot(ui.RoleLabel, "Katherine Johnson")
	r.wantNot(ui.RoleLabel, "Loading…")
	r.wantNot(ui.RoleLabel, "No one yet")
	for _, l := range r.labels() {
		if strings.HasPrefix(l, "Some receipts") || strings.HasSuffix(l, "remaining") {
			t.Errorf("unexpected %q", l)
		}
	}
}

// MI-004: Family-1 was sent before receipts were recorded: none are known.
func TestDemoNoReceipts(t *testing.T) {
	r := newDemoRig(t)
	r.openDemo(demoFamily, "Family-1")
	r.waitLabel("No receipts were recorded for this message. Details are available for messages sent from now on.")
	r.wantNot(ui.RoleLabel, "Read by")
	r.wantNot(ui.RoleLabel, "Delivered to")
}

// MI-005: the core follows the message the pane shows and stops when it
// closes; a direct chat's message is shown from its own row.
func TestDemoDirectMessage(t *testing.T) {
	r := newDemoRig(t)
	ref := r.a.Model.Chats.Find([]byte(demoAda))
	r.a.OpenChat(ref)
	var id string
	r.waitFor("Ada's history", func() bool {
		v := r.a.Model.Conversation([]byte(demoAda))
		if v == nil {
			return false
		}
		for i := v.Len() - 1; i >= 0; i-- {
			if v.At(i).FromMe() {
				id = string(v.At(i).ID())
				return true
			}
		}
		return false
	})
	r.openDemo(demoAda, id)
	r.waitLabel("Message info")
	got := strings.Join(r.labels(), "|")
	if !strings.Contains(got, "Delivered") {
		t.Errorf("no Delivered row: %s", got)
	}
}
