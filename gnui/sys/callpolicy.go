package sys

import (
	"zapfast/abi"
	"zapfast/i18n"
)

// What the call notifications and the waiting list need. Package logic
// decides about messages (State.Notices); a ringing call is the system's own.

// waitingLimit is how many notifications may wait for a click: the oldest
// past it is released (zf:src/notify.rs WAITING_LIMIT).
const waitingLimit = 32

// callEligible is the chat's own rules for a call: as quiet as a message in
// a muted, archived or locked chat (zf:src/app/calls.rs).
func callEligible(archived, locked, muted bool) bool {
	return !archived && !muted && !locked
}

// incomingCallLocked is the title and body of a call that is behind the app
// lock: it learns that a call came, and nothing about who is calling.
func incomingCallLocked() (title, body string) {
	return "Zap", i18n.T("Incoming call")
}

// incomingText is what a ringing call says it is (zf:src/ui/call.rs
// incoming_text): a one-to-one call its kind, a group call also who is
// calling. caller is "" when unknown or hidden.
func incomingText(u *abi.CallUpdate, caller string) string {
	switch {
	case !u.HasGroup && !u.Video:
		return i18n.T("Incoming voice call")
	case !u.HasGroup:
		return i18n.T("Incoming video call")
	case caller != "" && !u.Video:
		return i18n.Format(i18n.T("Group voice call from {name}"), "{name}", caller)
	case caller != "":
		return i18n.Format(i18n.T("Group video call from {name}"), "{name}", caller)
	case !u.Video:
		return i18n.T("Incoming group voice call")
	}
	return i18n.T("Incoming group video call")
}

// ---- The notifications waiting for a click ----

// callKey is the key a call's notification waits under, apart from the
// chat's message notifications: answering a call takes back its own
// notification and leaves a message that arrived meanwhile on the desktop.
func callKey(chat string) string { return "call:" + chat }

// posted is one notification the system was given and may still show.
type posted struct {
	id        string
	acct, key string
	order     uint64
}

// waiting is the notifications that may still wait for a click, oldest
// first. Each holds a response slot in the system, so the list is bounded:
// the oldest past waitingLimit is released (left on the desktop, no longer
// tracked).
type waiting struct {
	list  []posted
	order uint64
}

// add registers a notification under key and returns the id released to
// make room ("" for none).
func (w *waiting) add(acct, key, id string) (released string) {
	if len(w.list) >= waitingLimit {
		released = w.list[0].id
		w.list = append(w.list[:0], w.list[1:]...)
	}
	w.order++
	w.list = append(w.list, posted{id: id, acct: acct, key: key, order: w.order})
	return released
}

// take removes the notifications that match and returns their ids.
func (w *waiting) take(match func(p *posted) bool) []string {
	var ids []string
	n := 0
	for i := range w.list {
		if match(&w.list[i]) {
			ids = append(ids, w.list[i].id)
			continue
		}
		w.list[n] = w.list[i]
		n++
	}
	clear(w.list[n:])
	w.list = w.list[:n]
	return ids
}

// clear takes back the chat's notifications: delivered and pending.
func (w *waiting) clear(acct, chat string) []string {
	return w.take(func(p *posted) bool { return p.acct == acct && p.key == chat })
}

// clearCall takes back a call's notification, and only that one.
func (w *waiting) clearCall(acct, chat string) []string { return w.clear(acct, callKey(chat)) }

// clearAccount takes back everything of the account.
func (w *waiting) clearAccount(acct string) []string {
	return w.take(func(p *posted) bool { return p.acct == acct })
}

// clearAll takes back everything.
func (w *waiting) clearAll() []string { return w.take(func(*posted) bool { return true }) }

// forget drops id from the list (the system refused it).
func (w *waiting) forget(id string) {
	w.take(func(p *posted) bool { return p.id == id })
}
