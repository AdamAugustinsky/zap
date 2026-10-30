package wa

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/out"
	"zapfast/gocore/store"
)

// Labels, stars and message pins, as upstream's worker keeps them: labels
// are this computer's own (and the phone's WhatsApp labels, filed beside
// them under "wa-" ids); a star or a pin reaches the archive only once
// WhatsApp accepted it, or when it comes from the phone.

const (
	// pinSeconds is how long a message pin lasts (upstream's PIN_SECS).
	pinSeconds = 7 * 24 * 60 * 60
	// pinnedChats is how many chats may be pinned (upstream's
	// PINNED_CHATS; the Plus subscription's 20 is not asked for).
	pinnedChats = 3
)

// --- labels ------------------------------------------------------------------

func (e *Engine) emitLabels() {
	labels, err := e.arch.Labels()
	if err != nil {
		slog.Warn("zapfast wa: listing labels", "err", err)
		return
	}
	out.Chunks(e.out, labels, func(_ *out.Record, l store.Label) abi.Label {
		return abi.Label{ID: b(l.ID), Name: b(out.Clip(l.Name, 1024)), ColorHex: b(l.Color), CreatedAt: l.CreatedAt}
	}, func(batch uint32, last bool, chunk []abi.Label) *abi.Event {
		return &abi.Event{Tag: abi.EventTagLabels, Labels: abi.EventLabels{Batch: batch, Last: last, Labels: chunk}}
	})
}

// createLabel adds a label. The UI refuses a full set or a taken name in
// the reader's language first; the archive checks again and says nothing.
func (e *Engine) createLabel(name, color string) {
	_, ok, err := e.arch.CreateLabel(name, color, store.Now())
	switch {
	case err != nil:
		slog.Warn("zapfast wa: creating a label", "err", err)
	case ok:
		e.emitLabels()
	}
}

func (e *Engine) updateLabel(id, name, color string) {
	ok, err := e.arch.UpdateLabel(id, name, color)
	switch {
	case err != nil:
		slog.Warn("zapfast wa: updating a label", "err", err)
	case ok:
		e.emitLabels()
	}
}

func (e *Engine) deleteLabel(id string) {
	ok, err := e.arch.DeleteLabel(id)
	switch {
	case err != nil:
		slog.Warn("zapfast wa: deleting a label", "err", err)
	case ok:
		e.emitLabels()
	}
}

func (e *Engine) setChatLabels(chat string, ids []string) {
	if err := e.arch.SetChatLabels(chat, ids); err != nil {
		slog.Warn("zapfast wa: labelling a chat", "err", err)
	}
	e.emitChat(chat)
}

// labelColors are WhatsApp's label colours, by the index the phone sends.
var labelColors = [...]string{
	"#ff9485", "#64c4ff", "#ffd429", "#dfaef0", "#99b6c1", "#55ccb3", "#ff9dff", "#d3a91d", "#6d7cce", "#d7e752",
	"#00d0e2", "#ffc5c7", "#93ceac", "#f74848", "#00a0f2", "#83e422", "#ffaf04", "#b5ebff", "#9ba6ff", "#9368cf",
}

func phoneLabel(id string) string { return "wa-" + id }

// labelEdited files a WhatsApp label from app state.
func (e *Engine) labelEdited(v *events.LabelEdit) error {
	id := phoneLabel(v.LabelID)
	if v.Action.GetDeleted() {
		ok, err := e.arch.DeleteLabel(id)
		if ok {
			e.emitLabels()
		}
		return err
	}
	color := labelColors[0]
	if c := v.Action.GetColor(); c >= 0 && int(c) < len(labelColors) {
		color = labelColors[c]
	}
	ok, err := e.arch.PutLabel(store.Label{ID: id, Name: v.Action.GetName(), Color: color, CreatedAt: v.Timestamp.Unix()})
	if ok {
		e.emitLabels()
	}
	return err
}

// chatLabelled puts a WhatsApp label on a chat, or takes it off.
func (e *Engine) chatLabelled(v *events.LabelAssociationChat) error {
	chat := e.canonical(v.JID)
	ok, err := e.arch.LabelChat(chat, phoneLabel(v.LabelID), v.Action.GetLabeled())
	if ok {
		e.emitChat(chat)
	}
	return err
}

// --- stars -------------------------------------------------------------------

