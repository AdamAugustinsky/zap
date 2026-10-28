package state

import (
	"bytes"
	"strconv"

	"zapfast/abi"
	"zapfast/i18n"
)

// policy is what the app does with an event besides modelling it, after
// the screens' listeners (ls is how many there are): upstream's
// apply_backend_event (zf:src/app.rs:3068-4040), minus what belongs to a
// screen. live is whether the account is on screen; an account that is not
// leaves the window's panes alone, but its toasts can show and name it in
// speech (upstream's events_hidden).
func (s *State) policy(a *Account, e *abi.Event, ls int, wasSyncing bool) {
	live := a.Active()
	switch e.Tag {
	case abi.EventTagSendRefused:
		s.sendRefused(a, &e.SendRefused, live)
	case abi.EventTagOpenPath:
		if ls == 0 {
			s.OpenFile(string(e.OpenPath.Path))
		}
	case abi.EventTagPickRequest:
		// Every dialog must be answered: without a listener (package sys
		// shows the panels), it is answered with no paths.
		if ls == 0 {
			cmd := a.Cmd(abi.CommandTagPickAnswer)
			cmd.PickAnswer.ID = e.PickRequest.ID
			a.Send(cmd)
		}
	case abi.EventTagChatRemoved:
		s.chatRemoved(a, e.ChatRemoved.Chat, live)
		a.reloadStarred()
	case abi.EventTagChatCleared:
		a.reloadStarred()
	case abi.EventTagChats:
		if _, ok := a.Model.Chats.Get(a.Open); a.Open != 0 && !ok {
			s.chatGoing(a) // gone: nothing of it is stored
			a.setOpen(0)
		}
		a.hideLocked()
		// The chat open at quit opens again once the list is in
		// (zf:src/account.rs:132).
		if e.Chats.Last && !a.restored {
			a.restored = true
			if ref := a.Model.Chats.Find(a.Settings.LastChat); ref != 0 && a.Open == 0 {
				if c, _ := a.Model.Chats.Get(ref); !c.Locked() {
					if live {
						a.openChat(ref, s.Focused) // read if it is being looked at
					} else {
						a.setOpen(ref) // it loads when the account comes on screen
					}
				}
			}
		}
		a.started()
		s.added(a)
	case abi.EventTagChatUpdated:
		// A chat locked from the phone outside the open folder closes, with
		// what showed it (zf:src/app.rs:4215, hide_locked_chat).
		a.hideLocked()
		a.started()
	case abi.EventTagLink:
		if a.Model.Link.Status() == abi.LinkStatusTagLoggedOut {
			s.loggedOut(a, live)
		}
		s.linkNews(a, live)
		s.added(a)
	case abi.EventTagError, abi.EventTagInfo:
		if ls == 0 {
			if e.Tag == abi.EventTagError {
				// The core sends the English catalog key (zf:src/app.rs:4018).
				s.toast(a, []byte(i18n.T(string(e.Error.Text))), true)
			} else {
				s.toast(a, e.Info.Text, false)
			}
		}
	case abi.EventTagLabels:
		// A deleted label stops filtering the list (zf:src/app.rs:2365).
		if e.Labels.Last && a.Label.Len() > 0 && a.Model.FindLabel(a.Label.At(0)) < 0 {
			a.Label.Reset()
			if a.Filter == FilterLabel {
				a.Filter = FilterAll
			}
			a.Settings.ListFilter = nil
			a.SettingsChanged()
		}
	case abi.EventTagStarChanged:
		// Only a click here that WhatsApp confirmed says so: stars synced
		// from the phone arrive in bursts (zf:src/app.rs:3158).
		if x := &e.StarChanged; x.Local && live {
			if x.Starred {
				s.Toast([]byte(i18n.T("Starred")), false)
			} else {
				s.Toast([]byte(i18n.T("Star removed")), false)
			}
		}
		a.reloadStarred()
	case abi.EventTagPinChanged:
		if live {
			if e.PinChanged.Pinned {
				s.Toast([]byte(i18n.T("Pinned for 7 days")), false)
			} else {
				s.Toast([]byte(i18n.T("Pin removed")), false)
			}
		}
	case abi.EventTagMessageUpdated:
		m := &e.MessageUpdated.Message
		if m.Content.Tag == abi.ContentTagRevoked || a.Model.Starred(m.Chat, m.ID) {
			a.reloadStarred()
		}
	case abi.EventTagMessageDeleted:
		d := &e.MessageDeleted
		if live && s.Panes.Edit.Len() > 0 && bytes.Equal(s.Panes.Edit.At(0), d.ID) && a.openIs(d.Chat) {
			s.Panes.Edit.Reset()
		}
		if live && a.openIs(d.Chat) {
			s.Panes.Selection.Remove(d.ID)
			s.Panes.PendingDelete.Remove(d.ID)
		}
		a.reloadStarred()
	case abi.EventTagMessages:
		s.messagesPolicy(a, &e.Messages, live)
	case abi.EventTagSyncing:
		if wasSyncing && !e.Syncing.Syncing {
			s.toast(a, []byte(i18n.T("History loaded")), false)
		}
		if !wasSyncing && e.Syncing.Syncing && live {
			s.Announce(AnnounceStatus, i18n.T("Loading chat history"))
		}
	case abi.EventTagStorageStats:
		if e.StorageStats.Cleared && live {
			s.Toast([]byte(i18n.T("Media cache cleared")), false)
		}
	case abi.EventTagPollCreated:
		x := &e.PollCreated
		if x.HasError {
			s.toast(a, x.Error, true)
		} else if live && s.Dialog.Kind == DialogCreatePoll && a.openIs(x.Chat) {
			s.CloseDialog()
		}
	case abi.EventTagPollVoted:
		if x := &e.PollVoted; x.HasError {
			s.toast(a, x.Error, true)
		}
	case abi.EventTagDownloadFolderPicked:
		if live {
			s.SetDownloadFolder(e.DownloadFolderPicked.Path)
		}
	case abi.EventTagNotificationSoundPicked:
		if x := &e.NotificationSoundPicked; live {
			c := SoundChoice{Sound: SoundCustom, Path: append([]byte(nil), x.Path...)}
			if x.Mention {
				s.Settings.MentionSound = c
			} else {
				s.Settings.MessageSound = c
			}
			s.SettingsChanged()
		}
	case abi.EventTagChatSoundPicked:
		x := &e.ChatSoundPicked
		cmd := a.Cmd(abi.CommandTagSetChatSound)
		cmd.SetChatSound.Chat, cmd.SetChatSound.HasSound = x.Chat, true
		cmd.SetChatSound.Sound = abi.NotificationSound{Tag: abi.NotificationSoundTagCustom}
		cmd.SetChatSound.Sound.Custom.Path = x.Path
		a.Send(cmd)
	case abi.EventTagInviteJoined:
		if x := &e.InviteJoined; x.Result.Tag == abi.JoinResultTagOk && x.Result.Ok.Pending && ls == 0 {
			s.toast(a, []byte("Request sent. An admin must approve it before you join."), false)
		}
	case abi.EventTagAccountRemoved:
		a.Removed = true
	}
}

