package model_test

import (
	"fmt"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/model"
)

const ada = "1@s.whatsapp.net"

func picture(chatID, id string, ts int64) abi.Message {
	var m abi.Message
	m.ID, m.Chat, m.Sender, m.Timestamp = []byte(id), []byte(chatID), []byte(chatID), ts
	m.Content.Tag = abi.ContentTagImage
	m.Content.Image.Media = abi.Media{Mime: []byte("image/jpeg"), Size: 1000}
	return m
}

func history(s *model.Store, msgs ...abi.Message) *model.Conversation {
	s.Event(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(ada), Batch: 1, Last: true, Messages: msgs}
	}))
	return s.Conversation([]byte(ada))
}

func TestLabelsReplaceAtTheLastChunk(t *testing.T) {
	s := model.New()
	labels := func(batch uint32, last bool, names ...string) *abi.Event {
		return ev(abi.EventTagLabels, func(e *abi.Event) {
			e.Labels = abi.EventLabels{Batch: batch, Last: last}
			for _, n := range names {
				e.Labels.Labels = append(e.Labels.Labels, abi.Label{ID: []byte("id-" + n), Name: []byte(n), ColorHex: []byte("#00ff00")})
			}
		})
	}
	s.Event(labels(1, true, "Work", "Family"))
	gen := s.LabelGen()
	s.Event(labels(2, false, "New"))
	if s.Labels() != 2 || string(s.LabelAt(1).Name) != "Family" || s.LabelGen() != gen {
		t.Fatalf("a reload in progress replaced the labels: %d", s.Labels())
	}
	s.Event(labels(2, true, "Other"))
	if s.Labels() != 2 || string(s.LabelAt(0).Name) != "New" || string(s.LabelAt(1).Color) != "#00ff00" || s.FindLabel([]byte("id-Other")) != 1 {
		t.Errorf("after the reload: %d %q", s.Labels(), s.LabelAt(0).Name)
	}
}

func TestDraftsAndPresence(t *testing.T) {
	s := model.New()
	s.SetDraft([]byte(ada), []byte("typed here"))
	s.Event(ev(abi.EventTagDrafts, func(e *abi.Event) {
		e.Drafts = abi.EventDrafts{Batch: 1, Last: true, Drafts: []abi.Draft{
			{Chat: []byte(ada), Text: []byte("stored")}, {Chat: []byte("2@s.whatsapp.net"), Text: []byte("other")},
		}}
	}))
	if d := string(s.Draft([]byte(ada))); d != "typed here" {
		t.Errorf("the session's text lost to the stored one: %q", d)
	}
	if d := string(s.Draft([]byte("2@s.whatsapp.net"))); d != "other" {
		t.Errorf("stored draft %q", d)
	}
	s.SetDraft([]byte(ada), nil)
	if s.Draft([]byte(ada)) != nil {
		t.Error("an empty draft is kept")
	}

	if _, _, ok := s.Presence([]byte(ada)); ok {
		t.Error("presence before any")
	}
	s.Event(ev(abi.EventTagPresence, func(e *abi.Event) {
		e.Presence = abi.EventPresence{ID: []byte(ada), LastSeen: 1234, HasLastSeen: true}
	}))
	if on, seen, ok := s.Presence([]byte(ada)); on || seen != 1234 || !ok {
		t.Errorf("presence %v %d %v", on, seen, ok)
	}
	s.Event(ev(abi.EventTagPresence, func(e *abi.Event) { e.Presence = abi.EventPresence{ID: []byte(ada), Online: true} }))
	if on, seen, ok := s.Presence([]byte(ada)); !on || seen != 0 || !ok {
		t.Errorf("online %v %d %v", on, seen, ok)
	}
}

