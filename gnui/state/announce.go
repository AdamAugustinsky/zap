package state

import (
	"slices"
	"time"
)

// AnnounceKind is what an announcement is about (zf:src/announcer.rs
// Category).
type AnnounceKind uint8

const (
	AnnounceMessage AnnounceKind = iota // a new message in the open chat
	AnnounceTyping                      // someone started typing or recording there
	AnnounceSent                        // one of our messages was sent, or could not be
	AnnounceStatus                      // the connection and the history
	AnnounceToast                       // a confirmation shown as a toast
	AnnounceError                       // an error: read before anything else waiting
)

// private reports whether announcements of kind k stay silent while the
// app is locked: messages and typing say who wrote or what, and toasts,
// errors included, are not drawn on the lock screen and can name a contact
// or an account.
func (k AnnounceKind) private() bool {
	return k == AnnounceMessage || k == AnnounceTyping || k == AnnounceToast || k == AnnounceError
}

// AnnounceFresh is how long an announcement waits to be spoken, as long as
// an info toast stays up: no frame is drawn for a hidden window, and what
// is older than this when one comes is dropped. An error waits, as its
// toast stays until it is closed.
const AnnounceFresh = 3200 * time.Millisecond

type announcement struct {
	kind AnnounceKind
	text []byte
	at   time.Time
}

// Announce has the screen reader say text without moving the focus
// (AX-001, zf:src/announcer.rs say): confirmations, errors, connection and
// history changes, and what screens announce (a new message, typing). Any
// goroutine may call it; the shell speaks what waits once a frame.
func (s *State) Announce(kind AnnounceKind, text string) {
	s.toastMu.Lock()
	s.announceLocked(kind, []byte(text), s.Now())
	s.toastMu.Unlock()
	if w := s.Wake; w != nil {
		w()
	}
}

// announceLocked queues an announcement; toastMu is held.
func (s *State) announceLocked(kind AnnounceKind, text []byte, now time.Time) {
	if len(text) == 0 {
		return
	}
	// While no frame speaks them, stale items must not pile up.
	s.said = slices.DeleteFunc(s.said, func(a announcement) bool { return !fresh(a, now) })
	s.said = append(s.said, announcement{kind: kind, text: append([]byte(nil), text...), at: now})
}

func fresh(a announcement, now time.Time) bool {
	return a.kind == AnnounceError || now.Sub(a.at) < AnnounceFresh
}

// TakeAnnouncement appends to dst what the screen reader should say now
// and empties the queue: errors first, then the rest in the order they
// were said, joined by ". ", stale ones dropped, and private ones (a
// message, typing, a toast or an error) dropped while private is false
// (the app is locked). assertive is whether it holds an error, which
// interrupts the screen reader; the rest waits its turn. It allocates
// nothing once dst has room, and nothing at all when nothing waits.
func (s *State) TakeAnnouncement(dst []byte, private bool) (out []byte, assertive bool) {
	s.toastMu.Lock()
	defer s.toastMu.Unlock()
	if len(s.said) == 0 {
		return dst, false
	}
	now := s.Now()
	for pass := 0; pass < 2; pass++ {
		for _, a := range s.said {
			if (a.kind == AnnounceError) != (pass == 0) || !fresh(a, now) || (!private && a.kind.private()) {
				continue
			}
			if len(dst) > 0 {
				dst = append(dst, ". "...)
			}
			dst = append(dst, a.text...)
			assertive = assertive || a.kind == AnnounceError
		}
	}
	clear(s.said)
	s.said = s.said[:0]
	return dst, assertive
}
