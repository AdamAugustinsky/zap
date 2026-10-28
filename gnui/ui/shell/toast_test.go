package shell

import (
	"strings"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/i18n"
	"zapfast/state"
)

func TestToastCopyIconCopiesTheWholeError(t *testing.T) {
	i18n.SetLocale(i18n.English)
	s := state.New()
	sh, err := New(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := widgettest.Open(t, Config(s), 1100, 720, 1, sh)
	var copied string
	r.Context().ClipboardSet = func(b []byte) { copied = string(b) }
	const message = "Could not open the attachment: the file is no longer where it was saved"
	s.Toast([]byte(message), true)
	r.Frames(4)
	n, ok := r.Find(ui.RoleButton, "Copy this message")
	if !ok {
		t.Fatalf("no accessible copy button:\n%s", widgettest.Dump(r.Tree()))
	}
	if n.Bounds[2]-n.Bounds[0] > 1.5*(n.Bounds[3]-n.Bounds[1]) {
		t.Error("copy action is a text button, not a compact icon")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Copy this message"), widgettest.Idle(1))
	if copied != message {
		t.Errorf("copied %q, want the whole error %q", copied, message)
	}
	if len(s.ToastsInto(nil)) != 1 {
		t.Error("copy dismissed the error")
	}
}

// The lock screen shows no toasts, nor their accessibility nodes
// (zf:src/ui/mod.rs:40-48): not one shown before locking, nor one that
// arrives while locked. As upstream, they are kept behind the lock: an
// error waits for the reader after the unlock, an info toast whose lifetime
// passed meanwhile is gone.
func TestTheLockScreenShowsNoToasts(t *testing.T) {
	i18n.SetLocale(i18n.English)
	s := state.New()
	now := time.Unix(1_800_000_000, 0)
	s.Now = func() time.Time { return now }
	sh, err := New(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := widgettest.Open(t, Config(s), 1100, 720, 1, sh)
	shows := func(text string) bool { return strings.Contains(widgettest.Dump(r.Tree()), text) }
	s.Toast([]byte("Saved quarterly-report.pdf"), false)
	s.Toast([]byte("Could not send to Ada"), true)
	r.Frames(3)
	if !shows("Saved quarterly-report.pdf") || !shows("Could not send to Ada") {
		t.Fatalf("the toasts do not show unlocked:\n%s", widgettest.Dump(r.Tree()))
	}
	s.Lock.Locked = true
	s.Touch()
	r.Frames(3)
	s.Toast([]byte("Added Grace Hopper to contacts"), false)
	r.Frames(3)
	for _, text := range []string{"Saved quarterly-report.pdf", "Could not send to Ada", "Added Grace Hopper to contacts", "Copy this message"} {
		if shows(text) {
			t.Errorf("%q shows over the lock screen", text)
		}
	}
	now = now.Add(4 * time.Second)
	r.Frames(2)
	s.Lock.Locked = false
	s.Touch()
	r.Frames(3)
	if !shows("Could not send to Ada") {
		t.Error("the error did not wait behind the lock")
	}
	if shows("Saved quarterly-report.pdf") || shows("Added Grace Hopper to contacts") {
		t.Error("an info toast outlived its time behind the lock")
	}
}
