package accounts

import (
	"image/color"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/accounts/acct"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

type rig struct {
	*widgettest.Rig
	s  *state.State
	f  [3]*fake.Core
	sc *Screen
}

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

func link(tag abi.LinkStatusTag) *abi.Event {
	return ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = tag })
}

func me(jid, name string) *abi.Event {
	return ev(abi.EventTagMe, func(e *abi.Event) {
		e.Me.ID = []byte(jid)
		if name != "" {
			e.Me.Name, e.Me.HasName = []byte(name), true
		}
	})
}

func chat(id string, unread uint32, set func(c *abi.Chat)) abi.Chat {
	c := abi.Chat{ID: []byte(id), Name: []byte(id), Unread: unread, LastActivity: 10}
	if set != nil {
		set(&c)
	}
	return c
}

func chats(cs ...abi.Chat) *abi.Event {
	return ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: cs} })
}

// newRig is the app over three fake accounts: Ada Lovelace (linked, on
// screen), +1 555 000 0000 (linked, two unread chats, a muted, an archived and
// a locked one that do not count) and one not linked.
func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{s: state.New(), sc: New()}
	for i := range r.f {
		r.f[i] = fake.New(nil)
	}
	r.f[0].Emit(link(abi.LinkStatusTagConnected), me("5511912345678@s.whatsapp.net", "Ada Lovelace"), chats(chat("a@s.whatsapp.net", 0, nil)))
	r.f[1].Emit(link(abi.LinkStatusTagConnected), me("15550000000@s.whatsapp.net", ""), chats(
		chat("b@s.whatsapp.net", 3, nil),
		chat("c@s.whatsapp.net", 0, func(c *abi.Chat) { c.MarkedUnread = true }),
		chat("d@s.whatsapp.net", 5, func(c *abi.Chat) { c.Archived = true }),
		chat("e@s.whatsapp.net", 5, func(c *abi.Chat) { c.MutedUntil, c.HasMutedUntil = 0, true }),
		chat("f@s.whatsapp.net", 5, func(c *abi.Chat) { c.Locked = true }),
	))
	r.f[2].Emit(link(abi.LinkStatusTagUnlinked))
	for i, f := range r.f {
		r.s.AddAccount(string(rune('1'+i)), nil, f)
	}
	sh, err := shell.New(r.s, &screen.Set{Accounts: r.sc})
	if err != nil {
		t.Fatal(err)
	}
	sh.Traffic = true
	cfg := shell.Config(r.s)
	r.Rig = widgettest.Open(t, cfg, int(cfg.Width), int(cfg.Height), 1, sh)
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Frames(4)
	return r
}

func (r *rig) has(role ui.Role, name string) bool { _, ok := r.Find(role, name); return ok }

func (r *rig) open(t *testing.T) {
	t.Helper()
	r.Play(widgettest.Click(ui.RoleButton, "Switch account"), widgettest.Idle(3))
	if !r.s.AccountMenu {
		t.Fatal("the avatar did not open the switcher")
	}
}

// AC-001, AC-002: the avatar opens the switcher; each row has the name, the
// number, the unread chats, and a check on the account on screen; an unlinked
// one reads "Not linked".
func TestTheSwitcherListsTheAccounts(t *testing.T) {
	r := newRig(t)
	r.open(t)
	rows := map[string]bool{}
	for _, n := range r.Tree().Nodes {
		if n.Role == ui.RoleRadio {
			rows[string(r.Tree().Text(n.Name))] = n.State&ui.SemChecked != 0
		}
	}
	want := map[string]bool{
		"Ada Lovelace, +55 (11) 91234-5678": true,
		"+1 555 000 0000, 2 unread chats":   false,
		"Account 3, Not linked":             false,
	}
	if len(rows) != len(want) {
		t.Fatalf("rows %v", rows)
	}
	for name, checked := range want {
		got, ok := rows[name]
		if !ok || got != checked {
			t.Errorf("row %q: present %v checked %v (rows %v)", name, ok, got, rows)
		}
	}
	if !r.has(ui.RoleMenuItem, "Add account") {
		t.Error("no Add account")
	}
}

