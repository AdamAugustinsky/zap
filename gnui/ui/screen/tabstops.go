package screen

import "gonativeui/ui"

// TabStop is a place in the main window's deliberate Tab order (SH-016,
// zf:src/ui/focus.rs Stop): Tab and Shift+Tab walk the stops built this
// frame in the order of their values, wrapping around, instead of build
// order. Message text and chat rows stay pointer and accessibility
// targets, as upstream. The values with "+ n" are ranges: add the item's
// place (from 0).
type TabStop uint16

const (
	StopComposer TabStop = 100
	StopSend     TabStop = 110
	StopAttach   TabStop = 120
	StopEmoji    TabStop = 130
	// StopPhoneLink + n: the phone links of the messages shown, top to
	// bottom (n < 100).
	StopPhoneLink TabStop = 200
	// The chat header's Search, then the search pane's controls in reading
	// order.
	StopChatSearch      TabStop = 300
	StopChatSearchClose TabStop = 310
	StopChatSearchDate  TabStop = 320
	StopChatSearchField TabStop = 330
	StopChatSearchDay   TabStop = 340
	// The info panel's close or back button, then StopInfoControl + n, its
	// buttons and tabs in reading order (n < 90).
	StopInfoClose   TabStop = 400
	StopInfoControl TabStop = 410
	// The rail, top to bottom (the shell's).
	StopChats    TabStop = 500
	StopCalls    TabStop = 510
	StopStarred  TabStop = 520
	StopArchived TabStop = 530
	StopLocked   TabStop = 540
	StopSettings TabStop = 550
	StopProfile  TabStop = 560
	// The list header's Back and buttons, and its search field. Show the
	// chat list keeps Hide the chat list's place (StopSidebar).
	StopBack    TabStop = 600
	StopNewChat TabStop = 610
	StopSidebar TabStop = 620
	StopUpdate  TabStop = 625 // the titlebar's update button, at its end
	StopSearch  TabStop = 630
	// The chips: All (the chat list's and the Calls page's), Missed (the
	// Calls page's), then the chat list's; StopLabel + n the label row's by
	// position (n < 99), and Manage labels.
	StopAll          TabStop = 700
	StopMissed       TabStop = 710
	StopUnread       TabStop = 720
	StopPrivate      TabStop = 730
	StopFavorites    TabStop = 740
	StopGroups       TabStop = 750
	StopChannels     TabStop = 760
	StopLabel        TabStop = 800
	StopManageLabels TabStop = 900
)

// shellStop reports whether stop is one the shell itself registers (the
// rail): the deliberate order starts once a screen registers one of its own.
func shellStop(stop TabStop) bool { return stop >= StopChats && stop <= StopProfile }

type tabStop struct {
	stop TabStop
	key  ui.Key
}

// tabStops are the stops built in one frame of the window.
type tabStops struct {
	frame uint32
	list  []tabStop
	// off is the frame TabInBuildOrder was called in.
	off uint32
}

// TabStop puts the focusable box id (a button, a field, a chip) at stop in
// the main window's Tab order for this frame. Call it after building the
// box, every frame it shows. Outside the main window it does nothing
// useful (the other windows keep build order).
func (e *Env) TabStop(stop TabStop, id ui.ID) {
	if id == 0 {
		return
	}
	e.TabStopKey(stop, e.C().KeyOf(id))
}

// TabStopKey is TabStop for the box that takes the focus by its key: a
// text field's (base.FieldSignal.Key, rexui.ComposerSignal.Key), whose
// focus is on a box inside the field's.
func (e *Env) TabStopKey(stop TabStop, key ui.Key) {
	if key == 0 {
		return
	}
	t := e.thisFrame()
	if t.list == nil {
		t.list = make([]tabStop, 0, 128)
	}
	t.list = append(t.list, tabStop{stop, key})
}

// TabStops is how many stops were put in the order so far this frame: the
// shell's check that a pane put its own.
func (e *Env) TabStops() int { return len(e.thisFrame().list) }

// TabInBuildOrder leaves this frame's Tab to the framework's build order:
// a part that keeps its own order among its controls (a calendar's days,
// a recording's buttons) or whose controls are not stops (yet) calls it
// while it shows, so that Tab still reaches them.
func (e *Env) TabInBuildOrder() { e.tabs.off = e.thisFrame().frame }

func (e *Env) thisFrame() *tabStops {
	c := e.C()
	t := &e.tabs
	if t.frame != c.Frame() {
		t.frame, t.list = c.Frame(), t.list[:0]
	}
	return t
}

// TabOrder moves the keyboard focus on a Tab or Shift+Tab through the
// stops of this frame (TabStop) in their order; the shell calls it at the
// end of the main window's frame. It leaves Tab to the framework (build
// order) while no screen has registered a stop of its own, in a frame a
// part asked for build order (TabInBuildOrder), while a modal, a menu or a
// popup is open, when a field took Tab itself, and when the
// system's keyboard navigation is off (Tab then reaches only text fields
// and lists, the macOS convention). It allocates nothing.
func (e *Env) TabOrder() {
	c := e.C()
	t := &e.tabs
	if t.frame != c.Frame() || t.off == t.frame || len(t.list) == 0 || c.ModalOpen() || e.K.MenuOpen() || !c.TabReachesControls() {
		return
	}
	own := false
	for _, s := range t.list {
		own = own || !shellStop(s.stop)
	}
	if !own {
		return
	}
	fwd := c.TakeKey(ui.KeyTab, 0)
	back := !fwd && c.TakeKey(ui.KeyTab, ui.ModShift)
	if !fwd && !back {
		return
	}
	l := t.list
	for i := 1; i < len(l); i++ { // stable: a stop built twice keeps build order
		for j := i; j > 0 && l[j].stop < l[j-1].stop; j-- {
			l[j], l[j-1] = l[j-1], l[j]
		}
	}
	cur := -1
	for i, s := range l {
		if s.key == c.FocusKey() {
			cur = i
		}
	}
	var to int
	switch {
	case fwd && cur >= 0:
		to = (cur + 1) % len(l)
	case fwd:
		to = 0
	case cur >= 0:
		to = (cur - 1 + len(l)) % len(l)
	default:
		to = len(l) - 1
	}
	c.SetFocus(l[to].key)
	c.SetFocusVisible(true)
	c.RequestFrame()
}
