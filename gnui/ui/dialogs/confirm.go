package dialogs

import (
	"strconv"
	"strings"

	"gonativeui/platform"
	"gonativeui/rexui"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/logic"
	"zapfast/model"
	"zapfast/state"
)

// prepare writes the title and paragraphs of the dialogs that only ask a
// question (upstream's confirm_destructive and its callers), once, when the
// dialog opens.
func (s *Screen) prepare(e *screenEnv, a *state.Account) {
	d := &e.S.Dialog
	name := i18n.T("this chat")
	if c, ok := a.Model.Chats.Get(d.Chat); ok {
		if n := chatTitle(a, c); len(n) > 0 {
			name = string(n)
		}
	}
	switch d.Kind {
	case state.DialogShortcuts:
		s.title = i18n.T("Keyboard shortcuts")
	case state.DialogAbout:
		s.title = i18n.T("About")
	case state.DialogConfirmUnlink:
		s.title = i18n.T("Unlink this computer?")
		s.paras[0] = i18n.T("This removes the device from WhatsApp and deletes the chats stored here. You can link again with a new code.")
		s.verb = i18n.T("Unlink")
	case state.DialogConfirmRemoveAccount:
		s.title = i18n.T("Remove this account?")
		s.paras[0] = i18n.T("This unlinks the number on this computer and deletes its local chats. Other accounts stay.")
		s.verb = i18n.T("Remove")
	case state.DialogConfirmClearMediaCache:
		s.title = i18n.T("Clear the media cache?")
		s.paras[0] = i18n.Format(i18n.T("This deletes the photos, videos, stickers and files {account} downloaded to this computer. Messages stay, and attachments download again when you open them, while WhatsApp still has them."),
			"{account}", accountLabel(e.S, d.Account))
		s.paras[1] = i18n.T("Files in a download folder you chose are kept.")
		s.verb = i18n.T("Clear")
	case state.DialogConfirmLockChat:
		s.title = i18n.T("Lock this chat?")
		s.paras[0] = i18n.T("This moves the chat to the Locked tab, hiding it from the normal chat list, search, unread badges, and notifications. The lock status also syncs to your phone. Open Locked with a separate local Zap code to read it. Sending from locked chats is not supported yet.")
		s.verb = i18n.T("Lock chat")
	case state.DialogConfirmDeleteChat:
		s.title = i18n.T("Delete chat?")
		s.paras[0] = i18n.Format(i18n.T("This deletes your chat with {name}, including all messages, on this computer and on your phone. It cannot be undone."), "{name}", name)
		s.verb = i18n.T("Delete")
	case state.DialogConfirmClearChat:
		s.title = i18n.T("Clear chat?")
		s.paras[0] = i18n.Format(i18n.T("This clears every message in your chat with {name}, including downloaded media, on this computer and on your phone. The chat itself stays. It cannot be undone."), "{name}", name)
		s.verb = i18n.T("Clear chat")
	case state.DialogConfirmLeaveGroup:
		if isChannel(a, d.Chat) {
			s.title = i18n.T("Leave this channel?")
			s.verb = i18n.T("Leave channel")
		} else {
			s.title = i18n.T("Leave this group?")
			s.verb = i18n.T("Leave group")
		}
		s.paras[0] = i18n.T("You will not receive new messages. The local history stays on this computer.")
	case state.DialogConfirmStartOver:
		s.title = i18n.T("Start over?")
		s.paras[0] = i18n.T("Zap keeps your unreadable archive as a separate file, creates a new one, and asks you to link again. Linking again brings back recent history from your phone. Afterwards, remove the old ZapFast entry under Linked devices on your phone.")
		s.paras[1] = i18n.T("If you can restore the original keyring instead, choose Cancel and Try again: nothing is lost that way.")
		s.verb = i18n.T("Start over")
	case state.DialogConfirmRemoveStickerPack:
		s.title = i18n.T("Remove sticker pack?")
		s.paras[0] = i18n.Format(i18n.T("This deletes \"{name}\" and its stickers from this computer. To use it again, import it again."), "{name}", string(d.Text))
		s.verb = i18n.T("Remove")
	case state.DialogConfirmDeleteMessage, state.DialogConfirmDeleteSelection:
		s.deleteTexts(d)
	}
}

// deleteTexts prepares the delete confirmations (upstream's deletion_body and
// confirm_delete_messages). A selection also says how many messages.
func (s *Screen) deleteTexts(d *state.Dialog) {
	selection := d.Kind == state.DialogConfirmDeleteSelection
	n := d.IDs.Len()
	if d.ForEveryone {
		s.title = i18n.T("Delete for everyone?")
	} else {
		s.title = i18n.T("Delete for me?")
	}
	s.verb = i18n.T("Delete")
	var body string
	switch {
	case !d.ForEveryone && !selection:
		body = i18n.T("This removes the message from your phone and linked devices. Other people keep their copy. Connect to WhatsApp to delete it. This cannot be undone.")
	case d.ForEveryone && !selection:
		body = i18n.T("Everyone in this chat will see \"This message was deleted\" instead. It cannot be undone.")
	case !d.ForEveryone:
		body = i18n.T("This removes these messages from your phone and linked devices. Other people keep their copy. Connect to WhatsApp to delete them. This cannot be undone.")
	default:
		body = i18n.N("Everyone in this chat will see \"This message was deleted\" in place of the selected message. This cannot be undone.",
			"Everyone in this chat will see \"This message was deleted\" in place of the selected messages. This cannot be undone.", n)
	}
	if selection && n > 0 {
		s.paras[0] = i18n.Format(i18n.N("{count} selected message", "{count} selected messages", n), "{count}", strconv.Itoa(n))
		s.paras[1] = body
		return
	}
	s.paras[0], s.paras[1] = body, ""
}

