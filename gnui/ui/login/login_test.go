package login

import (
	"testing"

	"gonativeui/qr"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

type rig struct {
	*widgettest.Rig
	s  *state.State
	sc *Screen
	f  *fake.Core
	n  *fake.Core // a second account's core, when there is one
	cb []byte     // the clipboard
}

// newRig is the app over one unlinked fake account with only the login
// screen in its slot.
func newRig(t *testing.T) *rig { return newRigAt(t, 0, 0) }

// newRigAt is newRig with a window of w × h points (0: the default).
func newRigAt(t *testing.T, w, h int) *rig {
	t.Helper()
	r := &rig{s: state.New(), sc: New(), f: fake.New(nil)}
	r.s.AddAccount("1", nil, r.f)
	sh, err := shell.New(r.s, &screen.Set{Login: r.sc})
	if err != nil {
		t.Fatal(err)
	}
	sh.Traffic = true
	cfg := shell.Config(r.s)
	if w > 0 {
		cfg.Width, cfg.Height = float32(w), float32(h)
	}
	r.Rig = widgettest.Open(t, cfg, int(cfg.Width), int(cfg.Height), 1, sh)
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Context().ClipboardSet = func(b []byte) { r.cb = append(r.cb[:0], b...) }
	r.Frames(4)
	return r
}

func status(set func(u *abi.LinkStatus)) *abi.Event {
	e := new(abi.Event)
	e.Tag = abi.EventTagLink
	set(&e.Link.Status)
	return e
}

func unlinked(qrPayload string) *abi.Event {
	return status(func(s *abi.LinkStatus) {
		s.Tag = abi.LinkStatusTagUnlinked
		if qrPayload != "" {
			s.Unlinked.QR, s.Unlinked.HasQR = []byte(qrPayload), true
		}
	})
}

func (r *rig) has(role ui.Role, name string) bool { _, ok := r.Find(role, name); return ok }

func (r *rig) emit(t *testing.T, ev ...*abi.Event) {
	t.Helper()
	if err := r.f.Emit(ev...); err != nil {
		t.Fatal(err)
	}
	r.Frames(3)
}

func (r *rig) sent(tag abi.CommandTag) []abi.Command {
	var out []abi.Command
	for _, c := range r.f.Sent() {
		if c.Tag == tag {
			out = append(out, c)
		}
	}
	return out
}

// LG-001, LG-008, LG-015: an unlinked account shows the QR code and the three
// steps, with the unofficial-client warning.
func TestAnUnlinkedAccountShowsTheQRCodeAndTheSteps(t *testing.T) {
	r := newRig(t)
	if !r.has(ui.RoleLabel, "Connecting to WhatsApp…") {
		t.Errorf("before any status:\n%s", widgettest.Dump(r.Tree()))
	}
	r.emit(t, unlinked("2@abcdef,ghij=="))
	for _, name := range []string{
		"Link this computer", "Open WhatsApp on your phone", "Tap Menu or Settings, then Linked devices",
		"Tap Link a device and point the phone at this code",
		"Unofficial client. Using it may be against WhatsApp's terms of service.",
	} {
		if !r.has(ui.RoleLabel, name) {
			t.Errorf("no %q:\n%s", name, widgettest.Dump(r.Tree()))
		}
	}
	if !r.has(ui.RoleLink, "Link with phone number instead") {
		t.Error("no way to link with a phone number")
	}
	img := r.Must(ui.RoleImage, "Link this computer")
	if w, h := img.Bounds[2]-img.Bounds[0], img.Bounds[3]-img.Bounds[1]; w < 200 || w != h {
		t.Errorf("the QR code is %v × %v", w, h)
	}
	var want qr.Code
	qr.Encode(&want, []byte("2@abcdef,ghij=="), qr.LevelM)
	if !sameCode(&r.sc.code, &want) {
		t.Error("the code drawn is not the core's payload")
	}
	// Only the login screen: no rail, no chats.
	if r.has(ui.RoleButton, "Chats") {
		t.Error("the chats' rail shows while unlinked")
	}
	// A strip across the top moves the window (LG-015).
	drags := r.Context().DragRegions(nil)
	top := false
	for _, d := range drags {
		if !d.Block && d.Rect[1] <= 0 && d.Rect[3] > 20 && d.Rect[2]-d.Rect[0] >= 700 {
			top = true
		}
	}
	if !top {
		t.Errorf("no draggable strip across the top: %+v", drags)
	}
}

func sameCode(a, b *qr.Code) bool {
	if a.Size() != b.Size() || a.Size() == 0 {
		return false
	}
	for y := 0; y < a.Size(); y++ {
		for x := 0; x < a.Size(); x++ {
			if a.Dark(x, y) != b.Dark(x, y) {
				return false
			}
		}
	}
	return true
}

// LG-002: the QR code is replaced as the core sends new payloads.
func TestTheCodeIsReplacedAsNewOnesArrive(t *testing.T) {
	r := newRig(t)
	r.emit(t, unlinked("first-payload-1"))
	var first qr.Code
	qr.Encode(&first, []byte("first-payload-1"), qr.LevelM)
	if !sameCode(&r.sc.code, &first) {
		t.Fatal("the first code is not drawn")
	}
	r.emit(t, unlinked("a-second-and-longer-payload-2"))
	var second qr.Code
	qr.Encode(&second, []byte("a-second-and-longer-payload-2"), qr.LevelM)
	if !sameCode(&r.sc.code, &second) {
		t.Error("the new payload did not replace the code")
	}
	// Waiting for the next one.
	r.emit(t, unlinked(""))
	if !r.has(ui.RoleLabel, "Waiting for a code from WhatsApp…") {
		t.Errorf("no wait while there is no payload:\n%s", widgettest.Dump(r.Tree()))
	}
}

// LG-003: linking with a phone number asks for it, validates it, and offers
// Get a code, which sends only the digits.
func TestLinkingWithAPhoneNumberAsksForItAndSendsTheDigits(t *testing.T) {
	r := newRig(t)
	r.emit(t, unlinked("payload"))
	r.Play(widgettest.Click(ui.RoleLink, "Link with phone number instead"), widgettest.Idle(2))
	if !r.has(ui.RoleLabel, "Link with a phone number") {
		t.Fatalf("no phone entry:\n%s", widgettest.Dump(r.Tree()))
	}
	if n, ok := r.Focused(); !ok || n.Role != ui.RoleTextField {
		t.Error("the number's field does not take the keyboard")
	}
	if r.Must(ui.RoleButton, "Get a code").State&ui.SemDisabled == 0 {
		t.Error("Get a code is available with nothing typed")
	}
	r.Play(widgettest.Type("5511"), widgettest.Idle(2))
	if r.Must(ui.RoleButton, "Get a code").State&ui.SemDisabled == 0 {
		t.Error("Get a code is available with a short number")
	}
	r.Play(widgettest.Type(" (91) 234-5678"), widgettest.Idle(2))
	if r.Must(ui.RoleButton, "Get a code").State&ui.SemDisabled != 0 {
		t.Fatal("Get a code is not available with a whole number")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Get a code"), widgettest.Idle(2))
	got := r.sent(abi.CommandTagPairWithPhone)
	if len(got) != 1 || string(got[0].PairWithPhone.Phone) != "5511912345678" {
		t.Errorf("sent %+v", got)
	}
	// Cancel goes back to the QR code.
	r2 := newRig(t)
	r2.emit(t, unlinked("payload"))
	r2.Play(widgettest.Click(ui.RoleLink, "Link with phone number instead"), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "Cancel"), widgettest.Idle(2))
	if !r2.has(ui.RoleLabel, "Link this computer") {
		t.Error("Cancel did not go back to the QR code")
	}
}

// LG-004: phone linking says "Requesting a code for +N…", then "Waiting for a
// code from WhatsApp…", then shows the eight-character code with its steps and
// "Copy code".
func TestPhoneLinkingShowsItsProgressAndTheCode(t *testing.T) {
	r := newRig(t)
	r.emit(t, status(func(s *abi.LinkStatus) {
		s.Tag = abi.LinkStatusTagUnlinked
		s.Unlinked.PairingPhone, s.Unlinked.HasPairingPhone = []byte("5511912345678"), true
	}))
	if !r.has(ui.RoleLabel, "Requesting a code for +5511912345678…") {
		t.Fatalf("no progress:\n%s", widgettest.Dump(r.Tree()))
	}
	r.emit(t, status(func(s *abi.LinkStatus) {
		s.Tag = abi.LinkStatusTagUnlinked
		s.Unlinked.PairingPhone, s.Unlinked.HasPairingPhone = []byte("5511912345678"), true
		s.Unlinked.PairCode, s.Unlinked.HasPairCode = []byte("ABCD1234"), true
	}))
	for _, name := range []string{
		"Enter this code on your phone", "for +5511912345678", "ABCD-1234",
		"Tap Link a device, then Link with phone number instead",
	} {
		if !r.has(ui.RoleLabel, name) {
			t.Errorf("no %q:\n%s", name, widgettest.Dump(r.Tree()))
		}
	}
	r.Play(widgettest.Click(ui.RoleButton, "Copy code"), widgettest.Idle(2))
	if string(r.cb) != "ABCD1234" {
		t.Errorf("copied %q", r.cb)
	}
	if len(r.s.Toasts) == 0 || string(r.s.Toasts[0].Text) != "Copied" {
		t.Errorf("toasts %v", r.s.Toasts)
	}
}

// LG-005, LG-009: a failed link shows its error and offers Try again, which
// asks the core to reconnect; a locked keyring is not a lost key, so there is
// no Start over.
func TestAFailureOffersTryAgain(t *testing.T) {
	r := newRig(t)
	r.emit(t, status(func(s *abi.LinkStatus) {
		s.Tag = abi.LinkStatusTagFailed
		s.Failed.Error = []byte("Unlock your OS keyring and restart Zap")
	}))
	if !r.has(ui.RoleLabel, "Unlock your OS keyring and restart Zap") {
		t.Errorf("the error is not shown:\n%s", widgettest.Dump(r.Tree()))
	}
	if r.has(ui.RoleButton, "Start over…") {
		t.Error("Start over is offered for a locked keyring")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Try again"), widgettest.Idle(2))
	if len(r.sent(abi.CommandTagReconnect)) != 1 {
		t.Errorf("sent %v", r.f.SentTags())
	}
}

func TestOnlyALostKeyOffersToStartOver(t *testing.T) {
	for _, lost := range []string{
		"The archive is encrypted but its OS keyring key is missing. Restore the original keyring; the archive has not been changed",
		"The archive key in the OS keyring is invalid",
		"The archive could not be unlocked with its OS keyring key: file is not a database",
	} {
		if !archiveKeyLost(lost) {
			t.Errorf("%q", lost)
		}
	}
	for _, recoverable := range []string{
		"Unlock your OS keyring and restart Zap",
		"The OS keyring could not open Zap's archive key",
	} {
		if archiveKeyLost(recoverable) {
			t.Errorf("%q", recoverable)
		}
	}
}

// LG-010: a missing archive key offers Start over…, behind a confirmation
// that Cancel leaves alone.
func TestAMissingKeyOffersStartOverWithAConfirmation(t *testing.T) {
	r := newRig(t)
	r.emit(t, status(func(s *abi.LinkStatus) {
		s.Tag = abi.LinkStatusTagFailed
		s.Failed.Error = []byte("The archive key in the OS keyring is invalid")
	}))
	r.Play(widgettest.Click(ui.RoleButton, "Start over…"), widgettest.Idle(3))
	if !r.has(ui.RoleDialog, "Start over?") {
		t.Fatalf("no confirmation:\n%s", widgettest.Dump(r.Tree()))
	}
	r.Play(widgettest.Click(ui.RoleButton, "Cancel"), widgettest.Idle(3))
	if len(r.sent(abi.CommandTagStartOverArchive)) != 0 || r.has(ui.RoleDialog, "Start over?") {
		t.Fatal("Cancel started over, or left the dialog open")
	}
	// Enter is deliberately not bound to the destructive action: a stray key
	// destroys nothing (it may press the Cancel the focus is on).
	r.Play(widgettest.Click(ui.RoleButton, "Start over…"), widgettest.Idle(3), widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(2))
	if len(r.sent(abi.CommandTagStartOverArchive)) != 0 {
		t.Fatal("Enter started over")
	}
	if !r.has(ui.RoleDialog, "Start over?") {
		r.Play(widgettest.Click(ui.RoleButton, "Start over…"), widgettest.Idle(3))
	}
	r.Play(widgettest.Click(ui.RoleButton, "Start over"), widgettest.Idle(3))
	if len(r.sent(abi.CommandTagStartOverArchive)) != 1 || r.has(ui.RoleDialog, "Start over?") {
		t.Errorf("sent %v", r.f.SentTags())
	}
}

// LG-006, LG-007: the linking states, and the phone unlinking this computer.
func TestLinkingStatesReadAsTheyDo(t *testing.T) {
	r := newRig(t)
	// (an account being added shows the linking screen whatever its status)
	r.s.AddingAccount = true
	r.s.Touch()
	for _, c := range []struct {
		tag  abi.LinkStatusTag
		want []string
	}{
		{abi.LinkStatusTagConnecting, []string{"Connecting to WhatsApp…"}},
		{abi.LinkStatusTagLoggedOut, []string{
			"This computer was unlinked from your phone. Requesting a new code.", "Requesting a new code…"}},
	} {
		tag := c.tag
		r.emit(t, status(func(s *abi.LinkStatus) { s.Tag = tag }))
		for _, w := range c.want {
			if !r.has(ui.RoleLabel, w) {
				t.Errorf("%v: no %q:\n%s", c.tag, w, widgettest.Dump(r.Tree()))
			}
		}
	}
	// Linked, with no chats yet: the screen waits for them.
	r.emit(t, status(func(s *abi.LinkStatus) { s.Tag = abi.LinkStatusTagConnected }))
	if !r.has(ui.RoleLabel, "Linked. Waiting for your chats…") {
		t.Errorf("no wait for the chats:\n%s", widgettest.Dump(r.Tree()))
	}
}

// AC-004, AC-005, AC-009: with several accounts the linking screen offers a
// way back; adding a number offers Cancel, an unlinked one Remove this
// account, behind a confirmation.
func TestSeveralAccountsOfferAWayBackAndRemoval(t *testing.T) {
	r := newRig(t)
	r.emit(t, unlinked("payload"))
	if r.has(ui.RoleLink, "Use another account") || r.has(ui.RoleLink, "Remove this account") {
		t.Error("choices with one account")
	}
	r.n = fake.New(nil)
	r.n.Emit(status(func(s *abi.LinkStatus) { s.Tag = abi.LinkStatusTagConnected }))
	r.s.AddAccount("2", nil, r.n)
	r.s.Touch()
	r.Frames(3)
	r.Play(widgettest.Click(ui.RoleLink, "Use another account"), widgettest.Idle(2))
	if !r.s.AccountMenu {
		t.Error("Use another account did not open the switcher")
	}
	r.s.AccountMenu = false
	r.Play(widgettest.Click(ui.RoleLink, "Remove this account"), widgettest.Idle(3))
	if !r.has(ui.RoleDialog, "Remove this account?") || !r.has(ui.RoleLabel, "This unlinks the number on this computer and deletes its local chats. Other accounts stay.") {
		t.Fatalf("no confirmation:\n%s", widgettest.Dump(r.Tree()))
	}
	r.Play(widgettest.Click(ui.RoleButton, "Cancel"), widgettest.Idle(3))
	if len(r.sent(abi.CommandTagRemoveAccount)) != 0 {
		t.Fatal("Cancel removed the account")
	}
	r.Play(widgettest.Click(ui.RoleLink, "Remove this account"), widgettest.Idle(3), widgettest.Click(ui.RoleButton, "Remove"), widgettest.Idle(3))
	if len(r.sent(abi.CommandTagRemoveAccount)) != 1 {
		t.Errorf("sent %v", r.f.SentTags())
	}
}

// AC-004: adding a number shows the linking screen over the linked account,
// with Cancel back to it.
func TestAddingANumberOffersCancelBackToThePreviousAccount(t *testing.T) {
	r := newRig(t)
	r.emit(t, status(func(s *abi.LinkStatus) { s.Tag = abi.LinkStatusTagConnected }))
	r.s.Acct() // account 1, linked
	f2 := fake.New(nil)
	f2.Emit(unlinked("payload-2"))
	r.s.AddAccount("2", nil, f2)
	r.s.SwitchAccount(1)
	r.s.AddingAccount = true
	r.s.Touch()
	r.Frames(3)
	if !r.has(ui.RoleLabel, "Link this computer") || !r.has(ui.RoleLink, "Cancel") {
		t.Fatalf("no linking screen with Cancel:\n%s", widgettest.Dump(r.Tree()))
	}
	if r.has(ui.RoleLink, "Remove this account") {
		t.Error("Remove this account while adding")
	}
	r.Play(widgettest.Click(ui.RoleLink, "Cancel"), widgettest.Idle(3))
	if r.s.AddingAccount || r.s.Active != 0 {
		t.Errorf("adding %v, active %d", r.s.AddingAccount, r.s.Active)
	}
	// The unlinked account that was abandoned is removed (its core is asked).
	var removed int
	for _, tag := range f2.SentTags() {
		if tag == abi.CommandTagRemoveAccount {
			removed++
		}
	}
	if removed != 1 {
		t.Errorf("the abandoned account was not removed: %v", f2.SentTags())
	}
}

// AX-004: the linking screen's controls have names a screen reader says, in
// each state that has any (the QR code, the phone number form, a failure).
func TestTheLinkingScreenNamesItsControls(t *testing.T) {
	r := newRig(t)
	r.emit(t, unlinked("2@abcdef,ghij=="))
	r.LintNames("qr code")
	r.Play(widgettest.Click(ui.RoleLink, "Link with phone number instead"), widgettest.Idle(3))
	r.LintNames("phone number")
	r = newRig(t)
	r.emit(t, status(func(s *abi.LinkStatus) {
		s.Tag = abi.LinkStatusTagFailed
		s.Failed.Error = []byte("Unlock your OS keyring and restart Zap")
	}))
	r.LintNames("failure")
}

// LG-001: the code drawn is the account's own. Link.Gen counts each account's
// statuses by itself, so another account's QR code at the same generation is
// encoded again, not shown as the first one's; and a number being typed for
// one account is not another's.
func TestEachAccountShowsItsOwnQRCode(t *testing.T) {
	r := newRig(t)
	r.emit(t, unlinked("2@first,account=="))
	f2 := fake.New(nil)
	f2.Emit(unlinked("2@second,account=="))
	b := r.s.AddAccount("2", nil, f2)
	r.s.Pump(0)
	if g1, g2 := r.s.Accounts[0].Model.Link.Gen(), b.Model.Link.Gen(); g1 != g2 {
		t.Fatalf("the generations differ (%d, %d): the test needs them equal", g1, g2)
	}
	r.Play(widgettest.Click(ui.RoleLink, "Link with phone number instead"))
	r.Frames(2)
	r.s.SwitchAccount(1)
	r.Frames(3)
	var want qr.Code
	qr.Encode(&want, []byte("2@second,account=="), qr.LevelM)
	if !sameCode(&r.sc.code, &want) {
		t.Error("the second account shows the first one's code")
	}
	if r.sc.phoning || !r.has(ui.RoleImage, "Link this computer") {
		t.Error("the first account's phone number entry carried over to the second")
	}
}
