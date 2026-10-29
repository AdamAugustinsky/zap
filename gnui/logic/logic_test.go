package logic_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/logic"
	"zapfast/model"
	"zapfast/state"
)

const ada = "1@s.whatsapp.net"

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := &abi.Event{Tag: tag}
	set(e)
	return e
}

func text(chat, id string, ts int64) abi.Message {
	m := abi.Message{ID: []byte(id), Chat: []byte(chat), Sender: []byte(chat), Timestamp: ts}
	m.Content.Tag = abi.ContentTagText
	m.Content.Text.Text = []byte("m " + id)
	return m
}

func voice(chat, id string, ts int64, path string) abi.Message {
	m := abi.Message{ID: []byte(id), Chat: []byte(chat), Sender: []byte(chat), Timestamp: ts}
	m.Content.Tag = abi.ContentTagAudio
	m.Content.Audio.VoiceNote = true
	if path != "" {
		m.Content.Audio.Media.Path, m.Content.Audio.Media.HasPath = []byte(path), true
	}
	return m
}

type rig struct {
	t     *testing.T
	s     *state.State
	f     *fake.Core
	a     *state.Account
	l     *logic.Logic
	clock time.Time
}

// newRig: one account linked, with chats (the first, Ada, open) and the
// open chat's messages.
func newRig(t *testing.T, chats []abi.Chat, msgs ...abi.Message) *rig {
	r := &rig{t: t, s: state.New(), f: fake.New(nil), l: logic.New(), clock: time.Unix(1_800_000_000, 0)}
	r.s.Now = func() time.Time { return r.clock }
	r.a = r.s.AddAccount("1", nil, r.f)
	r.a.Model.Now = r.s.Now
	r.l.Init(r.s)
	if chats == nil {
		chats = []abi.Chat{{ID: []byte(ada), Name: []byte("Ada")}}
	}
	r.emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: chats} }))
	r.a.OpenChat(r.a.Model.Chats.Find(chats[0].ID))
	if len(msgs) > 0 {
		r.emit(ev(abi.EventTagMessages, func(e *abi.Event) {
			e.Messages = abi.EventMessages{Chat: chats[0].ID, Batch: 1, Last: true, Messages: msgs}
		}))
	}
	return r
}

func (r *rig) emit(es ...*abi.Event) {
	r.t.Helper()
	r.f.Emit(es...)
	r.s.Pump(0)
}

func (r *rig) sent(tag abi.CommandTag) []abi.Command {
	var out []abi.Command
	for _, c := range r.f.Sent() {
		if c.Tag == tag {
			out = append(out, c)
		}
	}
	return out
}

func ids(l *model.IDList) string {
	var out []string
	for i := range l.Len() {
		out = append(out, string(l.At(i)))
	}
	return fmt.Sprint(out)
}

func list(xs ...string) *model.IDList {
	var l model.IDList
	for _, x := range xs {
		l.Add([]byte(x))
	}
	return &l
}

func chats(n int) []abi.Chat {
	out := []abi.Chat{{ID: []byte("source"), Name: []byte("Source")}}
	for i := range n {
		id := fmt.Sprintf("chat-%d", i)
		out = append(out, abi.Chat{ID: []byte(id), Name: []byte(id)})
	}
	return out
}