// isQuestion reports whether the dialog only asks a yes or no.
func isQuestion(k state.DialogKind) bool {
	switch k {
	case state.DialogConfirmUnlink, state.DialogConfirmRemoveAccount, state.DialogConfirmClearMediaCache,
		state.DialogConfirmLockChat, state.DialogConfirmDeleteChat, state.DialogConfirmClearChat,
		state.DialogConfirmLeaveGroup, state.DialogConfirmStartOver, state.DialogConfirmRemoveStickerPack,
		state.DialogConfirmDeleteMessage, state.DialogConfirmDeleteSelection:
		return true
	}
	return false
}

// alerts is how a question reaches the system's alert (a platform.AlertSpec
// for App.Alert, answered with App.TakeDialogFor, which gives this dialog its
// own ticket's result, never a file panel's). Where the system has none, or
// is busy with another sheet, the question is drawn (confirm) instead. A test
// gives the window widgettest's FakeDialogs; none shows a real alert.
type alerts struct {
	ticket  platform.DialogTicket // the open alert, 0 when none
	yes     int                   // the index of the button that confirms
	archive int                   // the index of "Leave … and archive", -1 if none
}

// ask shows the question as a native alert and reports whether it did.
func (s *Screen) ask(e *screenEnv, a *state.Account) bool {
	if e.A == nil {
		return false
	}
	d := &e.S.Dialog
	spec := platform.AlertSpec{Message: s.title, Style: platform.AlertWarning}
	var info []string
	for _, p := range s.paras {
		if p != "" {
			info = append(info, p)
		}
	}
	spec.Info = strings.Join(info, "\n\n")
	cancel := i18n.T("Cancel")
	s.alert.archive = -1
	if d.Kind == state.DialogConfirmLockChat {
		// Not destructive: the action is the default button (Return).
		spec.Style = platform.AlertInformational
		spec.Buttons = []string{s.verb, cancel}
		s.alert.yes = 0
	} else {
		// Cancel is the default, so Return never destroys anything.
		spec.Buttons = []string{cancel, s.verb}
		spec.Destructive = []int{1}
		s.alert.yes = 1
		if d.Kind == state.DialogConfirmLeaveGroup {
			if c, ok := a.Model.Chats.Get(d.Chat); ok && !c.Archived() {
				label := i18n.T("Leave group and archive")
				if isChannel(a, d.Chat) {
					label = i18n.T("Leave channel and archive")
				}
				spec.Buttons = append(spec.Buttons, label)
				spec.Destructive = append(spec.Destructive, 2)
				s.alert.archive = 2
			}
		}
	}
	t, err := e.A.Alert(spec)
	if err != nil {
		return false
	}
	s.alert.ticket = t
	return true
}

// withdraw closes the alert of a question that is no longer asked.
func (s *Screen) withdraw(e *screenEnv) {
	if s.alert.ticket != 0 && e.A != nil {
		e.A.CancelDialog(s.alert.ticket)
	}
	s.alert.ticket = 0
}

// withdrawAlert is withdraw from outside a frame (lock or session end): the alert
// goes, and the dialog counts as new, so the question is asked again when
// it is next built.
func (s *Screen) withdrawAlert() {
	if s.alert.ticket != 0 && s.app != nil {
		s.app.CancelDialog(s.alert.ticket)
	}
	s.alert.ticket = 0
	s.cur = 0
}

// answered takes the alert's answer, once there is one, and carries it out.
// It reports whether the question is still being asked natively; false after
// the answer, or when the alert could not be shown and confirm draws it.
func (s *Screen) answered(e *screenEnv, a *state.Account) bool {
	r, ok := e.A.TakeDialogFor(s.alert.ticket)
	if !ok {
		return true
	}
	s.alert.ticket = 0
	if r.Err != nil {
		return false // never shown: draw it
	}
	if r.Outcome == platform.DialogAnswered {
		switch r.Button {
		case s.alert.yes:
			s.confirmed(e, a)
		case s.alert.archive:
			s.leave(e, a, true)
		}
	}
	// The answer may have opened the next dialog (locking a chat asks for the
	// code); otherwise this one is done.
	if e.S.Dialog.Kind == s.kind && identity(&e.S.Dialog) == s.cur {
		e.S.CloseDialog()
	}
	return true
}

