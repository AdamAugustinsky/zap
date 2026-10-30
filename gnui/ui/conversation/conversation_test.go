package conversation

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

// The tests run the main window offscreen with the shell, this package's
// two slots and the fake core; the other slots are the shell's placeholders.

const (
	ada   = "1@s.whatsapp.net"
	group = "123@g.us"
	base0 = int64(1_792_000_000) // 2026-10-15, a Thursday
)

type opened struct{ paths []string }

func (o *opened) Open(p string) error { o.paths = append(o.paths, p); return nil }

type rig struct {
	*widgettest.Rig
	t    testing.TB
	s    *state.State
	f    *fake.Core
	a    *state.Account
	scr  *Screen
	now  time.Time
	clip []byte
}

func (r *rig) Clipboard() []byte { return r.clip }

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

func newRig(t testing.TB) *rig {
	t.Helper()
	return newRigSized(t, 0, 0)
}

// newRigSized is newRig in a window of w×h pixels (0: the shell's size).
func newRigSized(t testing.TB, w, h int) *rig { return newRigLook(t, w, h, false) }

// newRigLook is newRigSized with, if themed, the theme slot wired. The
// package's theme goes back to the default afterwards.
func newRigLook(t testing.TB, w, h int, themed bool, composer ...screen.ComposerPane) *rig {
	t.Helper()
	s := state.New()
	s.Shell = &opened{}
	now := time.Unix(base0+10*86400, 0)
	s.Now = func() time.Time { return now }
	scr := New()
	set := &screen.Set{Conversation: scr, Search: NewSearch()}
	if len(composer) > 0 {
		set.Composer = composer[0]
	}
	if themed {
		set.Theme = ztheme.New()
		t.Cleanup(func() { ztheme.New() })
	}
	sh, err := shell.New(s, set)
	if err != nil {
		t.Fatal(err)
	}
	f := fake.New(nil)
	a := s.AddAccount("1", nil, f)
	a.Model.Now = s.Now
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
				{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: 20},
				{ID: []byte(group), Name: []byte("Engines"), Kind: abi.ChatKindGroup, LastActivity: 10},
			}}
		}))
	cfg := shell.Config(s)
	if w == 0 {
		w, h = int(cfg.Width), int(cfg.Height)
	}
	cfg.Width, cfg.Height = float32(w), float32(h)
	r := &rig{Rig: widgettest.Open(t, cfg, w, h, 1, sh), t: t, s: s, f: f, a: a, scr: scr, now: now}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Offscreen().UI.ClipboardSet = func(b []byte) { r.clip = append(r.clip[:0], b...) }
	r.Frames(3)
	return r
}

// open opens the chat and answers its LoadChat with msgs.
func (r *rig) open(chat string, msgs ...abi.Message) {
	r.t.Helper()
	ref := r.a.Model.Chats.Find([]byte(chat))
	if ref == 0 {
		r.t.Fatalf("no chat %s", chat)
	}
	r.a.OpenChat(ref)
	r.Frames(2)
	r.page(chat, false, true, msgs...)
}

// page delivers messages of chat as one Messages answer.
func (r *rig) page(chat string, older, complete bool, msgs ...abi.Message) {
	r.f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(chat), Batch: 1, Last: true, Older: older, Complete: complete, Messages: msgs}
	}))
	r.Frames(4)
}

func msg(chat, id string, ts int64, fromMe bool, body string) abi.Message {
	var m abi.Message
	m.ID, m.Chat, m.Timestamp, m.FromMe = []byte(id), []byte(chat), ts, fromMe
	m.Sender = []byte(chat)
	if fromMe {
		m.Sender = []byte("9@s.whatsapp.net")
		m.Status = abi.DeliveryRead
	}
	m.Content.Tag = abi.ContentTagText
	m.Content.Text.Text = []byte(body)
	return m
}

func dayName(ts int64) string {
	t := time.Unix(ts, 0).Local()
	return t.Format("Monday") + ", " + strconv.Itoa(t.Day()) + " " + t.Format("January") + " " + strconv.Itoa(t.Year())
}

