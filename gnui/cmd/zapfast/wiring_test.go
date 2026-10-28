//go:build !gnui_release

package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The shell ticks the app lock with the window: with a password set,
// ZapFast locks once unused for "Lock after", and nothing reads the chats
// while it is locked.
func TestTheWindowLocksWhenUnused(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	now := time.Now()
	r.s.Now = func() time.Time { return now }
	r.s.Settings.AppLockHash = []byte("pbkdf2-sha256$600000$c2FsdA$aGFzaA")
	r.Frames(2)
	if r.s.Lock.Locked {
		t.Fatal("locked while in use")
	}
	now = now.Add(16 * time.Minute)
	r.Frames(3)
	if !r.s.Lock.Locked || r.s.Focused {
		t.Fatalf("locked %v, focused %v", r.s.Lock.Locked, r.s.Focused)
	}
	if _, ok := r.Find(ui.RoleListItem, "Ada Lovelace"); ok {
		t.Error("the chat list shows behind the lock")
	}
}

// A narrow window gives the open side pane the whole window, and says so.
func TestANarrowWindowGivesTheSidePaneTheWindow(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRigSized(t, f, 600, 700)
	a := r.s.Accounts[0]
	a.OpenChat(a.Model.Chats.Find([]byte("2@s.whatsapp.net")))
	r.Frames(2)
	if r.s.Panes.Covering {
		t.Fatal("covering with no pane open")
	}
	r.s.Panes.SetInfo([]byte("2@s.whatsapp.net"))
	r.s.Touch()
	r.Frames(3)
	if !r.s.Panes.Covering || r.s.Panes.Pane != state.PaneInfo {
		t.Errorf("covering %v, pane %v", r.s.Panes.Covering, r.s.Panes.Pane)
	}
	if _, ok := r.Find(ui.RoleButton, "Back"); !ok {
		t.Errorf("no Back over the narrow chat:\n%s", widgettest.Dump(r.Tree()))
	}
}

// SH-007: a narrow window slides an opened chat in over the list, which
// stays beneath it until the chat lands and takes no press meanwhile; going
// back brings the list in at once, without the chat beneath.
func TestANarrowWindowSlidesBetweenTheListAndTheChat(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRigSized(t, f, 500, 700)
	a := r.s.Accounts[0]
	r.Click(ui.RoleListItem, "Ada Lovelace")
	ada := a.Model.Chats.Find([]byte("1@s.whatsapp.net"))
	if a.Open != ada {
		t.Fatalf("open %v", a.Open)
	}
	r.Frame()
	if _, ok := r.Find(ui.RoleListItem, "Grace Hopper"); !ok {
		t.Fatal("the list is not beneath the sliding chat")
	}
	if _, ok := r.Find(ui.RoleTextField, "Message"); !ok {
		t.Error("the chat is not coming in")
	}
	if _, ok := r.Find(ui.RoleButton, "Back to chats"); !ok {
		t.Error("the sliding chat's header has no Back")
	}
	r.Play(widgettest.Click(ui.RoleListItem, "Grace Hopper").Through())
	if a.Open != ada {
		t.Error("a press mid-slide reached the list beneath")
	}
	r.Frames(14)
	if _, ok := r.Find(ui.RoleListItem, "Grace Hopper"); ok {
		t.Error("the list stayed once the chat landed")
	}
	a.OpenChat(0)
	r.s.Touch()
	r.Frame()
	if _, ok := r.Find(ui.RoleListItem, "Grace Hopper"); !ok {
		t.Error("going back does not bring the list")
	}
	if _, ok := r.Find(ui.RoleTextField, "Message"); ok {
		t.Error("the closed chat is beneath the list")
	}
}

