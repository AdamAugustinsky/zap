package info_test

import (
	"strings"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

func photo(id, chat string, ts int64) abi.SharedEntry {
	return abi.SharedEntry{Tag: abi.SharedEntryTagMedia, Media: abi.SharedEntryMedia{Message: abi.Message{
		ID: b(id), Chat: b(chat), Timestamp: ts, Content: abi.Content{Tag: abi.ContentTagImage},
	}}}
}

func video(id, chat string, ts int64, seconds uint32) abi.SharedEntry {
	return abi.SharedEntry{Tag: abi.SharedEntryTagMedia, Media: abi.SharedEntryMedia{Message: abi.Message{
		ID: b(id), Chat: b(chat), Timestamp: ts,
		Content: abi.Content{Tag: abi.ContentTagVideo, Video: abi.ContentVideo{Seconds: seconds, HasSeconds: true}},
	}}}
}

func doc(id, chat, name string, ts int64, size uint64, pages uint32) abi.SharedEntry {
	d := abi.ContentDocument{FileName: b(name), Media: abi.Media{Size: size}}
	if pages > 0 {
		d.Pages, d.HasPages = pages, true
	}
	return abi.SharedEntry{Tag: abi.SharedEntryTagDoc, Doc: abi.SharedEntryDoc{Message: abi.Message{
		ID: b(id), Chat: b(chat), Timestamp: ts, Content: abi.Content{Tag: abi.ContentTagDocument, Document: d},
	}}}
}

func link(id, url, title string, ts int64) abi.SharedEntry {
	l := abi.SharedEntryLink{Message: b(id), Timestamp: ts, URL: b(url)}
	if title != "" {
		l.Title, l.HasTitle = b(title), true
	}
	return abi.SharedEntry{Tag: abi.SharedEntryTagLink, Link: l}
}

type answerOpts struct {
	cut     [3]bool
	failed  bool
	request uint64 // 0: the last one asked
}

// answer plays the core's answer to the last ListChatShared.
func (r *rig) answer(o answerOpts, entries ...abi.SharedEntry) {
	r.t.Helper()
	reqs := r.sent(abi.CommandTagListChatShared)
	if len(reqs) == 0 {
		r.t.Fatal("no ListChatShared was sent")
	}
	last := reqs[len(reqs)-1].ListChatShared
	id := last.Request
	if o.request != 0 {
		id = o.request
	}
	r.f.Emit(ev(abi.EventTagChatShared, func(e *abi.Event) {
		e.ChatShared = abi.EventChatShared{Request: id, Chat: last.Chat, Last: true, Entries: entries,
			MediaTruncated: o.cut[0], DocsTruncated: o.cut[1], LinksTruncated: o.cut[2]}
		if o.failed {
			e.ChatShared.HasError, e.ChatShared.Error = true, b("offline")
		}
	}))
	r.Frames(4)
}

// names are the accessible names of the nodes of role, in order.
func (r *rig) names(role ui.Role) []string {
	tr := r.Tree()
	var out []string
	for _, n := range tr.Nodes {
		if n.Role == role {
			out = append(out, string(tr.Text(n.Name)))
		}
	}
	return out
}

func (r *rig) wantValue(role ui.Role, name, value string) {
	r.t.Helper()
	n := r.Must(role, name)
	if got := string(r.Tree().Text(n.Value)); got != value {
		r.t.Errorf("%q has the value %q, want %q", name, got, value)
	}
}

func (r *rig) count(role ui.Role, prefix string) int {
	n := 0
	for _, s := range r.names(role) {
		if strings.HasPrefix(s, prefix) {
			n++
		}
	}
	return n
}

var epoch = time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local).Unix()

// IN-011: opening the panel asks for the listing of its chat, once.
func TestOpeningAsksForTheListing(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	reqs := r.sent(abi.CommandTagListChatShared)
	if len(reqs) != 1 || string(reqs[0].ListChatShared.Chat) != ada {
		t.Fatalf("ListChatShared %+v", reqs)
	}
	r.Frames(10)
	if len(r.sent(abi.CommandTagListChatShared)) != 1 {
		t.Error("the listing was asked for again with nothing changed")
	}
}

