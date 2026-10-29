package conversation

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
	"zapfast/ui/composer"
)

// A downloaded photo's menu offers Copy image and Save as… (a safe name);
// a downloaded sticker's offers Add to favorites (MM-015, MM-018).
func TestAttachmentMenuItems(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	photo := filepath.Join(dir, "IMG 1.jpg")
	os.WriteFile(photo, []byte("x"), 0o644)
	p := msg(ada, "ph", base0, false, "")
	p.Content = abi.Content{Tag: abi.ContentTagImage}
	p.Content.Image.Media = abi.Media{Mime: []byte("image/jpeg"), Size: 1, Path: []byte(photo), HasPath: true}
	st := msg(ada, "st", base0+60, false, "")
	st.Content = abi.Content{Tag: abi.ContentTagSticker}
	st.Content.Sticker.Media = abi.Media{Mime: []byte("image/webp"), Size: 1, Path: []byte(photo), HasPath: true}
	r.open(ada, p, st)
	menu := func(ts int64, item string) {
		r.Play(widgettest.RClick(ui.RoleGroup, bubble("Ada Lovelace", ts)), widgettest.Idle(2),
			widgettest.Click(ui.RoleButton, item), widgettest.Idle(2))
	}
	menu(base0, "Copy image")
	if c := r.sent(abi.CommandTagPrepareClipboardImage); len(c) != 1 || string(c[0].PrepareClipboardImage.Path) != photo {
		t.Errorf("Copy image %+v", c)
	}
	menu(base0, "Save as…")
	if c := r.sent(abi.CommandTagSaveAttachmentAs); len(c) != 1 || string(c[0].SaveAttachmentAs.Name) != "IMG 1.jpg" {
		t.Errorf("Save as %+v", c)
	}
	menu(base0+60, "Add to favorites")
	if c := r.sent(abi.CommandTagSaveSticker); len(c) != 1 || string(c[0].SaveSticker.Path) != photo {
		t.Errorf("favorites %+v", c)
	}
}

// Right-clicking a selected message offers the selection's delete choices
// (SL-009).
func TestRightClickInSelectionDeletes(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "x1", base0, true, "mine"), msg(ada, "x2", base0+60, false, "theirs"))
	r.Play(widgettest.RClick(ui.RoleGroup, bubble("You", base0)), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "Select"), widgettest.Idle(2))
	if !r.s.Panes.Selection.Has([]byte("x1")) {
		t.Fatal("not selected")
	}
	r.Play(widgettest.RClick(ui.RoleGroup, bubble("You", base0)), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "Delete for me"), widgettest.Idle(2))
	if d := r.s.Dialog; d.Kind != state.DialogConfirmDeleteSelection || d.IDs.Len() != 1 {
		t.Errorf("dialog %v ids %d", d.Kind, d.IDs.Len())
	}
}

// In a narrow window the header leads with Back to the chats, which closes
// the chat (CV-006).
func TestNarrowHeaderGoesBack(t *testing.T) {
	r := newRigSized(t, 640, 760)
	r.open(ada, msg(ada, "n1", base0, false, "hi"))
	r.Play(widgettest.Click(ui.RoleButton, "Back to chats"), widgettest.Idle(2))
	if r.a.Open != 0 {
		t.Error("the chat stayed open")
	}
	w := newRig(t)
	w.open(ada, msg(ada, "n1", base0, false, "hi"))
	w.mustNot(ui.RoleButton, "Back to chats")
}

// A message from someone else arriving at the end of the open chat is
// announced to VoiceOver, politely as upstream's (it finishes what it is
// reading first); the chat's first page is not (AX-003).
func TestNewMessageIsAnnounced(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "a1", base0, false, "old news"))
	if tr := r.Tree(); tr.AnnounceSeq != 0 && string(tr.Text(tr.Announce)) == "Ada Lovelace: old news" {
		t.Error("the first page was announced")
	}
	r.f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(ada), Batch: 9, Last: true, Messages: []abi.Message{msg(ada, "a2", base0+60, false, "fresh")}}
	}))
	said, polite := "", false
	for i := 0; i < 4 && said == ""; i++ {
		r.Frame()
		if tr := r.Tree(); tr.Announce[1] > 0 {
			said, polite = string(tr.Text(tr.Announce)), tr.AnnouncePolite
		}
	}
	if said != "Ada Lovelace: fresh" || !polite {
		t.Errorf("announced %q, polite %v: want it read once the reader is done", said, polite)
	}
	// locked, nothing is said, then or once unlocked
	r.s.Lock.Locked = true
	r.Frames(2)
	r.f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(ada), Batch: 10, Last: true, Messages: []abi.Message{msg(ada, "a3", base0+120, false, "secret")}}
	}))
	heard := func() {
		for i := 0; i < 4; i++ {
			r.Frame()
			if tr := r.Tree(); tr.Announce[1] > 0 {
				t.Errorf("a message that came while locked was announced (locked %v): %q", r.s.Lock.Locked, tr.Text(tr.Announce))
			}
		}
	}
	heard()
	r.s.Lock.Locked = false
	heard()
}

