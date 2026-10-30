package state_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/core/fake"
	"zapfast/state"
)

// Recovery contracts: what the window keeps across a restart, a reconnect,
// and a core that fails or is replaced. The cores are fakes; the archive's
// part (drafts, pins, the chat list) is what a fake replays.

const hopper = "3@s.whatsapp.net"

// pinnedChats is a chat list with two pinned chats (Hopper pinned last, so
// first) above two unpinned ones by activity; Grace has unread messages.
func pinnedChats(batch uint32) *abi.Event {
	return ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: batch, Last: true, Chats: []abi.Chat{
			{ID: []byte(ada), Name: []byte("Ada"), LastActivity: 5},
			{ID: []byte(grace), Name: []byte("Grace"), LastActivity: 4, Pinned: true, PinnedAt: 100, Unread: 2},
			{ID: []byte(hopper), Name: []byte("Hopper"), LastActivity: 3, Pinned: true, PinnedAt: 200},
			{ID: []byte("4@s.whatsapp.net"), Name: []byte("Babbage"), LastActivity: 9},
		}}
	})
}

func linkStatus(tag abi.LinkStatusTag) *abi.Event {
	return ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = tag })
}

// orderIDs is the list's base order as chat ids.
func orderIDs(a *state.Account) []string {
	var out []string
	for _, ref := range a.Model.Chats.Order() {
		out = append(out, string(a.Model.Chats.At(ref).ID()))
	}
	return out
}

// archived is what an archive keeps of the SaveDraft commands a fake
// received: the last text per chat, as the Drafts event of the next start.
func archived(f *fake.Core) *abi.Event {
	last := map[string][]byte{}
	var order []string
	for _, c := range f.Sent() {
		if c.Tag != abi.CommandTagSaveDraft {
			continue
		}
		k := string(c.SaveDraft.Chat)
		if _, ok := last[k]; !ok {
			order = append(order, k)
		}
		last[k] = append([]byte(nil), c.SaveDraft.Text...)
	}
	return ev(abi.EventTagDrafts, func(e *abi.Event) {
		for _, k := range order {
			if len(last[k]) > 0 {
				e.Drafts.Drafts = append(e.Drafts.Drafts, abi.Draft{Chat: []byte(k), Text: last[k]})
			}
		}
	})
}

// run is one process's life over the settings in dir: the accounts the
// roster lists, each on a fake core, the one on screen as the roster says
// (as cmd/zapfast opens them).
type run struct {
	s     *state.State
	cores map[string]*fake.Core
}

func start(t *testing.T, dir string, boot func(id string, f *fake.Core)) (*run, *state.Loaded) {
	t.Helper()
	l, ok := state.LoadSettings(state.Paths{Config: dir, Data: dir}).Wait(5 * time.Second)
	if !ok || l.ReadErr != nil {
		t.Fatalf("the settings did not load: %v", l.ReadErr)
	}
	r := &run{s: state.New(), cores: map[string]*fake.Core{}}
	r.s.UseSettings(l)
	roster := r.s.Roster()
	for i, id := range roster.Order {
		f := fake.New(nil)
		r.cores[id] = f
		if boot != nil {
			boot(id, f)
		}
		r.s.AddAccount(id, nil, f)
		if id == roster.Active {
			r.s.Active = i
		}
	}
	r.s.Pump(0)
	return r, l
}