// zf:src/app.rs, forwarding_takes_at_most_five_chats.
func TestForwardingTakesAtMostFiveChats(t *testing.T) {
	r := newRig(t, chats(7))
	logic.OpenForward(r.s, r.a, r.a.Open, list("m"))
	for i := range 7 {
		logic.ToggleForwardRecipient(r.s, r.a, fmt.Appendf(nil, "chat-%d", i))
	}
	if got := ids(&r.s.Forwarding.Recipients); got != "[chat-0 chat-1 chat-2 chat-3 chat-4]" {
		t.Fatalf("recipients %s", got)
	}
	logic.ToggleForwardRecipient(r.s, r.a, []byte("chat-0"))
	logic.ToggleForwardRecipient(r.s, r.a, []byte("chat-6"))
	if r.s.Forwarding.Recipients.Len() != logic.ForwardLimit || !r.s.Forwarding.Recipients.Has([]byte("chat-6")) {
		t.Error("dropping one did not make room")
	}
	logic.ReviewForward(r.s)
	if !logic.ConfirmForward(r.s, r.a) {
		t.Fatal("not sent")
	}
	if f := r.sent(abi.CommandTagForward); len(f) != 1 || len(f[0].Forward.ToChats) != logic.ForwardLimit || r.s.Dialog.Open() {
		t.Errorf("sent %v, dialog %v", f, r.s.Dialog.Kind)
	}
}

// zf:src/app.rs, a_message_forwarded_many_times_goes_to_one_chat.
func TestAMessageForwardedManyTimesGoesToOneChat(t *testing.T) {
	r := newRig(t, chats(3))
	logic.OpenForward(r.s, r.a, r.a.Open, list("m"))
	if len(r.sent(abi.CommandTagForwardLimit)) != 1 {
		t.Error("the limit was not asked for")
	}
	logic.ToggleForwardRecipient(r.s, r.a, []byte("chat-0"))
	logic.ToggleForwardRecipient(r.s, r.a, []byte("chat-1"))
	logic.ReviewForward(r.s)
	limit := func(msg string) *abi.Event {
		return ev(abi.EventTagForwardLimit, func(e *abi.Event) {
			e.ForwardLimit = abi.EventForwardLimit{FromChat: []byte("source"), Messages: [][]byte{[]byte(msg)}, Limit: 1}
		})
	}
	r.emit(limit("other"))
	if r.s.Forwarding.Limit != logic.ForwardLimit {
		t.Error("an answer for other messages changed the limit")
	}
	r.emit(limit("m"))
	if f := &r.s.Forwarding; f.Limit != 1 || ids(&f.Recipients) != "[chat-0]" || f.Reviewing {
		t.Errorf("limit %d, recipients %s, reviewing %v", f.Limit, ids(&f.Recipients), f.Reviewing)
	}
	logic.ToggleForwardRecipient(r.s, r.a, []byte("chat-2"))
	logic.ConfirmForward(r.s, r.a)
	if f := r.sent(abi.CommandTagForward); len(f) != 1 || string(f[0].Forward.ToChats[0]) != "chat-2" {
		t.Errorf("sent %v", f)
	}
}

// zf:src/app.rs, forwarding_rechecks_destinations_before_sending.
func TestForwardingRechecksDestinations(t *testing.T) {
	r := newRig(t, chats(2))
	logic.OpenForward(r.s, r.a, r.a.Open, list("m"))
	logic.ToggleForwardRecipient(r.s, r.a, []byte("chat-0"))
	logic.ToggleForwardRecipient(r.s, r.a, []byte("chat-1"))
	logic.ReviewForward(r.s)
	r.emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte("chat-1"), Name: []byte("chat-1"), Left: true}
	}))
	if logic.ConfirmForward(r.s, r.a) || len(r.sent(abi.CommandTagForward)) != 0 {
		t.Fatal("forwarded to a chat that was left")
	}
	if ids(&r.s.Forwarding.Recipients) != "[chat-0]" || r.s.Forwarding.Reviewing || len(r.s.Toasts) != 1 {
		t.Errorf("recipients %s, toasts %d", ids(&r.s.Forwarding.Recipients), len(r.s.Toasts))
	}
}

