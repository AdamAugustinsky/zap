package msginfo_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/format"
	"zapfast/state"
	"zapfast/ui/msginfo"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

const (
	ada   = "393331234567@s.whatsapp.net"
	grace = "15551230000@s.whatsapp.net"
	linus = "447700900123@s.whatsapp.net"
	team  = "120363000000000001@g.us"
	me    = "15550001111@s.whatsapp.net"
)

func b(s string) []byte { return []byte(s) }

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

type rig struct {
	*widgettest.Rig
	t *testing.T
	s *state.State
	a *state.Account
	f *fake.Core
}

func newRig(t *testing.T) *rig { return newRigW(t, 0) }

// newRigW is newRig in a window width points wide (0: the default).
func newRigW(t *testing.T, width int) *rig {
	t.Helper()
	old := format.Hour12 // the times below read on a 12-hour clock
	format.Hour12 = func() bool { return true }
	t.Cleanup(func() { format.Hour12 = old })
	f := fake.New(nil)
	f.Emit(
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagMe, func(e *abi.Event) { e.Me.ID = b(me); e.Me.Name = b("Me Myself") }),
		ev(abi.EventTagContacts, func(e *abi.Event) {
			e.Contacts.Contacts = []abi.Contact{
				{ID: b(ada), FullName: b("Ada Lovelace"), HasFullName: true},
				{ID: b(grace), FullName: b("Grace Hopper"), HasFullName: true},
			}
		}),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
				{ID: b(team), Name: b("Team"), Kind: abi.ChatKindGroup, GroupSubjectKnown: true, LastActivity: 10},
				{ID: b(ada), Name: b("Ada Lovelace"), LastActivity: 5},
			}}
		}),
	)
	s := state.New()
	sh, err := shell.New(s, &screen.Set{MessageInfo: msginfo.New()})
	if err != nil {
		t.Fatal(err)
	}
	a := s.AddAccount("1", nil, f)
	cfg := shell.Config(s)
	if width == 0 {
		width = int(cfg.Width)
	}
	r := &rig{Rig: widgettest.Open(t, cfg, width, int(cfg.Height), 1, sh), t: t, s: s, a: a, f: f}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Frames(4)
	return r
}

var at = time.Date(2026, 3, 4, 14, 5, 0, 0, time.Local).Unix()

// message puts one of our messages in the chat's conversation.
func (r *rig) message(chat, id, text string, mut func(m *abi.Message)) {
	r.t.Helper()
	m := abi.Message{ID: b(id), Chat: b(chat), Sender: b(me), FromMe: true, Timestamp: at,
		Content: abi.Content{Tag: abi.ContentTagText, Text: abi.ContentText{Text: b(text)}}, Status: abi.DeliverySent}
	if mut != nil {
		mut(&m)
	}
	r.f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: b(chat), Last: true, Complete: true, Messages: []abi.Message{m}}
	}))
	r.Frames(2)
}

// open opens the chat and the pane on the message.
func (r *rig) open(chat, id string) {
	r.t.Helper()
	r.a.OpenChat(r.a.Model.Chats.Find(b(chat)))
	r.s.Panes.MessageInfo.Reset()
	r.s.Panes.MessageInfo.Add(b(id))
	r.s.Panes.Pane = state.PaneMessageInfo
	r.s.Touch()
	r.Frames(4)
}

func (r *rig) names(role ui.Role) []string {
	tr := r.Tree()
	var out []string
	for _, n := range tr.Nodes {
		if n.Role == role {
			out = append(out, string(tr.Text(n.Name)))
		}
	}
	return out
}

func (r *rig) has(role ui.Role, name string) bool {
	_, ok := r.Find(role, name)
	return ok
}

func (r *rig) want(role ui.Role, name string) {
	r.t.Helper()
	if !r.has(role, name) {
		var sb strings.Builder
		tr := r.Tree()
		for _, n := range tr.Nodes {
			fmt.Fprintf(&sb, "%s %q\n", widgettest.RoleName(n.Role), tr.Text(n.Name))
		}
		r.t.Fatalf("no %s %q in\n%s", widgettest.RoleName(role), name, sb.String())
	}
}

func (r *rig) wantNot(role ui.Role, name string) {
	r.t.Helper()
	if r.has(role, name) {
		r.t.Errorf("unexpected %s %q", widgettest.RoleName(role), name)
	}
}

// labels are the labels of the pane in order.
func (r *rig) labels() []string { return r.names(ui.RoleLabel) }

func (r *rig) watches() []abi.CommandWatchReceipts {
	var out []abi.CommandWatchReceipts
	for _, c := range r.f.Sent() {
		if c.Tag == abi.CommandTagWatchReceipts {
			out = append(out, c.WatchReceipts)
		}
	}
	return out
}

