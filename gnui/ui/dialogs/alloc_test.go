package dialogs

import (
	"testing"

	"gonativeui/widget/widgettest"

	"zapfast/model"
	"zapfast/state"
)

// A steady frame of an open dialog builds without allocating.
func TestSteadyFramesAllocateNothing(t *testing.T) {
	r := newRig(t, baseEvents()...)
	ada := r.ref("1@s.whatsapp.net")
	var ids model.IDList
	ids.Add([]byte("m1"))
	r.openForward(ada, &ids)
	r.Play(widgettest.Settle())
	r.Frames(5)
	if n := testing.AllocsPerRun(50, func() { r.Frame() }); n != 0 {
		t.Errorf("forward: %v allocations per frame", n)
	}
	for _, c := range []struct {
		name string
		d    state.Dialog
	}{
		{"delete chat", state.Dialog{Kind: state.DialogConfirmDeleteChat, Chat: ada}},
		{"about", state.Dialog{Kind: state.DialogAbout}},
		{"shortcuts", state.Dialog{Kind: state.DialogShortcuts}},
		{"new chat", state.Dialog{Kind: state.DialogNewChat}},
		{"new contact", state.Dialog{Kind: state.DialogNewContact}},
		{"poll", state.Dialog{Kind: state.DialogCreatePoll, Chat: ada}},
		{"location", state.Dialog{Kind: state.DialogSendLocation, Chat: ada}},
		{"labels", state.Dialog{Kind: state.DialogLabels}},
		{"unlock", state.Dialog{Kind: state.DialogUnlockLockedChats}},
	} {
		r.open(c.d)
		r.Play(widgettest.Settle())
		r.Frames(5)
		if n := testing.AllocsPerRun(50, func() { r.Frame() }); n != 0 {
			t.Errorf("%s: %v allocations per frame", c.name, n)
		}
	}
}