// The banner under the header: history loading with its percentage, then
// the connection dropping, with Retry asking the core to reconnect
// (SH-009, LG-012).
func TestTheBannerSaysHowTheLinkIs(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	r.open(adaJID)
	f.Emit(ev(abi.EventTagSyncing, func(e *abi.Event) { e.Syncing.Syncing = true }),
		ev(abi.EventTagSyncProgress, func(e *abi.Event) { e.SyncProgress.Percent = 42 }))
	r.Frames(100) // past the banner's grace
	if _, ok := r.Find(ui.RoleGroup, "Info: Loading chat history… 42%"); !ok {
		t.Errorf("no sync banner:\n%s", widgettest.Dump(r.Tree()))
	}
	f.Emit(ev(abi.EventTagSyncing, func(e *abi.Event) { e.Syncing.Syncing = false }),
		ev(abi.EventTagLink, func(e *abi.Event) {
			e.Link.Status.Tag = abi.LinkStatusTagDisconnected
			e.Link.Status.Disconnected.Reason = []byte("timeout")
		}))
	r.Frames(3)
	if _, ok := r.Find(ui.RoleGroup, "Warning: Offline (timeout). Reconnecting…"); !ok {
		t.Fatalf("no offline banner:\n%s", widgettest.Dump(r.Tree()))
	}
	r.Play(widgettest.Click(ui.RoleButton, "Retry"), widgettest.Idle(2))
	if sentTag(f, abi.CommandTagReconnect) != 1 {
		t.Error("Retry did not reconnect")
	}
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }))
	r.Frames(3)
	if _, ok := r.Find(ui.RoleButton, "Retry"); ok {
		t.Error("the banner stayed once connected")
	}
}

// Every control of the main window has a name a screen reader says: the
// rail, the toolbar, the panes, with a chat and its info open (AX-004).
func TestTheMainWindowNamesItsControls(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	r.LintNames("chat list")
	r.open(adaJID)
	r.s.Panes.SetInfo([]byte(adaJID))
	r.s.Touch()
	r.Frames(3)
	r.LintNames("chat and info")
	for _, name := range []string{"Chats", "Calls", "Starred messages", "Settings", "Switch account"} {
		n, ok := r.Find(ui.RoleButton, name)
		if !ok {
			t.Errorf("the rail has no button %q", name)
			continue
		}
		if name == "Chats" && n.State&ui.SemSelected == 0 {
			t.Error("Chats is not marked as the page on screen")
		}
	}
}

// SH-004: Archived in the rail shows how many archived chats are unread.
func TestArchivedShowsItsUnreadChats(t *testing.T) {
	f := fake.New(nil)
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
				{ID: []byte(adaJID), Name: []byte("Ada Lovelace"), LastActivity: 20},
				{ID: []byte("3@s.whatsapp.net"), Name: []byte("Old Friend"), LastActivity: 5, Archived: true, Unread: 2},
				{ID: []byte("4@s.whatsapp.net"), Name: []byte("Older Friend"), LastActivity: 4, Archived: true, Unread: 1},
				{ID: []byte("5@s.whatsapp.net"), Name: []byte("Quiet"), LastActivity: 3, Archived: true},
			}}
		}))
	r := newRig(t, f)
	r.Frames(3)
	if _, ok := r.Find(ui.RoleLabel, "2"); !ok {
		t.Errorf("no badge of 2:\n%s", widgettest.Dump(r.Tree()))
	}
}

// Workers post toasts while the window draws them (SH-010): each shows as
// a group, stacked at the bottom right, errors with Copy this message.
func TestToastsPostedFromWorkersWhileFramesRun(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRigSized(t, f, 1000, 700)
	r.open(adaJID)
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				r.s.Toast([]byte(fmt.Sprint("worker ", g, " ", i)), i%3 == 0)
			}
		}()
	}
	for range 20 {
		r.Frames(1)
	}
	wg.Wait()
	r.s.Toast([]byte("Saved"), false)
	r.s.Toast([]byte("It failed"), true)
	r.Frames(3)
	info, ok := r.Find(ui.RoleGroup, "Info: Saved")
	if !ok {
		t.Fatalf("no info toast:\n%s", widgettest.Dump(r.Tree()))
	}
	errNode, ok := r.Find(ui.RoleGroup, "Error: It failed")
	if !ok {
		t.Fatalf("no error toast:\n%s", widgettest.Dump(r.Tree()))
	}
	// the newest at the bottom, right-aligned, above the composer
	if !(errNode.Bounds[1] > info.Bounds[1]) || errNode.Bounds[2] != info.Bounds[2] {
		t.Errorf("not stacked: info %v error %v", info.Bounds, errNode.Bounds)
	}
	if errNode.Bounds[2] < 1000*0.9 {
		t.Errorf("not at the right: %v", errNode.Bounds)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Copy this message"), widgettest.Idle(1))
	// An error's reason goes on the wrapped line under it, whole: the
	// title is one line that fades.
	r.s.Toast([]byte("Could not open the attachment: the file is no longer where it was saved"), true)
	r.Frames(4)
	n, ok := r.Find(ui.RoleGroup, "Error: Could not open the attachment")
	if tr := r.Tree(); !ok || string(tr.Text(n.Value)) != "the file is no longer where it was saved" {
		t.Errorf("the error toast: %+v (%v)", n, ok)
	}
}