// zf:src/app.rs, selected_messages_forward_together_in_chat_order.
func TestSelectionInChatOrder(t *testing.T) {
	gone := text(ada, "gone", 4)
	gone.Content.Tag = abi.ContentTagRevoked
	r := newRig(t, []abi.Chat{{ID: []byte(ada), Name: []byte("Ada")}, {ID: []byte("2@s.whatsapp.net"), Name: []byte("Grace")}},
		text(ada, "first", 1), text(ada, "second", 2), text(ada, "third", 3), gone, text(ada, "fifth", 5))
	s, a := r.s, r.a
	logic.SelectMessage(s, a, []byte("third"))
	logic.ToggleSelected(s, a, []byte("first"))
	if got := ids(&s.Panes.Selection); got != "[first third]" {
		t.Fatalf("selection %s", got)
	}
	logic.OpenForward(s, a, a.Open, &s.Panes.Selection)
	logic.ToggleForwardRecipient(s, a, []byte("2@s.whatsapp.net"))
	logic.ConfirmForward(s, a)
	if f := r.sent(abi.CommandTagForward); len(f) != 1 || fmt.Sprintf("%s", f[0].Forward.Messages) != "[first third]" || s.Panes.Selecting {
		t.Errorf("forwarded %v, selecting %v", f, s.Panes.Selecting)
	}
	// Shift-click selects everything between, skipping what cannot be forwarded.
	logic.SelectMessage(s, a, []byte("second"))
	logic.SelectRange(s, a, []byte("fifth"))
	if got := ids(&s.Panes.Selection); got != "[second third fifth]" {
		t.Errorf("range %s", got)
	}
	logic.CancelSelection(s)
	// The last one out keeps selecting; StartSelection opens an empty one.
	logic.SelectMessage(s, a, []byte("second"))
	logic.ToggleSelected(s, a, []byte("second"))
	if !s.Panes.Selecting || s.Panes.Selection.Len() != 0 {
		t.Error("the empty selection closed")
	}
	logic.CancelSelection(s)
	logic.StartSelection(s, a)
	logic.ToggleSelected(s, a, []byte("fifth"))
	logic.StartSelection(s, a)
	logic.ToggleSelected(s, a, []byte("gone"))
	if got := ids(&s.Panes.Selection); !s.Panes.Selecting || got != "[fifth]" {
		t.Errorf("after StartSelection again: %s", got)
	}
}

// SL-003: a drag adds every message it passes, over what was selected
// when it began; sweeping back shrinks it to the rows still swept, and a
// new drag starts from the selection as it is (zf:src/app.rs sweep).
func TestADragSelectsWhatItSweeps(t *testing.T) {
	gone := text(ada, "gone", 4)
	gone.Content.Tag = abi.ContentTagRevoked
	r := newRig(t, []abi.Chat{{ID: []byte(ada), Name: []byte("Ada")}},
		text(ada, "first", 1), text(ada, "second", 2), text(ada, "third", 3), gone, text(ada, "fifth", 5))
	s, a := r.s, r.a
	logic.SweepMessages(s, a, []byte("second"), []byte("second"))
	if got := ids(&s.Panes.Selection); !s.Panes.Selecting || got != "[second]" {
		t.Fatalf("a drag did not start a selection: %s", got)
	}
	logic.SweepMessages(s, a, []byte("second"), []byte("fifth"))
	if got := ids(&s.Panes.Selection); got != "[second third fifth]" {
		t.Errorf("swept down %s", got)
	}
	logic.SweepMessages(s, a, []byte("second"), []byte("third"))
	if got := ids(&s.Panes.Selection); got != "[second third]" {
		t.Errorf("swept back %s", got)
	}
	logic.EndSweep(s)
	logic.SweepMessages(s, a, []byte("fifth"), []byte("fifth"))
	if got := ids(&s.Panes.Selection); got != "[second third fifth]" {
		t.Errorf("a second drag lost the first's: %s", got)
	}
	// Shift-click from the last one clicked.
	logic.ToggleSelected(s, a, []byte("first"))
	logic.SelectRange(s, a, []byte("third"))
	if got := ids(&s.Panes.Selection); got != "[first second third fifth]" {
		t.Errorf("shift-click %s", got)
	}
}

