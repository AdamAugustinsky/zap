package sys

import (
	"gonativeui/app/dock"

	"zapfast/state"
)

// The Dock tile: the badge with the number of unread chats, and the bounce
// that asks for attention while a call to us rings (NT-008, CA-029).
//
// Upstream does not bounce the Dock for a message on macOS (the
// notification is the signal), and keeps the Dock icon in resident mode, so
// neither does this.

type dockState struct {
	badge int
	set   bool
	ring  dock.Attention
}

// unreadChats is the unread chats across the accounts that count: not
// archived, not locked, not muted (zf:src/account.rs unread_chat_count).
func unreadChats(s *state.State) int {
	now := s.Now().Unix()
	n := 0
	for _, a := range s.Accounts {
		for _, ref := range a.Model.Chats.Order() {
			c := a.Model.Chats.At(ref)
			if !c.Archived() && !c.Locked() && !c.Muted(now) && (c.Unread() > 0 || c.MarkedUnread()) {
				n++
			}
		}
	}
	return n
}

// setBadge shows the unread count on the Dock icon, if it changed. The badge
// stays with the app when its window is closed and reopened.
func (y *System) setBadge(s *state.State) {
	n := unreadChats(s)
	d := &y.d
	if n == d.badge && (d.set || n == 0) {
		return
	}
	d.badge, d.set = n, true
	if y.dock != nil {
		if err := y.dock.SetBadgeCount(n); err != nil {
			logWarn("the Dock badge", "err", err)
		}
	}
}

// Dock is what ui/calls rings through while a call to us rings.
func (y *System) Dock() Dock { return callDock{y} }

type callDock struct{ y *System }

// Ring bounces the Dock icon until the app is active.
func (d callDock) Ring() {
	y := d.y
	if y.dock == nil || y.d.ring != 0 {
		return
	}
	id, err := y.dock.Bounce(true)
	if err != nil {
		logWarn("the Dock bounce", "err", err)
		return
	}
	y.d.ring = id
}

// Stop ends the bounce.
func (d callDock) Stop() {
	y := d.y
	if y.dock != nil && y.d.ring != 0 {
		y.dock.CancelBounce(y.d.ring)
	}
	y.d.ring = 0
}
