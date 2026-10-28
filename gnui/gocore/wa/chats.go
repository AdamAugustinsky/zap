package wa

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/out"
	"zapfast/gocore/store"
)

const (
	// page is how many messages LoadChat sends (upstream's PAGE).
	page = 60
	// untilMax is the most messages LoadUntil sends to reach a quote.
	untilMax = 2000
	// chatSearchLimit is how many in-chat matches the pane lists (upstream's
	// CHAT_SEARCH_LIMIT).
	chatSearchLimit = 80
	// searchLimit is how many matches a message search returns.
	searchLimit = 50
)

// loadChat sends a page of the archive: the newest, or the one before a
// key. Opening a direct chat follows its contact's presence.
func (e *Engine) loadChat(chat string, before *abi.PageKey) {
	key := ""
	if before != nil {
		key = string(before.ID)
	}
	messages, more, err := e.arch.Messages(chat, key, page)
	if err != nil {
		if !noRows(err) {
			e.emitError("Could not read the chat: " + err.Error())
		}
		// The page key is gone from the archive: nothing can be paged from it.
		e.emitMessages(chat, nil, before != nil, true)
		return
	}
	e.emitMessages(chat, messages, before != nil, !more)
	if before == nil {
		e.followPresence(chat)
	}
}

// followPresence subscribes to a direct chat's presence once a session.
func (e *Engine) followPresence(chat string) {
	j, ok := jidOf(chat)
	if !ok || chatKind("", chat) != abi.ChatKindDirect || e.isMe(chat) || e.presence[chat] || !e.cli.IsConnected() {
		return
	}
	e.presence[chat] = true
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		if err := cli.SubscribePresence(ctx, j); err != nil {
			slog.Debug("zapfast wa: presence not followed", "err", err)
		}
		return nil
	})
}

// loadUntil sends the messages from id up to (not including) the page key,
// so the view can scroll to a quoted message.
func (e *Engine) loadUntil(chat, id string, before abi.PageKey) {
	target, err := e.arch.Message(chat, id)
	if err != nil {
		e.emitMessages(chat, nil, true, false)
		e.emitError("This message is not stored on this computer")
		return
	}
	var messages []store.Message
	key := string(before.ID)
	for len(messages) < untilMax {
		chunk, more, err := e.arch.Messages(chat, key, 500)
		if err != nil {
			e.emitError("Could not read the chat: " + err.Error())
			break
		}
		messages = append(chunk, messages...)
		if !more || len(chunk) == 0 || chunk[0].Timestamp < target.Timestamp || chunk[0].ID == id {
			break
		}
		key = chunk[0].ID
	}
	first := 0
	for first < len(messages) && messages[first].Timestamp < target.Timestamp {
		first++
	}
	messages = messages[first:]
	if len(messages) > untilMax {
		messages = messages[len(messages)-untilMax:]
	}
	e.emitMessages(chat, messages, true, false)
}

// ensureChat lists a chat the reader starts (a new contact): the archive
// keeps it with its name until messages come.
func (e *Engine) ensureChat(chat, name string) {
	if _, err := e.arch.Chat(chat); err == nil {
		return
	} else if !noRows(err) {
		slog.Warn("zapfast wa: reading a chat", "err", err)
		return
	}
	if _, ok := jidOf(chat); !ok {
		return
	}
	if err := e.arch.UpsertChat(store.Chat{ID: chat, Name: name, Kind: archiveKind(chat), LastActivity: store.Now()}); err != nil {
		slog.Warn("zapfast wa: could not create the chat", "err", err)
		return
	}
	e.emitChat(chat)
}

func (e *Engine) composing(chat string, composing bool) {
	j, ok := jidOf(chat)
	if !ok || !e.cli.IsConnected() {
		return
	}
	state := types.ChatPresencePaused
	if composing {
		state = types.ChatPresenceComposing
	}
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		if err := cli.SendChatPresence(ctx, j, state); err != nil {
			slog.Debug("zapfast wa: chat state not sent", "err", err)
		}
		return nil
	})
}

// lastKey is a chat's newest message and its time (now when it has
// none), which the phone's chat actions are anchored to.
func (e *Engine) lastKey(chat string) (time.Time, *store.Message) {
	page, _, err := e.arch.Messages(chat, "", 1)
	if err != nil || len(page) == 0 {
		return time.Now(), nil
	}
	return time.Unix(page[0].Timestamp, 0), &page[0]
}

// tellPhone sends a chat setting to the phone without holding anything
// up; a failure is logged, as upstream's tell_phone does.
func (e *Engine) tellPhone(patch appstate.PatchInfo) {
	if !e.cli.IsConnected() {
		return
	}
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		if err := cli.SendAppState(ctx, patch); err != nil {
			slog.Warn("zapfast wa: could not synchronize a chat preference", "err", err)
		}
		return nil
	})
}