// zf:src/app.rs, selection_drops_messages_that_become_ineligible.
func TestSelectionDropsIneligibleMessages(t *testing.T) {
	r := newRig(t, nil, text(ada, "first", 1), text(ada, "second", 2), text(ada, "kept", 3))
	s, a := r.s, r.a
	logic.SelectMessage(s, a, []byte("first"))
	logic.SweepMessages(s, a, []byte("first"), []byte("second"))
	revoked := text(ada, "first", 1)
	revoked.Content.Tag = abi.ContentTagRevoked
	r.emit(ev(abi.EventTagMessageUpdated, func(e *abi.Event) { e.MessageUpdated.Message = revoked }))
	if got := ids(&s.Panes.Selection); got != "[second]" || string(s.Panes.SelectAnchor.At(0)) != "second" || s.Panes.SweepBase.Len() != 0 {
		t.Errorf("after the revoke: %s", got)
	}
	logic.DeleteForEveryone(s, a, []byte("second"))
	if s.Panes.Selection.Len() != 0 || !s.Panes.Selecting || len(r.sent(abi.CommandTagRevoke)) != 1 {
		t.Error("deleting for everyone did not leave the selection")
	}
	// Delete for me keeps the row until the deletion is accepted.
	logic.SelectMessage(s, a, []byte("kept"))
	logic.DeleteForMe(s, a, []byte("kept"))
	if ids(&s.Panes.Selection) != "[kept]" || !s.Panes.PendingDelete.Has([]byte("kept")) {
		t.Error("the row left before the core accepted")
	}
	r.emit(ev(abi.EventTagMessageDeleted, func(e *abi.Event) { e.MessageDeleted = abi.EventMessageDeleted{Chat: []byte(ada), ID: []byte("kept")} }))
	if s.Panes.Selection.Len() != 0 || s.Panes.PendingDelete.Len() != 0 || a.Conversation().Find([]byte("kept")) >= 0 {
		t.Error("the accepted deletion stayed")
	}
}

// zf:src/app.rs, selection_rejects_messages_that_cannot_be_forwarded.
func TestSelectionRejectsWhatCannotBeForwarded(t *testing.T) {
	msgs := []abi.Message{text(ada, "first", 1)}
	for i, tag := range []abi.ContentTag{abi.ContentTagRevoked, abi.ContentTagPhoneOnly, abi.ContentTagUnsupported, abi.ContentTagPoll, abi.ContentTagInteractive} {
		m := text(ada, fmt.Sprint("blocked-", i), int64(i+2))
		m.Content.Tag = tag
		msgs = append(msgs, m)
	}
	msgs = append(msgs, text(ada, "last", 10))
	r := newRig(t, nil, msgs...)
	s, a := r.s, r.a
	for _, id := range []string{"blocked-0", "blocked-1", "blocked-2", "blocked-3", "blocked-4", "missing"} {
		logic.SelectMessage(s, a, []byte(id))
		if s.Panes.Selecting {
			t.Errorf("%s started a selection", id)
		}
		logic.SelectMessage(s, a, []byte("first"))
		logic.SelectMessage(s, a, []byte(id))
		logic.ToggleSelected(s, a, []byte(id))
		if got := ids(&s.Panes.Selection); got != "[first]" || string(s.Panes.SelectAnchor.At(0)) != "first" {
			t.Errorf("%s joined: %s", id, got)
		}
		logic.CancelSelection(s)
	}
	logic.SelectMessage(s, a, []byte("first"))
	logic.SelectRange(s, a, []byte("last"))
	if got := ids(&s.Panes.Selection); got != "[first last]" {
		t.Errorf("range %s", got)
	}
}

