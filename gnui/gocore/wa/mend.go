package wa

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/gocore/store"
)

// askBack is how far back, and askMost how many, messages still waiting
// are asked of the phone again each session.
const (
	askBack = 30 * 24 * time.Hour
	askMost = 200
)

// mend repairs, once a session, what builds before the filters in
// parse.go and incoming filed, and asks the phone for what still waits.
//
// Status updates and broadcast lists made chats of their own: they go. A
// row that held a group's sender key stood in for the message it came
// with, so it waits for that message; any other signalling row goes, and
// a row the parser now names better is renamed. Then every recent message
// still waiting is asked of the phone, quietly: it is old news by now.
func (e *Engine) mend() {
	if e.mended {
		return
	}
	e.mended = true
	if chats, err := e.arch.Chats(); err == nil {
		for _, c := range chats {
			if strings.HasSuffix(c.ID, "@"+types.BroadcastServer) {
				e.deleted(c.ID, 0)
			}
		}
	}
	e.mendSignalling()
	waiting, err := e.arch.OfKind(kindWaiting, time.Now().Add(-askBack).Unix())
	if err != nil {
		slog.Warn("zapfast wa: listing waiting messages", "err", err)
		return
	}
	if len(waiting) > askMost {
		waiting = waiting[len(waiting)-askMost:]
	}
	var requests []*waE2E.Message
	for _, m := range waiting {
		j, ok := jidOf(m.ChatID)
		if !ok {
			continue
		}
		e.asked[[2]string{m.ChatID, m.ID}] = true
		keys := []*waCommon.MessageKey{e.messageKey(j, m)}
		// A group may know its members by their privacy ids only: the
		// phone answers the key it knows.
		if s, ok := jidOf(m.Sender); ok && j.Server == types.GroupServer && s.Server == types.DefaultUserServer {
			if lid, err := e.cli.LIDForPN(e.ctx, s); err == nil && !lid.IsEmpty() {
				alt := e.messageKey(j, m)
				alt.Participant = proto.String(lid.ToNonAD().String())
				keys = append(keys, alt)
			}
		}
		requests = append(requests, resendRequest(keys...))
	}
	if len(requests) == 0 {
		return
	}
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		for _, r := range requests {
			if _, err := cli.SendPeerMessage(ctx, r); err != nil {
				slog.Warn("zapfast wa: asking the phone for a message", "err", err)
				break
			}
		}
		return nil
	})
}

// mendSignalling looks again at the rows filed as unsupported messages.
func (e *Engine) mendSignalling() {
	rows, err := e.arch.OfKind(kindUnsupported, 0)
	if err != nil {
		slog.Warn("zapfast wa: listing unsupported messages", "err", err)
		return
	}
	touched := map[string]bool{}
	for _, m := range rows {
		var p waE2E.Message
		if len(m.Raw) == 0 || proto.Unmarshal(m.Raw, &p) != nil {
			continue
		}
		again := parseMessage(&events.Message{Info: types.MessageInfo{ID: m.ID}, Message: &p}, m.ChatID, m.Sender)
		switch {
		case again.Kind == kindProtocol && (p.GetSenderKeyDistributionMessage() != nil || p.GetFastRatchetKeySenderKeyDistributionMessage() != nil):
			err = e.arch.UpdateMessage(m.ChatID, m.ID, func(old *store.Message) { old.Kind, old.Text = kindWaiting, "" })
			if err == nil {
				e.emitMessage(m.ChatID, m.ID)
			}
		case again.Kind == kindProtocol:
			if err = e.arch.DeleteLocal(m.ChatID, m.ID); err == nil {
				e.messageDeleted(m.ChatID, m.ID)
			}
		case again.Kind != m.Kind || again.Text != m.Text:
			err = e.arch.UpdateMessage(m.ChatID, m.ID, func(old *store.Message) { old.Kind, old.Text, old.Call = again.Kind, again.Text, again.Call })
			if err == nil {
				e.emitMessage(m.ChatID, m.ID)
			}
		default:
			continue
		}
		if err != nil {
			slog.Warn("zapfast wa: mending a message", "err", err)
			continue
		}
		touched[m.ChatID] = true
	}
	for chat := range touched {
		newest, _, err := e.arch.Messages(chat, "", 1)
		if err != nil || len(newest) == 0 {
			continue
		}
		if err := e.arch.UpdateChat(chat, func(c *store.Chat) { c.Preview = summary(newest[0]) }); err == nil {
			e.emitChat(chat)
		}
	}
}