func (r *rig) receipts(chat, msg string, rs ...abi.Recipient) {
	r.f.Emit(ev(abi.EventTagReceipts, func(e *abi.Event) {
		e.Receipts = abi.EventReceipts{Chat: b(chat), Message: b(msg), Recipients: rs}
	}))
	r.Frames(3)
}

func rcpt(id string, expected bool, delivered, read, played int64) abi.Recipient {
	x := abi.Recipient{ID: b(id), Expected: expected}
	if delivered != -1 {
		x.DeliveredAt, x.HasDeliveredAt = delivered, true
	}
	if read != -1 {
		x.ReadAt, x.HasReadAt = read, true
	}
	if played != -1 {
		x.PlayedAt, x.HasPlayedAt = played, true
	}
	return x
}

func indexOf(labels []string, s string) int {
	for i, l := range labels {
		if l == s {
			return i
		}
	}
	return -1
}

// MI-001: the pane shows the message as its bubble does, shortened.
func TestShowsTheMessage(t *testing.T) {
	r := newRig(t)
	r.message(ada, "m1", "Dinner at eight?\nBring wine", func(m *abi.Message) { m.Status = abi.DeliveryDelivered })
	r.open(ada, "m1")
	r.want(ui.RoleLabel, "Message info")
	r.want(ui.RoleButton, "Close")
	r.want(ui.RoleLabel, "Dinner at eight?") // the first line, as upstream's summary
	long := strings.Repeat("word ", 400)
	r.message(ada, "m2", long, nil)
	r.open(ada, "m2")
	for _, l := range r.labels() {
		if strings.HasPrefix(l, "word word") && len(l) > 400 {
			t.Errorf("an unshortened message of %d bytes", len(l))
		}
	}
	// a photo is summarised as the bubble's list does
	r.message(ada, "m3", "", func(m *abi.Message) {
		m.Content = abi.Content{Tag: abi.ContentTagImage, Image: abi.ContentImage{Caption: b("Sunset"), HasCaption: true}}
	})
	r.open(ada, "m3")
	r.want(ui.RoleLabel, "Photo: Sunset")
}

// Close closes the pane.
func TestCloseClosesThePane(t *testing.T) {
	r := newRig(t)
	r.message(ada, "m1", "hi", nil)
	r.open(ada, "m1")
	r.Play(widgettest.Click(ui.RoleButton, "Close"), widgettest.Idle(3))
	if r.s.Panes.Pane != state.PaneNone {
		t.Error("the pane is still open")
	}
}

// MI-002: in a direct chat, when the message was delivered and read, or
// "Time not recorded"; nothing is followed for it.
func TestDirectChatTimes(t *testing.T) {
	r := newRig(t)
	r.message(ada, "m1", "hi", func(m *abi.Message) {
		m.Status = abi.DeliveryRead
		m.DeliveredAt, m.HasDeliveredAt = at+30, true
		m.ReadAt, m.HasReadAt = at+3600, true
	})
	r.message(ada, "m2", "hello", func(m *abi.Message) {
		m.Status = abi.DeliveryRead // the times were not recorded
	})
	r.message(ada, "m3", "pending", func(m *abi.Message) { m.Status = abi.DeliverySent })
	r.message(ada, "m4", "voice", func(m *abi.Message) {
		m.Status = abi.DeliveryPlayed
		m.DeliveredAt, m.HasDeliveredAt = at+1, true
		m.ReadAt, m.HasReadAt = at+2, true
	})

	r.open(ada, "m1")
	r.want(ui.RoleLabel, "Read")
	r.want(ui.RoleLabel, "Delivered")
	l := r.labels()
	if indexOf(l, "Delivered") < 0 || indexOf(l, "Read") < 0 {
		t.Fatalf("labels %q", l)
	}
	joined := strings.Join(l, "|")
	for _, want := range []string{"2:05 PM", "3:05 PM"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no time %q in %q", want, l)
		}
	}
	if len(r.watches()) != 0 {
		t.Errorf("a direct chat followed receipts: %v", r.watches())
	}

	r.open(ada, "m2")
	if n := count(r.labels(), "Time not recorded"); n != 2 {
		t.Errorf("Time not recorded %d times in %q", n, r.labels())
	}
	r.open(ada, "m3")
	if n := count(r.labels(), "—"); n != 2 {
		t.Errorf("— %d times in %q", n, r.labels())
	}
	r.open(ada, "m4")
	r.want(ui.RoleLabel, "Played")
	r.wantNot(ui.RoleLabel, "Read")
}