func hhmm(ts int64) string { return time.Unix(ts, 0).Local().Format("15:04") }

// bubble is a message's accessible name: its sender and time.
func bubble(who string, ts int64) string { return who + ", " + hhmm(ts) }

func (r *rig) sent(tag abi.CommandTag) []abi.Command {
	var out []abi.Command
	for _, c := range r.f.Sent() {
		if c.Tag == tag {
			out = append(out, c)
		}
	}
	return out
}

func (r *rig) must(role ui.Role, name string) {
	r.t.Helper()
	if _, ok := r.Find(role, name); !ok {
		r.t.Fatalf("no %v %q in\n%s", role, name, widgettest.Dump(r.Tree()))
	}
}

func (r *rig) mustNot(role ui.Role, name string) {
	r.t.Helper()
	if _, ok := r.Find(role, name); ok {
		r.t.Fatalf("%v %q is on screen", role, name)
	}
}

// Opening a chat asks for its pins; its messages show as bubbles named by
// sender and time, the text inside, with the day's divider.
func TestOpeningAChatShowsItsMessages(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "m1", base0, false, "Hello there"), msg(ada, "m2", base0+60, true, "Hi Ada"))
	if len(r.sent(abi.CommandTagLoadChat)) == 0 || len(r.sent(abi.CommandTagLoadPins)) != 1 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	r.must(ui.RoleGroup, bubble("Ada Lovelace", base0))
	r.must(ui.RoleGroup, bubble("You", base0+60))
	r.must(ui.RoleLabel, "Hello there")
	r.must(ui.RoleLabel, "Hi Ada")
	r.must(ui.RoleLabel, dayName(base0))
	r.must(ui.RoleButton, "Ada Lovelace") // the header
}

// An empty chat asks the phone for its history, then says it has none.
func TestAnEmptyChatSaysSo(t *testing.T) {
	r := newRig(t)
	r.open(ada)
	if c := r.sent(abi.CommandTagFetchOlder); len(c) != 1 || string(c[0].FetchOlder.Chat) != ada {
		t.Fatalf("FetchOlder %+v", c)
	}
	r.must(ui.RoleLabel, "Loading messages from your phone…")
	r.f.Emit(ev(abi.EventTagOlderFetched, func(e *abi.Event) { e.OlderFetched = abi.EventOlderFetched{Chat: []byte(ada)} }))
	r.Frames(3)
	r.must(ui.RoleLabel, "No messages here yet")
	r.Frames(3)
	if c := r.sent(abi.CommandTagFetchOlder); len(c) != 1 {
		t.Errorf("asked the phone %d times", len(c))
	}
}

// The unread divider sits before the first message that was unread when
// the chat opened.
func TestUnreadDivider(t *testing.T) {
	r := newRig(t)
	r.f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: 30, Unread: 2}
	}))
	r.Frames(2)
	r.open(ada, msg(ada, "a", base0, false, "old"), msg(ada, "b", base0+60, true, "mine"),
		msg(ada, "c", base0+120, false, "new one"), msg(ada, "d", base0+180, false, "new two"))
	r.must(ui.RoleLabel, "2 unread messages")
	if id := r.scr.unreadAt; id == 0 || id != r.scr.t.ids[2] {
		t.Errorf("the divider is at %x, want the item of c", id)
	}
}

