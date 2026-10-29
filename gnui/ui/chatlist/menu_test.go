package chatlist

import (
	"bytes"
	"slices"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

// choose right-clicks the chat and picks the menu's item.
func (r *rig) choose(chatName, item string) {
	r.tt.Helper()
	r.Play(widgettest.RClick(ui.RoleListItem, chatName), widgettest.Idle(2),
		widgettest.Click(ui.RoleMenuItem, item), widgettest.Idle(2))
}

func TestMenuMarkReadAndUnread(t *testing.T) {
	r := newRig(t)
	un := chat("1", "Ada", 10)
	un.Unread = 2
	r.chats(un, chat("2", "Grace", 5))
	r.choose("Ada", "Mark as read")
	if _, ok := r.lastCmd(abi.CommandTagMarkRead); !ok {
		t.Errorf("MarkRead not sent: %v", r.f.SentTags())
	}
	r.choose("Grace", "Mark as unread")
	if c, ok := r.lastCmd(abi.CommandTagMarkUnread); !ok || !bytes.Equal(c.MarkUnread.Chat, jid("2")) {
		t.Errorf("MarkUnread not sent for Grace: %v", r.f.SentTags())
	}
	if r.has(ui.RoleMenu, "Chat menu") {
		t.Errorf("the menu stays open after a choice")
	}
}

func TestMenuPinFavoriteArchive(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 10))
	r.choose("Ada", "Pin to top")
	if c, ok := r.lastCmd(abi.CommandTagSetPinned); !ok || !c.SetPinned.Pinned {
		t.Errorf("SetPinned: %v", r.f.SentTags())
	}
	r.choose("Ada", "Add to favorites")
	if c, ok := r.lastCmd(abi.CommandTagSetFavorite); !ok || !c.SetFavorite.Favorite {
		t.Errorf("SetFavorite: %v", r.f.SentTags())
	}
	r.choose("Ada", "Archive")
	if c, ok := r.lastCmd(abi.CommandTagSetArchived); !ok || !c.SetArchived.Archived {
		t.Errorf("SetArchived: %v", r.f.SentTags())
	}
}

func TestPinPastTheLimitToastsInstead(t *testing.T) {
	r := newRig(t)
	var cs []abi.Chat
	for i, n := range []string{"1", "2", "3"} {
		c := chat(n, "P"+n, int64(10+i))
		c.Pinned, c.PinnedAt = true, int64(i+1)
		cs = append(cs, c)
	}
	cs = append(cs, chat("4", "Extra", 1))
	r.chats(cs...)
	r.choose("Extra", "Pin to top")
	if _, ok := r.lastCmd(abi.CommandTagSetPinned); ok {
		t.Errorf("pinned past the limit")
	}
	if len(r.s.Toasts) == 0 || string(r.s.Toasts[len(r.s.Toasts)-1].Text) != "You can only pin 3 chats" {
		t.Errorf("toasts %v", r.s.Toasts)
	}
}

func TestMenuMuteAndDelete(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 10))
	r.choose("Ada", "Mute indefinitely")
	if c, ok := r.lastCmd(abi.CommandTagSetMuted); !ok || !c.SetMuted.HasUntil || c.SetMuted.Until != 0 {
		t.Errorf("SetMuted: %v", r.f.SentTags())
	}
	r.choose("Ada", "Delete chat")
	// deleting asks first
	if r.s.Dialog.Kind != state.DialogConfirmDeleteChat {
		t.Errorf("no confirmation: dialog %v", r.s.Dialog.Kind)
	}
	if _, ok := r.lastCmd(abi.CommandTagDeleteChat); ok {
		t.Errorf("deleted without asking")
	}
}

func TestMenuLabelsSubmenuTogglesALabel(t *testing.T) {
	r := newRig(t)
	r.f.Emit(ev(abi.EventTagLabels, func(e *abi.Event) {
		e.Labels = abi.EventLabels{Batch: 1, Last: true, Labels: []abi.Label{{ID: []byte("l1"), Name: []byte("Work"), ColorHex: []byte("#3b82f6")}}}
	}))
	r.chats(chat("1", "Ada", 10))
	if !r.has(ui.RoleCheckbox, "Work") {
		t.Fatalf("no label chip")
	}
	r.Play(widgettest.RClick(ui.RoleListItem, "Ada"), widgettest.Idle(2),
		widgettest.Click(ui.RoleMenuItem, "Labels"), widgettest.Idle(2),
		widgettest.Click(ui.RoleMenuItem, "Work"), widgettest.Idle(2))
	c, ok := r.lastCmd(abi.CommandTagSetChatLabels)
	if !ok || len(c.SetChatLabels.Labels) != 1 || string(c.SetChatLabels.Labels[0]) != "l1" {
		t.Fatalf("SetChatLabels: %v", r.f.SentTags())
	}
}

