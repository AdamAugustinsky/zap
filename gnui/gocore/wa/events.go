// Parts from zapfast-go 3104c46 (internal/backend/backend.go: handle,
// incoming, receipt), MIT. Changed for Zap: the events are abi records,
// emitted as upstream's worker emits them.

package wa

import (
	"log/slog"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/reflect/protoreflect"

	"zapfast/abi"
	"zapfast/gocore/store"
)

// handle applies one whatsmeow event, on the engine's goroutine.
func (e *Engine) handle(evt any) {
	var err error
	switch v := evt.(type) {
	case *events.Connected:
		e.connected()
	case *events.PairSuccess:
		e.qr, e.pairCode, e.pairingPhone = "", "", ""
		e.setStatus(linkTag(abi.LinkStatusTagConnecting))
	case *events.Disconnected:
		if e.linkIs(abi.LinkStatusTagConnected, abi.LinkStatusTagConnecting) {
			e.setStatus(linkDisconnected("The connection to WhatsApp was lost"))
		}
	case *events.StreamReplaced:
		e.setStatus(linkDisconnected("Another WhatsApp client replaced this device's connection"))
	case *events.LoggedOut:
		e.loggedOut()
	case *events.ConnectFailure:
		e.setStatus(linkFailed("WhatsApp refused the connection: " + v.Reason.String()))
	case *events.ClientOutdated:
		e.setStatus(linkFailed("WhatsApp says this client is outdated"))
	case *events.TemporaryBan:
		e.setStatus(linkFailed(v.String()))
	case *events.PushNameSetting:
		e.pushNameSet(v)
	case *events.Picture:
		e.pictureEvent(v)
	case *events.UserAbout:
		e.aboutEvent(v)

	case *events.Message:
		err = e.incoming(v)
	case *events.UndecryptableMessage:
		err = e.undecryptable(v)
	case *events.HistorySync:
		err = e.history(v)
	case *events.Receipt:
		err = e.receipt(v)
	case *events.ChatPresence:
		ev := abi.Event{Tag: abi.EventTagTyping}
		ev.Typing = abi.EventTyping{Chat: b(e.canonical(v.Chat)), Sender: b(e.canonical(v.Sender)), Composing: v.State == types.ChatPresenceComposing}
		e.out.Emit(&ev)
	case *events.Presence:
		ev := abi.Event{Tag: abi.EventTagPresence}
		ev.Presence = abi.EventPresence{ID: b(e.canonical(v.From)), Online: !v.Unavailable}
		if !v.LastSeen.IsZero() {
			ev.Presence.LastSeen, ev.Presence.HasLastSeen = v.LastSeen.Unix(), true
		}
		e.out.Emit(&ev)

	// The phone's chat settings (app state).
	case *events.Pin:
		err = e.updateChat(e.canonical(v.JID), func(c *store.Chat) {
			c.Pinned = v.Action.GetPinned()
			c.PinnedAt = 0
			if c.Pinned {
				c.PinnedAt = v.Timestamp.Unix()
			}
		})
	case *events.Mute:
		err = e.updateChat(e.canonical(v.JID), func(c *store.Chat) {
			switch end := v.Action.GetMuteEndTimestamp(); {
			case !v.Action.GetMuted():
				c.MutedUntil = 0
			case end <= 0:
				c.MutedUntil = -1
			default:
				c.MutedUntil = end / 1000
			}
		})
	case *events.Archive:
		err = e.updateChat(e.canonical(v.JID), func(c *store.Chat) { c.Archived = v.Action.GetArchived() })
	case *events.MarkChatAsRead:
		err = e.updateChat(e.canonical(v.JID), func(c *store.Chat) {
			c.MarkedUnread = !v.Action.GetRead()
			if v.Action.GetRead() {
				c.Unread = 0
			}
		})
	case *events.DeleteForMe:
		chat := e.canonical(v.ChatJID)
		if err = e.arch.DeleteLocal(chat, v.MessageID); err == nil {
			e.messageDeleted(chat, v.MessageID)
		}
	case *events.ClearChat:
		chat := e.canonical(v.JID)
		e.cleared(chat, v.Action.GetMessageRange().GetLastMessageTimestamp())
	case *events.DeleteChat:
		chat := e.canonical(v.JID)
		e.deleted(chat, v.Action.GetMessageRange().GetLastMessageTimestamp())
	case *events.Star:
		err = e.starred(v)
	case *events.LabelEdit:
		err = e.labelEdited(v)
	case *events.LabelAssociationChat:
		err = e.chatLabelled(v)
	case *events.UnarchiveChatsSetting:
		e.unarchiveSetting(v)
	case *events.Contact, *events.PushName:
		e.contactEvent(evt)

	// Groups.
	case *events.GroupInfo:
		err = e.groupChanged(v)
	case *events.JoinedGroup:
		chat := e.canonical(v.JID)
		e.ensureChat(chat, v.Name)
		e.applyGroup(chat, &v.GroupInfo)

	default:
		// Call signaling (calls.go).
		e.callSignal(evt)
	}
	if err != nil {
		slog.Warn("zapfast wa: an update was not stored", "event", eventName(evt), "err", err)
		e.emitError("An update from WhatsApp could not be saved")
	}
}

