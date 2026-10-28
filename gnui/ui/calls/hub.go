package calls

import (
	"bytes"
	"slices"
	"time"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
)

// The window's side of a call (zf:src/app/calls.rs): which account it is on,
// what the call screen shows, and the commands the screens send, routed to
// the core of the account the call belongs to. The call's state comes from
// that core (Call events); the hub never decides what a call is doing, except
// that a call whose button was just pressed shows as placing at once. One
// call at a time across every account: an offer to another account while a
// call is up is declined there. Tones and media stay in the core (D33).

const (
	// farewell is how long a call that ended says how (zf:src/app.rs:52).
	farewell = 1500 * time.Millisecond
	// placingTimeout is how long a call may stay placing with no word from
	// its core before the screen gives up on it.
	placingTimeout = 60 * time.Second
)

// hub is the calls' state for one state.State, shared by the Calls page (in
// the main window) and the call window.
type hub struct {
	s     *state.State
	accts []*acct
	call  call
	devs  devices
	dock  Dock
	// tone plays the ringback (tone.go); toneOn: it plays.
	tone   tonePort
	toneOn bool
	// vid are the textures of the call's video (video.go).
	vid videoTex

	// gen moves whenever anything the screens draw changed.
	gen uint64
	// ownGen numbers the calls placed while the account has no core to
	// number them (it is still opening).
	ownGen uint64
	// dismissed is the call the reader hung up or declined here, whose last
	// words from the core are not drawn; ended the calls that are over.
	dismissed uint64
	ended     [4]uint64
	endedN    int

	// winSeen: the call window has built since it last closed. waitUnlock:
	// the call's window waits for the app lock to open (it shows who is
	// calling, which a locked app does not say).
	winSeen, waitUnlock bool
	scratch             abi.CallUpdate
	nameBuf             []byte
}

// call is the call on screen.
type call struct {
	on bool
	// serial is the call's account (state.Account.Serial): an index moves
	// when an earlier account goes, a Serial never names another account.
	serial uint64
	rec    []byte         // u, encoded: u's bytes alias it
	u      abi.CallUpdate // the core's last word, or the placing one
	// started is when the media came up (the core's ActiveForMS ago).
	started    time.Time
	hasStarted bool
	// until is the farewell's end, zero while the call is live; placed is
	// when the button was pressed, zero once the core answered.
	until, placed time.Time
	// cancel is the token of a call still being placed (CancelCall).
	cancel uint64
	// hidden: the reader put the screen aside (or a locked chat hides it);
	// the call window is closed and the call bar shows it.
	hidden bool
	// ringing: Dock attention was asked for this call.
	ringing bool
}

// acct is one account's calls.
type acct struct {
	serial uint64 // the account's (state.Account.Serial)
	log    callLog
	// asked: LoadCallLog was sent since the page last opened on the account.
	asked bool
	// missed is the chip (Missed, not All), read from the account's
	// settings once (read).
	missed, read bool
	// newCall shows the new-call list instead of the history.
	newCall bool
	// picked is the call picked from the history (its chat and message id).
	picked model.IDList
}

// devices are the machine's microphones, speakers and cameras.
type devices struct {
	rec  []byte
	list abi.DeviceList
	gen  uint32
}

var hubs []*hub

// hubOf is s's hub, made on first use.
func hubOf(s *state.State) *hub {
	for _, h := range hubs {
		if h.s == s {
			return h
		}
	}
	h := &hub{s: s}
	s.Listen(h, abi.EventTagCall, abi.EventTagCallDevices, abi.EventTagCallLog, abi.EventTagIncoming)
	s.OnLock(h, h.locked)
	s.OnAccountGone(h, h.gone)
	hubs = append(hubs, h)
	return h
}

// acct is account a's calls, by its Serial.
func (h *hub) acct(a *state.Account) *acct {
	for _, ac := range h.accts {
		if ac.serial == a.Serial {
			return ac
		}
	}
	ac := &acct{serial: a.Serial}
	h.accts = append(h.accts, ac)
	return ac
}

// gone forgets what belonged to an account that is gone: its call history,
// and its call, which went with its core (zf:src/app.rs:1769
// forget_call_of).
func (h *hub) gone(_ *state.State, serial uint64) {
	if h.call.on && h.call.serial == serial {
		h.clear()
	}
	h.accts = slices.DeleteFunc(h.accts, func(ac *acct) bool { return ac.serial == serial })
	h.gen++
}

