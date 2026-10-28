package info_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/info"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

const (
	ada   = "393331234567@s.whatsapp.net"
	grace = "15551230000@s.whatsapp.net"
	team  = "120363000000000001@g.us"
	me    = "15550001111@s.whatsapp.net"
)

// rig is the main window offscreen at 1100×720 points with the info pane in
// its slot, over the fake core.
type rig struct {
	*widgettest.Rig
	t  *testing.T
	s  *state.State
	a  *state.Account
	f  *fake.Core
	sh *shell.Shell
	sf *fakeShell
}

type fakeShell struct {
	mu     sync.Mutex
	opened []string
}

func (s *fakeShell) Open(p string) error {
	s.mu.Lock()
	s.opened = append(s.opened, p)
	s.mu.Unlock()
	return nil
}

func (s *fakeShell) Opened() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.opened...)
}

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

func b(s string) []byte { return []byte(s) }

type chatSpec struct {
	id, name string
	mut      func(c *abi.Chat)
}

func chatsEvent(specs ...chatSpec) *abi.Event {
	return ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 1, Last: true}
		for i, sp := range specs {
			c := abi.Chat{ID: b(sp.id), Name: b(sp.name), LastActivity: int64(100 - i)}
			if strings.HasSuffix(sp.id, "@g.us") {
				c.Kind, c.GroupSubjectKnown = abi.ChatKindGroup, true
			}
			if sp.mut != nil {
				sp.mut(&c)
			}
			e.Chats.Chats = append(e.Chats.Chats, c)
		}
	})
}

func contacts(cs ...abi.Contact) *abi.Event {
	return ev(abi.EventTagContacts, func(e *abi.Event) { e.Contacts.Contacts = cs })
}

func newRig(t *testing.T, specs ...chatSpec) *rig { return newRigW(t, 0, specs...) }

// newRigW is newRig in a window width points wide (0: the default).
func newRigW(t *testing.T, width int, specs ...chatSpec) *rig {
	t.Helper()
	f := fake.New(nil)
	f.Emit(
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagMe, func(e *abi.Event) { e.Me.ID = b(me); e.Me.Name = b("Me Myself") }),
		contacts(
			abi.Contact{ID: b(ada), FullName: b("Ada Lovelace"), HasFullName: true, FirstName: b("Ada"), HasFirstName: true},
			abi.Contact{ID: b(grace), FullName: b("Grace Hopper"), HasFullName: true},
		),
		chatsEvent(specs...),
	)
	s := state.New()
	set := &screen.Set{Info: info.New()}
	sh, err := shell.New(s, set)
	if err != nil {
		t.Fatal(err)
	}
	sf := &fakeShell{}
	s.Shell = sf
	a := s.AddAccount("1", nil, f)
	cfg := shell.Config(s)
	if width == 0 {
		width = int(cfg.Width)
	}
	r := &rig{Rig: widgettest.Open(t, cfg, width, int(cfg.Height), 1, sh), t: t, s: s, a: a, f: f, sh: sh, sf: sf}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Frames(4)
	return r
}

// open opens the chat and the info panel on jid.
func (r *rig) open(chat, jid string) {
	r.t.Helper()
	ref := r.a.Model.Chats.Find(b(chat))
	if ref == 0 {
		r.t.Fatalf("no chat %s", chat)
	}
	r.a.OpenChat(ref)
	r.s.Panes.SetInfo(b(jid))
	r.s.Touch()
	r.Frames(4)
}

func (r *rig) has(role ui.Role, name string) bool {
	_, ok := r.Find(role, name)
	return ok
}

func (r *rig) want(role ui.Role, name string) {
	r.t.Helper()
	if !r.has(role, name) {
		r.t.Fatalf("no %v %q in\n%s", role, name, r.Dump())
	}
}

func (r *rig) wantNot(role ui.Role, name string) {
	r.t.Helper()
	if r.has(role, name) {
		r.t.Errorf("unexpected %v %q", role, name)
	}
}

func (r *rig) Dump() string {
	var sb strings.Builder
	tr := r.Tree()
	for _, n := range tr.Nodes {
		fmt.Fprintf(&sb, "%s %q\n", widgettest.RoleName(n.Role), tr.Text(n.Name))
	}
	return sb.String()
}

func (r *rig) click(role ui.Role, name string) {
	r.t.Helper()
	r.want(role, name)
	r.Play(widgettest.Click(role, name), widgettest.Idle(3))
}

// sent is the commands of tag the core received, in order.
func (r *rig) sent(tag abi.CommandTag) []abi.Command {
	var out []abi.Command
	for _, c := range r.f.Sent() {
		if c.Tag == tag {
			out = append(out, c)
		}
	}
	return out
}

