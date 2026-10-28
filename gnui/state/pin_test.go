package state_test

import (
	"fmt"
	"testing"

	"zapfast/abi"
	"zapfast/model"
	"zapfast/state"
)

func churnConversations(a *state.Account, prefix string) {
	for i := 0; i < model.MaxConversations; i++ {
		a.Event(&abi.Event{Tag: abi.EventTagMessages, Messages: abi.EventMessages{
			Chat: []byte(fmt.Sprintf("%s-%d@demo.invalid", prefix, i)), Last: true}})
	}
}

func TestConversationPinLifecycle(t *testing.T) {
	r := newRig(t, 2)
	a, b := r.s.Accounts[0], r.s.Accounts[1]
	ref := a.Model.Chats.Find([]byte(ada))
	a.OpenChat(ref)
	a.Event(&abi.Event{Tag: abi.EventTagMessages, Messages: abi.EventMessages{Chat: []byte(ada), Last: true, Messages: []abi.Message{text("kept", 1)}}})
	v := a.Model.Conversation([]byte(ada))
	keep := func(label string) {
		t.Helper()
		churnConversations(a, label)
		if a.Model.Conversation([]byte(ada)) != v || v.Find([]byte("kept")) != 0 {
			t.Fatalf("%s: open conversation evicted", label)
		}
	}
	keep("displayed")
	r.s.Focused = false // main-window close parks the account's open chat
	keep("window-closed")
	r.s.Locked()
	keep("locked")
	r.s.SwitchAccount(1)
	b.OpenChat(b.Model.Chats.Find([]byte(ada)))
	keep("account-parked")
	r.s.SwitchAccount(0)
	keep("account-restored")
	// The viewer can still show Ada after the main transcript changes chat.
	r.s.OpenViewer(ref, []byte("kept"), model.PartMain)
	r.s.Win.Open[state.WinViewer] = true
	a.Event(&abi.Event{Tag: abi.EventTagChatUpdated, ChatUpdated: abi.EventChatUpdated{Chat: abi.Chat{ID: []byte(grace)}}})
	a.OpenChat(a.Model.Chats.Find([]byte(grace)))
	keep("viewer")
	if a.Model.Conversation([]byte(grace)) == nil {
		t.Fatal("viewer displaced the main transcript")
	}
	r.s.Locked() // closes the viewer, but not the parked main transcript
	keep("viewer-closing")
	// The shell clears these flags on the native window's close event.
	r.s.Win.Open[state.WinViewer], r.s.Win.Shut[state.WinViewer] = false, false
	churnConversations(a, "viewer-closed")
	if a.Model.Conversation([]byte(ada)) != nil {
		t.Fatal("closed viewer kept its pin")
	}
	// A viewer request cancelled by an account switch must not pin its chat.
	a.Model.Open([]byte(ada))
	r.s.OpenViewer(ref, []byte("kept"), model.PartMain)
	r.s.SwitchAccount(1)
	churnConversations(a, "viewer-account-switch")
	if a.Model.Conversation([]byte(ada)) != nil || a.Model.Conversation([]byte(grace)) == nil {
		t.Fatal("account switch retained the viewer or lost the parked chat")
	}
	r.s.SwitchAccount(0)
	a.OpenChat(0)
	churnConversations(a, "chat-closed")
	if a.Model.Conversation([]byte(grace)) != nil {
		t.Fatal("closed chat kept its pin")
	}
	// Removal releases this store's pins, without touching the other account.
	a.OpenChat(ref)
	r.cores[0].Emit(&abi.Event{Tag: abi.EventTagAccountRemoved})
	r.pump()
	for i := 0; i < model.MaxConversations; i++ {
		a.Model.Open([]byte(fmt.Sprint("removed-", i)))
	}
	if a.Model.Conversation([]byte(ada)) != nil || r.s.Acct() != b {
		t.Fatal("removed account retained a pin or displaced its successor")
	}
	churnConversations(b, "remaining")
	if b.Model.Conversation([]byte(ada)) == nil {
		t.Fatal("remaining account lost its pin")
	}
}