// History arriving above keeps the transcript where it was (Splices, not a
// reset); a message deleted for everyone leaves the list in place.
func TestPrependAndDeleteSplice(t *testing.T) {
	r := newRig(t)
	var page []abi.Message
	for i := 0; i < 60; i++ {
		page = append(page, msg(ada, "n"+strconv.Itoa(i), base0+int64(1000+i*60), i%3 == 0, "message "+strconv.Itoa(i)))
	}
	r.open(ada, page...)
	tr := &r.scr.t
	resets := tr.resets
	anchor, _ := tr.st.Anchor()
	var older []abi.Message
	for i := 0; i < 30; i++ {
		older = append(older, msg(ada, "o"+strconv.Itoa(i), base0+int64(i*10), false, "older "+strconv.Itoa(i)))
	}
	r.page(ada, true, false, older...)
	if tr.resets != resets || tr.st.Len() != 90 {
		t.Fatalf("resets %d→%d, len %d", resets, tr.resets, tr.st.Len())
	}
	if a2, _ := tr.st.Anchor(); a2 != anchor {
		t.Errorf("the anchor moved from %x to %x", anchor, a2)
	}
	splices := tr.splices
	r.f.Emit(ev(abi.EventTagMessageDeleted, func(e *abi.Event) {
		e.MessageDeleted.Chat, e.MessageDeleted.ID = []byte(ada), []byte("n58")
	}))
	r.Frames(3)
	if tr.resets != resets || tr.splices != splices+1 || tr.st.Len() != 89 {
		t.Errorf("after the delete: resets %d, splices %d→%d, len %d", tr.resets, splices, tr.splices, tr.st.Len())
	}
	r.mustNot(ui.RoleLabel, "message 58")
	r.must(ui.RoleLabel, "message 59")
}

// Near the top of an incomplete history the transcript asks the archive for
// the page before its oldest message.
func TestScrollingUpLoadsOlderHistory(t *testing.T) {
	r := newRig(t)
	r.open(ada) // the initial answer: nothing
	r.page(ada, false, false, msg(ada, "x1", base0+100, false, "one"), msg(ada, "x2", base0+200, false, "two"))
	r.Frames(2)
	var got *abi.Command
	for _, c := range r.sent(abi.CommandTagLoadChat) {
		if c.LoadChat.HasBefore {
			c := c
			got = &c
		}
	}
	if got == nil || string(got.LoadChat.Before.ID) != "x1" || got.LoadChat.Before.Timestamp != base0+100 {
		t.Fatalf("no LoadChat before x1: %v", r.f.SentTags())
	}
	n := len(r.sent(abi.CommandTagLoadChat))
	r.Frames(3)
	if len(r.sent(abi.CommandTagLoadChat)) != n {
		t.Error("asked again before the answer came")
	}
}

// Clicking a quote jumps to the quoted message and flashes it.
func TestQuoteJumps(t *testing.T) {
	r := newRig(t)
	var msgs []abi.Message
	for i := 0; i < 80; i++ {
		msgs = append(msgs, msg(ada, "q"+strconv.Itoa(i), base0+int64(i*60), false, "line "+strconv.Itoa(i)))
	}
	reply := msg(ada, "reply", base0+80*60, true, "answering")
	reply.HasQuoted = true
	reply.Quoted = abi.Quoted{ID: []byte("q3"), Sender: []byte(ada), SenderName: []byte("Ada"), HasSenderName: true, Summary: []byte("line 3")}
	msgs = append(msgs, reply)
	r.open(ada, msgs...)
	r.mustNot(ui.RoleGroup, bubble("Ada Lovelace", base0+3*60))
	r.Play(widgettest.Click(ui.RoleButton, "~Ada: line 3"), widgettest.Idle(6))
	r.must(ui.RoleGroup, bubble("Ada Lovelace", base0+3*60))
	if r.scr.flash == 0 || r.scr.flash != r.scr.t.ids[3] {
		t.Errorf("flash %x, want q3's item", r.scr.flash)
	}
}

// A reaction pill reacts with its emoji; ours takes it back.
func TestReactionPills(t *testing.T) {
	r := newRig(t)
	m := msg(ada, "r1", base0, false, "react to me")
	m.Reactions = []abi.Reaction{{Sender: []byte(ada), Emoji: []byte("👍")}, {Sender: []byte("2@s.whatsapp.net"), Emoji: []byte("👍")}, {FromMe: true, Emoji: []byte("❤️")}}
	r.open(ada, m)
	r.Play(widgettest.Click(ui.RoleButton, "👍 2"), widgettest.Idle(2))
	r.Play(widgettest.Click(ui.RoleButton, "❤️"), widgettest.Idle(2))
	got := r.sent(abi.CommandTagReact)
	if len(got) != 2 || string(got[0].React.Emoji) != "👍" || len(got[1].React.Emoji) != 0 || string(got[1].React.Message) != "r1" {
		t.Fatalf("reacts %+v", got)
	}
}

