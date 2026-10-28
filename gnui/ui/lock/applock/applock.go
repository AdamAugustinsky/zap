// Package applock is the app lock's logic, ported line by line from
// zf:src/app_lock.rs: the wrong-password back-off, the lock's state machine,
// the Settings password form and what the lock holds until it is lifted. It
// has no interface; the verifier itself is package lockhash's.
//
// The app lock, like WhatsApp Web's screen lock, hides the whole window after
// a while without input, at start, or on request. It keeps people using this
// computer out of the chats. It encrypts nothing beyond what the archive
// already is, and a forgotten password means unlinking. The password is never
// stored: settings keep a salted PBKDF2 verifier (lockhash), and checking or
// making one runs on its own goroutine because it takes tens of milliseconds.
//
// Security properties kept from upstream:
//
//   - the verifier's format, parameters and checks are lockhash's;
//   - three wrong passwords in a row are free, then each try waits one second
//     doubling to half a minute; the count starts over at the next unlock;
//   - a try during a wait is not made at all;
//   - every password handed to the checking goroutine is zeroed once checked.
package applock

import (
	"bytes"
	"time"
	"unicode/utf8"

	"zapfast/lockhash"
)

// MinPasswordChars is the shortest password Settings accepts, in characters.
const MinPasswordChars = lockhash.MinPasswordChars

// FreeTries is the number of consecutive wrong passwords allowed before each
// try has to wait.
const FreeTries = 3

// MaxWait is the longest wait between tries after repeated wrong passwords.
const MaxWait = 30 * time.Second

// WaitAfter is how long the next try waits after failures wrong passwords in
// a row: nothing for the first few, then one second doubling up to half a
// minute.
func WaitAfter(failures uint32) time.Duration {
	if failures < FreeTries {
		return 0
	}
	extra := min(failures-FreeTries, 5)
	return min(time.Second<<extra, MaxWait)
}

// Clock is where the time comes from; tests replace it to move time forward.
type Clock func() time.Time

// FormMode is what a Settings form does with the password.
type FormMode uint8

const (
	// FormSet turns the app lock on with a new password.
	FormSet FormMode = iota
	// FormChange replaces the password; the current one is asked first.
	FormChange
	// FormTurnOff turns the app lock off; the current password is asked first.
	FormTurnOff
)

// FormError is why a Settings form was refused.
type FormError uint8

const (
	FormOK FormError = iota
	FormTooShort
	FormMismatch
	FormWrongCurrent
)

// Form is the password form on the Settings page. Its fields belong to the
// view; they hold passwords, so SubmitForm takes them and zeroes them.
type Form struct {
	Mode                  FormMode
	Current, New, Confirm []byte
	Error                 FormError
	// Busy: a check or a new verifier is being worked out.
	Busy bool
}

// NewForm is an empty form for mode.
func NewForm(mode FormMode) *Form { return &Form{Mode: mode} }

// Check checks what can be checked without the stored verifier.
func (f *Form) Check() FormError {
	if f.Mode == FormTurnOff {
		return FormOK
	}
	if utf8.RuneCount(f.New) < MinPasswordChars {
		return FormTooShort
	}
	if !bytes.Equal(f.New, f.Confirm) {
		return FormMismatch
	}
	return FormOK
}

// Forgetting is where "Forgot password?" on the lock screen has got to.
type Forgetting uint8

const (
	ForgetNo Forgetting = iota
	// ForgetConfirming: asking whether to unlink.
	ForgetConfirming
	// ForgetUnlinking: unlinking was asked for; the lock lifts once WhatsApp
	// confirms it.
	ForgetUnlinking
)

// OutcomeKind is what the checking goroutine worked out.
type OutcomeKind uint8

const (
	// OutcomeUnlock: the lock screen's password matched, or did not.
	OutcomeUnlock OutcomeKind = iota
	// OutcomeWrongCurrent: the form's current password did not match.
	OutcomeWrongCurrent
	// OutcomeSet: a new verifier for the form's new password.
	OutcomeSet
	// OutcomeTurnOff: the current password matched: turn the lock off.
	OutcomeTurnOff
)

// Outcome is the answer of the checking goroutine.
type Outcome struct {
	Kind OutcomeKind
	// Matched is OutcomeUnlock's answer.
	Matched bool
	// Verifier is OutcomeSet's new verifier.
	Verifier string
}

// work is what the checking goroutine is given. It carries passwords, so it
// is never logged.
type work struct {
	unlock    bool
	stored    string
	hasStored bool
	password  []byte
	mode      FormMode
	current   []byte
	new       []byte
}