// A group's subtitle lists its members by first name, sorted, then You
// (CV-001).
func TestGroupSubtitleNamesMembers(t *testing.T) {
	r := newRig(t)
	r.f.Emit(ev(abi.EventTagContacts, func(e *abi.Event) {
		e.Contacts = abi.EventContacts{Batch: 1, Last: true, Contacts: []abi.Contact{
			{ID: []byte("2@s.whatsapp.net"), FullName: []byte("Bo Diddley"), HasFullName: true, FirstName: []byte("Bo"), HasFirstName: true},
			{ID: []byte("3@s.whatsapp.net"), FullName: []byte("ana Lima"), HasFullName: true},
		}}
	}), ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(group), Name: []byte("Engines"), Kind: abi.ChatKindGroup, LastActivity: 10,
			Participants: [][]byte{[]byte("2@s.whatsapp.net"), []byte("3@s.whatsapp.net")}}
	}))
	r.Frames(2)
	r.open(group, msg(group, "g1", base0, true, "hello all"))
	r.must(ui.RoleLabel, "ana, Bo")
}

// Escape closes the day filter's calendar first, then the search (CS-007).
func TestEscapeClosesTheCalendarFirst(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "e1", base0, false, "hi"))
	r.s.Req.FindInChat = true
	r.Frames(3)
	r.Play(widgettest.Click(ui.RoleButton, "Filter by date"), widgettest.Idle(2))
	r.must(ui.RoleButton, "Previous month")
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	r.mustNot(ui.RoleButton, "Previous month")
	if r.s.Panes.Pane != state.PaneSearch {
		t.Fatalf("Escape closed the pane with the calendar (%v)", r.s.Panes.Pane)
	}
	r.Play(widgettest.Click(ui.RoleSearchField, "Search"), widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	if r.s.Panes.Pane == state.PaneSearch {
		t.Error("the second Escape left the pane open")
	}
}

func TestCalendarEscapeBeforeFocusedComposer(t *testing.T) {
	r := newRigLook(t, 0, 0, false, composer.New())
	r.open(ada, msg(ada, "reply", base0, false, "hello"))
	r.s.Req.FindInChat = true
	r.Frames(3)
	r.s.Req.FocusComposer = true
	r.Frames(3)
	if focused, ok := r.Focused(); !ok || focused.Role != ui.RoleTextField || !r.Context().TextFieldFocused() {
		t.Fatal("composer text field is not focused")
	}
	st := &r.scr.h.search
	st.calendar = true
	st.month = dayAt(r.now)
	r.s.Panes.Reply.Add([]byte("reply"))
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	if st.calendar || r.s.Panes.Reply.Len() != 1 || r.s.Panes.Pane != state.PaneSearch {
		t.Fatalf("Escape must close only the calendar: calendar=%v reply=%d pane=%v", st.calendar, r.s.Panes.Reply.Len(), r.s.Panes.Pane)
	}
	// Starred is above the calendar in upstream's chain.
	st.calendar = true
	r.a.Mode = state.ListStarred
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	if r.a.Mode != state.ListChats || !st.calendar || r.s.Panes.Reply.Len() != 1 {
		t.Fatal("calendar intercepted Escape before Starred")
	}
}

// Links in a message's text go through the app's link rules: an invite
// opens the join dialog, a web link the browser (the fake shell), any other
// scheme is refused; a phone number offers a chat or a copy (MK-004..006).
func TestTextLinks(t *testing.T) {
	r := newRig(t)
	sh := r.s.Shell.(*opened)
	r.open(ada, msg(ada, "l1", base0, false, "https://chat.whatsapp.com/AbCdEfGhIjKl1"),
		msg(ada, "l2", base0+60, false, "see example.com"), msg(ada, "l3", base0+120, false, "call +1 555 0100"))
	r.Play(widgettest.Click(ui.RoleLink, "https://chat.whatsapp.com/AbCdEfGhIjKl1"), widgettest.Idle(2))
	if r.s.Dialog.Kind != state.DialogJoinGroup || string(r.s.Dialog.Text) != "AbCdEfGhIjKl1" {
		t.Errorf("invite: dialog %v %q", r.s.Dialog.Kind, r.s.Dialog.Text)
	}
	r.s.CloseDialog()
	r.Play(widgettest.Click(ui.RoleLink, "example.com"), widgettest.Idle(2), widgettest.Pause(50), widgettest.Idle(2))
	if len(sh.paths) != 1 || sh.paths[0] != "https://example.com" {
		t.Errorf("web link opened %v", sh.paths)
	}
	r.Play(widgettest.Click(ui.RoleLink, "+1 555 0100"), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "Copy number"), widgettest.Idle(2))
	if got := string(r.clip); got != "+1 555 0100" {
		t.Errorf("copied %q", got)
	}
	r.Play(widgettest.Click(ui.RoleLink, "+1 555 0100"), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "Message"), widgettest.Idle(2))
	if c := r.sent(abi.CommandTagEnsureChat); len(c) != 1 || string(c[0].EnsureChat.Chat) != "15550100@s.whatsapp.net" {
		t.Errorf("EnsureChat %+v", c)
	}
}

