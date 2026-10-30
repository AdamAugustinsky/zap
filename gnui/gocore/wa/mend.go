package wa

import (
	"context"
	"log/slog"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/gocore/store"
)

// mendSignalling mends, once a session, the rows that builds before the
// signalling filter (parse.go) filed as unsupported messages. A row that
// held a group's sender key stood in for the message it came with, so it
// waits for that message and the phone is asked for it; any other
// signalling row goes. A row the parser now names better is renamed.
func (e *Engine) mendSignalling() {
	if e.mended {
		return
	}
	e.mended = true
	rows, err := e.arch.Unsupported()
	if err != nil {
		slog.Warn("zapfast wa: listing unsupported messages", "err", err)
		return
	}
	var ask []store.Message
	touched := map[string]bool{}
	for _, m := range rows {
		var p waE2E.Message
		if proto.Unmarshal(m.Raw, &p) != nil {
			continue
		}
		again := parseMessage(&events.Message{Info: types.MessageInfo{ID: m.ID}, Message: &p}, m.ChatID, m.Sender)
		switch {
		case again.Kind == kindProtocol && (p.GetSenderKeyDistributionMessage() != nil || p.GetFastRatchetKeySenderKeyDistributionMessage() != nil):
			err = e.arch.UpdateMessage(m.ChatID, m.ID, func(old *store.Message) { old.Kind, old.Text = kindWaiting, "" })
			if err == nil {
				ask = append(ask, m)
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
	var requests []*waE2E.Message
	for _, m := range ask {
		if j, ok := jidOf(m.ChatID); ok {
			e.asked[[2]string{m.ChatID, m.ID}] = true
			requests = append(requests, resendRequest(e.messageKey(j, m)))
		}
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
