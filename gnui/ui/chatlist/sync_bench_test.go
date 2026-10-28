package chatlist

import (
	"testing"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/state"
	"zapfast/ui/screen"
)

// This archive is synthetic, not a copy of any account. Setup and event
// ingestion are outside the timer: one operation is sync after one event.
func syncArchive() (*Screen, *screen.Env, []abi.Chat) {
	st := state.New()
	a := st.AddAccount("1", nil, nil)
	cs := make([]abi.Chat, 500)
	for i := range cs {
		c := chat(itoa(i+1), "Contact "+itoa(i+1), int64(500-i))
		c.Unread = uint32(i % 4)
		c.MarkedUnread = i%17 == 0
		c.Pinned, c.PinnedAt = i%41 == 0, int64(i)
		c.Archived, c.Locked = i%11 == 0, i%19 == 0
		c.Favorite, c.FavoritePosition = i%3 == 0, uint32(500-i)
		c.HasMutedUntil, c.MutedUntil = i%7 == 0, -1
		if i%5 == 0 {
			c.Kind = abi.ChatKindGroup
		} else if i%13 == 0 {
			c.ID = []byte(itoa(i+1) + "@newsletter")
		}
		c.Labels = [][]byte{[]byte("l" + itoa(i%4))}
		c.HasLast = true
		c.Last = abi.LastMessage{Summary: []byte("A synthetic preview"), FromMe: true, Status: abi.DeliverySent}
		cs[i] = c
	}
	a.Model.Event(&abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: 1, Last: true, Chats: cs}})
	var labels []abi.Label
	for i := 0; i < 4; i++ {
		labels = append(labels, abi.Label{ID: []byte("l" + itoa(i)), Name: []byte("Folder " + itoa(i))})
	}
	a.Model.Event(&abi.Event{Tag: abi.EventTagLabels, Labels: abi.EventLabels{Batch: 1, Last: true, Labels: labels}})
	return New(), &screen.Env{S: st}, cs
}

var syncViews = []string{"all", "unread", "private", "favorites", "groups", "channels", "label", "archived", "locked", "search"}
var syncEvents = []string{"typing", "message", "receipt", "rename", "mute", "pin", "archive"}

func syncView(s *Screen, e *screen.Env, view string) {
	a := e.S.Acct()
	switch view {
	case "unread":
		a.Filter = state.FilterUnread
	case "private":
		a.Filter = state.FilterPrivate
	case "favorites":
		a.Filter = state.FilterFavorites
	case "groups":
		a.Filter = state.FilterGroups
	case "channels":
		a.Filter = state.FilterChannels
	case "label":
		a.Filter = state.FilterLabel
		a.Label.Add([]byte("l1"))
	case "archived":
		a.Mode = state.ListArchived
	case "locked":
		a.Mode = state.ListLocked
	case "search":
		s.query, s.needle, s.needleB = "contact", format.FoldKey("contact"), []byte("contact")
	}
	s.sync(e, a)
}

func syncEvent(e *screen.Env, c *abi.Chat, event string, i int) {
	a := e.S.Acct()
	if event == "typing" {
		a.Model.Event(&abi.Event{Tag: abi.EventTagTyping, Typing: abi.EventTyping{Chat: c.ID, Sender: c.ID, Composing: i%2 == 0}})
	} else {
		switch event {
		case "message":
			// Alternate between the original slot and the newest activity.
			c.LastActivity = int64(499 + i%2*1000)
			c.Unread = uint32(1 + i%2)
		case "receipt":
			c.Last.Status = abi.DeliverySent
			if i%2 == 0 {
				c.Last.Status = abi.DeliveryRead
			}
		case "rename":
			c.Name = []byte("Renamed contact")
			if i%2 == 0 {
				c.Name = []byte("Contact 2")
			}
		case "mute":
			c.HasMutedUntil = !c.HasMutedUntil
		case "pin":
			c.Pinned, c.PinnedAt = !c.Pinned, 1000
		case "archive":
			c.Archived = !c.Archived
		}
		a.Model.Event(&abi.Event{Tag: abi.EventTagChatUpdated, ChatUpdated: abi.EventChatUpdated{Chat: *c}})
	}
}

func BenchmarkSync500(b *testing.B) {
	for _, view := range syncViews {
		for _, event := range syncEvents {
			b.Run(view+"/"+event, func(b *testing.B) {
				s, e, cs := syncArchive()
				syncView(s, e, view)
				a := e.S.Acct()
				var rebuilds uint64
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					syncEvent(e, &cs[1], event, i)
					before := s.version
					b.StartTimer()
					s.sync(e, a)
					b.StopTimer()
					rebuilds += uint64(s.version - before)
				}
				// Each rebuild runs one recount and one collect/filter pass.
				b.ReportMetric(float64(rebuilds)/float64(b.N), "rebuilds/op")
			})
		}
	}
}

func TestSync500SteadyAllocs(t *testing.T) {
	for _, view := range syncViews {
		t.Run(view, func(t *testing.T) {
			s, e, _ := syncArchive()
			syncView(s, e, view)
			if n := testing.AllocsPerRun(100, func() { s.sync(e, e.S.Acct()) }); n != 0 {
				t.Fatalf("steady sync: %v allocations", n)
			}
		})
	}
}
