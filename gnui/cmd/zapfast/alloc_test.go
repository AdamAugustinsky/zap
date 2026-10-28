package main

import (
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
)

// A steady frame of the main window, with every screen wired and a chat
// open, allocates nothing.
func TestSteadyFramesAllocateNothing(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	r.Frames(4)
	if n := testing.AllocsPerRun(20, func() { r.Frame() }); n != 0 {
		t.Errorf("chat list: %v allocations per frame", n)
	}
	r.Play(widgettest.Click(ui.RoleListItem, "Grace Hopper"), widgettest.Idle(4))
	r.Frames(8)
	for i := 0; i < 30; i++ {
		r.Frames(2)
		time.Sleep(5 * time.Millisecond)
	}
	if n := testing.AllocsPerRun(20, func() { r.Frame() }); n != 0 {
		t.Errorf("open chat: %v allocations per frame", n)
	}
}

// The history banner, with its percentage showing, allocates nothing per
// frame (100 is the number a string conversion would allocate for).
func TestTheHistoryBannerAllocatesNothing(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	r.open(adaJID)
	f.Emit(ev(abi.EventTagSyncing, func(e *abi.Event) { e.Syncing.Syncing = true }),
		ev(abi.EventTagSyncProgress, func(e *abi.Event) { e.SyncProgress.Percent = 100 }))
	r.Frames(100) // past the banner's grace
	if _, ok := r.Find(ui.RoleGroup, "Info: Loading chat history… 100%"); !ok {
		t.Fatalf("no sync banner:\n%s", widgettest.Dump(r.Tree()))
	}
	if n := testing.AllocsPerRun(20, func() { r.Frame() }); n != 0 {
		t.Errorf("history banner: %v allocations per frame", n)
	}
}