func TestSyncingAndPinLimit(t *testing.T) {
	s := model.New()
	if s.PinLimit() != 3 {
		t.Errorf("default pin limit %d", s.PinLimit())
	}
	s.Event(ev(abi.EventTagPinLimit, func(e *abi.Event) { e.PinLimit.Limit = 5 }))
	s.Event(ev(abi.EventTagSyncing, func(e *abi.Event) { e.Syncing.Syncing = true }))
	if on, _, ok := s.Syncing(); !on || ok || s.PinLimit() != 5 {
		t.Errorf("syncing %v %v, limit %d", on, ok, s.PinLimit())
	}
	s.Event(ev(abi.EventTagSyncProgress, func(e *abi.Event) { e.SyncProgress.Percent = 40 }))
	if on, p, ok := s.Syncing(); !on || p != 40 || !ok {
		t.Errorf("progress %v %d %v", on, p, ok)
	}
	s.Event(ev(abi.EventTagSyncing, func(e *abi.Event) { e.Syncing.Syncing = false }))
	if on, _, ok := s.Syncing(); on || ok {
		t.Error("still syncing")
	}
}

func TestStarsAndPins(t *testing.T) {
	s := model.New()
	history(s, text(ada, "a", "one", 1), text(ada, "b", "two", 2), text(ada, "c", "three", 3))
	stars := func(batch uint32, last bool, ids ...string) *abi.Event {
		return ev(abi.EventTagStars, func(e *abi.Event) {
			e.Stars = abi.EventStars{Chat: []byte(ada), Batch: batch, Last: last}
			for _, id := range ids {
				e.Stars.Ids = append(e.Stars.Ids, abi.MessageId{ID: []byte(id)})
			}
		})
	}
	s.Event(stars(1, false, "a"))
	s.Event(stars(1, true, "b"))
	if !s.Starred([]byte(ada), []byte("a")) || !s.Starred([]byte(ada), []byte("b")) || s.Starred([]byte(ada), []byte("c")) {
		t.Error("stars of one batch")
	}
	s.Event(stars(2, true, "c"))
	if s.Starred([]byte(ada), []byte("a")) || !s.Starred([]byte(ada), []byte("c")) {
		t.Error("a new batch did not replace the chat's stars")
	}
	s.Event(ev(abi.EventTagStarChanged, func(e *abi.Event) {
		e.StarChanged = abi.EventStarChanged{Chat: []byte(ada), Message: []byte("a"), Starred: true}
	}))
	if !s.Starred([]byte(ada), []byte("a")) {
		t.Error("StarChanged")
	}

	pin := func(id string) abi.Pinned {
		return abi.Pinned{Chat: []byte(ada), ID: []byte(id), Text: []byte("pinned " + id), ExpiresAt: 99}
	}
	s.Event(ev(abi.EventTagPins, func(e *abi.Event) {
		e.Pins = abi.EventPins{Chat: []byte(ada), Batch: 1, Last: true, Items: []abi.Pinned{pin("a"), pin("b")}}
	}))
	var p abi.Pinned
	if s.ChatPins([]byte(ada)) != 2 || !s.PinnedMessage([]byte(ada), []byte("b")) {
		t.Fatalf("pins %d", s.ChatPins([]byte(ada)))
	}
	if err := s.ChatPin([]byte(ada), 1, &p); err != nil || string(p.Text) != "pinned b" || p.ExpiresAt != 99 {
		t.Errorf("pin 1: %v %q", err, p.Text)
	}

	// A deleted message and a revoked one leave their stars and pins.
	s.Event(ev(abi.EventTagMessageDeleted, func(e *abi.Event) {
		e.MessageDeleted = abi.EventMessageDeleted{Chat: []byte(ada), ID: []byte("a")}
	}))
	revoked := text(ada, "c", "", 3)
	revoked.Content.Tag = abi.ContentTagRevoked
	s.Event(ev(abi.EventTagMessageUpdated, func(e *abi.Event) { e.MessageUpdated.Message = revoked }))
	if s.Starred([]byte(ada), []byte("a")) || s.Starred([]byte(ada), []byte("c")) || s.PinnedMessage([]byte(ada), []byte("a")) || s.ChatPins([]byte(ada)) != 1 {
		t.Errorf("after delete and revoke: pins %d", s.ChatPins([]byte(ada)))
	}
	if err := s.ChatPin([]byte(ada), 0, &p); err != nil || string(p.ID) != "b" {
		t.Errorf("the pin left: %q", p.ID)
	}
	s.Event(ev(abi.EventTagChatCleared, func(e *abi.Event) { e.ChatCleared = abi.EventChatCleared{Chat: []byte(ada), Through: 10} }))
	if s.ChatPins([]byte(ada)) != 0 || s.PinnedMessage([]byte(ada), []byte("b")) {
		t.Error("a cleared chat keeps its pins")
	}
}