// bySerial is the account with the serial, nil once it is gone.
func (h *hub) bySerial(serial uint64) *state.Account {
	for _, a := range h.s.Accounts {
		if a.Serial == serial {
			return a
		}
	}
	return nil
}

// Event takes the core's call events (state.Listener).
func (h *hub) Event(a *state.Account, e *abi.Event) {
	switch e.Tag {
	case abi.EventTagCall:
		h.update(a, &e.Call.Update)
	case abi.EventTagCallDevices:
		h.devs.rec, _ = e.CallDevices.Devices.Encode(h.devs.rec[:0])
		h.devs.list.Decode(h.devs.rec)
		h.devs.gen++
		h.gen++
	case abi.EventTagCallLog:
		h.acct(a).log.add(&e.CallLog)
		h.gen++
	case abi.EventTagIncoming:
		// A call filed while the Calls page is open joins its list.
		if e.Incoming.Message.Content.Tag == abi.ContentTagCall && a.Active() && h.s.Page == state.PageCalls {
			h.loadLog(a)
		}
	}
}

// loadLog asks a's core for its call history. A core that cannot send one
// (the demo's refuses) leaves an empty, loaded history.
func (h *hub) loadLog(a *state.Account) {
	ac := h.acct(a)
	ac.asked = true
	if a.Send(a.Cmd(abi.CommandTagLoadCallLog)) != nil && !ac.log.loaded {
		ac.log.loaded = true
		ac.log.gen++
		h.gen++
	}
}

// liveCall is whether a call is up or ringing, on any account.
func (h *hub) liveCall() bool { return h.call.on && live(h.call.u.Phase) }

// showing is whether the call window should be open: not while the app is
// locked.
func (h *hub) showing() bool { return h.call.on && !h.call.hidden && !h.s.Lock.Locked }

// locked is the app lock locking: the call window goes, and comes back when
// the lock opens.
func (h *hub) locked(s *state.State) {
	if h.call.on && !h.call.hidden {
		h.winSeen, h.waitUnlock = false, true
		s.CloseWindow(state.WinCall)
	}
}

// openWindow asks for the call window, or for it once the app lock opens.
func (h *hub) openWindow(alone bool) {
	if h.s.Lock.Locked {
		h.waitUnlock = true
		return
	}
	if h.s.Win.Open[state.WinCall] {
		return
	}
	if alone {
		h.s.OpenWindowAlone(state.WinCall)
		return
	}
	h.s.OpenWindow(state.WinCall)
}

// account is the call's account, nil without a call.
func (h *hub) account() *state.Account {
	if !h.call.on {
		return nil
	}
	return h.bySerial(h.call.serial)
}

// liveHere is whether chat of the account on screen has the live call (the
// chat header's call button).
func (h *hub) liveHere(a *state.Account, chat []byte) bool {
	return h.liveCall() && h.call.serial == a.Serial && bytes.Equal(h.call.u.Chat, chat)
}

// keep copies u into the call on screen.
func (h *hub) keep(u *abi.CallUpdate, now time.Time) {
	c := &h.call
	var err error
	c.rec, err = u.Encode(c.rec[:0])
	if err == nil {
		err = c.u.Decode(c.rec)
	}
	if err != nil { // a record the core sent always encodes again
		c.u.Generation, c.u.Phase = u.Generation, u.Phase
	}
	if u.HasActiveForMS {
		c.started, c.hasStarted = now.Add(-time.Duration(u.ActiveForMS)*time.Millisecond), true
	} else if u.Phase != abi.CallPhaseActive {
		c.hasStarted = false
	}
}

func (h *hub) isEnded(gen uint64) bool {
	for i := 0; i < h.endedN; i++ {
		if h.ended[i] == gen {
			return true
		}
	}
	return false
}

func (h *hub) remember(gen uint64) {
	if h.isEnded(gen) {
		return
	}
	if h.endedN < len(h.ended) {
		h.endedN++
	} else {
		copy(h.ended[:], h.ended[1:])
	}
	h.ended[h.endedN-1] = gen
}

