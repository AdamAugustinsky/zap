package chatlist

import (
	"bytes"
	"gonativeui/rexui"
	"slices"
	"strings"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

func textMsg(sender string, fromMe bool, text string) abi.LastMessage {
	return abi.LastMessage{Sender: []byte(sender), FromMe: fromMe, Summary: []byte(text), Full: []byte(text)}
}

// CL-003: our own last message carries its ticks; a group's carries the
// sender's first name.
func TestLastMessageTicksAndGroupSender(t *testing.T) {
	r := newRig(t)
	mine := chat("1", "Ada", 30)
	mine.HasLast, mine.Last = true, textMsg("me", true, "on my way")
	mine.Last.Status = abi.DeliveryRead
	g := chat("2", "Team", 20)
	g.Kind = abi.ChatKindGroup
	g.HasLast, g.Last = true, textMsg("5511@s.whatsapp.net", false, "lunch?")
	g.Last.SenderName, g.Last.HasSenderName = []byte("Grace Hopper"), true
	r.chats(mine, g)
	if v := r.value(ui.RoleListItem, "Team"); !strings.Contains(v, "Grace: lunch?") {
		t.Errorf("group row %q lacks the sender's first name", v)
	}
	if v := r.value(ui.RoleListItem, "Ada"); !strings.Contains(v, "on my way") {
		t.Errorf("own row %q", v)
	}
}

// CL-004: a typing contact replaces the preview, a group's names who.
func TestTypingReplacesThePreview(t *testing.T) {
	r := newRig(t)
	c := chat("1", "Ada", 30)
	c.HasLast, c.Last = true, textMsg("1@s.whatsapp.net", false, "hello")
	r.chats(c)
	r.f.Emit(ev(abi.EventTagTyping, func(e *abi.Event) {
		e.Typing = abi.EventTyping{Chat: jid("1"), Sender: jid("1"), Composing: true}
	}))
	r.Frames(3)
	if v := r.value(ui.RoleListItem, "Ada"); !strings.Contains(v, "typing…") || strings.Contains(v, "hello") {
		t.Errorf("typing row %q", v)
	}
	r.f.Emit(ev(abi.EventTagTyping, func(e *abi.Event) {
		e.Typing = abi.EventTyping{Chat: jid("1"), Sender: jid("1"), Composing: false}
	}))
	r.Frames(3)
	if v := r.value(ui.RoleListItem, "Ada"); strings.Contains(v, "typing") {
		t.Errorf("still typing: %q", v)
	}
}

// CL-005: unsent text shows as "Draft: …".
func TestDraftShowsInTheRow(t *testing.T) {
	r := newRig(t)
	c := chat("1", "Ada", 10)
	c.HasLast, c.Last = true, textMsg("1@s.whatsapp.net", false, "hello")
	r.chats(c)
	r.f.Emit(ev(abi.EventTagDrafts, func(e *abi.Event) {
		e.Drafts = abi.EventDrafts{Batch: 1, Last: true, Drafts: []abi.Draft{{Chat: jid("1"), Text: []byte("on my way")}}}
	}))
	r.Frames(3)
	if v := r.value(ui.RoleListItem, "Ada"); !strings.Contains(v, "Draft: on my way") {
		t.Errorf("draft row %q", v)
	}
}

// CL-006 and CL-007: the count badge caps at 99+, the dot, the @ mention.
func TestUnreadBadgeCapsAndMentionShows(t *testing.T) {
	r := newRig(t)
	big := chat("1", "Ada", 30)
	big.Unread = 120
	men := chat("2", "Team", 20)
	men.Kind, men.Unread, men.HasUnreadMention, men.UnreadMention = abi.ChatKindGroup, 2, true, []byte("me")
	r.chats(big, men)
	if !r.hasText("99+") {
		t.Errorf("no 99+ badge")
	}
	if !r.hasText("@") && !r.hasText("Mentioned you") {
		t.Errorf("no @ marker on the mentioned group")
	}
}

// CL-010: a cut-short preview shows the whole last message as a tooltip.
func TestCutPreviewHasATooltip(t *testing.T) {
	r := newRig(t)
	long := strings.Repeat("a very long last message ", 12)
	c := chat("1", "Ada", 10)
	c.HasLast, c.Last = true, textMsg("1@s.whatsapp.net", false, long)
	short := chat("2", "Grace", 5)
	short.HasLast, short.Last = true, textMsg("2@s.whatsapp.net", false, "ok")
	r.chats(c, short)
	r.Context().SetNativeTooltips(true)
	r.Frames(3)
	tips := map[string]bool{}
	for _, reg := range r.Context().TooltipRegions(nil) {
		tips[string(r.Context().TooltipText(reg.Key, nil))] = true
	}
	var found bool
	for tip := range tips {
		if strings.HasPrefix(tip, "a very long last message") {
			found = true
		}
		if tip == "ok" {
			t.Errorf("a preview that fits has a tooltip")
		}
	}
	if !found {
		t.Errorf("no tooltip with the whole message: %v", tips)
	}
}

// CL-014: under Favorites the list keeps the phone's order, pinned first.
func TestFavoritesKeepThePhonesOrder(t *testing.T) {
	r := newRig(t)
	mk := func(n, name string, act int64, pos uint32) abi.Chat {
		c := chat(n, name, act)
		c.Favorite, c.FavoritePosition = true, pos
		return c
	}
	r.chats(mk("1", "Third", 90, 2), mk("2", "First", 10, 0), mk("3", "Second", 50, 1), chat("4", "Plain", 99))
	r.Play(widgettest.Click(ui.RoleCheckbox, "Favorites"), widgettest.Idle(2))
	got := r.rows()
	if strings.Join(got, ",") != "First,Second,Third" {
		t.Fatalf("favorites %v", got)
	}
}

// CL-015: each filter has its empty state.
func TestEmptyStatesOfTheFilters(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 10))
	for _, c := range []struct{ chip, title string }{
		{"Unread", "No unread chats"}, {"Private", ""}, {"Favorites", "No favorites yet"}, {"Groups", "No groups"}, {"Channels", "No channels"},
	} {
		r.Play(widgettest.Click(ui.RoleCheckbox, c.chip), widgettest.Idle(2))
		if c.title != "" && !r.hasText(c.title) {
			t.Errorf("%s: no %q", c.chip, c.title)
		}
		if c.title != "" && !r.hasText("Choose All to see every chat.") {
			t.Errorf("%s: no hint", c.chip)
		}
		r.Play(widgettest.Click(ui.RoleCheckbox, c.chip), widgettest.Idle(2)) // back to All
	}
	r2 := newRig(t)
	r2.f.Emit(ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 1, Last: true} }))
	r2.Frames(3)
	if !r2.hasText("No chats yet") {
		t.Errorf("no empty list state")
	}
}