func (r *run) acct(id string) *state.Account {
	for _, a := range r.s.Accounts {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// The first run: two accounts with the same chats (a JID is unique only
// inside one account), each with its own open chat and draft, account 2 on
// screen; the window's panes and frame changed. It quits.
var (
	draft1 = []byte("first line\nsecond line 👩🏽‍💻 with ünïcode\n\n  trailing  ")
	draft2 = []byte("🇧🇷 só emoji 🎉\r\nand CRLF")
)

func firstRun(t *testing.T, dir string) *run {
	t.Helper()
	r, _ := start(t, dir, func(_ string, f *fake.Core) {
		f.Emit(linkStatus(abi.LinkStatusTagConnected), pinnedChats(1))
	})
	one := r.acct("1")
	f2 := fake.New(nil)
	f2.Emit(linkStatus(abi.LinkStatusTagConnected), pinnedChats(1))
	two := r.s.AddAccount(r.s.NextAccountID(), nil, f2)
	r.cores[two.ID] = f2
	r.s.Pump(0)

	one.OpenChat(one.Model.Chats.Find([]byte(ada)))
	one.SaveDraft([]byte(grace), draft1)
	r.s.SwitchAccount(two.Index)
	two.OpenChat(two.Model.Chats.Find([]byte(grace)))
	two.SaveDraft([]byte(ada), draft2)

	st := &r.s.Settings
	st.SidebarWidth, st.SearchPaneWidth, st.InfoPaneWidth = 410, 333, 290
	st.WindowW, st.WindowH, st.WindowX, st.WindowY, st.HasWindowPos, st.WindowMaximized = 1180, 760, 40, 60, true, true
	r.s.Pump(0)
	r.s.Quit()
	return r
}

// RC-001: a restart puts the window back as it was: the account on screen,
// each account's open chat (the hidden one's parked, not read), each chat's
// unsent text byte for byte from the archive and in its own account, the
// pinned order, the pane widths and the window's frame.
func TestARestartPutsTheWindowBack(t *testing.T) {
	dir := t.TempDir()
	first := firstRun(t, dir)

	r, _ := start(t, dir, func(id string, f *fake.Core) {
		f.Emit(linkStatus(abi.LinkStatusTagConnected), archived(first.cores[id]), pinnedChats(1))
	})
	t.Cleanup(r.s.Quit)
	if a := r.s.Acct(); a == nil || a.ID != "2" || len(r.s.Accounts) != 2 {
		t.Fatalf("on screen %v of %d accounts, want account 2 of 2", a, len(r.s.Accounts))
	}
	one, two := r.acct("1"), r.acct("2")
	if c, ok := two.OpenChatView(); !ok || string(c.ID()) != grace {
		t.Errorf("account 2 reopened %v, want Grace", two.Open)
	}
	if one.Open != one.Model.Chats.Find([]byte(ada)) || one.Open == 0 {
		t.Errorf("account 1 parked %v, want Ada", one.Open)
	}
	// Not looked at yet (the window has no focus): nothing is marked read,
	// and the hidden account loads nothing until it comes on screen.
	if n := countTag(r.cores["2"], abi.CommandTagMarkRead) + countTag(r.cores["1"], abi.CommandTagMarkRead); n != 0 {
		t.Errorf("the restart marked %d chats read", n)
	}
	if n := countTag(r.cores["1"], abi.CommandTagLoadChat); n != 0 {
		t.Errorf("the hidden account loaded %d chats", n)
	}
	r.s.SwitchAccount(one.Index)
	if n := countTag(r.cores["1"], abi.CommandTagLoadChat); n != 1 {
		t.Errorf("account 1 coming on screen loaded %d chats, want its parked one", n)
	}

	for _, c := range []struct {
		a          *state.Account
		chat, want []byte
	}{
		{one, []byte(grace), draft1}, {one, []byte(ada), nil},
		{two, []byte(ada), draft2}, {two, []byte(grace), nil},
	} {
		if got := c.a.Model.Draft(c.chat); !bytes.Equal(got, c.want) {
			t.Errorf("account %s, %s: draft %q, want %q", c.a.ID, c.chat, got, c.want)
		}
	}
	want := []string{hopper, grace, "4@s.whatsapp.net", ada}
	for _, a := range r.s.Accounts {
		if got := orderIDs(a); !slices.Equal(got, want) {
			t.Errorf("account %s order %v, want %v", a.ID, got, want)
		}
	}
	st := r.s.Settings
	if st.SidebarWidth != 410 || st.SearchPaneWidth != 333 || st.InfoPaneWidth != 290 {
		t.Errorf("pane widths %v %v %v", st.SidebarWidth, st.SearchPaneWidth, st.InfoPaneWidth)
	}
	if st.WindowW != 1180 || st.WindowH != 760 || st.WindowX != 40 || st.WindowY != 60 || !st.HasWindowPos || !st.WindowMaximized {
		t.Errorf("window frame %+v", st)
	}
	if !st.Transparency {
		t.Error("transparency is off after a restart that never turned it off")
	}
}

// RC-002: a damaged file degrades only what it holds. A torn settings.json
// gives the window's defaults (moved aside as .corrupt) but the roster's
// account on screen and every account's last chat survive; a torn account
// file loses only that account's last chat.
func TestADamagedSettingsFileLosesOnlyWhatItHeld(t *testing.T) {
	for _, c := range []struct {
		name, file string
	}{
		{"settings", "settings.json"},
		{"account 2", filepath.Join("accounts", "2", "settings.json")},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			firstRun(t, dir)
			path := filepath.Join(dir, c.file)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, b[:len(b)/2], 0o600); err != nil { // a write cut short
				t.Fatal(err)
			}
			r, l := start(t, dir, func(_ string, f *fake.Core) {
				f.Emit(linkStatus(abi.LinkStatusTagConnected), pinnedChats(1))
			})
			t.Cleanup(r.s.Quit)
			// The global file's notice goes in Loaded.Errs; an account
			// file's is logged as it is read.
			if c.file == "settings.json" && len(l.Errs) == 0 {
				t.Error("no notice of the damaged file")
			}
			if _, err := os.Stat(path + ".corrupt"); err != nil {
				t.Errorf("the damaged file was not kept aside: %v", err)
			}
			if a := r.s.Acct(); a == nil || a.ID != "2" {
				t.Fatalf("on screen %v, want account 2", a)
			}
			one, two := r.acct("1"), r.acct("2")
			if string(one.Model.Chats.At(one.Open).ID()) != ada {
				t.Error("account 1 lost its last chat")
			}
			wide := r.s.Settings.SidebarWidth == 410 && r.s.Settings.WindowW == 1180
			reopened := two.Open != 0 && string(two.Model.Chats.At(two.Open).ID()) == grace
			if c.file == "settings.json" {
				d := state.DefaultSettings()
				if wide || r.s.Settings.SidebarWidth != d.SidebarWidth || r.s.Settings.WindowW != 0 || !r.s.Settings.Transparency {
					t.Errorf("a torn settings file kept or garbled the window: %+v", r.s.Settings)
				}
				if !reopened {
					t.Error("account 2 lost its last chat with the global settings")
				}
			} else {
				if !wide {
					t.Error("a torn account file reset the window")
				}
				if reopened {
					t.Error("account 2's torn file still reopened its chat")
				}
			}
		})
	}
}