// emitStars sends a chat's starred ids, when it is opened.
func (e *Engine) emitStars(chat string) {
	ids, err := e.arch.StarredIDs(chat)
	if err != nil {
		return
	}
	list := make([]abi.MessageId, len(ids))
	for i, id := range ids {
		list[i] = abi.MessageId{ID: b(id)}
	}
	c := b(chat)
	out.List(e.out, list, func(batch uint32, last bool, chunk []abi.MessageId) *abi.Event {
		return &abi.Event{Tag: abi.EventTagStars, Stars: abi.EventStars{Chat: c, Batch: batch, Last: last, Ids: chunk}}
	})
}

// loadStarred sends every starred message, newest star first.
func (e *Engine) loadStarred() {
	list, err := e.arch.Starred(store.StarredLimit)
	if err != nil {
		e.emitError(err.Error())
		return
	}
	for i := range list {
		e.receiptTimes(&list[i].Message)
	}
	cv := e.conv()
	out.Chunks(e.out, list, func(r *out.Record, s store.Starred) abi.Starred {
		return abi.Starred{Message: cv.message(r, s.Message), StarredAt: s.StarredAt}
	}, func(batch uint32, last bool, chunk []abi.Starred) *abi.Event {
		return &abi.Event{Tag: abi.EventTagStarredList, StarredList: abi.EventStarredList{Batch: batch, Last: last, Starred: chunk}}
	})
}

// participant is the sender a message's app-state index names: someone
// else's message in a group; for the rest, the chat itself, which
// whatsmeow writes as "0".
func (e *Engine) participant(j types.JID, m store.Message) types.JID {
	if j.Server == types.GroupServer && !m.FromMe {
		if s, ok := jidOf(m.Sender); ok {
			return s
		}
	}
	return j
}

// setStar stars a message on WhatsApp first; the archive follows the
// answer, so the list never claims what WhatsApp refused.
func (e *Engine) setStar(chat, id string, starred bool) {
	e.serial++
	key, serial := [2]string{chat, id}, e.serial
	e.stars[key] = serial
	j, ok := jidOf(chat)
	if !ok || !e.cli.IsConnected() {
		delete(e.stars, key)
		e.emitError("Not connected to WhatsApp")
		return
	}
	m, err := e.arch.Message(chat, id)
	if err != nil {
		delete(e.stars, key)
		e.emitError("This message is not on this computer")
		return
	}
	patch := appstate.BuildStar(j, e.participant(j, m), id, m.FromMe, starred)
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		err := cli.SendAppState(ctx, patch)
		return func() {
			if gen != e.gen || e.stars[key] != serial {
				return
			}
			delete(e.stars, key)
			if err != nil {
				e.emitError("Could not star the message: " + err.Error())
				return
			}
			// The time is this answer's: an older phone event cannot undo it.
			changed, err := e.arch.SetStar(chat, id, starred, store.Now(), false)
			if err != nil {
				e.emitError(err.Error())
				return
			}
			if changed {
				e.starChanged(chat, id, starred, true)
			}
		}
	})
}

func (e *Engine) starChanged(chat, id string, starred, local bool) {
	ev := abi.Event{Tag: abi.EventTagStarChanged}
	ev.StarChanged = abi.EventStarChanged{Chat: b(chat), Message: b(id), Starred: starred, Local: local}
	e.out.Emit(&ev)
}

// starred is a star or unstar from the phone or another device. An older
// one loses to the time stored and does not cancel a request in flight.
func (e *Engine) starred(v *events.Star) error {
	chat := e.canonical(v.ChatJID)
	starred := v.Action.GetStarred()
	changed, err := e.arch.SetStar(chat, v.MessageID, starred, v.Timestamp.Unix(), v.FromFullSync)
	if err != nil || !changed {
		return err
	}
	delete(e.stars, [2]string{chat, v.MessageID})
	e.starChanged(chat, v.MessageID, starred, false)
	return nil
}

// --- message pins ------------------------------------------------------------

