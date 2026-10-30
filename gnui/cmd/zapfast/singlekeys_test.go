package main

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// The single keys (ui/shell/keyboard.go): a lone key, after a pause, is its
// shortcut; a burst is typing, which goes to the open chat's composer; in
// the composer no key is a shortcut.
func TestSingleKeys(t *testing.T) {
	t.Run("a lone J opens the next chat", func(t *testing.T) {
		r, _ := keyRig(t)
		r.open(adaJID)
		r.Play(widgettest.Key('J', 0), widgettest.Pause(200), widgettest.Idle(2))
		if got := r.openName(); got != "Grace Hopper" {
			t.Fatalf("J opened %q, want Grace Hopper", got)
		}
		r.Play(widgettest.Key('K', 0), widgettest.Pause(200), widgettest.Idle(2))
		if got := r.openName(); got != "Ada Lovelace" {
			t.Fatalf("K opened %q, want Ada Lovelace", got)
		}
	})
	t.Run("a burst is typing, for the composer", func(t *testing.T) {
		r, f := keyRig(t)
		r.open(adaJID)
		n := sentTag(f, abi.CommandTagSetArchived)
		// E then M a frame apart: a word, not two shortcuts
		r.Play(widgettest.Key('E', 0), widgettest.Key('M', 0), widgettest.Pause(200), widgettest.Idle(3))
		if sentTag(f, abi.CommandTagSetArchived) != n || sentTag(f, abi.CommandTagSetMuted) != 0 {
			t.Fatal("a burst ran its letters as shortcuts")
		}
		if got := r.openName(); got != "Ada Lovelace" {
			t.Fatalf("the burst moved to %q", got)
		}
		if r.focusedName() != "Message" {
			t.Fatalf("the burst left the focus on %q, want the composer", r.focusedName())
		}
	})
	t.Run("in the composer no key is a shortcut", func(t *testing.T) {
		r, f := keyRig(t)
		r.open(adaJID)
		r.s.Req.FocusComposer = true
		r.s.Touch()
		r.Frames(3)
		r.Play(widgettest.Key('E', 0), widgettest.Pause(200), widgettest.Idle(2))
		if sentTag(f, abi.CommandTagSetArchived) != 0 {
			t.Fatal("E archived while typing in the composer")
		}
	})
	t.Run("E archives and Z puts it back", func(t *testing.T) {
		r, f := keyRig(t)
		r.open(adaJID)
		r.Play(widgettest.Key('E', 0), widgettest.Pause(200), widgettest.Idle(2))
		c, ok := lastSent(f, abi.CommandTagSetArchived)
		if !ok || !c.SetArchived.Archived {
			t.Fatalf("E sent %v", f.SentTags())
		}
		r.Play(widgettest.Key('Z', 0), widgettest.Pause(200), widgettest.Idle(2))
		if c, ok := lastSent(f, abi.CommandTagSetArchived); !ok || c.SetArchived.Archived {
			t.Fatalf("Z did not unarchive: %v", f.SentTags())
		}
	})
	t.Run("G then A goes to Archived", func(t *testing.T) {
		r, _ := keyRig(t)
		r.Play(widgettest.Key('G', 0), widgettest.Pause(200), widgettest.Key('A', 0), widgettest.Idle(2))
		if m := r.s.Accounts[0].Mode; m != state.ListArchived {
			t.Fatalf("mode %v, want Archived", m)
		}
	})
	t.Run("Esc lets go of the composer before it closes the chat", func(t *testing.T) {
		r, _ := keyRig(t)
		r.open(adaJID)
		r.s.Req.FocusComposer = true
		r.s.Touch()
		r.Frames(3)
		r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(3))
		if r.focusedName() == "Message" || r.openName() != "Ada Lovelace" {
			t.Fatalf("Esc: focus %q, open %q; want the chat open, the composer let go", r.focusedName(), r.openName())
		}
	})
}

// lastSent is the last command with tag the core was sent.
func lastSent(f *fake.Core, tag abi.CommandTag) (abi.Command, bool) {
	sent := f.Sent()
	for i := len(sent) - 1; i >= 0; i-- {
		if sent[i].Tag == tag {
			return sent[i], true
		}
	}
	return abi.Command{}, false
}

// Spotlight (ui/spotlight): ⌘K opens it on the field, a name finds its chat,
// Enter opens it; Escape closes; / opens it as a single key.
func TestSpotlight(t *testing.T) {
	r, _ := keyRig(t)
	r.Play(widgettest.Key('K', prim), widgettest.Idle(3))
	if !r.s.Spotlight {
		t.Fatal("⌘K did not open Spotlight")
	}
	r.Play(widgettest.Type("grace"), widgettest.Idle(3))
	r.Play(widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(3))
	if r.s.Spotlight || r.openName() != "Grace Hopper" {
		t.Fatalf("Enter: open %q, Spotlight %v; want Grace Hopper, closed", r.openName(), r.s.Spotlight)
	}
	r.Play(widgettest.Key('/', 0), widgettest.Idle(3))
	if !r.s.Spotlight {
		t.Fatal("/ did not open Spotlight")
	}
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(3))
	if r.s.Spotlight {
		t.Fatal("Escape did not close Spotlight")
	}
}