func TestPhoneMenuAnchorsToKeyboardLinkOrPointer(t *testing.T) {
	for _, key := range []ui.KeyCode{ui.KeyEnter, ui.KeySpace, 0} {
		r := newRig(t)
		r.open(ada, msg(ada, "phone", base0, false, "Call +1 555 0100 today"))
		r.Play(widgettest.MoveTo(20, 20), widgettest.Focus(ui.RoleLink, "+1 555 0100"), widgettest.Idle(2))
		link := r.node(ui.RoleLink, "+1 555 0100")
		if r.Context().FocusKey() != link.Key {
			t.Fatal("the phone link is not focused")
		}
		want := link.Bounds
		if key == 0 {
			r.Play(widgettest.ClickAt(ui.RoleLink, "+1 555 0100", 4, 4), widgettest.Idle(2))
			want = [4]float32{want[0] + 4, want[1] + 4, want[0] + 5, want[1] + 5}
		} else {
			r.Play(widgettest.Key(key, 0), widgettest.Idle(2))
		}
		r.must(ui.RoleButton, "Copy number")
		if got := r.scr.phone.at; got != want {
			t.Errorf("key %v: menu anchor %v, want %v", key, got, want)
		}
	}
}

// Pins leave notices where they were made (CV-030).
func TestPinNotices(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "n1", base0, false, "one"), msg(ada, "n2", base0+600, false, "two"))
	r.f.Emit(ev(abi.EventTagPins, func(e *abi.Event) {
		e.Pins = abi.EventPins{Chat: []byte(ada), Batch: 1, Last: true, Items: []abi.Pinned{
			{Chat: []byte(ada), ID: []byte("n1"), PinnedAt: base0 + 60, Pinner: abi.Pinner{ByMe: true}, Text: []byte("one")},
			{Chat: []byte(ada), ID: []byte("n2"), PinnedAt: base0 + 900, Text: []byte("two")},
		}}
	}))
	r.Frames(3)
	mine, anon := r.node(ui.RoleLabel, "You pinned a message").Bounds, r.node(ui.RoleLabel, "A message was pinned").Bounds
	one, two := r.node(ui.RoleGroup, bubble("Ada Lovelace", base0)).Bounds, r.node(ui.RoleGroup, bubble("Ada Lovelace", base0+600)).Bounds
	if !(one[3] <= mine[1] && mine[3] <= two[1] && two[3] <= anon[1]) {
		t.Errorf("notices %v %v around %v %v", mine, anon, one, two)
	}
}

// Beside a message: React (named for its sender) opens the picker there,
// and Reply quotes it (CV-029).
func TestHoverReactAndReply(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "h1", base0, false, "hello"))
	r.Play(widgettest.Click(ui.RoleButton, "React to Ada Lovelace's message"), widgettest.Idle(2))
	if rt := &r.s.Panes.ReactTarget; rt.Msg.Len() != 1 || string(rt.Msg.At(0)) != "h1" {
		t.Errorf("react target %v", rt.Msg)
	}
	r.s.Panes.ReactTarget.Reset()
	r.Play(widgettest.Click(ui.RoleButton, "Reply"), widgettest.Idle(2))
	if p := &r.s.Panes.Reply; p.Len() != 1 || string(p.At(0)) != "h1" {
		t.Errorf("reply %v", p)
	}
}

// A call's menu calls back; a group's says it happens on the phone
// (CV-033).
func TestCallEntryCallsBack(t *testing.T) {
	r := newRig(t)
	call := msg(ada, "c1", base0, false, "")
	call.Content = abi.Content{Tag: abi.ContentTagCall, Call: abi.ContentCall{Video: true, Status: abi.CallStatusMissed}}
	r.open(ada, call)
	r.Play(widgettest.RClick(ui.RoleGroup, bubble("Ada Lovelace", base0)), widgettest.Idle(2))
	r.mustNot(ui.RoleButton, "Reply")
	r.Play(widgettest.Click(ui.RoleButton, "Video call").Nth(1), widgettest.Idle(2))
	if c := r.sent(abi.CommandTagStartCall); len(c) != 1 || !c[0].StartCall.Video || string(c[0].StartCall.Chat) != ada {
		t.Errorf("StartCall %+v", c)
	}
}

// A private reply's quote opens the group it came from (CV-026).
func TestPrivateReplyQuoteOpensItsGroup(t *testing.T) {
	r := newRig(t)
	reply := msg(ada, "pr", base0, false, "about that")
	reply.HasQuoted = true
	reply.Quoted = abi.Quoted{Chat: []byte(group), HasChat: true, ID: []byte("g9"), Sender: []byte(ada), SenderName: []byte("Ada"), HasSenderName: true, Summary: []byte("the plan")}
	r.open(ada, reply)
	r.Play(widgettest.Click(ui.RoleButton, "~Ada: the plan"), widgettest.Idle(3))
	if c, ok := r.a.OpenChatView(); !ok || string(c.ID()) != group {
		t.Errorf("open %v", r.a.Open)
	}
}