// run does the slow part and zeroes the passwords it was given.
func run(w work, rounds uint32) Outcome {
	if w.unlock {
		defer clear(w.password)
		return Outcome{Kind: OutcomeUnlock, Matched: lockhash.VerifiesApp(w.stored, w.password)}
	}
	defer clear(w.current)
	defer clear(w.new)
	// Changing or turning off needs the current password first.
	if w.hasStored && w.mode != FormSet && !lockhash.VerifiesApp(w.stored, w.current) {
		return Outcome{Kind: OutcomeWrongCurrent}
	}
	if w.mode == FormTurnOff {
		return Outcome{Kind: OutcomeTurnOff}
	}
	return Outcome{Kind: OutcomeSet, Verifier: lockhash.AppVerifierWithRounds(w.new, rounds)}
}

// DeferredKind is what a request held by the lock asked for.
type DeferredKind uint8

const (
	// DeferMessage: a clicked notification's message, with its account.
	DeferMessage DeferredKind = iota
	// DeferChat: a chat a link named, with the text it carried.
	DeferChat
	// DeferInvite: the code of a group invite a link named.
	DeferInvite
)

// Deferred is one request the app lock holds until it is unlocked.
type Deferred struct {
	Kind DeferredKind
	// Account is DeferMessage's account folder; Chat its chat (and
	// DeferChat's); Message the message of a notification, when it has one (a
	// call's has none).
	Account, Chat, Message string
	HasMessage             bool
	// Text is the text a link carried for DeferChat's chat.
	Text    string
	HasText bool
	// Code is DeferInvite's invite code.
	Code string
}

// Lock is the lock's state while the app runs. All its methods run on the
// interface thread; only the checking goroutine runs elsewhere, and it shares
// nothing with it but a channel.
type Lock struct {
	clock     Clock
	locked    bool
	lastInput time.Time
	failures  uint32
	retryAt   time.Time // zero: no wait
	job       chan Outcome
	rounds    uint32

	// Wrong: the last try was wrong.
	Wrong      bool
	Forgetting Forgetting
	// Deferred is what was asked for from outside while locked, in the order
	// it arrived, replayed once unlocked: a clicked notification's message, a
	// chat a link named, or a group invite. Each names something the lock
	// would otherwise hide, so it waits rather than being dropped, and the
	// last one asked for ends up on screen.
	Deferred []Deferred
	// Form is the Settings password form, when open.
	Form *Form
}

// New starts locked when a password is set, as the app does at start.
func New(locked bool) *Lock {
	l := &Lock{clock: time.Now, locked: locked, rounds: lockhash.AppRounds}
	l.lastInput = l.clock()
	return l
}

// SetClock replaces the clock, and counts inactivity from its present.
func (l *Lock) SetClock(c Clock) {
	l.clock = c
	l.lastInput = c()
}

// SetRounds sets the round count of the verifiers the form makes (tests use
// few; the app keeps lockhash.AppRounds).
func (l *Lock) SetRounds(r uint32) { l.rounds = r }

// Now is the lock's clock.
func (l *Lock) Now() time.Time { return l.clock() }

// IsLocked reports whether the lock is on.
func (l *Lock) IsLocked() bool { return l.locked }

// Defer holds request until the unlock, after everything asked for before
// it. A newer request of the same kind replaces the older one, which would
// only be covered by it.
func (l *Lock) Defer(request Deferred) {
	l.Deferred = slicesDeleteFunc(l.Deferred, func(held Deferred) bool { return held.Kind == request.Kind })
	l.Deferred = append(l.Deferred, request)
}

func slicesDeleteFunc(s []Deferred, del func(Deferred) bool) []Deferred {
	out := s[:0]
	for _, d := range s {
		if !del(d) {
			out = append(out, d)
		}
	}
	return out
}

// DeferText adds a link's text to the chat request it came with, when that
// request is the one waiting for the unlock.
func (l *Lock) DeferText(chat, text string) {
	for i := range l.Deferred {
		if d := &l.Deferred[i]; d.Kind == DeferChat {
			if d.Chat == chat {
				d.Text, d.HasText = text, true
			}
			return
		}
	}
}

// TakeDeferred returns what the lock held, in the order it arrived, and
// forgets it.
func (l *Lock) TakeDeferred() []Deferred {
	held := l.Deferred
	l.Deferred = nil
	return held
}