// A Ticker runs once per main-window frame, the playback's among them.
func TestTickersRunEveryFrame(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	if len(r.sh.Set.Tickers) == 0 {
		t.Fatal("the playback is not a ticker")
	}
	n := 0
	r.sh.Set.Tickers = append(r.sh.Set.Tickers, screen.TickFunc(func(e *screen.Env) { n++ }))
	r.Frames(5)
	if n != 5 {
		t.Errorf("%d ticks in 5 frames", n)
	}
}

// AC-006: with several accounts the linking screen has the switcher's
// button in its top-left corner, after the window's buttons; it opens the
// switcher there.
func TestTheLinkingScreenHasTheSwitcherInItsCorner(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	if _, ok := r.Find(ui.RoleButton, "Switch account"); !ok {
		t.Fatal("no switcher on the rail")
	}
	g := fake.New(nil)
	g.Emit(ev(abi.EventTagLink, func(e *abi.Event) {
		e.Link.Status.Tag = abi.LinkStatusTagUnlinked
		e.Link.Status.Unlinked.QR, e.Link.Status.Unlinked.HasQR = []byte("qr"), true
	}))
	r.s.AddAccount("2", nil, g)
	r.s.SwitchAccount(1)
	r.Frames(4)
	if _, ok := r.Find(ui.RoleButton, "Chats"); ok {
		t.Fatal("not the linking screen")
	}
	n, ok := r.Find(ui.RoleButton, "Switch account")
	if !ok {
		t.Fatalf("no switcher on the linking screen:\n%s", widgettest.Dump(r.Tree()))
	}
	if n.Bounds[0] < 60 || n.Bounds[0] > 160 || n.Bounds[1] > 40 {
		t.Errorf("the switcher is not in the top-left corner: %v", n.Bounds)
	}
	r.LintNames("linking screen with the switcher's button") // AX-004
	r.Play(widgettest.Click(ui.RoleButton, "Switch account"), widgettest.Idle(3))
	if !r.s.AccountMenu {
		t.Error("the switcher did not open")
	}
	if _, ok := r.Find(ui.RoleMenuItem, "Add account"); !ok {
		t.Errorf("no switcher shown:\n%s", widgettest.Dump(r.Tree()))
	}
}

// SH-015: dragging the chat list's edge resizes it within 260–520 points,
// and the width is a setting.
func TestDraggingTheListsEdgeResizes(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRigSized(t, f, 1200, 700)
	before := r.s.Settings.SidebarWidth
	r.Play(widgettest.Drag(ui.RoleSplitter, "Resize", 60, 6), widgettest.Idle(2))
	if got := r.s.Settings.SidebarWidth; got <= before || got > 520 {
		t.Errorf("width %v after dragging from %v", got, before)
	}
	r.Play(widgettest.Drag(ui.RoleSplitter, "Resize", 800, 6), widgettest.Idle(2))
	if got := r.s.Settings.SidebarWidth; got != 520 {
		t.Errorf("width %v, want the widest, 520", got)
	}
	r.Play(widgettest.Drag(ui.RoleSplitter, "Resize", -900, 6), widgettest.Idle(2))
	if got := r.s.Settings.SidebarWidth; got != 260 {
		t.Errorf("width %v, want the narrowest, 260", got)
	}
}

// DM-002: --demo-page puts the window in upstream's demo states once the
// chats are in: here the first chat open with its info, the shortcuts
// listed, the list folded; flags parse into the demo.
func TestDemoPages(t *testing.T) {
	o, err := parseOptions([]string{"-demo-shot", "x.png", "--demo-page", "chat,light"})
	if err != nil || !o.demo || o.out != "x.png" || o.page != "chat,light" {
		t.Fatalf("options %+v, %v", o, err)
	}
	if p := newDemoPage(o.page); !p.light() {
		t.Error("light not seen")
	}
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	r.sh.Set.Tickers = append(r.sh.Set.Tickers, newDemoPage("info,shortcuts,nosidebar,toasts"))
	r.Frames(4)
	a := r.s.Accounts[0]
	if a.Open == 0 || r.s.Panes.Pane != state.PaneInfo || r.s.Dialog.Kind != state.DialogShortcuts || !r.s.Panes.ListHidden || len(r.s.Toasts) != 2 {
		t.Errorf("open %v pane %v dialog %v hidden %v toasts %d", a.Open, r.s.Panes.Pane, r.s.Dialog.Kind, r.s.Panes.ListHidden, len(r.s.Toasts))
	}
}

