package state

import (
	"bytes"

	"zapfast/abi"
	"zapfast/model"
)

// The split view: several chats of the account side by side in the main
// window, as Zen's split tabs (Account.SplitPanes, left to right). Every
// pane is the whole conversation and composer; the open chat is the pane in
// use, and a press in another puts that one in use (sideState). All are read
// as the open chat is: when they come in, and while the window has the focus
// (logic). A chat is in one pane at a time; how many panes fit is the
// shell's to say (State.MaxPanes), from the window's width.

// minSplitShare and maxSplitShare bound a pane's share of the width.
const (
	minSplitShare = 0.03 // the panes' least width keeps them usable (the shell)
	maxSplitShare = 0.97
)

// sideState is a pane's part of the window's state while it is not the
// pane in use: what the account keeps for its open chat and the per-chat
// fields of Panes, and its staged attachments. Putting the pane in use (a
// press in it) trades it with the live one, so that everything that acts on
// the open chat (the keys, the menus, the picker, deletes, reading) acts on
// the pane in use; a pane not in use is built with its state traded in for
// the time it builds (SideBegin, SideEnd).
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

// swapLive swaps the open chat's per-chat state with sd's (the open chat's
// ref itself is the caller's).
func (a *Account) swapLive(sd *sideState) {
	a.openID, sd.openID = sd.openID, a.openID
	a.unreadRef, sd.unreadRef = sd.unreadRef, a.unreadRef
	a.unreadAt, sd.unreadAt = sd.unreadAt, a.unreadAt
	if a.Active() {
		sd.panes.trade(&a.st.Panes)
		a.st.Staged, sd.staged = sd.staged, a.st.Staged
	}
}

// trade makes pane ref's chat the open one, its state the live one; the
// chat open before keeps its own in a side state.
func (a *Account) trade(ref model.ChatRef) {
	sd := a.side[ref]
	if sd == nil || ref == a.Open {
		return
	}
	a.swapLive(sd)
	delete(a.side, ref)
	a.side[a.Open] = sd
	a.Open = ref
}

// SplitOn reports whether the split view is up: two panes or more.
func (a *Account) SplitOn() bool { return len(a.SplitPanes) >= 2 }

// Panes are the split view's chats left to right; nil with no split.
func (a *Account) Panes() []model.ChatRef {
	if !a.SplitOn() {
		return nil
	}
	return a.SplitPanes
}

// InSplit reports whether ref is in one of the split view's panes.
func (a *Account) InSplit(ref model.ChatRef) bool {
	if ref == 0 || !a.SplitOn() {
		return false
	}
	for _, r := range a.SplitPanes {
		if r == ref {
			return true
		}
	}
	return false
}

// IsSplit reports whether chat (a JID) is in a pane of the split view
// other than the one in use.
func (a *Account) IsSplit(chat []byte) bool {
	ref := a.Model.Chats.Find(chat)
	return ref != 0 && ref != a.Open && a.InSplit(ref)
}

// paneAt is ref's place in SplitPanes, -1 for none.
func (a *Account) paneAt(ref model.ChatRef) int {
	for i, r := range a.SplitPanes {
		if r == ref {
			return i
		}
	}
	return -1
}

// UsePane puts pane ref in use (a press in it): its chat becomes the open
// one (the tabs, the keys and the menus follow it); the panes stay where
// they are.
func (a *Account) UsePane(ref model.ChatRef) {
	if ref == a.Open || !a.InSplit(ref) {
		return
	}
	left := a.Open // the chat the person was on: the last before this one
	a.trade(ref)
	if left != 0 {
		a.prevOpen = left
	}
	if c, ok := a.Model.Chats.Get(a.Open); ok {
		a.tabs.note(a, a.Open)
		if !bytes.Equal(a.Settings.LastChat, c.ID()) {
			a.Settings.LastChat = append(a.Settings.LastChat[:0], c.ID()...)
			a.SettingsChanged()
		}
	}
	a.pinConversations()
	a.st.gen++
}

// SideBegin trades pane ref's state in, so that the pane builds as the
// open chat; SideEnd(ref) trades it back. While it is in, the state says so
// (State.Side): the one-shot requests and the keys are the live pane's.
func (a *Account) SideBegin(ref model.ChatRef) {
	sd := a.side[ref]
	if sd == nil {
		return
	}
	a.swapLive(sd)
	a.sideWas, a.Open = a.Open, ref
	a.st.Side = true
}

// SideEnd ends SideBegin(ref).
func (a *Account) SideEnd(ref model.ChatRef) {
	sd := a.side[ref]
	if sd == nil || !a.st.Side {
		return
	}
	a.st.Side = false
	a.Open = a.sideWas
	a.swapLive(sd)
}

