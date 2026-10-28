package sys

import (
	"slices"
	"strings"
	"testing"

	"gonativeui/app/notify"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

func TestWithdrawalCancelsPendingNotifications(t *testing.T) {
	for _, scope := range []struct {
		name  string
		clear func(*rig)
		want  []string
	}{
		{"chat read", func(r *rig) { r.read(ada) }, []string{"group", "other account", "Incoming voice call"}},
		{"queued chat read", func(r *rig) {
			r.s.NoticeClears = append(r.s.NoticeClears, state.NoticeClear{Account: "1", Chat: []byte(ada)})
		}, []string{"group", "other account", "Incoming voice call"}},
		{"all", func(r *rig) {
			r.s.NoticeClears = append(r.s.NoticeClears, state.NoticeClear{All: true})
			r.frame()
		}, nil},
		{"lock", func(r *rig) { r.s.Lock.Locked = true; r.s.Locked(); r.frame() }, nil},
		{"queued lock", func(r *rig) { r.s.Lock.Locked = true; r.s.Locked() }, nil},
		{"unlink", func(r *rig) {
			r.pump(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagLoggedOut }))
		}, []string{"other account"}},
		{"unlinked", func(r *rig) {
			r.pump(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagUnlinked }))
		}, []string{"other account"}},
		{"account removed", func(r *rig) { r.pump(&abi.Event{Tag: abi.EventTagAccountRemoved}) }, []string{"other account"}},
		{"call end", func(r *rig) {
			r.pump(ev(abi.EventTagCall, func(e *abi.Event) {
				e.Call.Update = abi.CallUpdate{Generation: 7, Chat: []byte(ada), Direction: abi.CallDirectionIncoming, Phase: abi.CallPhaseConnecting}
			}))
		}, []string{"message", "group", "other account"}},
	} {
		for _, pending := range []string{"permission", "attachment retry"} {
			t.Run(scope.name+"/"+pending, func(t *testing.T) {
				r := newRig(t)
				f2 := fake.New(nil)
				r.s.AddAccount("2", nil, f2)
				f2.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
					ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: chats()} }))
				r.pump(ev(abi.EventTagAvatar, func(e *abi.Event) {
					e.Avatar = abi.EventAvatar{ID: []byte(ada), Path: []byte("/fixture/avatar.jpg"), HasPath: true}
				}))
				if pending == "permission" {
					r.y.n.perm = permAsk
				}
				for _, n := range []state.Notice{
					{Account: "1", Chat: []byte(ada), Body: "message"},
					{Account: "1", Chat: []byte(group), Body: "group"},
					{Account: "2", Chat: []byte(ada), Body: "other account"},
				} {
					n.Title, n.Picture = "Private name", []byte("/fixture/avatar.jpg")
					r.notice(n)
				}
				r.pump(ev(abi.EventTagCall, func(e *abi.Event) {
					e.Call.Update = abi.CallUpdate{Generation: 7, Chat: []byte(ada), Direction: abi.CallDirectionIncoming, Phase: abi.CallPhaseIncoming}
				}))
				before := len(r.c.posts)
				if pending == "permission" && (before != 0 || r.c.asked != 1) {
					t.Fatal("notifications did not wait on one permission prompt")
				}
				if pending == "attachment retry" && before != 4 {
					t.Fatalf("posted %d, want four", before)
				}
				scope.clear(r)
				if pending == "attachment retry" && scope.name != "queued chat read" && scope.name != "queued lock" {
					if len(r.c.shown) != len(scope.want) {
						t.Fatalf("withdrawal left %d banners, want %d", len(r.c.shown), len(scope.want))
					}
				}
				if pending == "permission" {
					r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPermission, Granted: true}))
				} else {
					for ticket := notify.Ticket(1); ticket <= 4; ticket++ {
						r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPost, Ticket: ticket, ErrCode: notify.CodeAttachment}))
					}
				}
				var got []string
				for _, note := range r.c.posts[before:] {
					got = append(got, note.Body)
				}
				if !slices.Equal(got, scope.want) {
					t.Fatalf("late posts = %q, want %q", got, scope.want)
				}
			})
		}
	}
}

