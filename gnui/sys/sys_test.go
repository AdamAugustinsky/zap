package sys

import (
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/app/dock"
	"gonativeui/app/login"
	"gonativeui/app/notify"
	"gonativeui/menu"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// NT-001: a message in a chat you are not looking at raises a notification
// with the chat, the sender, the text and a picture.
func TestAMessageRaisesANotificationWithTheChatAndTheText(t *testing.T) {
	r := newRig(t)
	r.incoming(ada, ada, "the engine is ready", 1)
	n := r.lastNote()
	if n.Title != "Ada Lovelace" || n.Body != "the engine is ready" {
		t.Errorf("%q %q", n.Title, n.Body)
	}
	if n.Sound != notify.SoundDefault || n.Category != catMessage {
		t.Errorf("sound %q category %q", n.Sound, n.Category)
	}
	acct, chat, msg, ok := decodeTarget(n.UserData)
	if !ok || acct != "1" || chat != ada || msg != "m1" {
		t.Errorf("the note carries %q %q %q", acct, chat, msg)
	}
	if len(r.c.cats) != 1 || r.c.cats[0].ID != catMessage || len(r.c.cats[0].Actions) != 1 || !r.c.cats[0].Actions[0].Reply {
		t.Errorf("the reply action is registered: %+v", r.c.cats)
	}
}

func TestANotificationWhosePictureCannotBeCopiedIsPostedWithoutIt(t *testing.T) {
	r := newRig(t)
	r.incoming(ada, ada, "one", 1)
	r.c.posts[0].Attachment = "/gone.jpg" // as if the avatar had one
	r.y.n.inflight[1].note.Attachment = "/gone.jpg"
	r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPost, Ticket: 1, ErrCode: notify.CodeAttachment}))
	if len(r.c.posts) != 2 || r.c.posts[1].Attachment != "" || r.c.posts[1].Body != "one" {
		t.Errorf("%+v", r.c.posts)
	}
}

// The permission question is asked once, at the first notification, which
// waits for the answer.
func TestTheFirstNotificationAsksForPermissionAndWaits(t *testing.T) {
	r := newRig(t)
	r.y.n.perm = permUnknown
	r.incoming(ada, ada, "one", 1)
	if len(r.c.posts) != 0 || r.c.asked != 0 {
		t.Fatal("it posted, or asked, before the status came")
	}
	r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultStatus, Status: notify.StatusNotDetermined}))
	if r.c.asked != 1 {
		t.Fatalf("asked %d times", r.c.asked)
	}
	r.incoming(group, grace, "two", 1)
	if r.c.asked != 1 || len(r.c.posts) != 0 {
		t.Fatalf("it asked again, or posted, while the question is up: %d %d", r.c.asked, len(r.c.posts))
	}
	r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPermission, Granted: true}))
	if len(r.c.posts) != 2 {
		t.Errorf("the held notifications did not go out: %d", len(r.c.posts))
	}
	r.incoming(ada, ada, "three", 2)
	if len(r.c.posts) != 3 {
		t.Error("posting stopped after the answer")
	}
}

func TestWithoutACenterThereAreNoNotifications(t *testing.T) {
	r := newRig(t, func(r *rig) { r.c.available = false })
	r.incoming(ada, ada, "one", 1)
	if len(r.c.posts) != 0 || len(r.c.cats) != 0 {
		t.Error("it used a center that is not available")
	}
}

// NT-004: reading a chat, here or on another device, takes its notifications
// back, and only that chat's.
func TestReadingAChatTakesBackItsNotifications(t *testing.T) {
	r := newRig(t)
	r.incoming(ada, ada, "one", 1)
	r.incoming(ada, ada, "two", 2)
	r.incoming(group, grace, "three", 1)
	if len(r.c.shown) != 3 {
		t.Fatalf("%d shown", len(r.c.shown))
	}
	// Read on another device: the chat's unread count drops to nothing.
	r.read(ada)
	if len(r.c.shown) != 1 || !strings.Contains(r.c.shown["n3"].Title, "Engines") {
		t.Errorf("only the group's notification is left: %v", r.c.shown)
	}
	// Opened here: logic asks for the same.
	r.s.NoticeClears = append(r.s.NoticeClears, state.NoticeClear{Account: "1", Chat: []byte(group)})
	r.frame()
	if len(r.c.shown) != 0 {
		t.Errorf("clearing the chat left %d", len(r.c.shown))
	}
}