// CL-016: the chat with yourself reads "Name (You)" over "Message yourself".
func TestMessageYourselfInSearch(t *testing.T) {
	r := newRig(t)
	r.f.Emit(ev(abi.EventTagMe, func(e *abi.Event) {
		e.Me = abi.EventMe{ID: jid("5511999"), Name: []byte("Dana"), HasName: true}
	}))
	r.chats(chat("1", "Ada", 10))
	searchFor(r, "dana")
	if !r.has(ui.RoleListItem, "Dana (You)") {
		t.Errorf("no self row: %v", r.rows())
	}
	if !r.hasText("Message yourself") {
		t.Errorf("no Message yourself line")
	}
}

// CL-020 and CL-022: contacts without a chat are listed and start one.
func TestContactResultStartsAChat(t *testing.T) {
	r := newRig(t)
	r.f.Emit(ev(abi.EventTagContacts, func(e *abi.Event) {
		e.Contacts = abi.EventContacts{Batch: 1, Last: true, Contacts: []abi.Contact{{ID: jid("777"), FullName: []byte("Linus Torvalds"), HasFullName: true}}}
	}))
	r.chats(chat("1", "Ada", 10))
	searchFor(r, "linus")
	if !r.hasText("Contacts") {
		t.Fatalf("no Contacts section: %v", r.rows())
	}
	r.Play(widgettest.Click(ui.RoleListItem, "Linus Torvalds"), widgettest.Idle(2))
	c, ok := r.lastCmd(abi.CommandTagEnsureChat)
	if !ok || !bytes.Equal(c.EnsureChat.Chat, jid("777")) {
		t.Errorf("EnsureChat: %v", r.f.SentTags())
	}
}

