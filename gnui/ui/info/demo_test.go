package info_test

import (
	"strings"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/core"
	"zapfast/gocore/demo"
	"zapfast/state"
	"zapfast/ui/info"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

// The panel against the demo core (skipped with -short): Ada's shared photos,
// clip, documents and links, and the two groups with their members and
// descriptions. The demo refuses the commands that change a group (name,
// photo, leave), so those stay with the fake core; here the panel offers them
// or not as the group's metadata says.
const (
	demoAda    = "393331234567@s.whatsapp.net"
	demoBerlin = "120363012345678901@g.us"
	demoFamily = "120363098765432109@g.us"
)

func newDemoRig(t *testing.T) *rig {
	t.Helper()
	if testing.Short() {
		t.Skip("the demo core takes seconds")
	}
	c, err := demo.Open(core.Config{Wake: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(5 * time.Second) })
	s := state.New()
	s.Demo = true
	sf := &fakeShell{}
	s.Shell = sf
	a := s.AddAccount("1", nil, c)
	sh, err := shell.New(s, &screen.Set{Info: info.New()})
	if err != nil {
		t.Fatal(err)
	}
	cfg := shell.Config(s)
	r := &rig{Rig: widgettest.Open(t, cfg, 1100, 720, 1, sh), t: t, s: s, a: a, sh: sh, sf: sf}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.waitFor("the chats", func() bool { return a.Model.Chats.Loaded() })
	return r
}

// waitFor runs frames until ok, for up to ten seconds.
func (r *rig) waitFor(what string, ok func() bool) {
	r.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		r.Frames(1)
		if ok() {
			r.Frames(3)
			return
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("no %s within 10 s:\n%s", what, r.Dump())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// openDemo opens the chat and its panel and waits for the listing, if any.
func (r *rig) openDemo(jid string, listing bool) {
	r.t.Helper()
	r.open(jid, jid)
	if listing {
		r.waitFor("the shared listing", func() bool { return r.count(ui.RoleButton, "Photo, ")+r.count(ui.RoleButton, "Video, ") > 0 })
	}
}

// IN-011: Ada's shared items in their tabs, newest first; the document goes
// to its message.
func TestDemoSharedMediaDocsAndLinks(t *testing.T) {
	r := newDemoRig(t)
	r.openDemo(demoAda, true)
	r.click(ui.RoleButton, "Media, links and docs")
	r.want(ui.RoleTab, "Media 4")
	r.want(ui.RoleTab, "Docs 2")
	r.want(ui.RoleTab, "Links 3")
	if n := r.count(ui.RoleButton, "Photo, ") + r.count(ui.RoleButton, "Video, "); n != 4 {
		t.Errorf("%d media cells, want 3 photos and a clip\n%s", n, r.Dump())
	}
	if r.count(ui.RoleButton, "Video, ") != 1 || r.count(ui.RoleButton, "Photo, ") != 3 {
		t.Errorf("photos and clip: %q", r.names(ui.RoleButton))
	}

	r.click(ui.RoleTab, "Docs 2")
	var docs []string
	for _, n := range r.names(ui.RoleButton) {
		if strings.Contains(n, ".pdf, ") || strings.Contains(n, ".csv, ") {
			docs = append(docs, n)
		}
	}
	if len(docs) != 2 {
		t.Fatalf("documents %q\n%s", docs, r.Dump())
	}
	r.click(ui.RoleButton, docs[0])
	if j := &r.s.Panes.Jump; j.Msg.Len() != 1 {
		t.Error("the document did not take the conversation to its message")
	}

	r.s.Panes.SetInfo([]byte(demoAda))
	r.s.Panes.InfoView = state.InfoLinks
	r.s.Touch()
	r.Frames(4)
	var links int
	for _, n := range r.names(ui.RoleButton) {
		if strings.Contains(n, " · ") {
			links++
		}
	}
	if links != 3 {
		t.Errorf("%d links, want 3\n%s", links, r.Dump())
	}
	if r.sf != nil && len(r.sf.Opened()) != 0 {
		t.Errorf("a link opened by itself: %v", r.sf.Opened())
	}
}

// IN-009, IN-008: the groups' members and descriptions.
func TestDemoGroupMembersAndDescription(t *testing.T) {
	r := newDemoRig(t)
	r.openDemo(demoBerlin, false)
	r.want(ui.RoleLabel, "Group info")
	r.want(ui.RoleLabel, "Rust Berlin")
	r.want(ui.RoleLabel, "Rust meetups in Berlin, every second Tuesday.")
	var members int
	tr := r.Tree()
	for _, n := range tr.Nodes {
		if n.Role == ui.RoleLabel && strings.HasPrefix(string(tr.Text(n.Name)), "Members (") {
			members++
			t.Logf("%s", tr.Text(n.Name))
		}
	}
	if members != 1 {
		t.Fatalf("no members heading\n%s", r.Dump())
	}
	r.want(ui.RoleButton, "You")
	// a member opens their own panel without leaving the chat
	r.click(ui.RoleButton, "Ada Lovelace")
	if string(r.s.Panes.Info.At(0)) != demoAda {
		t.Errorf("the panel is on %q", r.s.Panes.Info.At(0))
	}
	r.want(ui.RoleLabel, "Contact info")

	r.openDemo(demoFamily, false)
	r.wantNot(ui.RoleLabel, "Description") // Family has none
	r.want(ui.RoleButton, "Leave group")
}

// IN-006, IN-007: we are an admin of Rust Berlin, whose info only admins
// edit: the pencil and the photo menu are offered. In Family, open to
// everyone, they are offered too; the demo cannot mark other admins, so a
// group where we are neither admin nor allowed has no demo case (the fake
// core has it).
func TestDemoGroupEditingPermissions(t *testing.T) {
	r := newDemoRig(t)
	r.openDemo(demoBerlin, false)
	r.want(ui.RoleButton, "Edit group name")
	r.want(ui.RoleButton, "Change group photo")
	r.openDemo(demoFamily, false)
	r.want(ui.RoleButton, "Edit group name")
	r.want(ui.RoleButton, "Change group photo")
}