// update applies one call state from account a's core
// (zf:src/app/calls.rs:259). A finished call keeps its screen for the
// farewell, then goes.
func (h *hub) update(a *state.Account, u *abi.CallUpdate) {
	c := &h.call
	now := h.s.Now()
	other := c.on && c.serial != a.Serial
	if other && live(c.u.Phase) {
		// One call at a time: an offer to another account while a call is
		// up is declined there, and its later updates are not this call's.
		if u.Phase == abi.CallPhaseIncoming {
			a.Send(a.Cmd(abi.CommandTagDeclineCall))
		}
		return
	}
	if h.dismissed != 0 && h.dismissed == u.Generation {
		return
	}
	same := c.on && c.u.Generation == u.Generation
	if !same && h.isEnded(u.Generation) {
		return // a late word about a call that is over
	}
	// The end of a call other than the one on screen (one placed here and
	// refused because a call to us came first) is not drawn over it.
	if !other && c.on && !same && live(c.u.Phase) && !live(u.Phase) {
		return
	}
	if u.Phase != abi.CallPhasePlacing {
		c.placed = time.Time{}
	}
	ringing := u.Phase == abi.CallPhaseIncoming && !same
	finished := !live(u.Phase)
	wasLive := c.on && !other && live(c.u.Phase)
	hadFarewell := c.on && !c.until.IsZero()
	h.keep(u, now)
	c.on, c.serial = true, a.Serial
	if finished {
		h.remember(u.Generation)
		if !wasLive && !hadFarewell {
			// Nothing was drawn for this call: nothing to say farewell to.
			h.clear()
			return
		}
		c.until = now.Add(farewell)
		// How a call ended is the reason the screen lingers, but a locked
		// chat still says nothing: its farewell stays in the call bar.
		c.hidden = h.private()
	} else {
		c.until = time.Time{}
		// A call that has just begun takes the screen, unless its chat is
		// locked: then it waits in the call bar as "Locked chat".
		if !wasLive {
			c.hidden = h.private()
		}
	}
	if finished || u.Direction == abi.CallDirectionIncoming {
		c.cancel = 0
	}
	if !c.hidden {
		// A call that rings while ZapFast is not in front (its window closed
		// or hidden) opens its own window, with no main window behind it.
		h.openWindow(ringing && !h.s.Focused)
	}
	h.attention(ringing)
	h.syncTone()
	h.gen++
	h.s.Touch()
	h.s.Wake()
}

// attention asks the Dock for attention while a call to us rings, and stops
// once it no longer does.
func (h *hub) attention(start bool) {
	c := &h.call
	if start && !c.ringing && !h.private() {
		c.ringing = true
		if h.dock != nil {
			h.dock.Ring()
		}
	}
	if c.ringing && (!c.on || c.u.Phase != abi.CallPhaseIncoming) {
		c.ringing = false
		if h.dock != nil {
			h.dock.Stop()
		}
	}
}

// clear takes the call off the screen.
func (h *hub) clear() {
	ringing := h.call.ringing
	h.call.on, h.call.until, h.call.placed, h.call.cancel, h.call.hasStarted = false, time.Time{}, time.Time{}, 0, false
	h.call.ringing = false
	if ringing && h.dock != nil {
		h.dock.Stop()
	}
	h.syncTone()
	h.vid.release()
	h.gen++
	h.s.Touch()
}

// private is whether the call belongs to a chat the lock hides now: a locked
// chat says nothing until its folder is open on the account on screen
// (zf:src/app/calls.rs:88).
func (h *hub) private() bool {
	a := h.account()
	if a == nil {
		return false
	}
	ref := a.Model.Chats.Find(h.call.u.Chat)
	if ref == 0 || !a.Model.Chats.At(ref).Locked() {
		return false
	}
	return !(a.Active() && a.Mode == state.ListLocked)
}

// link is whether the call was joined through a call link.
func (h *hub) link() bool { return h.call.u.HasGroup && h.call.u.Group.Link }

// appendName appends the name the call shows for the other side: the chat's
// title (a group's subject for a group call), "Call link", or "Locked chat"
// while the lock hides it.
func (h *hub) appendName(dst []byte) []byte {
	if h.link() {
		return append(dst, i18n.T("Call link")...)
	}
	if h.private() {
		return append(dst, i18n.T("Locked chat")...)
	}
	a := h.account()
	if a == nil {
		return dst
	}
	return append(dst, chatTitle(a, h.call.u.Chat)...)
}

// caller is who rang a group call, by name: nil for a one-to-one call and
// while the lock hides the chat.
func (h *hub) caller() []byte {
	a := h.account()
	if a == nil || !h.call.u.HasGroup || !h.call.u.Group.HasCaller || h.private() {
		return nil
	}
	return a.Model.DisplayName(h.call.u.Group.Caller)
}

// chatTitle is how the interface names a chat: its name, else its person's.
func chatTitle(a *state.Account, jid []byte) []byte {
	if ref := a.Model.Chats.Find(jid); ref != 0 {
		if n := a.Model.Chats.At(ref).Name(); len(n) > 0 {
			return n
		}
	}
	return a.Model.DisplayName(jid)
}