func TestLockingTheAppTakesEveryNotificationBack(t *testing.T) {
	r := newRig(t)
	r.incoming(ada, ada, "one", 1)
	r.incoming(group, grace, "two", 1)
	r.s.Lock.Locked = true
	r.s.Locked() // the lock asks for every notification to go
	r.frame()
	if len(r.c.shown) != 0 {
		t.Errorf("%d notifications outlast the lock", len(r.c.shown))
	}
}

func TestUnlinkingAnAccountTakesItsNotificationsBack(t *testing.T) {
	r := newRig(t)
	r.incoming(ada, ada, "one", 1)
	r.pump(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagLoggedOut }))
	if len(r.c.shown) != 0 {
		t.Errorf("%d left", len(r.c.shown))
	}
}

// NT-015: a reply typed into the notification is sent, and answering reads.
func TestAReplyFromTheNotificationIsSent(t *testing.T) {
	r := newRig(t)
	r.incoming(ada, ada, "lunch?", 1)
	n := r.lastNote()
	e := r.c.respond(notify.Response{Action: actReply, Reply: []byte("  yes, at noon "), UserData: n.UserData})
	if !r.y.AppEvent(nil, r.s, e) {
		t.Error("a reply needs no window")
	}
	r.frame()
	sent := r.sent(abi.CommandTagSendText)
	if len(sent) != 1 || string(sent[0].SendText.Chat) != ada || string(sent[0].SendText.Text) != "yes, at noon" {
		t.Fatalf("%+v", sent)
	}
	if reads := r.sent(abi.CommandTagMarkRead); len(reads) != 1 || string(reads[0].MarkRead.Chat) != ada {
		t.Errorf("the chat was not marked read: %+v", reads)
	}
	r.pump()
	r.read(ada)
	if len(r.c.shown) != 0 {
		t.Error("the answered chat's notification stays")
	}
}

func TestAnEmptyReplyIsNotSentAndAReplyBehindTheLockWaits(t *testing.T) {
	r := newRig(t)
	r.incoming(ada, ada, "lunch?", 1)
	n := r.lastNote()
	r.y.AppEvent(nil, r.s, r.c.respond(notify.Response{Action: actReply, Reply: []byte("   "), UserData: n.UserData}))
	r.frame()
	if len(r.sent(abi.CommandTagSendText)) != 0 {
		t.Fatal("a blank reply was sent")
	}
	r.s.Lock.Locked = true
	r.y.AppEvent(nil, r.s, r.c.respond(notify.Response{Action: actReply, Reply: []byte("later"), UserData: n.UserData}))
	r.frame()
	if len(r.sent(abi.CommandTagSendText)) != 0 {
		t.Fatal("a reply was sent behind the lock")
	}
	r.s.Lock.Locked = false
	r.frame()
	if got := r.sent(abi.CommandTagSendText); len(got) != 1 || string(got[0].SendText.Text) != "later" {
		t.Errorf("%+v", got)
	}
}

func TestAResponseOfNoRecognisedKindDoesNothing(t *testing.T) {
	r := newRig(t)
	r.incoming(ada, ada, "lunch?", 1)
	n := r.lastNote()
	for _, a := range []int64{notify.ActionDismiss, notify.ActionOther, 55} {
		r.y.AppEvent(nil, r.s, r.c.respond(notify.Response{Action: a, UserData: n.UserData}))
	}
	r.y.AppEvent(nil, r.s, r.c.respond(notify.Response{Action: notify.ActionClick, UserData: []byte("garbage")}))
	r.frame()
	if r.a.Open != 0 || len(r.sent(abi.CommandTagSendText)) != 0 || len(r.sent(abi.CommandTagLoadChat)) != 0 {
		t.Errorf("%v", r.f.SentTags())
	}
}