// A reaction pill's tooltip says who added it, You for ours (CV-028).
func TestReactionPillsSayWho(t *testing.T) {
	r := newRig(t)
	m := msg(ada, "r1", base0, false, "react to me")
	m.Reactions = []abi.Reaction{{Sender: []byte(ada), Emoji: []byte("👍")}, {FromMe: true, Emoji: []byte("👍")}, {FromMe: true, Emoji: []byte("❤️")}}
	r.Context().SetNativeTooltips(true)
	r.open(ada, m)
	for pill, who := range map[string]string{"👍 2": "Ada Lovelace, You", "❤️": "You"} {
		r.Play(widgettest.Move(ui.RoleButton, pill), widgettest.Idle(1))
		tips := map[string][4]float32{}
		for _, reg := range r.Context().TooltipRegions(nil) {
			tips[string(r.Context().TooltipText(reg.Key, nil))] = reg.Rect
		}
		b := r.node(ui.RoleButton, pill).Bounds
		if at, ok := tips[who]; !ok || at != b {
			t.Errorf("%s: no tooltip %q over it (%v): %v", pill, who, b, tips)
		}
	}
}

// The context menu: Star sends SetStar, Pin SetMessagePinned, Forward and
// Delete open their dialogs, Copy message ID copies, Reply sets the reply.
func TestContextMenu(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "c1", base0, false, "menu please"), msg(ada, "c2", r.now.Unix()-60, true, "mine"))
	name := bubble("Ada Lovelace", base0)
	menu := func(item string) {
		t.Helper()
		r.Play(widgettest.RClick(ui.RoleGroup, name), widgettest.Idle(2))
		r.must(ui.RoleButton, item)
		r.Play(widgettest.Click(ui.RoleButton, item), widgettest.Idle(2))
	}
	menu("Star")
	if s := r.sent(abi.CommandTagSetStar); len(s) != 1 || !s[0].SetStar.Starred || string(s[0].SetStar.Message) != "c1" {
		t.Errorf("SetStar %+v", s)
	}
	menu("Pin message")
	if s := r.sent(abi.CommandTagSetMessagePinned); len(s) != 1 || !s[0].SetMessagePinned.Pinned {
		t.Errorf("SetMessagePinned %+v", s)
	}
	menu("Copy message ID")
	if string(r.Clipboard()) != "c1" {
		t.Errorf("clipboard %q", r.Clipboard())
	}
	menu("Forward")
	if d := r.s.Dialog; d.Kind != state.DialogForward || d.IDs.Len() != 1 || string(d.IDs.At(0)) != "c1" {
		t.Errorf("dialog %+v", d)
	}
	r.s.CloseDialog()
	menu("Delete for me")
	if d := r.s.Dialog; d.Kind != state.DialogConfirmDeleteMessage || d.ForEveryone {
		t.Errorf("dialog %+v", d)
	}
	r.s.CloseDialog()
	menu("Reply")
	if p := r.s.Panes.Reply; p.Len() != 1 || string(p.At(0)) != "c1" || !r.s.Req.FocusComposer {
		t.Errorf("reply %v", p.Len())
	}
	// someone else's message cannot be edited or deleted for everyone
	r.Play(widgettest.RClick(ui.RoleGroup, name), widgettest.Idle(2))
	r.mustNot(ui.RoleButton, "Edit")
	r.mustNot(ui.RoleButton, "Delete for everyone")
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	r.mustNot(ui.RoleButton, "Copy message ID")
	// ours, a minute old, can
	r.Play(widgettest.RClick(ui.RoleGroup, bubble("You", r.now.Unix()-60)), widgettest.Idle(2))
	r.must(ui.RoleButton, "Edit")
	r.Play(widgettest.Click(ui.RoleButton, "Delete for everyone"), widgettest.Idle(2))
	if d := r.s.Dialog; d.Kind != state.DialogConfirmDeleteMessage || !d.ForEveryone || string(d.IDs.At(0)) != "c2" {
		t.Errorf("dialog %+v", d)
	}
}