// IN-011: the media row opens the lists in tabs, newest first; a photo or a
// document goes to its message and a link opens the browser.
func TestMediaDocsAndLinks(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	r.answer(answerOpts{},
		photo("p1", ada, epoch), video("v1", ada, epoch+60, 75), photo("p2", ada, epoch+120),
		doc("d1", ada, "report.pdf", epoch+10, 1_500_000, 12),
		doc("d2", ada, "notes.txt", epoch+20, 800, 0),
		link("l1", "https://www.example.com/post?id=1", "A post", epoch+30),
		link("l2", "https://spotifast.rocks/", "", epoch+40),
	)
	r.click(ui.RoleButton, "Media, links and docs")
	if r.s.Panes.InfoView != state.InfoMedia {
		t.Fatalf("view %v", r.s.Panes.InfoView)
	}
	r.want(ui.RoleButton, "Back")
	r.want(ui.RoleTab, "Media 3")
	r.want(ui.RoleTab, "Docs 2")
	r.want(ui.RoleTab, "Links 2")
	if n := r.count(ui.RoleButton, "Photo, ") + r.count(ui.RoleButton, "Video, "); n != 3 {
		t.Errorf("%d cells\n%s", n, r.Dump())
	}

	r.click(ui.RoleTab, "Docs 2")
	if r.s.Panes.InfoView != state.InfoDocs {
		t.Fatalf("view %v", r.s.Panes.InfoView)
	}
	docs := r.names(ui.RoleButton)
	var shown []string
	for _, d := range docs {
		if strings.HasPrefix(d, "notes.txt") || strings.HasPrefix(d, "report.pdf") {
			shown = append(shown, d)
		}
	}
	if len(shown) != 2 || !strings.HasPrefix(shown[0], "notes.txt, 800 B · ") || !strings.HasPrefix(shown[1], "report.pdf, 12 pages · 1.4 MB · ") {
		t.Fatalf("docs newest first: %q", shown)
	}
	r.click(ui.RoleButton, shown[0])
	if c, _ := r.a.OpenChatView(); string(c.ID()) != ada {
		t.Error("the document's chat is not open")
	}
	if j := &r.s.Panes.Jump; j.Msg.Len() != 1 || string(j.Msg.At(0)) != "d2" {
		t.Errorf("the conversation was not asked to jump to the document's message")
	}
	if r.s.Panes.Pane != state.PaneInfo {
		t.Error("a wide window's panel closed on a jump")
	}

	r.click(ui.RoleTab, "Links 2")
	var links []string
	for _, n := range r.names(ui.RoleButton) {
		if strings.Contains(n, " · ") {
			links = append(links, n)
		}
	}
	if len(links) != 2 || !strings.HasPrefix(links[0], "https://spotifast.rocks/, spotifast.rocks · ") || !strings.HasPrefix(links[1], "A post, example.com · ") {
		t.Fatalf("links: %q", links)
	}
	r.click(ui.RoleButton, links[1])
	if got := r.sf.Opened(); len(got) != 0 {
		// opened on a goroutine
		t.Logf("opened %v", got)
	}
	time.Sleep(50 * time.Millisecond)
	if got := r.sf.Opened(); len(got) != 1 || got[0] != "https://www.example.com/post?id=1" {
		t.Errorf("opened %v", got)
	}
}

// The media view's search keeps the tab's rows whose caption, name, title or
// address match, ignoring case and accents.
func TestSearchingMediaDocsAndLinks(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	beach := photo("p1", ada, epoch)
	beach.Media.Message.Content.Image.Caption, beach.Media.Message.Content.Image.HasCaption = b("Praia em São Paulo"), true
	r.answer(answerOpts{}, beach, photo("p2", ada, epoch+60),
		doc("d1", ada, "Orçamento.pdf", epoch+10, 1_500_000, 0),
		doc("d2", ada, "notes.txt", epoch+20, 800, 0),
		link("l1", "https://www.example.com/post", "A post", epoch+30),
		link("l2", "https://spotifast.rocks/", "", epoch+40),
	)
	r.click(ui.RoleButton, "Media, links and docs")
	r.click(ui.RoleSearchField, "Search")
	r.Type("sao")
	if n := r.count(ui.RoleButton, "Photo, "); n != 1 {
		t.Errorf("%d pictures match \"sao\", want the captioned one", n)
	}
	r.click(ui.RoleTab, "Docs 2")
	r.click(ui.RoleSearchField, "Search")
	r.Key(ui.KeyEscape, 0) // the search field clears
	r.Type("orcamento")
	if r.count(ui.RoleButton, "Orçamento.pdf") != 1 || r.count(ui.RoleButton, "notes.txt") != 0 {
		t.Errorf("docs matching \"orcamento\": %q", r.names(ui.RoleButton))
	}
	r.click(ui.RoleTab, "Links 2")
	if r.count(ui.RoleButton, "A post") != 0 || r.count(ui.RoleButton, "https://spotifast.rocks/") != 0 {
		t.Errorf("a link matched \"orcamento\"")
	}
	r.want(ui.RoleGroup, "No results")
}

