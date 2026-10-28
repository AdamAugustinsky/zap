package sys

import (
	"bytes"
	"slices"
	"strings"

	"gonativeui/app/notify"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/logic"
	"zapfast/model"
	"zapfast/state"
)

// Notifications: a message in a chat you are not looking at raises a
// notification through the system's center (app/notify); a click opens the
// chat at the message, a reply sends the text, and reading the chat takes
// the notification back (zf:src/notify.rs, zf:src/app.rs maybe_notify).

const (
	catMessage = "message" // the category of a message: its Reply action
	actReply   = 1         // the Reply action's ID (NT-015)

	// maxHeld is how many notifications wait for the answer to the
	// permission question.
	maxHeld = 8
)

// permission is whether the system lets the app post.
type permission uint8

const (
	permUnknown permission = iota // the status has not arrived yet
	permAsk                       // the user has not been asked
	permAsking                    // the question is up
	permGranted
	permDenied
)

// sent is a notification given to the center and its result not in yet.
type sent struct {
	note       notify.Note
	acct, chat string // chat is the key it waits under
	id         string
	retried    bool
}

// ringing is the incoming call the desktop was told about.
type ringing struct {
	gen  uint64
	acct string
	chat string
	on   bool
	tone bool // the ringtone plays for it
}

type notifications struct {
	on       bool // the center can be used
	perm     permission
	held     []*sent
	tracked  waiting
	inflight map[notify.Ticket]*sent

	// call is the incoming call the desktop was told about.
	call ringing
}

// startNotifications registers the message category and asks the system
// whether the app may post. Without a center (not a bundled app) there are
// no notifications.
func (y *System) startNotifications() {
	n := &y.n
	n.inflight = map[notify.Ticket]*sent{}
	if y.center == nil || !y.center.Available() {
		return
	}
	T := i18n.T
	err := y.center.RegisterCategories(notify.Category{ID: catMessage, Actions: []notify.Action{
		{ID: actReply, Title: T("Reply"), Reply: true, ReplyButton: T("Send"), ReplyPlaceholder: T("Type a message")},
	}})
	if err != nil {
		logWarn("notifications are off", "err", err)
		return
	}
	n.on = true
	if _, err := y.center.Status(); err != nil {
		logWarn("notification status", "err", err)
	}
}

// chatTitle is the chat's name, else its number.
func (y *System) chatTitle(m *model.Store, ch model.Chat) string {
	if n := ch.Name(); len(n) > 0 {
		return string(n)
	}
	return string(m.DisplayName(ch.ID()))
}

// picture is the chat's cached picture, else the sender's: new requests help
// later notifications.
func (y *System) picture(a *state.Account, chat, sender []byte) string {
	for _, id := range [2][]byte{chat, sender} {
		if p, ok, known := a.Model.Avatars.Thumb(id); ok && len(p) > 0 {
			return string(p)
		} else if !known {
			a.FetchAvatar(id, false)
		}
	}
	return ""
}

// nativeSound is what the notification itself plays: the system's sound for
// every sound of the settings but a custom file, which the system cannot
// take from outside the bundle (the app plays it) and none. Pidgin's own
// sounds are GPL-2.0 and not shipped.
func nativeSound(c state.SoundChoice) string {
	switch c.Sound {
	case state.SoundNone, state.SoundCustom:
		return ""
	}
	return notify.SoundDefault
}

// ---- Posting ----

// send gives the notification to the center, or holds it until the system
// has said whether the app may post.
func (y *System) send(n *sent) {
	switch y.n.perm {
	case permDenied:
		return
	case permGranted:
		y.post(n)
		return
	}
	if len(y.n.held) >= maxHeld {
		y.n.held = append(y.n.held[:0], y.n.held[1:]...)
	}
	y.n.held = append(y.n.held, n)
	if y.n.perm == permAsk {
		y.askPermission()
	}
}

func (y *System) askPermission() {
	if _, err := y.center.RequestPermission(notify.Alert | notify.Sound | notify.Badge); err != nil {
		logWarn("notification permission", "err", err)
		return
	}
	y.n.perm = permAsking
}