func TestWithdrawalCancelsInflightNotesBeyondTheTrackingLimit(t *testing.T) {
	r := newRig(t)
	for i := 0; i <= waitingLimit; i++ {
		r.notice(state.Notice{Account: "1", Chat: []byte(ada), Body: "old", Picture: []byte("/fixture/avatar.jpg")})
	}
	r.read(ada)
	if len(r.c.shown) != 0 {
		t.Fatalf("%d pending notes survived withdrawal", len(r.c.shown))
	}
	// A new note for the same chat remains eligible, but old callbacks cannot
	// revive the notes cancelled before it.
	r.notice(state.Notice{Account: "1", Chat: []byte(ada), Body: "new"})
	before := len(r.c.posts)
	for ticket := notify.Ticket(1); ticket <= waitingLimit+1; ticket++ {
		r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPost, Ticket: ticket, ErrCode: notify.CodeAttachment}))
	}
	if len(r.c.posts) != before || len(r.c.shown) != 1 || r.lastNote().Body != "new" {
		t.Fatal("late callbacks revived old notes or cancelled the new note")
	}
}

// Deliberately omit withdrawal: the last posting boundary must still keep
// stale private content off the desktop, including after a retry or prompt.
func TestPostingRechecksPrivacy(t *testing.T) {
	for _, change := range []struct {
		name  string
		apply func(*rig)
		drop  bool
	}{
		{"lock", func(r *rig) { r.s.Lock.Locked = true }, false},
		{"content hidden", func(r *rig) { r.s.Settings.NotificationText = false }, false},
		{"notifications disabled", func(r *rig) { r.a.Settings.Notifications = false }, true},
		{"unlinked", func(r *rig) {
			r.a.Model.Event(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagUnlinked }))
		}, true},
		{"account removed", func(r *rig) { r.a.Removed = true }, true},
		{"account absent", func(r *rig) { r.s.Accounts = nil }, true},
		// Locked from the phone while it waited; the model alone, so that
		// only the posting boundary stands between it and the desktop.
		{"chat locked", func(r *rig) {
			r.a.Model.Event(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
				e.ChatUpdated.Chat = abi.Chat{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: 40, Locked: true}
			}))
		}, true},
		{"chat locked, folder open", func(r *rig) {
			r.a.Mode = state.ListLocked
			r.a.Model.Event(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
				e.ChatUpdated.Chat = abi.Chat{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: 40, Locked: true}
			}))
		}, true},
	} {
		for _, kind := range []string{"message", "call"} {
			for _, pending := range []string{"direct", "permission", "attachment retry"} {
				t.Run(change.name+"/"+kind+"/"+pending, func(t *testing.T) {
					r := newRig(t)
					r.s.Settings.MessageSound = state.SoundChoice{Sound: state.SoundNone}
					key := ada
					if kind == "call" {
						key = callKey(ada)
					}
					n := &sent{acct: "1", chat: key, note: notify.Note{
						Title: "Private name", Body: "Private body", Attachment: "/fixture/avatar.jpg", Category: catMessage,
						UserData: encodeTarget("1", ada, "m1"),
					}}
					if kind == "message" {
						n.note.Sound = notify.SoundDefault // a stale chat/mention sound
					}
					if pending == "permission" {
						r.y.n.perm = permAsk
					}
					if pending != "direct" {
						r.y.send(n)
					}
					before := len(r.c.posts)
					change.apply(r)
					switch pending {
					case "direct":
						r.y.send(n)
					case "permission":
						r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPermission, Granted: true}))
					case "attachment retry":
						r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPost, Ticket: 1, ErrCode: notify.CodeAttachment}))
					}
					if change.drop {
						if len(r.c.posts) != before {
							t.Fatal("posted for an unavailable account")
						}
						return
					}
					if len(r.c.posts) != before+1 {
						t.Fatal("missing generic notification")
					}
					if kind == "call" && change.name == "content hidden" {
						// Upstream's content setting is for messages; an unlocked
						// incoming call still names its caller (app/calls.rs).
						got := r.lastNote()
						if got.Title != "Private name" || got.Body != "Private body" {
							t.Fatal("message-content setting changed the unlocked call")
						}
						return
					}
					wantBody := "New message"
					if kind == "call" {
						wantBody = "Incoming call"
					}
					got := r.lastNote()
					if got.Title != "Zap" || got.Body != wantBody || got.Attachment != "" || got.Category != "" || got.Sound != "" {
						t.Fatalf("private content reached the center: %+v", got)
					}
					if acct, chat, msg, ok := decodeTarget(got.UserData); !ok || acct != "1" || chat != ada || msg != "m1" {
						t.Fatal("generic notification lost its click target")
					}
				})
			}
		}
	}
}

