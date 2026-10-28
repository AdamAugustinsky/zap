package conversation

import (
	"testing"

	"gonativeui/ui"

	"zapfast/abi"
)

// The line under the header lists only live pins: one past its expiry
// leaves although the core listed it, and a deleted message's pin leaves
// with it (CV-031, zf:src/ui/conversation.rs live_pins).
func TestExpiredAndDeletedPinsLeaveTheLine(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "p1", base0, false, "pin one"), msg(ada, "p2", base0+1, false, "pin two"), msg(ada, "p3", base0+2, false, "pin three"))
	r.f.Emit(ev(abi.EventTagPins, func(e *abi.Event) {
		e.Pins = abi.EventPins{Chat: []byte(ada), Batch: 1, Last: true}
		for i, exp := range []int64{r.now.Unix() + 3600, r.now.Unix() - 10, r.now.Unix() + 7200} {
			id := "p" + string(rune('1'+i))
			e.Pins.Items = append(e.Pins.Items, abi.Pinned{Chat: []byte(ada), ID: []byte(id), PinnedAt: int64(i + 1), ExpiresAt: exp, Text: []byte("pinned " + id)})
		}
	}))
	r.Frames(3)
	r.must(ui.RoleButton, "Pinned message 1 of 2: pinned p3")
	r.f.Emit(ev(abi.EventTagMessageDeleted, func(e *abi.Event) {
		e.MessageDeleted = abi.EventMessageDeleted{Chat: []byte(ada), ID: []byte("p3")}
	}))
	r.Frames(3)
	r.must(ui.RoleButton, "Pinned message 1 of 1: pinned p1")
}
