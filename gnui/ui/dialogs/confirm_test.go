package dialogs

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/model"
	"zapfast/state"
)

func baseEvents() []*abi.Event {
	return []*abi.Event{linkedEvent(), chatsEvent(
		chat("1@s.whatsapp.net", "Ada Lovelace", 20),
		chat("2@s.whatsapp.net", "Grace Hopper", 10),
		chat("12345@g.us", "Analytical Engine", 5),
	)}
}

// DL-009: Delete chat? explains it deletes the chat here and on the phone;
// Delete sends DeleteChat and closes, Cancel and Escape send nothing.
func TestDeleteChatAsksFirst(t *testing.T) {
	r := newRig(t, baseEvents()...)
	ada := r.ref("1@s.whatsapp.net")
	r.open(state.Dialog{Kind: state.DialogConfirmDeleteChat, Chat: ada})
	if !r.has(ui.RoleButton, "Delete") || !r.has(ui.RoleButton, "Cancel") {
		t.Fatalf("no Delete and Cancel buttons:\n%s", widgettest.Dump(r.Tree()))
	}
	if !r.has(ui.RoleLabel, "This deletes your chat with Ada Lovelace, including all messages, on this computer and on your phone. It cannot be undone.") {
		t.Error("the explanation does not name the chat")
	}
	r.click(ui.RoleButton, "Cancel")
	if r.s.Dialog.Open() || len(r.sent(abi.CommandTagDeleteChat)) != 0 {
		t.Fatalf("Cancel left the dialog %v, sent %v", r.s.Dialog.Kind, r.f.SentTags())
	}
	r.open(state.Dialog{Kind: state.DialogConfirmDeleteChat, Chat: ada})
	r.Key(ui.KeyEscape, 0)
	r.Frames(2)
	if r.s.Dialog.Open() || len(r.sent(abi.CommandTagDeleteChat)) != 0 {
		t.Fatalf("Escape left the dialog %v, sent %v", r.s.Dialog.Kind, r.f.SentTags())
	}
	// Enter does not choose Delete: a stray key must not destroy a chat.
	r.open(state.Dialog{Kind: state.DialogConfirmDeleteChat, Chat: ada})
	r.Key(ui.KeyEnter, 0)
	r.Frames(2)
	if len(r.sent(abi.CommandTagDeleteChat)) != 0 {
		t.Fatal("Enter confirmed a destructive question")
	}
	r.open(state.Dialog{Kind: state.DialogConfirmDeleteChat, Chat: ada})
	r.click(ui.RoleButton, "Delete")
	got := r.sent(abi.CommandTagDeleteChat)
	if len(got) != 1 || string(got[0].DeleteChat.Chat) != "1@s.whatsapp.net" {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	if r.s.Dialog.Open() {
		t.Error("the dialog stays open after Delete")
	}
}

// DL-010: Clear chat? clears the messages and keeps the chat.
func TestClearChat(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogConfirmClearChat, Chat: r.ref("2@s.whatsapp.net")})
	if !r.has(ui.RoleButton, "Clear chat") {
		t.Fatal("no Clear chat button")
	}
	r.click(ui.RoleButton, "Clear chat")
	got := r.sent(abi.CommandTagClearChat)
	if len(got) != 1 || string(got[0].ClearChat.Chat) != "2@s.whatsapp.net" {
		t.Fatalf("sent %v", r.f.SentTags())
	}
}

