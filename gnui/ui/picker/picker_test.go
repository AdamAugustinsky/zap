package picker

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/state"
)

func TestOpensWithTheThreeTabs(t *testing.T) {
	r := newRig(t)
	if _, ok := r.Find(ui.RoleDialog, "Emoji picker"); ok {
		t.Fatal("the picker shows while closed")
	}
	r.open(state.PickerEmoji)
	if _, ok := r.Find(ui.RoleDialog, "Emoji picker"); !ok {
		t.Fatalf("the picker did not open:\n%s", widgettest.Dump(r.Tree()))
	}
	for _, name := range []string{"Emoji", "GIF", "Stickers"} {
		if _, ok := r.Find(ui.RoleTab, name); !ok {
			t.Errorf("no %q tab", name)
		}
	}
	if _, ok := r.Find(ui.RoleSearchField, "Search emoji"); !ok {
		t.Error("no search field")
	}
}
