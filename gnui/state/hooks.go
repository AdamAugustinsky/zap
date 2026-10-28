package state

import (
	"bytes"
	"reflect"

	"gonativeui/app"

	"zapfast/abi"
)

// hooks are what screens add to the app's moments, in their Init, by
// owner: an owner's second registration (its Init in a window opened again)
// replaces its first.
type hooks struct {
	lock      []hook[func(s *State)]
	inUse     []hook[func(s *State) bool]
	quit      []hook[func(s *State)]
	appEvent  []hook[func(s *State, e *app.Event) bool]
	chatClose []hook[func(s *State, a *Account, chat []byte)]
	unlinked  []hook[func(s *State, a *Account)]
	chatGone  []hook[func(s *State, a *Account, chat []byte)]
	acctGone  []hook[func(s *State, serial uint64)]
}

type hook[F any] struct {
	owner any
	f     F
}

func add[F any](list []hook[F], owner any, f F) []hook[F] {
	if owner != nil && reflect.TypeOf(owner).Comparable() {
		for i := range list {
			if list[i].owner == owner {
				list[i].f = f
				return list
			}
		}
	}
	return append(list, hook[F]{owner, f})
}

// OnLock adds f to what runs when the app lock locks, after the window is
// locked (zf:src/app.rs lock_app), for what only its package can end: the
// composer saves the open draft and discards a recording, the players stop.
// The state itself drops logic's voice run, withdraws the notifications
// (NoticeClear{All: true}, for package sys) and closes the viewer and
// Settings windows. Register in Init with the screen as owner.
func (s *State) OnLock(owner any, f func(s *State)) { s.hooks.lock = add(s.hooks.lock, owner, f) }

// OnInUse adds f to what says ZapFast is being used without input (a voice
// message being recorded): the lock's inactivity count restarts while any
// says so.
func (s *State) OnInUse(owner any, f func(s *State) bool) {
	s.hooks.inUse = add(s.hooks.inUse, owner, f)
}

// Locked runs what locking ends: the state's part, then the OnLock hooks in
// the order they were added. The app lock calls it (ui/lock's OnLock).
func (s *State) Locked() {
	s.Focused = false
	s.Voice = Voice{}
	s.Notices = s.Notices[:0]
	s.NoticeClears = append(s.NoticeClears[:0], NoticeClear{All: true})
	s.Attention = false
	s.Panes.ReactTarget.Reset()
	s.Panes.Picker = PickerNone
	for _, w := range [...]Window{WinViewer, WinSettings} {
		if s.Win.Open[w] || s.Win.Want[w] {
			s.CloseWindow(w)
		}
	}
	for _, h := range s.hooks.lock {
		h.f(s)
	}
	s.gen++
}

// InUse reports whether a hook says ZapFast is in use without input.
func (s *State) InUse() bool {
	for _, h := range s.hooks.inUse {
		if h.f(s) {
			return true
		}
	}
	return false
}

// OnQuit adds f to what runs as the app ends (app.EvWillTerminate), before
// the settings are written and the cores close: the composer saves the open
// chat's draft (upstream saves it at quit). It runs on the main thread, in
// the order added; keep it quick and synchronous.
func (s *State) OnQuit(owner any, f func(s *State)) { s.hooks.quit = add(s.hooks.quit, owner, f) }

// Quit runs the OnQuit hooks, then writes the settings at once and stops
// saving them. The app calls it at EvWillTerminate (on macOS the process ends
// when that event returns, so nothing deferred in main runs); the cores close
// after it.
func (s *State) Quit() {
	if s.quitting {
		return
	}
	s.quitting = true
	for _, h := range s.hooks.quit {
		h.f(s)
	}
	s.CloseSettings()
}

// OnChatClose adds f to what runs whenever an account's open chat stops
// being the open one, by any route: another chat opens, the chat closes
// (Escape, archiving, ⌘W of the chat, the history's Back), it is removed or
// locked away, or the account leaves the screen (its chat stays its open one,
// parked, and opens again when it comes back). f runs before the change, with
// the account and the chat's JID (valid during the call): the composer
// flushes that chat's draft. It does not run at quit (OnQuit) or for a
// removed account.
func (s *State) OnChatClose(owner any, f func(s *State, a *Account, chat []byte)) {
	s.hooks.chatClose = add(s.hooks.chatClose, owner, f)
}

