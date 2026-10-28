package conversation

import (
	"strconv"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/logic"
	"zapfast/state"
)

func lines(n int) []abi.Message {
	var msgs []abi.Message
	for i := 0; i < n; i++ {
		msgs = append(msgs, msg(ada, "q"+strconv.Itoa(i), base0+int64(i*60), false, "line "+strconv.Itoa(i)))
	}
	return msgs
}

// A jump from elsewhere (a starred message, a notification) goes through
// Panes.Jump: the transcript brings the message into view, flashes it and
// clears the request; asking again flashes it again.
func TestSharedJump(t *testing.T) {
	r := newRig(t)
	r.open(ada, lines(80)...)
	r.mustNot(ui.RoleGroup, bubble("Ada Lovelace", base0+3*60))
	if !r.s.OpenMessage([]byte(ada), []byte("q3")) {
		t.Fatal("OpenMessage refused")
	}
	r.Frames(6)
	r.must(ui.RoleGroup, bubble("Ada Lovelace", base0+3*60))
	p := &r.s.Panes
	if r.scr.flash != r.scr.t.ids[3] || p.Jump.Msg.Len() != 0 {
		t.Fatalf("flash %x (want %x), jump %d", r.scr.flash, r.scr.t.ids[3], p.Jump.Msg.Len())
	}
	// the jump's anchor is cleared (one left is the phone fetch's, from
	// the top of the history now in view)
	if p.Anchor.Len() > 0 && string(p.Anchor.At(0)) == "q3" {
		t.Error("the jump's anchor stayed")
	}
	at := r.scr.flashAt
	r.Frames(2)
	r.s.OpenMessage([]byte(ada), []byte("q3"))
	r.Frames(2)
	if r.scr.flashAt == at || p.Jump.Msg.Len() != 0 {
		t.Errorf("a second jump did not flash again (%d → %d)", at, r.scr.flashAt)
	}
}

// In selection mode the selection bar takes the composer's place at the foot
// of the conversation, and the composer comes back when it ends.
func TestSelectionBarTakesTheComposersPlace(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "s1", base0, false, "first"))
	r.must(ui.RoleLabel, "Composer")
	r.Play(widgettest.Click(ui.RoleButton, "More"), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "Select messages"), widgettest.Idle(2))
	if !r.s.Panes.Selecting {
		t.Fatal("not selecting")
	}
	r.mustNot(ui.RoleLabel, "Composer")
	bar := r.node(ui.RoleToolbar, "Select messages").Bounds
	list := r.node(ui.RoleList, "Messages").Bounds
	if bar[1] < list[3]-1 {
		t.Errorf("the bar %v is not under the transcript %v", bar, list)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Cancel selection"), widgettest.Idle(2))
	if r.s.Panes.Selecting {
		t.Fatal("still selecting")
	}
	r.must(ui.RoleLabel, "Composer")
}

// The menu's quick reactions start with the most used one and count it;
// "React with any emoji" opens the reaction picker for the message.
func TestMenuReactions(t *testing.T) {
	r := newRig(t)
	r.s.Settings.ReactionEmoji = []state.EmojiCount{{Emoji: []byte("🦀"), Count: 5}}
	r.open(ada, msg(ada, "m1", base0, false, "react"))
	name := bubble("Ada Lovelace", base0)
	r.Play(widgettest.RClick(ui.RoleGroup, name), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "🦀"), widgettest.Idle(2))
	got := r.sent(abi.CommandTagReact)
	if len(got) != 1 || string(got[0].React.Emoji) != "🦀" || string(got[0].React.Message) != "m1" {
		t.Fatalf("reacts %+v", got)
	}
	if re := r.s.Settings.ReactionEmoji; len(re) == 0 || string(re[0].Emoji) != "🦀" || re[0].Count != 6 {
		t.Errorf("reaction counts %+v", re)
	}
	r.Play(widgettest.RClick(ui.RoleGroup, name), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "React with any emoji"), widgettest.Idle(2))
	rt := &r.s.Panes.ReactTarget
	if !rt.Open() || string(rt.Msg.At(0)) != "m1" || rt.Chat != r.a.Open || rt.Anchor.Empty() {
		t.Errorf("reaction target %+v", rt)
	}
	r.mustNot(ui.RoleButton, "Copy message ID") // the menu closed
}

// The header places calls through ui/calls; while the chat has the live
// call, its button hangs up.
func TestHeaderCalls(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "h1", base0, false, "hi"))
	r.Play(widgettest.Click(ui.RoleButton, "Voice call"), widgettest.Idle(2))
	c := r.sent(abi.CommandTagStartCall)
	if len(c) != 1 || c[0].StartCall.Video || string(c[0].StartCall.Chat) != ada {
		t.Fatalf("StartCall %+v", c)
	}
	r.f.Emit(ev(abi.EventTagCall, func(e *abi.Event) {
		e.Call.Update = abi.CallUpdate{Generation: c[0].StartCall.Generation, Chat: []byte(ada), Direction: abi.CallDirectionOutgoing, Phase: abi.CallPhaseActive}
	}))
	r.Frames(3)
	r.mustNot(ui.RoleButton, "Voice call")
	r.Play(widgettest.Click(ui.RoleButton, "Hang up"), widgettest.Idle(2))
	if len(r.sent(abi.CommandTagHangupCall)) != 1 {
		t.Errorf("sent %v", r.f.SentTags())
	}
}

