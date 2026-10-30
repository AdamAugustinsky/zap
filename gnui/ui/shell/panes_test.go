package shell

import (
	"testing"

	"gonativeui/app"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The panes' user-visible layout and focus (docs/gnui-panes-inventory.md),
// pinned through the slots so that a later framework workspace can replace
// panes.go and the layout in main without changing them.

// slot is a pane's screen that notes where it was built each frame.
type slot struct {
	name  string
	width float32 // points, for a side pane
	built bool
	rect  screen.Rect
}

func (p *slot) Init(*screen.Env) error { return nil }
func (p *slot) Build(e *screen.Env, r screen.Rect) {
	p.built, p.rect = true, r
	e.Begin(r, p.name)
	rexui.Button(e.K.Kit, p.name, rexui.ButtonOpts{}) // something to press in the pane
	e.End()
}
func (p *slot) Toolbar(_ *screen.Env, tb *rexui.Toolbar) { tb.Title() }
func (p *slot) Width(e *screen.Env) float32              { return e.Pt(p.width) }

type paneRig struct {
	t                *testing.T
	s                *state.State
	sh               *Shell
	a                *state.Account
	r                *widgettest.Rig
	list, chat, side *slot
}

func newPaneRig(t *testing.T, w int) *paneRig {
	t.Helper()
	p := &paneRig{t: t, s: state.New(), list: &slot{name: "List slot"}, chat: &slot{name: "Chat slot"}, side: &slot{name: "Side slot", width: 380}}
	f := fake.New(nil)
	link := &abi.Event{Tag: abi.EventTagLink}
	link.Link.Status.Tag = abi.LinkStatusTagConnected
	f.Emit(link, &abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
		{ID: []byte("1@s.whatsapp.net"), Name: []byte("Ada Lovelace"), LastActivity: 5},
		{ID: []byte("2@s.whatsapp.net"), Name: []byte("Grace Hopper"), LastActivity: 7, Pinned: true, PinnedAt: 9},
	}}})
	sh, err := New(p.s, &screen.Set{ChatList: p.list, Conversation: p.chat, Info: p.side, Search: p.side})
	if err != nil {
		t.Fatal(err)
	}
	p.sh = sh
	p.a = p.s.AddAccount("1", nil, f)
	p.r = widgettest.Open(t, Config(p.s), w, 720, 1, sh)
	p.frames()
	return p
}

// frames draws a few frames, noting which slots were built in the last.
func (p *paneRig) frames() {
	p.s.Touch()
	p.r.Frames(2)
	for _, s := range []*slot{p.list, p.chat, p.side} {
		s.built = false
	}
	p.s.Touch()
	p.r.Frames(1)
}

func (p *paneRig) open(id string) {
	p.a.OpenChat(p.a.Model.Chats.Find([]byte(id)))
	p.frames()
}

// The list is as wide as the settings say, within 260 to 520 points, and
// goes with ⌘B; the side pane takes its width beside an open chat, leaving
// the conversation at least 320 points.
func TestThePanesWidths(t *testing.T) {
	p := newPaneRig(t, 1100)
	for _, c := range []struct{ set, want float32 }{{410, 410}, {100, minListPt}, {900, maxListPt}} {
		p.s.Settings.SidebarWidth = c.set
		p.frames()
		if !p.list.built || p.list.rect.W != c.want {
			t.Errorf("sidebar width %v: list built %v at %v, want %v", c.set, p.list.built, p.list.rect.W, c.want)
		}
	}
	if p.chat.built {
		t.Error("the conversation's slot built with no chat open")
	}
	p.s.Settings.SidebarWidth = 300
	p.s.Panes.ListHidden = true
	p.frames()
	if p.list.built {
		t.Error("the hidden list still built")
	}
	p.s.Panes.ListHidden = false

	p.open("1@s.whatsapp.net")
	p.s.Panes.SetInfo([]byte("1@s.whatsapp.net"))
	p.frames()
	if !p.chat.built || !p.side.built || p.side.rect.W != 380 {
		t.Errorf("chat %v, side %v at %v, want both with the side 380 wide", p.chat.built, p.side.built, p.side.rect.W)
	}
	if p.s.Panes.Covering {
		t.Error("a wide window's side pane covers the window")
	}
	p.side.width = 5000
	p.frames()
	// It takes the room the list and the conversation's 320 points leave, up
	// to the window's inset.
	if right := theme.Snap(1100 - p.sh.env.K.Theme().Px.Inset); !p.side.built || p.chat.rect.W != 320 || p.side.rect.X+p.side.rect.W != right {
		t.Errorf("a side pane asking 5000 is %v wide, to %v, beside a conversation %v wide; want it to %v beside 320",
			p.side.rect.W, p.side.rect.X+p.side.rect.W, p.chat.rect.W, right)
	}
	// A width below the pane's least (a hand-edited or old settings file)
	// opens it at its least, as upstream clamps it, rather than never.
	p.side.width = 0
	p.frames()
	if !p.side.built || p.side.rect.W != 240 {
		t.Errorf("a side pane asking 0 built %v at %v, want 240", p.side.built, p.side.rect.W)
	}
	// The side pane belongs to its chat: opening another closes it.
	p.side.width = 380
	p.open("2@s.whatsapp.net")
	if p.side.built || p.s.Panes.Pane != state.PaneNone {
		t.Error("the side pane stayed open on another chat")
	}
}

