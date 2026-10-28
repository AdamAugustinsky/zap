package lock

import (
	"strings"
	"sync"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/lockhash"
	"zapfast/state"
	"zapfast/ui/lock/applock"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

const (
	password = "open-sesame"
	chat     = "1@s.whatsapp.net"
)

// clock is a clock that moves only when told to.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// host is the shell with the lock's Tick run first each frame, as the app
// wires it.
type host struct {
	*shell.Shell
	ls *Screen
	s  *state.State
}

func (h *host) Build(c *ui.Context) {
	h.ls.Tick(h.s, c)
	h.Shell.Build(c)
}

type rig struct {
	*widgettest.Rig
	s     *state.State
	ls    *Screen
	f     *fake.Core
	clock *clock
	held  [][]applock.Deferred
	locks int
}

// newRig is the app over a linked fake account with the lock set to password
// (empty: no lock), the lock screen in its slot and nothing else.
func newRig(t *testing.T, pw string) *rig {
	t.Helper()
	r := &rig{clock: &clock{now: time.Unix(1_000_000, 0)}}
	r.s = state.New()
	r.s.Now = r.clock.Now
	if pw != "" {
		r.s.Settings.AppLockHash = []byte(lockhash.AppVerifierWithRounds([]byte(pw), 10))
	}
	r.f = fake.New(nil)
	r.f.Emit(link(abi.LinkStatusTagConnected), chats())
	r.s.AddAccount("1", nil, r.f)
	r.ls = New()
	r.ls.OnUnlock = func(_ *state.State, held []applock.Deferred) { r.held = append(r.held, held) }
	r.ls.OnLock = func(*state.State) { r.locks++ }
	sh, err := shell.New(r.s, &screen.Set{Lock: r.ls})
	if err != nil {
		t.Fatal(err)
	}
	sh.Traffic = true
	cfg := shell.Config(r.s)
	r.Rig = widgettest.Open(t, cfg, int(cfg.Width), int(cfg.Height), 1, &host{Shell: sh, ls: r.ls, s: r.s})
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Frames(4)
	return r
}

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

func link(tag abi.LinkStatusTag) *abi.Event {
	return ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = tag })
}

func chats() *abi.Event {
	return ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(chat), Name: []byte("Ada Lovelace"), LastActivity: 20}}}
	})
}

// until runs frames until cond holds (the checking goroutine takes a few
// milliseconds).
func (r *rig) until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 1000 && !cond(); i++ {
		r.Frame()
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatalf("never: %s", what)
	}
}

func (r *rig) has(role ui.Role, name string) bool {
	_, ok := r.Find(role, name)
	return ok
}

// tryPassword types pw into the field and presses Enter, and waits for the
// answer.
func (r *rig) tryPassword(t *testing.T, pw string) {
	t.Helper()
	r.Play(widgettest.Type(pw), widgettest.Idle(1), widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(1))
	r.until(t, "the check finishes", func() bool { return !r.ls.al.Checking() })
	r.Frames(2)
}

// AL-003: with a password set, ZapFast starts locked, and only then.
func TestItStartsLockedOnlyWithAPassword(t *testing.T) {
	r := newRig(t, password)
	if !r.s.Lock.Locked || !r.has(ui.RoleLabel, "Zap is locked") {
		t.Fatal("not locked at start")
	}
	if r.locks != 1 {
		t.Errorf("OnLock ran %d times at start", r.locks)
	}
	r = newRig(t, "")
	if r.s.Lock.Locked || r.has(ui.RoleLabel, "Zap is locked") {
		t.Error("locked without a password")
	}
}