// zf:src/privacy.rs:371-448.
func TestPrivacySnapshot(t *testing.T) {
	demo := func() *model.Store {
		s := model.New()
		s.Event(ev(abi.EventTagAccountPrivacy, func(e *abi.Event) {
			e.AccountPrivacy.Values = []abi.PrivacyValue{
				{Kind: abi.PrivacyKindLastSeen, Choice: abi.PrivacyChoiceExcept},
				{Kind: abi.PrivacyKindOnline, Choice: abi.PrivacyChoiceSameAsLastSeen},
				{Kind: abi.PrivacyKindProfile, Choice: abi.PrivacyChoiceContacts},
				{Kind: abi.PrivacyKindAbout, Choice: abi.PrivacyChoiceEveryone},
				{Kind: abi.PrivacyKindGroupAdd, Choice: abi.PrivacyChoiceContacts},
				{Kind: abi.PrivacyKindReadReceipts, Choice: abi.PrivacyChoiceEveryone},
				{Kind: abi.PrivacyKindCallAdd, Choice: abi.PrivacyChoiceEveryone},
			}
		}))
		return s
	}
	get := func(s *model.Store, k abi.PrivacyKind) abi.PrivacyChoice { c, _ := s.Privacy.Get(k); return c }

	// A failed write restores the confirmed value.
	s := demo()
	if !s.Privacy.BeginSet(abi.PrivacyKindProfile, abi.PrivacyChoiceNobody) || get(s, abi.PrivacyKindProfile) != abi.PrivacyChoiceNobody {
		t.Fatal("begin")
	}
	if s.Privacy.BeginSet(abi.PrivacyKindProfile, abi.PrivacyChoiceEveryone) {
		t.Error("a second write did not wait for the first")
	}
	s.Event(ev(abi.EventTagAccountPrivacyFailed, func(e *abi.Event) { e.AccountPrivacyFailed.Kind = abi.PrivacyKindProfile }))
	if get(s, abi.PrivacyKindProfile) != abi.PrivacyChoiceContacts || s.Privacy.Pending(abi.PrivacyKindProfile) {
		t.Error("the failure did not restore")
	}

	// A fetch during a write keeps it pending; a refusal restores the fetched value.
	s = demo()
	s.Privacy.BeginSet(abi.PrivacyKindProfile, abi.PrivacyChoiceNobody)
	s.Event(ev(abi.EventTagAccountPrivacy, func(e *abi.Event) {
		e.AccountPrivacy.Values = []abi.PrivacyValue{{Kind: abi.PrivacyKindProfile, Choice: abi.PrivacyChoiceEveryone}, {Kind: abi.PrivacyKindAbout, Choice: abi.PrivacyChoiceNobody}}
	}))
	if !s.Privacy.Pending(abi.PrivacyKindProfile) || get(s, abi.PrivacyKindProfile) != abi.PrivacyChoiceNobody || get(s, abi.PrivacyKindAbout) != abi.PrivacyChoiceNobody {
		t.Error("the fetch overrode the write")
	}
	s.Event(ev(abi.EventTagAccountPrivacyFailed, func(e *abi.Event) { e.AccountPrivacyFailed.Kind = abi.PrivacyKindProfile }))
	if get(s, abi.PrivacyKindProfile) != abi.PrivacyChoiceEveryone {
		t.Error("the refusal did not restore the fetched value")
	}

	// A failed fetch keeps the values but stops editing; a fetch is whole.
	s = demo()
	if !s.Privacy.Editable() {
		t.Error("not editable")
	}
	s.Event(ev(abi.EventTagAccountPrivacy, func(e *abi.Event) { e.AccountPrivacy.Failed = true }))
	if s.Privacy.Editable() || get(s, abi.PrivacyKindProfile) != abi.PrivacyChoiceContacts {
		t.Error("a failed fetch")
	}
	s.Event(ev(abi.EventTagAccountPrivacy, func(e *abi.Event) {
		e.AccountPrivacy.Values = []abi.PrivacyValue{{Kind: abi.PrivacyKindProfile, Choice: abi.PrivacyChoiceNobody}}
	}))
	if _, ok := s.Privacy.Get(abi.PrivacyKindLastSeen); ok || !s.Privacy.Editable() {
		t.Error("the fetch is not whole")
	}

	// Nothing is written for an unknown or unchanged kind.
	if model.New().Privacy.BeginSet(abi.PrivacyKindProfile, abi.PrivacyChoiceNobody) || demo().Privacy.BeginSet(abi.PrivacyKindProfile, abi.PrivacyChoiceContacts) {
		t.Error("began a write that changes nothing")
	}

	// The account's read receipts follow the phone.
	s = demo()
	s.Privacy.BeginSet(abi.PrivacyKindReadReceipts, abi.PrivacyChoiceNobody)
	s.Event(ev(abi.EventTagAccountPrivacySaved, func(e *abi.Event) { e.AccountPrivacySaved.Kind = abi.PrivacyKindReadReceipts }))
	if !s.ReceiptsOff() {
		t.Error("receipts on after turning them off")
	}
	s.Event(ev(abi.EventTagReceiptsPrivacy, func(e *abi.Event) { e.ReceiptsPrivacy.Disabled = false }))
	if s.ReceiptsOff() {
		t.Error("ReceiptsPrivacy")
	}
}