// A link that is not for the browser is refused; an invite link opens the
// join dialog.
func TestUnsafeAndInviteLinks(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	r.answer(answerOpts{}, link("l1", "javascript:alert(1)", "Evil", epoch), link("l2", "https://chat.whatsapp.com/AbCdEf1234567890XyZ", "Join us", epoch+1))
	r.click(ui.RoleButton, "Media, links and docs")
	r.click(ui.RoleTab, "Links 2")
	var evil, join string
	for _, n := range r.names(ui.RoleButton) {
		switch {
		case strings.HasPrefix(n, "Evil, "):
			evil = n
		case strings.HasPrefix(n, "Join us, "):
			join = n
		}
	}
	r.click(ui.RoleButton, evil)
	if len(r.s.Toasts) != 1 || !r.s.Toasts[0].Error {
		t.Errorf("toasts %v", r.s.Toasts)
	}
	time.Sleep(20 * time.Millisecond)
	if len(r.sf.Opened()) != 0 {
		t.Error("a script link was opened")
	}
	r.click(ui.RoleButton, join)
	if r.s.Dialog.Kind != state.DialogJoinGroup || string(r.s.Dialog.Text) != "AbCdEf1234567890XyZ" {
		t.Errorf("dialog %+v", r.s.Dialog)
	}
	if pi := r.sent(abi.CommandTagPreviewInvite); len(pi) != 1 || string(pi[0].PreviewInvite.Code) != "AbCdEf1234567890XyZ" {
		t.Errorf("PreviewInvite %+v", pi)
	}
}

// IN-012: the empty states, the cut note and the error with Try again.
func TestEmptyAndErrorStates(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	r.answer(answerOpts{})
	r.click(ui.RoleButton, "Media, links and docs")
	r.wantValue(ui.RoleGroup, "No media", "Photos and videos shared in this chat will show here.")
	r.click(ui.RoleTab, "Docs")
	r.wantValue(ui.RoleGroup, "No documents", "Files shared in this chat will show here.")
	r.click(ui.RoleTab, "Links")
	r.wantValue(ui.RoleGroup, "No links", "Links shared in this chat will show here.")
	r.wantNot(ui.RoleLabel, "Only the newest items are listed.")

	// a listing the archive cut
	r.f.Emit(ev(abi.EventTagChatSharedChanged, func(e *abi.Event) { e.ChatSharedChanged.Chat = b(ada) }))
	r.Frames(2)
	r.answer(answerOpts{cut: [3]bool{false, false, true}}, link("l1", "https://example.com", "x", epoch))
	r.want(ui.RoleLabel, "Only the newest items are listed.")
	r.click(ui.RoleTab, "Docs")
	r.wantNot(ui.RoleLabel, "Only the newest items are listed.")

}

// An error shows the line in the overview and in the media view, and Try
// again asks once more.
func TestErrorAndTryAgain(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	r.answer(answerOpts{failed: true})
	r.want(ui.RoleLabel, "Could not list the media.")
	n := len(r.sent(abi.CommandTagListChatShared))
	r.click(ui.RoleButton, "Try again")
	if got := len(r.sent(abi.CommandTagListChatShared)); got != n+1 {
		t.Errorf("Try again sent %d requests, want 1", got-n)
	}
	r.answer(answerOpts{failed: true})
	r.click(ui.RoleButton, "Media, links and docs")
	r.want(ui.RoleLabel, "Could not list the media.")
	r.click(ui.RoleButton, "Try again")
	r.answer(answerOpts{}, photo("p", ada, epoch))
	r.wantNot(ui.RoleLabel, "Could not list the media.")
	r.want(ui.RoleTab, "Media 1")
}

// An answer to an earlier request, or for another chat, is ignored.
func TestStaleAnswersAreIgnored(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	r.answer(answerOpts{request: 99}, photo("p", ada, epoch))
	r.click(ui.RoleButton, "Media, links and docs")
	r.wantNot(ui.RoleTab, "Media 1")
	r.answer(answerOpts{}, photo("p", ada, epoch))
	r.want(ui.RoleTab, "Media 1")
}

// The listing follows the chat: another chat's panel asks again, and the
// worker's word that something changed asks for a fresh one.
func TestListingFollowsTheChat(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	r.answer(answerOpts{}, photo("p", ada, epoch))
	n := len(r.sent(abi.CommandTagListChatShared))
	r.f.Emit(ev(abi.EventTagChatSharedChanged, func(e *abi.Event) { e.ChatSharedChanged.Chat = b(grace) }))
	r.Frames(3)
	if len(r.sent(abi.CommandTagListChatShared)) != n {
		t.Error("another chat's change asked again")
	}
	r.f.Emit(ev(abi.EventTagChatSharedChanged, func(e *abi.Event) { e.ChatSharedChanged.Chat = b(ada) }))
	r.Frames(3)
	if len(r.sent(abi.CommandTagListChatShared)) != n+1 {
		t.Error("a change did not ask again")
	}
	r.open(grace, grace)
	reqs := r.sent(abi.CommandTagListChatShared)
	if len(reqs) != n+2 || string(reqs[n+1].ListChatShared.Chat) != grace {
		t.Errorf("ListChatShared %+v", reqs)
	}
}