// post gives n to the center and tracks it for its withdrawal.
func (y *System) post(n *sent) {
	// Permission and attachment callbacks may arrive after the account or
	// privacy state changed. Never trust the content prepared before them.
	a := y.s.AccountByID(n.acct)
	if a == nil || a.Removed || !a.Model.Linked() || a.Model.Link.Status() == abi.LinkStatusTagUnlinked || !a.Settings.Notifications {
		return
	}
	call := strings.HasPrefix(n.chat, "call:")
	// Nor the chat's: a message's chat must still be there, and neither
	// may be locked away (locked while the notification waited for the
	// permission or an attachment retry).
	jid := strings.TrimPrefix(n.chat, "call:")
	ref := a.Model.Chats.Find([]byte(jid))
	if !call && ref == 0 {
		return
	}
	// A locked chat never notifies, its folder open or not
	// (zf:src/app.rs notification_eligible, app/calls.rs
	// call_notification_eligible).
	ch, ok := a.Model.Chats.Get(ref)
	if ok && ch.Locked() {
		return
	}
	if y.s.Lock.Locked || (!call && !y.s.Settings.NotificationText) {
		n.note.Title, n.note.Body = "Zap", i18n.P("notification", "New message")
		n.note.Attachment, n.note.Category = "", ""
		if call {
			n.note.Title, n.note.Body = incomingCallLocked()
		} else {
			n.note.Sound = nativeSound(logic.SoundFor(&y.s.Settings, nil, ok && ch.Group(), false))
		}
	}
	res, err := y.center.Post(n.note)
	if err != nil {
		logWarn("notification not posted", "err", err)
		return
	}
	n.id = res.ID
	key := n.chat
	y.n.tracked.add(n.acct, key, res.ID)
	y.n.inflight[res.Request] = n
}

// result is the answer to a request of the center.
func (y *System) result(r notify.Result) {
	// App events can beat the next frame's drain of withdrawal requests.
	y.clearNotices(y.s)
	n := &y.n
	switch r.Kind {
	case notify.ResultStatus:
		switch r.Status {
		case notify.StatusAuthorized, notify.StatusProvisional, notify.StatusEphemeral:
			y.granted()
		case notify.StatusDenied:
			y.denied()
		default:
			n.perm = permAsk
			if len(n.held) > 0 {
				y.askPermission()
			}
		}
	case notify.ResultPermission:
		if r.Granted {
			y.granted()
		} else {
			y.denied()
		}
	case notify.ResultPost:
		s := n.inflight[r.Ticket]
		delete(n.inflight, r.Ticket)
		if r.ErrCode == 0 || s == nil {
			return
		}
		if r.ErrCode == notify.CodeAttachment && s.note.Attachment != "" && !s.retried {
			// The picture could not be copied: the notification matters more.
			s.retried, s.note.Attachment = true, ""
			y.n.tracked.forget(s.id)
			y.post(s)
			return
		}
		if r.ErrCode != notify.CodeSuperseded {
			logWarn("notification refused", "code", r.ErrCode, "err", r.Err)
		}
		y.n.tracked.forget(s.id)
	}
}

func (y *System) granted() {
	held := y.n.held
	y.n.perm, y.n.held = permGranted, nil
	for _, n := range held {
		y.post(n)
	}
}

func (y *System) denied() {
	y.n.perm, y.n.held = permDenied, nil
}

// ---- Withdrawing ----

func (y *System) withdraw(ids []string) {
	if len(ids) > 0 && y.n.on {
		if err := y.center.Remove(ids...); err != nil {
			logWarn("notifications not withdrawn", "err", err)
		}
	}
}

// withdrawMatching cancels delivery as well as banners. Removing the ticket
// invalidates any late result, so an attachment failure cannot retry it.
func (y *System) withdrawMatching(match func(acct, chat string) bool) {
	n := &y.n
	held := n.held[:0]
	for _, s := range n.held {
		if !match(s.acct, s.chat) {
			held = append(held, s)
		}
	}
	clear(n.held[len(held):])
	n.held = held
	ids := n.tracked.take(func(p *posted) bool { return match(p.acct, p.key) })
	for ticket, s := range n.inflight {
		if match(s.acct, s.chat) {
			// A burst may have released its tracking slot before delivery.
			if !slices.Contains(ids, s.id) {
				ids = append(ids, s.id)
			}
			delete(n.inflight, ticket)
		}
	}
	y.withdraw(ids)
}

