package chatlist

import (
	"slices"
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

func TestListsTheChatsAndOpensOne(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada Lovelace", 20), chat("2", "Grace Hopper", 10))
	if got := r.rows(); !slices.Equal(got, []string{"Ada Lovelace", "Grace Hopper"}) {
		t.Fatalf("rows %v", got)
	}
	r.Play(widgettest.Click(ui.RoleListItem, "Grace Hopper"), widgettest.Idle(2))
	a := r.acct()
	if c, ok := a.OpenChatView(); !ok || string(c.Name()) != "Grace Hopper" {
		t.Fatalf("open chat %v", a.Open)
	}
	if _, ok := r.lastCmd(abi.CommandTagLoadChat); !ok {
		t.Errorf("sent %v", r.f.SentTags())
	}
	if len(r.s.Visible) != 2 {
		t.Errorf("visible %v", r.s.Visible)
	}
}

// A row opens its chat as the button goes down, before it comes up; the
// pointer resting on a row loads its chat's messages ahead of the click, and
// passing over one does not.
func TestRowsOpenOnPressAndPrefetchWhereThePointerRests(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada Lovelace", 30), chat("2", "Grace Hopper", 20), chat("3", "Katherine Johnson", 10))
	a := r.acct()
	loads := func() (n int) {
		for _, c := range r.f.Sent() {
			if c.Tag == abi.CommandTagLoadChat {
				n++
			}
		}
		return n
	}
	r.Play(widgettest.Move(ui.RoleListItem, "Katherine Johnson"), widgettest.Idle(1),
		widgettest.Move(ui.RoleListItem, "Grace Hopper"), widgettest.Idle(10)) // 1 frame on Katherine, ~170 ms on Grace
	if n := loads(); n != 1 {
		t.Fatalf("%d LoadChat after resting on one row and passing another, want 1", n)
	}
	if c, _ := r.lastCmd(abi.CommandTagLoadChat); string(c.LoadChat.Chat) != string(jid("2")) || a.Open != 0 {
		t.Fatalf("prefetched %q, open %v", c.LoadChat.Chat, a.Open)
	}
	r.Play(widgettest.Down(ui.RoleListItem, "Grace Hopper"), widgettest.Idle(1))
	if c, ok := a.OpenChatView(); !ok || string(c.Name()) != "Grace Hopper" {
		t.Fatal("the chat did not open as the button went down")
	}
	r.Play(widgettest.Up(), widgettest.Idle(1))
}

// Pinned chats come first, then the latest activity; a new message moves its
// chat to the top without rebuilding the others.
func TestOrderFollowsPinsAndActivity(t *testing.T) {
	r := newRig(t)
	p := chat("3", "Pinned", 1)
	p.Pinned, p.PinnedAt = true, 5
	r.chats(chat("1", "Old", 10), chat("2", "New", 20), p)
	if got := r.rows(); !slices.Equal(got, []string{"Pinned", "New", "Old"}) {
		t.Fatalf("rows %v", got)
	}
	o := chat("1", "Old", 30)
	r.update(o)
	if got := r.rows(); !slices.Equal(got, []string{"Pinned", "Old", "New"}) {
		t.Fatalf("after a message: %v", got)
	}
}

func TestRowShowsWhatTheChatHolds(t *testing.T) {
	r := newRig(t)
	un := chat("1", "Ada", 30)
	un.Unread = 3
	un.HasUnreadMention, un.UnreadMention = true, jid("9")
	mk := chat("2", "Grace", 20)
	mk.MarkedUnread = true
	mu := chat("3", "Linus", 10)
	mu.Unread = 1
	r.chats(un, mk, mu)
	if got := r.value(ui.RoleListItem, "Ada"); got != "3 unread messages, mentioned you" {
		t.Errorf("Ada: %q", got)
	}
	if got := r.value(ui.RoleListItem, "Grace"); got != "unread" {
		t.Errorf("Grace: %q", got)
	}
	if got := r.value(ui.RoleListItem, "Linus"); got != "1 unread messages" {
		t.Errorf("Linus: %q", got)
	}
}

func TestLastMessageAndDraft(t *testing.T) {
	r := newRig(t)
	c := chat("1", "Ada", 10)
	c.HasLast, c.Last = true, abi.LastMessage{Summary: []byte("see you at nine"), FromMe: true}
	r.chats(c)
	r.f.Emit(ev(abi.EventTagDrafts, func(e *abi.Event) {
		e.Drafts = abi.EventDrafts{Batch: 1, Last: true, Drafts: []abi.Draft{{Chat: jid("1"), Text: []byte("on my way")}}}
	}))
	r.Frames(3)
	if got := r.value(ui.RoleListItem, "Ada"); got == "" {
		t.Fatalf("no value")
	} else if !contains(got, "on my way") {
		t.Errorf("the draft is not shown: %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestChipsFilterAndSecondClickReturnsToAll(t *testing.T) {
	r := newRig(t)
	un := chat("1", "Ada", 30)
	un.Unread = 2
	g := chat("2", "Team", 20)
	g.Kind = abi.ChatKindGroup
	r.chats(un, g, chat("3", "Grace", 10))
	r.Play(widgettest.Click(ui.RoleCheckbox, "Unread"), widgettest.Idle(2))
	if got := r.rows(); !slices.Equal(got, []string{"Ada"}) {
		t.Fatalf("Unread: %v", got)
	}
	r.Play(widgettest.Click(ui.RoleCheckbox, "Unread"), widgettest.Idle(2))
	if got := r.rows(); len(got) != 3 {
		t.Fatalf("a second click: %v", got)
	}
	r.Play(widgettest.Click(ui.RoleCheckbox, "Groups"), widgettest.Idle(2))
	if got := r.rows(); !slices.Equal(got, []string{"Team"}) {
		t.Fatalf("Groups: %v", got)
	}
	r.Play(widgettest.Click(ui.RoleCheckbox, "Favorites"), widgettest.Idle(2))
	if r.has(ui.RoleListItem, "Team") || !r.has(ui.RoleLabel, "No favorites yet") && !r.hasText("No favorites yet") {
		t.Fatalf("Favorites: %v", r.rows())
	}
}

// hasText reports whether any node's name or value is the text.
func (r *rig) hasText(s string) bool {
	t := r.Tree()
	for _, n := range t.Nodes {
		if string(t.Text(n.Name)) == s || string(t.Text(n.Value)) == s {
			return true
		}
	}
	return false
}

func TestSkeletonWhileLoading(t *testing.T) {
	r := newRig(t)
	if !r.hasText("Loading your chats") {
		t.Fatalf("no loading state")
	}
	r.chats(chat("1", "Ada", 1))
	if r.hasText("Loading your chats") {
		t.Fatalf("still loading")
	}
}

// The first frame shows a cached chat list as it is, with no skeleton and no
// loading words; skeleton rows come only when the list is not there yet
// (LG-014).
func TestTheCachedListNeedsNoSkeleton(t *testing.T) {
	r := newRigCached(t, 1, chat("1", "Ada", 2), chat("2", "Grace", 1))
	r.Frames(1) // the model applies the core's events at the frame's start
	if got := r.rows(); !slices.Equal(got, []string{"Ada", "Grace"}) {
		t.Fatalf("first frames: %v", got)
	}
	if r.hasText("Loading your chats") {
		t.Error("a skeleton over a cached list")
	}
	slow := newRigCached(t, 1)
	slow.Frames(1)
	if got := slow.rows(); len(got) != 0 || !slow.hasText("Loading your chats") {
		t.Errorf("no cache: rows %v, skeleton %v", got, slow.hasText("Loading your chats"))
	}
}

func TestEmptyStateForAnEmptyArchive(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 1))
	r.acct().Mode = state.ListArchived
	r.s.Touch()
	r.Frames(3)
	if !r.hasText("Nothing archived") {
		t.Fatalf("no empty state: %v", r.rows())
	}
	if r.has(ui.RoleListItem, "Ada") {
		t.Errorf("an unarchived chat is in the archive")
	}
}

func TestArchivedModeListsArchivedChats(t *testing.T) {
	r := newRig(t)
	ar := chat("2", "Old", 1)
	ar.Archived = true
	r.chats(chat("1", "Ada", 5), ar)
	if r.has(ui.RoleListItem, "Old") {
		t.Fatalf("archived chat in the main list")
	}
	r.acct().Mode = state.ListArchived
	r.s.Touch()
	r.Frames(3)
	if got := r.rows(); !slices.Equal(got, []string{"Old"}) {
		t.Fatalf("archive: %v", got)
	}
	if !r.has(ui.RoleButton, "Back") && !r.hasText("Archived") {
		t.Errorf("no way back")
	}
}

func searchFor(r *rig, q string) {
	r.Play(widgettest.Click(ui.RoleSearchField, "Search"), widgettest.Type(q), widgettest.Idle(3))
}

func TestSearchFindsChatsIncludingArchived(t *testing.T) {
	r := newRig(t)
	ar := chat("2", "Graham", 1)
	ar.Archived = true
	r.chats(chat("1", "Ada", 5), ar, chat("3", "Grace", 4))
	searchFor(r, "gra")
	got := r.rows()
	if !slices.Contains(got, "Grace") || !slices.Contains(got, "Graham") || slices.Contains(got, "Ada") {
		t.Fatalf("hits: %v", got)
	}
	if _, ok := r.lastCmd(abi.CommandTagSearchMessages); !ok {
		t.Errorf("the message search was not asked: %v", r.f.SentTags())
	}
}

func TestSearchWithNothingShowsNoResults(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 5))
	searchFor(r, "zzz")
	if !r.hasText("No results") {
		t.Fatalf("no empty state")
	}
}