// IN-013: Escape goes from the media list back to the overview, then closes
// the panel.
func TestEscapeWalksBack(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	r.click(ui.RoleButton, "Media, links and docs")
	if r.s.Panes.InfoView != state.InfoMedia {
		t.Fatal("not in the media view")
	}
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(3))
	if r.s.Panes.InfoView != state.InfoOverview || r.s.Panes.Pane != state.PaneInfo {
		t.Fatalf("view %v pane %v after the first Escape", r.s.Panes.InfoView, r.s.Panes.Pane)
	}
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(3))
	if r.s.Panes.Pane != state.PaneNone {
		t.Error("the second Escape left the panel open")
	}
	// Back does what the first Escape does
	r.open(ada, ada)
	r.click(ui.RoleButton, "Media, links and docs")
	r.click(ui.RoleButton, "Back")
	if r.s.Panes.InfoView != state.InfoOverview {
		t.Error("Back stayed in the media view")
	}
}

// IN-015: a locked chat's media is listed only while the locked folder is
// open.
func TestLockedChatMedia(t *testing.T) {
	r := newRig(t, chatSpec{id: ada, name: "Ada Lovelace", mut: func(c *abi.Chat) { c.Locked = true }}, chatSpec{id: team, name: "Team", mut: func(c *abi.Chat) { c.Participants = [][]byte{b(me), b(ada), b(grace)} }})
	// A locked chat opens only in its folder; once the folder closes its
	// panel goes with it, whatever closed it (zf:src/app.rs
	// drop_locked_panel).
	r.a.Mode = state.ListLocked
	r.open(ada, ada)
	r.want(ui.RoleLabel, "Contact info")
	r.a.Mode = state.ListChats
	r.s.Touch()
	r.Frames(3)
	r.wantNot(ui.RoleLabel, "Contact info")
	// A locked chat's panel from an open chat that is not locked (a member
	// of a group) lists none of its media.
	r.open(team, ada)
	asked := len(r.sent(abi.CommandTagListChatShared))
	r.want(ui.RoleLabel, "Contact info")
	r.wantNot(ui.RoleButton, "Media, links and docs")
	if n := len(r.sent(abi.CommandTagListChatShared)) - asked; n != 0 {
		t.Errorf("a locked chat's media was asked for (%d)", n)
	}
	// the media view of a locked chat is never drawn
	r.s.Panes.InfoView = state.InfoMedia
	r.Frames(3)
	r.want(ui.RoleLabel, "Contact info")
	if r.s.Panes.InfoView != state.InfoOverview {
		t.Error("the media view stayed")
	}
	// with the folder open it is listed
	r.a.Mode = state.ListLocked
	r.s.Touch()
	r.Frames(3)
	r.want(ui.RoleButton, "Media, links and docs")
	if n := len(r.sent(abi.CommandTagListChatShared)) - asked; n != 1 {
		t.Errorf("requests with the folder open: %d", n)
	}
}

// IN-014: in a narrow window the panel is a page of its own with Back, and
// going to a listed message closes it so the message can be seen.
func TestNarrowWindowCoversAndJumpClosesThePanel(t *testing.T) {
	r := newRigW(t, 600, direct()...)
	r.open(ada, ada)
	if !r.s.Panes.Covering {
		t.Fatal("the shell does not cover the window")
	}
	r.want(ui.RoleButton, "Back")
	r.wantNot(ui.RoleButton, "Close")
	r.answer(answerOpts{}, doc("d1", ada, "report.pdf", epoch+10, 1_500_000, 12))
	r.click(ui.RoleButton, "Media, links and docs")
	r.click(ui.RoleTab, "Docs 1")
	var name string
	for _, n := range r.names(ui.RoleButton) {
		if strings.HasPrefix(n, "report.pdf") {
			name = n
		}
	}
	if name == "" {
		t.Fatalf("no document\n%s", r.Dump())
	}
	r.click(ui.RoleButton, name)
	if r.s.Panes.Pane != state.PaneNone {
		t.Error("the panel stayed over the message")
	}
	if j := &r.s.Panes.Jump; j.Msg.Len() != 1 || string(j.Msg.At(0)) != "d1" {
		t.Error("no jump to the document's message")
	}
}