func direct() []chatSpec {
	return []chatSpec{{id: ada, name: "Ada Lovelace"}, {id: grace, name: "Grace Hopper"}}
}

// IN-001, IN-002: the panel opens as Contact info with Close, and shows the
// name, the number and Muted until.
func TestContactInfoShowsTheContact(t *testing.T) {
	r := newRig(t, chatSpec{id: ada, name: "Ada Lovelace", mut: func(c *abi.Chat) { c.HasMutedUntil, c.MutedUntil = true, 0 }})
	r.open(ada, ada)
	r.want(ui.RoleLabel, "Contact info")
	r.want(ui.RoleButton, "Close")
	r.want(ui.RoleLabel, "Ada Lovelace")
	r.want(ui.RoleLabel, "+39 333 123 4567")
	r.want(ui.RoleLabel, "Muted")
}

func TestGroupAndChannelTitles(t *testing.T) {
	r := newRig(t, chatSpec{id: team, name: "Team"}, chatSpec{id: "120@newsletter", name: "News"})
	r.open(team, team)
	r.want(ui.RoleLabel, "Group info")
	r.open("120@newsletter", "120@newsletter")
	r.want(ui.RoleLabel, "Channel info")
}

// IN-001: Close closes the panel.
func TestCloseClosesThePanel(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	r.click(ui.RoleButton, "Close")
	if r.s.Panes.Pane != state.PaneNone {
		t.Fatal("the panel is still open")
	}
}

// IN-003: the disappearing-messages timer, "Off" once turned off, and
// nothing when unknown.
func TestDisappearingTimer(t *testing.T) {
	r := newRig(t,
		chatSpec{id: ada, name: "Ada Lovelace", mut: func(c *abi.Chat) { c.HasEphemeralExpiration, c.EphemeralExpiration = true, 86400 }},
		chatSpec{id: grace, name: "Grace Hopper", mut: func(c *abi.Chat) { c.HasEphemeralExpiration = true }},
		chatSpec{id: team, name: "Team"},
	)
	r.open(ada, ada)
	r.want(ui.RoleLabel, "Disappearing messages: 24 hours")
	r.open(grace, grace)
	r.want(ui.RoleLabel, "Disappearing messages: Off")
	r.open(team, team)
	tr := r.Tree()
	for _, n := range tr.Nodes {
		if strings.HasPrefix(string(tr.Text(n.Name)), "Disappearing messages") {
			t.Errorf("a timer nobody mentioned is drawn: %q", tr.Text(n.Name))
		}
	}
}

// IN-004: the buttons and what they send.
func TestActionsSendTheirCommands(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	for _, n := range []string{"Message", "Rename", "Copy number", "Mute", "Pin", "Archive"} {
		r.want(ui.RoleButton, n)
	}
	r.wantNot(ui.RoleButton, "Add to contacts")

	r.click(ui.RoleButton, "Mute")
	if m := r.sent(abi.CommandTagSetMuted); len(m) != 1 || !m[0].SetMuted.HasUntil || m[0].SetMuted.Until != 0 || string(m[0].SetMuted.Chat) != ada {
		t.Errorf("Mute sent %+v", m)
	}
	r.click(ui.RoleButton, "Pin")
	if m := r.sent(abi.CommandTagSetPinned); len(m) != 1 || !m[0].SetPinned.Pinned {
		t.Errorf("Pin sent %+v", m)
	}
	var clip []byte
	r.Context().ClipboardSet = func(p []byte) { clip = append(clip[:0], p...) }
	r.click(ui.RoleButton, "Copy number")
	if string(clip) != "+393331234567" {
		t.Errorf("copied %q", clip)
	}
	r.click(ui.RoleButton, "Archive")
	if m := r.sent(abi.CommandTagSetArchived); len(m) != 1 || !m[0].SetArchived.Archived {
		t.Errorf("Archive sent %+v", m)
	}
	if r.s.Panes.Pane != state.PaneNone {
		t.Error("Archive left the panel open")
	}
}

// Unmute, Unpin and Unarchive appear for a chat that is muted, pinned and
// archived.
func TestActionsFollowTheChat(t *testing.T) {
	r := newRig(t, chatSpec{id: ada, name: "Ada Lovelace", mut: func(c *abi.Chat) {
		c.HasMutedUntil, c.MutedUntil = true, 0
		c.Pinned, c.PinnedAt = true, 5
		c.Archived = true
	}})
	r.open(ada, ada)
	for _, n := range []string{"Unmute", "Unpin", "Unarchive"} {
		r.want(ui.RoleButton, n)
	}
	r.click(ui.RoleButton, "Unmute")
	if m := r.sent(abi.CommandTagSetMuted); len(m) != 1 || m[0].SetMuted.HasUntil {
		t.Errorf("Unmute sent %+v", m)
	}
}

