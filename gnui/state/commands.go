package state

import (
	"time"

	"zapfast/abi"
	"zapfast/model"
)

// PhoneCooldown is how long after an answer the phone is not asked again
// for older history, doubled per empty answer up to ten minutes
// (zf:src/app.rs:38).
const PhoneCooldown = 6 * time.Second

// SaveDraft keeps the chat's unsent text in the model and in the archive
// (SaveDraft); empty text forgets it.
func (a *Account) SaveDraft(chat, text []byte) {
	if string(a.Model.Draft(chat)) == string(text) {
		return
	}
	a.Model.SetDraft(chat, text)
	cmd := a.Cmd(abi.CommandTagSaveDraft)
	cmd.SaveDraft.Chat, cmd.SaveDraft.Text = chat, text
	a.Send(cmd)
}

// Download asks for a message's file (Download; DownloadMotion for
// model.PartMotion) and marks it in progress. manual is a click, not the
// auto-download of a bubble in view.
func (a *Account) Download(chat, msg []byte, part int, manual bool) {
	if part == model.PartMotion {
		cmd := a.Cmd(abi.CommandTagDownloadMotion)
		cmd.DownloadMotion.Chat, cmd.DownloadMotion.Message = chat, msg
		if a.Send(cmd) != nil {
			return
		}
	} else {
		cmd := a.Cmd(abi.CommandTagDownload)
		cmd.Download.Chat, cmd.Download.Message, cmd.Download.Manual = chat, msg, manual
		if part >= 0 {
			cmd.Download.Card, cmd.Download.HasCard = uint64(part), true
		}
		if a.Send(cmd) != nil {
			return
		}
	}
	a.Model.MarkDownloading(chat, msg, part)
}

// LoadOlder pages the open chat's archive back from its oldest loaded
// message, or asks the phone once the archive has nothing older
// (zf:src/app.rs:4984). explicit: the reader scrolled to the top. It sets
// Panes.Anchor to the oldest message so the transcript keeps it in place.
func (a *Account) LoadOlder(explicit bool) {
	c, ok := a.OpenChatView()
	if !ok {
		return
	}
	chat := c.ID()
	v := a.Model.Conversation(chat)
	if v == nil || v.LoadingOlder() || v.Len() == 0 {
		return
	}
	if v.Complete() {
		a.FetchOlder(chat, explicit)
		return
	}
	oldest := v.At(0)
	v.SetLoadingOlder(true)
	p := &a.st.Panes
	p.Anchor.Reset()
	p.Anchor.Add(oldest.ID())
	cmd := a.Cmd(abi.CommandTagLoadChat)
	cmd.LoadChat.Chat, cmd.LoadChat.HasBefore = chat, true
	cmd.LoadChat.Before = abi.PageKey{Timestamp: oldest.Time(), ID: oldest.ID()}
	a.Send(cmd)
}

// FetchOlder asks the phone for the chat's older history when it may have
// some and the back-off allows (zf:src/app.rs:5013). explicit: the reader
// asked; automatic requests never report a silent phone.
func (a *Account) FetchOlder(chat []byte, explicit bool) {
	v := a.Model.Conversation(chat)
	if v == nil || v.PhoneExhausted() {
		return
	}
	send := func() {
		cmd := a.Cmd(abi.CommandTagFetchOlder)
		cmd.FetchOlder.Chat, cmd.FetchOlder.Explicit = chat, explicit
		if a.Send(cmd) != nil {
			// A core that cannot ask the phone (the demo's refuses): as if
			// it had nothing, so nothing waits on it or asks again.
			v.PhoneRefused()
		}
	}
	if on, mine := v.FetchingPhone(); on {
		if explicit && !mine {
			// The reader scrolled up while an automatic request waits: the
			// core makes that request theirs instead of asking twice.
			v.SetFetchingPhone(true, true)
			send()
		}
		return
	}
	if !a.Model.Link.Connected() {
		return
	}
	cooldown := min(PhoneCooldown<<v.PhoneMisses(), 10*time.Minute)
	if at := v.PhoneAnswered(); at != 0 && a.st.Now().Sub(time.Unix(0, at)) < cooldown {
		return
	}
	v.SetFetchingPhone(true, explicit)
	if v.Len() > 0 && a.Active() && a.openIs(chat) {
		p := &a.st.Panes
		p.Anchor.Reset()
		p.Anchor.Add(v.At(0).ID())
	}
	send()
}