// r2 adds a second account that is not the one the test talks to.
func (r *rig) r2() {
	r.s.AddAccount("2", nil, nil)
}

// NT-008: the Dock icon shows the number of unread chats across accounts.
func TestTheDockBadgeCountsUnreadChats(t *testing.T) {
	r := newRig(t)
	if got := r.d.badges; len(got) != 1 || got[0] != 2 {
		t.Fatalf("the badge at start: %v", got)
	}
	r.frame()
	r.frame()
	if len(r.d.badges) != 1 {
		t.Errorf("the badge was set again with nothing changed: %v", r.d.badges)
	}
	r.incoming(grace, grace, "hi", 1)
	r.read(ada)
	if got := r.d.badges[len(r.d.badges)-1]; got != 2 {
		t.Errorf("badges %v", r.d.badges)
	}
	r.read(group)
	r.read(grace)
	if got := r.d.badges[len(r.d.badges)-1]; got != 0 {
		t.Errorf("badges %v", r.d.badges)
	}
	// Archived, locked and muted chats do not count.
	for _, c := range []abi.Chat{
		{ID: []byte(ada), Unread: 4, Archived: true},
		{ID: []byte(grace), Unread: 4, Locked: true},
		{ID: []byte(group), Unread: 4, Kind: abi.ChatKindGroup, HasMutedUntil: true},
	} {
		c := c
		r.pump(ev(abi.EventTagChatUpdated, func(e *abi.Event) { e.ChatUpdated.Chat = c }))
	}
	if got := r.d.badges[len(r.d.badges)-1]; got != 0 {
		t.Errorf("badges %v", r.d.badges)
	}
	// A chat marked unread by hand counts.
	r.pump(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(ada), MarkedUnread: true}
	}))
	if got := r.d.badges[len(r.d.badges)-1]; got != 1 {
		t.Errorf("badges %v", r.d.badges)
	}
}

// CA-029: a ringing call bounces the Dock until it stops ringing.
func TestARingingCallBouncesTheDockUntilItStops(t *testing.T) {
	r := newRig(t)
	d := r.y.Dock()
	d.Ring()
	d.Ring()
	if len(r.d.bounces) != 1 || !r.d.bounces[0] {
		t.Fatalf("bounces %v: one critical bounce", r.d.bounces)
	}
	d.Stop()
	d.Stop()
	if len(r.d.cancels) != 1 || r.d.cancels[0] != dock.Attention(1) {
		t.Errorf("cancels %v", r.d.cancels)
	}
	d.Ring()
	if len(r.d.bounces) != 2 {
		t.Error("a second call does not bounce")
	}
}

func TestTheCallDockIsInstalledForTheCalls(t *testing.T) {
	r := newRig(t)
	var got Dock
	r.y.CallDock = func(_ *state.State, d Dock) { got = d }
	r.y.begin(r.s, false)
	if got == nil {
		t.Fatal("ui/calls was not given the Dock")
	}
	got.Ring()
	if len(r.d.bounces) != 1 {
		t.Error("the Dock does not bounce")
	}
}

// CA-004: an incoming call raises a notification that goes when it stops
// ringing, and that follows the chat's own rules.
func TestAnIncomingCallRaisesANotificationUntilItStopsRinging(t *testing.T) {
	r := newRig(t)
	call := func(phase abi.CallPhase, dir abi.CallDirection, video bool) {
		r.pump(ev(abi.EventTagCall, func(e *abi.Event) {
			e.Call.Update = abi.CallUpdate{Generation: 7, Chat: []byte(ada), Direction: dir, Video: video, Phase: phase}
		}))
	}
	call(abi.CallPhasePlacing, abi.CallDirectionOutgoing, false)
	if len(r.c.posts) != 0 {
		t.Fatal("an outgoing call notified")
	}
	call(abi.CallPhaseIncoming, abi.CallDirectionIncoming, true)
	call(abi.CallPhaseIncoming, abi.CallDirectionIncoming, true)
	if len(r.c.posts) != 1 {
		t.Fatalf("%d notifications for one call", len(r.c.posts))
	}
	n := r.lastNote()
	if n.Title != "Ada Lovelace" || n.Body != "Incoming video call" || n.Sound != "" || n.Category != "" {
		t.Errorf("%+v", n)
	}
	r.incoming(ada, ada, "a message while it rings", 2)
	call(abi.CallPhaseConnecting, abi.CallDirectionIncoming, true)
	if len(r.c.shown) != 1 || !strings.Contains(r.c.shown["n2"].Body, "message") {
		t.Errorf("answering a call takes back its own notification only: %v", r.c.shown)
	}
}

