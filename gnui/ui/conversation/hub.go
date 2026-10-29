package conversation

import (
	"bytes"

	"zapfast/abi"
	"zapfast/model"
	"zapfast/state"
)

// hub is what the conversation and its search pane share in one app state:
// the events both listen to and the search. Pins, stars and presence are the
// model's (Store.ChatPin, Starred, Presence).
type hub struct {
	s *state.State

	// the open chat's paging (zf:src/app.rs:174 Conversation).
	hist history

	search searchState

	listening bool
}

// history is the open chat's first page; older pages are the model's
// (Conversation.LoadingOlder, FetchingPhone; Account.LoadOlder, FetchOlder).
type history struct {
	acct  uint64
	chat  model.ChatRef
	first bool // the chat's first page has not come
}

var hubs []*hub

// hubOf is the hub of the app state s, made on first use.
func hubOf(s *state.State) *hub {
	for _, h := range hubs {
		if h.s == s {
			return h
		}
	}
	h := &hub{s: s}
	if len(hubs) > 16 {
		hubs = hubs[1:]
	}
	hubs = append(hubs, h)
	return h
}

// listen registers the hub for the events it keeps, once.
func (h *hub) listen() {
	if h.listening {
		return
	}
	h.listening = true
	h.s.Listen(h, abi.EventTagMessages, abi.EventTagPinChanged, abi.EventTagChatHits)
}

// openChat reports whether chat is the chat a has open.
func openChat(a *state.Account, chat []byte) bool {
	c, ok := a.OpenChatView()
	return ok && bytes.Equal(c.ID(), chat)
}

// Event follows what the model does not keep: the first page's arrival, a
// new pin's listing and the search's hits (state.Listener).
func (h *hub) Event(a *state.Account, e *abi.Event) {
	switch e.Tag {
	case abi.EventTagMessages:
		m := &e.Messages
		if h.hist.acct == a.Serial && h.hist.chat != 0 && a.Model.Chats.Find(m.Chat) == h.hist.chat && m.Last && !m.Older {
			h.hist.first = false
		}
	case abi.EventTagPinChanged:
		// the model keeps the mark; the line under the header lists the
		// chat's pins again for a new one
		p := &e.PinChanged
		if p.Pinned && a.Active() && openChat(a, p.Chat) {
			cmd := a.Cmd(abi.CommandTagLoadPins)
			cmd.LoadPins.Chat = p.Chat
			a.Send(cmd)
		}
	case abi.EventTagChatHits:
		if a.Active() && openChat(a, e.ChatHits.Chat) {
			h.search.answer(a, &e.ChatHits)
		}
	}
}