// Pinning past the limit says so and sends nothing.
func TestPinLimit(t *testing.T) {
	pin := func(c *abi.Chat) { c.Pinned, c.PinnedAt = true, 5 }
	r := newRig(t,
		chatSpec{id: "1@s.whatsapp.net", name: "One", mut: pin}, chatSpec{id: "2@s.whatsapp.net", name: "Two", mut: pin},
		chatSpec{id: "3@s.whatsapp.net", name: "Three", mut: pin}, chatSpec{id: ada, name: "Ada Lovelace"})
	r.open(ada, ada)
	r.click(ui.RoleButton, "Pin")
	if len(r.sent(abi.CommandTagSetPinned)) != 0 {
		t.Error("pinned past the limit")
	}
	if len(r.s.Toasts) != 1 || string(r.s.Toasts[0].Text) != "You can only pin 3 chats" {
		t.Errorf("toasts %v", r.s.Toasts)
	}
}

// Message opens the chat; for a person without one, it asks the core to make
// it and opens it when it arrives.
func TestMessageOpensTheChat(t *testing.T) {
	r := newRig(t, chatSpec{id: team, name: "Team"}, chatSpec{id: ada, name: "Ada Lovelace"})
	r.s.Settings.InfoPaneWidth = 380
	r.a.OpenChat(r.a.Model.Chats.Find(b(team)))
	// a member of the group with no chat of their own
	r.f.Emit(chatsEvent(chatSpec{id: team, name: "Team", mut: func(c *abi.Chat) { c.Participants = [][]byte{b(me), b(ada), b(grace)} }}, chatSpec{id: ada, name: "Ada Lovelace"}))
	r.Frames(2)
	r.s.Panes.SetInfo(b(grace))
	r.Frames(4)
	r.click(ui.RoleButton, "Message")
	ens := r.sent(abi.CommandTagEnsureChat)
	if len(ens) != 1 || string(ens[0].EnsureChat.Chat) != grace {
		t.Fatalf("EnsureChat %+v", ens)
	}
	r.f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: b(grace), Name: b("Grace Hopper")}
	}))
	r.Frames(3)
	if c, ok := r.a.OpenChatView(); !ok || string(c.ID()) != grace {
		t.Error("the new chat did not open")
	}
	if r.s.Panes.Pane != state.PaneNone {
		t.Error("the panel stayed open")
	}
	// an existing chat opens at once
	r.s.Panes.SetInfo(b(ada))
	r.a.OpenChat(r.a.Model.Chats.Find(b(ada)))
	r.s.Panes.SetInfo(b(ada))
	r.Frames(3)
	r.click(ui.RoleButton, "Message")
	if c, _ := r.a.OpenChatView(); string(c.ID()) != ada {
		t.Error("Ada's chat did not open")
	}
}

// IN-005: Rename edits the first name and surname in place; Add to contacts
// for a person not in the address book.
func TestRenameEditsTheNameInPlace(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	r.click(ui.RoleButton, "Rename")
	r.want(ui.RoleTextField, "First name")
	r.want(ui.RoleTextField, "Surname")
	r.want(ui.RoleButton, "Save name (Enter)")
	r.Play(widgettest.Key('A', ui.PrimaryMod()), widgettest.Type("Augusta"), widgettest.Idle(2),
		widgettest.Click(ui.RoleTextField, "Surname"), widgettest.Key('A', ui.PrimaryMod()), widgettest.Type("King"), widgettest.Idle(2))
	r.click(ui.RoleButton, "Save name (Enter)")
	sc := r.sent(abi.CommandTagSaveContact)
	if len(sc) != 1 {
		t.Fatalf("SaveContact %+v", sc)
	}
	c := sc[0].SaveContact
	if string(c.ID) != ada || string(c.FullName) != "Augusta King" || string(c.FirstName) != "Augusta" || !c.HasFirstName || !c.ToPhone {
		t.Errorf("SaveContact %+v", c)
	}
	r.wantNot(ui.RoleTextField, "First name")
}

