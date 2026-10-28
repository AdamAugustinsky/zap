package state_test

import (
	"fmt"
	"testing"
	"time"

	"gonativeui/app"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/core/fake"
	"zapfast/state"
)

const grace = "2@s.whatsapp.net"

func TestOpeningAChatJumpsToItsUnreadMention(t *testing.T) {
	const group = "123@g.us"
	for _, explicit := range []bool{false, true} {
		r := newRig(t, 1)
		a := r.s.Accounts[0]
		r.chats(0, abi.Chat{ID: []byte(group), Kind: abi.ChatKindGroup, Unread: 3,
			HasUnreadMention: true, UnreadMention: []byte("mention")})
		ref := a.Model.Chats.Find([]byte(group))
		want := "mention"
		if explicit {
			want = "target"
			a.OpenMessage([]byte(group), []byte(want))
		} else {
			a.OpenChat(ref)
		}
		if j := &r.s.Panes.Jump; j.Chat != ref || j.Msg.Len() != 1 || string(j.Msg.At(0)) != want {
			t.Fatalf("explicit=%v: jump %v, want %s", explicit, j, want)
		}
		ch, _ := a.Model.Chats.Get(ref)
		if _, has := ch.UnreadMention(); ch.Unread() != 0 || has {
			t.Fatal("opening did not mark the chat read")
		}
		// A missing target is paged toward when the first page arrives.
		m := text("newer", 100)
		m.Chat = []byte(group)
		r.cores[0].Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
			e.Messages = abi.EventMessages{Chat: []byte(group), Batch: 1, Last: true, Messages: []abi.Message{m}}
		}))
		r.pump()
		if r.sent(0, abi.CommandTagLoadUntil) != 1 {
			t.Fatal("missing mention/explicit target was not paged toward")
		}
		for _, cmd := range r.cores[0].Sent() {
			if cmd.Tag == abi.CommandTagLoadUntil && string(cmd.LoadUntil.ID) != want {
				t.Fatalf("paged toward %s, want %s", cmd.LoadUntil.ID, want)
			}
		}
		seq := r.s.Panes.Jump.Seq
		a.OpenChat(ref)
		if r.s.Panes.Jump.Seq != seq {
			t.Fatal("reopening the current chat jumped again")
		}
		a.OpenChat(0)
		a.OpenChat(ref)
		if r.s.Panes.Jump.Msg.Len() != 0 {
			t.Fatal("read mention was restored on a later open")
		}
	}
}

func (r *rig) chats(i int, cs ...abi.Chat) {
	r.cores[i].Emit(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 2, Last: true, Chats: cs}
	}))
	r.pump()
}

// OpenMessage opens the chat, asks the conversation to jump to and flash the
// message, and pages toward it when it is not loaded; a locked chat stays
// shut outside its folder.
func TestOpenMessageJumpsAndPagesTowardTheMessage(t *testing.T) {
	r := newRig(t, 1)
	a := r.s.Accounts[0]
	a.OpenChat(a.Model.Chats.Find([]byte(ada)))
	r.cores[0].Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(ada), Batch: 1, Last: true, Messages: []abi.Message{text("b", 20), text("c", 30)}}
	}))
	r.pump()
	if !r.s.OpenMessage([]byte(ada), []byte("c")) {
		t.Fatal("not opened")
	}
	j := r.s.Panes.Jump
	if j.Chat != a.Open || string(j.Msg.At(0)) != "c" || j.Seq != 1 || r.sent(0, abi.CommandTagLoadUntil) != 0 {
		t.Fatalf("jump %v %q seq %d, LoadUntil %d", j.Chat, j.Msg.At(0), j.Seq, r.sent(0, abi.CommandTagLoadUntil))
	}
	r.s.OpenMessage([]byte(ada), []byte("a"))
	if r.s.Panes.Jump.Seq != 2 || r.sent(0, abi.CommandTagLoadUntil) != 1 || string(r.s.Panes.Anchor.At(0)) != "a" {
		t.Errorf("an older message: seq %d, LoadUntil %d", r.s.Panes.Jump.Seq, r.sent(0, abi.CommandTagLoadUntil))
	}
	// Another chat drops the jump and the side panes.
	r.chats(0, abi.Chat{ID: []byte(grace), Name: []byte("Grace"), LastActivity: 4})
	r.s.Panes.SetInfo([]byte(ada))
	a.OpenChat(a.Model.Chats.Find([]byte(grace)))
	if r.s.Panes.Jump.Msg.Len() != 0 || r.s.Panes.Pane != state.PaneNone {
		t.Error("the jump or the info pane outlived the chat")
	}
	r.chats(0, abi.Chat{ID: []byte("3@s.whatsapp.net"), Name: []byte("Secret"), Locked: true, LastActivity: 3})
	if r.s.OpenMessage([]byte("3@s.whatsapp.net"), []byte("x")) {
		t.Error("a locked chat opened outside its folder")
	}
}