func TestLabelChipFiltersToItsChats(t *testing.T) {
	r := newRig(t)
	r.f.Emit(ev(abi.EventTagLabels, func(e *abi.Event) {
		e.Labels = abi.EventLabels{Batch: 1, Last: true, Labels: []abi.Label{{ID: []byte("l1"), Name: []byte("Work"), ColorHex: []byte("#3b82f6")}}}
	}))
	w := chat("1", "Ada", 10)
	w.Labels = [][]byte{[]byte("l1")}
	r.chats(w, chat("2", "Grace", 5))
	r.Play(widgettest.Click(ui.RoleCheckbox, "Work"), widgettest.Idle(2))
	if got := r.rows(); len(got) != 1 || got[0] != "Ada" {
		t.Fatalf("rows %v", got)
	}
}

func TestChannelsMenuMutesAllChannels(t *testing.T) {
	r := newRig(t)
	b := chat("1", "News", 10)
	b.ID = []byte("1@newsletter")
	r.chats(b)
	r.Play(widgettest.RClick(ui.RoleCheckbox, "Channels"), widgettest.Idle(2),
		widgettest.Click(ui.RoleMenuItem, "Mute all channels"), widgettest.Idle(2))
	if c, ok := r.lastCmd(abi.CommandTagSetMuted); !ok || string(c.SetMuted.Chat) != "1@newsletter" || !c.SetMuted.HasUntil {
		t.Errorf("SetMuted: %v", r.f.SentTags())
	}
}