// zf:src/app.rs, a_voice_run_goes_through_consecutive_unheard_voice_messages.
func TestAVoiceRun(t *testing.T) {
	own := voice(ada, "own", 2, "")
	own.FromMe = true
	song := voice(ada, "song", 7, "")
	song.Content.Audio.VoiceNote = false
	r := newRig(t, nil, voice(ada, "played", 1, ""), own, voice(ada, "heard", 3, ""), voice(ada, "waiting", 4, ""),
		text(ada, "text", 5), voice(ada, "after text", 6, ""), song, voice(ada, "after song", 8, ""))
	v, played := r.a.Conversation(), list("played", "heard")
	for _, c := range []struct{ after, want string }{
		{"played", "waiting"}, {"waiting", ""}, {"after text", ""}, {"song", ""},
	} {
		if got := string(logic.NextVoiceAfter(v, []byte(c.after), played)); got != c.want {
			t.Errorf("after %q: %q, want %q", c.after, got, c.want)
		}
	}
}

// A run plays on: the next clip, downloaded first when it is not
// (zf:src/app.rs, a_finished_voice_message_plays_the_next_unheard_one).
func TestAVoiceRunPlaysOn(t *testing.T) {
	r := newRig(t, nil, voice(ada, "first", 1, "/v/first.ogg"), voice(ada, "second", 2, ""), voice(ada, "third", 3, "/v/third.ogg"))
	s, a := r.s, r.a
	logic.PlayVoice(s, a, []byte("first"))
	if p := r.sent(abi.CommandTagMarkPlayed); len(p) != 1 || string(p[0].MarkPlayed.Message) != "first" || !p[0].MarkPlayed.Receipts {
		t.Fatalf("played %v", p)
	}
	logic.VoiceEnded(s, a, []byte("first"))
	if d := r.sent(abi.CommandTagDownload); len(d) != 1 || string(d[0].Download.Message) != "second" || !logic.FetchingNext(s) {
		t.Fatalf("download %v", d)
	}
	r.emit(ev(abi.EventTagMedia, func(e *abi.Event) {
		e.Media = abi.EventMedia{Chat: []byte(ada), Message: []byte("second"), Result: abi.PathResult{Ok: abi.PathResultOk{Path: []byte("/v/second.ogg")}}}
	}))
	if ids(&s.Voice.Next) != "[second /v/second.ogg]" || s.Voice.Want.Len() != 0 {
		t.Errorf("next %s", ids(&s.Voice.Next))
	}
	logic.PlayVoice(s, a, []byte("second"))
	logic.VoiceEnded(s, a, []byte("second"))
	if ids(&s.Voice.Next) != "[third /v/third.ogg]" {
		t.Errorf("a downloaded clip did not play at once: %s", ids(&s.Voice.Next))
	}
	// Leaving the chat ends the run.
	a.OpenChat(0)
	logic.New().Frame(s)
	if s.Voice.Chat != 0 || s.Voice.Next.Len() != 0 {
		t.Error("the run outlived its chat")
	}
}

func incoming(chat, id string, ts int64) *abi.Event {
	return ev(abi.EventTagIncoming, func(e *abi.Event) { e.Incoming = abi.EventIncoming{Chat: []byte(chat), Message: text(chat, id, ts)} })
}

func unread(id, name string, n uint32) *abi.Event {
	return ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(id), Name: []byte(name), Unread: n, LastActivity: 1_800_000_000}
		e.ChatUpdated.Chat.Last.Summary, e.ChatUpdated.Chat.HasLast = []byte("hello"), true
		if strings.HasSuffix(id, "@g.us") {
			e.ChatUpdated.Chat.Kind = abi.ChatKindGroup
		}
	})
}