// RC-003: a dropped link and its recovery leave the window as it was: the
// open chat (its ref unchanged through the core's reload of the list, sent
// in two chunks), the side pane, the hidden list, a draft typed meanwhile
// (an archive replay of an older one does not overwrite it), and the
// pinned order. The reconnect itself sends nothing of the chat again; a
// command sent while offline reaches the core once, and the UI keeps no
// queue of its own to replay.
func TestAReconnectKeepsTheWindow(t *testing.T) {
	r := newRig(t, 1)
	a, f := r.s.Accounts[0], r.cores[0]
	f.Emit(pinnedChats(2))
	r.pump()
	ref := a.Model.Chats.Find([]byte(grace))
	a.OpenChat(ref)
	r.s.Panes.Pane = state.PaneSearch
	r.s.Panes.ListHidden = true
	order := orderIDs(a)

	f.Emit(linkStatus(abi.LinkStatusTagDisconnected))
	r.pump()
	if err := a.SendText(ref, []byte("sent offline")); err != nil {
		t.Fatal(err)
	}
	local := []byte("typed while offline ✍️\nsecond line")
	a.SaveDraft([]byte(ada), local)
	before := len(f.Sent())

	f.Emit(linkStatus(abi.LinkStatusTagConnecting), linkStatus(abi.LinkStatusTagConnected),
		ev(abi.EventTagDrafts, func(e *abi.Event) {
			e.Drafts.Drafts = []abi.Draft{{Chat: []byte(ada), Text: []byte("older, from the archive")}}
		}),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 3, Chats: []abi.Chat{
				{ID: []byte(ada), Name: []byte("Ada"), LastActivity: 5},
				{ID: []byte("4@s.whatsapp.net"), Name: []byte("Babbage"), LastActivity: 9},
			}}
		}))
	r.pump()
	if a.Open != ref {
		t.Fatalf("the open chat closed mid-reload: %v, want %v", a.Open, ref)
	}
	f.Emit(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 3, Last: true, Chats: []abi.Chat{
			{ID: []byte(grace), Name: []byte("Grace"), LastActivity: 4, Pinned: true, PinnedAt: 100},
			{ID: []byte(hopper), Name: []byte("Hopper"), LastActivity: 3, Pinned: true, PinnedAt: 200},
		}}
	}))
	r.pump()

	if a.Open != ref || a.Model.Chats.Find([]byte(grace)) != ref {
		t.Errorf("open %v, Grace now %v, want %v", a.Open, a.Model.Chats.Find([]byte(grace)), ref)
	}
	if r.s.Panes.Pane != state.PaneSearch || !r.s.Panes.ListHidden {
		t.Errorf("panes %+v", r.s.Panes)
	}
	if got := a.Model.Draft([]byte(ada)); !bytes.Equal(got, local) {
		t.Errorf("draft %q, want the one typed offline", got)
	}
	if got := orderIDs(a); !slices.Equal(got, order) {
		t.Errorf("order %v, want %v", got, order)
	}
	for _, c := range f.Sent()[before:] {
		switch c.Tag {
		case abi.CommandTagLoadChat, abi.CommandTagLoadPins, abi.CommandTagSaveDraft, abi.CommandTagSendText, abi.CommandTagMarkRead:
			t.Errorf("the reconnect sent %v", c.Tag)
		}
	}
	if n := r.sent(0, abi.CommandTagSendText); n != 1 {
		t.Errorf("the offline send reached the core %d times", n)
	}
	if !hasToast(r.s, "Back online") {
		t.Error("no toast says the link is back")
	}

	// A chat the reader closed stays closed through the next reload: the
	// last chat reopens once per run, not per reconnect.
	a.OpenChat(0)
	f.Emit(linkStatus(abi.LinkStatusTagDisconnected), linkStatus(abi.LinkStatusTagConnected), pinnedChats(4))
	r.pump()
	if a.Open != 0 {
		t.Errorf("a reconnect reopened %v", a.Open)
	}
}

