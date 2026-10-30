package logic

import (
	"bytes"
	"time"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/mentions"
	"zapfast/model"
	"zapfast/state"
)

// The notification policy (zf:src/app.rs:2011-2147, 9717-9781,
// zf:src/notify.rs:51-196, 395-412): whether an incoming message notifies,
// and with what. logic appends its decisions to State.Notices; package sys
// posts them.

// ThrottleWindow is how long a limited chat stays quiet after it notified
// (zf:src/notify.rs:51).
const ThrottleWindow = 10 * time.Minute

type throttleKey struct{ account, chat string }

// Eligible reports whether a message of the chat may notify at all: the
// chat is not archived, muted or locked and has unread messages, and the
// message is at most a minute old (zf:src/app.rs:9776).
func Eligible(c model.Chat, now, messageAt int64) bool {
	if c.Archived() || c.Unread() == 0 || c.Muted(now) || c.Locked() {
		return false
	}
	return now-messageAt <= 60
}

// SoundFor is the sound a notification makes: the chat's own, else the
// mention sound for a message to us, else nothing for a group without
// group sounds, else the message sound (zf:src/app.rs:9717).
func SoundFor(st *state.Settings, chatSound *abi.NotificationSound, group, forUs bool) state.SoundChoice {
	switch {
	case chatSound != nil:
		return state.SoundChoice{Sound: state.Sound(chatSound.Tag), Path: chatSound.Custom.Path}
	case forUs:
		return st.MentionSound
	case group && !st.GroupSounds:
		return state.SoundChoice{Sound: state.SoundNone}
	}
	return st.MessageSound
}

// AddressesUs reports whether a message mentions us or replies to one of
// ours (zf:src/app.rs:2132).
func AddressesUs(me *model.Me, m *abi.Message) bool {
	ours := [2][]byte{me.ID(), me.LID()}
	list := ours[:0]
	for _, id := range ours {
		if len(id) > 0 {
			list = append(list, id)
		}
	}
	for i := range m.Mentions {
		if mentions.NamesUs(&m.Mentions[i], list) {
			return true
		}
	}
	if m.HasQuoted {
		for _, id := range list {
			if bytes.Equal(m.Quoted.Sender, id) {
				return true
			}
		}
	}
	return false
}

// Lines are a notification's title and body: the chat, and for a group the
// sender before the summary (zf:src/notify.rs:405).
func Lines(chatName string, group bool, sender, summary string) (title, body string) {
	if group {
		return chatName, sender + ": " + summary
	}
	return chatName, summary
}

// allow is the throttle: a limited chat notifies once, then waits.
func (l *Logic) allow(a *state.Account, chat []byte, now time.Time, limited bool) bool {
	if !limited {
		return true
	}
	k := throttleKey{a.ID, string(chat)}
	if last, ok := l.throttle[k]; ok && now.Sub(last) < ThrottleWindow {
		return false
	}
	l.throttle[k] = now
	return true
}

// clearNotices takes a read chat's notifications away and lets its next
// message notify at once.
func (l *Logic) clearNotices(a *state.Account, chat []byte) {
	k := throttleKey{a.ID, string(chat)}
	delete(l.throttle, k)
	if !l.posted[k] {
		return
	}
	delete(l.posted, k)
	l.s.NoticeClears = append(l.s.NoticeClears, state.NoticeClear{Account: a.ID, Chat: append([]byte(nil), chat...)})
}

// maybeNotify decides about an incoming message (zf:src/app.rs:2011).
func (l *Logic) maybeNotify(a *state.Account, chatID []byte, m *abi.Message) {
	s := l.s
	if !a.Settings.Notifications || m.FromMe {
		return
	}
	ref := a.Model.Chats.Find(chatID)
	c, ok := a.Model.Chats.Get(ref)
	if !ok {
		return
	}
	now := s.Now()
	if !Eligible(c, now.Unix(), m.Timestamp) {
		return
	}
	if reading(s, a) && (a.Open == ref || a.InSplit(ref)) || pipReading(s, a, ref) {
		return
	}
	group := c.Group()
	forUs := group && AddressesUs(&a.Model.Me, m)
	limited := !forUs && a.Settings.LimitNotifications.Applies(group)
	if !l.allow(a, chatID, now, limited) {
		return
	}
	n := state.Notice{Account: a.ID, Chat: append([]byte(nil), chatID...), Message: append([]byte(nil), m.ID...), Limited: limited}
	if s.Lock.Locked || !s.Settings.NotificationText {
		// Nothing the lock hides: "New message" from ZapFast, the message
		// sound only (zf:src/app.rs:2085-2124).
		n.Hidden = true
		n.Title, n.Body = "Zap", i18n.P("notification", "New message")
		n.Sound = SoundFor(&s.Settings, nil, group, false)
	} else {
		var chatSound *abi.NotificationSound
		if c.Decode(&l.chat) == nil && l.chat.HasNotificationSound {
			chatSound = &l.chat.NotificationSound
		}
		// Names the way the chat list writes them (zf:src/app.rs:2486,
		// 2547): the address book first, a profile name after a "~".
		name := string(c.Name())
		if !group && !bytes.Equal(chatID, a.Model.Me.ID()) || name == "" {
			name = string(a.Model.DisplayName(chatID))
		}
		var hint []byte // an absent optional keeps the last decode's bytes
		if m.HasSenderName {
			hint = m.SenderName
		}
		sender := string(a.Model.PersonName(m.Sender, hint))
		summary := string(c.Preview())
		if summary == "" {
			summary = string(m.Content.Text.Text)
		}
		n.Title, n.Body = Lines(name, group, sender, summary)
		n.Sound = SoundFor(&s.Settings, chatSound, group, forUs)
		for _, jid := range [2][]byte{chatID, m.Sender} {
			if p, ok, _ := a.Model.Avatars.Thumb(jid); ok {
				n.Picture = append([]byte(nil), p...)
				break
			}
		}
		a.FetchAvatar(chatID, false)
	}
	s.Notices = append(s.Notices, n)
	s.Attention = true
	l.posted[throttleKey{a.ID, string(chatID)}] = true
	s.Touch()
}