func TestACallInAMutedOrLockedChatAndBehindTheLockStaysQuiet(t *testing.T) {
	r := newRig(t)
	gen := uint64(0)
	ring := func(chat string) {
		gen++
		r.pump(ev(abi.EventTagCall, func(e *abi.Event) {
			e.Call.Update = abi.CallUpdate{Generation: gen, Chat: []byte(chat), Direction: abi.CallDirectionIncoming, Phase: abi.CallPhaseIncoming}
		}))
	}
	r.pump(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(grace), Name: []byte("Grace"), HasMutedUntil: true}
	}))
	ring(grace)
	if len(r.c.posts) != 0 {
		t.Fatal("a call in a muted chat notified")
	}
	r.s.Lock.Locked = true
	ring(ada)
	if n := r.lastNote(); n.Title != "Zap" || n.Body != "Incoming call" {
		t.Errorf("behind the lock: %+v", n)
	}
	// A caller we have no chat with rings with the ordinary sound.
	r.s.Lock.Locked = false
	ring("55@s.whatsapp.net")
	if len(r.c.posts) != 2 {
		t.Error("a stranger's call did not notify")
	}
}

// NT-009, NT-010, NT-012: the status item.
func TestTheStatusItemShowsOrHidesTheWindowLocksAndQuits(t *testing.T) {
	r := newRig(t)
	if len(r.item.menus) != 1 || r.item.idx[0] != 0 {
		t.Fatalf("the menu is set once, without Lock Zap: %v", r.item.idx)
	}
	m := r.item.menus[0]
	titles := func(idx int) []string {
		var out []string
		for _, it := range m.Menus()[idx].Items {
			if it.Kind == menu.ItemCommand {
				c, _ := m.Command(it.Command)
				out = append(out, c.Title)
			}
		}
		return out
	}
	if got := strings.Join(titles(0), "|"); got != "Show or hide Zap|Quit" {
		t.Errorf("%s", got)
	}
	if got := strings.Join(titles(1), "|"); got != "Show or hide Zap|Lock Zap|Quit" {
		t.Errorf("%s", got)
	}

	cmd := func(id uint64) *app.Event { return &app.Event{Kind: app.EvCommand, ID: id} }
	// Shown: the command hides it.
	if !r.y.AppEvent(nil, r.s, cmd(uint64(cmdTrayShow))) || r.win.hides != 1 {
		t.Errorf("hides %d", r.win.hides)
	}
	// Hidden: the command asks the shell to show it.
	if !r.y.AppEvent(nil, r.s, cmd(uint64(cmdTrayShow))) || !r.s.Req.RaiseMain || !r.y.shown {
		t.Errorf("raise %v shown %v", r.s.Req.RaiseMain, r.y.shown)
	}
	// No window at all: the same request, which the shell answers by
	// opening one.
	r.s.Req.RaiseMain = false
	r.win = nil
	r.y.shown = false
	if !r.y.AppEvent(nil, r.s, cmd(uint64(cmdTrayShow))) || !r.s.Req.RaiseMain {
		t.Error("the shell was not asked to open the window")
	}

	var locked int
	r.y.LockNow = func(*state.State) { locked++ }
	r.y.AppEvent(nil, r.s, cmd(uint64(cmdTrayLock)))
	if locked != 0 {
		t.Error("it locked with no password set")
	}
	r.s.Settings.AppLockHash = []byte("hash")
	r.frame()
	if len(r.item.idx) != 2 || r.item.idx[1] != 1 {
		t.Errorf("Lock Zap is offered while a password is set: %v", r.item.idx)
	}
	r.y.AppEvent(nil, r.s, cmd(uint64(cmdTrayLock)))
	if locked != 1 {
		t.Error("Lock Zap did not lock")
	}
	r.s.Settings.AppLockHash = nil
	r.frame()
	if len(r.item.idx) != 3 || r.item.idx[2] != 0 {
		t.Errorf("%v", r.item.idx)
	}
	if !r.y.AppEvent(nil, r.s, cmd(uint64(cmdTrayQuit))) || r.quit != 1 {
		t.Error("Quit did not quit")
	}
	if r.y.AppEvent(nil, r.s, cmd(3)) {
		t.Error("the shell's own commands are the shell's")
	}
}