// A fourth pin is refused with a toast.
func TestFourthPinIsRefused(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "p0", base0, false, "pin me"))
	r.f.Emit(ev(abi.EventTagPins, func(e *abi.Event) {
		e.Pins = abi.EventPins{Chat: []byte(ada), Batch: 1, Last: true}
		for i := 1; i <= 3; i++ {
			e.Pins.Items = append(e.Pins.Items, abi.Pinned{Chat: []byte(ada), ID: []byte("p" + strconv.Itoa(i)), PinnedAt: int64(i), ExpiresAt: r.now.Unix() + 3600, Text: []byte("pinned " + strconv.Itoa(i))})
		}
	}))
	r.Frames(3)
	r.must(ui.RoleButton, "Pinned message 1 of 3: pinned 3")
	r.Play(widgettest.RClick(ui.RoleGroup, bubble("Ada Lovelace", base0)), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "Pin message"), widgettest.Idle(2))
	if len(r.sent(abi.CommandTagSetMessagePinned)) != 0 || len(r.s.Toasts) == 0 ||
		string(r.s.Toasts[len(r.s.Toasts)-1].Text) != "This chat already has three pinned messages" {
		t.Errorf("pinned %d, toasts %d", len(r.sent(abi.CommandTagSetMessagePinned)), len(r.s.Toasts))
	}
	// the bar jumps to the shown pin and moves on to the next
	r.Play(widgettest.Click(ui.RoleButton, "Pinned message 1 of 3: pinned 3"), widgettest.Idle(2))
	r.must(ui.RoleButton, "Pinned message 2 of 3: pinned 2")
	if len(r.sent(abi.CommandTagLoadUntil)) != 1 {
		t.Errorf("no LoadUntil for the unloaded pin: %v", r.f.SentTags())
	}
}

// Selection mode: Select picks the message, the checks toggle, ⌘C copies
// the selection as a transcript, Forward… opens the dialog, Escape leaves.
func TestSelectionMode(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "s1", base0, false, "first"), msg(ada, "s2", base0+60, true, "second"))
	r.Play(widgettest.RClick(ui.RoleGroup, bubble("Ada Lovelace", base0)), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "Select"), widgettest.Idle(2))
	if !r.s.Panes.Selecting || r.s.Panes.Selection.Len() != 1 {
		t.Fatalf("selecting %v, %d", r.s.Panes.Selecting, r.s.Panes.Selection.Len())
	}
	r.must(ui.RoleLabel, "1 selected")
	r.Play(widgettest.Click(ui.RoleCheckbox, "Select message from You, "+hhmm(base0+60)+": second"), widgettest.Idle(2))
	r.must(ui.RoleLabel, "2 selected")
	r.Play(widgettest.Key('C', ui.PrimaryMod()), widgettest.Idle(2))
	want := "[" + stamp(base0) + "] Ada Lovelace: first\n[" + stamp(base0+60) + "] You: second"
	if got := string(r.Clipboard()); got != want {
		t.Errorf("copied\n%q\nwant\n%q", got, want)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Forward…"), widgettest.Idle(2))
	if d := r.s.Dialog; d.Kind != state.DialogForward || d.IDs.Len() != 2 {
		t.Errorf("dialog %+v", d)
	}
	r.s.CloseDialog()
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	if r.s.Panes.Selecting {
		t.Error("Escape left selection mode on")
	}
}

func stamp(ts int64) string {
	t := time.Unix(ts, 0).Local()
	return strconv.Itoa(t.Hour()) + ":" + t.Format("04") + ", " + strconv.Itoa(int(t.Month())) + "/" + strconv.Itoa(t.Day()) + "/" + strconv.Itoa(t.Year())
}