func TestAnswersKeepTheLatestWholeList(t *testing.T) {
	s := model.New()
	hits := func(batch uint32, last bool, ids ...string) *abi.Event {
		return ev(abi.EventTagSearchHits, func(e *abi.Event) {
			e.SearchHits = abi.EventSearchHits{Query: []byte("q"), Batch: batch, Last: last}
			for _, id := range ids {
				e.SearchHits.Messages = append(e.SearchHits.Messages, text(ada, id, "hit "+id, 1))
			}
		})
	}
	a := s.Answer(abi.EventTagSearchHits)
	s.Event(hits(1, false, "a", "b"))
	if a.Has() {
		t.Error("an answer before its last chunk")
	}
	s.Event(hits(1, true, "c"))
	if !a.Has() || a.Len() != 3 || a.Chunks() != 2 {
		t.Fatalf("answer: %d items in %d chunks", a.Len(), a.Chunks())
	}
	var got []string
	for k := range a.Len() {
		e, j := a.Item(k)
		got = append(got, string(e.SearchHits.Messages[j].ID))
	}
	if fmt.Sprint(got) != "[a b c]" || string(a.Event().SearchHits.Query) != "q" {
		t.Errorf("items %v", got)
	}
	gen := a.Gen()
	s.Event(hits(2, false, "d"))
	if a.Len() != 3 || a.Gen() != gen {
		t.Error("the next batch showed before its end")
	}
	s.Event(hits(2, true))
	if a.Len() != 1 || a.Gen() == gen {
		t.Errorf("the next batch: %d", a.Len())
	}

	s.Event(ev(abi.EventTagReceipts, func(e *abi.Event) {
		e.Receipts = abi.EventReceipts{Chat: []byte(ada), Message: []byte("m"), Recipients: []abi.Recipient{{ID: []byte("x"), ReadAt: 5, HasReadAt: true}}}
	}))
	if r := s.Answer(abi.EventTagReceipts).Event(); r == nil || string(r.Receipts.Message) != "m" || r.Receipts.Recipients[0].ReadAt != 5 {
		t.Errorf("receipts %+v", r)
	}
	s.Event(ev(abi.EventTagCall, func(e *abi.Event) {
		e.Call.Update = abi.CallUpdate{Generation: 7, Chat: []byte(ada), Phase: abi.CallPhaseRinging}
	}))
	if c := s.CallUpdate(); c == nil || c.Generation != 7 || !model.CallLive(c.Phase) {
		t.Errorf("call %+v", c)
	}
}