// A chat locked from the phone takes its notifications off the desktop
// (zf:src/app.rs leave_chat): one held for the permission is not posted
// when it is granted, a shown banner is withdrawn, and an attachment retry
// does not bring it back. The other chats' stay.
func TestAChatLockedFromThePhoneWithdrawsItsNotifications(t *testing.T) {
	for _, pending := range []string{"permission", "shown", "attachment retry"} {
		t.Run(pending, func(t *testing.T) {
			r := newRig(t)
			if pending == "permission" {
				r.y.n.perm = permAsk
			}
			for _, n := range []state.Notice{
				{Account: "1", Chat: []byte(ada), Body: "private body"},
				{Account: "1", Chat: []byte(group), Body: "group"},
			} {
				n.Title, n.Picture = "Private name", []byte("/fixture/avatar.jpg")
				r.notice(n)
			}
			before := len(r.c.posts)
			r.pump(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
				e.ChatUpdated.Chat = abi.Chat{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: 40, Locked: true, Unread: 1}
			}))
			switch pending {
			case "permission":
				r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPermission, Granted: true}))
			case "attachment retry":
				for ticket := notify.Ticket(1); ticket <= 2; ticket++ {
					r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPost, Ticket: ticket, ErrCode: notify.CodeAttachment}))
				}
			}
			for _, note := range r.c.posts[before:] {
				if note.Body == "private body" || note.Title == "Private name" && note.Body != "group" {
					t.Errorf("the locked chat's notification reached the desktop: %+v", note)
				}
			}
			for _, note := range r.c.shown {
				if note.Body == "private body" {
					t.Error("the locked chat's banner stays on the desktop")
				}
			}
			if pending != "shown" {
				found := false
				for _, note := range r.c.shown {
					found = found || note.Body == "group"
				}
				if !found {
					t.Error("another chat's notification went too")
				}
			}
		})
	}
}

// A locked chat never notifies, its folder open or not (zf:src/app.rs
// notification_eligible): a chat locked from the phone while the Locked
// folder is open takes its banner off the desktop then, not when the folder
// closes; and nothing of it posts while the folder is open.
func TestAChatLockedWhileTheFolderIsOpenWithdrawsItsNotifications(t *testing.T) {
	r := newRig(t)
	r.notice(state.Notice{Account: "1", Chat: []byte(ada), Title: "Private name", Body: "private body"})
	if len(r.c.shown) != 1 {
		t.Fatalf("%d shown", len(r.c.shown))
	}
	r.a.Mode = state.ListLocked // browsing another locked chat
	r.pump(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: 40, Locked: true, Unread: 1}
	}))
	if len(r.c.shown) != 0 {
		t.Error("the banner of a chat locked with the folder open stays")
	}
	before := len(r.c.posts)
	r.notice(state.Notice{Account: "1", Chat: []byte(ada), Title: "Private name", Body: "later"})
	if len(r.c.posts) != before {
		t.Error("a locked chat notified while its folder was open")
	}
	r.a.CloseLockedFolder()
	r.frame()
	if len(r.c.shown) != 0 {
		t.Error("a banner came back when the folder closed")
	}
}