// NT-013: started at login, the app runs hidden: no window shown, and a
// message still notifies.
func TestAHiddenStartShowsNoWindowAndStillNotifies(t *testing.T) {
	r := newRig(t)
	r.y.begin(r.s, true)
	r.y.n.perm = permGranted
	if r.y.shown || r.y.active {
		t.Fatal("a hidden start has a shown window")
	}
	r.s.Focused = true // stale: no frame has run
	r.openChat(ada)
	r.incoming(ada, ada, "good morning", 1)
	if len(r.c.posts) != 1 {
		t.Error("a hidden app did not notify for the chat it has open")
	}
	if r.win.shows != 0 || r.s.Req.RaiseMain {
		t.Error("a hidden start surfaced")
	}
	// The status item's first window.
	r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvCommand, ID: uint64(cmdTrayShow)})
	if !r.s.Req.RaiseMain || !r.y.shown {
		t.Error("the status item did not open the first window")
	}
}

func TestADockClickMarksTheWindowShown(t *testing.T) {
	r := newRig(t)
	r.y.shown = false
	r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvReopen})
	if !r.y.shown {
		t.Error("the window is still taken for hidden")
	}
	r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvDeactivated})
	if r.y.active {
		t.Error("still active")
	}
	r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvActivated})
	if !r.y.active {
		t.Error("not active")
	}
}

// NT-014: after the machine wakes every account reconnects, and the lock
// hears of the sleep.
func TestAWakeReconnectsEveryAccountAndASleepLocksAndSaves(t *testing.T) {
	r := newRig(t)
	f2 := fake.New(nil)
	r.s.AddAccount("2", nil, f2)
	n := len(f2.SentTags())       // the settings its core hears as it opens
	r.s.AddAccount("3", nil, nil) // its core is still opening
	if r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvDidWake}) {
		t.Error("the shell and the lock may want the wake too")
	}
	if got := r.sent(abi.CommandTagResumed); len(got) != 1 {
		t.Errorf("account 1: %d", len(got))
	}
	if got := f2.SentTags()[n:]; len(got) != 1 || got[0] != abi.CommandTagResumed {
		t.Errorf("account 2: %v", got)
	}
	var slept, saved int
	r.y.Sleep = func(*state.State) { slept++ }
	r.y.Save = func(*state.State) { saved++ }
	r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvWillSleep})
	if slept != 1 || saved != 1 {
		t.Errorf("slept %d saved %d", slept, saved)
	}
}