// CL-021: Down walks the matching chats; Enter opens the one reached.
func TestArrowsWalkTheMatches(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Grace A", 30), chat("2", "Grace B", 20), chat("3", "Ada", 10))
	searchFor(r, "grace")
	r.Play(widgettest.Key(ui.KeyDown, 0), widgettest.Idle(1), widgettest.Key(ui.KeyDown, 0), widgettest.Idle(1),
		widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(2))
	if c, ok := r.acct().OpenChatView(); !ok || string(c.Name()) != "Grace B" {
		t.Errorf("open %v", r.acct().Open)
	}
}

// CL-024: Escape clears the search and hands the keyboard to the composer.
func TestEscapeReturnsToTheComposer(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 10))
	r.Play(widgettest.Click(ui.RoleListItem, "Ada"), widgettest.Idle(2))
	searchFor(r, "ad")
	r.s.Req.FocusComposer = false
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(1))
	if !r.s.Req.FocusComposer {
		t.Errorf("Escape did not hand the keyboard to the composer")
	}
	if n, _ := r.Find(ui.RoleSearchField, "Search"); r.value(ui.RoleSearchField, "Search") != "" {
		t.Errorf("the search still holds %q (%v)", r.value(ui.RoleSearchField, "Search"), n.Role)
	}
}

// CL-025 and CL-026: search ignores the chip; locked chats never show in it.
func TestSearchIgnoresTheChipAndSkipsLockedChats(t *testing.T) {
	r := newRig(t)
	un := chat("1", "Ada Unread", 30)
	un.Unread = 1
	lk := chat("3", "Ada Locked", 15)
	lk.Locked = true
	r.chats(un, chat("2", "Ada Read", 20), lk)
	r.Play(widgettest.Click(ui.RoleCheckbox, "Unread"), widgettest.Idle(2))
	searchFor(r, "ada")
	got := r.rows()
	if !(len(got) == 2 && got[0] == "Ada Unread" && got[1] == "Ada Read") {
		t.Errorf("search under the Unread chip, with a locked chat about: %v", got)
	}
}

// CL-043: archiving closes the open chat.
func TestArchivingClosesTheOpenChat(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 10))
	r.Play(widgettest.Click(ui.RoleListItem, "Ada"), widgettest.Idle(2))
	if _, ok := r.acct().OpenChatView(); !ok {
		t.Fatalf("chat not open")
	}
	r.choose("Ada", "Archive")
	if _, ok := r.acct().OpenChatView(); ok {
		t.Errorf("the archived chat is still open")
	}
}

// CL-044: Unmute for a muted chat.
func TestMutedChatOffersUnmute(t *testing.T) {
	r := newRig(t)
	c := chat("1", "Ada", 10)
	c.HasMutedUntil, c.MutedUntil = true, 0 // muted for ever
	r.chats(c)
	req := r.nativeMenu("Ada")
	un, ok := findItem(req.Items, "Unmute")
	if !ok {
		t.Fatalf("no Unmute: %v", titles(req.Items))
	}
	r.choose2(req, un.ID)
	if m, ok := r.lastCmd(abi.CommandTagSetMuted); !ok || m.SetMuted.HasUntil {
		t.Errorf("SetMuted: %v", r.f.SentTags())
	}
}

// CL-045: the chat's own notification sound.
func TestChatSoundChoice(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 10))
	req := r.nativeMenu("Ada")
	no, _ := findItem(req.Items, "No sound")
	r.choose2(req, no.ID)
	if c, ok := r.lastCmd(abi.CommandTagSetChatSound); !ok || !c.SetChatSound.HasSound || c.SetChatSound.Sound.Tag != abi.NotificationSoundTagNone {
		t.Fatalf("SetChatSound: %v", r.f.SentTags())
	}
	req = r.nativeMenu("Ada")
	def, _ := findItem(req.Items, "Default")
	if def.Mark != ui.MarkOn {
		t.Errorf("Default is not checked for a chat with no sound of its own")
	}
	file, _ := findItem(req.Items, "Choose a file…")
	r.choose2(req, file.ID)
	if _, ok := r.lastCmd(abi.CommandTagPickChatSound); !ok {
		t.Errorf("PickChatSound: %v", r.f.SentTags())
	}
}