func TestLabelManagerCreatesAndDeletes(t *testing.T) {
	r := newRig(t)
	r.f.Emit(ev(abi.EventTagLabels, func(e *abi.Event) {
		e.Labels = abi.EventLabels{Batch: 1, Last: true, Labels: []abi.Label{{ID: []byte("l1"), Name: []byte("Work"), ColorHex: []byte("#3b82f6")}}}
	}))
	r.chats(chat("1", "Ada", 10))
	r.Play(widgettest.Click(ui.RoleButton, "Manage labels"), widgettest.Idle(3))
	if !r.hasText("Labels") {
		t.Fatalf("the manager did not open")
	}
	r.Play(widgettest.Click(ui.RoleTextField, "New label"), widgettest.Type("Home"),
		widgettest.Click(ui.RoleButton, "Create"), widgettest.Idle(2))
	if c, ok := r.lastCmd(abi.CommandTagCreateLabel); !ok || string(c.CreateLabel.Name) != "Home" {
		t.Fatalf("CreateLabel: %v", r.f.SentTags())
	}
	// a duplicate name is refused with a toast and nothing is sent
	n := len(r.f.Sent())
	r.Play(widgettest.Click(ui.RoleTextField, "New label"), widgettest.Type("work"),
		widgettest.Click(ui.RoleButton, "Create"), widgettest.Idle(2))
	if len(r.f.Sent()) != n {
		t.Errorf("a duplicate label was sent")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Delete this label. Its chats keep their messages."), widgettest.Idle(2))
	if c, ok := r.lastCmd(abi.CommandTagDeleteLabel); !ok || string(c.DeleteLabel.ID) != "l1" {
		t.Fatalf("DeleteLabel: %v", r.f.SentTags())
	}
}

func TestToolbarNewChatAndHide(t *testing.T) {
	r := newRig(t)
	r.chats(chat("1", "Ada", 10))
	if !r.has(ui.RoleButton, "New chat (⌘N)") || !r.has(ui.RoleButton, "Hide the chat list (⌘B)") {
		t.Fatalf("toolbar items missing")
	}
	r.Play(widgettest.Click(ui.RoleButton, "New chat (⌘N)"), widgettest.Idle(2))
	if r.s.Dialog.Kind == 0 {
		t.Errorf("New chat opened nothing")
	}
}

// CL-001: Hide the chat list is left out of a narrow window (the list is the
// whole window then), and over the starred messages the header has no New
// chat (zf:src/ui/chats.rs:381-400). The list's toggle is the titlebar's,
// beside the account, on every page of the chat list, starred included.
func TestToolbarButtonsFollowTheWindowAndThePage(t *testing.T) {
	r := newRigSized(t, 1, 600, 700)
	r.chats(chat("1", "Ada", 10))
	if !r.has(ui.RoleButton, "New chat (⌘N)") {
		t.Error("no New chat in a narrow window")
	}
	if r.has(ui.RoleButton, "Hide the chat list (⌘B)") {
		t.Error("Hide the chat list in a narrow window")
	}
	r = newRig(t)
	r.chats(chat("1", "Ada", 10))
	r.acct().Mode = state.ListStarred
	r.s.Touch()
	r.Frames(3)
	if r.has(ui.RoleButton, "New chat (⌘N)") {
		t.Error("New chat over the starred messages")
	}
	if !r.has(ui.RoleButton, "Hide the chat list (⌘B)") {
		t.Error("no list toggle over the starred messages")
	}
	if !r.has(ui.RoleLabel, "Starred") || !r.has(ui.RoleButton, "Back to chats") {
		t.Errorf("no title or Back over the starred messages:\n%s", widgettest.Dump(r.Tree()))
	}
}

// nativeMenu right-clicks the row of the chat and returns the native menu
// that was asked for; choose then answers it as the app would.
func (r *rig) nativeMenu(name string) ui.MenuRequest {
	r.tt.Helper()
	r.Context().SetNativeMenus(true)
	r.Play(widgettest.RClick(ui.RoleListItem, name), widgettest.Idle(1))
	req, ok := r.Context().TakeMenuRequest()
	if !ok || req.Kind != ui.MenuItems {
		r.tt.Fatalf("no native menu for %q: %+v", name, req)
	}
	return req
}

func (r *rig) choose2(req ui.MenuRequest, id int) {
	r.ans.owner, r.ans.item, r.ans.outcome = req.Owner, id, ui.MenuChosen
	r.Frames(2)
}

func findItem(items []ui.MenuItem, title string) (ui.MenuItem, bool) {
	for _, it := range items {
		if it.Title == title {
			return it, true
		}
		if x, ok := findItem(it.Sub, title); ok {
			return x, true
		}
	}
	return ui.MenuItem{}, false
}

func titles(items []ui.MenuItem) []string {
	var out []string
	for _, it := range items {
		if it.Separator {
			out = append(out, "-")
		} else {
			out = append(out, it.Title)
		}
	}
	return out
}

func TestNativeMenuHasUpstreamsItemsInOrder(t *testing.T) {
	r := newRig(t)
	un := chat("1", "Ada", 10)
	un.Unread = 1
	r.chats(un)
	req := r.nativeMenu("Ada")
	// Zap's Float first (no chat is open to split beside), then upstream's
	want := []string{"Float over other apps", "-", "Mark as read", "Pin to top", "Add to favorites", "Archive", "Mute for 8 hours", "Mute for a week", "Mute indefinitely",
		"Notification sound", "Folder", "Labels", "Lock chat", "-", "Copy number", "Info", "Delete chat"}
	if got := titles(req.Items); !slices.Equal(got, want) {
		t.Fatalf("items\n got %v\nwant %v", got, want)
	}
	if d, _ := findItem(req.Items, "Delete chat"); !d.Destructive {
		t.Errorf("Delete chat is not destructive")
	}
	if r.has(ui.RoleMenu, "Chat menu") {
		t.Errorf("a drawn menu opened beside the native one")
	}
}

// CL-042: channels have neither favorite action, even if a stale row says
// favorite. Only Delete chat waits for a connection (src/ui/chats.rs:1989-2106).
func TestChannelMenuItemsAndEnabledStates(t *testing.T) {
	for _, connected := range []bool{true, false} {
		for _, favorite := range []bool{false, true} {
			r := newRig(t)
			ch := chat("1", "News", 10)
			ch.ID, ch.Favorite = []byte("1@newsletter"), favorite
			r.chats(ch)
			r.Play(widgettest.Click(ui.RoleCheckbox, "Channels"), widgettest.Idle(2))
			if !connected {
				r.f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagDisconnected }))
				r.Frames(2)
			}
			req := r.nativeMenu("News")
			want := []string{"Float over other apps", "-", "Mark as unread", "Pin to top", "Archive", "Leave channel", "Mute for 8 hours", "Mute for a week", "Mute indefinitely",
				"Notification sound", "Folder", "Labels", "Lock chat", "-", "Info", "Delete chat"}
			if got := titles(req.Items); !slices.Equal(got, want) {
				t.Fatalf("connected=%v favorite=%v: items %v, want %v", connected, favorite, got, want)
			}
			for _, it := range req.Items {
				if wantDisabled := it.Title == "Delete chat" && !connected; it.Disabled != wantDisabled {
					t.Errorf("connected=%v: %q disabled=%v, want %v", connected, it.Title, it.Disabled, wantDisabled)
				}
			}
		}
	}
}