// A notification's account comes on screen with the message.
func TestOpenMessageInSwitchesAccount(t *testing.T) {
	r := newRig(t, 2)
	if !r.s.OpenMessageIn("2", []byte(ada), []byte("m")) || r.s.Active != 1 || r.s.Acct().Open == 0 {
		t.Fatalf("active %d, open %v", r.s.Active, r.s.Acct().Open)
	}
	if !r.s.OpenMessageIn("1", nil, nil) || r.s.Active != 0 {
		t.Error("a call's notification did not switch")
	}
	if r.s.OpenMessageIn("9", []byte(ada), []byte("m")) {
		t.Error("no such account")
	}
}

// React counts the emoji for the quick reactions, most used first, and
// remembers it among the recent ones; UseEmoji keeps the recent list unique.
func TestReactionsAndRecentEmoji(t *testing.T) {
	r := newRig(t, 1)
	a := r.s.Accounts[0]
	r.s.OpenReaction(a.Model.Chats.Find([]byte(ada)), []byte("m"), state.Rect{W: 10, H: 10})
	a.React([]byte(ada), []byte("m"), []byte("👍"))
	a.React([]byte(ada), []byte("m"), []byte("❤️"))
	a.React([]byte(ada), []byte("m"), []byte("❤️"))
	re := r.s.Settings.ReactionEmoji
	if len(re) != 2 || string(re[0].Emoji) != "❤️" || re[0].Count != 2 || re[1].Count != 1 {
		t.Fatalf("reaction emoji %v", re)
	}
	if r.s.Panes.ReactTarget.Open() || r.sent(0, abi.CommandTagReact) != 3 {
		t.Error("the picker stayed open, or a reaction was not sent")
	}
	r.s.InsertEmoji([]byte("😀"))
	if got := string(r.s.TakeInsert()); got != "😀" || len(r.s.TakeInsert()) != 0 || !r.s.Req.FocusComposer {
		t.Errorf("insert %q", got)
	}
	rec := r.s.Settings.RecentEmoji
	if len(rec) != 3 || string(rec[0]) != "😀" || string(rec[1]) != "❤️" {
		t.Errorf("recent %q", rec)
	}
	for range 40 {
		r.s.UseEmoji([]byte("😀"))
	}
	if len(r.s.Settings.RecentEmoji) != 3 {
		t.Error("an emoji used again was added again")
	}
}

// StartChat asks the core for a chat it does not have and opens it once it
// is in, with a link's text waiting for the composer.
func TestStartChatOpensOnceTheChatIsIn(t *testing.T) {
	r := newRig(t, 1)
	a := r.s.Accounts[0]
	a.StartChat([]byte(grace), []byte("+2"), []byte("hello"))
	if r.sent(0, abi.CommandTagEnsureChat) != 1 || a.Open != 0 {
		t.Fatal("not asked for, or opened early")
	}
	if p := r.s.Req.Prefill; string(p.Chat) != grace || string(p.Text) != "hello" {
		t.Errorf("prefill %q %q", p.Chat, p.Text)
	}
	r.chats(0, abi.Chat{ID: []byte(grace), Name: []byte("Grace"), LastActivity: 9})
	if c, ok := a.OpenChatView(); !ok || string(c.ID()) != grace {
		t.Error("not opened once in")
	}
}

