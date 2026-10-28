package msginfo_test

import "testing"

// Steady frames allocate nothing: a direct message's times, and a group
// message with its receipts in sections.
func TestSteadyFramesAllocateNothing(t *testing.T) {
	r := newRig(t)
	r.message(ada, "m1", "hi", nil)
	r.message(team, "g1", "Standup", nil)
	check := func(what string) {
		t.Helper()
		r.Frames(10)
		if n := testing.AllocsPerRun(30, r.Frame); n != 0 {
			t.Errorf("%s: %v allocations per frame", what, n)
		}
	}
	r.open(ada, "m1")
	check("direct")
	r.open(team, "g1")
	r.receipts(team, "g1",
		rcpt(ada, true, at+10, at+20, -1),
		rcpt(grace, true, at+15, at+400, at+500),
		rcpt(linus, true, at+30, -1, -1),
		rcpt("15557770000@s.whatsapp.net", true, -1, -1, -1),
	)
	check("group receipts")
}