// secs is how long the call has been up.
func (h *hub) secs(now time.Time) (int64, bool) {
	if !h.call.hasStarted {
		return 0, false
	}
	return int64(now.Sub(h.call.started) / time.Second), true
}

// appendStatus appends the call's line under its name.
func (h *hub) appendStatus(dst []byte, now time.Time) []byte {
	s, ok := h.secs(now)
	return appendStatus(dst, &h.call.u, s, ok, h.caller())
}

// tick is the upkeep of each frame (zf:src/app/calls.rs:540): the
// farewell's end, a placing call that never heard back, a locked chat's call
// put back in the call bar, and the call window closed by its own button.
func (h *hub) tick(now time.Time) {
	c := &h.call
	if c.on && !c.until.IsZero() && !now.Before(c.until) {
		h.clear()
	}
	if c.on && !c.placed.IsZero() && c.u.Phase == abi.CallPhasePlacing && now.Sub(c.placed) >= placingTimeout {
		if a := h.account(); a != nil {
			h.cancelPlacing(a)
			h.notPlaced(a)
		}
	}
	if c.on && !c.hidden && h.private() {
		c.hidden = true
		h.gen++
	}
	if h.waitUnlock && !h.s.Lock.Locked {
		h.waitUnlock = false
		if h.showing() && !h.s.Win.Open[state.WinCall] {
			h.s.OpenWindow(state.WinCall)
		}
	}
	if h.winSeen && !h.s.Win.Open[state.WinCall] && !h.s.Win.Want[state.WinCall] {
		// The window closed by its own button: the call keeps running.
		h.winSeen = false
		h.vid.release()
		if c.on && !c.hidden {
			c.hidden = true
			h.gen++
		}
	}
}

// next is a generation no call has had yet, from the counter a's core
// numbers the calls it is offered with (core.CallNumbers), so the two never
// collide. With no core yet (the send fails anyway) the hub numbers from
// 1<<48.
func (h *hub) next(a *state.Account) uint64 {
	if n, ok := a.Conn.(core.CallNumbers); ok {
		return n.CallGeneration()
	}
	h.ownGen++
	return 1<<48 + h.ownGen
}

// toast says msg in the main window.
func (h *hub) toast(msg string) { h.s.Toast([]byte(msg), true) }

// place places a call to chat from the Calls page or a chat's header: the
// screen shows it as placing at once, and the core's updates move it on
// (zf:src/app/calls.rs:372).
func (h *hub) place(a *state.Account, chat []byte, video bool) {
	if h.liveCall() {
		h.toast(i18n.T("Hang up the current call first"))
		return
	}
	gen := h.next(a)
	cmd := a.Cmd(abi.CommandTagStartCall)
	cmd.StartCall.Chat, cmd.StartCall.Video, cmd.StartCall.Generation, cmd.StartCall.Cancel = chat, video, gen, gen
	err := a.Send(cmd)
	u := &h.scratch
	*u = abi.CallUpdate{Generation: gen, Chat: append(u.Chat[:0], chat...), Direction: abi.CallDirectionOutgoing, Video: video, Phase: abi.CallPhasePlacing}
	h.update(a, u)
	h.call.cancel, h.call.placed = gen, h.s.Now()
	if err != nil {
		h.call.cancel = 0
		h.notPlaced(a)
	}
}

// joinLink joins a call link (call.whatsapp.com/voice/… or /video/…): shown
// as connecting at once, under the link's name (zf:src/app/calls.rs:388).
func (h *hub) joinLink(a *state.Account, token []byte, video bool) {
	if h.liveCall() {
		h.toast(i18n.T("Hang up the current call first"))
		return
	}
	gen := h.next(a)
	cmd := a.Cmd(abi.CommandTagJoinCallLink)
	cmd.JoinCallLink.Token, cmd.JoinCallLink.Video, cmd.JoinCallLink.Generation, cmd.JoinCallLink.Cancel = token, video, gen, gen
	err := a.Send(cmd)
	u := &h.scratch
	*u = abi.CallUpdate{Generation: gen, Chat: u.Chat[:0], Direction: abi.CallDirectionOutgoing, Video: video, Phase: abi.CallPhasePlacing, HasGroup: true}
	u.Group.Link = true
	h.update(a, u)
	h.call.cancel, h.call.placed = gen, h.s.Now()
	if err != nil {
		h.call.cancel = 0
		h.notPlaced(a)
	}
}