// badgeWatch counts, every frame after the events, the unread chats as the
// Dock badge does (sys's rule), and keeps the most seen.
type badgeWatch struct{ most int }

func (b *badgeWatch) Frame(e *screen.Env) {
	now := e.S.Now().Unix()
	n := 0
	for _, a := range e.S.Accounts {
		for _, ref := range a.Model.Chats.Order() {
			c := a.Model.Chats.At(ref)
			if !c.Archived() && !c.Locked() && !c.Muted(now) && (c.Unread() > 0 || c.MarkedUnread()) {
				n++
			}
		}
	}
	b.most = max(b.most, n)
}

// A message arriving in the open chat while it is read is read here at
// once (upstream's mark_read): the badge never counts it, and MarkRead
// goes to the core. Opening an unread chat reads it the same way.
func TestTheOpenChatsMessageNeverCountsAsUnread(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	r.open(adaJID)
	if !r.s.Focused {
		t.Fatal("the rig's window is not focused")
	}
	w := &badgeWatch{}
	r.sh.Set.Tickers = append(r.sh.Set.Tickers, w)
	before := sentTag(f, abi.CommandTagMarkRead)
	f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(adaJID), Name: []byte("Ada Lovelace"), LastActivity: 30, Unread: 1}
	}))
	r.Frames(4)
	if w.most != 0 {
		t.Errorf("the badge counted %d for the open chat's message", w.most)
	}
	if sentTag(f, abi.CommandTagMarkRead) != before+1 {
		t.Errorf("MarkRead sent %d times", sentTag(f, abi.CommandTagMarkRead)-before)
	}
	// Grace's message waits unread; opening her chat reads it at once.
	f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte("2@s.whatsapp.net"), Name: []byte("Grace Hopper"), LastActivity: 31, Unread: 2}
	}))
	r.Frames(2)
	if w.most != 1 {
		t.Errorf("the badge counted %d for another chat's message", w.most)
	}
	a := r.s.Accounts[0]
	ref := a.Model.Chats.Find([]byte("2@s.whatsapp.net"))
	a.OpenChat(ref)
	if a.Model.Chats.At(ref).Unread() != 0 {
		t.Error("opening the chat left it unread until the core answers")
	}
}

// SH-008: the empty pane: the logo, ZapFast, where the chats are, and the
// shortcut hint while the hints show (zf:src/ui/conversation.rs:274-315).
func TestTheEmptyPaneHasTheLogoAndTheHint(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	const hint = "⌘+K to search · ? for keyboard shortcuts"
	for _, n := range []string{"Zap", "Select a chat on the left.", hint} {
		if _, ok := r.Find(ui.RoleLabel, n); !ok {
			t.Errorf("no %q:\n%s", n, widgettest.Dump(r.Tree()))
		}
	}
	r.s.Settings.ShowShortcutHints = false
	r.s.Touch()
	r.Frames(2)
	if _, ok := r.Find(ui.RoleLabel, hint); ok {
		t.Error("the hint shows with the hints off")
	}
}

// SH-005: in a short window the rail scrolls, and a button the keyboard
// focuses comes into view.
func TestTheRailScrollsInAShortWindow(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRigSized(t, f, 900, 240)
	if _, ok := r.Find(ui.RoleScrollArea, "Rail"); !ok {
		t.Fatalf("no scrolling rail:\n%s", widgettest.Dump(r.Tree()))
	}
	n := r.Must(ui.RoleButton, "Settings")
	if n.Bounds[3] <= 240 {
		t.Fatalf("Settings fits a short window (%v): nothing to scroll", n.Bounds)
	}
	r.Context().SetFocus(n.Key)
	r.Context().SetFocusVisible(true)
	r.Frames(30)
	if n := r.Must(ui.RoleButton, "Settings"); n.Bounds[3] > 240 || n.Bounds[1] < 52 {
		t.Errorf("the focused Settings is not in view: %v", n.Bounds)
	}
}