func TestSearchEnterOpensTheFirstHit(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 5), chat("3", "Grace", 4))
	searchFor(r, "gra")
	r.Play(widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(2))
	if c, ok := r.acct().OpenChatView(); !ok || string(c.Name()) != "Grace" {
		t.Fatalf("open %v", r.acct().Open)
	}
}

func TestEscapeClearsTheSearch(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 5), chat("3", "Grace", 4))
	searchFor(r, "gra")
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	if got := r.rows(); len(got) != 2 {
		t.Fatalf("after Escape: %v", got)
	}
}

func TestMessageHitsListUnderMessages(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 5))
	searchFor(r, "lunch")
	r.f.Emit(ev(abi.EventTagSearchHits, func(e *abi.Event) {
		e.SearchHits = abi.EventSearchHits{Query: []byte("lunch"), Batch: 1, Last: true, Messages: []abi.Message{{
			Chat: jid("1"), ID: []byte("m1"), Timestamp: 100, Content: abi.Content{Tag: abi.ContentTagText, Text: abi.ContentText{Text: []byte("lunch at noon")}}}}}
	}))
	r.Frames(3)
	if !r.hasText("Messages") {
		t.Errorf("no Messages section")
	}
	if !r.has(ui.RoleListItem, "Ada") {
		t.Fatalf("no hit row: %v", r.rows())
	}
	r.Play(widgettest.Click(ui.RoleListItem, "Ada"), widgettest.Idle(2))
	if j := r.s.Panes.Jump; j.Msg.Len() != 1 || string(j.Msg.At(0)) != "m1" {
		t.Errorf("no jump to the message: %+v", j)
	}
	if c, ok := r.acct().OpenChatView(); !ok || string(c.Name()) != "Ada" {
		t.Errorf("chat not open")
	}
}