// LN-001, LN-002, LN-005: a link opens its chat, switching to the window.
func TestALinkOpensItsChatWithItsTemplate(t *testing.T) {
	r := newRig(t)
	r.win = nil
	r.urls[1] = "whatsapp://send?phone=1&text=Hi%20Ada"
	// "1" is too short for a number: the link is unreadable.
	e := &app.Event{Kind: app.EvOpenURLs, ID: 1}
	r.y.AppEvent(nil, r.s, e)
	if len(r.y.k.pending) != 0 || len(r.s.Toasts) != 1 || !r.s.Toasts[0].Error {
		t.Fatalf("an unreadable link: pending %d toasts %d", len(r.y.k.pending), len(r.s.Toasts))
	}
	if !strings.Contains(string(r.s.Toasts[0].Text), "cannot read a phone number") {
		t.Errorf("%q", r.s.Toasts[0].Text)
	}
	// A chat the account has: its number is Ada's.
	r.pump(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 2, Last: true, Chats: []abi.Chat{{ID: []byte("20123456789@s.whatsapp.net"), Name: []byte("Egypt"), LastActivity: 5}}}
	}))
	r.urls[2] = "wa://send?phone=%2B20123456789&text=Hi%20there"
	e = &app.Event{Kind: app.EvOpenURLs, ID: 2}
	if !r.y.AppEvent(nil, r.s, e) || !r.s.Req.RaiseMain {
		t.Error("the window was not brought forward")
	}
	r.frame()
	if r.a.Open == 0 || string(r.a.Model.Chats.At(r.a.Open).ID()) != "20123456789@s.whatsapp.net" {
		t.Fatal("the chat did not open")
	}
	if p := &r.s.Req.Prefill; string(p.Chat) != "20123456789@s.whatsapp.net" || string(p.Text) != "Hi there" {
		t.Errorf("the template: %q %q", p.Chat, p.Text)
	}
}

func TestALinkToANumberTheCoreHasNoChatWithMakesTheChat(t *testing.T) {
	r := newRig(t)
	r.urls[1] = "https://wa.me/20123456789"
	r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvOpenURLs, ID: 1})
	r.frame()
	ensure := r.sent(abi.CommandTagEnsureChat)
	if len(ensure) != 1 || string(ensure[0].EnsureChat.Chat) != "20123456789@s.whatsapp.net" || string(ensure[0].EnsureChat.Name) != "+20123456789" {
		t.Fatalf("%+v", ensure)
	}
	r.frame()
	if len(r.sent(abi.CommandTagEnsureChat)) != 1 {
		t.Error("the chat was asked for twice")
	}
	if r.a.Open != 0 {
		t.Fatal("opened before the chat came")
	}
	r.pump(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte("20123456789@s.whatsapp.net"), Name: []byte("+20 123 456 789")}
	}))
	if r.a.Open == 0 {
		t.Error("the chat did not open once it came")
	}
}

// LN-004: a group invite link shows the invite.
func TestAnInviteLinkShowsTheInvite(t *testing.T) {
	r := newRig(t)
	r.s.OpenDialog(state.Dialog{Kind: state.DialogShortcuts})
	r.urls[1] = "https://chat.whatsapp.com/AbCdEf1234567890XyZ"
	r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvOpenURLs, ID: 1})
	r.frame()
	if r.s.Dialog.Kind != state.DialogJoinGroup || string(r.s.Dialog.Text) != "AbCdEf1234567890XyZ" {
		t.Errorf("%+v", r.s.Dialog)
	}
}

func TestACallLinkJoinsTheCall(t *testing.T) {
	r := newRig(t)
	var got struct {
		token string
		video bool
	}
	r.y.JoinCall = func(_ *state.State, _ *state.Account, token []byte, video bool) {
		got.token, got.video = string(token), video
	}
	r.urls[1] = "https://call.whatsapp.com/video/AbCdEf1234567890"
	r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvOpenURLs, ID: 1})
	r.frame()
	if got.token != "AbCdEf1234567890" || !got.video {
		t.Errorf("%+v", got)
	}
}

// The guide: while the app lock is up links wait for the unlock rather than
// opening anything the lock hides, and then open in the order they arrived.
func TestLinksWaitForTheUnlockAndOpenInOrder(t *testing.T) {
	r := newRig(t)
	r.pump(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 2, Last: true, Chats: []abi.Chat{
			{ID: []byte("20123456789@s.whatsapp.net"), Name: []byte("One"), LastActivity: 5},
			{ID: []byte("20123456780@s.whatsapp.net"), Name: []byte("Two"), LastActivity: 4}}}
	}))
	r.s.Lock.Locked = true
	r.urls[1], r.urls[2] = "wa.me/20123456789", "wa.me/20123456780"
	r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvOpenURLs, ID: 1})
	r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvOpenURLs, ID: 2})
	r.frame()
	if r.a.Open != 0 {
		t.Fatal("a link opened a chat behind the lock")
	}
	r.s.Lock.Locked = false
	r.frame()
	if string(r.a.Model.Chats.At(r.a.Open).ID()) != "20123456780@s.whatsapp.net" {
		t.Error("the links did not both open, in order")
	}
	loads := r.sent(abi.CommandTagLoadChat)
	if len(loads) != 2 || string(loads[0].LoadChat.Chat) != "20123456789@s.whatsapp.net" {
		t.Errorf("%d loads", len(loads))
	}
}

