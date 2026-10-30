package wa

import (
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/gocore/store"
)

// A send whose acknowledgement died with the process (its row still pending
// at the next start) is failed under its original id at the next open,
// shown as failed when the chat loads, and never put on the wire again by
// the engine: not on the open, not when the link comes up, not on a later
// start. The user decides; main has no retry command yet, so the original
// id is all a retry could reuse.
func TestACrashedSendFailsUnderItsIDAndIsNeverResent(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.send(abi.Command{Tag: abi.CommandTagSendText, SendText: abi.CommandSendText{Chat: chatID(peer), Text: []byte("in flight at the crash")}})
	evs := h.take()
	id := string(evs[0].Messages.Messages[0].ID)
	if h.f.sentCount() != 1 || h.f.lastSent().id != id {
		t.Fatalf("the send did not reach the wire once under %q", id)
	}
	// The process ends before the server's answer is filed: the row is as
	// SendText left it, pending.
	if err := h.e.arch.UpdateMessage(peer.String(), id, func(m *store.Message) { m.Status = "pending" }); err != nil {
		t.Fatal(err)
	}
	if err := h.c.Close(10 * time.Second); err != nil {
		t.Fatal(err)
	}

	for run := 2; run <= 3; run++ {
		again := newHarness(t, options{keys: h.keys, state: h.state, cache: h.cache})
		again.start()
		again.wait(50 * time.Millisecond) // the engine's timers after connecting
		m, err := again.e.arch.Message(peer.String(), id)
		if err != nil || m.Status != "failed" || m.Text != "in flight at the crash" {
			t.Fatalf("run %d: the row under %q is %+v (%v), want it failed", run, id, m, err)
		}
		rows, _, err := again.e.arch.Messages(peer.String(), "", 50)
		if err != nil {
			t.Fatal(err)
		}
		mine := 0
		for _, r := range rows {
			if r.FromMe {
				mine++
				if r.ID != id {
					t.Errorf("run %d: a row of ours under a new id %q", run, r.ID)
				}
			}
		}
		if mine != 1 {
			t.Errorf("run %d: %d rows of ours, want the one", run, mine)
		}
		again.send(abi.Command{Tag: abi.CommandTagLoadChat, LoadChat: abi.CommandLoadChat{Chat: chatID(peer)}})
		shown := false
		for _, msg := range messagesOf(again.take()) {
			if string(msg.ID) == id {
				shown = msg.Status == abi.DeliveryFailed
			}
		}
		if !shown {
			t.Errorf("run %d: the chat does not show %q as failed", run, id)
		}
		if n := again.f.sentCount(); n != 0 {
			t.Errorf("run %d: the engine sent %d messages on its own after the crash", run, n)
		}
		if err := again.c.Close(10 * time.Second); err != nil {
			t.Fatal(err)
		}
	}
}