// notPlaced fails a call that never went out: "Could not connect", then
// gone.
func (h *hub) notPlaced(a *state.Account) {
	u := &h.scratch
	gen, video, group := h.call.u.Generation, h.call.u.Video, h.call.u.HasGroup && h.call.u.Group.Link
	*u = abi.CallUpdate{Generation: gen, Chat: append(u.Chat[:0], h.call.u.Chat...), Direction: abi.CallDirectionOutgoing, Video: video,
		Phase: abi.CallPhaseFailed, Outcome: abi.CallStatusFailed, HasOutcome: true, HasGroup: group}
	u.Group.Link = group
	h.update(a, u)
}

// cancelPlacing tells the core not to offer a call still being placed.
func (h *hub) cancelPlacing(a *state.Account) {
	if h.call.cancel == 0 {
		return
	}
	cmd := a.Cmd(abi.CommandTagCancelCall)
	cmd.CancelCall.Token = h.call.cancel
	a.Send(cmd)
	h.call.cancel = 0
}

// send sends a payload-less command to the call's account.
func (h *hub) send(tag abi.CommandTag) {
	if a := h.account(); a != nil {
		a.Send(a.Cmd(tag))
	}
}

// answer accepts the ringing call.
func (h *hub) answer() { h.send(abi.CommandTagAnswerCall) }

// decline refuses the ringing call and takes it down at once.
func (h *hub) decline() {
	h.send(abi.CommandTagDeclineCall)
	h.dismiss()
}

// hangup ends the call (a call still being placed is not offered at all) and
// takes it down at once: the reader knows how it ended.
func (h *hub) hangup() {
	h.send(abi.CommandTagHangupCall)
	h.dismiss()
}

// dismiss takes down the call the reader ended here; the core's last words
// about it are not drawn again (zf:src/app/calls.rs:418).
func (h *hub) dismiss() {
	a := h.account()
	if a == nil {
		return
	}
	h.cancelPlacing(a)
	h.dismissed = h.call.u.Generation
	h.remember(h.call.u.Generation)
	h.clear()
}

// setMuted mutes our microphone (the other side is told).
func (h *hub) setMuted(on bool) {
	if a := h.account(); a != nil {
		cmd := a.Cmd(abi.CommandTagSetCallMuted)
		cmd.SetCallMuted.Muted = on
		a.Send(cmd)
	}
}

// setCamera turns our camera on or off; on a voice call it asks to switch to
// video.
func (h *hub) setCamera(on bool) {
	if a := h.account(); a != nil {
		cmd := a.Cmd(abi.CommandTagSetCallCamera)
		cmd.SetCallCamera.On = on
		a.Send(cmd)
	}
}

// setDevice moves the running call to device id of kind (nil: the system
// default, or the first camera), without changing the system's defaults.
func (h *hub) setDevice(kind abi.DeviceKind, id []byte) {
	a := h.account()
	if a == nil {
		return
	}
	var cmd *abi.Command
	switch kind {
	case abi.DeviceKindMicrophone:
		cmd = a.Cmd(abi.CommandTagSetCallMicrophone)
		cmd.SetCallMicrophone.Device, cmd.SetCallMicrophone.HasDevice = id, id != nil
	case abi.DeviceKindSpeaker:
		cmd = a.Cmd(abi.CommandTagSetCallSpeaker)
		cmd.SetCallSpeaker.Device, cmd.SetCallSpeaker.HasDevice = id, id != nil
	default:
		cmd = a.Cmd(abi.CommandTagSetCallCameraDevice)
		cmd.SetCallCameraDevice.Device, cmd.SetCallCameraDevice.HasDevice = id, id != nil
	}
	a.Send(cmd)
}

// refreshDevices asks the call's core to list the devices again.
func (h *hub) refreshDevices() { h.send(abi.CommandTagRefreshCallDevices) }

// leave puts the call screen aside: the call keeps running and the call bar
// offers the way back.
func (h *hub) leave() {
	if h.call.on && !h.call.hidden {
		h.call.hidden = true
		h.gen++
		h.s.Touch()
	}
}

// returnTo brings the call screen back. A locked chat's call cannot lift the
// lock on its own: it asks for the code instead.
func (h *hub) returnTo() {
	if !h.call.on {
		return
	}
	if !h.private() {
		h.call.hidden = false
		h.gen++
		h.openWindow(false)
		return
	}
	if a := h.account(); a != nil && a.Active() {
		h.s.OpenDialog(state.Dialog{Kind: state.DialogUnlockLockedChats})
	}
}