// AC-003: choosing a row switches to that number and closes the switcher.
func TestChoosingARowSwitchesAccounts(t *testing.T) {
	r := newRig(t)
	r.open(t)
	r.Play(widgettest.Click(ui.RoleRadio, "+1 555 000 0000, 2 unread chats"), widgettest.Idle(3))
	if r.s.Active != 1 || r.s.AccountMenu {
		t.Errorf("active %d, menu open %v", r.s.Active, r.s.AccountMenu)
	}
	// The number on screen has no badge but the check; the others keep theirs.
	r.open(t)
	if !r.has(ui.RoleRadio, "Ada Lovelace, +55 (11) 91234-5678") {
		t.Errorf("rows after switching:\n%s", widgettest.Dump(r.Tree()))
	}
	// Choosing the account that is on screen only closes the switcher.
	r.Play(widgettest.Click(ui.RoleRadio, "+1 555 000 0000, 2 unread chats"), widgettest.Idle(3))
	if r.s.Active != 1 {
		t.Errorf("active %d", r.s.Active)
	}
}

// Escape or a press outside closes the switcher.
func TestEscapeClosesTheSwitcher(t *testing.T) {
	r := newRig(t)
	r.open(t)
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(3))
	if r.s.AccountMenu {
		t.Error("Escape left the switcher open")
	}
}

// AC-004: Add account opens a core for the next number and shows its linking
// screen; without a way to open cores it says so and changes nothing.
func TestAddAccount(t *testing.T) {
	r := newRig(t)
	r.s.OpenCore = nil
	r.open(t)
	r.Play(widgettest.Click(ui.RoleMenuItem, "Add account"), widgettest.Idle(3))
	if len(r.s.Accounts) != 3 || len(r.s.Toasts) != 1 || string(r.s.Toasts[0].Text) != acct.ErrCannotAdd {
		t.Fatalf("accounts %d toasts %v", len(r.s.Accounts), r.s.Toasts)
	}
	var asked []string
	f4 := fake.New(nil)
	r.s.OpenCore = func(id string) (*core.Pending, core.Conn) {
		asked = append(asked, id)
		return nil, f4
	}
	r.open(t)
	r.Play(widgettest.Click(ui.RoleMenuItem, "Add account"), widgettest.Idle(3))
	if len(asked) != 1 || asked[0] != "4" {
		t.Fatalf("asked for %v", asked)
	}
	if len(r.s.Accounts) != 4 || r.s.Active != 3 || !r.s.AddingAccount || r.s.AccountMenu {
		t.Errorf("accounts %d active %d adding %v menu %v", len(r.s.Accounts), r.s.Active, r.s.AddingAccount, r.s.AccountMenu)
	}
}

// The AccountRemoved event drops the account; the window follows the one on
// screen.
func TestARemovedAccountLeavesTheList(t *testing.T) {
	r := newRig(t)
	r.s.SwitchAccount(2)
	r.Frames(2)
	r.f[2].Emit(ev(abi.EventTagAccountRemoved, func(e *abi.Event) {}))
	r.Frames(3)
	if len(r.s.Accounts) != 2 || r.s.Active != 1 || r.s.Accounts[1].ID != "2" || r.s.Accounts[1].Index != 1 {
		t.Fatalf("accounts %d active %d", len(r.s.Accounts), r.s.Active)
	}
	// An account before the one on screen leaves: the index follows.
	r.f[0].Emit(ev(abi.EventTagAccountRemoved, func(e *abi.Event) {}))
	r.Frames(3)
	if len(r.s.Accounts) != 1 || r.s.Active != 0 || r.s.Accounts[0].ID != "2" || r.s.Accounts[0].Index != 0 {
		t.Fatalf("accounts %d active %d", len(r.s.Accounts), r.s.Active)
	}
	// The last account stays: it is only unlinked.
	r.f[1].Emit(ev(abi.EventTagAccountRemoved, func(e *abi.Event) {}))
	r.Frames(3)
	if len(r.s.Accounts) != 1 {
		t.Errorf("the last account was dropped")
	}
	var unlinks int
	for _, tag := range r.f[1].SentTags() {
		if tag == abi.CommandTagUnlink {
			unlinks++
		}
	}
	if unlinks != 1 {
		t.Errorf("sent %v", r.f[1].SentTags())
	}
}

func TestRemoveSendsRemoveAccountOrUnlinksTheLast(t *testing.T) {
	r := newRig(t)
	// What an account's core is told when it opens (its settings) comes
	// first; only what Remove sends counts.
	n := len(r.f[2].SentTags())
	acct.Remove(r.s, r.s.Accounts[2])
	if got := r.f[2].SentTags()[n:]; len(got) != 1 || got[0] != abi.CommandTagRemoveAccount {
		t.Errorf("sent %v", got)
	}
	s := state.New()
	f := fake.New(nil)
	a := s.AddAccount("1", nil, f)
	n = len(f.SentTags())
	acct.Remove(s, a)
	if got := f.SentTags()[n:]; len(got) != 1 || got[0] != abi.CommandTagUnlink {
		t.Errorf("the last account: sent %v", got)
	}
}