func TestALinkWaitsForALinkedAccount(t *testing.T) {
	r := newRig(t)
	r.pump(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagLoggedOut }))
	r.y.openArg("wa.me/20123456789")
	r.frame()
	if len(r.y.k.pending) != 1 || len(r.sent(abi.CommandTagEnsureChat)) != 0 {
		t.Errorf("pending %d, sent %v", len(r.y.k.pending), r.f.SentTags())
	}
}

// LN-006, LN-007: the command line.
func TestTheCommandLine(t *testing.T) {
	for args, want := range map[string]command{"": cmdShow, "quit": cmdQuit, "reload-themes": cmdReloadThemes,
		"+20123456789": cmdLink, "https://chat.whatsapp.com/AbCdEf1234567890XyZ": cmdLink} {
		var in []string
		if args != "" {
			in = []string{args}
		}
		if got, _, err := parseArgs(in); err != nil || got != want {
			t.Errorf("%q: %v %v", args, got, err)
		}
	}
	_, _, err := parseArgs([]string{"hello"})
	if err == nil || err.Error() != "Zap cannot read a phone number from hello" {
		t.Errorf("%v", err)
	}
	if _, _, err := parseArgs([]string{"reload-themes", "20123456789"}); err == nil {
		t.Error("reload-themes takes no number")
	}
}

func TestASecondLaunchesRequestsReachTheRunningCopy(t *testing.T) {
	r := newRig(t)
	r.win = nil
	r.y.openArgs([]string{"quit"})
	r.y.openArgs([]string{"reload-themes"})
	if r.quit != 1 || r.reload != 1 {
		t.Errorf("quit %d reload %d", r.quit, r.reload)
	}
	if !r.y.openArgs(nil) {
		t.Error("a launch with no argument brings the window forward")
	}
	if r.y.openArgs([]string{"nonsense"}) || len(r.s.Toasts) != 1 {
		t.Error("an unreadable argument opens nothing and says so")
	}
	if !r.y.openArgs([]string{"wa.me/20123456789"}) || len(r.y.k.pending) != 1 {
		t.Error("a link queues its chat")
	}
}

// Settings → Start at login.
func TestStartAtLoginIsTheLoginAgent(t *testing.T) {
	it := &fakeLogin{}
	if approval, err := setLogin(it, true); !approval || err != nil || it.enabled != 1 {
		t.Errorf("enable: %v %v", approval, err)
	}
	if approval, err := setLogin(it, false); approval || err != nil || it.off != 1 {
		t.Errorf("disable: %v %v", approval, err)
	}
	it.err = errTest
	if _, err := setLogin(it, true); err != errTest {
		t.Errorf("%v", err)
	}
}

func TestTheSettingFollowsTheSystemsRecordAtStart(t *testing.T) {
	r := newRig(t, func(r *rig) { r.lg.state = login.Enabled })
	deadline := time.Now().Add(2 * time.Second)
	for !r.s.Settings.StartAtLogin && time.Now().Before(deadline) {
		r.frame()
		time.Sleep(time.Millisecond)
	}
	if !r.s.Settings.StartAtLogin {
		t.Error("the setting does not follow the record")
	}
}

func TestConfigKeepsRunningAsTheSettingSays(t *testing.T) {
	if c := Config("x", true); c.ID != "x" || !c.KeepRunning {
		t.Errorf("%+v", c)
	}
	if c := Config("x", false); c.KeepRunning {
		t.Errorf("%+v", c)
	}
}

func TestTheServicesEndWithTheApp(t *testing.T) {
	r := newRig(t)
	r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvWillTerminate})
	if !r.item.removed {
		t.Error("the status item stays")
	}
}