// Copying a text selection across messages gives WhatsApp's transcript;
// within one message, the text alone.
func TestCopyFormat(t *testing.T) {
	r := newRig(t)
	photo := msg(ada, "k2", base0+60, false, "")
	photo.Content = abi.Content{Tag: abi.ContentTagImage}
	q := msg(ada, "k3", base0+120, true, "lovely")
	q.HasQuoted = true
	q.Quoted = abi.Quoted{ID: []byte("k1"), Sender: []byte(ada), SenderName: []byte("Ada"), HasSenderName: true, Summary: []byte("look at this")}
	q.Reactions = []abi.Reaction{{Sender: []byte(ada), Emoji: []byte("❤️")}}
	r.open(ada, msg(ada, "k1", base0, false, "look at this"), photo, q)
	sc := &r.scr.sc
	tr := &r.scr.t
	r.Play(widgettest.Click(ui.RoleLabel, "lovely"), widgettest.Idle(2))
	sc.Set(ui.Pos{Item: tr.ids[0], Off: 5}, ui.Pos{Item: tr.ids[2], Off: 6})
	r.Play(widgettest.Key('C', ui.PrimaryMod()), widgettest.Idle(2))
	want := "[" + stamp(base0) + "] Ada Lovelace: at this\n[" + stamp(base0+60) + "] Ada Lovelace: [photo]\n[" +
		stamp(base0+120) + "] You: (replying to Ada: \"look at this\") lovely (❤️ Ada Lovelace)"
	if got := string(r.Clipboard()); got != want {
		t.Errorf("copied\n%q\nwant\n%q", got, want)
	}
	sc.Set(ui.Pos{Item: tr.ids[0], Off: 0}, ui.Pos{Item: tr.ids[0], Off: 4})
	r.Play(widgettest.Key('C', ui.PrimaryMod()), widgettest.Idle(2))
	if got := string(r.Clipboard()); got != "look" {
		t.Errorf("one message copied %q", got)
	}
}