// lockedChat takes everything that names a chat that became locked off the
// desktop: its messages' notifications and its ringing call's, whose
// ringtone stops. Upstream neither notifies nor rings for a call in a locked
// chat (zf:src/app/calls.rs:484-490, call_notification_eligible), so one
// locked while it rings goes quiet as if it had come in locked; the call
// itself waits in the strip as "Locked chat" (LK-008). Upstream's
// leave_chat clears only the message key (zf:src/app.rs:2266, notify.rs
// clear), which leaves the caller's name on a ringing call's banner.
func (y *System) lockedChat(acct, chat string) {
	ck := callKey(chat)
	y.withdrawMatching(func(a, c string) bool { return a == acct && (c == chat || c == ck) })
	if c := &y.n.call; c.on && c.acct == acct && c.chat == chat {
		y.endTone()
	}
}

// forgetAccount is an account that was unlinked: its notifications go.
func (y *System) forgetAccount(a *state.Account) {
	y.withdrawMatching(func(acct, _ string) bool { return acct == a.ID })
}

// ---- What logic decided ----

// drain posts the notifications logic queued and takes back the ones it
// asked to; then it empties the queues. Clears come after posts: a chat read
// in the same frame is read.
func (y *System) drain(s *state.State) {
	if len(s.Notices) > 0 {
		for i := range s.Notices {
			y.notice(&s.Notices[i])
		}
		clear(s.Notices)
		s.Notices = s.Notices[:0]
	}
	y.clearNotices(s)
	clear(s.NoticeClears)
	s.NoticeClears = s.NoticeClears[:0]
	// Upstream asks for attention for a message on Linux only
	// (zf:src/app.rs:8513-8522, cfg!(target_os = "linux")): on macOS the
	// notification is the signal and the Dock does not bounce.
	s.Attention = false
}

func (y *System) clearNotices(s *state.State) {
	if len(s.NoticeClears) > 0 {
		for i := range s.NoticeClears {
			c := &s.NoticeClears[i]
			switch {
			case c.All:
				y.withdrawMatching(func(_, _ string) bool { return true })
			case c.Locked:
				y.lockedChat(c.Account, string(c.Chat))
			default:
				y.withdrawMatching(func(acct, chat string) bool { return acct == c.Account && chat == string(c.Chat) })
			}
		}
	}
}

// notice gives the desktop a message notification logic decided on. What it
// says and which sound it asks for are logic's; this is how the system shows
// it.
func (y *System) notice(c *state.Notice) {
	if !y.n.on {
		return
	}
	chat := string(c.Chat)
	note := notify.Note{
		Thread:   c.Account + "/" + chat,
		Title:    c.Title,
		Body:     c.Body,
		UserData: encodeTarget(c.Account, chat, string(c.Message)),
		Sound:    nativeSound(c.Sound),
	}
	if !c.Hidden {
		// A hidden one carries nothing of the message, not even Reply.
		note.Category = catMessage
		note.Attachment = string(c.Picture)
	}
	if c.Sound.Sound == state.SoundCustom {
		y.play(string(c.Sound.Path))
	}
	y.send(&sent{note: note, acct: c.Account, chat: chat})
}

// ---- A call rings ----

// call follows a call: an incoming one that rings notifies, and its
// notification goes when it stops ringing (zf:src/app/calls.rs
// notify_incoming_call).
func (y *System) call(a *state.Account, u *abi.CallUpdate) {
	c := &y.n.call
	rings := u.Direction == abi.CallDirectionIncoming && u.Phase == abi.CallPhaseIncoming
	if !rings {
		if c.on && c.gen == u.Generation && c.acct == a.ID {
			y.withdrawMatching(func(acct, chat string) bool { return acct == c.acct && chat == callKey(c.chat) })
			y.endTone()
			*c = ringing{}
		}
		return
	}
	if c.on && c.gen == u.Generation && c.acct == a.ID {
		return
	}
	y.notifyCall(a, u)
}