// AL-005: the lock screen alone shows "ZapFast is locked", a focused password
// field and Unlock; secure input is on while the field has the keyboard.
func TestTheLockScreenShowsAFocusedPasswordFieldAndUnlock(t *testing.T) {
	r := newRig(t, password)
	for _, name := range []string{"Chats", "Calls", "Settings", "Accounts"} {
		if r.has(ui.RoleButton, name) {
			t.Errorf("the rail's %q shows behind the lock", name)
		}
	}
	if n, ok := r.Focused(); !ok || n.Role != ui.RoleTextField || string(r.Tree().Text(n.Name)) != "Password" {
		t.Errorf("focus %v %v", n, ok)
	}
	if n := r.Must(ui.RoleButton, "Unlock"); n.State&ui.SemDisabled == 0 {
		t.Error("Unlock is enabled with nothing typed")
	}
	if !r.Context().SecureFieldFocused() {
		t.Error("secure keyboard entry is not asked for while the password field has the keyboard")
	}
	if n := r.Must(ui.RoleTextField, "Password"); n.State&ui.SemSecure == 0 {
		t.Error("the field is not a secure text field to assistive technologies")
	}
	top := false
	for _, d := range r.Context().DragRegions(nil) {
		top = top || (!d.Block && d.Rect[1] <= 0 && d.Rect[3] > 20 && d.Rect[2]-d.Rect[0] >= 700)
	}
	if !top {
		t.Error("no draggable strip across the top of the lock screen")
	}
	r.Play(widgettest.Type("abc"), widgettest.Idle(1))
	r.Frames(1)
	if n := r.Must(ui.RoleButton, "Unlock"); n.State&ui.SemDisabled != 0 {
		t.Error("Unlock is disabled with a password typed")
	}
	if v := string(r.Tree().Text(r.Must(ui.RoleTextField, "Password").Value)); v != "•••" {
		t.Errorf("the field announces %q", v)
	}
}

// A right password unlocks and ends secure input; what the lock held comes
// back in the order it arrived.
func TestARightPasswordUnlocks(t *testing.T) {
	r := newRig(t, password)
	r.Offscreen() // (the window)
	if !r.ls.Defer(r.s, applock.Deferred{Kind: applock.DeferInvite, Code: "AbCdEf1234567890XyZ"}) {
		t.Fatal("a request was not held while locked")
	}
	r.ls.Defer(r.s, applock.Deferred{Kind: applock.DeferChat, Chat: chat})
	r.tryPassword(t, password)
	if r.s.Lock.Locked {
		t.Fatal("still locked")
	}
	if len(r.held) != 1 || len(r.held[0]) != 2 || r.held[0][0].Kind != applock.DeferInvite || r.held[0][1].Kind != applock.DeferChat {
		t.Errorf("replayed %+v", r.held)
	}
	if r.Context().SecureFieldFocused() {
		t.Error("secure keyboard entry is still asked for after the unlock")
	}
	if !r.has(ui.RoleButton, "Chats") && !r.has(ui.RoleListItem, "Ada Lovelace") {
		t.Error("the chats are not back")
	}
	if r.ls.Defer(r.s, applock.Deferred{Kind: applock.DeferInvite}) {
		t.Error("a request is held with no lock")
	}
}

// AL-006: a wrong password says so, and from the third each try waits longer.
func TestWrongPasswordsSayWhyAndBackOff(t *testing.T) {
	r := newRig(t, password)
	r.tryPassword(t, "not-it")
	if !r.has(ui.RoleLabel, "Wrong password. Try again.") {
		t.Error("no message for a wrong password")
	}
	if n, ok := r.Focused(); !ok || n.Role != ui.RoleTextField {
		t.Error("the field does not take the keyboard back after a try")
	}
	if v := string(r.Tree().Text(r.Must(ui.RoleTextField, "Password").Value)); v != "" {
		t.Errorf("the field keeps %q after a try", v)
	}
	r.tryPassword(t, "not-it")
	r.tryPassword(t, "not-it")
	if !r.has(ui.RoleLabel, "Too many wrong passwords. Try again in 1 s.") {
		t.Fatalf("no wait after three:\n%s", widgettest.Dump(r.Tree()))
	}
	// A try during the wait is not made at all.
	r.Play(widgettest.Type(password), widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(2))
	if r.ls.al.Checking() || !r.s.Lock.Locked {
		t.Error("a try was made during the wait")
	}
	// The wait ends with time.
	r.clock.Advance(1100 * time.Millisecond)
	r.Frames(2)
	if r.has(ui.RoleLabel, "Too many wrong passwords. Try again in 1 s.") {
		t.Error("the wait did not end")
	}
	r.Play(widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(1))
	r.until(t, "the check finishes", func() bool { return !r.ls.al.Checking() })
	r.Frames(2)
	if r.s.Lock.Locked {
		t.Error("the right password was refused after the wait")
	}
	// The count starts over for the next lock.
	r.ls.LockNow(r.s)
	r.Frames(2)
	r.tryPassword(t, "not-it")
	if _, waiting := r.ls.al.WaitLeft(); waiting {
		t.Error("the back-off survived an unlock")
	}
}

