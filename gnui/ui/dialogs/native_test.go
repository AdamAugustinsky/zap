package dialogs

import (
	"errors"
	"slices"
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

// The questions are the system's alerts where there is one. These tests give
// the window widgettest's FakeDialogs: no real alert is ever shown.

type question struct {
	name  string
	d     state.Dialog
	title string
	yes   string // the button that confirms
	sends abi.CommandTag
}

func questions(r *rig) []question {
	ada, group := r.ref("1@s.whatsapp.net"), r.ref("12345@g.us")
	var m1 = oneID("m1")
	return []question{
		{"unlink", state.Dialog{Kind: state.DialogConfirmUnlink}, "Unlink this computer?", "Unlink", abi.CommandTagUnlink},
		{"remove account", state.Dialog{Kind: state.DialogConfirmRemoveAccount}, "Remove this account?", "Remove", abi.CommandTagUnlink},
		{"clear media", state.Dialog{Kind: state.DialogConfirmClearMediaCache}, "Clear the media cache?", "Clear", abi.CommandTagClearMediaCache},
		{"delete chat", state.Dialog{Kind: state.DialogConfirmDeleteChat, Chat: ada}, "Delete chat?", "Delete", abi.CommandTagDeleteChat},
		{"clear chat", state.Dialog{Kind: state.DialogConfirmClearChat, Chat: ada}, "Clear chat?", "Clear chat", abi.CommandTagClearChat},
		{"leave group", state.Dialog{Kind: state.DialogConfirmLeaveGroup, Chat: group}, "Leave this group?", "Leave group", abi.CommandTagLeaveGroup},
		{"start over", state.Dialog{Kind: state.DialogConfirmStartOver}, "Start over?", "Start over", abi.CommandTagStartOverArchive},
		{"remove pack", state.Dialog{Kind: state.DialogConfirmRemoveStickerPack, Text: []byte("Ducks"), Path: []byte("/p/ducks")}, "Remove sticker pack?", "Remove", abi.CommandTagDeleteStickerPack},
		{"delete for me", state.Dialog{Kind: state.DialogConfirmDeleteMessage, Chat: ada, IDs: *m1}, "Delete for me?", "Delete", abi.CommandTagDeleteLocal},
		{"delete for everyone", state.Dialog{Kind: state.DialogConfirmDeleteMessage, Chat: ada, IDs: *m1, ForEveryone: true}, "Delete for everyone?", "Delete", abi.CommandTagRevoke},
		{"delete selection", state.Dialog{Kind: state.DialogConfirmDeleteSelection, Chat: ada, IDs: *m1}, "Delete for me?", "Delete", abi.CommandTagDeleteLocal},
	}
}

func nativeRig(t *testing.T) (*rig, *widgettest.FakeDialogs) {
	r := newRig(t, baseEvents()...)
	r.a.OpenChat(r.ref("1@s.whatsapp.net"))
	r.Frames(2)
	return r, r.FakeDialogs()
}

func (r *rig) drawnDialog() bool {
	_, ok := r.Find(ui.RoleDialog, "")
	return ok || r.has(ui.RoleButton, "Cancel")
}

// Each question is one alert with the question as its message, Cancel as the
// default button (Return never destroys anything) and the verb marked
// destructive; the answer carries it out and closes the question.
func TestQuestionsAreNativeAlerts(t *testing.T) {
	probe, _ := nativeRig(t)
	for _, q := range questions(probe) {
		t.Run(q.name, func(t *testing.T) {
			r, fd := nativeRig(t)
			r.open(q.d)
			calls := fd.Open()
			if len(calls) != 1 || calls[0].Kind != platform.DialogAlert {
				t.Fatalf("alerts %+v\n%s", fd.Calls, widgettest.Dump(r.Tree()))
			}
			al := calls[0].Alert
			if al.Message != q.title || al.Info == "" || !slices.Equal(al.Buttons[:2], []string{"Cancel", q.yes}) || !slices.Contains(al.Destructive, 1) {
				t.Errorf("alert %+v", al)
			}
			if r.drawnDialog() {
				t.Errorf("drawn as well\n%s", widgettest.Dump(r.Tree()))
			}
			base := len(r.sent(q.sends))
			// Cancel and a dismissed alert send nothing and end the question.
			if err := fd.AnswerButton(0, false); err != nil {
				t.Fatal(err)
			}
			r.Frames(3)
			if r.s.Dialog.Open() || len(r.sent(q.sends)) != base {
				t.Fatalf("Cancel: open %v sent %v", r.s.Dialog.Open(), r.f.SentTags())
			}
			r.open(q.d)
			if err := fd.Dismiss(); err != nil {
				t.Fatal(err)
			}
			r.Frames(3)
			if r.s.Dialog.Open() || len(r.sent(q.sends)) != base {
				t.Fatalf("Escape: open %v sent %v", r.s.Dialog.Open(), r.f.SentTags())
			}
			r.open(q.d)
			if err := fd.AnswerButton(1, false); err != nil {
				t.Fatal(err)
			}
			r.Frames(3)
			if len(r.sent(q.sends)) <= base || (r.s.Dialog.Open() && r.s.Dialog.Kind == q.d.Kind) {
				t.Fatalf("%s: open %v sent %v", q.yes, r.s.Dialog.Open(), r.f.SentTags())
			}
		})
	}
}

// Leaving a group offers the archive in the same alert.
func TestLeaveAlertOffersArchive(t *testing.T) {
	r, fd := nativeRig(t)
	r.open(state.Dialog{Kind: state.DialogConfirmLeaveGroup, Chat: r.ref("12345@g.us")})
	al := fd.Open()[0].Alert
	if !slices.Equal(al.Buttons, []string{"Cancel", "Leave group", "Leave group and archive"}) {
		t.Fatalf("buttons %q", al.Buttons)
	}
	if err := fd.AnswerButton(2, false); err != nil {
		t.Fatal(err)
	}
	r.Frames(3)
	if got := r.sent(abi.CommandTagLeaveGroup); len(got) != 1 || !got[0].LeaveGroup.Archive {
		t.Fatalf("sent %v", r.f.SentTags())
	}
}

// Locking is not destructive: its button is the default, and without a local
// code the answer goes on to the set-up form.
func TestLockAlertAndOnToTheCode(t *testing.T) {
	r, fd := nativeRig(t)
	r.open(state.Dialog{Kind: state.DialogConfirmLockChat, Chat: r.ref("1@s.whatsapp.net")})
	al := fd.Open()[0].Alert
	if al.Buttons[0] != "Lock chat" || al.Buttons[1] != "Cancel" || len(al.Destructive) != 0 {
		t.Fatalf("alert %+v", al)
	}
	if err := fd.AnswerButton(0, false); err != nil {
		t.Fatal(err)
	}
	r.Frames(4)
	if len(r.sent(abi.CommandTagSetLocked)) != 1 || r.s.Dialog.Kind != state.DialogUnlockLockedChats {
		t.Fatalf("sent %v, dialog %v", r.f.SentTags(), r.s.Dialog.Kind)
	}
}

// Where the system shows no alert, or is busy with another sheet, the question
// is drawn, and answers as before.
func TestQuestionIsDrawnWhenThereIsNoAlert(t *testing.T) {
	for _, err := range []error{errors.ErrUnsupported, platform.ErrDialogBusy} {
		r, fd := nativeRig(t)
		fd.Err = err
		r.open(state.Dialog{Kind: state.DialogConfirmDeleteChat, Chat: r.ref("1@s.whatsapp.net")})
		if len(fd.Calls) != 0 || !r.has(ui.RoleButton, "Delete") {
			t.Fatalf("%v: calls %+v\n%s", err, fd.Calls, widgettest.Dump(r.Tree()))
		}
		r.click(ui.RoleButton, "Delete")
		if len(r.sent(abi.CommandTagDeleteChat)) != 1 {
			t.Errorf("%v: sent %v", err, r.f.SentTags())
		}
	}
}

// A question that goes away (its chat is deleted, another dialog replaces it)
// takes its alert with it.
func TestAlertIsWithdrawnWithItsQuestion(t *testing.T) {
	r, fd := nativeRig(t)
	r.open(state.Dialog{Kind: state.DialogConfirmDeleteChat, Chat: r.ref("1@s.whatsapp.net")})
	r.s.CloseDialog()
	r.Frames(3)
	if len(fd.Withdrawn) != 1 || len(fd.Open()) != 0 {
		t.Fatalf("withdrawn %v open %v", fd.Withdrawn, fd.Open())
	}
	r.open(state.Dialog{Kind: state.DialogConfirmClearChat, Chat: r.ref("1@s.whatsapp.net")})
	r.open(state.Dialog{Kind: state.DialogAbout})
	if len(fd.Withdrawn) != 2 {
		t.Fatalf("withdrawn %v", fd.Withdrawn)
	}
}

// Another owner's answer (a file panel) is not taken for the question's.
func TestAnotherPanelsAnswerIsLeftAlone(t *testing.T) {
	r, fd := nativeRig(t)
	tk, err := r.Offscreen().App.OpenPanel(platform.OpenSpec{Files: true})
	if err != nil {
		t.Fatal(err)
	}
	r.Frames(2)
	r.open(state.Dialog{Kind: state.DialogConfirmDeleteChat, Chat: r.ref("1@s.whatsapp.net")})
	if err := fd.AnswerPaths("/tmp/x"); err != nil {
		t.Fatal(err)
	}
	r.Frames(3)
	if !r.s.Dialog.Open() || len(r.sent(abi.CommandTagDeleteChat)) != 0 {
		t.Fatal("a panel's answer ended the question")
	}
	if res, ok := r.Offscreen().App.TakeDialogFor(tk); !ok || len(res.Paths) != 1 {
		t.Errorf("the panel's answer was taken: %+v %v", res, ok)
	}
}

// LO-005: a spot that was refused comes back in the field.
func TestLocationComesBackWithItsSpot(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogSendLocation, Chat: r.ref("1@s.whatsapp.net"), Text: []byte("40.71280, -74.00600")})
	if got := r.value("Paste a map link or coordinates"); got != "40.71280, -74.00600" {
		t.Fatalf("field %q", got)
	}
	if !r.has(ui.RoleButton, "Send") {
		t.Error("the spot is not offered for sending")
	}
}

// Waiting for the answer allocates nothing.
func TestWaitingForAnAlertAllocatesNothing(t *testing.T) {
	r, fd := nativeRig(t)
	r.open(state.Dialog{Kind: state.DialogConfirmDeleteChat, Chat: r.ref("1@s.whatsapp.net")})
	r.Play(widgettest.Settle())
	r.Frames(5)
	if len(fd.Open()) != 1 {
		t.Fatal("no alert")
	}
	if n := testing.AllocsPerRun(50, func() { r.Frame() }); n != 0 {
		t.Errorf("%v allocations per frame", n)
	}
}