// A ringing call whose chat is locked from the phone goes quiet: its banner,
// which names the caller, is withdrawn and the ringtone stops, as a call in a
// locked chat never rings (zf:src/app/calls.rs:484-490). Reading the chat
// leaves the call alone.
func TestARingingCallWhoseChatIsLockedGoesQuiet(t *testing.T) {
	r := newRig(t)
	r.ring(ada, 7)
	calls := func() int {
		n := 0
		for _, note := range r.c.shown {
			if note.Body != "" && note.Title != "" && strings.Contains(note.Thread, "call:") {
				n++
			}
		}
		return n
	}
	if calls() != 1 || !r.tone.on {
		t.Fatalf("the call does not ring: %d banners, tone %v", calls(), r.tone.on)
	}
	r.read(ada)
	if calls() != 1 || !r.tone.on {
		t.Fatal("reading the chat took the call away")
	}
	r.pump(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: 40, Locked: true}
	}))
	if calls() != 0 {
		t.Error("the ringing call's banner still names the locked chat's caller")
	}
	if r.tone.on {
		t.Error("the ringtone plays on for a locked chat")
	}
	r.ring(ada, 7) // the same ring, updated
	if calls() != 0 || r.tone.on {
		t.Error("the locked chat's call rang again")
	}
}

// Covers both phone event forms and every delivery stage, including a
// completed banner (no inflight ticket). The folder is open on another
// chat; closing it later must not revive anything that became private.
func TestPrivacyWithdrawalIncludesCallsInEveryDeliveryStage(t *testing.T) {
	for _, event := range []string{"update", "snapshot"} {
		for _, stage := range []string{"held", "inflight", "retry", "shown"} {
			t.Run(event+"/"+stage, func(t *testing.T) {
				r := newRig(t)
				r.a.Mode = state.ListLocked
				r.pump(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
					e.ChatUpdated.Chat = abi.Chat{ID: []byte(grace), Name: []byte("Grace Hopper"), LastActivity: 20, Locked: true}
				}))
				r.a.OpenChat(r.a.Model.Chats.Find([]byte(grace)))
				if stage == "held" {
					r.y.n.perm = permAsk
				}
				for _, key := range []string{ada, callKey(ada), group, callKey(group)} {
					r.y.send(&sent{acct: "1", chat: key, note: notify.Note{Title: "Private name", Body: key, Attachment: "/fixture/avatar.jpg"}})
				}
				if stage == "shown" {
					for ticket := notify.Ticket(1); ticket <= 4; ticket++ {
						r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPost, Ticket: ticket}))
					}
				}
				c := abi.Chat{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: 40, Locked: true, Unread: 1}
				if event == "update" {
					r.pump(ev(abi.EventTagChatUpdated, func(e *abi.Event) { e.ChatUpdated.Chat = c }))
				} else {
					cs := chats()
					cs[0] = c
					cs[1].Locked = true
					r.pump(ev(abi.EventTagChats, func(e *abi.Event) {
						e.Chats = abi.EventChats{Batch: 2, Last: true, Chats: cs}
					}))
				}
				if stage != "held" && len(r.c.shown) != 2 {
					t.Fatalf("lock left %d banners, want only unrelated chat and call", len(r.c.shown))
				}
				for _, pending := range r.y.n.held {
					if pending.chat == ada || pending.chat == callKey(ada) {
						t.Fatal("private notification still held for permission")
					}
				}
				for _, pending := range r.y.n.inflight {
					if pending.chat == ada || pending.chat == callKey(ada) {
						t.Fatal("private notification still eligible for a retry callback")
					}
				}
				before := len(r.c.posts)
				if stage == "held" {
					r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPermission, Granted: true}))
				} else {
					var code int64
					if stage == "retry" {
						code = notify.CodeAttachment
					}
					for ticket := notify.Ticket(1); ticket <= 4; ticket++ {
						if stage == "retry" {
							// A failed native post did not deliver its banner.
							if n := r.y.n.inflight[ticket]; n != nil {
								delete(r.c.shown, n.id)
							}
						}
						r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPost, Ticket: ticket, ErrCode: code}))
					}
				}
				r.a.CloseLockedFolder()
				r.frame()
				for _, note := range r.c.posts[before:] {
					if note.Body == ada || note.Body == callKey(ada) {
						t.Fatal("cancelled message or call was posted again")
					}
				}
				var remaining []string
				for _, note := range r.c.shown {
					remaining = append(remaining, note.Body)
				}
				slices.Sort(remaining)
				if !slices.Equal(remaining, []string{callKey(group), group}) {
					t.Fatalf("remaining banners %v, want unrelated message and call", remaining)
				}
			})
		}
	}
}

