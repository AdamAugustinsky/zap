// The history sync is from zapfast-go 3104c46 (internal/backend/backend.go
// history), MIT. Changed for Zap: Syncing and SyncProgress, and on-demand
// chunks answer FetchOlder as upstream's worker does (answer_older).

package wa

import (
	"context"
	"log/slog"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/store"
)

const (
	// phoneBatch is how many older messages one request asks the phone for
	// (upstream's PHONE_BATCH).
	phoneBatch = 50
	// olderShown is how many archived messages an answered request shows
	// at most.
	olderShown = 500
)

// olderRequest is a history request waiting for the phone's answer.
type olderRequest struct {
	before   string // the oldest archived message when asked; "" for none
	explicit bool
	serial   uint64
	timer    *time.Timer
}

// setSyncing reports a change of the initial sync.
func (e *Engine) setSyncing(syncing bool) {
	if e.syncing == syncing {
		return
	}
	e.syncing = syncing
	if !syncing && e.syncTimer != nil {
		e.syncTimer.Stop()
		e.syncTimer = nil
	}
	ev := abi.Event{Tag: abi.EventTagSyncing}
	ev.Syncing.Syncing = syncing
	e.out.Emit(&ev)
}

// history files one history chunk into the archive.
func (e *Engine) history(evt *events.HistorySync) error {
	h := evt.Data
	onDemand := h.GetSyncType() == waHistorySync.HistorySync_ON_DEMAND
	if !onDemand {
		e.setSyncing(true)
		if e.syncTimer != nil {
			e.syncTimer.Stop()
		}
		e.syncTimer = time.AfterFunc(e.syncQuiet, func() { e.post(func() { e.setSyncing(false) }) })
		if h.Progress != nil {
			ev := abi.Event{Tag: abi.EventTagSyncProgress}
			ev.SyncProgress.Percent = min(h.GetProgress(), 100)
			e.out.Emit(&ev)
		}
	}
	type filed struct {
		chat  string
		count int
		more  bool
	}
	var answered []filed
	for _, conv := range h.GetConversations() {
		if e.ctx.Err() != nil {
			return e.ctx.Err()
		}
		j, ok := jidOf(conv.GetID())
		if !ok {
			continue
		}
		id := e.canonical(j)
		if conv.GetPnJID() != "" && conv.GetLidJID() != "" {
			if err := e.arch.Canonicalize(conv.GetLidJID(), conv.GetPnJID()); err != nil {
				return err
			}
			id = conv.GetPnJID()
		}
		if err := e.arch.UpdateChat(id, func(c *store.Chat) {
			if conv.GetName() != "" {
				c.Name = conv.GetName()
			}
			if c.Kind == "" {
				c.Kind = archiveKind(id)
			}
			c.LastActivity = max(c.LastActivity, int64(conv.GetLastMsgTimestamp()))
			if onDemand {
				// An answer to a request holds older messages only: the
				// chat's state is the live one already.
				c.HistoryStart = c.HistoryStart || conv.GetEndOfHistoryTransfer()
				return
			}
			if conv.UnreadCount != nil {
				c.Unread = int(conv.GetUnreadCount())
			}
			if conv.Archived != nil {
				c.Archived = conv.GetArchived()
			}
			if conv.Pinned != nil {
				c.Pinned = conv.GetPinned() > 0
				c.PinnedAt = int64(conv.GetPinned())
			}
			if conv.MuteEndTime != nil {
				c.MutedUntil = int64(conv.GetMuteEndTime())
			}
			if conv.ReadOnly != nil {
				c.ReadOnly = conv.GetReadOnly()
			}
			if conv.Locked != nil && c.LockUpdatedAt == 0 {
				c.Locked = c.Locked || conv.GetLocked()
				c.LockKnown = true
			}
			c.MarkedUnread = conv.GetMarkedAsUnread()
			c.HistoryStart = c.HistoryStart || conv.GetEndOfHistoryTransfer()
			c.EphemeralSeconds = conv.GetEphemeralExpiration()
		}); err != nil {
			return err
		}
		batch := make([]store.Message, 0, 128)
		var polls []*events.Message
		var photos []string
		count := 0
		var latest store.Message
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			err := e.arch.SaveMessages(batch)
			batch = batch[:0]
			return err
		}
		for _, item := range conv.GetMessages() {
			msg, err := e.cli.ParseWebMessage(j, item.GetMessage())
			if err != nil {
				continue
			}
			sender := e.canonical(msg.Info.Sender)
			if parent, clip, ok := motionClip(unwrap(msg.Message)); ok {
				if err := flush(); err != nil {
					return err
				}
				if err := e.motionArrived(id, parent, clip); err != nil {
					return err
				}
				continue
			}
			m := parseMessage(msg, id, sender)
			m.Order = int64(item.GetMsgOrderID())
			e.nameQuote(&m)
			switch m.Kind {
			case kindProtocol:
				continue
			case kindPollUpdate, kindPin:
				if err := flush(); err != nil {
					return err
				}
				var err error
				if m.Kind == kindPin {
					err = e.pinMessage(msg, id, sender)
				} else {
					err = e.pollVote(msg)
				}
				if err != nil {
					return err
				}
				continue
			case kindReaction:
				if err := flush(); err != nil {
					return err
				}
				if err := e.historyReaction(msg, id, e.canonical(msg.Info.Sender)); err != nil {
					return err
				}
				continue
			}
			if m.Edited || m.Deleted {
				// An edit or a revocation in history changes a row that
				// may be in this very batch.
				if err := flush(); err != nil {
					return err
				}
				if err := e.arch.UpdateMessage(id, m.ID, func(old *store.Message) {
					if m.Deleted {
						revoked(old)
					} else {
						old.Text, old.Edited, old.Mentions = m.Text, true, m.Mentions
					}
				}); err != nil && !noRows(err) {
					return err
				}
				continue
			}
			if m.Kind == kindPoll && len(msg.SourceWebMsg.GetPollUpdates()) > 0 {
				polls = append(polls, msg)
			}
			if m.Kind == store.KindImage {
				photos = append(photos, m.ID)
			}
			batch = append(batch, m)
			count++
			if m.Timestamp > latest.Timestamp {
				latest = m
			}
			if len(batch) >= 128 {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if err := flush(); err != nil {
			return err
		}
		for _, p := range polls {
			if err := e.pollSnapshots(p, id); err != nil {
				return err
			}
		}
		for _, photo := range photos {
			e.pendingMotion(id, photo)
		}
		if latest.ID != "" {
			if err := e.arch.UpdateChat(id, func(c *store.Chat) {
				if latest.Timestamp >= c.LastActivity {
					c.LastActivity = latest.Timestamp
					c.Preview = summary(latest)
				}
			}); err != nil {
				return err
			}
		}
		if onDemand {
			answered = append(answered, filed{id, count, count > 0 && !conv.GetEndOfHistoryTransfer()})
		}
	}
	if !onDemand {
		e.emitChats()
		return nil
	}
	for _, f := range answered {
		e.emitChat(f.chat)
		e.answerOlder(f.chat, f.more)
	}
	return nil
}

func (e *Engine) historyReaction(msg *events.Message, chat, sender string) error {
	x := unwrap(msg.Message).GetReactionMessage()
	at := x.GetSenderTimestampMS()
	if at <= 0 {
		at = msg.Info.Timestamp.UnixMilli()
	}
	_, err := e.arch.ApplyReaction(chat, x.GetKey().GetID(), store.Reaction{Sender: sender, Emoji: x.GetText(), Timestamp: at / 1000}, at)
	if noRows(err) {
		return nil
	}
	return err
}

// answerOlder completes the request an on-demand chunk answers: the
// archived page before the request's anchor, then OlderFetched. A chunk
// nobody here asked for (a poll's request, one from before a restart)
// only says, quietly, that the archive has more to page.
func (e *Engine) answerOlder(chat string, more bool) {
	r := e.older[chat]
	if r == nil {
		e.olderFetched(chat, more, true)
		return
	}
	r.timer.Stop()
	delete(e.older, chat)
	page, _, err := e.arch.Messages(chat, r.before, olderShown)
	if err != nil && !noRows(err) {
		slog.Warn("zapfast wa: reading older messages", "err", err)
	}
	if err == nil {
		e.emitMessages(chat, page, true, false)
	}
	e.olderFetched(chat, more, false)
}

func (e *Engine) olderFetched(chat string, more, silent bool) {
	ev := abi.Event{Tag: abi.EventTagOlderFetched}
	ev.OlderFetched = abi.EventOlderFetched{Chat: b(chat), More: more, Silent: silent}
	e.out.Emit(&ev)
	e.pumpPrefetch()
}

// oldest is the chat's oldest archived message.
func (e *Engine) oldest(chat string) (store.Message, bool) {
	before := ""
	for {
		page, more, err := e.arch.Messages(chat, before, 500)
		if err != nil || len(page) == 0 {
			return store.Message{}, false
		}
		if !more {
			return page[0], true
		}
		before = page[0].ID
	}
}

// fetchOlder asks the phone for messages before the archive's oldest.
func (e *Engine) fetchOlder(chat string, explicit bool) {
	if r := e.older[chat]; r != nil {
		r.explicit = r.explicit || explicit
		return
	}
	if c, err := e.arch.Chat(chat); err == nil && c.HistoryStart {
		// The phone already said it has nothing older.
		e.olderFetched(chat, false, false)
		return
	}
	anchor, ok := e.oldest(chat)
	if !ok {
		anchor = store.Message{ChatID: chat, Timestamp: time.Now().Unix()}
	}
	e.requestHistory(chat, anchor, explicit)
}

// reloadHistory asks the phone again for what precedes a message, to fill
// a gap inside the history.
func (e *Engine) reloadHistory(chat, id string) {
	if e.older[chat] != nil {
		return
	}
	anchor, err := e.arch.Message(chat, id)
	if err != nil {
		e.olderFetched(chat, true, false)
		e.emitError("This message is no longer in the archive")
		return
	}
	e.requestHistory(chat, anchor, true)
}

func (e *Engine) requestHistory(chat string, anchor store.Message, explicit bool) {
	j, ok := jidOf(chat)
	if !ok || !e.cli.IsConnected() {
		// Offline: the banner shows the state, and the reader asks again.
		e.olderFetched(chat, true, false)
		return
	}
	e.olderNext++
	serial := e.olderNext
	r := &olderRequest{before: anchor.ID, explicit: explicit, serial: serial}
	r.timer = time.AfterFunc(e.olderPatience, func() { e.post(func() { e.olderExpired(chat, serial) }) })
	e.older[chat] = r
	msg := historyRequest(e.messageKey(j, anchor), anchor.Timestamp, phoneBatch)
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		if _, err := cli.SendPeerMessage(ctx, msg); err != nil {
			return func() {
				if r := e.older[chat]; r != nil && r.serial == serial {
					r.timer.Stop()
					delete(e.older, chat)
					e.olderFetched(chat, true, false)
					e.emitError("Could not request older messages from your phone: " + err.Error())
				}
			}
		}
		return nil
	})
}