// Folders and spaces (state/folders.go, chatlist/folders.go, shell/spaces.go):
// a folder's row shows over its chats and closes over them; a folder shown
// as a space is ⌃2, which narrows the list to it; ⌃1 is the whole account.
func TestFoldersAndSpaces(t *testing.T) {
	r, _ := keyRig(t)
	a := r.s.Accounts[0]
	work := a.FolderByID(a.NewFolder("Work", "briefcase"))
	a.MoveToFolder([]byte(graceJID), work)
	r.s.Touch()
	r.Frames(3)
	if _, ok := r.Find(ui.RoleButton, "Work"); !ok {
		t.Fatalf("no folder row:\n%s", widgettest.Dump(r.Tree()))
	}
	if _, ok := r.Find(ui.RoleListItem, "Grace Hopper"); !ok {
		t.Fatal("the open folder hides its chat")
	}
	r.Click(ui.RoleButton, "Work") // closes it
	r.Frames(3)
	if _, ok := r.Find(ui.RoleListItem, "Grace Hopper"); ok || !a.Settings.Folders[work].Collapsed {
		t.Fatal("the closed folder still shows its chat")
	}
	r.Click(ui.RoleButton, "Work")
	a.EditFolder(work, func(f *state.Folder) { f.Space = true })
	r.s.Touch()
	r.Frames(3)
	r.Play(widgettest.Key('2', ui.ModCtrl), widgettest.Idle(4))
	if a.SpaceFolder != a.Settings.Folders[work].ID {
		t.Fatalf("⌃2 went to %q, want the Work space", a.SpaceFolder)
	}
	if _, ok := r.Find(ui.RoleListItem, "Ada Lovelace"); ok {
		t.Fatal("the Work space shows a chat outside it")
	}
	if _, ok := r.Find(ui.RoleButton, "Work###space 1"); !ok {
		if _, ok := r.Find(ui.RoleButton, "Work"); !ok {
			t.Fatalf("no strip button for the space:\n%s", widgettest.Dump(r.Tree()))
		}
	}
	r.Play(widgettest.Key('1', ui.ModCtrl), widgettest.Idle(4))
	if a.SpaceFolder != "" {
		t.Fatal("⌃1 did not go back to the whole account")
	}
	if _, ok := r.Find(ui.RoleListItem, "Ada Lovelace"); !ok {
		t.Fatal("the account's space hides Ada")
	}
}

// Peek (ui/peek): ⌥-click on a row opens it on the chat, which stays unread
// (no MarkRead goes to the core); a reply from it is sent and reads nothing;
// Open opens the chat.
func TestPeek(t *testing.T) {
	r, f := keyRig(t)
	a := r.s.Accounts[0]
	// Grace has unread messages: opening her chat would read them
	f.Emit(ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 2, Last: true, Chats: []abi.Chat{
			{ID: []byte(graceJID), Name: []byte("Grace Hopper"), LastActivity: 30, Unread: 3},
		}}
	}))
	r.Frames(4)
	if ch, _ := a.Model.Chats.Get(a.Model.Chats.Find([]byte(graceJID))); ch.Unread() == 0 {
		t.Fatal("the rig's Grace has nothing unread")
	}
	r.Play(widgettest.Click(ui.RoleListItem, "Grace Hopper").With(ui.ModAlt), widgettest.Idle(4))
	if !r.s.Peek.Open() || a.Open != 0 {
		t.Fatalf("⌥-click: peek %v, open %v; want a peek, no chat open", r.s.Peek, a.Open)
	}
	r.Play(widgettest.Type("on my way"), widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(3))
	if c, ok := lastSent(f, abi.CommandTagSendText); !ok || string(c.SendText.Text) != "on my way" {
		t.Fatalf("the reply sent %v", f.SentTags())
	}
	if sentTag(f, abi.CommandTagMarkRead) != 0 {
		t.Fatal("a peek marked the chat read")
	}
	if ch, _ := a.Model.Chats.Get(a.Model.Chats.Find([]byte(graceJID))); ch.Unread() == 0 {
		t.Fatal("after the peek and its reply Grace's chat is read")
	}
	// the side buttons come out once the panel has landed
	r.Play(widgettest.Settle())
	r.Click(ui.RoleButton, "Open")
	r.Frames(3)
	if r.s.Peek.Open() || r.openName() != "Grace Hopper" {
		t.Fatalf("Open: peek %v, open %q", r.s.Peek.Open(), r.openName())
	}
}

// A peek with a reply typed asks before Escape throws it away, as Zen's
// glance asks: the first Escape turns the close button into a red Close, the
// second closes; with nothing typed one Escape closes, and the panel flies
// back before the peek is gone.
func TestThePeekAsksBeforeADraftGoes(t *testing.T) {
	r, _ := keyRig(t)
	r.Play(widgettest.Click(ui.RoleListItem, "Ada Lovelace").With(ui.ModAlt), widgettest.Settle())
	if !r.s.Peek.Open() {
		t.Fatal("⌥-click opened no peek")
	}
	r.Play(widgettest.Type("half a thou"), widgettest.Key(ui.KeyEscape, 0), widgettest.Settle())
	if !r.s.Peek.Open() {
		t.Fatal("the first Escape over a draft closed the peek")
	}
	if _, ok := r.Find(ui.RoleButton, "Close"); !ok {
		t.Fatal("asked, there is no Close button")
	}
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Settle())
	if r.s.Peek.Open() {
		t.Fatal("the second Escape left the peek open")
	}

	r.Play(widgettest.Click(ui.RoleListItem, "Ada Lovelace").With(ui.ModAlt), widgettest.Settle())
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(1))
	if !r.s.Peek.Open() {
		t.Fatal("the peek went at once: it flies back to its row first")
	}
	r.Play(widgettest.Settle())
	if r.s.Peek.Open() {
		t.Fatal("one Escape with nothing typed left the peek open")
	}
}
