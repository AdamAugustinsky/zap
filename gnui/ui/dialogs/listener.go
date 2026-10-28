package dialogs

import (
	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
)

// listener is the Screen as a state.Listener: the answers the core gives to
// what a dialog asked. An account that is not on screen updates only its own
// data (the labels); it never touches a dialog (upstream's events_hidden).
type listener Screen

func (l *listener) Event(a *state.Account, e *abi.Event) {
	s := (*Screen)(l)
	st := s.st
	if e.Tag == abi.EventTagLabels {
		s.lab.event(a, &e.Labels)
		return
	}
	if e.Tag == abi.EventTagStickerPackPreview && e.StickerPackPreview.Result.Tag == abi.StickerPackResultTagErr {
		if a.Active() && st.Dialog.Kind == state.DialogStickerPack {
			st.CloseDialog()
		}
		a.Toast(e.StickerPackPreview.Result.Err.Error, true)
		return
	}
	if !a.Active() {
		return
	}
	d := &st.Dialog
	switch e.Tag {
	case abi.EventTagChats, abi.EventTagChatUpdated:
		// The model has applied the lock, but hideLocked has not yet closed
		// the dialog. Cancel the native sheet even without another Build.
		if d.Chat != 0 && a.LockedAway(d.Chat) {
			s.withdrawAlert()
		}
	case abi.EventTagLink:
		if status := a.Model.Link.Status(); status == abi.LinkStatusTagLoggedOut || status == abi.LinkStatusTagUnlinked {
			s.withdrawAlert()
			st.CloseDialog()
		}
	case abi.EventTagAccountRemoved:
		s.withdrawAlert()
		st.CloseDialog()
	case abi.EventTagPollCreated:
		p := &e.PollCreated
		// An error's toast is the state's.
		if !p.HasError && d.Kind == state.DialogCreatePoll && s.poll.sameChat(p.Chat) {
			st.CloseDialog()
			s.poll.reset(a, d)
		}
	case abi.EventTagInvitePreview:
		v := &s.inv
		if d.Kind != state.DialogJoinGroup || string(e.InvitePreview.Code) != v.code {
			return
		}
		r := &e.InvitePreview.Result
		if r.Tag == abi.InviteResultTagOk {
			info := &r.Ok.Info
			v.phase, v.id, v.subject = inviteReady, string(info.ID), string(info.Subject)
			v.desc, v.hasDesc, v.members, v.approval = string(info.Description), info.HasDescription, info.Members, info.Approval
			s.title = v.subject
		} else {
			v.phase, v.err = inviteFailed, string(r.Err.Error)
		}
	case abi.EventTagInviteJoined:
		v := &s.inv
		if d.Kind != state.DialogJoinGroup || string(e.InviteJoined.Code) != v.code {
			return
		}
		r := &e.InviteJoined.Result
		if r.Tag != abi.JoinResultTagOk {
			v.phase, v.err = inviteFailed, string(r.Err.Error)
			return
		}
		st.CloseDialog()
		if r.Ok.Pending {
			st.Toast([]byte(i18n.T("Request sent. An admin must approve it before you join.")), false)
		} else {
			startChat(a, r.Ok.Chat, v.subject)
		}
	case abi.EventTagContactFailed:
		n := &s.contact
		if n.request == e.ContactFailed.Request && n.request != 0 {
			n.pending, n.request = false, 0
			st.Toast([]byte(contactFailure(string(e.ContactFailed.Error))), true)
		}
	case abi.EventTagContactReady:
		n := &s.contact
		r := &e.ContactReady
		if n.request != r.Request || n.request == 0 {
			return
		}
		n.pending, n.request = false, 0
		if d.Kind == state.DialogNewContact {
			st.CloseDialog()
		}
		name := string(r.Name)
		if !r.HasName || name == "" {
			name = format.Phone(model.S(model.Phone(r.ID)))
		}
		startChat(a, r.ID, name)
	case abi.EventTagStickerPackPreview:
		p := &s.pack
		r := &e.StickerPackPreview.Result
		if d.Kind != state.DialogStickerPack {
			return
		}
		p.ready, p.name, p.dir, p.publisher = true, string(r.Ok.Pack.Name), string(r.Ok.Pack.Dir), string(r.Ok.Publisher)
		p.stickers = p.stickers[:0]
		for _, x := range r.Ok.Pack.Stickers {
			p.stickers = append(p.stickers, string(x))
		}
		s.title = p.name
	}
}

func (p *pollDraft) sameChat(chat []byte) bool { return string(p.chat) == string(chat) }