// A narrow window (under 720 points) shows the list alone, then the chat
// alone once one opens, and a side pane over the whole window.
func TestANarrowWindowShowsOnePane(t *testing.T) {
	p := newPaneRig(t, 700)
	if !p.list.built || p.chat.built || p.list.rect.W < 700-railPt-40 {
		t.Errorf("narrow, nothing open: list %v at %v, chat %v", p.list.built, p.list.rect.W, p.chat.built)
	}
	p.open("1@s.whatsapp.net")
	if p.list.built || !p.chat.built {
		t.Errorf("narrow, a chat open: list %v, chat %v", p.list.built, p.chat.built)
	}
	p.s.Panes.SetInfo([]byte("1@s.whatsapp.net"))
	p.frames()
	if !p.s.Panes.Covering || !p.side.built || p.chat.built || p.side.rect.W < 600 {
		t.Errorf("narrow, info open: covering %v, side %v at %v, chat %v", p.s.Panes.Covering, p.side.built, p.side.rect.W, p.chat.built)
	}
}

// The pane in use follows a chat that opens and a press in another pane,
// and falls back to the open chat when the side pane in use closes; an
// account switch and back keeps the parked chat and the hidden list.
func TestThePaneInUse(t *testing.T) {
	p := newPaneRig(t, 1100)
	if p.sh.panes.Focus() != paneList {
		t.Errorf("at start the pane in use is %v, want the list", p.sh.panes.Focus())
	}
	p.open("1@s.whatsapp.net")
	if p.sh.panes.Focus() != paneChat {
		t.Errorf("an opened chat left the pane in use at %v", p.sh.panes.Focus())
	}
	p.r.Click(ui.RoleButton, "List slot")
	p.frames()
	if p.sh.panes.Focus() != paneList {
		t.Errorf("a press in the list left the pane in use at %v", p.sh.panes.Focus())
	}
	p.r.Click(ui.RoleButton, "Chat slot")
	p.frames()
	if p.sh.panes.Focus() != paneChat {
		t.Errorf("a press in the chat left the pane in use at %v", p.sh.panes.Focus())
	}
	p.s.Panes.SetInfo([]byte("1@s.whatsapp.net"))
	p.frames()
	p.r.Click(ui.RoleButton, "Side slot")
	p.frames()
	if p.sh.panes.Focus() != paneSide {
		t.Errorf("a press in the side pane left the pane in use at %v", p.sh.panes.Focus())
	}
	p.s.Panes.SetInfo(nil)
	p.frames()
	if p.sh.panes.Focus() != paneChat {
		t.Errorf("the side pane in use closed, and the pane in use is %v, want the open chat", p.sh.panes.Focus())
	}

	open := p.a.Model.Chats.Find([]byte("2@s.whatsapp.net"))
	p.a.OpenChat(open)
	p.s.Panes.ListHidden = true
	f := fake.New(nil)
	b := p.s.AddAccount("2", nil, f)
	p.s.SwitchAccount(b.Index)
	p.frames()
	p.s.SwitchAccount(p.a.Index)
	p.frames()
	if p.a.Open != open || !p.s.Panes.ListHidden || p.sh.panes.Focus() != paneChat {
		t.Errorf("back on account 1: open %v (want %v), list hidden %v, pane in use %v", p.a.Open, open, p.s.Panes.ListHidden, p.sh.panes.Focus())
	}
}

