package state

import (
	"bytes"

	"zapfast/abi"
	"zapfast/model"
)

// The split view: a second chat of the account beside the open one in the
// main window, as Zen's split tabs (Account.Split). Both panes are the
// whole conversation and composer; the open chat is the pane in use, and a
// press in the other puts it in use (sideState). Both are read as the open
// chat is: when they come in, and while the window has the focus (logic).
// A chat is in one pane at a time.

// minSplitShare and maxSplitShare bound the open chat's share of the width.
const (
	minSplitShare = 0.25
	maxSplitShare = 0.75
)

// sideState is the split chat's part of the window's state while it is
// not the pane in use: what the account keeps for its open chat and the
// per-chat fields of Panes, and its staged attachments. Putting the split
// pane in use (a press in it) trades it with the live one, so that
// everything that acts on the open chat (the keys, the menus, the picker,
// deletes, reading) acts on the pane in use; the other pane is built with
// its state traded in for the time it builds (SideBegin, SideEnd).
type sideState struct {
	openID    []byte
	unreadRef model.ChatRef
	unreadAt  int
	panes     chatPanes
	staged    []Staged
}

// chatPanes are the fields of Panes that belong to the chat they were set
// in.
type chatPanes struct {
	Selecting, Sweeping                                             bool
	Selection, PendingDelete, Revoking, SelectAnchor                model.IDList
	SweepAnchor, SweepTo, SweepBase, Reply, Edit, ReplyChat, Anchor model.IDList
	Picker                                                          Picker
	PickerAnchor                                                    Rect
	Jump                                                            Jump
	ReactTarget                                                     ReactTarget
}

func (c *chatPanes) trade(p *Panes) {
	c.Selecting, p.Selecting = p.Selecting, c.Selecting
	c.Sweeping, p.Sweeping = p.Sweeping, c.Sweeping
	c.Selection, p.Selection = p.Selection, c.Selection
	c.PendingDelete, p.PendingDelete = p.PendingDelete, c.PendingDelete
	c.Revoking, p.Revoking = p.Revoking, c.Revoking
	c.SelectAnchor, p.SelectAnchor = p.SelectAnchor, c.SelectAnchor
	c.SweepAnchor, p.SweepAnchor = p.SweepAnchor, c.SweepAnchor
	c.SweepTo, p.SweepTo = p.SweepTo, c.SweepTo
	c.SweepBase, p.SweepBase = p.SweepBase, c.SweepBase
	c.Reply, p.Reply = p.Reply, c.Reply
	c.Edit, p.Edit = p.Edit, c.Edit
	c.ReplyChat, p.ReplyChat = p.ReplyChat, c.ReplyChat
	c.Anchor, p.Anchor = p.Anchor, c.Anchor
	c.Picker, p.Picker = p.Picker, c.Picker
	c.PickerAnchor, p.PickerAnchor = p.PickerAnchor, c.PickerAnchor
	c.Jump, p.Jump = p.Jump, c.Jump
	c.ReactTarget, p.ReactTarget = p.ReactTarget, c.ReactTarget
}

// trade swaps the open chat's state with the split chat's: the split chat
// becomes the open one, without closing or opening anything.
func (a *Account) trade() {
	sd := &a.side
	a.Open, a.Split = a.Split, a.Open
	a.openID, sd.openID = sd.openID, a.openID
	a.unreadRef, sd.unreadRef = sd.unreadRef, a.unreadRef
	a.unreadAt, sd.unreadAt = sd.unreadAt, a.unreadAt
	if a.Active() {
		sd.panes.trade(&a.st.Panes)
		a.st.Staged, sd.staged = sd.staged, a.st.Staged
	}
}

// useSide puts the split pane in use: its chat becomes the open one (the
// tabs, the keys and the menus follow it), the other the split; the panes
// stay where they are.
func (a *Account) useSide() {
	if a.Split == 0 {
		return
	}
	left := a.Open // the chat the person was on: the last before this one
	a.trade()
	if left != 0 {
		a.prevOpen = left
	}
	a.SplitFirst = !a.SplitFirst
	if c, ok := a.Model.Chats.Get(a.Open); ok {
		a.tabs.note(a, a.Open)
		if !bytes.Equal(a.Settings.LastChat, c.ID()) {
			a.Settings.LastChat = append(a.Settings.LastChat[:0], c.ID()...)
			a.SettingsChanged()
		}
	}
	a.pinConversations()
}

// UseSplit puts the split pane in use (a press in it).
func (a *Account) UseSplit() {
	if a.Split == 0 {
		return
	}
	a.useSide()
	a.st.gen++
}

// SideBegin trades the split chat's state in, so that the split pane builds
// as the open chat; SideEnd trades it back. While it is in, the state says
// so (State.Side): the one-shot requests and the keys are the live pane's.
func (a *Account) SideBegin() {
	a.trade()
	a.st.Side = true
}

// SideEnd ends SideBegin.
func (a *Account) SideEnd() {
	a.st.Side = false
	a.trade()
}