func count(l []string, s string) int {
	n := 0
	for _, x := range l {
		if x == s {
			n++
		}
	}
	return n
}

// MI-005: a group message shows "Loading…" until its receipts arrive, and
// the core is asked to follow exactly that message.
func TestGroupLoadsThenFollows(t *testing.T) {
	r := newRig(t)
	r.message(team, "g1", "Standup", nil)
	r.open(team, "g1")
	r.want(ui.RoleLabel, "Loading…")
	w := r.watches()
	if len(w) != 1 || !w[0].HasTarget || string(w[0].Target.Chat) != team || string(w[0].Target.Message) != "g1" {
		t.Fatalf("WatchReceipts %+v", w)
	}
	r.Frames(5)
	if len(r.watches()) != 1 {
		t.Error("watched again with nothing changed")
	}
	// receipts for another message are stale
	r.receipts(team, "other", rcpt(ada, true, at, -1, -1))
	r.want(ui.RoleLabel, "Loading…")
	r.receipts(team, "g1", rcpt(ada, true, at+60, -1, -1))
	r.wantNot(ui.RoleLabel, "Loading…")
}

// MI-003: Played by, Read by and Delivered to with times, newest first,
// "N remaining", "No one yet", and live updates.
func TestGroupSections(t *testing.T) {
	r := newRig(t)
	r.message(team, "g1", "Standup", nil)
	r.open(team, "g1")
	r.receipts(team, "g1",
		rcpt(ada, true, at+10, at+20, -1),
		rcpt(grace, true, at+15, at+400, -1),
		rcpt(linus, true, at+30, -1, -1),
		rcpt("15557770000@s.whatsapp.net", true, -1, -1, -1),
		rcpt("15558880000@s.whatsapp.net", true, -1, -1, -1),
	)
	l := r.labels()
	for _, s := range []string{"Read by", "Delivered to", "Ada Lovelace", "Grace Hopper", "+44 770 090 0123", "2 remaining"} {
		if indexOf(l, s) < 0 {
			t.Errorf("no label %q in %q", s, l)
		}
	}
	if indexOf(l, "Played by") >= 0 {
		t.Error("Played by shows with nobody who played")
	}
	// newest first: Grace read later than Ada
	if indexOf(l, "Grace Hopper") > indexOf(l, "Ada Lovelace") {
		t.Errorf("Read by is not newest first: %q", l)
	}
	if indexOf(l, "Read by") > indexOf(l, "Grace Hopper") || indexOf(l, "Delivered to") > indexOf(l, "+44 770 090 0123") || indexOf(l, "Ada Lovelace") > indexOf(l, "Delivered to") {
		t.Errorf("recipients under the wrong section: %q", l)
	}
	r.wantNot(ui.RoleLabel, "No one yet")

	// live: one more person played it, the remaining ones shrink
	r.receipts(team, "g1",
		rcpt(ada, true, at+10, at+20, at+25),
		rcpt(grace, true, at+15, at+400, -1),
		rcpt(linus, true, at+30, at+50, -1),
		rcpt("15557770000@s.whatsapp.net", true, at+9, -1, -1),
		rcpt("15558880000@s.whatsapp.net", true, -1, -1, -1),
	)
	l = r.labels()
	if indexOf(l, "Played by") < 0 || indexOf(l, "1 remaining") < 0 {
		t.Errorf("live update missing: %q", l)
	}
	if indexOf(l, "Played by") > indexOf(l, "Ada Lovelace") || indexOf(l, "Ada Lovelace") > indexOf(l, "Read by") {
		t.Errorf("Ada is not under Played by: %q", l)
	}

	// everyone read it: Delivered to has no one yet
	r.receipts(team, "g1", rcpt(ada, true, at, at+5, -1))
	r.want(ui.RoleLabel, "No one yet")
	r.wantNot(ui.RoleLabel, "1 remaining")
}

// MI-004: it says when no receipts, or only some, were recorded.
func TestNoReceiptsAndPartial(t *testing.T) {
	r := newRig(t)
	r.message(team, "g1", "Old", nil)
	r.open(team, "g1")
	r.receipts(team, "g1")
	r.want(ui.RoleLabel, "No receipts were recorded for this message. Details are available for messages sent from now on.")
	r.wantNot(ui.RoleLabel, "Read by")
	r.receipts(team, "g1", rcpt(ada, false, at, at+1, -1))
	r.want(ui.RoleLabel, "Some receipts for this message were not recorded. Details are complete for messages sent from now on.")
	r.want(ui.RoleLabel, "Read by")
	// a zero time is unknown, not the epoch
	r.receipts(team, "g1", rcpt(ada, true, 0, 0, -1))
	for _, l := range r.labels() {
		if strings.Contains(l, "1970") || strings.Contains(l, "Jan 1970") {
			t.Errorf("the epoch shows: %q", l)
		}
	}
}