// Locking ends what the state owns, runs every screen's hook once however
// often it registered, and InUse asks the hooks.
func TestLockingRunsTheHooks(t *testing.T) {
	r := newRig(t, 1)
	owner, runs := new(int), 0
	for range 2 { // a window opened twice registers twice
		r.s.OnLock(owner, func(*state.State) { runs++ })
	}
	recording := false
	r.s.OnInUse(owner, func(*state.State) bool { return recording })
	r.s.Win.Open[state.WinViewer] = true
	r.s.Notices = append(r.s.Notices, state.Notice{Title: "Ada"})
	r.s.Locked()
	if runs != 1 || !r.s.Win.Shut[state.WinViewer] || len(r.s.Notices) != 0 || len(r.s.NoticeClears) != 1 || !r.s.NoticeClears[0].All {
		t.Errorf("runs %d, viewer shut %v, notices %d, clears %+v", runs, r.s.Win.Shut[state.WinViewer], len(r.s.Notices), r.s.NoticeClears)
	}
	if r.s.InUse() {
		t.Error("in use with no recording")
	}
	recording = true
	if !r.s.InUse() {
		t.Error("a recording is not use")
	}
}

// A new number stays on the login screen until it linked and its chats are
// in.
func TestAddingEndsOnceLinkedWithChats(t *testing.T) {
	r := newRig(t, 1)
	var added *fake.Core
	r.s.OpenCore = func(string) (*core.Pending, core.Conn) {
		added = fake.New(nil)
		return nil, added
	}
	r.s.AddNewAccount()
	added.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnecting }))
	r.pump()
	added.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }))
	r.pump()
	if !r.s.AddingAccount {
		t.Fatal("adding ended before the chats came")
	}
	added.Emit(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(ada), Name: []byte("Ada")}}}
	}))
	r.pump()
	if r.s.AddingAccount {
		t.Error("still adding with the chats in")
	}
}

type countListener struct{ n int }

func (l *countListener) Event(*state.Account, *abi.Event) { l.n++ }

// A listener a screen's Init adds again (its window opened again) hears
// each event once.
func TestListenersAreAddedOnce(t *testing.T) {
	r := newRig(t, 1)
	l := &countListener{}
	r.s.Listen(l, abi.EventTagInfo)
	r.s.Listen(l, abi.EventTagInfo)
	r.cores[0].Emit(ev(abi.EventTagInfo, func(e *abi.Event) { e.Info.Text = []byte("hi") }))
	r.pump()
	if l.n != 1 {
		t.Errorf("heard %d times", l.n)
	}
}

func TestFlashStrength(t *testing.T) {
	for _, c := range []struct {
		ms   int
		want float32
	}{{-1, 0}, {75, 0.5}, {500, 1}, {int(state.JumpFlash.Milliseconds()), 0}} {
		if got := state.FlashStrength(msec(c.ms)); got < c.want-0.01 || got > c.want+0.01 {
			t.Errorf("%d ms: %v, want %v", c.ms, got, c.want)
		}
	}
}