func TestNotifications(t *testing.T) {
	const grace, group = "2@s.whatsapp.net", "3@g.us"
	r := newRig(t, []abi.Chat{{ID: []byte(ada), Name: []byte("Ada")}, {ID: []byte(grace), Name: []byte("Grace")}, {ID: []byte(group), Name: []byte("Team"), Kind: abi.ChatKindGroup}})
	s, now := r.s, r.clock.Unix()
	s.Focused = true
	// The open chat, focused, is being read: no notice, and it is read.
	r.emit(unread(ada, "Ada", 1), incoming(ada, "a1", now))
	if len(s.Notices) != 0 || len(r.sent(abi.CommandTagMarkRead)) == 0 {
		t.Fatalf("reading: %d notices", len(s.Notices))
	}
	r.emit(unread(grace, "Grace", 1), incoming(grace, "g1", now))
	if len(s.Notices) != 1 || s.Notices[0].Title != "Grace" || s.Notices[0].Body != "hello" || s.Notices[0].Sound.Sound != state.SoundReceive {
		t.Fatalf("notices %+v", s.Notices)
	}
	// Old messages, archived chats, the lock.
	r.emit(incoming(grace, "old", now-120))
	if len(s.Notices) != 1 {
		t.Error("a message older than a minute notified")
	}
	s.Lock.Locked = true
	r.emit(incoming(grace, "g2", now))
	if n := s.Notices[len(s.Notices)-1]; len(s.Notices) != 2 || !n.Hidden || n.Title != "Zap" || n.Body != "New message" {
		t.Errorf("behind the lock %+v", n)
	}
	s.Lock.Locked = false
	// A limited group notifies once, then waits; a mention gets through.
	r.a.Settings.LimitNotifications = state.LimitGroup
	r.emit(unread(group, "Team", 3), incoming(group, "t1", now), incoming(group, "t2", now))
	if len(s.Notices) != 3 || !s.Notices[2].Limited {
		t.Fatalf("limited: %d", len(s.Notices))
	}
	r.emit(ev(abi.EventTagMe, func(e *abi.Event) { e.Me.ID = []byte("15550001111@s.whatsapp.net") }))
	m := text(group, "t3", now)
	m.Mentions = []abi.MentionRef{{User: []byte("15550001111"), ID: []byte("15550001111@s.whatsapp.net")}}
	r.emit(ev(abi.EventTagIncoming, func(e *abi.Event) { e.Incoming = abi.EventIncoming{Chat: []byte(group), Message: m} }))
	if n := s.Notices[len(s.Notices)-1]; len(s.Notices) != 4 || n.Limited || n.Sound.Sound != state.SoundAlert {
		t.Errorf("a mention: %d %+v", len(s.Notices), n)
	}
	// Reading the group elsewhere clears its notices and the wait.
	r.emit(unread(group, "Team", 0))
	if len(s.NoticeClears) != 1 || string(s.NoticeClears[0].Chat) != group {
		t.Errorf("clears %+v", s.NoticeClears)
	}
	r.emit(unread(group, "Team", 1), incoming(group, "t4", now))
	if len(s.Notices) != 5 {
		t.Error("the wait outlived the chat's reading")
	}
}

func TestEligible(t *testing.T) {
	m := model.New()
	m.Event(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(ada), Unread: 1, Archived: true}, {ID: []byte("2@g.us"), Unread: 1}}}
	}))
	now := int64(1_800_000_000)
	if logic.Eligible(m.Chats.At(m.Chats.Find([]byte(ada))), now, now) || !logic.Eligible(m.Chats.At(m.Chats.Find([]byte("2@g.us"))), now, now) {
		t.Error("archived chats qualify, or unarchived ones do not")
	}
}

// Focus regained reads the open chat; presence follows focus.
func TestReadOnFocusAndPresence(t *testing.T) {
	r := newRig(t, nil)
	r.emit(unread(ada, "Ada", 2))
	reads := len(r.sent(abi.CommandTagMarkRead))
	r.l.Frame(r.s)
	if on := r.sent(abi.CommandTagSetOnline); len(on) != 1 || on[0].SetOnline.Online {
		t.Errorf("presence while unfocused: %v", on)
	}
	r.s.Focused = true
	r.l.Frame(r.s)
	if len(r.sent(abi.CommandTagMarkRead)) != reads+1 {
		t.Error("regaining the focus did not read the open chat")
	}
	if on := r.sent(abi.CommandTagSetOnline); len(on) != 2 || !on[1].SetOnline.Online {
		t.Errorf("presence %v", on)
	}
	r.l.Frame(r.s)
	if len(r.sent(abi.CommandTagSetOnline)) != 2 {
		t.Error("presence sent again unchanged")
	}
}