func TestNativeMenuChoicesSendTheCommands(t *testing.T) {
	r := newRig(t)
	un := chat("1", "Ada", 10)
	un.Unread = 1
	r.chats(un)
	req := r.nativeMenu("Ada")
	it, _ := findItem(req.Items, "Pin to top")
	r.choose2(req, it.ID)
	if c, ok := r.lastCmd(abi.CommandTagSetPinned); !ok || !c.SetPinned.Pinned {
		t.Fatalf("SetPinned: %v", r.f.SentTags())
	}
	req = r.nativeMenu("Ada")
	it, _ = findItem(req.Items, "Mark as read")
	r.choose2(req, it.ID)
	if _, ok := r.lastCmd(abi.CommandTagMarkRead); !ok {
		t.Errorf("MarkRead: %v", r.f.SentTags())
	}
	// deleting asks first
	req = r.nativeMenu("Ada")
	it, _ = findItem(req.Items, "Delete chat")
	r.choose2(req, it.ID)
	if r.s.Dialog.Kind != state.DialogConfirmDeleteChat {
		t.Errorf("no confirmation: %v", r.s.Dialog.Kind)
	}
	// the choice comes once
	n := len(r.f.Sent())
	r.Frames(3)
	if len(r.f.Sent()) != n {
		t.Errorf("the choice acted twice")
	}
}

func TestNativeLabelsSubmenuMarksTheLabelsTheChatWears(t *testing.T) {
	r := newRig(t)
	r.f.Emit(ev(abi.EventTagLabels, func(e *abi.Event) {
		e.Labels = abi.EventLabels{Batch: 1, Last: true, Labels: []abi.Label{
			{ID: []byte("l1"), Name: []byte("Work"), ColorHex: []byte("#3b82f6")}, {ID: []byte("l2"), Name: []byte("Family"), ColorHex: []byte("#22c55e")}}}
	}))
	c := chat("1", "Ada", 10)
	c.Labels = [][]byte{[]byte("l2")}
	r.chats(c)
	req := r.nativeMenu("Ada")
	work, _ := findItem(req.Items, "Work")
	fam, _ := findItem(req.Items, "Family")
	if work.Mark != ui.MarkNone || fam.Mark != ui.MarkOn {
		t.Fatalf("marks: Work %v Family %v", work.Mark, fam.Mark)
	}
	r.choose2(req, work.ID)
	cmd, ok := r.lastCmd(abi.CommandTagSetChatLabels)
	if !ok || len(cmd.SetChatLabels.Labels) != 2 {
		t.Fatalf("SetChatLabels: %v", r.f.SentTags())
	}
	r.nativeMenu("Ada") // the menu is built per click: nothing stale
}

func TestNativeChannelsChipMenuAndUnstar(t *testing.T) {
	r := newRig(t)
	b := chat("1", "News", 10)
	b.ID = []byte("1@newsletter")
	r.chats(b)
	r.Context().SetNativeMenus(true)
	r.Play(widgettest.RClick(ui.RoleCheckbox, "Channels"), widgettest.Idle(1))
	req, ok := r.Context().TakeMenuRequest()
	if !ok || len(req.Items) != 1 || req.Items[0].Title != "Mute all channels" {
		t.Fatalf("request %+v", req)
	}
	r.choose2(req, req.Items[0].ID)
	if c, ok := r.lastCmd(abi.CommandTagSetMuted); !ok || string(c.SetMuted.Chat) != "1@newsletter" {
		t.Errorf("SetMuted: %v", r.f.SentTags())
	}
}

// AX-004: the chat list's controls have names a screen reader says: the
// chips, the label chips, a row's menu button, and in the label manager
// each label's colour swatches (labels.rs:65), rename and delete.
func TestTheChatListAndLabelManagerNameTheirControls(t *testing.T) {
	r := newRig(t)
	r.f.Emit(ev(abi.EventTagLabels, func(e *abi.Event) {
		e.Labels = abi.EventLabels{Batch: 1, Last: true, Labels: []abi.Label{{ID: []byte("l1"), Name: []byte("Work"), ColorHex: []byte("#3b82f6")}}}
	}))
	r.chats(chat("1", "Ada", 10), chat("2", "Grace", 5))
	r.LintNames("chat list")
	r.Play(widgettest.Click(ui.RoleButton, "Manage labels"), widgettest.Idle(3))
	if !r.hasText("Labels") {
		t.Fatal("the manager did not open")
	}
	r.LintNames("label manager")
}