// OpenSplit puts chat ref beside the open chat, on the second pane, in
// use. With no chat open it opens ref. A locked chat outside the locked
// folder and a chat that is not there are refused; a chat already in a
// pane is put in use.
func (a *Account) OpenSplit(ref model.ChatRef) bool {
	c, ok := a.Model.Chats.Get(ref)
	if !ok || (c.Locked() && a.Mode != ListLocked) {
		return false
	}
	switch {
	case a.Open == 0:
		a.OpenChat(ref)
		return a.Open == ref
	case ref == a.Open:
		return false
	case ref == a.Split:
		a.UseSplit()
		return true
	case a.Split != 0:
		a.CloseSplit() // two chats at most: the one beside goes
	}
	a.Split, a.SplitFirst = ref, false
	sd := &a.side
	sd.openID = append(sd.openID[:0], c.ID()...)
	sd.unreadRef, sd.unreadAt = ref, c.Unread()
	sd.panes = chatPanes{}
	sd.staged = nil
	a.loadSide(c)
	if a.Active() && (c.Unread() > 0 || c.MarkedUnread()) {
		a.MarkRead(ref)
	}
	a.useSide()         // the chat put beside is the one in use,
	a.SplitFirst = true // on the second pane: the one it went beside keeps the first
	a.st.gen++
	return true
}

// SplitWith shows ref and the open chat side by side, ref in use: first
// (the left pane) or second. The chat open before stays beside it.
func (a *Account) SplitWith(ref model.ChatRef, first bool) bool {
	if !a.OpenSplit(ref) {
		return false
	}
	a.SplitFirst = !first // the split chat is the other one
	a.st.gen++
	return true
}

// CloseSplit takes the split chat away; the open chat has the window again.
func (a *Account) CloseSplit() {
	if a.Split == 0 {
		return
	}
	// the split chat closes as an open chat does, as its own: its pane's
	// composer keeps its text, quote and attachments (OnChatClose)
	a.SideBegin()
	a.st.chatClosing(a)
	a.SideEnd()
	a.Split, a.SplitFirst = 0, false
	a.side = sideState{openID: a.side.openID[:0]}
	a.pinConversations()
	a.st.gen++
}

// Expand gives the window to one pane's chat (ref): the split ends, the
// other chat keeps its tab.
func (a *Account) Expand(ref model.ChatRef) {
	if ref == a.Split {
		a.useSide()
	}
	a.CloseSplit()
}

// SwapSplit trades the panes' places; the pane in use stays in use.
func (a *Account) SwapSplit() {
	if a.Split == 0 {
		return
	}
	a.SplitFirst = !a.SplitFirst
	a.st.gen++
}

// Panes is the split view's chats in their places: first (left) and
// second; with no split, the open chat and 0.
func (a *Account) Panes() (first, second model.ChatRef) {
	if a.Split == 0 {
		return a.Open, 0
	}
	if a.SplitFirst {
		return a.Split, a.Open
	}
	return a.Open, a.Split
}

// PrevOpen is the chat open before the open one (0 for none): the chat a
// tab dragged to a split goes beside when it is the open one, as Zen's.
func (a *Account) PrevOpen() model.ChatRef {
	if c, ok := a.Model.Chats.Get(a.prevOpen); !ok || a.prevOpen == a.Open || (c.Locked() && a.Mode != ListLocked) {
		return 0
	}
	return a.prevOpen
}

// SplitChat is the split chat when there is one to show: there, and not a
// locked chat outside the locked folder.
func (a *Account) SplitChat() (model.Chat, bool) {
	c, ok := a.Model.Chats.Get(a.Split)
	if !ok || a.Split == a.Open || (c.Locked() && a.Mode != ListLocked) {
		return model.Chat{}, false
	}
	return c, true
}

// IsSplit reports whether chat is the split chat.
func (a *Account) IsSplit(chat []byte) bool {
	c, ok := a.Model.Chats.Get(a.Split)
	return ok && string(c.ID()) == string(chat)
}

// Share is the first pane's share of the split view's width.
func (s *State) Share() float32 {
	if s.SplitShare <= 0 {
		return 0.5
	}
	return min(max(s.SplitShare, minSplitShare), maxSplitShare)
}

// loadSide readies a chat shown beside the open one (the split, the
// floating chat): its conversation, its latest messages and its pins.
func (a *Account) loadSide(c model.Chat) {
	if a.Conn == nil {
		return
	}
	id := c.ID()
	a.Model.Open(id)
	cmd := a.Cmd(abi.CommandTagLoadChat)
	cmd.LoadChat.Chat = id
	a.Send(cmd)
	cmd = a.Cmd(abi.CommandTagLoadPins)
	cmd.LoadPins.Chat = id
	a.Send(cmd)
}

// LoadOlderIn pages chat's archive back from its oldest loaded message, or
// asks the phone once the archive has nothing older, as LoadOlder does for
// the open chat; the pane that asks keeps its own place.
func (a *Account) LoadOlderIn(chat []byte, explicit bool) {
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
	cmd := a.Cmd(abi.CommandTagLoadChat)
	cmd.LoadChat.Chat, cmd.LoadChat.HasBefore = chat, true
	cmd.LoadChat.Before = abi.PageKey{Timestamp: oldest.Time(), ID: oldest.ID()}
	a.Send(cmd)
}