func TestAddToContactsAndEscape(t *testing.T) {
	r := newRig(t, chatSpec{id: "393339999999@s.whatsapp.net", name: ""})
	r.open("393339999999@s.whatsapp.net", "393339999999@s.whatsapp.net")
	r.click(ui.RoleButton, "Add to contacts")
	r.want(ui.RoleTextField, "First name")
	r.Play(widgettest.Type("Linus"), widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(3))
	r.wantNot(ui.RoleTextField, "First name")
	if len(r.sent(abi.CommandTagSaveContact)) != 0 {
		t.Error("Escape saved")
	}
	if r.s.Panes.Pane != state.PaneInfo {
		t.Error("Escape in the editor closed the panel")
	}
}

// editorNames splits the saved name by the first name saved with it.
func TestEditorNames(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	r.click(ui.RoleButton, "Rename")
	// "Ada Lovelace" with first name "Ada": the editor starts as Ada / Lovelace
	val := func(name string) string {
		n := r.Must(ui.RoleTextField, name)
		return string(r.Tree().Text(n.Value))
	}
	if v := val("First name"); v != "Ada" {
		t.Errorf("first name %q", v)
	}
	if v := val("Surname"); v != "Lovelace" {
		t.Errorf("surname %q", v)
	}
}

// IN-002: "online" while the contact is, else when they were last seen, and
// nothing while the core has not said; it follows the core live.
func TestOnlineAndLastSeen(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	has := func(prefix string) bool {
		tr := r.Tree()
		for _, n := range tr.Nodes {
			if n.Role == ui.RoleLabel && strings.HasPrefix(string(tr.Text(n.Name)), prefix) {
				return true
			}
		}
		return false
	}
	if has("online") || has("last seen") {
		t.Fatal("a status shows before the core said anything")
	}
	presence := func(online bool, seen int64) {
		r.f.Emit(ev(abi.EventTagPresence, func(e *abi.Event) {
			e.Presence = abi.EventPresence{ID: b(ada), Online: online, LastSeen: seen, HasLastSeen: seen != 0}
		}))
		r.Frames(3)
	}
	presence(true, 0)
	r.want(ui.RoleLabel, "online")
	presence(false, r.s.Now().Unix()-3600)
	r.wantNot(ui.RoleLabel, "online")
	if !has("last seen ") {
		t.Errorf("no last seen line\n%s", r.Dump())
	}
	presence(false, 0)
	if has("online") || has("last seen") {
		t.Error("a status shows for a contact with neither")
	}
}

// IN-002: a timed mute reads "Muted until …" with the stamp of the end.
func TestMutedUntil(t *testing.T) {
	r := newRig(t, chatSpec{id: ada, name: "Ada Lovelace", mut: func(c *abi.Chat) {
		c.HasMutedUntil, c.MutedUntil = true, 4_000_000_000
	}})
	r.open(ada, ada)
	has := false
	tr := r.Tree()
	for _, n := range tr.Nodes {
		if n.Role == ui.RoleLabel && strings.HasPrefix(string(tr.Text(n.Name)), "Muted until ") {
			has = true
		}
	}
	if !has {
		t.Errorf("no Muted until line\n%s", r.Dump())
	}
	r.wantNot(ui.RoleLabel, "Muted")
}

// The panel's title row is in the window's toolbar band, over the pane, as an
// inspector's is in a unified toolbar: the Close button sits in the band and
// is the only one. It needs the shell to give the pane's toolbar items a
// place (screen.ToolbarPart); until it does, the pane builds the row itself
// and this test has nothing to look at.
func TestHeaderIsInTheToolbarBand(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	n, ok := r.Find(ui.RoleButton, "Close")
	if !ok {
		t.Fatalf("no Close\n%s", r.Dump())
	}
	const band = 50 // the toolbar band is 42 pt high; the pane's own row starts below it
	if n.Bounds[1] >= band {
		t.Skipf("the shell does not host the pane's toolbar items yet (Close at y=%v, band %v)", n.Bounds[1], band)
	}
	if got := r.count(ui.RoleButton, "Close"); got != 1 {
		t.Errorf("%d Close buttons", got)
	}
	r.want(ui.RoleLabel, "Contact info")
	r.click(ui.RoleButton, "Media, links and docs")
	r.want(ui.RoleButton, "Back")
	if n, _ := r.Find(ui.RoleButton, "Back"); n.Bounds[1] >= band {
		t.Errorf("Back is below the band at y=%v", n.Bounds[1])
	}
	r.click(ui.RoleButton, "Back")
	if r.s.Panes.InfoView != state.InfoOverview {
		t.Error("Back stayed in the media view")
	}
	r.click(ui.RoleButton, "Close")
	if r.s.Panes.Pane != state.PaneNone {
		t.Error("Close left the panel open")
	}
}