// In a group, incoming messages sit beside their senders' pictures
// (PR-008).
func TestGroupSendersHavePictures(t *testing.T) {
	r := newRig(t)
	g := msg(group, "gp", base0, false, "from Bo")
	g.Sender, g.SenderName, g.HasSenderName = []byte("2@s.whatsapp.net"), []byte("Bo"), true
	r.open(group, g)
	gb := r.node(ui.RoleGroup, bubble("~Bo", base0)).Bounds
	d := newRig(t)
	d.open(ada, msg(ada, "dp", base0, false, "from Ada"))
	db := d.node(ui.RoleGroup, bubble("Ada Lovelace", base0)).Bounds
	if gb[0] < db[0]+senderPicPt {
		t.Errorf("group bubble at %v, direct at %v: no room for the picture", gb[0], db[0])
	}
}

// Scrolled up, the Newest message button carries the unread count
// (CV-010); scrolling shows the top message's day, which fades once it
// stops (CV-014).
// The Newest message arrow sits at the centre of its circle: the box its
// dark pixels fill is centred on the button's, within a pixel.
func TestNewestArrowIsCentred(t *testing.T) {
	r := newRig(t)
	var msgs []abi.Message
	for i := 0; i < 120; i++ {
		msgs = append(msgs, msg(ada, "s"+strconv.Itoa(i), base0+int64(i)*3600, false, "line "+strconv.Itoa(i)))
	}
	r.open(ada, msgs...)
	r.Play(widgettest.Wheel(ui.RoleList, "Messages", 400, 3), widgettest.Idle(1))
	b := r.node(ui.RoleButton, "Newest message").Bounds
	img := r.Image()
	x0, y0, x1, y1 := int(b[2]), int(b[3]), int(b[0]), int(b[1])
	for y := int(b[1]); y < int(b[3]); y++ {
		for x := int(b[0]); x < int(b[2]); x++ {
			if p := img.RGBAAt(x, y); int(p.R)+int(p.G)+int(p.B) < 3*110 {
				x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x+1), max(y1, y+1)
			}
		}
	}
	if x1 <= x0 {
		t.Fatal("no arrow drawn in the button")
	}
	cx, cy := float64(b[0]+b[2])/2, float64(b[1]+b[3])/2
	if dx, dy := float64(x0+x1)/2-cx, float64(y0+y1)/2-cy; dx > 1 || dx < -1 || dy > 1 || dy < -1 {
		t.Errorf("the arrow's centre is %.1f, %.1f px off the button's", dx, dy)
	}
}

func TestNewestCountAndFloatingDay(t *testing.T) {
	r := newRig(t)
	var msgs []abi.Message
	for i := 0; i < 120; i++ {
		msgs = append(msgs, msg(ada, "s"+strconv.Itoa(i), base0+int64(i)*3600, false, "line "+strconv.Itoa(i)))
	}
	r.f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: 30, Unread: 4}
	}))
	r.open(ada, msgs...)
	r.Play(widgettest.Wheel(ui.RoleList, "Messages", 400, 3), widgettest.Idle(1))
	if n := len(r.all(ui.RoleLabel, "4 unread messages")); n < 1 {
		t.Error("no unread count on Newest message")
	}
	list := r.node(ui.RoleList, "Messages").Bounds
	// upstream's place: a 40 pt circle centred 34 pt in from the
	// transcript's bottom right, the count centred 14 pt right of its centre
	// and 16 pt above
	b := r.node(ui.RoleButton, "Newest message").Bounds
	cx, cy := (b[0]+b[2])/2, (b[1]+b[3])/2
	if b[2]-b[0] != newestPt || b[3]-b[1] != newestPt || abs32(cx-(list[2]-34)) > 1 || abs32(cy-(list[3]-34)) > 1 {
		t.Errorf("Newest message at %v in %v", b, list)
	}
	if c := r.all(ui.RoleLabel, "4 unread messages")[0].Bounds; abs32((c[0]+c[2])/2-(cx+14)) > 1 || abs32((c[1]+c[3])/2-(cy-16)) > 1 || c[3]-c[1] != 20 {
		t.Errorf("the count at %v, the button's centre %v,%v", c, cx, cy)
	}
	floating := func() bool {
		v := r.a.Conversation()
		mi := r.scr.t.index(r.scr.sticky.anchor)
		if mi < 0 {
			return false
		}
		day := dayLabel(r.Context().Fmt(), v.At(mi).Time(), r.scr.now).String()
		// the list runs under the header: its content starts topInset down
		top := list[1] + r.scr.topInset
		for _, n := range r.all(ui.RoleLabel, day) {
			if n.Bounds[1] >= top && n.Bounds[1] < top+20 {
				return true
			}
		}
		return false
	}
	if !floating() {
		t.Fatal("no day at the top while scrolling")
	}
	r.Play(widgettest.Pause(1400), widgettest.Idle(2))
	if floating() {
		t.Error("the day stayed after scrolling stopped")
	}
}

