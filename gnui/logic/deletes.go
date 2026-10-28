package logic

import (
	"zapfast/abi"
	"zapfast/state"
)

// Deleting messages (zf:src/app.rs:6955-6971, zf:src/ui/dialogs.rs:1295).
// Upstream has no undo: a deletion for us waits for the core's
// MessageDeleted (State.Panes.PendingDelete until then), a deletion for
// everyone shows at once (Panes.Revoking until the core's update).

// DeleteForEveryone revokes a message of the open chat for everyone.
func DeleteForEveryone(s *state.State, a *state.Account, id []byte) {
	c, ok := a.OpenChatView()
	if !ok {
		return
	}
	s.Panes.Revoking.Add(id)
	PruneSelection(s, a)
	cmd := a.Cmd(abi.CommandTagRevoke)
	cmd.Revoke.Chat, cmd.Revoke.ID = c.ID(), id
	a.Send(cmd)
	s.Touch()
}

// DeleteForMe deletes our copy of a message of the open chat; it stays
// until WhatsApp accepts the deletion (MessageDeleted removes it and
// prunes the selection).
func DeleteForMe(s *state.State, a *state.Account, id []byte) {
	c, ok := a.OpenChatView()
	if !ok {
		return
	}
	s.Panes.PendingDelete.Add(id)
	cmd := a.Cmd(abi.CommandTagDeleteLocal)
	cmd.DeleteLocal.Chat, cmd.DeleteLocal.ID = c.ID(), id
	a.Send(cmd)
	s.Touch()
}

// ConfirmDelete deletes the messages of the open delete dialog
// (DialogConfirmDeleteMessage or DialogConfirmDeleteSelection: its IDs and
// ForEveryone), leaves selection mode for a selection, and closes the
// dialog.
func ConfirmDelete(s *state.State, a *state.Account) {
	d := &s.Dialog
	if d.Kind != state.DialogConfirmDeleteMessage && d.Kind != state.DialogConfirmDeleteSelection {
		return
	}
	if a.Open != d.Chat {
		s.CloseDialog()
		return
	}
	selection := d.Kind == state.DialogConfirmDeleteSelection
	for i := range d.IDs.Len() {
		if d.ForEveryone {
			DeleteForEveryone(s, a, d.IDs.At(i))
		} else {
			DeleteForMe(s, a, d.IDs.At(i))
		}
	}
	if selection {
		CancelSelection(s)
	}
	s.CloseDialog()
}