func msec(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// The chat-close hook runs whichever way the open chat stops being open,
// with the chat that closes; the quit hook runs once, before the settings'
// last write.
func TestChatCloseAndQuitHooks(t *testing.T) {
	r := newRig(t, 2)
	a := r.s.Accounts[0]
	r.chats(0, abi.Chat{ID: []byte(ada), Name: []byte("Ada"), LastActivity: 5}, abi.Chat{ID: []byte(grace), Name: []byte("Grace"), LastActivity: 4})
	var closed []string
	r.s.OnChatClose(a, func(_ *state.State, _ *state.Account, chat []byte) { closed = append(closed, string(chat)) })
	a.OpenChat(a.Model.Chats.Find([]byte(ada)))
	a.OpenChat(a.Model.Chats.Find([]byte(grace))) // another chat
	a.OpenChat(0)                                 // Escape
	a.OpenChat(a.Model.Chats.Find([]byte(ada)))
	r.s.Back() // the history: Grace's chat again
	r.cores[0].Emit(ev(abi.EventTagChatRemoved, func(e *abi.Event) { e.ChatRemoved.Chat = []byte(grace) }))
	r.pump()
	a.OpenChat(a.Model.Chats.Find([]byte(ada)))
	r.s.SwitchAccount(1) // parked with its account
	want := []string{ada, grace, ada, grace, ada}
	if fmt.Sprint(closed) != fmt.Sprint(want) {
		t.Errorf("closed %q, want %q", closed, want)
	}
	quits := 0
	r.s.OnQuit(a, func(*state.State) { quits++ })
	r.s.Quit()
	r.s.Quit()
	if quits != 1 {
		t.Errorf("quit hooks ran %d times", quits)
	}
}

// A locked chat opens only in the unlocked folder, whoever asks.
func TestALockedChatOpensOnlyInItsFolder(t *testing.T) {
	r := newRig(t, 1)
	a := r.s.Accounts[0]
	r.chats(0, abi.Chat{ID: []byte(grace), Name: []byte("Secret"), Locked: true, LastActivity: 4})
	ref := a.Model.Chats.Find([]byte(grace))
	a.OpenChat(ref)
	if a.Open != 0 {
		t.Fatal("opened outside the folder")
	}
	a.Mode = state.ListLocked
	a.OpenChat(ref)
	if a.Open != ref {
		t.Error("not opened in the folder")
	}
}

// CP-021, LO-005 (in part): a refused send's text goes back to the open
// chat's composer (Requests.Restore) or into the chat's empty draft, a
// location as its coordinates, and an error toast says why; a restore the
// composer did not take goes to the draft when the chat closes.
func TestARefusedSendComesBack(t *testing.T) {
	r := newRig(t, 1)
	a := r.s.Accounts[0]
	r.chats(0, abi.Chat{ID: []byte(ada), Name: []byte("Ada"), LastActivity: 5}, abi.Chat{ID: []byte(grace), Name: []byte("Grace"), LastActivity: 4})
	a.OpenChat(a.Model.Chats.Find([]byte(ada)))
	refuse := func(chat string, set func(u *abi.Unsent), reason abi.Refusal) {
		r.cores[0].Emit(ev(abi.EventTagSendRefused, func(e *abi.Event) {
			e.SendRefused.Chat, e.SendRefused.Reason = []byte(chat), reason
			set(&e.SendRefused.Unsent)
		}))
		r.pump()
	}
	refuse(ada, func(u *abi.Unsent) { u.Tag, u.Text.Text = abi.UnsentTagText, []byte("hello") }, abi.RefusalOffline)
	if got := r.s.Req.Restore; string(got.Chat) != ada || string(got.Text) != "hello" {
		t.Errorf("restore %q %q", got.Chat, got.Text)
	}
	if n := len(r.s.Toasts); n != 1 || !r.s.Toasts[0].Error || string(r.s.Toasts[0].Text) != "Not sent: Zap is not connected to WhatsApp." {
		t.Errorf("toasts %+v", r.s.Toasts)
	}
	refuse(grace, func(u *abi.Unsent) {
		u.Tag, u.Location.Latitude, u.Location.Longitude = abi.UnsentTagLocation, -23.55052, -46.633308
	}, abi.RefusalQuoteUnavailable)
	if got := string(a.Model.Draft([]byte(grace))); got != "-23.55052, -46.63331" {
		t.Errorf("Grace's draft %q", got)
	}
	a.OpenChat(0)
	if got := string(a.Model.Draft([]byte(ada))); got != "hello" || len(r.s.Req.Restore.Chat) != 0 {
		t.Errorf("Ada's draft %q, restore %q", got, r.s.Req.Restore.Chat)
	}
}

// LO-005: a refused location goes back to its dialog, the spot in
// Dialog.Text, while its chat is on screen and no dialog is up
// (zf:src/app.rs send_refused); a late refusal never replaces a newer
// dialog, and its spot waits in the composer, or in the draft once the
// reader has moved on.
func TestARefusedLocationReopensItsDialog(t *testing.T) {
	r := newRig(t, 1)
	a := r.s.Accounts[0]
	r.chats(0, abi.Chat{ID: []byte(ada), Name: []byte("Ada"), LastActivity: 5}, abi.Chat{ID: []byte(grace), Name: []byte("Grace"), LastActivity: 4})
	adaRef := a.Model.Chats.Find([]byte(ada))
	a.OpenChat(adaRef)
	refuse := func() {
		r.cores[0].Emit(ev(abi.EventTagSendRefused, func(e *abi.Event) {
			e.SendRefused.Chat, e.SendRefused.Reason = []byte(ada), abi.RefusalOffline
			e.SendRefused.Unsent.Tag = abi.UnsentTagLocation
			e.SendRefused.Unsent.Location.Latitude, e.SendRefused.Unsent.Location.Longitude = -23.5505, -46.6333
		}))
		r.pump()
	}
	refuse()
	if d := r.s.Dialog; d.Kind != state.DialogSendLocation || d.Chat != adaRef || string(d.Text) != "-23.55050, -46.63330" {
		t.Fatalf("dialog %v chat %v text %q", d.Kind, d.Chat, d.Text)
	}
	if len(r.s.Req.Restore.Chat) != 0 || len(a.Model.Draft([]byte(ada))) != 0 {
		t.Error("the spot went to the composer too")
	}
	if n := len(r.s.Toasts); n != 1 || !r.s.Toasts[0].Error {
		t.Errorf("toasts %+v", r.s.Toasts)
	}
	// A newer dialog stays, with its own spot; the refused one waits in
	// the composer.
	r.s.OpenDialog(state.Dialog{Kind: state.DialogSendLocation, Chat: adaRef, Text: []byte("51.5074, -0.1278")})
	refuse()
	if d := r.s.Dialog; string(d.Text) != "51.5074, -0.1278" {
		t.Errorf("the newer spot became %q", d.Text)
	}
	if got := r.s.Req.Restore; string(got.Chat) != ada || string(got.Text) != "-23.55050, -46.63330" {
		t.Errorf("restore %q %q", got.Chat, got.Text)
	}
	// Moved on to another chat: the spot waits in Ada's draft.
	r.s.CloseDialog()
	r.s.Req.Restore = state.Prefill{}
	a.OpenChat(a.Model.Chats.Find([]byte(grace)))
	refuse()
	if r.s.Dialog.Open() {
		t.Error("a dialog opened over another chat")
	}
	if got := string(a.Model.Draft([]byte(ada))); got != "-23.55050, -46.63330" {
		t.Errorf("Ada's draft %q", got)
	}
}

// SH-018: the chat open at quit opens again once the list is in; a locked
// one does not.
func TestTheLastChatReopensAtStart(t *testing.T) {
	for _, locked := range []bool{false, true} {
		s := state.New()
		f := fake.New(nil)
		a := s.AddAccount("1", nil, f)
		a.Settings.LastChat = []byte(grace)
		f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
			ev(abi.EventTagChats, func(e *abi.Event) {
				e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
					{ID: []byte(ada), Name: []byte("Ada"), LastActivity: 5},
					{ID: []byte(grace), Name: []byte("Grace"), LastActivity: 4, Locked: locked},
				}}
			}))
		s.Pump(0)
		c, ok := a.OpenChatView()
		if got := ok && string(c.ID()) == grace; got == locked {
			t.Errorf("locked %v: Grace's chat open %v", locked, got)
		}
	}
}

