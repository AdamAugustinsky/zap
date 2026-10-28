package applock

import (
	"sync"
	"testing"
	"time"

	"zapfast/lockhash"
)

// manualClock is a clock that moves only when told to.
type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *manualClock { return &manualClock{now: time.Unix(1_000_000, 0)} }

func (m *manualClock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

func (m *manualClock) Advance(d time.Duration) {
	m.mu.Lock()
	m.now = m.now.Add(d)
	m.mu.Unlock()
}

// The verifier's own tests are lockhash's.

func TestRepeatedFailuresWaitLongerUpToALimit(t *testing.T) {
	for _, c := range []struct {
		failures uint32
		want     time.Duration
	}{
		{1, 0}, {2, 0}, {3, time.Second}, {4, 2 * time.Second}, {5, 4 * time.Second},
		{8, MaxWait}, {^uint32(0), MaxWait},
	} {
		if got := WaitAfter(c.failures); got != c.want {
			t.Errorf("WaitAfter(%d) = %v, want %v", c.failures, got, c.want)
		}
	}
}

func TestInactivityIsCountedOnTheInjectedClock(t *testing.T) {
	clock := newClock()
	l := New(false)
	l.SetClock(clock.Now)
	after := 60 * time.Second
	if got := l.IdleLeft(after); got != after {
		t.Fatalf("idle left %v", got)
	}
	clock.Advance(45 * time.Second)
	if got := l.IdleLeft(after); got != 15*time.Second {
		t.Errorf("idle left %v", got)
	}
	l.NoteInput()
	if got := l.IdleLeft(after); got != after {
		t.Errorf("input did not restart the count: %v", got)
	}
	clock.Advance(61 * time.Second)
	if got := l.IdleLeft(after); got != 0 {
		t.Errorf("idle left %v after the time passed", got)
	}
}

func TestWrongPasswordsBackOffAndARightOneResets(t *testing.T) {
	clock := newClock()
	l := New(true)
	l.SetClock(clock.Now)
	stored := lockhash.AppVerifierWithRounds([]byte("right-password"), 10)
	for attempt := 1; attempt <= 3; attempt++ {
		if !l.CanTry(true) {
			t.Fatalf("attempt %d cannot be made", attempt)
		}
		l.TryUnlock(stored, []byte("wrong-password"), nil)
		out, ok := l.Wait(30 * time.Second)
		if !ok || out.Kind != OutcomeUnlock || out.Matched {
			t.Fatalf("attempt %d: %+v %v", attempt, out, ok)
		}
		l.Unlocked(false)
		if !l.IsLocked() || !l.Wrong {
			t.Fatalf("attempt %d: locked %v wrong %v", attempt, l.IsLocked(), l.Wrong)
		}
	}
	// The third wrong password makes the next try wait a second.
	if left, ok := l.WaitLeft(); !ok || left != time.Second {
		t.Errorf("wait left %v %v", left, ok)
	}
	if l.CanTry(true) {
		t.Error("a try during the wait is allowed")
	}
	l.TryUnlock(stored, []byte("right-password"), nil)
	if l.Checking() {
		t.Error("a try during the wait is not made")
	}
	clock.Advance(time.Second)
	if !l.CanTry(true) {
		t.Fatal("the wait did not end")
	}
	l.TryUnlock(stored, []byte("right-password"), nil)
	if out, ok := l.Wait(30 * time.Second); !ok || !out.Matched {
		t.Fatalf("outcome %+v %v", out, ok)
	}
	l.Unlocked(true)
	if l.IsLocked() || l.Wrong {
		t.Error("still locked")
	}
	if _, ok := l.WaitLeft(); ok {
		t.Error("still waiting")
	}
	// The count starts over for the next lock.
	l.Lock()
	l.Unlocked(false)
	if _, ok := l.WaitLeft(); ok {
		t.Error("the count did not start over")
	}
}

func TestANewPasswordNeedsSixCharactersTypedTwice(t *testing.T) {
	f := NewForm(FormSet)
	f.New, f.Confirm = []byte("five5"), []byte("five5")
	if got := f.Check(); got != FormTooShort {
		t.Errorf("five characters: %v", got)
	}
	f.New = []byte("sixsix")
	if got := f.Check(); got != FormMismatch {
		t.Errorf("mismatch: %v", got)
	}
	f.Confirm = []byte("sixsix")
	if got := f.Check(); got != FormOK {
		t.Errorf("six characters typed twice: %v", got)
	}
	// Characters, not bytes.
	f.New, f.Confirm = []byte("ééééé"), []byte("ééééé")
	if got := f.Check(); got != FormTooShort {
		t.Errorf("five two-byte characters: %v", got)
	}
}

func TestChangingOrTurningOffRequiresTheCurrentPassword(t *testing.T) {
	stored := lockhash.AppVerifierWithRounds([]byte("old-password"), 10)
	l := New(false)
	l.SetRounds(10)
	l.Form = NewForm(FormTurnOff)
	l.Form.Current = []byte("not-it")
	l.SubmitForm(stored, true, nil)
	if !l.Form.Busy {
		t.Error("the form is not busy")
	}
	if out, ok := l.Wait(30 * time.Second); !ok || out.Kind != OutcomeWrongCurrent {
		t.Fatalf("outcome %+v %v", out, ok)
	}
	l.Form.Current = []byte("old-password")
	l.SubmitForm(stored, true, nil)
	if out, ok := l.Wait(30 * time.Second); !ok || out.Kind != OutcomeTurnOff {
		t.Fatalf("outcome %+v %v", out, ok)
	}

	form := NewForm(FormChange)
	form.Current, form.New, form.Confirm = []byte("not-it"), []byte("new-password"), []byte("new-password")
	l.Form = form
	l.SubmitForm(stored, true, nil)
	if out, ok := l.Wait(30 * time.Second); !ok || out.Kind != OutcomeWrongCurrent {
		t.Fatalf("outcome %+v %v", out, ok)
	}
	form.Current, form.New, form.Confirm = []byte("old-password"), []byte("new-password"), []byte("new-password")
	l.SubmitForm(stored, true, nil)
	out, ok := l.Wait(30 * time.Second)
	if !ok || out.Kind != OutcomeSet {
		t.Fatalf("expected a new verifier, got %+v %v", out, ok)
	}
	if !lockhash.VerifiesApp(out.Verifier, []byte("new-password")) || lockhash.VerifiesApp(out.Verifier, []byte("old-password")) {
		t.Error("the new verifier opens the wrong password")
	}
	if len(form.Current)+len(form.New)+len(form.Confirm) != 0 {
		t.Error("the form keeps the passwords")
	}
}

func TestAnInvalidFormIsRefusedWithoutACheck(t *testing.T) {
	l := New(false)
	f := NewForm(FormSet)
	f.New, f.Confirm = []byte("short"), []byte("short")
	l.Form = f
	l.SubmitForm("", false, nil)
	if l.Checking() {
		t.Error("an invalid form was checked")
	}
	if f.Error != FormTooShort {
		t.Errorf("error %v", f.Error)
	}
}

// The passwords the goroutine was given are zeroed when it is done.
func TestThePasswordIsZeroedOnceChecked(t *testing.T) {
	l := New(true)
	stored := lockhash.AppVerifierWithRounds([]byte("right-password"), 10)
	pw := []byte("right-password")
	l.TryUnlock(stored, pw, nil)
	if _, ok := l.Wait(30 * time.Second); !ok {
		t.Fatal("no answer")
	}
	for _, b := range pw {
		if b != 0 {
			t.Fatalf("password left in memory: %q", pw)
		}
	}
	// And a refused try zeroes it too.
	l.Unlocked(false)
	l.Unlocked(false)
	l.Unlocked(false)
	pw = []byte("right-password")
	l.TryUnlock(stored, pw, nil)
	for _, b := range pw {
		if b != 0 {
			t.Fatalf("password left in memory by a refused try: %q", pw)
		}
	}
}

func TestDeferredRequestsKeepTheirOrderAndReplaceTheirKind(t *testing.T) {
	l := New(true)
	chat := func(text string) Deferred {
		return Deferred{Kind: DeferChat, Chat: "1@s.whatsapp.net", Text: text, HasText: text != ""}
	}
	invite := Deferred{Kind: DeferInvite, Code: "AbCdEf1234567890XyZ"}
	l.Defer(invite)
	l.Defer(chat(""))
	l.Defer(invite)
	if len(l.Deferred) != 2 || l.Deferred[0].Kind != DeferChat || l.Deferred[1].Kind != DeferInvite {
		t.Fatalf("held %+v", l.Deferred)
	}
	l.DeferText("1@s.whatsapp.net", "Invoice 123 is attached")
	if d := l.Deferred[0]; !d.HasText || d.Text != "Invoice 123 is attached" {
		t.Errorf("text %+v", d)
	}
	l.DeferText("2@s.whatsapp.net", "for someone else")
	if l.Deferred[0].Text != "Invoice 123 is attached" {
		t.Error("another chat's text replaced it")
	}
	held := l.TakeDeferred()
	if len(held) != 2 || len(l.Deferred) != 0 {
		t.Errorf("took %d, left %d", len(held), len(l.Deferred))
	}
}