// An album's items show as one grid in the first one's bubble: each opens
// its own menu; Download all fetches the missing ones and Save all… saves
// the rest (MK-028..030).
func TestAlbumGrid(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	var msgs []abi.Message
	for i := 0; i < 3; i++ {
		p := msg(ada, "al"+strconv.Itoa(i), base0+int64(i), false, "")
		p.Content = abi.Content{Tag: abi.ContentTagImage}
		p.Content.Image.Media = abi.Media{Mime: []byte("image/jpeg"), Size: 50_000_000, Album: []byte("A1"), HasAlbum: true}
		if i == 0 {
			path := filepath.Join(dir, "first.jpg")
			os.WriteFile(path, []byte("x"), 0o644)
			p.Content.Image.Media.Path, p.Content.Image.Media.HasPath = []byte(path), true
		}
		msgs = append(msgs, p)
	}
	msgs = append(msgs, msg(ada, "after", base0+60, false, "after"))
	r.open(ada, msgs...)
	b := r.node(ui.RoleGroup, bubble("Ada Lovelace", base0)).Bounds
	if n := len(r.all(ui.RoleGroup, bubble("Ada Lovelace", base0))); n != 1 {
		t.Errorf("%d bubbles for the album", n)
	}
	tiles := 0
	for _, n := range r.Tree().Nodes {
		if n.Role == ui.RoleButton && inside(n.Bounds, b) && n.Bounds[2]-n.Bounds[0] > 80 && n.Bounds[2]-n.Bounds[0] == n.Bounds[3]-n.Bounds[1] {
			tiles++
		}
	}
	if tiles != 3 {
		t.Errorf("%d square tiles in the album's bubble", tiles)
	}
	// an item's own menu
	r.Play(widgettest.RClick(ui.RoleButton, "Download photo").Nth(1), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "Copy message ID"), widgettest.Idle(2))
	if string(r.clip) != "al2" {
		t.Errorf("the item's menu copied %q", r.clip)
	}
	before := len(r.sent(abi.CommandTagDownload))
	r.Play(widgettest.Click(ui.RoleButton, "Download all"), widgettest.Idle(2))
	manual := 0
	for _, d := range r.sent(abi.CommandTagDownload)[before:] {
		if d.Download.Manual && string(d.Download.Message) != "al0" {
			manual++
		}
	}
	if manual != 2 {
		t.Errorf("Download all asked for %d", manual)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Save all…"), widgettest.Idle(2))
	if c := r.sent(abi.CommandTagSaveAttachments); len(c) != 1 || len(c[0].SaveAttachments.Files) != 1 || string(c[0].SaveAttachments.Files[0].Name) != "first.jpg" {
		t.Errorf("Save all %+v", c)
	}
	// a failure marks its own tile, the others go on, and a click on it
	// asks again for it alone
	r.f.Emit(ev(abi.EventTagMedia, func(e *abi.Event) {
		e.Media = abi.EventMedia{Chat: []byte(ada), Message: []byte("al1"),
			Result: abi.PathResult{Tag: abi.PathResultTagErr, Err: abi.PathResultErr{Error: []byte("expired")}}}
	}))
	r.Frames(2)
	if n := len(r.all(ui.RoleButton, "Retry download")); n != 1 {
		t.Fatalf("%d failed tiles", n)
	}
	other := len(r.all(ui.RoleButton, "Downloading")) + len(r.all(ui.RoleButton, "Download photo"))
	if fail := r.node(ui.RoleButton, "Retry download").Bounds; !inside(fail, b) || other != 1 {
		t.Errorf("the failure at %v (the album %v), %d others to download", fail, b, other)
	}
	// its reason under the pointer
	r.Context().SetNativeTooltips(true)
	r.Play(widgettest.Move(ui.RoleButton, "Retry download"), widgettest.Idle(1))
	said := false
	for _, reg := range r.Context().TooltipRegions(nil) {
		said = said || string(r.Context().TooltipText(reg.Key, nil)) == "expired · Click to retry"
	}
	if !said {
		t.Error("no reason over the failed tile")
	}
	before = len(r.sent(abi.CommandTagDownload))
	r.Play(widgettest.Click(ui.RoleButton, "Retry download"), widgettest.Idle(2))
	if d := r.sent(abi.CommandTagDownload)[before:]; len(d) != 1 || string(d[0].Download.Message) != "al1" || !d[0].Download.Manual {
		t.Errorf("the retry asked for %+v", d)
	}
}

// The viewer's React asks for the picker with no anchor alongside the jump
// to the message: it opens beside the message's bubble once that is on
// screen (VW-006).
func TestReactionFromTheViewerOpensBesideTheBubble(t *testing.T) {
	r := newRig(t)
	var msgs []abi.Message
	for i := 0; i < 80; i++ {
		msgs = append(msgs, msg(ada, "v"+strconv.Itoa(i), base0+int64(i*60), false, "line "+strconv.Itoa(i)))
	}
	r.open(ada, msgs...)
	r.a.OpenMessage([]byte(ada), []byte("v3"))
	r.s.OpenReaction(r.a.Open, []byte("v3"), state.Rect{})
	r.Frames(8)
	rt := &r.s.Panes.ReactTarget
	if !rt.Open() || string(rt.Msg.At(0)) != "v3" {
		t.Fatalf("picker %v", rt.Msg)
	}
	b := r.node(ui.RoleGroup, bubble("Ada Lovelace", base0+3*60)).Bounds
	an := rt.Anchor
	if an.W <= 0 || an.X < b[0]-1 || an.Y < b[1]-1 || an.X+an.W > b[2]+1 || an.Y+an.H > b[3]+1 {
		t.Errorf("anchor %+v, bubble %v", an, b)
	}
}