// The app's events reach the screens' hooks in order until one takes it;
// an owner's second registration replaces its first.
func TestAppEventHooks(t *testing.T) {
	s := state.New()
	var seen []string
	type owner struct{ n int }
	a, b := &owner{1}, &owner{2}
	s.OnAppEvent(a, func(_ *state.State, e *app.Event) bool { seen = append(seen, "a-old"); return false })
	s.OnAppEvent(a, func(_ *state.State, e *app.Event) bool { seen = append(seen, "a"); return e.ID == 1 })
	s.OnAppEvent(b, func(_ *state.State, e *app.Event) bool { seen = append(seen, "b"); return true })
	if !s.AppEvent(&app.Event{Kind: app.EvService, ID: 1}) || !s.AppEvent(&app.Event{Kind: app.EvService, ID: 2}) {
		t.Error("not taken")
	}
	if fmt.Sprint(seen) != "[a a b]" {
		t.Errorf("seen %v", seen)
	}
}

// MM-007: a private reply quoting a locked group is hidden (and not sent)
// while the locked folder is closed, shown with it open or the group
// unlocked, and hidden for good once the group is gone.
func TestAPrivateReplyFromALockedGroupWaits(t *testing.T) {
	r := newRig(t, 1)
	a := r.s.Accounts[0]
	const group = "3@g.us"
	r.chats(0, abi.Chat{ID: []byte(ada), Name: []byte("Ada"), LastActivity: 5},
		abi.Chat{ID: []byte(group), Name: []byte("Team"), Kind: abi.ChatKindGroup, Locked: true, LastActivity: 4})
	a.OpenChat(a.Model.Chats.Find([]byte(ada)))
	if r.s.QuoteHidden(a) {
		t.Error("no reply, yet hidden")
	}
	r.s.Panes.Reply.Add([]byte("m1"))
	r.s.Panes.ReplyChat.Add([]byte(group))
	if !r.s.QuoteHidden(a) {
		t.Error("a locked group's quote shows with the folder closed")
	}
	a.Mode = state.ListLocked
	if r.s.QuoteHidden(a) {
		t.Error("hidden with the folder open")
	}
	a.Mode = state.ListChats
	r.chats(0, abi.Chat{ID: []byte(ada), Name: []byte("Ada"), LastActivity: 5},
		abi.Chat{ID: []byte(group), Name: []byte("Team"), Kind: abi.ChatKindGroup, LastActivity: 4})
	if r.s.QuoteHidden(a) {
		t.Error("hidden once unlocked")
	}
	r.chats(0, abi.Chat{ID: []byte(ada), Name: []byte("Ada"), LastActivity: 5})
	if !r.s.QuoteHidden(a) {
		t.Error("shown with its group gone")
	}
}