// DL-011: leaving a group keeps the local history and offers "Leave … and
// archive"; a channel says channel.
func TestLeaveGroupOffersArchive(t *testing.T) {
	r := newRig(t, baseEvents()...)
	grp := r.ref("12345@g.us")
	r.open(state.Dialog{Kind: state.DialogConfirmLeaveGroup, Chat: grp})
	if !r.has(ui.RoleButton, "Leave group and archive") || !r.has(ui.RoleButton, "Leave group") {
		t.Fatalf("buttons:\n%s", widgettest.Dump(r.Tree()))
	}
	r.click(ui.RoleButton, "Leave group and archive")
	got := r.sent(abi.CommandTagLeaveGroup)
	if len(got) != 1 || !got[0].LeaveGroup.Archive || string(got[0].LeaveGroup.Chat) != "12345@g.us" {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	if r.s.Dialog.Open() {
		t.Error("the dialog stays open")
	}
	r.open(state.Dialog{Kind: state.DialogConfirmLeaveGroup, Chat: grp})
	r.click(ui.RoleButton, "Leave group")
	got = r.sent(abi.CommandTagLeaveGroup)
	if len(got) != 2 || got[1].LeaveGroup.Archive {
		t.Fatalf("plain leave sent %v", r.f.SentTags())
	}
}

func TestLeaveChannelWords(t *testing.T) {
	r := newRig(t, linkedEvent(), chatsEvent(chat("999@newsletter", "News", 1)))
	r.open(state.Dialog{Kind: state.DialogConfirmLeaveGroup, Chat: r.ref("999@newsletter")})
	for _, b := range []string{"Leave channel", "Leave channel and archive"} {
		if !r.has(ui.RoleButton, b) {
			t.Errorf("no %q button", b)
		}
	}
}

// Deleting messages: for me is DeleteLocal, for everyone is Revoke, one per
// message; a confirmed selection ends selection mode.
func TestDeleteMessages(t *testing.T) {
	r := newRig(t, baseEvents()...)
	ada := r.ref("1@s.whatsapp.net")
	r.a.OpenChat(ada)
	var ids model.IDList
	ids.Add([]byte("m1"))
	ids.Add([]byte("m2"))
	r.s.Panes.Selecting = true
	r.s.Panes.Selection.Add([]byte("m1"))
	r.open(state.Dialog{Kind: state.DialogConfirmDeleteSelection, Chat: ada, IDs: ids})
	if !r.has(ui.RoleLabel, "2 selected messages") {
		t.Errorf("no count:\n%s", widgettest.Dump(r.Tree()))
	}
	if !r.has(ui.RoleLabel, "Delete for me?") && r.Tree() != nil {
		// the title is the dialog's name
		if _, ok := r.Find(ui.RoleDialog, "Delete for me?"); !ok {
			t.Error("not titled Delete for me?")
		}
	}
	r.click(ui.RoleButton, "Delete")
	got := r.sent(abi.CommandTagDeleteLocal)
	if len(got) != 2 || string(got[0].DeleteLocal.ID) != "m1" || string(got[1].DeleteLocal.ID) != "m2" || string(got[0].DeleteLocal.Chat) != "1@s.whatsapp.net" {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	if r.s.Panes.Selecting || r.s.Panes.Selection.Len() != 0 {
		t.Error("selection mode did not end")
	}
	// Our copy stays, marked, until the core accepts the deletion.
	if !r.s.Panes.PendingDelete.Has([]byte("m1")) || !r.s.Panes.PendingDelete.Has([]byte("m2")) {
		t.Error("the messages are not marked as being deleted")
	}
	one := model.IDList{}
	one.Add([]byte("m3"))
	r.open(state.Dialog{Kind: state.DialogConfirmDeleteMessage, Chat: ada, IDs: one, ForEveryone: true})
	if _, ok := r.Find(ui.RoleDialog, "Delete for everyone?"); !ok {
		t.Error("not titled Delete for everyone?")
	}
	r.click(ui.RoleButton, "Delete")
	rev := r.sent(abi.CommandTagRevoke)
	if len(rev) != 1 || string(rev[0].Revoke.ID) != "m3" {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	if !r.s.Panes.Revoking.Has([]byte("m3")) {
		t.Error("a deletion for everyone does not show at once")
	}
}

// DL-012/013 and the account questions.
func TestAccountQuestions(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogConfirmUnlink})
	r.click(ui.RoleButton, "Unlink")
	if len(r.sent(abi.CommandTagUnlink)) != 1 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	r.open(state.Dialog{Kind: state.DialogConfirmClearMediaCache, Account: 0})
	r.click(ui.RoleButton, "Clear")
	if got := r.sent(abi.CommandTagClearMediaCache); len(got) != 1 || got[0].ClearMediaCache.Token == 0 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	// The only account is unlinked rather than removed.
	r.open(state.Dialog{Kind: state.DialogConfirmRemoveAccount, Account: 0})
	r.click(ui.RoleButton, "Remove")
	if len(r.sent(abi.CommandTagUnlink)) != 2 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	r.open(state.Dialog{Kind: state.DialogConfirmStartOver})
	r.click(ui.RoleButton, "Start over")
	if len(r.sent(abi.CommandTagStartOverArchive)) != 1 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	r.open(state.Dialog{Kind: state.DialogConfirmRemoveStickerPack, Text: []byte("Ducks"), Path: []byte("/packs/ducks")})
	r.click(ui.RoleButton, "Remove")
	if got := r.sent(abi.CommandTagDeleteStickerPack); len(got) != 1 || string(got[0].DeleteStickerPack.Dir) != "/packs/ducks" {
		t.Fatalf("sent %v", r.f.SentTags())
	}
}

// DL-017: Escape closes the open dialog.
func TestEscapeClosesAnyDialog(t *testing.T) {
	r := newRig(t, baseEvents()...)
	for _, k := range []state.DialogKind{state.DialogAbout, state.DialogShortcuts, state.DialogNewChat, state.DialogLabels} {
		r.open(state.Dialog{Kind: k})
		if !r.s.Dialog.Open() {
			t.Fatalf("%v did not open", k)
		}
		r.Play(widgettest.Key(ui.KeyEscape, 0))
		r.Frames(2)
		if r.s.Dialog.Open() {
			t.Errorf("Escape left %v open", k)
		}
	}
}