// Seeking a voice message takes the run to the open chat and drops a clip
// being fetched, but tells nobody it was played (zf:src/app.rs:7098).
func TestSeekingAVoiceSendsNoReceipt(t *testing.T) {
	r := newRig(t, nil, voice(ada, "first", 1, ""))
	s, a := r.s, r.a
	s.Voice.Want.Add([]byte("other"))
	logic.SeekVoice(s, a)
	if s.Voice.Chat != a.Open || s.Voice.Want.Len() != 0 {
		t.Errorf("chat %v want %d", s.Voice.Chat, s.Voice.Want.Len())
	}
	if n := len(r.sent(abi.CommandTagMarkPlayed)); n != 0 {
		t.Errorf("%d played receipts", n)
	}
}

// A notification names people the way the chat list does: the address book
// before the name a message carries, a profile name after a "~" (PR-007).
func TestNotificationNamesAddressBookFirst(t *testing.T) {
	const grace, group, bob, eve = "2@s.whatsapp.net", "3@g.us", "5@s.whatsapp.net", "6@s.whatsapp.net"
	r := newRig(t, []abi.Chat{{ID: []byte(ada), Name: []byte("Ada")}, {ID: []byte(grace), Name: []byte("Gracie (chat)")}, {ID: []byte(group), Name: []byte("Team"), Kind: abi.ChatKindGroup}})
	s, now := r.s, r.clock.Unix()
	s.Focused = true
	r.emit(ev(abi.EventTagContacts, func(e *abi.Event) {
		e.Contacts = abi.EventContacts{Batch: 1, Last: true, Contacts: []abi.Contact{
			{ID: []byte(grace), FullName: []byte("Grace Hopper"), HasFullName: true},
			{ID: []byte(bob), FullName: []byte("Bob Saved"), HasFullName: true},
			{ID: []byte(eve), PushName: []byte("Eve Public"), HasPushName: true},
		}}
	}))
	r.emit(unread(grace, "Gracie (chat)", 1), incoming(grace, "g1", now))
	if n := s.Notices[len(s.Notices)-1]; n.Title != "Grace Hopper" {
		t.Errorf("a direct chat's title is %q, not the address-book name", n.Title)
	}
	from := func(id, sender, hint string) {
		m := text(group, id, now)
		m.Sender, m.SenderName, m.HasSenderName = []byte(sender), []byte(hint), hint != ""
		r.emit(unread(group, "Team", 1), ev(abi.EventTagIncoming, func(e *abi.Event) { e.Incoming = abi.EventIncoming{Chat: []byte(group), Message: m} }))
	}
	for _, c := range []struct{ id, sender, hint, want string }{
		{"t1", bob, "Bobby", "Bob Saved: hello"},
		{"t2", eve, "", "~Eve Public: hello"},
		{"t3", "7@s.whatsapp.net", "Zed", "~Zed: hello"},
		{"t4", "8@s.whatsapp.net", "", "+8: hello"},
	} {
		r.a.Settings.LimitNotifications = state.LimitOff
		from(c.id, c.sender, c.hint)
		if n := s.Notices[len(s.Notices)-1]; n.Title != "Team" || n.Body != c.want {
			t.Errorf("%s: %q / %q, want %q", c.id, n.Title, n.Body, c.want)
		}
	}
}