// Every demo part this port knows applies without trouble, together.
func TestEveryDemoPartApplies(t *testing.T) {
	var parts []string
	for _, p := range demoParts {
		switch p {
		case "login", "phone", "pair", "app-lock", "empty", "calls", "call-entries", "account-menu", "accounts", "locked", "locked-prompt":
		default:
			parts = append(parts, p)
		}
	}
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	page := newDemoPage(strings.Join(parts, ","))
	r.sh.Set.Tickers = append(r.sh.Set.Tickers, page)
	r.Frames(12)
	if !page.done {
		t.Fatal("the page never applied")
	}
	if r.s.Accounts[0].Open == 0 || r.s.Settings.Theme != state.ThemeDark || r.s.Panes.ListHidden != true {
		t.Errorf("open %v theme %v", r.s.Accounts[0].Open, r.s.Settings.Theme)
	}
	login := newDemoPage("pair")
	g := fake.New(nil)
	linked(g)
	r2 := newRig(t, g)
	r2.sh.Set.Tickers = append(r2.sh.Set.Tickers, login)
	r2.Frames(6)
	if _, ok := r2.Find(ui.RoleButton, "Chats"); ok || r2.s.Accounts[0].Model.Linked() {
		t.Error("the pair page shows the chats")
	}
}

// DM-004, DM-006: the tour's flags, and its player waits for the samples
// and Space, plays upstream's launch steps over the window, and holds.
func TestTheTourWaitsForSpaceAndPlays(t *testing.T) {
	o, err := parseOptions([]string{"-demo-tour-frames", "dir", "-demo-fps", "12", "-demo-size", "1280x800"})
	if err != nil || !o.demo || !o.tour.on || o.tour.frames != "dir" || o.tour.fps != 12 {
		t.Fatalf("options %+v, %v", o.tour, err)
	}
	if w, h, err := o.tour.windowSize(); w != 1280 || h != 800 || err != nil {
		t.Errorf("size %v×%v, %v", w, h, err)
	}
	if _, _, err := (&tourOptions{size: "12x9"}).windowSize(); err == nil {
		t.Error("a tiny size passed")
	}
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	tr := newTour(r.s, r.sh, true)
	tr.delay = 500
	var in ui.Input
	in.Time = 1000
	if !tr.Script(0, &in) || tr.phase != tourWaiting {
		t.Fatal("the tour did not wait")
	}
	// -demo-tour-delay counts on the input's clock from the first frame
	if tr.start != 1500 {
		t.Errorf("the automatic start at %d", tr.start)
	}
	in.PushKey(ui.KeyEvent{Code: ui.KeySpace, Down: true})
	tr.Script(1, &in)
	// The fake core has none of the demo's chats: the tour waits on.
	if tr.phase != tourWaiting {
		t.Error("the tour started without the demo's chats")
	}
	if len(tr.steps()) < 40 {
		t.Errorf("%d steps", len(tr.steps()))
	}
	tr.script = "whats-new"
	if len(tr.steps()) < 60 {
		t.Errorf("what's new: %d steps", len(tr.steps()))
	}
	if _, err := parseOptions([]string{"-demo-tour", "-demo-tour-script", "whats-new"}); err != nil {
		t.Error(err)
	}
	// DM-006: -demo-tour-events and -demo-hover
	o, err = parseOptions([]string{"-demo-tour", "-demo-tour-events", "t.json", "-demo-hover", "590, 450"})
	if err != nil || o.tour.events != "t.json" {
		t.Fatalf("%+v %v", o.tour, err)
	}
	if x, y, on, err := o.tour.hoverAt(); !on || x != 590 || y != 450 || err != nil {
		t.Errorf("hover %v,%v %v %v", x, y, on, err)
	}
	if _, _, _, err := (&tourOptions{hover: "590"}).hoverAt(); err == nil {
		t.Error("a bad -demo-hover passed")
	}
	h := &hoverer{Shell: r.sh, x: 10, y: 20}
	var hin ui.Input
	if !h.Script(0, &hin) || hin.MouseX != 10*r.sh.Context().Scale() || hin.MouseY != 20*r.sh.Context().Scale() || !hin.Inside {
		t.Errorf("the held pointer %v,%v", hin.MouseX, hin.MouseY)
	}
}

// LO-005 end to end: a location the core refused comes back in its
// dialog, the spot in the field, ready to send again.
func TestARefusedLocationIsBackInItsDialog(t *testing.T) {
	r, f := keyRig(t)
	r.open(adaJID)
	f.Emit(ev(abi.EventTagSendRefused, func(e *abi.Event) {
		e.SendRefused.Chat, e.SendRefused.Reason = []byte(adaJID), abi.RefusalOffline
		e.SendRefused.Unsent.Tag = abi.UnsentTagLocation
		e.SendRefused.Unsent.Location.Latitude, e.SendRefused.Unsent.Location.Longitude = 40.7128, -74.006
	}))
	r.Frames(4)
	n, ok := r.Find(ui.RoleTextField, "Paste a map link or coordinates")
	if !ok {
		t.Fatalf("no location dialog: %v", r.s.Dialog.Kind)
	}
	if got := string(r.Tree().Text(n.Value)); got != "40.71280, -74.00600" {
		t.Errorf("the field has %q", got)
	}
	if _, ok := r.Find(ui.RoleButton, "Send"); !ok {
		t.Error("no Send")
	}
}