// messagesPolicy: a call filed while the Calls page is open joins its list;
// a chat that sync created without messages asks the phone; a pending
// scroll anchor pages toward itself once (zf:src/app.rs:3257-3319).
func (s *State) messagesPolicy(a *Account, m *abi.EventMessages, live bool) {
	if !live {
		return
	}
	if s.Page == PageCalls {
		for i := range m.Messages {
			if m.Messages[i].Content.Tag == abi.ContentTagCall {
				a.Send(a.Cmd(abi.CommandTagLoadCallLog))
				break
			}
		}
	}
	if !m.Last || !a.openIs(m.Chat) {
		return
	}
	v := a.Model.Conversation(m.Chat)
	if v == nil {
		return
	}
	if !m.Older && m.Complete && v.Len() == 0 {
		a.FetchOlder(m.Chat, false)
	}
	if !m.Older && s.Panes.Anchor.Len() > 0 {
		anchor := s.Panes.Anchor.At(0)
		if v.Find(anchor) < 0 && !v.LoadingOlder() && v.Len() > 0 {
			oldest := v.At(0)
			v.SetLoadingOlder(true)
			cmd := a.Cmd(abi.CommandTagLoadUntil)
			cmd.LoadUntil.Chat, cmd.LoadUntil.ID = m.Chat, anchor
			cmd.LoadUntil.Before = abi.PageKey{Timestamp: oldest.Time(), ID: oldest.ID()}
			a.Send(cmd)
		}
	}
}