// RC-004: one account's core failing (its Drain errors, as a closed or
// crashed core's does) is that account's alone: the others go on draining
// events for chats of the same JID without them reaching it, and it keeps
// its open chat and draft. An account removed and a new one added on a new
// core start clean: nothing the old core still had queued reaches anyone,
// and the other accounts keep their chats and drafts.
func TestACoreFailureOrReplacementStaysInItsAccount(t *testing.T) {
	r := newRig(t, 3)
	a, b, c := r.s.Accounts[0], r.s.Accounts[1], r.s.Accounts[2]
	refA := a.Model.Chats.Find([]byte(ada))
	a.OpenChat(refA)
	a.SaveDraft([]byte(ada), []byte("A's text"))
	b.SaveDraft([]byte(ada), []byte("B's text"))

	r.cores[0].Close(0)
	msg := func(id string) *abi.Event {
		return ev(abi.EventTagMessages, func(e *abi.Event) {
			e.Messages = abi.EventMessages{Chat: []byte(ada), Last: true, Messages: []abi.Message{text(id, 9)}}
		})
	}
	b.Model.Open([]byte(ada))
	r.cores[1].Emit(msg("for-b"))
	r.pump()
	r.pump() // a failed core is asked again each pump without harm
	if a.Err == "" {
		t.Error("the failed core's account does not say so")
	}
	if b.Err != "" || c.Err != "" {
		t.Errorf("the failure spread: %q %q", b.Err, c.Err)
	}
	if v := b.Model.Conversation([]byte(ada)); v == nil || v.Len() != 1 {
		t.Error("account 2 stopped draining when account 1's core failed")
	}
	if v := a.Model.Conversation([]byte(ada)); v != nil && v.Len() != 0 {
		t.Error("account 2's message reached account 1's chat of the same JID")
	}
	if a.Open != refA || !bytes.Equal(a.Model.Draft([]byte(ada)), []byte("A's text")) || r.s.Acct() != a {
		t.Error("account 1 lost its open chat, draft or place on screen")
	}

	// Account 3 goes; its core keeps events queued that nobody may see.
	old := r.cores[2]
	r.s.RemoveAccount(c.Index)
	old.Emit(ev(abi.EventTagAccountRemoved, func(*abi.Event) {}))
	r.pump()
	old.Emit(msg("stale"), ev(abi.EventTagDrafts, func(e *abi.Event) {
		e.Drafts.Drafts = []abi.Draft{{Chat: []byte(ada), Text: []byte("old core's draft")}}
	}))
	var added *fake.Core
	r.s.OpenCore = func(string) (*core.Pending, core.Conn) { added = fake.New(nil); return nil, added }
	d := r.s.AddNewAccount()
	added.Emit(linkStatus(abi.LinkStatusTagConnected), pinnedChats(1))
	r.pump()
	if d == nil || d.Serial == c.Serial {
		t.Fatal("the new account took the removed one's Serial")
	}
	d.Model.Open([]byte(ada))
	r.pump()
	if v := d.Model.Conversation([]byte(ada)); v != nil && v.Len() != 0 {
		t.Error("the removed account's core reached the new account")
	}
	if got := d.Model.Draft([]byte(ada)); len(got) != 0 {
		t.Errorf("the new account has the old core's draft %q", got)
	}
	for _, x := range []*state.Account{a, b} {
		if x.Model.Chats.Find([]byte(ada)) == 0 {
			t.Errorf("account %s lost its chats", x.ID)
		}
	}
	if got := a.Model.Draft([]byte(ada)); !bytes.Equal(got, []byte("A's text")) {
		t.Errorf("account 1's draft is now %q", got)
	}
	if slices.Contains(r.s.Accounts, c) {
		t.Error("the removed account is still listed")
	}
}

