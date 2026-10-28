package chatlist

import (
	"testing"

	"zapfast/abi"
)

// The framework's layout audit finds nothing on the list: rows with ticks
// (one and two) and chips with counts, at 1x and at 2x.
func TestLayoutAuditIsCleanOnTheList(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		r := newRigScale(t, scale)
		r.f.Emit(ev(abi.EventTagMe, func(e *abi.Event) { e.Me = abi.EventMe{ID: jid("5511999"), Name: []byte("Dana"), HasName: true} }))
		un := chat("1", "Ada", 40)
		un.Unread = 3
		un.HasLast, un.Last = true, textMsg("1@s.whatsapp.net", false, "hello")
		rd := chat("2", "Grace", 30)
		rd.HasLast, rd.Last = true, textMsg("me", true, "read")
		rd.Last.Status = abi.DeliveryRead
		sent := chat("3", "Linus", 20)
		sent.HasLast, sent.Last = true, textMsg("me", true, "sent")
		sent.Last.Status = abi.DeliverySent
		pend := chat("4", "Ken", 10)
		pend.Unread = 12
		pend.HasLast, pend.Last = true, textMsg("me", true, "pending")
		pend.Last.Status = abi.DeliveryPending
		r.chats(un, rd, sent, pend)
		r.Frames(3)
		r.AuditLayout("chat list at " + itoa(int(scale)) + "x")
	}
}