func TestLockingAnotherChatPreservesTheRingingCall(t *testing.T) {
	for _, jid := range []string{group, "404@s.whatsapp.net"} {
		t.Run(jid, func(t *testing.T) {
			r := newRig(t)
			r.ring(ada, 7)
			r.pump(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
				e.ChatUpdated.Chat = abi.Chat{ID: []byte(jid), Name: []byte("Fixture chat"), Locked: true, Unread: 1}
			}))
			if !r.tone.on || len(r.c.shown) != 1 {
				t.Fatal("locking an unrelated chat stopped the call")
			}
		})
	}
}

// A stranger can ring before a chat row exists. Its first locked row must
// retire that call just as a known chat's unlocked-to-locked transition does.
func TestUnknownCallerFirstAppearsLocked(t *testing.T) {
	const unknown = "404@s.whatsapp.net"
	for _, event := range []string{"update", "snapshot"} {
		for _, stage := range []string{"held", "inflight", "shown"} {
			t.Run(event+"/"+stage, func(t *testing.T) {
				r := newRig(t)
				f2 := fake.New(nil)
				r.s.AddAccount("2", nil, f2)
				f2.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }))
				r.pump()
				if stage == "held" {
					r.y.n.perm = permAsk
					// A message queued before its row also waits for permission.
					r.notice(state.Notice{Account: "1", Chat: []byte(unknown), Body: "private message"})
				}
				r.ring(unknown, 9)
				if !r.tone.on || r.a.Model.Chats.Find([]byte(unknown)) != 0 {
					t.Fatal("fixture did not ring before its chat existed")
				}
				for _, n := range []sent{
					{acct: "1", chat: callKey(ada), note: notify.Note{Body: "other caller"}},
					{acct: "2", chat: callKey(unknown), note: notify.Note{Body: "other account"}},
				} {
					r.y.send(&n)
				}
				if stage == "shown" {
					for ticket := notify.Ticket(1); ticket <= notify.Ticket(r.c.n); ticket++ {
						r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPost, Ticket: ticket}))
					}
				}
				c := abi.Chat{ID: []byte(unknown), Name: []byte("Fixture caller"), Locked: true, Unread: 1}
				if event == "update" {
					r.pump(ev(abi.EventTagChatUpdated, func(e *abi.Event) { e.ChatUpdated.Chat = c }))
				} else {
					r.pump(ev(abi.EventTagChats, func(e *abi.Event) {
						e.Chats = abi.EventChats{Batch: 2, Last: true, Chats: append(chats(), c)}
					}))
				}
				if r.tone.on {
					t.Error("first-seen locked caller's ringtone survives")
				}
				for _, n := range r.y.n.held {
					if n.acct == "1" && (n.chat == unknown || n.chat == callKey(unknown)) {
						t.Error("first-seen locked caller's notification is still held")
					}
				}
				for _, n := range r.y.n.inflight {
					if n.acct == "1" && n.chat == callKey(unknown) {
						t.Error("first-seen locked caller still has an inflight ticket")
					}
				}
				if stage == "held" {
					r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPermission, Granted: true}))
				} else {
					r.y.appEvent(r.c.result(notify.Result{Kind: notify.ResultPost, Ticket: 1, ErrCode: notify.CodeAttachment}))
				}
				var bodies []string
				for _, n := range r.c.shown {
					bodies = append(bodies, n.Body)
				}
				slices.Sort(bodies)
				if !slices.Equal(bodies, []string{"other account", "other caller"}) {
					t.Fatalf("remaining banners %v, want only unrelated callers", bodies)
				}
			})
		}
	}
}