// In a narrow window the search pane covers the conversation: it shows
// Back, and picking a hit folds it away so the message shows; Search brings
// it back with the query and its hits.
func TestNarrowSearchFolds(t *testing.T) {
	r := newRigSized(t, 640, 760)
	r.open(ada, lines(50)...)
	r.s.Req.FindInChat = true
	r.Frames(3)
	if !r.s.Panes.Covering {
		t.Fatal("the pane does not cover a narrow window")
	}
	r.must(ui.RoleButton, "Back")
	r.mustNot(ui.RoleButton, "Close search")
	r.Play(widgettest.Type("line 2"), widgettest.Idle(2))
	r.f.Emit(ev(abi.EventTagChatHits, func(e *abi.Event) {
		e.ChatHits = abi.EventChatHits{Chat: []byte(ada), Query: []byte("line 2"), Batch: 1, Last: true, Messages: []abi.Message{msg(ada, "q2", base0+120, false, "line 2")}}
	}))
	r.Frames(3)
	r.Play(widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(6))
	if r.s.Panes.Pane != state.PaneNone || r.scr.flash != r.scr.t.ids[2] {
		t.Fatalf("pane %v, flash %x", r.s.Panes.Pane, r.scr.flash)
	}
	r.must(ui.RoleLabel, "line 2")
	r.s.Req.FindInChat = true
	r.Frames(3)
	if q := r.scr.h.search.buf.Bytes(); string(q) != "line 2" || len(r.scr.h.search.hits) != 1 {
		t.Errorf("the search came back as %q with %d hits", q, len(r.scr.h.search.hits))
	}
	r.Play(widgettest.Click(ui.RoleButton, "Back"), widgettest.Idle(2))
	if r.s.Panes.Pane != state.PaneNone {
		t.Errorf("Back left the pane %v", r.s.Panes.Pane)
	}
}

// The menu's viewer item opens the message, not a card.
func TestMenuOpensTheViewer(t *testing.T) {
	r := newRig(t)
	p := msg(ada, "v1", base0, false, "")
	p.Content = abi.Content{Tag: abi.ContentTagImage}
	p.Content.Image.Media = abi.Media{Mime: []byte("image/jpeg"), Size: 10, Width: 40, HasWidth: true, Height: 30, HasHeight: true,
		Path: []byte("/nonexistent/v1.jpg"), HasPath: true}
	r.open(ada, p)
	r.Play(widgettest.RClick(ui.RoleGroup, bubble("Ada Lovelace", base0)), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "Open in the viewer"), widgettest.Idle(2))
	// (the shell takes Want; the rig has no viewer window)
	w := &r.s.Win
	if w.ViewerHasCard || w.ViewerMsg.Len() != 1 || string(w.ViewerMsg.At(0)) != "v1" || w.ViewerChat != r.a.Open {
		t.Errorf("viewer card %v msg %d chat %v", w.ViewerHasCard, w.ViewerMsg.Len(), w.ViewerChat)
	}
}

// longDate is a day's accessible name in the calendar.
func longDate(y int, m time.Month, d int) string {
	return string(format.AppendLongDate(nil, i18n.Current(), time.Date(y, m, d, 12, 0, 0, 0, time.Local)))
}