// sidePress presses a mouse side button at the next frame.
type sidePress struct{ b int }

func (p *sidePress) Frame(e *screen.Env) {
	if p.b >= 0 {
		e.C().Input().Pressed[p.b] = true
		p.b = -1
	}
}

// SH-012: the mouse's Back goes back through the visited places (in a
// narrow window, from a chat opened from the list to the list) and Forward
// forward again; neither acts while a dialog is open, and a place whose
// chat is gone is skipped.
func TestTheMouseSideButtonsWalkTheHistory(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRigSized(t, f, 500, 700)
	a := r.s.Accounts[0]
	press := &sidePress{b: -1}
	r.sh.Set.Tickers = append(r.sh.Set.Tickers, press)
	side := func(b int) {
		press.b = b
		r.Frames(20)
	}
	ada, grace := a.Model.Chats.Find([]byte(adaJID)), a.Model.Chats.Find([]byte("2@s.whatsapp.net"))
	r.Click(ui.RoleListItem, "Ada Lovelace")
	r.Frames(20)
	side(ui.ButtonBack)
	if a.Open != 0 {
		t.Fatalf("Back left %v open", a.Open)
	}
	if _, ok := r.Find(ui.RoleListItem, "Grace Hopper"); !ok {
		t.Error("Back did not show the list")
	}
	side(ui.ButtonForward)
	if a.Open != ada {
		t.Errorf("Forward opened %v", a.Open)
	}
	r.s.OpenDialog(state.Dialog{Kind: state.DialogShortcuts})
	side(ui.ButtonBack)
	if a.Open != ada {
		t.Error("Back acted under a dialog")
	}
	r.s.CloseDialog()
	// The Calls page, Grace, Ada, then Grace's chat goes: Back skips its
	// place, to the Calls page.
	a.OpenChat(0)
	r.s.Page = state.PageCalls
	r.s.Touch()
	r.Frames(20)
	r.s.Page = state.PageChats
	a.OpenChat(grace)
	r.Frames(20)
	a.OpenChat(ada)
	r.Frames(20)
	f.Emit(ev(abi.EventTagChatRemoved, func(e *abi.Event) { e.ChatRemoved.Chat = []byte("2@s.whatsapp.net") }))
	r.Frames(4)
	side(ui.ButtonBack)
	if a.Open != 0 || r.s.Page != state.PageCalls {
		t.Errorf("Back went to %v on page %v, not over Grace's place to the Calls page", a.Open, r.s.Page)
	}
}

// AX-001: what the reader is told reaches the screen reader without moving
// the focus: a drop after being connected ("Connection lost"), the retry
// ("Connecting", once), getting back ("Back online", a toast), an error
// before it all, interrupting; while locked only the connection is said.
func TestTheScreenReaderIsToldWhatHappens(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	said := func() (string, bool) {
		r.Frame()
		tr := r.Tree()
		return string(tr.Text(tr.Announce)), tr.AnnouncePolite
	}
	link := func(tag abi.LinkStatusTag) {
		f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = tag }))
	}
	r.Frames(4)
	link(abi.LinkStatusTagDisconnected)
	if got, polite := said(); got != "Connection lost" || !polite {
		t.Errorf("a drop: %q (polite %v)", got, polite)
	}
	link(abi.LinkStatusTagConnecting)
	if got, _ := said(); got != "Connecting" {
		t.Errorf("the retry: %q", got)
	}
	link(abi.LinkStatusTagDisconnected)
	link(abi.LinkStatusTagConnecting)
	r.s.Toast([]byte("Could not send"), true)
	link(abi.LinkStatusTagConnected)
	if got, polite := said(); got != "Could not send. Back online" || polite {
		t.Errorf("back: %q (polite %v)", got, polite)
	}
	r.s.Settings.AppLockHash = []byte("pbkdf2-sha256$600000$c2FsdA$aGFzaA")
	r.s.Lock.Locked = true
	r.s.Toast([]byte("Copied"), false)
	link(abi.LinkStatusTagDisconnected)
	if got, _ := said(); got != "Connection lost" {
		t.Errorf("locked: %q", got)
	}
}
