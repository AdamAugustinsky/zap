package sys

import (
	"strconv"
	"testing"

	"gonativeui/app"
	"gonativeui/app/notify"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/logic"
	"zapfast/state"
)

// logic decides, sys shows: whatever the queues hold is given to the desktop
// and emptied, frame after frame.
func TestTheQueuesAreDrainedEveryFrame(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 3; i++ {
		r.incoming(ada, ada, "one", 1)
		r.s.NoticeClears = append(r.s.NoticeClears, state.NoticeClear{Account: "1", Chat: []byte(grace)})
		r.frame()
		if len(r.s.Notices) != 0 || len(r.s.NoticeClears) != 0 || r.s.Attention {
			t.Fatalf("round %d: the queues kept %d, %d, %v", i, len(r.s.Notices), len(r.s.NoticeClears), r.s.Attention)
		}
	}
	if len(r.c.posts) != 3 {
		t.Errorf("%d posted", len(r.c.posts))
	}
}

// A notice carries everything the desktop shows: who, what, which sound.
func TestANoticeIsPostedAsLogicDecidedIt(t *testing.T) {
	r := newRig(t)
	r.notice(state.Notice{Account: "1", Chat: []byte(group), Message: []byte("m7"), Title: "Engines", Body: "Grace: save me a seat",
		Picture: []byte("/p/engines.jpg"), Sound: state.SoundChoice{Sound: state.SoundAlert}})
	n := r.lastNote()
	if n.Title != "Engines" || n.Body != "Grace: save me a seat" || n.Attachment != "/p/engines.jpg" {
		t.Errorf("%+v", n)
	}
	if n.Sound != notify.SoundDefault || n.Category != catMessage || n.Thread != "1/"+group {
		t.Errorf("sound %q category %q thread %q", n.Sound, n.Category, n.Thread)
	}
	if acct, chat, msg, ok := decodeTarget(n.UserData); !ok || acct != "1" || chat != group || msg != "m7" {
		t.Errorf("the note carries %q %q %q", acct, chat, msg)
	}
	if len(r.c.cats) != 1 || !r.c.cats[0].Actions[0].Reply {
		t.Errorf("the reply action is registered: %+v", r.c.cats)
	}
}

func TestAHiddenNoticeCarriesNothingOfTheMessageNotEvenReply(t *testing.T) {
	r := newRig(t)
	r.notice(state.Notice{Account: "1", Chat: []byte(ada), Message: []byte("m1"), Title: "Zap", Body: "New message",
		Hidden: true, Picture: []byte("/p/ada.jpg"), Sound: state.SoundChoice{Sound: state.SoundReceive}})
	n := r.lastNote()
	if n.Attachment != "" || n.Category != "" || n.Body != "New message" {
		t.Errorf("%+v", n)
	}
	if _, _, msg, ok := decodeTarget(n.UserData); !ok || msg != "m1" {
		t.Error("a click on it must still open the message")
	}
}

func TestTheSoundOfTheNoticeIsTheDesktopsOrNoneOrPlayedHere(t *testing.T) {
	r := newRig(t)
	for _, c := range []struct {
		sound state.SoundChoice
		want  string
		plays string
	}{
		{state.SoundChoice{Sound: state.SoundReceive}, notify.SoundDefault, ""},
		{state.SoundChoice{Sound: state.SoundNone}, "", ""},
		{state.SoundChoice{Sound: state.SoundCustom, Path: []byte("/s/ding.wav")}, "", "/s/ding.wav"},
	} {
		r.played = nil
		r.notice(state.Notice{Account: "1", Chat: []byte(ada), Message: []byte("m"), Title: "t", Body: "b", Sound: c.sound})
		if n := r.lastNote(); n.Sound != c.want {
			t.Errorf("%v: sound %q", c.sound, n.Sound)
		}
		if (c.plays == "") != (len(r.played) == 0) || (c.plays != "" && r.played[0] != c.plays) {
			t.Errorf("%v: played %v", c.sound, r.played)
		}
	}
}

func TestADeniedPermissionPostsNothingAndTheQueueStillEmpties(t *testing.T) {
	r := newRig(t)
	r.y.n.perm = permAsking
	r.notice(state.Notice{Account: "1", Chat: []byte(ada), Title: "t", Body: "one"})
	r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPermission, Granted: false}))
	r.notice(state.Notice{Account: "1", Chat: []byte(ada), Title: "t", Body: "two"})
	if len(r.c.posts) != 0 || len(r.s.Notices) != 0 {
		t.Errorf("%d posted, %d queued", len(r.c.posts), len(r.s.Notices))
	}
}