// The search's day filter (CS-004): the calendar picks a day of this month
// or one before (none to come), the search lists that day's messages, of
// the query or all of them, and the chip's cross clears it.
func TestSearchDayFilter(t *testing.T) {
	r := newRig(t) // today is Sunday 25 October 2026
	r.open(ada, lines(5)...)
	r.s.Req.FindInChat = true
	r.Frames(3)
	r.Play(widgettest.Click(ui.RoleButton, "Filter by date"), widgettest.Idle(2))
	r.must(ui.RoleButton, "Previous month")
	r.mustNot(ui.RoleButton, "Next month")
	if n := r.node(ui.RoleButton, longDate(2026, 10, 26)); n.State&ui.SemDisabled == 0 {
		t.Error("a day to come can be picked")
	}
	r.Play(widgettest.Click(ui.RoleButton, longDate(2026, 10, 15)), widgettest.Idle(3))
	from, until := format.DayBounds(2026, 10, 15, time.Local)
	got := r.sent(abi.CommandTagSearchChatMessages)
	if len(got) != 1 {
		t.Fatalf("searches %d", len(got))
	}
	if sc := got[0].SearchChatMessages; len(sc.Query) != 0 || !sc.HasFrom || !sc.HasUntil || sc.From != from || sc.Until != until {
		t.Fatalf("search %+v (want %d…%d)", sc, from, until)
	}
	r.mustNot(ui.RoleButton, "Previous month") // picking closes the calendar
	r.must(ui.RoleButton, "Filter by date: "+longDate(2026, 10, 15))

	// an answer for another range is not this search's
	hit := func(id string, ranged bool) {
		r.f.Emit(ev(abi.EventTagChatHits, func(e *abi.Event) {
			e.ChatHits = abi.EventChatHits{Chat: []byte(ada), Batch: 1, Last: true, Messages: []abi.Message{msg(ada, id, base0, false, "line "+id)}}
			if ranged {
				e.ChatHits.From, e.ChatHits.HasFrom, e.ChatHits.Until, e.ChatHits.HasUntil = from, true, until, true
			}
		}))
		r.Frames(3)
	}
	hit("stale", false)
	hit("q0", true)
	if h := r.scr.h.search.hits; len(h) != 1 || string(r.scr.h.search.at(h[0].id)) != "q0" {
		t.Fatalf("hits %d", len(h))
	}

	r.Play(widgettest.Click(ui.RoleSearchField, "Search"), widgettest.Type("line"), widgettest.Idle(3))
	got = r.sent(abi.CommandTagSearchChatMessages)
	if sc := got[len(got)-1].SearchChatMessages; string(sc.Query) != "line" || sc.From != from || !sc.HasFrom {
		t.Errorf("with a query %+v", sc)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Clear the day filter"), widgettest.Idle(3))
	got = r.sent(abi.CommandTagSearchChatMessages)
	if sc := got[len(got)-1].SearchChatMessages; string(sc.Query) != "line" || sc.HasFrom || sc.HasUntil {
		t.Errorf("cleared %+v", sc)
	}
	r.mustNot(ui.RoleButton, "Filter by date: "+longDate(2026, 10, 15))

	// a month before has the way back
	r.Play(widgettest.Click(ui.RoleButton, "Filter by date"), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "Previous month"), widgettest.Idle(2))
	r.must(ui.RoleButton, "Next month")
	r.must(ui.RoleButton, longDate(2026, 9, 30))
}

// A message deleted for us hides at once and its neighbours join up; one
// deleted for everyone reads as deleted before the core answers.
func TestDeletesShowAtOnce(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "d1", base0, false, "one"), msg(ada, "d2", base0+60, false, "two"), msg(ada, "d3", base0+120, true, "three"))
	r.must(ui.RoleGroup, bubble("Ada Lovelace", base0+60))
	logic.DeleteForMe(r.s, r.a, []byte("d2"))
	r.Frames(3)
	r.mustNot(ui.RoleGroup, bubble("Ada Lovelace", base0+60))
	r.must(ui.RoleGroup, bubble("Ada Lovelace", base0))
	if len(r.sent(abi.CommandTagDeleteLocal)) != 1 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	logic.DeleteForEveryone(r.s, r.a, []byte("d3"))
	r.Frames(3)
	r.must(ui.RoleLabel, "This message was deleted")
	r.mustNot(ui.RoleLabel, "three")
}

// An unpinned message leaves the bar at once; a new pin lists the chat's
// pins again.
func TestPinBarFollowsTheMarks(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "p1", base0, false, "pin me"))
	pins := func(ids ...string) {
		r.f.Emit(ev(abi.EventTagPins, func(e *abi.Event) {
			e.Pins = abi.EventPins{Chat: []byte(ada), Batch: 1, Last: true}
			for i, id := range ids {
				e.Pins.Items = append(e.Pins.Items, abi.Pinned{Chat: []byte(ada), ID: []byte(id), PinnedAt: int64(i + 1), Text: []byte("pinned " + id)})
			}
		}))
		r.Frames(3)
	}
	pins("p1", "p2")
	r.must(ui.RoleButton, "Pinned message 1 of 2: pinned p2")
	r.f.Emit(ev(abi.EventTagPinChanged, func(e *abi.Event) {
		e.PinChanged = abi.EventPinChanged{Chat: []byte(ada), Message: []byte("p2")}
	}))
	r.Frames(3)
	r.must(ui.RoleButton, "Pinned message 1 of 1: pinned p1")
	before := len(r.sent(abi.CommandTagLoadPins))
	r.f.Emit(ev(abi.EventTagPinChanged, func(e *abi.Event) {
		e.PinChanged = abi.EventPinChanged{Chat: []byte(ada), Message: []byte("p3"), Pinned: true}
	}))
	r.Frames(3)
	if len(r.sent(abi.CommandTagLoadPins)) != before+1 {
		t.Errorf("a new pin did not list the pins again: %v", r.f.SentTags())
	}
}