func TestStarredModeListsStarredMessages(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 5))
	r.acct().Mode = state.ListStarred
	r.s.Touch()
	r.Frames(3)
	if _, ok := r.lastCmd(abi.CommandTagLoadStarred); !ok {
		t.Errorf("the starred list was not asked for: %v", r.f.SentTags())
	}
	if !r.hasText("No starred messages") {
		t.Errorf("no empty state")
	}
	r.f.Emit(ev(abi.EventTagStarredList, func(e *abi.Event) {
		e.StarredList = abi.EventStarredList{Batch: 1, Last: true, Starred: []abi.Starred{{StarredAt: 9, Message: abi.Message{
			Chat: jid("1"), ID: []byte("m1"), Timestamp: 100,
			Content: abi.Content{Tag: abi.ContentTagText, Text: abi.ContentText{Text: []byte("remember the milk")}}}}}}
	}))
	r.Frames(3)
	if r.hasText("No starred messages") {
		t.Errorf("still empty")
	}
	if !r.has(ui.RoleListItem, "Ada") {
		t.Fatalf("no starred row: %v", r.rows())
	}
	r.Play(widgettest.Click(ui.RoleListItem, "Ada"), widgettest.Idle(2))
	if j := r.s.Panes.Jump; j.Msg.Len() != 1 || string(j.Msg.At(0)) != "m1" {
		t.Errorf("no jump to the starred message: %+v", j)
	}
}