// SetPrivacy changes one of the account's privacy settings: shown at once,
// written to the phone, restored if it refuses (model.Privacy). It reports
// whether a write was sent.
func (a *Account) SetPrivacy(kind abi.PrivacyKind, choice abi.PrivacyChoice) bool {
	if a.Conn == nil || !a.Model.Privacy.BeginSet(kind, choice) {
		return false
	}
	cmd := a.Cmd(abi.CommandTagSetAccountPrivacy)
	cmd.SetAccountPrivacy.Kind, cmd.SetAccountPrivacy.Choice = kind, choice
	a.Send(cmd)
	return true
}

// LoadStorageStats measures the media cache (Settings → Storage).
func (a *Account) LoadStorageStats() {
	cmd := a.Cmd(abi.CommandTagLoadStorageStats)
	cmd.LoadStorageStats.Token = a.Model.Storage.Ask()
	a.Send(cmd)
}

// ClearMediaCache deletes the downloaded media (the dialog confirmed).
func (a *Account) ClearMediaCache() {
	cmd := a.Cmd(abi.CommandTagClearMediaCache)
	cmd.ClearMediaCache.Token = a.Model.Storage.Clear()
	a.Send(cmd)
}

// SetDownloadFolder sets where new downloads go (nil: the cache) and tells
// every account's core (zf:src/app.rs:8110).
func (s *State) SetDownloadFolder(path []byte) {
	s.Settings.DownloadFolder = append(s.Settings.DownloadFolder[:0], path...)
	s.SettingsChanged()
	for _, a := range s.Accounts {
		a.sendDownloadFolder()
	}
}

// SetKeepChatsArchived sets whether archived chats stay archived when a
// message comes, and tells every account's core (zf:src/app.rs:8119).
func (s *State) SetKeepChatsArchived(keep bool) {
	s.Settings.KeepChatsArchived = keep
	s.SettingsChanged()
	for _, a := range s.Accounts {
		a.sendKeepArchived()
	}
}

// SetProxy sets the proxy every account's core connects through.
func (s *State) SetProxy(proxy []byte) {
	s.Settings.Proxy = append(s.Settings.Proxy[:0], proxy...)
	s.SettingsChanged()
	for _, a := range s.Accounts {
		cmd := a.Cmd(abi.CommandTagSetProxy)
		cmd.SetProxy.Proxy = proxy
		a.Send(cmd)
	}
}

func (a *Account) sendDownloadFolder() {
	if a.st.Demo {
		return
	}
	cmd := a.Cmd(abi.CommandTagSetDownloadFolder)
	f := a.st.Settings.DownloadFolder
	cmd.SetDownloadFolder.Path, cmd.SetDownloadFolder.HasPath = f, len(f) > 0
	a.Send(cmd)
}

// opened sends a core that has just opened the app's settings it keeps
// (zf:src/app.rs:1185-1192).
func (a *Account) opened() {
	a.sendDownloadFolder()
	a.sendKeepArchived()
	a.prefetchSent = false
}

func (a *Account) sendKeepArchived() {
	if a.st.Demo {
		return
	}
	cmd := a.Cmd(abi.CommandTagSetKeepChatsArchived)
	cmd.SetKeepChatsArchived.Keep = a.st.Settings.KeepChatsArchived
	a.Send(cmd)
}

// syncPrefetch tells each core whether to prefetch history in the
// background and which chat is being read, when that changes
// (zf:src/app.rs:5730).
func (s *State) syncPrefetch() {
	for i, a := range s.Accounts {
		if a.Conn == nil {
			continue
		}
		var focused model.ChatRef
		if i == s.Active {
			focused = a.Open
		}
		if a.prefetchSent && a.prefetchOn == a.Settings.HistoryPrefetch && a.prefetchChat == focused {
			continue
		}
		cmd := a.Cmd(abi.CommandTagSetHistoryPrefetch)
		cmd.SetHistoryPrefetch.On = a.Settings.HistoryPrefetch
		if c, ok := a.Model.Chats.Get(focused); ok {
			cmd.SetHistoryPrefetch.Focused, cmd.SetHistoryPrefetch.HasFocused = c.ID(), true
		}
		if a.Send(cmd) == nil {
			a.prefetchSent, a.prefetchOn, a.prefetchChat = true, a.Settings.HistoryPrefetch, focused
		}
	}
}