func TestAClearOfAllTakesBackEveryNotificationAndAClearOfAChatOnlyItsOwn(t *testing.T) {
	r := newRig(t)
	r.notice(state.Notice{Account: "1", Chat: []byte(ada), Title: "a", Body: "1"})
	r.notice(state.Notice{Account: "1", Chat: []byte(group), Title: "g", Body: "2"})
	r.s.NoticeClears = append(r.s.NoticeClears, state.NoticeClear{Account: "1", Chat: []byte(ada)})
	r.frame()
	if len(r.c.shown) != 1 {
		t.Fatalf("%d shown", len(r.c.shown))
	}
	r.s.NoticeClears = append(r.s.NoticeClears, state.NoticeClear{All: true})
	r.frame()
	if len(r.c.shown) != 0 {
		t.Errorf("%d left", len(r.c.shown))
	}
}

// A message's attention is drained but never bounces the Dock on macOS:
// upstream asks for it on Linux only (zf:src/app.rs:8513-8522).
func TestAMessageDoesNotBounceTheDock(t *testing.T) {
	r := newRig(t)
	r.y.active = false
	r.notice(state.Notice{Account: "1", Chat: []byte(ada), Title: "a", Body: "1"})
	r.notice(state.Notice{Account: "1", Chat: []byte(grace), Title: "g", Body: "2"})
	if len(r.d.bounces) != 0 || r.s.Attention {
		t.Fatalf("bounces %v, attention left %v", r.d.bounces, r.s.Attention)
	}
	if len(r.c.shown) != 2 {
		t.Errorf("%d notifications shown", len(r.c.shown))
	}
	r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvActivated})
	if len(r.d.cancels) != 0 {
		t.Errorf("cancels %v", r.d.cancels)
	}
}

// NT-003: a click opens the chat at the message and shows the window.
func TestAClickOpensTheMessageAndRaisesTheWindow(t *testing.T) {
	r := newRig(t)
	r.incoming(group, grace, "look here", 1)
	n := r.lastNote()
	if !r.y.AppEvent(nil, r.s, r.c.respond(notify.Response{Action: notify.ActionClick, UserData: n.UserData})) {
		t.Error("the click was not handled")
	}
	r.frame()
	if string(r.a.Model.Chats.At(r.a.Open).ID()) != group {
		t.Fatalf("the chat did not open: %v", r.a.Open)
	}
	if string(r.s.Panes.Jump.Msg.At(0)) != "m1" {
		t.Errorf("the message was not jumped to")
	}
	if !r.s.Req.RaiseMain {
		t.Error("the main window was not asked for")
	}
}

func TestAClickBehindTheLockIsHandedToTheLock(t *testing.T) {
	r := newRig(t)
	r.incoming(group, grace, "look here", 1)
	n := r.lastNote()
	var held struct {
		acct, chat, msg string
	}
	r.y.DeferOpen = func(_ *state.State, account, chat string, message []byte) bool {
		held.acct, held.chat, held.msg = account, chat, string(message)
		return true
	}
	r.s.Lock.Locked = true
	r.y.AppEvent(nil, r.s, r.c.respond(notify.Response{Action: notify.ActionClick, UserData: n.UserData}))
	r.frame()
	if held.acct != "1" || held.chat != group || held.msg != "m1" {
		t.Errorf("held %+v", held)
	}
	if r.a.Open != 0 {
		t.Error("a notification opened a chat behind the lock")
	}
}

func TestAClickBehindTheLockWithoutTheLocksHookWaitsForTheUnlock(t *testing.T) {
	r := newRig(t)
	r.incoming(group, grace, "look here", 1)
	n := r.lastNote()
	r.s.Lock.Locked = true
	r.y.AppEvent(nil, r.s, r.c.respond(notify.Response{Action: notify.ActionClick, UserData: n.UserData}))
	r.frame()
	if r.a.Open != 0 {
		t.Fatal("a notification opened a chat behind the lock")
	}
	r.s.Lock.Locked = false
	r.frame()
	if r.a.Open == 0 {
		t.Error("the chat did not open after the unlock")
	}
}