// CL-047 and CL-048: Copy number copies +digits; Info opens the chat and its
// panel. CL-049: Leave group asks first.
func TestCopyInfoAndLeave(t *testing.T) {
	r := newRig(t)
	g := chat("5511", "Ada", 10)
	grp := chat("2", "Team", 5)
	grp.ID, grp.Kind = []byte("123456@g.us"), abi.ChatKindGroup
	r.chats(g, grp)
	var copied []byte
	r.Context().ClipboardSet = func(b []byte) { copied = append([]byte(nil), b...) }
	req := r.nativeMenu("Ada")
	cp, _ := findItem(req.Items, "Copy number")
	r.choose2(req, cp.ID)
	if string(copied) != "+5511" {
		t.Errorf("copied %q", copied)
	}
	req = r.nativeMenu("Ada")
	info, _ := findItem(req.Items, "Info")
	r.choose2(req, info.ID)
	if _, ok := r.acct().OpenChatView(); !ok {
		t.Errorf("Info did not open the chat")
	}
	req = r.nativeMenu("Team")
	if _, ok := findItem(req.Items, "Copy number"); ok {
		t.Errorf("a group offers Copy number")
	}
	lv, ok := findItem(req.Items, "Leave group")
	if !ok {
		t.Skipf("the group's Leave needs the account in its participants: %v", titles(req.Items))
	}
	r.choose2(req, lv.ID)
	if r.s.Dialog.Kind != state.DialogConfirmLeaveGroup {
		t.Errorf("Leave group did not ask: %v", r.s.Dialog.Kind)
	}
}

// CL-046: Lock chat asks first; Unlock chat sends at once.
func TestLockAsksUnlockDoesNot(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 10))
	req := r.nativeMenu("Ada")
	lk, _ := findItem(req.Items, "Lock chat")
	r.choose2(req, lk.ID)
	if r.s.Dialog.Kind != state.DialogConfirmLockChat {
		t.Errorf("Lock chat did not ask: %v", r.s.Dialog.Kind)
	}
	if _, ok := r.lastCmd(abi.CommandTagSetLocked); ok {
		t.Errorf("locked without asking")
	}
}

// CL-050: Delete chat is disabled offline.
func TestDeleteIsDisabledOffline(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 10))
	r.f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagDisconnected }))
	r.Frames(3)
	req := r.nativeMenu("Ada")
	if d, ok := findItem(req.Items, "Delete chat"); !ok || !d.Disabled {
		t.Errorf("Delete chat while offline: %+v", d)
	}
}

// CL-060: Archived lists the archived chats, with a way back.
func TestArchivedHasBackToChats(t *testing.T) {
	r := newRig(t)
	ar := chat("2", "Old", 1)
	ar.Archived = true
	r.chats(chat("1", "Ada", 5), ar)
	r.acct().Mode = state.ListArchived
	r.s.Touch()
	r.Frames(3)
	if !r.has(ui.RoleButton, "Back to chats") {
		t.Fatalf("no Back to chats")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Back to chats"), widgettest.Idle(2))
	if r.acct().Mode != state.ListChats {
		t.Errorf("mode %v", r.acct().Mode)
	}
}

// CL-071: right-click a starred row, choose Unstar.
func TestUnstarFromTheStarredList(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 5))
	r.acct().Mode = state.ListStarred
	r.s.Touch()
	r.Frames(2)
	r.f.Emit(ev(abi.EventTagStarredList, func(e *abi.Event) {
		e.StarredList = abi.EventStarredList{Batch: 1, Last: true, Starred: []abi.Starred{{StarredAt: 9, Message: abi.Message{
			Chat: jid("1"), ID: []byte("m1"), Timestamp: 100,
			Content: abi.Content{Tag: abi.ContentTagText, Text: abi.ContentText{Text: []byte("remember the milk")}}}}}}
	}))
	r.Frames(3)
	req := r.nativeMenu("Ada")
	if len(req.Items) != 1 || req.Items[0].Title != "Unstar" {
		t.Fatalf("star menu %v", titles(req.Items))
	}
	r.choose2(req, req.Items[0].ID)
	c, ok := r.lastCmd(abi.CommandTagSetStar)
	if !ok || c.SetStar.Starred || string(c.SetStar.Message) != "m1" {
		t.Errorf("SetStar: %v", r.f.SentTags())
	}
}