// The header: the name opens the info, the calls start, Search opens the
// pane, More holds the chat's actions.
func TestHeader(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "h1", base0, false, "hi"))
	r.f.Emit(ev(abi.EventTagPresence, func(e *abi.Event) {
		e.Presence = abi.EventPresence{ID: []byte(ada), Online: true}
	}))
	r.Frames(2)
	r.must(ui.RoleLabel, "online")
	r.Play(widgettest.Click(ui.RoleButton, "Video call"), widgettest.Idle(2))
	if c := r.sent(abi.CommandTagStartCall); len(c) != 1 || !c[0].StartCall.Video || string(c[0].StartCall.Chat) != ada {
		t.Errorf("StartCall %+v", c)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Ada Lovelace"), widgettest.Idle(2))
	if r.s.Panes.Pane != state.PaneInfo {
		t.Errorf("pane %v", r.s.Panes.Pane)
	}
	r.Play(widgettest.Click(ui.RoleButton, "More"), widgettest.Idle(2), widgettest.Click(ui.RoleButton, "Archive"), widgettest.Idle(2))
	if c := r.sent(abi.CommandTagSetArchived); len(c) != 1 || !c[0].SetArchived.Archived {
		t.Errorf("SetArchived %+v", c)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Search messages"), widgettest.Idle(2))
	if r.s.Panes.Pane != state.PaneSearch {
		t.Errorf("pane %v", r.s.Panes.Pane)
	}
}

// A group has call buttons, and names who wrote each first bubble.
func TestGroupHeaderAndSenders(t *testing.T) {
	r := newRig(t)
	m := msg(group, "g1", base0, false, "from Bo")
	m.Sender, m.SenderName, m.HasSenderName = []byte("2@s.whatsapp.net"), []byte("Bo"), true
	r.open(group, m)
	r.must(ui.RoleButton, "Voice call")
	r.must(ui.RoleLabel, "~Bo")
	r.must(ui.RoleLabel, "Group")
}

// The search pane sends the query, lists the hits, and Enter jumps to the
// first.
func TestSearchPane(t *testing.T) {
	r := newRig(t)
	var msgs []abi.Message
	for i := 0; i < 50; i++ {
		msgs = append(msgs, msg(ada, "z"+strconv.Itoa(i), base0+int64(i*60), false, "chatter "+strconv.Itoa(i)))
	}
	r.open(ada, msgs...)
	r.s.Req.FindInChat = true
	r.Frames(3)
	r.must(ui.RoleLabel, "Search messages with Ada Lovelace")
	r.Play(widgettest.Type("engine"), widgettest.Idle(2))
	q := r.sent(abi.CommandTagSearchChatMessages)
	if len(q) == 0 || string(q[len(q)-1].SearchChatMessages.Query) != "engine" {
		t.Fatalf("search %v", r.f.SentTags())
	}
	hit := msg(ada, "z2", base0+120, false, "the engine room")
	r.f.Emit(ev(abi.EventTagChatHits, func(e *abi.Event) {
		e.ChatHits = abi.EventChatHits{Chat: []byte(ada), Query: []byte("engine"), Batch: 1, Last: true, Messages: []abi.Message{hit}}
	}))
	r.Frames(3)
	var row string
	tree := r.Tree()
	for _, n := range tree.Nodes {
		if name := string(tree.Text(n.Name)); n.Role == ui.RoleListItem && strings.HasSuffix(name, "the engine room") {
			row = name
		}
	}
	if row == "" {
		t.Fatalf("no hit row in\n%s", widgettest.Dump(r.Tree()))
	}
	r.Play(widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(6))
	r.must(ui.RoleLabel, "chatter 2")
	if r.scr.flash != r.scr.t.ids[2] {
		t.Errorf("flash %x", r.scr.flash)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Close search"), widgettest.Idle(2))
	if r.s.Panes.Pane != state.PaneNone {
		t.Errorf("pane %v", r.s.Panes.Pane)
	}
}

// No hits: the empty state.
func TestSearchFindsNothing(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "y1", base0, false, "hello"))
	r.s.Req.FindInChat = true
	r.Frames(3)
	r.Play(widgettest.Type("zzz"), widgettest.Idle(2))
	r.f.Emit(ev(abi.EventTagChatHits, func(e *abi.Event) {
		e.ChatHits = abi.EventChatHits{Chat: []byte(ada), Query: []byte("zzz"), Batch: 1, Last: true}
	}))
	r.Frames(3)
	r.must(ui.RoleGroup, "No messages found")
}

// PgUp scrolls a page up and shows Newest message; ⌘End (the shell's
// JumpToBottom) and the button go back to the newest.
func TestPagingKeysAndNewest(t *testing.T) {
	r := newRig(t)
	var msgs []abi.Message
	for i := 0; i < 80; i++ {
		msgs = append(msgs, msg(ada, "k"+strconv.Itoa(i), base0+int64(i*60), i%2 == 0, "page "+strconv.Itoa(i)))
	}
	r.open(ada, msgs...)
	st := &r.scr.t.st
	if !st.AtEnd() {
		t.Fatal("a chat opens at its newest message")
	}
	r.Play(widgettest.Key(ui.KeyPageUp, 0), widgettest.Idle(4))
	if st.AtEnd() {
		t.Fatal("PgUp left the view at the end")
	}
	r.must(ui.RoleButton, "Newest message")
	r.s.Req.JumpToBottom = true
	r.Frames(4)
	if !st.AtEnd() || r.s.Req.JumpToBottom {
		t.Fatalf("⌘End: at end %v", st.AtEnd())
	}
	r.Play(widgettest.Key(ui.KeyHome, 0), widgettest.Idle(4))
	r.must(ui.RoleLabel, "page 0")
	r.Play(widgettest.Click(ui.RoleButton, "Newest message"), widgettest.Idle(4))
	if !st.AtEnd() {
		t.Error("Newest message did not go to the end")
	}
}