func TestDownloads(t *testing.T) {
	s := model.New()
	v := history(s, picture(ada, "p", 1))
	s.MarkDownloading([]byte(ada), []byte("p"), model.PartMain)
	if md, _ := v.At(0).Media(); md.State != abi.MediaStateTagDownloading {
		t.Errorf("state %v", md.State)
	}
	// An update (a reaction) keeps the download's state.
	upd := picture(ada, "p", 1)
	upd.Reactions = []abi.Reaction{{Sender: []byte("x"), Emoji: []byte("👍")}}
	s.Event(ev(abi.EventTagMessageUpdated, func(e *abi.Event) { e.MessageUpdated.Message = upd }))
	if md, _ := v.At(0).Media(); md.State != abi.MediaStateTagDownloading || v.At(0).Reactions() != 1 {
		t.Errorf("after an update: %v", md.State)
	}
	s.Event(ev(abi.EventTagMedia, func(e *abi.Event) {
		e.Media = abi.EventMedia{Chat: []byte(ada), Message: []byte("p"), Result: abi.PathResult{Ok: abi.PathResultOk{Path: []byte("/cache/media/p.jpg")}}}
	}))
	md, _ := v.At(0).Media()
	if md.State != abi.MediaStateTagIdle || string(md.Path) != "/cache/media/p.jpg" {
		t.Errorf("downloaded: %v %q", md.State, md.Path)
	}
	if p, st, _, ok := s.File([]byte(ada), []byte("p"), model.PartMain); !ok || st != abi.MediaStateTagIdle || string(p) != "/cache/media/p.jpg" {
		t.Errorf("file %q %v %v", p, st, ok)
	}
	// An archived item the transcript has not loaded still has its result.
	s.Event(ev(abi.EventTagMedia, func(e *abi.Event) {
		e.Media = abi.EventMedia{Chat: []byte(ada), Message: []byte("old"), Result: abi.PathResult{Tag: abi.PathResultTagErr, Err: abi.PathResultErr{Error: []byte("gone")}}}
	}))
	if _, st, why, ok := s.File([]byte(ada), []byte("old"), model.PartMain); !ok || st != abi.MediaStateTagFailed || string(why) != "gone" {
		t.Errorf("failed file %v %q", st, why)
	}
	// Clearing the cache forgets the files under it.
	s.CacheDir = []byte("/cache/media/")
	s.Event(ev(abi.EventTagStorageStats, func(e *abi.Event) {
		e.StorageStats = abi.EventStorageStats{Token: s.Storage.Clear(), Cleared: true, HasStats: true}
	}))
	if md, _ := v.At(0).Media(); len(md.Path) != 0 {
		t.Errorf("a cleared file is still there: %q", md.Path)
	}
	if _, _, _, ok := s.File([]byte(ada), []byte("p"), model.PartMain); ok || s.Storage.Clearing || !s.Storage.Has {
		t.Error("the cleared file's record, or the storage state")
	}
}

func TestPagingState(t *testing.T) {
	s := model.New()
	s.Now = func() time.Time { return time.Unix(100, 0) }
	v := history(s, text(ada, "a", "one", 1))
	v.SetFetchingPhone(true, true)
	s.Event(ev(abi.EventTagOlderFetched, func(e *abi.Event) { e.OlderFetched = abi.EventOlderFetched{Chat: []byte(ada), More: false} }))
	if on, _ := v.FetchingPhone(); on || !v.PhoneExhausted() || v.PhoneMisses() != 1 || v.PhoneAnswered() != time.Unix(100, 0).UnixNano() {
		t.Errorf("after a miss: exhausted %v misses %d", v.PhoneExhausted(), v.PhoneMisses())
	}
	v.SetLoadingOlder(true)
	s.Event(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(ada), Batch: 2, Last: true, Older: true, Complete: true, Messages: []abi.Message{text(ada, "z", "zero", 0)}}
	}))
	if v.LoadingOlder() || !v.Complete() || v.Len() != 2 {
		t.Errorf("older page: loading %v complete %v", v.LoadingOlder(), v.Complete())
	}
	s.Event(ev(abi.EventTagOlderFetched, func(e *abi.Event) { e.OlderFetched = abi.EventOlderFetched{Chat: []byte(ada), More: true} }))
	if v.PhoneMisses() != 0 || v.Complete() {
		t.Errorf("a delivering answer: misses %d", v.PhoneMisses())
	}
}