func TestLockedModeHidesLockedChatsFromTheMainList(t *testing.T) {
	r := newRig(t)
	l := chat("2", "Secret", 5)
	l.Locked = true
	r.chats(chat("1", "Ada", 6), l)
	if r.has(ui.RoleListItem, "Secret") {
		t.Fatalf("a locked chat in the main list")
	}
	r.acct().Mode = state.ListLocked
	r.s.Touch()
	r.Frames(3)
	if got := r.rows(); len(got) != 1 || got[0] != "Secret" {
		t.Fatalf("locked folder: %v", got)
	}
}

// Once the list is built, a frame with nothing new allocates nothing.
func TestSteadyFramesAllocateNothing(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "drawn menus", true: "native menus"}[native], func(t *testing.T) { steadyFrames(t, native) })
	}
}

func steadyFrames(t *testing.T, native bool) {
	r := newRig(t)
	r.Context().SetNativeMenus(native)
	var cs []abi.Chat
	for i := 0; i < 30; i++ {
		c := chat(itoa(i+1), "Chat "+itoa(i), int64(100-i))
		c.Unread = uint32(i % 3)
		cs = append(cs, c)
	}
	r.f.Emit(ev(abi.EventTagLabels, func(e *abi.Event) {
		e.Labels = abi.EventLabels{Batch: 1, Last: true, Labels: []abi.Label{{ID: []byte("l1"), Name: []byte("Work"), ColorHex: []byte("#3b82f6")}}}
	}))
	r.chats(cs...)
	r.Frames(5)
	if n := testing.AllocsPerRun(20, func() { r.Frame() }); n != 0 {
		t.Errorf("%v allocations per frame", n)
	}
}

// With the list settled and nothing happening, the window asks for no frames.
func TestIdleListRequestsNoFrames(t *testing.T) {
	r := newRig(t)
	c := chat("1", "Ada", 10)
	c.Unread = 2
	c.Pinned, c.PinnedAt = true, 1
	c.HasLast, c.Last = true, abi.LastMessage{Summary: []byte("hi"), Status: abi.DeliveryRead, FromMe: true}
	r.chats(c, chat("2", "Grace", 5))
	r.Play(widgettest.Pause(1500))
	r.Frames(10)
	if r.Context().Animating() {
		t.Errorf("the idle list is still animating")
	}
}

