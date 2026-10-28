package main

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// DL-017: with a dialog open, the chat's shortcuts do nothing beneath it:
// ⌘F opens no search in the chat, ⌘N does not replace the dialog, and ↑
// edits no message.
func TestChatShortcutsDoNothingBeneathADialog(t *testing.T) {
	f := fake.New(nil)
	linked(f)
	r := newRig(t, f)
	r.Frames(3)
	r.open(adaJID)
	var mine abi.Message
	mine.ID, mine.Chat, mine.FromMe, mine.Status = []byte("m1"), []byte(adaJID), true, abi.DeliveryRead
	mine.Sender, mine.Timestamp = []byte("9@s.whatsapp.net"), r.s.Now().Unix()-30
	mine.Content.Tag = abi.ContentTagText
	mine.Content.Text.Text = []byte("editable")
	f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(adaJID), Batch: 1, Last: true, Complete: true, Messages: []abi.Message{mine}}
	}))
	r.Frames(4)
	r.Play(widgettest.Click(ui.RoleTextField, "Message"), widgettest.Idle(2)) // the composer holds the focus
	r.s.OpenDialog(state.Dialog{Kind: state.DialogAbout})
	r.Frames(4)
	for _, k := range []struct {
		code ui.KeyCode
		mods ui.Mods
	}{{'F', prim}, {'N', prim}, {ui.KeyUp, 0}} {
		r.Play(widgettest.Key(k.code, k.mods), widgettest.Idle(2))
		if r.s.Dialog.Kind != state.DialogAbout || r.s.Panes.Pane == state.PaneSearch || r.s.Panes.Edit.Len() != 0 {
			t.Fatalf("after key %v: dialog %v, pane %v, editing %d", k.code, r.s.Dialog.Kind, r.s.Panes.Pane, r.s.Panes.Edit.Len())
		}
	}
	r.s.CloseDialog()
	r.Frames(3)
	if r.s.Panes.Pane == state.PaneSearch || r.s.Req.FindInChat {
		t.Errorf("⌘F waited under the dialog and opened the search once it closed: pane %v, request %v", r.s.Panes.Pane, r.s.Req.FindInChat)
	}
	// With the dialog gone the same keys work.
	r.Play(widgettest.Key('F', prim), widgettest.Idle(2))
	if r.s.Panes.Pane != state.PaneSearch {
		t.Error("⌘F does not search the chat without a dialog")
	}
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	r.Play(widgettest.Click(ui.RoleTextField, "Message"), widgettest.Idle(2))
	r.Play(widgettest.Key(ui.KeyUp, 0), widgettest.Idle(2))
	if r.s.Panes.Edit.Len() != 1 {
		t.Error("↑ does not edit the last message without a dialog")
	}
}