// SetMaxPanes is the shell's: how many panes the window has room for now.
func (a *Account) SetMaxPanes(n int) { a.st.MaxPanes = n }

// MaxPanes is how many panes the window has room for (the shell's,
// State.MaxPanes; two at least).
func (a *Account) MaxPanes() int { return max(a.st.MaxPanes, 2) }

// OpenSplit puts chat ref beside the open chat (on its right), in use. With
// no chat open it opens ref; a chat already in a pane is put in use. A
// locked chat outside the locked folder, a chat that is not there, and a
// split with no room left are refused.
func (a *Account) OpenSplit(ref model.ChatRef) bool {
	at := 1
	if a.SplitOn() {
		at = a.paneAt(a.Open) + 1
	}
	return a.InsertPane(ref, at)
}

// InsertPane puts chat ref in a pane at place at (0 the first; past the
// end, the last), in use, as OpenSplit does.
func (a *Account) InsertPane(ref model.ChatRef, at int) bool {
	c, ok := a.Model.Chats.Get(ref)
	if !ok || (c.Locked() && a.Mode != ListLocked) {
		return false
	}
	switch {
	case a.Open == 0:
		a.OpenChat(ref)
		return a.Open == ref
	case ref == a.Open && !a.SplitOn():
		return false
	case a.InSplit(ref):
		a.UsePane(ref)
		return true
	}
	if !a.SplitOn() {
		a.SplitPanes = append(a.SplitPanes[:0], a.Open)
		a.parked = Group{}
	}
	if len(a.SplitPanes) >= a.MaxPanes() {
		return false
	}
	at = min(max(at, 0), len(a.SplitPanes))
	a.SplitPanes = append(a.SplitPanes, 0)
	copy(a.SplitPanes[at+1:], a.SplitPanes[at:])
	a.SplitPanes[at] = ref
	a.st.SplitShares = a.st.SplitShares[:0] // even again
	a.newSide(c)
	if a.Active() && (c.Unread() > 0 || c.MarkedUnread()) {
		a.MarkRead(ref)
	}
	a.UsePane(ref) // the chat put beside is the one in use
	a.st.gen++
	return true
}

// newSide gives chat c a pane's state of its own, and readies its
// conversation.
func (a *Account) newSide(c model.Chat) {
	if a.side == nil {
		a.side = map[model.ChatRef]*sideState{}
	}
	a.side[c.Ref()] = &sideState{openID: append([]byte(nil), c.ID()...), unreadRef: c.Ref(), unreadAt: c.Unread()}
	a.loadSide(c)
}

// ClosePane takes pane ref away; the pane in use closing puts its
// neighbour in use. A split left with one pane ends.
func (a *Account) ClosePane(ref model.ChatRef) {
	at := a.paneAt(ref)
	if at < 0 || !a.SplitOn() {
		return
	}
	if ref == a.Open {
		next := at + 1
		if next >= len(a.SplitPanes) {
			next = at - 1
		}
		a.UsePane(a.SplitPanes[next])
	}
	// it closes as an open chat does, as its own: its pane's composer keeps
	// its text, quote and attachments (OnChatClose)
	a.SideBegin(ref)
	a.st.chatClosing(a)
	a.SideEnd(ref)
	delete(a.side, ref)
	a.SplitPanes = append(a.SplitPanes[:at], a.SplitPanes[at+1:]...)
	a.st.SplitShares = a.st.SplitShares[:0]
	if len(a.SplitPanes) < 2 {
		a.SplitPanes = a.SplitPanes[:0]
		clear(a.side)
	}
	a.pinConversations()
	a.st.gen++
}

// CloseSplit ends the split: the open chat has the window again.
func (a *Account) CloseSplit() {
	for a.SplitOn() {
		for _, ref := range a.SplitPanes {
			if ref != a.Open {
				a.ClosePane(ref)
				break
			}
		}
	}
	a.parked = Group{}
}

// Expand gives the window to one pane's chat (ref): the split ends, the
// others keep their tabs.
func (a *Account) Expand(ref model.ChatRef) {
	a.UsePane(ref)
	a.CloseSplit()
}