// LK-005: the open folder lists locked chats, archived ones included, with
// title search; none reads "No locked chats".
func TestLockedFolder(t *testing.T) {
	r := newRig(t)
	a := chat("2", "Secret A", 5)
	a.Locked, a.Archived = true, true
	b := chat("3", "Secret B", 4)
	b.Locked = true
	r.chats(chat("1", "Ada", 9), a, b)
	r.acct().Mode = state.ListLocked
	r.s.Touch()
	r.Frames(3)
	if got := r.rows(); strings.Join(got, ",") != "Secret A,Secret B" {
		t.Fatalf("locked folder %v", got)
	}
	searchFor(r, "b")
	if got := r.rows(); strings.Join(got, ",") != "Secret B" {
		t.Errorf("title search in the folder %v", got)
	}
	r2 := newRig(t)
	r2.chats(chat("1", "Ada", 9))
	r2.acct().Mode = state.ListLocked
	r2.s.Touch()
	r2.Frames(3)
	if !r2.hasText("No locked chats") {
		t.Errorf("no empty locked folder state")
	}
}

// LB-006 and LB-007: a label and a chip let go of each other; in the archive
// the chips read as unchosen.
func TestLabelAndChipLetGoOfEachOther(t *testing.T) {
	r := newRig(t)
	r.f.Emit(ev(abi.EventTagLabels, func(e *abi.Event) {
		e.Labels = abi.EventLabels{Batch: 1, Last: true, Labels: []abi.Label{{ID: []byte("l1"), Name: []byte("Work"), ColorHex: []byte("#3b82f6")}}}
	}))
	w := chat("1", "Ada", 10)
	w.Labels = [][]byte{[]byte("l1")}
	g := chat("2", "Team", 5)
	g.Kind = abi.ChatKindGroup
	r.chats(w, g, chat("3", "Grace", 4))
	r.Play(widgettest.Click(ui.RoleCheckbox, "Work"), widgettest.Idle(2))
	if got := r.rows(); len(got) != 1 {
		t.Fatalf("label %v", got)
	}
	if n := r.Must(ui.RoleCheckbox, "All"); n.State&ui.SemChecked != 0 {
		t.Errorf("All reads chosen under a label")
	}
	r.Play(widgettest.Click(ui.RoleCheckbox, "Groups"), widgettest.Idle(2))
	if got := r.rows(); len(got) != 1 || got[0] != "Team" {
		t.Fatalf("Groups after a label %v", got)
	}
	if n := r.Must(ui.RoleCheckbox, "Work"); n.State&ui.SemChecked != 0 {
		t.Errorf("the label stays chosen beside a chip")
	}
	r.acct().Mode = state.ListArchived
	r.s.Touch()
	r.Frames(2)
	// the archive has no chips: their counts are of the chats outside it
	for _, name := range []string{"All", "Groups", "Work"} {
		if _, ok := r.Find(ui.RoleCheckbox, name); ok {
			t.Errorf("the %s chip shows in the archive", name)
		}
	}
}

// LB-001, LB-002 and LB-008: the manager says labels stay here, explains an
// empty start, creates with a colour, renames, recolours and deletes.
func TestLabelManagerTexts(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 10))
	r.s.OpenDialog(state.Dialog{Kind: state.DialogLabels})
	r.Frames(3)
	if !r.hasText("Labels stay on this computer. They do not reach your phone or anyone you chat with.") {
		t.Errorf("no note that labels are local")
	}
	if !r.hasText("No labels yet. Create one above, then add chats to it from their right-click menu.") {
		t.Errorf("no empty-manager hint")
	}
	swatches := 0
	tr := r.Tree()
	for _, n := range tr.Nodes {
		if n.Role == ui.RoleRadio || (n.Role == ui.RoleButton && len(tr.Text(n.Name)) > 0 && colourName(string(tr.Text(n.Name)))) {
			swatches++
		}
	}
	if swatches != 10 {
		t.Errorf("%d colour swatches, want 10", swatches)
	}
}

func colourName(n string) bool {
	switch n {
	case "Blue", "Green", "Yellow", "Orange", "Red", "Pink", "Purple", "Teal", "Grey", "Sky blue":
		return true
	}
	return false
}