// AL-007: "Forgot password? Unlink this computer" confirms, unlinks every
// number, and the lock lifts only once WhatsApp has confirmed it.
func TestForgotPasswordUnlinksAndLiftsTheLockWhenLoggedOut(t *testing.T) {
	r := newRig(t, password)
	r.Play(widgettest.Click(ui.RoleLink, "Forgot password? Unlink this computer"), widgettest.Idle(2))
	if !r.has(ui.RoleLabel, "Unlink this computer?") {
		t.Fatalf("no confirmation:\n%s", widgettest.Dump(r.Tree()))
	}
	// Cancel goes back to the password.
	r.Play(widgettest.Click(ui.RoleButton, "Cancel"), widgettest.Idle(2))
	if !r.has(ui.RoleButton, "Unlock") {
		t.Fatal("Cancel did not go back to the password")
	}
	r.Play(widgettest.Click(ui.RoleLink, "Forgot password? Unlink this computer"), widgettest.Idle(2))
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	if !r.has(ui.RoleButton, "Unlock") {
		t.Fatal("Escape did not go back to the password")
	}
	r.Play(widgettest.Click(ui.RoleLink, "Forgot password? Unlink this computer"), widgettest.Idle(2))
	r.Play(widgettest.Click(ui.RoleButton, "Unlink"), widgettest.Idle(2))
	if !r.has(ui.RoleLabel, "Unlinking this computer…") {
		t.Errorf("no progress:\n%s", widgettest.Dump(r.Tree()))
	}
	var unlinks int
	for _, tag := range r.f.SentTags() {
		if tag == abi.CommandTagUnlink {
			unlinks++
		}
	}
	if unlinks != 1 {
		t.Errorf("sent %d Unlink commands", unlinks)
	}
	if !r.s.Lock.Locked || len(r.s.Settings.AppLockHash) == 0 {
		t.Fatal("the lock lifted before WhatsApp unlinked")
	}
	r.f.Emit(link(abi.LinkStatusTagLoggedOut))
	r.Frames(4)
	if r.s.Lock.Locked || len(r.s.Settings.AppLockHash) != 0 {
		t.Errorf("locked %v hash %q after the unlink", r.s.Lock.Locked, r.s.Settings.AppLockHash)
	}
}

// AL-011: any unlink, including from the phone, turns the app lock off; one
// number of several going does not.
func TestAnyUnlinkTurnsTheLockOff(t *testing.T) {
	r := newRig(t, password)
	r.tryPassword(t, password)
	f2 := fake.New(nil)
	f2.Emit(link(abi.LinkStatusTagConnected), chats())
	r.s.AddAccount("2", nil, f2)
	r.Frames(2)
	r.f.Emit(link(abi.LinkStatusTagLoggedOut))
	r.Frames(3)
	if len(r.s.Settings.AppLockHash) == 0 {
		t.Error("the lock went with one of two numbers")
	}
	f2.Emit(link(abi.LinkStatusTagLoggedOut))
	r.Frames(3)
	if len(r.s.Settings.AppLockHash) != 0 {
		t.Error("the lock stayed after the last number was unlinked")
	}
}

