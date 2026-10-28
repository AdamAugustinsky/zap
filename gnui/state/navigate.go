package state

import (
	"bytes"
	"time"

	"zapfast/abi"
	"zapfast/model"
)

// Rect is a place in a window, in pixels (window coordinates); the zero
// Rect is none. screen.Rect is this type.
type Rect struct{ X, Y, W, H float32 }

// Empty reports whether r is no place (nothing set it).
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Jump is a message ui/conversation brings into view and flashes once it is
// loaded (upstream's jump_highlight): OpenMessage sets it, the conversation
// clears Msg once the message showed, and opening another chat drops it.
type Jump struct {
	Chat model.ChatRef
	Msg  model.IDList // one message id of Chat; empty: none
	// Seq moves with every jump, so that asking for the message already
	// flashing flashes it again.
	Seq uint32
}

// JumpFlash is how long a jumped-to message flashes once it is in view.
const JumpFlash = 2 * time.Second

// FlashStrength is the jump flash's strength (0…1) elapsed after the message
// came into view: a quick rise, a hold, then a fade to nothing
// (zf:src/app.rs JumpHighlight::strength).
func FlashStrength(elapsed time.Duration) float32 {
	const rise, hold = 150 * time.Millisecond, 900 * time.Millisecond
	var v float64
	switch {
	case elapsed < 0:
		v = 0
	case elapsed < rise:
		v = float64(elapsed) / float64(rise)
	case elapsed < hold:
		v = 1
	default:
		v = 1 - float64(elapsed-hold)/float64(JumpFlash-hold)
	}
	return float32(min(max(v, 0), 1))
}

// OpenMessage opens the chat with JID chat and asks the conversation to show
// message id there and flash it (Action::OpenMessage): a search hit, a starred
// message, an info media item, the viewer's "Show in the chat", a clicked
// notification. A message not loaded yet is paged toward (LoadUntil). It
// reports whether the chat opened: not for a chat the account does not have,
// nor for a locked chat outside the locked folder. It puts the account on
// screen first if it was not.
func (a *Account) OpenMessage(chat, id []byte) bool {
	st := a.st
	ref := a.Model.Chats.Find(chat)
	if ref == 0 || len(id) == 0 {
		return false
	}
	// A notification, or a stale request, never opens a locked chat outside
	// the unlocked folder.
	if c, _ := a.Model.Chats.Get(ref); c.Locked() && a.Mode != ListLocked {
		return false
	}
	if !a.Active() {
		st.SwitchAccount(a.Index)
		if !a.Active() {
			return false
		}
	}
	a.openChat(ref, true) // an explicit target takes precedence over an unread mention
	if a.Open != ref {
		return false
	}
	p := &st.Panes
	p.Anchor.Reset()
	p.Anchor.Add(id)
	p.Jump.Chat = ref
	p.Jump.Msg.Reset()
	p.Jump.Msg.Add(id)
	p.Jump.Seq++
	if v := a.Model.Conversation(chat); v != nil && v.Find(id) < 0 && !v.LoadingOlder() && v.Len() > 0 {
		oldest := v.At(0)
		v.SetLoadingOlder(true)
		cmd := a.Cmd(abi.CommandTagLoadUntil)
		cmd.LoadUntil.Chat, cmd.LoadUntil.ID = chat, id
		cmd.LoadUntil.Before = abi.PageKey{Timestamp: oldest.Time(), ID: oldest.ID()}
		a.Send(cmd)
	}
	st.gen++
	return true
}

// OpenMessage is Account.OpenMessage on the account on screen.
func (s *State) OpenMessage(chat, id []byte) bool {
	a := s.Acct()
	return a != nil && a.OpenMessage(chat, id)
}

// OpenMessageIn is OpenMessage in the account with ID account (a clicked
// notification names its account); with no id, it only puts the chat's
// account on screen (a call's notification). Callers check the app lock
// first: while locked the request waits for the unlock (ui/lock's Defer).
func (s *State) OpenMessageIn(account string, chat, id []byte) bool {
	a := s.AccountByID(account)
	if a == nil {
		return false
	}
	if len(id) == 0 {
		s.SwitchAccount(a.Index)
		return a.Active()
	}
	return a.OpenMessage(chat, id)
}