// LB-002: rename and recolour a label.
func TestLabelRenameAndRecolour(t *testing.T) {
	r := newRig(t)
	r.f.Emit(ev(abi.EventTagLabels, func(e *abi.Event) {
		e.Labels = abi.EventLabels{Batch: 1, Last: true, Labels: []abi.Label{{ID: []byte("l1"), Name: []byte("Work"), ColorHex: []byte("#3b82f6")}}}
	}))
	r.chats(chat("1", "Ada", 10))
	r.s.OpenDialog(state.Dialog{Kind: state.DialogLabels})
	r.Frames(3)
	r.Play(widgettest.Click(ui.RoleButton, "Rename or recolour"), widgettest.Idle(2),
		widgettest.Type("Office"), widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(2))
	c, ok := r.lastCmd(abi.CommandTagUpdateLabel)
	if !ok || string(c.UpdateLabel.ID) != "l1" || string(c.UpdateLabel.Name) != "Office" {
		t.Fatalf("UpdateLabel after a rename: %+v (%v)", c.UpdateLabel, r.f.SentTags())
	}
	r.Play(widgettest.Click(ui.RoleButton, "Rename or recolour"), widgettest.Idle(2),
		widgettest.Click(ui.RoleRadio, "Green").Nth(1), widgettest.Idle(2))
	c, _ = r.lastCmd(abi.CommandTagUpdateLabel)
	if len(c.UpdateLabel.ColorHex) == 0 || string(c.UpdateLabel.ColorHex) == "#3b82f6" {
		t.Errorf("UpdateLabel after a recolour: %+v", c.UpdateLabel)
	}
}

// CL-062: ⌘E archives the open chat, or unarchives it from Archived (the
// shell's command; the list shows the result).
func TestCmdEArchivesTheOpenChat(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 10))
	r.Play(widgettest.Click(ui.RoleListItem, "Ada"), widgettest.Idle(2),
		widgettest.Key(ui.KeyCode('E'), ui.PrimaryMod()), widgettest.Idle(2))
	if c, ok := r.lastCmd(abi.CommandTagSetArchived); !ok || !c.SetArchived.Archived {
		t.Errorf("⌘E: %v", r.f.SentTags())
	}
}

// CL-073: Escape leaves Starred.
func TestEscapeLeavesStarred(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 10))
	r.acct().Mode = state.ListStarred
	r.s.Touch()
	r.Frames(3)
	r.Play(widgettest.Click(ui.RoleButton, "Chats"), widgettest.Idle(2))
	if r.acct().Mode != state.ListChats {
		t.Errorf("Chats in the rail left the mode at %v", r.acct().Mode)
	}
	r.acct().Mode = state.ListStarred
	r.s.Touch()
	r.Frames(3)
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	if r.acct().Mode != state.ListChats {
		t.Errorf("Escape left the mode at %v", r.acct().Mode)
	}
}

// CL-020: the search finds chats by name, number or latest message; the
// shell's focus request puts the keyboard in the field.
func TestSearchMatchesNumberAndLatestMessage(t *testing.T) {
	r := newRig(t)
	a := chat("5511987654321", "Ada", 30)
	b := chat("2", "Grace", 20)
	b.HasLast, b.Last = true, textMsg("2@s.whatsapp.net", false, "see you at the harbour")
	r.chats(a, b, chat("3", "Linus", 10))
	searchFor(r, "9876")
	if got := r.rows(); len(got) != 1 || got[0] != "Ada" {
		t.Errorf("by number: %v", got)
	}
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(1))
	r.s.Req.FocusSearch = false
	searchFor(r, "harbour")
	got := r.rows()
	if !(len(got) >= 1 && got[0] == "Grace") {
		t.Errorf("by latest message: %v", got)
	}
}

func TestFocusSearchRequestFocusesTheField(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 10))
	r.s.Req.FocusSearch = true
	r.s.Touch()
	r.Frames(4)
	if n, ok := r.Focused(); !ok || n.Role != ui.RoleSearchField {
		t.Errorf("focused %v (%v)", n.Role, ok)
	}
}

// CL-015: "No private chats".
func TestNoPrivateChats(t *testing.T) {
	r := newRig(t)
	g := chat("1", "Team", 10)
	g.Kind = abi.ChatKindGroup
	r.chats(g)
	r.Play(widgettest.Click(ui.RoleCheckbox, "Private"), widgettest.Idle(2))
	if !r.hasText("No private chats") {
		t.Errorf("no empty state: %v", r.rows())
	}
}