// Everyone typing in a group: the header names them as upstream does and
// the typing bubble stacks their pictures (CV-001, CV-016).
func TestTypersInAGroup(t *testing.T) {
	r := newRig(t)
	r.f.Emit(ev(abi.EventTagContacts, func(e *abi.Event) {
		e.Contacts = abi.EventContacts{Batch: 1, Last: true, Contacts: []abi.Contact{
			{ID: []byte("2@s.whatsapp.net"), FullName: []byte("Bo Diddley"), HasFullName: true},
			{ID: []byte("3@s.whatsapp.net"), FullName: []byte("Cy Twombly"), HasFullName: true},
			{ID: []byte("4@s.whatsapp.net"), FullName: []byte("Di Prima"), HasFullName: true},
		}}
	}))
	r.open(group, msg(group, "t1", base0, true, "anyone?"))
	typing := func(who string) {
		r.f.Emit(ev(abi.EventTagTyping, func(e *abi.Event) {
			e.Typing = abi.EventTyping{Chat: []byte(group), Sender: []byte(who), Composing: true}
		}))
		r.Frames(2)
	}
	typing("2@s.whatsapp.net")
	r.must(ui.RoleLabel, "Bo Diddley is typing…")
	typing("3@s.whatsapp.net")
	typing("4@s.whatsapp.net")
	r.must(ui.RoleLabel, "Bo Diddley, Cy Twombly and Di Prima are typing…")
	// the bubble: three pictures, overlapping, before the dots
	list := r.node(ui.RoleList, "Messages").Bounds
	found := false
	for _, n := range r.all(ui.RoleLabel, "Bo Diddley, Cy Twombly and Di Prima are typing…") {
		if n.Bounds[1] > list[1]+r.scr.topInset { // below the header, which the list runs under
			found = true
			if want := list[0] + sidePt + senderPicPt + 2*senderPicPt*0.6; n.Bounds[0] < want-2 {
				t.Errorf("the dots at %v, want them after three stacked pictures (%v)", n.Bounds[0], want)
			}
		}
	}
	if !found {
		t.Error("no typing bubble")
	}
}

// A reply settles at the card width at least, and its quote spans it; a
// link preview's card spans its text when that is wider. A quote of our
// own message says "You" (zf:src/ui/conversation.rs settled_width,
// quote_block).
func TestQuoteAndCardsSettle(t *testing.T) {
	r := newRig(t)
	r.f.Emit(ev(abi.EventTagMe, func(e *abi.Event) {
		e.Me = abi.EventMe{ID: []byte("9@s.whatsapp.net"), Name: []byte("Me"), HasName: true}
	}))
	short := msg(ada, "s1", base0, false, "the plan")
	reply := msg(ada, "s2", base0+60, true, "ok")
	reply.HasQuoted = true
	reply.Quoted = abi.Quoted{ID: []byte("s1"), Sender: []byte(ada), SenderName: []byte("Ada"), HasSenderName: true, Summary: []byte("the plan")}
	mine := msg(ada, "s3", base0+120, false, "sure")
	mine.HasQuoted = true
	mine.Quoted = abi.Quoted{ID: []byte("s2"), Sender: []byte("9@s.whatsapp.net"), Summary: []byte("ok")}
	long := strings.Repeat("background reading on the engine ", 2) + "https://example.com/a"
	link := msg(ada, "s4", base0+180, false, long)
	link.Content.Text.HasPreview = true
	link.Content.Text.Preview = abi.LinkPreview{URL: []byte("https://example.com/a"), Title: []byte("Example"), HasTitle: true}
	r.open(ada, short, reply, mine, link)

	b := r.node(ui.RoleGroup, bubble("You", base0+60)).Bounds
	q := r.node(ui.RoleButton, "~Ada: the plan").Bounds
	if w := b[2] - b[0]; w < kindsCardPt {
		t.Errorf("the reply is %v wide, want the card's %v at least", w, kindsCardPt)
	}
	if qw, inner := q[2]-q[0], b[2]-b[0]-2*rexui.BubbleLook.PadX; qw < inner-2 {
		t.Errorf("the quote is %v wide in a bubble %v wide inside", qw, inner)
	}
	r.must(ui.RoleButton, "You: ok")

	lb := r.node(ui.RoleGroup, bubble("Ada Lovelace", base0+180)).Bounds
	card := r.node(ui.RoleLink, "Example").Bounds
	if lw, inner := lb[2]-lb[0]-2*rexui.BubbleLook.PadX, card[2]-card[0]; lw <= kindsCardPt || inner < lw-2 {
		t.Errorf("the preview is %v wide in a bubble %v wide inside, want it to span the text", inner, lw)
	}
}