func (e *Engine) setArchived(chat string, archived bool) {
	j, ok := jidOf(chat)
	if !ok {
		return
	}
	if err := e.updateChat(chat, func(c *store.Chat) { c.Archived = archived }); err != nil {
		return
	}
	at, m := e.lastKey(chat)
	e.tellPhone(appstate.BuildArchive(j, archived, at, e.keyOf(j, m)))
}

func (e *Engine) setPinned(chat string, pinned bool) {
	j, ok := jidOf(chat)
	if !ok {
		return
	}
	if err := e.updateChat(chat, func(c *store.Chat) {
		c.Pinned, c.PinnedAt = pinned, 0
		if pinned {
			c.PinnedAt = time.Now().Unix()
		}
	}); err != nil {
		return
	}
	e.tellPhone(appstate.BuildPin(j, pinned))
}

// setMuted mutes until a Unix second (the archive's: 0 unmuted, -1 always).
func (e *Engine) setMuted(chat string, until int64) {
	j, ok := jidOf(chat)
	if !ok {
		return
	}
	if err := e.updateChat(chat, func(c *store.Chat) { c.MutedUntil = until }); err != nil {
		return
	}
	switch {
	case until == 0:
		e.tellPhone(appstate.BuildMuteAbs(j, false, nil))
	case until < 0:
		forever := int64(-1)
		e.tellPhone(appstate.BuildMuteAbs(j, true, &forever))
	default:
		ms := until * 1000
		e.tellPhone(appstate.BuildMuteAbs(j, true, &ms))
	}
}

// setFavorite adds a chat to the favourites, last, or takes it off; the
// phone gets the whole list.
func (e *Engine) setFavorite(chat string, favorite bool) {
	j, ok := jidOf(chat)
	if !ok || j.Server == types.NewsletterServer {
		e.emitError("This chat cannot be a favourite")
		return
	}
	chats, err := e.arch.Chats()
	if err != nil {
		return
	}
	position := 0
	for _, c := range chats {
		if c.Favorite && c.ID != chat {
			position = max(position, c.FavoritePosition+1)
		}
	}
	if err := e.updateChat(chat, func(c *store.Chat) {
		if favorite && !c.Favorite {
			c.FavoritePosition = position
		}
		c.Favorite = favorite
		if !favorite {
			c.FavoritePosition = 0
		}
	}); err != nil {
		return
	}
	chats, err = e.arch.Chats()
	if err != nil {
		return
	}
	var list []store.Chat
	for _, c := range chats {
		if c.Favorite {
			list = append(list, c)
		}
	}
	slices.SortStableFunc(list, func(a, b store.Chat) int { return a.FavoritePosition - b.FavoritePosition })
	action := &waSyncAction.FavoritesAction{}
	for _, c := range list {
		action.Favorites = append(action.Favorites, &waSyncAction.FavoritesAction_Favorite{ID: proto.String(c.ID)})
	}
	e.tellPhone(appstate.PatchInfo{Type: appstate.WAPatchRegularHigh, Mutations: []appstate.MutationInfo{{Index: []string{appstate.IndexFavorites}, Version: 1, Value: &waSyncAction.SyncActionValue{FavoritesAction: action}}}})
}

// markRead reads a chat here and on the phone, and sends read receipts
// for its unread messages when receipts is set.
func (e *Engine) markRead(chat string, receipts bool) {
	c, err := e.arch.Chat(chat)
	if err != nil {
		return
	}
	j, ok := jidOf(chat)
	if !ok {
		return
	}
	var unread []store.Message
	if receipts && c.Unread > 0 {
		page, _, err := e.arch.Messages(chat, "", min(c.Unread*4+8, 500))
		for i := len(page) - 1; err == nil && i >= 0 && len(unread) < c.Unread; i-- {
			if !page[i].FromMe && !page[i].Deleted {
				unread = append(unread, page[i])
			}
		}
	}
	if err := e.updateChat(chat, func(c *store.Chat) { c.Unread, c.MarkedUnread = 0, false }); err != nil {
		return
	}
	// A chat marked unread with nothing pending still tells the phone it
	// was read, which takes the phone's mark off.
	if c.Unread == 0 && !c.MarkedUnread {
		return
	}
	at, m := e.lastKey(chat)
	e.tellPhone(appstate.BuildMarkChatAsRead(j, true, at, e.keyOf(j, m)))
	if len(unread) == 0 || !e.cli.IsConnected() {
		return
	}
	bySender := map[types.JID][]types.MessageID{}
	for _, m := range unread {
		sender := types.EmptyJID
		if j.Server == types.GroupServer {
			s, ok := jidOf(m.Sender)
			if !ok {
				continue
			}
			sender = s
		}
		bySender[sender] = append(bySender[sender], m.ID)
	}
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		for sender, ids := range bySender {
			if err := cli.MarkRead(ctx, ids, time.Now(), j, sender, types.ReceiptTypeRead); err != nil {
				slog.Debug("zapfast wa: read receipt not sent", "err", err)
			}
		}
		return nil
	})
}

