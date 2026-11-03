package chatlist

import (
	"gonativeui/widget/base"

	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The list's keyboard navigation is the framework's (base.ListNav): while a
// chat's row has the focus, ↑ and ↓, Page Up and Page Down, Home and End
// open the chat they reach, past headers, folders and rules; from the search
// field the arrows walk the matches (searchCmd). The rows are a virtual list,
// which brings the chat reached into view (reveal). J and K, ⌥↑ and ⌥↓ and
// ⌘⇧[ ] stay the shell's, from anywhere (shell/keyboard.go).

// keys moves the list with the keyboard, before its rows are built.
func (s *Screen) keys(e *screen.Env, a *state.Account) {
	k := e.K.Kit
	focused := s.rowFocus
	s.rowFocus = false
	cur := a.Open
	if s.query != "" {
		cur = s.selRef // the search's own selection, none at first
	}
	s.nav.Sel = -1
	if i, ok := s.st.IndexOf(itemID(kChat, uint32(cur))); ok && cur != 0 {
		s.nav.Sel = i
	}
	s.nav.Begin(k, len(s.ids), 0, false)
	if focused {
		if m, _ := base.TakeNavKey(k.Context(), false); m != base.NavNone && s.nav.Move(m) {
			s.reach(s.nav.Sel)
			s.focusRef = s.refAt(s.nav.Sel)
			if s.query == "" {
				if ref := s.focusRef; ref != 0 {
					s.KeepUnread(e, ref)
					a.OpenChat(ref)
				}
			}
		}
	}
	s.nav.End(k)
}

// reach makes row i, a chat, the one the keys reached: the search's
// selection, brought into view.
func (s *Screen) reach(i int) {
	if ref := s.refAt(i); ref != 0 {
		if s.query != "" {
			s.selRef = ref
		}
		s.reveal(ref)
	}
}

// refAt is the chat of row i, 0 when it is not a chat's.
func (s *Screen) refAt(i int) model.ChatRef {
	if i < 0 || i >= len(s.ids) || itemKind(s.ids[i]) != kChat {
		return 0
	}
	return model.ChatRef(itemN(s.ids[i]))
}

// navRow says whether row r can be selected: a chat's (ListNav.Selectable).
func (s *Screen) navRow(r int) bool { return s.refAt(r) != 0 }