func TestAClickOnACallRingingBringsUpTheCallWindow(t *testing.T) {
	r := newRig(t)
	ring := &abi.CallUpdate{Generation: 7, Chat: []byte(ada), Direction: abi.CallDirectionIncoming, Phase: abi.CallPhaseIncoming}
	r.pump(ev(abi.EventTagCall, func(e *abi.Event) { e.Call.Update = *ring }))
	n := r.lastNote()
	r.y.AppEvent(nil, r.s, r.c.respond(notify.Response{Action: notify.ActionClick, UserData: n.UserData}))
	r.frame()
	if !r.s.Win.Want[state.WinCall] || !r.s.Req.RaiseMain {
		t.Errorf("window wanted %v, raise %v", r.s.Win.Want[state.WinCall], r.s.Req.RaiseMain)
	}
	if r.a.Open != 0 {
		t.Error("a call's click opened a chat")
	}
}

// With logic's own reducers feeding the queue, a message posts and reading
// the chat takes the notification back.
func TestLogicsQueueEndsOnTheDesktop(t *testing.T) {
	r := newRig(t)
	l := logic.New()
	l.Init(r.s)
	r.s.Settings.NotificationText = true
	r.a.Settings.Notifications = true
	r.s.Focused = false
	r.incomingEvents(ada, ada, "hello from logic", 1)
	if len(r.c.posts) != 1 || r.lastNote().Body != "hello from logic" {
		t.Fatalf("%+v", r.c.posts)
	}
	r.pump(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 2, Last: true, Chats: []abi.Chat{{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: r.now.Unix()}}}
	}))
	if len(r.c.shown) != 0 {
		t.Errorf("%d left after the chat was read", len(r.c.shown))
	}
	if len(r.s.Notices) != 0 || len(r.s.NoticeClears) != 0 {
		t.Error("the queues grew")
	}
}

// NT-003: only the 32 most recent notifications are tracked for taking back
// (zf:src/notify.rs WAITING_LIMIT); the one released stays on the desktop and
// its click still carries its message.
func TestTheOldestWaitingNotificationIsReleasedWithoutBreakingItsClick(t *testing.T) {
	r := newRig(t)
	many := make([]abi.Chat, waitingLimit+1)
	for i := range many {
		many[i] = abi.Chat{ID: []byte("c" + strconv.Itoa(i)), LastActivity: int64(i + 1)}
	}
	r.pump(ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 2, Last: true, Chats: many} }))
	for i := 0; i < waitingLimit+1; i++ {
		r.notice(state.Notice{Account: "1", Chat: []byte("c" + strconv.Itoa(i)), Message: []byte("m"), Title: "t", Body: "b"})
	}
	if got := len(r.y.n.tracked.list); got != waitingLimit {
		t.Errorf("%d tracked", got)
	}
	first := r.c.posts[0]
	if _, chat, msg, ok := decodeTarget(first.UserData); !ok || chat != "c0" || msg != "m" {
		t.Error("the released notification lost what it opens")
	}
	if len(r.c.shown) != waitingLimit+1 {
		t.Errorf("%d on the desktop", len(r.c.shown))
	}
}

// A click on a notification of an account that is not on screen switches to
// that account and opens the message there (AC-008).
func TestAClickOnAHiddenAccountsNoticeSwitchesToIt(t *testing.T) {
	r := newRig(t)
	f2 := fake.New(nil)
	b := r.s.AddAccount("2", nil, f2)
	f2.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(grace), Name: []byte("Second Grace"), Unread: 1, LastActivity: 5}}}
		}))
	r.pump()
	if r.s.Acct() != r.a {
		t.Fatal("the first account is not on screen")
	}
	r.notice(state.Notice{Account: "2", Chat: []byte(grace), Message: []byte("b1"), Title: "Second Grace", Body: "hi",
		Sound: state.SoundChoice{Sound: state.SoundReceive}})
	n := r.lastNote()
	if !r.y.AppEvent(nil, r.s, r.c.respond(notify.Response{Action: notify.ActionClick, UserData: n.UserData})) {
		t.Error("the click was not handled")
	}
	r.frame()
	if r.s.Acct() != b || b.Open == 0 || string(b.Model.Chats.At(b.Open).ID()) != grace {
		t.Errorf("after the click: account %v, open %v", r.s.Acct() == b, b.Open)
	}
}
