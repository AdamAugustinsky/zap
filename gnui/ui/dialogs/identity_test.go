package dialogs

import (
	"strings"
	"testing"

	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// With accounts one, two and three, removing one moves three to two's
// index; the label manager on three shows three's labels, never two's, and
// the removed account's labels go.
func TestRemovingAnEarlierAccountKeepsLabelsWithTheirAccount(t *testing.T) {
	r := newRig(t, baseEvents()...)
	add := func(id string) (*state.Account, *fake.Core) {
		f := fake.New(nil)
		f.Emit(baseEvents()...)
		return r.s.AddAccount(id, nil, f), f
	}
	labels := func(f *fake.Core, name string) {
		f.Emit(ev(abi.EventTagLabels, func(e *abi.Event) {
			e.Labels = abi.EventLabels{Batch: 1, Last: true, Labels: []abi.Label{{ID: []byte("l-" + name), Name: []byte(name), ColorHex: []byte(DefaultColor)}}}
		}))
	}
	labels(r.f, "Account one's label")
	two, f2 := add("2")
	three, _ := add("3")
	labels(f2, "Account two's label")
	r.Frames(3)
	r.s.RemoveAccount(0)
	r.f.Emit(ev(abi.EventTagAccountRemoved, func(e *abi.Event) {}))
	r.Frames(3)
	if len(r.s.Accounts) != 2 || three.Index != 1 || two.Index != 0 {
		t.Fatalf("account one was not removed: %d accounts", len(r.s.Accounts))
	}
	r.s.SwitchAccount(three.Index)
	r.Frames(2)
	r.open(state.Dialog{Kind: state.DialogLabels})
	if d := widgettest.Dump(r.Tree()); strings.Contains(d, "Account two's label") || strings.Contains(d, "Account one's label") {
		t.Errorf("account three's label manager shows another account's labels\n%s", d)
	}
	for serial := range r.h.scr.lab.sets {
		if serial != two.Serial && serial != three.Serial {
			t.Errorf("a removed account's labels are kept (serial %d)", serial)
		}
	}
}