// NoteInput says someone used the window: the inactivity count starts again.
func (l *Lock) NoteInput() { l.lastInput = l.Now() }

// LastInput is the time of the last input.
func (l *Lock) LastInput() time.Time { return l.lastInput }

// SetLastInput sets the time of the last input (the state keeps it when the
// shell sees the input).
func (l *Lock) SetLastInput(t time.Time) { l.lastInput = t }

// IdleLeft is how long until after of inactivity has passed; zero once it
// has.
func (l *Lock) IdleLeft(after time.Duration) time.Duration {
	return max(after-l.Now().Sub(l.lastInput), 0)
}

// Lock locks, forgetting anything half typed on a previous lock screen.
func (l *Lock) Lock() {
	l.locked = true
	l.Wrong = false
	l.Forgetting = ForgetNo
	l.wipeForm()
	l.Form = nil
}

func (l *Lock) wipeForm() {
	if f := l.Form; f != nil {
		clear(f.Current)
		clear(f.New)
		clear(f.Confirm)
	}
}

// Release lifts the lock without a password: when the password itself is
// gone, after unlinking or when it was turned off.
func (l *Lock) Release() {
	l.locked = false
	l.Wrong = false
	l.Forgetting = ForgetNo
	l.failures = 0
	l.retryAt = time.Time{}
	l.lastInput = l.Now()
}

// WaitLeft is how long until another password may be tried, if it must
// wait.
func (l *Lock) WaitLeft() (time.Duration, bool) {
	if l.retryAt.IsZero() {
		return 0, false
	}
	left := l.retryAt.Sub(l.Now())
	return left, left > 0
}

// Checking reports whether a check is running.
func (l *Lock) Checking() bool { return l.job != nil }

// CanTry reports whether the lock screen may try its password now; entered
// says whether the password field holds something.
func (l *Lock) CanTry(entered bool) bool {
	_, waiting := l.WaitLeft()
	return l.locked && !l.Checking() && !waiting && entered
}

// TryUnlock checks the lock screen's password against stored on a goroutine,
// then calls wake (from that goroutine) when the answer waits. It takes
// ownership of password and zeroes it when done. It does nothing, zeroing the
// password, when CanTry would say no.
func (l *Lock) TryUnlock(stored string, password []byte, wake func()) {
	if !l.CanTry(len(password) > 0) {
		clear(password)
		return
	}
	l.Wrong = false
	l.spawn(work{unlock: true, stored: stored, password: password}, wake)
}

// SubmitForm submits the Settings form: checks it, then checks the current
// password and makes the new verifier on a goroutine. hasStored says whether
// a verifier is set. The form's passwords are handed over and zeroed.
func (l *Lock) SubmitForm(stored string, hasStored bool, wake func()) {
	if l.Checking() {
		return
	}
	f := l.Form
	if f == nil {
		return
	}
	if err := f.Check(); err != FormOK {
		f.Error = err
		return
	}
	f.Error = FormOK
	f.Busy = true
	w := work{mode: f.Mode, stored: stored, hasStored: hasStored, current: f.Current, new: f.New}
	f.Current, f.New = nil, nil
	clear(f.Confirm)
	f.Confirm = nil
	l.spawn(w, wake)
}

func (l *Lock) spawn(w work, wake func()) {
	ch := make(chan Outcome, 1)
	rounds := l.rounds
	l.job = ch
	go func() {
		ch <- run(w, rounds)
		if wake != nil {
			wake()
		}
	}()
}

// Poll returns the finished check, if there is one.
func (l *Lock) Poll() (Outcome, bool) {
	if l.job == nil {
		return Outcome{}, false
	}
	select {
	case o := <-l.job:
		l.job = nil
		return o, true
	default:
		return Outcome{}, false
	}
}

// Wait waits for the running check; for tests.
func (l *Lock) Wait(timeout time.Duration) (Outcome, bool) {
	if l.job == nil {
		return Outcome{}, false
	}
	select {
	case o := <-l.job:
		l.job = nil
		return o, true
	case <-time.After(timeout):
		l.job = nil
		return Outcome{}, false
	}
}

// Unlocked applies the lock screen's answer.
func (l *Lock) Unlocked(matched bool) {
	if matched {
		l.Release()
		return
	}
	if l.failures < ^uint32(0) {
		l.failures++
	}
	l.Wrong = true
	if wait := WaitAfter(l.failures); wait > 0 {
		l.retryAt = l.Now().Add(wait)
	} else {
		l.retryAt = time.Time{}
	}
}