// OnChatGone adds f to what runs when an account's open chat closes
// because the chat is gone: removed, missing from a reload of the list (the
// core empties it as it logs out, zf:src/backend/worker.rs:3763), or with
// the session. f runs first, with the chat's JID (valid during the call),
// and the OnChatClose hooks after it: what belonged to the chat goes, never
// stored (upstream clears open_chat without saving, zf:src/app.rs:3233,
// :2299). The composer drops the chat's text, quote and attachments here.
func (s *State) OnChatGone(owner any, f func(s *State, a *Account, chat []byte)) {
	s.hooks.chatGone = add(s.hooks.chatGone, owner, f)
}

// OnAccountGone adds f to what runs when an account's Serial stops naming
// it: the account was removed, or its number unlinked (it has a new
// Serial). f drops what its screen keeps by that Serial. Register in Init
// with the screen as owner.
func (s *State) OnAccountGone(owner any, f func(s *State, serial uint64)) {
	s.hooks.acctGone = add(s.hooks.acctGone, owner, f)
}

func (s *State) accountGone(serial uint64) {
	for _, h := range s.hooks.acctGone {
		h.f(s, serial)
	}
}

// AcctRef is an account held past a frame, by its Serial: work that
// finishes later (a worker's blob, a recording) is for it only while it is
// the same session.
type AcctRef struct {
	A      *Account
	Serial uint64
}

// Ref is a's AcctRef now; a nil account's holds none.
func (a *Account) Ref() AcctRef {
	if a == nil {
		return AcctRef{}
	}
	return AcctRef{a, a.Serial}
}

// Valid reports whether the account is still the one held: not removed,
// nor unlinked since.
func (r AcctRef) Valid() bool { return r.A != nil && r.A.Serial == r.Serial && !r.A.Removed }

// chatGoing runs the OnChatGone hooks for a's open chat, which is about to
// close because it is gone, and drops the refused text waiting for it: none
// of it may reach the archive through the close.
func (s *State) chatGoing(a *Account) {
	if a.Open == 0 || len(a.openID) == 0 {
		return
	}
	for _, h := range s.hooks.chatGone {
		h.f(s, a, a.openID)
	}
	if r := &s.Req.Restore; len(r.Chat) > 0 && r.Acct == a.Serial && bytes.Equal(r.Chat, a.openID) {
		r.Chat, r.Text, r.Acct = r.Chat[:0], r.Text[:0], 0
	}
}

// chatClosing runs the OnChatClose hooks for a's open chat.
func (s *State) chatClosing(a *Account) {
	if a.Open == 0 || len(a.openID) == 0 {
		return
	}
	for _, h := range s.hooks.chatClose {
		h.f(s, a, a.openID)
	}
	// A refused send the composer did not take goes to the chat's draft:
	// that account's chat, not another account's of the same JID.
	if r := &s.Req.Restore; len(r.Chat) > 0 && r.Acct == a.Serial && bytes.Equal(r.Chat, a.openID) {
		if len(bytes.TrimSpace(a.Model.Draft(r.Chat))) == 0 {
			a.Model.SetDraft(r.Chat, r.Text)
			cmd := a.Cmd(abi.CommandTagSaveDraft)
			cmd.SaveDraft.Chat, cmd.SaveDraft.Text = r.Chat, r.Text
			a.Send(cmd)
		}
		r.Chat, r.Text = r.Chat[:0], r.Text[:0]
	}
}

// OnAppEvent adds f to what sees the app's events (app.Event: a permission's
// answer, perm.ResultOf; a player's or recorder's end, media.EventOf; a
// video's end; the app activating), in the order added, after package sys
// and before the shell. f returns true when the event was its own, which
// ends the walk. It runs on the main thread, maybe with no window open:
// change state and call s.Wake to be drawn; build nothing. Register in Init
// with the screen as owner (a package with no screen: any comparable
// value of its own).
func (s *State) OnAppEvent(owner any, f func(s *State, e *app.Event) bool) {
	s.hooks.appEvent = add(s.hooks.appEvent, owner, f)
}

// AppEvent runs the OnAppEvent hooks for e until one takes it, and reports
// whether one did. The app's handler calls it.
func (s *State) AppEvent(e *app.Event) bool {
	for _, h := range s.hooks.appEvent {
		if h.f(s, e) {
			return true
		}
	}
	return false
}