// added ends adding an account once it linked and its chats are in: the
// login screen shows "Linked. Waiting for your chats…" until then, and the
// chats after (a new number's Connecting or Connected routes to the chats
// only then).
// linkNews says what a link change means to the reader (zf:src/app.rs
// handle_link): a drop after being connected is announced, then the
// reconnecting once, and getting back toasts "Back online" (which
// announces itself). The start (Starting, Connecting, Connected) is
// silent. Off-screen connection statuses are silent, but a direct return
// from Disconnected still toasts (zf:src/app.rs:4111-4115).
func (s *State) linkNews(a *Account, live bool) {
	was, now := a.linkWas, a.Model.Link.Status()
	a.linkWas = now
	switch now {
	case abi.LinkStatusTagDisconnected:
		if live && was == abi.LinkStatusTagConnected {
			a.offlineSaid = true
			s.Announce(AnnounceStatus, i18n.T("Connection lost"))
		}
	case abi.LinkStatusTagConnecting, abi.LinkStatusTagStarting:
		if live && a.offlineSaid && !a.retrySaid {
			a.retrySaid = true
			s.Announce(AnnounceStatus, i18n.T("Connecting"))
		}
	case abi.LinkStatusTagConnected:
		if (live && a.offlineSaid) || was == abi.LinkStatusTagDisconnected {
			s.toast(a, []byte(i18n.T("Back online")), false)
		}
		if live {
			a.offlineSaid, a.retrySaid = false, false
		}
	}
}

func (s *State) added(a *Account) {
	if s.AddingAccount && a.Active() && a.Model.Link.Connected() && (a.Model.Chats.Loaded() || a.Model.Chats.Len() > 0) {
		s.AddingAccount = false
		s.beforeAdding = ""
		s.gen++
	}
}

// IsOpen reports whether chat is the account's open chat.
func (a *Account) IsOpen(chat []byte) bool { return a.openIs(chat) }

func (a *Account) openIs(chat []byte) bool {
	c, ok := a.Model.Chats.Get(a.Open)
	return ok && bytes.Equal(c.ID(), chat)
}

// reloadStarred asks for the starred list again while it shows, so it does
// not keep a row that changed (upstream's reload_lists).
func (a *Account) reloadStarred() {
	if a.Mode == ListStarred && a.Active() {
		a.Send(a.Cmd(abi.CommandTagLoadStarred))
	}
}

// sendRefused takes back a send the core refused (zf:src/app.rs:4630-4766
// send_refused): nothing typed is lost. A location goes back to its dialog
// (DialogSendLocation, the spot in Dialog.Text) while its chat is on screen
// and no dialog is up (LO-005). Text, and a location that cannot have its
// dialog back (a late refusal never replaces a newer dialog) as its
// coordinates, go back to the open chat's composer (Requests.Restore) or
// into the chat's empty draft; an error toast says why. Refused files,
// pictures and voice are ui/composer's (it listens to SendRefused).
func (s *State) sendRefused(a *Account, r *abi.EventSendRefused, live bool) {
	// A private reply's recovery needs its chat to still be there.
	if r.HasQuoting && len(r.Quoting.Chat) > 0 && a.Model.Chats.Find(r.Chat) == 0 {
		return
	}
	var text []byte
	switch r.Unsent.Tag {
	case abi.UnsentTagText:
		text = r.Unsent.Text.Text
	case abi.UnsentTagLocation:
		text = appendSpot(nil, r.Unsent.Location.Latitude, r.Unsent.Location.Longitude)
	}
	open := false
	if c, ok := a.Model.Chats.Get(a.Open); live && ok && bytes.Equal(c.ID(), r.Chat) {
		open = true
	}
	if r.Unsent.Tag == abi.UnsentTagLocation && open && !s.Dialog.Open() {
		s.OpenDialog(Dialog{Kind: DialogSendLocation, Chat: a.Open, Text: text})
		text = nil
	}
	if len(bytes.TrimSpace(text)) > 0 {
		switch {
		case open:
			s.Req.Restore.Chat = append(s.Req.Restore.Chat[:0], r.Chat...)
			s.Req.Restore.Text = append(s.Req.Restore.Text[:0], text...)
			s.Req.Restore.Acct = a.Serial
		case len(bytes.TrimSpace(a.Model.Draft(r.Chat))) == 0:
			a.Model.SetDraft(r.Chat, text)
			cmd := a.Cmd(abi.CommandTagSaveDraft)
			cmd.SaveDraft.Chat, cmd.SaveDraft.Text = r.Chat, text
			a.Send(cmd)
		}
	}
	msg := i18n.T("Not sent: Zap is not connected to WhatsApp.")
	if r.Reason == abi.RefusalQuoteUnavailable {
		msg = i18n.T("Not sent: the message you’re replying to isn’t available on this computer. Cancel the reply to send without a quote.")
	}
	s.toast(a, []byte(msg), true)
}

// appendSpot appends coordinates as upstream writes them (geo::Spot::text:
// five places, dot decimals).
func appendSpot(b []byte, lat, lon float64) []byte {
	b = strconv.AppendFloat(b, lat, 'f', 5, 64)
	b = append(b, ", "...)
	return strconv.AppendFloat(b, lon, 'f', 5, 64)
}