// A jump asked for before or in the frame the chat's history lands goes to
// its message: neither the unread divider nor the transcript's settling at
// the end undoes it.
func TestJumpAsTheHistoryLands(t *testing.T) {
	for _, tc := range []struct {
		unread int
		early  bool // the jump opens the chat, frames before its history
	}{{0, false}, {3, false}, {0, true}, {3, true}} {
		r := newRig(t)
		if tc.unread > 0 {
			r.f.Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
				e.ChatUpdated.Chat = abi.Chat{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: 20, Unread: uint32(tc.unread)}
			}))
			r.Frames(2)
		}
		if tc.early {
			r.a.OpenMessage([]byte(ada), []byte("h21"))
		} else {
			r.a.OpenChat(r.a.Model.Chats.Find([]byte(ada)))
		}
		r.Frames(3)
		var msgs []abi.Message
		for i := 0; i < 200; i++ {
			msgs = append(msgs, msg(ada, "h"+strconv.Itoa(i), base0+int64(i)*60, i%4 == 0, "line "+strconv.Itoa(i)))
		}
		r.f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
			e.Messages = abi.EventMessages{Chat: []byte(ada), Batch: 1, Last: true, Complete: true, Messages: msgs}
		}))
		if !tc.early {
			r.a.OpenMessage([]byte(ada), []byte("h21"))
		}
		r.Frames(12)
		n, ok := r.Find(ui.RoleGroup, bubble("Ada Lovelace", base0+21*60))
		list := r.node(ui.RoleList, "Messages").Bounds
		if !ok || n.Bounds[3] < list[1] || n.Bounds[1] > list[3] {
			t.Errorf("%+v: the jump's message is not in view (%v in %v)", tc, n.Bounds, list)
		}
	}
}

// A Shift-click in selection mode selects every message from the last one
// clicked (SL-003, logic.SelectRange), on the check and on the row; a click
// anywhere on a row, its text too, ticks it (SL-002).
func TestShiftClickSelectsARange(t *testing.T) {
	r := newRig(t)
	var msgs []abi.Message
	for i := 0; i < 6; i++ {
		msgs = append(msgs, msg(ada, "r"+strconv.Itoa(i), base0+int64(i)*60, false, "line "+strconv.Itoa(i)))
	}
	r.open(ada, msgs...)
	check := func(i int) string {
		return "Select message from Ada Lovelace, " + hhmm(base0+int64(i)*60) + ": line " + strconv.Itoa(i)
	}
	r.Play(widgettest.RClick(ui.RoleGroup, bubble("Ada Lovelace", base0+60)), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "Select"), widgettest.Idle(2))
	r.Play(widgettest.Click(ui.RoleCheckbox, check(4)).With(ui.ModShift), widgettest.Idle(2))
	if n := r.s.Panes.Selection.Len(); n != 4 {
		t.Errorf("a Shift-click on the check selected %d, want 4 (1 to 4)", n)
	}
	// a click on a bubble's text ticks it as a click on its padding does
	r.Play(widgettest.Click(ui.RoleGroup, bubble("Ada Lovelace", base0)), widgettest.Idle(2))
	if n := r.s.Panes.Selection.Len(); n != 5 {
		t.Errorf("a click on a bubble's text: %d selected, want 5", n)
	}
	r.Play(widgettest.ClickAt(ui.RoleGroup, bubble("Ada Lovelace", base0), 4, 4), widgettest.Idle(2))
	r.Play(widgettest.Click(ui.RoleGroup, bubble("Ada Lovelace", base0+5*60)).With(ui.ModShift), widgettest.Idle(2))
	if n := r.s.Panes.Selection.Len(); n != 6 {
		t.Errorf("a Shift-click on the row selected %d, want 6 (from 0, the last clicked, to 5)", n)
	}
}

// A double click beside a bubble quotes its message in the composer; one on
// its text selects a word instead (MM-005).
func TestDoubleClickBesideABubbleReplies(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "d1", base0, false, "hello there"), msg(ada, "d2", base0+60, false, "hi"))
	r.Play(widgettest.DoubleClick(ui.RoleGroup, bubble("Ada Lovelace", base0)), widgettest.Idle(2))
	if r.s.Panes.Reply.Len() != 0 {
		t.Fatal("a double click on the text replied")
	}
	r.Play(widgettest.DoubleClick(ui.RoleListItem, "").Nth(1), widgettest.Idle(2))
	if p := &r.s.Panes; p.Reply.Len() != 1 || string(p.Reply.At(0)) != "d2" {
		t.Errorf("a double click beside the bubble: reply %d", p.Reply.Len())
	}
}