// MovePane moves pane from to place to (the panes between shift); the pane
// in use stays in use.
func (a *Account) MovePane(from, to int) {
	p := a.SplitPanes
	if !a.SplitOn() || from < 0 || from >= len(p) || to < 0 || to >= len(p) || from == to {
		return
	}
	ref := p[from]
	if from < to {
		copy(p[from:to], p[from+1:to+1])
	} else {
		copy(p[to+1:from+1], p[to:from])
	}
	p[to] = ref
	if sh := a.st.SplitShares; len(sh) == len(p) {
		v := sh[from]
		if from < to {
			copy(sh[from:to], sh[from+1:to+1])
		} else {
			copy(sh[to+1:from+1], sh[to:from])
		}
		sh[to] = v
	}
	a.st.gen++
}

// PrevOpen is the chat open before the open one (0 for none): the chat a
// tab dragged to a split goes beside when it is the open one, as Zen's.
func (a *Account) PrevOpen() model.ChatRef {
	if c, ok := a.Model.Chats.Get(a.prevOpen); !ok || a.prevOpen == a.Open || (c.Locked() && a.Mode != ListLocked) {
		return 0
	}
	return a.prevOpen
}

// Shares are the n panes' shares of the split view's width, left to right
// (even until a divider moves them); they sum to one.
func (s *State) Shares(n int) []float32 {
	sh := s.SplitShares
	if len(sh) != n {
		sh = append(sh[:0], make([]float32, n)...)
		for i := range sh {
			sh[i] = 1 / float32(n)
		}
		s.SplitShares = sh
	}
	return sh
}

// Share is the first pane's share of a two-pane split.
func (s *State) Share() float32 { return s.Shares(2)[0] }

// SetShare moves the divider between panes i and i+1 so that pane i takes
// share v of the width the two hold together (kept to the bounds).
func (s *State) SetShare(n, i int, v float32) {
	sh := s.Shares(n)
	if i < 0 || i+1 >= n {
		return
	}
	pair := sh[i] + sh[i+1]
	v = min(max(v, minSplitShare), pair-minSplitShare)
	sh[i], sh[i+1] = v, pair-v
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

// Group is a split view's chats in their places, and the one in use: shown,
// or put aside while another chat shows alone, as Zen keeps split tabs as
// a group. Opening a chat of a group put aside brings the split back.
type Group struct {
	Chats []model.ChatRef
	Live  model.ChatRef
}

// Has reports whether ref is one of the group's chats.
func (g Group) Has(ref model.ChatRef) bool {
	for _, r := range g.Chats {
		if r != 0 && r == ref {
			return true
		}
	}
	return false
}

// Empty reports whether there is no group.
func (g Group) Empty() bool { return len(g.Chats) < 2 }

// Group is the account's split group: the split shown, or the one put
// aside; empty for none.
func (a *Account) Group() Group {
	if a.SplitOn() {
		return Group{Chats: a.SplitPanes, Live: a.Open}
	}
	return a.parked
}

// GroupShown reports whether the group is the split on screen.
func (a *Account) GroupShown() bool { return a.SplitOn() }

// park puts the split aside as its group, for another chat to show alone.
func (a *Account) park() {
	g := Group{Chats: append([]model.ChatRef(nil), a.SplitPanes...), Live: a.Open}
	a.CloseSplit()
	a.parked = g
}

// reopenGroup brings back the split put aside when ref is one of its
// chats, ref in use; it reports whether it did.
func (a *Account) reopenGroup(ref model.ChatRef) bool {
	g := a.parked
	if a.SplitOn() || !g.Has(ref) {
		return false
	}
	a.parked = Group{}
	var chats []model.ChatRef
	for _, r := range g.Chats {
		if c, ok := a.Model.Chats.Get(r); ok && !(c.Locked() && a.Mode != ListLocked) {
			chats = append(chats, r)
		}
	}
	if len(chats) < 2 {
		return false
	}
	if a.Open != ref {
		a.openChat(ref, true)
	}
	if a.Open != ref {
		return false
	}
	a.SplitPanes = append(a.SplitPanes[:0], chats...)
	a.st.SplitShares = a.st.SplitShares[:0]
	for _, r := range chats {
		if r == ref {
			continue
		}
		c, _ := a.Model.Chats.Get(r)
		a.newSide(c)
		if a.Active() && (c.Unread() > 0 || c.MarkedUnread()) {
			a.MarkRead(r)
		}
	}
	a.pinConversations()
	a.st.gen++
	return true
}

// ReopenGroup brings back the split put aside, the chat in use before in
// use again.
func (a *Account) ReopenGroup() {
	g := a.parked
	live := g.Live
	if !g.Has(live) && len(g.Chats) > 0 {
		live = g.Chats[0]
	}
	a.reopenGroup(live)
}

// CloseGroup ends the split group, shown or aside.
func (a *Account) CloseGroup() {
	a.CloseSplit()
	a.parked = Group{}
}