// An account that is not on screen notifies with its own contacts' names,
// under its own id, and an account on screen does not know them (AC-008).
func TestAHiddenAccountNotifiesUnderItsOwnNames(t *testing.T) {
	r := newRig(t, nil)
	f2 := fake.New(nil)
	b := r.s.AddAccount("2", nil, f2)
	b.Model.Now = r.s.Now
	f2.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte("7@s.whatsapp.net"), Name: []byte("chat 7")}}}
		}),
		ev(abi.EventTagContacts, func(e *abi.Event) {
			e.Contacts = abi.EventContacts{Batch: 1, Last: true, Contacts: []abi.Contact{{ID: []byte("7@s.whatsapp.net"), FullName: []byte("Seven (second account)"), HasFullName: true}}}
		}))
	r.s.Pump(0)
	r.s.Focused = true
	if r.s.Acct() != r.a || b.Active() {
		t.Fatal("the second account is on screen")
	}
	f2.Emit(unread("7@s.whatsapp.net", "chat 7", 1), incoming("7@s.whatsapp.net", "x1", r.clock.Unix()))
	r.s.Pump(0)
	if len(r.s.Notices) != 1 {
		t.Fatalf("%d notices", len(r.s.Notices))
	}
	if n := r.s.Notices[0]; n.Account != "2" || n.Title != "Seven (second account)" {
		t.Errorf("notice for %q titled %q", n.Account, n.Title)
	}
	if got := string(r.a.Model.DisplayName([]byte("7@s.whatsapp.net"))); got == "Seven (second account)" {
		t.Error("the first account knows the second's contact")
	}
}

// A locked chat never notifies (LK-001).
func TestALockedChatNeverNotifies(t *testing.T) {
	const grace = "2@s.whatsapp.net"
	r := newRig(t, []abi.Chat{{ID: []byte(ada), Name: []byte("Ada")}, {ID: []byte(grace), Name: []byte("Grace")}})
	r.s.Focused = true
	locked := unread(grace, "Grace", 1)
	locked.ChatUpdated.Chat.Locked = true
	r.emit(locked, incoming(grace, "g1", r.clock.Unix()))
	if len(r.s.Notices) != 0 {
		t.Errorf("a locked chat notified: %+v", r.s.Notices)
	}
	r.emit(unread(grace, "Grace", 1), incoming(grace, "g2", r.clock.Unix()))
	if len(r.s.Notices) != 1 {
		t.Errorf("an unlocked chat did not: %d notices", len(r.s.Notices))
	}
}

// The split chat is read as the open chat is (a message while the window
// is looked at); the floating chat only while its own window has the focus.
func TestSplitAndFloatingChatsAreReadWhenLookedAt(t *testing.T) {
	const grace, eve = "2@s.whatsapp.net", "6@s.whatsapp.net"
	r := newRig(t, []abi.Chat{{ID: []byte(ada), Name: []byte("Ada")}, {ID: []byte(grace), Name: []byte("Grace")}, {ID: []byte(eve), Name: []byte("Eve")}})
	s, a := r.s, r.a
	if !a.OpenSplit(a.Model.Chats.Find([]byte(grace))) {
		t.Fatal("no split")
	}
	s.Focused = true
	r.l.Frame(s)
	reads := len(r.sent(abi.CommandTagMarkRead))
	r.emit(unread(grace, "Grace", 1))
	if len(r.sent(abi.CommandTagMarkRead)) != reads+1 {
		t.Fatal("a message in the split chat, looked at, was not read")
	}

	if !s.FloatChat(a, a.Model.Chats.Find([]byte(eve))) {
		t.Fatal("no floating chat")
	}
	s.Focused = false // the person is in another app
	r.l.Frame(s)
	reads = len(r.sent(abi.CommandTagMarkRead))
	r.emit(unread(eve, "Eve", 1))
	if len(r.sent(abi.CommandTagMarkRead)) != reads {
		t.Fatal("the floating chat was read while its window did not have the focus")
	}
	s.Pip.Focused = true
	r.l.Frame(s)
	if len(r.sent(abi.CommandTagMarkRead)) != reads+1 {
		t.Fatal("the floating chat taking the focus did not read it")
	}
	s.SetPipMini(true) // shrunk to its icon: nothing is read
	r.l.Frame(s)
	reads = len(r.sent(abi.CommandTagMarkRead))
	r.emit(unread(eve, "Eve", 2))
	if len(r.sent(abi.CommandTagMarkRead)) != reads {
		t.Error("the floating chat was read while shrunk to its icon")
	}
}