// eventName is an event's type, for the log: never its content.
func eventName(evt any) string {
	switch evt.(type) {
	case *events.Message:
		return "Message"
	case *events.HistorySync:
		return "HistorySync"
	case *events.Receipt:
		return "Receipt"
	case *events.GroupInfo, *events.JoinedGroup:
		return "group"
	}
	return "app state"
}

// updateChat changes a chat's row and sends it.
func (e *Engine) updateChat(id string, update func(*store.Chat)) error {
	if err := e.arch.UpdateChat(id, func(c *store.Chat) {
		if c.Kind == "" {
			c.Kind = archiveKind(id)
		}
		update(c)
	}); err != nil {
		return err
	}
	e.emitChat(id)
	return nil
}

func (e *Engine) messageDeleted(chat, id string) {
	ev := abi.Event{Tag: abi.EventTagMessageDeleted}
	ev.MessageDeleted = abi.EventMessageDeleted{Chat: b(chat), ID: b(id)}
	e.out.Emit(&ev)
	e.emitChat(chat)
}

// cleared empties a chat through a time (Unix seconds) and says so.
func (e *Engine) cleared(chat string, through int64) bool {
	if through == 0 {
		through = store.Now()
	}
	if err := e.arch.ClearChat(chat, through); err != nil {
		slog.Warn("zapfast wa: clearing a chat", "err", err)
		return false
	}
	ev := abi.Event{Tag: abi.EventTagChatCleared}
	ev.ChatCleared = abi.EventChatCleared{Chat: b(chat), Through: through}
	e.out.Emit(&ev)
	e.emitChat(chat)
	return true
}

// deleted removes a chat through a time and says so.
func (e *Engine) deleted(chat string, through int64) bool {
	if through == 0 {
		through = store.Now()
	}
	if err := e.arch.DeleteChat(chat, through); err != nil {
		slog.Warn("zapfast wa: deleting a chat", "err", err)
		return false
	}
	ev := abi.Event{Tag: abi.EventTagChatRemoved}
	ev.ChatRemoved.Chat = b(chat)
	e.out.Emit(&ev)
	return true
}

// incoming stores a live message (theirs, or ours from another device),
// or applies what it changes: a reaction, a revocation, an edit.
func (e *Engine) incoming(evt *events.Message) error {
	if noChat(evt.Info) {
		return nil
	}
	chat, sender := e.canonical(e.chatOf(evt.Info)), e.canonical(evt.Info.Sender)
	if x := unwrap(evt.Message); x != nil {
		e.timerOf(chat, x, evt.Info)
	}
	if parent, clip, ok := motionClip(unwrap(evt.Message)); ok {
		return e.motionArrived(chat, parent, clip)
	}
	m := parseMessage(evt, chat, sender)
	e.nameQuote(&m)
	switch m.Kind {
	case kindReaction:
		x := unwrap(evt.Message).GetReactionMessage()
		at := x.GetSenderTimestampMS()
		if at <= 0 {
			at = evt.Info.Timestamp.UnixMilli()
		}
		changed, err := e.arch.ApplyReaction(chat, x.GetKey().GetID(), store.Reaction{Sender: sender, Emoji: x.GetText(), Timestamp: at / 1000}, at)
		if noRows(err) {
			return nil
		}
		if changed {
			e.emitMessage(chat, x.GetKey().GetID())
		}
		return err
	case kindPollUpdate:
		return e.pollVote(evt)
	case kindPin:
		return e.pinMessage(evt, chat, sender)
	case kindProtocol:
		// Other protocol messages change nothing shown; a timer change
		// was taken above.
		return nil
	}
	old, existsErr := e.arch.Message(chat, m.ID)
	existed := existsErr == nil
	if m.Deleted {
		if !existed {
			return nil
		}
		if err := e.arch.UpdateMessage(chat, m.ID, revoked); err != nil {
			return err
		}
		e.emitMessage(chat, m.ID)
		e.emitChat(chat)
		return nil
	}
	if m.Edited {
		if !existed {
			return nil
		}
		if err := e.arch.UpdateMessage(chat, m.ID, func(old *store.Message) {
			old.Text, old.Edited, old.Mentions, old.LinkPreview = m.Text, true, m.Mentions, m.LinkPreview
		}); err != nil {
			return err
		}
		e.emitMessage(chat, m.ID)
		e.emitChat(chat)
		return nil
	}
	return e.keep(m, evt.Info, existed, existed && old.Kind == kindWaiting && !e.asked[[2]string{chat, m.ID}])
}