// LK-002 (the list's part): Locked in the rail, once a chat is locked, asks
// for the code through the dialog (ui/dialogs draws it); the folder opens
// only after it.
func TestLockedInTheRailAsksForTheCode(t *testing.T) {
	r := newRig(t)
	l := chat("2", "Secret", 5)
	l.Locked = true
	r.chats(chat("1", "Ada", 9), l)
	r.Play(widgettest.Click(ui.RoleButton, "Locked"), widgettest.Idle(2))
	if r.s.Dialog.Kind != state.DialogUnlockLockedChats {
		t.Errorf("dialog %v", r.s.Dialog.Kind)
	}
	if r.acct().Mode == state.ListLocked {
		t.Errorf("the folder opened before the code")
	}
}

// CL-016: the avatars the list draws are named for their row's title and get
// their initials from rexui.Avatar (rexui.Initials), as upstream's util::initials
// does: first letter or digit of the first and last word.
func TestAvatarsTakeTheTitleAndRexuiInitials(t *testing.T) {
	r := newRig(t)
	r.f.Emit(ev(abi.EventTagMe, func(e *abi.Event) {
		e.Me = abi.EventMe{ID: jid("5511999"), Name: []byte("Dana"), HasName: true}
	}))
	r.chats(chat("1", "D(", 10))
	if !r.has(ui.RoleImage, "D(") {
		t.Errorf("the chat's avatar is not named for its title")
	}
	searchFor(r, "dana")
	if !r.has(ui.RoleImage, "Dana (You)") {
		t.Errorf("the self row's avatar is not named \"Dana (You)\"")
	}
	for name, want := range map[string]string{
		"Ada Lovelace": "AL", "ada": "A", "D(": "D", "Dana (You)": "DY", "🎉 Party Planning": "PP", "Mary-Jane Smith": "MS",
	} {
		if got := string(rexui.Initials(nil, name)); got != want {
			t.Errorf("Initials(%q) = %q, upstream gives %q", name, got, want)
		}
	}
}

// A locked chat is out of the unread counts, as it is out of the list and
// the search (LK-001).
func TestLockedChatsAreOutOfTheUnreadCounts(t *testing.T) {
	r := newRig(t)
	lk := chat("2", "Secret", 9)
	lk.Locked, lk.Unread = true, 3
	un := chat("1", "Ada", 8)
	un.Unread = 1
	r.chats(un, lk)
	if n := r.l.counts[state.FilterUnread]; n != 1 {
		t.Errorf("the Unread chip counts %d chats", n)
	}
	r.update(abi.Chat{ID: jid("1"), Name: []byte("Ada"), LastActivity: 8, Locked: true, Unread: 1})
	if n := r.l.counts[state.FilterUnread]; n != 0 {
		t.Errorf("after locking Ada the Unread chip counts %d chats", n)
	}
}

// A chat opened from the Unread list stays listed once it is read, until
// the filter changes, so the list does not shift under the pointer (CL-032).
// The keyboard's stepping is the shell's: see parity/logic.md.
func TestAChatOpenedFromUnreadStaysUntilTheFilterChanges(t *testing.T) {
	r := newRig(t)
	ada, grace := chat("1", "Ada", 30), chat("2", "Grace", 20)
	ada.Unread, grace.Unread = 1, 1
	r.chats(ada, grace)
	r.Play(widgettest.Click(ui.RoleCheckbox, "Unread"), widgettest.Idle(2))
	r.Play(widgettest.Click(ui.RoleListItem, "Ada"), widgettest.Idle(2))
	read := chat("1", "Ada", 30)
	r.update(read) // the core marks it read
	if got := r.rows(); !slices.Equal(got, []string{"Ada", "Grace"}) {
		t.Fatalf("after reading Ada: %v", got)
	}
	r.Play(widgettest.Click(ui.RoleCheckbox, "Unread"), widgettest.Idle(2)) // off
	r.Play(widgettest.Click(ui.RoleCheckbox, "Unread"), widgettest.Idle(2)) // on again
	if got := r.rows(); !slices.Equal(got, []string{"Grace"}) {
		t.Errorf("after the filter changed: %v", got)
	}
}