func TestInFlightSets(t *testing.T) {
	s := model.New()
	s.Event(ev(abi.EventTagGroupSaving, func(e *abi.Event) { e.GroupSaving = abi.EventGroupSaving{Chat: []byte("g"), Saving: true} }))
	s.Event(ev(abi.EventTagInteractiveReplyState, func(e *abi.Event) {
		e.InteractiveReplyState = abi.EventInteractiveReplyState{Chat: []byte("c"), Message: []byte("m"), Pending: true}
	}))
	s.MarkPollVoting([]byte("c"), []byte("poll"))
	s.MarkPollCreating()
	if !s.GroupSaving([]byte("g")) || !s.InteractiveSending([]byte("c"), []byte("m")) || !s.PollVoting([]byte("c"), []byte("poll")) || !s.PollCreating() {
		t.Error("marks")
	}
	s.Event(ev(abi.EventTagPollVoted, func(e *abi.Event) { e.PollVoted = abi.EventPollVoted{Chat: []byte("c"), Message: []byte("poll")} }))
	s.Event(ev(abi.EventTagPollCreated, func(e *abi.Event) { e.PollCreated.Chat = []byte("c") }))
	s.Event(ev(abi.EventTagGroupSaving, func(e *abi.Event) { e.GroupSaving = abi.EventGroupSaving{Chat: []byte("g")} }))
	if s.GroupSaving([]byte("g")) || s.PollVoting([]byte("c"), []byte("poll")) || s.PollCreating() {
		t.Error("the answers did not end them")
	}
}

// A warm store applies presence, typing, downloads and answers without
// allocating.
func TestNewEventsDoNotAllocate(t *testing.T) {
	s := model.New()
	history(s, picture(ada, "p", 1))
	pres := ev(abi.EventTagPresence, func(e *abi.Event) { e.Presence = abi.EventPresence{ID: []byte(ada), Online: true} })
	media := ev(abi.EventTagMedia, func(e *abi.Event) {
		e.Media = abi.EventMedia{Chat: []byte(ada), Message: []byte("p"), Result: abi.PathResult{Ok: abi.PathResultOk{Path: []byte("/c/p.jpg")}}}
	})
	rc := ev(abi.EventTagReceipts, func(e *abi.Event) {
		e.Receipts = abi.EventReceipts{Chat: []byte(ada), Message: []byte("p"), Recipients: []abi.Recipient{{ID: []byte("x")}}}
	})
	run := func() {
		s.Event(pres)
		s.MarkDownloading([]byte(ada), []byte("p"), model.PartMain)
		s.Event(media)
		s.Event(rc)
		_ = s.Answer(abi.EventTagReceipts).Event()
		_, _, _ = s.Presence([]byte(ada))
	}
	for range 4000 {
		run()
	}
	if n := testing.AllocsPerRun(200, run); n != 0 {
		t.Errorf("%.1f allocations per round", n)
	}
}

// MarkRead clears a chat's unread state at once (upstream's mark_read
// before the core confirms), without allocating.
func TestMarkReadClearsAtOnce(t *testing.T) {
	s := model.New()
	c := chat(ada, "Ada", 5)
	c.Unread, c.MarkedUnread = 2, true
	c.UnreadMention, c.HasUnreadMention = []byte("m1"), true
	s.Event(ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{c}} }))
	ref := s.Chats.Find([]byte(ada))
	g := s.Chats.Gen()
	if !s.Chats.MarkRead(ref) {
		t.Fatal("nothing to clear")
	}
	v := s.Chats.At(ref)
	if _, men := v.UnreadMention(); v.Unread() != 0 || v.MarkedUnread() || men || s.Chats.Gen() == g {
		t.Errorf("unread %d marked %v mention %v", v.Unread(), v.MarkedUnread(), men)
	}
	if s.Chats.MarkRead(ref) || s.Chats.MarkRead(0) {
		t.Error("a read chat changed again")
	}
	up := ev(abi.EventTagChatUpdated, func(e *abi.Event) { u := chat(ada, "Ada", 6); u.Unread = 1; e.ChatUpdated.Chat = u })
	run := func() {
		s.Event(up)
		s.Chats.MarkRead(ref)
	}
	for range 100 {
		run()
	}
	if n := testing.AllocsPerRun(200, run); n != 0 {
		t.Errorf("%.1f allocations per message read at once", n)
	}
}