// In the narrowest list the chips that do not fit are cut off, in a row that
// scrolls (its sideways scroll is proved live, TestLiveChipsScroll); the
// labels are a row of their own below it, ending in + for the manager.
func TestChipRowsScrollAndLabelsAreTheirOwnRow(t *testing.T) {
	r := newRig(t)
	r.s.Settings.SidebarWidth = 240
	r.f.Emit(ev(abi.EventTagLabels, func(e *abi.Event) {
		e.Labels = abi.EventLabels{Batch: 1, Last: true, Labels: []abi.Label{{ID: []byte("l1"), Name: []byte("Work"), ColorHex: []byte("#3b82f6")}}}
	}))
	r.chats(chat("1", "Ada", 1))
	if !r.chipShown("chip-filters", "All") || !r.chipShown("chip-filters", "More") || r.has(ui.RoleCheckbox, "Channels") {
		t.Fatalf("at 240 pt All shows and Channels folds into More")
	}
	filters := r.Must(ui.RoleScrollArea, "chip-filters")
	labels := r.Must(ui.RoleScrollArea, "label-chips")
	if labels.Bounds[1] < filters.Bounds[3] {
		t.Errorf("the label row %v is not below the filter row %v", labels.Bounds, filters.Bounds)
	}
	if !r.chipShown("label-chips", "Work") || !r.chipShown("label-chips", "Manage labels") {
		t.Errorf("the label chip and + show in their row")
	}
}

// The chips hide while searching, except in the locked folder.
func TestChipsHideWhileSearching(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 1))
	if !r.has(ui.RoleCheckbox, "Unread") {
		t.Fatalf("no chips")
	}
	searchFor(r, "a")
	if r.has(ui.RoleCheckbox, "Unread") {
		t.Errorf("chips while searching")
	}
}

// Neither scroll-bar style ("Always", which takes room, and automatic) draws a
// bar at rest under the chip rows, and the filters that do not fit fold into
// a More chip that fits.
func TestChipRowsShowNoScrollBarInEitherStyle(t *testing.T) {
	for _, always := range []bool{true, false} {
		name := map[bool]string{true: "always", false: "automatic"}[always]
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.s.Settings.SidebarWidth = 240
			r.Context().SetSystemValues(2, &platform.SystemValues{Known: true, LegacyScrollers: always})
			r.f.Emit(ev(abi.EventTagLabels, func(e *abi.Event) {
				e.Labels = abi.EventLabels{Batch: 1, Last: true, Labels: []abi.Label{{ID: []byte("l1"), Name: []byte("Work"), ColorHex: []byte("#3b82f6")}}}
			}))
			r.chats(chat("1", "Ada", 1))
			r.Frames(5)
			for _, row := range []string{"chip-filters", "label-chips"} {
				area := r.Must(ui.RoleScrollArea, row)
				chip := r.Must(ui.RoleCheckbox, "All")
				if row == "label-chips" {
					chip = r.Must(ui.RoleCheckbox, "Work")
				}
				if chip.Bounds[3] > area.Bounds[3]+0.5 {
					t.Errorf("%s: the chips (%v) do not fit the row (%v): a bar took its room", row, chip.Bounds, area.Bounds)
				}
				tr := r.Tree()
				for _, n := range tr.Nodes {
					if n.Role == ui.RoleScrollBar && n.Bounds[2] > area.Bounds[0] && n.Bounds[0] < area.Bounds[2] &&
						n.Bounds[3] > area.Bounds[1]-1 && n.Bounds[1] < area.Bounds[3]+40 {
						t.Errorf("%s: a scroll bar at %v under the chips", row, n.Bounds)
					}
				}
			}
			if r.has(ui.RoleCheckbox, "Channels") || !r.chipShown("chip-filters", "More") {
				t.Errorf("at 240 pt Channels is not folded into a More chip that fits")
			}
		})
	}
}