// undecryptable stores a placeholder for a message this device could not
// decrypt, so that it does not vanish (zf:src/backend/worker.rs:4703).
// whatsmeow has asked the sender's phone to send it again; when it comes,
// incoming replaces the row and notifies then, so the placeholder does not.
func (e *Engine) undecryptable(evt *events.UndecryptableMessage) error {
	// The sender marks internal traffic, such as reactions, as hidden.
	if noChat(evt.Info) || evt.DecryptFailMode == events.DecryptFailHide {
		return nil
	}
	chat, sender := e.canonical(e.chatOf(evt.Info)), e.canonical(evt.Info.Sender)
	if _, err := e.arch.Message(chat, evt.Info.ID); !noRows(err) {
		return err // stored already, or the archive failed
	}
	m := store.Message{ID: evt.Info.ID, ChatID: chat, Sender: sender, SenderName: evt.Info.PushName, FromMe: evt.Info.IsFromMe,
		Timestamp: evt.Info.Timestamp.Unix(), Kind: kindWaiting, Status: "delivered"}
	if evt.UnavailableType == events.UnavailableTypeViewOnce {
		// The phone never shares these with linked devices: no use waiting.
		m.Kind = kindViewOnce
	}
	return e.keep(m, evt.Info, false, false)
}

// keep stores a message that is new, or replaces one (existed), and
// announces it. A message replacing a placeholder (waited) is announced as
// it would have been when the placeholder came.
func (e *Engine) keep(m store.Message, info types.MessageInfo, existed, waited bool) error {
	chat := m.ChatID
	if blocked, err := e.arch.Blocked(chat, m.ID, m.Timestamp); err != nil || blocked {
		return err
	}
	if err := e.arch.SaveMessages([]store.Message{m}); err != nil {
		return err
	}
	stored, err := e.arch.Message(chat, m.ID)
	if noRows(err) {
		return nil
	}
	if err != nil {
		return err
	}
	unread := !existed && !m.FromMe
	if err := e.arch.UpdateChat(chat, func(c *store.Chat) {
		if c.Kind == "" {
			c.Kind = archiveKind(chat)
		}
		if c.Name == "" && !info.IsGroup && !m.FromMe {
			c.Name = info.PushName
		}
		if stored.Timestamp >= c.LastActivity {
			c.LastActivity = stored.Timestamp
			c.Preview = summary(stored)
		}
		if !existed && !e.keepArchived {
			// A new message brings the chat back, as WhatsApp does unless
			// chats are kept archived.
			c.Archived = false
		}
		if unread {
			c.Unread++
		} else if m.FromMe && !existed {
			// A reply from the phone reads the chat there.
			c.Unread, c.MarkedUnread = 0, false
		}
	}); err != nil {
		return err
	}
	e.pendingMotion(chat, m.ID)
	if existed {
		e.emitMessage(chat, m.ID)
		e.emitChat(chat)
		if waited && !m.FromMe {
			e.announce(stored)
		}
		return nil
	}
	e.live(stored, unread && m.Kind != kindWaiting)
	if shared(stored) {
		e.sharedChanged(chat)
	}
	return nil
}

// revoked turns a stored message into a revoked one.
func revoked(m *store.Message) {
	m.Deleted = true
	m.Text = deletedText
	m.Kind = kindDeleted
	m.Media = nil
	m.Raw = nil
	m.LinkPreview = nil
}

