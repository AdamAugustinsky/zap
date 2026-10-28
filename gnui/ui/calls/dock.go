package calls

import "zapfast/state"

// Dock asks for the person's attention while a call to us rings (parity
// CA-029): the hub calls Ring once when the call starts ringing and Stop once
// it no longer does (answered, declined, given up or gone). Package sys
// provides it (sys.Hooks.CallDock).
type Dock interface {
	Ring()
	Stop()
}

// SetDock gives the calls of s the Dock to ask for attention through.
func SetDock(s *state.State, d Dock) { hubOf(s).dock = d }