// SH-020: a chat too large for the ABI (EventDropped, kind Chats) is not in
// the model, so the list says that some could not be shown, under the chats,
// and only there: not for another kind of record, not in a search, a filter
// or the archive. It is no chat row.
func TestChatsTooLargeToShowGetAPlaceholder(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada Lovelace", 20), chat("2", "Grace Hopper", 10))
	const note = "Some chats could not be shown"
	drop := func(kind abi.EventTag) {
		r.f.Emit(ev(abi.EventTagEventDropped, func(e *abi.Event) { e.EventDropped.Kind, e.EventDropped.Size = uint16(kind), 2<<20 }))
		r.Frames(3)
	}
	drop(abi.EventTagMessages)
	if r.has(ui.RoleLabel, note) {
		t.Error("a dropped message put the note in the chat list")
	}
	drop(abi.EventTagChats)
	if !r.has(ui.RoleLabel, note) {
		t.Fatalf("no note under the chats:\n%s", widgettest.Dump(r.Tree()))
	}
	if got := r.rows(); !slices.Equal(got, []string{"Ada Lovelace", "Grace Hopper"}) {
		t.Errorf("rows %v", got)
	}
	// not in a filter
	r.Play(widgettest.Click(ui.RoleCheckbox, "Unread"), widgettest.Idle(3))
	if r.has(ui.RoleLabel, note) {
		t.Error("the note shows under a filter")
	}
}

// The filters that do not fit fold into a More chip: its menu picks one, and
// the chip then reads that filter's name, chosen; picking it again returns to
// every chat.
func TestFoldedFiltersPickFromMore(t *testing.T) {
	r := newRig(t)
	r.s.Settings.SidebarWidth = 240
	r.chats(chat("1", "Ada", 1))
	r.Play(widgettest.Click(ui.RoleCheckbox, "More"), widgettest.Idle(3))
	r.Play(widgettest.Click(ui.RoleMenuItem, "Groups"), widgettest.Idle(3))
	if r.acct().Filter != state.FilterGroups {
		t.Fatalf("the More menu chose filter %v, want Groups", r.acct().Filter)
	}
	if n, ok := r.Find(ui.RoleCheckbox, "Groups"); !ok || n.State&ui.SemChecked == 0 || r.has(ui.RoleCheckbox, "More") {
		t.Fatalf("the More chip does not read Groups, chosen")
	}
	r.Play(widgettest.Click(ui.RoleCheckbox, "Groups"), widgettest.Idle(3))
	r.Play(widgettest.Click(ui.RoleMenuItem, "Groups"), widgettest.Idle(3))
	if r.acct().Filter != state.FilterAll || !r.has(ui.RoleCheckbox, "More") {
		t.Errorf("picking Groups again left filter %v, want All", r.acct().Filter)
	}
}

// A folder's badge counts its unread chats but not a locked one, which would
// give it away.
func TestAFoldersBadgeLeavesOutLockedChats(t *testing.T) {
	r := newRig(t)
	l := chat("2", "Secret", 5)
	l.Locked, l.Unread = true, 3
	open := chat("1", "Ada", 6)
	open.Unread = 1
	r.chats(open, l)
	a := r.acct()
	i := a.FolderByID(a.NewFolder("Work", "briefcase"))
	for _, ch := range []abi.Chat{open, l} {
		a.MoveToFolder(ch.ID, i)
	}
	if n := folderUnread(a, &a.Settings.Folders[i]); n != 1 {
		t.Errorf("the folder's badge counts %d unread chats, want 1 (the locked one left out)", n)
	}
}

// ⌘K asks the archive for its own text in the answer slot the list's search
// uses: once it closes, the list asks for its text again, so its messages
// come back.
func TestTheListsSearchAsksAgainAfterSpotlight(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 5))
	searchFor(r, "lunch")
	before := len(r.sent())
	r.s.Spotlight = true
	r.acct().Model.Answer(abi.EventTagSearchHits).Clear() // ⌘K's own search
	r.s.Touch()
	r.Frames(2)
	r.s.Spotlight = false
	r.s.Touch()
	r.Frames(2)
	var asked []string
	for _, c := range r.sent()[before:] {
		if c.Tag == abi.CommandTagSearchMessages {
			asked = append(asked, string(c.SearchMessages.Query))
		}
	}
	if len(asked) != 1 || asked[0] != "lunch" {
		t.Fatalf("after ⌘K closed the list asked %q", asked)
	}
}