// receipt records delivery, read and played receipts of our messages.
func (e *Engine) receipt(evt *events.Receipt) error {
	status := 0
	switch evt.Type {
	case types.ReceiptTypeDelivered:
		status = 1
	case types.ReceiptTypeRead:
		status = 2
	case types.ReceiptTypePlayed:
		status = 3
	case types.ReceiptTypeReadSelf:
		return e.readElsewhere(evt)
	default:
		return nil
	}
	chat := e.canonical(evt.Chat)
	sender := chat
	if !evt.Sender.IsEmpty() && evt.IsGroup {
		sender = e.canonical(evt.Sender)
	}
	var at int64
	if !evt.Timestamp.IsZero() {
		at = evt.Timestamp.Unix()
	}
	names := []string{"sent", "delivered", "read", "played"}
	for _, id := range evt.MessageIDs {
		level, known, err := e.arch.ReceiptAt(chat, id, sender, status, at, evt.IsGroup)
		if err != nil {
			return err
		}
		if !known {
			continue
		}
		err = e.arch.UpdateMessage(chat, id, func(m *store.Message) {
			rank := map[string]int{"pending": -1, "failed": -1, "sent": 0, "delivered": 1, "read": 2, "played": 3}
			if m.FromMe && level > rank[m.Status] {
				m.Status = names[level]
			}
		})
		if noRows(err) {
			continue
		}
		if err != nil {
			return err
		}
		e.emitMessage(chat, id)
		if e.watch != nil && string(e.watch.Chat) == chat && string(e.watch.Message) == id {
			e.emitReceipts()
		}
	}
	return nil
}

// readElsewhere is our own read receipt from another device: the chat is
// read through the newest of the messages it names.
func (e *Engine) readElsewhere(evt *events.Receipt) error {
	chat := e.canonical(evt.Chat)
	var newest store.Message
	for _, id := range evt.MessageIDs {
		m, err := e.arch.Message(chat, id)
		if err == nil && (m.Timestamp > newest.Timestamp || newest.ID == "") {
			newest = m
		}
	}
	if newest.ID == "" {
		return nil
	}
	left, err := e.arch.UnreadAfter(chat, newest)
	if err != nil {
		return err
	}
	return e.updateChat(chat, func(c *store.Chat) {
		c.Unread = min(c.Unread, left)
		c.MarkedUnread = false
	})
}

// noChat reports a message that belongs to no chat here: a status update
// (zf:src/backend/worker.rs:4102), or one we sent to a broadcast list,
// which Zap does not show.
func noChat(info types.MessageInfo) bool {
	return info.Chat == types.StatusBroadcastJID || info.Chat.Server == types.BroadcastServer && info.IsFromMe
}

// chatOf is the chat a message belongs to: a broadcast list's message
// goes to its sender's chat, as on the phone.
func (e *Engine) chatOf(info types.MessageInfo) types.JID {
	if info.Chat.Server == types.BroadcastServer {
		return info.Sender
	}
	return info.Chat
}

// timerOf follows a chat's disappearing-message timer: a protocol message
// sets it, as of its own timestamp, and a message carrying an expiration
// sets it only while the chat's is unknown (zf:src/backend/worker.rs:4113).
func (e *Engine) timerOf(chat string, p *waE2E.Message, info types.MessageInfo) {
	at := info.Timestamp.Unix()
	var seconds uint32
	set := false
	if x := p.GetProtocolMessage(); x.GetType() == waE2E.ProtocolMessage_EPHEMERAL_SETTING {
		seconds, set = x.GetEphemeralExpiration(), true
		if t := x.GetEphemeralSettingTimestamp(); t > 0 {
			at = t
		}
	} else if s, ok := expirationOf(p); ok {
		seconds = s
	} else {
		return
	}
	changed := false
	err := e.arch.UpdateChat(chat, func(c *store.Chat) {
		if c.Kind == "" {
			c.Kind = archiveKind(chat)
		}
		if set && at < c.EphemeralAt || !set && (c.EphemeralAt != 0 || c.EphemeralSeconds != 0) {
			return
		}
		changed = c.EphemeralSeconds != seconds
		c.EphemeralSeconds = seconds
		if set {
			c.EphemeralAt = at
		}
	})
	if err != nil {
		slog.Warn("zapfast wa: a chat's timer", "err", err)
		return
	}
	if changed {
		e.emitChat(chat)
	}
}

// expirationOf is the disappearing timer a message's content was sent
// under, when it says.
func expirationOf(p *waE2E.Message) (uint32, bool) {
	var seconds uint32
	found := false
	p.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind || fd.IsList() || fd.IsMap() {
			return true
		}
		inner := v.Message()
		ctx := inner.Descriptor().Fields().ByName("contextInfo")
		if ctx == nil || !inner.Has(ctx) {
			return true
		}
		info, ok := inner.Get(ctx).Message().Interface().(*waE2E.ContextInfo)
		if ok && info.Expiration != nil {
			seconds, found = info.GetExpiration(), true
			return false
		}
		return true
	})
	return seconds, found
}