// The in-chat search keeps within 300 to 520 points, as upstream's
// (zf:src/ui/pane.rs:22-23), where info keeps its own 240 to 640.
func TestTheSearchPaneIsClamped(t *testing.T) {
	p := newPaneRig(t, 1440)
	p.open("1@s.whatsapp.net")
	p.s.Panes.Pane = state.PaneSearch
	for _, c := range []struct{ ask, want float32 }{{100, 300}, {380, 380}, {5000, 520}} {
		p.side.width = c.ask
		p.frames()
		if !p.side.built || p.side.rect.W != c.want {
			t.Errorf("search asking %v: built %v at %v, want %v", c.ask, p.side.built, p.side.rect.W, c.want)
		}
	}
	p.s.Panes.SetInfo([]byte("1@s.whatsapp.net"))
	p.side.width = 5000
	p.frames()
	if !p.side.built || p.side.rect.W != 640 {
		t.Errorf("info asking 5000: built %v at %v, want 640", p.side.built, p.side.rect.W)
	}
}

// Closing the chat in use leaves its pane empty, and the list is the pane
// in use; a chat opened again takes it back.
func TestClosingTheChatFocusesTheList(t *testing.T) {
	p := newPaneRig(t, 1100)
	p.open("1@s.whatsapp.net")
	if p.sh.panes.Focus() != paneChat {
		t.Fatalf("an opened chat left the pane in use at %v", p.sh.panes.Focus())
	}
	p.a.OpenChat(0)
	p.frames()
	if p.sh.panes.Focus() != paneList {
		t.Errorf("the chat in use closed, and the pane in use is %v, want the list", p.sh.panes.Focus())
	}
	p.open("1@s.whatsapp.net")
	if p.sh.panes.Focus() != paneChat {
		t.Errorf("the chat opened again, and the pane in use is %v, want the chat", p.sh.panes.Focus())
	}
}

// With no window open (resident), the app's drain post applies every
// account's events, and a failed core leaves the others draining.
func TestTheResidentDrainKeepsEveryAccountCurrent(t *testing.T) {
	s := state.New()
	h := NewApp(&Shell{S: s, Set: &screen.Set{}})
	one, two := fake.New(nil), fake.New(nil)
	a := s.AddAccount("1", nil, one)
	b := s.AddAccount("2", nil, two)
	chats := func(f *fake.Core, id string) {
		f.Emit(&abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(id), Name: []byte("x"), LastActivity: 1}}}})
	}
	chats(one, "1@s.whatsapp.net")
	chats(two, "1@s.whatsapp.net")
	h.Event(nil, &app.Event{Kind: app.EvPost, ID: postCore})
	if a.Model.Chats.Len() != 1 || b.Model.Chats.Len() != 1 {
		t.Fatalf("resident drain: %d and %d chats", a.Model.Chats.Len(), b.Model.Chats.Len())
	}
	one.Close(0)
	two.Emit(&abi.Event{Tag: abi.EventTagChatUpdated, ChatUpdated: abi.EventChatUpdated{Chat: abi.Chat{ID: []byte("9@s.whatsapp.net"), Name: []byte("y"), LastActivity: 2}}})
	h.Event(nil, &app.Event{Kind: app.EvPost, ID: postCore})
	if a.Err == "" || b.Model.Chats.Find([]byte("9@s.whatsapp.net")) == 0 || a.Model.Chats.Find([]byte("9@s.whatsapp.net")) != 0 {
		t.Errorf("after account 1's core failed: err %q, account 2 got the chat %v, account 1 %v",
			a.Err, b.Model.Chats.Find([]byte("9@s.whatsapp.net")) != 0, a.Model.Chats.Find([]byte("9@s.whatsapp.net")) != 0)
	}
}