// AL-004 and KB-027: ⌘⇧L (the shell's Lock ZapFast) locks at once when a
// password is set, and does nothing otherwise.
func TestLockZapFastLocksAtOnce(t *testing.T) {
	r := newRig(t, password)
	r.tryPassword(t, password)
	if r.s.Lock.Locked {
		t.Fatal("not unlocked")
	}
	r.Play(widgettest.Command(uint16(shell.CmdLock)), widgettest.Idle(3))
	if !r.s.Lock.Locked || !r.has(ui.RoleLabel, "Zap is locked") {
		t.Fatalf("the menu command did not lock:\n%s", widgettest.Dump(r.Tree()))
	}
	// The screen is fresh: nothing half typed from the last time.
	if v := string(r.Tree().Text(r.Must(ui.RoleTextField, "Password").Value)); v != "" {
		t.Errorf("the field keeps %q", v)
	}
	r = newRig(t, "")
	r.Play(widgettest.Command(uint16(shell.CmdLock)), widgettest.Idle(3))
	if r.s.Lock.Locked {
		t.Error("locked without a password")
	}
	r.ls.LockNow(r.s)
	if r.s.Lock.Locked {
		t.Error("LockNow locked without a password")
	}
}

// AL-002: the app locks after the chosen time without input, a longer choice
// waits longer, and input restarts the count.
func TestInactivityLocksAfterTheChosenTime(t *testing.T) {
	r := newRig(t, password)
	r.tryPassword(t, password)
	r.s.Settings.AppLockAfter = state.LockOneMinute
	r.ls.SetAutoLock(r.s, state.LockOneMinute)
	r.clock.Advance(59 * time.Second)
	r.Frames(2)
	if r.s.Lock.Locked {
		t.Fatal("locked after 59 s")
	}
	// A key restarts the count.
	r.Play(widgettest.Key(ui.KeyDown, 0), widgettest.Idle(1))
	r.clock.Advance(59 * time.Second)
	r.Frames(2)
	if r.s.Lock.Locked {
		t.Fatal("input did not restart the count")
	}
	r.clock.Advance(2 * time.Second)
	r.Frames(2)
	if !r.s.Lock.Locked {
		t.Fatal("still unlocked after a minute without input")
	}
	// A longer choice waits longer.
	r.tryPassword(t, password)
	r.ls.SetAutoLock(r.s, state.LockOneHour)
	r.clock.Advance(59 * time.Minute)
	r.Frames(2)
	if r.s.Lock.Locked {
		t.Fatal("locked after 59 minutes with an hour chosen")
	}
	r.clock.Advance(time.Minute + time.Second)
	r.Frames(2)
	if !r.s.Lock.Locked {
		t.Fatal("not locked after an hour")
	}
}

// With no window open, the tick still locks (time hidden in the tray counts),
// and a recording counts as use.
func TestTheTickLocksWithNoWindowAndARecordingCountsAsUse(t *testing.T) {
	s := state.New()
	c := &clock{now: time.Unix(1_000_000, 0)}
	s.Now = c.Now
	s.Settings.AppLockHash = []byte(lockhash.AppVerifierWithRounds([]byte(password), 10))
	s.Settings.AppLockAfter = state.LockOneMinute
	ls := New()
	ls.Tick(s, nil) // starts locked
	if !s.Lock.Locked {
		t.Fatal("not locked at start")
	}
	ls.al.Release()
	s.Lock.Locked = false
	recording := true
	ls.InUse = func(*state.State) bool { return recording }
	c.Advance(10 * time.Minute)
	ls.Tick(s, nil)
	if s.Lock.Locked {
		t.Error("locked during a recording")
	}
	recording = false
	c.Advance(59 * time.Second)
	ls.Tick(s, nil)
	if s.Lock.Locked {
		t.Error("locked 59 s after the recording")
	}
	c.Advance(2 * time.Second)
	ls.Tick(s, nil)
	if !s.Lock.Locked {
		t.Error("not locked after a minute hidden")
	}
	ls.cancelTimer()
}

// The system's sleep locks (the hook package sys calls on app.EvWillSleep).
func TestSleepLocks(t *testing.T) {
	r := newRig(t, password)
	r.tryPassword(t, password)
	r.ls.Sleep(r.s)
	r.Frames(2)
	if !r.s.Lock.Locked || !r.has(ui.RoleLabel, "Zap is locked") {
		t.Error("sleep did not lock")
	}
}