func TestNextIDIsTheSmallestFree(t *testing.T) {
	s := state.New()
	if got := acct.NextID(s); got != "1" {
		t.Errorf("first %q", got)
	}
	s.AddAccount("1", nil, fake.New(nil))
	s.AddAccount("3", nil, fake.New(nil))
	if got := acct.NextID(s); got != "2" {
		t.Errorf("next %q", got)
	}
}

func TestUnreadElsewhereCountsOnlyChatsThatMakeNoise(t *testing.T) {
	r := newRig(t)
	now := time.Now().Unix()
	if got := acct.UnreadElsewhere(r.s, now); got != 2 {
		t.Errorf("elsewhere %d", got)
	}
	r.s.SwitchAccount(1)
	if got := acct.UnreadElsewhere(r.s, now); got != 0 {
		t.Errorf("elsewhere %d from the busy account", got)
	}
}

// pictures counts the picture boxes the switcher's rows show (a nameless
// image inside an avatar; the initials show until a picture is ready).
func (r *rig) pictures() int {
	n := 0
	for _, nd := range r.Tree().Nodes {
		if nd.Role == ui.RoleImage && len(r.Tree().Text(nd.Name)) == 0 {
			n++
		}
	}
	return n
}

// The rows show the profile pictures the core has: asked once for each linked
// number, drawn when they arrive.
func TestTheRowsShowProfilePictures(t *testing.T) {
	r := newRig(t)
	r.open(t)
	asked := 0
	for _, c := range r.f[0].Sent() {
		if c.Tag == abi.CommandTagFetchAvatar && string(c.FetchAvatar.ID) == "5511912345678@s.whatsapp.net" && !c.FetchAvatar.Full {
			asked++
		}
	}
	if asked != 1 {
		t.Errorf("the picture was asked for %d times", asked)
	}
	before := r.pictures() // the icons and the like

	r.f[0].Emit(ev(abi.EventTagAvatar, func(e *abi.Event) {
		e.Avatar = abi.EventAvatar{ID: []byte("5511912345678@s.whatsapp.net"), Path: []byte("../../gocore/demo/assets/avatars/ada.jpg"), HasPath: true}
	}))
	for i := 0; i < 200 && r.pictures() < before+2; i++ {
		r.Frames(1)
		time.Sleep(10 * time.Millisecond)
	}
	// the row's and the rail's account button (the shell's, AC-001)
	if r.pictures() != before+2 {
		t.Errorf("%d pictures after one arrived, %d before:\n%s", r.pictures(), before, widgettest.Dump(r.Tree()))
	}
}

// AC-001: the rail's account button carries a dot, in the accent, when an
// account not on screen has unread chats, and none when they are all read.
func TestTheAccountButtonHasADotForUnreadChatsElsewhere(t *testing.T) {
	r := newRig(t)
	dotAt := func() color.RGBA {
		t.Helper()
		n := r.Must(ui.RoleButton, "Switch account")
		// the titlebar's account block starts with our 26-point picture,
		// centred down it; the dot is 3 points in from its top right corner
		sc := r.Context().Scale()
		top := (n.Bounds[1]+n.Bounds[3])/2 - 13*sc
		x, y := int(n.Bounds[0]+23*sc), int(top+3*sc)
		return r.Image().RGBAAt(x, y)
	}
	// Ada is on screen; the second number has two unread chats.
	with := dotAt()
	// On the second number, the others (Ada, one not linked) have none.
	r.s.SwitchAccount(1)
	r.Frames(4)
	without := dotAt()
	if with == without {
		t.Fatalf("the button looks the same with and without unread chats elsewhere: %v", with)
	}
	// The dot is the accent: blue-leaning, unlike the field behind it.
	if with.B < with.R || with.B < 150 {
		t.Errorf("the dot's colour %v is not the accent", with)
	}
}

// AX-004: the titlebar's account button and the switcher's rows, with their
// pictures and badges, and Add account all have names a screen reader says.
func TestTheSwitcherNamesItsControls(t *testing.T) {
	r := newRig(t)
	r.LintNames("rail")
	r.open(t)
	r.LintNames("account switcher")
}