// Typers lists everyone typing in a group, in upstream's order (a refresh
// goes to the end), drops who stopped or lapsed, and allocates nothing.
func TestTypersListsEveryone(t *testing.T) {
	const group = "9@g.us"
	s := model.New()
	clock := time.Unix(1_800_000_000, 0)
	s.Now = func() time.Time { return clock }
	s.Event(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{chat(group, "Team", 5)}}
	}))
	ref := s.Chats.Find([]byte(group))
	typing := func(who string, on bool) {
		s.Event(ev(abi.EventTagTyping, func(e *abi.Event) {
			e.Typing = abi.EventTyping{Chat: []byte(group), Sender: []byte(who), Composing: on}
		}))
	}
	got := func() string {
		var out []string
		for _, w := range s.Typers(ref, nil) {
			out = append(out, string(w))
		}
		return fmt.Sprint(out)
	}
	typing("a", true)
	typing("b", true)
	typing("c", true)
	if g := got(); g != "[a b c]" {
		t.Errorf("typers %s", g)
	}
	typing("a", true) // a refresh goes to the end
	typing("b", false)
	if g := got(); g != "[c a]" {
		t.Errorf("typers %s, want [c a]", g)
	}
	if who, n := s.Typing(ref); n != 2 || string(who) != "c" {
		t.Errorf("Typing %q %d", who, n)
	}
	clock = clock.Add(model.TypingTimeout)
	if g := got(); g != "[]" {
		t.Errorf("lapsed typers %s", g)
	}
	clock = clock.Add(time.Second)
	typing("a", true)
	typing("c", true)
	buf := make([][]byte, 0, 8)
	if n := testing.AllocsPerRun(200, func() { buf = s.Typers(ref, buf[:0]) }); n != 0 || len(buf) != 2 {
		t.Errorf("%.1f allocations, %d typers", n, len(buf))
	}
}

// A pinned message that is deleted or revoked stops being a pin, in the
// line under the header and on its bubble. That the pin does not come back
// when the core lists it again is the archive's (zf:src/archive/pins.rs:80,
// pin_target_gone), upstream's own code (CV-031).
func TestAPinnedMessageThatIsRevokedStopsBeingAPin(t *testing.T) {
	const ada = "1@s.whatsapp.net"
	s := model.New()
	pin := func(id string) abi.Pinned {
		return abi.Pinned{Chat: []byte(ada), ID: []byte(id), Text: []byte("pinned " + id), ExpiresAt: 99}
	}
	s.Event(ev(abi.EventTagPins, func(e *abi.Event) {
		e.Pins = abi.EventPins{Chat: []byte(ada), Batch: 1, Last: true, Items: []abi.Pinned{pin("a"), pin("b"), pin("c")}}
	}))
	revoked := text(ada, "a", "", 3)
	revoked.Content.Tag = abi.ContentTagRevoked
	s.Event(ev(abi.EventTagMessageUpdated, func(e *abi.Event) { e.MessageUpdated.Message = revoked }))
	s.Event(ev(abi.EventTagMessageDeleted, func(e *abi.Event) {
		e.MessageDeleted = abi.EventMessageDeleted{Chat: []byte(ada), ID: []byte("b")}
	}))
	var p abi.Pinned
	if n := s.ChatPins([]byte(ada)); n != 1 || s.ChatPin([]byte(ada), 0, &p) != nil || string(p.ID) != "c" {
		t.Fatalf("%d pins left, the first %q", n, p.ID)
	}
	if s.PinnedMessage([]byte(ada), []byte("a")) || s.PinnedMessage([]byte(ada), []byte("b")) || !s.PinnedMessage([]byte(ada), []byte("c")) {
		t.Error("the pin marks do not follow the pins")
	}
}