// Locking hides the locked-chats folder, the account menu and the unlock
// dialog, and never touches the chat lock's code (LK-009, LK-006).
func TestLockingClosesTheLockedFolderAndKeepsTheChatCodeSeparate(t *testing.T) {
	r := newRig(t, password)
	r.tryPassword(t, password)
	a := r.s.Acct()
	r.s.Settings.ChatLockHash = []byte("chat-code-verifier")
	a.Mode = state.ListLocked
	r.s.AccountMenu = true
	r.s.OpenDialog(state.Dialog{Kind: state.DialogUnlockLockedChats})
	r.ls.LockNow(r.s)
	if a.Mode == state.ListLocked || r.s.AccountMenu || r.s.Dialog.Open() {
		t.Errorf("mode %v menu %v dialog %v", a.Mode, r.s.AccountMenu, r.s.Dialog.Kind)
	}
	r.Frames(2)
	r.tryPassword(t, password)
	if string(r.s.Settings.ChatLockHash) != "chat-code-verifier" || a.Mode == state.ListLocked {
		t.Errorf("unlocking the app touched the chat lock: %q mode %v", r.s.Settings.ChatLockHash, a.Mode)
	}
}

// AL-001's settings side: setting, changing and turning off the password
// through the screen's form actions; AL-012: only a verifier is stored.
func TestThePasswordFormSetsChangesAndTurnsOff(t *testing.T) {
	s := state.New()
	ls := New()
	ls.al.SetRounds(10)
	var saved int
	ls.Save = func(*state.State) { saved++ }
	await := func() {
		t.Helper()
		for i := 0; i < 5000 && ls.al.Checking(); i++ {
			ls.poll(s)
			time.Sleep(time.Millisecond)
		}
		ls.poll(s)
		if ls.al.Checking() {
			t.Fatal("the check never finished")
		}
	}
	// Changing or turning off needs a password to exist; setting needs none.
	ls.OpenForm(s, applock.FormChange, true)
	ls.OpenForm(s, applock.FormTurnOff, true)
	if ls.al.Form != nil {
		t.Fatal("a change form opened without a password")
	}
	ls.OpenForm(s, applock.FormSet, true)
	f := ls.al.Form
	f.New, f.Confirm = []byte("short"), []byte("short")
	ls.SubmitForm(s)
	if f.Error != applock.FormTooShort || len(s.Settings.AppLockHash) != 0 {
		t.Fatalf("error %v hash %q", f.Error, s.Settings.AppLockHash)
	}
	f.New, f.Confirm = []byte("a-good-one"), []byte("a-good-one")
	ls.SubmitForm(s)
	await()
	if ls.al.Form != nil || len(s.Settings.AppLockHash) == 0 || saved != 1 {
		t.Fatalf("form %v hash %q saved %d", ls.al.Form, s.Settings.AppLockHash, saved)
	}
	if got := string(s.Settings.AppLockHash); !lockhash.VerifiesApp(got, []byte("a-good-one")) || got == "a-good-one" {
		t.Errorf("stored %q", got)
	}
	if len(s.Toasts) != 1 || string(s.Toasts[0].Text) != "App lock password saved" {
		t.Errorf("toasts %v", s.Toasts)
	}
	// Setting again must not replace the password without it.
	ls.OpenForm(s, applock.FormSet, true)
	if ls.al.Form != nil {
		t.Error("a set form opened over an existing password")
	}
	// Changing: a wrong current password is refused.
	ls.OpenForm(s, applock.FormChange, true)
	f = ls.al.Form
	f.Current, f.New, f.Confirm = []byte("nope"), []byte("another-one"), []byte("another-one")
	ls.SubmitForm(s)
	await()
	if f.Error != applock.FormWrongCurrent || f.Busy || !lockhash.VerifiesApp(string(s.Settings.AppLockHash), []byte("a-good-one")) {
		t.Fatalf("error %v busy %v", f.Error, f.Busy)
	}
	// (a submitted form is emptied: the person types it again)
	f.Current, f.New, f.Confirm = []byte("a-good-one"), []byte("another-one"), []byte("another-one")
	ls.SubmitForm(s)
	await()
	if !lockhash.VerifiesApp(string(s.Settings.AppLockHash), []byte("another-one")) {
		t.Error("the password did not change")
	}
	// Turning off.
	ls.OpenForm(s, applock.FormTurnOff, true)
	ls.al.Form.Current = []byte("another-one")
	ls.SubmitForm(s)
	await()
	if len(s.Settings.AppLockHash) != 0 || s.Toasts[len(s.Toasts)-1].Text == nil || string(s.Toasts[len(s.Toasts)-1].Text) != "App lock turned off" {
		t.Errorf("hash %q toasts %v", s.Settings.AppLockHash, s.Toasts)
	}
}

