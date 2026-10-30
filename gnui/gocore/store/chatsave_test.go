package store

import (
	"database/sql"
	"errors"
	"testing"

	"zapfast/gocore/store/sqlite"
)

// SaveWithChat files a message and moves its chat together, or neither.
func TestSaveWithChatIsOneTransaction(t *testing.T) {
	s, _, _ := testStore(t)
	m := Message{ID: "M1", ChatID: "c@s.whatsapp.net", Text: "hello", Timestamp: 100, Status: "pending", FromMe: true}
	bump := func(c *Chat, stored []Message) {
		if len(stored) == 1 {
			c.LastActivity, c.Preview = stored[0].Timestamp, stored[0].Text
		}
	}

	// The COMMIT fails: no message, and no chat.
	restore := sqlite.InjectFaults(func(_ string, op sqlite.FaultOp) sqlite.Fault {
		if op == sqlite.FaultCommit {
			return sqlite.FaultIOErr
		}
		return sqlite.FaultNone
	})
	if err := s.SaveWithChat(m.ChatID, []Message{m}, bump); err == nil {
		t.Fatal("a failed commit reported success")
	}
	restore()
	if _, err := s.Message(m.ChatID, m.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("the message was filed: %v", err)
	}
	if _, err := s.Chat(m.ChatID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("the chat was filed: %v", err)
	}

	if err := s.SaveWithChat(m.ChatID, []Message{m}, bump); err != nil {
		t.Fatal(err)
	}
	c, err := s.Chat(m.ChatID)
	if err != nil || c.LastActivity != 100 || c.Preview != "hello" || c.ID != m.ChatID {
		t.Fatalf("chat %+v, %v", c, err)
	}
}

// A row filed live keeps its place among the messages of its second when
// history files it again with an order id; a row history brings first
// keeps history's order.
func TestHistoryKeepsALiveRowInPlace(t *testing.T) {
	s, _, _ := testStore(t)
	chat := "c@s.whatsapp.net"
	live := []Message{{ID: "A", ChatID: chat, Text: "a", Timestamp: 100}, {ID: "B", ChatID: chat, Text: "b", Timestamp: 100}}
	if err := s.SaveMessages(live); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMessages([]Message{{ID: "A", ChatID: chat, Text: "a", Timestamp: 100, Order: 7}, {ID: "C", ChatID: chat, Text: "c", Timestamp: 90, Order: 5}}); err != nil {
		t.Fatal(err)
	}
	page, _, err := s.Messages(chat, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	var ids string
	for _, m := range page {
		ids += m.ID
	}
	if ids != "CAB" {
		t.Fatalf("order %s, want CAB", ids)
	}
	if c, _ := s.Message(chat, "C"); c.Order != 5 {
		t.Fatalf("history's own row lost its order: %d", c.Order)
	}
}