func (e *Engine) emitPins(chat string) {
	pins, err := e.arch.ChatPins(chat, store.Now())
	if err != nil {
		e.emitError(err.Error())
		return
	}
	c := b(chat)
	out.Chunks(e.out, pins, func(_ *out.Record, p store.Pinned) abi.Pinned {
		return abi.Pinned{
			Chat: c, ID: b(p.ID), PinnedAt: p.PinnedAt, ExpiresAt: p.ExpiresAt,
			Text: b(p.Message.Text), FromMe: p.Message.FromMe, SentAt: p.Message.Timestamp,
			Pinner: abi.Pinner{ByMe: p.Pinner.ByMe, Sender: b(p.Pinner.Sender)},
		}
	}, func(batch uint32, last bool, chunk []abi.Pinned) *abi.Event {
		return &abi.Event{Tag: abi.EventTagPins, Pins: abi.EventPins{Chat: c, Batch: batch, Last: last, Items: chunk}}
	})
}

// setMessagePinned pins a message for everyone for seven days, or unpins
// it. WhatsApp allows three active pins in a chat.
func (e *Engine) setMessagePinned(chat, id string, pinned bool) {
	now := store.Now()
	if pinned {
		if full, err := e.arch.PinFull(chat, id, now); err != nil || full {
			e.emitError("This chat already has three pinned messages")
			return
		}
	}
	j, ok := jidOf(chat)
	if !ok || !e.cli.IsConnected() {
		e.emitError("Not connected to WhatsApp")
		return
	}
	m, err := e.arch.Message(chat, id)
	if err != nil {
		e.emitError("This message is not stored on this computer")
		return
	}
	e.serial++
	key, serial := [2]string{chat, id}, e.serial
	e.pins[key] = serial
	kind := waE2E.PinInChatMessage_PIN_FOR_ALL
	msg := &waE2E.Message{PinInChatMessage: &waE2E.PinInChatMessage{Key: e.messageKey(j, m), SenderTimestampMS: proto.Int64(time.Now().UnixMilli())}}
	if pinned {
		msg.MessageContextInfo = &waE2E.MessageContextInfo{MessageAddOnDurationInSecs: proto.Uint32(pinSeconds)}
	} else {
		kind = waE2E.PinInChatMessage_UNPIN_FOR_ALL
	}
	msg.PinInChatMessage.Type = kind.Enum()
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		_, err := cli.SendMessage(ctx, j, msg, "")
		return func() {
			if gen != e.gen || e.pins[key] != serial {
				return
			}
			delete(e.pins, key)
			if err != nil {
				verb := "pin"
				if !pinned {
					verb = "unpin"
				}
				e.emitError(fmt.Sprintf("Could not %s the message: %v", verb, err))
				return
			}
			changed, err := e.arch.SetPin(chat, id, pinned, now, now+pinSeconds, store.Pinner{ByMe: true})
			if err != nil {
				e.emitError(err.Error())
				return
			}
			if changed {
				e.pinChanged(chat, id, pinned)
			}
		}
	})
}

func (e *Engine) pinChanged(chat, id string, pinned bool) {
	ev := abi.Event{Tag: abi.EventTagPinChanged}
	ev.PinChanged = abi.EventPinChanged{Chat: b(chat), Message: b(id), Pinned: pinned}
	e.out.Emit(&ev)
	e.emitPins(chat)
}

// pinMessage files a pin or an unpin that arrived as a message: from the
// account's other device, or from anyone in the chat.
func (e *Engine) pinMessage(evt *events.Message, chat, sender string) error {
	p := unwrap(evt.Message)
	pin := p.GetPinInChatMessage()
	id := pin.GetKey().GetID()
	if id == "" {
		return nil
	}
	at := pin.GetSenderTimestampMS() / 1000
	if at <= 0 {
		at = evt.Info.Timestamp.Unix()
	}
	pinned := pin.GetType() != waE2E.PinInChatMessage_UNPIN_FOR_ALL
	expires := int64(0)
	if pinned {
		d := int64(p.GetMessageContextInfo().GetMessageAddOnDurationInSecs())
		if d == 0 {
			d = pinSeconds
		}
		expires = at + d
	}
	delete(e.pins, [2]string{chat, id})
	changed, err := e.arch.SetPin(chat, id, pinned, at, expires, store.Pinner{ByMe: evt.Info.IsFromMe, Sender: sender})
	if err != nil || !changed {
		return err
	}
	e.pinChanged(chat, id, pinned)
	return nil
}

// announcePinLimit tells the UI how many chats may be pinned, once a
// session.
func (e *Engine) announcePinLimit() {
	if e.pinLimit {
		return
	}
	e.pinLimit = true
	ev := abi.Event{Tag: abi.EventTagPinLimit}
	ev.PinLimit.Limit = pinnedChats
	e.out.Emit(&ev)
}