// AccountByID is the account with folder ID id, nil for none.
func (s *State) AccountByID(id string) *Account {
	for _, a := range s.Accounts {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// RaiseMain brings the main window forward, opening it again when it was
// closed (the viewer after Reply, Forward or Show in the chat; a clicked
// notification). The shell acts on it at its next frame, or the app with no
// main window.
func (s *State) RaiseMain() {
	s.Req.RaiseMain = true
	s.gen++
	s.Wake()
}

// CloseWindow asks the shell to close window w (the app lock closes the
// viewer and Settings).
func (s *State) CloseWindow(w Window) {
	s.Win.Want[w] = false
	if s.Win.Open[w] {
		s.Win.Shut[w] = true
	}
	s.gen++
	s.Wake()
}

// OpenViewer opens the media viewer on message msg of chat, and card (a
// carousel card's index) when it is not model.PartMain.
func (s *State) OpenViewer(chat model.ChatRef, msg []byte, card int) {
	w := &s.Win
	w.ViewerChat = chat
	w.ViewerMsg.Reset()
	w.ViewerMsg.Add(msg)
	w.ViewerCard, w.ViewerHasCard = max(card, 0), card >= 0
	w.ViewerPlay, w.ViewerAt = false, 0
	s.OpenWindow(WinViewer)
}

// OpenViewerPlaying opens the viewer on a video of chat, playing at once
// from its start (Win.ViewerPlay); OpenViewerPlayingAt from where the
// bubble's clip stood.
func (s *State) OpenViewerPlaying(chat model.ChatRef, msg []byte) {
	s.OpenViewerPlayingAt(chat, msg, 0)
}

// OpenViewerPlayingAt opens the viewer on a video of chat, playing at once
// (Win.ViewerPlay) from at (Win.ViewerAt): a double click on a clip in the
// transcript, or its "Fill the window" (zf:src/ui/conversation.rs video,
// Action::ExpandVideo). at is where the bubble's clip of this message
// stood, 0 when it was not loaded: upstream's one player carries on from
// there (zf:src/app.rs expand_video, video.rs set_expanded).
func (s *State) OpenViewerPlayingAt(chat model.ChatRef, msg []byte, at time.Duration) {
	s.OpenViewer(chat, msg, model.PartMain)
	s.Win.ViewerPlay, s.Win.ViewerAt = true, max(at, 0)
}

// TakeViewerPlay reports whether the viewer should start its video playing
// now, and clears it: the viewer calls it once it shows the item.
func (s *State) TakeViewerPlay() bool {
	p := s.Win.ViewerPlay
	s.Win.ViewerPlay = false
	return p
}

// ---- the picker and reactions ----

// ReactTarget is the message the reaction picker reacts to (upstream's
// reaction_target and reaction_anchor): the conversation sets it to open
// the full picker for a message; React clears it.
type ReactTarget struct {
	Chat   model.ChatRef
	Msg    model.IDList // one message id; empty: no reaction picker
	Anchor Rect         // the bubble or the "+" it opens from (window pixels)
}

// Open reports whether a reaction is being picked.
func (t *ReactTarget) Open() bool { return t.Msg.Len() > 0 }

// Reset closes the reaction picker.
func (t *ReactTarget) Reset() {
	t.Chat, t.Anchor = 0, Rect{}
	t.Msg.Reset()
}

// OpenReaction opens the reaction picker for message msg of chat, from
// anchor.
func (s *State) OpenReaction(chat model.ChatRef, msg []byte, anchor Rect) {
	t := &s.Panes.ReactTarget
	t.Chat, t.Anchor = chat, anchor
	t.Msg.Reset()
	t.Msg.Add(msg)
	s.gen++
}

// React reacts with emoji to message msg of the chat with JID chat (empty
// emoji takes our reaction away): the reaction picker closes and the emoji
// counts for the quick reactions (Settings.ReactionEmoji) and the recent
// ones (Action::React).
func (a *Account) React(chat, msg, emoji []byte) {
	st := a.st
	if len(emoji) > 0 {
		r := &st.Settings.ReactionEmoji
		count := uint32(1)
		for i, e := range *r {
			if bytes.Equal(e.Emoji, emoji) {
				count = e.Count + 1
				*r = append((*r)[:i], (*r)[i+1:]...)
				break
			}
		}
		*r = append(*r, EmojiCount{})
		copy((*r)[1:], *r)
		(*r)[0] = EmojiCount{Emoji: append([]byte(nil), emoji...), Count: count}
		// Most used first; a stable sort keeps the newest first among equals.
		for i := 1; i < len(*r); i++ {
			for j := i; j > 0 && (*r)[j].Count > (*r)[j-1].Count; j-- {
				(*r)[j], (*r)[j-1] = (*r)[j-1], (*r)[j]
			}
		}
		if len(*r) > 36 {
			*r = (*r)[:36]
		}
		st.UseEmoji(emoji)
	}
	if a.Active() {
		st.Panes.ReactTarget.Reset()
	}
	cmd := a.Cmd(abi.CommandTagReact)
	cmd.React.Chat, cmd.React.Message, cmd.React.Emoji = chat, msg, emoji
	a.Send(cmd)
	st.gen++
}

// UseEmoji puts emoji first among the recent ones (Settings.RecentEmoji, 36
// at most): the one owner of that list (zf:src/app.rs remember_emoji).
func (s *State) UseEmoji(emoji []byte) {
	if len(emoji) == 0 {
		return
	}
	r := &s.Settings.RecentEmoji
	for i, e := range *r {
		if bytes.Equal(e, emoji) {
			*r = append((*r)[:i], (*r)[i+1:]...)
			break
		}
	}
	*r = append(*r, nil)
	copy((*r)[1:], *r)
	(*r)[0] = append([]byte(nil), emoji...)
	if len(*r) > 36 {
		*r = (*r)[:36]
	}
	s.SettingsChanged()
}

// InsertEmoji asks the composer to insert emoji at its cursor and take the
// keyboard (Action::InsertEmoji): the picker's choice. It counts as used.
func (s *State) InsertEmoji(emoji []byte) {
	s.Req.Insert = append(s.Req.Insert, emoji...)
	s.Req.FocusComposer = true
	s.UseEmoji(emoji)
}

// TakeInsert is the text waiting for the composer (InsertEmoji), which it
// inserts; the request is cleared. The bytes are valid until the next
// InsertEmoji.
func (s *State) TakeInsert() []byte {
	b := s.Req.Insert
	s.Req.Insert = s.Req.Insert[:0]
	return b
}

// ---- the composer ----

// StagedKind is how a staged attachment goes.
type StagedKind uint8

const (
	StagedFile     StagedKind = iota // as what it is: a picture as a photo
	StagedDocument                   // as a document, with its own name and bytes
	StagedPicture                    // a pasted or dropped picture (pixels in Blob), sent as a photo
)

// Staged is an attachment staged in the open chat's composer (upstream's
// Staged): ui/composer owns the list (State.Staged), the cropper replaces a
// picture by ID.
type Staged struct {
	ID   uint64
	Kind StagedKind
	Path []byte // the file; empty for a pasted picture
	Name string
	// A picture's size and straight-alpha RGBA pixels, once read.
	W, H   uint32
	Blob   abi.BlobRef
	Ready  bool
	Failed bool
}

// StagedByID is the open chat's staged attachment id, nil for none.
func (s *State) StagedByID(id uint64) *Staged {
	for i := range s.Staged {
		if s.Staged[i].ID == id {
			return &s.Staged[i]
		}
	}
	return nil
}

// Prefill is text a link brought for a chat's composer (upstream's
// PrefillComposer): the composer takes it when that chat is open, only into
// an empty composer that is not editing, recording or replying, and with no
// draft (zf:src/app.rs:6149), then clears it.
type Prefill struct {
	Chat []byte // the chat's JID; empty: none
	Text []byte
	// Acct is the Serial of the account whose chat it is: a JID names a
	// chat only inside one account. Requests.Restore sets and checks it.
	Acct uint64
}

// PrefillComposer asks the composer to start chat's message with text.
func (s *State) PrefillComposer(chat, text []byte) {
	s.Req.Prefill.Chat = append(s.Req.Prefill.Chat[:0], chat...)
	s.Req.Prefill.Text = append(s.Req.Prefill.Text[:0], text...)
	s.gen++
}

// StartChat opens the chat with jid, asking the core to make it first when
// the account does not have it (Action::StartChat: a contact, a link, a
// member); it opens once it appears, within 10 seconds. text, when not nil,
// prefills the composer once it is open (a link's text).
func (a *Account) StartChat(jid, name, text []byte) {
	st := a.st
	if text != nil {
		st.PrefillComposer(jid, text)
	}
	if ref := a.Model.Chats.Find(jid); ref != 0 {
		if c, _ := a.Model.Chats.Get(ref); !c.Locked() || a.Mode == ListLocked {
			a.OpenChat(ref)
		}
		return
	}
	cmd := a.Cmd(abi.CommandTagEnsureChat)
	cmd.EnsureChat.Chat, cmd.EnsureChat.Name = jid, name
	if a.Send(cmd) == nil {
		a.starting = append(a.starting[:0], jid...)
		a.startBy = st.Now().Add(10 * time.Second)
	}
}

// started opens the chat StartChat asked the core for, once it is in.
func (a *Account) started() {
	if len(a.starting) == 0 {
		return
	}
	if a.st.Now().After(a.startBy) {
		a.starting = a.starting[:0]
		return
	}
	if ref := a.Model.Chats.Find(a.starting); ref != 0 {
		a.starting = a.starting[:0]
		if a.Active() {
			a.OpenChat(ref)
		}
	}
}

// LockedAway reports whether chat ref is a locked chat that may not show
// now: the locked folder is not open (its authorization ended or never
// began). Views of a chat's content refuse such a chat.
func (a *Account) LockedAway(ref model.ChatRef) bool {
	c, ok := a.Model.Chats.Get(ref)
	return ok && c.Locked() && a.Mode != ListLocked
}

// CloseLockedFolder is upstream's close_locked_folder (zf:src/app.rs:4270):
// the locked folder's authorization ends, and what it let the reader see
// goes with it (hideLocked), and the chat list's search is emptied
// (Req.ClearSearch, upstream's search.clear()). The list goes back to the
// chats. Every way out of the folder comes here: the rail, Escape, the
// shortcuts, a chip or label, the app lock, the main window closing,
// another account on screen. It does nothing while the folder is not open.
func (a *Account) CloseLockedFolder() {
	if a.Mode != ListLocked {
		return
	}
	a.Mode = ListChats
	a.hideLocked()
	if a.Active() {
		a.st.Req.ClearSearch = true
	}
	a.st.gen++
}

// SetMode makes m the list shown; leaving the locked folder closes it
// (CloseLockedFolder).
func (a *Account) SetMode(m ListMode) {
	if m != ListLocked {
		a.CloseLockedFolder()
	}
	a.Mode = m
}

// hideLocked closes what shows a locked chat that may no longer show
// (LockedAway; upstream's hide_locked_chat, leave_chat and
// drop_locked_panel): the open chat, with its draft parked by the
// OnChatClose hooks, its side panes, its search and its selection, and its
// notifications; an info panel, the viewer or a dialog (poll results, a
// list's options, forwarding its messages …) on a locked chat.
func (a *Account) hideLocked() {
	st := a.st
	if a.LockedAway(a.Open) {
		if len(a.openID) > 0 {
			a.clearNotices(a.openID)
		}
		a.openChat(0, false)
	}
	if a.Active() {
		st.dropChatUI(func(ref model.ChatRef, id []byte) bool {
			if ref == 0 {
				ref = a.Model.Chats.Find(id)
			}
			return a.LockedAway(ref)
		})
	}
}

// lockingNotices takes the notifications of a chat that e locks (locked
// from the phone, in a ChatUpdated or a Chats batch) off the desktop, as
// upstream's leave_chat, but also while the locked folder is open: an
// intentional privacy improvement over upstream's handle_chat_updated.
// A locked chat never notifies (notification_eligible in src/app.rs and
// call_notification_eligible in app/calls.rs). It runs before the model
// applies e, which is when the known chat is still seen unlocked: a chat
// already locked queues nothing on each subsequent update.
func (a *Account) lockingNotices(e *abi.Event) {
	switch e.Tag {
	case abi.EventTagChatUpdated:
		a.lockingNotice(&e.ChatUpdated.Chat)
	case abi.EventTagChats:
		for i := range e.Chats.Chats {
			a.lockingNotice(&e.Chats.Chats[i])
		}
	}
}

func (a *Account) lockingNotice(c *abi.Chat) {
	if !c.Locked {
		return
	}
	// A stranger can ring before its chat exists; its first locked row must
	// withdraw that call too. Already-locked rows still queue nothing.
	if was, ok := a.Model.Chats.Get(a.Model.Chats.Find(c.ID)); !ok || !was.Locked() {
		a.clearNotices(c.ID)
	}
}

// clearNotices asks package sys to take a locked chat's notifications off
// the desktop, held and in flight ones included, its ringing call's with
// its ringtone (State.NoticeClears, Locked).
func (a *Account) clearNotices(chat []byte) {
	st := a.st
	st.NoticeClears = append(st.NoticeClears, NoticeClear{Account: a.ID, Chat: append([]byte(nil), chat...), Locked: true})
}

// QuoteHidden reports whether the composer's private reply (Panes.Reply
// with Panes.ReplyChat) quotes a chat the reader may not see now: a locked
// chat while the locked folder is closed, or a chat that is gone
// (zf:src/app.rs reply_preview; MM-007). While it is, the composer hides
// the quote and sends nothing; the reply stays, and shows again once the
// folder is open or the chat unlocked.
func (s *State) QuoteHidden(a *Account) bool {
	p := &s.Panes
	if a == nil || p.Reply.Len() == 0 || p.ReplyChat.Len() == 0 {
		return false
	}
	c, ok := a.Model.Chats.Get(a.Model.Chats.Find(p.ReplyChat.At(0)))
	if !ok {
		return true
	}
	return c.Locked() && a.Mode != ListLocked
}