// RC-005: Try again (Reconnect on an account whose core failed to open,
// State.Reopen) replaces the core and nothing else. A failed attempt's
// half-open core, events queued, is never started nor drained; the new
// core gets its permit (the cores having started), the error clears, and
// the account comes back as its settings left it: its last chat reopens
// once the new core's list is in, unread while unfocused, with the new
// core's drafts. The other account keeps its parked chat and draft, the
// window stays on the account it showed, and the old attempt's commands
// went nowhere.
func TestTryAgainReplacesOnlyTheCore(t *testing.T) {
	r := newRig(t, 1)
	one := r.s.Accounts[0]
	ref := one.Model.Chats.Find([]byte(ada))
	one.OpenChat(ref)
	one.SaveDraft([]byte(ada), []byte("account 1's text"))

	halfOpen, fresh := fake.New(nil), fake.New(nil)
	halfOpen.Emit(linkStatus(abi.LinkStatusTagConnected), ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte("leak@s.whatsapp.net"), Name: []byte("Leak")}}}
	}), ev(abi.EventTagDrafts, func(e *abi.Event) {
		e.Drafts.Drafts = []abi.Draft{{Chat: []byte(grace), Text: []byte("from the failed attempt")}}
	}))
	queued := halfOpen.Pending()
	opens := 0
	r.s.OpenCore = func(string) (*core.Pending, core.Conn) {
		opens++
		if opens == 1 {
			return core.OpenAsync(nil, func(*core.Timing) (core.Conn, error) {
				return halfOpen, errors.New("the archive is locked")
			}), nil
		}
		return core.OpenAsync(nil, func(*core.Timing) (core.Conn, error) { return fresh, nil }), nil
	}
	p, _ := r.s.OpenCore("2")
	two := r.s.AddAccount("2", p, nil)
	two.Settings.LastChat = []byte(grace) // as the settings file left it
	p.Wait(5 * time.Second)
	r.s.StartCores()
	r.pump()
	if two.Conn != nil || two.Err == "" {
		t.Fatalf("the failed open: conn %v, err %q", two.Conn, two.Err)
	}

	r.s.SwitchAccount(two.Index)
	oneSent := len(r.cores[0].Sent())
	if err := two.Send(two.Cmd(abi.CommandTagReconnect)); err != nil || two.Err != "" || two.Pending == nil {
		t.Fatalf("Try again: %v, err %q, pending %v", err, two.Err, two.Pending != nil)
	}
	two.Pending.Wait(5 * time.Second)
	r.pump()
	if two.Conn != fresh || !fresh.Started() || !two.Started {
		t.Fatalf("reopened: conn is the new core %v, started %v", two.Conn == fresh, fresh.Started())
	}
	fresh.Emit(linkStatus(abi.LinkStatusTagConnected),
		ev(abi.EventTagDrafts, func(e *abi.Event) {
			e.Drafts.Drafts = []abi.Draft{{Chat: []byte(grace), Text: []byte("from the archive")}}
		}),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
				{ID: []byte(ada), Name: []byte("Ada"), LastActivity: 5},
				{ID: []byte(grace), Name: []byte("Grace"), LastActivity: 4, Unread: 1},
			}}
		}))
	r.pump()

	if halfOpen.Started() || halfOpen.Pending() != queued || len(halfOpen.Sent()) != 0 {
		t.Errorf("the failed attempt's core: started %v, %d of %d events left, %d commands", halfOpen.Started(), halfOpen.Pending(), queued, len(halfOpen.Sent()))
	}
	if two.Model.Chats.Find([]byte("leak@s.whatsapp.net")) != 0 {
		t.Error("the failed attempt's chat list reached the account")
	}
	if c, ok := two.OpenChatView(); !ok || string(c.ID()) != grace {
		t.Errorf("account 2 reopened %v, want its last chat, Grace", two.Open)
	}
	if got := two.Model.Draft([]byte(grace)); string(got) != "from the archive" {
		t.Errorf("account 2's draft %q, want the new core's", got)
	}
	if n := countTag(fresh, abi.CommandTagMarkRead); n != 0 {
		t.Errorf("the reopened chat was marked read %d times while unfocused", n)
	}
	if r.s.Acct() != two {
		t.Errorf("on screen %v", r.s.Acct())
	}
	if one.Open != ref || string(one.Model.Draft([]byte(ada))) != "account 1's text" || one.Err != "" {
		t.Error("account 1 lost its parked chat or draft")
	}
	for _, c := range r.cores[0].Sent()[oneSent:] {
		if c.Tag == abi.CommandTagReconnect {
			t.Error("Try again reached the other account's core")
		}
	}
}