// confirm is the body of every question dialog: the paragraphs, then Cancel
// and a red button (never the default: Enter must not destroy anything).
func (s *Screen) confirm(e *screenEnv, a *state.Account, dl *rexui.Dialog) {
	k, d := e.K, &e.S.Dialog
	para(k, s.paras[0], rexui.TextBody, rexui.Tint{})
	para(k, s.paras[1], rexui.TextCallout, secondary)
	para(k, s.paras[2], rexui.TextCallout, secondary)

	if d.Kind == state.DialogConfirmLeaveGroup {
		s.leaveArchive(e, a)
	}
	beginActions(e)
	variant := rexui.ButtonDestructive
	if d.Kind == state.DialogConfirmLockChat {
		variant = rexui.ButtonPrimary
	}
	dl.Action(i18n.T("Cancel"), rexui.ButtonDefault)
	if dl.Action(s.verb, variant) {
		s.confirmed(e, a)
	}
	endActions(e)
}

// leaveArchive offers "Leave … and archive" above Cancel, for a chat that is
// not archived yet.
func (s *Screen) leaveArchive(e *screenEnv, a *state.Account) {
	d := &e.S.Dialog
	c, ok := a.Model.Chats.Get(d.Chat)
	if !ok || c.Archived() {
		return
	}
	label := i18n.T("Leave group and archive")
	if isChannel(a, d.Chat) {
		label = i18n.T("Leave channel and archive")
	}
	if rexui.Button(e.K.Kit, label, rexui.ButtonOpts{}).Clicked {
		s.leave(e, a, true)
		s.open = false
	}
}

func (s *Screen) leave(e *screenEnv, a *state.Account, archive bool) {
	if c, ok := a.Model.Chats.Get(e.S.Dialog.Chat); ok {
		cmd := a.Cmd(abi.CommandTagLeaveGroup)
		cmd.LeaveGroup.Chat, cmd.LeaveGroup.Archive = c.ID(), archive
		a.Send(cmd)
	}
}

// confirmed carries out the question the person said yes to.
func (s *Screen) confirmed(e *screenEnv, a *state.Account) {
	st, d := e.S, &e.S.Dialog
	switch d.Kind {
	case state.DialogConfirmUnlink:
		a.Send(a.Cmd(abi.CommandTagUnlink))
	case state.DialogConfirmRemoveAccount:
		st.RemoveAccount(d.Account) // the core logs out first; the last account is only unlinked
	case state.DialogConfirmClearMediaCache:
		if d.Account >= 0 && d.Account < len(st.Accounts) {
			st.Accounts[d.Account].ClearMediaCache()
		}
	case state.DialogConfirmLockChat:
		if c, ok := a.Model.Chats.Get(d.Chat); ok {
			cmd := a.Cmd(abi.CommandTagSetLocked)
			cmd.SetLocked.Chat, cmd.SetLocked.Locked = c.ID(), true
			a.Send(cmd)
			// Locking the open chat closes it, as the phone does.
			if a.Open == d.Chat {
				a.OpenChat(0)
			}
		}
		if len(st.Settings.ChatLockHash) == 0 {
			s.next(e, state.Dialog{Kind: state.DialogUnlockLockedChats})
		}
	case state.DialogConfirmDeleteChat:
		if c, ok := a.Model.Chats.Get(d.Chat); ok {
			cmd := a.Cmd(abi.CommandTagDeleteChat)
			cmd.DeleteChat.Chat = c.ID()
			a.Send(cmd)
		}
	case state.DialogConfirmClearChat:
		if c, ok := a.Model.Chats.Get(d.Chat); ok {
			cmd := a.Cmd(abi.CommandTagClearChat)
			cmd.ClearChat.Chat = c.ID()
			a.Send(cmd)
		}
	case state.DialogConfirmLeaveGroup:
		s.leave(e, a, false)
	case state.DialogConfirmStartOver:
		a.Send(a.Cmd(abi.CommandTagStartOverArchive))
	case state.DialogConfirmRemoveStickerPack:
		cmd := a.Cmd(abi.CommandTagDeleteStickerPack)
		cmd.DeleteStickerPack.Dir = d.Path
		a.Send(cmd)
	case state.DialogConfirmDeleteMessage, state.DialogConfirmDeleteSelection:
		logic.ConfirmDelete(st, a) // marks them deleting, sends each, ends a selection, closes
	}
}

func isChannel(a *state.Account, ref model.ChatRef) bool {
	c, ok := a.Model.Chats.Get(ref)
	if !ok {
		return false
	}
	id := c.ID()
	const suffix = "@newsletter"
	return len(id) >= len(suffix) && string(id[len(id)-len(suffix):]) == suffix
}

// accountLabel is the name the account switcher shows: the profile name, the
// number, else "Account" and its folder.
func accountLabel(st *state.State, i int) string {
	if i < 0 || i >= len(st.Accounts) {
		return ""
	}
	a := st.Accounts[i]
	if n := a.Model.Me.Name(); len(n) > 0 {
		return string(n)
	}
	if p := model.Phone(a.Model.Me.ID()); len(p) > 0 {
		return format.Phone(model.S(p))
	}
	return i18n.Format(i18n.T("Account {id}"), "{id}", a.ID)
}
