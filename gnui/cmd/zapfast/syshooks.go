package main

import (
	"zapfast/state"
	"zapfast/sys"
	"zapfast/ui/calls"
	"zapfast/ui/lock"
	"zapfast/ui/lock/applock"
)

// newSys makes the system services with what only other packages know: the
// app lock (sleep, the status item's Lock, a click that waits for the
// unlock), the settings' save, and the calls' Dock and call links.
func newSys(ls *lock.Screen) *sys.System {
	y := sys.New()
	y.Hooks = sys.Hooks{
		Sleep:    ls.Sleep,
		LockNow:  ls.LockNow,
		Save:     (*state.State).SettingsChanged,
		CallDock: func(s *state.State, d sys.Dock) { calls.SetDock(s, d) },
		JoinCall: calls.JoinLink,
		DeferOpen: func(s *state.State, account, chat string, message []byte) bool {
			return ls.Defer(s, applock.Deferred{
				Kind: applock.DeferMessage, Account: account, Chat: chat,
				Message: string(message), HasMessage: len(message) > 0,
			})
		},
	}
	return y
}