// A double click on a clip opens the viewer playing (Win.ViewerPlay, as
// upstream's ExpandVideo); the viewer takes it once, and a plain open of
// another item does not play.
func TestTheViewerCanOpenPlaying(t *testing.T) {
	s := state.New()
	s.OpenViewerPlayingAt(3, []byte("clip"), 7*time.Second)
	w := &s.Win
	if !w.Want[state.WinViewer] || w.ViewerChat != 3 || string(w.ViewerMsg.At(0)) != "clip" || w.ViewerHasCard || !w.ViewerPlay || w.ViewerAt != 7*time.Second {
		t.Fatalf("viewer %+v", w)
	}
	if !s.TakeViewerPlay() || s.TakeViewerPlay() {
		t.Error("taken more than once")
	}
	s.OpenViewerPlayingAt(3, []byte("clip"), time.Second)
	s.OpenViewer(3, []byte("photo"), -1)
	if w.ViewerPlay || w.ViewerAt != 0 {
		t.Error("a plain open plays, or from a place")
	}
}

// A chat locked from the phone asks once for its notifications to go: in
// the update that locks it, or a list that has it locked; a chat already
// locked asks nothing on its every update.
func TestLockingAChatWithdrawsItsNotificationsOnce(t *testing.T) {
	s := state.New()
	f := fake.New(nil)
	s.AddAccount("1", nil, f)
	chat := func(locked bool, act int64) abi.Chat {
		return abi.Chat{ID: []byte(grace), Name: []byte("Grace"), LastActivity: act, Locked: locked}
	}
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{chat(false, 4)}}
		}))
	s.Pump(0)
	s.NoticeClears = s.NoticeClears[:0]
	for i, step := range []struct {
		e    *abi.Event
		want int
	}{
		{ev(abi.EventTagChatUpdated, func(e *abi.Event) { e.ChatUpdated.Chat = chat(true, 5) }), 1},
		{ev(abi.EventTagChatUpdated, func(e *abi.Event) { e.ChatUpdated.Chat = chat(true, 6) }), 0},
		{ev(abi.EventTagChatUpdated, func(e *abi.Event) { e.ChatUpdated.Chat = chat(false, 7) }), 0},
		{ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 2, Last: true, Chats: []abi.Chat{chat(true, 8)}}
		}), 1},
	} {
		f.Emit(step.e)
		s.Pump(0)
		n := 0
		for _, c := range s.NoticeClears {
			if string(c.Chat) == grace {
				n++
			}
		}
		if n != step.want {
			t.Errorf("step %d: %d withdrawals asked, want %d", i, n, step.want)
		}
		s.NoticeClears = s.NoticeClears[:0]
	}
}

func TestFirstSeenLockedChatWithdrawsItsNotificationsOnce(t *testing.T) {
	for _, tag := range []abi.EventTag{abi.EventTagChatUpdated, abi.EventTagChats} {
		t.Run(fmt.Sprint(tag), func(t *testing.T) {
			s := state.New()
			f := fake.New(nil)
			s.AddAccount("1", nil, f)
			c := abi.Chat{ID: []byte(grace), Locked: true}
			e := ev(tag, func(e *abi.Event) {
				if tag == abi.EventTagChatUpdated {
					e.ChatUpdated.Chat = c
				} else {
					e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{c}}
				}
			})
			for i, want := range []int{1, 0, 0} {
				f.Emit(e)
				s.Pump(0)
				if len(s.NoticeClears) != want {
					t.Fatalf("event %d: %d clears, want %d", i, len(s.NoticeClears), want)
				}
				for _, clear := range s.NoticeClears {
					if clear.Account != "1" || string(clear.Chat) != grace || !clear.Locked || clear.All {
						t.Fatalf("wrong privacy withdrawal: %+v", clear)
					}
				}
				s.NoticeClears = s.NoticeClears[:0]
			}
		})
	}
}