func (y *System) notifyCall(a *state.Account, u *abi.CallUpdate) {
	if !a.Settings.Notifications {
		return
	}
	m := a.Model
	link := u.HasGroup && u.Group.Link
	var ch model.Chat
	haveChat := false
	var own *abi.NotificationSound
	if ref := m.Chats.Find(u.Chat); ref != 0 && !link {
		ch, haveChat = m.Chats.At(ref), true
		// The chat's own rules: a call in a chat that is muted, archived or
		// locked stays as quiet as a message in it. A caller without a chat
		// row is a stranger, and rings with the ordinary sound.
		if !callEligible(ch.Archived(), ch.Locked(), ch.Muted(y.s.Now().Unix())) {
			return
		}
		var sc abi.Chat
		if ch.Decode(&sc) == nil && sc.HasNotificationSound {
			own = &sc.NotificationSound
		}
	}
	chat := string(u.Chat)
	c := &y.n.call
	c.gen, c.acct, c.chat, c.on = u.Generation, a.ID, chat, true
	// A call rings with the ringtone whenever its notification would make a
	// sound at all, whichever sound that is; with sounds off it only shows.
	if logic.SoundFor(&y.s.Settings, own, false, false).Sound != state.SoundNone {
		y.startTone()
	}
	if !y.n.on {
		return
	}
	note := notify.Note{Thread: a.ID + "/" + callKey(chat), UserData: encodeTarget(a.ID, chat, "")}
	if y.s.Lock.Locked {
		// Behind the app lock the desktop learns that a call came, and
		// nothing about who is calling.
		note.Title, note.Body = incomingCallLocked()
	} else {
		caller := ""
		if u.HasGroup && u.Group.HasCaller {
			caller = y.callerName(m, u.Group.Caller)
		}
		switch {
		case link:
			note.Title = i18n.T("Call link")
		case haveChat:
			note.Title = y.chatTitle(m, ch)
		default:
			note.Title = string(m.DisplayName(u.Chat))
		}
		note.Body = incomingText(u, caller)
		if haveChat {
			note.Attachment = y.picture(a, u.Chat, nil)
		}
	}
	// The ringtone is ours; the notification itself stays quiet so the two
	// never play over each other.
	y.send(&sent{note: note, acct: a.ID, chat: callKey(chat)})
}

// startTone plays the ringtone for the call that rings.
func (y *System) startTone() {
	if y.tone == nil || y.n.call.tone {
		return
	}
	if err := y.tone.Start(); err != nil {
		logWarn("the ringtone is silent", "err", err)
		return
	}
	y.n.call.tone = true
}

// endTone ends the ringtone, if it plays.
func (y *System) endTone() {
	if y.tone != nil && y.n.call.tone {
		y.tone.Stop()
	}
	y.n.call.tone = false
}

func (y *System) callerName(m *model.Store, jid []byte) string {
	return string(m.DisplayName(jid))
}

// ---- What a response says ----

// encodeTarget is what a note carries back with a response: the account, the
// chat and the message it announced (none for a call).
func encodeTarget(acct, chat, msg string) []byte {
	b := make([]byte, 0, len(acct)+len(chat)+len(msg)+2)
	b = append(b, acct...)
	b = append(b, 0)
	b = append(b, chat...)
	b = append(b, 0)
	return append(b, msg...)
}

func decodeTarget(b []byte) (acct, chat, msg string, ok bool) {
	a, rest, ok := bytes.Cut(b, []byte{0})
	if !ok {
		return
	}
	c, m, ok := bytes.Cut(rest, []byte{0})
	if !ok {
		return
	}
	return string(a), string(c), string(m), true
}

// respond is the user's response to a note: a click opens the chat at the
// message (or the call), the Reply action sends the text.
func (y *System) respond(r *notify.Response) {
	acct, chat, msg, ok := decodeTarget(r.UserData)
	if !ok {
		return
	}
	switch r.Action {
	case notify.ActionClick:
		if y.s.Lock.Locked && y.DeferOpen != nil && y.DeferOpen(y.s, acct, chat, []byte(msg)) {
			y.showMain() // the lock screen; the unlock opens the chat
			return
		}
		y.queue(target{kind: tOpen, acct: acct, chat: chat, msg: msg})
		y.showMain()
	case actReply:
		if text := strings.TrimSpace(string(r.Reply)); text != "" {
			y.queue(target{acct: acct, chat: chat, kind: tReply, text: text})
		}
	}
}
