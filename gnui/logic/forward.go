package logic

import (
	"bytes"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
)

// ForwardLimit is how many chats a batch may go to until the core has read
// the messages' forwarding scores (zf:src/model.rs:2213).
const ForwardLimit = 5

// CanSend reports whether messages can be sent to the chat: not locked,
// read-only, left or a broadcast list (zf:src/model.rs:360).
func CanSend(c model.Chat) bool {
	return !c.Locked() && !c.ReadOnly() && !c.Left() && c.Kind() != abi.ChatKindBroadcast
}

// OpenForward opens the forwarding dialog for the messages of chat (ids in
// the chat's order) and asks the core for their real limit
// (zf:src/app.rs:7579-7590).
func OpenForward(s *state.State, a *state.Account, chat model.ChatRef, ids *model.IDList) {
	c, ok := a.Model.Chats.Get(chat)
	if !ok || ids.Len() == 0 {
		return
	}
	d := state.Dialog{Kind: state.DialogForward, Chat: chat}
	for i := range ids.Len() {
		d.IDs.Add(ids.At(i))
	}
	s.OpenDialog(d)
	f := &s.Forwarding
	f.Recipients.Reset()
	f.Reviewing = false
	f.Limit = ForwardLimit
	cmd := a.Cmd(abi.CommandTagForwardLimit)
	cmd.ForwardLimit.FromChat = c.ID()
	for i := range s.Dialog.IDs.Len() {
		cmd.ForwardLimit.Messages = append(cmd.ForwardLimit.Messages, s.Dialog.IDs.At(i))
	}
	a.Send(cmd)
}

func forwarding(s *state.State) bool { return s.Dialog.Kind == state.DialogForward }

// ToggleForwardRecipient adds a chat to the recipients or removes it, within
// the limit; a limit of one replaces the choice, as a radio button would
// (zf:src/app.rs:6717).
func ToggleForwardRecipient(s *state.State, a *state.Account, chat []byte) {
	f := &s.Forwarding
	c, ok := a.Model.Chats.Get(a.Model.Chats.Find(chat))
	if !forwarding(s) || f.Reviewing || !ok || !CanSend(c) {
		return
	}
	switch {
	case f.Recipients.Has(chat):
		f.Recipients.Remove(chat)
	case f.Limit == 1:
		f.Recipients.Reset()
		f.Recipients.Add(chat)
	case f.Recipients.Len() < f.Limit:
		f.Recipients.Add(chat)
	}
	s.Touch()
}

// ReviewForward shows two or more recipients for confirmation.
func ReviewForward(s *state.State) {
	if forwarding(s) && s.Forwarding.Recipients.Len() > 1 {
		s.Forwarding.Reviewing = true
		s.Touch()
	}
}

// BackToForwardSelection returns from the confirmation, keeping the list.
func BackToForwardSelection(s *state.State) {
	s.Forwarding.Reviewing = false
	s.Touch()
}

// ConfirmForward forwards: to one recipient at once, to several from their
// confirmation, after checking every recipient can still be written to
// (zf:src/app.rs:6742). It reports whether the batch was sent.
func ConfirmForward(s *state.State, a *state.Account) bool {
	f := &s.Forwarding
	if !forwarding(s) {
		return false
	}
	confirmed := f.Recipients.Len() == 1 || (f.Recipients.Len() > 1 && f.Reviewing)
	from, ok := a.Model.Chats.Get(s.Dialog.Chat)
	if !confirmed || !ok || s.Dialog.IDs.Len() == 0 || f.Recipients.Len() > f.Limit {
		return false
	}
	// A destination may have become locked or read-only while the review
	// was open.
	n := f.Recipients.Len()
	for i := n - 1; i >= 0; i-- {
		if c, ok := a.Model.Chats.Get(a.Model.Chats.Find(f.Recipients.At(i))); !ok || !CanSend(c) {
			f.Recipients.Remove(f.Recipients.At(i))
		}
	}
	if f.Recipients.Len() != n {
		f.Reviewing = false
		s.Toast([]byte(i18n.T("A selected chat is no longer writable")), true)
		return false
	}
	cmd := a.Cmd(abi.CommandTagForward)
	cmd.Forward.FromChat = from.ID()
	for i := range s.Dialog.IDs.Len() {
		cmd.Forward.Messages = append(cmd.Forward.Messages, s.Dialog.IDs.At(i))
	}
	for i := range f.Recipients.Len() {
		cmd.Forward.ToChats = append(cmd.Forward.ToChats, f.Recipients.At(i))
	}
	a.Send(cmd)
	f.Recipients.Reset()
	f.Reviewing = false
	s.CloseDialog()
	CancelSelection(s)
	return true
}

// forwardLimit takes the core's limit for the dialog on screen, not one
// asked earlier or by an account that is not on screen
// (zf:src/app.rs:3422).
func forwardLimit(s *state.State, a *state.Account, e *abi.EventForwardLimit) {
	if !a.Active() || !forwarding(s) || !dialogChatIs(s, a, e.FromChat) {
		return
	}
	ids := &s.Dialog.IDs
	if ids.Len() != len(e.Messages) {
		return
	}
	for i, m := range e.Messages {
		if !bytes.Equal(ids.At(i), m) {
			return
		}
	}
	f := &s.Forwarding
	f.Limit = int(min(e.Limit, 1<<16))
	for f.Recipients.Len() > f.Limit {
		f.Recipients.Remove(f.Recipients.At(f.Recipients.Len() - 1))
	}
	if f.Recipients.Len() < 2 {
		f.Reviewing = false
	}
	s.Touch()
}

func dialogChatIs(s *state.State, a *state.Account, chat []byte) bool {
	c, ok := a.Model.Chats.Get(s.Dialog.Chat)
	return ok && bytes.Equal(c.ID(), chat)
}
