package logic

import (
	"time"

	"zapfast/abi"
	"zapfast/state"
	"zapfast/ui/screen"
)

// Logic is the reducers: the listeners and the per-frame work. Screens
// call the package's functions (ToggleSelected, ConfirmForward, VoiceEnded
// …), which act on the state.
type Logic struct {
	s        *state.State
	focused  bool
	throttle map[throttleKey]time.Time
	posted   map[throttleKey]bool // chats with a notice on the desktop
	chat     abi.Chat
}

var _ screen.Reducer = (*Logic)(nil)

// New returns the reducers.
func New() *Logic {
	return &Logic{throttle: map[throttleKey]time.Time{}, posted: map[throttleKey]bool{}}
}

// Init registers the reducers' listeners (state.Listen).
func (l *Logic) Init(s *state.State) {
	l.s = s
	s.Listen(l, abi.EventTagChats, abi.EventTagChatUpdated, abi.EventTagIncoming, abi.EventTagMedia,
		abi.EventTagForwardLimit, abi.EventTagMessageUpdated, abi.EventTagMessageDeleted)
}

// Event is the reducers' listener.
func (l *Logic) Event(a *state.Account, e *abi.Event) {
	s := l.s
	switch e.Tag {
	case abi.EventTagChats:
		for i := range e.Chats.Chats {
			if c := &e.Chats.Chats[i]; c.Unread == 0 {
				l.clearNotices(a, c.ID)
			}
		}
	case abi.EventTagChatUpdated:
		l.chatUpdated(a, &e.ChatUpdated.Chat)
	case abi.EventTagIncoming:
		l.maybeNotify(a, e.Incoming.Chat, &e.Incoming.Message)
	case abi.EventTagMedia:
		mediaArrived(s, a, &e.Media)
	case abi.EventTagForwardLimit:
		forwardLimit(s, a, &e.ForwardLimit)
	case abi.EventTagMessageUpdated:
		if m := &e.MessageUpdated.Message; a.Active() && openIs(a, m.Chat) {
			s.Panes.Revoking.Remove(m.ID)
			PruneSelection(s, a)
		}
	case abi.EventTagMessageDeleted:
		if a.Active() && openIs(a, e.MessageDeleted.Chat) {
			PruneSelection(s, a)
		}
	}
}

// Frame runs once per main-window frame: read state on regaining the
// focus, the presence the cores report, and the end of a voice run whose
// chat was left.
func (l *Logic) Frame(s *state.State) {
	if l.s == nil {
		l.s = s
	}
	l.focusFrame(s)
	voiceFrame(s)
}
