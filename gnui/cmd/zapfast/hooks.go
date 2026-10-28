package main

import (
	"zapfast/state"
	"zapfast/ui/conversation/av"
	"zapfast/ui/lock"
	"zapfast/ui/lock/applock"
	"zapfast/ui/screen"
)

// wireLock gives the app lock the state's hooks: locking runs what the
// screens registered (State.OnLock), a recording keeps ZapFast in use, the
// settings save after a password change, and an unlock replays what was
// asked for while locked.
func wireLock(ls *lock.Screen) {
	ls.OnLock = (*state.State).Locked
	ls.InUse = (*state.State).InUse
	ls.Save = (*state.State).SettingsChanged
	ls.OnUnlock = replay
}

// replay carries out the requests the lock held, in the order they came, so
// the last one is what is on screen (zf:src/app.rs replay_deferred): a
// clicked notification's message in its account (a call's switches account
// only), a link's chat with its text, a group invite.
func replay(s *state.State, held []applock.Deferred) {
	for i := range held {
		d := &held[i]
		switch d.Kind {
		case applock.DeferMessage:
			var msg []byte
			if d.HasMessage {
				msg = []byte(d.Message)
			}
			s.OpenMessageIn(d.Account, []byte(d.Chat), msg)
		case applock.DeferChat:
			a := s.Acct()
			if a == nil {
				continue
			}
			var text []byte
			if d.HasText {
				text = []byte(d.Text)
			}
			jid := []byte(d.Chat)
			a.StartChat(jid, a.Model.DisplayName(jid), text)
		case applock.DeferInvite:
			s.OpenDialog(state.Dialog{Kind: state.DialogJoinGroup, Text: []byte(d.Code)})
		}
		s.RaiseMain()
	}
}

// avTicker runs the voice and video playback every main-window frame (a
// note that ended, the next of a run, the account switched, with no voice
// block on screen) and hands it the players' events.
type avTicker struct{ hooked *state.State }

func (t *avTicker) Frame(e *screen.Env) {
	if t.hooked != e.S {
		t.hooked = e.S
		e.S.OnAppEvent(t, av.Event)
	}
	av.Frame(e)
}
