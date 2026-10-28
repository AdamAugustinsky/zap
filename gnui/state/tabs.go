package state

import "zapfast/model"

// MaxChatTabs is how many chats the titlebar's tabs keep.
const MaxChatTabs = 8

// ChatTabs are the chats in the titlebar's tabs (Rex's sessions), in the
// user's order: a chat opened joins them at the end, a closed tab forgets its
// chat, and a full set lets the leftmost go. They are the session's: a
// relinked number (a new Serial) starts with none.
type ChatTabs struct {
	refs   [MaxChatTabs]model.ChatRef
	n      int
	serial uint64
}

func (t *ChatTabs) index(ref model.ChatRef) int {
	for i, r := range t.refs[:t.n] {
		if r == ref {
			return i
		}
	}
	return -1
}

func (t *ChatTabs) remove(i int) {
	copy(t.refs[i:t.n], t.refs[i+1:t.n])
	t.n--
}

// note puts the chat a opens in the tabs, if it is not there yet.
func (t *ChatTabs) note(a *Account, ref model.ChatRef) {
	t.session(a)
	if ref == 0 || t.index(ref) >= 0 {
		return
	}
	if t.n == MaxChatTabs {
		t.remove(0)
	}
	t.refs[t.n] = ref
	t.n++
}

func (t *ChatTabs) session(a *Account) {
	if t.serial != a.Serial {
		*t = ChatTabs{serial: a.Serial}
	}
}

// ChatTabs is the chats in the titlebar's tabs, in their order: those that
// are gone forgotten, and a locked chat only while the locked folder is open
// (it leaves the tabs when the folder closes). The slice is the account's,
// valid until the tabs change.
func (a *Account) ChatTabs() []model.ChatRef {
	t := &a.tabs
	t.session(a)
	for i := 0; i < t.n; {
		if c, ok := a.Model.Chats.Get(t.refs[i]); !ok || (c.Locked() && a.Mode != ListLocked) {
			t.remove(i)
			continue
		}
		i++
	}
	return t.refs[:t.n]
}

// CloseChatTab closes tab i: the chat leaves the tabs and, if it is the open
// one, the tab after it opens (or the one before; none when it was the last).
func (a *Account) CloseChatTab(i int) {
	t := &a.tabs
	if i < 0 || i >= t.n {
		return
	}
	ref := t.refs[i]
	t.remove(i)
	if ref != a.Open {
		return
	}
	next := model.ChatRef(0)
	if t.n > 0 {
		next = t.refs[min(i, t.n-1)]
	}
	a.OpenChat(next)
}

// MoveChatTab moves tab from to to (the tabs between shift by one).
func (a *Account) MoveChatTab(from, to int) {
	t := &a.tabs
	if from < 0 || from >= t.n || to < 0 || to >= t.n || from == to {
		return
	}
	ref := t.refs[from]
	if from < to {
		copy(t.refs[from:to], t.refs[from+1:to+1])
	} else {
		copy(t.refs[to+1:from+1], t.refs[to:from])
	}
	t.refs[to] = ref
}
