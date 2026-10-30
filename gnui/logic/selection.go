package logic

import (
	"zapfast/abi"
	"zapfast/model"
	"zapfast/state"
)

// Selection mode (zf:src/app.rs:6789-6912, 327-353, 4038-4072): the open
// chat's selected messages, in the chat's order (State.Panes.Selection),
// the last clicked one (SelectAnchor) and a mouse sweep (Sweep*).

// CanSelect reports whether a message of this kind can join a selection:
// the ones the core can forward (zf:src/app.rs:327).
func CanSelect(kind abi.ContentTag) bool {
	switch kind {
	case abi.ContentTagRevoked, abi.ContentTagPhoneOnly, abi.ContentTagUnsupported, abi.ContentTagPoll,
		abi.ContentTagInteractive, abi.ContentTagCall, abi.ContentTagWaiting:
		return false
	}
	return true
}

// selectable reports whether the open chat's message id can be selected.
func selectable(s *state.State, v *model.Conversation, id []byte) bool {
	if v == nil || s.Panes.Revoking.Has(id) {
		return false
	}
	i := v.Find(id)
	return i >= 0 && CanSelect(v.At(i).Kind())
}

// sortSelection puts ids in the chat's order (ids the chat has not loaded
// go last).
func sortSelection(v *model.Conversation, ids *model.IDList) {
	var out model.IDList
	for i := range v.Len() {
		if id := v.At(i).ID(); ids.Has(id) {
			out.Add(id)
		}
	}
	for i := range ids.Len() {
		out.Add(ids.At(i))
	}
	ids.Reset()
	for i := range out.Len() {
		ids.Add(out.At(i))
	}
}

// addRange adds the messages from anchor to to, in either direction, that
// can be selected (zf:src/app.rs:342).
func addRange(s *state.State, v *model.Conversation, ids *model.IDList, anchor, to []byte) {
	from, end := v.Find(anchor), v.Find(to)
	if from < 0 || end < 0 {
		return
	}
	from, end = min(from, end), max(from, end)
	for i := from; i <= end; i++ {
		if m := v.At(i); CanSelect(m.Kind()) && !s.Panes.Revoking.Has(m.ID()) {
			ids.Add(m.ID())
		}
	}
	sortSelection(v, ids)
}

func setAnchor(l *model.IDList, id []byte) {
	l.Reset()
	if id != nil {
		l.Add(id)
	}
}

// StartSelection opens selection mode with nothing picked; choosing it
// again keeps what is ticked.
func StartSelection(s *state.State, a *state.Account) {
	p := &s.Panes
	if a.Open == 0 || p.Selecting {
		return
	}
	p.Selecting = true
	p.Selection.Reset()
	p.SelectAnchor.Reset()
	s.Touch()
}

// SelectMessage starts selecting with this message.
func SelectMessage(s *state.State, a *state.Account, id []byte) {
	if !selectable(s, a.Conversation(), id) {
		return
	}
	p := &s.Panes
	p.Selecting = true
	p.Selection.Reset()
	p.Selection.Add(id)
	setAnchor(&p.SelectAnchor, id)
	s.Touch()
}

// SelectRange selects every message from the last one clicked to this one
// (shift-click).
func SelectRange(s *state.State, a *state.Account, id []byte) {
	p := &s.Panes
	v := a.Conversation()
	if !p.Selecting || v == nil {
		return
	}
	anchor := id
	if p.SelectAnchor.Len() > 0 {
		anchor = append([]byte(nil), p.SelectAnchor.At(0)...)
	}
	addRange(s, v, &p.Selection, anchor, id)
	setAnchor(&p.SelectAnchor, id)
	s.Touch()
}

// SweepMessages selects what a mouse drag swept, from the row it began on
// to the row under the pointer, over what was selected when it began; it
// starts a selection if none was open.
func SweepMessages(s *state.State, a *state.Account, anchor, to []byte) {
	p := &s.Panes
	v := a.Conversation()
	if v == nil {
		return
	}
	if !p.Sweeping || p.SweepAnchor.Len() == 0 || string(p.SweepAnchor.At(0)) != string(anchor) {
		p.Sweeping = true
		setAnchor(&p.SweepAnchor, anchor)
		p.SweepBase.Reset()
		if p.Selecting {
			for i := range p.Selection.Len() {
				p.SweepBase.Add(p.Selection.At(i))
			}
		}
	}
	setAnchor(&p.SweepTo, to)
	p.Selection.Reset()
	for i := range p.SweepBase.Len() {
		p.Selection.Add(p.SweepBase.At(i))
	}
	addRange(s, v, &p.Selection, anchor, to)
	// An open selection stays open, as its check boxes do, even empty.
	p.Selecting = p.Selecting || p.Selection.Len() > 0
	setAnchor(&p.SelectAnchor, to)
	s.Touch()
}

// EndSweep: the mouse button that swept was released.
func EndSweep(s *state.State) { s.Panes.Sweeping = false }

// ToggleSelected adds a message to the selection or takes it out; the last
// one out keeps selection mode open, as WhatsApp Web does.
func ToggleSelected(s *state.State, a *state.Account, id []byte) {
	p := &s.Panes
	v := a.Conversation()
	if !p.Selecting || !selectable(s, v, id) {
		return
	}
	setAnchor(&p.SelectAnchor, id)
	if p.Selection.Has(id) {
		p.Selection.Remove(id)
	} else {
		p.Selection.Add(id)
		sortSelection(v, &p.Selection)
	}
	s.Touch()
}

// CancelSelection leaves selection mode.
func CancelSelection(s *state.State) {
	p := &s.Panes
	p.Selecting, p.Sweeping = false, false
	p.Selection.Reset()
	p.SelectAnchor.Reset()
	p.SweepBase.Reset()
	s.Touch()
}

// PruneSelection drops the messages that can no longer be selected (gone,
// revoked), moving the anchor to the last one left (zf:src/app.rs:4038).
func PruneSelection(s *state.State, a *state.Account) {
	p := &s.Panes
	if !p.Selecting {
		return
	}
	v := a.Conversation()
	prune := func(l *model.IDList) {
		for i := l.Len() - 1; i >= 0; i-- {
			if id := l.At(i); !selectable(s, v, id) {
				l.Remove(id)
			}
		}
	}
	prune(&p.Selection)
	prune(&p.SweepBase)
	if p.SelectAnchor.Len() > 0 && !selectable(s, v, p.SelectAnchor.At(0)) {
		p.SelectAnchor.Reset()
		if n := p.Selection.Len(); n > 0 {
			p.SelectAnchor.Add(p.Selection.At(n - 1))
		}
	}
	s.Touch()
}