// The time sits beside a line of text when it fits, at the bubble's right
// (zf:src/ui/conversation.rs rich_body), and drops to a row of its own when
// it does not: a link preview's text keeps it on its line, with no room
// left over beside the text.
func TestFooterBesideTheLastLine(t *testing.T) {
	r := newRig(t)
	short := msg(ada, "f1", base0, true, "ok")
	// a column holds n letters; a last line of 3 leaves room for the time,
	// one of n-2 does not
	pre := msg(ada, "f0", base0-60, true, "x")
	r.open(ada, pre)
	ts, font := r.Offscreen().Text, r.scr.look.rich.Styles[0].Font
	n := int(r.scr.maxW / ts.Run(ts.Shape(font, []byte("a"))).Advance)
	room := msg(ada, "f2", base0+60, true, strings.Repeat("a", n-2)+" end")
	full := msg(ada, "f4", base0+180, true, strings.Repeat("a", n-2)+" "+strings.Repeat("a", n-2))
	link := msg(ada, "f3", base0+120, true, "Background reading: https://example.com/wiki/Analytical_engine")
	link.Content.Text.HasPreview = true
	link.Content.Text.Preview = abi.LinkPreview{URL: []byte("https://example.com/wiki/Analytical_engine"), Title: []byte("Example"), HasTitle: true}
	r.open(ada, short, room, link, full)
	tr := r.Tree()
	var infos [][4]float32
	for _, n := range tr.Nodes {
		if n.Role == ui.RoleButton && string(tr.Text(n.Name)) == "Message info" {
			infos = append(infos, n.Bounds)
		}
	}
	if len(infos) != 5 {
		t.Fatalf("%d footers", len(infos))
	}
	infos = infos[1:] // the first message's
	beside := func(i int, label string) bool {
		l := r.node(ui.RoleLabel, label).Bounds
		mid := (infos[i][1] + infos[i][3]) / 2
		return mid > l[1] && mid < l[3]
	}
	if !beside(0, "ok") {
		t.Error("a short line's time is not beside it")
	}
	// the wrapped text keeps its lines, the time on the last
	lr, lf := r.node(ui.RoleLabel, string(room.Content.Text.Text)).Bounds, r.node(ui.RoleLabel, string(full.Content.Text.Text)).Bounds
	if !beside(1, string(room.Content.Text.Text)) || lr[3]-lr[1] != lf[3]-lf[1] || abs32(infos[1][3]-lr[3]) > 2 {
		t.Errorf("a wrapped text's time is not on its last line: %v, the text %v", infos[1], lr)
	}
	if infos[3][1] < lf[3]-0.5 {
		t.Errorf("the time is beside a full last line: %v, the text %v", infos[3], lf)
	}
	if !beside(2, string(link.Content.Text.Text)) {
		t.Errorf("a link preview's one line has its time below (%v, label %v, maxW %v)", infos[2], r.node(ui.RoleLabel, string(link.Content.Text.Text)).Bounds, r.scr.maxW)
	}
	for i, at := range []int64{base0 + 60, base0 + 120} {
		b := r.node(ui.RoleGroup, bubble("You", at)).Bounds
		if gap := b[2] - rexui.BubbleLook.PadX - infos[i+1][2]; gap > 2 {
			t.Errorf("the time ends %v short of the bubble's edge", gap)
		}
	}
}

// Unavailable actions are disabled rather than hidden (PR-002): Reload
// earlier messages needs the connection and a sent message, Clear chat the
// connection, and a group call entry only says calls happen on the phone.
func TestUnavailableMenuItemsAreDisabled(t *testing.T) {
	r := newRig(t)
	pending := msg(ada, "p1", r.now.Unix()-60, true, "pending")
	pending.Status = abi.DeliveryPending
	groupCall := msg(group, "g1", base0, false, "")
	groupCall.Content = abi.Content{Tag: abi.ContentTagCall, Call: abi.ContentCall{Group: true, Status: abi.CallStatusMissed}}
	r.open(ada, msg(ada, "s1", base0, false, "sent"), pending)
	disabled := func(role ui.Role, name string) bool {
		t.Helper()
		return r.node(role, name).State&ui.SemDisabled != 0
	}
	menu := func(who string, ts int64) {
		r.Play(widgettest.RClick(ui.RoleGroup, bubble(who, ts)), widgettest.Idle(2))
	}
	menu("Ada Lovelace", base0)
	if disabled(ui.RoleButton, "Reload earlier messages") {
		t.Error("Reload earlier messages is disabled while connected")
	}
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	menu("You", r.now.Unix()-60)
	if !disabled(ui.RoleButton, "Reload earlier messages") {
		t.Error("Reload earlier messages is enabled on a pending message")
	}
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	r.Play(widgettest.Click(ui.RoleButton, "More"), widgettest.Idle(2))
	if disabled(ui.RoleButton, "Clear chat") {
		t.Error("Clear chat is disabled while connected")
	}
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))

	r.f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagDisconnected }))
	r.Frames(2)
	menu("Ada Lovelace", base0)
	if !disabled(ui.RoleButton, "Reload earlier messages") {
		t.Error("Reload earlier messages is enabled offline")
	}
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	r.Play(widgettest.Click(ui.RoleButton, "More"), widgettest.Idle(2))
	if !disabled(ui.RoleButton, "Clear chat") {
		t.Error("Clear chat is enabled offline")
	}
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))

	r.f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }))
	r.Frames(2)
	r.open(group, groupCall)
	menu("Engines", base0)
	if disabled(ui.RoleButton, "Voice call") {
		t.Error("a group call entry does not call the group back")
	}
}