// AL-009: while locked, the app's other commands do nothing.
func TestWhileLockedOtherCommandsDoNothing(t *testing.T) {
	r := newRig(t, password)
	for _, cmd := range []int{int(shell.CmdSettings), int(shell.CmdShortcuts), int(shell.CmdNewChat)} {
		r.Play(widgettest.Command(uint16(cmd)), widgettest.Idle(2))
		if r.s.Win.Want[state.WinSettings] || r.s.Dialog.Open() {
			t.Fatalf("command %d acted while locked", cmd)
		}
	}
	if !r.s.Lock.Locked {
		t.Error("a command unlocked")
	}
}

// zeroed reports whether every byte the buffer's storage can hold is zero.
func zeroed(b []byte) bool {
	for _, x := range b[:cap(b)] {
		if x != 0 {
			return false
		}
	}
	return true
}

// The password field is secure: bullets only in the semantic tree, the
// buffer is wiped (its storage too) once the password is submitted, when the
// lock screen goes to "Forgot password?", and at the unlock.
func TestThePasswordFieldIsSecureAndWiped(t *testing.T) {
	r := newRig(t, password)
	r.Play(widgettest.Type("hunter2"), widgettest.Idle(2))
	if strings.Contains(widgettest.Dump(r.Tree()), "hunter") {
		t.Error("the password is in the semantic tree")
	}
	if v := string(r.Tree().Text(r.Must(ui.RoleTextField, "Password").Value)); v != "•••••••" {
		t.Errorf("the field announces %q", v)
	}
	if r.ls.pw.Len() != 7 {
		t.Fatalf("the buffer holds %d bytes", r.ls.pw.Len())
	}
	bs := r.ls.pw.Bytes()
	r.Play(widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(1))
	r.until(t, "the check finishes", func() bool { return !r.ls.al.Checking() })
	if r.ls.pw.Len() != 0 || !zeroed(bs) {
		t.Errorf("submitted: %d bytes left, storage zeroed %v", r.ls.pw.Len(), zeroed(bs))
	}
	// Half typed, then Forgot password?: the field goes and takes it along.
	r.Frames(2)
	r.Play(widgettest.Type("half"), widgettest.Idle(1))
	bs = r.ls.pw.Bytes()
	r.Play(widgettest.Click(ui.RoleLink, "Forgot password? Unlink this computer"), widgettest.Idle(2))
	if r.ls.pw.Len() != 0 || !zeroed(bs) {
		t.Error("the half-typed password survived the screen going away")
	}
	// Half typed, then locked again by the shell (⌘⇧L) or the timer.
	r.Play(widgettest.Click(ui.RoleButton, "Cancel"), widgettest.Idle(2), widgettest.Type("open-"), widgettest.Idle(1))
	bs = r.ls.pw.Bytes()
	r.tryPassword(t, "sesame") // typed after the half that was already there
	if r.s.Lock.Locked || r.ls.pw.Len() != 0 || !zeroed(bs) {
		t.Errorf("the unlock left the password in the buffer: locked %v len %d zeroed %v", r.s.Lock.Locked, r.ls.pw.Len(), zeroed(bs))
	}
	r.ls.LockNow(r.s)
	r.Frames(2)
	r.Play(widgettest.Type("typed"), widgettest.Idle(1))
	bs = r.ls.pw.Bytes()
	r.s.Lock.Locked = false // the lock is lifted without a password (the unlink)
	r.Frames(2)
	r.ls.release(r.s)
	if r.ls.pw.Len() != 0 || !zeroed(bs) {
		t.Error("lifting the lock left the password in the buffer")
	}
}

// AX-004: the lock screen's password field and buttons have names.
func TestTheLockScreenNamesItsControls(t *testing.T) {
	r := newRig(t, password)
	r.LintNames("lock screen")
}