func (e *Engine) markUnread(chat string) {
	j, ok := jidOf(chat)
	if !ok {
		return
	}
	if err := e.updateChat(chat, func(c *store.Chat) { c.MarkedUnread = true }); err != nil {
		return
	}
	at, m := e.lastKey(chat)
	e.tellPhone(appstate.BuildMarkChatAsRead(j, false, at, e.keyOf(j, m)))
}

// markPlayed sends a voice note's played receipt when receipts are on, as
// upstream's mark_played does; nothing changes here.
func (e *Engine) markPlayed(chat, id, from string, receipts bool) {
	j, ok := jidOf(chat)
	if !receipts || !ok || !e.cli.IsConnected() {
		return
	}
	sender := types.EmptyJID
	if j.Server == types.GroupServer {
		s, ok := jidOf(from)
		if !ok {
			return
		}
		sender = s
	}
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		if err := cli.MarkRead(ctx, []types.MessageID{id}, time.Now(), j, sender, types.ReceiptTypePlayed); err != nil {
			slog.Debug("zapfast wa: played receipt not sent", "err", err)
		}
		return nil
	})
}

// removeChat deletes (or clears) a chat on the phone first, then here: a
// chat deleted only here would come back with the next sync.
func (e *Engine) removeChat(chat string, delete bool) {
	j, ok := jidOf(chat)
	if !ok {
		return
	}
	verb := "clear"
	if delete {
		verb = "delete"
	}
	if !e.cli.IsConnected() {
		e.emitError("Connect to WhatsApp to " + verb + " this chat")
		return
	}
	at, m := e.lastKey(chat)
	through := at.Unix()
	var patch appstate.PatchInfo
	if delete {
		patch = appstate.BuildDeleteChat(j, at, e.keyOf(j, m), true)
	} else {
		patch = appstate.PatchInfo{Type: appstate.WAPatchRegularHigh, Mutations: []appstate.MutationInfo{{
			Index:   []string{appstate.IndexClearChat, j.String(), "1", "1"},
			Version: 6,
			Value:   &waSyncAction.SyncActionValue{ClearChatAction: &waSyncAction.ClearChatAction{MessageRange: &waSyncAction.SyncActionMessageRange{LastMessageTimestamp: proto.Int64(through)}}},
		}}}
	}
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		err := cli.SendAppState(ctx, patch)
		return func() {
			if err != nil {
				e.emitError("The phone did not " + verb + " this chat. Try again when connected")
				return
			}
			if delete {
				e.deleted(chat, through)
			} else if !e.cleared(chat, through) {
				e.emitError("The phone cleared this chat, but it could not be cleared here")
			}
		}
	})
}

// keyOf is a message's key, nil for none.
func (e *Engine) keyOf(j types.JID, m *store.Message) *waCommon.MessageKey {
	if m == nil {
		return nil
	}
	return e.messageKey(j, *m)
}

func (e *Engine) searchMessages(query string) {
	hits, err := e.arch.Search(query, "", searchLimit)
	if err != nil {
		e.emitError("Could not search: " + err.Error())
		return
	}
	messages := make([]store.Message, len(hits))
	for i, h := range hits {
		messages[i] = h.Message
	}
	cv := e.conv()
	q := b(query)
	out.Chunks(e.out, messages, cv.message, func(batch uint32, last bool, chunk []abi.Message) *abi.Event {
		return &abi.Event{Tag: abi.EventTagSearchHits, SearchHits: abi.EventSearchHits{Query: q, Batch: batch, Last: last, Messages: chunk}}
	})
}

func (e *Engine) searchChatMessages(c *abi.CommandSearchChatMessages) {
	var from, until int64
	if c.HasFrom {
		from = c.From
	}
	if c.HasUntil {
		until = c.Until
	}
	chat := string(c.Chat)
	hits, truncated, err := e.arch.SearchRange(string(c.Query), chat, from, until, false, chatSearchLimit)
	if err != nil {
		e.emitError("Could not search: " + err.Error())
		return
	}
	messages := make([]store.Message, len(hits))
	for i, h := range hits {
		messages[i] = h.Message
	}
	ev := abi.EventChatHits{Chat: c.Chat, Query: c.Query, From: c.From, HasFrom: c.HasFrom, Until: c.Until, HasUntil: c.HasUntil}
	out.Chunks(e.out, messages, e.conv().message, func(batch uint32, last bool, chunk []abi.Message) *abi.Event {
		x := ev
		x.Batch, x.Last, x.Truncated, x.Messages = batch, last, truncated && last, chunk
		return &abi.Event{Tag: abi.EventTagChatHits, ChatHits: x}
	})
}