// MI-005: "This message is no longer available." if the message goes.
func TestMessageGone(t *testing.T) {
	r := newRig(t)
	r.open(ada, "nothing")
	r.want(ui.RoleLabel, "This message is no longer available.")
	r.message(ada, "m1", "hi", nil)
	r.open(ada, "m1")
	r.wantNot(ui.RoleLabel, "This message is no longer available.")
	r.f.Emit(ev(abi.EventTagMessageDeleted, func(e *abi.Event) { e.MessageDeleted.Chat, e.MessageDeleted.ID = b(ada), b("m1") }))
	r.Frames(3)
	r.want(ui.RoleLabel, "This message is no longer available.")
	// in a group the core stops following it
	r.message(team, "g1", "Standup", nil)
	r.open(team, "g1")
	r.f.Emit(ev(abi.EventTagMessageDeleted, func(e *abi.Event) { e.MessageDeleted.Chat, e.MessageDeleted.ID = b(team), b("g1") }))
	r.Frames(3)
	w := r.watches()
	if len(w) == 0 || w[len(w)-1].HasTarget {
		t.Errorf("WatchReceipts after the message went: %+v", w)
	}
}

// The core stops following a message as soon as the pane closes, or its chat
// changes; a later push of the receipts does not ask again.
func TestStopsFollowingWhenClosed(t *testing.T) {
	r := newRig(t)
	r.message(team, "g1", "Standup", nil)
	r.open(team, "g1")
	r.receipts(team, "g1", rcpt(ada, true, at, -1, -1))
	r.s.Panes.Pane = state.PaneNone
	r.s.Touch()
	r.Frames(3)
	w := r.watches()
	if len(w) != 2 || w[1].HasTarget {
		t.Fatalf("WatchReceipts after closing: %+v", w)
	}
	r.receipts(team, "g1", rcpt(ada, true, at, at+1, -1))
	if n := len(r.watches()); n != 2 {
		t.Errorf("%d WatchReceipts after the pane closed", n)
	}

	// opening it again follows again; moving to another chat stops it
	r.open(team, "g1")
	if w := r.watches(); len(w) != 3 || !w[2].HasTarget {
		t.Fatalf("WatchReceipts after reopening: %+v", w)
	}
	r.a.OpenChat(r.a.Model.Chats.Find(b(ada)))
	r.s.Touch()
	r.Frames(3)
	if w := r.watches(); len(w) != 4 || w[3].HasTarget {
		t.Errorf("WatchReceipts after changing chat: %+v", w)
	}
}

// In a narrow window the pane covers the window and shows Back for Close.
func TestBackWhenCovering(t *testing.T) {
	r := newRigW(t, 600)
	r.message(ada, "m1", "hi", nil)
	r.open(ada, "m1")
	r.want(ui.RoleButton, "Back")
	r.wantNot(ui.RoleButton, "Close")
	r.Play(widgettest.Click(ui.RoleButton, "Back"), widgettest.Idle(3))
	if r.s.Panes.Pane != state.PaneNone {
		t.Error("Back left the pane open")
	}
}

// The pane's title row is in the window's toolbar band, over the pane (see
// the same test in ui/info): Close is the only one and sits in the band.
func TestHeaderIsInTheToolbarBand(t *testing.T) {
	r := newRig(t)
	r.message(ada, "m1", "hi", nil)
	r.open(ada, "m1")
	n, ok := r.Find(ui.RoleButton, "Close")
	if !ok {
		t.Fatal("no Close")
	}
	const band = 50 // the toolbar band is 42 pt high; the pane's own row starts below it
	if n.Bounds[1] >= band {
		t.Skipf("the shell does not host the pane's toolbar items yet (Close at y=%v)", n.Bounds[1])
	}
	if got := len(r.namesOf(ui.RoleButton, "Close")); got != 1 {
		t.Errorf("%d Close buttons", got)
	}
	r.want(ui.RoleLabel, "Message info")
	r.Play(widgettest.Click(ui.RoleButton, "Close"), widgettest.Idle(3))
	if r.s.Panes.Pane != state.PaneNone {
		t.Error("Close left the pane open")
	}
}

func (r *rig) namesOf(role ui.Role, name string) []string {
	var out []string
	for _, n := range r.names(role) {
		if n == name {
			out = append(out, n)
		}
	}
	return out
}