// olderExpired reports a request the phone left unanswered.
func (e *Engine) olderExpired(chat string, serial uint64) {
	r := e.older[chat]
	if r == nil || r.serial != serial {
		return
	}
	delete(e.older, chat)
	e.olderFetched(chat, true, false)
	if r.explicit {
		e.emitError("Your phone did not send older messages. Check that it is online")
	}
}

// messageKey is the key of a stored message in chat, as whatsmeow's
// BuildMessageKey makes it.
func (e *Engine) messageKey(chat types.JID, m store.Message) *waCommon.MessageKey {
	k := &waCommon.MessageKey{FromMe: proto.Bool(m.FromMe), ID: proto.String(m.ID), RemoteJID: proto.String(chat.String())}
	if !m.FromMe && chat.Server != types.DefaultUserServer && chat.Server != types.HiddenUserServer && m.Sender != "" {
		k.Participant = proto.String(m.Sender)
	}
	return k
}

// historyRequest is whatsmeow's BuildHistorySyncRequest for the messages
// before key (sent at timestamp, Unix seconds).
func historyRequest(key *waCommon.MessageKey, timestamp int64, count int) *waE2E.Message {
	return &waE2E.Message{
		ProtocolMessage: &waE2E.ProtocolMessage{
			Type: waE2E.ProtocolMessage_PEER_DATA_OPERATION_REQUEST_MESSAGE.Enum(),
			PeerDataOperationRequestMessage: &waE2E.PeerDataOperationRequestMessage{
				PeerDataOperationRequestType: waE2E.PeerDataOperationRequestType_HISTORY_SYNC_ON_DEMAND.Enum(),
				HistorySyncOnDemandRequest: &waE2E.PeerDataOperationRequestMessage_HistorySyncOnDemandRequest{
					ChatJID:         key.RemoteJID,
					OldestMsgID:     key.ID,
					OldestMsgFromMe: key.FromMe,
					// Despite its name, the field takes seconds.
					OnDemandMsgCount:     proto.Int32(int32(count)),
					OldestMsgTimestampMS: proto.Int64(timestamp),
				},
			},
		},
	}
}

// resendRequest is whatsmeow's BuildUnavailableMessageRequest for key: the
// phone sends its copy of the message back.
func resendRequest(key *waCommon.MessageKey) *waE2E.Message {
	return &waE2E.Message{
		ProtocolMessage: &waE2E.ProtocolMessage{
			Type: waE2E.ProtocolMessage_PEER_DATA_OPERATION_REQUEST_MESSAGE.Enum(),
			PeerDataOperationRequestMessage: &waE2E.PeerDataOperationRequestMessage{
				PeerDataOperationRequestType: waE2E.PeerDataOperationRequestType_PLACEHOLDER_MESSAGE_RESEND.Enum(),
				PlaceholderMessageResendRequest: []*waE2E.PeerDataOperationRequestMessage_PlaceholderMessageResendRequest{{
					MessageKey: key,
				}},
			},
		},
	}
}
