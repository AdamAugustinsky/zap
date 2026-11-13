package wa

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/core"
)

// The archive as the agents' door reads it (core.History): a chat's
// messages oldest first and in pages, pictures without their thumbnails,
// and nothing at all of a locked chat or of one that is not stored.
func TestHistoryIsReadForAgentsButNotOfALockedChat(t *testing.T) {
	h := linked(t) // M1 "hello" from peer
	h.receive(peer, peer, "M2", t0.Add(time.Minute), &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Caption: proto.String("the engine"), Mimetype: proto.String("image/jpeg"), JPEGThumbnail: []byte{0xff, 0xd8, 1, 2}}})
	h.receive(peer, peer, "M3", t0.Add(2*time.Minute), text("did it build?"))
	ctx, chat := context.Background(), string(chatID(peer))

	page, more, err := h.e.History(ctx, chat, "", 2)
	if err != nil || len(page) != 2 || !more || string(page[0].ID) != "M2" || string(page[1].ID) != "M3" {
		t.Fatalf("the newest page: %d messages, more=%v, err=%v", len(page), more, err)
	}
	if m := page[0]; m.Content.Tag != abi.ContentTagImage || string(m.Content.Image.Caption) != "the engine" || m.HasThumbnail {
		t.Fatalf("the picture: tag=%v thumbnail=%v", m.Content.Tag, m.HasThumbnail)
	}
	older, more, err := h.e.History(ctx, chat, "M2", 2)
	if err != nil || len(older) != 1 || more || string(older[0].ID) != "M1" {
		t.Fatalf("the page before M2: %d messages, more=%v, err=%v", len(older), more, err)
	}
	if hits, err := h.e.SearchHistory(ctx, chat, "build", 10); err != nil || len(hits) != 1 || string(hits[0].ID) != "M3" {
		t.Fatalf("search: %d hits, err=%v", len(hits), err)
	}

	if _, _, err := h.e.History(ctx, "nobody@s.whatsapp.net", "", 10); !errors.Is(err, core.ErrNoHistory) {
		t.Fatalf("a chat that is not stored: %v", err)
	}
	h.send(abi.Command{Tag: abi.CommandTagSetLocked, SetLocked: abi.CommandSetLocked{Chat: chatID(peer), Locked: true}})
	h.take()
	if page, _, err := h.e.History(ctx, chat, "", 10); !errors.Is(err, core.ErrNoHistory) || len(page) != 0 {
		t.Fatalf("a locked chat gave %d messages, err=%v", len(page), err)
	}
	if hits, err := h.e.SearchHistory(ctx, chat, "build", 10); !errors.Is(err, core.ErrNoHistory) || len(hits) != 0 {
		t.Fatalf("a locked chat's search gave %d hits, err=%v", len(hits), err)
	}
}
